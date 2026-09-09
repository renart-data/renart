package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/tablename"

	"renart/internal/sqlintelligence"
	webmodel "renart/internal/web/model"
	"renart/internal/web/preview"
	"renart/internal/web/sqlnamespace"
)

type SQLColumnValuesResult struct {
	Status string `json:"status"`
	Values []any  `json:"values"`
	Error  string `json:"error,omitempty"`
}

// renart:web
// renart:web-name SqlQueryResponse
type SQLQueryResult struct {
	Status    string                    `json:"status"`
	Columns   []string                  `json:"columns"`
	Rows      []map[string]any          `json:"rows"`
	Truncated bool                      `json:"truncated,omitempty"`
	Preview   *webmodel.PreviewMetadata `json:"preview,omitempty"`
	Error     string                    `json:"error,omitempty"`
}

// renart:web
// renart:web-name SqlDiscoveryDatabasesResponse
type SQLDatabaseDiscoveryResult struct {
	Status         string   `json:"status"`
	ConnectionName string   `json:"connection_name"`
	ConnectionType string   `json:"connection_type,omitempty"`
	Databases      []string `json:"databases"`
	Error          string   `json:"error,omitempty"`
}

// renart:web-name SqlDiscoveryTable
type SQLDiscoveryTableItem struct {
	CatalogName  string `json:"catalog_name,omitempty"`
	Name         string `json:"name"`
	ShortName    string `json:"short_name"`
	SchemaName   string `json:"schema_name,omitempty"`
	DatabaseName string `json:"database_name,omitempty"`
}

// renart:web
// renart:web-name SqlDiscoveryTablesResponse
type SQLTableDiscoveryResult struct {
	Status         string                  `json:"status"`
	ConnectionName string                  `json:"connection_name"`
	ConnectionType string                  `json:"connection_type,omitempty"`
	Database       string                  `json:"database"`
	Tables         []SQLDiscoveryTableItem `json:"tables"`
	Error          string                  `json:"error,omitempty"`
}

type SQLColumn = webmodel.SQLColumn

// renart:web
// renart:web-name SqlDiscoveryTableColumnsResponse
type SQLTableColumnsResult struct {
	Status         string            `json:"status"`
	ConnectionName string            `json:"connection_name"`
	Table          string            `json:"table"`
	Columns        []SQLColumn       `json:"columns"`
	RawOutput      string            `json:"raw_output"`
	Operation      OperationMetadata `json:"operation,omitempty"`
	Error          string            `json:"error,omitempty"`
}

type SQLDependencies struct {
	Executor             BruinCommandExecutor
	NewConnectionManager func(context.Context, string) (config.ConnectionAndDetailsGetter, error)
	RunConnectionQuery   func(context.Context, string, string, string) ([]string, []map[string]any, error)
}

type SQLService struct {
	deps            SQLDependencies
	catalogObserver RemoteCatalogObserver
}

func NewSQLService(deps SQLDependencies) *SQLService {
	return &SQLService{deps: deps}
}

// SetRemoteCatalogObserver is server-startup wiring. It is called before the
// HTTP server accepts requests so explicit table/column discovery can warm the
// LSP's process-local catalog without a second warehouse round trip.
func (s *SQLService) SetRemoteCatalogObserver(observer RemoteCatalogObserver) {
	s.catalogObserver = observer
}

func (s *SQLService) ColumnValues(ctx context.Context, connectionName, environment, query string) SQLColumnValuesResult {
	_, rows, err := s.deps.RunConnectionQuery(ctx, connectionName, environment, query)
	if err != nil {
		return SQLColumnValuesResult{Status: "error", Values: []any{}, Error: err.Error()}
	}

	values := make([]any, 0, len(rows))
	for _, row := range rows {
		for _, value := range row {
			values = append(values, value)
			break
		}
	}

	return SQLColumnValuesResult{Status: "ok", Values: values}
}

