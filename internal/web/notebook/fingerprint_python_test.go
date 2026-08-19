package notebook

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
)

func pythonFingerprintCell(body string) string {
	return "\"\"\" @bruin\nid: python01\nclass: notebook\ntype: python\n@bruin \"\"\"\n\n" + body + "\n"
}

func TestPythonFingerprintPreservesSemanticWhitespace(t *testing.T) {
	first := loadRunFixture(t, map[string]string{
		ManifestFileName: "id: 11111111-0000-0000-0000-000000000201\nblocks:\n  - cell: python01\n",
		"model.py": pythonFingerprintCell(`def materialize():
    if True:
        return 1
    return 2`),
	})
	second := loadRunFixture(t, map[string]string{
		ManifestFileName: "id: 11111111-0000-0000-0000-000000000201\nblocks:\n  - cell: python01\n",
		"model.py": pythonFingerprintCell(`def materialize():
    if True:
        return 1
        return 2`),
	})

	if CellFingerprint(first, first.Cells[0]) == CellFingerprint(second, second.Cells[0]) {
		t.Fatal("Python indentation change did not move the fingerprint")
	}
}

func TestPythonFingerprintTracksEffectiveUvEnvironmentOnlyForPython(t *testing.T) {
	nb := loadRunFixture(t, map[string]string{
		ManifestFileName: "id: 11111111-0000-0000-0000-000000000202\nblocks:\n  - cell: python01\n  - cell: sqlcell1\n",
		"model.py":       pythonFingerprintCell("def materialize():\n    return None"),
		"summary.sql":    "/* @bruin\nid: sqlcell1\ntype: duckdb.sql\n@bruin */\nselect 1\n",
		"pyproject.toml": "[project]\nname = \"fingerprint-test\"\ndependencies = [\"pandas\"]\n",
	})
	pythonBefore := CellFingerprint(nb, nb.CellByID("python01"))
	sqlBefore := CellFingerprint(nb, nb.CellByID("sqlcell1"))

	pyprojectPath := filepath.Join(nb.Dir, "pyproject.toml")
	if err := os.WriteFile(pyprojectPath, []byte("[project]\nname = \"fingerprint-test\"\ndependencies = [\"polars\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withPyprojectEdit := loadRunFixtureReload(t, nb)
	if CellFingerprint(withPyprojectEdit, withPyprojectEdit.CellByID("python01")) == pythonBefore {
		t.Fatal("pyproject change did not move the Python fingerprint")
	}
	if CellFingerprint(withPyprojectEdit, withPyprojectEdit.CellByID("sqlcell1")) != sqlBefore {
		t.Fatal("Python dependency change moved an unrelated SQL fingerprint")
	}

	pythonAfterProject := CellFingerprint(withPyprojectEdit, withPyprojectEdit.CellByID("python01"))
	if err := os.WriteFile(filepath.Join(nb.Dir, "uv.lock"), []byte("version = 1\nrevision = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withLock := loadRunFixtureReload(t, nb)
	if CellFingerprint(withLock, withLock.CellByID("python01")) == pythonAfterProject {
		t.Fatal("uv.lock change did not move the Python fingerprint")
	}

	if err := os.WriteFile(filepath.Join(nb.Dir, "requirements.txt"), []byte("duckdb==1.4.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withRequirements := loadRunFixtureReload(t, nb)
	requirementsFingerprint := CellFingerprint(withRequirements, withRequirements.CellByID("python01"))
	if err := os.WriteFile(pyprojectPath, []byte("[project]\nname = \"ignored-in-requirements-mode\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pyprojectIgnored := loadRunFixtureReload(t, nb)
	if CellFingerprint(pyprojectIgnored, pyprojectIgnored.CellByID("python01")) != requirementsFingerprint {
		t.Fatal("pyproject changed the fingerprint while requirements.txt had execution precedence")
	}
	if err := os.WriteFile(filepath.Join(nb.Dir, "requirements.txt"), []byte("duckdb==1.4.5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	requirementsChanged := loadRunFixtureReload(t, nb)
	if CellFingerprint(requirementsChanged, requirementsChanged.CellByID("python01")) == requirementsFingerprint {
		t.Fatal("requirements.txt change did not move the Python fingerprint")
	}
}

func TestPythonEnvironmentFingerprintSearchesToWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	notebookDir := filepath.Join(root, "notebooks", "analysis")
	if err := os.MkdirAll(notebookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(root, "pyproject.toml")
	if err := os.WriteFile(projectPath, []byte("[project]\ndependencies = [\"pandas\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := PythonEnvironmentFingerprint(afero.NewOsFs(), notebookDir, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPath, []byte("[project]\ndependencies = [\"polars\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := PythonEnvironmentFingerprint(afero.NewOsFs(), notebookDir, root)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("workspace-level Python dependency change did not move the fingerprint")
	}

	standalone, err := PythonEnvironmentFingerprint(afero.NewOsFs(), notebookDir, "")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := PythonEnvironmentFingerprint(afero.NewOsFs(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if standalone != empty {
		t.Fatal("standalone loader escaped the notebook directory while discovering dependencies")
	}
}
