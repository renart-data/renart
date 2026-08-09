package scheduler

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPipelineRunPlanStrictRoundTrip(t *testing.T) {
	t.Parallel()
	plan := validPipelineRunPlan(t)

	body, err := marshalPipelineRunPlan(plan)
	require.NoError(t, err)
	persisted, err := unmarshalPipelineRunPlan(plan.Version, body)
	require.NoError(t, err)
	assert.Equal(t, plan, persisted)

	var document map[string]any
	require.NoError(t, json.Unmarshal(body, &document))
	document["future_behavior"] = true
	body, err = json.Marshal(document)
	require.NoError(t, err)
	_, err = unmarshalPipelineRunPlan(plan.Version, body)
	require.ErrorContains(t, err, "unknown field")
}

func TestPipelineRunPlanV3StrictRoundTrip(t *testing.T) {
	t.Parallel()
	plan := validPipelineRunPlanV3(t)

	body, err := marshalPipelineRunPlan(plan)
	require.NoError(t, err)
	persisted, err := unmarshalPipelineRunPlan(plan.Version, body)
	require.NoError(t, err)
	assert.Equal(t, plan, persisted)
}

func TestPipelineRunPlanV3RetainsReviewedCrossPipelinePrerequisite(t *testing.T) {
	t.Parallel()
	plan := validPipelineRunPlanV3(t)
	plan.Prerequisites = []PipelineRunPrerequisite{{
		Status: "ready", Reason: "Renart observed the producer",
		ConsumerAssetID: "consumer:analytics.orders", ConsumerAssetName: "analytics.orders",
		URI:                "duckdb://warehouse/raw/orders",
		ProducerPipelineID: "raw", ProducerPipelineUUID: "producer",
		ProducerPipelineName: "raw", ProducerAssetID: "producer:raw.orders", ProducerAssetName: "raw.orders",
		Environment:   "default",
		RequiredStart: "2026-07-17T11:00:00Z", RequiredEnd: "2026-07-17T12:00:00Z",
		ExpectedFingerprint: "v3:producer", TargetIdentity: strings.Repeat("a", 64), VarsHash: strings.Repeat("b", 64),
		TargetGeneration: 3, WriterCompletionID: "producer-run", WriterCompletionOrdinal: 1,
		WriterMaterializedAt: "2026-07-17T11:30:00Z", RequiredSeconds: 3600, CoveredSeconds: 3600,
	}}
	plan.Artifact = pipelineRunPlanArtifact(t, plan)

	body, err := marshalPipelineRunPlan(plan)
	require.NoError(t, err)
	persisted, err := unmarshalPipelineRunPlan(plan.Version, body)
	require.NoError(t, err)
	assert.Equal(t, plan.Prerequisites, persisted.Prerequisites)

	persisted.Prerequisites[0].TargetGeneration++
	require.ErrorContains(t, persisted.validate(), "artifact prerequisites")
}

func TestBlockedPipelineRunPlanMayRetainUnreadyPrerequisite(t *testing.T) {
	t.Parallel()
	plan := validPipelineRunPlanV3(t)
	plan.Blocked = true
	plan.Blockers = []string{"producer is not current"}
	plan.Prerequisites = []PipelineRunPrerequisite{{
		Status: "blocked", Reason: "producer is not current",
		ConsumerAssetID: "consumer:analytics.orders", URI: "duckdb://warehouse/raw/orders",
	}}
	plan.Artifact = pipelineRunPlanArtifact(t, plan)
	require.NoError(t, plan.validate())
}

