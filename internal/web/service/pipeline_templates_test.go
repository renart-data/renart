package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bruin-data/bruin/pkg/pipeline"
	gogit "github.com/go-git/go-git/v5"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/web/scheduler"
)

func TestPipelineTemplatesExposeFeatureFocusedStarters(t *testing.T) {
	t.Parallel()

	templates := PipelineTemplates()
	ids := make([]string, 0, len(templates))
	for _, template := range templates {
		ids = append(ids, template.ID)
		assert.NotEmpty(t, template.Title)
		assert.NotEmpty(t, template.Description)
		assert.NotEmpty(t, template.Category)
		assert.NotEmpty(t, template.SuggestedPath)
		assert.NotNil(t, template.AssetNames)
		assert.NotEmpty(t, template.Features)
		if template.ID != PipelineTemplateBlank {
			assert.NotEmpty(t, template.AssetNames)
		}
	}

	assert.Equal(t, []string{
		PipelineTemplateBlank,
		PipelineTemplateProductDemo,
		ProjectTemplateRetailDemo,
		PipelineTemplateOperationsDemo,
		PipelineTemplateEarthquakeDemo,
		PipelineTemplatePythonDemo,
		PipelineTemplateJinjaDemo,
		ProjectTemplateChessDemo,
	}, ids)
}

func TestPipelineServiceCreatesEveryTemplateAsParseablePipeline(t *testing.T) {
	for _, template := range PipelineTemplates() {
		t.Run(template.ID, func(t *testing.T) {
			workspaceRoot := t.TempDir()
			_, err := gogit.PlainInit(workspaceRoot, false)
			require.NoError(t, err)
			service := NewPipelineService(workspaceRoot)

			relPath, err := service.Create(
				context.Background(),
				template.SuggestedPath,
				template.Title,
				"",
				template.ID,
			)
			require.NoError(t, err)
			assert.Equal(t, template.SuggestedPath, relPath)
			assert.DirExists(t, filepath.Join(workspaceRoot, relPath, "assets"))

			parsed, err := NewRenartPipelineBuilder(afero.NewOsFs()).CreatePipelineFromPath(
				context.Background(),
				filepath.Join(workspaceRoot, relPath),
				pipeline.WithMutate(),
			)
			require.NoError(t, err)
			assert.Equal(t, template.Title, parsed.Name)

			names := make([]string, 0, len(parsed.Assets))
			for _, asset := range parsed.Assets {
				names = append(names, asset.Name)
			}
			assert.ElementsMatch(t, template.AssetNames, names)
			for _, asset := range parsed.Assets {
				for _, upstream := range asset.Upstreams {
					assert.Contains(t, names, upstream.Value, "%s depends on %s", asset.Name, upstream.Value)
				}
			}

			report := runTypeCheck(t, parsed, workspaceRoot)
			assert.Zero(t, report.Summary.Errors, "generated starter should not have typecheck errors: %+v", report.Assets)
			assert.Zero(t, report.Summary.Warnings, "generated starter should not have typecheck warnings: %+v", report.Assets)

			if template.ID != PipelineTemplateBlank {
				configContents, readErr := os.ReadFile(filepath.Join(workspaceRoot, ".bruin.yml"))
				require.NoError(t, readErr)
				assert.Contains(t, string(configContents), "duckdb-default")
			}
		})
	}
}

func TestOfflinePipelineDemoTemplatesExecute(t *testing.T) {
	for _, template := range PipelineTemplates() {
		if template.ID == PipelineTemplateBlank || !template.Offline {
			continue
		}
		t.Run(template.ID, func(t *testing.T) {
			executePipelineTemplate(t, template, 2*time.Minute)
		})
	}
}

func TestOnlinePipelineDemoTemplatesExecute(t *testing.T) {
	if os.Getenv("RENART_RUN_ONLINE_TEMPLATE_TESTS") != "1" {
		t.Skip("set RENART_RUN_ONLINE_TEMPLATE_TESTS=1 to execute network-dependent demo templates")
	}

	for _, template := range PipelineTemplates() {
		if template.ID == PipelineTemplateBlank || template.Offline {
			continue
		}
		t.Run(template.ID, func(t *testing.T) {
			executePipelineTemplate(t, template, 5*time.Minute)
		})
	}
}

