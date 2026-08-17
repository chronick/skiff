//go:build !unix

package scheduler

import "os/exec"

// setProcessGroup is a no-op on platforms without Unix process groups; the
// default context kill (direct child only) applies there.
func setProcessGroup(cmd *exec.Cmd) {}
