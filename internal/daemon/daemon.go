package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chronick/skiff/internal/config"
	"github.com/chronick/skiff/internal/dns"
	"github.com/chronick/skiff/internal/health"
	"github.com/chronick/skiff/internal/logbuf"
	"github.com/chronick/skiff/internal/runner"
	"github.com/chronick/skiff/internal/runtime"
	"github.com/chronick/skiff/internal/scheduler"
	"github.com/chronick/skiff/internal/status"
	"github.com/chronick/skiff/internal/supervisor"
)

// Daemon is the main control plane process.
type Daemon struct {
	cfg           *config.Config
	replicaGroups []config.ReplicaGroup // template→expanded name mapping
	state         *status.SharedState
	logs          *logbuf.LogBuffer
	supervisor    *supervisor.Supervisor
	scheduler     *scheduler.Scheduler
	health        *health.Checker
	runtime       runtime.ContainerRuntime
	dns           *dns.ServiceDNS
	runner        runner.ProcessRunner
	adhoc         *AdhocTracker
	logger        *slog.Logger
	server        *http.Server
	tcpServer     *http.Server

	logOffsetsMu sync.Mutex
	logOffsets   map[string]int // tracks last-seen log line count per container

	prevStatsMu sync.Mutex
	prevStats   map[string]prevStatsSample // previous stats sample for CPU% calculation
}

type prevStatsSample struct {
	cpuUsageUsec int64
	time         time.Time
}

