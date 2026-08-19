package presentation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResolveParameterValuesChecksDefaultsOptionsAndOverrides(t *testing.T) {
	definitions := []ParameterDefinition{
		{ID: "region", Label: "Region", Type: ParameterTypeSelect, Default: "eu", Options: &ParameterOptions{Values: []any{"eu", "us"}}},
		{ID: "minimum", Type: ParameterTypeNumber, Default: 10},
		{ID: "period", Type: ParameterTypeDateRange, Default: []any{"2026-08-01", "2026-08-12"}},
	}
	values, findings := ResolveParameterValues(definitions, map[string]any{"region": "us", "minimum": 12.5})
	if len(findings) != 0 {
		t.Fatalf("unexpected findings: %+v", findings)
	}
	if values["region"] != "us" || values["minimum"] != 12.5 {
		t.Fatalf("runtime values were not applied: %#v", values)
	}

	_, findings = ResolveParameterValues(definitions, map[string]any{"region": "apac", "missing": true})
	if !hasFinding(findings, "parameter-value-not-in-options") || !hasFinding(findings, "parameter-override-unknown") {
		t.Fatalf("invalid overrides were not reported: %+v", findings)
	}
}

func TestParameterSQLLiteralsEscapeAndPreserveTypes(t *testing.T) {
	definitions := []ParameterDefinition{
		{ID: "name", Type: ParameterTypeText, Default: "O'Reilly"},
		{ID: "active", Type: ParameterTypeBoolean, Default: true},
		{ID: "regions", Type: ParameterTypeMultiSelect, Default: []any{"eu", "u's"}},
		{ID: "period", Type: ParameterTypeDateRange, Default: []any{"2026-08-01", "2026-08-12"}},
	}
	literals, err := ParameterSQLLiterals(definitions, nil)
	if err != nil {
		t.Fatal(err)
	}
	if literals["name"] != "'O''Reilly'" || literals["active"] != "TRUE" || literals["regions"] != "'eu', 'u''s'" {
		t.Fatalf("unexpected literals: %#v", literals)
	}
	if !strings.Contains(literals["period"], " AS DATE) AND CAST(") {
		t.Fatalf("date range is not BETWEEN-compatible: %q", literals["period"])
	}
}

func TestParameterSQLLiteralsRejectInvalidJSONNumber(t *testing.T) {
	definitions := []ParameterDefinition{{ID: "limit", Type: ParameterTypeNumber, Default: 1}}
	_, err := ParameterSQLLiterals(definitions, map[string]any{"limit": json.Number("1); drop table users")})
	if err == nil {
		t.Fatal("expected invalid JSON number to be rejected")
	}
}

func TestCheckFilterBindingsValidatesDatasetsFieldsTypesAndOperators(t *testing.T) {
	filters := []FilterDefinition{{
		ID: "period", Type: ParameterTypeDateRange, Default: []any{"2026-08-01", "2026-08-12"},
		Options: &ParameterOptions{Dataset: "sales", ValueField: "created_at", LabelField: "label"},
	}}
	datasets := map[string]ResolvedSchema{
		"sales": {Columns: []ResolvedColumn{
			{Name: "created_at", PhysicalType: "timestamp", SemanticType: SemanticTemporal},
			{Name: "label", PhysicalType: "varchar", SemanticType: SemanticCategorical},
		}},
	}
	findings := CheckFilterBindings(filters, datasets, []FilterBinding{{
		Filter: "period", Dataset: "sales", Column: "created_at", Operator: "between",
	}}, CheckOptions{Strict: true})
	if len(findings) != 0 {
		t.Fatalf("valid binding failed: %+v", findings)
	}

	findings = CheckFilterBindings(filters, datasets, []FilterBinding{{
		Filter: "period", Dataset: "sales", Column: "label", Operator: "contains",
	}}, CheckOptions{Strict: true})
	if !hasFinding(findings, "filter-binding-operator-incompatible") || !hasFinding(findings, "filter-binding-type-incompatible") {
		t.Fatalf("invalid binding was not rejected: %+v", findings)
	}
}

func TestCheckParameterDefinitionsRejectsMalformedDefinitions(t *testing.T) {
	findings := CheckParameterDefinitions([]ParameterDefinition{
		{ID: "Bad-ID", Type: ParameterTypeText, Default: 4},
		{ID: "region", Type: ParameterTypeSelect, Default: "eu", Options: &ParameterOptions{Values: []any{"us"}, Dataset: "sales", ValueField: "region"}},
		{ID: "region", Type: ParameterTypeDateRange, Default: []any{"2026-08-12", "2026-08-01"}},
	})
	for _, code := range []string{
		"parameter-id-invalid", "parameter-default-invalid", "parameter-options-ambiguous",
		"parameter-default-not-in-options", "parameter-id-duplicate",
	} {
		if !hasFinding(findings, code) {
			t.Fatalf("missing finding %s: %+v", code, findings)
		}
	}
}

func hasFinding(findings []Finding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
