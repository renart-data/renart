package notebook

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
)

// singleCellNotebook builds a one-cell notebook with the given cell content
// (frontmatter is added automatically).
func singleCellNotebook(t *testing.T, body string) *Notebook {
	t.Helper()
	fs := afero.NewMemMapFs()
	dir := "/ws/notebooks/viz"
	if err := afero.WriteFile(fs, filepath.Join(dir, ManifestFileName), []byte("id: 22222222-0000-0000-0000-000000000001\nblocks:\n  - cell: vizcell1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content := "/* @bruin\nid: vizcell1\ntype: duckdb.sql\n@bruin */\n" + body + "\n"
	if err := afero.WriteFile(fs, filepath.Join(dir, "only.sql"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	nb, err := NewLoader(fs, pipeline.CreateTaskFromFileComments(fs), fakeUsedTables).Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return nb
}

func TestParseVizValidKinds(t *testing.T) {
	cases := map[string]struct {
		content string
		kind    string
		options map[string]any
	}{
		"table": {"-- @viz(table, limit: 50, columns: [a, b])\nselect 1", "table", map[string]any{"limit": float64(50), "columns": []string{"a", "b"}}},
		"bar":   {"-- @viz(bar, x: month, y: revenue, stacked: true)\nselect 1", "bar", map[string]any{"x": "month", "y": "revenue", "stacked": true}},
		"line":  {"-- @viz(line, x: day, y: [actual, forecast])\nselect 1", "line", map[string]any{"x": "day", "y": []string{"actual", "forecast"}}},
		"kpi":   {"-- @viz(kpi, value: total, format: 'currency')\nselect 1", "kpi", map[string]any{"value": "total", "format": "currency"}},
		// Block-comment form (what the SQL formatter produces), including the
		// inline case where the directive shares a line with SQL containing
		// its own parentheses.
		"block":  {"/* @viz(bar, x: month, y: revenue) */\nselect 1", "bar", map[string]any{"x": "month", "y": "revenue"}},
		"inline": {"/* @viz(bar, x: month, y: revenue) */ select count(*) as revenue, month from t", "bar", map[string]any{"x": "month", "y": "revenue"}},
	}
	for name, testCase := range cases {
		directive, diagnostics := ParseViz(testCase.content)
		if directive == nil {
			t.Fatalf("%s: expected directive, got nil (diagnostics %v)", name, diagnostics)
		}
		for _, diagnostic := range diagnostics {
			if diagnostic.Severity == "error" {
				t.Fatalf("%s: unexpected error diagnostic: %v", name, diagnostic)
			}
		}
		if directive.Kind != testCase.kind {
			t.Fatalf("%s: kind = %q", name, directive.Kind)
		}
		if !reflect.DeepEqual(directive.Options, testCase.options) {
			t.Fatalf("%s: options = %#v, want %#v", name, directive.Options, testCase.options)
		}
	}
}

func TestParseVizDiagnostics(t *testing.T) {
	cases := []struct {
		content      string
		wantNil      bool
		wantSeverity string
		wantContains string
	}{
		{"-- @viz(bar, y: revenue)\nselect 1", false, "error", "require"}, // missing x
		{"-- @viz(donut, x: a, y: b)\nselect 1", true, "error", "unknown chart kind"},
		{"-- @viz(bar, x: a, y: b, color: red)\nselect 1", false, "warning", "unknown key"},
		{"-- @viz(line, x: a, y: b)\n-- @viz(bar, x: a, y: b)\nselect 1", false, "warning", "duplicate"},
		{"-- @viz()\nselect 1", true, "error", "requires a kind"},
	}
	for _, testCase := range cases {
		directive, diagnostics := ParseViz(testCase.content)
		if testCase.wantNil && directive != nil {
			t.Fatalf("%q: expected nil directive", testCase.content)
		}
		if !testCase.wantNil && directive == nil {
			t.Fatalf("%q: expected a directive", testCase.content)
		}
		found := false
		for _, diagnostic := range diagnostics {
			if diagnostic.Severity == testCase.wantSeverity && containsFold(diagnostic.Message, testCase.wantContains) {
				found = true
			}
		}
		if !found {
			t.Fatalf("%q: missing %s diagnostic containing %q; got %v", testCase.content, testCase.wantSeverity, testCase.wantContains, diagnostics)
		}
	}
}

func TestParseVizIgnoresNonComment(t *testing.T) {
	directive, _ := ParseViz("select '@viz(bar, x: a, y: b)' as note")
	if directive != nil {
		t.Fatalf("@viz inside a string literal must not parse: %+v", directive)
	}
}

func TestValidateVizColumns(t *testing.T) {
	directive := &VizDirective{Kind: "bar", Options: map[string]any{"x": "month", "y": []string{"revenue", "missing"}}}
	diagnostics := ValidateVizColumns(directive, []string{"month", "revenue"}, 1)
	if len(diagnostics) != 1 {
		t.Fatalf("expected 1 column diagnostic, got %v", diagnostics)
	}
	if !containsFold(diagnostics[0].Message, "missing") {
		t.Fatalf("unexpected diagnostic: %v", diagnostics[0])
	}
}

func TestVizDirectiveRoundTrip(t *testing.T) {
	directive := &VizDirective{Kind: "bar", Options: map[string]any{"x": "month", "y": "revenue", "stacked": true}}
	line := VizDirectiveLine(directive)
	reparsed, diagnostics := ParseViz(line + "\nselect 1")
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("round-trip produced an error: %v (line %q)", diagnostic, line)
		}
	}
	if !reflect.DeepEqual(reparsed.Options, directive.Options) || reparsed.Kind != directive.Kind {
		t.Fatalf("round-trip mismatch: %q → %#v", line, reparsed)
	}
}

func TestVizIsOutsideFingerprint(t *testing.T) {
	// Invariant 3: changing only the @viz directive must not move the
	// fingerprint (CanonicalSQL strips comments).
	nbA := singleCellNotebook(t, "-- @viz(bar, x: a, y: b)\nselect 1 as a, 2 as b")
	nbB := singleCellNotebook(t, "-- @viz(line, x: a, y: b, stacked: true)\nselect 1 as a, 2 as b")
	fpA := CellFingerprint(nbA, nbA.Cells[0])
	fpB := CellFingerprint(nbB, nbB.Cells[0])
	if fpA != fpB {
		t.Fatalf("viz directive leaked into fingerprint: %s vs %s", fpA, fpB)
	}
}

func TestMigrateLegacyVisualizationPreservesSQLBytes(t *testing.T) {
	tests := map[string]struct {
		content string
		want    string
	}{
		"line": {
			content: "-- @viz(line, x: month, y: [actual, forecast])\nselect month, actual, forecast from metrics\n",
			want:    "select month, actual, forecast from metrics\n",
		},
		"formatted inline": {
			content: "/* @viz(bar, x: month, y: revenue) */ select month, revenue from metrics\n",
			want:    "select month, revenue from metrics\n",
		},
	}
	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			got, definition, migrated, err := MigrateLegacyVisualization(testCase.content)
			if err != nil || !migrated {
				t.Fatalf("migration failed: migrated=%v err=%v", migrated, err)
			}
			if got != testCase.want {
				t.Fatalf("SQL changed unexpectedly:\ngot:  %q\nwant: %q", got, testCase.want)
			}
			if definition["version"] != 1 || definition["type"] == "" {
				t.Fatalf("invalid structured definition: %+v", definition)
			}
		})
	}
}

func containsFold(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexFold(haystack, needle) >= 0)
}

func indexFold(s, substr string) int {
	lowerS := []rune(toLower(s))
	lowerSub := []rune(toLower(substr))
	for i := 0; i+len(lowerSub) <= len(lowerS); i++ {
		if string(lowerS[i:i+len(lowerSub)]) == string(lowerSub) {
			return i
		}
	}
	return -1
}

func toLower(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'A' && r <= 'Z' {
			out[i] = r + 32
		}
	}
	return string(out)
}
