package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chronick/plane/internal/config"
	"github.com/chronick/plane/internal/dns"
	"github.com/chronick/plane/internal/health"
	"github.com/chronick/plane/internal/logbuf"
	"github.com/chronick/plane/internal/runner"
	"github.com/chronick/plane/internal/runtime"
	"github.com/chronick/plane/internal/scheduler"
	"github.com/chronick/plane/internal/status"
	"github.com/chronick/plane/internal/supervisor"
)

// Daemon is the main control plane process.
type Daemon struct {
	cfg        *config.Config
	state      *status.SharedState
	logs       *logbuf.LogBuffer
	supervisor *supervisor.Supervisor
	scheduler  *scheduler.Scheduler
	health     *health.Checker
	runtime    runtime.ContainerRuntime
	dns        *dns.ServiceDNS
	runner     runner.ProcessRunner
	logger     *slog.Logger
	server     *http.Server
	tcpServer  *http.Server
}

// New creates a Daemon from config.
func New(cfg *config.Config, logger *slog.Logger) *Daemon {
	logs := logbuf.New(cfg.Daemon.LogBufferLines)
	state := status.NewSharedState()
	r := &runner.ExecRunner{}

	sup := supervisor.New(state, logs, cfg.Paths.Logs, logger)
	sched := scheduler.New(state, logs, cfg.Paths.StateFile, logger)
	hc := health.NewChecker(state, logs, r, logger)
	rt := runtime.NewAppleRuntime(r, logger)

	var dnsServer *dns.ServiceDNS
	if cfg.DNS.Enabled {
		gateway := dns.DetectGateway()
		dnsServer = dns.New(gateway, cfg.DNS.Domain, uint32(cfg.DNS.TTL), logger)
		dnsServer.SetUpstream(dns.DetectUpstream())
	}

	d := &Daemon{
		cfg:        cfg,
		state:      state,
		logs:       logs,
		supervisor: sup,
		scheduler:  sched,
		health:     hc,
		runtime:    rt,
		dns:        dnsServer,
		runner:     r,
		logger:     logger,
	}

	// Wire up health check auto-restart callback
	hc.OnUnhealthy = func(name string) {
		rs, ok := state.GetResource(name)
		if !ok {
			return
		}
		logger.Warn("auto-restarting unhealthy resource", "name", name)
		switch rs.Type {
		case status.TypeService:
			_ = sup.Stop(name)
			if svcCfg, ok := cfg.Services[name]; ok {
				_ = sup.Start(context.Background(), name, svcCfg)
			}
		case status.TypeContainer:
			_ = rt.Stop(context.Background(), name)
			if cCfg, ok := cfg.Containers[name]; ok {
				rtCfg := containerToRuntimeConfig(cCfg)
				_ = rt.Run(context.Background(), name, rtCfg)
			}
		}
	}

	return d
}

// Run starts the daemon and blocks until shutdown.
func (d *Daemon) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Handle signals
	sigCtx, sigCancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer sigCancel()

	// Ensure directories exist
	if err := os.MkdirAll(d.cfg.Paths.Base, 0755); err != nil {
		return fmt.Errorf("creating base dir: %w", err)
	}
	if err := os.MkdirAll(d.cfg.Paths.Logs, 0755); err != nil {
		return fmt.Errorf("creating logs dir: %w", err)
	}

	// State file locking
	lockFile := d.cfg.Paths.StateFile + ".lock"
	lock, err := os.OpenFile(lockFile, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("creating lock file: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another plane daemon is already running (could not acquire lock)")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	// Clean slate: stop any existing containers
	d.logger.Info("performing clean slate recovery")
	if containers, err := d.runtime.List(ctx); err == nil {
		for _, c := range containers {
			d.logger.Info("stopping orphan container", "name", c.Name)
			_ = d.runtime.Stop(ctx, c.Name)
		}
	}

	// Start DNS
	if d.dns != nil {
		if err := d.dns.Start(d.cfg.DNS.Port); err != nil {
			d.logger.Error("failed to start dns server", "error", err)
		}
	}

	// Start services and containers in dependency order
	if err := d.startAll(sigCtx); err != nil {
		d.logger.Error("failed to start all resources", "error", err)
	}

	// Start scheduler
	if len(d.cfg.Schedules) > 0 {
		d.scheduler.Start(sigCtx, d.cfg.Schedules)
	}

	// Start HTTP server on unix socket
	mux := d.setupRoutes()

	// Remove stale socket
	socketPath := d.cfg.Paths.Socket
	_ = os.Remove(socketPath)

	unixListener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listening on unix socket: %w", err)
	}
	defer unixListener.Close()

	if err := os.Chmod(socketPath, 0600); err != nil {
		return fmt.Errorf("setting socket permissions: %w", err)
	}

	d.server = &http.Server{Handler: mux}
	go func() {
		d.logger.Info("control API listening", "socket", socketPath)
		if err := d.server.Serve(unixListener); err != nil && err != http.ErrServerClosed {
			d.logger.Error("http server error", "error", err)
		}
	}()

	// Optional TCP listener
	if d.cfg.Daemon.Listen != "" {
		tcpMux := d.setupRoutes()
		if d.cfg.Proxy != nil {
			d.setupProxy(tcpMux)
		}

		d.tcpServer = &http.Server{
			Addr:    d.cfg.Daemon.Listen,
			Handler: d.authMiddleware(tcpMux),
		}
		go func() {
			d.logger.Info("TCP API listening", "addr", d.cfg.Daemon.Listen)
			if err := d.tcpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				d.logger.Error("tcp server error", "error", err)
			}
		}()
	}

	d.logger.Info("plane daemon started")

	// Wait for shutdown signal
	<-sigCtx.Done()
	d.logger.Info("shutting down...")

	return d.shutdown()
}

