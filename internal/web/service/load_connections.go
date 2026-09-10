package service

import "strings"

// Load connection categories. A Load asset moves data from one of these to
// another, so the asset editor restricts its source/target connection pickers to
// connections whose type maps to one of these categories.
const (
	LoadCategoryDatabase = "database"
	LoadCategoryStorage  = "storage"
	LoadCategoryFile     = "file"
)

// loadDatabaseConnectionTypes are bruin connection types that Load can read
// from / write to as a database.
var loadDatabaseConnectionTypes = map[string]struct{}{
	"athena":                {},
	"clickhouse":            {},
	"couchbase":             {},
	"databricks":            {},
	"db2":                   {},
	"duckdb":                {},
	"dynamodb":              {},
	"elasticsearch":         {},
	"fabric":                {},
	"google_cloud_platform": {}, // BigQuery
	"hana":                  {},
	"mariadb":               {},
	"mongo":                 {},
	"mongo_atlas":           {},
	"motherduck":            {},
	"mssql":                 {},
	"mysql":                 {},
	"oracle":                {},
	"postgres":              {},
	"redshift":              {},
	"snowflake":             {},
	"spanner":               {},
	"sqlite":                {},
	"starrocks":             {},
	"synapse":               {},
	"trino":                 {},
	"vertica":               {},
}

// loadStorageConnectionTypes are object-storage backends Load reads/writes via
// file formats (CSV/Parquet/JSON).
var loadStorageConnectionTypes = map[string]struct{}{
	"s3":  {},
	"gcs": {},
}

// loadFileConnectionTypes are file transports (a remote/local filesystem rather
// than an object store).
var loadFileConnectionTypes = map[string]struct{}{
	"sftp": {},
}

// loadConnectionCategory classifies a bruin connection type into a Load
// category, or returns "" when the connection is not a Load-movable data store
// (e.g. an API source such as stripe or notion).
func loadConnectionCategory(connectionType string) string {
	normalized := strings.ToLower(strings.TrimSpace(connectionType))
	if _, ok := loadDatabaseConnectionTypes[normalized]; ok {
		return LoadCategoryDatabase
	}
	if _, ok := loadStorageConnectionTypes[normalized]; ok {
		return LoadCategoryStorage
	}
	if _, ok := loadFileConnectionTypes[normalized]; ok {
		return LoadCategoryFile
	}
	return ""
}

// SupportsNotebookWarehouseSource advertises only query connections with an
// existing typed snapshot transport, independent of their write permissions.
func SupportsNotebookWarehouseSource(connectionType string) bool {
	return IsQueryableConnectionType(connectionType) && loadConnectionCategory(connectionType) == LoadCategoryDatabase
}
