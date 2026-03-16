package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/chronick/skiff/internal/runner"
)

// DockerRuntime implements ContainerRuntime using the Docker CLI.
type DockerRuntime struct {
	runner  runner.ProcessRunner
	binary  string
	logger  *slog.Logger
	dnsIP   string
	dnsPort int
}

// NewDockerRuntime creates a DockerRuntime.
func NewDockerRuntime(r runner.ProcessRunner, logger *slog.Logger) *DockerRuntime {
	return &DockerRuntime{
		runner: r,
		binary: "docker",
		logger: logger,
	}
}

func (d *DockerRuntime) Run(ctx context.Context, name string, cfg ContainerConfig) error {
	// Remove any leftover container with the same name (stopped, failed, etc.)
	_, _ = d.runner.Run(ctx, d.binary, []string{"rm", "-f", name}, runner.RunOpts{})

	args := []string{"run", "--name", name, "-d"}

	for _, v := range cfg.Volumes {
		args = append(args, "-v", v)
	}
	for _, p := range cfg.Ports {
		args = append(args, "-p", p)
	}
	for k, v := range cfg.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	if cfg.CPUs > 0 {
		args = append(args, "--cpus", fmt.Sprintf("%.0f", cfg.CPUs))
	}
	if cfg.Memory != "" {
		args = append(args, "--memory", cfg.Memory)
	}
	if d.dnsIP != "" {
		args = append(args, "--dns", d.dnsIP)
	}

	// Labels: always inject skiff system labels, then user labels
	labels := map[string]string{
		"skiff.managed":  "true",
		"skiff.resource": name,
	}
	for k, v := range cfg.Labels {
		if !strings.HasPrefix(k, "skiff.") {
			labels[k] = v
		}
	}
	// Sort keys for deterministic flag order
	labelKeys := make([]string, 0, len(labels))
	for k := range labels {
		labelKeys = append(labelKeys, k)
	}
	sort.Strings(labelKeys)
	for _, k := range labelKeys {
		args = append(args, "--label", fmt.Sprintf("%s=%s", k, labels[k]))
	}

	if cfg.Init {
		args = append(args, "--init")
	}
	if cfg.ReadOnly {
		args = append(args, "--read-only")
	}
	if cfg.Network != "" {
		args = append(args, "--network", cfg.Network)
	}

	args = append(args, cfg.Image)
	d.logger.Info("starting container", "name", name, "image", cfg.Image)
	out, err := d.runner.Run(ctx, d.binary, args, runner.RunOpts{})
	if err != nil {
		return fmt.Errorf("docker run: %w: %s", err, string(out))
	}
	return nil
}

func (d *DockerRuntime) Stop(ctx context.Context, name string) error {
	d.logger.Info("stopping container", "name", name)

	// Step 1: stop the container (may fail if already stopped — that's fine)
	_, err := d.runner.Run(ctx, d.binary, []string{"stop", name}, runner.RunOpts{})
	if err != nil {
		d.logger.Debug("stop failed (may already be stopped)", "name", name, "error", err)
	}

	// Step 2: always remove the container to prevent orphans
	out, err := d.runner.Run(ctx, d.binary, []string{"rm", "-f", name}, runner.RunOpts{})
	if err != nil {
		// "not found" / "No such" means already removed — treat as success
		s := string(out)
		if strings.Contains(s, "not found") || strings.Contains(s, "No such") {
			return nil
		}
		return fmt.Errorf("docker rm: %w: %s", err, s)
	}
	return nil
}

func (d *DockerRuntime) Build(ctx context.Context, name string, cfg ContainerConfig) error {
	ctxDir := cfg.Context
	if ctxDir == "" {
		ctxDir = "."
	}

	args := []string{"build"}
	if cfg.Dockerfile != "" {
		args = append(args, "-f", cfg.Dockerfile)
	}
	args = append(args, "-t", name)
	args = append(args, ctxDir)

	d.logger.Info("building container image", "name", name, "context", ctxDir)
	out, err := d.runner.Run(ctx, d.binary, args, runner.RunOpts{})
	if err != nil {
		return fmt.Errorf("docker build: %w: %s", err, string(out))
	}
	return nil
}

