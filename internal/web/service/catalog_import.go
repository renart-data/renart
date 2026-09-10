package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/bruin-data/bruin/pkg/ansisql"
	"renart/internal/web/sqlnamespace"
)

// Revalidate the requested catalog's immediate table listing. Source creation
// must not use GetDatabaseSummary, whose scope is the connection default.
func catalogImportSummary(ctx context.Context, connection any, engine string, parts []string) (*ansisql.DBDatabase, error) {
	selector, ok := connection.(sqlnamespace.Selector)
	if !ok {
		return nil, fmt.Errorf("connection does not support catalog source discovery")
	}
	if len(parts) != 3 {
		return nil, fmt.Errorf("a fully qualified table is required")
	}
	for _, part := range parts {
		// Asset names are also filesystem paths and Bruin identifiers. Browsing
		// supports quoted names more broadly; never turn unusual names into paths.
		if strings.ContainsAny(part, "/\\\x00\r\n\"`[].") || strings.TrimSpace(part) != part || part == "" {
			return nil, fmt.Errorf("this table can be browsed, but its quoted name cannot be represented safely as a Source asset")
		}
	}
	scope := sqlnamespace.Scope{Catalog: parts[0], Schema: parts[1]}
	if engine == "starrocks" || engine == "doris" {
		scope.Database, scope.Schema = parts[1], ""
	}
	entries, err := (sqlnamespace.Provider{Engine: engine, Client: selector}).Children(ctx, scope)
	if err != nil {
		return nil, err
	}
	matches := 0
	for _, entry := range entries {
		if entry.Kind == "table" && entry.Name == parts[2] {
			matches++
		}
	}
	if matches != 1 {
		return nil, fmt.Errorf("the selected table is no longer available in catalog %q", parts[0])
	}
	return &ansisql.DBDatabase{Name: parts[0], Schemas: []*ansisql.DBSchema{{Name: parts[0] + "." + parts[1], Tables: []*ansisql.DBTable{{Name: parts[2], Type: ansisql.DBTableTypeTable}}}}}, nil
}