func TestPipelineRunPlanV3AllowsWarehouseRelationClaims(t *testing.T) {
	t.Parallel()

	plan := validPipelineRunPlanV3(t)
	for contractIndex := range plan.ExecutionContracts {
		for claimIndex := range plan.ExecutionContracts[contractIndex].MutationResources.Claims {
			plan.ExecutionContracts[contractIndex].MutationResources.Claims[claimIndex].Kind =
				PipelineRunResourceKindWarehouse
		}
		for claimIndex := range plan.ExecutionContracts[contractIndex].CoordinationResources.Claims {
			plan.ExecutionContracts[contractIndex].CoordinationResources.Claims[claimIndex].Kind =
				PipelineRunResourceKindWarehouse
		}
	}
	for claimIndex := range plan.Resources.Claims {
		plan.Resources.Claims[claimIndex].Kind = PipelineRunResourceKindWarehouse
	}
	plan.Artifact = pipelineRunPlanArtifact(t, plan)

	body, err := marshalPipelineRunPlan(plan)
	require.NoError(t, err)
	persisted, err := unmarshalPipelineRunPlan(plan.Version, body)
	require.NoError(t, err)
	assert.Equal(t, plan, persisted)
}

func TestPipelineRunPlanV3ValidatesDependenciesAndRuntimeContracts(t *testing.T) {
	t.Parallel()

	t.Run("future dependency", func(t *testing.T) {
		plan := validPipelineRunPlanV3(t)
		plan.ExecutionUnits[0].DependencyPositions = []int{1}
		plan.Artifact = pipelineRunPlanArtifact(t, plan)
		require.ErrorContains(t, plan.validate(), "must refer to an earlier")
	})

	t.Run("missing contract", func(t *testing.T) {
		plan := validPipelineRunPlanV3(t)
		plan.ExecutionContracts = plan.ExecutionContracts[:1]
		plan.Artifact = pipelineRunPlanArtifact(t, plan)
		require.ErrorContains(t, plan.validate(), "one execution contract per selected asset")
	})

	t.Run("aggregate resource drift", func(t *testing.T) {
		plan := validPipelineRunPlanV3(t)
		plan.Resources.Claims = nil
		plan.Artifact = pipelineRunPlanArtifact(t, plan)
		require.ErrorContains(t, plan.validate(), "aggregate resources")
	})

	t.Run("non canonical connection key", func(t *testing.T) {
		plan := validPipelineRunPlanV3(t)
		plan.ExecutionContracts[0].ConnectionKeys = []string{"warehouse"}
		plan.Artifact = pipelineRunPlanArtifact(t, plan)
		require.ErrorContains(t, plan.validate(), "lowercase SHA-256")
	})

	t.Run("legacy contract", func(t *testing.T) {
		plan := validPipelineRunPlanV3(t)
		plan.Version = PipelineRunPlanVersionV2
		plan.MaxActiveSteps = 0
		plan.Artifact = pipelineRunPlanArtifact(t, plan)
		require.ErrorContains(t, plan.validate(), "cannot contain execution contracts")
	})
}

func TestPipelineRunPlanRejectsArtifactTamperingAndStageContent(t *testing.T) {
	t.Parallel()

	t.Run("identity", func(t *testing.T) {
		plan := validPipelineRunPlan(t)
		plan.SourceMerkle = strings.Repeat("e", 64)
		require.ErrorContains(t, plan.validate(), "artifact identity")
	})

	t.Run("selection", func(t *testing.T) {
		plan := validPipelineRunPlan(t)
		plan.Selection.DataStateToken = "renart-data-state-v1:" + strings.Repeat("f", 64)
		require.ErrorContains(t, plan.validate(), "artifact selection")
	})

	t.Run("units", func(t *testing.T) {
		plan := validPipelineRunPlan(t)
		plan.ExecutionUnits[0].Reason = "changed"
		require.ErrorContains(t, plan.validate(), "artifact execution units")
	})

	t.Run("content", func(t *testing.T) {
		plan := validPipelineRunPlan(t)
		var artifact map[string]any
		require.NoError(t, json.Unmarshal(plan.Artifact, &artifact))
		assets := artifact["assets"].([]any)
		renders := assets[0].(map[string]any)["renders"].([]any)
		stages := renders[0].(map[string]any)["stages"].([]any)
		stages[0].(map[string]any)["content"] = "select secret"
		plan.Artifact, _ = json.Marshal(artifact)
		require.ErrorContains(t, plan.validate(), "contains stage content")
	})

	t.Run("data state token format", func(t *testing.T) {
		plan := validPipelineRunPlan(t)
		plan.Selection.Mode = "needed"
		plan.Selection.DataStateToken = strings.Repeat("f", 64)
		plan.Artifact = pipelineRunPlanArtifact(t, plan)
		require.ErrorContains(t, plan.validate(), "renart-data-state-v1 token format")
	})
}

