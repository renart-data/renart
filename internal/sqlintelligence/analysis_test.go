package sqlintelligence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeQueryReturnsCompactSchemaAwareFacts(t *testing.T) {
	analysis, err := AnalyzeQuery(
		context.Background(),
		"select id, amount + 1 as gross from analytics.orders",
		"duckdb",
		Schema{
			"analytics.orders": {
				"id":     "BIGINT",
				"amount": "DECIMAL(10,2)",
			},
		},
	)
	require.NoError(t, err)
	assert.Equal(t, "select", analysis.Shape)
	assert.True(t, analysis.OutputNamesComplete)
	assert.True(t, analysis.OutputTypesComplete)
	assert.Equal(t, []SchemaColumn{
		{Name: "id", Type: "BIGINT"},
		{Name: "gross", Type: "DECIMAL(10, 2)"},
	}, analysis.OutputColumns)
	require.Len(t, analysis.BaseTables, 1)
	assert.Equal(t, "analytics.orders", analysis.BaseTables[0].Name)
	require.Len(t, analysis.Projections, 2)
	require.Len(t, analysis.Projections[1].Upstream, 1)
	assert.Equal(t, "amount", analysis.Projections[1].Upstream[0].Column)
}

func TestAnalyzeQueryClassifiesCompleteAndIncompleteProjectionNames(t *testing.T) {
	t.Run("known star is expanded", func(t *testing.T) {
		analysis, err := AnalyzeQuery(context.Background(), "select * from t", "duckdb", Schema{
			"t": {"id": "INTEGER", "label": "VARCHAR"},
		})
		require.NoError(t, err)
		assert.True(t, analysis.OutputNamesComplete)
		assert.True(t, analysis.OutputTypesComplete)
		assert.ElementsMatch(t, []SchemaColumn{{Name: "id", Type: "INTEGER"}, {Name: "label", Type: "VARCHAR"}}, analysis.OutputColumns)
		require.Len(t, analysis.StarProjections, 1)
		assert.ElementsMatch(t, []string{"id", "label"}, analysis.StarProjections[0].ExpandedColumns)
	})

	t.Run("unknown star stays incomplete", func(t *testing.T) {
		analysis, err := AnalyzeQuery(context.Background(), "select * from unknown_table", "duckdb", Schema{})
		require.NoError(t, err)
		assert.False(t, analysis.OutputNamesComplete)
		assert.False(t, analysis.OutputTypesComplete)
		assert.Empty(t, analysis.OutputColumns)
	})

	t.Run("synthetic expression name is not exposed as a relation column", func(t *testing.T) {
		analysis, err := AnalyzeQuery(context.Background(), "select 1 + 2", "duckdb", Schema{})
		require.NoError(t, err)
		assert.False(t, analysis.OutputNamesComplete)
		assert.Empty(t, analysis.OutputColumns)
	})
}

func TestAnalyzeQuerySupportsRenartDialects(t *testing.T) {
	for _, dialect := range []string{
		"duckdb", "postgres", "bigquery", "snowflake", "athena", "databricks",
		"tsql", "clickhouse", "trino", "mysql", "oracle", "generic",
	} {
		t.Run(dialect, func(t *testing.T) {
			analysis, err := AnalyzeQuery(context.Background(), "select id as output_id from t", dialect, Schema{
				"t": {"id": "INTEGER"},
			})
			require.NoError(t, err)
			require.Len(t, analysis.OutputColumns, 1)
			assert.Equal(t, "output_id", analysis.OutputColumns[0].Name)
			assert.NotEmpty(t, analysis.OutputColumns[0].Type)
			assert.True(t, analysis.OutputNamesComplete)
			assert.True(t, analysis.OutputTypesComplete)
		})
	}
}

