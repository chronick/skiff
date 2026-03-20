package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chronick/skiff/internal/config"
	"github.com/chronick/skiff/internal/health"
	"github.com/chronick/skiff/internal/logbuf"
	"github.com/chronick/skiff/internal/runner"
	"github.com/chronick/skiff/internal/runtime"
	"github.com/chronick/skiff/internal/scheduler"
	"github.com/chronick/skiff/internal/status"
	"github.com/chronick/skiff/internal/supervisor"
	"github.com/chronick/skiff/internal/testutil"
)

// newTestDaemon creates a Daemon wired with mocks for route testing.
func newTestDaemon(cfg *config.Config) (*Daemon, *testutil.MockContainerRuntime) {
	if cfg == nil {
		cfg = &config.Config{
			Version: 1,
			Paths: config.PathsConfig{
				Base:   "/tmp/skiff-test",
				Socket: "/tmp/skiff-test.sock",
				Logs:   "/tmp/skiff-test-logs",
			},
			Daemon: config.DaemonConfig{
				LogBufferLines:         100,
				StatusPollIntervalSecs: 5,
				ShutdownTimeoutSecs:    5,
			},
		}
	}

	logger := testutil.NewTestLogger()
	logs := logbuf.New(cfg.Daemon.LogBufferLines)
	state := status.NewSharedState()
	mockRT := testutil.NewMockRuntime()
	r := &runner.ExecRunner{}
	sup := supervisor.New(state, logs, cfg.Paths.Logs, logger)
	sched := scheduler.New(state, logs, "/tmp/skiff-test-sched.json", logger)
	hc := health.NewChecker(state, logs, r, logger)

	adhocTracker := NewAdhocTracker(state, mockRT, logger)

	d := &Daemon{
		cfg:        cfg,
		state:      state,
		logs:       logs,
		supervisor: sup,
		scheduler:  sched,
		health:     hc,
		runtime:    mockRT,
		adhoc:      adhocTracker,
		runner:     r,
		logger:     logger,
		logOffsets: make(map[string]int),
		prevStats:  make(map[string]prevStatsSample),
	}
	return d, mockRT
}