func TestPipelineRunPlanAdmissionBinding(t *testing.T) {
	t.Parallel()
	plan := validPipelineRunPlan(t)
	executionTime, err := time.Parse(time.RFC3339Nano, plan.ExecutionTime)
	require.NoError(t, err)
	run := PipelineRun{
		PipelineID: "pipeline-id", Pipeline: "analytics", Trigger: RunTriggerManual,
		Status: RunStatusQueued, ExecutionTime: &executionTime,
		ExpectedSourceMerkle:        plan.SourceMerkle,
		ExpectedConfigurationDigest: plan.ConfigurationDigest,
	}
	spec := manualRunSpec(run, RunSourceWorkingTree, "")
	require.NoError(t, validateRunPlanAdmissionBinding(run, spec, plan))

	wrongTime := plan
	wrongTime.ExecutionTime = "2026-07-17T13:00:00Z"
	require.ErrorContains(t, validateRunPlanAdmissionBinding(run, spec, wrongTime), "artifact identity")

	needed := validPipelineRunPlan(t)
	needed.Selection.Mode = "needed"
	needed.Artifact = pipelineRunPlanArtifact(t, needed)
	require.NoError(t, validateRunPlanAdmissionBinding(run, spec, needed))

	wrongPipeline := validPipelineRunPlan(t)
	wrongPipeline.PipelineID = "other-pipeline"
	wrongPipeline.Artifact = pipelineRunPlanArtifact(t, wrongPipeline)
	require.ErrorContains(t, validateRunPlanAdmissionBinding(run, spec, wrongPipeline), "admitted pipeline")
}

func TestPipelineRunPlanV2BindingUsesPrivateStableIdentityAfterReload(t *testing.T) {
	t.Parallel()
	plan := validPipelineRunPlan(t)
	plan.Version = PipelineRunPlanVersionV2
	plan.Resources = PipelineRunPlanResources{Isolation: PipelineRunResourceIsolationResources}
	plan.Artifact = pipelineRunPlanArtifact(t, plan)
	executionTime, err := time.Parse(time.RFC3339Nano, plan.ExecutionTime)
	require.NoError(t, err)
	run := PipelineRun{
		PipelineID: "pipeline-id", PipelineUUID: plan.PipelineUUID, Pipeline: "analytics",
		Trigger: RunTriggerManual, Status: RunStatusQueued, ExecutionTime: &executionTime,
		ExpectedSourceMerkle:        plan.SourceMerkle,
		ExpectedConfigurationDigest: plan.ConfigurationDigest,
	}
	spec := manualRunSpec(run, RunSourceWorkingTree, "")
	require.NoError(t, validateRunPlanAdmissionBinding(run, spec, plan))

	// PipelineUUID deliberately lives in the private RunSpec and durable
	// admission claims, not the public pipeline_runs row. Reloading a run must
	// therefore accept the retained private identity when the public projection
	// no longer carries the UUID.
	run.PipelineUUID = ""
	require.NoError(t, validateRunPlanAdmissionBinding(run, spec, plan))

	run.PipelineUUID = "other-pipeline-uuid"
	require.ErrorContains(t, validateRunPlanAdmissionBinding(run, spec, plan), "stable pipeline identity")
}

func TestPipelineRunPlanAllowsRetainedBlockedPlanWithoutExecutionUnits(t *testing.T) {
	t.Parallel()
	plan := validPipelineRunPlan(t)
	plan.Blocked = true
	plan.Blockers = []string{"analytics.orders cannot be rendered"}
	plan.ExecutionUnits = nil
	plan.Artifact = pipelineRunPlanArtifact(t, plan)
	require.NoError(t, plan.validate())

	plan.Blocked = false
	require.ErrorContains(t, plan.validate(), "requires at least one execution unit")
}

