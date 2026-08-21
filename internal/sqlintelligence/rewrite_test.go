package sqlintelligence

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsReadOnlySingleQuery(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		query   string
		want    bool
		wantErr bool
	}{
		{name: "select", query: "SELECT * FROM users", want: true},
		{name: "CTE", query: "WITH active AS (SELECT * FROM users) SELECT * FROM active", want: true},
		{name: "union", query: "SELECT 1 UNION ALL SELECT 2", want: true},
		{name: "multiple", query: "SELECT 1; SELECT 2", want: false},
		{name: "insert", query: "INSERT INTO users SELECT 1", want: false},
		{name: "select into", query: "SELECT * INTO copied_users FROM users", want: false},
		{name: "invalid", query: "SELECT * FROM", wantErr: true},
		{name: "empty", query: "  \n", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := IsReadOnlySingleQuery(test.query, "duckdb")
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}

func TestRenameTablesPreservesUntouchedSource(t *testing.T) {
	t.Parallel()

	query := "select orders.id, note\nfrom analytics.orders -- keep this comment\nwhere note = 'analytics.orders'\n"
	got, err := RenameTables(query, "duckdb", map[string]string{"analytics.orders": "__renart_cell_base"})
	require.NoError(t, err)
	require.Equal(t, "select orders.id, note\nfrom __renart_cell_base AS orders -- keep this comment\nwhere note = 'analytics.orders'\n", got)
}

func TestRenameTablesRepairsQualifiedColumns(t *testing.T) {
	t.Parallel()

	query := "SELECT analytics.orders.id, o.name FROM analytics.orders JOIN analytics.owners AS o ON o.id = analytics.orders.owner_id"
	got, err := RenameTables(query, "postgres", map[string]string{"analytics.orders": "dev_analytics.orders"})
	require.NoError(t, err)
	require.Equal(t, "SELECT orders.id, o.name FROM dev_analytics.orders JOIN analytics.owners AS o ON o.id = orders.owner_id", got)
}

func TestRenameTablesKeepsExplicitAlias(t *testing.T) {
	t.Parallel()

	query := "SELECT o.id FROM analytics.orders o"
	got, err := RenameTables(query, "duckdb", map[string]string{"analytics.orders": "cell_orders"})
	require.NoError(t, err)
	require.Equal(t, "SELECT o.id FROM cell_orders o", got)
}

func TestRenameTablesRejectsSQLAsDestination(t *testing.T) {
	t.Parallel()

	_, err := RenameTables("select * from users", "duckdb", map[string]string{"users": "safe; drop table users"})
	require.Error(t, err)
}

func TestQualifyColumnPreservesSource(t *testing.T) {
	t.Parallel()

	query := "select id, /* id */ name as id\nfrom users u join orders o on u.id = o.user_id\nwhere id > 0 and note = 'id'"
	got, err := QualifyColumn(query, "postgres", "id", "o")
	require.NoError(t, err)
	require.Equal(t, "select o.id, /* id */ name as id\nfrom users u join orders o on u.id = o.user_id\nwhere o.id > 0 and note = 'id'", got)
}

func TestAliasRelationPreservesSourceAndQualifiers(t *testing.T) {
	t.Parallel()

	query := "select analytics.orders.id, orders.total\nfrom analytics.orders -- keep\nwhere analytics.orders.id > 0"
	got, err := AliasRelation(query, "duckdb", "analytics.orders", "o")
	require.NoError(t, err)
	require.Equal(t, "select o.id, o.total\nfrom analytics.orders AS o -- keep\nwhere o.id > 0", got)

	got, err = AliasRelation("select old.id from analytics.orders old", "duckdb", "analytics.orders", "renamed")
	require.NoError(t, err)
	require.Equal(t, "select renamed.id from analytics.orders renamed", got)
}

func TestAliasRelationRejectsAmbiguousOccurrence(t *testing.T) {
	t.Parallel()

	_, err := AliasRelation("select * from orders a join orders b on a.id = b.id", "duckdb", "orders", "o")
	require.ErrorContains(t, err, "occurs 2 times")
}

func TestAddLimitPreservesSource(t *testing.T) {
	t.Parallel()

	got, err := AddLimit("select  *\nfrom users; -- keep\n", 25, "duckdb")
	require.NoError(t, err)
	require.Equal(t, "select  *\nfrom users LIMIT 25; -- keep\n", got)

	got, err = AddLimit("SELECT * FROM users LIMIT /* requested */ 100", 25, "duckdb")
	require.NoError(t, err)
	require.Equal(t, "SELECT * FROM users LIMIT /* requested */ 25", got)
}

func TestAddLimitUsesDialectSyntax(t *testing.T) {
	t.Parallel()

	got, err := AddLimit("WITH x AS (SELECT 1 AS id) SELECT DISTINCT id FROM x", 7, "tsql")
	require.NoError(t, err)
	require.Equal(t, "WITH x AS (SELECT 1 AS id) SELECT DISTINCT TOP (7) id FROM x", got)

	got, err = AddLimit("SELECT * FROM users", 7, "oracle")
	require.NoError(t, err)
	require.Equal(t, "SELECT * FROM users FETCH FIRST 7 ROWS ONLY", got)
}

func TestAddLimitReplacesExistingLimitOnly(t *testing.T) {
	t.Parallel()

	got, err := AddLimit("SELECT TOP (100) * FROM users", 7, "tsql")
	require.NoError(t, err)
	require.Equal(t, "SELECT TOP (7) * FROM users", got)

	got, err = AddLimit("SELECT * FROM users FETCH FIRST 100 ROWS ONLY", 7, "oracle")
	require.NoError(t, err)
	require.Equal(t, "SELECT * FROM users FETCH FIRST 7 ROWS ONLY", got)
}
