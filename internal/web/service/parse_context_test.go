package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"renart/internal/sqlintelligence"
	"renart/internal/web/model"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssetTypeToDialect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		assetType pipeline.AssetType
		expected  string
	}{
		{pipeline.AssetTypeDuckDBQuery, "duckdb"},
		{pipeline.AssetTypePostgresQuery, "postgres"},
		{pipeline.AssetTypeBigqueryQuery, "bigquery"},
		{pipeline.AssetTypeSynapseQuery, "tsql"},
		{pipeline.AssetTypeMsSQLQuery, "tsql"},
		{pipeline.AssetTypeVerticaQuery, "postgres"},
		{pipeline.AssetTypeFabricQuery, "fabric"},
		{pipeline.AssetTypeFabricQueryLegacy, "fabric"},
		{pipeline.AssetTypeMySQLQuery, "mysql"},
		{pipeline.AssetTypeMySQLQuerySensor, "mysql"},
		{pipeline.AssetTypeOracleQuery, "oracle"},
		{pipeline.AssetTypeDuckDBQuerySensor, "duckdb"},
		{pipeline.AssetTypePostgresQuerySensor, "postgres"},
		{pipeline.AssetTypeMotherduckQuery, "duckdb"},
	}

	for _, tt := range tests {
		t.Run(string(tt.assetType), func(t *testing.T) {
			t.Parallel()
			dialect, err := AssetTypeToDialect(tt.assetType)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, dialect)
		})
	}
}

func TestAssetTypeToDialect_Unsupported(t *testing.T) {
	t.Parallel()

	_, err := AssetTypeToDialect(pipeline.AssetTypePython)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported asset type")
}

func TestParseContextServiceUsesSelectedConnectionDialect(t *testing.T) {
	t.Parallel()

	service := NewParseContextService(ParseContextDependencies{
		ResolveAssetByID: func(_ context.Context, _ string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "", nil, &pipeline.Asset{Type: pipeline.AssetTypeDuckDBQuery}, nil
		},
		CurrentState: func() model.WorkspaceState {
			return model.WorkspaceState{Connections: map[string]string{
				"databricks-default": "databricks",
			}}
		},
	})

	result, apiError := service.Parse(
		context.Background(),
		"asset-id",
		"select 1",
		nil,
		"databricks-default",
	)

	require.Nil(t, apiError)
	assert.Equal(t, "ok", result.Status)
	assert.Equal(t, "databricks", result.Dialect)
}

func TestBuildParseContextSchema_MergesSuggestedAndAssetColumns(t *testing.T) {
	t.Parallel()

	asset := &pipeline.Asset{
		Name: "marts.orders",
		Columns: []pipeline.Column{
			{Name: "order_id", Type: "integer"},
			{Name: "customer_id", Type: "integer"},
		},
	}

	schema := BuildParseContextSchema(asset, []ParseContextSchemaTable{
		{
			Name: "raw.customers",
			Columns: []ParseContextSchemaColumn{
				{Name: "id", Type: "integer"},
				{Name: "email", Type: "varchar"},
			},
		},
		{
			Name:    " ",
			Columns: []ParseContextSchemaColumn{{Name: "ignored", Type: "text"}},
		},
	})

	assert.Equal(t, map[string]string{"id": "integer", "email": "varchar"}, schema["raw.customers"])
	assert.Equal(t, map[string]string{"order_id": "integer", "customer_id": "integer"}, schema["marts.orders"])
	_, exists := schema[" "]
	assert.False(t, exists)
}

func TestBuildParseContextSchema_SkipsBlankColumnNames(t *testing.T) {
	t.Parallel()

	schema := BuildParseContextSchema(nil, []ParseContextSchemaTable{
		{
			Name: "raw.events",
			Columns: []ParseContextSchemaColumn{
				{Name: "", Type: "text"},
				{Name: "event_id", Type: "uuid"},
			},
		},
	})

	assert.Equal(t, map[string]string{"event_id": "uuid"}, schema["raw.events"])
}

