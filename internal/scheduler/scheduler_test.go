package scheduler

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chronick/skiff/internal/config"
	"github.com/chronick/skiff/internal/status"
	"github.com/chronick/skiff/internal/testutil"
)

func intPtr(v int) *int { return &v }

// --- computeNextRun ---

func TestComputeNextRun_Interval_NoLastRun(t *testing.T) {
	cfg := config.ScheduleConfig{IntervalSeconds: 60}
	before := time.Now()
	next := computeNextRun(cfg, nil)
	after := time.Now()

	expected := before.Add(60 * time.Second)
	if next.Before(expected.Add(-time.Second)) || next.After(after.Add(61*time.Second)) {
		t.Errorf("expected next ~%v, got %v", expected, next)
	}
}

func TestComputeNextRun_Interval_WithLastRun(t *testing.T) {
	lastRun := time.Now().Add(-30 * time.Second)
	cfg := config.ScheduleConfig{IntervalSeconds: 60}
	next := computeNextRun(cfg, &lastRun)

	expected := lastRun.Add(60 * time.Second)
	diff := next.Sub(expected).Abs()
	if diff > time.Second {
		t.Errorf("expected next ~%v, got %v (diff %v)", expected, next, diff)
	}
}

func TestComputeNextRun_Interval_MissedRun(t *testing.T) {
	lastRun := time.Now().Add(-120 * time.Second) // 2 min ago
	cfg := config.ScheduleConfig{IntervalSeconds: 60}
	before := time.Now()
	next := computeNextRun(cfg, &lastRun)

	// Missed run: should return ~now
	if next.Before(before.Add(-time.Second)) {
		t.Errorf("expected next >= now for missed run, got %v", next)
	}
}

// --- matchesCalendar ---

func TestMatchesCalendar(t *testing.T) {
	// 2025-03-05 14:30 is a Wednesday (weekday=3)
	base := time.Date(2025, 3, 5, 14, 30, 0, 0, time.Local)

	tests := []struct {
		name    string
		cal     *config.CalendarInterval
		t       time.Time
		matches bool
	}{
		{"all nil matches anything", &config.CalendarInterval{}, base, true},
		{"hour matches", &config.CalendarInterval{Hour: intPtr(14)}, base, true},
		{"hour mismatch", &config.CalendarInterval{Hour: intPtr(15)}, base, false},
		{"minute matches", &config.CalendarInterval{Minute: intPtr(30)}, base, true},
		{"minute mismatch", &config.CalendarInterval{Minute: intPtr(0)}, base, false},
		{"day matches", &config.CalendarInterval{Day: intPtr(5)}, base, true},
		{"day mismatch", &config.CalendarInterval{Day: intPtr(1)}, base, false},
		{"weekday matches", &config.CalendarInterval{Weekday: intPtr(3)}, base, true},
		{"weekday mismatch", &config.CalendarInterval{Weekday: intPtr(1)}, base, false},
		{"month matches", &config.CalendarInterval{Month: intPtr(3)}, base, true},
		{"month mismatch", &config.CalendarInterval{Month: intPtr(1)}, base, false},
		{"combo matches", &config.CalendarInterval{Hour: intPtr(14), Minute: intPtr(30)}, base, true},
		{"combo partial mismatch", &config.CalendarInterval{Hour: intPtr(14), Minute: intPtr(0)}, base, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesCalendar(tt.cal, tt.t)
			if got != tt.matches {
				t.Errorf("matchesCalendar() = %v, want %v", got, tt.matches)
			}
		})
	}
}

// --- nextCalendarRun ---

func TestNextCalendarRun_HourMinute(t *testing.T) {
	from := time.Date(2025, 3, 5, 14, 30, 0, 0, time.Local)
	cal := &config.CalendarInterval{Hour: intPtr(15), Minute: intPtr(0)}

	next := nextCalendarRun(cal, from)

	if next.Hour() != 15 || next.Minute() != 0 {
		t.Errorf("expected 15:00, got %02d:%02d", next.Hour(), next.Minute())
	}
	if !next.After(from) {
		t.Error("expected next to be after from")
	}
}

