package assetmeta

import (
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
)

func col(name, typ string) pipeline.Column {
	return pipeline.Column{Name: name, Type: typ}
}

func columnNames(cols []pipeline.Column) []string {
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		out = append(out, c.Name)
	}
	return out
}

func findColumn(cols []pipeline.Column, name string) (pipeline.Column, bool) {
	for _, c := range cols {
		if c.Name == name {
			return c, true
		}
	}
	return pipeline.Column{}, false
}

func TestReconcileColumnsPreservesUserMetadata(t *testing.T) {
	// order_id has a description + check; inference only knows name+type.
	current := []pipeline.Column{
		{Name: "order_id", Type: "integer", Description: "the order", PrimaryKey: true,
			Checks: []pipeline.ColumnCheck{{Name: "not_null"}}},
	}
	inferred := []pipeline.Column{col("order_id", "bigint")}

	final, items, _ := ReconcileColumns(ColumnReconcileInput{
		Inferred: inferred,
		Current:  current,
		Prev:     RenartMeta{},
	})
	if len(items) != 0 {
		t.Fatalf("unexpected reconcile items: %+v", items)
	}
	got, ok := findColumn(final, "order_id")
	if !ok {
		t.Fatalf("order_id missing")
	}
	if got.Description != "the order" || !got.PrimaryKey || len(got.Checks) != 1 {
		t.Fatalf("user metadata lost: %+v", got)
	}
	// type is not owned → refreshed from inference
	if got.Type != "bigint" {
		t.Fatalf("expected inferred type bigint, got %q", got.Type)
	}
}

func TestReconcileColumnsTypeOwnership(t *testing.T) {
	current := []pipeline.Column{col("order_total", "numeric")}
	inferred := []pipeline.Column{col("order_total", "integer")}

	// Without ownership, inference owns the type and refreshes it.
	unowned, _, _ := ReconcileColumns(ColumnReconcileInput{
		Inferred: inferred,
		Current:  current,
		Prev:     RenartMeta{},
	})
	if got, _ := findColumn(unowned, "order_total"); got.Type != "integer" {
		t.Fatalf("unowned type not refreshed from inference: %q", got.Type)
	}

	// With explicit ownership (set by a column.field.own transaction), the
	// user's type is preserved across reconciliation.
	owned, _, next := ReconcileColumns(ColumnReconcileInput{
		Inferred: inferred,
		Current:  current,
		Prev:     RenartMeta{ColOwn: map[string][]string{"order_total": {"type"}}},
	})
	if got, _ := findColumn(owned, "order_total"); got.Type != "numeric" {
		t.Fatalf("owned type not preserved: %q", got.Type)
	}
	if fields := next.ColOwn["order_total"]; len(fields) != 1 || fields[0] != "type" {
		t.Fatalf("ownership not carried forward: %+v", next.ColOwn)
	}
}

func TestReconcileColumnsPreservesKnownAlternateSourceTypeWhenDefinitionIsUnknown(t *testing.T) {
	current := []pipeline.Column{col("order_total", "numeric")}
	inferred := []pipeline.Column{col("order_total", "")}

	final, _, next := ReconcileColumns(ColumnReconcileInput{
		Inferred: inferred,
		Current:  current,
		Prev:     RenartMeta{ColSource: map[string]string{"order_total": "m"}},
	})
	if got, _ := findColumn(final, "order_total"); got.Type != "numeric" {
		t.Fatalf("materialized type was erased by unknown definition type: %q", got.Type)
	}
	if next.ColSource["order_total"] != "m" {
		t.Fatalf("alternate source provenance not carried forward: %+v", next.ColSource)
	}
}

func TestReconcileColumnsStaleColumnFlagged(t *testing.T) {
	// debug_rank had a description but is no longer inferred → stale, kept.
	current := []pipeline.Column{
		col("order_id", "integer"),
		{Name: "debug_rank", Type: "integer", Description: "scratch"},
	}
	inferred := []pipeline.Column{col("order_id", "integer")}

	final, items, _ := ReconcileColumns(ColumnReconcileInput{
		Inferred: inferred,
		Current:  current,
		Prev:     RenartMeta{},
	})
	if len(items) != 1 || items[0].Column != "debug_rank" || items[0].Kind != "column.stale" {
		t.Fatalf("expected one stale item for debug_rank: %+v", items)
	}
	if _, ok := findColumn(final, "debug_rank"); !ok {
		t.Fatalf("stale column was destroyed: %v", columnNames(final))
	}
}