func TestPipelineRunPlanValidatesNeededPreviewDelta(t *testing.T) {
	t.Parallel()

	plan := validPipelineRunPlan(t)
	plan.Selection.Mode = "needed"
	reviewedUnits := append([]PipelineRunExecutionUnit(nil), plan.ExecutionUnits...)
	reviewedUnits = append(reviewedUnits, PipelineRunExecutionUnit{
		AssetID: "pipeline-uuid:analytics.items", AssetName: "analytics.items",
		StartDate: "2026-07-17T11:00:00Z", EndDate: "2026-07-17T12:00:00Z",
		RenderIndex: 1, Reason: "coverage_gap",
	})
	plan.Preview = &PipelineRunPlanPreview{
		PlanID: strings.Repeat("e", 64), DataStateToken: "renart-data-state-v1:" + strings.Repeat("f", 64),
		ExecutionUnits:        reviewedUnits,
		OmittedExecutionUnits: []PipelineRunExecutionUnit{reviewedUnits[1]},
	}
	plan.Artifact = pipelineRunPlanArtifact(t, plan)
	require.NoError(t, plan.validate())

	mismatched := plan
	mismatched.Preview = &PipelineRunPlanPreview{
		PlanID: strings.Repeat("e", 64), DataStateToken: "renart-data-state-v1:" + strings.Repeat("f", 64),
		ExecutionUnits:        reviewedUnits,
		OmittedExecutionUnits: []PipelineRunExecutionUnit{reviewedUnits[0]},
	}
	require.ErrorContains(t, mismatched.validate(), "preview delta")

	empty := plan
	empty.ExecutionUnits = nil
	empty.Preview = &PipelineRunPlanPreview{
		PlanID: strings.Repeat("e", 64), DataStateToken: "renart-data-state-v1:" + strings.Repeat("f", 64),
		ExecutionUnits: reviewedUnits, OmittedExecutionUnits: reviewedUnits,
	}
	empty.Artifact = pipelineRunPlanArtifact(t, empty)
	require.NoError(t, empty.validate())

	selectorNeeded := plan
	selectorNeeded.Selection.Mode = "selector_needed"
	selectorNeeded.Selection.Selector = "tag:daily"
	selectorNeeded.Artifact = pipelineRunPlanArtifact(t, selectorNeeded)
	require.NoError(t, selectorNeeded.validate())
}

func TestPipelineRunPlanValidatesSelectorSelection(t *testing.T) {
	t.Parallel()

	plan := validPipelineRunPlan(t)
	plan.Selection = PipelineRunPlanSelection{Mode: "selector", Selector: "tag:daily,+analytics.orders"}
	plan.Artifact = pipelineRunPlanArtifact(t, plan)
	require.NoError(t, plan.validate())

	plan.Selection.Selector = ""
	plan.Artifact = pipelineRunPlanArtifact(t, plan)
	require.ErrorContains(t, plan.validate(), "requires selector")
}

func validPipelineRunPlan(t testing.TB) PipelineRunPlan {
	t.Helper()
	plan := PipelineRunPlan{
		Version: PipelineRunPlanVersionV1,
		PlanID:  strings.Repeat("a", 64), SourceMerkle: strings.Repeat("b", 64),
		PipelineID: "pipeline-id", PipelineUUID: "pipeline-uuid",
		ConfigurationDigest: strings.Repeat("c", 64),
		ExecutionTime:       "2026-07-17T12:00:00Z",
		Selection:           PipelineRunPlanSelection{Mode: "all"},
		ExecutionUnits: []PipelineRunExecutionUnit{{
			AssetID: "pipeline-uuid:analytics.orders", AssetName: "analytics.orders",
			StartDate: "2026-07-17T11:00:00Z", EndDate: "2026-07-17T12:00:00Z",
			RenderIndex: 0, Reason: "selected_all",
		}},
	}
	plan.Artifact = pipelineRunPlanArtifact(t, plan)
	return plan
}

