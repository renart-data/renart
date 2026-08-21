package sqlintelligence

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func BenchmarkGolyglotOutputAnalysis(b *testing.B) {
	type benchmarkCase struct {
		name   string
		query  string
		schema Schema
	}

	wideColumns := make(map[string]string, 50)
	wideNames := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		name := fmt.Sprintf("column_%02d", i)
		wideNames = append(wideNames, name)
		wideColumns[name] = "DECIMAL(18,2)"
	}
	cases := []benchmarkCase{
		{
			name:   "short",
			query:  "select id, amount + 1 as gross from orders",
			schema: Schema{"orders": {"id": "BIGINT", "amount": "DECIMAL(10,2)"}},
		},
		{
			name: "cte-heavy",
			query: `with base as (select id, amount from orders),
filtered as (select * from base where amount > 0),
aggregated as (select id, sum(amount) as total from filtered group by id)
select id, total, total / 2 as half_total from aggregated`,
			schema: Schema{"orders": {"id": "BIGINT", "amount": "DECIMAL(10,2)"}},
		},
		{
			name:   "wide-schema",
			query:  "select " + strings.Join(wideNames, ", ") + " from wide_table",
			schema: Schema{"wide_table": wideColumns},
		},
	}

	for _, benchmark := range cases {
		optionsJSON, err := marshalAnalyzeQueryOptions("duckdb", benchmark.schema)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := analyzeQueryUncached(context.Background(), benchmark.query, optionsJSON, benchmark.schema); err != nil {
			b.Fatal(err)
		}
		if _, _, err := annotateOutputColumns(context.Background(), benchmark.query, "duckdb", benchmark.schema); err != nil {
			b.Fatal(err)
		}

		b.Run(benchmark.name+"/analyze_query", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := analyzeQueryUncached(context.Background(), benchmark.query, optionsJSON, benchmark.schema); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(benchmark.name+"/annotate_types", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, _, err := annotateOutputColumns(context.Background(), benchmark.query, "duckdb", benchmark.schema); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