// Query explicitly runs an ad hoc statement. Safe SELECTs get bounded query
// execution; other statements keep Run semantics with a bounded response only.
func (s *SQLService) Query(ctx context.Context, connectionName, environment, query string, limit int) SQLQueryResult {
	// Explicit Run retains its statement semantics. Only positively validated
	// SELECTs use the separate bounded adapter and advertise continuation.
	if s.deps.NewConnectionManager != nil {
		if body, connectionType, err := s.previewQuery(ctx, connectionName, environment, query); err == nil {
			return s.runPreview(ctx, connectionName, environment, body, connectionType, limit)
		}
	}
	columns, rows, err := s.deps.RunConnectionQuery(ctx, connectionName, environment, query)
	if err != nil {
		return SQLQueryResult{Status: "error", Columns: []string{}, Rows: []map[string]any{}, Error: err.Error()}
	}

	truncated := false
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
		truncated = true
	}
	if columns == nil {
		columns = []string{}
	}
	if rows == nil {
		rows = []map[string]any{}
	}

	rows, metadata := preview.Bound(rows, limit, truncated)
	if metadata.Continuation != "none" {
		metadata.Continuation, metadata.Reason, metadata.NextLimit = "none", "unsupported", 0
	}
	return SQLQueryResult{Status: "ok", Columns: columns, Rows: rows, Truncated: metadata.HasMore, Preview: metadata}
}

func (s *SQLService) previewQuery(ctx context.Context, connectionName, environment, query string) (string, string, error) {
	if s.deps.NewConnectionManager == nil {
		return "", "", fmt.Errorf("query preview is unavailable")
	}
	manager, err := s.deps.NewConnectionManager(ctx, environment)
	if err != nil {
		return "", "", err
	}
	if manager == nil || manager.GetConnectionType(connectionName) == "" {
		return "", "", fmt.Errorf("query connection not found")
	}
	body, err := sqlintelligence.ReadOnlyQueryBody(query, brokerQueryDialect(manager, connectionName))
	if err == nil && usesNativePreviewTop(manager.GetConnectionType(connectionName)) {
		_, err = sqlintelligence.LimitUnboundedSelect(body, 1, "tsql")
	}
	return body, manager.GetConnectionType(connectionName), err
}

// Preview independently validates every continuation; it cannot execute writes.
func (s *SQLService) Preview(ctx context.Context, connectionName, environment, query string, limit int) SQLQueryResult {
	body, connectionType, err := s.previewQuery(ctx, connectionName, environment, query)
	if err != nil {
		return SQLQueryResult{Status: "error", Columns: []string{}, Rows: []map[string]any{}, Error: "Cannot load this preview: " + err.Error()}
	}
	return s.runPreview(ctx, connectionName, environment, body, connectionType, limit)
}

func (s *SQLService) runPreview(ctx context.Context, connectionName, environment, body, connectionType string, limit int) SQLQueryResult {
	limit = preview.NormalizeLimit(limit)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	boundedSQL := wrapReadOnlySampleQuery(body, connectionType, "renart_preview", limit+1)
	if usesNativePreviewTop(connectionType) {
		var err error
		boundedSQL, err = sqlintelligence.LimitUnboundedSelect(body, limit+1, "tsql")
		if err != nil {
			return SQLQueryResult{Status: "error", Columns: []string{}, Rows: []map[string]any{}, Error: err.Error()}
		}
	}
	columns, rows, err := s.deps.RunConnectionQuery(ctx, connectionName, environment, boundedSQL)
	if err != nil {
		return SQLQueryResult{Status: "error", Columns: []string{}, Rows: []map[string]any{}, Error: err.Error()}
	}
	rows, metadata := preview.Bound(rows, limit, false)
	if columns == nil {
		columns = []string{}
	}
	return SQLQueryResult{Status: "ok", Columns: columns, Rows: rows, Preview: metadata, Truncated: metadata.HasMore}
}

func usesNativePreviewTop(connectionType string) bool {
	switch normalizeConnectionType(connectionType) {
	case "mssql", "synapse", "fabric":
		return true
	default:
		return false
	}
}

