package runner

import (
	"context"
	"io"
	"os/exec"
)

// ProcessRunner abstracts subprocess execution for testability.
type ProcessRunner interface {
	Run(ctx context.Context, name string, args []string, opts RunOpts) ([]byte, error)
}

type RunOpts struct {
	Dir    string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// ExecRunner is the real implementation that uses os/exec.
type ExecRunner struct{}

func (r *ExecRunner) Run(ctx context.Context, name string, args []string, opts RunOpts) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = opts.Dir
	if len(opts.Env) > 0 {
		cmd.Env = opts.Env
	}
	if opts.Stdin != nil {
		cmd.Stdin = opts.Stdin
	}
	if opts.Stdout != nil {
		cmd.Stdout = opts.Stdout
		cmd.Stderr = opts.Stderr
		return nil, cmd.Run()
	}
	return cmd.CombinedOutput()
}
