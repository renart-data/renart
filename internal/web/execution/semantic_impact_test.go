package execution

import "testing"

func TestSemanticColumnChangesAlignsInsertedAndRemovedOutputs(t *testing.T) {
	before := []SemanticColumnContract{{Name: "id", Type: "INTEGER"}, {Name: "total", Type: "INTEGER"}}
	after := []SemanticColumnContract{before[0], {Name: "email", Type: "VARCHAR"}, before[1]}
	changes := semanticColumnChanges(before, after)
	if len(changes) != 1 || changes[0].Before != nil || changes[0].After.Name != "email" || changes[0].Index != 1 {
		t.Fatalf("insert should affect only email: %#v", changes)
	}
	changes = semanticColumnChanges(after, before)
	if len(changes) != 1 || changes[0].After != nil || changes[0].Before.Name != "email" || changes[0].Index != 1 {
		t.Fatalf("removal should affect only email: %#v", changes)
	}
}

func TestSemanticColumnChangesDoesNotHideRenamesReordersOrTypeChanges(t *testing.T) {
	before := []SemanticColumnContract{{Name: "id", Type: "INTEGER"}, {Name: "total", Type: "INTEGER"}}
	for _, after := range [][]SemanticColumnContract{
		{{Name: "renamed", Type: "INTEGER"}, before[1]},
		{before[1], before[0]},
		{before[0], {Name: "total", Type: "DOUBLE"}},
	} {
		changes := semanticColumnChanges(before, after)
		if len(changes) == 0 || changes[0].Before == nil || changes[0].After == nil {
			t.Fatalf("existing output change became an addition: %#v", changes)
		}
	}
}

func TestSemanticColumnChangesRetainsBothSourcePositions(t *testing.T) {
	before := []SemanticColumnContract{{Name: "id", Type: "INTEGER"}, {Name: "total", Type: "INTEGER"}}
	after := []SemanticColumnContract{{Name: "email", Type: "VARCHAR"}, before[0], {Name: "total", Type: "DOUBLE"}}
	changes := semanticColumnChanges(before, after)
	if len(changes) != 2 || changes[1].BeforeIndex == nil || *changes[1].BeforeIndex != 1 || changes[1].AfterIndex == nil || *changes[1].AfterIndex != 2 || !changes[1].TypeChanged || changes[1].PositionChanged {
		t.Fatalf("wrong source positions for shifted type change: %#v", changes)
	}
	reordered := semanticColumnChanges(before, []SemanticColumnContract{before[1], before[0], after[0]})
	if len(reordered) != 3 || !reordered[0].PositionChanged || !reordered[1].PositionChanged || reordered[2].Before != nil || reordered[2].After.Name != "email" {
		t.Fatalf("reorder with addition: %#v", reordered)
	}
	duplicates := semanticColumnChanges([]SemanticColumnContract{before[0], before[0]}, []SemanticColumnContract{before[0], after[0], before[0]})
	if len(duplicates) != 1 || duplicates[0].Before != nil || duplicates[0].After.Name != "email" {
		t.Fatalf("duplicate names should not invent renames: %#v", duplicates)
	}
}

func TestCompareSemanticImpactIgnoresPresentationAnchorChanges(t *testing.T) {
	before := semanticTestAsset("revenue", "source:a", "canonical:a", true, "HUGEINT")
	after := before
	after.Source = &SemanticSourceAnchors{Fingerprint: "presentation-only", Query: SemanticSourceRange{Line: 20}}
	report := CompareSemanticImpact("snapshot", []SemanticAssetSnapshot{before}, []SemanticAssetSnapshot{after})
	if len(report.Assets) != 0 {
		t.Fatalf("presentation locations changed semantics: %#v", report)
	}
}