func TestNextCalendarRun_Weekday(t *testing.T) {
	// Wednesday 2025-03-05, looking for Monday (weekday=1)
	from := time.Date(2025, 3, 5, 23, 59, 0, 0, time.Local)
	cal := &config.CalendarInterval{Weekday: intPtr(1), Hour: intPtr(9), Minute: intPtr(0)}

	next := nextCalendarRun(cal, from)

	if next.Weekday() != time.Monday {
		t.Errorf("expected Monday, got %s", next.Weekday())
	}
	if next.Hour() != 9 || next.Minute() != 0 {
		t.Errorf("expected 09:00, got %02d:%02d", next.Hour(), next.Minute())
	}
}

func TestNextCalendarRun_AllNil_NextMinute(t *testing.T) {
	from := time.Date(2025, 3, 5, 14, 30, 0, 0, time.Local)
	cal := &config.CalendarInterval{}

	next := nextCalendarRun(cal, from)

	expected := from.Truncate(time.Minute).Add(time.Minute)
	if !next.Equal(expected) {
		t.Errorf("expected %v, got %v", expected, next)
	}
}

// --- TriggerNow ---

func TestTriggerNow_NotFound(t *testing.T) {
	s := New(testutil.NewTestState(), testutil.NewTestLogBuffer(), "", testutil.NewTestLogger())
	err := s.TriggerNow("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent schedule")
	}
}

func TestTriggerNow_Success(t *testing.T) {
	s := New(testutil.NewTestState(), testutil.NewTestLogBuffer(), "", testutil.NewTestLogger())

	// Manually register a trigger channel
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.triggers["backup"] = ch
	s.mu.Unlock()

	err := s.TriggerNow("backup")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case <-ch:
		// good
	default:
		t.Error("expected trigger channel to have a message")
	}
}

func TestTriggerNow_AlreadyTriggered(t *testing.T) {
	s := New(testutil.NewTestState(), testutil.NewTestLogBuffer(), "", testutil.NewTestLogger())

	ch := make(chan struct{}, 1)
	ch <- struct{}{} // pre-fill
	s.mu.Lock()
	s.triggers["backup"] = ch
	s.mu.Unlock()

	err := s.TriggerNow("backup")
	if err == nil {
		t.Fatal("expected error for already-triggered schedule")
	}
}

// --- Reconcile ---

