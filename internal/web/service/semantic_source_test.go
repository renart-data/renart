package service

import (
	"testing"

	"renart/internal/sqlintelligence"
)

func TestSemanticSourceAnchors(t *testing.T) {
	source := "/* @bruin\nname: revenue\n@bruin */\nWITH x AS (SELECT 1 AS amount)\nSELECT '😀' AS label,\n  SUM(amount) AS total FROM x"
	identity, err := sqlintelligence.QuerySemanticIdentity(source, "duckdb")
	if err != nil {
		t.Fatal(err)
	}
	anchors := semanticSourceAnchors(source, "duckdb", identity.CanonicalFingerprint, 2)
	if anchors == nil || len(anchors.Projections) != 2 {
		t.Fatalf("anchors = %#v", anchors)
	}
	if got := anchors.Projections[1]; got.Line != 6 || got.Column != 3 || got.EndColumn != 23 {
		t.Fatalf("SUM projection = %#v", got)
	}
	if got := anchors.Projections[0]; got.Line != 5 || got.Column != 8 || got.EndColumn != 21 {
		t.Fatalf("UTF-16 projection = %#v", got)
	}
	if anchors.Fingerprint == "" {
		t.Fatal("missing source identity")
	}
	if semanticSourceAnchors(source, "duckdb", "different", 2) != nil {
		t.Fatal("mapped a different rendered query")
	}
	if semanticSourceAnchors("SELECT {{ value }}", "duckdb", identity.CanonicalFingerprint, 1) != nil {
		t.Fatal("mapped unresolved template")
	}
	if semanticSourceAnchors("SELECT 1; SELECT 2", "duckdb", identity.CanonicalFingerprint, 1) != nil {
		t.Fatal("mapped multiple statements")
	}
}

func TestSemanticSourceAnchorsDoNotGuessExpandedOrSetProjectionOrdinals(t *testing.T) {
	for _, source := range []string{"SELECT * FROM x", "SELECT x.* FROM x", "SELECT 1 UNION ALL SELECT 2", "SELECT 1, 2"} {
		identity, err := sqlintelligence.QuerySemanticIdentity(source, "duckdb")
		if err != nil {
			t.Fatal(err)
		}
		anchors := semanticSourceAnchors(source, "duckdb", identity.CanonicalFingerprint, 1)
		if anchors == nil || len(anchors.Projections) != 0 {
			t.Fatalf("unsafe projection mapping for %q: %#v", source, anchors)
		}
	}
}
