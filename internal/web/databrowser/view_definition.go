package databrowser

import (
	"fmt"
	"strings"
)

// Definition discovery reads the catalog, never the view itself. Unsupported
// engines return no query rather than trying a dialect or scanning all schemas.
func viewDefinitionQuery(ref objectRef) string {
	parts := identifierParts(ref.Name)
	name := ref.LeafName
	if name == "" {
		name = parts[len(parts)-1]
	}
	schema := ref.Schema
	if schema == "" && len(parts) > 1 {
		schema = parts[len(parts)-2]
	}
	catalog := ref.Database
	if len(parts) > 2 {
		catalog = strings.Join(parts[:len(parts)-2], ".")
	}
	if ref.Catalog != "" {
		catalog = ref.Catalog
	}
	if ref.Schema == "" && ref.Database != "" && (ref.ConnectionType == "starrocks" || ref.ConnectionType == "doris") {
		schema = ref.Database
		if ref.Catalog == "" && len(parts) < 3 {
			// Older operation tokens carried a database, not a catalog. Keep
			// their metadata query in the connection's configured default.
			catalog = ""
		}
	}
	// Some engines interpret backslashes in literals. Do not guess their session
	// escaping mode for these unusual identifiers; ordinary browsing still works.
	if strings.ContainsAny(name+schema+catalog, "\\\x00\r\n") {
		return ""
	}
	literal := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	identifier := func(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }
	source := "information_schema.views"
	switch ref.ConnectionType {
	case "duckdb", "motherduck":
		database := "current_database()"
		if ref.Catalog != "" || len(parts) > 2 {
			database = literal(catalog)
		}
		if schema == "" {
			return ""
		}
		return "select sql as view_definition from duckdb_views() where database_name = " + database + " and schema_name = " + literal(schema) + " and view_name = " + literal(name) + " limit 2"
	case "clickhouse":
		database := schema
		if database == "" {
			database = catalog
		}
		if database == "" {
			return ""
		}
		return "select create_table_query as view_definition from system.tables where database = " + literal(database) + " and name = " + literal(name) + " and engine in ('View', 'MaterializedView') limit 2"
	case "starrocks", "doris":
		if catalog != "" {
			source = "`" + strings.ReplaceAll(catalog, "`", "``") + "`.information_schema.views"
		}
	case "trino", "snowflake", "databricks":
		if catalog == "" {
			return ""
		}
		source = identifier(catalog) + ".information_schema.views"
		if ref.ConnectionType == "databricks" {
			source = "`" + strings.ReplaceAll(catalog, "`", "``") + "`.information_schema.views"
		}
	case "postgres", "redshift", "mysql":
		if schema == "" {
			schema = catalog
		}
	default:
		return ""
	}
	if schema == "" {
		return ""
	}
	return "select view_definition from " + source + " where table_schema = " + literal(schema) + " and table_name = " + literal(name) + " limit 2"
}

func (s *Service) describeError(ref objectRef, err error) error {
	message := strings.ToLower(err.Error())
	if ref.ConnectionType == "duckdb" && s.deps.WorkspaceRoot != "" &&
		(strings.Contains(message, "no files found that match") || strings.Contains(message, "no such file or directory")) {
		return fmt.Errorf("DuckDB could not find a file. Relative reads use the active project root %q. For a view from another or nested project, check its SQL definition and file paths. Details: %w", s.deps.WorkspaceRoot, err)
	}
	return err
}
