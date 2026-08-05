package service

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"

	"github.com/bruin-data/bruin/pkg/bigquery"
	"github.com/bruin-data/bruin/pkg/config"
	bruinexecutor "github.com/bruin-data/bruin/pkg/executor"
	bruingit "github.com/bruin-data/bruin/pkg/git"
	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/lint"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/query"
	"github.com/fatih/color"
	"github.com/spf13/afero"
	"go.uber.org/zap"
)

func (e *HybridBruinExecutor) dryRunPipeline(ctx context.Context, foundPipeline *pipeline.Pipeline, cfg *config.Config, manager config.ConnectionAndDetailsGetter, onChunk func([]byte)) ([]byte, error) {
	printer := &streamCaptureWriter{buffer: bytes.NewBuffer(nil), onChunk: onChunk}
	rootCheckPath := e.workspaceRoot
	pipelinePath := "."
	if foundPipeline.DefinitionFile.Path != "" {
		if relPath, err := filepath.Rel(rootCheckPath, filepath.Dir(foundPipeline.DefinitionFile.Path)); err == nil {
			pipelinePath = filepath.ToSlash(relPath)
		}
	}
	_, _ = fmt.Fprintf(printer, "Validating pipelines in '.' for '%s' environment...\n", cfg.SelectedEnvironmentName)

	// Passing nil skips Bruin's parser-dependent used-tables rule. Renart's dry-run
	// is validation-only and should not fail because the optional parser process
	// cannot be initialized inside the long-running web server.
	rules, err := lint.GetRules(afero.NewOsFs(), &bruingit.RepoFinder{}, false, nil, true)
	if err != nil {
		return printer.buffer.Bytes(), err
	}
	rules = renartDryRunRules(rules)
	renderer := jinja.NewRendererWithYesterday(foundPipeline.Name, "renart-dry-run")
	rules = append(rules, directDryRunQueryValidatorRules(zap.NewNop().Sugar(), cfg, manager)...)
	rules = append(rules, lint.GetCustomCheckQueryDryRunRule(manager, renderer))
	rules = append(rules, lint.GetHookQueryDryRunRule(manager))

	runCtx := context.WithValue(ctx, pipeline.RunConfigRunID, "renart-dry-run")
	runCtx = context.WithValue(runCtx, pipeline.RunConfigFullRefresh, false)
	runCtx = context.WithValue(runCtx, config.EnvironmentContextKey, cfg.SelectedEnvironment)
	restoreAssets := applyDeveloperEnvironmentPipelineIdentity(runCtx, foundPipeline)
	defer restoreAssets()
	issues, err := lint.RunLintRulesOnPipeline(runCtx, foundPipeline, rules, nil)
	if err != nil {
		return printer.buffer.Bytes(), err
	}

	errorCount := 0
	warningCount := 0
	for rule, ruleIssues := range issues.Issues {
		if rule.GetSeverity() == lint.ValidatorSeverityCritical {
			errorCount += len(ruleIssues)
		} else {
			warningCount += len(ruleIssues)
		}
	}

	writeDirectDryRunIssues(printer, issues, pipelinePath)
	if errorCount > 0 || warningCount > 0 {
		writeDirectDryRunIssueSummary(printer, errorCount, warningCount)
		if errorCount > 0 {
			return printer.buffer.Bytes(), fmt.Errorf("dry run failed with %d validation error(s)", errorCount)
		}
		return printer.buffer.Bytes(), nil
	}

	_, _ = fmt.Fprintf(printer, "\n%s\n", directDryRunGreenPrinter("✓ Successfully validated %d assets across 1 pipeline, all good.", len(foundPipeline.Assets)))
	return printer.buffer.Bytes(), nil
}

func renartDryRunRules(rules []lint.Rule) []lint.Rule {
	result := make([]lint.Rule, 0, len(rules)+1)
	for _, rule := range rules {
		if rule.Name() != "valid-task-type" {
			result = append(result, rule)
		}
	}
	result = append(result, &lint.SimpleRule{
		Identifier:       "valid-task-type",
		Fast:             true,
		Severity:         lint.ValidatorSeverityCritical,
		AssetValidator:   ensureRenartAssetType,
		ApplicableLevels: []lint.Level{lint.LevelAsset},
	})
	return result
}

func ensureRenartAssetType(_ context.Context, _ *pipeline.Pipeline, asset *pipeline.Asset) ([]*lint.Issue, error) {
	if asset == nil {
		return nil, nil
	}
	if asset.Type == "" {
		return []*lint.Issue{{Task: asset, Description: "Asset type must exist"}}, nil
	}
	if isAPIAsset(asset) || isLoadAsset(asset) {
		return nil, nil
	}
	if _, ok := bruinexecutor.DefaultExecutorsV2[asset.Type]; ok {
		return nil, nil
	}
	return []*lint.Issue{{
		Task:        asset,
		Description: fmt.Sprintf("Invalid asset type '%s'", asset.Type),
	}}, nil
}

