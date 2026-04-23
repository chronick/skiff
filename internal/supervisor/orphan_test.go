package supervisor

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/chronick/skiff/internal/logbuf"
	"github.com/chronick/skiff/internal/status"
)

func TestPIDStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "supervised.json")

	s := newPIDStore(path)
	rec := pidRecord{Name: "svc1", PID: 1234, PGID: 1234, Command: "/usr/bin/foo", StartedAt: time.Now()}
	if err := s.Set(rec); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Reload from disk and check
	s2 := newPIDStore(path)
	got := s2.Snapshot()
	if len(got) != 1 || got[0].Name != "svc1" || got[0].PID != 1234 {
		t.Fatalf("expected svc1/1234, got %+v", got)
	}

	if err := s2.Remove("svc1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// File should now be empty array
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var recs []pidRecord
	if err := json.Unmarshal(data, &recs); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("expected empty after Remove, got %+v", recs)
	}
}

func TestReapOrphans_DeadPIDIsCleaned(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "supervised.json")

	state := status.NewSharedState()
	logs := logbuf.New(100)
	sup := New(state, logs, "", path, newDiscardLogger())

	// PID 1 is init — definitely not "ours". Use a PID we know is dead:
	// spawn a process, get its PID, wait for it, then record that PID.
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("spawn helper: %v", err)
	}
	deadPID := cmd.Process.Pid

	if err := sup.pids.Set(pidRecord{
		Name:    "ghost",
		PID:     deadPID,
		Command: "true",
	}); err != nil {
		t.Fatalf("seed pidStore: %v", err)
	}

	killed := sup.ReapOrphans()
	if len(killed) != 0 {
		t.Errorf("expected no kills for dead pid, got %v", killed)
	}
	if got := sup.pids.Snapshot(); len(got) != 0 {
		t.Errorf("expected pidStore empty after reap, got %+v", got)
	}
}

func TestReapOrphans_PIDReuseIsSafe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "supervised.json")

	state := status.NewSharedState()
	logs := logbuf.New(100)
	sup := New(state, logs, "", path, newDiscardLogger())

	// Use our own PID (alive) but with a recorded command that won't
	// match. ReapOrphans should refuse to kill us.
	if err := sup.pids.Set(pidRecord{
		Name:    "imposter",
		PID:     os.Getpid(),
		Command: "/definitely/not/our/binary",
	}); err != nil {
		t.Fatalf("seed pidStore: %v", err)
	}

	killed := sup.ReapOrphans()
	if len(killed) != 0 {
		t.Errorf("expected no kills for mismatched command, got %v", killed)
	}
}

func TestReapOrphans_KillsLiveOrphan(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "supervised.json")

	state := status.NewSharedState()
	logs := logbuf.New(100)
	sup := New(state, logs, "", path, newDiscardLogger())

	// Spawn a long-running child in its own process group, then "leak"
	// it by recording its pid in the store and reaping it.
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn sleep: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	pgid, _ := syscall.Getpgid(pid)

	if err := sup.pids.Set(pidRecord{
		Name:    "leaked",
		PID:     pid,
		PGID:    pgid,
		Command: "sleep",
	}); err != nil {
		t.Fatalf("seed pidStore: %v", err)
	}

	killed := sup.ReapOrphans()
	if len(killed) != 1 || killed[0] != "leaked" {
		t.Errorf("expected to kill 'leaked', got %v", killed)
	}

	// Wait for the kernel to reap; cmd.Wait will return.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Error("orphan was not reaped within 8s")
	}
}

func TestSupervisor_PersistsPIDOnStart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "supervised.json")

	f := newMockFactory()
	state := status.NewSharedState()
	logs := logbuf.New(100)
	sup := New(state, logs, "", path, newDiscardLogger(), f)

	cfg := baseCfg()
	cfg.Command = []string{"/fake/bin/test-cmd", "--flag"}
	if err := sup.Start(t.Context(), "svc1", cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cmd := f.waitForCmd(t, 2*time.Second)
	time.Sleep(50 * time.Millisecond)

	recs := sup.pids.Snapshot()
	if len(recs) != 1 {
		t.Fatalf("expected 1 pid record, got %d", len(recs))
	}
	if recs[0].Name != "svc1" {
		t.Errorf("expected name svc1, got %s", recs[0].Name)
	}
	if recs[0].Command != "/fake/bin/test-cmd" {
		t.Errorf("expected command /fake/bin/test-cmd, got %s", recs[0].Command)
	}

	// Clean up: exit child and let supervise drain
	go func() {
		time.Sleep(20 * time.Millisecond)
		cmd.exit(nil)
	}()
	_ = sup.Stop("svc1")

	// PID record should be removed after stop
	if recs := sup.pids.Snapshot(); len(recs) != 0 {
		t.Errorf("expected empty pid store after Stop, got %+v", recs)
	}
}
