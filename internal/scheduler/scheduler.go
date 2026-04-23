package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/chronick/skiff/internal/config"
	"github.com/chronick/skiff/internal/logbuf"
	"github.com/chronick/skiff/internal/status"
)

// Scheduler manages internal scheduled jobs.
type Scheduler struct {
	mu        sync.Mutex
	triggers  map[string]chan struct{}
	cancels   map[string]context.CancelFunc
	hashes    map[string]string // config hash per running schedule, for change detection
	state     *status.SharedState
	logs      *logbuf.LogBuffer
	stateFile string
	logger    *slog.Logger
}

// New creates a Scheduler.
func New(state *status.SharedState, logs *logbuf.LogBuffer, stateFile string, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		triggers:  make(map[string]chan struct{}),
		cancels:   make(map[string]context.CancelFunc),
		hashes:    make(map[string]string),
		state:     state,
		logs:      logs,
		stateFile: stateFile,
		logger:    logger,
	}
}

// Start begins running all schedules.
func (s *Scheduler) Start(ctx context.Context, schedules map[string]config.ScheduleConfig) {
	s.Reconcile(ctx, schedules)
}

// Reconcile updates running schedules to match the desired state.
// Schedules absent from `schedules` are stopped; new entries are started;
// existing entries with a changed config are restarted. Returns the names
// affected by each action.
func (s *Scheduler) Reconcile(ctx context.Context, schedules map[string]config.ScheduleConfig) (started, restarted, stopped []string) {
	persisted := s.loadState()

	s.mu.Lock()
	defer s.mu.Unlock()

	// Stop schedules removed from config.
	for name := range s.cancels {
		if _, ok := schedules[name]; ok {
			continue
		}
		s.cancels[name]()
		delete(s.cancels, name)
		delete(s.triggers, name)
		delete(s.hashes, name)
		s.state.RemoveSchedule(name)
		stopped = append(stopped, name)
	}

	// Start new and restart changed schedules.
	for name, cfg := range schedules {
		newHash := config.Hash(cfg)
		if existing, ok := s.hashes[name]; ok && existing == newHash {
			continue
		}

		isRestart := false
		if cancel, ok := s.cancels[name]; ok {
			cancel()
			delete(s.cancels, name)
			delete(s.triggers, name)
			isRestart = true
		}

		triggerCh := make(chan struct{}, 1)
		schedCtx, cancel := context.WithCancel(ctx)
		s.triggers[name] = triggerCh
		s.cancels[name] = cancel
		s.hashes[name] = newHash

		var lastRun *time.Time
		if ps, ok := persisted[name]; ok {
			lastRun = ps.LastRun
		}
		s.state.SetSchedule(&status.ScheduleStatus{
			Name:       name,
			LastRun:    lastRun,
			NextRun:    computeNextRun(cfg, lastRun),
			LastResult: "pending",
		})

		go s.runSchedule(schedCtx, name, cfg, triggerCh, lastRun)

		if isRestart {
			restarted = append(restarted, name)
		} else {
			started = append(started, name)
		}
	}
	return
}

// TriggerNow triggers a schedule to run immediately.
func (s *Scheduler) TriggerNow(name string) error {
	s.mu.Lock()
	ch, ok := s.triggers[name]
	s.mu.Unlock()

	if !ok {
		return fmt.Errorf("schedule %q not found", name)
	}

	select {
	case ch <- struct{}{}:
	default:
		return fmt.Errorf("schedule %q already triggered", name)
	}
	return nil
}

// StopAll stops all running schedules.
func (s *Scheduler) StopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cancel := range s.cancels {
		cancel()
	}
	s.triggers = make(map[string]chan struct{})
	s.cancels = make(map[string]context.CancelFunc)
	s.hashes = make(map[string]string)
}

func (s *Scheduler) runSchedule(ctx context.Context, name string, cfg config.ScheduleConfig, triggerCh chan struct{}, lastRun *time.Time) {
	for {
		nextRun := computeNextRun(cfg, lastRun)

		ss, _ := s.state.GetSchedule(name)
		if ss != nil {
			ss.NextRun = nextRun
			s.state.SetSchedule(ss)
		}

		waitDuration := time.Until(nextRun)
		if waitDuration < 0 {
			// Missed run — execute immediately
			waitDuration = 0
		}

		var timer *time.Timer
		if waitDuration > 0 {
			timer = time.NewTimer(waitDuration)
		} else {
			timer = time.NewTimer(0)
		}

		select {
		case <-timer.C:
			s.execute(ctx, name, cfg)
		case <-triggerCh:
			timer.Stop()
			s.execute(ctx, name, cfg)
		case <-ctx.Done():
			timer.Stop()
			return
		}

		now := time.Now()
		lastRun = &now
	}
}

