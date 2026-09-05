package sqlintelligence

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/renart-data/golyglot/pkg/golyglot"

	"renart/internal/authoringdiag"
)

type cachedDataType struct {
	value golyglot.DataType
	ok    bool
}

var parsedDataTypes sync.Map

// OutputDriftDiagnostics compares a SQL asset's inferred projection with its
// declared output contract. Explicit projection names are compared as a set;
// same-name columns understood by Golyglot's standalone data-type parser are
// also compared by type. Compact analysis selectively fills names and types
// through CTEs/stars and checks explicit NOT NULL contracts. Unknown facts stay
// silent rather than producing low-confidence warnings.
func OutputDriftDiagnostics(
	ctx context.Context,
	uri, query, dialect string,
	schema Schema,
	constraints SchemaConstraints,
	relationConfidence map[string]RelationConfidence,
	expected []SchemaColumn,
) ([]authoringdiag.Diagnostic, error) {
	if strings.TrimSpace(query) == "" || len(expected) == 0 {
		return nil, nil
	}
	inferred, completeNames, err := annotateOutputColumns(ctx, query, dialect, schema)
	if err != nil {
		return nil, err
	}
	if completeNames {
		if analysis, analysisErr := AnalyzeQuery(ctx, query, dialect, schema, constraints); analysisErr == nil {
			completeNames = analysis.OutputNamesComplete && outputDriftAnalysisNamesReliable(analysis, relationConfidence)
		}
	}
	if outputDriftNeedsCompactAnalysis(inferred, completeNames, expected) {
		analysis, analysisErr := AnalyzeQuery(ctx, query, dialect, schema, constraints)
		if analysisErr == nil {
			compactNamesComplete := analysis.OutputNamesComplete && outputDriftAnalysisNamesReliable(analysis, relationConfidence)
			inferred = mergeCompactOutputColumns(inferred, analysis.OutputColumns, !completeNames && compactNamesComplete)
			completeNames = completeNames || compactNamesComplete
		}
	}
	declaredByName := make(map[string]SchemaColumn, len(expected))
	for _, column := range expected {
		name := strings.ToLower(strings.TrimSpace(column.Name))
		if name != "" {
			declaredByName[name] = column
		}
	}
	inferredByName := make(map[string]SchemaColumn, len(inferred))
	for _, column := range inferred {
		name := strings.ToLower(strings.TrimSpace(column.Name))
		if name != "" {
			inferredByName[name] = column
		}
	}

	diagnostics := make([]authoringdiag.Diagnostic, 0)
	if completeNames {
		missing := make([]string, 0)
		for _, declared := range expected {
			name := strings.ToLower(strings.TrimSpace(declared.Name))
			if name == "" {
				continue
			}
			if _, ok := inferredByName[name]; !ok {
				missing = append(missing, strings.TrimSpace(declared.Name))
			}
		}
		undeclared := make([]string, 0)
		for _, actual := range inferred {
			name := strings.ToLower(strings.TrimSpace(actual.Name))
			if name == "" {
				continue
			}
			if _, ok := declaredByName[name]; !ok {
				undeclared = append(undeclared, strings.TrimSpace(actual.Name))
			}
		}
		if len(missing) > 0 || len(undeclared) > 0 {
			diagnostics = append(diagnostics, authoringdiag.Diagnostic{
				Code:       authoringdiag.CodeDeclaredOutputSchemaDrift,
				Source:     authoringdiag.SourceRenart,
				Severity:   authoringdiag.SeverityWarning,
				Message:    outputSchemaDriftMessage(missing, undeclared),
				URI:        uri,
				Scope:      authoringdiag.ScopeAsset,
				Confidence: authoringdiag.ConfidenceHigh,
			})
		}
	}
	for _, declared := range expected {
		name := strings.ToLower(strings.TrimSpace(declared.Name))
		actual, ok := inferredByName[name]
		if !ok || strings.TrimSpace(declared.Type) == "" || strings.TrimSpace(actual.Type) == "" {
			continue
		}
		compatible, comparable, err := dataTypesEquivalent(ctx, declared.Type, actual.Type, dialect)
		if err != nil {
			return nil, err
		}
		if !comparable || compatible {
			continue
		}
		diagnostics = append(diagnostics, authoringdiag.Diagnostic{
			Code:       authoringdiag.CodeDeclaredColumnTypeDrift,
			Subject:    &authoringdiag.Subject{Column: declared.Name, Field: "type"},
			Source:     authoringdiag.SourceRenart,
			Severity:   authoringdiag.SeverityWarning,
			Message:    fmt.Sprintf("Column %q is declared as %s, but the SQL output is inferred as %s.", declared.Name, declared.Type, actual.Type),
			URI:        uri,
			Scope:      authoringdiag.ScopeAsset,
			Confidence: authoringdiag.ConfidenceHigh,
		})
	}
	for _, declared := range expected {
		if declared.Nullable == nil || *declared.Nullable {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(declared.Name))
		actual, ok := inferredByName[name]
		if !ok || actual.Nullable == nil || !*actual.Nullable {
			continue
		}
		diagnostics = append(diagnostics, authoringdiag.Diagnostic{
			Code:       authoringdiag.CodeDeclaredColumnNullabilityDrift,
			Source:     authoringdiag.SourceRenart,
			Severity:   authoringdiag.SeverityWarning,
			Message:    fmt.Sprintf("Column %q is declared NOT NULL, but the SQL output may be NULL.", declared.Name),
			URI:        uri,
			Scope:      authoringdiag.ScopeAsset,
			Confidence: authoringdiag.ConfidenceHigh,
		})
	}
	return diagnostics, nil
}

