package sqlintelligence

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"renart/internal/authoringdiag"
)

type parseContextToken struct {
	Text string
	Span parseContextSpan
}

type parseContextSpan struct {
	Start int
	End   int
}

type validationIssue struct {
	Message  string
	Severity string
	Code     string
	Start    *int
	End      *int
}

type parseContextCTE struct {
	Name         string
	Columns      []SchemaColumn
	ColumnRanges map[string]ParseContextRange
	Metadata     map[string]columnSourceMetadata
}

type columnSourceMetadata struct {
	SourceMethods     []string
	OriginTable       string
	ActualSchemaKnown bool
}

type selectAliasOffsets map[string][]int

var (
	quotedDiagnosticIdentifierPattern = regexp.MustCompile(`'([^']+)'`)
	callLikeSubqueryPattern           = regexp.MustCompile(`(?i)\b([A-Za-z_][\w$]*)\s+\(\s*select\b`)
)

func ParseContextWithSchemaGolyglot(query, dialect string, schema Schema, columnSourceMethods ...SchemaColumnSourceMethods) (*ParseContext, error) {
	return ParseContextWithSchemaGolyglotContext(context.Background(), query, dialect, schema, columnSourceMethods...)
}

func ParseContextWithSchemaGolyglotContext(ctx context.Context, query, dialect string, schema Schema, columnSourceMethods ...SchemaColumnSourceMethods) (*ParseContext, error) {
	return parseContextWithSchemaGolyglotContext(ctx, query, dialect, schema, nil, columnSourceMethods...)
}

func ParseContextWithSchemaConstraintsGolyglotContext(ctx context.Context, query, dialect string, schema Schema, constraints SchemaConstraints, columnSourceMethods ...SchemaColumnSourceMethods) (*ParseContext, error) {
	return parseContextWithSchemaGolyglotContext(ctx, query, dialect, schema, constraints, columnSourceMethods...)
}

func recoverCallLikeSubquery(query, parseError string) *ParseContext {
	if !strings.Contains(strings.ToLower(parseError), "unexpected token: select") {
		return nil
	}
	matches := callLikeSubqueryPattern.FindAllStringSubmatchIndex(query, -1)
	if len(matches) == 0 {
		return nil
	}
	match := matches[len(matches)-1]
	if len(match) < 4 {
		return nil
	}
	name := query[match[2]:match[3]]
	rangeInfo := rangeFromOffsets(query, match[2], match[3])
	return &ParseContext{
		QueryKind:        "select",
		IsSingleSelect:   true,
		IsReadOnlyResult: true,
		Diagnostics: []ParseContextDiagnostic{{
			Code:     authoringdiag.CodeUnresolvedColumn,
			Source:   authoringdiag.SourceRenart,
			Message:  "Unresolved column: " + name,
			Severity: "error",
			Range:    &rangeInfo,
		}},
		Errors: []string{},
	}
}

func cloneColumnSourceMethods(source SchemaColumnSourceMethods) SchemaColumnSourceMethods {
	cloned := SchemaColumnSourceMethods{}
	for tableName, methodsByColumn := range source {
		clonedMethods := map[string][]string{}
		for columnName, methods := range methodsByColumn {
			clonedMethods[columnName] = append([]string(nil), methods...)
		}
		cloned[tableName] = clonedMethods
	}
	return cloned
}

func schemaMapForCTE(cte parseContextCTE) map[string]string {
	columns := make(map[string]string, len(cte.Columns))
	for _, column := range cte.Columns {
		columns[column.Name] = column.Type
	}
	return columns
}

func sourceMethodsForCTE(cte parseContextCTE) map[string][]string {
	methods := make(map[string][]string, len(cte.Columns))
	for _, column := range cte.Columns {
		methods[column.Name] = append([]string(nil), cte.Metadata[column.Name].SourceMethods...)
	}
	return methods
}

