package sqlintelligence

import (
	"context"
	"strings"
)

// AnnotateOutputColumns derives a SELECT result schema from the query and its
// upstream asset declarations without querying a warehouse.
func AnnotateOutputColumns(ctx context.Context, query, dialect string, schema Schema) ([]SchemaColumn, error) {
	inference, err := InferOutputSchema(ctx, query, dialect, schema)
	return inference.Columns, err
}

type OutputSchemaInference struct {
	Columns       []SchemaColumn
	NamesComplete bool
}

// InferOutputSchema exposes whether every output name could be resolved. The
// native Golyglot analysis expands known stars and follows CTEs/subqueries; an
// unknown wildcard keeps NamesComplete false so callers do not emit speculative
// declaration drift warnings.
func InferOutputSchema(ctx context.Context, query, dialect string, schema Schema) (OutputSchemaInference, error) {
	columns, complete, err := annotateOutputColumns(ctx, query, dialect, schema)
	return OutputSchemaInference{Columns: columns, NamesComplete: complete}, err
}

func annotateOutputColumns(ctx context.Context, query, dialect string, schema Schema) ([]SchemaColumn, bool, error) {
	if strings.TrimSpace(query) == "" {
		return nil, false, nil
	}
	analysis, err := AnalyzeQuery(ctx, query, dialect, schema)
	if err != nil {
		return nil, false, err
	}
	return append([]SchemaColumn(nil), analysis.OutputColumns...), analysis.OutputNamesComplete, nil
}

// normalizeInferredType maps parser-internal or engine-native aliases to the
// conventional SQL spellings used by Renart's schema UI and comparison code.
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