func outputDriftAnalysisNamesReliable(analysis QueryAnalysis, confidence map[string]RelationConfidence) bool {
	if len(analysis.StarProjections) == 0 {
		return true
	}
	for _, relation := range analysis.BaseTables {
		if !strings.EqualFold(strings.TrimSpace(relation.Kind), "table") {
			continue
		}
		value, ok := relationConfidenceForName(relation.Name, confidence)
		if !ok || value != RelationKnown {
			return false
		}
	}
	return true
}

func outputDriftNeedsCompactAnalysis(inferred []SchemaColumn, completeNames bool, expected []SchemaColumn) bool {
	if !completeNames {
		return true
	}
	inferredByName := make(map[string]SchemaColumn, len(inferred))
	for _, column := range inferred {
		inferredByName[strings.ToLower(strings.TrimSpace(column.Name))] = column
	}
	for _, declared := range expected {
		if declared.Nullable != nil && !*declared.Nullable {
			return true
		}
		actual, ok := inferredByName[strings.ToLower(strings.TrimSpace(declared.Name))]
		if ok && strings.TrimSpace(declared.Type) != "" && strings.TrimSpace(actual.Type) == "" {
			return true
		}
	}
	return false
}

// mergeCompactOutputColumns supplements the annotated-AST fast path. When
// compact analysis has the complete name set (for example, a star through a
// CTE), its columns become the base; otherwise only matching facts are merged.
func mergeCompactOutputColumns(annotated, compact []SchemaColumn, compactNamesComplete bool) []SchemaColumn {
	if compactNamesComplete {
		result := append([]SchemaColumn(nil), compact...)
		return mergeCompactOutputColumns(result, annotated, false)
	}
	result := append([]SchemaColumn(nil), annotated...)
	for _, supplement := range compact {
		for index := range result {
			if !strings.EqualFold(strings.TrimSpace(result[index].Name), strings.TrimSpace(supplement.Name)) {
				continue
			}
			if strings.TrimSpace(result[index].Type) == "" {
				result[index].Type = supplement.Type
			}
			if result[index].Nullable == nil {
				result[index].Nullable = supplement.Nullable
			}
			break
		}
	}
	return result
}

func outputSchemaDriftMessage(missing, undeclared []string) string {
	parts := make([]string, 0, 2)
	if len(missing) > 0 {
		parts = append(parts, fmt.Sprintf("SQL does not produce declared %s %s", columnLabel(len(missing)), quotedColumnNames(missing)))
	}
	if len(undeclared) > 0 {
		parts = append(parts, fmt.Sprintf("SQL produces undeclared %s %s", columnLabel(len(undeclared)), quotedColumnNames(undeclared)))
	}
	return strings.Join(parts, "; ") + "."
}

func columnLabel(count int) string {
	if count == 1 {
		return "column"
	}
	return "columns"
}