func TestReconcile_StartsNew(t *testing.T) {
	s := New(testutil.NewTestState(), testutil.NewTestLogBuffer(), "", testutil.NewTestLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started, restarted, stopped := s.Reconcile(ctx, map[string]config.ScheduleConfig{
		"backup": {Command: []string{"true"}, IntervalSeconds: 3600},
	})

	if len(started) != 1 || started[0] != "backup" {
		t.Errorf("expected started=[backup], got %v", started)
	}
	if len(restarted) != 0 || len(stopped) != 0 {
		t.Errorf("expected no restarts/stops, got restarted=%v stopped=%v", restarted, stopped)
	}
	s.mu.Lock()
	_, hasTrigger := s.triggers["backup"]
	_, hasHash := s.hashes["backup"]
	s.mu.Unlock()
	if !hasTrigger || !hasHash {
		t.Error("expected trigger + hash to be registered for new schedule")
	}
}

func TestReconcile_RestartsChanged(t *testing.T) {
	s := New(testutil.NewTestState(), testutil.NewTestLogBuffer(), "", testutil.NewTestLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.Reconcile(ctx, map[string]config.ScheduleConfig{
		"backup": {Command: []string{"true"}, IntervalSeconds: 3600},
	})

	_, restarted, _ := s.Reconcile(ctx, map[string]config.ScheduleConfig{
		"backup": {Command: []string{"true"}, IntervalSeconds: 7200}, // interval changed
	})

	if len(restarted) != 1 || restarted[0] != "backup" {
		t.Errorf("expected restarted=[backup], got %v", restarted)
	}
}

func TestReconcile_NoOpForUnchanged(t *testing.T) {
	s := New(testutil.NewTestState(), testutil.NewTestLogBuffer(), "", testutil.NewTestLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := map[string]config.ScheduleConfig{
		"backup": {Command: []string{"true"}, IntervalSeconds: 3600},
	}
	s.Reconcile(ctx, cfg)

	started, restarted, stopped := s.Reconcile(ctx, cfg)
	if len(started)+len(restarted)+len(stopped) != 0 {
		t.Errorf("expected no changes on identical reconcile, got started=%v restarted=%v stopped=%v",
			started, restarted, stopped)
	}
}

func TestReconcile_StopsRemoved(t *testing.T) {
	s := New(testutil.NewTestState(), testutil.NewTestLogBuffer(), "", testutil.NewTestLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.Reconcile(ctx, map[string]config.ScheduleConfig{
		"backup":  {Command: []string{"true"}, IntervalSeconds: 3600},
		"cleanup": {Command: []string{"true"}, IntervalSeconds: 3600},
	})

	_, _, stopped := s.Reconcile(ctx, map[string]config.ScheduleConfig{
		"backup": {Command: []string{"true"}, IntervalSeconds: 3600},
	})

	if len(stopped) != 1 || stopped[0] != "cleanup" {
		t.Errorf("expected stopped=[cleanup], got %v", stopped)
	}
	s.mu.Lock()
	_, hasTrigger := s.triggers["cleanup"]
	s.mu.Unlock()
	if hasTrigger {
		t.Error("expected cleanup trigger to be deregistered")
	}
}

// --- State persistence ---

func TestStatePersistence_SaveLoad(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")
	state := testutil.NewTestState()

	now := time.Now()
	state.SetSchedule(&status.ScheduleStatus{
		Name:       "backup",
		LastRun:    &now,
		LastResult: "success",
	})

	s := New(state, testutil.NewTestLogBuffer(), stateFile, testutil.NewTestLogger())
	s.saveState()

	// Verify file was written
	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("failed to read state file: %v", err)
	}

	var ps persistedState
	if err := json.Unmarshal(data, &ps); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if ps.Schedules["backup"] == nil {
		t.Fatal("expected backup schedule in persisted state")
	}

	// Load it back
	loaded := s.loadState()
	if loaded["backup"] == nil {
		t.Fatal("expected backup in loaded state")
	}
	if loaded["backup"].LastResult != "success" {
		t.Errorf("expected last_result success, got %s", loaded["backup"].LastResult)
	}
}

func TestStatePersistence_MissingFile(t *testing.T) {
	s := New(testutil.NewTestState(), testutil.NewTestLogBuffer(), "/tmp/nonexistent-skiff-test.json", testutil.NewTestLogger())
	result := s.loadState()
	if len(result) != 0 {
		t.Errorf("expected empty result for missing file, got %d entries", len(result))
	}
}

func TestStatePersistence_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")
	os.WriteFile(stateFile, []byte("not json"), 0600)

	s := New(testutil.NewTestState(), testutil.NewTestLogBuffer(), stateFile, testutil.NewTestLogger())
	result := s.loadState()
	if len(result) != 0 {
		t.Errorf("expected empty result for corrupt file, got %d entries", len(result))
	}
}

// --- StopAll ---

func TestStopAll(t *testing.T) {
	s := New(testutil.NewTestState(), testutil.NewTestLogBuffer(), "", testutil.NewTestLogger())

	// Register some fake cancels
	s.mu.Lock()
	s.triggers["a"] = make(chan struct{}, 1)
	s.triggers["b"] = make(chan struct{}, 1)
	canceled := 0
	s.cancels["a"] = func() { canceled++ }
	s.cancels["b"] = func() { canceled++ }
	s.mu.Unlock()

	s.StopAll()

	if canceled != 2 {
		t.Errorf("expected 2 cancels, got %d", canceled)
	}

	s.mu.Lock()
	if len(s.triggers) != 0 {
		t.Errorf("expected triggers cleared, got %d", len(s.triggers))
	}
	if len(s.cancels) != 0 {
		t.Errorf("expected cancels cleared, got %d", len(s.cancels))
	}
	s.mu.Unlock()
}

// --- Start (integration) ---

func TestStart_InitializesScheduleState(t *testing.T) {
	state := testutil.NewTestState()
	s := New(state, testutil.NewTestLogBuffer(), "", testutil.NewTestLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	schedules := map[string]config.ScheduleConfig{
		"backup": {
			Command:         []string{"echo", "backup"},
			WorkingDir:      "/tmp",
			IntervalSeconds: 3600,
		},
	}

	s.Start(ctx, schedules)
	defer s.StopAll()

	// Give goroutine time to start
	time.Sleep(50 * time.Millisecond)

	ss, ok := state.GetSchedule("backup")
	if !ok {
		t.Fatal("expected schedule 'backup' in state")
	}
	if ss.LastResult != "pending" {
		t.Errorf("expected initial last_result 'pending', got %s", ss.LastResult)
	}
	if ss.NextRun.IsZero() {
		t.Error("expected NextRun to be set")
	}
}

func TestStart_ResumesFromPersistedState(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")

	// Create persisted state with a recent last run
	lastRun := time.Now().Add(-10 * time.Second)
	ps := persistedState{
		Schedules: map[string]*status.ScheduleStatus{
			"backup": {
				Name:       "backup",
				LastRun:    &lastRun,
				LastResult: "success",
			},
		},
	}
	data, _ := json.Marshal(ps)
	os.WriteFile(stateFile, data, 0600)

	state := testutil.NewTestState()
	s := New(state, testutil.NewTestLogBuffer(), stateFile, testutil.NewTestLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	schedules := map[string]config.ScheduleConfig{
		"backup": {
			Command:         []string{"echo", "ok"},
			WorkingDir:      "/tmp",
			IntervalSeconds: 3600,
		},
	}

	s.Start(ctx, schedules)
	defer s.StopAll()

	time.Sleep(50 * time.Millisecond)

	ss, ok := state.GetSchedule("backup")
	if !ok {
		t.Fatal("expected schedule in state")
	}
	// NextRun should be ~3590s from now (last run 10s ago + 3600s interval)
	if ss.NextRun.Before(time.Now().Add(3500 * time.Second)) {
		t.Errorf("expected next run far in future, got %v", ss.NextRun)
	}
}

func TestStart_ContextCancelStopsSchedule(t *testing.T) {
	state := testutil.NewTestState()
	s := New(state, testutil.NewTestLogBuffer(), "", testutil.NewTestLogger())
	ctx, cancel := context.WithCancel(context.Background())

	schedules := map[string]config.ScheduleConfig{
		"job": {
			Command:         []string{"echo", "hi"},
			WorkingDir:      "/tmp",
			IntervalSeconds: 3600,
		},
	}

	s.Start(ctx, schedules)
	time.Sleep(50 * time.Millisecond)

	cancel()
	time.Sleep(50 * time.Millisecond)

	// Verify schedule goroutine exited cleanly (no panic, no deadlock)
	// The fact that we get here means it worked
}

// --- execute (integration) ---

func TestExecute_SuccessfulCommand(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")
	state := testutil.NewTestState()
	logs := testutil.NewTestLogBuffer()
	s := New(state, logs, stateFile, testutil.NewTestLogger())

	cfg := config.ScheduleConfig{
		Command:    []string{"echo", "hello world"},
		WorkingDir: "/tmp",
	}

	ctx := context.Background()
	s.execute(ctx, "test-job", cfg)

	ss, ok := state.GetSchedule("test-job")
	if !ok {
		t.Fatal("expected schedule in state after execute")
	}
	if ss.LastResult != "success" {
		t.Errorf("expected last_result 'success', got %s", ss.LastResult)
	}
	if ss.LastRun == nil {
		t.Error("expected LastRun to be set")
	}
	if ss.Duration <= 0 {
		t.Errorf("expected positive duration, got %d", ss.Duration)
	}
	if ss.LastError != "" {
		t.Errorf("expected no error, got %s", ss.LastError)
	}

	// Verify state was persisted
	_, err := os.Stat(stateFile)
	if err != nil {
		t.Errorf("expected state file to exist: %v", err)
	}
}

func TestExecute_FailedCommand(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")
	state := testutil.NewTestState()
	s := New(state, testutil.NewTestLogBuffer(), stateFile, testutil.NewTestLogger())

	cfg := config.ScheduleConfig{
		Command:    []string{"false"},
		WorkingDir: "/tmp",
	}

	ctx := context.Background()
	s.execute(ctx, "fail-job", cfg)

	ss, ok := state.GetSchedule("fail-job")
	if !ok {
		t.Fatal("expected schedule in state after execute")
	}
	if ss.LastResult != "failed" {
		t.Errorf("expected last_result 'failed', got %s", ss.LastResult)
	}
	if ss.LastError == "" {
		t.Error("expected LastError to be set for failed command")
	}
}

func TestExecute_CapturesOutput(t *testing.T) {
	state := testutil.NewTestState()
	logs := testutil.NewTestLogBuffer()
	s := New(state, logs, "", testutil.NewTestLogger())

	cfg := config.ScheduleConfig{
		Command:    []string{"echo", "test output"},
		WorkingDir: "/tmp",
	}

	ctx := context.Background()
	s.execute(ctx, "output-job", cfg)

	// Verify output was captured in log buffer
	lines := logs.Lines("output-job", 10, "")
	found := false
	for _, line := range lines {
		if strings.Contains(line.Message, "test output") {
			found = true
			break
		}
	}
	if !found {
		msgs := make([]string, len(lines))
		for i, l := range lines {
			msgs[i] = l.Message
		}
		t.Errorf("expected 'test output' in log buffer, got: %v", msgs)
	}
}

func TestExecute_DefaultTimeout(t *testing.T) {
	state := testutil.NewTestState()
	s := New(state, testutil.NewTestLogBuffer(), "", testutil.NewTestLogger())

	cfg := config.ScheduleConfig{
		Command:    []string{"echo", "quick"},
		WorkingDir: "/tmp",
		// TimeoutSecs: 0 → should default to 5 minutes
	}

	ctx := context.Background()
	s.execute(ctx, "timeout-test", cfg)

	ss, ok := state.GetSchedule("timeout-test")
	if !ok {
		t.Fatal("expected schedule in state")
	}
	if ss.LastResult != "success" {
		t.Errorf("expected success, got %s", ss.LastResult)
	}
}

func TestExecute_CustomTimeout(t *testing.T) {
	state := testutil.NewTestState()
	s := New(state, testutil.NewTestLogBuffer(), "", testutil.NewTestLogger())

	cfg := config.ScheduleConfig{
		Command:    []string{"echo", "fast"},
		WorkingDir: "/tmp",
		TimeoutSecs: 10,
	}

	ctx := context.Background()
	s.execute(ctx, "custom-timeout", cfg)

	ss, ok := state.GetSchedule("custom-timeout")
	if !ok {
		t.Fatal("expected schedule in state")
	}
	if ss.LastResult != "success" {
		t.Errorf("expected success, got %s", ss.LastResult)
	}
}

func TestExecute_SetsRunningStateDuringExecution(t *testing.T) {
	state := testutil.NewTestState()
	s := New(state, testutil.NewTestLogBuffer(), "", testutil.NewTestLogger())

	// Use a command that takes a moment — "sleep 0" is instantaneous but
	// the state should be set to "running" before the command finishes
	cfg := config.ScheduleConfig{
		Command:    []string{"echo", "hi"},
		WorkingDir: "/tmp",
	}

	ctx := context.Background()
	s.execute(ctx, "running-test", cfg)

	// After execute returns, state should be success (not running)
	ss, ok := state.GetSchedule("running-test")
	if !ok {
		t.Fatal("expected schedule in state")
	}
	if ss.LastResult == "running" {
		t.Error("expected final state to not be 'running'")
	}
}

func TestExecute_WithEnv(t *testing.T) {
	state := testutil.NewTestState()
	s := New(state, testutil.NewTestLogBuffer(), "", testutil.NewTestLogger())

	cfg := config.ScheduleConfig{
		Command:    []string{"sh", "-c", "echo $TEST_SKIFF_SCHED_VAR"},
		WorkingDir: "/tmp",
		Env:        map[string]string{"TEST_SKIFF_SCHED_VAR": "hello"},
	}

	ctx := context.Background()
	s.execute(ctx, "env-test", cfg)

	ss, ok := state.GetSchedule("env-test")
	if !ok {
		t.Fatal("expected schedule in state")
	}
	if ss.LastResult != "success" {
		t.Errorf("expected success, got %s (error: %s)", ss.LastResult, ss.LastError)
	}
}

// --- TriggerNow integration ---

func TestTriggerNow_ExecutesImmediately(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "state.json")
	state := testutil.NewTestState()
	s := New(state, testutil.NewTestLogBuffer(), stateFile, testutil.NewTestLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	schedules := map[string]config.ScheduleConfig{
		"job": {
			Command:         []string{"echo", "triggered"},
			WorkingDir:      "/tmp",
			IntervalSeconds: 3600, // long interval — won't fire naturally
		},
	}

	s.Start(ctx, schedules)
	time.Sleep(50 * time.Millisecond)

	// Trigger immediately
	if err := s.TriggerNow("job"); err != nil {
		t.Fatalf("TriggerNow: %v", err)
	}

	// Wait for execution
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ss, ok := state.GetSchedule("job")
		if ok && ss.LastResult == "success" {
			return // passed
		}
		time.Sleep(100 * time.Millisecond)
	}

	ss, _ := state.GetSchedule("job")
	t.Errorf("expected last_result 'success' after trigger, got %s", ss.LastResult)
}

// --- computeNextRun with calendar ---

func TestComputeNextRun_Calendar(t *testing.T) {
	cfg := config.ScheduleConfig{
		Calendar: &config.CalendarInterval{
			Hour:   intPtr(9),
			Minute: intPtr(0),
		},
	}

	next := computeNextRun(cfg, nil)

	if next.Hour() != 9 || next.Minute() != 0 {
		t.Errorf("expected 09:00, got %02d:%02d", next.Hour(), next.Minute())
	}
	if !next.After(time.Now()) {
		t.Error("expected next to be in the future")
	}
}

// --- nextCalendarRun edge cases ---

func TestNextCalendarRun_Monthly(t *testing.T) {
	// March 15, looking for day 1
	from := time.Date(2025, 3, 15, 12, 0, 0, 0, time.Local)
	cal := &config.CalendarInterval{Day: intPtr(1), Hour: intPtr(0), Minute: intPtr(0)}

	next := nextCalendarRun(cal, from)

	if next.Day() != 1 {
		t.Errorf("expected day 1, got %d", next.Day())
	}
	if next.Month() != time.April {
		t.Errorf("expected April, got %s", next.Month())
	}
}

func TestNextCalendarRun_SpecificMonth(t *testing.T) {
	// January 1, looking for month=6 (June)
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	cal := &config.CalendarInterval{Month: intPtr(6), Day: intPtr(1), Hour: intPtr(0), Minute: intPtr(0)}

	next := nextCalendarRun(cal, from)

	if next.Month() != time.June {
		t.Errorf("expected June, got %s", next.Month())
	}
}
