package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/sqllsp"
)

func TestPureAuthoringSchemaDoesNotFetchOpenAPI(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`openapi: 3.0.3
paths: {}`))
	}))
	defer server.Close()

	openAPIOnly := &pipeline.Asset{
		Name: "analytics.remote_api", Type: pipeline.AssetType(apiAssetType),
		ExecutableFile: pipeline.ExecutableFile{Content: `name: analytics.remote_api
type: api
parameters:
  openapi:
    url: ` + server.URL + `
  request:
    url: https://example.invalid/records
`},
	}
	resolver := newAssetDefinitionSchemaResolver(&pipeline.Pipeline{Assets: []*pipeline.Asset{openAPIOnly}})
	assert.Empty(t, resolver.Available(context.Background(), openAPIOnly))
	assert.Equal(t, int32(0), requests.Load(), "pure authoring must not fetch an OpenAPI URL")

	withFields := *openAPIOnly
	withFields.ExecutableFile.Content += `  response:
    fields:
      id: id
      display_name: profile.name
`
	columns := newAssetDefinitionSchemaResolver(&pipeline.Pipeline{Assets: []*pipeline.Asset{&withFields}}).Available(context.Background(), &withFields)
	require.Len(t, columns, 2)
	assert.Equal(t, []string{"display_name", "id"}, []string{columns[0].Name, columns[1].Name})
	assert.Equal(t, int32(0), requests.Load())
}

func TestPureAuthoringSchemaResolvesLoadChainsAndStopsCycles(t *testing.T) {
	source := &pipeline.Asset{
		Name: "analytics.api", Type: pipeline.AssetType(apiAssetType),
		ExecutableFile: pipeline.ExecutableFile{Content: `name: analytics.api
type: api
parameters:
  response:
    fields:
      id: id
`},
	}
	first := &pipeline.Asset{
		Name: "analytics.first_load", Type: pipeline.AssetType(loadAssetType),
		Parameters: pipeline.ParameterMap{loadParamSourceTable: source.Name},
	}
	second := &pipeline.Asset{
		Name: "analytics.second_load", Type: pipeline.AssetType(loadAssetType),
		Upstreams: []pipeline.Upstream{{Type: "asset", Value: first.Name}},
	}
	pp := &pipeline.Pipeline{Assets: []*pipeline.Asset{source, first, second}}
	columns := newAssetDefinitionSchemaResolver(pp).Available(context.Background(), second)
	require.Len(t, columns, 1)
	assert.Equal(t, "id", columns[0].Name)

	left := &pipeline.Asset{Name: "analytics.left", Type: pipeline.AssetType(loadAssetType), Parameters: pipeline.ParameterMap{loadParamSourceTable: "analytics.right"}}
	right := &pipeline.Asset{Name: "analytics.right", Type: pipeline.AssetType(loadAssetType), Parameters: pipeline.ParameterMap{loadParamSourceTable: "analytics.left"}}
	cycle := &pipeline.Pipeline{Assets: []*pipeline.Asset{left, right}}
	assert.Empty(t, newAssetDefinitionSchemaResolver(cycle).Available(context.Background(), left))
}

func TestAuthoringSchemaGraphPropagatesSQLThroughLoad(t *testing.T) {
	source := &pipeline.Asset{
		Name: "analytics.source", Type: pipeline.AssetTypeDuckDBQuery,
		Materialization: pipeline.Materialization{Type: pipeline.MaterializationTypeView},
	}
	load := &pipeline.Asset{
		Name: "analytics.mirror", Type: pipeline.AssetType(loadAssetType),
		Parameters: pipeline.ParameterMap{loadParamSourceTable: source.Name},
	}
	report := &pipeline.Asset{
		Name: "analytics.report", Type: pipeline.AssetTypeDuckDBQuery,
		Materialization: pipeline.Materialization{Type: pipeline.MaterializationTypeView},
	}
	pp := &pipeline.Pipeline{Assets: []*pipeline.Asset{source, load, report}}
	nodes := []sqllsp.AssetNode{
		{ID: source.Name, Name: source.Name, Kind: "sql_model", Dialect: "duckdb", URI: "file:///source.sql"},
		{ID: load.Name, Name: load.Name, Kind: "load", URI: "file:///mirror.asset.yml"},
		{ID: report.Name, Name: report.Name, Kind: "sql_model", Dialect: "duckdb", URI: "file:///report.sql"},
	}
	graph := sqllsp.GraphFromRenartAssets("file:///workspace", nodes, nil)
	graph = resolveAuthoringSchemaGraph(context.Background(), graph, pp, []sqllsp.InferenceAsset{
		{ID: source.Name, Name: source.Name, URI: "file:///source.sql", SQL: "select 1::BIGINT as id", Dialect: "duckdb"},
		{ID: report.Name, Name: report.Name, URI: "file:///report.sql", SQL: "select id from analytics.mirror", Dialect: "duckdb"},
	})

	assert.Equal(t, []sqllsp.ColumnInfo{{Name: "id", Type: "BIGINT"}}, authoringGraphColumns(graph, load.Name))
	assert.Equal(t, []sqllsp.ColumnInfo{{Name: "id", Type: "BIGINT"}}, authoringGraphColumns(graph, report.Name))
}

func authoringGraphColumns(graph sqllsp.CanonicalGraph, relationName string) []sqllsp.ColumnInfo {
	relationID := ""
	for _, relation := range graph.Relations {
		if relation.Name == relationName {
			relationID = relation.ID
			break
		}
	}
	var result []sqllsp.ColumnInfo
	for _, layer := range graph.Schemas {
		if layer.RelationID == relationID {
			result = layer.Columns
		}
	}
	return result
}
