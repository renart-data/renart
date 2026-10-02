package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	gogit "github.com/go-git/go-git/v5"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/web/presentation"
	"renart/internal/web/scheduler"
)

func TestScaffoldProjectRetailDemoIntoFreshDirectory(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	target := filepath.Join(parent, "my-retail")

	result, err := ScaffoldProject(ScaffoldProjectRequest{
		TargetDir:     target,
		Template:      ProjectTemplateRetailDemo,
		NewRepository: true,
	})
	require.NoError(t, err)

	assert.Equal(t, "retail", result.PipelinePath)
	assert.Equal(t, EncodeID("retail"), result.PipelineID)
	assert.True(t, result.GitInitialized)

	for _, relPath := range []string{
		"retail/pipeline.yml",
		"retail/assets/raw/customers.asset.yml",
		"retail/assets/raw/customers.csv",
		"retail/assets/raw/orders.asset.yml",
		"retail/assets/raw/orders.csv",
		"retail/assets/analytics/customer_orders.sql",
		"retail/assets/analytics/daily_revenue.sql",
		".bruin.yml",
		".gitignore",
		".renart/project.yml",
	} {
		assert.FileExists(t, filepath.Join(target, relPath), relPath)
		assert.Contains(t, result.Files, relPath)
	}

	configContents, err := os.ReadFile(filepath.Join(target, ".bruin.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(configContents), "duckdb-default")
	assert.Contains(t, string(configContents), "duckdb-files/retail.duckdb")

	customersDefinition, err := os.ReadFile(filepath.Join(target, "retail", "assets", "raw", "customers.asset.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(customersDefinition), "type: duckdb.seed")
	assert.Contains(t, string(customersDefinition), "path: ./customers.csv")
	assert.Contains(t, string(customersDefinition), "renart_seed_file: customers.csv")
	ordersDefinition, err := os.ReadFile(filepath.Join(target, "retail", "assets", "raw", "orders.asset.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(ordersDefinition), "type: duckdb.seed")
	assert.Contains(t, string(ordersDefinition), "path: ./orders.csv")
	assert.Contains(t, string(ordersDefinition), "renart_seed_file: orders.csv")

	customersCSV, err := os.ReadFile(filepath.Join(target, "retail", "assets", "raw", "customers.csv"))
	require.NoError(t, err)
	assert.Len(t, strings.Split(strings.TrimSpace(string(customersCSV)), "\n"), 13)
	ordersCSV, err := os.ReadFile(filepath.Join(target, "retail", "assets", "raw", "orders.csv"))
	require.NoError(t, err)
	orderRows := strings.Split(strings.TrimSpace(string(ordersCSV)), "\n")
	assert.Len(t, orderRows, 481)
	assert.Equal(t, "1,8,2024-01-14,4.87,completed", orderRows[1])
	assert.Equal(t, "480,1,2024-01-01,92.10,returned", orderRows[480])
	assert.NoFileExists(t, filepath.Join(target, "retail", "assets", "raw", "customers.sql"))
	assert.NoFileExists(t, filepath.Join(target, "retail", "assets", "raw", "orders.sql"))

	repo, err := gogit.PlainOpen(target)
	require.NoError(t, err)
	head, err := repo.Head()
	require.NoError(t, err)
	assert.Equal(t, "main", head.Name().Short())
	commit, err := repo.CommitObject(head.Hash())
	require.NoError(t, err)
	assert.Equal(t, "Initialize renart project", commit.Message)
	tree, err := commit.Tree()
	require.NoError(t, err)
	_, err = tree.File("retail/pipeline.yml")
	assert.NoError(t, err, "scaffold files must be part of the initial commit")

	worktree, err := repo.Worktree()
	require.NoError(t, err)
	status, err := worktree.Status()
	require.NoError(t, err)
	assert.True(t, status.IsClean(), "scaffold must leave a clean worktree, got: %v", status)
}

func TestScaffoldProjectProductDemoIncludesNotebookAndDashboard(t *testing.T) {
	t.Parallel()

	target := filepath.Join(t.TempDir(), "product-demo")
	// A file the user already has is never replaced.
	require.NoError(t, os.MkdirAll(filepath.Join(target, "dashboards"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(target, "dashboards", "product-overview.dashboard.yml"), []byte("mine"), 0o644))

	result, err := ScaffoldProject(ScaffoldProjectRequest{
		TargetDir:     target,
		Template:      ProjectTemplateProductDemo,
		NewRepository: true,
	})
	require.NoError(t, err)

	for _, relPath := range []string{
		"notebooks/product-activity/notebook.yml",
		"notebooks/product-activity/daily_activity.sql",
		"notebooks/product-activity/plan_journeys.sql",
	} {
		assert.FileExists(t, filepath.Join(target, relPath), relPath)
		assert.Contains(t, result.Files, relPath)
	}
	assert.NotContains(t, result.Files, "dashboards/product-overview.dashboard.yml")
	kept, err := os.ReadFile(filepath.Join(target, "dashboards", "product-overview.dashboard.yml"))
	require.NoError(t, err)
	assert.Equal(t, "mine", string(kept))

	// The dashboard definition itself is valid and reads the pipeline's tables.
	artifact, err := presentation.DecodeArtifact("product-overview.dashboard.yml", []byte(productDashboardYAML()))
	require.NoError(t, err)
	for _, finding := range presentation.CheckArtifactDefinition(*artifact) {
		assert.NotEqual(t, "error", finding.Severity, "%s: %s", finding.Path, finding.Message)
	}
	assert.Equal(t, "product.daily_active_users", artifact.Datasets["daily_activity"].Asset)

	// The in-workspace "New pipeline" starter doesn't add project files.
	starter, ok := pipelineTemplateByID(PipelineTemplateProductDemo)
	require.True(t, ok)
	for relPath := range starter.files("product") {
		assert.False(t, strings.HasPrefix(relPath, "notebooks/") || strings.HasPrefix(relPath, "dashboards/"), relPath)
	}
}

func TestScaffoldProjectChessDemoAvoidsCatalogSchemaCollision(t *testing.T) {
	t.Parallel()

	target := t.TempDir()
	result, err := ScaffoldProject(ScaffoldProjectRequest{
		TargetDir:     target,
		Template:      ProjectTemplateChessDemo,
		NewRepository: true,
	})
	require.NoError(t, err)

	configContents, err := os.ReadFile(filepath.Join(target, ".bruin.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(configContents), "duckdb-files/chess_playground.duckdb")
	assert.NotContains(t, string(configContents), "duckdb-files/chess.duckdb")
	template, ok := projectTemplateByID(ProjectTemplateChessDemo)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{
		"chess.players",
		"chess.games",
		"chess.game_results",
		"chess.player_performance",
		"chess.opening_repertoire",
	}, template.info.AssetNames)
	assert.Contains(t, result.Files, "chess/assets/chess/game_results.sql")
	assert.Contains(t, result.Files, "chess/assets/chess/player_performance.sql")
	assert.Contains(t, result.Files, "chess/assets/chess/opening_repertoire.sql")
	assert.NoFileExists(t, filepath.Join(target, "chess", "assets", "chess", "my_python_asset.py"))
}

func TestScaffoldProjectEarthquakeDemoIncludesEnvironmentSchedules(t *testing.T) {
	t.Parallel()

	target := t.TempDir()
	result, err := ScaffoldProject(ScaffoldProjectRequest{
		TargetDir:     target,
		Template:      ProjectTemplateEarthquakeDemo,
		NewRepository: true,
	})
	require.NoError(t, err)

	assert.Contains(t, result.Files, ".renart/schedules.yml")
	assert.FileExists(t, filepath.Join(target, ".renart", "schedules.yml"))

	configContents, err := os.ReadFile(filepath.Join(target, ".bruin.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(configContents), "earthquake_monitoring.duckdb")
	assert.Contains(t, string(configContents), "earthquake_monitoring_production.duckdb")
	assert.Contains(t, string(configContents), "production:")

	parsed, err := NewRenartPipelineBuilder(afero.NewOsFs()).CreatePipelineFromPath(
		context.Background(),
		filepath.Join(target, "earthquake_monitoring"),
		pipeline.WithMutate(),
	)
	require.NoError(t, err)
	require.NotEmpty(t, parsed.LegacyID)

	declarations, err := scheduler.NewScheduleDeclarationStore(
		filepath.Join(target, ".renart", "schedules.yml"),
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
	assert.EqualValues(t, 3, byEnvironment["default"].Variables["min_magnitude"])
	assert.Equal(t, "15 * * * *", byEnvironment["production"].Cron)
	assert.Equal(t, scheduler.CatchupRunOnce, byEnvironment["production"].CatchupPolicy)
	assert.EqualValues(t, 6, byEnvironment["production"].Variables["notable_magnitude"])
}

func TestScaffoldProjectIntoWorkspaceWithBareDotGitDirectory(t *testing.T) {
	t.Parallel()

	// The server can be started on a directory whose .git exists but holds no
	// repository yet (the e2e fixtures do exactly this); scaffolding must
	// initialize it rather than fail.
	target := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(target, ".git"), 0o755))

	result, err := ScaffoldProject(ScaffoldProjectRequest{
		TargetDir: target,
		Template:  ProjectTemplateEmpty,
	})
	require.NoError(t, err)
	assert.True(t, result.GitInitialized)
	assert.FileExists(t, filepath.Join(target, "analytics", "pipeline.yml"))
	assert.FileExists(t, filepath.Join(target, "analytics", "assets", "example.sql"))
	assert.FileExists(t, filepath.Join(target, ".gitignore"))

	repo, err := gogit.PlainOpen(target)
	require.NoError(t, err)
	head, err := repo.Head()
	require.NoError(t, err)
	assert.Equal(t, "main", head.Name().Short())
}

func TestScaffoldProjectKeepsExistingRepoAndConnections(t *testing.T) {
	t.Parallel()

	target := t.TempDir()
	_, err := gogit.PlainInit(target, false)
	require.NoError(t, err)
	existingConfig := "default_environment: default\nenvironments:\n  default:\n    connections:\n      duckdb:\n        - name: duckdb-default\n          path: /somewhere/else.duckdb\n"
	require.NoError(t, os.WriteFile(filepath.Join(target, ".bruin.yml"), []byte(existingConfig), 0o644))

	result, err := ScaffoldProject(ScaffoldProjectRequest{
		TargetDir: target,
		Template:  ProjectTemplateChessDemo,
	})
	require.NoError(t, err)
	assert.False(t, result.GitInitialized)

	configContents, err := os.ReadFile(filepath.Join(target, ".bruin.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(configContents), "/somewhere/else.duckdb", "existing duckdb-default connection must be preserved")
	assert.NotContains(t, string(configContents), "duckdb-files/chess.duckdb")

	// No commit was made: the repository has no HEAD and the files stay
	// visible as uncommitted changes for the user to review.
	repo, err := gogit.PlainOpen(target)
	require.NoError(t, err)
	_, err = repo.Head()
	assert.Error(t, err)

	performanceContents, err := os.ReadFile(filepath.Join(target, "chess", "assets", "chess", "player_performance.sql"))
	require.NoError(t, err)
	assert.Contains(t, string(performanceContents), "FROM chess.game_results")
	assert.NotContains(t, string(performanceContents), "quickstart")
}

func TestScaffoldProjectRejectsUnknownTemplateAndExistingPipeline(t *testing.T) {
	t.Parallel()

	target := t.TempDir()
	_, err := ScaffoldProject(ScaffoldProjectRequest{TargetDir: target, Template: "demo:nope"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown project template")

	require.NoError(t, os.MkdirAll(filepath.Join(target, "analytics"), 0o755))
	_, err = ScaffoldProject(ScaffoldProjectRequest{TargetDir: target, Template: ProjectTemplateEmpty})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestProjectTemplatesListsAllTemplates(t *testing.T) {
	t.Parallel()

	templates := ProjectTemplates()
	ids := make([]string, 0, len(templates))
	for _, tpl := range templates {
		ids = append(ids, tpl.ID)
		assert.NotEmpty(t, tpl.Title)
		assert.NotEmpty(t, tpl.Category)
		assert.NotEmpty(t, tpl.Features)
		if tpl.ID != ProjectTemplateBare {
			assert.NotEmpty(t, tpl.PipelineName)
			assert.NotEmpty(t, tpl.AssetNames)
		}
	}
	assert.Equal(t, []string{
		ProjectTemplateProductDemo,
		ProjectTemplateOperationsDemo,
		ProjectTemplateEarthquakeDemo,
		ProjectTemplatePythonDemo,
		ProjectTemplateJinjaDemo,
		ProjectTemplateChessDemo,
		ProjectTemplateRetailDemo,
		ProjectTemplateEmpty,
		ProjectTemplateBare,
	}, ids)

	offline := map[string]bool{}
	for _, tpl := range templates {
		offline[tpl.ID] = tpl.Offline
	}
	assert.False(t, offline[ProjectTemplateChessDemo])
	assert.True(t, offline[ProjectTemplateProductDemo])
	assert.True(t, offline[ProjectTemplateOperationsDemo])
	assert.False(t, offline[ProjectTemplateEarthquakeDemo])
	assert.False(t, offline[ProjectTemplatePythonDemo])
	assert.True(t, offline[ProjectTemplateJinjaDemo])
	assert.False(t, offline[ProjectTemplateRetailDemo])
	assert.True(t, offline[ProjectTemplateEmpty])
}

func TestScaffoldedTemplatesParseAsPipelines(t *testing.T) {
	t.Parallel()

	for _, tpl := range ProjectTemplates() {
		if tpl.PipelineName == "" {
			continue
		}
		t.Run(tpl.ID, func(t *testing.T) {
			t.Parallel()

			target := t.TempDir()
			result, err := ScaffoldProject(ScaffoldProjectRequest{
				TargetDir:     target,
				Template:      tpl.ID,
				NewRepository: true,
			})
			require.NoError(t, err)
			require.Equal(t, tpl.PipelineName, result.PipelinePath)

			parsed, err := NewRenartPipelineBuilder(afero.NewOsFs()).
				CreatePipelineFromPath(context.Background(), filepath.Join(target, tpl.PipelineName), pipeline.WithMutate())
			require.NoError(t, err)
			require.Equal(t, tpl.PipelineName, parsed.Name)

			names := make([]string, 0, len(parsed.Assets))
			for _, asset := range parsed.Assets {
				names = append(names, asset.Name)
				if tpl.ID == ProjectTemplateRetailDemo && strings.HasPrefix(asset.Name, "raw.") {
					assert.Equal(t, pipeline.AssetTypeDuckDBSeed, asset.Type, asset.Name)
				}
			}
			assert.ElementsMatch(t, tpl.AssetNames, names)

			// Every declared dependency must resolve inside the pipeline.
			for _, asset := range parsed.Assets {
				for _, upstream := range asset.Upstreams {
					assert.Contains(t, names, upstream.Value, "%s depends on %s", asset.Name, upstream.Value)
				}
			}
		})
	}
}
