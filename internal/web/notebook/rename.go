package notebook

import (
	"fmt"
	"path/filepath"
	"strings"
)

// RenameEdit is one file mutation produced by a rename: either a content
// rewrite (referencing cells) or a file move (the renamed cell itself).
type RenameEdit struct {
	// Path is the absolute file path being edited.
	Path string
	// NewContent is the rewritten file content (empty for a pure move).
	NewContent string
	// NewPath, when set, renames Path → NewPath (the cell file move).
	NewPath string
}

// PlanRename computes the atomic edit set to rename a cell's display name,
// without touching disk. The notebook manifest is unaffected because it
// references cells by ID, not name.
//
// References are rewritten by splicing identifier tokens in place (positions
// only — never an AST re-print), so user formatting, casing, and comments
// are preserved, and names inside string literals, comments, and Jinja are
// left alone (a regex would corrupt them). The renamed cell's own body is
// untouched: it is the LHS target, named by ID.
func PlanRename(nb *Notebook, cellID, newName string) ([]RenameEdit, error) {
	cell := nb.CellByID(cellID)
	if cell == nil {
		return nil, fmt.Errorf("cell %q not found", cellID)
	}
	oldName := cell.Asset.Name
	if oldName == newName {
		return nil, nil
	}

	edits := make([]RenameEdit, 0, len(nb.Cells))

	// Rewrite every sibling that references the old name, splicing over the
	// full file content (the @bruin frontmatter is a block comment, so it is
	// skipped automatically).
	for _, sibling := range nb.Cells {
		if sibling.ID == cellID || !referencesName(sibling, oldName) {
			continue
		}
		rewritten := spliceIdentifierReferences(sibling.Raw, oldName, newName)
		if rewritten != sibling.Raw {
			edits = append(edits, RenameEdit{Path: sibling.Path, NewContent: rewritten})
		}
	}

	// Move the cell file itself (preserving its directory and extension).
	ext := filepath.Ext(cell.Path)
	if IsSourcePath(cell.Path) {
		if strings.HasSuffix(strings.ToLower(cell.Path), ".source.yaml") {
			ext = ".source.yaml"
		} else {
			ext = ".source.yml"
		}
	}
	newPath := filepath.Join(filepath.Dir(cell.Path), newName+ext)
	edits = append(edits, RenameEdit{Path: cell.Path, NewPath: newPath})

	return edits, nil
}

// spliceIdentifierReferences replaces standalone identifier tokens equal to
// oldName (case-insensitively) with newName.
func spliceIdentifierReferences(content, oldName, newName string) string {
	return spliceIdentifiers(content, map[string]string{strings.ToLower(oldName): newName})
}