func TestAnalyzeQueryNormalizesPostgresDialectAndKeepsParameterizedType(t *testing.T) {
	analysis, err := AnalyzeQuery(context.Background(), "select id::numeric(12,2) as amount from t", "postgres", Schema{
		"t": {"id": "INTEGER"},
	})
	require.NoError(t, err)
	require.Len(t, analysis.OutputColumns, 1)
	assert.Equal(t, SchemaColumn{Name: "amount", Type: "DECIMAL(12, 2)"}, analysis.OutputColumns[0])
}

func TestAnalyzeQueryPropagatesDeclaredNullabilityThroughOuterJoin(t *testing.T) {
	notNullable := false
	analysis, err := AnalyzeQuery(
		context.Background(),
		"select o.id, u.name from orders o left join users u on o.user_id = u.id",
		"duckdb",
		Schema{
			"orders": {"id": "INTEGER", "user_id": "INTEGER"},
			"users":  {"id": "INTEGER", "name": "VARCHAR"},
		},
		SchemaConstraints{
			"orders": {Columns: map[string]SchemaColumnConstraints{
				"id":      {Nullable: &notNullable},
				"user_id": {Nullable: &notNullable},
			}},
			"users": {Columns: map[string]SchemaColumnConstraints{
				"id":   {Nullable: &notNullable},
				"name": {Nullable: &notNullable},
			}},
		},
	)
	require.NoError(t, err)
	require.Len(t, analysis.OutputColumns, 2)
	require.NotNil(t, analysis.OutputColumns[0].Nullable)
	assert.False(t, *analysis.OutputColumns[0].Nullable)
	require.NotNil(t, analysis.OutputColumns[1].Nullable)
	assert.True(t, *analysis.OutputColumns[1].Nullable, "the nullable side of a LEFT JOIN must stay nullable")
}

func TestAnalyzeQueryHonorsCanceledContextBeforeCacheLookup(t *testing.T) {
	const query = "select id as cached_id from cancellation_table"
	schema := Schema{"cancellation_table": {"id": "INTEGER"}}
	_, err := AnalyzeQuery(context.Background(), query, "duckdb", schema)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = AnalyzeQuery(ctx, query, "duckdb", schema)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestAnalyzeQueryOptionsAreDeterministicAcrossMapOrder(t *testing.T) {
	left := Schema{}
	left["z"] = map[string]string{"b": "BIGINT", "a": "INTEGER"}
	left["a"] = map[string]string{"z": "VARCHAR"}
	right := Schema{}
	right["a"] = map[string]string{"z": "VARCHAR"}
	right["z"] = map[string]string{"a": "INTEGER", "b": "BIGINT"}

	leftOptions, err := marshalAnalyzeQueryOptions("duckdb", left)
	require.NoError(t, err)
	rightOptions, err := marshalAnalyzeQueryOptions("duckdb", right)
	require.NoError(t, err)
	assert.Equal(t, leftOptions, rightOptions)
	assert.Equal(t, queryAnalysisKey("select 1", leftOptions), queryAnalysisKey("select 1", rightOptions))
}

func TestQueryAnalysisCacheIsBoundedAndLeastRecentlyUsed(t *testing.T) {
	cache := newQueryAnalysisCache(2)
	key1 := queryAnalysisKey("select 1", "{}")
	key2 := queryAnalysisKey("select 2", "{}")
	key3 := queryAnalysisKey("select 3", "{}")
	cache.add(key1, QueryAnalysis{Shape: "one"})
	cache.add(key2, QueryAnalysis{Shape: "two"})

	got, ok := cache.get(key1)
	require.True(t, ok)
	assert.Equal(t, "one", got.Shape)
	cache.add(key3, QueryAnalysis{Shape: "three"})

	_, ok = cache.get(key2)
	assert.False(t, ok, "least recently used entry should be evicted")
	_, ok = cache.get(key1)
	assert.True(t, ok)
	_, ok = cache.get(key3)
	assert.True(t, ok)
}

func TestAnalyzeQueryRejectsEmptySQL(t *testing.T) {
	_, err := AnalyzeQuery(context.Background(), "  ", "duckdb", Schema{})
	assert.EqualError(t, err, "cannot analyze empty SQL")
}
