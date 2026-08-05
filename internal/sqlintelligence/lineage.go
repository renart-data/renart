package sqlintelligence

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"renart/internal/sqlcatalog"
	"renart/internal/sqlformat"
)

// AnnotateOutputColumns derives the output columns (name + type) of a SELECT
// from the query text and a schema built from the upstream *asset definitions*
// — not from querying the warehouse. It drives the polyglot engine's
// `annotate_types` function, which returns a type-annotated AST: computed
// expressions carry an `inferred_type`, while bare column references are
// resolved against the provided schema.
//
// This is the asset-as-source-of-truth column derivation: render an asset's SQL,
// pass its upstream assets' declared columns as the schema, and read back the
// projected columns with types.
func AnnotateOutputColumns(ctx context.Context, query, dialect string, schema Schema) ([]SchemaColumn, error) {
	inference, err := InferOutputSchema(ctx, query, dialect, schema)
	return inference.Columns, err
}

type OutputSchemaInference struct {
	Columns       []SchemaColumn
	NamesComplete bool
}

// InferOutputSchema exposes whether annotated-AST inference saw an explicit
// name for every projection. Callers can retain this fast path for ordinary
// SELECT lists and ask Polyglot's heavier compact analysis only when stars or
// otherwise incomplete projections need deeper scope expansion.
func InferOutputSchema(ctx context.Context, query, dialect string, schema Schema) (OutputSchemaInference, error) {
	columns, complete, err := annotateOutputColumns(ctx, query, dialect, schema)
	return OutputSchemaInference{Columns: columns, NamesComplete: complete}, err
}

