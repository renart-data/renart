package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

const (
	SemanticImpactVersion = "v1"

	SemanticImpactStatusAvailable   = "available"
	SemanticImpactStatusNoBaseline  = "no_baseline"
	SemanticImpactStatusUnavailable = "unavailable"

	SemanticAssetAdded    = "added"
	SemanticAssetRemoved  = "removed"
	SemanticAssetModified = "modified"

	SemanticSourceUnchanged      = "unchanged"
	SemanticSourceFormattingOnly = "formatting_only"
	SemanticSourceChanged        = "changed"
	SemanticSourceUnknown        = "unknown"

	SemanticImpactDirect     = "direct"
	SemanticImpactPropagated = "propagated"

	SemanticImpactInfo    = "info"
	SemanticImpactWarning = "warning"
)

// SemanticColumnContract is one ordered output-column contract in an inferred
// pipeline schema snapshot.
// renart:web-name PipelinePlanSemanticColumnContract
type SemanticColumnContract struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Nullability string `json:"nullability,omitempty"`
}

// SemanticSourceRange uses one-based UTF-16 editor coordinates, end-exclusive.
// renart:web-name PipelinePlanSemanticSourceRange
type SemanticSourceRange struct {
	Line      int `json:"line"`
	Column    int `json:"column"`
	EndLine   int `json:"end_line"`
	EndColumn int `json:"end_column"`
}

// SemanticSourceAnchors are presentation-only locations in the saved SQL file.
// Projections are omitted when output ordinals cannot be mapped unambiguously.
// renart:web-name PipelinePlanSemanticSourceAnchors
type SemanticSourceAnchors struct {
	Fingerprint string                `json:"fingerprint"`
	Query       SemanticSourceRange   `json:"query"`
	Projections []SemanticSourceRange `json:"projections"`
}

// SemanticAssetSnapshot is the internal, normalized input to the pure
// comparator. It intentionally contains no source text.
type SemanticAssetSnapshot struct {
	Name                 string
	Dialect              string
	SourceFingerprint    string
	CanonicalFingerprint string
	Complete             bool
	Columns              []SemanticColumnContract
	Source               *SemanticSourceAnchors
}

// renart:web-name PipelinePlanSemanticColumnImpact
type SemanticColumnImpact struct {
	Index              int                     `json:"index"`
	BeforeIndex        *int                    `json:"before_index,omitempty"`
	AfterIndex         *int                    `json:"after_index,omitempty"`
	PositionChanged    bool                    `json:"position_changed,omitempty"`
	Before             *SemanticColumnContract `json:"before,omitempty"`
	After              *SemanticColumnContract `json:"after,omitempty"`
	NameChanged        bool                    `json:"name_changed"`
	TypeChanged        bool                    `json:"type_changed"`
	NullabilityChanged bool                    `json:"nullability_changed"`
}

// renart:web-name PipelinePlanSemanticAssetImpact
type SemanticAssetImpact struct {
	Name                       string                 `json:"name"`
	Dialect                    string                 `json:"dialect,omitempty"`
	Change                     string                 `json:"change"`
	SourceChange               string                 `json:"source_change"`
	Origin                     string                 `json:"origin"`
	Severity                   string                 `json:"severity"`
	Complete                   bool                   `json:"complete"`
	BeforeCanonicalFingerprint string                 `json:"before_canonical_fingerprint,omitempty"`
	AfterCanonicalFingerprint  string                 `json:"after_canonical_fingerprint,omitempty"`
	Columns                    []SemanticColumnImpact `json:"columns"`
	BeforeSource               *SemanticSourceAnchors `json:"before_source,omitempty"`
	AfterSource                *SemanticSourceAnchors `json:"after_source,omitempty"`
}

// renart:web-name PipelinePlanSemanticImpactSummary
type SemanticImpactSummary struct {
	Added           int `json:"added"`
	Removed         int `json:"removed"`
	Modified        int `json:"modified"`
	FormattingOnly  int `json:"formatting_only"`
	BehaviorChanges int `json:"behavior_changes"`
	SchemaChanges   int `json:"schema_changes"`
	Incomplete      int `json:"incomplete"`
	Warnings        int `json:"warnings"`
}

// SemanticImpactReport is a read-only comparison between the latest deployed
// pipeline snapshot and the candidate working tree.
// renart:web-name PipelinePlanSemanticImpact
type SemanticImpactReport struct {
	Version           string                `json:"version"`
	Digest            string                `json:"digest"`
	Status            string                `json:"status"`
	BaselineVersionID string                `json:"baseline_version_id,omitempty"`
	Complete          bool                  `json:"complete"`
	Reason            string                `json:"reason,omitempty"`
	Assets            []SemanticAssetImpact `json:"assets"`
	Summary           SemanticImpactSummary `json:"summary"`
}

