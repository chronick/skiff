package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/chronick/skiff/internal/runtime"
	"github.com/chronick/skiff/internal/testutil"
)

func newTestRuntime() (*runtime.AppleRuntime, *testutil.MockProcessRunner) {
	runner := testutil.NewMockRunner()
	logger := testutil.NewTestLogger()
	rt := runtime.NewAppleRuntime(runner, logger)
	return rt, runner
}

// --- Run ---

func TestRun_BasicFlags(t *testing.T) {
	rt, runner := newTestRuntime()
	err := rt.Run(context.Background(), "web", runtime.ContainerConfig{Image: "nginx:latest"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if runner.CallCount() != 2 {
		t.Fatalf("expected 2 calls (rm + run), got %d", runner.CallCount())
	}
	// First call: preemptive rm
	rmCall := runner.Calls[0]
	if rmCall.Args[0] != "rm" {
		t.Errorf("expected rm as first call, got: %v", rmCall.Args)
	}
	// Second call: run
	call, _ := runner.LastCall()
	if call.Name != "container" {
		t.Errorf("expected binary 'container', got %q", call.Name)
	}

	args := strings.Join(call.Args, " ")
	if !strings.Contains(args, "run --name web -d") {
		t.Errorf("expected 'run --name web -d' in args, got: %s", args)
	}
	if !strings.HasSuffix(args, "nginx:latest") {
		t.Errorf("expected args to end with image, got: %s", args)
	}
}

func TestRun_AllOptions(t *testing.T) {
	rt, runner := newTestRuntime()
	cfg := runtime.ContainerConfig{
		Image:    "myapp:v1",
		Volumes:  []string{"/data:/data"},
		Ports:    []string{"8080:80"},
		Env:      map[string]string{"NODE_ENV": "production"},
		CPUs:     2,
		Memory:   "512m",
		Init:     true,
		ReadOnly: true,
		Network:  "mynet",
		Labels:   map[string]string{"app": "test"},
	}
	err := rt.Run(context.Background(), "app", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	call, _ := runner.LastCall()
	args := strings.Join(call.Args, " ")

	checks := []string{
		"-v /data:/data",
		"-p 8080:80",
		"-e NODE_ENV=production",
		"--cpus 2",
		"--memory 512m",
		"--init",
		"--read-only",
		"--network mynet",
		"--label app=test",
	}
	for _, check := range checks {
		if !strings.Contains(args, check) {
			t.Errorf("expected %q in args, got: %s", check, args)
		}
	}
}

func TestRun_SystemLabels(t *testing.T) {
	rt, runner := newTestRuntime()
	err := rt.Run(context.Background(), "db", runtime.ContainerConfig{Image: "postgres:15"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	call, _ := runner.LastCall()
	args := strings.Join(call.Args, " ")

	if !strings.Contains(args, "--label skiff.managed=true") {
		t.Errorf("expected skiff.managed label, got: %s", args)
	}
	if !strings.Contains(args, "--label skiff.resource=db") {
		t.Errorf("expected skiff.resource label, got: %s", args)
	}
}

func TestRun_LabelFiltering(t *testing.T) {
	rt, runner := newTestRuntime()
	cfg := runtime.ContainerConfig{
		Image: "test:v1",
		Labels: map[string]string{
			"app":           "myapp",
			"skiff.evil":    "shouldbe-excluded",
			"skiff.managed": "false", // should not override system label
		},
	}
	err := rt.Run(context.Background(), "test", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	call, _ := runner.LastCall()
	args := strings.Join(call.Args, " ")

	if !strings.Contains(args, "--label app=myapp") {
		t.Errorf("expected user label app=myapp, got: %s", args)
	}
	if strings.Contains(args, "skiff.evil") {
		t.Errorf("expected skiff.evil to be filtered, got: %s", args)
	}
	if !strings.Contains(args, "skiff.managed=true") {
		t.Errorf("expected skiff.managed=true (system), got: %s", args)
	}
}

func TestRun_DeterministicLabels(t *testing.T) {
	rt, runner := newTestRuntime()
	cfg := runtime.ContainerConfig{
		Image:  "test:v1",
		Labels: map[string]string{"zzz": "last", "aaa": "first"},
	}
	rt.Run(context.Background(), "test", cfg)

	call, _ := runner.LastCall()
	var labelArgs []string
	for i, a := range call.Args {
		if a == "--label" && i+1 < len(call.Args) {
			labelArgs = append(labelArgs, call.Args[i+1])
		}
	}

	for i := 1; i < len(labelArgs); i++ {
		if labelArgs[i] < labelArgs[i-1] {
			t.Errorf("labels not sorted: %v", labelArgs)
			break
		}
	}
}

func TestRun_Error(t *testing.T) {
	rt, runner := newTestRuntime()
	runner.DefaultResult = testutil.MockResult{
		Output: []byte("image not found"),
		Err:    fmt.Errorf("exit status 1"),
	}

	err := rt.Run(context.Background(), "bad", runtime.ContainerConfig{Image: "missing:v1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "image not found") {
		t.Errorf("expected output in error, got: %v", err)
	}
}

func TestRun_DNSFlag(t *testing.T) {
	rt, runner := newTestRuntime()
	rt.InjectDNS(runtime.ContainerConfig{}, "192.168.1.1", 15353)

	rt.Run(context.Background(), "web", runtime.ContainerConfig{Image: "nginx"})
	call, _ := runner.LastCall()
	args := strings.Join(call.Args, " ")
	if !strings.Contains(args, "--dns 192.168.1.1") {
		t.Errorf("expected --dns flag, got: %s", args)
	}
}

// --- Stop ---

func TestStop_Success(t *testing.T) {
	rt, runner := newTestRuntime()
	err := rt.Stop(context.Background(), "web")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if runner.CallCount() != 2 {
		t.Fatalf("expected 2 calls (stop + rm -f), got %d", runner.CallCount())
	}
	stopCall := runner.Calls[0]
	if stopCall.Args[0] != "stop" {
		t.Errorf("expected stop as first call, got: %v", stopCall.Args)
	}
	rmCall := runner.Calls[1]
	if rmCall.Args[0] != "rm" || rmCall.Args[1] != "-f" {
		t.Errorf("expected 'rm -f' as second call, got: %v", rmCall.Args)
	}
}

func TestStop_StopFailsStillRemoves(t *testing.T) {
	rt, runner := newTestRuntime()
	runner.Results["container stop web"] = testutil.MockResult{Err: fmt.Errorf("not running")}

	err := rt.Stop(context.Background(), "web")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if runner.CallCount() != 2 {
		t.Fatalf("expected 2 calls (stop + rm -f), got %d", runner.CallCount())
	}
	call, _ := runner.LastCall()
	if call.Args[0] != "rm" {
		t.Errorf("expected rm as second call even when stop fails, got: %v", call.Args)
	}
}

func TestStop_RmNotFoundIsSuccess(t *testing.T) {
	rt, runner := newTestRuntime()
	runner.Results["container rm -f web"] = testutil.MockResult{
		Output: []byte("container not found"),
		Err:    fmt.Errorf("exit status 1"),
	}

	err := rt.Stop(context.Background(), "web")
	if err != nil {
		t.Fatalf("expected idempotent success for not found, got: %v", err)
	}
}

func TestStop_RmFailsReturnsError(t *testing.T) {
	rt, runner := newTestRuntime()
	runner.Results["container rm -f web"] = testutil.MockResult{
		Output: []byte("permission denied"),
		Err:    fmt.Errorf("exit status 1"),
	}

	err := rt.Stop(context.Background(), "web")
	if err == nil {
		t.Fatal("expected error when rm fails with non-not-found error")
	}
}

// --- Build ---

func TestBuild_WithDockerfile(t *testing.T) {
	rt, runner := newTestRuntime()
	cfg := runtime.ContainerConfig{Dockerfile: "Dockerfile.prod", Context: "/app"}
	err := rt.Build(context.Background(), "web", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	call, _ := runner.LastCall()
	args := strings.Join(call.Args, " ")
	if !strings.Contains(args, "-f Dockerfile.prod") {
		t.Errorf("expected -f flag, got: %s", args)
	}
	if !strings.Contains(args, "/app") {
		t.Errorf("expected context /app, got: %s", args)
	}
}

func TestBuild_DefaultContext(t *testing.T) {
	rt, runner := newTestRuntime()
	cfg := runtime.ContainerConfig{}
	rt.Build(context.Background(), "web", cfg)

	call, _ := runner.LastCall()
	if call.Args[len(call.Args)-1] != "." {
		t.Errorf("expected default context '.', got: %v", call.Args)
	}
}

// --- Exec ---

func TestExec(t *testing.T) {
	rt, runner := newTestRuntime()
	runner.DefaultResult = testutil.MockResult{Output: []byte("hello")}

	out, err := rt.Exec(context.Background(), "web", []string{"echo", "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "hello" {
		t.Errorf("expected 'hello', got %q", string(out))
	}

	call, _ := runner.LastCall()
	if call.Args[0] != "exec" || call.Args[1] != "web" {
		t.Errorf("expected 'exec web ...', got: %v", call.Args)
	}
}

// --- List ---

func TestList_ParsesJSON(t *testing.T) {
	rt, runner := newTestRuntime()

	// Mirror the unexported listEntry structure
	type imageRef struct {
		Reference string `json:"reference"`
	}
	type configBlock struct {
		ID    string   `json:"id"`
		Image imageRef `json:"image"`
	}
	type entry struct {
		Configuration configBlock `json:"configuration"`
		Status        string      `json:"status"`
	}

	entries := []entry{
		{Configuration: configBlock{ID: "web", Image: imageRef{Reference: "nginx:latest"}}, Status: "running"},
		{Configuration: configBlock{ID: "db", Image: imageRef{Reference: "postgres:15"}}, Status: "stopped"},
	}

	data, _ := json.Marshal(entries)
	runner.Results["container list --all --format json"] = testutil.MockResult{Output: data}

	containers, err := rt.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(containers))
	}
	if containers[0].Name != "web" || containers[0].State != "running" {
		t.Errorf("unexpected container: %+v", containers[0])
	}
}

// --- Inspect ---

func TestInspect_ParsesJSON(t *testing.T) {
	rt, runner := newTestRuntime()

	type imageRef struct {
		Reference string `json:"reference"`
	}
	type configBlock struct {
		ID    string   `json:"id"`
		Image imageRef `json:"image"`
	}
	type entry struct {
		Configuration configBlock `json:"configuration"`
		Status        string      `json:"status"`
	}

	e := entry{
		Configuration: configBlock{ID: "web", Image: imageRef{Reference: "nginx:latest"}},
		Status:        "running",
	}
	data, _ := json.Marshal(e)
	runner.Results["container inspect web --format json"] = testutil.MockResult{Output: data}

	info, err := rt.Inspect(context.Background(), "web")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Name != "web" || info.State != "running" {
		t.Errorf("unexpected info: %+v", info)
	}
}

// --- Stats ---

func TestStats_ParsesJSON(t *testing.T) {
	rt, runner := newTestRuntime()

	type statsJSON struct {
		ID               string `json:"id"`
		CPUUsageUsec     int64  `json:"cpuUsageUsec"`
		MemoryUsageBytes int64  `json:"memoryUsageBytes"`
		MemoryLimitBytes int64  `json:"memoryLimitBytes"`
		NumProcesses     int    `json:"numProcesses"`
	}

	entries := []statsJSON{{
		ID:               "web",
		CPUUsageUsec:     123456,
		MemoryUsageBytes: 512 * 1024 * 1024,
		MemoryLimitBytes: 1024 * 1024 * 1024,
		NumProcesses:     5,
	}}
	data, _ := json.Marshal(entries)
	runner.Results["container stats web --format json --no-stream"] = testutil.MockResult{Output: data}

	stats, err := rt.Stats(context.Background(), "web")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.MemUsageMB != 512 {
		t.Errorf("expected 512 MB, got %d", stats.MemUsageMB)
	}
	if stats.MemLimitMB != 1024 {
		t.Errorf("expected 1024 MB limit, got %d", stats.MemLimitMB)
	}
	if stats.PIDs != 5 {
		t.Errorf("expected 5 PIDs, got %d", stats.PIDs)
	}
}

func TestStats_EmptyResponse(t *testing.T) {
	rt, runner := newTestRuntime()
	runner.Results["container stats web --format json --no-stream"] = testutil.MockResult{Output: []byte("[]")}

	_, err := rt.Stats(context.Background(), "web")
	if err == nil {
		t.Fatal("expected error for empty stats")
	}
}

// --- Logs ---

func TestLogs_WithLineCount(t *testing.T) {
	rt, runner := newTestRuntime()
	runner.Results["container logs -n 50 web"] = testutil.MockResult{Output: []byte("line1\nline2\n")}

	out, err := rt.Logs(context.Background(), "web", 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), "line1") {
		t.Errorf("expected logs output, got: %s", string(out))
	}
}

func TestLogs_ZeroLines(t *testing.T) {
	rt, runner := newTestRuntime()
	runner.Results["container logs web"] = testutil.MockResult{Output: []byte("all logs")}

	out, err := rt.Logs(context.Background(), "web", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "all logs" {
		t.Errorf("expected 'all logs', got %q", string(out))
	}
}

// --- InjectDNS ---

func TestInjectDNS(t *testing.T) {
	rt, _ := newTestRuntime()
	cfg := runtime.ContainerConfig{Image: "test"}
	result := rt.InjectDNS(cfg, "10.0.0.1", 15353)

	if result.Image != "test" {
		t.Errorf("expected config to be preserved")
	}
	// Verify DNS is applied by running a container and checking args
}

func TestInjectDNS_AppliedOnRun(t *testing.T) {
	rt, runner := newTestRuntime()
	rt.InjectDNS(runtime.ContainerConfig{}, "10.0.0.1", 15353)
	rt.Run(context.Background(), "web", runtime.ContainerConfig{Image: "nginx"})

	call, _ := runner.LastCall()
	args := strings.Join(call.Args, " ")
	if !strings.Contains(args, "--dns 10.0.0.1") {
		t.Errorf("expected --dns flag after InjectDNS, got: %s", args)
	}
}

// --- SetLimits ---

func TestSetLimits(t *testing.T) {
	rt, _ := newTestRuntime()
	cfg := runtime.ContainerConfig{Image: "test"}
	result := rt.SetLimits(cfg, runtime.ResourceLimits{CPUs: 2.5, Memory: "1g"})

	if result.CPUs != 2.5 {
		t.Errorf("expected CPUs 2.5, got %f", result.CPUs)
	}
	if result.Memory != "1g" {
		t.Errorf("expected Memory '1g', got %q", result.Memory)
	}
}

func TestSetLimits_PartialUpdate(t *testing.T) {
	rt, _ := newTestRuntime()
	cfg := runtime.ContainerConfig{Image: "test", CPUs: 1, Memory: "512m"}
	result := rt.SetLimits(cfg, runtime.ResourceLimits{Memory: "2g"})

	if result.CPUs != 1 {
		t.Errorf("expected CPUs unchanged at 1, got %f", result.CPUs)
	}
	if result.Memory != "2g" {
		t.Errorf("expected Memory '2g', got %q", result.Memory)
	}
}

// --- Network operations ---

func TestCreateNetwork_Success(t *testing.T) {
	rt, runner := newTestRuntime()
	err := rt.CreateNetwork(context.Background(), "mynet", runtime.NetworkConfig{Subnet: "10.0.0.0/24"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	call, _ := runner.LastCall()
	args := strings.Join(call.Args, " ")
	if !strings.Contains(args, "network create mynet --subnet 10.0.0.0/24") {
		t.Errorf("expected network create args, got: %s", args)
	}
}

func TestCreateNetwork_AlreadyExists(t *testing.T) {
	rt, runner := newTestRuntime()
	runner.DefaultResult = testutil.MockResult{
		Output: []byte("network already exists"),
		Err:    fmt.Errorf("exit status 1"),
	}

	err := rt.CreateNetwork(context.Background(), "mynet", runtime.NetworkConfig{})
	if err != nil {
		t.Fatalf("expected idempotent success, got: %v", err)
	}
}

func TestCreateNetwork_Internal(t *testing.T) {
	rt, runner := newTestRuntime()
	rt.CreateNetwork(context.Background(), "internal", runtime.NetworkConfig{Internal: true})

	call, _ := runner.LastCall()
	args := strings.Join(call.Args, " ")
	if !strings.Contains(args, "--internal") {
		t.Errorf("expected --internal flag, got: %s", args)
	}
}

func TestDeleteNetwork_Success(t *testing.T) {
	rt, _ := newTestRuntime()
	err := rt.DeleteNetwork(context.Background(), "mynet")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteNetwork_NotFound(t *testing.T) {
	rt, runner := newTestRuntime()
	runner.DefaultResult = testutil.MockResult{
		Output: []byte("network not found"),
		Err:    fmt.Errorf("exit status 1"),
	}

	err := rt.DeleteNetwork(context.Background(), "mynet")
	if err != nil {
		t.Fatalf("expected idempotent success, got: %v", err)
	}
}