func mergeCTEsIntoSchema(schema Schema, ctes map[string]parseContextCTE) Schema {
	merged := Schema{}
	for tableName, columns := range schema {
		mergedColumns := map[string]string{}
		for columnName, columnType := range columns {
			mergedColumns[columnName] = columnType
		}
		merged[tableName] = mergedColumns
	}
	for _, cte := range ctes {
		columns := map[string]string{}
		for _, column := range cte.Columns {
			columns[column.Name] = column.Type
		}
		merged[cte.Name] = columns
	}
	return merged
}

func describeOutputColumns(query string) map[string]bool {
	columns := map[string]bool{}
	if !strings.Contains(strings.ToLower(query), "describe") {
		return columns
	}
	for _, column := range []string{"column_name", "column_type", "null", "key", "default", "extra"} {
		columns[column] = true
	}
	return columns
}

func appendUniqueSchemaColumn(columns []SchemaColumn, column SchemaColumn) []SchemaColumn {
	for _, existing := range columns {
		if strings.EqualFold(existing.Name, column.Name) {
			return columns
		}
	}
	return append(columns, column)
}

// qualifiedSchemaTableForShortName resolves an unqualified table reference to
// the unique schema entry whose last path segment matches (e.g. "accounts" →
// "public.accounts"), the way engines resolve tables via the search path.
// Ambiguous short names (present in more than one schema) stay unresolved.
func qualifiedSchemaTableForShortName(schema Schema, shortName string) string {
	if shortName == "" || strings.Contains(shortName, ".") {
		return ""
	}
	var match string
	for tableName := range schema {
		lastDot := strings.LastIndex(tableName, ".")
		if lastDot < 0 || !strings.EqualFold(tableName[lastDot+1:], shortName) {
			continue
		}
		if match != "" {
			return ""
		}
		match = tableName
	}
	return match
}

func schemaColumns(columns map[string]string) []SchemaColumn {
	result := make([]SchemaColumn, 0, len(columns))
	for name, columnType := range columns {
		result = append(result, SchemaColumn{Name: name, Type: columnType})
	}
	return result
}

func sortParseContextTablesBySource(tables []ParseContextTable) {
	sort.SliceStable(tables, func(i, j int) bool {
		return parseContextTableStart(tables[i]) < parseContextTableStart(tables[j])
	})
}

func parseContextTableStart(table ParseContextTable) int {
	if len(table.Parts) == 0 {
		return 1 << 30
	}
	return table.Parts[0].Range.Start
}

func findTokenRange(query string, tokens []parseContextToken, parts []string) *ParseContextRange {
	_, rangeInfo := findTokenRangeFrom(query, tokens, parts, 0)
	return rangeInfo
}

func findTokenRangeFrom(query string, tokens []parseContextToken, parts []string, start int) (int, *ParseContextRange) {
	for index := start; index <= len(tokens)-len(parts); index++ {
		matched := true
		for partIndex, part := range parts {
			if !strings.EqualFold(tokens[index+partIndex].Text, part) {
				matched = false
				break
			}
		}
		if matched {
			rangeInfo := rangeFromOffsets(query, tokens[index].Span.Start, tokens[index+len(parts)-1].Span.End)
			return index, &rangeInfo
		}
	}
	return -1, nil
}

