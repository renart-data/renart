package sqlintelligence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/authoringdiag"
)

func TestValidateSQLReportsUnqualifiedColumn(t *testing.T) {
	query := "select a, b from a.example_asset"
	result, err := ValidateSQL(context.Background(), ValidationRequest{
		URI:     "file:///workspace/a/another_asset.sql",
		SQL:     query,
		Dialect: "duckdb",
		Schema: Schema{
			"a.example_asset": {"a": "INTEGER"},
		},
		RelationConfidence: map[string]RelationConfidence{"a.example_asset": RelationKnown},
	})
	require.NoError(t, err)
	require.Len(t, result.Diagnostics, 1)
	diagnostic := result.Diagnostics[0]
	assert.Equal(t, authoringdiag.CodeUnresolvedColumn, diagnostic.Code)
	assert.Equal(t, "Unresolved column: b", diagnostic.Message)
	require.NotNil(t, diagnostic.StartByte)
	require.NotNil(t, diagnostic.EndByte)
	assert.Equal(t, "b", query[*diagnostic.StartByte:*diagnostic.EndByte])
}

func TestValidateSQLSuppressesOnlyColumnsDependingOnUnknownSchema(t *testing.T) {
	result, err := ValidateSQL(context.Background(), ValidationRequest{
		SQL:     "select known.missing, unknown.anything from known join unknown on true",
		Dialect: "duckdb",
		Schema: Schema{
			"known":   {"id": "INTEGER"},
			"unknown": {},
		},
		RelationConfidence: map[string]RelationConfidence{
			"known":   RelationKnown,
			"unknown": RelationUnknown,
		},
	})
	require.NoError(t, err)
	require.Len(t, result.Diagnostics, 1)
	assert.Equal(t, authoringdiag.CodeUnresolvedColumn, result.Diagnostics[0].Code)
	assert.Equal(t, "Unresolved column: missing", result.Diagnostics[0].Message)
}

func TestValidateSQLReportsGolyglotExpressionTypeMismatch(t *testing.T) {
	result, err := ValidateSQL(context.Background(), ValidationRequest{
		SQL:     "select id + 'not a number' from values_table",
		Dialect: "duckdb",
		Schema: Schema{
			"values_table": {"id": "INTEGER"},
		},
		RelationConfidence: map[string]RelationConfidence{"values_table": RelationKnown},
	})
	require.NoError(t, err)
	require.Len(t, result.Diagnostics, 1)
	assert.Equal(t, authoringdiag.CodeSQLTypeMismatch, result.Diagnostics[0].Code)
	assert.Equal(t, authoringdiag.SourcePolyglot, result.Diagnostics[0].Source)
	assert.Equal(t, authoringdiag.SeverityError, result.Diagnostics[0].Severity)
	assert.Contains(t, result.Diagnostics[0].Message, "numeric-compatible operands")
}

func TestValidateSQLReportsAmbiguousJoinColumnAcrossRenartDialects(t *testing.T) {
	for _, dialect := range []string{
		"duckdb", "postgres", "bigquery", "snowflake", "athena", "databricks",
		"tsql", "clickhouse", "trino", "mysql", "oracle", "generic",
	} {
		t.Run(dialect, func(t *testing.T) {
			const query = "select id from users u join orders o on u.id = o.user_id"
			result, err := ValidateSQL(context.Background(), ValidationRequest{
				SQL:     query,
				Dialect: dialect,
				Schema: Schema{
					"users":  {"id": "INTEGER"},
					"orders": {"id": "INTEGER", "user_id": "INTEGER"},
				},
				RelationConfidence: map[string]RelationConfidence{
					"users": RelationKnown, "orders": RelationKnown,
				},
			})
			require.NoError(t, err)
			require.Len(t, result.Diagnostics, 1)
			diagnostic := result.Diagnostics[0]
			assert.Equal(t, authoringdiag.CodeUnresolvedColumn, diagnostic.Code)
			assert.Contains(t, diagnostic.Message, "Ambiguous unqualified column 'id'")
			require.NotNil(t, diagnostic.StartByte)
			require.NotNil(t, diagnostic.EndByte)
			assert.Equal(t, "id", query[*diagnostic.StartByte:*diagnostic.EndByte])
		})
	}
}

func TestValidateSQLKeepsHeuristicJoinWarningsOutOfCoreTypechecking(t *testing.T) {
	constraints := SchemaConstraints{
		"users": {Columns: map[string]SchemaColumnConstraints{
			"id": {PrimaryKey: true},
		}},
		"orders": {Columns: map[string]SchemaColumnConstraints{
			"user_id": {ForeignKey: &SchemaColumnReference{Table: "users", Column: "id"}},
		}},
	}
	for name, query := range map[string]string{
		"alternate join key":  "select o.id from orders o join users u on o.id = u.id",
		"explicit cross join": "select o.id from orders o cross join users u",
	} {
		t.Run(name, func(t *testing.T) {
			result, err := ValidateSQL(context.Background(), ValidationRequest{
				SQL:               query,
				Dialect:           "duckdb",
				Schema:            Schema{"users": {"id": "INTEGER"}, "orders": {"id": "INTEGER", "user_id": "INTEGER"}},
				SchemaConstraints: constraints,
				RelationConfidence: map[string]RelationConfidence{
					"users": RelationKnown, "orders": RelationKnown,
				},
			})
			require.NoError(t, err)
			assert.Empty(t, result.Diagnostics)
		})
	}
}

