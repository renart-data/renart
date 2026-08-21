package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/query"
	"github.com/spf13/afero"

	"renart/internal/bruincompat"
	"renart/internal/sqlintelligence"
	"renart/internal/web/duckcoord"
	"renart/internal/web/secretstore"
)

func (e *HybridBruinExecutor) QueryAsset(ctx context.Context, req QueryAssetRequest) ([]byte, error) {
	ctx = secretstore.WithPurpose(ctx, secretstore.PurposeQuery)
	queryStartedAt := time.Now()
	if e.newPipelineBuilder == nil {
		return nil, fmt.Errorf("direct asset query requires a pipeline builder")
	}

	pp, err := getDirectPipelineAndAsset(ctx, e.workspaceRoot, req.AssetPath, afero.NewOsFs())
	if err != nil {
		output, marshalErr := json.Marshal(directErrorResponse{Error: err.Error()})
		if marshalErr != nil {
			return nil, err
		}
		return output, err
	}

	if !pp.Asset.IsSQLAsset() {
		err := fmt.Errorf("asset '%s' is not a SQL asset (type: %s). Only SQL assets can be queried", req.AssetPath, pp.Asset.Type)
		output, marshalErr := json.Marshal(directErrorResponse{Error: err.Error()})
		if marshalErr != nil {
			return nil, err
		}
		return output, err
	}

	connName, conn, queryStr, manager, err := e.buildDirectAssetQuery(ctx, pp, req.Environment, req.StartDate, req.EndDate)
	if err != nil {
		output, marshalErr := json.Marshal(directErrorResponse{Error: err.Error()})
		if marshalErr != nil {
			return nil, err
		}
		return output, err
	}

	dialect, err := bruincompat.AssetTypeToDialect(pp.Asset.Type)
	if err != nil {
		dialect = ""
	}

	if dialect != "" {
		isSelect, selectErr := isReadOnlySelectQuery(queryStr, pp.Asset.Type)
		if selectErr == nil && !isSelect {
			_ = e.executionLogSink().SaveQueryLog(ctx, QueryLogRecord{
				Query:               queryStr,
				QueryStartTimestamp: queryStartedAt,
				Connection:          connName,
				Error:               fmt.Errorf(inspectReadOnlyErrorMessage),
				Asset:               req.AssetPath,
				Environment:         req.Environment,
				Limit:               parseQueryLogLimit(req.Limit),
			})
			output, marshalErr := json.Marshal(directErrorResponse{Error: inspectReadOnlyErrorMessage})
			if marshalErr != nil {
				return nil, fmt.Errorf(inspectReadOnlyErrorMessage)
			}
			return output, fmt.Errorf(inspectReadOnlyErrorMessage)
		}
	}

	if pp.Config.SelectedEnvironment.SchemaPrefix != "" {
		queryStr, err = applyDirectSchemaPrefix(ctx, queryStr, dialect, pp, conn)
		if err != nil {
			wrappedErr := fmt.Errorf("failed to apply schema prefix: %w", err)
			_ = e.executionLogSink().SaveQueryLog(ctx, QueryLogRecord{
				Query:               queryStr,
				QueryStartTimestamp: queryStartedAt,
				Connection:          connName,
				Error:               wrappedErr,
				Asset:               req.AssetPath,
				Environment:         req.Environment,
				Limit:               parseQueryLogLimit(req.Limit),
			})
			output, marshalErr := json.Marshal(directErrorResponse{Error: wrappedErr.Error()})
			if marshalErr != nil {
				return nil, wrappedErr
			}
			return output, wrappedErr
		}
	}

	if strings.TrimSpace(req.Limit) != "" {
		limitValue, convErr := strconv.ParseInt(strings.TrimSpace(req.Limit), 10, 64)
		if convErr == nil {
			queryStr = addDirectLimitToQuery(queryStr, limitValue, conn, dialect)
		}
	}

	querier, ok := conn.(directSchemaQuerier)
	if !ok {
		err := fmt.Errorf("connection type %s does not support querying", connName)
		_ = e.executionLogSink().SaveQueryLog(ctx, QueryLogRecord{
			Query:               queryStr,
			QueryStartTimestamp: queryStartedAt,
			Connection:          connName,
			Error:               err,
			Asset:               req.AssetPath,
			Environment:         req.Environment,
			Limit:               parseQueryLogLimit(req.Limit),
		})
		output, marshalErr := json.Marshal(directErrorResponse{Error: err.Error()})
		if marshalErr != nil {
			return nil, err
		}
		return output, err
	}
	lease, err := e.acquireDuckDBConnections(ctx, manager, []string{connName}, duckcoord.Owner{
		Operation: "inspect asset",
		Pipeline:  pp.Pipeline.Name,
		Asset:     pp.Asset.Name,
	}, nil)
	if err != nil {
		output, marshalErr := json.Marshal(directErrorResponse{Error: err.Error()})
		if marshalErr != nil {
			return nil, err
		}
		return output, err
	}
	defer lease.Release()

	result, err := selectWithComplexJSONFallback(ctx, querier, queryStr)
	if err != nil {
		wrappedErr := fmt.Errorf("query execution failed: %w", err)
		_ = e.executionLogSink().SaveQueryLog(ctx, QueryLogRecord{
			Query:               queryStr,
			QueryStartTimestamp: queryStartedAt,
			Connection:          connName,
			Error:               wrappedErr,
			Asset:               req.AssetPath,
			Environment:         req.Environment,
			Limit:               parseQueryLogLimit(req.Limit),
		})
		output, marshalErr := json.Marshal(directErrorResponse{Error: wrappedErr.Error()})
		if marshalErr != nil {
			return nil, wrappedErr
		}
		return output, wrappedErr
	}

	_ = e.executionLogSink().SaveQueryLog(ctx, QueryLogRecord{
		Query:               queryStr,
		QueryStartTimestamp: queryStartedAt,
		Connection:          connName,
		Result:              result,
		Asset:               req.AssetPath,
		Environment:         req.Environment,
		Limit:               parseQueryLogLimit(req.Limit),
	})

	response := NewQueryResultDTO(result, connName, queryStr)
	return json.Marshal(response)
}