// spliceIdentifiers replaces standalone identifier tokens whose lowercased
// form is a key of mapping with the mapped value, in a single pass, while
// skipping string literals, line and block comments, and Jinja regions.
// Bare identifiers and double-quoted identifiers both match; a name preceded
// by `.` (the trailing segment of a qualified reference, never a cell) is
// left alone. Never fails — unlike an AST re-print, malformed or templated
// SQL passes through unchanged outside the matched tokens.
func spliceIdentifiers(content string, mapping map[string]string) string {
	const (
		code = iota
		singleQuote
		doubleQuote
		lineComment
		blockComment
		jinjaExpr
		jinjaStmt
	)

	runes := []rune(content)
	var out strings.Builder
	out.Grow(len(content) + 16)
	state := code

	isIdentRune := func(r rune) bool {
		return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
	}

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		next := rune(0)
		if i+1 < len(runes) {
			next = runes[i+1]
		}

		switch state {
		case code:
			switch {
			case r == '-' && next == '-':
				state = lineComment
				out.WriteString("--")
				i++
			case r == '/' && next == '*':
				state = blockComment
				out.WriteString("/*")
				i++
			case r == '\'':
				state = singleQuote
				out.WriteRune(r)
			case r == '{' && next == '{':
				state = jinjaExpr
				out.WriteString("{{")
				i++
			case r == '{' && next == '%':
				state = jinjaStmt
				out.WriteString("{%")
				i++
			case r == '"':
				// Double-quoted identifier: match the whole quoted name.
				closing := indexRune(runes, i+1, '"')
				if closing > i {
					inner := string(runes[i+1 : closing])
					if replacement, ok := mapping[strings.ToLower(inner)]; ok && !precededByDot(runes, i) {
						out.WriteString(`"` + replacement + `"`)
						i = closing
						continue
					}
				}
				state = doubleQuote
				out.WriteRune(r)
			case isIdentRune(r):
				start := i
				for i < len(runes) && isIdentRune(runes[i]) {
					i++
				}
				token := string(runes[start:i])
				i--
				// A name preceded by `.` is the trailing segment of a
				// qualified reference (schema.base) — never a bare cell. A
				// name *followed* by `.` (base.id) is a table qualifier and
				// must be renamed.
				if replacement, ok := mapping[strings.ToLower(token)]; ok && !precededByDot(runes, start) {
					out.WriteString(replacement)
				} else {
					out.WriteString(token)
				}
			default:
				out.WriteRune(r)
			}
		case singleQuote:
			out.WriteRune(r)
			if r == '\'' {
				state = code
			}
		case doubleQuote:
			out.WriteRune(r)
			if r == '"' {
				state = code
			}
		case lineComment:
			out.WriteRune(r)
			if r == '\n' {
				state = code
			}
		case blockComment:
			out.WriteRune(r)
			if r == '*' && next == '/' {
				out.WriteRune(next)
				i++
				state = code
			}
		case jinjaExpr:
			out.WriteRune(r)
			if r == '}' && next == '}' {
				out.WriteRune(next)
				i++
				state = code
			}
		case jinjaStmt:
			out.WriteRune(r)
			if r == '%' && next == '}' {
				out.WriteRune(next)
				i++
				state = code
			}
		}
	}
	return out.String()
}

func indexRune(runes []rune, from int, target rune) int {
	for i := from; i < len(runes); i++ {
		if runes[i] == target {
			return i
		}
	}
	return -1
}

func precededByDot(runes []rune, identStart int) bool {
	for i := identStart - 1; i >= 0; i-- {
		switch runes[i] {
		case ' ', '\t', '\n', '\r':
			continue
		case '.':
			return true
		default:
			return false
		}
	}
	return false
}

// referencesName reports whether a cell lists the given name as an upstream
// (the dependency index already resolved sibling references during load).
func referencesName(cell *Cell, name string) bool {
	for _, upstream := range cell.Asset.Upstreams {
		if strings.EqualFold(upstream.Value, name) {
			return true
		}
	}
	return false
}

// ValidateCellName checks a proposed cell name against the identifier
// charset, sibling cells, pipeline asset names, and reserved words. excludeID
// is the cell being renamed (so renaming to its own name is allowed for the
// case-only path). Returns a user-facing message, or "" when valid.
func ValidateCellName(nb *Notebook, newName, excludeID string, pipelineAssetNames map[string]bool) string {
	if !cellNamePattern.MatchString(newName) {
		return "names may only contain letters, digits, and underscores"
	}
	if reservedWords[strings.ToLower(newName)] {
		return fmt.Sprintf("%q is a reserved SQL word", newName)
	}
	for _, cell := range nb.Cells {
		if cell.ID == excludeID {
			continue
		}
		if strings.EqualFold(cell.Asset.Name, newName) {
			return fmt.Sprintf("a cell named %q already exists", newName)
		}
	}
	if pipelineAssetNames[strings.ToLower(newName)] {
		return fmt.Sprintf("%q is already a pipeline asset name", newName)
	}
	return ""
}

// reservedWords blocks the most dangerous identifier collisions; the full
// dialect set is enforced by the warehouse, this just catches obvious
// mistakes early.
var reservedWords = map[string]bool{
	"select": true, "from": true, "where": true, "join": true, "table": true,
	"view": true, "group": true, "order": true, "by": true, "having": true,
	"union": true, "insert": true, "update": true, "delete": true, "create": true,
	"drop": true, "alter": true, "with": true, "as": true, "on": true, "and": true,
	"or": true, "not": true, "null": true, "case": true, "when": true, "then": true,
	"else": true, "end": true, "limit": true, "offset": true,
}