func TestValidateSQLReportsDeclaredOutputTypeDrift(t *testing.T) {
	result, err := ValidateSQL(context.Background(), ValidationRequest{
		URI:            "file:///workspace/example.sql",
		SQL:            "select 1 as id, 'kept' as label",
		Dialect:        "duckdb",
		Schema:         Schema{},
		ExpectedOutput: []SchemaColumn{{Name: "id", Type: "VARCHAR"}, {Name: "label", Type: "TEXT"}},
	})
	require.NoError(t, err)
	require.Len(t, result.Diagnostics, 1)
	diagnostic := result.Diagnostics[0]
	assert.Equal(t, authoringdiag.CodeDeclaredColumnTypeDrift, diagnostic.Code)
	assert.Equal(t, authoringdiag.SeverityWarning, diagnostic.Severity)
	assert.Equal(t, authoringdiag.ScopeAsset, diagnostic.Scope)
	assert.Equal(t, authoringdiag.ConfidenceHigh, diagnostic.Confidence)
	assert.Equal(t, `Column "id" is declared as VARCHAR, but the SQL output is inferred as INTEGER.`, diagnostic.Message)
}

func TestValidateSQLReportsDeclaredOutputNameDrift(t *testing.T) {
	result, err := ValidateSQL(context.Background(), ValidationRequest{
		URI:            "file:///workspace/range_10.sql",
		SQL:            "select range as ronge from range(10)",
		Dialect:        "duckdb",
		Schema:         Schema{},
		ExpectedOutput: []SchemaColumn{{Name: "range"}},
	})
	require.NoError(t, err)
	require.Len(t, result.Diagnostics, 1)
	diagnostic := result.Diagnostics[0]
	assert.Equal(t, authoringdiag.CodeDeclaredOutputSchemaDrift, diagnostic.Code)
	assert.Equal(t, authoringdiag.SeverityWarning, diagnostic.Severity)
	assert.Equal(t, authoringdiag.ScopeAsset, diagnostic.Scope)
	assert.Equal(t, authoringdiag.ConfidenceHigh, diagnostic.Confidence)
	assert.Equal(t, `SQL does not produce declared column "range"; SQL produces undeclared column "ronge".`, diagnostic.Message)
}

func TestValidateSQLUsesCompactAnalysisForTypeDriftThroughNestedCTEs(t *testing.T) {
	result, err := ValidateSQL(context.Background(), ValidationRequest{
		URI:     "file:///workspace/report.sql",
		SQL:     "with base as (select amount from orders), nested as (select amount from base) select amount from nested",
		Dialect: "duckdb",
		Schema: Schema{
			"orders": {"amount": "INTEGER"},
		},
		ExpectedOutput: []SchemaColumn{{Name: "amount", Type: "VARCHAR"}},
	})
	require.NoError(t, err)
	require.Len(t, result.Diagnostics, 1)
	assert.Equal(t, authoringdiag.CodeDeclaredColumnTypeDrift, result.Diagnostics[0].Code)
	assert.Equal(t, `Column "amount" is declared as VARCHAR, but the SQL output is inferred as INTEGER.`, result.Diagnostics[0].Message)
}

func TestValidateSQLUsesCompactAnalysisForNameDriftThroughCTEStar(t *testing.T) {
	result, err := ValidateSQL(context.Background(), ValidationRequest{
		URI:     "file:///workspace/report.sql",
		SQL:     "with base as (select * from orders) select * from base",
		Dialect: "duckdb",
		Schema: Schema{
			"orders": {"id": "INTEGER", "amount": "INTEGER"},
		},
		RelationConfidence: map[string]RelationConfidence{"orders": RelationKnown},
		ExpectedOutput:     []SchemaColumn{{Name: "id"}, {Name: "total"}},
	})
	require.NoError(t, err)
	require.Len(t, result.Diagnostics, 1)
	assert.Equal(t, authoringdiag.CodeDeclaredOutputSchemaDrift, result.Diagnostics[0].Code)
	assert.Equal(t, `SQL does not produce declared column "total"; SQL produces undeclared column "amount".`, result.Diagnostics[0].Message)
}

func TestValidateSQLKeepsCTEStarNameDriftSilentForPartialSchema(t *testing.T) {
	result, err := ValidateSQL(context.Background(), ValidationRequest{
		SQL:     "with base as (select * from orders) select * from base",
		Dialect: "duckdb",
		Schema: Schema{
			"orders": {"id": "INTEGER"},
		},
		RelationConfidence: map[string]RelationConfidence{"orders": RelationUnknown},
		ExpectedOutput:     []SchemaColumn{{Name: "id"}, {Name: "amount"}},
	})
	require.NoError(t, err)
	assert.Empty(t, result.Diagnostics)
}

