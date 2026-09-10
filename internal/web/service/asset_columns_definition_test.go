package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	duck "github.com/bruin-data/bruin/pkg/duckdb"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/query"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInferAssetColumnsFromDefinition checks the asset-as-source-of-truth path:
// a downstream asset's columns are derived from its rendered SQL plus the
// declared columns of its upstream asset — no database is involved.
func TestInferAssetColumnsFromDefinition(t *testing.T) {
	workspaceRoot := t.TempDir()
	pipelineRoot := filepath.Join(workspaceRoot, "analytics")
	assetsRoot := filepath.Join(pipelineRoot, "assets")
	require.NoError(t, os.MkdirAll(assetsRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte(strings.TrimSpace(`
name: analytics
schedule: daily
start_date: "2024-01-01"
default_connections:
  duckdb: duckdb-default
`)+"\n"), 0o644))
	// Upstream asset with declared columns — the source of truth for types.
	require.NoError(t, os.WriteFile(filepath.Join(assetsRoot, "customers.sql"), []byte(strings.TrimSpace(`
/* @bruin
name: analytics.customers
type: duckdb.sql
materialization:
  type: view
columns:
  - name: customer_id
    type: INTEGER
  - name: customer_name
    type: VARCHAR
@bruin */

select 1 as customer_id, 'Ada' as customer_name
`)+"\n"), 0o644))
	// Downstream selecting from the upstream plus a computed column.
	require.NoError(t, os.WriteFile(filepath.Join(assetsRoot, "report.sql"), []byte(strings.TrimSpace(`
/* @bruin
name: analytics.report
type: duckdb.sql
materialization:
  type: view
depends:
  - analytics.customers
@bruin */

select customer_id, upper(customer_name) as shout from analytics.customers
`)+"\n"), 0o644))

	service := NewAssetService(AssetDependencies{
		WorkspaceRoot:                workspaceRoot,
		ResolveAssetByID:             newAssetTestResolver(workspaceRoot).ResolveAssetByID,
		SuppressWatcher:              func(string) {},
		PushWorkspaceUpdateImmediate: func(context.Context, string, string) {},
	})

	cols, apiErr := service.InferAssetColumnsFromDefinition(context.Background(), EncodeID("analytics/assets/report.sql"))
	require.Nil(t, apiErr)

	byName := map[string]string{}
	for _, c := range cols {
		byName[c.Name] = c.Type
	}
	// customer_id resolves to its upstream asset's declared type
	assert.Equal(t, "INTEGER", byName["customer_id"])
	// computed column gets a type from Golyglot's semantic analysis
	assert.Equal(t, "VARCHAR", byName["shout"])
}

