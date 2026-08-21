package sqlintelligence

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/renart-data/golyglot/pkg/golyglot"
)

func buildGolyglotSchema(schema Schema, constraintSets ...SchemaConstraints) golyglot.ValidationSchema {
	constraints := firstSchemaConstraints(constraintSets)
	tableNames := make([]string, 0, len(schema))
	for tableName := range schema {
		tableNames = append(tableNames, tableName)
	}
	sort.Strings(tableNames)

	result := golyglot.ValidationSchema{Tables: make([]golyglot.SchemaTable, 0, len(tableNames))}
	for _, tableName := range tableNames {
		columns := schema[tableName]
		columnNames := make([]string, 0, len(columns))
		for columnName := range columns {
			columnNames = append(columnNames, columnName)
		}
		sort.Strings(columnNames)

		tableConstraints := constraintsForTable(constraints, tableName)
		table := golyglot.SchemaTable{Name: tableName, Columns: make([]golyglot.SchemaColumn, 0, len(columnNames))}
		for _, columnName := range columnNames {
			column := golyglot.SchemaColumn{Name: columnName, Type: columns[columnName]}
			if metadata, ok := constraintsForColumn(tableConstraints.Columns, columnName); ok {
				column.Nullable = metadata.Nullable
				column.PrimaryKey = metadata.PrimaryKey
				if metadata.PrimaryKey {
					table.PrimaryKey = append(table.PrimaryKey, columnName)
				}
				if metadata.ForeignKey != nil {
					column.References = &golyglot.SchemaColumnReference{Table: metadata.ForeignKey.Table, Column: metadata.ForeignKey.Column}
					table.ForeignKeys = append(table.ForeignKeys, golyglot.SchemaForeignKey{
						Columns: []string{columnName},
						References: golyglot.SchemaTableReference{
							Table:   metadata.ForeignKey.Table,
							Columns: []string{metadata.ForeignKey.Column},
						},
					})
				}
			}
			table.Columns = append(table.Columns, column)
		}
		result.Tables = append(result.Tables, table)
	}
	return result
}

// buildPolyglotSchema makes the map-backed Renart schema deterministic before
// it crosses the WASM boundary. Stable ordering is required for analysis cache
// keys and also keeps identical requests byte-for-byte identical.
func buildPolyglotSchema(schema Schema, constraintSets ...SchemaConstraints) polyglotSchema {
	constraints := firstSchemaConstraints(constraintSets)
	tableNames := make([]string, 0, len(schema))
	for tableName := range schema {
		tableNames = append(tableNames, tableName)
	}
	sort.Strings(tableNames)

	result := polyglotSchema{Tables: make([]polyglotSchemaTable, 0, len(tableNames))}
	for _, tableName := range tableNames {
		columns := schema[tableName]
		columnNames := make([]string, 0, len(columns))
		for columnName := range columns {
			columnNames = append(columnNames, columnName)
		}
		sort.Strings(columnNames)

		tableConstraints := constraintsForTable(constraints, tableName)
		table := polyglotSchemaTable{Name: tableName, Columns: make([]polyglotSchemaColumn, 0, len(columnNames))}
		for _, columnName := range columnNames {
			column := polyglotSchemaColumn{Name: columnName, Type: columns[columnName]}
			if metadata, ok := constraintsForColumn(tableConstraints.Columns, columnName); ok {
				column.Nullable = metadata.Nullable
				column.PrimaryKey = metadata.PrimaryKey
				if metadata.PrimaryKey {
					table.PrimaryKey = append(table.PrimaryKey, columnName)
				}
				if metadata.ForeignKey != nil {
					reference := polyglotColumnReference{Table: metadata.ForeignKey.Table, Column: metadata.ForeignKey.Column}
					column.References = &reference
					table.ForeignKeys = append(table.ForeignKeys, polyglotTableForeignKey{
						Columns: []string{columnName},
						References: polyglotTableForeignKeyTarget{
							Table:   metadata.ForeignKey.Table,
							Columns: []string{metadata.ForeignKey.Column},
						},
					})
				}
			}
			table.Columns = append(table.Columns, column)
		}
		result.Tables = append(result.Tables, table)
	}
	return result
}

func marshalPolyglotSchema(schema Schema, constraints ...SchemaConstraints) ([]byte, error) {
	return json.Marshal(buildPolyglotSchema(schema, constraints...))
}

func firstSchemaConstraints(values []SchemaConstraints) SchemaConstraints {
	if len(values) == 0 {
		return nil
	}
	return values[0]
}

func constraintsForTable(constraints SchemaConstraints, name string) SchemaTableConstraints {
	for candidate, table := range constraints {
		if equalSchemaIdentifier(candidate, name) {
			return table
		}
	}
	return SchemaTableConstraints{}
}

func constraintsForColumn(columns map[string]SchemaColumnConstraints, name string) (SchemaColumnConstraints, bool) {
	for candidate, column := range columns {
		if equalSchemaIdentifier(candidate, name) {
			return column, true
		}
	}
	return SchemaColumnConstraints{}, false
}

func equalSchemaIdentifier(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}
