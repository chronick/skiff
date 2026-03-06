package health

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chronick/plane/internal/config"
	"github.com/chronick/plane/internal/status"
	"github.com/chronick/plane/internal/testutil"
)

func newTestChecker() (*Checker, *testutil.MockProcessRunner, *status.SharedState) {
	state := testutil.NewTestState()
	logs := testutil.NewTestLogBuffer()
	runner := testutil.NewMockRunner()
	logger := testutil.NewTestLogger()
	c := NewChecker(state, logs, runner, logger)
	return c, runner, state
}

// --- CheckOnce: HTTP ---

func TestCheckOnce_HTTP_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, _, _ := newTestChecker()
	err := c.CheckOnce(context.Background(), &config.HealthCheckConfig{
		Type: "http",
		URL:  srv.URL,
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestCheckOnce_HTTP_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c, _, _ := newTestChecker()
	err := c.CheckOnce(context.Background(), &config.HealthCheckConfig{
		Type: "http",
		URL:  srv.URL,
	})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestCheckOnce_HTTP_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()

	c, _, _ := newTestChecker()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := c.CheckOnce(ctx, &config.HealthCheckConfig{
		Type:        "http",
		URL:         srv.URL,
		TimeoutSecs: 1, // 1s timeout in the client, but ctx cancels at 100ms
	})
	if err == nil {
		t.Fatal("expected error for timeout")
	}
}

// --- CheckOnce: TCP ---

func TestCheckOnce_TCP_Success(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	c, _, _ := newTestChecker()
	err = c.CheckOnce(context.Background(), &config.HealthCheckConfig{
		Type: "tcp",
		Port: port,
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestCheckOnce_TCP_Refused(t *testing.T) {
	// Use a port that's (almost certainly) not listening
	c, _, _ := newTestChecker()
	err := c.CheckOnce(context.Background(), &config.HealthCheckConfig{
		Type:        "tcp",
		Port:        59123,
		TimeoutSecs: 1,
	})
	if err == nil {
		t.Fatal("expected error for refused connection")
	}
}

// --- CheckOnce: Command ---

func TestCheckOnce_Command_Success(t *testing.T) {
	c, runner, _ := newTestChecker()
	runner.DefaultResult = testutil.MockResult{Output: []byte("ok"), Err: nil}

	err := c.CheckOnce(context.Background(), &config.HealthCheckConfig{
		Type:    "command",
		Command: []string{"check", "--status"},
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if runner.CallCount() != 1 {
		t.Fatalf("expected 1 call, got %d", runner.CallCount())
	}
}

func TestCheckOnce_Command_Failure(t *testing.T) {
	c, runner, _ := newTestChecker()
	runner.DefaultResult = testutil.MockResult{Err: fmt.Errorf("exit status 1")}

	err := c.CheckOnce(context.Background(), &config.HealthCheckConfig{
		Type:    "command",
		Command: []string{"check"},
	})
	if err == nil {
		t.Fatal("expected error for failed command")
	}
}

// --- CheckOnce: Unknown type ---

func TestCheckOnce_UnknownType(t *testing.T) {
	c, _, _ := newTestChecker()
	err := c.CheckOnce(context.Background(), &config.HealthCheckConfig{
		Type: "grpc",
	})
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}

// --- recordResult: state transitions ---

func TestRecordResult_HealthyTransition(t *testing.T) {
	c, _, state := newTestChecker()
	state.SetResource(&status.ResourceStatus{
		Name:  "web",
		State: status.StateRunning,
	})

	hc := &config.HealthCheckConfig{FailureThreshold: 3}
	c.recordResult("web", hc, nil) // no error = healthy

	rs, _ := state.GetResource("web")
	if rs.Health == nil {
		t.Fatal("expected health state to be set")
	}
	if rs.Health.Status != "healthy" {
		t.Errorf("expected status healthy, got %s", rs.Health.Status)
	}
	if rs.Health.ConsecutiveFails != 0 {
		t.Errorf("expected 0 consecutive fails, got %d", rs.Health.ConsecutiveFails)
	}
}

func TestRecordResult_UnhealthyAfterThreshold(t *testing.T) {
	c, _, state := newTestChecker()
	state.SetResource(&status.ResourceStatus{
		Name:  "db",
		State: status.StateRunning,
	})

	hc := &config.HealthCheckConfig{FailureThreshold: 3}
	err := fmt.Errorf("connection refused")

	// Fail 3 times (threshold)
	for i := 0; i < 3; i++ {
		c.recordResult("db", hc, err)
	}

	rs, _ := state.GetResource("db")
	if rs.Health.Status != "unhealthy" {
		t.Errorf("expected unhealthy after %d failures, got %s", 3, rs.Health.Status)
	}
	if rs.Health.ConsecutiveFails != 3 {
		t.Errorf("expected 3 consecutive fails, got %d", rs.Health.ConsecutiveFails)
	}
	if rs.Health.LastError != "connection refused" {
		t.Errorf("expected last error to be set, got %q", rs.Health.LastError)
	}
}

func TestRecordResult_NotUnhealthyBeforeThreshold(t *testing.T) {
	c, _, state := newTestChecker()
	state.SetResource(&status.ResourceStatus{
		Name:  "db",
		State: status.StateRunning,
	})

	hc := &config.HealthCheckConfig{FailureThreshold: 3}
	err := fmt.Errorf("timeout")

	// Fail only 2 times (below threshold)
	c.recordResult("db", hc, err)
	c.recordResult("db", hc, err)

	rs, _ := state.GetResource("db")
	if rs.Health.Status == "unhealthy" {
		t.Error("should not be unhealthy before reaching threshold")
	}
}

func TestRecordResult_OnUnhealthyCallback(t *testing.T) {
	c, _, state := newTestChecker()
	state.SetResource(&status.ResourceStatus{
		Name:  "api",
		State: status.StateRunning,
	})

	var called atomic.Int32
	c.OnUnhealthy = func(name string) {
		called.Add(1)
		if name != "api" {
			t.Errorf("expected name api, got %s", name)
		}
	}

	hc := &config.HealthCheckConfig{
		FailureThreshold: 1,
		AutoRestart:      true,
	}

	c.recordResult("api", hc, fmt.Errorf("down"))

	if called.Load() != 1 {
		t.Errorf("expected OnUnhealthy called once, got %d", called.Load())
	}
}

func TestRecordResult_NoCallbackWithoutAutoRestart(t *testing.T) {
	c, _, state := newTestChecker()
	state.SetResource(&status.ResourceStatus{
		Name:  "api",
		State: status.StateRunning,
	})

	var called atomic.Int32
	c.OnUnhealthy = func(name string) {
		called.Add(1)
	}

	hc := &config.HealthCheckConfig{
		FailureThreshold: 1,
		AutoRestart:      false,
	}

	c.recordResult("api", hc, fmt.Errorf("down"))

	if called.Load() != 0 {
		t.Errorf("expected OnUnhealthy not called, got %d", called.Load())
	}
}

func TestRecordResult_RecoveryResetsCount(t *testing.T) {
	c, _, state := newTestChecker()
	state.SetResource(&status.ResourceStatus{
		Name:  "web",
		State: status.StateRunning,
	})

	hc := &config.HealthCheckConfig{FailureThreshold: 3}

	// Fail twice, then recover
	c.recordResult("web", hc, fmt.Errorf("fail"))
	c.recordResult("web", hc, fmt.Errorf("fail"))
	c.recordResult("web", hc, nil)

	rs, _ := state.GetResource("web")
	if rs.Health.ConsecutiveFails != 0 {
		t.Errorf("expected 0 consecutive fails after recovery, got %d", rs.Health.ConsecutiveFails)
	}
	if rs.Health.Status != "healthy" {
		t.Errorf("expected healthy after recovery, got %s", rs.Health.Status)
	}
	if rs.Health.LastError != "" {
		t.Errorf("expected empty last error, got %q", rs.Health.LastError)
	}
}

func TestRecordResult_MissingResource(t *testing.T) {
	c, _, _ := newTestChecker()
	// Should not panic for a resource that doesn't exist in state
	c.recordResult("nonexistent", &config.HealthCheckConfig{FailureThreshold: 1}, fmt.Errorf("fail"))
}

// --- Probe lifecycle ---

func TestStartProbe_NilHealthCheck(t *testing.T) {
	c, _, _ := newTestChecker()
	// Should not panic or create a probe for nil config
	c.StartProbe(context.Background(), "web", nil)
	c.mu.Lock()
	count := len(c.probes)
	c.mu.Unlock()
	if count != 0 {
		t.Errorf("expected 0 probes, got %d", count)
	}
}

func TestStartProbe_CancelsPrevious(t *testing.T) {
	c, _, _ := newTestChecker()

	ctx := context.Background()
	hc := &config.HealthCheckConfig{
		Type:         "command",
		Command:      []string{"true"},
		IntervalSecs: 3600, // long interval so it doesn't fire
	}

	c.StartProbe(ctx, "web", hc)
	time.Sleep(10 * time.Millisecond) // let goroutine start

	// Start another probe for same name — should cancel the first
	c.StartProbe(ctx, "web", hc)
	time.Sleep(10 * time.Millisecond)

	c.mu.Lock()
	count := len(c.probes)
	c.mu.Unlock()
	if count != 1 {
		t.Errorf("expected 1 probe after replacement, got %d", count)
	}
}

func TestStopProbe(t *testing.T) {
	c, _, _ := newTestChecker()

	hc := &config.HealthCheckConfig{
		Type:         "command",
		Command:      []string{"true"},
		IntervalSecs: 3600,
	}
	c.StartProbe(context.Background(), "web", hc)
	time.Sleep(10 * time.Millisecond)

	c.StopProbe("web")

	c.mu.Lock()
	count := len(c.probes)
	c.mu.Unlock()
	if count != 0 {
		t.Errorf("expected 0 probes after stop, got %d", count)
	}
}

func TestStopProbe_Nonexistent(t *testing.T) {
	c, _, _ := newTestChecker()
	// Should not panic
	c.StopProbe("nonexistent")
}

func TestStopAll(t *testing.T) {
	c, _, _ := newTestChecker()

	hc := &config.HealthCheckConfig{
		Type:         "command",
		Command:      []string{"true"},
		IntervalSecs: 3600,
	}
	c.StartProbe(context.Background(), "web", hc)
	c.StartProbe(context.Background(), "api", hc)
	time.Sleep(10 * time.Millisecond)

	c.StopAll()

	c.mu.Lock()
	count := len(c.probes)
	c.mu.Unlock()
	if count != 0 {
		t.Errorf("expected 0 probes after StopAll, got %d", count)
	}
}
