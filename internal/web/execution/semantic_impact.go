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
	count := len(before)
	if len(after) > count {
		count = len(after)
	}
	result := make([]SemanticColumnImpact, 0, count)
	for index := 0; index < count; index++ {
		var beforeColumn, afterColumn *SemanticColumnContract
		if index < len(before) {
			value := before[index]
			beforeColumn = &value
		}
		if index < len(after) {
			value := after[index]
			afterColumn = &value
		}
		change := SemanticColumnImpact{Index: index, Before: beforeColumn, After: afterColumn}
		switch {
		case beforeColumn == nil || afterColumn == nil:
			change.NameChanged, change.TypeChanged, change.NullabilityChanged = true, true, true
		default:
			change.NameChanged = beforeColumn.Name != afterColumn.Name
			change.TypeChanged = beforeColumn.Type != afterColumn.Type
			change.NullabilityChanged = beforeColumn.Nullability != afterColumn.Nullability
		}
		if change.NameChanged || change.TypeChanged || change.NullabilityChanged {
			result = append(result, change)
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
