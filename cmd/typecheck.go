package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/fatih/color"
	"github.com/spf13/afero"
	"github.com/urfave/cli/v3"

	"renart/internal/web/service"
)

// TypeCheck type-checks every asset in a pipeline: it renders each SQL asset
// (Jinja templates, pipeline variables, and start/end dates) and validates the
// rendered query against the schema of its upstreams, validates declared
// dependencies, and warns about non-SQL assets (Python/API/...) that declare no
// columns.
func TypeCheck() *cli.Command {
	return &cli.Command{
		Name:      "type-check",
		Usage:     "type-check a pipeline's dependencies, SQL declarations, and custom checks",
		ArgsUsage: "[pipeline name or directory]",
		Category:  categoryPipeline,
		Flags: []cli.Flag{
			workspaceFlag(),
			&cli.StringFlag{
				Name:  "start-date",
				Usage: "RFC3339 start timestamp used for the Jinja date context (defaults to the pipeline schedule)",
			},
			&cli.StringFlag{
				Name:  "end-date",
				Usage: "RFC3339 end timestamp used for the Jinja date context (defaults to the pipeline schedule)",
			},
			&cli.BoolFlag{
				Name:  "json",
				Usage: "emit the full report as JSON instead of human-readable text",
			},
			&cli.BoolFlag{
				Name:  "strict",
				Usage: "exit non-zero when there are warnings, not just errors",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			workspaceRoot, absTarget, err := resolvePipelineTargetAndWorkspace(c)
			if err != nil {
				return err
			}

			fs := afero.NewOsFs()
			builder := service.NewRenartPipelineBuilder(fs)
			parsed, err := builder.CreatePipelineFromPath(ctx, absTarget, pipeline.WithMutate())
			if err != nil {
				return fmt.Errorf("failed to load pipeline at %s: %w", absTarget, err)
			}

			tw, err := service.ResolveExecutionTimeWindow(
				string(parsed.Schedule), c.String("start-date"), c.String("end-date"), time.Now().UTC(),
			)
			if err != nil {
				return fmt.Errorf("failed to resolve execution window: %w", err)
			}

			report := service.CheckPipeline(ctx, fs, parsed, workspaceRoot, tw)
			state, stateErr := service.NewWorkspaceService(
				workspaceRoot,
				filepath.Join(workspaceRoot, ".bruin.yml"),
			).ComputeState(ctx)
			if stateErr != nil {
				return fmt.Errorf("failed to load workspace presentation checks: %w", stateErr)
			}
			for _, candidate := range state.Pipelines {
				candidatePath := filepath.Clean(filepath.Join(workspaceRoot, filepath.FromSlash(candidate.Path)))
				if candidatePath != filepath.Clean(absTarget) {
					continue
				}
				report.PipelineID = candidate.ID
				service.AppendPresentationTypeChecks(
					ctx, fs, workspaceRoot, candidate.ID, state, &report,
				)
				break
			}

			if c.Bool("json") {
				encoded, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(c.Writer, string(encoded))
			} else {
				printTypeCheckReport(c.Writer, report)
			}

			if report.Summary.Errors > 0 || (c.Bool("strict") && report.Summary.Warnings > 0) {
				return cli.Exit("", 1)
			}
			return nil
		},
	}
}

func printTypeCheckReport(w interface{ Write([]byte) (int, error) }, report service.TypeCheckReport) {
	ok := color.New(color.FgGreen).SprintFunc()
	warn := color.New(color.FgYellow).SprintFunc()
	bad := color.New(color.FgRed).SprintFunc()
	dim := color.New(color.Faint).SprintFunc()

	fmt.Fprintf(w, "Type checking pipeline %q (%d assets)\n", report.PipelineName, report.Summary.Assets)

	for _, asset := range report.Assets {
		var marker string
		switch asset.Status {
		case "error":
			marker = bad("✗")
		case "warning":
			marker = warn("⚠")
		default:
			marker = ok("✓")
		}
		fmt.Fprintf(w, "  %s %s %s\n", marker, asset.Name, dim("("+asset.Type+")"))
		for _, finding := range asset.Findings {
			label := warn("warning")
			if finding.Severity == "error" {
				label = bad("error")
			}
			location := ""
			if finding.Line > 0 {
				location = dim(fmt.Sprintf(" [L%d:C%d]", finding.Line, finding.Column))
			}
			fmt.Fprintf(w, "      %s%s: %s\n", label, location, finding.Message)
		}
	}
	for _, artifact := range report.Presentations {
		marker := ok("✓")
		if artifact.Status == "warning" {
			marker = warn("⚠")
		} else if artifact.Status == "error" {
			marker = bad("✗")
		}
		fmt.Fprintf(w, "  %s %s %s\n", marker, artifact.Title, dim("("+artifact.Kind+")"))
		for _, finding := range artifact.Findings {
			label := warn("warning")
			if finding.Severity == "error" {
				label = bad("error")
			}
			location := ""
			if finding.Path != "" {
				location = dim(" [" + finding.Path + "]")
			}
			fmt.Fprintf(w, "      %s%s: %s\n", label, location, finding.Message)
		}
	}

	summary := fmt.Sprintf("Summary: %d error(s), %d warning(s) across %d asset(s) and %d presentation(s)",
		report.Summary.Errors, report.Summary.Warnings, report.Summary.Assets, report.Summary.Presentations)
	switch {
	case report.Summary.Errors > 0:
		fmt.Fprintln(w, bad(summary))
	case report.Summary.Warnings > 0:
		fmt.Fprintln(w, warn(summary))
	default:
		fmt.Fprintln(w, ok(summary))
	}
}
