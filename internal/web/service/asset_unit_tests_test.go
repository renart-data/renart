package service

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/unittest"
	"github.com/stretchr/testify/require"

	"renart/internal/sqlintelligence"
	webmodel "renart/internal/web/model"
)

func TestUnitTestExpectedPreservesExplicitEmptyRows(t *testing.T) {
	absent, err := json.Marshal(webmodel.SQLUnitTestExpected{})
	require.NoError(t, err)
	require.NotContains(t, string(absent), "rows")
	empty, err := json.Marshal(webmodel.SQLUnitTestExpected{Rows: []map[string]any{}})
	require.NoError(t, err)
	require.JSONEq(t, `{"rows":[]}`, string(empty))
}

func TestUnitTestEmptyRowsSurviveCanonicalPersistence(t *testing.T) {
	s, id, _ := newTransactionWorkspace(t, txCustomersHeader)
	current, apiErr := s.UnitTestContext(t.Context(), id)
	require.Nil(t, apiErr)
	saved, apiErr := s.ApplyAssetTransaction(t.Context(), id, AssetTransaction{
		Type: TxUnitTestsSet, ExpectedUnitTestsRevision: current.Revision,
		UnitTests: []webmodel.SQLUnitTest{{Name: "empty", Expected: webmodel.SQLUnitTestExpected{Rows: []map[string]any{}, Match: "exact"}}},
	})
	require.Nil(t, apiErr)
	reloaded, apiErr := s.UnitTestContext(t.Context(), id)
	require.Nil(t, apiErr)
	require.NoError(t, validateSQLUnitTests(reloaded.Tests))
	require.Equal(t, saved.UnitTestsRevision, reloaded.Revision)
	expected := modelUnitTestsToPipeline(reloaded.Tests)[0].Expected
	require.True(t, unittest.CompareResult(expected, []string{"n"}, nil).Passed)
	require.False(t, unittest.CompareResult(expected, []string{"n"}, [][]any{{1}}).Passed)
}

func TestUnitTestExecutionReturnsAssertionsWithoutMaterializing(t *testing.T) {
	s, id, _ := newTransactionWorkspace(t, txCustomersHeader)
	calls := 0
	s.deps.RunUnitTestQuery = func(ctx context.Context, connection, environment, query string) ([]string, []map[string]any, error) {
		calls++
		require.Equal(t, "duckdb-default", connection)
		require.Equal(t, "readonly", environment)
		_, ok := ctx.Deadline()
		require.True(t, ok)
		tables, err := sqlintelligence.UsedTables(query, "duckdb")
		require.NoError(t, err)
		require.Empty(t, tables)
		return []string{"order_id"}, []map[string]any{{"order_id": 1}}, nil
	}
	ctx, apiErr := s.UnitTestContext(t.Context(), id)
	require.Nil(t, apiErr)
	tests := []webmodel.SQLUnitTest{
		{Name: "passes", Expected: webmodel.SQLUnitTestExpected{Rows: []map[string]any{{"order_id": 1}}, Match: "exact"}},
		{Name: "fails", Expected: webmodel.SQLUnitTestExpected{Rows: []map[string]any{{"order_id": 2}}, Match: "exact"}},
	}
	saved, apiErr := s.ApplyAssetTransaction(t.Context(), id, AssetTransaction{Type: TxUnitTestsSet, UnitTests: tests, ExpectedUnitTestsRevision: ctx.Revision})
	require.Nil(t, apiErr)
	_, apiErr = s.RunUnitTests(t.Context(), id, SQLUnitTestRunRequest{Revision: ctx.Revision})
	require.Equal(t, 409, apiErr.Status)
	require.Zero(t, calls)
	result, apiErr := s.RunUnitTests(t.Context(), id, SQLUnitTestRunRequest{Revision: saved.UnitTestsRevision, Environment: "readonly"})
	require.Nil(t, apiErr)
	require.Len(t, result.Results, 2)
	require.Equal(t, "passed", result.Results[0].Status)
	require.Equal(t, "failed", result.Results[1].Status)
	require.NotEmpty(t, result.Results[1].Message)
	require.Equal(t, 2, calls)
}

