package presentation

import (
	"context"
	"strings"
	"testing"
)

func TestVisualizationDefinitionYAMLRoundTripIsDeterministic(t *testing.T) {
	raw, definition, findings := DecodeVisualizationDefinitionYAML(`type: line
version: 1
encoding:
  y:
    - field: revenue
  x:
    field: month
`)
	if len(findings) != 0 || definition.Type != "line" {
		t.Fatalf("decode: definition=%+v findings=%+v", definition, findings)
	}
	first, err := EncodeVisualizationDefinitionYAML(raw)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, _, reparsedFindings := DecodeVisualizationDefinitionYAML(first)
	if len(reparsedFindings) != 0 {
		t.Fatalf("reparse findings: %+v", reparsedFindings)
	}
	second, err := EncodeVisualizationDefinitionYAML(reparsed)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("canonical YAML changed:\n%s\n---\n%s", first, second)
	}
	if !strings.Contains(first, "type: line") || !strings.Contains(first, "field: revenue") {
		t.Fatalf("canonical YAML lost definition fields:\n%s", first)
	}
}

func TestVisualizationCheckerFindsMissingAndIncompatibleFields(t *testing.T) {
	definition, decodeFindings := DecodeVisualizationDefinition(map[string]any{
		"version": 1,
		"type":    "line",
		"encoding": map[string]any{
			"x": map[string]any{"field": "month", "format": "date"},
			"y": []any{
				map[string]any{"field": "label"},
				map[string]any{"field": "missing"},
			},
		},
	})
	if len(decodeFindings) != 0 {
		t.Fatalf("valid definition did not decode: %+v", decodeFindings)
	}
	schema := ResolvedSchema{Complete: true, Columns: []ResolvedColumn{
		{Name: "month", PhysicalType: "DATE"},
		{Name: "label", PhysicalType: "VARCHAR"},
	}}
	findings := (Checker{}).CheckVisualization(context.Background(), definition, schema, CheckOptions{})
	assertFinding(t, findings, "visualization-field-type-incompatible", "encoding.y[0].field", "error")
	assertFinding(t, findings, "visualization-field-missing", "encoding.y[1].field", "warning")

	strict := (Checker{}).CheckVisualization(context.Background(), definition, schema, CheckOptions{Strict: true})
	assertFinding(t, strict, "visualization-field-missing", "encoding.y[1].field", "error")
}

func TestVisualizationCheckerUnderstandsWarehousePhysicalTypes(t *testing.T) {
	cases := map[string]SemanticType{
		"NUMBER(38, 2)": SemanticNumeric,
		"INT8":          SemanticNumeric,
		"TIMESTAMPTZ":   SemanticTemporal,
		"VARCHAR(255)":  SemanticCategorical,
		"JSONB":         SemanticSemiStructured,
		"STRUCT(a INT)": SemanticSemiStructured,
		"GEOGRAPHY":     SemanticGeospatial,
		"BYTEA":         SemanticBinary,
	}
	for physical, expected := range cases {
		if actual := SemanticTypeForPhysicalType(physical); actual != expected {
			t.Errorf("%s: got %s, want %s", physical, actual, expected)
		}
	}
}

func TestVisualizationCheckerRejectsIncompleteRequiredSource(t *testing.T) {
	definition, findings := DecodeVisualizationDefinition(map[string]any{
		"version":          1,
		"type":             "kpi",
		"require_complete": true,
		"value":            map[string]any{"field": "revenue"},
	})
	if len(findings) != 0 {
		t.Fatalf("decode: %+v", findings)
	}
	findings = (Checker{}).CheckVisualization(context.Background(), definition, ResolvedSchema{
		Sampled: true,
		Columns: []ResolvedColumn{{Name: "revenue", PhysicalType: "DECIMAL(12,2)"}},
	}, CheckOptions{Strict: true})
	assertFinding(t, findings, "visualization-requires-complete-data", "", "error")
}

func assertFinding(t *testing.T, findings []Finding, code, path, severity string) {
	t.Helper()
	for _, finding := range findings {
		if finding.Code == code && finding.Path == path && finding.Severity == severity {
			return
		}
	}
	t.Fatalf("finding %s at %s (%s) not found in %+v", code, path, severity, findings)
}