var directDryRunBlueBoldPrinter = directColorPrinter(color.FgBlue, color.Bold)
var directDryRunFaintPrinter = directColorPrinter(color.Faint)
var directDryRunGreenPrinter = directColorPrinter(color.FgGreen)
var directDryRunRedPrinter = directColorPrinter(color.FgRed)
var directDryRunWhiteBoldPrinter = directColorPrinter(color.FgWhite, color.Bold)
var directDryRunYellowPrinter = directColorPrinter(color.FgYellow)

func writeDirectDryRunIssues(w *streamCaptureWriter, issues *lint.PipelineIssues, pipelinePath string) {
	_, _ = fmt.Fprintf(w, "\n%s\n", directDryRunBlueBoldPrinter("Pipeline: %s %s", issues.Pipeline.Name, directDryRunFaintPrinter("(%s)", pipelinePath)))
	if len(issues.Issues) == 0 {
		_, _ = fmt.Fprintf(w, "%s\n", directDryRunGreenPrinter("  No issues found"))
		return
	}

	for rule, ruleIssues := range issues.Issues {
		for _, issue := range ruleIssues {
			if issue.Task != nil {
				_, _ = fmt.Fprintf(w, "  %s %s\n", directDryRunWhiteBoldPrinter("%s", issue.Task.Name), directDryRunFaintPrinter("(%s)", issues.Pipeline.RelativeAssetPath(issue.Task)))
			}
			message := fmt.Sprintf("    └── %s %s", issue.Description, directDryRunFaintPrinter("(%s)", rule.Name()))
			if rule.GetSeverity() == lint.ValidatorSeverityWarning {
				_, _ = fmt.Fprintf(w, "%s\n", directDryRunYellowPrinter("%s", message))
			} else {
				_, _ = fmt.Fprintf(w, "%s\n", directDryRunRedPrinter("%s", message))
			}
			for _, contextLine := range issue.Context {
				_, _ = fmt.Fprintf(w, "        %s\n", contextLine)
			}
		}
	}
}

func writeDirectDryRunIssueSummary(w *streamCaptureWriter, errorCount, warningCount int) {
	foundMessage := "found"
	if errorCount > 0 {
		foundMessage += directDryRunRedPrinter(" %d %s", errorCount, pluralize("issue", errorCount))
	}
	if warningCount > 0 {
		if errorCount > 0 {
			foundMessage += " and"
		}
		foundMessage += directDryRunYellowPrinter(" %d %s", warningCount, pluralize("warning", warningCount))
	}
	_, _ = fmt.Fprintf(w, "\n✘ Checked 1 pipeline and %s, please check above.\n", foundMessage)
}

func pluralize(word string, count int) string {
	if count == 1 {
		return word
	}
	return word + "s"
}

func directDryRunQueryValidatorRules(logger *zap.SugaredLogger, cfg *config.Config, manager config.ConnectionGetter) []lint.Rule {
	rules := make([]lint.Rule, 0)
	renderer := jinja.NewRendererWithYesterday("your-pipeline-name", "renart-dry-run")
	fs := afero.NewOsFs()

	if len(cfg.SelectedEnvironment.Connections.GoogleCloudPlatform) > 0 {
		rules = append(rules, &lint.QueryValidatorRule{
			Identifier:  "bigquery-validator",
			TaskType:    pipeline.AssetTypeBigqueryQuery,
			Connections: manager,
			Extractor: &query.WholeFileExtractor{
				Fs:       fs,
				Renderer: renderer,
			},
			Materializer: bigquery.NewMaterializer(false),
			WorkerCount:  32,
			Logger:       logger,
		})
	}
	if len(cfg.SelectedEnvironment.Connections.Snowflake) > 0 {
		rules = append(rules, directQueryValidatorRule("snowflake-validator", pipeline.AssetTypeSnowflakeQuery, manager, renderer, fs, logger))
	}
	if len(cfg.SelectedEnvironment.Connections.RedShift) > 0 {
		rules = append(rules, directQueryValidatorRule("redshift-validator", pipeline.AssetTypeRedshiftQuery, manager, renderer, fs, logger))
	}
	if len(cfg.SelectedEnvironment.Connections.Postgres) > 0 {
		rules = append(rules, directQueryValidatorRule("postgres-validator", pipeline.AssetTypePostgresQuery, manager, renderer, fs, logger))
	}
	return rules
}

func directQueryValidatorRule(identifier string, assetType pipeline.AssetType, manager config.ConnectionGetter, renderer jinja.RendererInterface, fs afero.Fs, logger *zap.SugaredLogger) lint.Rule {
	return &lint.QueryValidatorRule{
		Identifier:  identifier,
		TaskType:    assetType,
		Connections: manager,
		Extractor: &query.FileQuerySplitterExtractor{
			Fs:       fs,
			Renderer: renderer,
		},
		WorkerCount: 32,
		Logger:      logger,
	}
}
