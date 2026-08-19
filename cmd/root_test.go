package cmd

import (
	"testing"
)

// TestRootSurface enforces the CLI surface contract from plans/cli-v1.md §0:
// the visible commands are only what a pipeline author needs, categorized;
// everything else hides under `renart debug`. If this test fails because you
// added a command, decide its category (or hide it) deliberately.
func TestRootSurface(t *testing.T) {
	root := Root("test")

	if root.Usage != "the data pipeline IDE — build, run, and schedule pipelines from one binary" {
		t.Errorf("root usage changed: %q", root.Usage)
	}

	wantVisible := map[string]string{
		"web":        categoryIDE,
		"standalone": categoryIDE,
		"mcp":        categoryIDE,
		"run":        categoryPipeline,
		"plan":       categoryPipeline,
		"render":     categoryPipeline,
		"ls":         categoryPipeline,
		"type-check": categoryPipeline,
		"deploy":     categoryPipeline,
		"init":       categoryProject,
		"secrets":    categoryProject,
	}
	wantHidden := map[string]bool{
		"debug": true,
	}

	seen := map[string]bool{}
	for _, command := range root.Commands {
		seen[command.Name] = true
		if command.Hidden {
			if !wantHidden[command.Name] {
				t.Errorf("command %q is hidden but not in the hidden allowlist", command.Name)
			}
			continue
		}
		wantCategory, ok := wantVisible[command.Name]
		if !ok {
			t.Errorf("unexpected visible command %q — add it to the surface contract deliberately", command.Name)
			continue
		}
		if command.Category != wantCategory {
			t.Errorf("command %q category = %q, want %q", command.Name, command.Category, wantCategory)
		}
	}
	for name := range wantVisible {
		if !seen[name] {
			t.Errorf("expected visible command %q is missing", name)
		}
	}
	for name := range wantHidden {
		if !seen[name] {
			t.Errorf("expected hidden command %q is missing", name)
		}
	}
}

// TestDebugGroup pins the debug group's contents: internal tools stay
// reachable, just not advertised.
func TestDebugGroup(t *testing.T) {
	debug := Debug()
	if !debug.Hidden {
		t.Fatal("debug group must be hidden from the root help")
	}
	want := map[string]bool{"fp": true, "sql-lsp": true, "warm-cache": true}
	for _, command := range debug.Commands {
		if !want[command.Name] {
			t.Errorf("unexpected debug command %q", command.Name)
		}
		delete(want, command.Name)
	}
	for name := range want {
		t.Errorf("expected debug command %q is missing", name)
	}
}
