package cmd

import (
	"github.com/urfave/cli/v3"
)

// Command categories shown in the root help. chi of urfave/cli sorts
// categories alphabetically, so the names are chosen to read well in that
// order: IDE, Pipeline, Project.
const (
	categoryIDE      = "IDE"
	categoryPipeline = "Pipeline"
	categoryProject  = "Project"
)

// buildVersion is the version string the binary was built with, recorded by
// Root so commands (health endpoint, version-skew warning) can report it.
var buildVersion = "dev"

// Root assembles the renart CLI: the visible surface is only what a pipeline
// author needs (see plans/cli-v1.md §0 for the contract); debugging tools
// hide under `renart debug`.
func Root(version string) *cli.Command {
	if version != "" {
		buildVersion = version
	}
	return &cli.Command{
		Name:    "renart",
		Version: buildVersion,
		Usage:   "the data pipeline IDE — build, run, and schedule pipelines from one binary",
		Commands: []*cli.Command{
			Web(),
			Standalone(),
			MCP(),
			Run(),
			Plan(),
			Render(),
			Ls(),
			TypeCheck(),
			Deploy(),
			Init(),
			Secrets(),
			Debug(),
		},
		DisableSliceFlagSeparator: true,
	}
}
