package status

import (
	"testing"
	"time"
)

func TestSetAndGetResource(t *testing.T) {
	s := NewSharedState()

	s.SetResource(&ResourceStatus{
		Name:  "web",
		Type:  TypeService,
		State: StateRunning,
		PID:   1234,
	})

	rs, ok := s.GetResource("web")
	if !ok {
		t.Fatal("expected to find resource 'web'")
	}
	if rs.PID != 1234 {
		t.Errorf("expected PID 1234, got %d", rs.PID)
	}
	if rs.State != StateRunning {
		t.Errorf("expected running, got %s", rs.State)
	}
}

func TestGetResourceNotFound(t *testing.T) {
	s := NewSharedState()

	_, ok := s.GetResource("nonexistent")
	if ok {
		t.Fatal("expected not found")
	}
}

func TestRemoveResource(t *testing.T) {
	s := NewSharedState()

	s.SetResource(&ResourceStatus{Name: "web", Type: TypeService, State: StateRunning})
	s.RemoveResource("web")

	_, ok := s.GetResource("web")
	if ok {
		t.Fatal("expected resource to be removed")
	}
}

func TestResourcesByType(t *testing.T) {
	s := NewSharedState()

	s.SetResource(&ResourceStatus{Name: "web", Type: TypeService, State: StateRunning})
	s.SetResource(&ResourceStatus{Name: "db", Type: TypeContainer, State: StateRunning})
	s.SetResource(&ResourceStatus{Name: "worker", Type: TypeService, State: StateStopped})

	services := s.ResourcesByType(TypeService)
	if len(services) != 2 {
		t.Errorf("expected 2 services, got %d", len(services))
	}

	containers := s.ResourcesByType(TypeContainer)
	if len(containers) != 1 {
		t.Errorf("expected 1 container, got %d", len(containers))
	}
}

func TestScheduleSetAndGet(t *testing.T) {
	s := NewSharedState()

	now := time.Now()
	s.SetSchedule(&ScheduleStatus{
		Name:       "backup",
		LastResult: "success",
		LastRun:    &now,
		NextRun:    now.Add(time.Hour),
	})

	ss, ok := s.GetSchedule("backup")
	if !ok {
		t.Fatal("expected to find schedule")
	}
	if ss.LastResult != "success" {
		t.Errorf("expected success, got %s", ss.LastResult)
	}
}

func TestSnapshot(t *testing.T) {
	s := NewSharedState()

	s.SetResource(&ResourceStatus{Name: "web", Type: TypeService, State: StateRunning})
	s.SetSchedule(&ScheduleStatus{Name: "backup", LastResult: "pending"})

	snap := s.Snapshot()
	resources, ok := snap["resources"].([]*ResourceStatus)
	if !ok || len(resources) != 1 {
		t.Error("expected 1 resource in snapshot")
	}
	schedules, ok := snap["schedules"].([]*ScheduleStatus)
	if !ok || len(schedules) != 1 {
		t.Error("expected 1 schedule in snapshot")
	}
}

func TestResourceStateString(t *testing.T) {
	tests := []struct {
		state ResourceState
		want  string
	}{
		{StateRunning, "running"},
		{StateStopped, "stopped"},
		{StateFailed, "failed"},
		{StateStarting, "starting"},
		{StateUnknown, "unknown"},
	}

	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}
