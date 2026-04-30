package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOnboardingImportDatabaseReturnsSchemaAssetPaths(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	runner := &stubRunRunner{output: []byte(`{"status":"ok"}`)}
	svc := NewOnboardingService(workspaceRoot, filepath.Join(workspaceRoot, ".bruin.yml"), runner)

	result := svc.ImportDatabase(context.Background(), OnboardingImportRequest{
		ConnectionName:  "postgres-default",
		EnvironmentName: "default",
		PipelineName:    "analytics",
		Schema:          "analytics",
		Tables:          []string{"analytics.orders"},
		CreateIfMissing: true,
	})

	require.Equal(t, "ok", result.Status)
	assert.Equal(t, []string{"analytics/assets/analytics/orders.asset.yml"}, result.AssetPaths)
	assert.Equal(t, []string{"patch", "fill-asset-dependencies", "analytics"}, runner.args)

	contents, err := os.ReadFile(filepath.Join(workspaceRoot, "analytics", "pipeline.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(contents), "name: analytics")
}

func TestCreateDuckDBQuickstartCreatesBruinDefaultAssetsAndDatabaseFile(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	runner := &stubRunRunner{output: []byte("quickstart run complete")}
	svc := NewOnboardingService(workspaceRoot, filepath.Join(workspaceRoot, ".bruin.yml"), runner)

	result := svc.CreateDuckDBQuickstart(context.Background(), OnboardingQuickstartRequest{
		EnvironmentName: "default",
		PipelineName:    "quickstart",
		ConnectionName:  "duckdb-default",
		DatabasePath:    "duckdb-files/chess_playground.duckdb",
		Materialize:     true,
	})

	require.Equal(t, "ok", result.Status)
	assert.Equal(t, []string{"run", "quickstart", "--env", "default"}, runner.args)
	assert.Equal(t, []string{
		"quickstart/assets/players.asset.yml",
		"quickstart/assets/games.asset.yml",
		"quickstart/assets/player_stats.sql",
		"quickstart/assets/my_python_asset.py",
	}, result.AssetPaths)

	configContents, err := os.ReadFile(filepath.Join(workspaceRoot, ".bruin.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(configContents), "duckdb-files/chess_playground.duckdb")
	assert.Contains(t, string(configContents), "chess-default")

	playersAsset, err := os.ReadFile(filepath.Join(workspaceRoot, "quickstart", "assets", "players.asset.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(playersAsset), "name: dataset.players")
	assert.Contains(t, string(playersAsset), "type: ingestr")
	assert.Contains(t, string(playersAsset), "source_connection: chess-default")
	assert.Contains(t, string(playersAsset), "source_table: profiles")

	gamesAsset, err := os.ReadFile(filepath.Join(workspaceRoot, "quickstart", "assets", "games.asset.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(gamesAsset), "name: quickstart.games")
	assert.Contains(t, string(gamesAsset), "source_table: games")

	statsAsset, err := os.ReadFile(filepath.Join(workspaceRoot, "quickstart", "assets", "player_stats.sql"))
	require.NoError(t, err)
	assert.Contains(t, string(statsAsset), "name: dataset.player_stats")
	assert.Contains(t, string(statsAsset), "type: duckdb.sql")
	assert.Contains(t, string(statsAsset), "depends:")
	assert.Contains(t, string(statsAsset), "- dataset.players")
	assert.Contains(t, string(statsAsset), "- quickstart.games")
	assert.Contains(t, string(statsAsset), "players_white")
	assert.Contains(t, string(statsAsset), "players_black")
	assert.Contains(t, string(statsAsset), "games_white")
	assert.Contains(t, string(statsAsset), "games_black")
	assert.NotContains(t, string(statsAsset), "columns:")
	assert.NotContains(t, string(statsAsset), "custom_checks:")

	pythonAsset, err := os.ReadFile(filepath.Join(workspaceRoot, "quickstart", "assets", "my_python_asset.py"))
	require.NoError(t, err)
	assert.Contains(t, string(pythonAsset), "name: my_python_asset")
	assert.Contains(t, string(pythonAsset), "print('hello world')")

	_, err = os.Stat(filepath.Join(workspaceRoot, "duckdb-files"))
	require.NoError(t, err)
}

func TestCreateDuckDBQuickstartRemovesStaleEmptyDuckDBPlaceholder(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceRoot, "duckdb-files"), 0o755))
	databasePath := filepath.Join(workspaceRoot, "duckdb-files", "chess_playground.duckdb")
	require.NoError(t, os.WriteFile(databasePath, nil, 0o644))

	runner := &stubRunRunner{output: []byte("quickstart run complete")}
	svc := NewOnboardingService(workspaceRoot, filepath.Join(workspaceRoot, ".bruin.yml"), runner)

	result := svc.CreateDuckDBQuickstart(context.Background(), OnboardingQuickstartRequest{
		EnvironmentName: "default",
		PipelineName:    "quickstart",
		ConnectionName:  "duckdb-default",
		DatabasePath:    "duckdb-files/chess_playground.duckdb",
		Materialize:     true,
	})

	require.Equal(t, "ok", result.Status)
	_, err := os.Stat(databasePath)
	require.True(t, os.IsNotExist(err), "empty placeholder should be removed so DuckDB can create a valid database")
}

func TestCreateDuckDBQuickstartPreparesDuckDBPathRelativeToConfigFile(t *testing.T) {
	t.Parallel()

	repoRoot := t.TempDir()
	workspaceRoot := filepath.Join(repoRoot, "onboarding")
	require.NoError(t, os.MkdirAll(workspaceRoot, 0o755))

	runner := &stubRunRunner{output: []byte("quickstart run complete")}
	svc := NewOnboardingService(workspaceRoot, filepath.Join(repoRoot, ".bruin.yml"), runner)

	result := svc.CreateDuckDBQuickstart(context.Background(), OnboardingQuickstartRequest{
		EnvironmentName: "default",
		PipelineName:    "quickstart",
		ConnectionName:  "duckdb-default",
		DatabasePath:    "duckdb-files/chess_playground.duckdb",
		Materialize:     true,
	})

	require.Equal(t, "ok", result.Status)
	_, err := os.Stat(filepath.Join(repoRoot, "duckdb-files"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(workspaceRoot, "duckdb-files"))
	require.True(t, os.IsNotExist(err))
}