func (s *SQLService) Databases(ctx context.Context, connectionName, environment string) (SQLDatabaseDiscoveryResult, *APIError) {
	manager, err := s.deps.NewConnectionManager(ctx, environment)
	if err != nil {
		return SQLDatabaseDiscoveryResult{}, &APIError{Status: http.StatusInternalServerError, Code: "connection_manager_failed", Message: err.Error()}
	}

	conn, err := resolveRuntimeConnection(manager, connectionName)
	if err != nil {
		return SQLDatabaseDiscoveryResult{}, &APIError{Status: http.StatusBadRequest, Code: "connection_resolution_failed", Message: err.Error()}
	}
	if conn == nil {
		return SQLDatabaseDiscoveryResult{}, &APIError{Status: http.StatusBadRequest, Code: "connection_not_found", Message: fmt.Sprintf("connection '%s' not found", connectionName)}
	}

	fetcher, ok := sqlDiscoveryAdapter(conn, manager.GetConnectionType(connectionName), configuredCatalog(manager.GetConnectionDetails(connectionName))).(interface {
		GetDatabases(ctx context.Context) ([]string, error)
	})
	if !ok {
		return SQLDatabaseDiscoveryResult{}, &APIError{Status: http.StatusBadRequest, Code: "connection_type_not_supported", Message: fmt.Sprintf("connection '%s' does not support database discovery", connectionName)}
	}

	databases, err := fetcher.GetDatabases(ctx)
	if err != nil {
		return SQLDatabaseDiscoveryResult{}, &APIError{Status: http.StatusBadRequest, Code: "sql_database_discovery_failed", Message: err.Error()}
	}

	sort.Strings(databases)
	return SQLDatabaseDiscoveryResult{
		Status:         "ok",
		ConnectionName: connectionName,
		ConnectionType: strings.TrimSpace(manager.GetConnectionType(connectionName)),
		Databases:      databases,
	}, nil
}

func (s *SQLService) Tables(ctx context.Context, connectionName, databaseName, environment string) (SQLTableDiscoveryResult, *APIError) {
	manager, err := s.deps.NewConnectionManager(ctx, environment)
	if err != nil {
		return SQLTableDiscoveryResult{}, &APIError{Status: http.StatusInternalServerError, Code: "connection_manager_failed", Message: err.Error()}
	}

	conn, err := resolveRuntimeConnection(manager, connectionName)
	if err != nil {
		return SQLTableDiscoveryResult{}, &APIError{Status: http.StatusBadRequest, Code: "connection_resolution_failed", Message: err.Error()}
	}
	if conn == nil {
		return SQLTableDiscoveryResult{}, &APIError{Status: http.StatusBadRequest, Code: "connection_not_found", Message: fmt.Sprintf("connection '%s' not found", connectionName)}
	}

	connectionType := strings.TrimSpace(manager.GetConnectionType(connectionName))
	conn = sqlDiscoveryAdapter(conn, connectionType, configuredCatalog(manager.GetConnectionDetails(connectionName)))
	tables := make([]SQLDiscoveryTableItem, 0)
	if fetcherWithSchemas, ok := conn.(interface {
		GetTablesWithSchemas(ctx context.Context, databaseName string) (map[string][]string, error)
	}); ok {
		items, err := fetcherWithSchemas.GetTablesWithSchemas(ctx, databaseName)
		if err != nil {
			return SQLTableDiscoveryResult{}, &APIError{Status: http.StatusBadRequest, Code: "sql_table_discovery_failed", Message: err.Error()}
		}
		tables = BuildSQLDiscoveryTableItemsForConnectionType(connectionType, databaseName, items)
	} else if fetcher, ok := conn.(interface {
		GetTables(ctx context.Context, databaseName string) ([]string, error)
	}); ok {
		items, err := fetcher.GetTables(ctx, databaseName)
		if err != nil {
			return SQLTableDiscoveryResult{}, &APIError{Status: http.StatusBadRequest, Code: "sql_table_discovery_failed", Message: err.Error()}
		}
		tables = BuildSQLDiscoveryTableItemsWithoutSchemasForConnectionType(connectionType, databaseName, items)
	} else {
		return SQLTableDiscoveryResult{}, &APIError{Status: http.StatusBadRequest, Code: "connection_type_not_supported", Message: fmt.Sprintf("connection '%s' does not support table discovery", connectionName)}
	}

	if adapter, ok := conn.(mysqlDiscovery); ok && adapter.catalog != "" {
		for i := range tables {
			tables[i].CatalogName = adapter.catalog
			tables[i].Name = sqlnamespace.Reference(connectionType, adapter.catalog, databaseName, tables[i].ShortName)
		}
	}
	if connectionType == "trino" {
		for i := range tables {
			// This compatibility API calls catalogs "databases". Preserve the
			// explicit identity when merging with lazy browser observations.
			tables[i].CatalogName = databaseName
		}
	}
	result := SQLTableDiscoveryResult{
		Status:         "ok",
		ConnectionName: connectionName,
		ConnectionType: connectionType,
		Database:       databaseName,
		Tables:         tables,
	}
	if s.catalogObserver != nil {
		s.catalogObserver.ObserveTables(RemoteCatalogScope{
			Connection:  connectionName,
			Environment: environment,
		}, tables)
	}
	return result, nil
}