func buildValidationDiagnostics(query string, tokens []parseContextToken, errors []validationIssue, tables []ParseContextTable, columns []ParseContextColumn, schema Schema, ctes map[string]parseContextCTE, sourceMethods SchemaColumnSourceMethods, selectAliases selectAliasOffsets, describeColumns map[string]bool) []ParseContextDiagnostic {
	diagnostics := make([]ParseContextDiagnostic, 0, len(errors))
	validationKeys := map[string]bool{}
	for _, item := range errors {
		// Golyglot preserves these compatibility warning codes. They are useful
		// lint-policy candidates but are not type errors: W220 currently flags an
		// explicit CROSS JOIN while missing JOIN ... ON TRUE, and W221 also flags
		// legitimate joins on an alternate key. Keep strict reference integrity
		// and ambiguity checks without enabling these heuristics globally.
		switch strings.ToUpper(strings.TrimSpace(item.Code)) {
		case "W220", "W221", "W222":
			continue
		}
		severity := strings.ToLower(strings.TrimSpace(item.Severity))
		if severity == "" {
			severity = "error"
		}
		var rangeInfo *ParseContextRange
		if item.Start != nil && item.End != nil {
			start := min(max(*item.Start, 0), len(query))
			end := min(max(*item.End, start), len(query))
			rangeValue := rangeFromOffsets(query, start, end)
			rangeInfo = &rangeValue
		}
		match := quotedDiagnosticIdentifierPattern.FindStringSubmatch(item.Message)
		if rangeInfo == nil && len(match) > 1 {
			rangeInfo = findDiagnosticIdentifierRange(query, tokens, match[1])
		}
		message := item.Message
		if strings.EqualFold(item.Code, "E200") && len(match) > 1 {
			if isDuckDBPathTable(match[1]) {
				continue
			}
			if qualifiedSchemaTableForShortName(schema, match[1]) != "" {
				continue
			}
			message = "Unresolved table: " + match[1]
		}
		if strings.EqualFold(item.Code, "E201") && len(match) > 1 {
			if selectAliasVisible(selectAliases, match[1], rangeInfo) || describeColumns[strings.ToLower(match[1])] {
				continue
			}
			if isCopyOptionValue(query, match[1], rangeInfo) {
				continue
			}
			message = "Unresolved column: " + match[1]
		}
		for _, key := range validationDiagnosticKeys(item, message, match) {
			validationKeys[key] = true
		}
		diagnostics = append(diagnostics, ParseContextDiagnostic{Code: validationDiagnosticCode(item.Code), Source: authoringdiag.SourcePolyglot, Message: message, Severity: severity, Range: rangeInfo})
	}
	diagnostics = append(diagnostics, localTableDiagnostics(tables, schema, validationKeys)...)
	diagnostics = append(diagnostics, localColumnDiagnostics(columns, tables, selectAliases, describeColumns, validationKeys)...)
	diagnostics = append(diagnostics, unmaterializedColumnWarnings(columns, ctes, sourceMethods)...)
	return diagnostics
}

var (
	copyStatementPrefixPattern = regexp.MustCompile(`(?is)^\s*(?:(?:--[^\n]*(?:\n|$))|(?:/\*.*?\*/\s*))*copy\b`)
	copyToKeywordPattern       = regexp.MustCompile(`(?i)\bto\b`)
)

func isCopyOptionValue(query, identifier string, rangeInfo *ParseContextRange) bool {
	if rangeInfo == nil || rangeInfo.Start <= 0 || rangeInfo.Start > len(query) || rangeInfo.End > len(query) ||
		!copyStatementPrefixPattern.MatchString(query) {
		return false
	}
	prefix := query[:rangeInfo.Start]
	if !copyToKeywordPattern.MatchString(prefix) {
		return false
	}
	lineStart := strings.LastIndexByte(prefix, '\n') + 1
	linePrefix := strings.TrimSpace(prefix[lineStart:])
	linePrefix = strings.TrimRight(linePrefix, " \t=(,")
	fields := strings.Fields(linePrefix)
	if len(fields) == 0 {
		return false
	}
	option := strings.ToLower(strings.Trim(fields[len(fields)-1], "`\"'()"))
	switch option {
	case "format", "header", "delimiter", "quote", "escape", "null", "compression", "encoding", "dateformat", "timestampformat", "partition_by", "overwrite_or_ignore", "append", "use_tmp_file":
		return strings.EqualFold(strings.TrimSpace(query[rangeInfo.Start:rangeInfo.End]), identifier)
	default:
		return false
	}
}

