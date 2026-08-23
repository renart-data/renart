package sqlintelligence

import (
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