func (d *DockerRuntime) Exec(ctx context.Context, name string, command []string) ([]byte, error) {
	args := append([]string{"exec", name}, command...)
	return d.runner.Run(ctx, d.binary, args, runner.RunOpts{})
}

// dockerPSEntry matches one JSON object from `docker ps --format json` (NDJSON).
type dockerPSEntry struct {
	ID     string `json:"ID"`
	Names  string `json:"Names"`
	Image  string `json:"Image"`
	State  string `json:"State"`
	Status string `json:"Status"`
	Ports  string `json:"Ports"`
}

func (d *DockerRuntime) List(ctx context.Context) ([]ContainerInfo, error) {
	out, err := d.runner.Run(ctx, d.binary, []string{
		"ps", "-a",
		"--filter", "label=skiff.managed=true",
		"--format", "json",
	}, runner.RunOpts{})
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}

	// Docker outputs one JSON object per line (NDJSON), not an array
	var containers []ContainerInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry dockerPSEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("parsing docker ps line: %w", err)
		}
		containers = append(containers, ContainerInfo{
			Name:  entry.Names,
			Image: entry.Image,
			State: entry.State,
		})
	}
	return containers, nil
}

// dockerInspectEntry matches the JSON from `docker inspect --format json`.
type dockerInspectEntry struct {
	Name   string `json:"Name"`
	Config struct {
		Image string `json:"Image"`
	} `json:"Config"`
	State struct {
		Status string `json:"Status"`
	} `json:"State"`
}

func (d *DockerRuntime) Inspect(ctx context.Context, name string) (*ContainerInfo, error) {
	out, err := d.runner.Run(ctx, d.binary, []string{"inspect", "--format", "json", name}, runner.RunOpts{})
	if err != nil {
		return nil, fmt.Errorf("docker inspect: %w: %s", err, string(out))
	}

	// Docker inspect returns a JSON array with one element
	var entries []dockerInspectEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("parsing docker inspect: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no inspect data returned for %s", name)
	}
	entry := entries[0]

	// Docker prefixes container names with "/"
	containerName := strings.TrimPrefix(entry.Name, "/")

	return &ContainerInfo{
		Name:  containerName,
		Image: entry.Config.Image,
		State: entry.State.Status,
	}, nil
}

// dockerStatsEntry matches the JSON from `docker stats --format json --no-stream`.
type dockerStatsEntry struct {
	CPUPerc  string `json:"CPUPerc"`
	MemUsage string `json:"MemUsage"`
	PIDs     string `json:"PIDs"`
}

func (d *DockerRuntime) Stats(ctx context.Context, name string) (*ContainerStats, error) {
	out, err := d.runner.Run(ctx, d.binary, []string{"stats", name, "--no-stream", "--format", "json"}, runner.RunOpts{})
	if err != nil {
		return nil, fmt.Errorf("docker stats: %w: %s", err, string(out))
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, fmt.Errorf("no stats returned for %s", name)
	}

	var entry dockerStatsEntry
	if err := json.Unmarshal([]byte(trimmed), &entry); err != nil {
		return nil, fmt.Errorf("parsing docker stats: %w", err)
	}

	// Parse CPUPerc: "1.23%" -> 1.23
	cpuPercent, _ := strconv.ParseFloat(strings.TrimSuffix(entry.CPUPerc, "%"), 64)

	// Parse MemUsage: "45.2MiB / 512MiB" -> usage=45, limit=512
	var memUsageMB, memLimitMB int64
	parts := strings.SplitN(entry.MemUsage, " / ", 2)
	if len(parts) == 2 {
		memUsageMB = parseMemValue(parts[0])
		memLimitMB = parseMemValue(parts[1])
	}

	// Parse PIDs: "5" -> 5
	pids, _ := strconv.Atoi(entry.PIDs)

	return &ContainerStats{
		CPUPercent: cpuPercent,
		MemUsageMB: memUsageMB,
		MemLimitMB: memLimitMB,
		PIDs:       pids,
	}, nil
}

