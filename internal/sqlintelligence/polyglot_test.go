package sqlintelligence

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/authoringdiag"
)

func TestParseContextWithSchemaPolyglotExtractsTablesColumnsAndDiagnostics(t *testing.T) {
	parseContext, err := ParseContextWithSchemaPolyglot(
		"select c.customer_id, o.total from analytics.customers c join analytics.orders o on c.customer_id = o.customer_id where missing_col = 1",
		"duckdb",
		Schema{
			"analytics.customers": {"customer_id": "int"},
			"analytics.orders":    {"customer_id": "int", "total": "int"},
		},
	)
	require.NoError(t, err)

	assert.Equal(t, "select", parseContext.QueryKind)
	assert.True(t, parseContext.IsSingleSelect)
	assert.True(t, parseContext.IsReadOnlyResult)
	require.Len(t, parseContext.Tables, 2)
	tablesByName := map[string]ParseContextTable{}
	for _, table := range parseContext.Tables {
		tablesByName[table.Name] = table
	}
	assert.Equal(t, "c", tablesByName["analytics.customers"].Alias)
	assert.Equal(t, "o", tablesByName["analytics.orders"].Alias)

	columnNames := make([]string, 0, len(parseContext.Columns))
	for _, column := range parseContext.Columns {
		columnNames = append(columnNames, column.Name)
	}
	assert.Contains(t, columnNames, "customer_id")
	assert.Contains(t, columnNames, "total")
	assert.Contains(t, columnNames, "missing_col")

	require.NotEmpty(t, parseContext.Diagnostics)
	assert.Contains(t, parseContext.Diagnostics[0].Message, "missing_col")
	assert.Equal(t, "error", parseContext.Diagnostics[0].Severity)
	require.NotNil(t, parseContext.Diagnostics[0].Range)
	assert.Equal(t, "missing_col", parseContext.Diagnostics[0].Range.RangeText("select c.customer_id, o.total from analytics.customers c join analytics.orders o on c.customer_id = o.customer_id where missing_col = 1"))
}

func TestParseContextWithSchemaPolyglotDoesNotTreatCopyOptionsAsColumns(t *testing.T) {
	query := `COPY create_partitions.create_partitions
TO .data/create_all_partitions.sql
format csv
header FALSE
delimiter '\t'`

	parseContext, err := ParseContextWithSchemaPolyglot(query, "duckdb", Schema{
		"create_partitions.create_partitions": {"partition": "varchar"},
	})
	require.NoError(t, err)

	for _, diagnostic := range parseContext.Diagnostics {
		assert.NotEqual(t, authoringdiag.CodeUnresolvedColumn, diagnostic.Code, diagnostic.Message)
	}
}

func TestCopyOptionValueDiagnosticSuppressionIsNarrow(t *testing.T) {
	query := "/* export */\nCOPY source.table\nTO 'output.csv'\nFORMAT = csv\nHEADER false"
	csvStart := strings.Index(query, "csv\n")
	assert.True(t, isCopyOptionValue(query, "csv", &ParseContextRange{
		Start: csvStart,
		End:   csvStart + len("csv"),
	}))
	falseStart := strings.LastIndex(query, "false")
	assert.True(t, isCopyOptionValue(query, "false", &ParseContextRange{
		Start: falseStart,
		End:   falseStart + len("false"),
	}))

	selectQuery := "select csv from source.table"
	selectStart := strings.Index(selectQuery, "csv")
	assert.False(t, isCopyOptionValue(selectQuery, "csv", &ParseContextRange{
		Start: selectStart,
		End:   selectStart + len("csv"),
	}))
}

func TestParseContextWithSchemaPolyglotResolvesCTEAfterVizComment(t *testing.T) {
	query := `/* @viz(line, x: count, y: count_star()) */
with preagg as (
SELECT
  round(trino_seconds / starrocks_seconds) AS ratio,
  count(*) over (order by trino_seconds / starrocks_seconds rows unbounded preceding) as count
FROM playful_maple
WHERE
  result = '"MATCH"'
  and trino_rows > 0
ORDER BY
  1,2 ASC
)

select
  count,
  count(*)
from preagg
group by count`

	parseContext, err := ParseContextWithSchemaPolyglot(query, "duckdb", Schema{
		"playful_maple": {
			"trino_seconds":     "double",
			"starrocks_seconds": "double",
			"result":            "varchar",
			"trino_rows":        "bigint",
		},
	})
	require.NoError(t, err)

	assert.NotContains(t, diagnosticMessages(parseContext.Diagnostics), "Unresolved table: preagg")
}