// annotateOutputColumns also reports whether every output name came from an
// explicit projection. Name-set diagnostics use that signal to avoid claiming
// that a declaration is missing when SELECT * expansion depends on a schema
// snapshot that may itself be incomplete.
func annotateOutputColumns(ctx context.Context, query, dialect string, schema Schema) ([]SchemaColumn, bool, error) {
	if strings.TrimSpace(query) == "" {
		return nil, false, nil
	}

	schemaJSON, err := marshalPolyglotSchema(schema)
	if err != nil {
		return nil, false, err
	}

	raw, err := sqlformat.Call(ctx, "annotate_types", query, dialect, string(schemaJSON))
	if err != nil {
		return nil, false, err
	}

	var response struct {
		Success bool   `json:"success"`
		AST     []any  `json:"ast"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		return nil, false, err
	}
	if !response.Success {
		if response.Error != "" {
			return nil, false, errors.New(response.Error)
		}
		return nil, false, errors.New("annotate_types failed")
	}

	selectNode := findPolyglotSelect(response.AST)
	if selectNode == nil {
		return nil, false, nil
	}
	sourceTables, schema := outputColumnSourceSchema(selectNode, dialect, schema)

	expressions, expressionsOK := selectNode["expressions"].([]any)
	completeNames := expressionsOK && len(expressions) > 0
	columns := make([]SchemaColumn, 0, len(expressions))
	for _, expression := range expressions {
		if isPolyglotStar(expression) {
			completeNames = false
			for _, tableName := range sourceTables {
				for _, column := range schemaColumns(schema[tableName]) {
					columns = appendUniqueSchemaColumn(columns, column)
				}
			}
			continue
		}

		name := polyglotExpressionOutputName(expression)
		if name == "" {
			completeNames = false
			continue
		}
		columns = appendUniqueSchemaColumn(columns, SchemaColumn{
			Name: name,
			Type: outputColumnType(expression, sourceTables, schema, dialect),
		})
	}
	return columns, completeNames && len(columns) > 0, nil
}

// outputColumnSourceSchema augments declared asset schemas with the fixed
// output columns of built-in table functions. The SQL editor has the same
// knowledge for diagnostics and completion; output inference needs it too so a
// bare table-function column does not become an unknown type.
func outputColumnSourceSchema(selectNode map[string]any, dialect string, schema Schema) ([]string, Schema) {
	sourceTables := polyglotSelectSourceTables(selectNode)
	if !strings.EqualFold(strings.TrimSpace(dialect), "duckdb") {
		return sourceTables, schema
	}

	augmented := schema
	cloned := false
	appendSource := func(name string, columns map[string]string) {
		if !cloned {
			augmented = make(Schema, len(schema)+1)
			for tableName, tableColumns := range schema {
				augmented[tableName] = tableColumns
			}
			cloned = true
		}
		augmented[name] = columns
		for _, existing := range sourceTables {
			if strings.EqualFold(existing, name) {
				return
			}
		}
		sourceTables = append(sourceTables, name)
	}

	visit := func(key string, value map[string]any) {
		if key != "function" {
			return
		}
		name := strings.TrimSpace(polyglotIdentifierName(value["name"]))
		functionColumns, known := sqlcatalog.DuckDBTableFunctionColumns(name)
		if !known {
			return
		}
		columns := make(map[string]string, len(functionColumns))
		for _, column := range functionColumns {
			columns[column.Name] = column.Type
		}
		appendSource(strings.ToLower(name), columns)
	}
	walkPolyglot(selectNode["from"], visit)
	walkPolyglot(selectNode["joins"], visit)
	return sourceTables, augmented
}

// outputColumnType resolves a projected expression's type: the annotated
// inferred_type for a computed expression, otherwise a schema lookup for a
// (possibly aliased) bare column reference.
func outputColumnType(expression any, sourceTables []string, schema Schema, dialect string) string {
	mapExpression, ok := expression.(map[string]any)
	if !ok {
		return ""
	}

	if aliasNode, ok := mapExpression["alias"].(map[string]any); ok {
		if inferred := inferredTypeName(aliasNode["inferred_type"]); inferred != "" {
			return reconcileDuckDBAnnotatedIntegerExpression(inferred, aliasNode["this"], sourceTables, schema, dialect)
		}
		// An alias over a bare column carries no inferred_type; resolve the
		// underlying column against the schema.
		if column := underlyingColumnName(aliasNode["this"]); column != "" {
			return schemaColumnType(column, sourceTables, schema)
		}
		// The engine does not annotate types inside UNION branches; fall back to
		// the literal's own type when the projection is a literal.
		if t := literalTypeName(aliasNode["this"]); t != "" {
			return t
		}
		return ""
	}

	if columnNode, ok := mapExpression["column"].(map[string]any); ok {
		if column := polyglotIdentifierName(columnNode["name"]); column != "" {
			return schemaColumnType(column, sourceTables, schema)
		}
	}
	return ""
}

// Polyglot currently types an arithmetic expression from an integer literal
// before it resolves an unqualified DuckDB table-function column. For example,
// `range * 2` is annotated as INTEGER even though range() yields BIGINT and
// DuckDB coerces fitting integer literals to a typed column (so TINYINT * 2
// stays TINYINT), and otherwise promotes to the wider operand (so BIGINT * 2
// stays BIGINT). Reconcile only direct integer arithmetic where every operand
// can be resolved; all other expressions retain Polyglot's annotation.
func reconcileDuckDBAnnotatedIntegerExpression(
	annotated string,
	expression any,
	sourceTables []string,
	schema Schema,
	dialect string,
) string {
	if !strings.EqualFold(strings.TrimSpace(dialect), "duckdb") {
		return annotated
	}
	if _, _, annotatedOK := integerTypeWidth(annotated); !annotatedOK {
		return annotated
	}
	summary, ok := summarizeDuckDBIntegerExpression(expression, sourceTables, schema)
	if !ok || !summary.hasColumn {
		return annotated
	}
	for _, literal := range summary.literals {
		if !integerLiteralFitsType(literal, summary.columnType) {
			return annotated
		}
	}
	return summary.columnType
}

type duckDBIntegerExpressionSummary struct {
	columnType string
	columnRank int
	hasColumn  bool
	literals   []string
}

func summarizeDuckDBIntegerExpression(expression any, sourceTables []string, schema Schema) (duckDBIntegerExpressionSummary, bool) {
	node, ok := expression.(map[string]any)
	if !ok {
		return duckDBIntegerExpressionSummary{}, false
	}
	if column := underlyingColumnName(node); column != "" {
		columnType, columnRank, resolved := integerTypeWidth(schemaColumnType(column, sourceTables, schema))
		return duckDBIntegerExpressionSummary{
			columnType: columnType,
			columnRank: columnRank,
			hasColumn:  resolved,
		}, resolved
	}
	if literal, resolved := integerLiteralValue(node); resolved {
		return duckDBIntegerExpressionSummary{literals: []string{literal}}, true
	}
	for _, operator := range []string{"add", "sub", "mul", "mod"} {
		operation, ok := node[operator].(map[string]any)
		if !ok {
			continue
		}
		left, leftOK := summarizeDuckDBIntegerExpression(operation["left"], sourceTables, schema)
		right, rightOK := summarizeDuckDBIntegerExpression(operation["right"], sourceTables, schema)
		if !leftOK || !rightOK {
			return duckDBIntegerExpressionSummary{}, false
		}
		result := left
		if right.columnRank > result.columnRank {
			result.columnType = right.columnType
			result.columnRank = right.columnRank
		}
		result.hasColumn = left.hasColumn || right.hasColumn
		result.literals = append(result.literals, right.literals...)
		return result, true
	}
	return duckDBIntegerExpressionSummary{}, false
}

func integerLiteralValue(node map[string]any) (string, bool) {
	literal, ok := node["literal"].(map[string]any)
	if !ok || literal["literal_type"] != "number" {
		return "", false
	}
	value, ok := literal["value"].(string)
	return strings.TrimSpace(value), ok && !strings.ContainsAny(value, ".eE")
}

func integerLiteralFitsType(value, dataType string) bool {
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(dataType)) {
	case "TINYINT":
		return number >= -128 && number <= 127
	case "SMALLINT":
		return number >= -32768 && number <= 32767
	case "INTEGER":
		return number >= -2147483648 && number <= 2147483647
	case "BIGINT", "HUGEINT":
		return true
	default:
		return false
	}
}

func integerTypeWidth(value string) (string, int, bool) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "TINYINT", "INT8":
		return "TINYINT", 1, true
	case "SMALLINT", "INT16":
		return "SMALLINT", 2, true
	case "INTEGER", "INT", "INT32":
		return "INTEGER", 3, true
	case "BIGINT", "INT64":
		return "BIGINT", 4, true
	case "HUGEINT", "INT128":
		return "HUGEINT", 5, true
	default:
		return "", 0, false
	}
}

func underlyingColumnName(node any) string {
	mapNode, ok := node.(map[string]any)
	if !ok {
		return ""
	}
	if columnNode, ok := mapNode["column"].(map[string]any); ok {
		return polyglotIdentifierName(columnNode["name"])
	}
	return ""
}

func schemaColumnType(columnName string, sourceTables []string, schema Schema) string {
	for _, tableName := range sourceTables {
		for name, columnType := range schema[tableName] {
			if strings.EqualFold(name, columnName) {
				return columnType
			}
		}
	}
	return ""
}

// literalTypeName derives a SQL type from a literal projection node, used when
// the engine leaves a projection un-annotated (e.g. inside UNION branches).
func literalTypeName(node any) string {
	mapNode, ok := node.(map[string]any)
	if !ok {
		return ""
	}
	literal, ok := mapNode["literal"].(map[string]any)
	if !ok {
		return ""
	}
	switch literal["literal_type"] {
	case "number":
		if value, _ := literal["value"].(string); strings.Contains(value, ".") {
			return "DOUBLE"
		}
		return "INTEGER"
	case "string":
		return "VARCHAR"
	case "boolean":
		return "BOOLEAN"
	default:
		return ""
	}
}

func inferredTypeName(node any) string {
	mapNode, ok := node.(map[string]any)
	if !ok {
		return ""
	}
	dataType, _ := mapNode["data_type"].(string)
	return normalizeInferredType(dataType)
}

// normalizeInferredType maps the polyglot engine's snake_case DataType tokens to
// conventional SQL type names.
func normalizeInferredType(dataType string) string {
	switch strings.ToLower(strings.TrimSpace(dataType)) {
	case "":
		return ""
	case "int", "int32":
		return "INTEGER"
	case "bigint", "int64":
		return "BIGINT"
	case "smallint", "int16":
		return "SMALLINT"
	case "tinyint", "int8":
		return "TINYINT"
	case "var_char":
		return "VARCHAR"
	case "double":
		return "DOUBLE"
	case "float":
		return "FLOAT"
	case "boolean", "bool":
		return "BOOLEAN"
	case "timestamp_tz", "timestamptz":
		return "TIMESTAMPTZ"
	default:
		return strings.ToUpper(strings.ReplaceAll(dataType, "_", ""))
	}
}