func findDiagnosticIdentifierRange(query string, tokens []parseContextToken, identifier string) *ParseContextRange {
	if !strings.Contains(identifier, ".") {
		return findTokenRange(query, tokens, []string{identifier})
	}
	parts := strings.Split(identifier, ".")
	for index := 0; index < len(tokens); index++ {
		matched := true
		for partIndex, part := range parts {
			tokenIndex := index + partIndex*2
			if tokenIndex >= len(tokens) || !strings.EqualFold(tokens[tokenIndex].Text, part) {
				matched = false
				break
			}
			if partIndex < len(parts)-1 && (tokenIndex+1 >= len(tokens) || tokens[tokenIndex+1].Text != ".") {
				matched = false
				break
			}
		}
		if matched {
			rangeInfo := rangeFromOffsets(query, tokens[index].Span.Start, tokens[index+len(parts)*2-2].Span.End)
			return &rangeInfo
		}
	}
	return nil
}

func validationDiagnosticKeys(item validationIssue, message string, quotedIdentifier []string) []string {
	keys := []string{validationDiagnosticKeyFromMessage(message)}
	if len(quotedIdentifier) <= 1 {
		return keys
	}
	identifier := quotedIdentifier[1]
	switch strings.ToUpper(strings.TrimSpace(item.Code)) {
	case "E200":
		keys = append(keys, validationDiagnosticKey("table", identifier))
	case "E201", "E221":
		keys = append(keys, validationDiagnosticKey("column", identifier))
	}
	return keys
}

func validationDiagnosticCode(code string) string {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "E200":
		return authoringdiag.CodeUnresolvedRelation
	case "E201", "E221":
		return authoringdiag.CodeUnresolvedColumn
	case "E210", "E211", "E212", "E213", "E214", "E215", "E216", "E217",
		"W210", "W211", "W212", "W213", "W214", "W215", "W216":
		return authoringdiag.CodeSQLTypeMismatch
	case "":
		return authoringdiag.CodeSQLValidationFailed
	default:
		return "polyglot-" + strings.ToLower(strings.TrimSpace(code))
	}
}

func validationDiagnosticKeyFromMessage(message string) string {
	lower := strings.ToLower(strings.TrimSpace(message))
	for _, prefix := range []string{"unresolved column: ", "unresolved table or alias: ", "unresolved table: "} {
		if strings.HasPrefix(lower, prefix) {
			kind := "column"
			if strings.Contains(prefix, "table") {
				kind = "table"
			}
			return validationDiagnosticKey(kind, strings.TrimSpace(message[len(prefix):]))
		}
	}
	return lower
}

func validationDiagnosticKey(kind, identifier string) string {
	return kind + ":" + strings.ToLower(strings.Trim(identifier, " `\"'"))
}

func localTableDiagnostics(tables []ParseContextTable, schema Schema, validationKeys map[string]bool) []ParseContextDiagnostic {
	diagnostics := []ParseContextDiagnostic{}
	for _, table := range tables {
		if table.Name == "" || table.SourceKind == "cte" || table.SourceKind == "derived" || table.SourceKind == "table_function" || isDuckDBPathTable(table.Name) {
			continue
		}
		if _, ok := schema[table.ResolvedName]; ok {
			continue
		}
		if _, ok := schema[table.Name]; ok {
			continue
		}
		if validationKeys[validationDiagnosticKey("table", table.Name)] || validationKeys[validationDiagnosticKey("table", table.ResolvedName)] {
			continue
		}
		var rangeInfo *ParseContextRange
		if len(table.Parts) > 0 {
			rangeCopy := table.Parts[0].Range
			if len(table.Parts) > 1 {
				rangeCopy.End = table.Parts[len(table.Parts)-1].Range.End
				rangeCopy.EndLine = table.Parts[len(table.Parts)-1].Range.EndLine
				rangeCopy.EndCol = table.Parts[len(table.Parts)-1].Range.EndCol
			}
			rangeInfo = &rangeCopy
		}
		diagnostics = append(diagnostics, ParseContextDiagnostic{Code: authoringdiag.CodeUnresolvedRelation, Source: authoringdiag.SourceRenart, Message: "Unresolved table: " + table.Name, Severity: "error", Range: rangeInfo})
	}
	return diagnostics
}

