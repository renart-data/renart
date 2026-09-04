package execution

import (
	"context"
	"testing"
	"time"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/web/apperror"
	"renart/internal/web/staleness"
	webtypecheck "renart/internal/web/typecheck"
)

func TestPlannerOwnsReviewedPlanWorkflow(t *testing.T) {
	asset := &pipeline.Asset{Name: "analytics.orders", Type: pipeline.AssetTypeDuckDBQuery}
	source := &plannerTestSource{parsed: &pipeline.Pipeline{
		LegacyID: "pipeline-uuid", Name: "analytics", Assets: []*pipeline.Asset{asset},
	}}
	session := &plannerTestSession{}
	planner := NewPlanner(PlannerDependencies{
		ResolvePipelineUUID: func(id string) (string, bool) { return "pipeline-uuid", id == "pipeline-id" },
		LoadConfiguration: func(string) (PlannerConfiguration, error) {
			return plannerTestConfiguration{}, nil
		},
		ResolveSource: func(context.Context, string, string, PlanSourceRequest, map[string]any) (PlannerSource, bool, *apperror.Error) {
			return source, false, nil
		},
		OpenSession: func(context.Context, PlannerSource, PlannerConfiguration, PlannerSessionInput) (PlannerSession, *apperror.Error) {
			return session, nil
		},
		ResolveVariables: func(*pipeline.Pipeline, map[string]any, string) PlannerVariableContext {
			return PlannerVariableContext{Digest: "variables"}
		},
		IsExecutable:        func(*pipeline.Asset) bool { return true },
		EffectiveSensorMode: func(string, bool) string { return "once" },
		Staleness: plannerTestStaleness{snapshot: staleness.Snapshot{
			DataStateToken: "state-token",
			Assets:         []staleness.AssetStatus{{AssetName: asset.Name, Status: staleness.StatusNeverBuilt}},
		}},
		Now: func() time.Time { return time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC) },
	})

	plan, apiErr := planner.Plan(context.Background(), "pipeline-id", PlanRequest{
		Selection: PlanSelectionRequest{Mode: PlanSelectionAll},
	})
	require.Nil(t, apiErr)
	assert.Equal(t, PlanStatusReady, plan.Status)
	assert.Equal(t, "state-token", plan.Selection.DataStateToken)
	assert.Equal(t, "variables", plan.Context.VariablesDigest)
	assert.Equal(t, "configuration", plan.Context.ConfigurationDigest)
	assert.Equal(t, "once", plan.Context.SensorMode)
	require.Len(t, plan.Assets, 1)
	require.Len(t, plan.ExecutionUnits, 1)
	require.Len(t, plan.ExecutionContracts, 1)
	assert.NotEmpty(t, plan.ID)
	assert.True(t, source.closed)
	assert.True(t, source.verified)
	assert.True(t, session.closed)
}

func TestPlannerAddsSemanticImpactToDeploymentReview(t *testing.T) {
	asset := &pipeline.Asset{Name: "analytics.revenue", Type: pipeline.AssetTypeDuckDBQuery}
	source := &plannerTestSource{parsed: &pipeline.Pipeline{
		LegacyID: "pipeline-uuid", Name: "analytics", Assets: []*pipeline.Asset{asset},
	}}
	semanticImpact := CompareSemanticImpact(
		"snapshot-7",
		[]SemanticAssetSnapshot{semanticTestAsset("analytics.revenue", "source:a", "canonical:a", true, "HUGEINT")},
		[]SemanticAssetSnapshot{semanticTestAsset("analytics.revenue", "source:a", "canonical:a", true, "DOUBLE")},
	)
	session := &plannerTestSemanticSession{
		plannerTestSession: &plannerTestSession{},
		semanticImpact:     semanticImpact,
	}
	planner := NewPlanner(PlannerDependencies{
		ResolvePipelineUUID: func(id string) (string, bool) { return "pipeline-uuid", id == "pipeline-id" },
		LoadConfiguration: func(string) (PlannerConfiguration, error) {
			return plannerTestConfiguration{}, nil
		},
		ResolveSource: func(context.Context, string, string, PlanSourceRequest, map[string]any) (PlannerSource, bool, *apperror.Error) {
			return source, false, nil
		},
		OpenSession: func(context.Context, PlannerSource, PlannerConfiguration, PlannerSessionInput) (PlannerSession, *apperror.Error) {
			return session, nil
		},
		ResolveVariables: func(*pipeline.Pipeline, map[string]any, string) PlannerVariableContext {
			return PlannerVariableContext{Digest: "variables"}
		},
		IsExecutable:        func(*pipeline.Asset) bool { return true },
		EffectiveSensorMode: func(string, bool) string { return "once" },
		Staleness:           plannerTestStaleness{},
		Now: func() time.Time {
			return time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
		},
	})

	plan, apiErr := planner.Plan(context.Background(), "pipeline-id", PlanRequest{
		Purpose: PlanPurposeDeployment, Selection: PlanSelectionRequest{Mode: PlanSelectionAll},
	})
	require.Nil(t, apiErr)
	require.NotNil(t, plan.SemanticImpact)
	assert.Equal(t, semanticImpact.Digest, plan.SemanticImpact.Digest)
	assert.Equal(t, semanticImpact.Digest, ReviewedIdentityFromPlan(plan).SemanticImpactDigest)
	assert.Equal(t, PlanStatusWarning, plan.Status)
	require.Len(t, plan.Readiness.Warnings, 1)
	assert.Equal(t, "semantic_impact_detected", plan.Readiness.Warnings[0].Code)
}