func quotedColumnNames(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, fmt.Sprintf("%q", name))
	}
	return strings.Join(quoted, ", ")
}

func dataTypesEquivalent(ctx context.Context, left, right, dialect string) (equivalent, comparable bool, err error) {
	if normalizedTypeText(left) == normalizedTypeText(right) {
		return true, true, nil
	}
	leftType, leftOK, err := parseComparableDataType(ctx, left, dialect)
	if err != nil {
		return false, false, err
	}
	rightType, rightOK, err := parseComparableDataType(ctx, right, dialect)
	if err != nil {
		return false, false, err
	}
	if !leftOK || !rightOK {
		return false, false, nil
	}
	return comparableDataTypeValues(leftType, rightType), true, nil
}

// StrictDataTypesEquivalent compares the full canonical logical type rather
// than treating a missing modifier as a wildcard. It is used when deciding
// whether two schema-evidence sources agree: precision, scale, length, nested
// element types, and timezone structure must round-trip losslessly to compare
// equal. Native spellings remain available to callers for display/persistence.
func StrictDataTypesEquivalent(ctx context.Context, left, right, dialect string) (equivalent, comparable bool, err error) {
	if normalizedTypeText(left) == normalizedTypeText(right) {
		return true, true, nil
	}
	leftType, leftOK, err := parseComparableDataType(ctx, left, dialect)
	if err != nil {
		return false, false, err
	}
	rightType, rightOK, err := parseComparableDataType(ctx, right, dialect)
	if err != nil {
		return false, false, err
	}
	if !leftOK || !rightOK {
		return false, false, nil
	}
	return reflect.DeepEqual(leftType, rightType), true, nil
}

func parseComparableDataType(ctx context.Context, value, dialect string) (golyglot.DataType, bool, error) {
	if err := ctx.Err(); err != nil {
		return golyglot.DataType{}, false, err
	}
	key := strings.ToLower(strings.TrimSpace(dialect)) + "\x00" + normalizedTypeText(value)
	if cached, ok := parsedDataTypes.Load(key); ok {
		entry := cached.(cachedDataType)
		return entry.value, entry.ok, nil
	}
	nativeDialect, err := golyglot.ParseDialect(dialect)
	if err != nil {
		return golyglot.DataType{}, false, err
	}
	dataType, err := golyglot.ParseDataType(strings.TrimSpace(value), nativeDialect)
	if err != nil || !dataType.Known() {
		parsedDataTypes.Store(key, cachedDataType{})
		return golyglot.DataType{}, false, nil
	}
	entry := cachedDataType{value: dataType, ok: true}
	parsedDataTypes.Store(key, entry)
	return entry.value, entry.ok, nil
}

func comparableDataTypeValues(left, right golyglot.DataType) bool {
	if left.Kind != right.Kind {
		return false
	}
	if left.Kind == golyglot.DataTypeCustom && !strings.EqualFold(strings.TrimSpace(left.Name), strings.TrimSpace(right.Name)) {
		return false
	}
	if !compatibleOptionalInt(left.Length, right.Length) || !compatibleOptionalInt(left.Precision, right.Precision) || !compatibleOptionalInt(left.Scale, right.Scale) {
		return false
	}
	if (left.Kind == golyglot.DataTypeTime || left.Kind == golyglot.DataTypeTimestamp) && left.WithTimezone != right.WithTimezone {
		return false
	}
	if !compatibleOptionalDataType(left.Element, right.Element) || !compatibleOptionalDataType(left.Key, right.Key) || !compatibleOptionalDataType(left.Value, right.Value) {
		return false
	}
	if len(left.Fields) > 0 && len(right.Fields) > 0 {
		if len(left.Fields) != len(right.Fields) {
			return false
		}
		for index := range left.Fields {
			if !strings.EqualFold(left.Fields[index].Name, right.Fields[index].Name) || !comparableDataTypeValues(left.Fields[index].Type, right.Fields[index].Type) {
				return false
			}
		}
	}
	return true
}

func compatibleOptionalInt(left, right *int) bool {
	return left == nil || right == nil || *left == *right
}

func compatibleOptionalDataType(left, right *golyglot.DataType) bool {
	return left == nil || right == nil || comparableDataTypeValues(*left, *right)
}

func normalizedTypeText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}
