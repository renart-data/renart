package notebook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
	"renart/internal/web/fingerprint"
)

// CellFingerprint hashes a cell's ID-resolved physical form: referenced
// logical names are rewritten to stable cell IDs (and external refs to
// their stable import names) before canonicalization, and the cell's own
// display name never enters the hash (its physical target is named by ID).
//
// Consequence — invariant 1: renaming a cell changes no fingerprint, so the
// staleness service sees nothing and no recompute can occur. The proof is
// the permanent regression test TestRenameChangesNoFingerprint.
//
// Name resolution uses the same identifier splice as the rename engine
// (never an AST re-print), so it never fails on templated SQL — a logical
// name left unresolved would silently break rename-invariance.
func CellFingerprint(nb *Notebook, cell *Cell) string {
	values := make(map[string]any, len(nb.Parameters))
	for _, parameter := range nb.Parameters {
		values[parameter.ID] = parameter.Default
	}
	return CellFingerprintWithParameters(nb, cell, values)
}

// CellFingerprintWithParameters binds a successful result to the effective
// typed runtime values that produced it. A server restart resets notebook
// parameters to authored defaults, so a result created with an override is
// correctly restored as stale instead of masquerading as the default result.
func CellFingerprintWithParameters(nb *Notebook, cell *Cell, values map[string]any) string {
	parameterFingerprint := notebookParameterFingerprint(nb, values)
	if IsSourceCell(cell) {
		definition := *cell.Source
		definition.ID = ""
		canonical, _ := yaml.Marshal(definition)
		hasher := sha256.New()
		hasher.Write([]byte("nbsource\x00"))
		hasher.Write(canonical)
		hasher.Write([]byte("\x00parameters\x00"))
		hasher.Write(parameterFingerprint)
		return "nb1:" + hex.EncodeToString(hasher.Sum(nil))
	}
	mapping := make(map[string]string, len(nb.Cells)+len(cell.ExternalRefs))
	for _, sibling := range nb.Cells {
		if sibling.ID == cell.ID {
			continue
		}
		mapping[strings.ToLower(sibling.Asset.Name)] = CellObjectName(sibling.ID)
	}
	for _, ref := range cell.ExternalRefs {
		mapping[strings.ToLower(ref)] = ImportObjectName(ref)
	}

	resolved := spliceIdentifiers(cell.Asset.ExecutableFile.Content, mapping)

	// CanonicalSQL strips comments (including @viz directives, invariant 3)
	// and collapses whitespace, so formatting and presentation never move
	// the fingerprint.
	canonical := fingerprint.CanonicalSQL(resolved)

	upstreamIDs := make([]string, 0, len(cell.Asset.Upstreams))
	for _, upstream := range cell.Asset.Upstreams {
		if sibling := nb.CellByName(upstream.Value); sibling != nil {
			upstreamIDs = append(upstreamIDs, sibling.ID)
		}
	}
	sort.Strings(upstreamIDs)
	externals := append([]string{}, cell.ExternalRefs...)
	sort.Strings(externals)

	hasher := sha256.New()
	hasher.Write([]byte("nbcell\x00"))
	hasher.Write([]byte(canonical))
	hasher.Write([]byte("\x00deps\x00"))
	hasher.Write([]byte(strings.Join(upstreamIDs, ",")))
	hasher.Write([]byte("\x00ext\x00"))
	hasher.Write([]byte(strings.Join(externals, ",")))
	// A source-native cell's connection and dialect are execution semantics:
	// the same SQL against another warehouse is a different snapshot. Local
	// cells also include their type so changing execution engines cannot reuse a
	// stale result merely because the query text is unchanged.
	hasher.Write([]byte("\x00type\x00"))
	hasher.Write([]byte(strings.ToLower(strings.TrimSpace(string(cell.Asset.Type)))))
	hasher.Write([]byte("\x00connection\x00"))
	hasher.Write([]byte(strings.ToLower(strings.TrimSpace(cell.Asset.Connection))))
	hasher.Write([]byte("\x00snapshot-mode\x00"))
	hasher.Write([]byte(strings.ToLower(strings.TrimSpace(cell.Asset.Meta[SnapshotModeMetaKey]))))
	hasher.Write([]byte("\x00snapshot-limit\x00"))
	hasher.Write([]byte(strings.TrimSpace(cell.Asset.Meta[SnapshotRowLimitMetaKey])))
	hasher.Write([]byte("\x00parameters\x00"))
	hasher.Write(parameterFingerprint)
	return "nb1:" + hex.EncodeToString(hasher.Sum(nil))
}

func notebookParameterFingerprint(nb *Notebook, values map[string]any) []byte {
	type fingerprintParameter struct {
		ID    string `json:"id"`
		Type  string `json:"type"`
		Value any    `json:"value"`
	}
	parameters := make([]fingerprintParameter, 0, len(nb.Parameters))
	for _, definition := range nb.Parameters {
		value, ok := values[definition.ID]
		if !ok {
			value = definition.Default
		}
		parameters = append(parameters, fingerprintParameter{
			ID: definition.ID, Type: string(definition.Type), Value: value,
		})
	}
	sort.Slice(parameters, func(i, j int) bool { return parameters[i].ID < parameters[j].ID })
	encoded, _ := json.Marshal(parameters)
	return encoded
}

// NotebookFingerprints returns the fingerprint of every cell, keyed by cell
// ID. Used by the staleness path and the rename-invariance regression test.
func NotebookFingerprints(nb *Notebook) map[string]string {
	result := make(map[string]string, len(nb.Cells))
	for _, cell := range nb.Cells {
		result[cell.ID] = CellFingerprint(nb, cell)
	}
	return result
}
