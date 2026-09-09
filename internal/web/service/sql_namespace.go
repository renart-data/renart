package service

import (
	"context"
	"fmt"

	"github.com/bruin-data/bruin/pkg/config"
	"renart/internal/web/sqlnamespace"
)

// NamespaceChildren shares the execution connection and catalog observations
// with SQL pickers/LSP; the browser itself owns no credentialed adapters.
func (s *SQLService) NamespaceChildren(ctx context.Context, connectionName string, scope sqlnamespace.Scope, environment string) ([]sqlnamespace.Entry, error) {
	manager, err := s.deps.NewConnectionManager(ctx, environment)
	if err != nil {
		return nil, err
	}
	conn, err := resolveRuntimeConnection(manager, connectionName)
	if err != nil {
		return nil, err
	}
	selector, ok := conn.(sqlnamespace.Selector)
	if !ok {
		return nil, fmt.Errorf("namespace discovery is unavailable for this connection")
	}
	kind := normalizeConnectionType(manager.GetConnectionType(connectionName))
	provider := sqlnamespace.Provider{Engine: kind, Client: selector, DefaultCatalog: configuredCatalog(manager.GetConnectionDetails(connectionName))}
	entries, err := provider.Children(ctx, scope)
	if err != nil {
		return nil, err
	}
	if s.catalogObserver != nil {
		tables := make([]SQLDiscoveryTableItem, 0)
		for _, entry := range entries {
			if entry.Kind == "table" {
				tables = append(tables, SQLDiscoveryTableItem{Name: entry.Reference, ShortName: entry.Name, CatalogName: entry.Scope.Catalog, DatabaseName: entry.Scope.Database, SchemaName: entry.Scope.Schema})
			}
		}
		if len(tables) > 0 {
			s.catalogObserver.ObserveTables(RemoteCatalogScope{Connection: connectionName, Environment: environment}, tables)
		}
	}
	return entries, nil
}

func configuredCatalog(details any) string {
	switch c := details.(type) {
	case *config.StarRocksConnection:
		if c != nil {
			return c.Catalog
		}
	case *config.TrinoConnection:
		if c != nil {
			return c.Catalog
		}
	case *config.DatabricksConnection:
		if c != nil {
			return c.Catalog
		}
	}
	return ""
}
