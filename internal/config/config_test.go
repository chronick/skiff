package config

import (
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
