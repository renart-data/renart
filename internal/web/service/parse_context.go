package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"renart/internal/sqlintelligence"
	"renart/internal/web/model"
)

var assetTypeDialectMap = map[pipeline.AssetType]string{
	pipeline.AssetTypeBigqueryQuery:         "bigquery",
	pipeline.AssetTypeBigqueryQuerySensor:   "bigquery",
	pipeline.AssetTypeSnowflakeQuery:        "snowflake",
	pipeline.AssetTypeSnowflakeQuerySensor:  "snowflake",
	pipeline.AssetTypePostgresQuery:         "postgres",
	pipeline.AssetTypePostgresQuerySensor:   "postgres",
	pipeline.AssetTypeRedshiftQuery:         "postgres",
	pipeline.AssetTypeRedshiftQuerySensor:   "postgres",
	pipeline.AssetTypeTrinoQuery:            "trino",
	pipeline.AssetTypeTrinoQuerySensor:      "trino",
	pipeline.AssetTypeAthenaQuery:           "athena",
	pipeline.AssetTypeAthenaSQLSensor:       "athena",
	pipeline.AssetTypeClickHouse:            "clickhouse",
	pipeline.AssetTypeClickHouseQuerySensor: "clickhouse",
	pipeline.AssetTypeDatabricksQuery:       "databricks",
	pipeline.AssetTypeDatabricksQuerySensor: "databricks",
	pipeline.AssetTypeMsSQLQuery:            "tsql",
	pipeline.AssetTypeMsSQLQuerySensor:      "tsql",
	pipeline.AssetTypeSynapseQuery:          "tsql",
	pipeline.AssetTypeSynapseQuerySensor:    "tsql",
	pipeline.AssetTypeFabricQuery:           "fabric",
	pipeline.AssetTypeFabricQueryLegacy:     "fabric",
	pipeline.AssetTypeMySQLQuery:            "mysql",
	pipeline.AssetTypeMySQLQuerySensor:      "mysql",
	pipeline.AssetTypeStarRocksQuery:        "mysql",
	pipeline.AssetTypeStarRocksQuerySensor:  "mysql",
	pipeline.AssetTypeOracleQuery:           "oracle",
	pipeline.AssetTypeVerticaQuery:          "postgres",
	pipeline.AssetTypeDuckDBQuery:           "duckdb",
	pipeline.AssetTypeDuckDBQuerySensor:     "duckdb",
	pipeline.AssetTypeMotherduckQuery:       "duckdb",
}

var (
	selectAliasPattern  = regexp.MustCompile(`(?i)\bas\s+([a-zA-Z_][\w$]*|"[^"]+"|` + "`" + `[^` + "`" + `]+` + "`" + `)\s*$`)
	directColumnPattern = regexp.MustCompile(`^(?:[a-zA-Z_][\w$]*\.)?([a-zA-Z_][\w$]*)$`)
)

type ParseContextSchemaColumn struct {
	Name          string   `json:"name"`
	Type          string   `json:"type,omitempty"`
	SourceMethods []string `json:"source_methods,omitempty"`
}

type ParseContextSchemaTable struct {
	Name           string                     `json:"name"`
	IsMaterialized bool                       `json:"is_materialized,omitempty"`
	Columns        []ParseContextSchemaColumn `json:"columns"`
}

type ParseContextRange struct {
	Start   int `json:"start"`
	End     int `json:"end"`
	Line    int `json:"line"`
	Col     int `json:"col"`
	EndLine int `json:"end_line"`
	EndCol  int `json:"end_col"`
}

type ParseContextPart struct {
	Name  string            `json:"name"`
	Kind  string            `json:"kind"`
	Range ParseContextRange `json:"range"`
}

type ParseContextDiagnostic struct {
	Code     string             `json:"code,omitempty"`
	Source   string             `json:"source,omitempty"`
	Message  string             `json:"message"`
	Severity string             `json:"severity"`
	Range    *ParseContextRange `json:"range,omitempty"`
}

type ParseContextTable struct {
	Name         string                       `json:"name"`
	SourceKind   string                       `json:"source_kind,omitempty"`
	ResolvedName string                       `json:"resolved_name,omitempty"`
	Alias        string                       `json:"alias,omitempty"`
	Columns      map[string]string            `json:"columns,omitempty"`
	ColumnRanges map[string]ParseContextRange `json:"column_ranges,omitempty"`
	Parts        []ParseContextPart           `json:"parts"`
	AliasRange   *ParseContextRange           `json:"alias_range,omitempty"`
	ScopeRange   *ParseContextRange           `json:"scope_range,omitempty"`
}

