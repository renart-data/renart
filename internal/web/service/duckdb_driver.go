package service

import (
	"context"
	"database/sql"

	duck "github.com/bruin-data/bruin/pkg/duckdb"
)

// DuckDBDriver reports and installs the DuckDB ADBC driver that every local
// DuckDB connection loads. Bruin downloads it (about 70 MB) the first time a
// machine opens DuckDB; the first-run flow prepares it as its own step so the
// download is visible instead of stalling the first asset.
type DuckDBDriver struct{}

// Ready reports whether the driver loads, using the same probe Bruin runs
// before deciding to download it.
func (DuckDBDriver) Ready(ctx context.Context) bool {
	db, err := sql.Open(duck.ADBCDriverName(), "driver=duckdb;path=:memory:")
	if err != nil {
		return false
	}
	defer db.Close()
	return db.PingContext(ctx) == nil
}

// Ensure downloads the driver when it is missing. Bruin remembers the first
// result for the life of the process, so a failed download keeps failing
// until Renart restarts.
func (DuckDBDriver) Ensure(ctx context.Context) error {
	return duck.EnsureADBCDriverInstalled(ctx)
}
