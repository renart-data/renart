package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	webmodel "renart/internal/web/model"
	"renart/internal/web/service/assetmeta"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTransactionWorkspace(t *testing.T, header string) (*AssetService, string, string) {
	t.Helper()
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
	// Sibling assets so dependency names resolve within the pipeline.
	require.NoError(t, os.WriteFile(filepath.Join(assetsRoot, "orders.sql"), []byte("/* @bruin\nname: analytics.orders\ntype: duckdb.sql\n@bruin */\n\nselect 1 as id\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(assetsRoot, "customers.sql"), []byte(header+"\nselect 1 as order_id\n"), 0o644))

	service := NewAssetService(AssetDependencies{
		WorkspaceRoot:                workspaceRoot,
		ResolveAssetByID:             newAssetTestResolver(workspaceRoot).ResolveAssetByID,
		SuppressWatcher:              func(string) {},
		PushWorkspaceUpdateImmediate: func(context.Context, string, string) {},
	})
	return service, EncodeID("analytics/assets/customers.sql"), filepath.Join(assetsRoot, "customers.sql")
}

const txCustomersHeader = `/* @bruin
name: analytics.customers
type: duckdb.sql
materialization:
  type: view
@bruin */`

func TestApplyTransactionAssetURISetAndClear(t *testing.T) {
	service, assetID, absPath := newTransactionWorkspace(t, txCustomersHeader)

	result, apiErr := service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type:     TxAssetURISet,
		AssetURI: " duckdb://warehouse/analytics/customers ",
	})
	require.Nil(t, apiErr)
	assert.Equal(t, "duckdb://warehouse/analytics/customers", result.URI)
	content, err := os.ReadFile(absPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "uri: duckdb://warehouse/analytics/customers")

	result, apiErr = service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type: TxAssetURISet,
	})
	require.Nil(t, apiErr)
	assert.Empty(t, result.URI)
	content, err = os.ReadFile(absPath)
	require.NoError(t, err)
	assert.NotContains(t, string(content), "uri:")
}

func TestApplyTransactionDependencyManualAdd(t *testing.T) {
	service, assetID, absPath := newTransactionWorkspace(t, txCustomersHeader)

	res, apiErr := service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type:       TxDependencyManualAdd,
		Dependency: &TransactionDependency{Asset: "analytics.orders", Mode: "symbolic"},
	})
	require.Nil(t, apiErr)
	assert.Equal(t, []string{"analytics.orders"}, res.Upstreams)

	content, err := os.ReadFile(absPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "depends:")
	assert.Contains(t, string(content), "a:analytics.orders#symbolic")
}

func TestApplyTransactionDependencyIgnoreAndRestore(t *testing.T) {
	service, assetID, _ := newTransactionWorkspace(t, txCustomersHeader)

	// Add a dependency, then ignore it.
	_, apiErr := service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type:       TxDependencyManualAdd,
		Dependency: &TransactionDependency{Asset: "analytics.orders"},
	})
	require.Nil(t, apiErr)

	res, apiErr := service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type:          TxDependencyInferredIgnore,
		DependencyKey: "a:analytics.orders#full",
	})
	require.Nil(t, apiErr)
	assert.Empty(t, res.Upstreams, "ignored dependency should be removed from upstreams")

	_, _, asset, err := service.deps.ResolveAssetByID(context.Background(), assetID)
	require.NoError(t, err)
	meta := assetmeta.Parse(asset.Meta)
	require.Len(t, meta.DepDrop, 1)
	assert.Equal(t, "a:analytics.orders#full", meta.DepDrop[0])

	// Restore brings it back and clears the drop.
	res, apiErr = service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type:          TxDependencyInferredRestore,
		DependencyKey: "a:analytics.orders#full",
	})
	require.Nil(t, apiErr)
	assert.Equal(t, []string{"analytics.orders"}, res.Upstreams)

	_, _, asset, err = service.deps.ResolveAssetByID(context.Background(), assetID)
	require.NoError(t, err)
	assert.Empty(t, assetmeta.Parse(asset.Meta).DepDrop)
}

func TestApplyTransactionColumnOwnPreservesTypeOnReconcile(t *testing.T) {
	header := `/* @bruin
name: analytics.customers
type: duckdb.sql
materialization:
  type: view
columns:
  - name: order_total
    type: numeric
@bruin */`
	service, assetID, _ := newTransactionWorkspace(t, header)

	// User takes ownership of the column's type.
	_, apiErr := service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type:   TxColumnFieldOwn,
		Column: "order_total",
		Field:  "type",
	})
	require.Nil(t, apiErr)

	// A later inference saying integer must not override the owned type.
	result, apiErr := service.ReconcileAssetColumns(context.Background(), assetID, []webmodel.Column{
		{Name: "order_total", Type: "integer"},
	})
	require.Nil(t, apiErr)
	require.Len(t, result.Columns, 1)
	assert.Equal(t, "numeric", result.Columns[0].Type)
}

