package notebookdoc

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNotebookFileTransactionRollsBackExactSnapshot(t *testing.T) {
	root := t.TempDir()
	notebookDir := filepath.Join(root, "notebooks", "rollback")
	writeTransactionTestFile(t, root, "notebooks/rollback/notebook.yml", "version: 2\n")
	writeTransactionTestFile(t, root, "notebooks/rollback/query.sql", "before\n")
	if err := os.WriteFile(filepath.Join(notebookDir, "empty.py"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := ReadAuthoredFiles(notebookDir)
	if err != nil {
		t.Fatal(err)
	}
	after := map[string][]byte{
		"notebook.yml": []byte("version: 2\ntitle: changed\n"),
		"new.sql":      []byte("new\n"),
		"query.sql":    []byte("after\n"),
	}
	injected := errors.New("injected transaction failure")
	err = ApplyNotebookFileTransaction(root, notebookDir, before, after, func(index int, _ string) error {
		if index == 2 {
			return injected
		}
		return nil
	})
	if !errors.Is(err, injected) {
		t.Fatalf("unexpected transaction error: %v", err)
	}
	restored, err := ReadAuthoredFiles(notebookDir)
	if err != nil {
		t.Fatal(err)
	}
	if !equalNotebookFiles(restored, before) {
		t.Fatalf("rollback did not restore exact files: before=%q restored=%q", before, restored)
	}
	if info, err := os.Stat(filepath.Join(notebookDir, "empty.py")); err != nil || info.Size() != 0 {
		t.Fatalf("zero-byte file was not restored: info=%v err=%v", info, err)
	}
}

func TestRecoverNotebookFileTransactionsRollsBackInterruptedApply(t *testing.T) {
	root := t.TempDir()
	targetRel := filepath.Join("notebooks", "recovery", "query.sql")
	target := filepath.Join(root, targetRel)
	writeTransactionTestFile(t, root, targetRel, "partial\n")
	journalDir := filepath.Join(root, ".renart", "notebook-transactions", "transaction-test")
	backupRel := filepath.Join("backups", "000")
	if err := os.MkdirAll(filepath.Join(journalDir, "backups"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(journalDir, backupRel), []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := notebookTransactionState{
		Phase: "applying",
		Entries: []notebookTransactionEntry{{
			Path:         filepath.ToSlash(targetRel),
			BeforeExists: true,
			Backup:       filepath.ToSlash(backupRel),
		}},
	}
	if err := writeNotebookTransactionState(journalDir, state); err != nil {
		t.Fatal(err)
	}
	if err := RecoverFileTransactions(root); err != nil {
		t.Fatalf("recover transactions: %v", err)
	}
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "original\n" {
		t.Fatalf("recovery kept partial content: %q", content)
	}
	if _, err := os.Stat(journalDir); !os.IsNotExist(err) {
		t.Fatalf("recovered journal still exists: %v", err)
	}
}

func writeTransactionTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
