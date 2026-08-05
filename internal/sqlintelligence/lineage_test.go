package sqlintelligence

import (
	"context"
	"testing"
)

func TestAnnotateOutputColumns(t *testing.T) {
	ctx := context.Background()
	schema := Schema{
		"analytics.customers": {
			"customer_id":   "INTEGER",
			"customer_name": "VARCHAR",
		},
	}

	got, err := AnnotateOutputColumns(
		ctx,
		"select customer_id, customer_name as full_name, upper(customer_name) as shout, 1 as const, customer_id + 1 as next_id from analytics.customers",
		"duckdb",
		schema,
	)
	if err != nil {
		t.Fatalf("AnnotateOutputColumns: %v", err)
	}

	byName := map[string]string{}
	for _, c := range got {
		byName[c.Name] = c.Type
	}

	// bare column resolved from upstream asset schema
	if byName["customer_id"] != "INTEGER" {
		t.Errorf("customer_id type = %q, want INTEGER", byName["customer_id"])
	}
	// aliased bare column resolved from schema
	if byName["full_name"] != "VARCHAR" {
		t.Errorf("full_name type = %q, want VARCHAR", byName["full_name"])
	}
	// computed expression types come from inferred_type
	if byName["shout"] != "VARCHAR" {
		t.Errorf("shout type = %q, want VARCHAR", byName["shout"])
	}
	if byName["const"] != "INTEGER" {
		t.Errorf("const type = %q, want INTEGER", byName["const"])
	}
	if byName["next_id"] != "INTEGER" {
		t.Errorf("next_id type = %q, want INTEGER", byName["next_id"])
	}
}

func TestAnnotateOutputColumnsStarExpansion(t *testing.T) {
	ctx := context.Background()
	schema := Schema{
		"analytics.customers": {
			"customer_id":   "INTEGER",
			"customer_name": "VARCHAR",
		},
	}
	got, err := AnnotateOutputColumns(ctx, "select * from analytics.customers", "duckdb", schema)
	if err != nil {
		t.Fatalf("AnnotateOutputColumns: %v", err)
	}
	byName := map[string]string{}
	for _, c := range got {
		byName[c.Name] = c.Type
	}
	if byName["customer_id"] != "INTEGER" || byName["customer_name"] != "VARCHAR" {
		t.Fatalf("star expansion did not carry schema types: %+v", got)
	}
}

func TestAnnotateOutputColumnsDuckDBRangeTableFunction(t *testing.T) {
	got, err := AnnotateOutputColumns(
		context.Background(),
		"select range as my_value from range(1, 2, 1)",
		"duckdb",
		Schema{},
	)
	if err != nil {
		t.Fatalf("AnnotateOutputColumns: %v", err)
	}
	if len(got) != 1 || got[0].Name != "my_value" || got[0].Type != "BIGINT" {
		t.Fatalf("range projection = %+v, want my_value BIGINT", got)
	}
}

func TestAnnotateOutputColumnsDuckDBRangeArithmeticKeepsBigInt(t *testing.T) {
	got, err := AnnotateOutputColumns(
		context.Background(),
		"select range, range * 2 as double_range from range(1, 2, 1)",
		"duckdb",
		Schema{},
	)
	if err != nil {
		t.Fatalf("AnnotateOutputColumns: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("range arithmetic projection = %+v, want two columns", got)
	}
	if got[0].Name != "range" || got[0].Type != "BIGINT" {
		t.Fatalf("range column = %+v, want range BIGINT", got[0])
	}
	if got[1].Name != "double_range" || got[1].Type != "BIGINT" {
		t.Fatalf("range arithmetic = %+v, want double_range BIGINT", got[1])
	}
}

func TestAnnotateOutputColumnsDuckDBIntegerLiteralDoesNotWidenTypedColumn(t *testing.T) {
	for _, test := range []struct {
		literal string
		want    string
	}{
		{literal: "2", want: "TINYINT"},
		{literal: "128", want: "INTEGER"},
	} {
		got, err := AnnotateOutputColumns(
			context.Background(),
			"select value * "+test.literal+" as doubled from input",
			"duckdb",
			Schema{"input": {"value": "TINYINT"}},
		)
		if err != nil {
			t.Fatalf("AnnotateOutputColumns(%s): %v", test.literal, err)
		}
		if len(got) != 1 || got[0].Name != "doubled" || got[0].Type != test.want {
			t.Fatalf("typed integer arithmetic with %s = %+v, want doubled %s", test.literal, got, test.want)
		}
	}
}

func TestAnnotateOutputColumnsDuckDBGenerateSeriesTableFunction(t *testing.T) {
	got, err := AnnotateOutputColumns(
		context.Background(),
		"select generate_series as my_value from generate_series(1, 2, 1)",
		"duckdb",
		Schema{},
	)
	if err != nil {
		t.Fatalf("AnnotateOutputColumns: %v", err)
	}
	if len(got) != 1 || got[0].Name != "my_value" || got[0].Type != "BIGINT" {
		t.Fatalf("generate_series projection = %+v, want my_value BIGINT", got)
	}
}
