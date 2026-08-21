package sqllsp

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	duck "github.com/bruin-data/bruin/pkg/duckdb"
	"github.com/bruin-data/bruin/pkg/query"
)

func TestEnrichDuckDBFileRelationsProvidesParquetColumnCompletion(t *testing.T) {
	workspaceRoot := t.TempDir()
	parquetPath := filepath.Join(workspaceRoot, "example.parquet")
	client, err := duck.NewClient(duck.Config{Path: ""})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	escapedPath := strings.ReplaceAll(filepath.ToSlash(parquetPath), "'", "''")
	if err := client.RunQueryWithoutResult(t.Context(), &query.Query{
		Query: "copy (select 1::integer as id, 'Ada'::varchar as name) to '" + escapedPath + "' (format parquet)",
	}); err != nil {
		t.Fatal(err)
	}

	const uri = URI("file:///workspace/query.sql")
	doc := TextDocumentItem{URI: uri, Text: `select  from "./example.parquet"`}
	graph := EnrichDuckDBFileRelations(t.Context(), CanonicalGraph{
		Version: 1,
		Assets:  []AssetNode{{ID: "query", URI: uri, Dialect: "duckdb"}},
	}, doc, workspaceRoot, NewDuckDBFileSchemaCache())

	labels := completionLabels(NewEngine(graph).Complete(doc, Position{Line: 0, Character: len("select ")}))
	for _, expected := range []string{"id", "name"} {
		if !slices.Contains(labels, expected) {
			t.Fatalf("expected file column %q in completions, got %#v", expected, labels)
		}
	}
	for _, diagnostic := range NewEngine(graph).Diagnostics(doc) {
		if diagnostic.Code == "unresolved-relation" {
			t.Fatalf("file relation was unexpectedly unresolved: %#v", diagnostic)
		}
	}
}

func TestIsDuckDBLocalFileRelationExcludesRemoteFilesystems(t *testing.T) {
	for _, relation := range []string{"./example.parquet", "data/example.csv", "example.jsonl", `/tmp/example.parquet`, `C:\\data\\example.parquet`} {
		if !IsDuckDBLocalFileRelation(relation) {
			t.Fatalf("expected %q to be a local file relation", relation)
		}
	}
	for _, relation := range []string{"analytics.orders", "s3://bucket/example.parquet", "https://example.com/data.csv"} {
		if IsDuckDBLocalFileRelation(relation) {
			t.Fatalf("did not expect %q to be a local file relation", relation)
		}
	}
}

func TestWorkspaceServerUsesDuckDBFilePolicyForDocumentEngines(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "example.csv"), []byte("id,name\n1,Ada\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	uri := FileURI(filepath.Join(root, "query.sql"))
	doc := TextDocumentItem{URI: uri, Text: `select  from "./example.csv"`}
	server := NewWorkspaceServer(root, CanonicalGraph{
		Version: 1,
		Assets:  []AssetNode{{ID: "query", URI: uri, Dialect: "duckdb"}},
	})

	labels := completionLabels(server.engineForDocument(t.Context(), doc).Complete(
		doc,
		Position{Line: 0, Character: len("select ")},
	))
	if !slices.Contains(labels, "id") || !slices.Contains(labels, "name") {
		t.Fatalf("expected stdio server file columns, got %#v", labels)
	}

	server.SetDuckDBFilesystemAccess(false)
	diagnostics := server.engineForDocument(t.Context(), doc).Diagnostics(doc)
	foundDisabled := false
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == "duckdb-filesystem-access-disabled" {
			foundDisabled = true
		}
	}
	if !foundDisabled {
		t.Fatalf("expected disabled stdio policy diagnostic, got %#v", diagnostics)
	}
}