func (s *Scheduler) execute(ctx context.Context, name string, cfg config.ScheduleConfig) {
	timeout := time.Duration(cfg.TimeoutSecs) * time.Second
	if timeout == 0 {
		timeout = 5 * time.Minute
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	s.logger.Info("executing schedule", "name", name)
	s.logs.Append(name, "INFO schedule started")

	ss := &status.ScheduleStatus{
		Name:       name,
		LastResult: "running",
	}
	now := time.Now()
	ss.LastRun = &now
	s.state.SetSchedule(ss)

	start := time.Now()
	cmd := exec.CommandContext(execCtx, cfg.Command[0], cfg.Command[1:]...)
	cmd.Dir = cfg.WorkingDir
	cmd.Env = buildEnv(cfg.Env)
	output, err := cmd.CombinedOutput()

	duration := time.Since(start)

	result := "success"
	var lastError string
	if err != nil {
		result = "failed"
		lastError = err.Error()
		s.logger.Error("schedule failed", "name", name, "error", err, "duration", duration)
	} else {
		s.logger.Info("schedule completed", "name", name, "duration", duration)
	}

	if len(output) > 0 {
		s.logs.Append(name, string(output))
	}
	s.logs.Append(name, fmt.Sprintf("INFO schedule %s (duration %s)", result, duration.Round(time.Millisecond)))

	runTime := time.Now()
	s.state.SetSchedule(&status.ScheduleStatus{
		Name:       name,
		LastRun:    &runTime,
		NextRun:    computeNextRun(cfg, &runTime),
		LastResult: result,
		LastError:  lastError,
		Duration:   duration.Milliseconds(),
	})

	s.saveState()
}

func computeNextRun(cfg config.ScheduleConfig, lastRun *time.Time) time.Time {
	if cfg.Calendar != nil {
		return nextCalendarRun(cfg.Calendar, time.Now())
	}

	interval := time.Duration(cfg.IntervalSeconds) * time.Second
	if lastRun == nil {
		return time.Now().Add(interval)
	}
	next := lastRun.Add(interval)
	if next.Before(time.Now()) {
		return time.Now() // Missed run
	}
	return next
}

func nextCalendarRun(cal *config.CalendarInterval, from time.Time) time.Time {
	// Start from the next minute
	t := from.Truncate(time.Minute).Add(time.Minute)

	for i := 0; i < 525960; i++ { // Max ~1 year of minutes
		if matchesCalendar(cal, t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	// Fallback: 24 hours from now
	return from.Add(24 * time.Hour)
}

func matchesCalendar(cal *config.CalendarInterval, t time.Time) bool {
	if cal.Month != nil && int(t.Month()) != *cal.Month {
		return false
	}
	if cal.Day != nil && t.Day() != *cal.Day {
		return false
	}
	if cal.Weekday != nil && int(t.Weekday()) != *cal.Weekday {
		return false
	}
	if cal.Hour != nil && t.Hour() != *cal.Hour {
		return false
	}
	if cal.Minute != nil && t.Minute() != *cal.Minute {
		return false
	}
	return true
}

type persistedState struct {
	Schedules map[string]*status.ScheduleStatus `json:"schedules"`
}

func (s *Scheduler) loadState() map[string]*status.ScheduleStatus {
	result := make(map[string]*status.ScheduleStatus)
	data, err := os.ReadFile(s.stateFile)
	if err != nil {
		return result
	}

	var ps persistedState
	if err := json.Unmarshal(data, &ps); err != nil {
		return result
	}
	if ps.Schedules != nil {
		return ps.Schedules
	}
	return result
}

func (s *Scheduler) saveState() {
	s.mu.Lock()
	defer s.mu.Unlock()

	schedules := make(map[string]*status.ScheduleStatus)
	// Build from shared state
	snapshot := s.state.Snapshot()
	if scheds, ok := snapshot["schedules"].([]*status.ScheduleStatus); ok {
		for _, ss := range scheds {
			schedules[ss.Name] = ss
		}
	}

	ps := persistedState{Schedules: schedules}
	data, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		s.logger.Error("failed to marshal scheduler state", "error", err)
		return
	}

	if err := os.WriteFile(s.stateFile, data, 0600); err != nil {
		s.logger.Error("failed to save scheduler state", "error", err)
	}
}

func buildEnv(env map[string]string) []string {
	result := os.Environ()
	for k, v := range env {
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}
	return result
}
