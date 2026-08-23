package execution

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

func TestNormalizePlanSelectionRejectsMixedCoordinates(t *testing.T) {
	_, err := NormalizePlanSelection(PlanSelectionRequest{
		Mode: PlanSelectionAll, AssetName: "analytics.orders",
	})
	require.ErrorContains(t, err, "not valid")
}
