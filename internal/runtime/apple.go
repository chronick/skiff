package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/chronick/plane/internal/runner"
)

// AppleRuntime implements ContainerRuntime using Apple Container CLI.
// NOTE: The exact Apple Container CLI commands need research before this
// implementation is production-ready. This is a best-effort implementation
// based on expected CLI patterns.
type AppleRuntime struct {
	runner runner.ProcessRunner
	binary string // path to container CLI binary
	logger *slog.Logger
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
		args = append(args, "--cpus", fmt.Sprintf("%.1f", cfg.CPUs))
	}
	if cfg.Memory != "" {
		args = append(args, "--memory", cfg.Memory)
	}

	args = append(args, cfg.Image)
	a.logger.Info("starting container", "name", name, "image", cfg.Image)
	_, err := a.runner.Run(ctx, a.binary, args, runner.RunOpts{})
	return err
}

func (a *AppleRuntime) Stop(ctx context.Context, name string) error {
	a.logger.Info("stopping container", "name", name)
	_, err := a.runner.Run(ctx, a.binary, []string{"stop", name}, runner.RunOpts{})
	if err != nil {
		// Try rm if stop fails (container may already be stopped)
		_, _ = a.runner.Run(ctx, a.binary, []string{"rm", name}, runner.RunOpts{})
	}
	return err
}

func (a *AppleRuntime) Build(ctx context.Context, name string, cfg ContainerConfig) error {
	args := []string{"build", "-t", cfg.Image}
	if cfg.Dockerfile != "" {
		args = append(args, "-f", cfg.Dockerfile)
	}
	context := cfg.Context
	if context == "" {
		context = "."
	}
	args = append(args, context)

	a.logger.Info("building container image", "name", name, "image", cfg.Image)
	_, err := a.runner.Run(ctx, a.binary, args, runner.RunOpts{})
	return err
}

func (a *AppleRuntime) Exec(ctx context.Context, name string, command []string) ([]byte, error) {
	args := append([]string{"exec", name}, command...)
	return a.runner.Run(ctx, a.binary, args, runner.RunOpts{})
}

func (a *AppleRuntime) List(ctx context.Context) ([]ContainerInfo, error) {
	out, err := a.runner.Run(ctx, a.binary, []string{"ps", "-a", "--format", "{{.Names}}\t{{.Image}}\t{{.Status}}"}, runner.RunOpts{})
	if err != nil {
		return nil, err
	}

	var containers []ContainerInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		state := "stopped"
		if strings.Contains(strings.ToLower(parts[2]), "up") {
			state = "running"
		}
		containers = append(containers, ContainerInfo{
			Name:  parts[0],
			Image: parts[1],
			State: state,
		})
	}
	return containers, nil
}

func (a *AppleRuntime) Inspect(ctx context.Context, name string) (*ContainerInfo, error) {
	containers, err := a.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range containers {
		if c.Name == name {
			return &c, nil
		}
	}
	return nil, fmt.Errorf("container %q not found", name)
}

func (a *AppleRuntime) InjectDNS(cfg ContainerConfig, dnsIP string, dnsPort int) ContainerConfig {
	cfg.Env["PLANE_DNS"] = fmt.Sprintf("%s:%d", dnsIP, dnsPort)
	// Add DNS flag if supported by runtime
	// This may need adjustment based on Apple Container CLI capabilities
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
