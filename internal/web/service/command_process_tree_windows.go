//go:build windows

package service

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// configureCommandProcessTree gives taskkill a distinct root and makes
// context cancellation terminate uv and all of its descendants.
func configureCommandProcessTree(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		killTree := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)) //nolint:gosec
		if err := killTree.Run(); err == nil {
			return nil
		}
		// taskkill is part of supported Windows installations, but killing the
		// root process is still safer than leaving everything alive if it is
		// unavailable in a constrained environment.
		err := cmd.Process.Kill()
		if errors.Is(err, os.ErrProcessDone) || cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 5 * time.Second
}