// parseMemValue converts Docker memory strings like "45.2MiB", "1.5GiB", "512KiB" to MB.
func parseMemValue(s string) int64 {
	s = strings.TrimSpace(s)

	var suffix string
	var numStr string
	switch {
	case strings.HasSuffix(s, "GiB"):
		suffix = "GiB"
		numStr = strings.TrimSuffix(s, "GiB")
	case strings.HasSuffix(s, "MiB"):
		suffix = "MiB"
		numStr = strings.TrimSuffix(s, "MiB")
	case strings.HasSuffix(s, "KiB"):
		suffix = "KiB"
		numStr = strings.TrimSuffix(s, "KiB")
	case strings.HasSuffix(s, "B"):
		suffix = "B"
		numStr = strings.TrimSuffix(s, "B")
	default:
		return 0
	}

	val, err := strconv.ParseFloat(strings.TrimSpace(numStr), 64)
	if err != nil {
		return 0
	}

	switch suffix {
	case "GiB":
		return int64(val * 1024)
	case "MiB":
		return int64(val)
	case "KiB":
		return int64(val / 1024)
	case "B":
		return int64(val / (1024 * 1024))
	}
	return 0
}

func (d *DockerRuntime) Logs(ctx context.Context, name string, lines int) ([]byte, error) {
	args := []string{"logs"}
	if lines > 0 {
		args = append(args, "--tail", strconv.Itoa(lines))
	}
	args = append(args, name)

	out, err := d.runner.Run(ctx, d.binary, args, runner.RunOpts{})
	if err != nil {
		return nil, fmt.Errorf("docker logs: %w: %s", err, string(out))
	}
	return out, nil
}

func (d *DockerRuntime) InjectDNS(cfg ContainerConfig, dnsIP string, dnsPort int) ContainerConfig {
	// Docker CLI supports --dns flag natively.
	// Store for use in Run(). The DNS IP is the host gateway.
	d.dnsIP = dnsIP
	d.dnsPort = dnsPort
	return cfg
}

func (d *DockerRuntime) SetLimits(cfg ContainerConfig, limits ResourceLimits) ContainerConfig {
	if limits.CPUs > 0 {
		cfg.CPUs = limits.CPUs
	}
	if limits.Memory != "" {
		cfg.Memory = limits.Memory
	}
	return cfg
}

func (d *DockerRuntime) CreateNetwork(ctx context.Context, name string, cfg NetworkConfig) error {
	args := []string{"network", "create"}
	if cfg.Subnet != "" {
		args = append(args, "--subnet", cfg.Subnet)
	}
	if cfg.Internal {
		args = append(args, "--internal")
	}
	args = append(args, name)

	d.logger.Info("creating network", "name", name)
	out, err := d.runner.Run(ctx, d.binary, args, runner.RunOpts{})
	if err != nil {
		// Treat "already exists" as idempotent success
		if strings.Contains(string(out), "already exists") {
			d.logger.Debug("network already exists", "name", name)
			return nil
		}
		return fmt.Errorf("docker network create: %w: %s", err, string(out))
	}
	return nil
}

func (d *DockerRuntime) DeleteNetwork(ctx context.Context, name string) error {
	d.logger.Info("deleting network", "name", name)
	out, err := d.runner.Run(ctx, d.binary, []string{"network", "rm", name}, runner.RunOpts{})
	if err != nil {
		// Treat "not found" / "No such" as idempotent success
		s := string(out)
		if strings.Contains(s, "not found") || strings.Contains(s, "No such") {
			return nil
		}
		return fmt.Errorf("docker network rm: %w: %s", err, s)
	}
	return nil
}