// CompareSemanticImpact compares normalized pipeline snapshots without
// assigning deployment policy. Stable SQL with a changed inferred output is
// classified as propagated; a canonical query change is direct.
func CompareSemanticImpact(
	baselineVersionID string,
	baseline, candidate []SemanticAssetSnapshot,
) SemanticImpactReport {
	report := SemanticImpactReport{
		Version: SemanticImpactVersion, Status: SemanticImpactStatusAvailable,
		BaselineVersionID: baselineVersionID, Complete: true,
		Assets: []SemanticAssetImpact{},
	}
	beforeByName := semanticAssetsByName(baseline)
	afterByName := semanticAssetsByName(candidate)
	names := make([]string, 0, len(beforeByName)+len(afterByName))
	for key := range beforeByName {
		names = append(names, key)
	}
	for key := range afterByName {
		if _, exists := beforeByName[key]; !exists {
			names = append(names, key)
		}
	}
	sort.Strings(names)

	for _, key := range names {
		before, beforeOK := beforeByName[key]
		after, afterOK := afterByName[key]
		impact, changed := compareSemanticAsset(before, beforeOK, after, afterOK)
		if !impact.Complete {
			report.Complete = false
			report.Summary.Incomplete++
		}
		if !changed {
			continue
		}
		report.Assets = append(report.Assets, impact)
		summarizeSemanticAsset(&report.Summary, impact)
	}
	report.Digest = semanticImpactDigest(report)
	return report
}

func NoBaselineSemanticImpact() SemanticImpactReport {
	report := SemanticImpactReport{
		Version: SemanticImpactVersion, Status: SemanticImpactStatusNoBaseline,
		Reason: "No previous deployment exists for comparison.",
		Assets: []SemanticAssetImpact{},
	}
	report.Digest = semanticImpactDigest(report)
	return report
}

func UnavailableSemanticImpact(reason string) SemanticImpactReport {
	report := SemanticImpactReport{
		Version: SemanticImpactVersion, Status: SemanticImpactStatusUnavailable,
		Reason: strings.TrimSpace(reason), Assets: []SemanticAssetImpact{},
	}
	report.Digest = semanticImpactDigest(report)
	return report
}

func semanticAssetsByName(assets []SemanticAssetSnapshot) map[string]SemanticAssetSnapshot {
	result := make(map[string]SemanticAssetSnapshot, len(assets))
	for _, asset := range assets {
		key := strings.ToLower(strings.TrimSpace(asset.Name))
		if key != "" {
			result[key] = asset
		}
	}
	return result
}

func compareSemanticAsset(
	before SemanticAssetSnapshot,
	beforeOK bool,
	after SemanticAssetSnapshot,
	afterOK bool,
) (SemanticAssetImpact, bool) {
	impact := SemanticAssetImpact{Columns: []SemanticColumnImpact{}, BeforeSource: before.Source, AfterSource: after.Source}
	switch {
	case !beforeOK:
		impact.Name, impact.Dialect = after.Name, after.Dialect
		impact.Change, impact.SourceChange = SemanticAssetAdded, SemanticSourceChanged
		impact.Origin, impact.Severity, impact.Complete = SemanticImpactDirect, SemanticImpactInfo, after.Complete
		impact.AfterCanonicalFingerprint = after.CanonicalFingerprint
		impact.Columns = semanticColumnChanges(nil, after.Columns)
		return impact, true
	case !afterOK:
		impact.Name, impact.Dialect = before.Name, before.Dialect
		impact.Change, impact.SourceChange = SemanticAssetRemoved, SemanticSourceChanged
		impact.Origin, impact.Severity, impact.Complete = SemanticImpactDirect, SemanticImpactWarning, before.Complete
		impact.BeforeCanonicalFingerprint = before.CanonicalFingerprint
		impact.Columns = semanticColumnChanges(before.Columns, nil)
		return impact, true
	}

	impact.Name, impact.Dialect = after.Name, after.Dialect
	impact.Change = SemanticAssetModified
	impact.Complete = before.Complete && after.Complete
	impact.BeforeCanonicalFingerprint = before.CanonicalFingerprint
	impact.AfterCanonicalFingerprint = after.CanonicalFingerprint
	impact.SourceChange = semanticSourceChange(before, after)
	impact.Columns = semanticColumnChanges(before.Columns, after.Columns)
	if impact.SourceChange == SemanticSourceUnchanged && len(impact.Columns) == 0 {
		return impact, false
	}
	if impact.SourceChange == SemanticSourceFormattingOnly && len(impact.Columns) == 0 && impact.Complete {
		impact.Origin, impact.Severity = SemanticImpactDirect, SemanticImpactInfo
		return impact, true
	}
	if impact.SourceChange == SemanticSourceUnchanged || impact.SourceChange == SemanticSourceFormattingOnly {
		impact.Origin = SemanticImpactPropagated
	} else {
		impact.Origin = SemanticImpactDirect
	}
	impact.Severity = SemanticImpactWarning
	return impact, true
}