type plannerTestConfiguration struct{}

func (plannerTestConfiguration) EnvironmentName() string { return "default" }
func (plannerTestConfiguration) SchemaPrefix() string    { return "" }
func (plannerTestConfiguration) InitialIdentity() ConfigurationIdentity {
	return ConfigurationIdentity{Digest: "initial", Fidelity: "exact"}
}
func (plannerTestConfiguration) BindSelection(*pipeline.Pipeline, []*pipeline.Asset, []string) (ConfigurationIdentity, *apperror.Error) {
	return ConfigurationIdentity{Digest: "configuration", Fidelity: "exact"}, nil
}

type plannerTestSource struct {
	parsed   *pipeline.Pipeline
	closed   bool
	verified bool
}

func (s *plannerTestSource) Pipeline() *pipeline.Pipeline { return s.parsed }
func (s *plannerTestSource) Identity() RenderSource {
	return RenderSource{Kind: PlanSourceWorkingTree, MerkleRoot: "source"}
}
func (s *plannerTestSource) Verify() *apperror.Error { s.verified = true; return nil }
func (s *plannerTestSource) Close()                  { s.closed = true }

type plannerTestSession struct{ closed bool }

func (s *plannerTestSession) CodeChecks() webtypecheck.Report {
	return webtypecheck.Report{Assets: []webtypecheck.Asset{}}
}
func (s *plannerTestSession) ApplyPrerequisites(context.Context, *Plan, []SelectedPlanAsset) {}
func (s *plannerTestSession) PlanAsset(_ context.Context, item SelectedPlanAsset, _ staleness.AssetStatus, _ TimeWindow) (PlannedAssetResult, *apperror.Error) {
	return PlannedAssetResult{
		Asset:          PlanAsset{ID: "pipeline-uuid:" + item.Asset.Name, Name: item.Asset.Name, Type: string(item.Asset.Type)},
		ExecutionUnits: []PlanExecutionUnit{{AssetID: "pipeline-uuid:" + item.Asset.Name, AssetName: item.Asset.Name}},
	}, nil
}
func (s *plannerTestSession) BindExecutionContracts(context.Context, []PlanAsset) ([]ExecutionContract, *apperror.Error) {
	return []ExecutionContract{{
		AssetID: "pipeline-uuid:analytics.orders", AssetName: "analytics.orders",
		MutationResources:     Resources{Isolation: PlanResourceIsolationResources, Claims: []ResourceClaim{}},
		CoordinationResources: Resources{Isolation: PlanResourceIsolationResources, Claims: []ResourceClaim{}},
	}}, nil
}
func (s *plannerTestSession) Close() { s.closed = true }

type plannerTestSemanticSession struct {
	*plannerTestSession
	semanticImpact SemanticImpactReport
}

func (s *plannerTestSemanticSession) SemanticImpact(context.Context) SemanticImpactReport {
	return s.semanticImpact
}

type plannerTestStaleness struct{ snapshot staleness.Snapshot }

func (s plannerTestStaleness) Evaluate(context.Context, staleness.Selection, *pipeline.Pipeline) (staleness.Snapshot, error) {
	return s.snapshot, nil
}
