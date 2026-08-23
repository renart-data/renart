package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateAPITypeScriptFromAnnotatedRoots(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.test/contracts\n\ngo 1.26\n")
	writeTestFile(t, filepath.Join(root, "internal/web/contracts/contracts.go"), `package contracts

import "time"

type Status string

const (
	StatusReady Status = "ready"
	StatusDone Status = "done"
)

type Embedded struct {
	ID string `+"`json:\"id\"`"+`
}

type EmbeddedAlias = Embedded

// renart:web
// renart:web-name APIResponse
type Response struct {
	Embedded
	Alias EmbeddedAlias `+"`json:\"alias\"`"+`
	Pointer *Embedded `+"`json:\"pointer,omitempty\"`"+`
	States map[string][]Status `+"`json:\"states\"`"+`
	CreatedAt time.Time `+"`json:\"created_at\"`"+`
	NoTag string
	Hidden string `+"`json:\"-\"`"+`
}
`)

	generated, err := generateAPITypeScript(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`export type APIResponse = {`,
		`  id: string;`,
		`  alias: Embedded;`,
		`  pointer?: Embedded;`,
		`  states: Record<string, Status[]>;`,
		`  created_at: string;`,
		`  NoTag: string;`,
		`export type Status = "ready" | "done";`,
	} {
		if !strings.Contains(generated, expected) {
			t.Fatalf("generated output is missing %q:\n%s", expected, generated)
		}
	}
	if strings.Contains(generated, "Hidden") {
		t.Fatalf("json-ignored field leaked into generated output:\n%s", generated)
	}
}

func TestGenerateAPITypeScriptRejectsIncompatibleNames(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.test/contracts\n\ngo 1.26\n")
	writeTestFile(t, filepath.Join(root, "internal/web/one/one.go"), `package one

// renart:web
// renart:web-name Shared
type One struct { Value string `+"`json:\"value\"`"+` }
`)
	writeTestFile(t, filepath.Join(root, "internal/web/two/two.go"), `package two

// renart:web
// renart:web-name Shared
type Two struct { Count int `+"`json:\"count\"`"+` }
`)

	_, err := generateAPITypeScript(root)
	if err == nil || !strings.Contains(err.Error(), "incompatible Go definitions") {
		t.Fatalf("expected incompatible-name error, got %v", err)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
