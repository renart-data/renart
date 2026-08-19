package notebook

import (
	"strings"
	"testing"
)

func TestConfigureSQLSourcePreservesBodyAndRoundTripsPolicy(t *testing.T) {
	const body = "select *\nfrom public.orders\nwhere note = 'keep: this'\n"
	original := "/* @bruin\nid: cell01\ntype: duckdb.sql\nclass: notebook\nowner: data\n@bruin */\n\n" + body
	configured, err := ConfigureSQLSource(original, "cell01", SQLSourceConfig{
		Connection: "postgres-other", AssetType: "pg.sql",
		SnapshotMode: SnapshotModeSample, RowLimit: 5000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(configured, body) {
		t.Fatalf("SQL body changed:\n%s", configured)
	}
	for _, expected := range []string{
		"type: pg.sql", "connection: postgres-other", "owner: data",
		SnapshotModeMetaKey + ": sample", SnapshotRowLimitMetaKey + ": \"5000\"",
	} {
		if !strings.Contains(configured, expected) {
			t.Fatalf("missing %q:\n%s", expected, configured)
		}
	}

	local, err := ConfigureSQLSource(configured, "cell01", SQLSourceConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(local, "type: duckdb.sql") || strings.Contains(local, "connection:") || strings.Contains(local, SnapshotModeMetaKey) {
		t.Fatalf("source metadata was not removed cleanly:\n%s", local)
	}
	if !strings.HasSuffix(local, body) {
		t.Fatalf("SQL body changed while returning local:\n%s", local)
	}
}
