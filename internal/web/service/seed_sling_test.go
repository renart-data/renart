package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bruinexecutor "github.com/bruin-data/bruin/pkg/executor"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/scheduler"
	gogit "github.com/go-git/go-git/v5"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlingSeedOperatorRunsSeedThroughSling(t *testing.T) {
	workspaceRoot := t.TempDir()
	assetDir := filepath.Join(workspaceRoot, "analytics", "assets", "analytics")
	require.NoError(t, os.MkdirAll(assetDir, 0o755))
	seedPath := filepath.Join(assetDir, "customers.csv")
	require.NoError(t, os.WriteFile(seedPath, []byte("customer_id,customer_name\n1,Ada\n"), 0o644))

	fakeSling := filepath.Join(workspaceRoot, "fake-sling")
	require.NoError(t, os.WriteFile(fakeSling, []byte("#!/bin/sh\nprintf 'target=%s\\n' \"$RENART_SEED_TARGET\"\nprintf 'args=%s\\n' \"$*\"\n"), 0o755))
	t.Setenv("RENART_SLING_BINARY", fakeSling)

	asset := &pipeline.Asset{
		Name:       "analytics.customers",
		Type:       pipeline.AssetTypeDuckDBSeed,
		Connection: "duckdb-default",
		Materialization: pipeline.Materialization{
			Strategy: pipeline.MaterializationStrategyTruncateInsert,
		},
		Parameters: pipeline.ParameterMap{
			"path":           "./customers.csv",
			"file_type":      "csv",
			"enforce_schema": "true",
		},
		Columns: []pipeline.Column{
			{Name: "customer_id", Type: "integer", PrimaryKey: true},
			{Name: "customer_name", Type: "varchar(100)"},
		},
		ExecutableFile: pipeline.ExecutableFile{Path: filepath.Join(assetDir, "customers.asset.yml")},
	}
	pl := &pipeline.Pipeline{Assets: []*pipeline.Asset{asset}}
	instance := &scheduler.AssetInstance{Pipeline: pl, Asset: asset}
	manager := &stubConnectionManager{conn: "duckdb:///tmp/seed-target.duckdb"}
	operator := newSlingSeedOperator(manager, nil, workspaceRoot)

	var output bytes.Buffer
	ctx := context.WithValue(context.Background(), bruinexecutor.KeyPrinter, &output)
	require.NoError(t, operator.Run(ctx, instance))

	text := output.String()
	assert.Contains(t, text, "target=duckdb:///tmp/seed-target.duckdb")
	assert.Contains(t, text, "run --src-stream file://"+filepath.ToSlash(seedPath))
	assert.Contains(t, text, `--src-options {"format":"csv"}`)
	assert.Contains(t, text, "--tgt-conn "+seedTargetConnectionEnv)
	assert.Contains(t, text, "--tgt-object analytics.customers --mode truncate")
	assert.Contains(t, text, `--tgt-options {"column_casing":"snake"}`)
	assert.Contains(t, text, `--select customer_id,customer_name --columns {"customer_id":"integer","customer_name":"string(100)"} --primary-key customer_id`)
	assert.NotContains(t, text, "ingestr")
}

func TestSlingSeedOperatorPassesDatabricksConnectionPayload(t *testing.T) {
	workspaceRoot := t.TempDir()
	assetDir := filepath.Join(workspaceRoot, "analytics", "assets")
	require.NoError(t, os.MkdirAll(assetDir, 0o755))
	seedPath := filepath.Join(assetDir, "customers.csv")
	require.NoError(t, os.WriteFile(seedPath, []byte("customer_id\n1\n"), 0o644))

	fakeSling := filepath.Join(workspaceRoot, "fake-sling")
	require.NoError(t, os.WriteFile(fakeSling, []byte("#!/bin/sh\nprintf 'target=%s\\nargs=%s\\n' \"$RENART_SEED_TARGET\" \"$*\"\n"), 0o755))
	t.Setenv("RENART_SLING_BINARY", fakeSling)

	asset := &pipeline.Asset{
		Name:           "analytics.customers",
		Type:           pipeline.AssetType("databricks.seed"),
		Connection:     "databricks-default",
		Parameters:     pipeline.ParameterMap{"path": "./customers.csv", "file_type": "csv"},
		ExecutableFile: pipeline.ExecutableFile{Path: filepath.Join(assetDir, "customers.asset.yml")},
	}
	instance := &scheduler.AssetInstance{
		Pipeline: &pipeline.Pipeline{Assets: []*pipeline.Asset{asset}},
		Asset:    asset,
	}
	operator := newSlingSeedOperator(databricksPATTestManager(), nil, workspaceRoot)

	var output bytes.Buffer
	ctx := context.WithValue(context.Background(), bruinexecutor.KeyPrinter, &output)
	require.NoError(t, operator.Run(ctx, instance))

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	require.Len(t, lines, 2)
	parsed := requireDatabricksSlingPayload(t, strings.TrimPrefix(lines[0], "target="))
	assert.Equal(t, "workspace.cloud.databricks.com:443", parsed.Host)
	assert.Contains(t, lines[1], `--tgt-options {"column_casing":"snake","use_bulk":false}`)
	assert.NotContains(t, lines[1], "test-token")
}