func TestUnitTestResolvesUnqualifiedMocksAndInfersTheirColumns(t *testing.T) {
	s, id, path := newTransactionWorkspace(t, txCustomersHeader)
	require.NoError(t, os.WriteFile(path, []byte(txCustomersHeader+"\nselect id from orders\n"), 0o644))
	fixtureContext, apiErr := s.UnitTestContext(t.Context(), id)
	require.Nil(t, apiErr)
	require.Len(t, fixtureContext.Inputs, 1)
	require.Equal(t, "analytics.orders", fixtureContext.Inputs[0].Asset)
	require.Len(t, fixtureContext.Inputs[0].Columns, 1)
	require.Equal(t, "id", fixtureContext.Inputs[0].Columns[0].Name)
	require.Equal(t, "INTEGER", fixtureContext.Inputs[0].Columns[0].Type)
	saved, apiErr := s.ApplyAssetTransaction(t.Context(), id, AssetTransaction{
		Type: TxUnitTestsSet, ExpectedUnitTestsRevision: fixtureContext.Revision,
		UnitTests: []webmodel.SQLUnitTest{{Name: "unqualified", Inputs: []webmodel.SQLUnitTestInput{{Asset: "orders", Rows: []map[string]any{{"id": 9}}}}, Expected: webmodel.SQLUnitTestExpected{Rows: []map[string]any{{"id": 9}}, Match: "exact"}}},
	})
	require.Nil(t, apiErr)
	s.deps.RunUnitTestQuery = func(_ context.Context, _, _, query string) ([]string, []map[string]any, error) {
		tables, err := sqlintelligence.UsedTables(query, "duckdb")
		require.NoError(t, err)
		require.Empty(t, tables)
		require.Contains(t, query, "9")
		return []string{"id"}, []map[string]any{{"id": 9}}, nil
	}
	result, apiErr := s.RunUnitTests(t.Context(), id, SQLUnitTestRunRequest{Revision: saved.UnitTestsRevision})
	require.Nil(t, apiErr)
	require.Equal(t, "passed", result.Results[0].Status)
}

func TestUnitTestFreezesClocksButNotQuotedColumns(t *testing.T) {
	query, err := (unitTestRewriter{}).FreezeTime(`select current_date, now(), 'now()', "current_date"`, "duckdb", "2024-02-03 04:05:06")
	require.NoError(t, err)
	require.Contains(t, query, "2024-02-03")
	require.Contains(t, query, "04:05:06")
	require.Contains(t, query, `'now()'`)
	require.Contains(t, query, `"current_date"`)
}

func TestUnitTestBoundsPreserveOrderAndExistingLimit(t *testing.T) {
	for _, sql := range []string{"select 1 as n order by n limit 2", "with x as (select 1 as n) select * from x order by n limit 0"} {
		bounded, err := boundedUnitTestQuery(sql, "duckdb")
		require.NoError(t, err)
		require.Equal(t, sql, bounded)
	}
	for _, dialect := range []string{"duckdb", "tsql"} {
		bounded, err := boundedUnitTestQuery("with x as (select 1 as n) select * from x order by n", dialect)
		require.NoError(t, err)
		require.Contains(t, bounded, "5001")
		require.NotContains(t, bounded, "renart_unit_test_result")
	}
	_, err := boundedUnitTestQuery("select 1 limit (select 10)", "duckdb")
	require.Error(t, err)
}

