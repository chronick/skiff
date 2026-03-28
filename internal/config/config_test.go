package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadValidConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
services:
  web:
    command: ["python", "-m", "http.server"]
    working_dir: /tmp
containers:
  db:
    image: postgres:15
    ports:
      - "5432:5432"
schedules:
  backup:
    command: ["echo", "backup"]
    working_dir: /tmp
    interval_seconds: 3600
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if cfg.Version != 1 {
		t.Errorf("expected version 1, got %d", cfg.Version)
	}
	if len(cfg.Services) != 1 {
		t.Errorf("expected 1 service, got %d", len(cfg.Services))
	}
	if len(cfg.Containers) != 1 {
		t.Errorf("expected 1 container, got %d", len(cfg.Containers))
	}
	if len(cfg.Schedules) != 1 {
		t.Errorf("expected 1 schedule, got %d", len(cfg.Schedules))
	}
}

func TestDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
services:
  web:
    command: ["echo", "hi"]
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Daemon.StatusPollIntervalSecs != 5 {
		t.Errorf("expected default poll interval 5, got %d", cfg.Daemon.StatusPollIntervalSecs)
	}
	if cfg.Daemon.LogBufferLines != 500 {
		t.Errorf("expected default log buffer 500, got %d", cfg.Daemon.LogBufferLines)
	}
	if cfg.DNS.Port != 15353 {
		t.Errorf("expected default DNS port 15353, got %d", cfg.DNS.Port)
	}
	if cfg.DNS.Domain != "skiff.local" {
		t.Errorf("expected default DNS domain skiff.local, got %s", cfg.DNS.Domain)
	}

	svc := cfg.Services["web"]
	if svc.RestartPolicy != "always" {
		t.Errorf("expected default restart policy 'always', got %s", svc.RestartPolicy)
	}
}

func TestInvalidVersion(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 2
services:
  web:
    command: ["echo"]
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for version 2")
	}
}

func TestDuplicateNames(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
services:
  app:
    command: ["echo"]
containers:
  app:
    image: app:latest
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
}

func TestInvalidName(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
services:
  "bad name!":
    command: ["echo"]
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for invalid name")
	}
}

func TestDottedNames(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
services:
  core.gangway:
    command: ["gangway", "--socket", "/tmp/gangway.sock"]
  comms.openclaw:
    command: ["openclaw", "gateway", "start"]
containers:
  obs.lookout:
    image: ghcr.io/chronick/lookout-go:latest
schedules:
  auto.morning-digest:
    command: ["echo", "morning"]
    working_dir: /tmp
    interval_seconds: 3600
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("dotted names should be valid: %v", err)
	}

	if len(cfg.Services) != 2 {
		t.Errorf("expected 2 services, got %d", len(cfg.Services))
	}
	if len(cfg.Containers) != 1 {
		t.Errorf("expected 1 container, got %d", len(cfg.Containers))
	}
	if len(cfg.Schedules) != 1 {
		t.Errorf("expected 1 schedule, got %d", len(cfg.Schedules))
	}
}

func TestDependencyCycleDetection(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
services:
  a:
    command: ["echo"]
    depends_on: [b]
  b:
    command: ["echo"]
    depends_on: [a]
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for dependency cycle")
	}
}

func TestCrossTypeDependency(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
services:
  worker:
    command: ["echo"]
containers:
  api:
    image: api:latest
    depends_on: [worker]
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("cross-type dependency should be valid: %v", err)
	}

	order, err := DependencyOrder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// worker should come before api
	workerIdx, apiIdx := -1, -1
	for i, name := range order {
		if name == "worker" {
			workerIdx = i
		}
		if name == "api" {
			apiIdx = i
		}
	}
	if workerIdx >= apiIdx {
		t.Errorf("expected worker before api, got order: %v", order)
	}
}

func TestUnknownDependency(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
services:
  web:
    command: ["echo"]
    depends_on: [nonexistent]
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for unknown dependency")
	}
}

func TestTCPWithoutAuthToken(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
daemon:
  listen: "127.0.0.1:9100"
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for TCP without auth_token")
	}
}

func TestVolumePathTraversal(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
containers:
  evil:
    image: evil:latest
    volumes:
      - "../../etc/passwd:/data"
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for path traversal in volume")
	}
}