func TestPipelineServiceEarthquakeTemplateIncludesEnvironmentSchedules(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	_, err := gogit.PlainInit(workspaceRoot, false)
	require.NoError(t, err)

	relPath, err := NewPipelineService(workspaceRoot).Create(
		context.Background(),
		"earthquake_monitoring",
		"Earthquake monitoring",
		"",
		PipelineTemplateEarthquakeDemo,
	)
	require.NoError(t, err)

	parsed, err := NewRenartPipelineBuilder(afero.NewOsFs()).CreatePipelineFromPath(
		context.Background(),
		filepath.Join(workspaceRoot, relPath),
		pipeline.WithMutate(),
	)
	require.NoError(t, err)
	require.NotEmpty(t, parsed.LegacyID)

	declarations, err := scheduler.NewScheduleDeclarationStore(
		filepath.Join(workspaceRoot, ".renart", "schedules.yml"),
	).List()
	require.NoError(t, err)
	require.Len(t, declarations, 2)

	byEnvironment := make(map[string]scheduler.ScheduleDeclaration, len(declarations))
	for _, declaration := range declarations {
		assert.Equal(t, parsed.LegacyID, declaration.PipelineUUID)
		byEnvironment[declaration.Environment] = declaration.Declaration
	}
	assert.Equal(t, "0 */6 * * *", byEnvironment["default"].Cron)
	assert.Equal(t, scheduler.CatchupSkip, byEnvironment["default"].CatchupPolicy)
	assert.Equal(t, "15 * * * *", byEnvironment["production"].Cron)
	assert.Equal(t, scheduler.CatchupRunOnce, byEnvironment["production"].CatchupPolicy)

	configContents, err := os.ReadFile(filepath.Join(workspaceRoot, ".bruin.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(configContents), "earthquake_monitoring.duckdb")
	assert.Contains(t, string(configContents), "earthquake_monitoring_production.duckdb")
}

func TestEarthquakeTemplateMakesHistoricalAssetRolesExplicit(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	_, err := gogit.PlainInit(workspaceRoot, false)
	require.NoError(t, err)
	relPath, err := NewPipelineService(workspaceRoot).Create(
		context.Background(),
		"earthquake_monitoring",
		"Earthquake monitoring",
		"",
		PipelineTemplateEarthquakeDemo,
	)
	require.NoError(t, err)
	parsed, err := NewRenartPipelineBuilder(afero.NewOsFs()).CreatePipelineFromPath(
		context.Background(),
		filepath.Join(workspaceRoot, relPath),
		pipeline.WithMutate(),
	)
	require.NoError(t, err)

	assets := make(map[string]*pipeline.Asset, len(parsed.Assets))
	for _, asset := range parsed.Assets {
		assets[asset.Name] = asset
	}

	events := assets["earthquakes.events"]
	require.NotNil(t, events)
	assert.Contains(t, events.Description, "Retained event history")
	assert.Contains(t, []string(events.Tags), "event-history")
	assert.Equal(t, "merge", string(events.Materialization.Strategy))

	notable := assets["earthquakes.notable_events"]
	require.NotNil(t, notable)
	assert.Contains(t, notable.Description, "Current notable-event shortlist")
	assert.Contains(t, []string(notable.Tags), "current-snapshot")
	assert.Equal(t, "truncate+insert", string(notable.Materialization.Strategy))

	summary := assets["earthquakes.window_summary"]
	require.NotNil(t, summary)
	assert.Contains(t, summary.Description, "Replay-safe historical time series")
	assert.Contains(t, []string(summary.Tags), "recommended-analysis")
	assert.Equal(t, "time_interval", string(summary.Materialization.Strategy))

	runLog := assets["earthquakes.run_log"]
	require.NotNil(t, runLog)
	assert.Contains(t, runLog.Description, "Append-only execution audit history")
	assert.Contains(t, []string(runLog.Tags), "append-only")
	assert.Equal(t, "append", string(runLog.Materialization.Strategy))
	runColumns := make([]string, 0, len(runLog.Columns))
	for _, column := range runLog.Columns {
		runColumns = append(runColumns, column.Name)
	}
	assert.Contains(t, runColumns, "average_magnitude")
	assert.Contains(t, runColumns, "maximum_magnitude")
}

func executePipelineTemplate(t *testing.T, template PipelineTemplateInfo, timeout time.Duration) {
	t.Helper()

	workspaceRoot := t.TempDir()
	_, err := gogit.PlainInit(workspaceRoot, false)
	require.NoError(t, err)

	relPath, err := NewPipelineService(workspaceRoot).Create(
		context.Background(),
		template.SuggestedPath,
		template.Title,
		"",
		template.ID,
	)
	require.NoError(t, err)

	executor := NewHybridBruinExecutor(
		workspaceRoot,
		"",
		nil,
		func() *pipeline.Builder {
			return NewRenartPipelineBuilder(afero.NewOsFs())
		},
	)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	output, err := executor.RunPipeline(ctx, RunPipelineRequest{
		Target:        relPath,
		SensorMode:    "once",
		StartDate:     "2026-07-22T00:00:00Z",
		EndDate:       "2026-07-23T00:00:00Z",
		ExecutionTime: time.Date(2026, time.July, 23, 0, 0, 0, 0, time.UTC),
	}, nil)
	require.NoError(t, err, "%s\n%s", template.ID, string(output))
	assert.Contains(t, string(output), "bruin run completed successfully")
}

func TestPipelineServiceTemplateCreationRejectsUnsafeOverwritesAndRollsBackInvalidContent(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	service := NewPipelineService(workspaceRoot)
	existing := filepath.Join(workspaceRoot, "existing")
	require.NoError(t, os.MkdirAll(existing, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(existing, "keep.txt"), []byte("keep"), 0o644))

	_, err := service.Create(context.Background(), "existing", "", "", PipelineTemplateProductDemo)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
	assert.FileExists(t, filepath.Join(existing, "keep.txt"))

	_, err = service.Create(context.Background(), "unknown", "", "", "demo:unknown")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown pipeline template")
	assert.NoDirExists(t, filepath.Join(workspaceRoot, "unknown"))

	_, err = service.Create(
		context.Background(),
		"mixed",
		"",
		"name: mixed\n",
		PipelineTemplateProductDemo,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be combined")
	assert.NoDirExists(t, filepath.Join(workspaceRoot, "mixed"))

	_, err = service.Create(
		context.Background(),
		"broken",
		"",
		"name: [\n",
		PipelineTemplateBlank,
	)
	require.Error(t, err)
	assert.NoDirExists(t, filepath.Join(workspaceRoot, "broken"))
}

func TestPipelineTemplatePythonQueryDeclaresItsDependency(t *testing.T) {
	t.Parallel()

	template, ok := pipelineTemplateByID(PipelineTemplatePythonDemo)
	require.True(t, ok)
	files := template.files("Python risk scoring")
	pythonAsset := files["assets/risk/scored_accounts.py"]
	assert.Contains(t, pythonAsset, "from renart import query")
	assert.Contains(t, pythonAsset, "select * from risk.account_features")
	assert.True(t, strings.Contains(pythonAsset, "depends:\n  - risk.account_features"))
}
