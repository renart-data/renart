package execution

import (
	"renart/internal/web/policy"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloneExecutionContractPreservesAccessWithoutAliasing(t *testing.T) {
	original := ExecutionContract{
		AssetID: "asset", AssetName: "analytics.orders", AccessPolicyIdentity: "revision",
		AccessRequirements:    []AccessRequirement{{ConnectionKey: "connection", Effect: policy.Read, Operation: "sql"}},
		ConnectionKeys:        []string{"connection"},
		MutationResources:     Resources{Isolation: PlanResourceIsolationResources, Claims: []ResourceClaim{{Kind: "warehouse", Identity: "target"}}},
		CoordinationResources: Resources{Isolation: PlanResourceIsolationResources, Claims: []ResourceClaim{{Kind: "warehouse", Identity: "lease"}}},
	}
	cloned := CloneExecutionContract(original)
	require.Equal(t, original, cloned)
	cloned.AccessPolicyIdentity = "changed"
	cloned.AccessRequirements[0].Effect = policy.Write
	cloned.ConnectionKeys[0] = "changed"
	cloned.MutationResources.Claims[0].Identity = "changed"
	cloned.CoordinationResources.Claims[0].Identity = "changed"
	assert.Equal(t, "revision", original.AccessPolicyIdentity)
	assert.Equal(t, policy.Read, original.AccessRequirements[0].Effect)
	assert.Equal(t, "connection", original.ConnectionKeys[0])
	assert.Equal(t, "target", original.MutationResources.Claims[0].Identity)
	assert.Equal(t, "lease", original.CoordinationResources.Claims[0].Identity)
}

func TestCanonicalResourcesTrimsSortsAndDeduplicates(t *testing.T) {
	resources := CanonicalResources(Resources{
		Isolation: " resources ",
		Claims: []ResourceClaim{
			{Kind: "warehouse", Identity: " b "},
			{Kind: " warehouse ", Identity: "a"},
			{Kind: "warehouse", Identity: "a"},
		},
	})

	assert.Equal(t, Resources{
		Isolation: PlanResourceIsolationResources,
		Claims: []ResourceClaim{
			{Kind: "warehouse", Identity: "a"},
			{Kind: "warehouse", Identity: "b"},
		},
	}, resources)
}

func TestPlanIdentityIgnoresPresentationOnlyFields(t *testing.T) {
	plan := Plan{
		PipelineUUID: "pipeline-uuid",
		Source:       RenderSource{Kind: PlanSourceWorkingTree, MerkleRoot: "source"},
		Context:      PlanContext{Environment: "default"},
		Selection:    PlanSelection{Mode: PlanSelectionAll},
		Resources:    PipelineExclusiveResources(),
	}
	first := PlanID(plan)
	plan.ID = "presentation-id"
	plan.Status = PlanStatusBlocked
	plan.PipelineName = "renamed"
	plan.Summary.Warnings = 2
	plan.Assets = []PlanAsset{{Name: "ignored"}}
	require.Equal(t, first, PlanID(plan))
}

func TestPlanIdentityIncludesSemanticImpactDigest(t *testing.T) {
	plan := Plan{
		PipelineUUID: "pipeline-uuid",
		Source:       RenderSource{Kind: PlanSourceWorkingTree, MerkleRoot: "source"},
		Context:      PlanContext{Environment: "default"},
		Selection:    PlanSelection{Mode: PlanSelectionAll},
		Resources:    PipelineExclusiveResources(),
		SemanticImpact: &SemanticImpactReport{
			Version: SemanticImpactVersion,
			Digest:  "v1:first",
			Status:  SemanticImpactStatusAvailable,
		},
	}
	first := PlanID(plan)
	plan.SemanticImpact.Digest = "v1:second"
	require.NotEqual(t, first, PlanID(plan))
}

func TestNormalizePlanSelectionRejectsMixedCoordinates(t *testing.T) {
	_, err := NormalizePlanSelection(PlanSelectionRequest{
		Mode: PlanSelectionAll, AssetName: "analytics.orders",
	})
	require.ErrorContains(t, err, "not valid")
}