func TestUnitTestTransactionsRoundTripAndConflict(t *testing.T) {
	s, id, path := newTransactionWorkspace(t, txCustomersHeader)
	definition := webmodel.SQLUnitTest{Name: "one order", Inputs: []webmodel.SQLUnitTestInput{{Asset: "analytics.orders", Rows: []map[string]any{{"id": 1.0}}}}, Expected: webmodel.SQLUnitTestExpected{Rows: []map[string]any{{"order_id": 1.0}}, Match: "exact"}}
	_, _, asset, err := s.deps.ResolveAssetByID(t.Context(), id)
	require.NoError(t, err)
	version := unitTestsRevision(asset.UnitTests)
	result, apiErr := s.ApplyAssetTransaction(t.Context(), id, AssetTransaction{Type: TxUnitTestsSet, UnitTests: []webmodel.SQLUnitTest{definition}, ExpectedUnitTestsRevision: version})
	require.Nil(t, apiErr)
	require.Len(t, result.UnitTests, 1)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(before), "unit_tests:")
	require.Contains(t, string(before), "select 1 as order_id")
	_, apiErr = s.ApplyAssetTransaction(t.Context(), id, AssetTransaction{Type: TxUnitTestsSet, ExpectedUnitTestsRevision: version})
	require.NotNil(t, apiErr)
	require.Equal(t, 409, apiErr.Status)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
	_, apiErr = s.ApplyAssetTransaction(t.Context(), id, AssetTransaction{Type: TxUnitTestsSet, ExpectedUnitTestsRevision: result.UnitTestsRevision, UnitTests: []webmodel.SQLUnitTest{}})
	require.Nil(t, apiErr)
	_, _, asset, err = s.deps.ResolveAssetByID(t.Context(), id)
	require.NoError(t, err)
	require.Empty(t, asset.UnitTests)
}

func TestUnitTestNativeCompilerUsesOnlyMockRelations(t *testing.T) {
	test := pipeline.UnitTest{Inputs: []pipeline.UnitTestInput{{Asset: "raw.orders", Rows: []map[string]any{{"id": 1, "amount": 10}, {"id": 2, "amount": 20}}}}}
	schemas := map[string][]pipeline.Column{"raw.orders": {{Name: "id", Type: "INTEGER"}, {Name: "amount", Type: "DOUBLE"}}}
	query, err := unittest.BuildWarehouseQuery(unitTestRewriter{}, "duckdb", "with totals as (select sum(amount) as total from raw.orders) select total from totals", test, schemas)
	require.NoError(t, err)
	tables, err := sqlintelligence.UsedTables(query, "duckdb")
	require.NoError(t, err)
	require.Empty(t, tables)
	require.Contains(t, query, "10")
	cte, err := (unitTestRewriter{}).SelectFromCTE(query, "duckdb", "totals")
	require.NoError(t, err)
	require.Contains(t, cte, "totals")
	_, err = (unitTestRewriter{}).SelectFromCTE(query, "duckdb", "missing")
	require.Error(t, err)
	for _, unsafe := range []string{
		"select * from read_csv('/private.csv')", "select 1; drop table x",
		"select 1 into x", "delete from raw.orders",
		"with removed as (delete from raw.orders returning *) select * from removed",
		"with changed as (update raw.orders set amount = 0 returning *) select * from changed",
		"with inserted as (insert into raw.orders values (1, 0) returning *) select * from inserted",
	} {
		_, err := (unitTestRewriter{}).ExtractSelect(unsafe, "duckdb")
		require.Error(t, err, unsafe)
	}
	_, err = unittest.BuildWarehouseQuery(unitTestRewriter{}, "duckdb", "select * from private.customers", test, schemas)
	require.Error(t, err, "unmocked relations without a schema must not read live tables")
}

func TestUnitTestValidationRejectsMalformedExpectations(t *testing.T) {
	for _, invalid := range []webmodel.SQLUnitTest{
		{Name: ""},
		{Name: "bad", Expected: webmodel.SQLUnitTestExpected{Match: "almost"}},
		{Name: "bad", Expected: webmodel.SQLUnitTestExpected{Order: "random"}},
		{Name: "bad", ExecutionTime: "not a time"},
		{Name: "bad", Inputs: []webmodel.SQLUnitTestInput{{Asset: "x"}, {Asset: "x"}}},
	} {
		require.Error(t, validateSQLUnitTests([]webmodel.SQLUnitTest{invalid}))
	}
}
