// Package navigationtarget owns resource addresses, not HTTP routes or commands.
package navigationtarget

import (
	"renart/internal/authoringdiag"
	"renart/internal/web/dataaddress"
)

// Target is a current editable resource. It never authorizes an edit. Unsupported
// diagnostics remain plain messages until their destination has a tested surface.
// renart:web-name ResourceTarget
type Target struct {
	NotebookID        string               `json:"notebook_id,omitempty"`
	CellID            string               `json:"cell_id,omitempty"`
	PresentationID    string               `json:"presentation_id,omitempty"`
	BlockID           string               `json:"block_id,omitempty"`
	Kind              string               `json:"kind"`
	AssetID           string               `json:"asset_id,omitempty"`
	Column            string               `json:"column,omitempty"`
	Field             string               `json:"field,omitempty"`
	Section           string               `json:"section,omitempty"`
	Address           *dataaddress.Address `json:"address,omitempty"`
	Connection        string               `json:"connection,omitempty"`
	SourceFingerprint string               `json:"source_fingerprint,omitempty"`
	Line              int                  `json:"line,omitempty"`
	EndLine           int                  `json:"end_line,omitempty"`
	CheckName         string               `json:"check_name,omitempty"`
}

func ForDiagnostic(assetID string, d authoringdiag.Diagnostic) *Target {
	policy, ok := DiagnosticPolicy(d.Code)
	if assetID == "" || !ok {
		return nil
	}
	if d.Code == authoringdiag.CodeDeclaredColumnTypeDrift && d.Subject != nil && d.Subject.Column != "" && d.Subject.Field == "type" {
		return &Target{Kind: "asset-column", AssetID: assetID, Column: d.Subject.Column, Field: "type"}
	}
	if d.Code == authoringdiag.CodeDeclaredColumnNullabilityDrift && d.Subject != nil && d.Subject.Column != "" && d.Subject.Field == "not_null" {
		return &Target{Kind: "asset-section", AssetID: assetID, Section: "checks", Column: d.Subject.Column, CheckName: "not_null"}
	}
	return &Target{Kind: "asset-section", AssetID: assetID, Section: policy.Section}
}

// Policy classifies the verified repair owner. Exact field targets require
// additional producer facts; a policy never extracts identifiers from prose.
type Policy struct{ Section string }

// DiagnosticLink refers to an item in the same response, not a persisted index.
// The target itself always uses durable semantic identity.
// renart:web
// renart:web-name SQLDiagnosticLink
type DiagnosticLink struct {
	Index  int     `json:"index"`
	Target *Target `json:"target"`
}

var policies = map[string]Policy{
	authoringdiag.CodeSQLSyntax:                       {"source"},
	authoringdiag.CodeSQLValidationFailed:             {"source"},
	authoringdiag.CodeUnresolvedRelation:              {"source"},
	authoringdiag.CodeUnresolvedAlias:                 {"source"},
	authoringdiag.CodeUnresolvedColumn:                {"source"},
	authoringdiag.CodeSQLTypeMismatch:                 {"source"},
	authoringdiag.CodeDeclaredOutputSchemaDrift:       {"columns"},
	authoringdiag.CodeDeclaredColumnTypeDrift:         {"columns"},
	authoringdiag.CodeDeclaredColumnNullabilityDrift:  {"checks"},
	authoringdiag.CodeUnmaterializedColumn:            {"materialization"},
	authoringdiag.CodeCrossConnectionReference:        {"identity"},
	authoringdiag.CodeExternalRelation:                {"dependencies"},
	authoringdiag.CodeCrossPipelineDependencyMissing:  {"dependencies"},
	authoringdiag.CodeCrossPipelineRelationAmbiguous:  {"dependencies"},
	authoringdiag.CodeDependencyValidationFailed:      {"dependencies"},
	authoringdiag.CodeMissingDependency:               {"dependencies"},
	authoringdiag.CodeCrossPipelineDuplicateURI:       {"identity"},
	authoringdiag.CodeCrossPipelineAmbiguousURI:       {"dependencies"},
	authoringdiag.CodeCrossPipelineUnresolvedURI:      {"dependencies"},
	authoringdiag.CodeCrossPipelineInvalidProducer:    {"dependencies"},
	authoringdiag.CodeCrossPipelineCycle:              {"dependencies"},
	authoringdiag.CodeInvalidMaterialization:          {"materialization"},
	authoringdiag.CodeInactiveMaterialization:         {"materialization"},
	authoringdiag.CodeMissingDeclaredColumns:          {"columns"},
	authoringdiag.CodePythonUndeclaredQueryDependency: {"dependencies"},
	authoringdiag.CodeTemplateRenderFailed:            {"source"},
	authoringdiag.CodeAssetDefinitionParseFailed:      {"source"},
}

func DiagnosticPolicy(code string) (Policy, bool) { policy, ok := policies[code]; return policy, ok }