func validPipelineRunPlanV3(t testing.TB) PipelineRunPlan {
	t.Helper()
	plan := validPipelineRunPlan(t)
	plan.Version = PipelineRunPlanVersionV3
	plan.MaxActiveSteps = 2
	plan.ExecutionUnits = append(plan.ExecutionUnits, PipelineRunExecutionUnit{
		AssetID: "pipeline-uuid:analytics.items", AssetName: "analytics.items",
		StartDate: "2026-07-17T11:00:00Z", EndDate: "2026-07-17T12:00:00Z",
		RenderIndex: 0, Reason: "selected_all", DependencyPositions: []int{0},
	})
	localIdentity := strings.Repeat("d", 64)
	connectionKey := strings.Repeat("e", 64)
	plan.Resources = PipelineRunPlanResources{
		Isolation: PipelineRunResourceIsolationResources,
		Claims: []PipelineRunResourceClaim{{
			Kind: PipelineRunResourceKindLocalFile, Identity: localIdentity,
		}},
	}
	plan.ExecutionContracts = []PipelineRunExecutionContract{
		{
			AssetID: "pipeline-uuid:analytics.items", AssetName: "analytics.items",
			ConnectionKeys: []string{connectionKey},
			MutationResources: PipelineRunPlanResources{
				Isolation: PipelineRunResourceIsolationResources,
				Claims:    []PipelineRunResourceClaim{},
			},
			CoordinationResources: PipelineRunPlanResources{
				Isolation: PipelineRunResourceIsolationResources,
				Claims:    []PipelineRunResourceClaim{},
			},
		},
		{
			AssetID: "pipeline-uuid:analytics.orders", AssetName: "analytics.orders",
			ConnectionKeys: []string{connectionKey},
			MutationResources: PipelineRunPlanResources{
				Isolation: PipelineRunResourceIsolationResources,
				Claims: []PipelineRunResourceClaim{{
					Kind: PipelineRunResourceKindLocalFile, Identity: localIdentity,
				}},
			},
			CoordinationResources: PipelineRunPlanResources{
				Isolation: PipelineRunResourceIsolationResources,
				Claims: []PipelineRunResourceClaim{{
					Kind: PipelineRunResourceKindLocalFile, Identity: localIdentity,
				}},
			},
		},
	}
	plan.Artifact = pipelineRunPlanArtifact(t, plan)
	return plan
}

func pipelineRunPlanArtifact(t testing.TB, plan PipelineRunPlan) json.RawMessage {
	t.Helper()
	artifact := map[string]any{
		"id":          plan.PlanID,
		"status":      map[bool]string{true: "blocked", false: "ready"}[plan.Blocked],
		"pipeline_id": plan.PipelineID, "pipeline_uuid": plan.PipelineUUID,
		"source": map[string]any{"merkle_root": plan.SourceMerkle},
		"context": map[string]any{
			"execution_time": plan.ExecutionTime, "configuration_digest": plan.ConfigurationDigest,
		},
		"selection":       plan.Selection,
		"execution_units": plan.ExecutionUnits,
		"readiness": map[string]any{
			"blockers": func() []map[string]string {
				result := make([]map[string]string, 0, len(plan.Blockers))
				for _, message := range plan.Blockers {
					result = append(result, map[string]string{"message": message})
				}
				return result
			}(),
		},
		"assets": []any{map[string]any{
			"id": "pipeline-uuid:analytics.orders", "name": "analytics.orders",
			"renders": []any{map[string]any{
				"stages": []any{map[string]any{"kind": "query", "content": ""}},
			}},
		}},
	}
	if plan.Version >= PipelineRunPlanVersionV2 {
		artifact["resources"] = plan.Resources
	}
	if plan.Version >= PipelineRunPlanVersionV3 {
		artifact["context"].(map[string]any)["max_active_steps"] = plan.MaxActiveSteps
		artifact["execution_contracts"] = plan.ExecutionContracts
		artifact["prerequisites"] = plan.Prerequisites
	}
	body, err := json.Marshal(artifact)
	require.NoError(t, err)
	return body
}
