package supervisor

import (
	"context"
	"io"
	"os"
	"os/exec"
	"syscall"
)

// Cmd abstracts a subprocess for testability.
type Cmd interface {
	Start() error
	Wait() error
	Pid() int
	Signal(sig os.Signal) error
	SignalGroup(sig syscall.Signal) error
	Kill() error
	SetDir(dir string)
	SetEnv(env []string)
	SetStdout(w io.Writer)
	SetStderr(w io.Writer)
}

// CmdFactory creates Cmd instances.
type CmdFactory interface {
	Command(ctx context.Context, name string, args ...string) Cmd
}

// execCmdFactory is the real implementation using os/exec.
type execCmdFactory struct{}

func newExecCmdFactory() CmdFactory {
	return &execCmdFactory{}
}

func (f *execCmdFactory) Command(ctx context.Context, name string, args ...string) Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &execCmd{cmd: cmd}
}

// execCmd wraps *exec.Cmd to implement the Cmd interface.
type execCmd struct {
	cmd *exec.Cmd
}

func (c *execCmd) Start() error            { return c.cmd.Start() }
func (c *execCmd) Wait() error             { return c.cmd.Wait() }
func (c *execCmd) SetDir(dir string)       { c.cmd.Dir = dir }
func (c *execCmd) SetEnv(env []string)     { c.cmd.Env = env }
func (c *execCmd) SetStdout(w io.Writer)   { c.cmd.Stdout = w }
func (c *execCmd) SetStderr(w io.Writer)   { c.cmd.Stderr = w }

func (c *execCmd) Pid() int {
	if c.cmd.Process != nil {
		return c.cmd.Process.Pid
	}
	return 0
}

func (c *execCmd) Signal(sig os.Signal) error {
	if c.cmd.Process == nil {
		return nil
	}
	return c.cmd.Process.Signal(sig)
}

func (c *execCmd) SignalGroup(sig syscall.Signal) error {
	if c.cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(c.cmd.Process.Pid)
	if err != nil {
		return c.cmd.Process.Signal(sig)
	}
	return syscall.Kill(-pgid, sig)
}

func (c *execCmd) Kill() error {
	if c.cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(c.cmd.Process.Pid)
	if err != nil {
		return c.cmd.Process.Kill()
	}
	return syscall.Kill(-pgid, syscall.SIGKILL)
}
