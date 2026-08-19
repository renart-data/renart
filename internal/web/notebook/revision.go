package notebook

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/afero"
)

// ContentRevision returns an opaque revision for an exact cell-file snapshot.
// It is intentionally content-derived rather than process-local: revisions
// remain valid across server restarts and also detect edits made directly on
// disk. Clients use it as an optimistic-concurrency precondition when saving.
func ContentRevision(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// SnapshotRevision returns an opaque revision for the complete authored
// notebook snapshot. It covers the exact manifest bytes plus every cell's
// relative filename and bytes. Including filenames makes rename/reorder-aware
// semantic edits conflict even though cell execution fingerprints intentionally
// remain name-independent.
func SnapshotRevision(filesystem afero.Fs, nb *Notebook) (string, error) {
	if filesystem == nil || nb == nil {
		return "", fmt.Errorf("notebook revision requires a filesystem and notebook")
	}
	hash := sha256.New()
	writeRevisionPart := func(label string, content []byte) error {
		if err := binary.Write(hash, binary.BigEndian, uint64(len(label))); err != nil {
			return err
		}
		if _, err := hash.Write([]byte(label)); err != nil {
			return err
		}
		if err := binary.Write(hash, binary.BigEndian, uint64(len(content))); err != nil {
			return err
		}
		_, err := hash.Write(content)
		return err
	}

	manifestPath := filepath.Join(nb.Dir, ManifestFileName)
	manifest, err := afero.ReadFile(filesystem, manifestPath)
	if err != nil {
		return "", fmt.Errorf("read manifest: %w", err)
	}
	if err := writeRevisionPart(ManifestFileName, manifest); err != nil {
		return "", err
	}

	type cellSnapshot struct {
		label string
		path  string
	}
	cells := make([]cellSnapshot, 0, len(nb.Cells))
	seenLabels := make(map[string]bool, len(nb.Cells)+1)
	for _, cell := range nb.Cells {
		if cell == nil || cell.Path == "" {
			continue
		}
		rel, relErr := filepath.Rel(nb.Dir, cell.Path)
		if relErr != nil {
			rel = filepath.Base(cell.Path)
		}
		label := filepath.ToSlash(rel)
		seenLabels[label] = true
		cells = append(cells, cellSnapshot{label: label, path: cell.Path})
	}
	entries, readDirErr := afero.ReadDir(filesystem, nb.Dir)
	if readDirErr != nil {
		return "", fmt.Errorf("read notebook directory: %w", readDirErr)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		lower := strings.ToLower(name)
		if name == ManifestFileName || seenLabels[filepath.ToSlash(name)] {
			continue
		}
		if lower != "pyproject.toml" && !strings.HasSuffix(lower, ".source.yml") && !strings.HasSuffix(lower, ".source.yaml") {
			continue
		}
		cells = append(cells, cellSnapshot{label: filepath.ToSlash(name), path: filepath.Join(nb.Dir, name)})
	}
	sort.Slice(cells, func(i, j int) bool { return cells[i].label < cells[j].label })
	for _, cell := range cells {
		content, readErr := afero.ReadFile(filesystem, cell.path)
		if readErr != nil {
			return "", fmt.Errorf("read cell %s: %w", cell.label, readErr)
		}
		if err := writeRevisionPart(cell.label, content); err != nil {
			return "", err
		}
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}
