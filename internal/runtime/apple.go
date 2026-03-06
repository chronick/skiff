package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/chronick/plane/internal/runner"
)

// AppleRuntime implements ContainerRuntime using the Apple Container CLI
// (https://apple.github.io/container/documentation/).
type AppleRuntime struct {
	runner runner.ProcessRunner
	binary string
	logger *slog.Logger
	dnsIP  string
	dnsPort int
}

// NewAppleRuntime creates an AppleRuntime.
func NewAppleRuntime(r runner.ProcessRunner, logger *slog.Logger) *AppleRuntime {
	return &AppleRuntime{
		runner: r,
		binary: "container",
		logger: logger,
	}
}

func (a *AppleRuntime) Run(ctx context.Context, name string, cfg ContainerConfig) error {
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
	if a.dnsIP != "" {
		args = append(args, "--dns", a.dnsIP)
	}

	args = append(args, cfg.Image)
	a.logger.Info("starting container", "name", name, "image", cfg.Image)
	out, err := a.runner.Run(ctx, a.binary, args, runner.RunOpts{})
	if err != nil {
		return fmt.Errorf("container run: %w: %s", err, string(out))
	}
	return nil
}

func (a *AppleRuntime) Stop(ctx context.Context, name string) error {
	a.logger.Info("stopping container", "name", name)
	out, err := a.runner.Run(ctx, a.binary, []string{"stop", name}, runner.RunOpts{})
	if err != nil {
		a.logger.Debug("stop failed, trying delete", "name", name, "error", err)
		// Container may already be stopped; try delete
		out2, err2 := a.runner.Run(ctx, a.binary, []string{"delete", name}, runner.RunOpts{})
		if err2 != nil {
			return fmt.Errorf("container stop/delete: %w: %s / %s", err, string(out), string(out2))
		}
	}
	return nil
}

func (a *AppleRuntime) Build(ctx context.Context, name string, cfg ContainerConfig) error {
	ctxDir := cfg.Context
	if ctxDir == "" {
		ctxDir = "."
	}

	args := []string{"build"}
	if cfg.Dockerfile != "" {
		args = append(args, "-f", cfg.Dockerfile)
	}
	args = append(args, ctxDir)

	a.logger.Info("building container image", "name", name, "context", ctxDir)
	out, err := a.runner.Run(ctx, a.binary, args, runner.RunOpts{})
	if err != nil {
		return fmt.Errorf("container build: %w: %s", err, string(out))
	}
	return nil
}

func (a *AppleRuntime) Exec(ctx context.Context, name string, command []string) ([]byte, error) {
	args := append([]string{"exec", name}, command...)
	return a.runner.Run(ctx, a.binary, args, runner.RunOpts{})
}

// listEntry matches the JSON output of `container list --format json`.
type listEntry struct {
	Configuration struct {
		ID    string `json:"id"`
		Image struct {
			Reference string `json:"reference"`
		} `json:"image"`
	} `json:"configuration"`
	Status string `json:"status"`
}

func (a *AppleRuntime) List(ctx context.Context) ([]ContainerInfo, error) {
	out, err := a.runner.Run(ctx, a.binary, []string{"list", "--all", "--format", "json"}, runner.RunOpts{})
	if err != nil {
		return nil, fmt.Errorf("container list: %w", err)
	}

	var entries []listEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("parsing container list: %w", err)
	}

	containers := make([]ContainerInfo, 0, len(entries))
	for _, e := range entries {
		containers = append(containers, ContainerInfo{
			Name:  e.Configuration.ID,
			Image: e.Configuration.Image.Reference,
			State: e.Status,
		})
	}
	return containers, nil
}

func (a *AppleRuntime) Inspect(ctx context.Context, name string) (*ContainerInfo, error) {
	out, err := a.runner.Run(ctx, a.binary, []string{"inspect", name, "--format", "json"}, runner.RunOpts{})
	if err != nil {
		return nil, fmt.Errorf("container inspect: %w: %s", err, string(out))
	}

	var entry listEntry
	if err := json.Unmarshal(out, &entry); err != nil {
		return nil, fmt.Errorf("parsing inspect: %w", err)
	}
	return &ContainerInfo{
		Name:  entry.Configuration.ID,
		Image: entry.Configuration.Image.Reference,
		State: entry.Status,
	}, nil
}

func (a *AppleRuntime) InjectDNS(cfg ContainerConfig, dnsIP string, dnsPort int) ContainerConfig {
	// Apple Container CLI supports --dns flag natively.
	// Store for use in Run(). The DNS IP is the host gateway.
	a.dnsIP = dnsIP
	a.dnsPort = dnsPort
	return cfg
}

func (a *AppleRuntime) SetLimits(cfg ContainerConfig, limits ResourceLimits) ContainerConfig {
	if limits.CPUs > 0 {
		cfg.CPUs = limits.CPUs
	}
	if limits.Memory != "" {
		cfg.Memory = limits.Memory
	}
	return cfg
}
