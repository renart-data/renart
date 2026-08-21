package sqlintelligence

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/renart-data/golyglot/pkg/golyglot"

	"renart/internal/authoringdiag"
)

func parseContextWithSchemaGolyglotContext(ctx context.Context, query, dialectName string, schema Schema, constraints SchemaConstraints, columnSourceMethods ...SchemaColumnSourceMethods) (*ParseContext, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dialect, err := golyglot.ParseDialect(dialectName)
	if err != nil {
		return nil, err
	}
	parsed, parseErr := golyglot.ParseStrict(query, dialect)
	if parseErr != nil {
		if recovered := recoverPolyglotCallLikeSubquery(query, parseErr.Error()); recovered != nil {
			return recovered, nil
		}
		span := golyglotSyntaxErrorSpan(parseErr, len(query))
		message := parseErr.Error()
		rangeInfo := rangeFromOffsets(query, span.Start, span.End)
		return &ParseContext{
			Diagnostics: []ParseContextDiagnostic{{Code: authoringdiag.CodeSQLSyntax, Source: authoringdiag.SourcePolyglot, Message: message, Severity: "error", Range: &rangeInfo}},
			Errors:      []string{message},
		}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	collector := nativeParseContextCollector{
		ctx:           ctx,
		query:         query,
		dialect:       dialect,
		schema:        schema,
		constraints:   constraints,
		sourceMethods: firstColumnSourceMethods(columnSourceMethods),
		ctes:          make(map[string]polyglotCTE),
		selectAliases: make(polyglotSelectAliasOffsets),
	}
	for _, statement := range parsed.Statements {
		if selectStmt, ok := statement.Node.(*golyglot.SelectStmt); ok {
			collector.visitSelect(selectStmt, nil, nil)
		}
	}
	sortPolyglotTablesBySource(collector.tables)

	validationSchema := mergePolyglotCTEsIntoSchema(schema, collector.ctes)
	strict := true
	nativeSchema := buildGolyglotSchema(validationSchema, constraints)
	nativeSchema.Strict = &strict
	validation := golyglot.ValidateWithOptions(query, golyglot.ValidationOptions{Dialect: dialect, Semantic: true, Schema: &nativeSchema})
	tokens := golyglotTokens(parsed.Tokens)
	validationErrors := nativeValidationErrors(validation.Errors)
	diagnostics := polyglotDiagnostics(query, tokens, validationErrors, collector.tables, collector.columns, validationSchema, collector.ctes, collector.sourceMethods, collector.selectAliases, polyglotDescribeColumns(query))
	diagnostics = append(diagnostics, golyglotCallLikeSubqueryDiagnostics(query, diagnostics)...)

	kind := golyglotQueryKind(parsed.Statements)
	readOnly := len(parsed.Statements) == 1 && golyglotReadOnlyStatement(parsed.Statements[0].Node)
	singleSelect := len(parsed.Statements) == 1 && kind == "select"
	return &ParseContext{
		QueryKind:        kind,
		IsSingleSelect:   singleSelect,
		IsReadOnlyResult: readOnly,
		Tables:           collector.tables,
		Columns:          collector.columns,
		Diagnostics:      diagnostics,
		Errors:           []string{},
	}, nil
}

func golyglotSyntaxErrorSpan(err error, queryLength int) golyglot.Span {
	var syntaxError *golyglot.SyntaxError
	if errors.As(err, &syntaxError) {
		span := syntaxError.Diagnostic.Span
		if syntaxError.Polyglot.Span.Valid(queryLength) {
			span = syntaxError.Polyglot.Span
		}
		if span.Valid(queryLength) {
			if span.Start == span.End && span.End < queryLength {
				span.End++
			}
			return span
		}
	}
	end := min(1, queryLength)
	return golyglot.Span{Start: 0, End: end}
}

type nativeParseScope struct {
	parent *nativeParseScope
	tables []ParseContextTable
	ctes   map[string]polyglotCTE
}

type nativeParseContextCollector struct {
	ctx           context.Context
	query         string
	dialect       golyglot.Dialect
	schema        Schema
	constraints   SchemaConstraints
	sourceMethods SchemaColumnSourceMethods
	tables        []ParseContextTable
	columns       []ParseContextColumn
	ctes          map[string]polyglotCTE
	selectAliases polyglotSelectAliasOffsets
}

func (c *nativeParseContextCollector) visitSelect(selectStmt *golyglot.SelectStmt, parent *nativeParseScope, inherited map[string]polyglotCTE) {
	if selectStmt == nil || c.ctx.Err() != nil {
		return
	}
	localCTEs := clonePolyglotCTEs(inherited)
	workingSchema := mergePolyglotCTEsIntoSchema(c.schema, localCTEs)
	workingMethods := clonePolyglotColumnSourceMethods(c.sourceMethods)
	for _, cte := range localCTEs {
		workingMethods[cte.Name] = sourceMethodsForPolyglotCTE(cte)
	}
	for _, cteNode := range selectStmt.With {
		c.visitSelect(cteNode.Query, nil, localCTEs)
		cte := c.deriveCTE(cteNode, workingSchema, workingMethods)
		localCTEs[strings.ToLower(cte.Name)] = cte
		c.ctes[strings.ToLower(cte.Name)] = cte
		workingSchema[cte.Name] = schemaMapForPolyglotCTE(cte)
		workingMethods[cte.Name] = sourceMethodsForPolyglotCTE(cte)
	}

	scopeRange := nativeNodeRange(c.query, selectStmt.SourceSpan())
	localTables, nestedFromQueries := c.collectSelectTables(selectStmt, localCTEs, scopeRange)
	scope := &nativeParseScope{parent: parent, tables: localTables, ctes: localCTEs}
	c.tables = append(c.tables, localTables...)
	for _, projection := range selectStmt.Projections {
		if projection.Alias != nil {
			name := strings.ToLower(projection.Alias.Text)
			c.selectAliases[name] = append(c.selectAliases[name], projection.Alias.Span.Start)
		}
		if alias, ok := projection.Expr.(*golyglot.AliasExpr); ok {
			name := strings.ToLower(alias.Alias.Text)
			c.selectAliases[name] = append(c.selectAliases[name], alias.Alias.Span.Start)
		}
	}

	nestedExpressionQueries := make([]*golyglot.SelectStmt, 0)
	for _, expression := range nativeSelectExpressions(selectStmt) {
		identifiers, nested := nativeExpressionIdentifiers(expression)
		for _, identifier := range identifiers {
			c.columns = append(c.columns, c.parseColumn(identifier, scope))
		}
		nestedExpressionQueries = append(nestedExpressionQueries, nested...)
	}
	for _, nested := range nestedFromQueries {
		c.visitSelect(nested, scope, localCTEs)
	}
	for _, nested := range nestedExpressionQueries {
		c.visitSelect(nested, scope, localCTEs)
	}
}

func clonePolyglotCTEs(source map[string]polyglotCTE) map[string]polyglotCTE {
	result := make(map[string]polyglotCTE, len(source))
	for name, cte := range source {
		result[name] = cte
	}
	return result
}

func (c *nativeParseContextCollector) deriveCTE(node golyglot.CTE, schema Schema, sourceMethods SchemaColumnSourceMethods) polyglotCTE {
	result := polyglotCTE{Name: node.Name.Text, ColumnRanges: make(map[string]ParseContextRange), Metadata: make(map[string]polyglotColumnMetadata)}
	if node.Query == nil {
		return result
	}
	body := nativeSourceSlice(c.query, node.Query.SourceSpan())
	nativeSchema := buildGolyglotSchema(schema, c.constraints)
	analysis, err := golyglot.AnalyzeQuery(body, golyglot.AnalyzeQueryOptions{Dialect: c.dialect, Schema: &nativeSchema})
	if err != nil {
		return result
	}
	for index, output := range analysis.OutputColumns {
		name := output.Name
		if index < len(node.Columns) {
			name = node.Columns[index].Text
		}
		column := SchemaColumn{Name: name}
		if output.TypeHint != nil {
			column.Type = normalizeInferredType(*output.TypeHint)
		}
		column.Nullable = queryProjectionNullable(output.Nullability)
		result.Columns = appendUniqueSchemaColumn(result.Columns, column)
		if index < len(node.Query.Projections) {
			if span := nativeProjectionNameSpan(node.Query.Projections[index]); span.Valid(len(c.query)) {
				result.ColumnRanges[name] = rangeFromOffsets(c.query, span.Start, span.End)
			}
		}
		result.Metadata[name] = nativeOutputColumnMetadata(name, index, analysis, sourceMethods)
	}
	return result
}

func nativeOutputColumnMetadata(name string, index int, analysis golyglot.QueryAnalysis, sourceMethods SchemaColumnSourceMethods) polyglotColumnMetadata {
	if index < len(analysis.Projections) {
		for _, upstream := range analysis.Projections[index].Upstream {
			sourceName := ""
			if upstream.SourceName != nil {
				sourceName = *upstream.SourceName
			} else if upstream.Table != nil {
				sourceName = *upstream.Table
			}
			if methods, resolved := nativeColumnSourceMethods(sourceMethods, sourceName, upstream.Column); len(methods) > 0 {
				return polyglotColumnMetadata{SourceMethods: methods, OriginTable: resolved, ActualSchemaKnown: polyglotActualSchemaKnown(sourceMethods[resolved])}
			}
		}
	}
	for sourceName, columns := range sourceMethods {
		for columnName, methods := range columns {
			if strings.EqualFold(columnName, name) {
				return polyglotColumnMetadata{SourceMethods: append([]string(nil), methods...), OriginTable: sourceName, ActualSchemaKnown: polyglotActualSchemaKnown(columns)}
			}
		}
	}
	return polyglotColumnMetadata{SourceMethods: []string{"query-expression"}}
}

func nativeColumnSourceMethods(sourceMethods SchemaColumnSourceMethods, tableName, columnName string) ([]string, string) {
	for candidate, columns := range sourceMethods {
		if tableName != "" && !strings.EqualFold(candidate, tableName) && !strings.EqualFold(lastIdentifier(candidate), lastIdentifier(tableName)) {
			continue
		}
		for candidateColumn, methods := range columns {
			if strings.EqualFold(candidateColumn, columnName) {
				return append([]string(nil), methods...), candidate
			}
		}
	}
	return nil, ""
}

func nativeProjectionNameSpan(projection golyglot.SelectItem) golyglot.Span {
	if projection.Alias != nil {
		return projection.Alias.Span
	}
	if alias, ok := projection.Expr.(*golyglot.AliasExpr); ok {
		return alias.Alias.Span
	}
	if identifier, ok := projection.Expr.(*golyglot.IdentifierExpr); ok && len(identifier.Parts) > 0 {
		return identifier.Parts[len(identifier.Parts)-1].Span
	}
	return golyglot.Span{Start: -1, End: -1}
}

func (c *nativeParseContextCollector) collectSelectTables(selectStmt *golyglot.SelectStmt, ctes map[string]polyglotCTE, scopeRange *ParseContextRange) ([]ParseContextTable, []*golyglot.SelectStmt) {
	var tables []ParseContextTable
	var nested []*golyglot.SelectStmt
	var collect func(golyglot.FromItem)
	collect = func(item golyglot.FromItem) {
		switch value := item.(type) {
		case *golyglot.TableName:
			tables = append(tables, c.parseTableName(value, ctes, scopeRange))
		case *golyglot.SubqueryFrom:
			if value.Alias != nil {
				tables = append(tables, c.parseDerivedTable(value, ctes, scopeRange))
			}
			if value.Query != nil {
				nested = append(nested, value.Query)
			}
		case *golyglot.GroupedFrom:
			for index := range value.Items {
				collect(value.Items[index].Primary)
				for _, join := range value.Items[index].Joins {
					collect(join.Right)
				}
			}
		case *golyglot.TableFunctionFrom:
			tables = append(tables, c.parseTableFunction(value, scopeRange))
		}
	}
	for index := range selectStmt.From {
		collect(selectStmt.From[index].Primary)
		for _, join := range selectStmt.From[index].Joins {
			collect(join.Right)
		}
	}
	return tables, nested
}

func (c *nativeParseContextCollector) parseTableName(value *golyglot.TableName, ctes map[string]polyglotCTE, scopeRange *ParseContextRange) ParseContextTable {
	parts := make([]string, 0, len(value.Parts))
	partRanges := make([]ParseContextPart, 0, len(value.Parts))
	for index, identifier := range value.Parts {
		parts = append(parts, identifier.Text)
		kind := "schema"
		if index == len(value.Parts)-1 {
			kind = "table"
		}
		partRanges = append(partRanges, ParseContextPart{Name: identifier.Text, Kind: kind, Range: rangeFromOffsets(c.query, identifier.Span.Start, identifier.Span.End)})
	}
	name := strings.Join(parts, ".")
	resolvedName := name
	columns := schemaColumns(c.schema[name])
	if _, known := c.schema[name]; !known {
		if qualified := qualifiedSchemaTableForShortName(c.schema, name); qualified != "" {
			resolvedName = qualified
			columns = schemaColumns(c.schema[qualified])
		}
	}
	sourceKind := "table"
	var columnRanges map[string]ParseContextRange
	if cte, ok := ctes[strings.ToLower(lastIdentifier(name))]; ok {
		sourceKind = "cte"
		resolvedName = cte.Name
		columns = cte.Columns
		columnRanges = cte.ColumnRanges
	}
	alias := optionalGolyglotIdentifier(value.Alias)
	return ParseContextTable{
		Name: name, SourceKind: sourceKind, ResolvedName: resolvedName, Alias: alias,
		Columns: columns, ColumnRanges: columnRanges, Parts: partRanges,
		AliasRange: nativeIdentifierRange(c.query, value.Alias), ScopeRange: cloneParseContextRange(scopeRange),
	}
}

func (c *nativeParseContextCollector) parseDerivedTable(value *golyglot.SubqueryFrom, ctes map[string]polyglotCTE, scopeRange *ParseContextRange) ParseContextTable {
	name := optionalGolyglotIdentifier(value.Alias)
	columns := []SchemaColumn(nil)
	if value.Query != nil {
		body := nativeSourceSlice(c.query, value.Query.SourceSpan())
		schema := buildGolyglotSchema(mergePolyglotCTEsIntoSchema(c.schema, ctes), c.constraints)
		if analysis, err := golyglot.AnalyzeQuery(body, golyglot.AnalyzeQueryOptions{Dialect: c.dialect, Schema: &schema}); err == nil {
			for _, output := range analysis.OutputColumns {
				column := SchemaColumn{Name: output.Name, Nullable: queryProjectionNullable(output.Nullability)}
				if output.TypeHint != nil {
					column.Type = normalizeInferredType(*output.TypeHint)
				}
				columns = append(columns, column)
			}
		}
	}
	parts := []ParseContextPart(nil)
	if value.Alias != nil {
		parts = append(parts, ParseContextPart{Name: name, Kind: "table", Range: rangeFromOffsets(c.query, value.Alias.Span.Start, value.Alias.Span.End)})
	}
	return ParseContextTable{Name: name, SourceKind: "derived", ResolvedName: name, Alias: name, Columns: columns, Parts: parts, AliasRange: nativeIdentifierRange(c.query, value.Alias), ScopeRange: cloneParseContextRange(scopeRange)}
}

func (c *nativeParseContextCollector) parseTableFunction(value *golyglot.TableFunctionFrom, scopeRange *ParseContextRange) ParseContextTable {
	name := identifiersFromGolyglot(value.Name)
	alias := optionalGolyglotIdentifier(value.Alias)
	resolvedName := name
	if alias != "" {
		resolvedName = alias
	}
	columns := []SchemaColumn(nil)
	switch strings.ToUpper(lastIdentifier(name)) {
	case "RANGE":
		columns = []SchemaColumn{{Name: "range", Type: "BIGINT"}}
	case "GENERATE_SERIES":
		columns = []SchemaColumn{{Name: "generate_series", Type: "BIGINT"}}
	case "UNNEST", "EXPLODE":
		columns = []SchemaColumn{{Name: strings.ToLower(lastIdentifier(name))}}
	}
	for index, column := range value.Columns {
		if index < len(columns) {
			columns[index].Name = column.Text
		} else {
			columns = append(columns, SchemaColumn{Name: column.Text})
		}
	}
	parts := make([]ParseContextPart, 0, len(value.Name))
	for index, identifier := range value.Name {
		kind := "schema"
		if index == len(value.Name)-1 {
			kind = "table"
		}
		parts = append(parts, ParseContextPart{Name: identifier.Text, Kind: kind, Range: rangeFromOffsets(c.query, identifier.Span.Start, identifier.Span.End)})
	}
	return ParseContextTable{Name: name, SourceKind: "table_function", ResolvedName: resolvedName, Alias: alias, Columns: columns, Parts: parts, AliasRange: nativeIdentifierRange(c.query, value.Alias), ScopeRange: cloneParseContextRange(scopeRange)}
}

func (c *nativeParseContextCollector) parseColumn(identifier *golyglot.IdentifierExpr, scope *nativeParseScope) ParseContextColumn {
	parts := make([]ParseContextPart, 0, len(identifier.Parts))
	names := make([]string, 0, len(identifier.Parts))
	for index, part := range identifier.Parts {
		kind := "schema"
		if index == len(identifier.Parts)-1 {
			kind = "column"
		} else if index == len(identifier.Parts)-2 {
			kind = "table"
		}
		names = append(names, part.Text)
		parts = append(parts, ParseContextPart{Name: part.Text, Kind: kind, Range: rangeFromOffsets(c.query, part.Span.Start, part.Span.End)})
	}
	name := names[len(names)-1]
	qualifier := ""
	if len(names) > 1 {
		qualifier = strings.Join(names[:len(names)-1], ".")
	}
	resolved := resolveNativeColumn(name, qualifier, scope)
	return ParseContextColumn{Name: name, Qualifier: qualifier, ResolvedTable: resolved, Parts: parts}
}

func resolveNativeColumn(name, qualifier string, scope *nativeParseScope) string {
	for current := scope; current != nil; current = current.parent {
		if qualifier != "" {
			matches := make([]ParseContextTable, 0, 1)
			for _, table := range current.tables {
				if nativeTableMatchesQualifier(table, qualifier) {
					matches = append(matches, table)
				}
			}
			if len(matches) == 1 {
				return nativeResolvedTableName(matches[0])
			}
			if len(matches) > 1 {
				return ""
			}
			continue
		}
		matches := make([]string, 0, 1)
		for _, table := range current.tables {
			for _, column := range table.Columns {
				if strings.EqualFold(column.Name, name) {
					matches = append(matches, nativeResolvedTableName(table))
					break
				}
			}
		}
		if len(matches) == 1 {
			return matches[0]
		}
		if len(matches) > 1 {
			return ""
		}
	}
	return ""
}

func nativeTableMatchesQualifier(table ParseContextTable, qualifier string) bool {
	if table.Alias != "" {
		return strings.EqualFold(table.Alias, qualifier)
	}
	return strings.EqualFold(table.Name, qualifier) || strings.EqualFold(table.ResolvedName, qualifier) || strings.EqualFold(lastIdentifier(table.Name), qualifier) || strings.EqualFold(lastIdentifier(table.ResolvedName), qualifier)
}

func nativeResolvedTableName(table ParseContextTable) string {
	if table.ResolvedName != "" {
		return table.ResolvedName
	}
	return table.Name
}

func nativeSelectExpressions(selectStmt *golyglot.SelectStmt) []golyglot.Expr {
	result := make([]golyglot.Expr, 0, len(selectStmt.Projections)+8)
	for _, projection := range selectStmt.Projections {
		result = append(result, projection.Expr)
		result = append(result, projection.Except...)
		for _, replacement := range projection.Replace {
			result = append(result, replacement.Expr)
		}
	}
	result = append(result, selectStmt.Where)
	for _, expression := range selectStmt.GroupBy {
		if identifier, ok := expression.(*golyglot.IdentifierExpr); ok && len(identifier.Parts) == 1 && strings.EqualFold(identifier.Parts[0].Text, "ALL") {
			continue
		}
		result = append(result, expression)
	}
	result = append(result, selectStmt.Having, selectStmt.Qualify, selectStmt.ConnectBy)
	for _, table := range selectStmt.From {
		result = append(result, nativeFromItemExpressions(table.Primary)...)
		for _, join := range table.Joins {
			result = append(result, join.Condition)
			result = append(result, nativeFromItemExpressions(join.Right)...)
		}
		for _, lateral := range table.LateralViews {
			result = append(result, lateral.Expression)
		}
	}
	for _, order := range selectStmt.SortBy {
		result = append(result, order.Expr)
	}
	for _, order := range selectStmt.OrderBy {
		result = append(result, order.Expr)
	}
	for _, window := range selectStmt.Windows {
		result = append(result, window.Spec.PartitionBy...)
		for _, order := range window.Spec.OrderBy {
			result = append(result, order.Expr)
		}
	}
	return result
}

func nativeFromItemExpressions(item golyglot.FromItem) []golyglot.Expr {
	switch value := item.(type) {
	case *golyglot.TableFunctionFrom:
		return append([]golyglot.Expr(nil), value.Args...)
	case *golyglot.GroupedFrom:
		var result []golyglot.Expr
		for _, table := range value.Items {
			result = append(result, nativeFromItemExpressions(table.Primary)...)
			for _, join := range table.Joins {
				result = append(result, join.Condition)
				result = append(result, nativeFromItemExpressions(join.Right)...)
			}
		}
		return result
	default:
		return nil
	}
}

func nativeExpressionIdentifiers(expression golyglot.Expr) ([]*golyglot.IdentifierExpr, []*golyglot.SelectStmt) {
	if expression == nil {
		return nil, nil
	}
	var identifiers []*golyglot.IdentifierExpr
	var nested []*golyglot.SelectStmt
	var typeSpans []golyglot.Span
	golyglot.Walk(expression, func(node golyglot.Node) golyglot.VisitAction {
		switch value := node.(type) {
		case *golyglot.SelectStmt:
			return golyglot.SkipChildren
		case *golyglot.SubqueryExpr:
			if value.Query != nil {
				nested = append(nested, value.Query)
			}
			return golyglot.SkipChildren
		case *golyglot.ExistsExpr:
			if value.Query != nil {
				nested = append(nested, value.Query)
			}
			return golyglot.SkipChildren
		case *golyglot.QuantifiedExpr:
			if value.Query != nil {
				nested = append(nested, value.Query)
			}
			return golyglot.SkipChildren
		case *golyglot.InExpr:
			if value.Query != nil {
				nested = append(nested, value.Query)
			}
		case *golyglot.CastExpr:
			if value.Type != nil {
				typeSpans = append(typeSpans, value.Type.SourceSpan())
			}
		case *golyglot.IdentifierExpr:
			if len(value.Parts) == 0 || value.Parts[len(value.Parts)-1].Text == "*" || spanInsideAny(value.SourceSpan(), typeSpans) {
				return golyglot.VisitChildren
			}
			identifiers = append(identifiers, value)
		}
		return golyglot.VisitChildren
	})
	return identifiers, nested
}

func golyglotCallLikeSubqueryDiagnostics(query string, existing []ParseContextDiagnostic) []ParseContextDiagnostic {
	var diagnostics []ParseContextDiagnostic
	for _, match := range polyglotCallLikeSubqueryPattern.FindAllStringSubmatchIndex(query, -1) {
		if len(match) < 4 {
			continue
		}
		name := query[match[2]:match[3]]
		if golyglotCallLikeSubqueryKeyword(name) {
			continue
		}
		message := "Unresolved column: " + name
		duplicate := false
		for _, diagnostic := range existing {
			if strings.EqualFold(diagnostic.Message, message) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		rangeInfo := rangeFromOffsets(query, match[2], match[3])
		diagnostics = append(diagnostics, ParseContextDiagnostic{
			Code: authoringdiag.CodeUnresolvedColumn, Source: authoringdiag.SourceRenart,
			Message: message, Severity: "error", Range: &rangeInfo,
		})
	}
	return diagnostics
}

func golyglotCallLikeSubqueryKeyword(name string) bool {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "AS", "IN", "EXISTS", "FROM", "JOIN", "ON", "WHERE", "HAVING", "QUALIFY", "WHEN", "THEN", "ELSE", "CASE", "WITH", "OVER", "FILTER", "LATERAL":
		return true
	default:
		return false
	}
}

func spanInsideAny(span golyglot.Span, parents []golyglot.Span) bool {
	for _, parent := range parents {
		if span.Start >= parent.Start && span.End <= parent.End {
			return true
		}
	}
	return false
}

func nativeValidationErrors(errors []golyglot.ValidationError) []polyglotValidationError {
	result := make([]polyglotValidationError, 0, len(errors))
	for _, issue := range errors {
		code := strings.ToUpper(strings.TrimSpace(issue.Code))
		switch code {
		case "SCHEMA_UNKNOWN_TABLE", "SCHEMA_UNKNOWN_COLUMN":
			continue
		case "SEMANTIC_AMBIGUOUS_COLUMN":
			code = "E221"
			name := quotedIdentifierFromMessage(issue.Message)
			if name != "" {
				issue.Message = fmt.Sprintf("Ambiguous unqualified column '%s' across multiple relations", name)
			}
		}
		start, end := issue.Span.Start, issue.Span.End
		line, column := issue.Line, issue.Column
		result = append(result, polyglotValidationError{Message: issue.Message, Severity: issue.Severity, Code: code, Line: line, Column: column, Start: &start, End: &end})
	}
	return result
}

func quotedIdentifierFromMessage(message string) string {
	for _, quote := range []byte{'"', '\'', '`'} {
		start := strings.IndexByte(message, quote)
		if start < 0 {
			continue
		}
		end := strings.IndexByte(message[start+1:], quote)
		if end >= 0 {
			return message[start+1 : start+1+end]
		}
	}
	return ""
}

func golyglotTokens(tokens []golyglot.Token) []polyglotToken {
	result := make([]polyglotToken, 0, len(tokens))
	for _, token := range tokens {
		if token.Kind == golyglot.TokenEOF {
			continue
		}
		result = append(result, polyglotToken{Type: golyglotTokenType(token), Text: token.Text, Span: polyglotSpan{Start: token.Span.Start, End: token.Span.End}})
	}
	return result
}

func golyglotTokenType(token golyglot.Token) string {
	switch token.Text {
	case "(":
		return "L_PAREN"
	case ")":
		return "R_PAREN"
	case ";":
		return "SEMICOLON"
	}
	if token.Kind == golyglot.TokenKeyword {
		return strings.ToUpper(token.Text)
	}
	switch token.Kind {
	case golyglot.TokenIdentifier, golyglot.TokenQuotedIdentifier:
		return "IDENTIFIER"
	case golyglot.TokenString:
		return "STRING"
	case golyglot.TokenNumber:
		return "NUMBER"
	case golyglot.TokenComment:
		return "COMMENT"
	default:
		return strings.ToUpper(token.Text)
	}
}

func golyglotQueryKind(statements []golyglot.Statement) string {
	if len(statements) == 0 {
		return "unknown"
	}
	switch node := statements[0].Node.(type) {
	case *golyglot.SelectStmt:
		if node.SetRight != nil && strings.TrimSpace(node.SetOperator) != "" {
			return strings.ToLower(strings.TrimSpace(node.SetOperator))
		}
		return "select"
	case *golyglot.InsertStmt:
		return "insert"
	case *golyglot.UpdateStmt:
		return "update"
	case *golyglot.DeleteStmt:
		return "delete"
	case *golyglot.CreateTableStmt:
		return "create"
	case *golyglot.CommandStmt:
		return strings.ToLower(strings.TrimSpace(node.Keyword))
	case *golyglot.RawStmt:
		return strings.ToLower(strings.TrimSpace(node.Keyword))
	default:
		return strings.TrimSuffix(string(statements[0].Node.Kind()), "_statement")
	}
}

func golyglotReadOnlyStatement(node golyglot.Node) bool {
	_, ok := node.(*golyglot.SelectStmt)
	return ok
}

func nativeNodeRange(query string, span golyglot.Span) *ParseContextRange {
	if !span.Valid(len(query)) {
		return nil
	}
	value := rangeFromOffsets(query, span.Start, span.End)
	return &value
}

func nativeIdentifierRange(query string, identifier *golyglot.Identifier) *ParseContextRange {
	if identifier == nil || !identifier.Span.Valid(len(query)) {
		return nil
	}
	value := rangeFromOffsets(query, identifier.Span.Start, identifier.Span.End)
	return &value
}

func cloneParseContextRange(value *ParseContextRange) *ParseContextRange {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func nativeSourceSlice(query string, span golyglot.Span) string {
	if !span.Valid(len(query)) {
		return ""
	}
	return query[span.Start:span.End]
}

func identifiersFromGolyglot(identifiers []golyglot.Identifier) string {
	parts := make([]string, len(identifiers))
	for index, identifier := range identifiers {
		parts[index] = identifier.Text
	}
	return strings.Join(parts, ".")
}

func optionalGolyglotIdentifier(identifier *golyglot.Identifier) string {
	if identifier == nil {
		return ""
	}
	return identifier.Text
}

func lastIdentifier(value string) string {
	if index := strings.LastIndexByte(value, '.'); index >= 0 {
		return value[index+1:]
	}
	return value
}
