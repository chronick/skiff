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

func newDockerTestRuntime() (*runtime.DockerRuntime, *testutil.MockProcessRunner) {
	runner := testutil.NewMockRunner()
	logger := testutil.NewTestLogger()
	rt := runtime.NewDockerRuntime(runner, logger)
	return rt, runner
}

// --- Run ---

func TestDockerRun_BasicFlags(t *testing.T) {
	rt, runner := newDockerTestRuntime()
	err := rt.Run(context.Background(), "web", runtime.ContainerConfig{Image: "nginx:latest"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if runner.CallCount() != 2 {
		t.Fatalf("expected 2 calls (rm -f + run), got %d", runner.CallCount())
	}
	// First call: preemptive rm -f
	rmCall := runner.Calls[0]
	if rmCall.Args[0] != "rm" || rmCall.Args[1] != "-f" {
		t.Errorf("expected 'rm -f' as first call, got: %v", rmCall.Args)
	}
	// Second call: run
	call, _ := runner.LastCall()
	if call.Name != "docker" {
		t.Errorf("expected binary 'docker', got %q", call.Name)
	}

	args := strings.Join(call.Args, " ")
	if !strings.Contains(args, "run --name web -d") {
		t.Errorf("expected 'run --name web -d' in args, got: %s", args)
	}
	if !strings.HasSuffix(args, "nginx:latest") {
		t.Errorf("expected args to end with image, got: %s", args)
	}
}

func TestDockerRun_AllOptions(t *testing.T) {
	rt, runner := newDockerTestRuntime()
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

func TestDockerRun_SystemLabels(t *testing.T) {
	rt, runner := newDockerTestRuntime()
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

func TestDockerRun_Error(t *testing.T) {
	rt, runner := newDockerTestRuntime()
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

// --- Stop ---

func TestDockerStop_Success(t *testing.T) {
	rt, runner := newDockerTestRuntime()
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

func TestDockerStop_StopFailsStillRemoves(t *testing.T) {
	rt, runner := newDockerTestRuntime()
	runner.Results["docker stop web"] = testutil.MockResult{Err: fmt.Errorf("not running")}

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

func TestDockerStop_RmNotFoundIsSuccess(t *testing.T) {
	rt, runner := newDockerTestRuntime()
	runner.Results["docker rm -f web"] = testutil.MockResult{
		Output: []byte("No such container: web"),
		Err:    fmt.Errorf("exit status 1"),
	}

	err := rt.Stop(context.Background(), "web")
	if err != nil {
		t.Fatalf("expected idempotent success for not found, got: %v", err)
	}
}

func TestDockerStop_RmFailsReturnsError(t *testing.T) {
	rt, runner := newDockerTestRuntime()
	runner.Results["docker rm -f web"] = testutil.MockResult{
		Output: []byte("permission denied"),
		Err:    fmt.Errorf("exit status 1"),
	}

	err := rt.Stop(context.Background(), "web")
	if err == nil {
		t.Fatal("expected error when rm fails with non-not-found error")
	}
}

// --- Build ---

func TestDockerBuild_WithDockerfile(t *testing.T) {
	rt, runner := newDockerTestRuntime()
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
	if !strings.Contains(args, "-t web") {
		t.Errorf("expected -t tag, got: %s", args)
	}
	if !strings.Contains(args, "/app") {
		t.Errorf("expected context /app, got: %s", args)
	}
}

// --- List ---

func TestDockerList_ParsesNDJSON(t *testing.T) {
	rt, runner := newDockerTestRuntime()

	line1, _ := json.Marshal(map[string]string{
		"ID": "abc123", "Names": "web", "Image": "nginx:latest", "State": "running",
	})
	line2, _ := json.Marshal(map[string]string{
		"ID": "def456", "Names": "db", "Image": "postgres:15", "State": "exited",
	})
	ndjson := string(line1) + "\n" + string(line2)

	runner.Results["docker ps -a --filter label=skiff.managed=true --format json"] = testutil.MockResult{
		Output: []byte(ndjson),
	}

	containers, err := rt.List(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(containers))
	}
	if containers[0].Name != "web" || containers[0].State != "running" {
		t.Errorf("unexpected first container: %+v", containers[0])
	}
	if containers[1].Name != "db" || containers[1].State != "exited" {
		t.Errorf("unexpected second container: %+v", containers[1])
	}
}

// --- Inspect ---

func TestDockerInspect_ParsesJSON(t *testing.T) {
	rt, runner := newDockerTestRuntime()

	// Docker inspect returns a JSON array; names are prefixed with "/"
	entries := []map[string]interface{}{
		{
			"Name":   "/web",
			"Config": map[string]string{"Image": "nginx:latest"},
			"State":  map[string]string{"Status": "running"},
		},
	}
	data, _ := json.Marshal(entries)
	runner.Results["docker inspect --format json web"] = testutil.MockResult{Output: data}

	info, err := rt.Inspect(context.Background(), "web")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Name != "web" {
		t.Errorf("expected leading / stripped, got %q", info.Name)
	}
	if info.Image != "nginx:latest" {
		t.Errorf("expected image nginx:latest, got %q", info.Image)
	}
	if info.State != "running" {
		t.Errorf("expected state running, got %q", info.State)
	}
}

// --- Stats ---

func TestDockerStats_ParsesStrings(t *testing.T) {
	rt, runner := newDockerTestRuntime()

	entry := map[string]string{
		"CPUPerc":  "1.23%",
		"MemUsage": "45.2MiB / 512MiB",
		"PIDs":     "5",
	}
	data, _ := json.Marshal(entry)
	runner.Results["docker stats web --no-stream --format json"] = testutil.MockResult{Output: data}

	stats, err := rt.Stats(context.Background(), "web")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.CPUPercent != 1.23 {
		t.Errorf("expected CPUPercent 1.23, got %f", stats.CPUPercent)
	}
	if stats.MemUsageMB != 45 {
		t.Errorf("expected MemUsageMB 45, got %d", stats.MemUsageMB)
	}
	if stats.MemLimitMB != 512 {
		t.Errorf("expected MemLimitMB 512, got %d", stats.MemLimitMB)
	}
	if stats.PIDs != 5 {
		t.Errorf("expected 5 PIDs, got %d", stats.PIDs)
	}
}

func TestDockerStats_EmptyResponse(t *testing.T) {
	rt, runner := newDockerTestRuntime()
	runner.Results["docker stats web --no-stream --format json"] = testutil.MockResult{Output: []byte("")}

	_, err := rt.Stats(context.Background(), "web")
	if err == nil {
		t.Fatal("expected error for empty stats")
	}
}

// --- Logs ---

func TestDockerLogs_WithLineCount(t *testing.T) {
	rt, runner := newDockerTestRuntime()
	runner.Results["docker logs --tail 50 web"] = testutil.MockResult{Output: []byte("line1\nline2\n")}

	out, err := rt.Logs(context.Background(), "web", 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(out), "line1") {
		t.Errorf("expected logs output, got: %s", string(out))
	}
}

// --- Network ---

func TestDockerCreateNetwork_Success(t *testing.T) {
	rt, runner := newDockerTestRuntime()
	err := rt.CreateNetwork(context.Background(), "mynet", runtime.NetworkConfig{Subnet: "10.0.0.0/24"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	call, _ := runner.LastCall()
	args := strings.Join(call.Args, " ")
	if !strings.Contains(args, "network create") {
		t.Errorf("expected 'network create' in args, got: %s", args)
	}
	if !strings.Contains(args, "--subnet 10.0.0.0/24") {
		t.Errorf("expected --subnet flag, got: %s", args)
	}
	if !strings.Contains(args, "mynet") {
		t.Errorf("expected network name, got: %s", args)
	}
}

func TestDockerCreateNetwork_AlreadyExists(t *testing.T) {
	rt, runner := newDockerTestRuntime()
	runner.DefaultResult = testutil.MockResult{
		Output: []byte("network already exists"),
		Err:    fmt.Errorf("exit status 1"),
	}

	err := rt.CreateNetwork(context.Background(), "mynet", runtime.NetworkConfig{})
	if err != nil {
		t.Fatalf("expected idempotent success, got: %v", err)
	}
}

func TestDockerDeleteNetwork_Success(t *testing.T) {
	rt, runner := newDockerTestRuntime()
	err := rt.DeleteNetwork(context.Background(), "mynet")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	call, _ := runner.LastCall()
	args := strings.Join(call.Args, " ")
	if !strings.Contains(args, "network rm mynet") {
		t.Errorf("expected 'network rm mynet' in args, got: %s", args)
	}
}

func TestDockerDeleteNetwork_NotFound(t *testing.T) {
	rt, runner := newDockerTestRuntime()
	runner.DefaultResult = testutil.MockResult{
		Output: []byte("No such network: mynet"),
		Err:    fmt.Errorf("exit status 1"),
	}

	err := rt.DeleteNetwork(context.Background(), "mynet")
	if err != nil {
		t.Fatalf("expected idempotent success, got: %v", err)
	}
}