func (d *Daemon) startAll(ctx context.Context) error {
	order, err := config.DependencyOrder(d.cfg)
	if err != nil {
		return fmt.Errorf("computing dependency order: %w", err)
	}

	// Update DNS records
	if d.dns != nil {
		d.dns.UpdateRecords(order)
	}

	for _, name := range order {
		if svcCfg, ok := d.cfg.Services[name]; ok {
			d.logger.Info("starting service", "name", name)
			if err := d.supervisor.Start(ctx, name, svcCfg); err != nil {
				d.logger.Error("failed to start service", "name", name, "error", err)
				continue
			}
			if svcCfg.HealthCheck != nil {
				d.health.StartProbe(ctx, name, svcCfg.HealthCheck)
			}
		}
		if cCfg, ok := d.cfg.Containers[name]; ok {
			d.logger.Info("starting container", "name", name)
			rtCfg := containerToRuntimeConfig(cCfg)
			if d.dns != nil {
				rtCfg = d.runtime.InjectDNS(rtCfg, dns.DetectGateway().String(), d.cfg.DNS.Port)
			}
			if cCfg.CPUs > 0 || cCfg.Memory != "" {
				rtCfg = d.runtime.SetLimits(rtCfg, runtime.ResourceLimits{
					CPUs:   cCfg.CPUs,
					Memory: cCfg.Memory,
				})
			}
			if err := d.runtime.Run(ctx, name, rtCfg); err != nil {
				d.logger.Error("failed to start container", "name", name, "error", err)
				d.state.SetResource(&status.ResourceStatus{
					Name:       name,
					Type:       status.TypeContainer,
					State:      status.StateFailed,
					LastError:  err.Error(),
					ConfigHash: config.Hash(cCfg),
				})
				continue
			}
			d.state.SetResource(&status.ResourceStatus{
				Name:       name,
				Type:       status.TypeContainer,
				State:      status.StateRunning,
				StartedAt:  time.Now(),
				ConfigHash: config.Hash(cCfg),
				Ports:      cCfg.Ports,
				DependsOn:  cCfg.DependsOn,
			})
			if cCfg.HealthCheck != nil {
				d.health.StartProbe(ctx, name, cCfg.HealthCheck)
			}
		}
	}
	return nil
}

func (d *Daemon) shutdown() error {
	timeout := time.Duration(d.cfg.Daemon.ShutdownTimeoutSecs) * time.Second

	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Stop health checks
	d.health.StopAll()

	// Stop scheduler
	d.scheduler.StopAll()

	// Stop supervisor (native services)
	d.supervisor.StopAll()

	// Stop containers
	for name := range d.cfg.Containers {
		d.logger.Info("stopping container", "name", name)
		_ = d.runtime.Stop(shutdownCtx, name)
	}

	// Stop DNS
	if d.dns != nil {
		d.dns.Stop()
	}

	// Shutdown HTTP servers
	if d.server != nil {
		_ = d.server.Shutdown(shutdownCtx)
	}
	if d.tcpServer != nil {
		_ = d.tcpServer.Shutdown(shutdownCtx)
	}

	// Clean up socket
	_ = os.Remove(d.cfg.Paths.Socket)

	// Save state
	_ = d.state.Save(d.cfg.Paths.StateFile)

	d.logger.Info("plane daemon stopped")
	return nil
}

func (d *Daemon) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		expected := "Bearer " + d.cfg.Daemon.AuthToken
		if token != expected {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func containerToRuntimeConfig(c config.ContainerConfig) runtime.ContainerConfig {
	return runtime.ContainerConfig{
		Image:      c.Image,
		Dockerfile: c.Dockerfile,
		Context:    c.Context,
		Volumes:    c.Volumes,
		Env:        c.Env,
		Ports:      c.Ports,
		CPUs:       c.CPUs,
		Memory:     c.Memory,
	}
}
