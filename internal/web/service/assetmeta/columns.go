package assetmeta

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
)

// ColumnReconcileInput carries everything ReconcileColumns needs.
type ColumnReconcileInput struct {
	// Inferred are the columns derived from SQL/warehouse introspection
	// (name + type), in SELECT-list order.
	Inferred []pipeline.Column
	// Current are the asset's existing columns, carrying user annotations.
	Current []pipeline.Column
	// Prev is the asset's existing provenance.
	Prev RenartMeta
}

// ReconcileItem flags a situation the reconciler could not resolve
// automatically and that the UI should surface (§17).
type ReconcileItem struct {
	// Kind is currently always "column.stale".
	Kind string `json:"kind"`
	// Column is the affected column name.
	Column string `json:"column"`
	// Detail is a short human-readable explanation.
	Detail string `json:"detail"`
}

const ownTypeField = "type"

// ReconcileColumns implements §8: it merges inferred columns with the asset's
// existing columns, preserving user-authored metadata by column name, honoring
// ownership/drop/add provenance, and flagging stale columns (had user metadata
// but are no longer inferred) instead of destroying them.
//
//	final = (inferred − c.drop) + manual columns + preserved stale columns
//
// A generated column whose type was changed outside renart is adopted into
// c.own[col].type so the user's type survives future inference (§9).
func ReconcileColumns(in ColumnReconcileInput) (final []pipeline.Column, items []ReconcileItem, next RenartMeta) {
	currentByName := make(map[string]pipeline.Column, len(in.Current))
	for _, c := range in.Current {
		currentByName[lowerName(c.Name)] = c
	}

	dropSet := lowerSet(in.Prev.ColDrop)
	addSet := lowerSet(in.Prev.ColAdd)
	own := cloneOwn(in.Prev.ColOwn)
	sources := cloneStringMapLower(in.Prev.ColSource)

	emitted := make(map[string]struct{})
	final = make([]pipeline.Column, 0, len(in.Current)+len(in.Inferred))
	managed := make([]pipeline.Column, 0, len(in.Inferred))

	// 1. Inferred columns (minus drops), in SELECT-list order.
	for _, inferred := range in.Inferred {
		name := lowerName(inferred.Name)
		if name == "" {
			continue
		}
		if _, dropped := dropSet[name]; dropped {
			managed = append(managed, inferred) // still managed for the checksum
			continue
		}
		if _, done := emitted[name]; done {
			continue
		}
		emitted[name] = struct{}{}
		managed = append(managed, inferred)

		current, ok := currentByName[name]
		if !ok {
			final = append(final, inferred)
			continue
		}
		final = append(final, mergeColumn(current, inferred, own[name], sources[name]))
	}

	// 2. Remaining current columns: manual, stale, or obsolete generated.
	for _, current := range in.Current {
		name := lowerName(current.Name)
		if name == "" {
			continue
		}
		if _, done := emitted[name]; done {
			continue
		}
		emitted[name] = struct{}{}

		if _, dropped := dropSet[name]; dropped {
			continue // user explicitly dropped this inferred column
		}
		if _, manual := addSet[name]; manual {
			final = append(final, current)
			continue
		}
		if columnHasUserMetadata(current) {
			items = append(items, ReconcileItem{
				Kind:   "column.stale",
				Column: current.Name,
				Detail: "no longer produced by the query but has user-authored metadata",
			})
			final = append(final, current)
			continue
		}
		// Generated column, now gone, no user data → drop it silently.
	}

	// Updated provenance.
	next = in.Prev
	next.Version = SchemaVersion
	next.Generator = GeneratorVersion

	inferredNames := lowerSetFromColumns(in.Inferred)

	// Manual adds: columns kept that are not inferred and not stale.
	staleNames := make(map[string]struct{}, len(items))
	for _, it := range items {
		staleNames[lowerName(it.Column)] = struct{}{}
	}
	colAdd := make([]string, 0)
	for _, c := range final {
		name := lowerName(c.Name)
		if _, inferred := inferredNames[name]; inferred {
			continue
		}
		if _, stale := staleNames[name]; stale {
			continue
		}
		colAdd = append(colAdd, c.Name)
	}
	next.ColAdd = colAdd

	// Keep only drops that still refer to an inferred column.
	colDrop := make([]string, 0, len(in.Prev.ColDrop))
	for _, name := range in.Prev.ColDrop {
		if _, ok := inferredNames[lowerName(name)]; ok {
			colDrop = append(colDrop, name)
		}
	}
	next.ColDrop = colDrop

	// Keep ownership only for columns still present.
	presentNames := lowerSetFromColumns(final)
	for name := range own {
		if _, ok := presentNames[name]; !ok {
			delete(own, name)
		}
	}
	if len(own) == 0 {
		next.ColOwn = nil
	} else {
		next.ColOwn = own
	}

	// Keep non-default source provenance only for columns still present. SQL /
	// definition inference is the implicit default, so it never needs an entry.
	for name := range sources {
		if _, ok := presentNames[name]; !ok {
			delete(sources, name)
		}
	}
	if len(sources) == 0 {
		next.ColSource = nil
	} else {
		next.ColSource = sources
	}

	next.SigCols = ColumnProjectionHash(managedNotDropped(managed, dropSet))
	return final, items, next
}