func TestCompareSemanticImpactFindsPropagatedTypeChange(t *testing.T) {
	baseline := []SemanticAssetSnapshot{semanticTestAsset("revenue", "source:a", "canonical:a", true, "HUGEINT")}
	candidate := []SemanticAssetSnapshot{semanticTestAsset("revenue", "source:a", "canonical:a", true, "DOUBLE")}

	report := CompareSemanticImpact("snapshot-7", baseline, candidate)
	if report.Status != SemanticImpactStatusAvailable || !report.Complete || report.Digest == "" {
		t.Fatalf("report identity = %#v", report)
	}
	if len(report.Assets) != 1 {
		t.Fatalf("asset impacts = %#v", report.Assets)
	}
	impact := report.Assets[0]
	if impact.SourceChange != SemanticSourceUnchanged || impact.Origin != SemanticImpactPropagated || impact.Severity != SemanticImpactWarning {
		t.Fatalf("impact classification = %#v", impact)
	}
	if len(impact.Columns) != 1 || !impact.Columns[0].TypeChanged {
		t.Fatalf("column impacts = %#v", impact.Columns)
	}
	if report.Summary.SchemaChanges != 1 || report.Summary.Warnings != 1 {
		t.Fatalf("summary = %#v", report.Summary)
	}
	if repeated := CompareSemanticImpact("snapshot-7", baseline, candidate); report.Digest != repeated.Digest {
		t.Fatalf("digest is unstable: %q != %q", report.Digest, repeated.Digest)
	}
}

func TestCompareSemanticImpactDistinguishesFormattingFromBehavior(t *testing.T) {
	baseline := []SemanticAssetSnapshot{semanticTestAsset("revenue", "source:a", "canonical:a", true, "HUGEINT")}

	formatted := CompareSemanticImpact(
		"snapshot-7",
		baseline,
		[]SemanticAssetSnapshot{semanticTestAsset("revenue", "source:b", "canonical:a", true, "HUGEINT")},
	)
	if len(formatted.Assets) != 1 || formatted.Assets[0].SourceChange != SemanticSourceFormattingOnly {
		t.Fatalf("formatting impact = %#v", formatted.Assets)
	}
	if formatted.Summary.FormattingOnly != 1 || formatted.Summary.Warnings != 0 {
		t.Fatalf("formatting summary = %#v", formatted.Summary)
	}

	behavior := CompareSemanticImpact(
		"snapshot-7",
		baseline,
		[]SemanticAssetSnapshot{semanticTestAsset("revenue", "source:c", "canonical:c", true, "HUGEINT")},
	)
	if len(behavior.Assets) != 1 || behavior.Assets[0].SourceChange != SemanticSourceChanged || behavior.Assets[0].Origin != SemanticImpactDirect {
		t.Fatalf("behavior impact = %#v", behavior.Assets)
	}
	if behavior.Summary.BehaviorChanges != 1 || behavior.Summary.Warnings != 1 {
		t.Fatalf("behavior summary = %#v", behavior.Summary)
	}
}

func TestCompareSemanticImpactIsConservativeForIncompleteAndRemovedAssets(t *testing.T) {
	baseline := []SemanticAssetSnapshot{
		semanticTestAsset("removed", "source:a", "canonical:a", true, "INTEGER"),
		semanticTestAsset("uncertain", "source:b", "canonical:b", false, ""),
	}
	candidate := []SemanticAssetSnapshot{semanticTestAsset("uncertain", "source:c", "", false, "")}

	report := CompareSemanticImpact("snapshot-7", baseline, candidate)
	if report.Complete {
		t.Fatalf("incomplete analysis reported complete: %#v", report)
	}
	if report.Summary.Removed != 1 || report.Summary.Incomplete != 1 || report.Summary.Warnings != 2 {
		t.Fatalf("summary = %#v", report.Summary)
	}
}

func semanticTestAsset(name, source, canonical string, complete bool, dataType string) SemanticAssetSnapshot {
	columns := []SemanticColumnContract{{Name: "total", Type: dataType, Nullability: "unknown"}}
	return SemanticAssetSnapshot{
		Name: name, Dialect: "duckdb", SourceFingerprint: source,
		CanonicalFingerprint: canonical, Complete: complete, Columns: columns,
	}
}