// New creates a Daemon from config. replicaGroups maps template names to
// their expanded container names (from LoadRaw + ReplicaGroups).
func New(cfg *config.Config, replicaGroups []config.ReplicaGroup, logger *slog.Logger) *Daemon {
	logs := logbuf.New(cfg.Daemon.LogBufferLines)
	state := status.NewSharedState()
	r := &runner.ExecRunner{}

	sup := supervisor.New(state, logs, cfg.Paths.Logs, logger)
	sched := scheduler.New(state, logs, cfg.Paths.StateFile, logger)
	hc := health.NewChecker(state, logs, r, logger)

	var rt runtime.ContainerRuntime
	switch cfg.Daemon.Runtime {
	case "apple":
		rt = runtime.NewAppleRuntime(r, logger)
	default: // "docker" or empty
		rt = runtime.NewDockerRuntime(r, logger)
	}

	var dnsServer *dns.ServiceDNS
	if cfg.DNS.Enabled {
		gateway := dns.DetectGateway()
		dnsServer = dns.New(gateway, cfg.DNS.Domain, uint32(cfg.DNS.TTL), logger)
		dnsServer.SetUpstream(dns.DetectUpstream())
	}

	adhocTracker := NewAdhocTracker(state, rt, logger)

	d := &Daemon{
		cfg:           cfg,
		replicaGroups: replicaGroups,
		state:         state,
		logs:       logs,
		supervisor: sup,
		scheduler:  sched,
		health:     hc,
		runtime:    rt,
		dns:        dnsServer,
		runner:     r,
		adhoc:      adhocTracker,
		logger:     logger,
		logOffsets: make(map[string]int),
		prevStats:  make(map[string]prevStatsSample),
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
				rtCfg := containerToRuntimeConfig(name, cCfg)
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
		return fmt.Errorf("another skiff daemon is already running (could not acquire lock)")
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

	// Start stats poller
	go d.statsPoller(sigCtx)

	// Start log poller
	go d.logPoller(sigCtx)

	// Start ad-hoc container exit poller
	go d.adhocExitPoller(sigCtx)

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

	d.logger.Info("skiff daemon started")

	// Wait for shutdown signal
	<-sigCtx.Done()
	d.logger.Info("shutting down...")

	return d.shutdown()
}

func (d *Daemon) startAll(ctx context.Context) error {
	// Create networks before starting containers
	for name, netCfg := range d.cfg.Networks {
		if err := d.runtime.CreateNetwork(ctx, name, runtime.NetworkConfig{
			Subnet:   netCfg.Subnet,
			Internal: netCfg.Internal,
		}); err != nil {
			d.logger.Error("failed to create network", "name", name, "error", err)
		}
	}

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
			rtCfg := containerToRuntimeConfig(name, cCfg)
			if d.dns != nil && cCfg.Network != "host" {
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

	// Stop ad-hoc containers
	d.adhoc.StopAll()

	// Stop containers
	for name := range d.cfg.Containers {
		d.logger.Info("stopping container", "name", name)
		_ = d.runtime.Stop(shutdownCtx, name)
	}

	// Delete networks
	for name := range d.cfg.Networks {
		_ = d.runtime.DeleteNetwork(shutdownCtx, name)
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

	d.logger.Info("skiff daemon stopped")
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

func containerToRuntimeConfig(name string, c config.ContainerConfig) runtime.ContainerConfig {
	return runtime.ContainerConfig{
		Image:      c.Image,
		Dockerfile: c.Dockerfile,
		Context:    c.Context,
		Volumes:    c.Volumes,
		Env:        c.Env,
		Ports:      c.Ports,
		CPUs:       c.CPUs,
		Memory:     c.Memory,
		Labels:     c.Labels,
		Init:       c.Init,
		ReadOnly:   c.ReadOnly,
		Network:    c.Network,
	}
}

// statsPoller periodically polls container stats and updates SharedState.
func (d *Daemon) statsPoller(ctx context.Context) {
	interval := time.Duration(d.cfg.Daemon.StatusPollIntervalSecs) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			for name := range d.cfg.Containers {
				rs, ok := d.state.GetResource(name)
				if !ok || rs.State != status.StateRunning {
					continue
				}
				stats, err := d.runtime.Stats(ctx, name)
				if err != nil {
					d.logger.Debug("stats poll failed", "name", name, "error", err)
					continue
				}

				// Compute CPU% from delta between samples
				d.prevStatsMu.Lock()
				prev, hasPrev := d.prevStats[name]
				d.prevStats[name] = prevStatsSample{
					cpuUsageUsec: stats.CPUUsageUsec,
					time:         now,
				}
				d.prevStatsMu.Unlock()

				if hasPrev {
					cpuDelta := stats.CPUUsageUsec - prev.cpuUsageUsec
					timeDelta := now.Sub(prev.time)
					if timeDelta > 0 && cpuDelta >= 0 {
						// cpuDelta is in microseconds, timeDelta in microseconds
						stats.CPUPercent = float64(cpuDelta) / float64(timeDelta.Microseconds()) * 100.0
					}
				}

				d.state.UpdateStats(name, stats)
			}
		}
	}
}

// adhocExitPoller periodically checks for exited ad-hoc containers and
// handles auto-remove and parent-child cleanup.
func (d *Daemon) adhocExitPoller(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.adhoc.PollExited()
		}
	}
}

// logPoller periodically pulls container logs and appends to the ring buffer.
func (d *Daemon) logPoller(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for name := range d.cfg.Containers {
				rs, ok := d.state.GetResource(name)
				if !ok || rs.State != status.StateRunning {
					continue
				}

				d.logOffsetsMu.Lock()
				offset := d.logOffsets[name]
				d.logOffsetsMu.Unlock()

				// Fetch more lines than the offset to get new ones
				fetchLines := offset + 200
				out, err := d.runtime.Logs(ctx, name, fetchLines)
				if err != nil {
					d.logger.Debug("log poll failed", "name", name, "error", err)
					continue
				}

				allLines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
				if len(allLines) == 1 && allLines[0] == "" {
					continue
				}

				// Only append lines beyond the offset
				newLines := allLines
				if offset < len(allLines) {
					newLines = allLines[offset:]
				} else {
					newLines = nil
				}

				for _, line := range newLines {
					if line != "" {
						d.logs.Append(name, line)
					}
				}

				d.logOffsetsMu.Lock()
				d.logOffsets[name] = len(allLines)
				d.logOffsetsMu.Unlock()
			}
		}
	}
}
