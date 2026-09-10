// Package sqlnamespace describes native warehouse namespaces without changing
// session state. It owns no connections, credentials, caches or HTTP transport.
package sqlnamespace

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/bruin-data/bruin/pkg/query"
)

type Scope struct{ Catalog, Database, Schema string }
type Entry struct {
	Scope     Scope
	Kind      string
	Name      string
	Reference string
	Default   bool
}
type Selector interface {
	Select(context.Context, *query.Query) ([][]any, error)
}
type Provider struct {
	Engine         string
	Client         Selector
	DefaultCatalog string
}

func Supported(engine string) bool {
	switch engine {
	case "starrocks", "doris", "trino", "databricks", "duckdb", "motherduck":
		return true
	}
	return false
}

func (p Provider) defaultCatalog(ctx context.Context) (string, error) {
	if p.DefaultCatalog != "" {
		return p.DefaultCatalog, nil
	}
	switch p.Engine {
	case "starrocks":
		return "default_catalog", nil
	case "doris":
		return "internal", nil
	case "duckdb", "motherduck", "databricks", "trino":
		sql := "SELECT current_database()"
		if p.Engine == "databricks" {
			sql = "SELECT current_catalog()"
		} else if p.Engine == "trino" {
			sql = "SELECT current_catalog"
		}
		rows, err := p.Client.Select(ctx, &query.Query{Query: sql})
		if err != nil {
			return "", err
		}
		if len(rows) == 1 && len(rows[0]) == 1 && rows[0][0] != nil {
			return name(rows[0][0])
		}
	}
	return "", fmt.Errorf("an explicit catalog is required for %s", p.Engine)
}

// Children lists only the immediate namespace. A missing catalog is accepted
// only for legacy addresses scoped to a database/schema, and resolves against
// the configured default; it never searches every catalog for a matching name.
func (p Provider) Children(ctx context.Context, scope Scope) ([]Entry, error) {
	if !Supported(p.Engine) || p.Client == nil {
		return nil, fmt.Errorf("catalog discovery is unavailable for %s", p.Engine)
	}
	for _, value := range []string{scope.Catalog, scope.Database, scope.Schema, p.DefaultCatalog} {
		if len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n\\") {
			return nil, fmt.Errorf("invalid namespace identifier")
		}
	}
	databaseEngine := p.Engine == "starrocks" || p.Engine == "doris"
	if (databaseEngine && scope.Schema != "") || (!databaseEngine && scope.Database != "") {
		return nil, fmt.Errorf("invalid %s namespace hierarchy", p.Engine)
	}
	leaf := scope.Schema
	if databaseEngine {
		leaf = scope.Database
	}
	if scope.Catalog == "" && leaf != "" {
		catalog, err := p.defaultCatalog(ctx)
		if err != nil {
			return nil, err
		}
		scope.Catalog = catalog
	}
	sql, kind, column := "", "catalog", 0
	q := func(value string) string { return Quote(p.Engine, value) }
	switch {
	case scope.Catalog == "":
		sql = "SHOW CATALOGS"
		if p.Engine == "doris" {
			column = 1
		}
		if p.Engine == "duckdb" || p.Engine == "motherduck" {
			sql = "SELECT database_name, database_name = current_database() FROM duckdb_databases() WHERE NOT internal ORDER BY database_name"
		}
	case leaf == "":
		kind = "schema"
		switch p.Engine {
		case "starrocks", "doris":
			kind = "database"
			sql = "SHOW DATABASES FROM " + q(scope.Catalog)
		case "trino":
			sql = "SHOW SCHEMAS FROM " + q(scope.Catalog)
		case "databricks":
			sql = "SHOW SCHEMAS IN " + q(scope.Catalog)
		case "duckdb", "motherduck":
			sql = "SELECT schema_name FROM information_schema.schemata WHERE catalog_name = " + literal(scope.Catalog) + " ORDER BY schema_name"
		}
	default:
		kind = "table"
		switch p.Engine {
		case "starrocks", "doris", "trino":
			sql = "SHOW TABLES FROM " + q(scope.Catalog) + "." + q(leaf)
		case "databricks":
			sql = "SHOW TABLES IN " + q(scope.Catalog) + "." + q(leaf)
			column = 1
		case "duckdb", "motherduck":
			sql = "SELECT table_name FROM information_schema.tables WHERE table_catalog = " + literal(scope.Catalog) + " AND table_schema = " + literal(leaf) + " ORDER BY table_name"
		}
	}
	rows, err := p.Client.Select(ctx, &query.Query{Query: sql})
	if err != nil {
		return nil, err
	}
	result := make([]Entry, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		if len(row) <= column {
			return nil, fmt.Errorf("invalid %s discovery result", p.Engine)
		}
		value, err := name(row[column])
		if err != nil {
			return nil, err
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		entry := Entry{Scope: scope, Kind: kind, Name: value}
		switch kind {
		case "catalog":
			entry.Scope.Catalog = value
			entry.Default = value == p.DefaultCatalog
			if p.DefaultCatalog == "" {
				entry.Default = (p.Engine == "starrocks" && value == "default_catalog") || (p.Engine == "doris" && value == "internal")
			}
			if (p.Engine == "duckdb" || p.Engine == "motherduck") && len(row) > 1 {
				if current, ok := row[1].(bool); ok {
					entry.Default = current
				}
			}
		case "database":
			entry.Scope.Database = value
		case "schema":
			entry.Scope.Schema = value
		case "table":
			entry.Reference = Reference(p.Engine, scope.Catalog, leaf, value)
		}
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func name(value any) (string, error) {
	var s string
	switch v := value.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	}
	if s == "" || strings.ContainsAny(s, "\x00\r\n\\") {
		return "", fmt.Errorf("discovery returned an invalid identifier")
	}
	return s, nil
}
func literal(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
func Quote(engine, value string) string {
	if engine == "bigquery" || engine == "google_cloud_platform" {
		return "`" + strings.NewReplacer("\\", "\\\\", "`", "\\`").Replace(value) + "`"
	}
	if engine == "mssql" || engine == "synapse" || engine == "fabric" {
		return "[" + strings.ReplaceAll(value, "]", "]]") + "]"
	}
	quote := `"`
	switch engine {
	case "starrocks", "doris", "databricks", "mysql":
		quote = "`"
	}
	return quote + strings.ReplaceAll(value, quote, quote+quote) + quote
}

var simpleIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Reference retains readable ordinary names and quotes individual unusual
// identifiers, never interpreting a literal dot as a namespace separator.
func Reference(engine string, parts ...string) string {
	result := make([]string, len(parts))
	for i, part := range parts {
		result[i] = part
		if !simpleIdentifier.MatchString(part) {
			result[i] = Quote(engine, part)
		}
	}
	return strings.Join(result, ".")
}