func TestApplyTransactionColumnManualAddPersistsColumnLocalProvenance(t *testing.T) {
	service, assetID, _ := newTransactionWorkspace(t, txCustomersHeader)

	result, apiErr := service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type:      TxColumnManualAdd,
		ColumnDef: &webmodel.Column{Name: "manual_note", Type: "VARCHAR"},
	})
	require.Nil(t, apiErr)
	require.Len(t, result.Columns, 1)
	assert.Equal(t, "manual_note", result.Columns[0].Name)

	_, _, asset, err := service.deps.ResolveAssetByID(context.Background(), assetID)
	require.NoError(t, err)
	require.Len(t, asset.Columns, 1)
	assert.Equal(t, "true", asset.Columns[0].Meta[assetmeta.ColumnKeyManual])
	assert.NotContains(t, asset.Meta, assetmeta.KeyColAdd)

	reconciled, reconcileErr := service.ReconcileAssetColumns(context.Background(), assetID, []webmodel.Column{
		{Name: "order_id", Type: "INTEGER"},
	})
	require.Nil(t, reconcileErr)
	require.Len(t, reconciled.Columns, 2)
	assert.ElementsMatch(t, []string{"order_id", "manual_note"}, []string{
		reconciled.Columns[0].Name,
		reconciled.Columns[1].Name,
	})
}

func TestApplyTransactionColumnDropAndDescription(t *testing.T) {
	header := `/* @bruin
name: analytics.customers
type: duckdb.sql
materialization:
  type: view
columns:
  - name: order_id
    type: integer
  - name: debug
    type: integer
@bruin */`
	service, assetID, _ := newTransactionWorkspace(t, header)

	// Set a description on order_id.
	res, apiErr := service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type:        TxColumnDescriptionSet,
		Column:      "order_id",
		Description: "the order identifier",
	})
	require.Nil(t, apiErr)
	var orderID *webmodel.Column
	for i := range res.Columns {
		if res.Columns[i].Name == "order_id" {
			orderID = &res.Columns[i]
		}
	}
	require.NotNil(t, orderID)
	assert.Equal(t, "the order identifier", orderID.Description)

	// Drop the debug column.
	res, apiErr = service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type:   TxColumnInferredDrop,
		Column: "debug",
	})
	require.Nil(t, apiErr)
	for _, c := range res.Columns {
		if c.Name == "debug" {
			t.Fatalf("debug column should have been dropped: %+v", res.Columns)
		}
	}

	_, _, asset, err := service.deps.ResolveAssetByID(context.Background(), assetID)
	require.NoError(t, err)
	assert.Equal(t, []string{"debug"}, assetmeta.Parse(asset.Meta).ColDrop)
}

func TestApplyTransactionColumnCheckAddAndRemove(t *testing.T) {
	header := `/* @bruin
name: analytics.customers
type: duckdb.sql
materialization:
  type: view
columns:
  - name: order_id
    type: integer
@bruin */`
	service, assetID, _ := newTransactionWorkspace(t, header)

	res, apiErr := service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type:   TxColumnCheckAdd,
		Column: "order_id",
		Check:  &webmodel.ColumnCheck{Name: "not_null"},
	})
	require.Nil(t, apiErr)
	require.Len(t, res.Columns, 1)
	require.Len(t, res.Columns[0].Checks, 1)
	assert.Equal(t, "not_null", res.Columns[0].Checks[0].Name)

	res, apiErr = service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type:   TxColumnCheckRemove,
		Column: "order_id",
		Check:  &webmodel.ColumnCheck{Name: "not_null"},
	})
	require.Nil(t, apiErr)
	require.Len(t, res.Columns, 1)
	assert.Empty(t, res.Columns[0].Checks, "the check should have been removed")
}

func TestApplyTransactionCustomCheckAddUpdateAndRemove(t *testing.T) {
	service, assetID, absPath := newTransactionWorkspace(t, txCustomersHeader)
	expected := int64(0)

	res, apiErr := service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type: TxCustomCheckUpsert,
		CustomCheck: &webmodel.CustomCheck{
			Name:        "valid orders",
			Description: "No invalid order IDs",
			Count:       &expected,
			Query:       "select * from analytics.customers where order_id < 0",
		},
	})
	require.Nil(t, apiErr)
	require.Len(t, res.CustomChecks, 1)
	assert.Equal(t, "valid orders", res.CustomChecks[0].Name)
	assert.Equal(t, int64(0), *res.CustomChecks[0].Count)

	res, apiErr = service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type:            TxCustomCheckUpsert,
		CustomCheckName: "valid orders",
		CustomCheck: &webmodel.CustomCheck{
			Name:  "non-negative orders",
			Value: 1,
			Query: "select 1",
		},
	})
	require.Nil(t, apiErr)
	require.Len(t, res.CustomChecks, 1)
	assert.Equal(t, "non-negative orders", res.CustomChecks[0].Name)
	assert.Nil(t, res.CustomChecks[0].Count)
	assert.Equal(t, int64(1), res.CustomChecks[0].Value)

	content, err := os.ReadFile(absPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "custom_checks:")
	assert.Contains(t, string(content), "name: non-negative orders")
	assert.NotContains(t, string(content), "name: valid orders")

	res, apiErr = service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type:            TxCustomCheckRemove,
		CustomCheckName: "non-negative orders",
	})
	require.Nil(t, apiErr)
	assert.Empty(t, res.CustomChecks)
}

