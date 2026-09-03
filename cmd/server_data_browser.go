package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"renart/internal/web/databrowser"
	"renart/internal/web/service"
)

// configureDataBrowserService keeps credential-free discovery and bounded
// preview adapters together at the composition root. The focused service owns
// object identity, local-path policy, and preview query construction.
func configureDataBrowserService(server *webServer, workspaceRoot string) {
	server.dataBrowserSvc = databrowser.New(databrowser.Dependencies{
		WorkspaceRoot: workspaceRoot,
		ListConnections: func(_ context.Context, environment string) (string, []databrowser.ConnectionConfig, int64, error) {
			resolvedEnvironment, summaries, err := server.configSvc.ConnectionSummaries(environment)
			if err != nil {
				return "", nil, 0, err
			}
			connections := make([]databrowser.ConnectionConfig, 0, len(summaries))
			for name, connectionType := range summaries {
				connections = append(connections, databrowser.ConnectionConfig{
					Name:      name,
					Type:      connectionType,
					Queryable: service.IsQueryableConnectionType(connectionType),
				})
			}
			state := server.currentState()
			revision := int64(0)
			if state.SelectedEnvironment == resolvedEnvironment {
				revision = state.Revision
			}
			return resolvedEnvironment, connections, revision, nil
		},
		ListDatabases: func(ctx context.Context, connection, environment string) ([]string, error) {
			result, apiErr := server.sqlSvc.Databases(ctx, connection, environment)
			if apiErr != nil {
				return nil, errors.New(apiErr.Message)
			}
			return result.Databases, nil
		},
		ListTables: func(ctx context.Context, connection, database, environment string) ([]databrowser.Table, error) {
			result, apiErr := server.sqlSvc.Tables(ctx, connection, database, environment)
			if apiErr != nil {
				return nil, errors.New(apiErr.Message)
			}
			tables := make([]databrowser.Table, 0, len(result.Tables))
			for _, table := range result.Tables {
				tables = append(tables, databrowser.Table{
					Name:         table.Name,
					ShortName:    table.ShortName,
					SchemaName:   table.SchemaName,
					DatabaseName: table.DatabaseName,
				})
			}
			return tables, nil
		},
		ListColumns: func(ctx context.Context, connection, table, environment string) ([]service.SQLColumn, error) {
			result, status := server.sqlSvc.TableColumns(ctx, connection, table, environment)
			if status < http.StatusOK || status >= http.StatusMultipleChoices || result.Status != "ok" {
				if strings.TrimSpace(result.Error) != "" {
					return nil, errors.New(result.Error)
				}
				return nil, fmt.Errorf("column discovery failed with status %d", status)
			}
			return result.Columns, nil
		},
		RunQuery: func(ctx context.Context, connection, environment, query string, limit int) (databrowser.QueryResult, error) {
			result := server.sqlSvc.Query(ctx, connection, environment, query, limit)
			if result.Status != "ok" {
				return databrowser.QueryResult{}, errors.New(result.Error)
			}
			return databrowser.QueryResult{
				Columns:   result.Columns,
				Rows:      result.Rows,
				Truncated: result.Truncated,
			}, nil
		},
	})
}