func semanticSourceChange(before, after SemanticAssetSnapshot) string {
	if before.SourceFingerprint == after.SourceFingerprint && before.SourceFingerprint != "" {
		return SemanticSourceUnchanged
	}
	if before.CanonicalFingerprint == "" || after.CanonicalFingerprint == "" {
		return SemanticSourceUnknown
	}
	if before.CanonicalFingerprint == after.CanonicalFingerprint {
		return SemanticSourceFormattingOnly
	}
	return SemanticSourceChanged
}

func semanticColumnChanges(before, after []SemanticColumnContract) []SemanticColumnImpact {
	// Match named outputs first, preserving occurrence order for duplicate names.
	// Inserting a projection must not rename every following output or bind its
	// annotation to the wrong side's ordinal. Unmatched pairs remain contract
	// renames; only surplus outputs are additions/removals.
	byName := make(map[string][]int, len(before))
	for index, column := range before {
		if column.Name != "" {
			byName[column.Name] = append(byName[column.Name], index)
		}
	}
	matched := make([]int, len(after))
	used := make([]bool, len(before))
	for index, column := range after {
		matched[index] = -1
		if candidates := byName[column.Name]; len(candidates) > 0 {
			matched[index] = candidates[0]
			used[candidates[0]] = true
			byName[column.Name] = candidates[1:]
		}
	}
	// Compare relative order of surviving names, not absolute positions shifted
	// by insertions. An actual reorder remains an explicit contract change.
	ranks := make(map[int]int, len(before))
	for index, present := range used {
		if present {
			ranks[index] = len(ranks)
		}
	}
	moved := make([]bool, len(after))
	rank := 0
	for index, previous := range matched {
		if previous >= 0 {
			moved[index] = ranks[previous] != rank
			rank++
		}
	}
	next := 0
	for index, previous := range matched {
		if previous >= 0 {
			continue
		}
		for next < len(before) && used[next] {
			next++
		}
		if next < len(before) {
			matched[index], used[next] = next, true
		}
	}

	result := make([]SemanticColumnImpact, 0)
	appendChange := func(beforeIndex, afterIndex int, positionChanged bool) {
		change := SemanticColumnImpact{Index: afterIndex, PositionChanged: positionChanged}
		if beforeIndex >= 0 {
			change.BeforeIndex, change.Before = &beforeIndex, &before[beforeIndex]
		}
		if afterIndex >= 0 {
			change.AfterIndex, change.After = &afterIndex, &after[afterIndex]
		} else {
			change.Index = beforeIndex
		}
		switch {
		case change.Before == nil || change.After == nil:
			change.NameChanged, change.TypeChanged, change.NullabilityChanged = true, true, true
		default:
			change.NameChanged = change.Before.Name != change.After.Name
			change.TypeChanged = change.Before.Type != change.After.Type
			change.NullabilityChanged = change.Before.Nullability != change.After.Nullability
		}
		if change.NameChanged || change.TypeChanged || change.NullabilityChanged || change.PositionChanged {
			result = append(result, change)
		}
	}
	for index, previous := range matched {
		appendChange(previous, index, moved[index])
	}
	for index, present := range used {
		if !present {
			appendChange(index, -1, false)
		}
	}
	return result
}

func summarizeSemanticAsset(summary *SemanticImpactSummary, impact SemanticAssetImpact) {
	switch impact.Change {
	case SemanticAssetAdded:
		summary.Added++
	case SemanticAssetRemoved:
		summary.Removed++
	default:
		summary.Modified++
	}
	if impact.SourceChange == SemanticSourceFormattingOnly && len(impact.Columns) == 0 {
		summary.FormattingOnly++
	}
	if impact.SourceChange == SemanticSourceChanged || impact.SourceChange == SemanticSourceUnknown {
		summary.BehaviorChanges++
	}
	if len(impact.Columns) > 0 {
		summary.SchemaChanges++
	}
	if impact.Severity == SemanticImpactWarning {
		summary.Warnings++
	}
}

func semanticImpactDigest(report SemanticImpactReport) string {
	report.Digest = ""
	encoded, _ := json.Marshal(report)
	digest := sha256.Sum256(encoded)
	return SemanticImpactVersion + ":" + hex.EncodeToString(digest[:])
}
