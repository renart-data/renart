package main

import (
	"context"
	"fmt"
	"os"

	bruintelemetry "github.com/bruin-data/bruin/pkg/telemetry"
	"github.com/fatih/color"
	"github.com/urfave/cli/v3"
	"renart/cmd"
)

var version = "dev"

func main() {
	color.NoColor = false
	configureManagedRuntimeEnvironment()

	err := cmd.Root(version).Run(context.Background(), argsWithDefaultCommand(os.Args))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		cli.HandleExitCoder(err)
		os.Exit(1) //nolint:gocritic
	}
}

// configureManagedRuntimeEnvironment applies process-wide policy before
// dependency initialization. Managed installers must not edit shell profiles,
// and embedded dependencies must not start separate trackers. The uv installer honors
// UV_NO_MODIFY_PATH; the dependency checker's older variable spelling is not
// recognized by current uv releases, so set the supported variable in the
// parent process where every Python and load-asset execution inherits it.
func configureManagedRuntimeEnvironment() {
	_ = os.Setenv("UV_NO_MODIFY_PATH", "1")
	// Renart owns its usage policy; embedded Bruin must never start a second tracker.
	bruintelemetry.OptOut = true
}

// argsWithDefaultCommand makes the desktop app the natural entry point while
// preserving the explicit CLI surface (`renart --help`, `renart run`, and so
// on). Subcommand-specific arguments remain explicit so command typos are not
// silently interpreted as workspace paths.
func argsWithDefaultCommand(args []string) []string {
	if len(args) != 1 {
		return args
	}
	return append(append([]string(nil), args...), "standalone")
}