type ParseContextColumn struct {
	Name          string             `json:"name"`
	Qualifier     string             `json:"qualifier,omitempty"`
	ResolvedTable string             `json:"resolved_table,omitempty"`
	Parts         []ParseContextPart `json:"parts"`
}

type ParseContextResult struct {
	Status           string                   `json:"status"`
	AssetID          string                   `json:"asset_id"`
	Dialect          string                   `json:"dialect,omitempty"`
	QueryKind        string                   `json:"query_kind,omitempty"`
	IsSingleSelect   bool                     `json:"is_single_select"`
	IsReadOnlyResult bool                     `json:"is_read_only_result"`
	Tables           []ParseContextTable      `json:"tables"`
	Columns          []ParseContextColumn     `json:"columns"`
	Diagnostics      []ParseContextDiagnostic `json:"diagnostics,omitempty"`
	Errors           []string                 `json:"errors,omitempty"`
	Error            string                   `json:"error,omitempty"`
}

type ParseContextDependencies struct {
	ResolveAssetByID func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error)
	CurrentState     func() model.WorkspaceState
}

type ParseContextService struct {
	deps ParseContextDependencies
}

func NewParseContextService(deps ParseContextDependencies) *ParseContextService {
	return &ParseContextService{deps: deps}
}

func (s *ParseContextService) Parse(
	ctx context.Context,
	assetID, content string,
	schemaTables []ParseContextSchemaTable,
	connection string,
) (ParseContextResult, *APIError) {
	_, parsedPipeline, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return ParseContextResult{}, &APIError{Status: 400, Code: "asset_not_found", Message: err.Error()}
	}

	assetType := asset.Type
	if s.deps.CurrentState != nil {
		connectionName := strings.TrimSpace(connection)
		if queryType, ok := queryAssetTypeForConnectionType(s.deps.CurrentState().Connections[connectionName]); ok {
			assetType = queryType
		}
	}
	dialect, err := AssetTypeToDialect(assetType)
	if err != nil {
		return ParseContextResult{
			Status:  "ok",
			AssetID: assetID,
			Errors:  []string{"unsupported SQL dialect for parse context"},
			Tables:  []ParseContextTable{},
			Columns: []ParseContextColumn{},
		}, nil
	}

	if strings.TrimSpace(content) == "" {
		content = asset.ExecutableFile.Content
	}
	schema := BuildParseContextSchema(asset, schemaTables)
	columnSourceMethods := BuildParseContextColumnSourceMethods(asset, schemaTables)
	ApplyAssetSQLDefinitionColumns(ctx, parsedPipeline, asset, schema, columnSourceMethods)

	parseContext, err := sqlintelligence.ParseContextWithSchemaContext(ctx, content, dialect, schema, columnSourceMethods)
	if err != nil {
		return ParseContextResult{
			Status:  "error",
			AssetID: assetID,
			Dialect: dialect,
			Error:   err.Error(),
			Tables:  []ParseContextTable{},
			Columns: []ParseContextColumn{},
		}, nil
	}

	return ParseContextResult{
		Status:           "ok",
		AssetID:          assetID,
		Dialect:          dialect,
		QueryKind:        parseContext.QueryKind,
		IsSingleSelect:   parseContext.IsSingleSelect,
		IsReadOnlyResult: parseContext.IsReadOnlyResult,
		Tables:           ParseContextTablesFromParser(parseContext.Tables),
		Columns:          ParseContextColumnsFromParser(parseContext.Columns),
		Diagnostics:      ParseContextDiagnosticsFromParser(parseContext.Diagnostics),
		Errors:           parseContext.Errors,
	}, nil
}

func BuildParseContextColumnSourceMethods(asset *pipeline.Asset, suggestionTables []ParseContextSchemaTable) sqlintelligence.SchemaColumnSourceMethods {
	sources := sqlintelligence.SchemaColumnSourceMethods{}

	for _, table := range suggestionTables {
		if strings.TrimSpace(table.Name) == "" {
			continue
		}

		columns := sources[table.Name]
		if columns == nil {
			columns = map[string][]string{}
		}
		for _, column := range table.Columns {
			if strings.TrimSpace(column.Name) == "" {
				continue
			}
			if len(column.SourceMethods) > 0 {
				columns[column.Name] = mergeStringSlices(columns[column.Name], column.SourceMethods)
			}
			if table.IsMaterialized && hasWorkspaceColumnSource(column.SourceMethods) {
				columns[column.Name] = mergeStringSlices(columns[column.Name], []string{"materialized-workspace-load"})
			}
		}

		if len(columns) > 0 {
			sources[table.Name] = columns
		}
	}

	if asset != nil && strings.TrimSpace(asset.Name) != "" && len(asset.Columns) > 0 {
		columns := sources[asset.Name]
		if columns == nil {
			columns = map[string][]string{}
		}
		for _, column := range asset.Columns {
			if strings.TrimSpace(column.Name) == "" {
				continue
			}
			columns[column.Name] = mergeStringSlices(columns[column.Name], []string{"workspace-load"})
		}
		if len(columns) > 0 {
			sources[asset.Name] = columns
		}
	}

	MergeShortNameColumnSourceMethods(sources)
	return sources
}