func TestParseContextUsesPolyglotStructuredErrorOffsets(t *testing.T) {
	query := "select\n  from"
	parseContext, err := ParseContextWithSchemaPolyglot(query, "duckdb", Schema{})
	require.NoError(t, err)
	require.Len(t, parseContext.Diagnostics, 1)
	require.NotNil(t, parseContext.Diagnostics[0].Range)
	assert.Equal(t, "from", parseContext.Diagnostics[0].Range.RangeText(query))
}

func TestParseContextWithSchemaPolyglotResolvesQuickstartPlayerStats(t *testing.T) {
	query := `WITH players_white AS (
    SELECT white->>'@id' AS player_id
    FROM quickstart.games
),

players_black AS (
    SELECT black->>'@id' AS player_id
    FROM quickstart.games
)

SELECT
    name,
    (
        SELECT count(*) FROM players_white
        WHERE quickstart.players.aid = players_white.player_id
    ) AS games_white,
    (
        SELECT count(*) FROM players_black
        WHERE quickstart.players.aid = players_black.player_id
    ) as games_black
FROM quickstart.players`

	parseContext, err := ParseContextWithSchemaPolyglot(query, "duckdb", Schema{
		"quickstart.games":   {"white": "json", "black": "json"},
		"quickstart.players": {"aid": "varchar", "name": "varchar"},
	})
	require.NoError(t, err)

	assert.Empty(t, parseContext.Diagnostics)
}

func TestParseContextWithSchemaPolyglotPropagatesSelectStarAcrossCTEs(t *testing.T) {
	query := `WITH opening_rollup AS (
    SELECT
        name,
        username,
        color,
        time_class,
        eco_code,
        count(*) AS games
    FROM chess.game_results
    GROUP BY name, username, color, time_class, eco_code
),
ranked_openings AS (
    SELECT
        *,
        row_number() OVER (
            PARTITION BY username, color, time_class
            ORDER BY games DESC, eco_code
        ) AS repertoire_rank
    FROM opening_rollup
)
SELECT * EXCLUDE (repertoire_rank)
FROM ranked_openings
ORDER BY username, time_class, color, games DESC`

	parseContext, err := ParseContextWithSchemaPolyglot(query, "duckdb", Schema{
		"chess.game_results": {
			"name":       "varchar",
			"username":   "varchar",
			"color":      "varchar",
			"time_class": "varchar",
			"eco_code":   "varchar",
		},
	})
	require.NoError(t, err)

	assert.Empty(t, parseContext.Diagnostics)
	var rankedColumns []string
	for _, table := range parseContext.Tables {
		if strings.EqualFold(table.Name, "ranked_openings") {
			for _, column := range table.Columns {
				rankedColumns = append(rankedColumns, column.Name)
			}
		}
	}
	for _, column := range []string{"username", "color", "time_class", "games", "repertoire_rank"} {
		assert.Contains(t, rankedColumns, column)
	}
}

func TestParseContextWithSchemaPolyglotResolvesScalarSubqueryColumnsInLocalScope(t *testing.T) {
	query := `SELECT
  *,
  (select first(range) from example.my_sql_asset_2)
FROM example.my_sql_asset_3`

	parseContext, err := ParseContextWithSchemaPolyglot(query, "duckdb", Schema{
		"example.my_sql_asset_2": {"range": "BIGINT"},
		"example.my_sql_asset_3": {"range": "BIGINT"},
	})
	require.NoError(t, err)

	assert.NotContains(t, diagnosticMessages(parseContext.Diagnostics), "Unresolved column: range")
}

func TestParseContextWithSchemaPolyglotResolvesShortQualifierForSchemaQualifiedTable(t *testing.T) {
	query := `SELECT *
FROM example.parabola p
JOIN example.range_10
  ON range_10.range = p.x`

	parseContext, err := ParseContextWithSchemaPolyglot(query, "duckdb", Schema{
		"example.parabola": {"x": "BIGINT", "y": "BIGINT"},
		"example.range_10": {"range": "BIGINT"},
	})
	require.NoError(t, err)

	assert.Empty(t, parseContext.Diagnostics, "unexpected diagnostics: %+v", parseContext.Diagnostics)
	var rangeColumn *ParseContextColumn
	for i := range parseContext.Columns {
		if parseContext.Columns[i].Name == "range" && parseContext.Columns[i].Qualifier == "range_10" {
			rangeColumn = &parseContext.Columns[i]
			break
		}
	}
	require.NotNil(t, rangeColumn)
	assert.Equal(t, "example.range_10", rangeColumn.ResolvedTable)
}

