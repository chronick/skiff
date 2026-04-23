package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// pidRecord persists enough metadata to identify a supervised process across
// daemon restarts. Stored in a sidecar JSON file so that even a hard-killed
// daemon (SIGKILL via launchctl kickstart -k) leaves behind a trail the next
// daemon can use to clean up orphans before they trigger port-bind crash loops.
type pidRecord struct {
	Name      string    `json:"name"`
	PID       int       `json:"pid"`
	PGID      int       `json:"pgid"`
	Command   string    `json:"command"`    // first arg of the command line
	StartedAt time.Time `json:"started_at"`
}

// pidStore is a JSON-backed map[name]pidRecord guarded by a mutex.
type pidStore struct {
	mu      sync.Mutex
	path    string
	records map[string]pidRecord
}

func newPIDStore(path string) *pidStore {
	s := &pidStore{path: path, records: make(map[string]pidRecord)}
	if path != "" {
		_ = s.load()
	}
	return s
}

func (s *pidStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var recs []pidRecord
	if err := json.Unmarshal(data, &recs); err != nil {
		return err
	}
	for _, r := range recs {
		s.records[r.Name] = r
	}
	return nil
}

func (s *pidStore) flushLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	recs := make([]pidRecord, 0, len(s.records))
	for _, r := range s.records {
		recs = append(recs, r)
	}
	data, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *pidStore) Set(rec pidRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[rec.Name] = rec
	return s.flushLocked()
}

func (s *pidStore) Remove(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[name]; !ok {
		return nil
	}
	delete(s.records, name)
	return s.flushLocked()
}

func (s *pidStore) Snapshot() []pidRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	recs := make([]pidRecord, 0, len(s.records))
	for _, r := range s.records {
		recs = append(recs, r)
	}
	return recs
}

// ReapOrphans inspects the persisted pid file and kills any processes that
// (a) are still alive, and (b) look like supervised children from a prior
// daemon (their command argv0 matches the recorded value). After reaping it
// truncates the pid file. Returns the names of services whose orphans were
// killed, and a warning slice for processes that we could not verify or kill.
//
// The verification step (matching command argv0 via `ps`) protects against
// PID reuse: if the OS handed our old PID to an unrelated process between
// daemon shutdown and startup, we leave it alone and log a warning.
func (s *Supervisor) ReapOrphans() (killed []string) {
	if s.pids == nil {
		return nil
	}
	records := s.pids.Snapshot()
	if len(records) == 0 {
		return nil
	}

	for _, rec := range records {
		if rec.PID <= 0 {
			_ = s.pids.Remove(rec.Name)
			continue
		}
		alive, err := isProcessAlive(rec.PID)
		if err != nil {
			s.logger.Warn("orphan check failed", "name", rec.Name, "pid", rec.PID, "error", err)
			_ = s.pids.Remove(rec.Name)
			continue
		}
		if !alive {
			_ = s.pids.Remove(rec.Name)
			continue
		}

		actual, err := processCommand(rec.PID)
		if err != nil {
			// Could not verify; safer to skip than to kill the wrong pid.
			s.logger.Warn("orphan reap skipped: could not read command",
				"name", rec.Name, "pid", rec.PID, "error", err)
			_ = s.pids.Remove(rec.Name)
			continue
		}
		if !commandsMatch(rec.Command, actual) {
			s.logger.Warn("orphan reap skipped: pid reused by unrelated process",
				"name", rec.Name, "pid", rec.PID,
				"expected_argv0", rec.Command, "actual_command", actual)
			_ = s.pids.Remove(rec.Name)
			continue
		}

		s.logger.Warn("reaping orphan from previous daemon",
			"name", rec.Name, "pid", rec.PID, "command", actual)

		killOrphan(rec, s.logger)
		_ = s.pids.Remove(rec.Name)
		killed = append(killed, rec.Name)
	}
	return killed
}

// killOrphan sends SIGTERM to the process group, waits up to 5s for exit,
// then escalates to SIGKILL.
func killOrphan(rec pidRecord, logger *slog.Logger) {
	pgid := rec.PGID
	if pgid == 0 {
		if pg, err := syscall.Getpgid(rec.PID); err == nil {
			pgid = pg
		} else {
			pgid = rec.PID
		}
	}

	_ = syscall.Kill(-pgid, syscall.SIGTERM)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		alive, err := isProcessAlive(rec.PID)
		if err != nil || !alive {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	logger.Warn("orphan did not exit after SIGTERM, escalating to SIGKILL",
		"name", rec.Name, "pid", rec.PID)
	_ = syscall.Kill(-pgid, syscall.SIGKILL)

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		alive, err := isProcessAlive(rec.PID)
		if err != nil || !alive {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	logger.Error("orphan still alive after SIGKILL", "name", rec.Name, "pid", rec.PID)
}

// isProcessAlive returns true if the given PID exists and we can signal it.
// Sending signal 0 is a probe that does not deliver a real signal.
func isProcessAlive(pid int) (bool, error) {
	if pid <= 0 {
		return false, nil
	}
	err := syscall.Kill(pid, syscall.Signal(0))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if errors.Is(err, syscall.EPERM) {
		// Process exists but we can't signal it. Treat as alive.
		return true, nil
	}
	return false, err
}

// processCommand returns the command line of the given PID, using `ps` so
// the implementation works on both macOS and Linux without /proc parsing
// or platform-specific syscalls.
func processCommand(pid int) (string, error) {
	out, err := exec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "command=").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// commandsMatch is true when the actual full command line plausibly came
// from a process spawned with the recorded argv0. We compare the basename
// of argv0 to be tolerant of absolute-vs-relative paths, and require the
// actual command to begin with that token.
func commandsMatch(expectedArgv0, actual string) bool {
	if expectedArgv0 == "" || actual == "" {
		return false
	}
	exp := filepath.Base(expectedArgv0)
	first := strings.Fields(actual)
	if len(first) == 0 {
		return false
	}
	return filepath.Base(first[0]) == exp
}
