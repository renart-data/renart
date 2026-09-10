package notebook

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"
	"renart/internal/web/identity"
)

// EnsureNotebookID returns the durable UUID from notebook.yml's top-level
// `id:` key, generating and prepending one when missing (same mechanics as
// pipeline IDs).
func EnsureNotebookID(filesystem afero.Fs, manifestPath string) (id string, generated bool, err error) {
	return identity.EnsurePipelineID(filesystem, manifestPath)
}

// CellAssetID derives the durable asset identifier for a cell. It hangs off
// the cell ID — never the display name — so renames keep history intact
// (invariant 1).
func CellAssetID(notebookUUID, cellID string) string {
	return identity.AssetID(notebookUUID, cellID)
}

// NewCellID generates a short random cell identifier (8 hex chars). The
// space is per-notebook, so collisions are vanishingly rare; callers should
// still check against existing siblings.
func NewCellID() string {
	return newShortID("")
}

// NewBlockID generates a durable manifest-owned block ID. Prefix should be a
// short semantic namespace such as "md" or "viz"; cell blocks continue to use
// their existing cell ID directly.
func NewBlockID(prefix string) string {
	return newShortID(strings.TrimSpace(prefix))
}

func newShortID(prefix string) string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failing is unrecoverable in practice; fall back to a
		// constant-free panic rather than silently reusing IDs.
		panic(fmt.Sprintf("notebook: cannot generate cell id: %v", err))
	}
	id := hex.EncodeToString(buf)
	if prefix == "" {
		return id
	}
	return prefix + "_" + id
}

// EnsureCellID returns the durable cell ID from the cell file's frontmatter
// `id:` key. When the key — or the whole frontmatter block — is missing, it
// generates an ID and writes it back, leaving the rest of the file
// byte-for-byte untouched.
func EnsureCellID(filesystem afero.Fs, path string) (id string, generated bool, err error) {
	content, err := afero.ReadFile(filesystem, path)
	if err != nil {
		return "", false, err
	}

	front, openerLine, ok := extractFrontmatter(string(content))
	if ok {
		if existing := frontmatterString(front, "id"); existing != "" {
			return existing, false, nil
		}

		id = NewCellID()
		lines := strings.SplitAfter(string(content), "\n")
		var out strings.Builder
		for index, line := range lines {
			out.WriteString(line)
			if index == openerLine {
				out.WriteString("id: " + quotedCellID(id) + "\n")
			}
		}
		if err := afero.WriteFile(filesystem, path, []byte(out.String()), 0o644); err != nil {
			return "", false, err
		}
		return id, true, nil
	}

	// No frontmatter at all: prepend a minimal block.
	id = NewCellID()
	header := minimalCellHeader(id, isPythonPath(path))
	if err := afero.WriteFile(filesystem, path, append([]byte(header), content...), 0o644); err != nil {
		return "", false, err
	}
	return id, true, nil
}

func isPythonPath(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".py")
}

// minimalCellHeader builds a frontmatter block in the comment style the cell's
// language uses: a `""" @bruin … """` docstring for Python, a `/* @bruin … */`
// block comment for SQL.
func minimalCellHeader(cellID string, python bool) string {
	if python {
		return fmt.Sprintf("\"\"\" @bruin\nid: %s\ntype: %s\nclass: %s\nmaterialization:\n  type: table\n@bruin \"\"\"\n\n", quotedCellID(cellID), PythonCellType, ClassNotebook)
	}
	return fmt.Sprintf("/* @bruin\nid: %s\ntype: %s\nclass: %s\n@bruin */\n\n", quotedCellID(cellID), DefaultCellType, ClassNotebook)
}

func quotedCellID(cellID string) string {
	return strconv.Quote(cellID)
}

