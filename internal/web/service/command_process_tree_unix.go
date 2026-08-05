//go:build !windows

package service

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// configureCommandProcessTree puts a launcher such as uv and every child it
// creates in one process group. exec.CommandContext otherwise kills only uv,
// which can orphan Sling's Python process with its stdout/stderr pipes open.
func configureCommandProcessTree(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	// If a descendant deliberately leaves the group while retaining a pipe,
	// keep cancellation bounded instead of hanging the Renart request forever.
	cmd.WaitDelay = 5 * time.Second
}
