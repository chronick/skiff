package supervisor

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/chronick/skiff/internal/config"
	"github.com/chronick/skiff/internal/logbuf"
	"github.com/chronick/skiff/internal/status"
)

// Supervisor manages native services as child processes.
type Supervisor struct {
	mu        sync.Mutex
	processes map[string]*managedProcess
	state     *status.SharedState
	logs      *logbuf.LogBuffer
	logsDir   string
	logger    *slog.Logger
}

type managedProcess struct {
	name      string
	cfg       config.ServiceConfig
	cmd       *exec.Cmd
	cancel    context.CancelFunc
	pid       int
	restarts  int
	startedAt time.Time
	stopping  bool
}

// New creates a Supervisor.
func New(state *status.SharedState, logs *logbuf.LogBuffer, logsDir string, logger *slog.Logger) *Supervisor {
	return &Supervisor{
		processes: make(map[string]*managedProcess),
		state:     state,
		logs:      logs,
		logsDir:   logsDir,
		logger:    logger,
	}
}

// Start launches a service as a child process with restart policy.
func (s *Supervisor) Start(ctx context.Context, name string, cfg config.ServiceConfig) error {
	s.mu.Lock()
	if p, exists := s.processes[name]; exists && p.cmd != nil && p.cmd.Process != nil && !p.stopping {
		s.mu.Unlock()
		return fmt.Errorf("service %q is already running (pid %d)", name, p.pid)
	}
	s.mu.Unlock()

	go s.supervise(ctx, name, cfg)
	return nil
}

func (s *Supervisor) supervise(parentCtx context.Context, name string, cfg config.ServiceConfig) {
	var restarts int
	backoff := time.Duration(cfg.RestartBackoffSecs) * time.Second
	if backoff == 0 {
		backoff = 5 * time.Second
	}
	currentBackoff := backoff

	for {
		ctx, cancel := context.WithCancel(parentCtx)

		s.state.SetResource(&status.ResourceStatus{
			Name:       name,
			Type:       status.TypeService,
			State:      status.StateStarting,
			ConfigHash: config.Hash(cfg),
			DependsOn:  cfg.DependsOn,
		})

		cmd := exec.CommandContext(ctx, cfg.Command[0], cfg.Command[1:]...)
		cmd.Dir = cfg.WorkingDir
		cmd.Env = buildEnv(cfg.Env)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		// Set up log file
		var logFile *os.File
		if cfg.LogFile != "" {
			logPath := cfg.LogFile
			if !filepath.IsAbs(logPath) {
				logPath = filepath.Join(s.logsDir, logPath)
			}
			var err error
			logFile, err = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err != nil {
				s.logger.Error("failed to open log file", "service", name, "error", err)
			} else {
				cmd.Stdout = logFile
				cmd.Stderr = logFile
			}
		}

		err := cmd.Start()
		if err != nil {
			s.logger.Error("failed to start service", "service", name, "error", err)
			s.state.SetResource(&status.ResourceStatus{
				Name:       name,
				Type:       status.TypeService,
				State:      status.StateFailed,
				LastError:  err.Error(),
				ConfigHash: config.Hash(cfg),
			})
			cancel()
			if logFile != nil {
				logFile.Close()
			}
			return
		}

		now := time.Now()
		pid := cmd.Process.Pid
		s.logger.Info("service started", "service", name, "pid", pid)
		s.logs.Append(name, fmt.Sprintf("service started (pid %d)", pid))

		s.mu.Lock()
		s.processes[name] = &managedProcess{
			name:      name,
			cfg:       cfg,
			cmd:       cmd,
			cancel:    cancel,
			pid:       pid,
			restarts:  restarts,
			startedAt: now,
		}
		s.mu.Unlock()

		s.state.SetResource(&status.ResourceStatus{
			Name:       name,
			Type:       status.TypeService,
			State:      status.StateRunning,
			PID:        pid,
			StartedAt:  now,
			ConfigHash: config.Hash(cfg),
			DependsOn:  cfg.DependsOn,
		})

		// Wait for process to exit
		waitErr := cmd.Wait()
		if logFile != nil {
			logFile.Close()
		}

		s.mu.Lock()
		proc := s.processes[name]
		isStopping := proc != nil && proc.stopping
		s.mu.Unlock()

		// Check if we were asked to stop
		if isStopping || parentCtx.Err() != nil {
			s.state.SetResource(&status.ResourceStatus{
				Name:       name,
				Type:       status.TypeService,
				State:      status.StateStopped,
				ConfigHash: config.Hash(cfg),
			})
			cancel()
			return
		}

		exitCode := 0
		if waitErr != nil {
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
		}

		s.logger.Warn("service exited", "service", name, "exit_code", exitCode, "restarts", restarts)
		s.logs.Append(name, fmt.Sprintf("service exited (code %d, restart %d)", exitCode, restarts))

		// Check restart policy
		shouldRestart := false
		switch cfg.RestartPolicy {
		case "always":
			shouldRestart = true
		case "on-failure":
			shouldRestart = exitCode != 0
		case "never":
			shouldRestart = false
		}

		if cfg.MaxRestarts > 0 && restarts >= cfg.MaxRestarts {
			shouldRestart = false
			s.logger.Error("service exceeded max restarts", "service", name, "max", cfg.MaxRestarts)
		}

		if !shouldRestart {
			s.state.SetResource(&status.ResourceStatus{
				Name:       name,
				Type:       status.TypeService,
				State:      status.StateStopped,
				ExitCode:   exitCode,
				ConfigHash: config.Hash(cfg),
			})
			cancel()
			return
		}

		restarts++

		s.state.SetResource(&status.ResourceStatus{
			Name:       name,
			Type:       status.TypeService,
			State:      status.StateStarting,
			LastError:  fmt.Sprintf("restarting (attempt %d, backoff %s)", restarts, currentBackoff),
			ConfigHash: config.Hash(cfg),
		})

		// Backoff before restart
		select {
		case <-time.After(currentBackoff):
		case <-parentCtx.Done():
			cancel()
			return
		}

		// Exponential backoff, cap at 60s
		currentBackoff *= 2
		if currentBackoff > 60*time.Second {
			currentBackoff = 60 * time.Second
		}

		cancel()
	}
}