// mergeColumn keeps every user-authored field from current and refreshes the
// generated fields (Name canonical case, Type) from inference. The type is a
// soft-generated field: inference owns it and may freely update it (e.g. a
// schema change integer→bigint) unless the user has explicitly taken ownership
// (c.own[col] contains "type"), in which case the user's type is preserved.
func mergeColumn(current, inferred pipeline.Column, ownedFields []string, source string) pipeline.Column {
	merged := current
	merged.Name = inferred.Name // canonical casing from inference
	if !containsFold(ownedFields, ownTypeField) &&
		!(strings.TrimSpace(source) != "" && strings.TrimSpace(current.Type) != "" && strings.TrimSpace(inferred.Type) == "") {
		merged.Type = inferred.Type
	}
	return merged
}

// columnHasUserMetadata reports whether a column carries anything a user would
// not want silently discarded.
func columnHasUserMetadata(c pipeline.Column) bool {
	return strings.TrimSpace(c.SourceColumn) != "" ||
		strings.TrimSpace(c.Mask) != "" ||
		strings.TrimSpace(c.Description) != "" ||
		len(c.Checks) > 0 ||
		c.PrimaryKey ||
		c.UpdateOnMerge ||
		len(c.Tags) > 0 ||
		strings.TrimSpace(c.Owner) != "" ||
		strings.TrimSpace(c.MergeSQL) != "" ||
		c.Nullable.Value != nil ||
		strings.TrimSpace(c.Default) != "" ||
		c.Precision != nil ||
		c.Scale != nil ||
		c.Length != nil ||
		strings.TrimSpace(c.Collation) != "" ||
		c.ForeignKey != nil ||
		len(c.Domains) > 0 ||
		columnHasUserMeta(c.Meta) ||
		strings.TrimSpace(c.Extends) != ""
}

func columnHasUserMeta(meta pipeline.EmptyStringMap) bool {
	for key := range meta {
		key = strings.TrimSpace(key)
		if !strings.EqualFold(key, ColumnKeyManual) &&
			!strings.EqualFold(key, ColumnKeyOwned) &&
			!strings.EqualFold(key, ColumnKeySource) {
			return true
		}
	}
	return false
}

// ColumnProjectionHash returns a stable checksum of the renart-managed column
// projection (§9 sig.c): the inferred name:type pairs, canonicalized.
func ColumnProjectionHash(managed []pipeline.Column) string {
	if len(managed) == 0 {
		return ""
	}
	entries := make([]string, 0, len(managed))
	for _, c := range managed {
		name := lowerName(c.Name)
		if name == "" {
			continue
		}
		entries = append(entries, name+":"+strings.ToLower(strings.TrimSpace(c.Type)))
	}
	if len(entries) == 0 {
		return ""
	}
	sort.Strings(entries)
	sum := sha256.Sum256([]byte(strings.Join(entries, "\n")))
	return hex.EncodeToString(sum[:])[:16]
}

// --- small helpers ---

func lowerName(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

func lowerSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, v := range values {
		if n := lowerName(v); n != "" {
			out[n] = struct{}{}
		}
	}
	return out
}

func lowerSetFromColumns(columns []pipeline.Column) map[string]struct{} {
	out := make(map[string]struct{}, len(columns))
	for _, c := range columns {
		if n := lowerName(c.Name); n != "" {
			out[n] = struct{}{}
		}
	}
	return out
}

func managedNotDropped(managed []pipeline.Column, dropSet map[string]struct{}) []pipeline.Column {
	out := make([]pipeline.Column, 0, len(managed))
	for _, c := range managed {
		if _, dropped := dropSet[lowerName(c.Name)]; dropped {
			continue
		}
		out = append(out, c)
	}
	return out
}

func cloneOwn(own map[string][]string) map[string][]string {
	out := make(map[string][]string, len(own))
	for k, v := range own {
		out[lowerName(k)] = append([]string(nil), v...)
	}
	return out
}

func cloneStringMapLower(values map[string]string) map[string]string {
	if len(values) == 0 {
		return make(map[string]string)
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		key = lowerName(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	return out
}

func containsFold(values []string, target string) bool {
	for _, v := range values {
		if strings.EqualFold(v, target) {
			return true
		}
	}
	return false
}
