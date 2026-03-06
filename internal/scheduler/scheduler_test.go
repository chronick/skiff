package scheduler

import (
	"encoding/json"
	"os"
	"path/filepath"
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
