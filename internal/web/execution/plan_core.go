package execution

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

func NormalizePlanPurpose(raw string) (string, error) {
	purpose := strings.TrimSpace(raw)
	if purpose == "" {
		return PlanPurposeExecution, nil
	}
	switch purpose {
	case PlanPurposeExecution, PlanPurposeDeployment:
		return purpose, nil
	default:
		return "", fmt.Errorf("purpose must be execution or deployment")
	}
}

func NormalizePlanSource(req PlanSourceRequest, deployedOnly bool) (PlanSourceRequest, error) {
	req.Kind = strings.TrimSpace(req.Kind)
	req.VersionID = strings.TrimSpace(req.VersionID)
	if req.Kind == "" && req.VersionID != "" {
		req.Kind = PlanSourceSnapshot
	}
	if req.Kind == "" {
		if deployedOnly {
			req.Kind = PlanSourceSnapshot
		} else {
			req.Kind = PlanSourceWorkingTree
		}
	}
	switch req.Kind {
	case PlanSourceWorkingTree:
		if req.VersionID != "" {
			return PlanSourceRequest{}, fmt.Errorf("working_tree source does not accept version_id")
		}
	case PlanSourceSnapshot:
	default:
		return PlanSourceRequest{}, fmt.Errorf("source kind must be working_tree or snapshot")
	}
	return req, nil
}

func NormalizePlanSelection(req PlanSelectionRequest) (PlanSelectionRequest, error) {
	req.Mode = strings.TrimSpace(req.Mode)
	req.AssetName = strings.TrimSpace(req.AssetName)
	req.Scope = strings.TrimSpace(req.Scope)
	req.Selector = strings.TrimSpace(req.Selector)
	if req.Mode == "" {
		req.Mode = PlanSelectionNeeded
	}
	switch req.Mode {
	case PlanSelectionNeeded, PlanSelectionAll:
		if req.AssetName != "" || req.Scope != "" || req.Selector != "" {
			return PlanSelectionRequest{}, fmt.Errorf("asset_name, scope, and selector are not valid for %s selection", req.Mode)
		}
	case PlanSelectionAsset:
		if req.Selector != "" {
			return PlanSelectionRequest{}, fmt.Errorf("selector is only valid for selector selection")
		}
		if req.AssetName == "" {
			return PlanSelectionRequest{}, fmt.Errorf("asset selection requires asset_name")
		}
		if req.Scope == "" {
			req.Scope = "asset"
		}
		switch req.Scope {
		case "asset", "asset_with_upstreams", "asset_with_downstreams", "asset_with_upstreams_and_downstreams":
		default:
			return PlanSelectionRequest{}, fmt.Errorf("invalid asset selection scope")
		}
	case PlanSelectionSelector, PlanSelectionSelectorNeeded:
		if req.AssetName != "" || req.Scope != "" {
			return PlanSelectionRequest{}, fmt.Errorf("asset_name and scope are not valid for selector selection")
		}
		if req.Selector == "" {
			return PlanSelectionRequest{}, fmt.Errorf("selector selection requires selector")
		}
		if len(req.Selector) > 4096 {
			return PlanSelectionRequest{}, fmt.Errorf("selector exceeds the 4096 byte limit")
		}
	default:
		return PlanSelectionRequest{}, fmt.Errorf("selection mode must be needed, all, asset, selector, or selector_needed")
	}
	return req, nil
}

func CloneRenderStages(stages []RenderStage, includeContent bool) []RenderStage {
	result := append([]RenderStage(nil), stages...)
	if !includeContent {
		for index := range result {
			result[index].Content = ""
		}
	}
	return result
}

func PartialRenderWarning(result RenderResult) (string, bool) {
	for _, stage := range result.Stages {
		if stage.Status == RenderStageStatusError {
			return "one or more execution stages could not be rendered", true
		}
		if stage.Status == RenderStageStatusUnsupported || stage.Fidelity == RenderFidelityUnsupported {
			return "one or more execution stages cannot be rendered statically", true
		}
	}
	for _, stage := range result.Stages {
		if stage.Fidelity == RenderFidelityRuntimeOnly {
			return "some execution details are only available at runtime", true
		}
	}
	if result.Asset.Target.Fidelity == RenderFidelityRuntimeOnly {
		return "the physical output target is only available at runtime", true
	}
	return "", false
}

// RenderIssues converts one renderer result into plan-level blockers and
// warnings without coupling render implementations to planner state.
func RenderIssues(assetID, assetName string, result RenderResult) (blockers, warnings []PlanIssue) {
	blockers = []PlanIssue{}
	warnings = []PlanIssue{}
	if result.Status == RenderStatusUnsupported || result.Status == RenderStatusError {
		blockers = append(blockers, PlanIssue{
			Code: "asset_render_" + string(result.Status), Severity: "error",
			Message: "asset execution could not be rendered completely",
			AssetID: assetID, AssetName: assetName,
		})
	} else if message, warn := PartialRenderWarning(result); result.Status == RenderStatusPartial && warn {
		warnings = append(warnings, PlanIssue{
			Code: "asset_render_partial", Severity: "warning", Message: message,
			AssetID: assetID, AssetName: assetName,
		})
	}
	for _, issue := range result.Issues {
		planIssue := PlanIssue{
			Code: issue.Code, Severity: issue.Severity, Message: issue.Message,
			AssetID: assetID, AssetName: assetName,
		}
		if issue.Severity == "error" {
			blockers = append(blockers, planIssue)
		} else {
			warnings = append(warnings, planIssue)
		}
	}
	for _, stage := range result.Stages {
		if stage.Status == RenderStageStatusOK {
			continue
		}
		issue := PlanIssue{
			Code: "stage_render_" + string(stage.Status), Severity: "error",
			Message: stage.Message, AssetID: assetID, AssetName: assetName,
		}
		if strings.TrimSpace(issue.Message) == "" {
			issue.Message = "execution stage could not be rendered"
		}
		if stage.Kind == "check" && stage.CheckBlocking != nil && !*stage.CheckBlocking {
			issue.Severity = "warning"
			warnings = append(warnings, issue)
		} else {
			blockers = append(blockers, issue)
		}
	}
	return blockers, warnings
}

func DedupePlanIssues(issues []PlanIssue) []PlanIssue {
	seen := make(map[string]struct{}, len(issues))
	result := make([]PlanIssue, 0, len(issues))
	for _, issue := range issues {
		key := issue.Code + "\x00" + issue.AssetID + "\x00" + issue.Message
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, issue)
	}
	return result
}

func PlanID(plan Plan) string {
	return ReviewedIdentityID(ReviewedIdentityFromPlan(plan))
}

func ReviewedIdentityID(identity ReviewedIdentity) string {
	encoded, _ := json.Marshal(identity)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func ReviewedIdentityFromPlan(plan Plan) ReviewedIdentity {
	return ReviewedIdentity{
		PipelineUUID: plan.PipelineUUID,
		Source:       plan.Source,
		Context:      plan.Context,
		Selection:    plan.Selection,
		Prerequisites: append(
			[]Prerequisite(nil), plan.Prerequisites...,
		),
		Resources:          CloneResources(plan.Resources),
		ExecutionContracts: append([]ExecutionContract(nil), plan.ExecutionContracts...),
		ExecutionUnits:     append([]PlanExecutionUnit(nil), plan.ExecutionUnits...),
	}
}