func TestEnvVarResolution(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	os.Setenv("TEST_SKIFF_VAR", "resolved-value")
	defer os.Unsetenv("TEST_SKIFF_VAR")

	content := `version: 1
services:
  web:
    command: ["echo"]
    env:
      MY_VAR: "${TEST_SKIFF_VAR}"
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Services["web"].Env["MY_VAR"] != "resolved-value" {
		t.Errorf("expected resolved-value, got %s", cfg.Services["web"].Env["MY_VAR"])
	}
}

func TestDotEnvFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")
	envPath := filepath.Join(dir, ".env")

	os.WriteFile(envPath, []byte("DOT_ENV_VAR=from-dotenv\n"), 0644)

	content := `version: 1
services:
  web:
    command: ["echo"]
    env:
      MY_VAR: "${DOT_ENV_VAR}"
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Services["web"].Env["MY_VAR"] != "from-dotenv" {
		t.Errorf("expected from-dotenv, got %s", cfg.Services["web"].Env["MY_VAR"])
	}
}

func TestProcessEnvOverridesDotEnv(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")
	envPath := filepath.Join(dir, ".env")

	os.WriteFile(envPath, []byte("OVERRIDE_VAR=from-dotenv\n"), 0644)
	os.Setenv("OVERRIDE_VAR", "from-process")
	defer os.Unsetenv("OVERRIDE_VAR")

	content := `version: 1
services:
  web:
    command: ["echo"]
    env:
      MY_VAR: "${OVERRIDE_VAR}"
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Services["web"].Env["MY_VAR"] != "from-process" {
		t.Errorf("expected from-process, got %s", cfg.Services["web"].Env["MY_VAR"])
	}
}

func TestConfigHash(t *testing.T) {
	svc1 := ServiceConfig{Command: []string{"echo", "hello"}}
	svc2 := ServiceConfig{Command: []string{"echo", "hello"}}
	svc3 := ServiceConfig{Command: []string{"echo", "world"}}

	h1 := Hash(svc1)
	h2 := Hash(svc2)
	h3 := Hash(svc3)

	if h1 != h2 {
		t.Errorf("identical configs should have same hash")
	}
	if h1 == h3 {
		t.Errorf("different configs should have different hashes")
	}
}

func TestHealthCheckValidation(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
services:
  web:
    command: ["echo"]
    health_check:
      type: http
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for http health check without url")
	}
}

func TestInvalidRestartPolicy(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
services:
  web:
    command: ["echo"]
    restart_policy: "restart-always"
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for invalid restart policy")
	}
}

func TestContainerLabelEmptyKey(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
containers:
  app:
    image: app:latest
    labels:
      "": "value"
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for empty label key")
	}
}

func TestContainerLabelReservedPrefix(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
containers:
  app:
    image: app:latest
    labels:
      skiff.custom: "value"
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for reserved skiff.* label prefix")
	}
}

func TestContainerNetworkNotDefined(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
containers:
  app:
    image: app:latest
    network: mynet
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for undefined network reference")
	}
}

func TestContainerNetworkHost(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
containers:
  app:
    image: app:latest
    network: host
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("network=host should be valid: %v", err)
	}
}

func TestContainerNoImageOrDockerfile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
containers:
  app:
    ports:
      - "8080:8080"
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for container without image or dockerfile")
	}
}

func TestHealthCheckTCPRequiresPort(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
services:
  web:
    command: ["echo"]
    health_check:
      type: tcp
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for tcp health check without port")
	}
}

func TestHealthCheckCommandRequiresCommand(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
services:
  web:
    command: ["echo"]
    health_check:
      type: command
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for command health check without command")
	}
}

func TestHealthCheckUnknownType(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
services:
  web:
    command: ["echo"]
    health_check:
      type: grpc
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for unknown health check type")
	}
}

func TestHealthCheckDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
services:
  web:
    command: ["echo"]
    health_check:
      type: http
      url: http://localhost:8080/health
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}

	hc := cfg.Services["web"].HealthCheck
	if hc.IntervalSecs != 30 {
		t.Errorf("expected default interval 30, got %d", hc.IntervalSecs)
	}
	if hc.TimeoutSecs != 5 {
		t.Errorf("expected default timeout 5, got %d", hc.TimeoutSecs)
	}
	if hc.FailureThreshold != 3 {
		t.Errorf("expected default threshold 3, got %d", hc.FailureThreshold)
	}
}

func TestEmptyConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("empty config should be valid: %v", err)
	}
}

func TestServiceMissingCommand(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
services:
  web:
    working_dir: /tmp
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for service without command")
	}
}

func TestScheduleRequiresInterval(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
schedules:
  job:
    command: ["echo"]
    working_dir: /tmp
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for schedule without interval")
	}
}

func TestReplicaExpansion(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
containers:
  coder:
    image: agent:latest
    replicas: 3
    volumes:
      - ~/worktrees/{name}:/workspace
    env:
      AGENT_NAME: "{name}"
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Template should be gone, replaced by 3 replicas
	if _, ok := cfg.Containers["coder"]; ok {
		t.Error("template 'coder' should not exist after expansion")
	}
	if len(cfg.Containers) != 3 {
		t.Errorf("expected 3 containers, got %d", len(cfg.Containers))
	}

	for i := 1; i <= 3; i++ {
		name := fmt.Sprintf("coder-%d", i)
		c, ok := cfg.Containers[name]
		if !ok {
			t.Errorf("expected container %q to exist", name)
			continue
		}
		if c.Image != "agent:latest" {
			t.Errorf("%s: expected image agent:latest, got %s", name, c.Image)
		}
		if c.Replicas != 0 {
			t.Errorf("%s: expanded replica should have Replicas=0, got %d", name, c.Replicas)
		}
		// Check {name} substitution in volumes (~ not expanded in volumes)
		expectedVol := fmt.Sprintf("~/worktrees/%s:/workspace", name)
		if len(c.Volumes) != 1 || c.Volumes[0] != expectedVol {
			t.Errorf("%s: expected volume %q, got %v", name, expectedVol, c.Volumes)
		}
		// Check {name} substitution in env
		if c.Env["AGENT_NAME"] != name {
			t.Errorf("%s: expected AGENT_NAME=%s, got %s", name, name, c.Env["AGENT_NAME"])
		}
	}
}

func TestReplicaPortsOnlyFirstReplica(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
containers:
  web:
    image: web:latest
    replicas: 2
    ports:
      - "8080:8080"
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// First replica gets ports
	if len(cfg.Containers["web-1"].Ports) != 1 {
		t.Errorf("web-1 should have 1 port, got %d", len(cfg.Containers["web-1"].Ports))
	}
	// Second replica should not
	if len(cfg.Containers["web-2"].Ports) != 0 {
		t.Errorf("web-2 should have 0 ports, got %d", len(cfg.Containers["web-2"].Ports))
	}
}

func TestReplicaZeroIsNoop(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
containers:
  singleton:
    image: app:latest
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if _, ok := cfg.Containers["singleton"]; !ok {
		t.Error("singleton container should remain unchanged")
	}
	if len(cfg.Containers) != 1 {
		t.Errorf("expected 1 container, got %d", len(cfg.Containers))
	}
}

func TestReplicaWithDependsOn(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
services:
  mail:
    command: ["mail-server"]
containers:
  coder:
    image: agent:latest
    replicas: 2
    depends_on: [mail]
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	for i := 1; i <= 2; i++ {
		name := fmt.Sprintf("coder-%d", i)
		c := cfg.Containers[name]
		if len(c.DependsOn) != 1 || c.DependsOn[0] != "mail" {
			t.Errorf("%s: expected depends_on=[mail], got %v", name, c.DependsOn)
		}
	}

	// Verify dependency ordering works
	order, err := DependencyOrder(cfg)
	if err != nil {
		t.Fatalf("dependency order failed: %v", err)
	}
	mailIdx := -1
	for i, n := range order {
		if n == "mail" {
			mailIdx = i
		}
	}
	for i, n := range order {
		if n == "coder-1" || n == "coder-2" {
			if i < mailIdx {
				t.Errorf("%s should come after mail in dependency order", n)
			}
		}
	}
}

func TestReplicaNamePlaceholderInImageRejected(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
containers:
  bad:
    image: "{name}:latest"
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for {name} in image field")
	}
}

func TestReplicaDuplicateNameCollision(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	// coder with replicas:2 creates coder-1, coder-2
	// but coder-1 already exists as a separate container
	content := `version: 1
containers:
  coder:
    image: agent:latest
    replicas: 2
  coder-1:
    image: other:latest
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	_, err := Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for replica name collision with existing container")
	}
}

func TestLoadRaw(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "skiff.yml")

	content := `version: 1
containers:
  coder:
    image: agent:latest
    replicas: 3
  singleton:
    image: app:latest
`
	os.WriteFile(cfgPath, []byte(content), 0644)
	raw, err := LoadRaw(cfgPath)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Raw should have unexpanded containers
	if len(raw) != 2 {
		t.Errorf("expected 2 raw containers, got %d", len(raw))
	}
	if raw["coder"].Replicas != 3 {
		t.Errorf("expected replicas=3 in raw, got %d", raw["coder"].Replicas)
	}

	groups := ReplicaGroups(nil, raw)
	if len(groups) != 1 {
		t.Fatalf("expected 1 replica group, got %d", len(groups))
	}
	if groups[0].Template != "coder" {
		t.Errorf("expected template 'coder', got %s", groups[0].Template)
	}
	if len(groups[0].Names) != 3 {
		t.Errorf("expected 3 names, got %d", len(groups[0].Names))
	}
}
