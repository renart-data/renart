package sqlintelligence

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"renart/internal/authoringdiag"
	"renart/internal/sqlformat"
)

type parsedDataTypeResponse struct {
	Success  bool            `json:"success"`
	DataType json.RawMessage `json:"dataType"`
	Error    any             `json:"error"`
}

type cachedDataType struct {
	value map[string]any
	ok    bool
}

var parsedDataTypes sync.Map

// OutputDriftDiagnostics compares a SQL asset's inferred projection with its
// declared output contract. Explicit projection names are compared as a set;
// same-name columns understood by Polyglot's standalone data-type parser are
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

func parseComparableDataType(ctx context.Context, value, dialect string) (map[string]any, bool, error) {
	key := strings.ToLower(strings.TrimSpace(dialect)) + "\x00" + normalizedTypeText(value)
	if cached, ok := parsedDataTypes.Load(key); ok {
		entry := cached.(cachedDataType)
		return entry.value, entry.ok, nil
	}
	raw, err := sqlformat.Call(ctx, "parse_data_type", strings.TrimSpace(value), dialect)
	if err != nil {
		return nil, false, err
	}
	var response parsedDataTypeResponse
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		return nil, false, err
	}
	if !response.Success || len(response.DataType) == 0 {
		parsedDataTypes.Store(key, cachedDataType{})
		return nil, false, nil
	}
	dataType, err := decodeParsedDataType(response.DataType)
	if err != nil {
		return nil, false, err
	}
	canonical, ok := canonicalDataTypeMap(dataType)
	entry := cachedDataType{value: canonical, ok: ok}
	parsedDataTypes.Store(key, entry)
	return entry.value, entry.ok, nil
}

func decodeParsedDataType(raw json.RawMessage) (map[string]any, error) {
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err == nil {
		return result, nil
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(encoded), &result); err != nil {
		return nil, err
	}
	return result, nil
}

func canonicalDataTypeMap(value map[string]any) (map[string]any, bool) {
	rawKind, _ := value["data_type"].(string)
	kind := canonicalDataTypeKind(rawKind, value)
	if kind == "" || kind == "unknown" {
		return nil, false
	}
	result := map[string]any{"data_type": kind}
	for key, child := range value {
		if key == "data_type" || child == nil || strings.Contains(key, "spelling") {
			continue
		}
		if rawKind == "custom" && key == "name" {
			continue
		}
		result[key] = canonicalDataTypeValue(child)
	}
	return result, true
}

func canonicalDataTypeValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		if canonical, ok := canonicalDataTypeMap(typed); ok {
			return canonical
		}
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			if child != nil && !strings.Contains(key, "spelling") {
				result[key] = canonicalDataTypeValue(child)
			}
		}
		return result
	case []any:
		result := make([]any, 0, len(typed))
		for _, child := range typed {
			result = append(result, canonicalDataTypeValue(child))
		}
		return result
	case string:
		return strings.ToUpper(strings.TrimSpace(typed))
	default:
		return typed
	}
}

func canonicalDataTypeKind(raw string, value map[string]any) string {
	kind := strings.ToLower(strings.TrimSpace(raw))
	if kind == "custom" {
		name, _ := value["name"].(string)
		kind = strings.ToLower(strings.TrimSpace(name))
	}
	switch strings.ReplaceAll(kind, " ", "_") {
	case "char", "character", "character_varying", "clob", "n_char", "n_varchar", "nchar", "nvarchar", "string", "text", "var_char", "varchar":
		return "string"
	case "bool", "boolean":
		return "boolean"
	case "int", "int4", "int32", "integer":
		return "integer"
	case "big_int", "bigint", "int8", "int64":
		return "bigint"
	case "small_int", "smallint", "int2", "int16":
		return "smallint"
	case "tiny_int", "tinyint", "int8_t":
		return "tinyint"
	case "decimal", "number", "numeric":
		return "decimal"
	case "double", "double_precision", "float64":
		return "double"
	case "float", "float32", "real":
		return "float"
	case "json", "jsonb", "object", "variant":
		return "json"
	default:
		return kind
	}
}

func comparableDataTypeValues(left, right any) bool {
	leftMap, leftIsMap := left.(map[string]any)
	rightMap, rightIsMap := right.(map[string]any)
	if leftIsMap || rightIsMap {
		if !leftIsMap || !rightIsMap {
			return false
		}
		leftKind, _ := leftMap["data_type"].(string)
		rightKind, _ := rightMap["data_type"].(string)
		if leftKind != rightKind {
			return false
		}
		for key, leftValue := range leftMap {
			if key == "data_type" {
				continue
			}
			rightValue, ok := rightMap[key]
			if !ok {
				continue
			}
			if !comparableDataTypeValues(leftValue, rightValue) {
				return false
			}
		}
		return true
	}
	leftSlice, leftIsSlice := left.([]any)
	rightSlice, rightIsSlice := right.([]any)
	if leftIsSlice || rightIsSlice {
		if !leftIsSlice || !rightIsSlice || len(leftSlice) != len(rightSlice) {
			return false
		}
		for index := range leftSlice {
			if !comparableDataTypeValues(leftSlice[index], rightSlice[index]) {
				return false
			}
		}
		return true
	}
	return fmt.Sprint(left) == fmt.Sprint(right)
}

func normalizedTypeText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}
