package status

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/chronick/skiff/internal/runtime"
)

// ResourceState represents the current state of a resource.
type ResourceState int

const (
	StateUnknown ResourceState = iota
	StateRunning
	StateStopped
	StateFailed
	StateStarting
)

func (s ResourceState) String() string {
	switch s {
	case StateRunning:
		return "running"
	case StateStopped:
		return "stopped"
	case StateFailed:
		return "failed"
	case StateStarting:
		return "starting"
	default:
		return "unknown"
	}
}

func (s ResourceState) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// ResourceType identifies the kind of resource.
type ResourceType int

const (
	TypeService ResourceType = iota
	TypeContainer
	TypeSchedule
)

func (t ResourceType) String() string {
	switch t {
	case TypeService:
		return "service"
	case TypeContainer:
		return "container"
	case TypeSchedule:
		return "schedule"
	default:
		return "unknown"
	}
}

func (t ResourceType) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.String())
}

// HealthState tracks health check status.
type HealthState struct {
	Status           string    `json:"status"` // healthy | unhealthy | unknown
	ConsecutiveFails int       `json:"consecutive_fails"`
	LastCheck        time.Time `json:"last_check"`
	LastError        string    `json:"last_error,omitempty"`
}

// ResourceStatus is the status of a single resource.
type ResourceStatus struct {
	Name       string                 `json:"name"`
	Type       ResourceType           `json:"type"`
	State      ResourceState          `json:"state"`
	PID        int                    `json:"pid,omitempty"`
	UptimeSecs int64                  `json:"uptime_secs,omitempty"`
	StartedAt  time.Time              `json:"started_at,omitempty"`
	ExitCode   int                    `json:"exit_code,omitempty"`
	LastError  string                 `json:"last_error,omitempty"`
	ConfigHash string                 `json:"config_hash"`
	Health     *HealthState           `json:"health,omitempty"`
	Ports      []string               `json:"ports,omitempty"`
	DependsOn  []string               `json:"depends_on,omitempty"`
	Stats      *runtime.ContainerStats `json:"stats,omitempty"`
}

// ScheduleStatus tracks schedule state.
type ScheduleStatus struct {
	Name       string     `json:"name"`
	LastRun    *time.Time `json:"last_run,omitempty"`
	NextRun    time.Time  `json:"next_run"`
	LastResult string     `json:"last_result"` // success | failed | running | pending
	LastError  string     `json:"last_error,omitempty"`
	Duration   int64      `json:"last_duration_ms,omitempty"`
}

// SharedState is the thread-safe platform state.
type SharedState struct {
	mu        sync.RWMutex
	Resources map[string]*ResourceStatus `json:"resources"`
	Schedules map[string]*ScheduleStatus `json:"schedules"`
	Timestamp time.Time                  `json:"timestamp"`
}

// NewSharedState creates an empty SharedState.
func NewSharedState() *SharedState {
	return &SharedState{
		Resources: make(map[string]*ResourceStatus),
		Schedules: make(map[string]*ScheduleStatus),
		Timestamp: time.Now(),
	}
}

// SetResource updates or creates a resource status.
func (s *SharedState) SetResource(rs *ResourceStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Resources[rs.Name] = rs
	s.Timestamp = time.Now()
}

// GetResource returns a copy of a resource status.
func (s *SharedState) GetResource(name string) (*ResourceStatus, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rs, ok := s.Resources[name]
	if !ok {
		return nil, false
	}
	copy := *rs
	return &copy, true
}

// UpdateStats updates the stats for a running container resource.
func (s *SharedState) UpdateStats(name string, stats *runtime.ContainerStats) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rs, ok := s.Resources[name]; ok {
		rs.Stats = stats
	}
}

// RemoveResource deletes a resource from state.
func (s *SharedState) RemoveResource(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Resources, name)
	s.Timestamp = time.Now()
}

// SetSchedule updates or creates a schedule status.
func (s *SharedState) SetSchedule(ss *ScheduleStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Schedules[ss.Name] = ss
	s.Timestamp = time.Now()
}

// GetSchedule returns a copy of a schedule status.
func (s *SharedState) GetSchedule(name string) (*ScheduleStatus, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ss, ok := s.Schedules[name]
	if !ok {
		return nil, false
	}
	copy := *ss
	return &copy, true
}

// Snapshot returns a JSON-serializable copy of the full state.
func (s *SharedState) Snapshot() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	resources := make([]*ResourceStatus, 0, len(s.Resources))
	for _, rs := range s.Resources {
		copy := *rs
		if copy.State == StateRunning && !copy.StartedAt.IsZero() {
			copy.UptimeSecs = int64(time.Since(copy.StartedAt).Seconds())
		}
		resources = append(resources, &copy)
	}
	schedules := make([]*ScheduleStatus, 0, len(s.Schedules))
	for _, ss := range s.Schedules {
		copy := *ss
		schedules = append(schedules, &copy)
	}

	return map[string]interface{}{
		"resources": resources,
		"schedules": schedules,
		"timestamp": s.Timestamp,
	}
}

// ResourcesByType returns all resources of a given type.
func (s *SharedState) ResourcesByType(t ResourceType) []*ResourceStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*ResourceStatus
	for _, rs := range s.Resources {
		if rs.Type == t {
			copy := *rs
			result = append(result, &copy)
		}
	}
	return result
}

// Save persists state to a JSON file.
func (s *SharedState) Save(path string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := json.MarshalIndent(s.Snapshot(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