func TestInferAssetColumnsFromPartitionedParquetGlob(t *testing.T) {
	workspaceRoot := t.TempDir()
	pipelineRoot := filepath.Join(workspaceRoot, "analytics")
	assetsRoot := filepath.Join(pipelineRoot, "assets")
	partitionDir := filepath.Join(workspaceRoot, "my_directory", "day=2026-09-01")
	require.NoError(t, os.MkdirAll(assetsRoot, 0o755))
	require.NoError(t, os.MkdirAll(partitionDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte(strings.TrimSpace(`
name: analytics
schedule: daily
start_date: "2024-01-01"
default_connections:
  duckdb: duckdb-default
`)+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(assetsRoot, "partitioned.sql"), []byte(strings.TrimSpace(`
/* @bruin
name: analytics.partitioned
type: duckdb.sql
materialization:
  type: view
@bruin */

SELECT
  *
FROM "./my_directory/day=*/example.parquet"
`)+"\n"), 0o644))

	parquetPath := filepath.Join(partitionDir, "example.parquet")
	client, err := duck.NewClient(duck.Config{Path: ""})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	escapedPath := strings.ReplaceAll(filepath.ToSlash(parquetPath), "'", "''")
	require.NoError(t, client.RunQueryWithoutResult(t.Context(), &query.Query{
		Query: "copy (select 1::integer as id, 'Ada'::varchar as name) to '" + escapedPath + "' (format parquet)",
	}))

	service := NewAssetService(AssetDependencies{
		WorkspaceRoot:                workspaceRoot,
		ResolveAssetByID:             newAssetTestResolver(workspaceRoot).ResolveAssetByID,
		SuppressWatcher:              func(string) {},
		PushWorkspaceUpdateImmediate: func(context.Context, string, string) {},
	})
	columns, apiErr := service.InferAssetColumnsFromDefinition(
		context.Background(), EncodeID("analytics/assets/partitioned.sql"),
	)
	require.Nil(t, apiErr)
	byName := make(map[string]string, len(columns))
	for _, column := range columns {
		byName[column.Name] = column.Type
	}
	assert.Equal(t, "DATE", byName["day"])
	assert.Equal(t, "INTEGER", byName["id"])
	assert.Equal(t, "VARCHAR", byName["name"])
}

func TestInferAPIAssetColumnsFromOpenAPIDefinition(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`openapi: 3.0.3
paths:
  /games:
    get:
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
                properties:
                  games:
                    type: array
                    items:
                      type: object
                      properties:
                        id:
                          type: integer
                        white_username:
                          type: string
                        rated:
                          type: boolean
`))
	}))
	defer server.Close()

	workspaceRoot := t.TempDir()
	pipelineRoot := filepath.Join(workspaceRoot, "quickstart")
	assetsRoot := filepath.Join(pipelineRoot, "assets")
	require.NoError(t, os.MkdirAll(assetsRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte("name: quickstart\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(assetsRoot, "games.asset.yml"), []byte(`name: quickstart.games
type: api

parameters:
  openapi:
    url: `+server.URL+`
  request:
    url: https://api.example.com/games
  response:
    records_path: games
`), 0o644))

	resolver := NewWorkspaceResolver(workspaceRoot, func(ctx context.Context, pipelinePath string) (*pipeline.Pipeline, error) {
		return NewRenartPipelineBuilder(afero.NewOsFs()).CreatePipelineFromPath(ctx, pipelinePath, pipeline.WithMutate())
	})
	service := NewAssetService(AssetDependencies{
		WorkspaceRoot:                workspaceRoot,
		ResolveAssetByID:             resolver.ResolveAssetByID,
		SuppressWatcher:              func(string) {},
		PushWorkspaceUpdateImmediate: func(context.Context, string, string) {},
	})

	cols, apiErr := service.InferAssetColumnsFromDefinition(context.Background(), EncodeID("quickstart/assets/games.asset.yml"))
	require.Nil(t, apiErr)

	byName := map[string]string{}
	for _, c := range cols {
		byName[c.Name] = c.Type
	}
	assert.Equal(t, "integer", byName["id"])
	assert.Equal(t, "boolean", byName["rated"])
	assert.Equal(t, "string", byName["white_username"])
}

func TestRefreshSeedColumnsFromLocalFileWithSling(t *testing.T) {
	workspaceRoot := t.TempDir()
	pipelineRoot := filepath.Join(workspaceRoot, "analytics")
	assetsRoot := filepath.Join(pipelineRoot, "assets")
	require.NoError(t, os.MkdirAll(assetsRoot, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(pipelineRoot, "pipeline.yml"),
		[]byte("name: analytics\ndefault_connections:\n  duckdb: duckdb-default\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(assetsRoot, "customers.csv"),
		[]byte("customer_id,customer_name,created_at\n1,Ada,2026-07-15\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(assetsRoot, "customers.asset.yml"),
		[]byte("name: analytics.customers\ntype: duckdb.seed\nparameters:\n  path: ./customers.csv\n  file_type: csv\n"),
		0o644,
	))

	capturePath := filepath.Join(workspaceRoot, "sling-args.txt")
	fakeSling := filepath.Join(workspaceRoot, "fake-sling")
	require.NoError(t, os.WriteFile(fakeSling, []byte(`#!/bin/sh
printf '%s\n' "$*" > "$CAPTURE_PATH"
printf '%s\n' '{"fields":["File","ID","Column","Native Type","General Type"],"rows":[["customers.csv",1,"customer_id","-","bigint"],["customers.csv",2,"customer_name","-","text"],["customers.csv",3,"created_at","-","date"]]}'
`), 0o755))
	t.Setenv("RENART_SLING_BINARY", fakeSling)
	t.Setenv("CAPTURE_PATH", capturePath)

	service := NewAssetService(AssetDependencies{
		Fs:                           afero.NewOsFs(),
		WorkspaceRoot:                workspaceRoot,
		ResolveAssetByID:             newAssetTestResolver(workspaceRoot).ResolveAssetByID,
		SuppressWatcher:              func(string) {},
		PushWorkspaceUpdateImmediate: func(context.Context, string, string) {},
	})
	result, apiErr := service.RefreshAssetColumnsFromDefinition(
		context.Background(),
		EncodeID("analytics/assets/customers.asset.yml"),
	)
	require.Nil(t, apiErr)
	require.Len(t, result.Columns, 3)
	assert.Equal(t, "customer_id", result.Columns[0].Name)
	assert.Equal(t, "bigint", result.Columns[0].Type)
	assert.Equal(t, "created_at", result.Columns[2].Name)
	assert.Equal(t, "date", result.Columns[2].Type)

	args, err := os.ReadFile(capturePath)
	require.NoError(t, err)
	assert.Contains(t, string(args), "conns discover LOCAL")
	assert.Contains(t, string(args), "--columns")
	assert.Contains(t, string(args), filepath.Join(assetsRoot, "customers.csv"))

	definition, err := os.ReadFile(filepath.Join(assetsRoot, "customers.asset.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(definition), "name: customer_id")
	assert.Contains(t, string(definition), "type: bigint")
}

func TestSeedColumnDiscoveryIsContentCachedAndSingleFlight(t *testing.T) {
	workspaceRoot := t.TempDir()
	seedPath := filepath.Join(workspaceRoot, "customers.csv")
	require.NoError(t, os.WriteFile(seedPath, []byte("id,name\n1,Ada\n"), 0o644))
	countPath := filepath.Join(workspaceRoot, "calls.txt")
	fakeSling := filepath.Join(workspaceRoot, "fake-sling")
	require.NoError(t, os.WriteFile(fakeSling, []byte(`#!/bin/sh
printf x >> "$COUNT_PATH"
sleep 0.2
printf '%s\n' '{"fields":["Column","General Type"],"rows":[["id","bigint"],["name","text"]]}'
`), 0o755))
	t.Setenv("RENART_SLING_BINARY", fakeSling)
	t.Setenv("COUNT_PATH", countPath)

	service := NewAssetService(AssetDependencies{WorkspaceRoot: workspaceRoot})
	const callers = 16
	var wait sync.WaitGroup
	wait.Add(callers)
	errors := make(chan error, callers)
	for range callers {
		go func() {
			defer wait.Done()
			columns, _, err := service.discoverSeedColumns(context.Background(), seedPath)
			if err == nil && len(columns) != 2 {
				err = fmt.Errorf("expected two columns, got %d", len(columns))
			}
			errors <- err
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	count, err := os.ReadFile(countPath)
	require.NoError(t, err)
	assert.Equal(t, "x", string(count), "concurrent observations must share one Sling process")

	_, _, err = service.discoverSeedColumns(context.Background(), seedPath)
	require.NoError(t, err)
	count, err = os.ReadFile(countPath)
	require.NoError(t, err)
	assert.Equal(t, "x", string(count), "unchanged content must use the cache")

	require.NoError(t, os.WriteFile(seedPath, []byte("id,name\n2,Grace\n"), 0o644))
	_, _, err = service.discoverSeedColumns(context.Background(), seedPath)
	require.NoError(t, err)
	count, err = os.ReadFile(countPath)
	require.NoError(t, err)
	assert.Equal(t, "xx", string(count), "changed content must be inspected again")
}
