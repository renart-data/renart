//go:build linux

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const commandProcessTreeHelperEnv = "RENART_TEST_COMMAND_PROCESS_TREE_HELPER"

func TestStreamingCommandCancellationKillsUvStyleChildTree(t *testing.T) {
	pidFile := t.TempDir() + "/child.pid"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := newStreamingCommand(
		ctx,
		os.Args[0],
		[]string{"-test.run=^TestCommandProcessTreeHelper$", "--", "parent", pidFile},
		"",
		nil,
	)
	cmd.Env = append(cmd.Env, commandProcessTreeHelperEnv+"=1")
	writer := &streamCaptureWriter{buffer: bytes.NewBuffer(nil)}
	done := make(chan error, 1)
	go func() {
		done <- runStreamingCommand(ctx, cmd, writer)
	}()

	childPID := waitForHelperPID(t, pidFile)
	defer func() { _ = syscall.Kill(childPID, syscall.SIGKILL) }()
	require.True(t, linuxProcessIsRunning(childPID), "helper child exited before cancellation")

	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(8 * time.Second):
		t.Fatal("streaming command did not return after cancellation")
	}

	require.Eventually(t, func() bool {
		return !linuxProcessIsRunning(childPID)
	}, 3*time.Second, 20*time.Millisecond, "descendant %d survived cancellation", childPID)
}

func TestSlingUVBootstrapRejectsImplicitPythonLauncherFallback(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is required for the Sling bootstrap regression test")
	}

	root := t.TempDir()
	packageDir := filepath.Join(root, "package", "sling")
	require.NoError(t, os.MkdirAll(packageDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(packageDir, "__init__.py"), nil, 0o644))
	require.NoError(t, os.WriteFile(
		filepath.Join(packageDir, "bin.py"),
		[]byte("import shutil\nSLING_BIN = shutil.which('sling')\n"),
		0o644,
	))

	launchMarker := filepath.Join(root, "launcher-ran")
	launcherDir := filepath.Join(root, "bin")
	require.NoError(t, os.MkdirAll(launcherDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(launcherDir, "sling"),
		[]byte("#!/bin/sh\nprintf launched > \"$RENART_TEST_SLING_LAUNCH_MARKER\"\n# from sling import cli\n"),
		0o755,
	))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-c", slingUVBootstrap, "run")
	configureCommandProcessTree(cmd)
	cmd.Env = append(os.Environ(),
		"PYTHONPATH="+filepath.Join(root, "package"),
		"PATH="+launcherDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"RENART_TEST_SLING_LAUNCH_MARKER="+launchMarker,
	)
	output, err := cmd.CombinedOutput()
	require.Error(t, err)
	require.Contains(t, string(output), "stopped Sling's Python launcher from recursively launching itself")
	_, statErr := os.Stat(launchMarker)
	require.ErrorIs(t, statErr, os.ErrNotExist, "the recursive launcher must never run")
}

// TestCommandProcessTreeHelper models uv launching a Python entrypoint. The
// parent waits for a child that keeps inherited output pipes open, reproducing
// the NixOS leak without depending on uv or Sling being installed.
func TestCommandProcessTreeHelper(t *testing.T) {
	if os.Getenv(commandProcessTreeHelperEnv) != "1" {
		return
	}
	separator := -1
	for index, arg := range os.Args {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		os.Exit(2)
	}
	mode := os.Args[separator+1]
	switch mode {
	case "parent":
		if separator+2 >= len(os.Args) {
			os.Exit(2)
		}
		child := exec.Command(os.Args[0], "-test.run=^TestCommandProcessTreeHelper$", "--", "child")
		child.Env = append(os.Environ(), commandProcessTreeHelperEnv+"=1")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		if err := os.WriteFile(os.Args[separator+2], []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			_ = child.Process.Kill()
			os.Exit(4)
		}
		if err := child.Wait(); err != nil {
			os.Exit(5)
		}
		os.Exit(0)
	case "child":
		for {
			time.Sleep(time.Hour)
		}
	default:
		os.Exit(2)
	}
}

func waitForHelperPID(t *testing.T, path string) int {
	t.Helper()
	var pid int
	require.Eventually(t, func() bool {
		content, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(content)))
		return err == nil && pid > 0
	}, 5*time.Second, 10*time.Millisecond)
	return pid
}

func linuxProcessIsRunning(pid int) bool {
	content, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	closing := bytes.LastIndexByte(content, ')')
	if closing >= 0 && closing+2 < len(content) && content[closing+2] == 'Z' {
		return false
	}
	err = syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