func isDuckDBPathTable(name string) bool {
	return strings.Contains(name, "/") || strings.HasPrefix(name, ".")
}

func localColumnDiagnostics(columns []ParseContextColumn, tables []ParseContextTable, selectAliases selectAliasOffsets, describeColumns map[string]bool, validationKeys map[string]bool) []ParseContextDiagnostic {
	diagnostics := []ParseContextDiagnostic{}
	for _, column := range columns {
		if column.Name == "" {
			continue
		}
		var columnRange *ParseContextRange
		if len(column.Parts) > 0 {
			columnRange = &column.Parts[len(column.Parts)-1].Range
		}
		if column.Qualifier == "" && (selectAliasVisible(selectAliases, column.Name, columnRange) || describeColumns[strings.ToLower(column.Name)]) {
			continue
		}
		if column.ResolvedTable == "" {
			if column.Qualifier != "" {
				if validationKeys[validationDiagnosticKey("table", column.Qualifier)] {
					continue
				}
				var rangeInfo *ParseContextRange
				if len(column.Parts) > 0 {
					rangeCopy := column.Parts[0].Range
					rangeInfo = &rangeCopy
				}
				diagnostics = append(diagnostics, ParseContextDiagnostic{Code: authoringdiag.CodeUnresolvedAlias, Source: authoringdiag.SourceRenart, Message: "Unresolved table or alias: " + column.Qualifier, Severity: "error", Range: rangeInfo})
				continue
			}
			if column.Qualifier == "" && len(tables) > 0 {
				if validationKeys[validationDiagnosticKey("column", column.Name)] {
					continue
				}
				var rangeInfo *ParseContextRange
				if len(column.Parts) > 0 {
					rangeCopy := column.Parts[len(column.Parts)-1].Range
					rangeInfo = &rangeCopy
				}
				diagnostics = append(diagnostics, ParseContextDiagnostic{Code: authoringdiag.CodeUnresolvedColumn, Source: authoringdiag.SourceRenart, Message: "Unresolved column: " + column.Name, Severity: "error", Range: rangeInfo})
			}
			continue
		}
		table := tableByResolvedName(tables, column.ResolvedTable)
		if table == nil || !tableHasColumn(*table, column.Name) {
			if validationKeys[validationDiagnosticKey("column", column.Name)] {
				continue
			}
			var rangeInfo *ParseContextRange
			if len(column.Parts) > 0 {
				rangeCopy := column.Parts[len(column.Parts)-1].Range
				rangeInfo = &rangeCopy
			}
			diagnostics = append(diagnostics, ParseContextDiagnostic{Code: authoringdiag.CodeUnresolvedColumn, Source: authoringdiag.SourceRenart, Message: "Unresolved column: " + column.Name, Severity: "error", Range: rangeInfo})
		}
	}
	return diagnostics
}

func selectAliasVisible(aliases selectAliasOffsets, name string, reference *ParseContextRange) bool {
	offsets := aliases[strings.ToLower(strings.TrimSpace(name))]
	if len(offsets) == 0 {
		return false
	}
	if reference == nil {
		return true
	}
	for _, offset := range offsets {
		if offset < reference.Start {
			return true
		}
	}
	return false
}

func tableByResolvedName(tables []ParseContextTable, resolvedName string) *ParseContextTable {
	for index := range tables {
		if strings.EqualFold(tables[index].ResolvedName, resolvedName) || strings.EqualFold(tables[index].Name, resolvedName) {
			return &tables[index]
		}
	}
	return nil
}

func tableHasColumn(table ParseContextTable, columnName string) bool {
	for _, column := range table.Columns {
		if strings.EqualFold(column.Name, columnName) {
			return true
		}
	}
	return false
}