func doRequest(mux *http.ServeMux, method, path string, body interface{}) *httptest.ResponseRecorder {
	var reqBody *bytes.Buffer
	if body != nil {
		data, _ := json.Marshal(body)
		reqBody = bytes.NewBuffer(data)
	} else {
		reqBody = &bytes.Buffer{}
	}

	req := httptest.NewRequest(method, path, reqBody)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func decodeJSON(t *testing.T, rr *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var result map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	return result
}

// --- Health ---

func TestHandleHealth(t *testing.T) {
	d, _ := newTestDaemon(nil)
	mux := d.setupRoutes()

	rr := doRequest(mux, "GET", "/v1/health", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	body := decodeJSON(t, rr)
	if body["status"] != "ok" {
		t.Errorf("expected status ok, got %v", body["status"])
	}
}

// --- Status ---

func TestHandleStatus(t *testing.T) {
	d, _ := newTestDaemon(nil)
	d.state.SetResource(&status.ResourceStatus{
		Name:  "web",
		Type:  status.TypeService,
		State: status.StateRunning,
	})

	mux := d.setupRoutes()
	rr := doRequest(mux, "GET", "/v1/status", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	body := decodeJSON(t, rr)
	if body["resources"] == nil {
		t.Error("expected resources in snapshot")
	}
}

func TestHandleStatusByName_Found(t *testing.T) {
	d, _ := newTestDaemon(nil)
	d.state.SetResource(&status.ResourceStatus{
		Name:  "web",
		Type:  status.TypeService,
		State: status.StateRunning,
	})

	mux := d.setupRoutes()
	rr := doRequest(mux, "GET", "/v1/status/web", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandleStatusByName_Schedule(t *testing.T) {
	d, _ := newTestDaemon(nil)
	d.state.SetSchedule(&status.ScheduleStatus{
		Name:       "backup",
		LastResult: "success",
	})

	mux := d.setupRoutes()
	rr := doRequest(mux, "GET", "/v1/status/backup", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandleStatusByName_NotFound(t *testing.T) {
	d, _ := newTestDaemon(nil)
	mux := d.setupRoutes()
	rr := doRequest(mux, "GET", "/v1/status/nonexistent", nil)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

// --- Services / Containers ---

func TestHandleServices(t *testing.T) {
	d, _ := newTestDaemon(nil)
	d.state.SetResource(&status.ResourceStatus{Name: "web", Type: status.TypeService, State: status.StateRunning})
	d.state.SetResource(&status.ResourceStatus{Name: "db", Type: status.TypeContainer, State: status.StateRunning})

	mux := d.setupRoutes()
	rr := doRequest(mux, "GET", "/v1/services", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	var services []map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&services)
	if len(services) != 1 {
		t.Errorf("expected 1 service, got %d", len(services))
	}
}

func TestHandleContainers(t *testing.T) {
	d, _ := newTestDaemon(nil)
	d.state.SetResource(&status.ResourceStatus{Name: "web", Type: status.TypeService, State: status.StateRunning})
	d.state.SetResource(&status.ResourceStatus{Name: "db", Type: status.TypeContainer, State: status.StateRunning})

	mux := d.setupRoutes()
	rr := doRequest(mux, "GET", "/v1/containers", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	var containers []map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&containers)
	if len(containers) != 1 {
		t.Errorf("expected 1 container, got %d", len(containers))
	}
}

// --- Schedules ---

func TestHandleSchedules(t *testing.T) {
	d, _ := newTestDaemon(nil)
	d.state.SetSchedule(&status.ScheduleStatus{Name: "backup", LastResult: "success"})

	mux := d.setupRoutes()
	rr := doRequest(mux, "GET", "/v1/schedules", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandleScheduleByName_Found(t *testing.T) {
	d, _ := newTestDaemon(nil)
	d.state.SetSchedule(&status.ScheduleStatus{Name: "backup", LastResult: "success"})

	mux := d.setupRoutes()
	rr := doRequest(mux, "GET", "/v1/schedule/backup", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandleScheduleByName_NotFound(t *testing.T) {
	d, _ := newTestDaemon(nil)
	mux := d.setupRoutes()
	rr := doRequest(mux, "GET", "/v1/schedule/nonexistent", nil)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

// --- Up ---

func TestHandleUp_Container(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Paths:   config.PathsConfig{Base: "/tmp/test", Logs: "/tmp/test"},
		Daemon:  config.DaemonConfig{LogBufferLines: 100, StatusPollIntervalSecs: 5, ShutdownTimeoutSecs: 5},
		Containers: map[string]config.ContainerConfig{
			"db": {Image: "postgres:15", Ports: []string{"5432:5432"}},
		},
	}
	d, mockRT := newTestDaemon(cfg)
	mux := d.setupRoutes()

	rr := doRequest(mux, "POST", "/v1/up", map[string]interface{}{
		"names": []string{"db"},
	})

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	body := decodeJSON(t, rr)
	started, _ := body["started"].([]interface{})
	if len(started) != 1 || started[0] != "db" {
		t.Errorf("expected [db] started, got %v", started)
	}

	// Verify runtime.Run was called
	calls := mockRT.CallsFor("Run")
	if len(calls) != 1 {
		t.Errorf("expected 1 Run call, got %d", len(calls))
	}

	// Verify state was updated
	rs, ok := d.state.GetResource("db")
	if !ok {
		t.Fatal("expected db resource in state")
	}
	if rs.State != status.StateRunning {
		t.Errorf("expected running, got %s", rs.State)
	}
}

func TestHandleUp_SkipsUnchanged(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Paths:   config.PathsConfig{Base: "/tmp/test", Logs: "/tmp/test"},
		Daemon:  config.DaemonConfig{LogBufferLines: 100, StatusPollIntervalSecs: 5, ShutdownTimeoutSecs: 5},
		Containers: map[string]config.ContainerConfig{
			"db": {Image: "postgres:15"},
		},
	}
	d, mockRT := newTestDaemon(cfg)

	// Pre-set the resource as running with matching config hash
	d.state.SetResource(&status.ResourceStatus{
		Name:       "db",
		Type:       status.TypeContainer,
		State:      status.StateRunning,
		ConfigHash: config.Hash(cfg.Containers["db"]),
	})

	mux := d.setupRoutes()
	rr := doRequest(mux, "POST", "/v1/up", map[string]interface{}{
		"names": []string{"db"},
	})

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	// Runtime.Run should NOT be called (unchanged)
	calls := mockRT.CallsFor("Run")
	if len(calls) != 0 {
		t.Errorf("expected 0 Run calls for unchanged resource, got %d", len(calls))
	}
}

func TestHandleUp_NotFound(t *testing.T) {
	d, _ := newTestDaemon(nil)
	mux := d.setupRoutes()

	rr := doRequest(mux, "POST", "/v1/up", map[string]interface{}{
		"names": []string{"nonexistent"},
	})

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 (with errors field), got %d", rr.Code)
	}
	body := decodeJSON(t, rr)
	errors, _ := body["errors"].(map[string]interface{})
	if errors["nonexistent"] == nil {
		t.Error("expected error for nonexistent resource")
	}
}

// --- Down ---

func TestHandleDown_Container(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Paths:   config.PathsConfig{Base: "/tmp/test", Logs: "/tmp/test"},
		Daemon:  config.DaemonConfig{LogBufferLines: 100, StatusPollIntervalSecs: 5, ShutdownTimeoutSecs: 5},
		Containers: map[string]config.ContainerConfig{
			"db": {Image: "postgres:15"},
		},
	}
	d, mockRT := newTestDaemon(cfg)
	mux := d.setupRoutes()

	rr := doRequest(mux, "POST", "/v1/down", map[string]interface{}{
		"names": []string{"db"},
	})

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	// Verify runtime.Stop was called
	calls := mockRT.CallsFor("Stop")
	if len(calls) != 1 {
		t.Errorf("expected 1 Stop call, got %d", len(calls))
	}
}

// --- Apply ---

func TestHandleApply_DryRun(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Paths:   config.PathsConfig{Base: "/tmp/test", Logs: "/tmp/test"},
		Daemon:  config.DaemonConfig{LogBufferLines: 100, StatusPollIntervalSecs: 5, ShutdownTimeoutSecs: 5},
		Services: map[string]config.ServiceConfig{
			"web": {Command: []string{"echo", "hi"}, RestartPolicy: "always"},
		},
		Containers: map[string]config.ContainerConfig{
			"db": {Image: "postgres:15"},
		},
	}
	d, mockRT := newTestDaemon(cfg)
	mux := d.setupRoutes()

	rr := doRequest(mux, "POST", "/v1/apply?dry_run=true", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	body := decodeJSON(t, rr)
	actions, _ := body["actions"].([]interface{})
	if len(actions) != 2 {
		t.Errorf("expected 2 actions, got %d", len(actions))
	}

	// Verify no actual calls were made
	if mockRT.CallCount() != 0 {
		t.Errorf("expected no runtime calls in dry run, got %d", mockRT.CallCount())
	}
}

func TestHandleApply_DetectsConfigChange(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Paths:   config.PathsConfig{Base: "/tmp/test", Logs: "/tmp/test"},
		Daemon:  config.DaemonConfig{LogBufferLines: 100, StatusPollIntervalSecs: 5, ShutdownTimeoutSecs: 5},
		Containers: map[string]config.ContainerConfig{
			"db": {Image: "postgres:16"}, // changed
		},
	}
	d, _ := newTestDaemon(cfg)

	// Set old hash
	d.state.SetResource(&status.ResourceStatus{
		Name:       "db",
		Type:       status.TypeContainer,
		State:      status.StateRunning,
		ConfigHash: "old-hash",
	})

	mux := d.setupRoutes()
	rr := doRequest(mux, "POST", "/v1/apply?dry_run=true", nil)

	body := decodeJSON(t, rr)
	actions, _ := body["actions"].([]interface{})
	found := false
	for _, a := range actions {
		action := a.(map[string]interface{})
		if action["resource"] == "db" && action["action"] == "restart" {
			found = true
		}
	}
	if !found {
		t.Error("expected restart action for changed config")
	}
}

// --- Restart ---

func TestHandleRestart_Container(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Paths:   config.PathsConfig{Base: "/tmp/test", Logs: "/tmp/test"},
		Daemon:  config.DaemonConfig{LogBufferLines: 100, StatusPollIntervalSecs: 5, ShutdownTimeoutSecs: 5},
		Containers: map[string]config.ContainerConfig{
			"db": {Image: "postgres:15"},
		},
	}
	d, mockRT := newTestDaemon(cfg)
	mux := d.setupRoutes()

	rr := doRequest(mux, "POST", "/v1/restart/db", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	stops := mockRT.CallsFor("Stop")
	runs := mockRT.CallsFor("Run")
	if len(stops) != 1 {
		t.Errorf("expected 1 Stop call, got %d", len(stops))
	}
	if len(runs) != 1 {
		t.Errorf("expected 1 Run call, got %d", len(runs))
	}
}

func TestHandleRestart_NotFound(t *testing.T) {
	d, _ := newTestDaemon(nil)
	mux := d.setupRoutes()
	rr := doRequest(mux, "POST", "/v1/restart/nonexistent", nil)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

// --- Exec ---

func TestHandleExec(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Paths:   config.PathsConfig{Base: "/tmp/test", Logs: "/tmp/test"},
		Daemon:  config.DaemonConfig{LogBufferLines: 100, StatusPollIntervalSecs: 5, ShutdownTimeoutSecs: 5},
		Containers: map[string]config.ContainerConfig{
			"db": {Image: "postgres:15"},
		},
	}
	d, mockRT := newTestDaemon(cfg)
	mockRT.ExecOutput = []byte("hello world")
	mux := d.setupRoutes()

	rr := doRequest(mux, "POST", "/v1/exec/db", map[string]interface{}{
		"command": []string{"echo", "hello"},
	})

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	body := decodeJSON(t, rr)
	if body["output"] != "hello world" {
		t.Errorf("expected output 'hello world', got %v", body["output"])
	}
}

func TestHandleExec_NotFound(t *testing.T) {
	d, _ := newTestDaemon(nil)
	mux := d.setupRoutes()
	rr := doRequest(mux, "POST", "/v1/exec/nonexistent", map[string]interface{}{
		"command": []string{"echo"},
	})

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

// --- Build ---

func TestHandleBuild(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Paths:   config.PathsConfig{Base: "/tmp/test", Logs: "/tmp/test"},
		Daemon:  config.DaemonConfig{LogBufferLines: 100, StatusPollIntervalSecs: 5, ShutdownTimeoutSecs: 5},
		Containers: map[string]config.ContainerConfig{
			"app": {Dockerfile: "Dockerfile", Context: "."},
		},
	}
	d, mockRT := newTestDaemon(cfg)
	mux := d.setupRoutes()

	rr := doRequest(mux, "POST", "/v1/build", map[string]interface{}{
		"names": []string{"app"},
	})

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	builds := mockRT.CallsFor("Build")
	if len(builds) != 1 {
		t.Errorf("expected 1 Build call, got %d", len(builds))
	}
}

func TestHandleBuild_NoDockerfile(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Paths:   config.PathsConfig{Base: "/tmp/test", Logs: "/tmp/test"},
		Daemon:  config.DaemonConfig{LogBufferLines: 100, StatusPollIntervalSecs: 5, ShutdownTimeoutSecs: 5},
		Containers: map[string]config.ContainerConfig{
			"db": {Image: "postgres:15"},
		},
	}
	d, _ := newTestDaemon(cfg)
	mux := d.setupRoutes()

	rr := doRequest(mux, "POST", "/v1/build", map[string]interface{}{
		"names": []string{"db"},
	})

	body := decodeJSON(t, rr)
	errors, _ := body["errors"].(map[string]interface{})
	if errors["db"] == nil {
		t.Error("expected error for container without dockerfile")
	}
}

// --- Logs ---

func TestHandleLogs(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Paths:   config.PathsConfig{Base: "/tmp/test", Logs: "/tmp/test"},
		Daemon:  config.DaemonConfig{LogBufferLines: 100, StatusPollIntervalSecs: 5, ShutdownTimeoutSecs: 5},
		Services: map[string]config.ServiceConfig{
			"web": {Command: []string{"echo"}},
		},
	}
	d, _ := newTestDaemon(cfg)
	d.logs.Append("web", "INFO server started")
	d.logs.Append("web", "ERROR something broke")

	mux := d.setupRoutes()
	rr := doRequest(mux, "GET", "/v1/logs/web?lines=10", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandleLogs_ContainerSource(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Paths:   config.PathsConfig{Base: "/tmp/test", Logs: "/tmp/test"},
		Daemon:  config.DaemonConfig{LogBufferLines: 100, StatusPollIntervalSecs: 5, ShutdownTimeoutSecs: 5},
		Containers: map[string]config.ContainerConfig{
			"db": {Image: "postgres:15"},
		},
	}
	d, mockRT := newTestDaemon(cfg)
	mockRT.LogsOutput = []byte("container log line")

	mux := d.setupRoutes()
	rr := doRequest(mux, "GET", "/v1/logs/db?source=container&lines=50", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if rr.Header().Get("Content-Type") != "text/plain" {
		t.Errorf("expected text/plain for container logs, got %s", rr.Header().Get("Content-Type"))
	}

	logsCalls := mockRT.CallsFor("Logs")
	if len(logsCalls) != 1 {
		t.Errorf("expected 1 Logs call, got %d", len(logsCalls))
	}
}

func TestHandleLogs_NotFound(t *testing.T) {
	d, _ := newTestDaemon(nil)
	mux := d.setupRoutes()
	rr := doRequest(mux, "GET", "/v1/logs/nonexistent", nil)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

// --- Stats ---

func TestHandleStats(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Paths:   config.PathsConfig{Base: "/tmp/test", Logs: "/tmp/test"},
		Daemon:  config.DaemonConfig{LogBufferLines: 100, StatusPollIntervalSecs: 5, ShutdownTimeoutSecs: 5},
		Containers: map[string]config.ContainerConfig{
			"db": {Image: "postgres:15"},
		},
	}
	d, _ := newTestDaemon(cfg)
	d.state.SetResource(&status.ResourceStatus{
		Name:      "db",
		Type:      status.TypeContainer,
		State:     status.StateRunning,
		StartedAt: time.Now(),
	})

	mux := d.setupRoutes()
	rr := doRequest(mux, "GET", "/v1/stats", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestHandleStatsByName_NotFound(t *testing.T) {
	d, _ := newTestDaemon(nil)
	mux := d.setupRoutes()
	rr := doRequest(mux, "GET", "/v1/stats/nonexistent", nil)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

// --- RunNow ---

func TestHandleRunNow_NotFound(t *testing.T) {
	d, _ := newTestDaemon(nil)
	mux := d.setupRoutes()
	rr := doRequest(mux, "POST", "/v1/schedule/nonexistent/run-now", nil)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

// --- Auth middleware ---

func TestAuthMiddleware_ValidToken(t *testing.T) {
	d, _ := newTestDaemon(nil)
	d.cfg.Daemon.AuthToken = "secret123"

	handler := d.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	d, _ := newTestDaemon(nil)
	d.cfg.Daemon.AuthToken = "secret123"

	handler := d.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestAuthMiddleware_MissingToken(t *testing.T) {
	d, _ := newTestDaemon(nil)
	d.cfg.Daemon.AuthToken = "secret123"

	handler := d.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

// --- writeJSON ---

// --- Run (POST /v1/run) ---

func TestHandleRun_Success(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Paths:   config.PathsConfig{Base: "/tmp/test", Logs: "/tmp/test"},
		Daemon:  config.DaemonConfig{LogBufferLines: 100, StatusPollIntervalSecs: 5, ShutdownTimeoutSecs: 5},
		Containers: map[string]config.ContainerConfig{
			"worker": {Image: "worker:latest"},
		},
	}
	d, mockRT := newTestDaemon(cfg)
	mux := d.setupRoutes()

	rr := doRequest(mux, "POST", "/v1/run", map[string]interface{}{
		"name": "worker",
	})

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	body := decodeJSON(t, rr)
	if body["status"] != "started" {
		t.Errorf("expected status started, got %v", body["status"])
	}

	runs := mockRT.CallsFor("Run")
	if len(runs) != 1 {
		t.Errorf("expected 1 Run call, got %d", len(runs))
	}
}

func TestHandleRun_NotFound(t *testing.T) {
	d, _ := newTestDaemon(nil)
	mux := d.setupRoutes()

	rr := doRequest(mux, "POST", "/v1/run", map[string]interface{}{
		"name": "nonexistent",
	})

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestHandleRun_BadBody(t *testing.T) {
	d, _ := newTestDaemon(nil)
	mux := d.setupRoutes()

	req := httptest.NewRequest("POST", "/v1/run", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

// --- StatsByName ---

func TestHandleStatsByName_WithStats(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Paths:   config.PathsConfig{Base: "/tmp/test", Logs: "/tmp/test"},
		Daemon:  config.DaemonConfig{LogBufferLines: 100, StatusPollIntervalSecs: 5, ShutdownTimeoutSecs: 5},
		Containers: map[string]config.ContainerConfig{
			"db": {Image: "postgres:15"},
		},
	}
	d, _ := newTestDaemon(cfg)
	d.state.SetResource(&status.ResourceStatus{
		Name:  "db",
		Type:  status.TypeContainer,
		State: status.StateRunning,
	})
	d.state.UpdateStats("db", &runtime.ContainerStats{
		CPUPercent: 25.5,
		MemUsageMB: 512,
		MemLimitMB: 1024,
		PIDs:       10,
	})

	mux := d.setupRoutes()
	rr := doRequest(mux, "GET", "/v1/stats/db", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	body := decodeJSON(t, rr)
	if body["cpu_percent"] != 25.5 {
		t.Errorf("expected cpu_percent 25.5, got %v", body["cpu_percent"])
	}
}

func TestHandleStatsByName_NoStatsYet(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Paths:   config.PathsConfig{Base: "/tmp/test", Logs: "/tmp/test"},
		Daemon:  config.DaemonConfig{LogBufferLines: 100, StatusPollIntervalSecs: 5, ShutdownTimeoutSecs: 5},
		Containers: map[string]config.ContainerConfig{
			"db": {Image: "postgres:15"},
		},
	}
	d, _ := newTestDaemon(cfg)
	d.state.SetResource(&status.ResourceStatus{
		Name:  "db",
		Type:  status.TypeContainer,
		State: status.StateRunning,
	})

	mux := d.setupRoutes()
	rr := doRequest(mux, "GET", "/v1/stats/db", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	body := decodeJSON(t, rr)
	if body["status"] != "no stats available yet" {
		t.Errorf("expected 'no stats available yet', got %v", body["status"])
	}
}

func TestHandleStatsByName_InConfigNotRunning(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Paths:   config.PathsConfig{Base: "/tmp/test", Logs: "/tmp/test"},
		Daemon:  config.DaemonConfig{LogBufferLines: 100, StatusPollIntervalSecs: 5, ShutdownTimeoutSecs: 5},
		Containers: map[string]config.ContainerConfig{
			"db": {Image: "postgres:15"},
		},
	}
	d, _ := newTestDaemon(cfg)
	mux := d.setupRoutes()

	rr := doRequest(mux, "GET", "/v1/stats/db", nil)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for not-running container, got %d", rr.Code)
	}
}

// --- Apply (execute path) ---

func TestHandleApply_Execute_StartNew(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Paths:   config.PathsConfig{Base: "/tmp/test", Logs: "/tmp/test"},
		Daemon:  config.DaemonConfig{LogBufferLines: 100, StatusPollIntervalSecs: 5, ShutdownTimeoutSecs: 5},
		Containers: map[string]config.ContainerConfig{
			"db": {Image: "postgres:15"},
		},
	}
	d, mockRT := newTestDaemon(cfg)
	mux := d.setupRoutes()

	rr := doRequest(mux, "POST", "/v1/apply", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	// Should have called Run for the new container
	runs := mockRT.CallsFor("Run")
	if len(runs) != 1 {
		t.Errorf("expected 1 Run call for new container, got %d", len(runs))
	}

	// State should show running
	rs, ok := d.state.GetResource("db")
	if !ok {
		t.Fatal("expected db resource in state")
	}
	if rs.State != status.StateRunning {
		t.Errorf("expected running, got %s", rs.State)
	}
}

func TestHandleApply_Execute_RestartChanged(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Paths:   config.PathsConfig{Base: "/tmp/test", Logs: "/tmp/test"},
		Daemon:  config.DaemonConfig{LogBufferLines: 100, StatusPollIntervalSecs: 5, ShutdownTimeoutSecs: 5},
		Containers: map[string]config.ContainerConfig{
			"db": {Image: "postgres:16"}, // changed image
		},
	}
	d, mockRT := newTestDaemon(cfg)

	// Pre-set as running with old config hash
	d.state.SetResource(&status.ResourceStatus{
		Name:       "db",
		Type:       status.TypeContainer,
		State:      status.StateRunning,
		ConfigHash: "old-hash",
	})

	mux := d.setupRoutes()
	rr := doRequest(mux, "POST", "/v1/apply", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	// Should have stopped then re-run
	stops := mockRT.CallsFor("Stop")
	runs := mockRT.CallsFor("Run")
	if len(stops) != 1 {
		t.Errorf("expected 1 Stop call for restart, got %d", len(stops))
	}
	if len(runs) != 1 {
		t.Errorf("expected 1 Run call for restart, got %d", len(runs))
	}
}

func TestHandleApply_Execute_StopRemoved(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Paths:   config.PathsConfig{Base: "/tmp/test", Logs: "/tmp/test"},
		Daemon:  config.DaemonConfig{LogBufferLines: 100, StatusPollIntervalSecs: 5, ShutdownTimeoutSecs: 5},
		// No containers in config
	}
	d, mockRT := newTestDaemon(cfg)

	// Pre-set a container that's no longer in config
	d.state.SetResource(&status.ResourceStatus{
		Name:  "old-db",
		Type:  status.TypeContainer,
		State: status.StateRunning,
	})

	mux := d.setupRoutes()
	rr := doRequest(mux, "POST", "/v1/apply", nil)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}

	// Should have stopped the removed container
	stops := mockRT.CallsFor("Stop")
	if len(stops) != 1 {
		t.Errorf("expected 1 Stop call for removed container, got %d", len(stops))
	}
}

// --- Up (service path) ---

func TestHandleUp_Service(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Paths:   config.PathsConfig{Base: "/tmp/test", Logs: "/tmp/test"},
		Daemon:  config.DaemonConfig{LogBufferLines: 100, StatusPollIntervalSecs: 5, ShutdownTimeoutSecs: 5},
		Services: map[string]config.ServiceConfig{
			"web": {Command: []string{"echo", "hi"}, RestartPolicy: "always"},
		},
	}
	d, _ := newTestDaemon(cfg)
	mux := d.setupRoutes()

	rr := doRequest(mux, "POST", "/v1/up", map[string]interface{}{
		"names": []string{"web"},
	})

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	body := decodeJSON(t, rr)
	started, _ := body["started"].([]interface{})
	if len(started) != 1 || started[0] != "web" {
		t.Errorf("expected [web] started, got %v", started)
	}
}

// --- Down (service path) ---

func TestHandleDown_Service(t *testing.T) {
	cfg := &config.Config{
		Version: 1,
		Paths:   config.PathsConfig{Base: "/tmp/test", Logs: "/tmp/test"},
		Daemon:  config.DaemonConfig{LogBufferLines: 100, StatusPollIntervalSecs: 5, ShutdownTimeoutSecs: 5},
		Services: map[string]config.ServiceConfig{
			"web": {Command: []string{"echo"}, RestartPolicy: "always"},
		},
	}
	d, _ := newTestDaemon(cfg)
	mux := d.setupRoutes()

	rr := doRequest(mux, "POST", "/v1/down", map[string]interface{}{
		"names": []string{"web"},
	})

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	body := decodeJSON(t, rr)
	stopped, _ := body["stopped"].([]interface{})
	if len(stopped) != 1 {
		t.Errorf("expected [web] stopped, got %v", stopped)
	}
}

// --- writeJSON ---

func TestWriteJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	writeJSON(rr, http.StatusCreated, map[string]string{"hello": "world"})

	if rr.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %s", ct)
	}

	body := decodeJSON(t, rr)
	if body["hello"] != "world" {
		t.Errorf("expected hello=world, got %v", body)
	}
}