func TestReconcileColumnsPreservesAllBruinColumnMetadata(t *testing.T) {
	precision := 10
	current := []pipeline.Column{{
		Name: "amount", Type: "decimal", Precision: &precision,
		Meta: pipeline.EmptyStringMap{"semantic_type": "currency"},
	}}

	final, items, _ := ReconcileColumns(ColumnReconcileInput{Current: current})
	if len(items) != 1 || items[0].Column != "amount" {
		t.Fatalf("column metadata should make a missing generated column stale: %#v", items)
	}
	if len(final) != 1 || final[0].Precision == nil || final[0].Meta["semantic_type"] != "currency" {
		t.Fatalf("Bruin column metadata was dropped: %#v", final)
	}
}

func TestReconcileColumnsDropsObsoleteGenerated(t *testing.T) {
	// A generated column with no user metadata that is no longer inferred is
	// silently dropped (no reconcile item).
	current := []pipeline.Column{col("order_id", "integer"), col("tmp_helper", "integer")}
	inferred := []pipeline.Column{col("order_id", "integer")}

	final, items, _ := ReconcileColumns(ColumnReconcileInput{
		Inferred: inferred,
		Current:  current,
		Prev:     RenartMeta{},
	})
	if len(items) != 0 {
		t.Fatalf("unexpected items: %+v", items)
	}
	if _, ok := findColumn(final, "tmp_helper"); ok {
		t.Fatalf("obsolete generated column not dropped: %v", columnNames(final))
	}
}

func TestReconcileColumnsManualColumnKept(t *testing.T) {
	// loaded_at is a manual column (in c.add), not inferred — must survive.
	current := []pipeline.Column{col("order_id", "integer"), col("loaded_at", "timestamp")}
	inferred := []pipeline.Column{col("order_id", "integer")}
	prev := RenartMeta{ColAdd: []string{"loaded_at"}}

	final, items, next := ReconcileColumns(ColumnReconcileInput{
		Inferred: inferred,
		Current:  current,
		Prev:     prev,
	})
	if len(items) != 0 {
		t.Fatalf("manual column should not be stale: %+v", items)
	}
	if _, ok := findColumn(final, "loaded_at"); !ok {
		t.Fatalf("manual column dropped: %v", columnNames(final))
	}
	if len(next.ColAdd) != 1 || next.ColAdd[0] != "loaded_at" {
		t.Fatalf("manual column not recorded in c.add: %+v", next.ColAdd)
	}
}

func TestReconcileColumnsDropExcludesInferred(t *testing.T) {
	// User dropped the inferred debug column; it must not be emitted.
	current := []pipeline.Column{col("order_id", "integer")}
	inferred := []pipeline.Column{col("order_id", "integer"), col("debug", "integer")}
	prev := RenartMeta{ColDrop: []string{"debug"}}

	final, _, next := ReconcileColumns(ColumnReconcileInput{
		Inferred: inferred,
		Current:  current,
		Prev:     prev,
	})
	if _, ok := findColumn(final, "debug"); ok {
		t.Fatalf("dropped inferred column resurfaced: %v", columnNames(final))
	}
	if len(next.ColDrop) != 1 {
		t.Fatalf("drop not retained while inferred: %+v", next.ColDrop)
	}
}

func TestColumnProjectionHashStable(t *testing.T) {
	a := ColumnProjectionHash([]pipeline.Column{col("a", "int"), col("b", "text")})
	b := ColumnProjectionHash([]pipeline.Column{col("b", "text"), col("a", "int")})
	if a != b {
		t.Fatalf("hash not order-independent")
	}
	if a == ColumnProjectionHash([]pipeline.Column{col("a", "bigint"), col("b", "text")}) {
		t.Fatalf("hash ignored a type change")
	}
}