// Stop sends SIGTERM to a service process and waits.
func (s *Supervisor) Stop(name string) error {
	s.mu.Lock()
	proc, exists := s.processes[name]
	if !exists || proc.cmd == nil || proc.cmd.Process == nil {
		s.mu.Unlock()
		return nil
	}
	proc.stopping = true
	s.mu.Unlock()

	s.logger.Info("stopping service", "service", name, "pid", proc.pid)

	// Send SIGTERM to process group
	pgid, err := syscall.Getpgid(proc.pid)
	if err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
	} else {
		_ = proc.cmd.Process.Signal(syscall.SIGTERM)
	}

	// Cancel context (which also kills the process after timeout)
	proc.cancel()

	s.mu.Lock()
	delete(s.processes, name)
	s.mu.Unlock()

	s.logs.Append(name, "service stopped")
	return nil
}

// Kill sends SIGKILL to a service process.
func (s *Supervisor) Kill(name string) error {
	s.mu.Lock()
	proc, exists := s.processes[name]
	if !exists || proc.cmd == nil || proc.cmd.Process == nil {
		s.mu.Unlock()
		return nil
	}
	proc.stopping = true
	s.mu.Unlock()

	pgid, err := syscall.Getpgid(proc.pid)
	if err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	} else {
		_ = proc.cmd.Process.Kill()
	}

	proc.cancel()

	s.mu.Lock()
	delete(s.processes, name)
	s.mu.Unlock()

	return nil
}

// StopAll gracefully stops all managed processes.
func (s *Supervisor) StopAll() {
	s.mu.Lock()
	names := make([]string, 0, len(s.processes))
	for name := range s.processes {
		names = append(names, name)
	}
	s.mu.Unlock()

	for _, name := range names {
		_ = s.Stop(name)
	}
}

// IsRunning checks if a service is currently managed and running.
func (s *Supervisor) IsRunning(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	proc, exists := s.processes[name]
	return exists && proc.cmd != nil && proc.cmd.Process != nil && !proc.stopping
}

// PID returns the PID of a managed process, or 0 if not running.
func (s *Supervisor) PID(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if proc, exists := s.processes[name]; exists {
		return proc.pid
	}
	return 0
}

func buildEnv(env map[string]string) []string {
	result := os.Environ()
	for k, v := range env {
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}
	return result
}
