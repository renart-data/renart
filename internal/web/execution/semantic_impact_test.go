package execution

import "testing"

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
