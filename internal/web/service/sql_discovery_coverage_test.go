package service

import (
	"context"
	"testing"

	"github.com/bruin-data/bruin/pkg/athena"
	"github.com/bruin-data/bruin/pkg/bigquery"
	"github.com/bruin-data/bruin/pkg/clickhouse"
	"github.com/bruin-data/bruin/pkg/databricks"
	"github.com/bruin-data/bruin/pkg/doris"
	"github.com/bruin-data/bruin/pkg/dremio"
	duckdb "github.com/bruin-data/bruin/pkg/duckdb"
	"github.com/bruin-data/bruin/pkg/fabric"
	"github.com/bruin-data/bruin/pkg/mssql"
	"github.com/bruin-data/bruin/pkg/mysql"
	"github.com/bruin-data/bruin/pkg/oracle"
	"github.com/bruin-data/bruin/pkg/postgres"
	"github.com/bruin-data/bruin/pkg/sail"
	"github.com/bruin-data/bruin/pkg/snowflake"
	"github.com/bruin-data/bruin/pkg/spark"
	"github.com/bruin-data/bruin/pkg/starrocks"
	"github.com/bruin-data/bruin/pkg/trino"
	"github.com/bruin-data/bruin/pkg/vertica"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Audit the actual pinned driver method sets without connecting to customer
// warehouses. Revisit the audit when a new platform or discovery method lands;
// method presence alone does not prove catalog scope or live permissions.
func TestSQLWarehouseDiscoveryCoverage(t *testing.T) {
	clients := map[string]any{
		"athena": (*athena.DB)(nil), "google_cloud_platform": (*bigquery.Client)(nil),
		"clickhouse": (*clickhouse.Client)(nil), "databricks": (*databricks.DB)(nil),
		"doris": (*doris.Client)(nil), "dremio": (*dremio.Client)(nil),
		"duckdb": (*duckdb.Client)(nil), "motherduck": (*duckdb.Client)(nil),
		"fabric": (*fabric.DB)(nil), "mssql": (*mssql.DB)(nil), "synapse": (*mssql.DB)(nil),
		"mysql": (*mysql.Client)(nil), "oracle": (*oracle.Client)(nil),
		"postgres": (*postgres.Client)(nil), "redshift": (*postgres.Client)(nil),
		"sail": (*sail.Client)(nil), "snowflake": (*snowflake.DB)(nil),
		"spark": (*spark.Client)(nil), "starrocks": (*starrocks.Client)(nil),
		"trino": (*trino.Client)(nil), "vertica": (*vertica.DB)(nil),
	}
	knownMissing := map[string]bool{"fabric": true, "dremio": true, "sail": true, "spark": true, "oracle": true}
	warehouses := warehouseConnectionTypes()
	require.Len(t, clients, len(warehouses), "update the discovery audit for new SQL platforms")
	for kind := range warehouses {
		t.Run(kind, func(t *testing.T) {
			client, exists := clients[kind]
			require.True(t, exists, "missing driver audit")
			adapted := sqlDiscoveryAdapter(client, kind)
			_, databases := adapted.(interface {
				GetDatabases(context.Context) ([]string, error)
			})
			_, tables := adapted.(interface {
				GetTables(context.Context, string) ([]string, error)
			})
			_, schemas := adapted.(interface {
				GetTablesWithSchemas(context.Context, string) (map[string][]string, error)
			})
			assert.Equal(t, !knownMissing[kind], databases)
			assert.Equal(t, !knownMissing[kind], tables || schemas)
			t.Logf("databases=%t tables=%t schemas=%t", databases, tables || schemas, schemas)
		})
	}
}