func unmaterializedColumnWarnings(columns []ParseContextColumn, ctes map[string]parseContextCTE, sourceMethods SchemaColumnSourceMethods) []ParseContextDiagnostic {
	warnings := []ParseContextDiagnostic{}
	for _, column := range columns {
		metadata := metadataForColumn(column.ResolvedTable, column.Name, ctes, sourceMethods)
		if !shouldWarnUnmaterialized(metadata) {
			continue
		}
		var rangeInfo *ParseContextRange
		if len(column.Parts) > 0 {
			rangeCopy := column.Parts[len(column.Parts)-1].Range
			rangeInfo = &rangeCopy
		}
		originTable := metadata.OriginTable
		if originTable == "" {
			originTable = "an upstream Bruin asset"
		}
		warnings = append(warnings, ParseContextDiagnostic{
			Code:     authoringdiag.CodeUnmaterializedColumn,
			Source:   authoringdiag.SourceRenart,
			Message:  fmt.Sprintf("Column '%s' is defined in the asset '%s', but it has not been materialized yet.", column.Name, originTable),
			Severity: "warning",
			Range:    rangeInfo,
		})
	}
	return warnings
}

func metadataForColumn(tableName, columnName string, ctes map[string]parseContextCTE, sourceMethods SchemaColumnSourceMethods) columnSourceMetadata {
	if tableName == "" {
		return columnSourceMetadata{}
	}
	if cte, ok := ctes[strings.ToLower(tableName)]; ok {
		if metadata, ok := cte.Metadata[columnName]; ok {
			return metadata
		}
		for sourceTable, methodsByColumn := range sourceMethods {
			if methods, ok := methodsByColumn[columnName]; ok {
				return columnSourceMetadata{SourceMethods: methods, OriginTable: sourceTable, ActualSchemaKnown: actualSchemaKnown(methodsByColumn)}
			}
		}
		return columnSourceMetadata{}
	}
	methodsByColumn := sourceMethods[tableName]
	return columnSourceMetadata{SourceMethods: methodsByColumn[columnName], OriginTable: tableName, ActualSchemaKnown: actualSchemaKnown(methodsByColumn)}
}

func shouldWarnUnmaterialized(metadata columnSourceMetadata) bool {
	methods := map[string]bool{}
	for _, method := range metadata.SourceMethods {
		methods[method] = true
	}
	definition := methods["workspace-load"] || methods["workspace-event"] || methods["asset-column-inference"] || methods["asset-sql-definition"]
	materialized := methods["connection-column-discovery"] || methods["materialized-workspace-load"]
	return metadata.ActualSchemaKnown && definition && !materialized
}

func actualSchemaKnown(methodsByColumn map[string][]string) bool {
	for _, methods := range methodsByColumn {
		for _, method := range methods {
			if method == "connection-column-discovery" || method == "materialized-workspace-load" {
				return true
			}
		}
	}
	return false
}

func firstColumnSourceMethods(columnSourceMethods []SchemaColumnSourceMethods) SchemaColumnSourceMethods {
	if len(columnSourceMethods) == 0 || columnSourceMethods[0] == nil {
		return SchemaColumnSourceMethods{}
	}
	return columnSourceMethods[0]
}

func rangeFromOffsets(query string, start, end int) ParseContextRange {
	line, col := offsetToLineCol(query, start)
	endLine, endCol := offsetToLineCol(query, end)
	return ParseContextRange{Start: start, End: end, Line: line, Col: col, EndLine: endLine, EndCol: endCol}
}

func offsetToLineCol(query string, offset int) (int, int) {
	if offset < 0 {
		return 1, 1
	}
	if offset > len(query) {
		offset = len(query)
	}
	line := 1
	lineStart := 0
	for index, char := range query[:offset] {
		if char == '\n' {
			line++
			lineStart = index + 1
		}
	}
	return line, offset - lineStart + 1
}

func offsetFromLineCol(query string, line, col int) int {
	if line < 1 || col < 1 {
		return -1
	}
	currentLine := 1
	lineStart := 0
	for index, char := range query {
		if currentLine == line {
			break
		}
		if char == '\n' {
			currentLine++
			lineStart = index + 1
		}
	}
	if currentLine != line {
		return -1
	}
	offset := lineStart + col - 1
	if offset > len(query) {
		return len(query)
	}
	return offset
}
