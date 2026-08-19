package notebook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/spf13/afero"
)

const notebookPythonFingerprintVersion = "nbpython2"

type notebookPythonEnvironment struct {
	Version  string `json:"version"`
	Mode     string `json:"mode"`
	Location string `json:"location,omitempty"`
	Config   string `json:"config,omitempty"`
	Lock     string `json:"lock,omitempty"`
}

// PythonEnvironmentFingerprint hashes the dependency files that uv
// actually consumes for notebook Python cells. Bruin gives requirements.txt
// precedence over pyproject.toml across the repository search path; uv.lock is
// relevant only in pyproject mode.
func PythonEnvironmentFingerprint(
	filesystem afero.Fs,
	notebookDir string,
	workspaceRoot string,
) (string, error) {
	root := filepath.Clean(workspaceRoot)
	start := filepath.Clean(notebookDir)
	if root == "." || root == "" || !pathWithinRoot(start, root) {
		root = start
	}

	environment := notebookPythonEnvironment{Version: notebookPythonFingerprintVersion, Mode: "none"}
	if requirementsPath := nearestNotebookDependencyFile(filesystem, start, root, "requirements.txt"); requirementsPath != "" {
		content, err := afero.ReadFile(filesystem, requirementsPath)
		if err != nil {
			return "", fmt.Errorf("read requirements.txt: %w", err)
		}
		environment.Mode = "requirements"
		environment.Location = notebookDependencyLocation(requirementsPath, root)
		environment.Config = string(content)
	} else if pyprojectPath := nearestNotebookDependencyFile(filesystem, start, root, "pyproject.toml"); pyprojectPath != "" {
		content, err := afero.ReadFile(filesystem, pyprojectPath)
		if err != nil {
			return "", fmt.Errorf("read pyproject.toml: %w", err)
		}
		environment.Mode = "pyproject"
		environment.Location = notebookDependencyLocation(pyprojectPath, root)
		environment.Config = string(content)
		if lock, lockErr := afero.ReadFile(filesystem, filepath.Join(filepath.Dir(pyprojectPath), "uv.lock")); lockErr == nil {
			environment.Lock = string(lock)
		} else if !errors.Is(lockErr, fs.ErrNotExist) {
			return "", fmt.Errorf("read uv.lock: %w", lockErr)
		}
	}

	encoded, err := json.Marshal(environment)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return notebookPythonFingerprintVersion + ":" + hex.EncodeToString(sum[:]), nil
}

func notebookDependencyLocation(path, root string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(filepath.Base(path))
	}
	return filepath.ToSlash(relative)
}

func nearestNotebookDependencyFile(filesystem afero.Fs, start, root, name string) string {
	for dir := start; ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, name)
		if info, err := filesystem.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		if dir == root {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir || !pathWithinRoot(parent, root) {
			return ""
		}
	}
}

func pathWithinRoot(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}
