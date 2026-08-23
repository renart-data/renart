package sqlintelligence

import (
	"context"
	"regexp"
	"strings"

	"renart/internal/authoringdiag"
)

var derivedTableAliasPattern = regexp.MustCompile(`(?is)\)\s+(?:as\s+)?([A-Za-z_][\w$]*)`)

type RelationConfidence string

const (
	RelationKnown   RelationConfidence = "known"
	RelationUnknown RelationConfidence = "unknown"
)

type ValidationRequest struct {
	URI                 string
	SQL                 string
	Dialect             string
	Schema              Schema
	SchemaConstraints   SchemaConstraints
	RelationConfidence  map[string]RelationConfidence
	ColumnSourceMethods SchemaColumnSourceMethods
	ExpectedOutput      []SchemaColumn
}

type ValidationResult struct {
	Diagnostics  []authoringdiag.Diagnostic
	ParseContext *ParseContext
}

// ValidateSQL is the context-aware semantic SQL validation entry point shared
// by type-checking and both LSP transports. It preserves the existing strict
// schema-validation rules while suppressing only column findings that depend
// on a relation whose schema is unknown.
func ValidateSQL(ctx context.Context, req ValidationRequest) (ValidationResult, error) {
	parseContext, err := ParseContextWithSchemaConstraintsGolyglotContext(
		ctx,
		req.SQL,
		req.Dialect,
		req.Schema,
		req.SchemaConstraints,
		req.ColumnSourceMethods,
	)
	if err != nil {
		return ValidationResult{}, err
	}

	result := ValidationResult{ParseContext: parseContext}
	seen := map[string]struct{}{}
	for _, diagnostic := range parseContext.Diagnostics {
		if isDerivedTableAliasDiagnostic(req.SQL, diagnostic) {
			continue
		}
		if shouldSuppressForUnknownSchema(diagnostic, parseContext, req.Schema, req.RelationConfidence) {
			continue
		}
		converted := authoringDiagnostic(req.URI, diagnostic)
		if _, duplicate := seen[converted.Key()]; duplicate {
			continue
		}
		seen[converted.Key()] = struct{}{}
		result.Diagnostics = append(result.Diagnostics, converted)
	}
	if len(parseContext.Errors) == 0 && len(req.ExpectedOutput) > 0 {
		drift, err := OutputDriftDiagnostics(ctx, req.URI, req.SQL, req.Dialect, req.Schema, req.SchemaConstraints, req.RelationConfidence, req.ExpectedOutput)
		if err == nil {
			for _, diagnostic := range drift {
				if _, duplicate := seen[diagnostic.Key()]; duplicate {
					continue
				}
				seen[diagnostic.Key()] = struct{}{}
				result.Diagnostics = append(result.Diagnostics, diagnostic)
			}
		}
	}
	return result, nil
}

func isDerivedTableAliasDiagnostic(sql string, diagnostic ParseContextDiagnostic) bool {
	if diagnostic.Code != authoringdiag.CodeUnresolvedAlias {
		return false
	}
	const prefix = "Unresolved table or alias: "
	if !strings.HasPrefix(diagnostic.Message, prefix) {
		return false
	}
	qualifier := strings.TrimSpace(strings.TrimPrefix(diagnostic.Message, prefix))
	for _, match := range derivedTableAliasPattern.FindAllStringSubmatch(sql, -1) {
		if len(match) > 1 && strings.EqualFold(match[1], qualifier) {
			return true
		}
	}
	return false
}

func authoringDiagnostic(uri string, diagnostic ParseContextDiagnostic) authoringdiag.Diagnostic {
	severity := authoringdiag.SeverityWarning
	switch strings.ToLower(strings.TrimSpace(diagnostic.Severity)) {
	case "error":
		severity = authoringdiag.SeverityError
	case "info":
		severity = authoringdiag.SeverityInfo
	case "hint":
		severity = authoringdiag.SeverityHint
	}
	code := strings.TrimSpace(diagnostic.Code)
	if code == "" {
		code = authoringdiag.CodeSQLValidationFailed
	}
	source := strings.TrimSpace(diagnostic.Source)
	if source == "" {
		source = authoringdiag.SourceRenart
	}
	result := authoringdiag.Diagnostic{
		Code:       code,
		Source:     source,
		Severity:   severity,
		Message:    diagnostic.Message,
		URI:        uri,
		Scope:      authoringdiag.ScopeDocument,
		Confidence: authoringdiag.ConfidenceMedium,
	}
	if diagnostic.Range != nil {
		result.StartByte, result.EndByte = authoringdiag.ByteRange(diagnostic.Range.Start, diagnostic.Range.End)
		result.Confidence = authoringdiag.ConfidenceHigh
	}
	return result
}

func shouldSuppressForUnknownSchema(diagnostic ParseContextDiagnostic, parseContext *ParseContext, schema Schema, confidence map[string]RelationConfidence) bool {
	if diagnostic.Code != authoringdiag.CodeUnresolvedColumn {
		return false
	}
	if parseContext == nil || len(parseContext.Tables) == 0 {
		return true
	}

	column := columnForDiagnostic(parseContext.Columns, diagnostic.Range)
	if column != nil && strings.TrimSpace(column.ResolvedTable) != "" {
		return !isRelationSchemaKnown(column.ResolvedTable, parseContext.Tables, schema, confidence)
	}
	if column != nil && strings.TrimSpace(column.Qualifier) != "" {
		for _, table := range parseContext.Tables {
			if strings.EqualFold(table.Alias, column.Qualifier) || strings.EqualFold(table.Name, column.Qualifier) || strings.EqualFold(table.ResolvedName, column.Qualifier) {
				name := table.ResolvedName
				if strings.TrimSpace(name) == "" {
					name = table.Name
				}
				return !isRelationSchemaKnown(name, parseContext.Tables, schema, confidence)
			}
		}
	}

	// An unqualified reference can belong to any visible relation. It is only
	// provably absent when every visible relation has a known schema.
	for _, table := range parseContext.Tables {
		name := table.ResolvedName
		if strings.TrimSpace(name) == "" {
			name = table.Name
		}
		if !isRelationSchemaKnown(name, parseContext.Tables, schema, confidence) {
			return true
		}
	}
	return false
}

func columnForDiagnostic(columns []ParseContextColumn, diagnosticRange *ParseContextRange) *ParseContextColumn {
	if diagnosticRange == nil {
		return nil
	}
	for i := range columns {
		for _, part := range columns[i].Parts {
			if part.Range.Start <= diagnosticRange.Start && part.Range.End >= diagnosticRange.End {
				return &columns[i]
			}
		}
	}
	return nil
}

func isRelationSchemaKnown(name string, tables []ParseContextTable, schema Schema, confidence map[string]RelationConfidence) bool {
	if value, ok := relationConfidenceForName(name, confidence); ok {
		return value == RelationKnown
	}
	for _, table := range tables {
		if table.SourceKind == "cte" && (strings.EqualFold(table.Name, name) || strings.EqualFold(table.ResolvedName, name)) {
			return len(table.Columns) > 0
		}
	}
	if columns, ok := schemaForName(name, schema); ok {
		return len(columns) > 0
	}
	return false
}

func relationConfidenceForName(name string, confidence map[string]RelationConfidence) (RelationConfidence, bool) {
	for candidate, value := range confidence {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(name)) {
			return value, true
		}
	}
	return "", false
}

func schemaForName(name string, schema Schema) (map[string]string, bool) {
	for candidate, columns := range schema {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(name)) {
			return columns, true
		}
	}
	return nil, false
}
