package sqlintelligence

import "testing"

func TestQuerySemanticIdentityIgnoresPresentationFormatting(t *testing.T) {
	before, err := QuerySemanticIdentity(
		"SELECT SUM(total_amount) AS total FROM lineitems",
		"duckdb",
	)
	if err != nil {
		t.Fatal(err)
	}
	after, err := QuerySemanticIdentity(
		"-- presentation only\nselect\n  sum( total_amount ) as total\nfrom lineitems;",
		"duckdb",
	)
	if err != nil {
		t.Fatal(err)
	}
	if before.SourceFingerprint == after.SourceFingerprint {
		t.Fatal("source fingerprint ignored formatting")
	}
	if before.CanonicalFingerprint != after.CanonicalFingerprint {
		t.Fatalf("canonical identity changed: %q != %q", before.CanonicalFingerprint, after.CanonicalFingerprint)
	}
}

func TestQuerySemanticIdentityRetainsDirectivesAndStringLiterals(t *testing.T) {
	plain, err := QuerySemanticIdentity("SELECT '-- not a comment' AS marker", "duckdb")
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := QuerySemanticIdentity("select\n '-- not a comment' as marker;", "duckdb")
	if err != nil {
		t.Fatal(err)
	}
	if plain.CanonicalFingerprint != formatted.CanonicalFingerprint {
		t.Fatal("comment marker inside a string changed canonical identity")
	}
	hinted, err := QuerySemanticIdentity("SELECT /*+ FORCE_INDEX(lineitems) */ '-- not a comment' AS marker", "duckdb")
	if err != nil {
		t.Fatal(err)
	}
	if plain.CanonicalFingerprint == hinted.CanonicalFingerprint {
		t.Fatal("optimizer directive was treated as presentation formatting")
	}
}
