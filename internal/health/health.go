package health

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/chronick/plane/internal/config"
	"github.com/chronick/plane/internal/logbuf"
	"github.com/chronick/plane/internal/runner"
	"github.com/chronick/plane/internal/status"
)

// Checker runs periodic health checks for all configured resources.
type Checker struct {
	mu      sync.Mutex
	probes  map[string]context.CancelFunc
	state   *status.SharedState
	logs    *logbuf.LogBuffer
	runner  runner.ProcessRunner
	logger  *slog.Logger
	client  *http.Client

	// OnUnhealthy is called when a resource becomes unhealthy. The callback
	// receives the resource name.
	OnUnhealthy func(name string)
}

// NewChecker creates a health checker.
func NewChecker(state *status.SharedState, logs *logbuf.LogBuffer, r runner.ProcessRunner, logger *slog.Logger) *Checker {
	return &Checker{
		probes: make(map[string]context.CancelFunc),
		state:  state,
		logs:   logs,
		runner: r,
		logger: logger,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// StartProbe begins periodic health checking for a resource.
func (c *Checker) StartProbe(ctx context.Context, name string, hc *config.HealthCheckConfig) {
	if hc == nil {
		return
	}

	c.mu.Lock()
	if cancel, exists := c.probes[name]; exists {
		cancel()
	}
	probeCtx, cancel := context.WithCancel(ctx)
	c.probes[name] = cancel
	c.mu.Unlock()

	go c.runProbe(probeCtx, name, hc)
}

// StopProbe stops the health check for a resource.
func (c *Checker) StopProbe(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cancel, exists := c.probes[name]; exists {
		cancel()
		delete(c.probes, name)
	}
}

// StopAll stops all health check probes.
func (c *Checker) StopAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, cancel := range c.probes {
		cancel()
		delete(c.probes, name)
	}
}

// CheckOnce runs a single health check and returns nil if healthy.
func (c *Checker) CheckOnce(ctx context.Context, hc *config.HealthCheckConfig) error {
	switch hc.Type {
	case "http":
		return c.checkHTTP(ctx, hc)
	case "tcp":
		return c.checkTCP(ctx, hc)
	case "command":
		return c.checkCommand(ctx, hc)
	default:
		return fmt.Errorf("unknown health check type: %s", hc.Type)
	}
}

func (c *Checker) runProbe(ctx context.Context, name string, hc *config.HealthCheckConfig) {
	interval := time.Duration(hc.IntervalSecs) * time.Second
	if interval == 0 {
		interval = 30 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			err := c.CheckOnce(ctx, hc)
			c.recordResult(name, hc, err)
		case <-ctx.Done():
			return
		}
	}
}

func (c *Checker) recordResult(name string, hc *config.HealthCheckConfig, err error) {
	rs, ok := c.state.GetResource(name)
	if !ok {
		return
	}

	if rs.Health == nil {
		rs.Health = &status.HealthState{Status: "unknown"}
	}

	rs.Health.LastCheck = time.Now()

	if err != nil {
		rs.Health.ConsecutiveFails++
		rs.Health.LastError = err.Error()

		if rs.Health.ConsecutiveFails >= hc.FailureThreshold {
			if rs.Health.Status != "unhealthy" {
				rs.Health.Status = "unhealthy"
				c.logger.Warn("resource unhealthy", "name", name, "error", err, "failures", rs.Health.ConsecutiveFails)
				c.logs.Append(name, fmt.Sprintf("WARN health check failed (%d consecutive): %s", rs.Health.ConsecutiveFails, err))

				if hc.AutoRestart && c.OnUnhealthy != nil {
					c.OnUnhealthy(name)
				}
			}
		}
	} else {
		if rs.Health.Status != "healthy" {
			c.logger.Info("resource healthy", "name", name)
			c.logs.Append(name, "INFO health check passed")
		}
		rs.Health.Status = "healthy"
		rs.Health.ConsecutiveFails = 0
		rs.Health.LastError = ""
	}

	c.state.SetResource(rs)
}

func (c *Checker) checkHTTP(ctx context.Context, hc *config.HealthCheckConfig) error {
	timeout := time.Duration(hc.TimeoutSecs) * time.Second
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, "GET", hc.URL, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http check: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http check: status %d", resp.StatusCode)
	}
	return nil
}

func (c *Checker) checkTCP(ctx context.Context, hc *config.HealthCheckConfig) error {
	timeout := time.Duration(hc.TimeoutSecs) * time.Second
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	addr := fmt.Sprintf("localhost:%d", hc.Port)
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return fmt.Errorf("tcp check: %w", err)
	}
	conn.Close()
	return nil
}

func (c *Checker) checkCommand(ctx context.Context, hc *config.HealthCheckConfig) error {
	timeout := time.Duration(hc.TimeoutSecs) * time.Second
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	_, err := c.runner.Run(cmdCtx, hc.Command[0], hc.Command[1:], runner.RunOpts{})
	if err != nil {
		return fmt.Errorf("command check: %w", err)
	}
	return nil
}
