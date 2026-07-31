package main

import (
	"testing"

	"github.com/apache/arrow-go/v18/arrow/decimal128"
	"github.com/databricks/databricks-sql-go/internal/cli_service"
)

func TestRenderStatementParameters(t *testing.T) {
	t.Parallel()

	integerType := "BIGINT"
	integerValue := "42"
	stringType := "STRING"
	stringValue := "O'Reilly"
	boolType := "BOOLEAN"
	boolValue := true
	statement := "select '?' as literal, ? as id, \"?\" as quoted, `?` as named, ? as label, ? as enabled -- ?\n/* ? */"

	got, err := renderStatementParameters(statement, []*cli_service.TSparkParameter{
		{Type: &integerType, Value: &cli_service.TSparkParameterValue{StringValue: &integerValue}},
		{Type: &stringType, Value: &cli_service.TSparkParameterValue{StringValue: &stringValue}},
		{Type: &boolType, Value: &cli_service.TSparkParameterValue{BooleanValue: &boolValue}},
	})
	if err != nil {
		t.Fatalf("render parameters: %v", err)
	}
	want := "select '?' as literal, 42 as id, \"?\" as quoted, `?` as named, 'O''Reilly' as label, TRUE as enabled -- ?\n/* ? */"
	if got != want {
		t.Fatalf("unexpected rendered statement\nwant: %s\n got: %s", want, got)
	}
}

func TestRenderStatementParametersRejectsCountMismatch(t *testing.T) {
	t.Parallel()

	valueType := "STRING"
	value := "extra"
	if _, err := renderStatementParameters("select 1", []*cli_service.TSparkParameter{{
		Type:  &valueType,
		Value: &cli_service.TSparkParameterValue{StringValue: &value},
	}}); err == nil {
		t.Fatal("expected an unused-parameter error")
	}
	if _, err := renderStatementParameters("select ?, ?", []*cli_service.TSparkParameter{{
		Type:  &valueType,
		Value: &cli_service.TSparkParameterValue{StringValue: &value},
	}}); err == nil {
		t.Fatal("expected a missing-parameter error")
	}
}

func TestTemporaryRelationName(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"analytics.orders":            "analytics.orders__rewrite",
		"`analytics`.`orders`":        "`analytics`.`orders__rewrite`",
		"`sail`.`analytics`.`orders`": "`sail`.`analytics`.`orders__rewrite`",
	}
	for input, expected := range tests {
		actual, err := temporaryRelationName(input, "__rewrite")
		if err != nil {
			t.Fatalf("temporary name for %q: %v", input, err)
		}
		if actual != expected {
			t.Fatalf("temporary name for %q: want %q, got %q", input, expected, actual)
		}
	}
}

func TestIntegerResultColumnsUseMatchingThriftWidths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind  columnKind
		valid func(*cli_service.TColumn) bool
	}{
		{kind: columnI8, valid: func(column *cli_service.TColumn) bool { return column.ByteVal != nil }},
		{kind: columnI16, valid: func(column *cli_service.TColumn) bool { return column.I16Val != nil }},
		{kind: columnI32, valid: func(column *cli_service.TColumn) bool { return column.I32Val != nil }},
		{kind: columnI64, valid: func(column *cli_service.TColumn) bool { return column.I64Val != nil }},
	}
	for _, test := range tests {
		column, err := (resultColumn{kind: test.kind, values: []any{1, nil}}).thriftColumn()
		if err != nil {
			t.Fatalf("encode integer kind %d: %v", test.kind, err)
		}
		if !test.valid(column) {
			t.Fatalf("integer kind %d used the wrong Thrift column union", test.kind)
		}
	}
}

func TestDecimalResultColumnUsesScale(t *testing.T) {
	t.Parallel()

	number, err := decimal128.FromString("2", 38, 6)
	if err != nil {
		t.Fatalf("create decimal: %v", err)
	}
	operation, err := buildQueryOperation([]resultColumn{{
		name:             "multiplier",
		kind:             columnString,
		typeID:           cli_service.TTypeId_DECIMAL_TYPE,
		decimalPrecision: 38,
		decimalScale:     6,
		values:           []any{number},
	}})
	if err != nil {
		t.Fatalf("build decimal result: %v", err)
	}
	if actual := operation.columns[0].StringVal.Values[0]; actual != "2.000000" {
		t.Fatalf("unexpected decimal value %q", actual)
	}
	qualifiers := operation.schema.Columns[0].TypeDesc.Types[0].PrimitiveEntry.TypeQualifiers.Qualifiers
	if qualifiers["precision"].GetI32Value() != 38 || qualifiers["scale"].GetI32Value() != 6 {
		t.Fatalf("unexpected decimal qualifiers: %#v", qualifiers)
	}
}