func (s *SQLService) TableColumns(ctx context.Context, connectionName, tableName, environment string) (SQLTableColumnsResult, int) {
	engine := ""
	if s.deps.NewConnectionManager != nil {
		manager, err := s.deps.NewConnectionManager(ctx, environment)
		if err != nil {
			return SQLTableColumnsResult{Status: "error", Error: err.Error()}, http.StatusBadRequest
		}
		engine = normalizeConnectionType(manager.GetConnectionType(connectionName))
	}
	reference, err := sqlnamespace.QuoteReference(engine, tableName)
	if err != nil {
		return SQLTableColumnsResult{Status: "error", Error: err.Error()}, http.StatusBadRequest
	}
	query := fmt.Sprintf("select * from %s limit 1", reference)
	operation := queryConnectionOperation(connectionName, query, environment)
	output, err := s.deps.Executor.QueryConnection(ctx, QueryConnectionRequest{
		ConnectionName: connectionName,
		Query:          query,
		Environment:    environment,
		Output:         "json",
	})
	if err != nil {
		return SQLTableColumnsResult{
			Status:         "error",
			ConnectionName: connectionName,
			Table:          tableName,
			Columns:        []SQLColumn{},
			RawOutput:      string(output),
			Operation:      operation,
			Error:          err.Error(),
		}, http.StatusBadRequest
	}

	columns := InferSQLColumnsFromQueryOutput(output)
	result := SQLTableColumnsResult{
		Status:         "ok",
		ConnectionName: connectionName,
		Table:          tableName,
		Columns:        columns,
		RawOutput:      string(output),
		Operation:      operation,
	}
	if s.catalogObserver != nil {
		s.catalogObserver.ObserveColumns(RemoteCatalogScope{
			Connection:  connectionName,
			Environment: environment,
		}, tableName, columns)
	}
	return result, http.StatusOK
}

func BuildSQLDiscoveryTableItems(databaseName string, tables map[string][]string) []SQLDiscoveryTableItem {
	return BuildSQLDiscoveryTableItemsForConnectionType("", databaseName, tables)
}

