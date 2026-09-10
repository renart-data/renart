package service

import (
	"context"
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/config"
)

func TestSQLPreviewGuardsEveryRequestAndPreservesAuthoredLimit(t *testing.T) {
	queries := []string{}
	svc := NewSQLService(SQLDependencies{
		NewConnectionManager: func(context.Context, string) (config.ConnectionAndDetailsGetter, error) {
			return &stubConnectionManager{connectionType: "duckdb"}, nil
		},
		RunConnectionQuery: func(_ context.Context, _, _, q string) ([]string, []map[string]any, error) {
			queries = append(queries, q)
			return []string{"id"}, []map[string]any{{"id": 1}, {"id": 2}, {"id": 3}}, nil
		},
	})
	for _, query := range []string{"delete from data", "create table data as select 1", "select 1; select 2", "select * into other from data", "with removed as (delete from data returning *) select * from removed"} {
		if result := svc.Preview(context.Background(), "db", "dev", query, 2); result.Status != "error" {
			t.Fatalf("accepted: %s", query)
		}
	}
	if len(queries) != 0 {
		t.Fatalf("executed unsafe SQL: %v", queries)
	}
	result := svc.Query(context.Background(), "db", "dev", "select * from data limit 7; -- end", 2)
	if result.Status != "ok" || result.Preview == nil || result.Preview.NextLimit == 0 {
		t.Fatalf("result: %+v", result)
	}
	if len(queries) != 1 || !strings.Contains(queries[0], "limit 7") || !strings.Contains(queries[0], "LIMIT 3") {
		t.Fatalf("query: %v", queries)
	}
}

func TestSQLServerPreviewUsesNativeTopAndDoesNotRewriteAuthoredBounds(t *testing.T) {
	var executed string
	svc := NewSQLService(SQLDependencies{
		NewConnectionManager: func(context.Context, string) (config.ConnectionAndDetailsGetter, error) {
			return &stubConnectionManager{connectionType: "mssql"}, nil
		},
		RunConnectionQuery: func(_ context.Context, _, _, q string) ([]string, []map[string]any, error) {
			executed = q
			return []string{"value"}, []map[string]any{{"value": 1}, {"value": 2}, {"value": 3}}, nil
		},
	})
	query := "with data as (select 1 as value) select value from data order by value"
	result := svc.Preview(context.Background(), "db", "dev", query, 2)
	if result.Status != "ok" || !strings.Contains(executed, "select TOP (3) value from data order by value") {
		t.Fatalf("native TOP: %+v %q", result, executed)
	}
	bounded := "select top (7) value from data order by value"
	result = svc.Query(context.Background(), "db", "dev", bounded, 2)
	if executed != bounded || result.Preview.Continuation != "none" || result.Preview.Reason != "unsupported" {
		t.Fatalf("authored TOP: %+v %q", result, executed)
	}
	executed = ""
	if result := svc.Preview(context.Background(), "db", "dev", bounded, 4); result.Status != "error" || executed != "" {
		t.Fatalf("unsafe rewrite: %+v %q", result, executed)
	}
}