func TestParsedSeedRetainsConfiguredMaterialization(t *testing.T) {
	workspaceRoot := t.TempDir()
	_, err := gogit.PlainInit(workspaceRoot, false)
	require.NoError(t, err)
	pipelineRoot := filepath.Join(workspaceRoot, "analytics")
	assetDir := filepath.Join(pipelineRoot, "assets")
	require.NoError(t, os.MkdirAll(assetDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(pipelineRoot, "pipeline.yml"),
		[]byte("name: analytics\ndefault_connections:\n  duckdb: duckdb-default\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(assetDir, "customers.csv"),
		[]byte("customer_id\n1\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(assetDir, "customers.asset.yml"),
		[]byte(`name: analytics.customers
type: duckdb.seed
parameters:
  path: ./customers.csv
materialization:
  type: table
  strategy: truncate+insert
`),
		0o644,
	))

	parsed, err := NewRenartPipelineBuilder(afero.NewOsFs()).CreatePipelineFromPath(
		context.Background(),
		pipelineRoot,
		pipeline.WithMutate(),
	)
	require.NoError(t, err)
	require.Len(t, parsed.Assets, 1)
	assert.Equal(t, pipeline.MaterializationTypeTable, parsed.Assets[0].Materialization.Type)
	assert.Equal(t, pipeline.MaterializationStrategyTruncateInsert, parsed.Assets[0].Materialization.Strategy)
}

func TestResolveSlingSeedSource(t *testing.T) {
	assetDir := t.TempDir()
	jsonlPath := filepath.Join(assetDir, "events.data")
	require.NoError(t, os.WriteFile(jsonlPath, []byte("{\"id\":1}\n"), 0o644))

	stream, options, err := resolveSlingSeedSource("./events.data", "jsonl", assetDir)
	require.NoError(t, err)
	assert.Equal(t, "file://"+filepath.ToSlash(jsonlPath), stream)
	assert.Equal(t, `{"format":"jsonlines"}`, options)

	stream, options, err = resolveSlingSeedSource("https://example.com/events.ndjson?token=secret", "", assetDir)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/events.ndjson?token=secret", stream)
	assert.Equal(t, `{"format":"jsonlines"}`, options)

	_, _, err = resolveSlingSeedSource("s3://bucket/events.csv", "csv", assetDir)
	require.ErrorContains(t, err, "http or https")
}

func TestSlingSeedColumnArgsCanDisableSchemaEnforcement(t *testing.T) {
	asset := &pipeline.Asset{
		Parameters: pipeline.ParameterMap{"enforce_schema": "false"},
		Columns:    []pipeline.Column{{Name: "id", Type: "integer"}},
	}
	args, err := slingSeedColumnArgs(asset)
	require.NoError(t, err)
	assert.Empty(t, args)
}

func TestDirectSeedExecutorsAllUseSling(t *testing.T) {
	executors, err := buildDirectMainExecutors(&stubConnectionManager{}, nil, nil, &pipeline.Pipeline{}, nil, nil, nil, nil, "", false, false, sensorModeOnce)
	require.NoError(t, err)

	for _, capability := range assetAuthoringCapabilities() {
		if capability.Kind != "seed" {
			continue
		}
		operator := executors[pipeline.AssetType(capability.Type)][scheduler.TaskInstanceTypeMain]
		assert.IsType(t, &slingSeedOperator{}, operator, capability.Type)
	}
}