func TestBuildParseContextSchema_MergesDuplicateSuggestionTables(t *testing.T) {
	t.Parallel()

	schema := BuildParseContextSchema(nil, []ParseContextSchemaTable{
		{
			Name: "quickstart.range_100",
			Columns: []ParseContextSchemaColumn{
				{Name: "range", Type: "bigint"},
				{Name: "bla", Type: "integer"},
			},
		},
		{
			Name: "quickstart.range_100",
			Columns: []ParseContextSchemaColumn{
				{Name: "range", Type: "bigint"},
			},
		},
	})

	assert.Equal(t, map[string]string{"range": "bigint", "bla": "integer"}, schema["quickstart.range_100"])
}

func TestBuildParseContextSchema_MergesUnqualifiedConnectionTableIntoQualifiedAsset(t *testing.T) {
	t.Parallel()

	schema := BuildParseContextSchema(nil, []ParseContextSchemaTable{
		{
			Name: "quickstart.range_100",
			Columns: []ParseContextSchemaColumn{
				{Name: "bla", Type: "integer"},
			},
		},
		{
			Name: "range_100",
			Columns: []ParseContextSchemaColumn{
				{Name: "range", Type: "bigint"},
			},
		},
	})

	assert.Equal(t, map[string]string{"range": "bigint", "bla": "integer"}, schema["quickstart.range_100"])
}

func TestBuildParseContextColumnSourceMethods_MergesDuplicateSuggestionTables(t *testing.T) {
	t.Parallel()

	sources := BuildParseContextColumnSourceMethods(nil, []ParseContextSchemaTable{
		{
			Name: "quickstart.range_100",
			Columns: []ParseContextSchemaColumn{
				{Name: "range", SourceMethods: []string{"workspace-load"}},
				{Name: "bla", SourceMethods: []string{"workspace-load"}},
			},
		},
		{
			Name: "quickstart.range_100",
			Columns: []ParseContextSchemaColumn{
				{Name: "range", SourceMethods: []string{"connection-column-discovery"}},
			},
		},
	})

	assert.ElementsMatch(t, []string{"workspace-load", "connection-column-discovery"}, sources["quickstart.range_100"]["range"])
	assert.Equal(t, []string{"workspace-load"}, sources["quickstart.range_100"]["bla"])
}

func TestBuildParseContextColumnSourceMethods_MergesUnqualifiedConnectionTableIntoQualifiedAsset(t *testing.T) {
	t.Parallel()

	sources := BuildParseContextColumnSourceMethods(nil, []ParseContextSchemaTable{
		{
			Name: "quickstart.range_100",
			Columns: []ParseContextSchemaColumn{
				{Name: "bla", SourceMethods: []string{"asset-sql-definition"}},
			},
		},
		{
			Name: "range_100",
			Columns: []ParseContextSchemaColumn{
				{Name: "range", SourceMethods: []string{"connection-column-discovery"}},
			},
		},
	})

	assert.Equal(t, []string{"asset-sql-definition"}, sources["quickstart.range_100"]["bla"])
	assert.Equal(t, []string{"connection-column-discovery"}, sources["quickstart.range_100"]["range"])
}

func TestExtractSQLDefinitionColumns_SelectWithoutFrom(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"plumbus", "blabli"}, ExtractSQLDefinitionColumns("select 1 as plumbus, 2 as blabli"))
}