func mergeStringSlices(left, right []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(left)+len(right))
	for _, value := range append(left, right...) {
		if strings.TrimSpace(value) == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func hasWorkspaceColumnSource(values []string) bool {
	for _, value := range values {
		if value == "workspace-load" || value == "workspace-event" {
			return true
		}
	}
	return false
}

func MergeShortNameColumnSourceMethods(sources sqlintelligence.SchemaColumnSourceMethods) {
	for tableName, columns := range sources {
		if strings.Contains(tableName, ".") {
			continue
		}

		qualifiedName := singleQualifiedTableForShortName(sources, tableName)
		if qualifiedName == "" {
			continue
		}

		qualifiedColumns := sources[qualifiedName]
		if qualifiedColumns == nil {
			qualifiedColumns = map[string][]string{}
		}
		for columnName, methods := range columns {
			qualifiedColumns[columnName] = mergeStringSlices(qualifiedColumns[columnName], methods)
		}
		sources[qualifiedName] = qualifiedColumns
	}
}

func AssetTypeToDialect(assetType pipeline.AssetType) (string, error) {
	dialect, ok := assetTypeDialectMap[assetType]
	if !ok {
		return "", fmt.Errorf("unsupported asset type %s", assetType)
	}

	return dialect, nil
}

func BuildParseContextSchema(asset *pipeline.Asset, suggestionTables []ParseContextSchemaTable) sqlintelligence.Schema {
	schema := sqlintelligence.Schema{}

	for _, table := range suggestionTables {
		if strings.TrimSpace(table.Name) == "" {
			continue
		}

		columns := schema[table.Name]
		if columns == nil {
			columns = map[string]string{}
		}
		for _, column := range table.Columns {
			if strings.TrimSpace(column.Name) == "" {
				continue
			}
			columns[column.Name] = strings.TrimSpace(column.Type)
		}

		// Register the table even with no columns: the frontend only sends
		// tables it knows exist (pipeline assets a notebook cell can read,
		// including non-SQL ones with no declared columns), so references to
		// them must resolve. Column checks against it stay lenient.
		schema[table.Name] = columns
	}

	if asset != nil && strings.TrimSpace(asset.Name) != "" && len(asset.Columns) > 0 {
		columns := schema[asset.Name]
		if columns == nil {
			columns = map[string]string{}
		}
		for _, column := range asset.Columns {
			if strings.TrimSpace(column.Name) == "" {
				continue
			}
			columns[column.Name] = strings.TrimSpace(column.Type)
		}
		if len(columns) > 0 {
			schema[asset.Name] = columns
		}
	}

	MergeShortNameSchemaTables(schema)
	return schema
}

func MergeShortNameSchemaTables(schema sqlintelligence.Schema) {
	for tableName, columns := range schema {
		if strings.Contains(tableName, ".") {
			continue
		}

		qualifiedName := singleQualifiedTableForShortName(schema, tableName)
		if qualifiedName == "" {
			continue
		}

		qualifiedColumns := schema[qualifiedName]
		if qualifiedColumns == nil {
			qualifiedColumns = map[string]string{}
		}
		for columnName, columnType := range columns {
			if _, exists := qualifiedColumns[columnName]; !exists {
				qualifiedColumns[columnName] = columnType
			}
		}
		schema[qualifiedName] = qualifiedColumns
	}
}

func ApplyAssetSQLDefinitionColumns(ctx context.Context, parsedPipeline *pipeline.Pipeline, currentAsset *pipeline.Asset, schema sqlintelligence.Schema, sources sqlintelligence.SchemaColumnSourceMethods) {
	if parsedPipeline == nil {
		return
	}

	definitionSchemas := newAssetDefinitionSchemaResolver(parsedPipeline)
	for _, asset := range parsedPipeline.Assets {
		if asset == nil || strings.TrimSpace(asset.Name) == "" {
			continue
		}
		if currentAsset != nil && strings.EqualFold(strings.TrimSpace(asset.Name), strings.TrimSpace(currentAsset.Name)) {
			continue
		}
		// SQL definition columns can only be read from SQL assets; non-SQL
		// assets (Python, ingestr, ...) that materialize a table are still
		// valid references and must be registered below.
		declaredColumns := definitionSchemas.Available(ctx, asset)
		var definitionColumns []string
		if _, err := AssetTypeToDialect(asset.Type); err == nil {
			definitionColumns = ExtractSQLDefinitionColumns(asset.ExecutableFile.Content)
		}
		if len(declaredColumns) == 0 && len(definitionColumns) == 0 {
			// The asset is still a real table even when its columns cannot be
			// inferred (a `select *` SQL cell, or a Python asset). Register it
			// with no columns so references to it are not flagged "unresolved";
			// column checks against it simply stay lenient.
			if _, exists := schema[asset.Name]; !exists {
				schema[asset.Name] = map[string]string{}
			}
			continue
		}

		schemaColumns := schema[asset.Name]
		if schemaColumns == nil {
			schemaColumns = map[string]string{}
		}
		sourceColumns := sources[asset.Name]
		if sourceColumns == nil {
			sourceColumns = map[string][]string{}
		}

		for _, column := range declaredColumns {
			columnName := strings.TrimSpace(column.Name)
			if columnName == "" {
				continue
			}
			if _, exists := schemaColumns[columnName]; !exists {
				schemaColumns[columnName] = strings.TrimSpace(column.Type)
			}
			sourceColumns[columnName] = mergeStringSlices(sourceColumns[columnName], []string{"workspace-load"})
		}

		for _, columnName := range definitionColumns {
			if _, exists := schemaColumns[columnName]; !exists {
				schemaColumns[columnName] = ""
			}
			sourceColumns[columnName] = mergeStringSlices(sourceColumns[columnName], []string{"asset-sql-definition"})
		}

		schema[asset.Name] = schemaColumns
		sources[asset.Name] = sourceColumns
	}

	MergeShortNameSchemaTables(schema)
	MergeShortNameColumnSourceMethods(sources)
}

func ExtractSQLDefinitionColumns(content string) []string {
	sql := strings.TrimSpace(content)
	if sql == "" {
		return nil
	}

	selectIndex := findTopLevelKeyword(sql, "select", 0)
	if selectIndex < 0 {
		return nil
	}

	fromIndex := findTopLevelKeyword(sql, "from", selectIndex+len("select"))
	if fromIndex < 0 {
		fromIndex = len(sql)
	}

	selectList := sql[selectIndex+len("select") : fromIndex]
	seen := map[string]bool{}
	result := []string{}
	for _, expression := range splitTopLevelComma(selectList) {
		columnName := extractSelectExpressionColumnName(expression)
		if columnName == "" || seen[strings.ToLower(columnName)] {
			continue
		}
		seen[strings.ToLower(columnName)] = true
		result = append(result, columnName)
	}

	return result
}

func findTopLevelKeyword(sql, keyword string, startIndex int) int {
	lower := strings.ToLower(sql)
	depth := 0
	var quote rune

	for index, char := range sql {
		if index < startIndex {
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			}
			continue
		}
		switch char {
		case '\'', '"', '`':
			quote = char
			continue
		case '(':
			depth++
			continue
		case ')':
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth != 0 || !strings.HasPrefix(lower[index:], keyword) {
			continue
		}
		before := byte(' ')
		if index > 0 {
			before = lower[index-1]
		}
		after := byte(' ')
		if index+len(keyword) < len(lower) {
			after = lower[index+len(keyword)]
		}
		if isIdentifierByte(before) || isIdentifierByte(after) {
			continue
		}
		return index
	}

	return -1
}

func splitTopLevelComma(value string) []string {
	result := []string{}
	depth := 0
	var quote rune
	start := 0

	for index, char := range value {
		if quote != 0 {
			if char == quote {
				quote = 0
			}
			continue
		}
		switch char {
		case '\'', '"', '`':
			quote = char
			continue
		case '(':
			depth++
			continue
		case ')':
			if depth > 0 {
				depth--
			}
			continue
		case ',':
			if depth == 0 {
				if expression := strings.TrimSpace(value[start:index]); expression != "" {
					result = append(result, expression)
				}
				start = index + 1
			}
		}
	}
	if expression := strings.TrimSpace(value[start:]); expression != "" {
		result = append(result, expression)
	}
	return result
}

func extractSelectExpressionColumnName(expression string) string {
	expression = strings.TrimSpace(expression)
	if expression == "" || expression == "*" || strings.HasSuffix(expression, ".*") {
		return ""
	}

	if match := selectAliasPattern.FindStringSubmatch(expression); len(match) == 2 {
		return normalizeSQLIdentifier(match[1])
	}
	if match := directColumnPattern.FindStringSubmatch(expression); len(match) == 2 {
		return normalizeSQLIdentifier(match[1])
	}
	return ""
}

func normalizeSQLIdentifier(value string) string {
	return strings.Trim(strings.TrimSpace(value), "\"`")
}

func isIdentifierByte(value byte) bool {
	return (value >= 'a' && value <= 'z') || (value >= '0' && value <= '9') || value == '_'
}

func singleQualifiedTableForShortName[T any](tables map[string]T, shortName string) string {
	var match string
	for tableName := range tables {
		if !strings.Contains(tableName, ".") || tableShortName(tableName) != shortName {
			continue
		}
		if match != "" {
			return ""
		}
		match = tableName
	}
	return match
}

func tableShortName(tableName string) string {
	parts := strings.Split(tableName, ".")
	return parts[len(parts)-1]
}

func ParseContextRangeFromParser(input sqlintelligence.ParseContextRange) ParseContextRange {
	return ParseContextRange{Start: input.Start, End: input.End, Line: input.Line, Col: input.Col, EndLine: input.EndLine, EndCol: input.EndCol}
}

func ParseContextPartsFromParser(input []sqlintelligence.ParseContextPart) []ParseContextPart {
	result := make([]ParseContextPart, 0, len(input))
	for _, part := range input {
		result = append(result, ParseContextPart{Name: part.Name, Kind: part.Kind, Range: ParseContextRangeFromParser(part.Range)})
	}
	return result
}

func ParseContextTablesFromParser(input []sqlintelligence.ParseContextTable) []ParseContextTable {
	result := make([]ParseContextTable, 0, len(input))
	for _, table := range input {
		item := ParseContextTable{
			Name:         table.Name,
			SourceKind:   table.SourceKind,
			ResolvedName: table.ResolvedName,
			Alias:        table.Alias,
			Columns:      ParseContextSchemaColumnsFromParser(table.Columns),
			ColumnRanges: ParseContextColumnRangesFromParser(table.ColumnRanges),
			Parts:        ParseContextPartsFromParser(table.Parts),
		}
		if table.AliasRange != nil {
			aliasRange := ParseContextRangeFromParser(*table.AliasRange)
			item.AliasRange = &aliasRange
		}
		if table.ScopeRange != nil {
			scopeRange := ParseContextRangeFromParser(*table.ScopeRange)
			item.ScopeRange = &scopeRange
		}
		result = append(result, item)
	}
	return result
}

func ParseContextSchemaColumnsFromParser(input []sqlintelligence.SchemaColumn) map[string]string {
	if len(input) == 0 {
		return nil
	}
	result := make(map[string]string, len(input))
	for _, column := range input {
		result[column.Name] = column.Type
	}
	return result
}

func ParseContextColumnRangesFromParser(input map[string]sqlintelligence.ParseContextRange) map[string]ParseContextRange {
	if len(input) == 0 {
		return nil
	}
	result := make(map[string]ParseContextRange, len(input))
	for name, rangeValue := range input {
		result[name] = ParseContextRangeFromParser(rangeValue)
	}
	return result
}

func ParseContextColumnsFromParser(input []sqlintelligence.ParseContextColumn) []ParseContextColumn {
	result := make([]ParseContextColumn, 0, len(input))
	for _, column := range input {
		name := column.Name
		if column.Qualifier != "" && !strings.Contains(name, ".") {
			name = column.Qualifier + "." + name
		}
		result = append(result, ParseContextColumn{
			Name:          name,
			Qualifier:     column.Qualifier,
			ResolvedTable: column.ResolvedTable,
			Parts:         ParseContextPartsFromParser(column.Parts),
		})
	}
	return result
}

func ParseContextDiagnosticsFromParser(input []sqlintelligence.ParseContextDiagnostic) []ParseContextDiagnostic {
	result := make([]ParseContextDiagnostic, 0, len(input))
	for _, diagnostic := range input {
		item := ParseContextDiagnostic{Code: diagnostic.Code, Source: diagnostic.Source, Message: diagnostic.Message, Severity: diagnostic.Severity}
		if diagnostic.Range != nil {
			rangeValue := ParseContextRangeFromParser(*diagnostic.Range)
			item.Range = &rangeValue
		}
		result = append(result, item)
	}
	return result
}
