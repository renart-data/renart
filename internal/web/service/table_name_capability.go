package service

import "github.com/bruin-data/bruin/pkg/tablename"

// The pinned Bruin capability predates external catalogs in these MySQL-wire
// engines. Extend only their qualified-name contract; don't change MySQL.
func warehouseTableCapability(engine string) (tablename.Capability, bool) {
	capability, ok := tablename.For(engine)
	if ok && (engine == "starrocks" || engine == "doris") {
		capability.MaxComponents = 3
		capability.Labels = [3]string{"catalog", "database", "table"}
		capability.FormatDesc = "`table`, `database.table`, or `catalog.database.table`"
	}
	return capability, ok
}