func TestValidateSQLReportsUnsafeDeclaredOutputNullabilityDrift(t *testing.T) {
	notNullable := false
	result, err := ValidateSQL(context.Background(), ValidationRequest{
		URI:            "file:///workspace/report.sql",
		SQL:            "select cast(null as integer) as id",
		Dialect:        "duckdb",
		ExpectedOutput: []SchemaColumn{{Name: "id", Type: "INTEGER", Nullable: &notNullable}},
	})
	require.NoError(t, err)
	require.Len(t, result.Diagnostics, 1)
	diagnostic := result.Diagnostics[0]
	assert.Equal(t, authoringdiag.CodeDeclaredColumnNullabilityDrift, diagnostic.Code)
	assert.Equal(t, authoringdiag.SeverityWarning, diagnostic.Severity)
	assert.Equal(t, authoringdiag.ScopeAsset, diagnostic.Scope)
	assert.Equal(t, authoringdiag.ConfidenceHigh, diagnostic.Confidence)
	assert.Equal(t, `Column "id" is declared NOT NULL, but the SQL output may be NULL.`, diagnostic.Message)
}

func TestValidateSQLTracksDeclaredNullabilityThroughOuterJoin(t *testing.T) {
	notNullable := false
	schema := Schema{
		"orders": {"id": "INTEGER", "user_id": "INTEGER"},
		"users":  {"id": "INTEGER", "name": "VARCHAR"},
	}
	constraints := SchemaConstraints{
		"orders": {Columns: map[string]SchemaColumnConstraints{
			"id": {Nullable: &notNullable}, "user_id": {Nullable: &notNullable},
		}},
		"users": {Columns: map[string]SchemaColumnConstraints{
			"id": {Nullable: &notNullable}, "name": {Nullable: &notNullable},
		}},
	}
	expected := []SchemaColumn{{Name: "name", Type: "VARCHAR", Nullable: &notNullable}}

	t.Run("nullable outer-join side warns", func(t *testing.T) {
		result, err := ValidateSQL(context.Background(), ValidationRequest{
			SQL:               "select u.name from orders o left join users u on o.user_id = u.id",
			Dialect:           "duckdb",
			Schema:            schema,
			SchemaConstraints: constraints,
			ExpectedOutput:    expected,
		})
		require.NoError(t, err)
		require.Len(t, result.Diagnostics, 1)
		assert.Equal(t, authoringdiag.CodeDeclaredColumnNullabilityDrift, result.Diagnostics[0].Code)
	})

	t.Run("direct non-null source stays silent", func(t *testing.T) {
		result, err := ValidateSQL(context.Background(), ValidationRequest{
			SQL:               "select name from users",
			Dialect:           "duckdb",
			Schema:            schema,
			SchemaConstraints: constraints,
			ExpectedOutput:    expected,
		})
		require.NoError(t, err)
		assert.Empty(t, result.Diagnostics)
	})
}

func TestValidateSQLKeepsUnknownOutputNullabilitySilent(t *testing.T) {
	notNullable := false
	result, err := ValidateSQL(context.Background(), ValidationRequest{
		SQL:     "select max(maybe) as maximum from orders",
		Dialect: "duckdb",
		Schema:  Schema{"orders": {"maybe": "INTEGER"}},
		ExpectedOutput: []SchemaColumn{{
			Name: "maximum", Type: "INTEGER", Nullable: &notNullable,
		}},
	})
	require.NoError(t, err)
	assert.Empty(t, result.Diagnostics)
}

func TestDataTypesEquivalentUsesGolyglotTypeParser(t *testing.T) {
	tests := []struct {
		name       string
		left       string
		right      string
		dialect    string
		equivalent bool
	}{
		{name: "integer spelling", left: "INTEGER", right: "INT", dialect: "duckdb", equivalent: true},
		{name: "string aliases", left: "TEXT", right: "VARCHAR(255)", dialect: "postgresql", equivalent: true},
		{name: "decimal spelling", left: "NUMERIC(10, 2)", right: "DECIMAL(10,2)", dialect: "duckdb", equivalent: true},
		{name: "timestamp timezone spelling", left: "TIMESTAMPTZ", right: "TIMESTAMP WITH TIME ZONE", dialect: "postgresql", equivalent: true},
		{name: "bigquery aliases", left: "INT64", right: "BIGINT", dialect: "bigquery", equivalent: true},
		{name: "integer width drift", left: "BIGINT", right: "INTEGER", dialect: "duckdb", equivalent: false},
		{name: "decimal precision drift", left: "DECIMAL(12, 2)", right: "DECIMAL(10, 2)", dialect: "duckdb", equivalent: false},
		{name: "timezone drift", left: "TIMESTAMP", right: "TIMESTAMPTZ", dialect: "postgresql", equivalent: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			equivalent, comparable, err := dataTypesEquivalent(context.Background(), tt.left, tt.right, tt.dialect)
			require.NoError(t, err)
			assert.True(t, comparable)
			assert.Equal(t, tt.equivalent, equivalent)
		})
	}
}