func (e *HybridBruinExecutor) QueryConnection(ctx context.Context, req QueryConnectionRequest) ([]byte, error) {
	ctx = secretstore.WithPurpose(ctx, secretstore.PurposeQuery)
	queryStartedAt := time.Now()
	if e.newConnectionManager == nil {
		return nil, fmt.Errorf("direct connection query requires a connection manager")
	}

	manager, err := e.newConnectionManager(ctx, req.Environment)
	if err != nil {
		return nil, err
	}

	conn, err := resolveRuntimeConnection(manager, req.ConnectionName)
	if err != nil {
		return nil, err
	}
	if conn == nil {
		return nil, fmt.Errorf("connection %q not found", req.ConnectionName)
	}

	querier, ok := conn.(directSchemaQuerier)
	if !ok {
		return nil, fmt.Errorf("connection %q does not support querying", req.ConnectionName)
	}
	lease, err := e.acquireDuckDBConnections(ctx, manager, []string{req.ConnectionName}, duckcoord.Owner{Operation: "query connection"}, nil)
	if err != nil {
		return nil, err
	}
	defer lease.Release()

	var result *query.QueryResult
	if req.LogicalSchema && strings.EqualFold(strings.TrimSpace(manager.GetConnectionType(req.ConnectionName)), "duckdb") {
		result, err = selectDuckDBLogicalSchema(ctx, querier, req.Query)
	} else {
		result, err = selectWithComplexJSONFallback(ctx, querier, req.Query)
	}
	if err != nil {
		_ = e.executionLogSink().SaveQueryLog(ctx, QueryLogRecord{
			Query:               req.Query,
			QueryStartTimestamp: queryStartedAt,
			Connection:          req.ConnectionName,
			Error:               err,
			Environment:         req.Environment,
		})
		return nil, err
	}

	_ = e.executionLogSink().SaveQueryLog(ctx, QueryLogRecord{
		Query:               req.Query,
		QueryStartTimestamp: queryStartedAt,
		Connection:          req.ConnectionName,
		Result:              result,
		Environment:         req.Environment,
	})

	response := NewQueryResultDTO(result, req.ConnectionName, req.Query)

	if strings.EqualFold(strings.TrimSpace(req.Output), "json") || strings.TrimSpace(req.Output) == "" {
		return json.Marshal(response)
	}

	return nil, fmt.Errorf("direct connection query only supports json output")
}

func parseQueryLogLimit(raw string) int64 {
	limit, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || limit <= 0 {
		return 0
	}
	return limit
}

type directSchemaQuerier interface {
	SelectWithSchema(context.Context, *query.Query) (*query.QueryResult, error)
}