// BuildSQLDiscoveryTableItemsForConnectionType renders each discovered table
// using the widest name that the warehouse and Bruin both accept. A catalog is
// part of the SQL identity for three-level platforms such as Snowflake and
// Databricks. PostgreSQL and other two-level platforms deliberately expose
// schema.table: prefixing the configured database would produce SQL and a Bruin
// asset name that those platforms reject.
func BuildSQLDiscoveryTableItemsForConnectionType(connectionType, databaseName string, tables map[string][]string) []SQLDiscoveryTableItem {
	items := make([]SQLDiscoveryTableItem, 0)
	schemas := make([]string, 0, len(tables))
	for schema := range tables {
		schemas = append(schemas, schema)
	}
	sort.Strings(schemas)

	for _, schema := range schemas {
		schemaTables := append([]string{}, tables[schema]...)
		sort.Strings(schemaTables)
		for _, table := range schemaTables {
			qualifiedName := discoveredTableName(connectionType, databaseName, schema, table)
			items = append(items, SQLDiscoveryTableItem{
				Name:         qualifiedName,
				ShortName:    table,
				SchemaName:   schema,
				DatabaseName: databaseName,
			})
		}
	}

	return items
}

func BuildSQLDiscoveryTableItemsWithoutSchemas(databaseName string, tables []string) []SQLDiscoveryTableItem {
	return BuildSQLDiscoveryTableItemsWithoutSchemasForConnectionType("", databaseName, tables)
}

func BuildSQLDiscoveryTableItemsWithoutSchemasForConnectionType(connectionType, databaseName string, tables []string) []SQLDiscoveryTableItem {
	items := make([]SQLDiscoveryTableItem, 0, len(tables))
	sortedTables := append([]string{}, tables...)
	sort.Strings(sortedTables)

	for _, table := range sortedTables {
		trimmed := strings.TrimSpace(table)
		if trimmed == "" {
			continue
		}

		shortName := trimmed
		if dotIndex := strings.LastIndex(trimmed, "."); dotIndex >= 0 && dotIndex < len(trimmed)-1 {
			shortName = trimmed[dotIndex+1:]
		}

		name := trimmed
		parts := strings.Split(trimmed, ".")
		if len(parts) == 1 {
			name = discoveredTableName(connectionType, databaseName, "", trimmed)
		} else if len(parts) == 2 && strings.TrimSpace(connectionType) != "" && discoveredTableNamesIncludeCatalog(connectionType) && strings.TrimSpace(databaseName) != "" {
			name = strings.TrimSpace(databaseName) + "." + trimmed
		}

		items = append(items, SQLDiscoveryTableItem{
			Name:         name,
			ShortName:    shortName,
			DatabaseName: databaseName,
		})
	}

	return items
}

func discoveredTableName(connectionType, databaseName, schemaName, tableName string) string {
	parts := make([]string, 0, 3)
	if discoveredTableNamesIncludeCatalog(connectionType) && strings.TrimSpace(databaseName) != "" {
		parts = append(parts, strings.TrimSpace(databaseName))
	}
	if strings.TrimSpace(schemaName) != "" {
		parts = append(parts, strings.TrimSpace(schemaName))
	} else if len(parts) == 0 && strings.TrimSpace(databaseName) != "" {
		// Engines without separately reported schemas have historically exposed
		// database.table. Keep that useful two-part spelling.
		parts = append(parts, strings.TrimSpace(databaseName))
	}
	parts = append(parts, strings.TrimSpace(tableName))
	return strings.Join(parts, ".")
}

func discoveredTableNamesIncludeCatalog(connectionType string) bool {
	capability, ok := tablename.For(normalizeConnectionType(connectionType))
	return !ok || capability.Unbounded || capability.MaxComponents >= 3
}

func InferSQLColumnsFromQueryOutput(output []byte) []SQLColumn {
	var envelope map[string]any
	if err := json.Unmarshal(output, &envelope); err != nil {
		return []SQLColumn{}
	}

	rawColumns, ok := envelope["columns"].([]any)
	if !ok {
		return []SQLColumn{}
	}

	result := make([]SQLColumn, 0, len(rawColumns))
	for _, raw := range rawColumns {
		if name, ok := raw.(string); ok {
			result = append(result, SQLColumn{Name: name})
			continue
		}

		mapped, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		name := ReadStringField(mapped, "name")
		if name == "" {
			continue
		}

		result = append(result, SQLColumn{Name: name, Type: ReadStringField(mapped, "type")})
	}

	return result
}
