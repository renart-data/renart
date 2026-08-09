package service

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/bruin-data/bruin/pkg/lint"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/fatih/color"

	"renart/internal/authoringdiag"
)

const dependencyExistsRule = "dependency-exists"

type pipelineDependencyIssue struct {
	Asset       *pipeline.Asset
	Code        string
	Description string
}

// pipelineDependencyIssues delegates local-name existence semantics to Bruin,
// then fails closed for full URI dependencies until Renart can review their
// cross-pipeline prerequisite coverage. Symbolic URI edges remain lineage-only.
func pipelineDependencyIssues(ctx context.Context, pl *pipeline.Pipeline) ([]pipelineDependencyIssue, error) {
	if pl == nil {
		return nil, nil
	}

	issues := make([]pipelineDependencyIssue, 0)
	for _, asset := range pl.Assets {
		if asset == nil {
			continue
		}
		assetIssues, err := lint.EnsureDependencyExistsForASingleAsset(ctx, pl, asset)
		if err != nil {
			return nil, err
		}
		for _, issue := range assetIssues {
			if issue == nil {
				continue
			}
			issues = append(issues, pipelineDependencyIssue{
				Asset:       asset,
				Code:        dependencyExistsRule,
				Description: issue.Description,
			})
		}
		for _, upstream := range asset.Upstreams {
			if !strings.EqualFold(strings.TrimSpace(upstream.Type), "uri") || upstream.Mode == pipeline.UpstreamModeSymbolic {
				continue
			}
			issues = append(issues, pipelineDependencyIssue{
				Asset: asset,
				Code:  authoringdiag.CodeCrossPipelineExecutionPending,
				Description: fmt.Sprintf(
					"Cross-pipeline dependency %q requires Renart prerequisite readiness, which is not executable yet",
					strings.TrimSpace(upstream.Value),
				),
			})
		}
	}
	return issues, nil
}

func dependencyTypeCheckFindings(ctx context.Context, asset *pipeline.Asset, pl *pipeline.Pipeline) []TypeCheckFinding {
	if asset == nil || pl == nil {
		return nil
	}

	assetIssues, err := lint.EnsureDependencyExistsForASingleAsset(ctx, pl, asset)
	if err != nil {
		return []TypeCheckFinding{{
			Code:       authoringdiag.CodeDependencyValidationFailed,
			Source:     authoringdiag.SourceRenart,
			Severity:   typeCheckSeverityError,
			Message:    "Failed to validate dependencies: " + err.Error(),
			Scope:      string(authoringdiag.ScopeAsset),
			Confidence: string(authoringdiag.ConfidenceHigh),
		}}
	}

	findings := make([]TypeCheckFinding, 0, len(assetIssues))
	for _, issue := range assetIssues {
		if issue == nil {
			continue
		}
		findings = append(findings, TypeCheckFinding{
			Code:       authoringdiag.CodeMissingDependency,
			Source:     authoringdiag.SourceRenart,
			Severity:   typeCheckSeverityError,
			Message:    issue.Description,
			Scope:      string(authoringdiag.ScopeAsset),
			Confidence: string(authoringdiag.ConfidenceHigh),
		})
	}
	return findings
}

type pipelineDependencyValidationError struct {
	count int
}

func (e pipelineDependencyValidationError) Error() string {
	return fmt.Sprintf("pipeline dependency validation failed with %d %s", e.count, pluralize("issue", e.count))
}

var directRunValidationBlueBoldPrinter = directColorPrinter(color.FgBlue, color.Bold)
var directRunValidationFaintPrinter = directColorPrinter(color.Faint)
var directRunValidationGreenPrinter = directColorPrinter(color.FgGreen)
var directRunValidationRedPrinter = directColorPrinter(color.FgRed)
var directRunValidationWhiteBoldPrinter = directColorPrinter(color.FgWhite, color.Bold)

func validateDirectRunDependencies(ctx context.Context, w io.Writer, pl *pipeline.Pipeline, workspaceRoot string) error {
	if pl == nil {
		return fmt.Errorf("pipeline is required for dependency validation")
	}
	issues, err := pipelineDependencyIssues(ctx, pl)
	if err != nil {
		return fmt.Errorf("failed to validate pipeline dependencies: %w", err)
	}

	writeDirectRunDependencyIssues(w, pl, issues, directPipelineDisplayPath(pl, workspaceRoot))
	if len(issues) == 0 {
		_, _ = fmt.Fprintf(w, "\n%s\n", directRunValidationGreenPrinter(
			"✓ Successfully validated dependencies for %d %s across 1 pipeline, all good.",
			len(pl.Assets),
			pluralize("asset", len(pl.Assets)),
		))
		return nil
	}

	_, _ = fmt.Fprintf(
		w,
		"\n%s\n",
		directRunValidationRedPrinter(
			"✘ Checked dependencies for %d %s and found %d %s, please check above.",
			len(pl.Assets),
			pluralize("asset", len(pl.Assets)),
			len(issues),
			pluralize("issue", len(issues)),
		),
	)
	return pipelineDependencyValidationError{count: len(issues)}
}

func writeDirectRunDependencyIssues(w io.Writer, pl *pipeline.Pipeline, issues []pipelineDependencyIssue, pipelinePath string) {
	if w == nil || pl == nil {
		return
	}

	_, _ = fmt.Fprintf(
		w,
		"\n%s\n",
		directRunValidationBlueBoldPrinter(
			"Pipeline: %s %s",
			pl.Name,
			directRunValidationFaintPrinter("(%s)", pipelinePath),
		),
	)
	if len(issues) == 0 {
		_, _ = fmt.Fprintf(w, "%s\n", directRunValidationGreenPrinter("  No dependency issues found"))
		return
	}

	var current *pipeline.Asset
	for _, issue := range issues {
		if issue.Asset != current {
			current = issue.Asset
			_, _ = fmt.Fprintf(
				w,
				"  %s %s\n",
				directRunValidationWhiteBoldPrinter("%s", current.Name),
				directRunValidationFaintPrinter("(%s)", directAssetDisplayPath(pl, current)),
			)
		}
		_, _ = fmt.Fprintf(
			w,
			"%s\n",
			directRunValidationRedPrinter(
				"    └── %s %s",
				issue.Description,
				directRunValidationFaintPrinter("(%s)", issue.Code),
			),
		)
	}
}

func directPipelineDisplayPath(pl *pipeline.Pipeline, workspaceRoot string) string {
	if pl == nil || strings.TrimSpace(pl.DefinitionFile.Path) == "" {
		return "."
	}

	pipelineRoot := filepath.Dir(pl.DefinitionFile.Path)
	if strings.TrimSpace(workspaceRoot) == "" {
		return "."
	}
	rel, err := filepath.Rel(workspaceRoot, pipelineRoot)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "."
	}
	if rel == "" {
		return "."
	}
	return filepath.ToSlash(rel)
}

func directAssetDisplayPath(pl *pipeline.Pipeline, asset *pipeline.Asset) string {
	if pl == nil || asset == nil {
		return ""
	}
	path := filepath.ToSlash(pl.RelativeAssetPath(asset))
	if path == "" || path == "." {
		path = filepath.Base(asset.DefinitionFile.Path)
	}
	return path
}