func TestParseContextWithSchema_DuckDBPathReferenceDoesNotProduceUnresolvedTableDiagnostic(t *testing.T) {
	t.Parallel()

	parseContext, err := sqlintelligence.ParseContextWithSchema(
		`select * from "./customers.csv"`,
		"duckdb",
		sqlintelligence.Schema{
			"analytics.orders": {"order_id": "integer"},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, parseContext)

	assert.Empty(t, parseContext.Errors)
	assert.Len(t, parseContext.Tables, 1)
	assert.Equal(t, "./customers.csv", parseContext.Tables[0].Name)
	assert.NotContains(t, parseContext.Diagnostics, sqlintelligence.ParseContextDiagnostic{
		Message:  "Unresolved table: ./customers.csv",
		Severity: "error",
	})

	for _, diagnostic := range parseContext.Diagnostics {
		assert.NotEqual(t, "Unresolved table: ./customers.csv", diagnostic.Message)
	}
}

func TestParseContextWithSchema_SelectAliasInOrderByDoesNotProduceUnresolvedColumnDiagnostic(t *testing.T) {
	t.Parallel()

	parseContext, err := sqlintelligence.ParseContextWithSchema(
		`SELECT
  customers.customer_id,
  customers.customer_name,
  customers.city,
  count(orders.order_id) AS order_count,
  sum(orders.order_total) AS total_revenue
FROM quickstart.customers AS customers
JOIN quickstart.orders AS orders
  ON customers.customer_id = orders.customer_id
GROUP BY 1, 2, 3
ORDER BY total_revenue DESC`,
		"duckdb",
		sqlintelligence.Schema{
			"quickstart.customers": {
				"customer_id":   "integer",
				"customer_name": "varchar",
				"city":          "varchar",
			},
			"quickstart.orders": {
				"order_id":    "integer",
				"customer_id": "integer",
				"order_total": "double",
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, parseContext)

	assert.Empty(t, parseContext.Errors)
	for _, diagnostic := range parseContext.Diagnostics {
		assert.NotEqual(t, "Unresolved column: total_revenue", diagnostic.Message)
	}
}

func TestParseContextWithSchema_CTEAliasedColumnsResolveThroughOuterAlias(t *testing.T) {
	t.Parallel()

	parseContext, err := sqlintelligence.ParseContextWithSchema(
		`with cust as (
  SELECT
    customers.customer_id as id,
    customers.customer_name as name,
    customers.city as cit
  FROM quickstart.customers
)

SELECT
  city,
  cit,
  cust.cit,
  customers.id as id,
  customers.name as name,
  customers.cit as city,
  count(orders.order_id) AS order_count,
  sum(orders.order_total) AS total_revenue
FROM cust AS customers
JOIN quickstart.orders AS orders
  ON customers.id = orders.customer_id
GROUP BY 1, 2, 3
ORDER BY total_revenue DESC`,
		"duckdb",
		sqlintelligence.Schema{
			"quickstart.customers": {
				"customer_id":   "integer",
				"customer_name": "varchar",
				"city":          "varchar",
			},
			"quickstart.orders": {
				"order_id":    "integer",
				"customer_id": "integer",
				"order_total": "double",
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, parseContext)

	assert.Empty(t, parseContext.Errors)
	for _, diagnostic := range parseContext.Diagnostics {
		assert.NotEqual(t, "Unresolved column: customers.id", diagnostic.Message)
		assert.NotEqual(t, "Unresolved column: customers.name", diagnostic.Message)
		assert.NotEqual(t, "Unresolved column: customers.cit", diagnostic.Message)
		assert.NotEqual(t, "Unresolved column: cit", diagnostic.Message)
	}
	foundCityDiagnostic := false
	foundShadowedCTEDiagnostic := false
	for _, diagnostic := range parseContext.Diagnostics {
		if diagnostic.Message == "Unresolved column: city" {
			foundCityDiagnostic = true
		}
		if diagnostic.Message == "Unresolved table or alias: cust" {
			foundShadowedCTEDiagnostic = true
		}
	}
	assert.True(t, foundCityDiagnostic)
	assert.True(t, foundShadowedCTEDiagnostic)

	var cteReference *sqlintelligence.ParseContextTable
	for index := range parseContext.Tables {
		if parseContext.Tables[index].Name == "cust" && parseContext.Tables[index].Alias == "customers" {
			cteReference = &parseContext.Tables[index]
			break
		}
	}
	require.NotNil(t, cteReference)
	assert.Equal(t, "cte", cteReference.SourceKind)
	assert.Equal(t, "cust", cteReference.ResolvedName)
	assert.ElementsMatch(t, []sqlintelligence.SchemaColumn{
		{Name: "id"},
		{Name: "name"},
		{Name: "cit"},
	}, cteReference.Columns)
}

func TestParseContextWithSchema_CTEColumnsFromJSONExpressionsResolveThroughJoinAlias(t *testing.T) {
	t.Parallel()

	parseContext, err := sqlintelligence.ParseContextWithSchema(
		`WITH game_results AS (
    SELECT
        CASE
            WHEN g.white->>'result' = 'win' THEN g.white->>'@id'
            WHEN g.black->>'result' = 'win' THEN g.black->>'@id'
            ELSE NULL
            END AS winner_aid,
        g.white->>'@id' AS white_aid,
        g.black->>'@id' AS black_aid
    FROM chess_playground.games g
)

SELECT
    p.username,
    p.aid,
    COUNT(*) AS total_games,
    COUNT(CASE WHEN g.white_aid = p.aid AND g.winner_aid = p.aid THEN 1 END) AS white_wins,
    COUNT(CASE WHEN g.black_aid = p.aid AND g.winner_aid = p.aid THEN 1 END) AS black_wins,
    COUNT(CASE WHEN g.white_aid = p.aid THEN 1 END) AS white_games,
    COUNT(CASE WHEN g.black_aid = p.aid THEN 1 END) AS black_games
FROM chess_playground.profiles p
LEFT JOIN game_results g
       ON p.aid IN (g.white_aid, g.black_aid)
GROUP BY p.username, p.aid
ORDER BY total_games DESC`,
		"duckdb",
		sqlintelligence.Schema{
			"chess_playground.games": {
				"white": "json",
				"black": "json",
			},
			"chess_playground.profiles": {
				"username": "varchar",
				"aid":      "varchar",
			},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, parseContext)

	assert.Empty(t, parseContext.Errors)
	var gameResultsReference *sqlintelligence.ParseContextTable
	for index := range parseContext.Tables {
		if parseContext.Tables[index].Name == "game_results" && parseContext.Tables[index].Alias == "g" {
			gameResultsReference = &parseContext.Tables[index]
			break
		}
	}
	require.NotNil(t, gameResultsReference)
	assert.NotZero(t, gameResultsReference.ColumnRanges["white_aid"].Start)
	assert.NotZero(t, gameResultsReference.ColumnRanges["black_aid"].Start)
	assert.NotZero(t, gameResultsReference.ColumnRanges["winner_aid"].Start)

	for _, diagnostic := range parseContext.Diagnostics {
		assert.NotEqual(t, "Unresolved column: g.white_aid", diagnostic.Message)
		assert.NotEqual(t, "Unresolved column: g.black_aid", diagnostic.Message)
		assert.NotEqual(t, "Unresolved column: g.winner_aid", diagnostic.Message)
	}
}

func TestParseContextWithSchema_DescribeSubqueryColumnsAreKnown(t *testing.T) {
	t.Parallel()

	parseContext, err := sqlintelligence.ParseContextWithSchema(
		`select
    *
from (describe some_table)
where column_name = 'example_column'`,
		"duckdb",
		sqlintelligence.Schema{
			"some_table": {"example_column": "varchar"},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, parseContext)

	assert.Empty(t, parseContext.Errors)
	for _, diagnostic := range parseContext.Diagnostics {
		assert.NotEqual(t, "Unresolved column: column_name", diagnostic.Message)
	}
}

func TestParseContextWithSchema_CTEStarColumnsAreAvailableForOuterQuery(t *testing.T) {
	t.Parallel()

	parseContext, err := sqlintelligence.ParseContextWithSchema(
		`with blub as (select * from quickstart.range_100)

select rangee from blub`,
		"duckdb",
		sqlintelligence.Schema{
			"quickstart.range_100": {"range": "bigint"},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, parseContext)

	assert.Empty(t, parseContext.Errors)
	assert.Contains(t, diagnosticMessages(parseContext.Diagnostics), "Unresolved column: rangee")

	var blubReference *sqlintelligence.ParseContextTable
	for index := range parseContext.Tables {
		if parseContext.Tables[index].Name == "blub" {
			blubReference = &parseContext.Tables[index]
			break
		}
	}
	require.NotNil(t, blubReference)
	assert.Equal(t, "cte", blubReference.SourceKind)
	assert.ElementsMatch(t, []sqlintelligence.SchemaColumn{{Name: "range", Type: "bigint"}}, blubReference.Columns)
}

func TestParseContextWithSchema_SubqueryColumnsDoNotLeakToOuterQuery(t *testing.T) {
	t.Parallel()

	parseContext, err := sqlintelligence.ParseContextWithSchema(
		`with blub as (select * from quickstart.range_100)

select test, (select blub from quickstart.test) from blub`,
		"duckdb",
		sqlintelligence.Schema{
			"quickstart.range_100": {"range": "bigint"},
			"quickstart.test":      {"test": "integer"},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, parseContext)

	assert.Empty(t, parseContext.Errors)
	messages := diagnosticMessages(parseContext.Diagnostics)
	assert.Contains(t, messages, "Unresolved column: test")
	assert.Contains(t, messages, "Unresolved column: blub")
}

func TestParseContextWithSchema_SubqueryAndOuterSourcesExposeScopeRanges(t *testing.T) {
	t.Parallel()

	parseContext, err := sqlintelligence.ParseContextWithSchema(
		`with blub as (select * from quickstart.range_100)

select range, (select test from quickstart.test) from blub`,
		"duckdb",
		sqlintelligence.Schema{
			"quickstart.range_100": {"range": "bigint"},
			"quickstart.test":      {"test": "integer"},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, parseContext)

	var blubReference *sqlintelligence.ParseContextTable
	var testReference *sqlintelligence.ParseContextTable
	for index := range parseContext.Tables {
		switch parseContext.Tables[index].Name {
		case "blub":
			blubReference = &parseContext.Tables[index]
		case "quickstart.test":
			testReference = &parseContext.Tables[index]
		}
	}

	require.NotNil(t, blubReference)
	require.NotNil(t, blubReference.ScopeRange)
	require.NotNil(t, testReference)
	require.NotNil(t, testReference.ScopeRange)
	assert.True(t, rangeContainsPosition(*blubReference.ScopeRange, 3, 23), "outer source should be visible inside correlated subquery")
	assert.True(t, rangeContainsPosition(*testReference.ScopeRange, 3, 23), "inner source should be visible inside its subquery")
	assert.False(t, rangeContainsPosition(*testReference.ScopeRange, 3, 8), "inner source should not be visible in the outer select list")
}

func TestParseContextWithSchema_SubqueryCallLikeExpressionReportsUnknownColumn(t *testing.T) {
	t.Parallel()

	parseContext, err := sqlintelligence.ParseContextWithSchema(
		`with blub as (select *, 1 as a from quickstart.range_100)

select range, aa (select test from quickstart.test) from blub`,
		"duckdb",
		sqlintelligence.Schema{
			"quickstart.range_100": {"range": "bigint"},
			"quickstart.test":      {"test": "integer"},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, parseContext)

	assert.Empty(t, parseContext.Errors)
	assert.Contains(t, diagnosticMessages(parseContext.Diagnostics), "Unresolved column: aa")
}

func TestParseContextWithSchema_AssetDefinedButUnmaterializedColumnWarns(t *testing.T) {
	t.Parallel()

	parseContext, err := sqlintelligence.ParseContextWithSchema(
		`with blub as (select *, 1 as schabla from quickstart.range_100)

select range, schabla, (select test from quickstart.test), bla from blub`,
		"duckdb",
		sqlintelligence.Schema{
			"quickstart.range_100": {"range": "bigint", "bla": "integer"},
			"quickstart.test":      {"test": "integer"},
		},
		sqlintelligence.SchemaColumnSourceMethods{
			"quickstart.range_100": {
				"range": []string{"workspace-load", "connection-column-discovery"},
				"bla":   []string{"asset-sql-definition"},
			},
			"quickstart.test": {"test": []string{"workspace-load", "connection-column-discovery"}},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, parseContext)

	assert.Empty(t, parseContext.Errors)
	warnings := warningDiagnostics(parseContext.Diagnostics)
	require.Len(t, warnings, 1)
	assert.Equal(t, "Column 'bla' is defined in the asset 'quickstart.range_100', but it has not been materialized yet.", warnings[0].Message)
	require.NotNil(t, warnings[0].Range)
	assert.Equal(t, 3, warnings[0].Range.Line)
	assert.Equal(t, 60, warnings[0].Range.Col)
}

func TestParseContextService_DuplicateSchemaEntriesWarnForUnmaterializedColumn(t *testing.T) {
	t.Parallel()

	service := NewParseContextService(ParseContextDependencies{
		ResolveAssetByID: func(_ context.Context, _ string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "", nil, &pipeline.Asset{Type: pipeline.AssetTypeDuckDBQuery}, nil
		},
	})

	result, apiError := service.Parse(
		context.Background(),
		"asset-id",
		`with blub as (select *, 1 as schabla from quickstart.range_100)

select range, schabla, (select test from quickstart.test), bla from blub`,
		[]ParseContextSchemaTable{
			{
				Name: "quickstart.range_100",
				Columns: []ParseContextSchemaColumn{
					{Name: "range", Type: "bigint", SourceMethods: []string{"workspace-load"}},
					{Name: "bla", Type: "integer", SourceMethods: []string{"asset-sql-definition"}},
				},
			},
			{
				Name: "range_100",
				Columns: []ParseContextSchemaColumn{
					{Name: "range", Type: "bigint", SourceMethods: []string{"connection-column-discovery"}},
				},
			},
			{
				Name: "quickstart.test",
				Columns: []ParseContextSchemaColumn{
					{Name: "test", Type: "integer", SourceMethods: []string{"connection-column-discovery"}},
				},
			},
		},
		"",
	)
	require.Nil(t, apiError)
	assert.Empty(t, result.Errors)
	assert.Contains(t, diagnosticMessagesFromService(result.Diagnostics), "Column 'bla' is defined in the asset 'quickstart.range_100', but it has not been materialized yet.")
	for _, diagnostic := range result.Diagnostics {
		assert.NotEqual(t, "Unresolved column: bla", diagnostic.Message)
	}
}

func TestParseContextService_InspectColumnsDoNotHideConnectionDiscoveryWarning(t *testing.T) {
	t.Parallel()

	service := NewParseContextService(ParseContextDependencies{
		ResolveAssetByID: func(_ context.Context, _ string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "", nil, &pipeline.Asset{Type: pipeline.AssetTypeDuckDBQuery}, nil
		},
	})

	result, apiError := service.Parse(
		context.Background(),
		"asset-id",
		`with blub as (select * from quickstart.range_100)

select bla from blub`,
		[]ParseContextSchemaTable{
			{
				Name: "quickstart.range_100",
				Columns: []ParseContextSchemaColumn{
					{Name: "range", Type: "bigint", SourceMethods: []string{"workspace-load"}},
					{Name: "bla", Type: "integer", SourceMethods: []string{"asset-sql-definition"}},
				},
			},
			{
				Name: "quickstart.range_100",
				Columns: []ParseContextSchemaColumn{
					{Name: "range", Type: "bigint", SourceMethods: []string{"connection-column-discovery"}},
				},
			},
			{
				Name: "quickstart.range_100",
				Columns: []ParseContextSchemaColumn{
					{Name: "bla", Type: "integer", SourceMethods: []string{"asset-inspect"}},
				},
			},
		},
		"",
	)
	require.Nil(t, apiError)
	assert.Empty(t, result.Errors)
	assert.Contains(t, diagnosticMessagesFromService(result.Diagnostics), "Column 'bla' is defined in the asset 'quickstart.range_100', but it has not been materialized yet.")
}

func TestParseContextService_SQLDefinitionColumnsFromResolvedPipelineWarnWhenMissingFromDiscoveredTable(t *testing.T) {
	t.Parallel()

	parsedPipeline := &pipeline.Pipeline{Assets: []*pipeline.Asset{
		{
			Name: "quickstart.unmaterialized_asset",
			Type: pipeline.AssetTypeDuckDBQuery,
			ExecutableFile: pipeline.ExecutableFile{
				Content: "select plumbus, 1 as blabli from quickstart.source_table",
			},
		},
	}}
	service := NewParseContextService(ParseContextDependencies{
		ResolveAssetByID: func(_ context.Context, _ string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "", parsedPipeline, &pipeline.Asset{Type: pipeline.AssetTypeDuckDBQuery}, nil
		},
	})

	result, apiError := service.Parse(
		context.Background(),
		"asset-id",
		`select plumbus, blabli from quickstart.unmaterialized_asset`,
		[]ParseContextSchemaTable{
			{
				Name: "quickstart.unmaterialized_asset",
				Columns: []ParseContextSchemaColumn{
					{Name: "plumbus", Type: "integer", SourceMethods: []string{"connection-column-discovery"}},
				},
			},
		},
		"",
	)
	require.Nil(t, apiError)
	assert.Empty(t, result.Errors)
	assert.Contains(t, diagnosticMessagesFromService(result.Diagnostics), "Column 'blabli' is defined in the asset 'quickstart.unmaterialized_asset', but it has not been materialized yet.")
	for _, diagnostic := range result.Diagnostics {
		assert.NotEqual(t, "Unresolved column: blabli", diagnostic.Message)
	}
}

func TestParseContextService_APIResponseFieldsResolveWorkspaceAssets(t *testing.T) {
	t.Parallel()

	parsedPipeline := &pipeline.Pipeline{Assets: []*pipeline.Asset{
		{
			Name: "quickstart.games",
			Type: pipeline.AssetType(apiAssetType),
			ExecutableFile: pipeline.ExecutableFile{Content: `type: api

parameters:
  request:
    url: https://example.invalid/games
  response:
    fields:
      white_username: white.username
      black_username: black.username
`},
		},
	}}
	service := NewParseContextService(ParseContextDependencies{
		ResolveAssetByID: func(_ context.Context, _ string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "", parsedPipeline, &pipeline.Asset{Type: pipeline.AssetTypeDuckDBQuery}, nil
		},
	})

	result, apiError := service.Parse(
		context.Background(),
		"asset-id",
		`select white_username, black_username from quickstart.games`,
		nil,
		"",
	)
	require.Nil(t, apiError)
	assert.Empty(t, result.Errors)
	diagnostics := diagnosticMessagesFromService(result.Diagnostics)
	assert.NotContains(t, diagnostics, "Unresolved table: quickstart.games")
	assert.NotContains(t, diagnostics, "Unresolved column: white_username")
}

func TestParseContextService_DoesNotFetchOpenAPIForPureAuthoring(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`openapi: 3.0.3
paths:
  /players/{username}:
    get:
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
                properties:
                  data:
                    type: object
                    properties:
                      username:
                        type: string
                      rating:
                        type: integer
`))
	}))
	defer server.Close()

	parsedPipeline := &pipeline.Pipeline{Assets: []*pipeline.Asset{
		{
			Name: "quickstart.players",
			Type: pipeline.AssetType(apiAssetType),
			ExecutableFile: pipeline.ExecutableFile{Content: `type: api

parameters:
  openapi:
    url: ` + server.URL + `
  request:
    url: https://api.example.com/players/{{ username }}
  response:
    records_path: data
`},
		},
	}}
	service := NewParseContextService(ParseContextDependencies{
		ResolveAssetByID: func(_ context.Context, _ string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "", parsedPipeline, &pipeline.Asset{Type: pipeline.AssetTypeDuckDBQuery}, nil
		},
	})

	result, apiError := service.Parse(
		context.Background(),
		"asset-id",
		`select username, rating from quickstart.players`,
		nil,
		"",
	)
	require.Nil(t, apiError)
	assert.Empty(t, result.Errors)
	diagnostics := diagnosticMessagesFromService(result.Diagnostics)
	assert.NotContains(t, diagnostics, "Unresolved table: quickstart.players")
	assert.Contains(t, diagnostics, "Unresolved column: username")
	assert.Contains(t, diagnostics, "Unresolved column: rating")
	assert.Equal(t, int32(0), requests.Load())
}

func TestParseContextService_SQLDefinitionColumnsWarnWhenMissingFromMaterializedWorkspaceColumns(t *testing.T) {
	t.Parallel()

	parsedPipeline := &pipeline.Pipeline{Assets: []*pipeline.Asset{
		{
			Name: "quickstart.unmaterialized_asset",
			Type: pipeline.AssetTypeDuckDBQuery,
			ExecutableFile: pipeline.ExecutableFile{
				Content: "select 1 as plumbus, 2 as blabli",
			},
		},
	}}
	service := NewParseContextService(ParseContextDependencies{
		ResolveAssetByID: func(_ context.Context, _ string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "", parsedPipeline, &pipeline.Asset{Type: pipeline.AssetTypeDuckDBQuery}, nil
		},
	})

	result, apiError := service.Parse(
		context.Background(),
		"asset-id",
		`select plumbus, blabli from quickstart.unmaterialized_asset`,
		[]ParseContextSchemaTable{
			{
				Name:           "quickstart.unmaterialized_asset",
				IsMaterialized: true,
				Columns: []ParseContextSchemaColumn{
					{Name: "plumbus", Type: "integer", SourceMethods: []string{"workspace-load"}},
				},
			},
		},
		"",
	)
	require.Nil(t, apiError)
	assert.Empty(t, result.Errors)
	assert.Contains(t, diagnosticMessagesFromService(result.Diagnostics), "Column 'blabli' is defined in the asset 'quickstart.unmaterialized_asset', but it has not been materialized yet.")
	for _, diagnostic := range result.Diagnostics {
		assert.NotEqual(t, "Unresolved column: blabli", diagnostic.Message)
	}
}

func TestParseContextWithSchema_ConnectionDiscoveredAssetColumnDoesNotWarn(t *testing.T) {
	t.Parallel()

	parseContext, err := sqlintelligence.ParseContextWithSchema(
		`with blub as (select * from quickstart.range_100)

select bla from blub`,
		"duckdb",
		sqlintelligence.Schema{
			"quickstart.range_100": {"bla": "integer"},
		},
		sqlintelligence.SchemaColumnSourceMethods{
			"quickstart.range_100": {"bla": []string{"workspace-load", "connection-column-discovery"}},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, parseContext)

	assert.Empty(t, parseContext.Errors)
	assert.Empty(t, warningDiagnostics(parseContext.Diagnostics))
}

func TestParseContextWithSchema_InspectedAssetColumnDoesNotProvideActualSchemaEvidence(t *testing.T) {
	t.Parallel()

	parseContext, err := sqlintelligence.ParseContextWithSchema(
		`with blub as (select * from quickstart.range_100)

select bla from blub`,
		"duckdb",
		sqlintelligence.Schema{
			"quickstart.range_100": {"bla": "integer"},
		},
		sqlintelligence.SchemaColumnSourceMethods{
			"quickstart.range_100": {"bla": []string{"workspace-load", "asset-inspect"}},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, parseContext)

	assert.Empty(t, parseContext.Errors)
	assert.Empty(t, warningDiagnostics(parseContext.Diagnostics))
}

func TestParseContextWithSchema_AssetDefinedColumnDoesNotWarnWithoutActualSchemaEvidence(t *testing.T) {
	t.Parallel()

	parseContext, err := sqlintelligence.ParseContextWithSchema(
		`with blub as (select * from quickstart.range_100)

select bla from blub`,
		"duckdb",
		sqlintelligence.Schema{
			"quickstart.range_100": {"bla": "integer"},
		},
		sqlintelligence.SchemaColumnSourceMethods{
			"quickstart.range_100": {"bla": []string{"asset-sql-definition"}},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, parseContext)

	assert.Empty(t, parseContext.Errors)
	assert.Empty(t, warningDiagnostics(parseContext.Diagnostics))
}

func diagnosticMessages(diagnostics []sqlintelligence.ParseContextDiagnostic) []string {
	result := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		result = append(result, diagnostic.Message)
	}
	return result
}

func warningDiagnostics(diagnostics []sqlintelligence.ParseContextDiagnostic) []sqlintelligence.ParseContextDiagnostic {
	result := make([]sqlintelligence.ParseContextDiagnostic, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "warning" {
			result = append(result, diagnostic)
		}
	}
	return result
}

func diagnosticMessagesFromService(diagnostics []ParseContextDiagnostic) []string {
	result := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		result = append(result, diagnostic.Message)
	}
	return result
}

func rangeContainsPosition(rangeValue sqlintelligence.ParseContextRange, line, column int) bool {
	return line >= rangeValue.Line &&
		line <= rangeValue.EndLine &&
		(line != rangeValue.Line || column >= rangeValue.Col) &&
		(line != rangeValue.EndLine || column <= rangeValue.EndCol)
}
