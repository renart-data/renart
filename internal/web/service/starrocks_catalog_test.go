package service

import (
	"testing"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/starrocks"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

func TestStarRocksCatalogNativeConnectionDefaults(t *testing.T) {
	for _, database := range []string{"", "sales"} {
		t.Run(database, func(t *testing.T) {
			c := starRocksCatalogConfig{Config: starrocks.Config{Host: "localhost", Username: "test", Catalog: "lake", Database: database}}
			dsn, err := c.ToDBConnectionURI()
			require.NoError(t, err)
			parsed, err := mysql.ParseDSN(dsn)
			require.NoError(t, err)
			if database == "" {
				require.Equal(t, "'lake'", parsed.Params["catalog"])
				require.Empty(t, parsed.DBName)
			} else {
				require.Equal(t, "lake.sales", parsed.DBName)
			}
			require.Equal(t, database, c.Database, "runtime must not mutate saved database")
		})
	}
	cfg := &config.Config{SelectedEnvironment: &config.Environment{Connections: &config.Connections{StarRocks: []config.StarRocksConnection{{ConnectionMetadata: config.ConnectionMetadata{Name: "sr"}, Host: "localhost", Catalog: "lake", Database: "sales"}}}}}
	manager, err := newConnectionManagerFromConfig(t.Context(), cfg)
	require.NoError(t, err)
	require.IsType(t, &starrocks.Client{}, manager.GetConnection("sr"), "keep Bruin operator concrete-client compatibility")
	require.Equal(t, "sales", manager.GetConnectionDetails("sr").(*config.StarRocksConnection).Database)
}