func TestPolyglotTableQualifierMapKeepsShortNamesSafe(t *testing.T) {
	t.Run("ambiguous short names stay unresolved", func(t *testing.T) {
		qualifiers := polyglotTableQualifierMap([]ParseContextTable{
			{Name: "sales.orders", ResolvedName: "sales.orders"},
			{Name: "audit.orders", ResolvedName: "audit.orders"},
		})

		assert.Equal(t, "sales.orders", qualifiers["sales.orders"])
		assert.Equal(t, "audit.orders", qualifiers["audit.orders"])
		assert.NotContains(t, qualifiers, "orders")
	})

	t.Run("an alias hides the original table qualifiers", func(t *testing.T) {
		qualifiers := polyglotTableQualifierMap([]ParseContextTable{
			{Name: "example.range_10", ResolvedName: "example.range_10", Alias: "r"},
		})

		assert.Equal(t, "example.range_10", qualifiers["r"])
		assert.NotContains(t, qualifiers, "range_10")
		assert.NotContains(t, qualifiers, "example.range_10")
	})
}

func TestParseContextWithSchemaPolyglotResolvesUnqualifiedTableToUniqueSchemaEntry(t *testing.T) {
	// pg_get_viewdef drops the schema for tables on the search path, so
	// imported views reference "accounts" while the asset is "public.accounts".
	parseContext, err := ParseContextWithSchemaPolyglot(
		"select id, user_id from accounts",
		"postgres",
		Schema{
			"public.accounts": {"id": "text", "user_id": "text"},
			"public.users":    {"id": "text", "email": "text"},
		},
	)
	require.NoError(t, err)

	assert.Empty(t, parseContext.Diagnostics)
	require.Len(t, parseContext.Tables, 1)
	assert.Equal(t, "accounts", parseContext.Tables[0].Name)
	assert.Equal(t, "public.accounts", parseContext.Tables[0].ResolvedName)
}

func TestParseContextWithSchemaPolyglotKeepsAmbiguousUnqualifiedTableUnresolved(t *testing.T) {
	parseContext, err := ParseContextWithSchemaPolyglot(
		"select id from accounts",
		"postgres",
		Schema{
			"public.accounts": {"id": "text"},
			"audit.accounts":  {"id": "text"},
		},
	)
	require.NoError(t, err)

	assert.Contains(t, diagnosticMessages(parseContext.Diagnostics), "Unresolved table: accounts")
}

func TestParseContextWithSchemaPolyglotReportsUnknownTable(t *testing.T) {
	parseContext, err := ParseContextWithSchemaPolyglot(
		"select * from analytics.ordrs",
		"duckdb",
		Schema{"analytics.orders": {"order_id": "integer"}},
	)
	require.NoError(t, err)

	assert.Contains(t, diagnosticMessages(parseContext.Diagnostics), "Unresolved table: analytics.ordrs")
	require.NotEmpty(t, parseContext.Diagnostics)
	require.NotNil(t, parseContext.Diagnostics[0].Range)
	assert.Equal(t, "analytics.ordrs", parseContext.Diagnostics[0].Range.RangeText("select * from analytics.ordrs"))
}

func TestParseContextWithSchemaPolyglotReportsDanglingComparisonOperator(t *testing.T) {
	query := `SELECT
  small
FROM simple.small
WHERE
  small = 1 AND small = 1
  >   -- I'd expect the '<' to cause problems`
	parseContext, err := ParseContextWithSchemaPolyglot(
		query,
		"duckdb",
		Schema{"simple.small": {"small": "integer"}},
	)
	require.NoError(t, err)

	require.NotEmpty(t, parseContext.Diagnostics)
	require.NotNil(t, parseContext.Diagnostics[0].Range)
	assert.Equal(t, ">", parseContext.Diagnostics[0].Range.RangeText(query))
}

func (r ParseContextRange) RangeText(query string) string {
	if r.Start < 0 || r.End > len(query) || r.Start > r.End {
		return ""
	}
	return query[r.Start:r.End]
}

func diagnosticMessages(diagnostics []ParseContextDiagnostic) []string {
	result := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		result = append(result, diagnostic.Message)
	}
	return result
}

func TestParseContextReadOnlyResultClassification(t *testing.T) {
	cases := []struct {
		name         string
		query        string
		singleSelect bool
		readOnly     bool
	}{
		{"plain select", "select 1 as n", true, true},
		{"cte select", "with t as (select 1 as n) select * from t", true, true},
		{"union all", "select 1 as n union all select 2", false, true},
		{"intersect", "select 1 as n intersect select 1", false, true},
		{"except", "select 1 as n except select 2", false, true},
		{"create table", "create table t as select 1 as n", false, false},
		{"insert", "insert into t values (1)", false, false},
		{"multi statement", "select 1; select 2", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pc, err := ParseContextWithSchemaPolyglot(tc.query, "duckdb", Schema{})
			require.NoError(t, err)
			assert.Equal(t, tc.singleSelect, pc.IsSingleSelect, "IsSingleSelect")
			assert.Equal(t, tc.readOnly, pc.IsReadOnlyResult, "IsReadOnlyResult")
		})
	}
}
