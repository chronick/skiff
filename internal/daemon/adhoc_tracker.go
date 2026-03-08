package daemon

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/chronick/skiff/internal/runtime"
	"github.com/chronick/skiff/internal/status"
)

// adhocEntry tracks an ad-hoc container and its lifecycle options.
type adhocEntry struct {
	name       string
	parent     string
	remove     bool
	timeoutSec int
	timer      *time.Timer
}

// AdhocTracker manages ad-hoc container lifecycles: timeouts, auto-remove,
// and parent-child relationships.
type AdhocTracker struct {
	mu      sync.Mutex
	entries map[string]*adhocEntry // keyed by container name
	state   *status.SharedState
	runtime runtime.ContainerRuntime
	logger  *slog.Logger
}

// NewAdhocTracker creates a new AdhocTracker.
func NewAdhocTracker(state *status.SharedState, rt runtime.ContainerRuntime, logger *slog.Logger) *AdhocTracker {
	return &AdhocTracker{
		entries: make(map[string]*adhocEntry),
		state:   state,
		runtime: rt,
		logger:  logger,
	}
}

// Track registers an ad-hoc container for lifecycle management.
func (a *AdhocTracker) Track(name, parent string, remove bool, timeoutSec int) {
	a.mu.Lock()
	defer a.mu.Unlock()

	entry := &adhocEntry{
		name:       name,
		parent:     parent,
		remove:     remove,
		timeoutSec: timeoutSec,
	}

	if timeoutSec > 0 {
		entry.timer = time.AfterFunc(time.Duration(timeoutSec)*time.Second, func() {
			a.logger.Info("ad-hoc container timeout expired", "name", name, "timeout_secs", timeoutSec)
			a.stopAndCleanup(name)
		})
	}

	a.entries[name] = entry
}

// Untrack removes an ad-hoc container from tracking.
func (a *AdhocTracker) Untrack(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if entry, ok := a.entries[name]; ok {
		if entry.timer != nil {
			entry.timer.Stop()
		}
		delete(a.entries, name)
	}
}

// IsAdhoc returns true if the named container is tracked as ad-hoc.
func (a *AdhocTracker) IsAdhoc(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.entries[name]
	return ok
}

// ShouldRemove returns true if the ad-hoc container was created with remove=true.
func (a *AdhocTracker) ShouldRemove(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if entry, ok := a.entries[name]; ok {
		return entry.remove
	}
	return false
}

// Children returns the names of all ad-hoc containers with the given parent.
func (a *AdhocTracker) Children(parent string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()

	var result []string
	for _, entry := range a.entries {
		if entry.parent == parent {
			result = append(result, entry.name)
		}
	}
	return result
}

// StopChildren stops and cleans up all ad-hoc containers with the given parent.
func (a *AdhocTracker) StopChildren(parent string) {
	children := a.Children(parent)
	for _, name := range children {
		a.logger.Info("stopping child container (parent stopped)", "child", name, "parent", parent)
		a.stopAndCleanup(name)
	}
}

// stopAndCleanup stops a container, updates state, and optionally removes it.
func (a *AdhocTracker) stopAndCleanup(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	a.mu.Lock()
	entry, ok := a.entries[name]
	if !ok {
		a.mu.Unlock()
		return
	}
	shouldRemove := entry.remove
	if entry.timer != nil {
		entry.timer.Stop()
	}
	delete(a.entries, name)
	a.mu.Unlock()

	if err := a.runtime.Stop(ctx, name); err != nil {
		a.logger.Error("failed to stop ad-hoc container", "name", name, "error", err)
	}

	if shouldRemove {
		a.state.RemoveResource(name)
		a.logger.Info("removed ad-hoc container", "name", name)
	} else {
		a.state.SetResource(&status.ResourceStatus{
			Name:  name,
			Type:  status.TypeContainer,
			State: status.StateStopped,
		})
	}
}

// StopAll stops all tracked ad-hoc containers.
func (a *AdhocTracker) StopAll() {
	a.mu.Lock()
	names := make([]string, 0, len(a.entries))
	for name := range a.entries {
		names = append(names, name)
	}
	a.mu.Unlock()

	for _, name := range names {
		a.stopAndCleanup(name)
	}
}

// PollExited checks for ad-hoc containers that have exited and handles
// auto-remove. Call this periodically from a background goroutine.
func (a *AdhocTracker) PollExited() {
	a.mu.Lock()
	names := make([]string, 0, len(a.entries))
	for name := range a.entries {
		names = append(names, name)
	}
	a.mu.Unlock()

	for _, name := range names {
		rs, ok := a.state.GetResource(name)
		if !ok {
			// Resource removed externally, clean up tracking.
			a.Untrack(name)
			continue
		}

		if rs.State == status.StateStopped || rs.State == status.StateFailed {
			a.mu.Lock()
			entry, ok := a.entries[name]
			if !ok {
				a.mu.Unlock()
				continue
			}
			shouldRemove := entry.remove
			if entry.timer != nil {
				entry.timer.Stop()
			}
			delete(a.entries, name)
			a.mu.Unlock()

			if shouldRemove {
				a.state.RemoveResource(name)
				a.logger.Info("auto-removed exited ad-hoc container", "name", name)
			}
		}
	}
}
