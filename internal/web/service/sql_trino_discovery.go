package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bruin-data/bruin/pkg/query"
)

type discoverySelector interface {
	Select(context.Context, *query.Query) ([][]any, error)
}

// The pinned Trino client can query metadata but does not implement Bruin's
// discovery interfaces. Adapt it locally without changing connection ownership
// or installing another credentialed client.
func sqlDiscoveryAdapter(connection any, connectionType string, catalog ...string) any {
	if selector, ok := connection.(discoverySelector); ok {
		switch normalizeConnectionType(connectionType) {
		case "trino":
			return trinoDiscovery{selector: selector}
		case "starrocks", "doris":
			selected := "default_catalog"
			if normalizeConnectionType(connectionType) == "doris" {
				selected = "internal"
			}
			if len(catalog) > 0 && catalog[0] != "" {
				selected = catalog[0]
			}
			return mysqlDiscovery{selector: selector, catalog: selected}
		case "mysql", "vitess", "planetscale":
			return mysqlDiscovery{selector: selector}
		}
	}
	return connection
}

// These clients speak the MySQL metadata protocol, but the pinned dependency
// only exposes Select. SHOW respects the connected user's object privileges.
type mysqlDiscovery struct {
	selector discoverySelector
	catalog  string
}

func (d mysqlDiscovery) GetDatabases(ctx context.Context) ([]string, error) {
	sql := "SHOW DATABASES"
	if d.catalog != "" {
		sql += " FROM `" + strings.ReplaceAll(d.catalog, "`", "``") + "`"
	}
	return discoveryNames(ctx, d.selector, sql)
}

func (d mysqlDiscovery) GetTables(ctx context.Context, database string) ([]string, error) {
	if strings.TrimSpace(database) == "" || strings.ContainsRune(database, '\x00') {
		return nil, fmt.Errorf("a valid database is required")
	}
	prefix := ""
	if d.catalog != "" {
		prefix = "`" + strings.ReplaceAll(d.catalog, "`", "``") + "`."
	}
	return discoveryNames(ctx, d.selector, "SHOW TABLES FROM "+prefix+"`"+strings.ReplaceAll(database, "`", "``")+"`")
}

func discoveryNames(ctx context.Context, selector discoverySelector, sql string) ([]string, error) {
	rows, err := selector.Select(ctx, &query.Query{Query: sql})
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	result := make([]string, 0, len(rows))
	for _, row := range rows {
		if len(row) == 0 {
			return nil, fmt.Errorf("SQL discovery returned an invalid row")
		}
		name, err := discoveryName(row[0])
		if err != nil {
			return nil, err
		}
		if !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result, nil
}

type trinoDiscovery struct{ selector discoverySelector }

func (d trinoDiscovery) GetDatabases(ctx context.Context) ([]string, error) {
	return discoveryNames(ctx, d.selector, "SHOW CATALOGS")
}

func (d trinoDiscovery) GetTablesWithSchemas(ctx context.Context, catalog string) (map[string][]string, error) {
	if strings.TrimSpace(catalog) == "" || strings.ContainsRune(catalog, '\x00') {
		return nil, fmt.Errorf("a valid Trino catalog is required")
	}
	// Catalog is one identifier, never an arbitrary dotted SQL expression. Keep
	// this explicit qualifier so browsing does not mutate the pooled session.
	sql := `SELECT table_schema, table_name FROM "` + strings.ReplaceAll(catalog, `"`, `""`) + `".information_schema.tables WHERE table_schema <> 'information_schema' ORDER BY table_schema, table_name`
	rows, err := d.selector.Select(ctx, &query.Query{Query: sql})
	if err != nil {
		return nil, err
	}
	result := make(map[string][]string)
	for _, row := range rows {
		if len(row) < 2 {
			return nil, fmt.Errorf("Trino table discovery returned an invalid row")
		}
		schema, err := discoveryName(row[0])
		if err != nil {
			return nil, err
		}
		table, err := discoveryName(row[1])
		if err != nil {
			return nil, err
		}
		result[schema] = append(result[schema], table)
	}
	return result, nil
}

func discoveryName(value any) (string, error) {
	var name string
	switch v := value.(type) {
	case string:
		name = v
	case []byte:
		name = string(v)
	}
	if name == "" {
		return "", fmt.Errorf("SQL discovery returned an invalid identifier")
	}
	return name, nil
}
