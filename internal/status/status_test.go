package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chronick/skiff/internal/runtime"
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

func TestResourceStateMarshalJSON(t *testing.T) {
	for _, tt := range []struct {
		state ResourceState
		want  string
	}{
		{StateRunning, `"running"`},
		{StateStopped, `"stopped"`},
		{StateFailed, `"failed"`},
		{StateStarting, `"starting"`},
		{StateUnknown, `"unknown"`},
	} {
		got, err := json.Marshal(tt.state)
		if err != nil {
			t.Fatalf("marshal state %d: %v", tt.state, err)
		}
		if string(got) != tt.want {
			t.Errorf("marshal State(%d) = %s, want %s", tt.state, got, tt.want)
		}
	}
}

func TestResourceTypeStringAndMarshalJSON(t *testing.T) {
	for _, tt := range []struct {
		typ  ResourceType
		want string
	}{
		{TypeService, "service"},
		{TypeContainer, "container"},
		{TypeSchedule, "schedule"},
		{ResourceType(99), "unknown"},
	} {
		if got := tt.typ.String(); got != tt.want {
			t.Errorf("Type(%d).String() = %q, want %q", tt.typ, got, tt.want)
		}
		got, err := json.Marshal(tt.typ)
		if err != nil {
			t.Fatalf("marshal type %d: %v", tt.typ, err)
		}
		if string(got) != `"`+tt.want+`"` {
			t.Errorf("marshal Type(%d) = %s, want %q", tt.typ, got, tt.want)
		}
	}
}

func TestUpdateStats(t *testing.T) {
	s := NewSharedState()
	s.SetResource(&ResourceStatus{Name: "db", Type: TypeContainer, State: StateRunning})

	s.UpdateStats("db", &runtime.ContainerStats{CPUPercent: 12.5, MemUsageMB: 256, PIDs: 7})

	rs, ok := s.GetResource("db")
	if !ok {
		t.Fatal("expected to find resource 'db'")
	}
	if rs.Stats == nil {
		t.Fatal("expected stats to be set")
	}
	if rs.Stats.CPUPercent != 12.5 || rs.Stats.MemUsageMB != 256 || rs.Stats.PIDs != 7 {
		t.Errorf("unexpected stats: %+v", rs.Stats)
	}
}

func TestUpdateStatsUnknownResourceIsNoop(t *testing.T) {
	s := NewSharedState()

	s.UpdateStats("nonexistent", &runtime.ContainerStats{CPUPercent: 1})

	if _, ok := s.GetResource("nonexistent"); ok {
		t.Fatal("UpdateStats must not create a resource")
	}
}

func TestRemoveSchedule(t *testing.T) {
	s := NewSharedState()
	s.SetSchedule(&ScheduleStatus{Name: "backup", LastResult: "success"})

	s.RemoveSchedule("backup")
	if _, ok := s.GetSchedule("backup"); ok {
		t.Fatal("expected schedule to be removed")
	}

	// Removing an absent schedule is a no-op.
	s.RemoveSchedule("backup")
}

func TestGetScheduleNotFound(t *testing.T) {
	s := NewSharedState()

	if _, ok := s.GetSchedule("nonexistent"); ok {
		t.Fatal("expected not found")
	}
}

func TestResourcesByLabel(t *testing.T) {
	s := NewSharedState()

	s.SetResource(&ResourceStatus{Name: "web", Type: TypeService, Labels: map[string]string{"tier": "front"}})
	s.SetResource(&ResourceStatus{Name: "api", Type: TypeService, Labels: map[string]string{"tier": "front"}})
	s.SetResource(&ResourceStatus{Name: "db", Type: TypeContainer, Labels: map[string]string{"tier": "back"}})
	s.SetResource(&ResourceStatus{Name: "cron", Type: TypeSchedule})

	if got := s.ResourcesByLabel("tier", "front"); len(got) != 2 {
		t.Errorf("expected 2 front-tier resources, got %d", len(got))
	}
	if got := s.ResourcesByLabel("tier", "back"); len(got) != 1 {
		t.Errorf("expected 1 back-tier resource, got %d", len(got))
	}
	if got := s.ResourcesByLabel("tier", "missing"); len(got) != 0 {
		t.Errorf("expected 0 resources for unmatched value, got %d", len(got))
	}
	if got := s.ResourcesByLabel("nosuchkey", "v"); len(got) != 0 {
		t.Errorf("expected 0 resources for unknown key, got %d", len(got))
	}
	// Documents current behavior: an empty value matches every labeled resource,
	// since a missing key reads back as "". Unlabeled resources never match.
	if got := s.ResourcesByLabel("nosuchkey", ""); len(got) != 3 {
		t.Errorf("expected 3 labeled resources for empty-value lookup, got %d", len(got))
	}
}