func selectDuckDBLogicalSchema(ctx context.Context, querier directSchemaQuerier, queryStr string) (*query.QueryResult, error) {
	describeQuery := "DESCRIBE " + strings.TrimRight(strings.TrimSpace(queryStr), ";")
	described, err := querier.SelectWithSchema(ctx, &query.Query{Query: describeQuery})
	if err != nil {
		return nil, err
	}
	if described == nil {
		return nil, fmt.Errorf("DuckDB returned no schema for query")
	}

	nameIndex := -1
	typeIndex := -1
	for index, column := range described.Columns {
		switch strings.ToLower(strings.TrimSpace(column)) {
		case "column_name":
			nameIndex = index
		case "column_type":
			typeIndex = index
		}
	}
	if nameIndex < 0 || typeIndex < 0 {
		return nil, fmt.Errorf("DuckDB DESCRIBE result did not include column_name and column_type")
	}

	columns := make([]string, 0, len(described.Rows))
	columnTypes := make([]string, 0, len(described.Rows))
	for _, row := range described.Rows {
		if nameIndex >= len(row) || typeIndex >= len(row) {
			continue
		}
		name := querySchemaValue(row[nameIndex])
		if name == "" {
			continue
		}
		columns = append(columns, name)
		columnTypes = append(columnTypes, querySchemaValue(row[typeIndex]))
	}

	return &query.QueryResult{
		Columns:     columns,
		ColumnTypes: columnTypes,
		Rows:        make([][]interface{}, 0),
	}, nil
}

func querySchemaValue(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(typed)
	}
}

func selectWithComplexJSONFallback(ctx context.Context, querier directSchemaQuerier, queryStr string) (*query.QueryResult, error) {
	result, err := querier.SelectWithSchema(ctx, &query.Query{Query: queryStr})
	if err == nil || !isComplexDuckDBPopulateError(err) {
		return result, err
	}

	schemaResult, schemaErr := querier.SelectWithSchema(ctx, &query.Query{Query: wrapDirectQueryWithLimit(queryStr, 0)})
	if schemaErr != nil {
		return nil, err
	}

	rewrittenQuery, ok := rewriteComplexColumnsToJSON(queryStr, schemaResult.Columns, schemaResult.ColumnTypes)
	if !ok {
		return nil, err
	}

	return querier.SelectWithSchema(ctx, &query.Query{Query: rewrittenQuery})
}

func isComplexDuckDBPopulateError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "not yet implemented populating from columns of type") ||
		strings.Contains(message, "not implemented") && strings.Contains(message, "populating from columns of type")
}

func rewriteComplexColumnsToJSON(queryStr string, columns []string, columnTypes []string) (string, bool) {
	if len(columns) == 0 {
		return "", false
	}

	selectItems := make([]string, 0, len(columns))
	hasComplexColumn := false
	for index, column := range columns {
		columnType := ""
		if index < len(columnTypes) {
			columnType = columnTypes[index]
		}

		quoted := quoteDirectIdentifier(column)
		if isComplexDuckDBColumnType(columnType) {
			hasComplexColumn = true
			selectItems = append(selectItems, fmt.Sprintf("to_json(%s) AS %s", quoted, quoted))
			continue
		}
		selectItems = append(selectItems, quoted)
	}

	if !hasComplexColumn {
		return "", false
	}

	return fmt.Sprintf("SELECT %s FROM (\n%s\n) AS renart_complex_query", strings.Join(selectItems, ", "), strings.TrimRight(queryStr, "; \n\t")), true
}

func isComplexDuckDBColumnType(columnType string) bool {
	lowered := strings.ToLower(strings.TrimSpace(columnType))
	return strings.Contains(lowered, "struct") ||
		strings.Contains(lowered, "map") ||
		strings.Contains(lowered, "union") ||
		strings.Contains(lowered, "[]") ||
		strings.Contains(lowered, "list")
}

var simpleDirectIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func quoteDirectIdentifier(identifier string) string {
	if simpleDirectIdentifierPattern.MatchString(identifier) {
		return identifier
	}
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func wrapDirectQueryWithLimit(queryStr string, limit int64) string {
	return fmt.Sprintf("SELECT * FROM (\n%s\n) AS renart_schema_query LIMIT %d", strings.TrimRight(queryStr, "; \n\t"), limit)
}

func (e *HybridBruinExecutor) buildDirectAssetQuery(ctx context.Context, pp *directPipelineInfo, environment, start, end string) (string, interface{}, string, config.ConnectionAndDetailsGetter, error) {
	if strings.TrimSpace(environment) != "" {
		if _, err := selectConfigEnvironment(pp.Config, environment); err != nil {
			return "", nil, "", nil, fmt.Errorf("failed to use the environment '%s': %w", environment, err)
		}
	}

	var manager config.ConnectionAndDetailsGetter
	if e.newConnectionManager != nil {
		var err error
		manager, err = e.newConnectionManager(ctx, pp.Config.SelectedEnvironmentName)
		if err != nil {
			return "", nil, "", nil, fmt.Errorf("failed to create connection manager: %w", err)
		}
	} else {
		connectionManager, err := newConnectionManagerFromConfig(ctx, pp.Config)
		if err != nil {
			return "", nil, "", nil, fmt.Errorf("failed to create connection manager: %w", err)
		}
		manager = connectionManager
	}

	connName, err := pp.Pipeline.GetConnectionNameForAsset(pp.Asset)
	if err != nil {
		return "", nil, "", nil, fmt.Errorf("failed to get connection: %w", err)
	}
	conn, err := resolveRuntimeConnection(manager, connName)
	if err != nil {
		return "", nil, "", nil, fmt.Errorf("failed to resolve connection %q: %w", connName, err)
	}
	if conn == nil {
		return "", nil, "", nil, fmt.Errorf("connection %q not found", connName)
	}

	now := time.Now().UTC()
	timeWindow, err := ResolveExecutionTimeWindow(string(pp.Pipeline.Schedule), start, end, now)
	if err != nil {
		return "", nil, "", nil, err
	}
	renderer := jinja.NewRendererWithStartEndDates(&timeWindow.Start, &timeWindow.End, &now, pp.Pipeline.Name, "your-run-id", nil)
	fetchCtx := context.WithValue(ctx, pipeline.RunConfigStartDate, timeWindow.Start)
	fetchCtx = context.WithValue(fetchCtx, pipeline.RunConfigEndDate, timeWindow.End)
	fetchCtx = context.WithValue(fetchCtx, pipeline.RunConfigExecutionDate, now)
	fetchCtx = context.WithValue(fetchCtx, pipeline.RunConfigRunID, "your-run-id")
	fetchCtx = context.WithValue(fetchCtx, config.EnvironmentContextKey, pp.Config.SelectedEnvironment)

	extractor := &query.WholeFileExtractor{Fs: afero.NewOsFs(), Renderer: renderer}
	clonedExtractor, err := extractor.CloneForAsset(fetchCtx, pp.Pipeline, pp.Asset)
	if err != nil {
		return "", nil, "", nil, fmt.Errorf("failed to clone extractor for asset %s: %w", pp.Asset.Name, err)
	}

	queries, err := clonedExtractor.ExtractQueriesFromString(pp.Asset.ExecutableFile.Content)
	if err != nil {
		return "", nil, "", nil, fmt.Errorf("failed to extract query: %w", err)
	}
	if len(queries) == 0 {
		return "", nil, "", nil, fmt.Errorf("no query found in asset")
	}

	return connName, conn, queries[0].Query, manager, nil
}

func addDirectLimitToQuery(queryStr string, limit int64, conn interface{}, dialect string) string {
	isSingleSelect, err := sqlintelligence.IsReadOnlySingleQuery(queryStr, dialect)
	if err == nil && !isSingleSelect {
		return queryStr
	}

	limitedQuery, err := sqlintelligence.AddLimit(queryStr, int(limit), dialect)
	if err == nil {
		return limitedQuery
	}

	if limiter, ok := conn.(interface{ Limit(string, int64) string }); ok {
		return limiter.Limit(queryStr, limit)
	}

	queryStr = strings.TrimRight(queryStr, "; \n\t")
	return fmt.Sprintf("SELECT * FROM (\n%s\n) as t LIMIT %d", queryStr, limit)
}

func applyDirectSchemaPrefix(_ context.Context, queryStr, dialect string, pp *directPipelineInfo, conn interface{}) (string, error) {
	if dialect == "" || pp.Config.SelectedEnvironment == nil || pp.Config.SelectedEnvironment.SchemaPrefix == "" {
		return queryStr, nil
	}

	usedTables, err := sqlintelligence.UsedTables(queryStr, dialect)
	if err != nil {
		return queryStr, nil
	}
	if len(usedTables) == 0 {
		return queryStr, nil
	}

	_ = conn
	renameMapping := map[string]string{}
	for _, tableReference := range usedTables {
		parts := strings.Split(tableReference, ".")
		if len(parts) != 2 {
			continue
		}
		schemaName := parts[0]
		tableName := parts[1]
		renameMapping[tableReference] = fmt.Sprintf("%s%s.%s", pp.Config.SelectedEnvironment.SchemaPrefix, schemaName, tableName)
	}
	if len(renameMapping) == 0 {
		return queryStr, nil
	}

	rewrittenQuery, err := sqlintelligence.RenameTables(queryStr, dialect, renameMapping)
	if err != nil {
		return "", fmt.Errorf("failed to rewrite query with schema prefix: %w", err)
	}
	return rewrittenQuery, nil
}