// extractFrontmatter locates the embedded Bruin frontmatter block and
// parses its YAML. openerLine is the zero-based index of the line holding
// the opening @bruin marker. ok is false when no block exists.
func extractFrontmatter(content string) (values map[string]yaml.Node, openerLine int, ok bool) {
	lines := strings.Split(content, "\n")
	opener := -1
	closer := -1
	for index, line := range lines {
		if !strings.Contains(line, "@bruin") {
			continue
		}
		if opener == -1 {
			opener = index
			continue
		}
		closer = index
		break
	}
	if opener == -1 || closer == -1 {
		return nil, 0, false
	}

	body := strings.Join(lines[opener+1:closer], "\n")
	values = map[string]yaml.Node{}
	if err := yaml.Unmarshal([]byte(body), &values); err != nil {
		return nil, 0, false
	}
	return values, opener, true
}

func frontmatterString(values map[string]yaml.Node, key string) string {
	if values == nil {
		return ""
	}
	if raw, ok := values[key]; ok {
		// Keep the authored scalar spelling instead of decoding through any.
		// A hexadecimal cell ID such as 4101e095 is a valid YAML float token;
		// decoding it generically would turn the durable ID into 4.101e+98.
		if raw.Kind == yaml.ScalarNode {
			return strings.TrimSpace(raw.Value)
		}
		var decoded any
		if err := raw.Decode(&decoded); err == nil {
			return strings.TrimSpace(fmt.Sprintf("%v", decoded))
		}
	}
	return ""
}

// CellFileTemplate is the initial content for a newly created SQL cell.
func CellFileTemplate(cellID string) string {
	return fmt.Sprintf("/* @bruin\nid: %s\ntype: %s\nclass: %s\n@bruin */\n\nselect 1\n", quotedCellID(cellID), DefaultCellType, ClassNotebook)
}

// PythonCellFileTemplate is the initial content for a newly created Python
// cell. A Python cell defines `materialize()` returning tabular data; the
// runner materializes it into the notebook session as the cell's table.
func PythonCellFileTemplate(cellID string) string {
	return fmt.Sprintf(`""" @bruin
id: %s
type: %s
class: %s
materialization:
  type: table
@bruin """

from renart import query


def materialize():
    # Read an upstream cell without opening its database directly:
    # return query("select * from some_upstream_cell")
    return query("select * from (values (1), (2), (3)) as example(n)")
`, quotedCellID(cellID), PythonCellType, ClassNotebook)
}

// NormalizeCellID returns content with the frontmatter `id:` forced to
// cellID: inserted when missing, corrected when different, and a full
// header prepended when the content has no frontmatter at all. Cell
// identity is not editable — history and physical objects hang off it.
func NormalizeCellID(content, cellID string, python bool) string {
	lines := strings.Split(content, "\n")
	opener := -1
	closer := -1
	for index, line := range lines {
		if !strings.Contains(line, "@bruin") {
			continue
		}
		if opener == -1 {
			opener = index
			continue
		}
		closer = index
		break
	}

	if opener == -1 || closer == -1 {
		return minimalCellHeader(cellID, python) + content
	}

	for index := opener + 1; index < closer; index++ {
		trimmed := strings.TrimSpace(lines[index])
		if strings.HasPrefix(trimmed, "id:") || strings.HasPrefix(trimmed, "id :") {
			lines[index] = "id: " + quotedCellID(cellID)
			return strings.Join(lines, "\n")
		}
	}

	inserted := make([]string, 0, len(lines)+1)
	inserted = append(inserted, lines[:opener+1]...)
	inserted = append(inserted, "id: "+quotedCellID(cellID))
	inserted = append(inserted, lines[opener+1:]...)
	return strings.Join(inserted, "\n")
}

// CellFrontmatterValue reads a single string value from a cell file's
// frontmatter (e.g. "id" or "class") without parsing the full asset.
func CellFrontmatterValue(filesystem afero.Fs, path, key string) (string, error) {
	content, err := afero.ReadFile(filesystem, path)
	if err != nil {
		return "", err
	}
	front, _, ok := extractFrontmatter(string(content))
	if !ok {
		return "", nil
	}
	return frontmatterString(front, key), nil
}
