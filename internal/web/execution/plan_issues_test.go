package execution

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"renart/internal/authoringdiag"
)

func TestRenderIssuesReportsTheStageReasonInsteadOfASummary(t *testing.T) {
	failed := RenderResult{Status: RenderStatusError, Stages: []RenderStage{
		{Kind: "compiled_query", Status: RenderStageStatusError, Message: "invalid Jinja syntax: expected name or integer"},
	}}
	blockers, _ := RenderIssues("id", "mart.daily", failed)
	assert.Len(t, blockers, 1)
	assert.Equal(t, "invalid Jinja syntax: expected name or integer", blockers[0].Message)

	unexplained := RenderResult{Status: RenderStatusError}
	blockers, _ = RenderIssues("id", "mart.daily", unexplained)
	assert.Len(t, blockers, 1)
	assert.Equal(t, "asset execution could not be rendered completely", blockers[0].Message)
}

func TestFinalizedPlanReportsATemplateErrorOnce(t *testing.T) {
	syntax := "invalid Jinja syntax: expected name or integer (Line: 7 Col: 26, near \"}}\")"
	finding := PlanIssue{
		Code: "code_check_error", DiagnosticCode: authoringdiag.CodeTemplateRenderFailed, Severity: "error",
		Message: "Failed to render template: " + syntax, AssetID: "uuid:mart.daily", AssetName: "mart.daily",
	}
	stage := PlanIssue{Code: "stage_render_error", Severity: "error", Message: syntax, AssetID: "asset-id", AssetName: "mart.daily"}
	otherStage := PlanIssue{Code: "stage_render_error", Severity: "error", Message: "materialization SQL failed", AssetID: "asset-id", AssetName: "mart.daily"}
	otherAsset := PlanIssue{Code: "stage_render_error", Severity: "error", Message: syntax, AssetID: "other-id", AssetName: "mart.other"}

	plan := Plan{Readiness: PlanReadiness{Blockers: []PlanIssue{finding, stage, otherStage, otherAsset}}}
	finalizePlan(&plan)

	assert.Equal(t, []PlanIssue{finding, otherStage, otherAsset}, plan.Readiness.Blockers)
}
