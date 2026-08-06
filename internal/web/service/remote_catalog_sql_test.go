package service

import (
	"context"
	"testing"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingRemoteCatalogObserver struct {
	tableScope    RemoteCatalogScope
	tables        []SQLDiscoveryTableItem
	columnScope   RemoteCatalogScope
	columnTable   string
	columnResults []SQLColumn
}

func (o *recordingRemoteCatalogObserver) ObserveTables(scope RemoteCatalogScope, tables []SQLDiscoveryTableItem) {
	o.tableScope = scope
	o.tables = append([]SQLDiscoveryTableItem(nil), tables...)
}

func (o *recordingRemoteCatalogObserver) ObserveColumns(scope RemoteCatalogScope, table string, columns []SQLColumn) {
	o.columnScope = scope
	o.columnTable = table
	o.columnResults = append([]SQLColumn(nil), columns...)
}

type remoteCatalogDiscoveryConnection struct{}

func (remoteCatalogDiscoveryConnection) GetDatabases(context.Context) ([]string, error) {
	return []string{"catalog"}, nil
}

func (remoteCatalogDiscoveryConnection) GetTablesWithSchemas(context.Context, string) (map[string][]string, error) {
	return map[string][]string{"analytics": {"orders"}}, nil
}

func TestSQLServiceSeedsRemoteCatalogObserverFromDiscoveryEndpoints(t *testing.T) {
	observer := &recordingRemoteCatalogObserver{}
	executor := &stubExecutionExecutor{
		queryConnOutput: []byte(`{"columns":[{"name":"order_id","type":"BIGINT"}]}`),
	}
	service := NewSQLService(SQLDependencies{
		Executor: executor,
		NewConnectionManager: func(context.Context, string) (config.ConnectionAndDetailsGetter, error) {
			return &stubConnectionManager{
				conn:           remoteCatalogDiscoveryConnection{},
				connectionType: "postgres",
			}, nil
		},
	})
	service.SetRemoteCatalogObserver(observer)

	tables, apiErr := service.Tables(t.Context(), "warehouse", "catalog", "dev")
	require.Nil(t, apiErr)
	require.Len(t, tables.Tables, 1)
	assert.Equal(t, RemoteCatalogScope{Connection: "warehouse", Environment: "dev"}, observer.tableScope)
	assert.Equal(t, tables.Tables, observer.tables)

	columns, status := service.TableColumns(t.Context(), "warehouse", "catalog.analytics.orders", "dev")
	assert.Equal(t, 200, status)
	require.Equal(t, "ok", columns.Status)
	assert.Equal(t, RemoteCatalogScope{Connection: "warehouse", Environment: "dev"}, observer.columnScope)
	assert.Equal(t, "catalog.analytics.orders", observer.columnTable)
	assert.Equal(t, []SQLColumn{{Name: "order_id", Type: "BIGINT"}}, observer.columnResults)
}