func TestApplyTransactionCustomCheckRejectsDuplicateName(t *testing.T) {
	header := `/* @bruin
name: analytics.customers
type: duckdb.sql
custom_checks:
  - name: first
    value: 0
    query: select 0
  - name: second
    value: 0
    query: select 0
@bruin */`
	service, assetID, _ := newTransactionWorkspace(t, header)

	_, apiErr := service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type:            TxCustomCheckUpsert,
		CustomCheckName: "first",
		CustomCheck:     &webmodel.CustomCheck{Name: "second", Query: "select 0"},
	})
	require.NotNil(t, apiErr)
	assert.Equal(t, "duplicate_custom_check", apiErr.Code)
}

func TestApplyTransactionHookAddUpdateAndRemove(t *testing.T) {
	service, assetID, absPath := newTransactionWorkspace(t, txCustomersHeader)

	res, apiErr := service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type:      TxHookUpsert,
		HookPhase: "pre",
		HookQuery: "create table if not exists audit_log(id bigint)",
	})
	require.Nil(t, apiErr)
	require.Equal(t, []string{"create table if not exists audit_log(id bigint)"}, res.PreHooks)
	assert.Empty(t, res.PostHooks)

	index := 0
	res, apiErr = service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type:      TxHookUpsert,
		HookPhase: "pre",
		HookIndex: &index,
		HookQuery: "delete from audit_log",
	})
	require.Nil(t, apiErr)
	assert.Equal(t, []string{"delete from audit_log"}, res.PreHooks)

	res, apiErr = service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type:      TxHookUpsert,
		HookPhase: "post",
		HookQuery: "insert into audit_log values (1)",
	})
	require.Nil(t, apiErr)
	assert.Equal(t, []string{"insert into audit_log values (1)"}, res.PostHooks)

	res, apiErr = service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type:      TxHookRemove,
		HookPhase: "pre",
		HookIndex: &index,
	})
	require.Nil(t, apiErr)
	assert.Empty(t, res.PreHooks)

	content, err := os.ReadFile(absPath)
	require.NoError(t, err)
	assert.NotContains(t, string(content), "delete from audit_log")
	assert.Contains(t, string(content), "post:")
	assert.Contains(t, string(content), "insert into audit_log values (1)")
}

func TestApplyTransactionClearsInactiveMaterializationMetadata(t *testing.T) {
	header := `/* @bruin
name: analytics.customers
type: duckdb.sql
materialization:
  type: table
  strategy: create+replace
  partition_by: event_date
  cluster_by:
    - customer_id
columns:
  - name: customer_id
    type: bigint
    update_on_merge: true
    merge_sql: source.customer_id
@bruin */`
	service, assetID, absPath := newTransactionWorkspace(t, header)

	_, apiErr := service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type:   TxColumnMergeSettingsClear,
		Column: "customer_id",
	})
	require.Nil(t, apiErr)
	_, apiErr = service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type: TxMaterializationPartitionByClear,
	})
	require.Nil(t, apiErr)
	_, apiErr = service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{
		Type: TxMaterializationClusterByClear,
	})
	require.Nil(t, apiErr)

	_, _, asset, err := service.deps.ResolveAssetByID(context.Background(), assetID)
	require.NoError(t, err)
	require.Len(t, asset.Columns, 1)
	assert.False(t, asset.Columns[0].UpdateOnMerge)
	assert.Empty(t, asset.Columns[0].MergeSQL)
	assert.Empty(t, asset.Materialization.PartitionBy)
	assert.Empty(t, asset.Materialization.ClusterBy)

	content, err := os.ReadFile(absPath)
	require.NoError(t, err)
	assert.NotContains(t, string(content), "update_on_merge")
	assert.NotContains(t, string(content), "merge_sql")
	assert.NotContains(t, string(content), "partition_by")
	assert.NotContains(t, string(content), "cluster_by")
}

func TestApplyTransactionUnknownTypeErrors(t *testing.T) {
	service, assetID, _ := newTransactionWorkspace(t, txCustomersHeader)
	_, apiErr := service.ApplyAssetTransaction(context.Background(), assetID, AssetTransaction{Type: "bogus.tx"})
	require.NotNil(t, apiErr)
}
