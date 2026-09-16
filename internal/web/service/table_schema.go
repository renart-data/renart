package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/bruin-data/bruin/pkg/query"
	"renart/internal/web/sqlnamespace"
)

// tableSchemaQuery reads database metadata without sampling table contents.
// Bruin v0.11.700's MySQL, StarRocks and Doris SelectWithSchema implementations
// omit ColumnTypes. Read COLUMN_TYPE as a value for these clients, preserving
// precision, length and complex types instead of guessing from returned rows.
func tableSchemaQuery(engine, table string) (sql string, metadataRows bool, err error) {
	engine = normalizeConnectionType(engine)
	reference, err := sqlnamespace.QuoteReference(engine, table)
	if err != nil {
		return "", false, err
	}
	switch engine {
	case "mysql", "vitess", "planetscale", "planetscale_mysql", "starrocks", "doris":
		parts, err := sqlnamespace.Parts(table)
		if err != nil {
			return "", false, err
		}
		maxParts := 2
		if engine == "starrocks" || engine == "doris" {
			maxParts = 3
		}
		if len(parts) > maxParts {
			return "", false, fmt.Errorf("invalid %s table name", engine)
		}
		// Backslash interpretation depends on SQL mode. Reject it rather than
		// changing the name or risking a different literal under that mode.
		for _, part := range parts {
			if strings.ContainsRune(part, '\\') {
				return "", false, fmt.Errorf("unsupported backslash in table identifier")
			}
		}
		literal := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
		catalog := ""
		if len(parts) == 3 {
			catalog = sqlnamespace.Quote(engine, parts[0]) + "."
		}
		database := "DATABASE()"
		if len(parts) >= 2 {
			database = literal(parts[len(parts)-2])
		}
		return "SELECT COLUMN_NAME, COLUMN_TYPE FROM " + catalog + "information_schema.columns WHERE TABLE_SCHEMA = " + database + " AND TABLE_NAME = " + literal(parts[len(parts)-1]) + " ORDER BY ORDINAL_POSITION", true, nil
	default:
		// WHERE works for SQL Server and Oracle too; LIMIT is not portable.
		return "SELECT * FROM " + reference + " WHERE 1 = 0", false, nil
	}
}

func selectTableSchema(ctx context.Context, querier directSchemaQuerier, engine, table string) (*query.QueryResult, error) {
	sql, metadataRows, err := tableSchemaQuery(engine, table)
	if err != nil {
		return nil, err
	}
	if normalizeConnectionType(engine) == "duckdb" {
		return selectDuckDBLogicalSchema(ctx, querier, sql)
	}
	result, err := querier.SelectWithSchema(ctx, &query.Query{Query: sql})
	if err != nil {
		return nil, err
	}
	if metadataRows {
		return tableSchemaFromMetadata(result)
	}
	return result, nil
}

func tableSchemaFromMetadata(metadata *query.QueryResult) (*query.QueryResult, error) {
	if metadata == nil {
		return nil, fmt.Errorf("database returned no column metadata")
	}
	nameIndex, typeIndex := -1, -1
	for i, name := range metadata.Columns {
		switch strings.ToLower(name) {
		case "column_name":
			nameIndex = i
		case "column_type":
			typeIndex = i
		}
	}
	if nameIndex < 0 || typeIndex < 0 {
		return nil, fmt.Errorf("column metadata did not include COLUMN_NAME and COLUMN_TYPE")
	}
	result := &query.QueryResult{Columns: []string{}, ColumnTypes: []string{}, Rows: [][]any{}}
	for _, row := range metadata.Rows {
		if nameIndex >= len(row) || typeIndex >= len(row) {
			return nil, fmt.Errorf("database returned incomplete column metadata")
		}
		name, columnType := querySchemaValue(row[nameIndex]), querySchemaValue(row[typeIndex])
		if name == "" || columnType == "" {
			return nil, fmt.Errorf("database returned a column without a name or type")
		}
		result.Columns = append(result.Columns, name)
		result.ColumnTypes = append(result.ColumnTypes, columnType)
	}
	if len(result.Columns) == 0 {
		return nil, fmt.Errorf("no visible columns found; check that the table exists and the connection can access its metadata")
	}
	return result, nil
}