func TestSnapshotComputesUptimeForRunning(t *testing.T) {
	s := NewSharedState()

	s.SetResource(&ResourceStatus{
		Name:      "web",
		Type:      TypeService,
		State:     StateRunning,
		StartedAt: time.Now().Add(-90 * time.Second),
	})
	// Stopped resources and running-without-StartedAt keep UptimeSecs at zero.
	s.SetResource(&ResourceStatus{Name: "worker", Type: TypeService, State: StateStopped,
		StartedAt: time.Now().Add(-90 * time.Second)})
	s.SetResource(&ResourceStatus{Name: "api", Type: TypeService, State: StateRunning})

	byName := map[string]*ResourceStatus{}
	for _, rs := range s.Snapshot()["resources"].([]*ResourceStatus) {
		byName[rs.Name] = rs
	}

	if got := byName["web"].UptimeSecs; got < 89 || got > 120 {
		t.Errorf("expected web uptime ~90s, got %d", got)
	}
	if got := byName["worker"].UptimeSecs; got != 0 {
		t.Errorf("expected stopped resource uptime 0, got %d", got)
	}
	if got := byName["api"].UptimeSecs; got != 0 {
		t.Errorf("expected zero-StartedAt uptime 0, got %d", got)
	}
	// Snapshot must not mutate the stored resource.
	stored, _ := s.GetResource("web")
	if stored.UptimeSecs != 0 {
		t.Errorf("snapshot mutated stored resource: uptime %d", stored.UptimeSecs)
	}
}

func TestSave(t *testing.T) {
	s := NewSharedState()
	s.SetResource(&ResourceStatus{Name: "web", Type: TypeService, State: StateRunning, PID: 42})
	s.SetSchedule(&ScheduleStatus{Name: "backup", LastResult: "pending"})

	path := filepath.Join(t.TempDir(), "state.json")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("expected mode 0600, got %o", perm)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var out struct {
		Resources []struct {
			Name  string `json:"name"`
			Type  string `json:"type"`
			State string `json:"state"`
			PID   int    `json:"pid"`
		} `json:"resources"`
		Schedules []struct {
			Name string `json:"name"`
		} `json:"schedules"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Resources) != 1 || out.Resources[0].Name != "web" {
		t.Fatalf("unexpected resources: %+v", out.Resources)
	}
	if out.Resources[0].Type != "service" || out.Resources[0].State != "running" || out.Resources[0].PID != 42 {
		t.Errorf("unexpected resource fields: %+v", out.Resources[0])
	}
	if len(out.Schedules) != 1 || out.Schedules[0].Name != "backup" {
		t.Fatalf("unexpected schedules: %+v", out.Schedules)
	}
}

func TestSaveBadPath(t *testing.T) {
	s := NewSharedState()

	if err := s.Save(filepath.Join(t.TempDir(), "no-such-dir", "state.json")); err == nil {
		t.Fatal("expected error writing to a nonexistent directory")
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := NewSharedState()
	s.SetResource(&ResourceStatus{Name: "db", Type: TypeContainer, State: StateRunning})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			s.SetResource(&ResourceStatus{Name: "web", Type: TypeService, State: StateRunning, PID: i})
			s.UpdateStats("db", &runtime.ContainerStats{CPUPercent: float64(i)})
			s.SetSchedule(&ScheduleStatus{Name: "backup", LastResult: "running"})
			s.RemoveSchedule("backup")
		}
	}()
	for i := 0; i < 200; i++ {
		s.GetResource("web")
		s.GetSchedule("backup")
		s.ResourcesByType(TypeService)
		s.ResourcesByLabel("tier", "front")
		s.Snapshot()
	}
	<-done
}
