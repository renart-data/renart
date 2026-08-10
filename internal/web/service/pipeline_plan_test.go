package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/web/dependencygraph"
	"renart/internal/web/fingerprint"
	"renart/internal/web/identity"
	"renart/internal/web/matlog"
	"renart/internal/web/policy"
	"renart/internal/web/scheduler"
	"renart/internal/web/snapshot"
	"renart/internal/web/staleness"
)

type pipelinePlanStalenessStub struct {
	snapshot staleness.Snapshot
	err      error
	parsed   *pipeline.Pipeline
	selectn  staleness.Selection
}

func (s *pipelinePlanStalenessStub) Evaluate(
	_ context.Context,
	selection staleness.Selection,
	parsed *pipeline.Pipeline,
) (staleness.Snapshot, error) {
	s.parsed = parsed
	s.selectn = selection
	return s.snapshot, s.err
}

type emptyPipelinePlanSnapshotStore struct{}

func (emptyPipelinePlanSnapshotStore) Latest(context.Context, string) (*snapshot.Snapshot, error) {
	return nil, nil
}

func (emptyPipelinePlanSnapshotStore) ValidateMetadata(context.Context, string, string) (snapshot.Snapshot, error) {
	return snapshot.Snapshot{}, os.ErrNotExist
}

func (emptyPipelinePlanSnapshotStore) MaterializeForPipelineExecution(context.Context, string, string, string) error {
	return os.ErrNotExist
}

func TestPipelinePlanNeededSelectionRendersExactGapWithoutStageContent(t *testing.T) {
	parsed, root := writeTypeCheckWorkspace(t, `
id: pipeline-uuid
name: analytics
schedule: daily
max_active_steps: 3
`, map[string]string{
		"up.sql": `
/* @bruin
name: analytics.up
type: duckdb.sql
materialization:
  type: table
columns:
  - name: id
    type: integer
@bruin */
select 1 as id
`,
		"down.sql": `
/* @bruin
name: analytics.down
type: duckdb.sql
depends:
  - analytics.up
materialization:
  type: table
columns:
  - name: id
    type: integer
@bruin */
select id from analytics.up
`,
	})
	require.Equal(t, "pipeline-uuid", parsed.LegacyID)
	start := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	stale := &pipelinePlanStalenessStub{snapshot: staleness.Snapshot{
		DataStateToken: "data-token",
		Assets: []staleness.AssetStatus{
			{AssetID: "pipeline-uuid:analytics.up", AssetName: "analytics.up", Status: staleness.StatusFresh},
			{
				AssetID: "pipeline-uuid:analytics.down", AssetName: "analytics.down", Status: staleness.StatusPartial,
				Gaps: []staleness.Interval{{Start: start.Add(6 * time.Hour), End: start.Add(12 * time.Hour)}},
			},
		},
	}}
	service := newTestPipelinePlanService(root, stale, nil)

	plan, apiErr := service.Plan(context.Background(), EncodeID("analytics"), PipelinePlanRequest{
		Environment:   "default",
		StartDate:     start.Format(time.RFC3339),
		EndDate:       end.Format(time.RFC3339),
		ExecutionTime: start.Add(18 * time.Hour).Format(time.RFC3339),
		Selection:     PipelinePlanSelectionRequest{Mode: PipelinePlanSelectionNeeded},
	})
	require.Nil(t, apiErr)
	assert.Equal(t, PipelinePlanStatusReady, plan.Status, plan.Readiness.Warnings)
	assert.NotContains(t, pipelinePlanIssueCodes(plan.Readiness.Warnings), "asset_render_partial")
	assert.Equal(t, PipelinePlanSourceWorkingTree, plan.Source.Kind)
	assert.NotEmpty(t, plan.Source.MerkleRoot)
	assert.Equal(t, "data-token", plan.Selection.DataStateToken)
	require.Len(t, plan.Assets, 1)
	assert.Equal(t, "analytics.down", plan.Assets[0].Name)
	assert.Equal(t, []string{"uncovered_interval"}, plan.Assets[0].InclusionReasons)
	require.Len(t, plan.Assets[0].Renders, 1)
	assert.Equal(t, start.Add(6*time.Hour).Format(time.RFC3339), plan.Assets[0].Renders[0].StartDate)
	assert.NotEmpty(t, plan.Assets[0].Renders[0].Stages)
	for _, stage := range plan.Assets[0].Renders[0].Stages {
		assert.Empty(t, stage.Content)
	}
	require.Len(t, plan.ExecutionUnits, 1)
	assert.Equal(t, "pipeline-uuid:analytics.down", plan.ExecutionUnits[0].AssetID)
	assert.Equal(t, "uncovered_interval", plan.ExecutionUnits[0].Reason)
	assert.Equal(t, 3, plan.Context.MaxActiveSteps)
	require.Len(t, plan.ExecutionContracts, 1)
	assert.Equal(t, "analytics.down", plan.ExecutionContracts[0].AssetName)
	require.Len(t, plan.ExecutionContracts[0].ConnectionKeys, 1)
	assert.Len(t, plan.ExecutionContracts[0].ConnectionKeys[0], 64)
	assert.Equal(t, "default", stale.selectn.Environment)
	assert.NotNil(t, stale.parsed)
	assert.NotEmpty(t, plan.ID)
}

func TestPipelineDeploymentPlanKeepsSourceAssetsWithoutRenderingThemAsExecutions(t *testing.T) {
	t.Parallel()

	_, root := writeTypeCheckWorkspace(t, `
id: pipeline-uuid
name: analytics
default_connections:
  duckdb: duckdb-default
`, map[string]string{
		"accounts.asset.yml": `
name: public.accounts
type: duckdb.source
connection: duckdb-default
columns:
  - name: id
    type: bigint
`,
		"report.sql": `
/* @bruin
name: analytics.report
type: duckdb.sql
depends:
  - public.accounts
materialization:
  type: table
columns:
  - name: id
    type: bigint
@bruin */
select id from public.accounts
`,
	})
	service := newTestPipelinePlanService(root, &pipelinePlanStalenessStub{}, nil)

	plan, apiErr := service.Plan(context.Background(), EncodeID("analytics"), PipelinePlanRequest{
		Purpose:     PipelinePlanPurposeDeployment,
		Environment: "default",
		Selection:   PipelinePlanSelectionRequest{Mode: PipelinePlanSelectionAll},
	})

	require.Nil(t, apiErr)
	assert.Equal(t, PipelinePlanStatusReady, plan.Status, plan.Readiness)
	assert.NotContains(t, pipelinePlanIssueCodes(plan.Readiness.Blockers), "asset_render_failed")
	assert.NotContains(t, pipelinePlanIssueCodes(plan.Readiness.Warnings), "asset_render_partial")
	require.Len(t, plan.Assets, 2)
	assert.Equal(t, "public.accounts", plan.Assets[0].Name)
	assert.Empty(t, plan.Assets[0].Renders)
	assert.Equal(t, assetRenderTargetKindNone, plan.Assets[0].Target.Kind)
	assert.Equal(t, assetWriteResourceNone, plan.Assets[0].Target.WriteResource.Kind)
	require.Len(t, plan.ExecutionUnits, 1)
	assert.Equal(t, "analytics.report", plan.ExecutionUnits[0].AssetName)
	require.Len(t, plan.ExecutionContracts, 1)
	assert.Equal(t, "analytics.report", plan.ExecutionContracts[0].AssetName)
}

func TestPipelinePlanBlocksFullCrossPipelineDependenciesWhenEvidenceIsUnavailable(t *testing.T) {
	t.Parallel()

	_, root := writeTypeCheckWorkspace(t, `
id: pipeline-uuid
name: analytics
default_connections:
  duckdb: duckdb-default
`, map[string]string{
		"full.sql": `
/* @bruin
name: analytics.full
type: duckdb.sql
depends:
  - uri: duckdb://warehouse/raw/orders
@bruin */
select 1 as id
`,
		"symbolic.sql": `
/* @bruin
name: analytics.symbolic
type: duckdb.sql
depends:
  - uri: duckdb://warehouse/raw/customers
    mode: symbolic
@bruin */
select 1 as id
`,
	})
	service := newTestPipelinePlanService(root, &pipelinePlanStalenessStub{}, nil)

	plan, apiErr := service.Plan(context.Background(), EncodeID("analytics"), PipelinePlanRequest{
		Purpose:     PipelinePlanPurposeExecution,
		Environment: "default",
		Selection:   PipelinePlanSelectionRequest{Mode: PipelinePlanSelectionAll},
	})

	require.Nil(t, apiErr)
	assert.Equal(t, PipelinePlanStatusBlocked, plan.Status)
	issues := plan.Readiness.Blockers
	require.Len(t, issues, 1)
	assert.Equal(t, pipelinePlanCodeCrossPipelinePrerequisiteNotReady, issues[0].Code)
	assert.Equal(t, "analytics.full", issues[0].AssetName)
	assert.Contains(t, issues[0].Message, "duckdb://warehouse/raw/orders")
}

func TestPipelinePlanWaitsOnlyForRetryableCrossPipelineReadiness(t *testing.T) {
	t.Parallel()
	plan := PipelinePlan{
		Status: PipelinePlanStatusBlocked,
		Prerequisites: []PipelinePlanPrerequisite{{
			Status: PipelinePlanPrerequisiteBlocked,
		}},
		Readiness: PipelinePlanReadiness{Blockers: []PipelinePlanIssue{{
			Code: pipelinePlanCodeCrossPipelinePrerequisiteNotReady,
		}}},
	}
	assert.True(t, PipelinePlanWaitsForCrossPipelinePrerequisites(plan))
	plan.Readiness.Blockers[0].Code = pipelinePlanCodeCrossPipelineDeploymentBindingInvalid
	assert.False(t, PipelinePlanWaitsForCrossPipelinePrerequisites(plan))
	plan.Readiness.Blockers[0].Code = pipelinePlanCodeCrossPipelinePrerequisiteNotReady
	plan.Readiness.Blockers = append(plan.Readiness.Blockers, PipelinePlanIssue{Code: "asset_render_failed"})
	assert.False(t, PipelinePlanWaitsForCrossPipelinePrerequisites(plan))
}

func TestPrerequisiteCoverageUnionsMixedProducerIntervalsAndRejectsGap(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)
	middle := start.Add(6 * time.Hour)
	end := start.Add(24 * time.Hour)
	covered, complete := prerequisiteCoverage([]matlog.CoverageRow{
		{IntervalStart: &start, IntervalEnd: &middle},
		{IntervalStart: &middle, IntervalEnd: &end},
	}, start, end)
	assert.True(t, complete)
	assert.Equal(t, 24*time.Hour, covered)

	gapStart := middle.Add(time.Hour)
	covered, complete = prerequisiteCoverage([]matlog.CoverageRow{
		{IntervalStart: &start, IntervalEnd: &middle},
		{IntervalStart: &gapStart, IntervalEnd: &end},
	}, start, end)
	assert.False(t, complete)
	assert.Equal(t, 6*time.Hour, covered)
}

func TestPipelinePlanAcceptsOnlyCurrentRenartObservedCrossPipelineProducer(t *testing.T) {
	_, root := writeTypeCheckWorkspace(t, `
id: consumer-uuid
name: analytics
default_connections:
  duckdb: duckdb-default
`, map[string]string{
		"orders.sql": `
/* @bruin
name: analytics.orders
type: duckdb.sql
materialization:
  type: table
depends:
  - uri: duckdb://warehouse/raw/orders
@bruin */
select id from raw.orders
`,
	})

	producerAsset := &pipeline.Asset{
		Name: "raw.orders", URI: "duckdb://warehouse/raw/orders", Type: pipeline.AssetTypeDuckDBQuery,
		ExecutableFile: pipeline.ExecutableFile{
			Path: filepath.Join(root, "raw", "assets", "orders.sql"), Content: "select 1 as id",
		},
		Materialization: pipeline.Materialization{Type: pipeline.MaterializationTypeTable},
	}
	producer := &pipeline.Pipeline{
		LegacyID: "producer-uuid", Name: "raw",
		DefinitionFile:     pipeline.DefinitionFile{Path: filepath.Join(root, "raw", "pipeline.yml")},
		Assets:             []*pipeline.Asset{producerAsset},
		DefaultConnections: map[string]string{"duckdb": "duckdb-default"},
	}
	require.NoError(t, os.MkdirAll(filepath.Dir(producerAsset.ExecutableFile.Path), 0o755))
	require.NoError(t, os.WriteFile(producer.DefinitionFile.Path, []byte(`
id: producer-uuid
name: raw
default_connections:
  duckdb: duckdb-default
`), 0o644))
	require.NoError(t, os.WriteFile(producerAsset.ExecutableFile.Path, []byte(`
/* @bruin
name: raw.orders
uri: duckdb://warehouse/raw/orders
type: duckdb.sql
materialization:
  type: table
@bruin */
select 1 as id
`), 0o644))

	schedStore, err := scheduler.OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = schedStore.Close() })
	materializations := matlog.NewStore(schedStore.DB())
	engine := fingerprint.NewEngine()
	var consumer *pipeline.Pipeline
	resolveGraph := func(_ context.Context, overrides map[string]*pipeline.Pipeline) (dependencygraph.Graph, error) {
		consumer = overrides["consumer-uuid"]
		require.NotNil(t, consumer)
		return dependencygraph.Resolve([]dependencygraph.PipelineInput{
			{UUID: producer.LegacyID, ID: EncodeID("raw"), Name: producer.Name, Parsed: producer},
			{UUID: consumer.LegacyID, ID: EncodeID("analytics"), Name: consumer.Name, Parsed: consumer},
		}), nil
	}

	cfg, err := loadSelectedConfigReadOnlyFS(afero.NewOsFs(), filepath.Join(root, ".bruin.yml"), "default")
	require.NoError(t, err)
	graph := dependencygraph.Resolve([]dependencygraph.PipelineInput{
		{UUID: producer.LegacyID, ID: EncodeID("raw"), Name: producer.Name, Parsed: producer},
	})
	producerResults, err := engine.WorkspaceDAG(graph, map[string]fingerprint.Vars{
		producer.LegacyID: fingerprint.EffectiveVars(producer, nil),
	})
	require.NoError(t, err)
	producerID := identity.AssetID(producer.LegacyID, producerAsset.Name)
	producerTarget := resolveAssetPhysicalTarget(root, &directPipelineInfo{
		Pipeline: producer, Asset: producerAsset, Config: cfg,
	})
	require.Equal(t, AssetRenderFidelityExact, producerTarget.Fidelity)
	require.NoError(t, materializations.Record(context.Background(), matlog.Materialization{
		AssetID: producerID, Environment: "default",
		Fingerprint:    string(producerResults[producerID].FP),
		OwnContent:     string(producerResults[producerID].OwnContent),
		VarsHash:       fingerprint.AllVarsHash(fingerprint.EffectiveVars(producer, nil)),
		TargetIdentity: producerTarget.Identity,
		RunID:          "producer-run", CompletionID: "producer-run", CompletionOrdinal: 0,
		MaterializedAt: time.Date(2026, 7, 17, 11, 0, 0, 0, time.UTC),
	}))

	planner := NewPipelinePlanService(PipelinePlanDependencies{
		WorkspaceRoot: root, ConfigPath: filepath.Join(root, ".bruin.yml"),
		Staleness: &pipelinePlanStalenessStub{}, DependencyGraph: resolveGraph,
		Fingerprints: engine, Materializations: materializations,
		ResolvePipelineUUID: func(pipelineID string) (string, bool) {
			return "consumer-uuid", pipelineID == EncodeID("analytics")
		},
		Now: func() time.Time { return time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC) },
	})
	request := PipelinePlanRequest{
		Purpose: PipelinePlanPurposeExecution, Environment: "default",
		Selection: PipelinePlanSelectionRequest{Mode: PipelinePlanSelectionAll},
	}
	plan, apiErr := planner.Plan(context.Background(), EncodeID("analytics"), request)
	require.Nil(t, apiErr)
	assert.NotEqual(t, PipelinePlanStatusBlocked, plan.Status)
	require.Len(t, plan.Prerequisites, 1)
	assert.Equal(t, PipelinePlanPrerequisiteReady, plan.Prerequisites[0].Status)
	assert.Equal(t, producerID, plan.Prerequisites[0].ProducerAssetID)
	assert.Equal(t, producerTarget.Identity, plan.Prerequisites[0].TargetIdentity)

	producerAsset.ExecutableFile.Content = "select 1 as id, 2 as version"
	changed, apiErr := planner.Plan(context.Background(), EncodeID("analytics"), request)
	require.Nil(t, apiErr)
	assert.Equal(t, PipelinePlanStatusBlocked, changed.Status)
	require.Len(t, changed.Prerequisites, 1)
	assert.Equal(t, PipelinePlanPrerequisiteBlocked, changed.Prerequisites[0].Status)
	assert.Contains(t, changed.Prerequisites[0].Reason, "does not match")
}

func TestDeploymentPlanResolvesPathNamedCrossPipelineAPIProducer(t *testing.T) {
	_, root := writeTypeCheckWorkspace(t, `
id: pipeline-uuid
name: renart-marketing
default_connections:
  duckdb: duckdb-default
`, map[string]string{
		"report.sql": `
/* @bruin
name: renart_marketing.report
type: duckdb.sql
depends:
  - uri: blub://asdf/asdf/another_asset
materialization:
  type: view
@bruin */
select * from example.another_asset
`,
	})

	producerRoot := filepath.Join(root, "example")
	producerAssetPath := filepath.Join(producerRoot, "assets", "example", "another_asset.asset.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(producerAssetPath), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(producerRoot, "pipeline.yml"), []byte(`
id: producer-uuid
name: example
default_connections:
  duckdb: duckdb-default
`), 0o644))
	// The API asset intentionally relies on Bruin's path-derived name. The
	// filesystem-only SQL index used by planning did not represent this asset,
	// even though Monaco and the interactive type checker did.
	require.NoError(t, os.WriteFile(producerAssetPath, []byte(`
type: api
connection: duckdb-default
uri: blub://asdf/asdf/another_asset
parameters:
  request:
    url: https://api.example.com/items
    method: GET
  response:
    records_path: data
columns:
  - name: id
    type: bigint
materialization:
  type: table
  strategy: create+replace
`), 0o644))

	plan, apiErr := newTestPipelinePlanService(root, &pipelinePlanStalenessStub{}, nil).Plan(
		context.Background(),
		EncodeID("analytics"),
		PipelinePlanRequest{
			Purpose:   PipelinePlanPurposeDeployment,
			Selection: PipelinePlanSelectionRequest{Mode: PipelinePlanSelectionAll},
		},
	)
	require.Nil(t, apiErr)
	for _, asset := range plan.Readiness.CodeChecks.Assets {
		for _, finding := range asset.Findings {
			assert.NotEqual(t, "unresolved-relation", finding.Code, finding.Message)
		}
	}
	for _, blocker := range plan.Readiness.Blockers {
		assert.NotContains(t, blocker.Message, "Unresolved table: example.another_asset")
	}
}

func TestSnapshotPipelinePlanBindsExactProducerDeploymentAndRenartEvidence(t *testing.T) {
	consumer, root := writeTypeCheckWorkspace(t, `
id: consumer-uuid
name: analytics
schedule: daily
default_connections:
  duckdb: duckdb-default
`, map[string]string{
		"orders.sql": `
/* @bruin
name: analytics.orders
type: duckdb.sql
materialization:
  type: table
depends:
  - uri: duckdb://warehouse/raw/orders
@bruin */
select id from raw.orders
`,
	})
	producerRoot := filepath.Join(root, "raw")
	require.NoError(t, os.MkdirAll(filepath.Join(producerRoot, "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(producerRoot, "pipeline.yml"), []byte(`
id: producer-uuid
name: raw
schedule: daily
default_connections:
  duckdb: duckdb-default
`), 0o644))
	producerAssetPath := filepath.Join(producerRoot, "assets", "orders.sql")
	require.NoError(t, os.WriteFile(producerAssetPath, []byte(`
/* @bruin
name: raw.orders
uri: duckdb://warehouse/raw/orders
type: duckdb.sql
materialization:
  type: table
@bruin */
select 1 as id
`), 0o644))
	producer, err := NewRenartPipelineBuilder(afero.NewOsFs()).
		CreatePipelineFromPath(context.Background(), producerRoot, pipeline.WithMutate())
	require.NoError(t, err)
	require.Len(t, producer.Assets, 1)

	schedStore, err := scheduler.OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = schedStore.Close() })
	snapshots := snapshot.NewStore(schedStore.DB())
	producerSnapshot, created, err := snapshots.DeployReviewedWithDependencies(
		context.Background(), producer.LegacyID, producerRoot, "test", "", snapshot.EmptyDependencyManifest(),
	)
	require.NoError(t, err)
	require.True(t, created)
	consumerSnapshot, created, err := snapshots.DeployReviewedWithDependencies(
		context.Background(), consumer.LegacyID, filepath.Join(root, "analytics"), "test", "",
		snapshot.DependencyManifest{Version: snapshot.DependencyManifestVersion, Dependencies: []snapshot.DependencyManifestItem{{
			ConsumerAssetID:      identity.AssetID(consumer.LegacyID, consumer.Assets[0].Name),
			URI:                  producer.Assets[0].URI,
			Mode:                 "full",
			ProducerPipelineUUID: producer.LegacyID,
			ProducerAssetURI:     producer.Assets[0].URI,
		}}},
	)
	require.NoError(t, err)
	require.True(t, created)

	engine := fingerprint.NewEngine()
	producerResults, err := engine.DAG(producer, fingerprint.EffectiveVars(producer, nil))
	require.NoError(t, err)
	cfg, err := loadSelectedConfigReadOnlyFS(afero.NewOsFs(), filepath.Join(root, ".bruin.yml"), "default")
	require.NoError(t, err)
	producerID := identity.AssetID(producer.LegacyID, producer.Assets[0].Name)
	producerTarget := resolveAssetPhysicalTarget(root, &directPipelineInfo{
		Pipeline: producer, Asset: producer.Assets[0], Config: cfg,
	})
	require.Equal(t, AssetRenderFidelityExact, producerTarget.Fidelity)
	materializations := matlog.NewStore(schedStore.DB())
	require.NoError(t, materializations.Record(context.Background(), matlog.Materialization{
		AssetID: producerID, Environment: "default",
		Fingerprint:    string(producerResults[producerID].FP),
		OwnContent:     string(producerResults[producerID].OwnContent),
		VarsHash:       fingerprint.AllVarsHash(fingerprint.EffectiveVars(producer, nil)),
		TargetIdentity: producerTarget.Identity,
		RunID:          "producer-run", SnapshotVersionID: producerSnapshot.VersionID,
		CompletionID: "producer-run", CompletionOrdinal: 0,
		MaterializedAt: time.Date(2026, 7, 17, 11, 0, 0, 0, time.UTC),
	}))

	selectedProducerSnapshot := producerSnapshot
	planner := NewPipelinePlanService(PipelinePlanDependencies{
		WorkspaceRoot: root, ConfigPath: filepath.Join(root, ".bruin.yml"),
		Snapshots: snapshots, Staleness: &pipelinePlanStalenessStub{},
		Fingerprints: engine, Materializations: materializations,
		ResolveProducerDeployment: func(context.Context, string, string) (PipelinePlanProducerDeployment, error) {
			return PipelinePlanProducerDeployment{
				PipelineID: EncodeID("raw"), PipelineName: producer.Name,
				SnapshotVersionID: selectedProducerSnapshot.VersionID,
			}, nil
		},
		ResolvePipelineUUID: func(pipelineID string) (string, bool) {
			return consumer.LegacyID, pipelineID == EncodeID("analytics")
		},
		Now: func() time.Time { return time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC) },
	})
	request := PipelinePlanRequest{
		Purpose: PipelinePlanPurposeExecution, Environment: "default",
		StartDate: "2026-07-17T00:00:00Z", EndDate: "2026-07-18T00:00:00Z",
		Source: PipelinePlanSourceRequest{
			Kind: PipelinePlanSourceSnapshot, VersionID: consumerSnapshot.VersionID,
		},
		Selection:          PipelinePlanSelectionRequest{Mode: PipelinePlanSelectionAll},
		Scheduled:          true,
		SkipDataStateCheck: true,
		SkipActiveRunCheck: true,
	}
	plan, apiErr := planner.Plan(context.Background(), EncodeID("analytics"), request)
	require.Nil(t, apiErr)
	assert.NotEqual(t, PipelinePlanStatusBlocked, plan.Status, plan.Readiness.Blockers)
	for _, asset := range plan.Readiness.CodeChecks.Assets {
		for _, finding := range asset.Findings {
			assert.NotEqual(t, "unresolved-relation", finding.Code, finding.Message)
		}
	}
	require.Len(t, plan.Prerequisites, 1)
	assert.Equal(t, PipelinePlanPrerequisiteReady, plan.Prerequisites[0].Status)
	assert.Equal(t, producerSnapshot.VersionID, plan.Prerequisites[0].ProducerSnapshotVersionID)
	assert.Equal(t, producerSnapshot.Ordinal, plan.Prerequisites[0].ProducerDeploymentOrdinal)
	assert.Equal(t, "producer-run", plan.Prerequisites[0].WriterRunID)

	require.NoError(t, os.WriteFile(producerAssetPath, []byte(`
/* @bruin
name: raw.orders
uri: duckdb://warehouse/raw/orders
type: duckdb.sql
materialization:
  type: table
@bruin */
select 1 as id, 2 as changed
`), 0o644))
	stillBound, apiErr := planner.Plan(context.Background(), EncodeID("analytics"), request)
	require.Nil(t, apiErr)
	assert.NotEqual(t, PipelinePlanStatusBlocked, stillBound.Status)
	assert.Equal(t, producerSnapshot.VersionID, stillBound.Prerequisites[0].ProducerSnapshotVersionID)

	newProducerSnapshot, created, err := snapshots.DeployReviewedWithDependencies(
		context.Background(), producer.LegacyID, producerRoot, "test", "", snapshot.EmptyDependencyManifest(),
	)
	require.NoError(t, err)
	require.True(t, created)
	selectedProducerSnapshot = newProducerSnapshot
	changed, apiErr := planner.Plan(context.Background(), EncodeID("analytics"), request)
	require.Nil(t, apiErr)
	assert.Equal(t, PipelinePlanStatusBlocked, changed.Status)
	require.Len(t, changed.Prerequisites, 1)
	assert.Equal(t, newProducerSnapshot.VersionID, changed.Prerequisites[0].ProducerSnapshotVersionID)
	assert.Contains(t, changed.Prerequisites[0].Reason, "does not match")

	request.ProducerDeploymentPins = map[string]string{
		producer.LegacyID: producerSnapshot.VersionID,
	}
	frozen, apiErr := planner.Plan(context.Background(), EncodeID("analytics"), request)
	require.Nil(t, apiErr)
	assert.NotEqual(t, PipelinePlanStatusBlocked, frozen.Status)
	require.Len(t, frozen.Prerequisites, 1)
	assert.Equal(t, producerSnapshot.VersionID, frozen.Prerequisites[0].ProducerSnapshotVersionID)
}

func TestBindPipelinePlanExecutionDependenciesChainsWindowsAndSelectedUpstreams(t *testing.T) {
	t.Parallel()
	pl := &pipeline.Pipeline{Assets: []*pipeline.Asset{
		{Name: "analytics.up"},
		{
			Name: "analytics.down",
			Upstreams: []pipeline.Upstream{{
				Type: "asset", Value: "analytics.up",
			}},
		},
		{Name: "analytics.independent"},
	}}
	units := []PipelinePlanExecutionUnit{
		{AssetName: "analytics.up"},
		{AssetName: "analytics.up"},
		{AssetName: "analytics.down"},
		{AssetName: "analytics.down"},
		{AssetName: "analytics.independent"},
	}

	require.NoError(t, bindPipelinePlanExecutionDependencies(pl, units))
	assert.Empty(t, units[0].DependencyPositions)
	assert.Equal(t, []int{0}, units[1].DependencyPositions)
	assert.Equal(t, []int{1}, units[2].DependencyPositions)
	assert.Equal(t, []int{2}, units[3].DependencyPositions)
	assert.Empty(t, units[4].DependencyPositions)
}

func TestBindPipelinePlanExecutionDependenciesIgnoresSymbolicBackEdges(t *testing.T) {
	t.Parallel()
	pl := &pipeline.Pipeline{Assets: []*pipeline.Asset{
		{
			Name: "analytics.up",
			Upstreams: []pipeline.Upstream{{
				Type: "asset", Value: "analytics.down", Mode: pipeline.UpstreamModeSymbolic,
			}},
		},
		{
			Name: "analytics.down",
			Upstreams: []pipeline.Upstream{{
				Type: "asset", Value: "analytics.up", Mode: pipeline.UpstreamModeFull,
			}},
		},
	}}
	units := []PipelinePlanExecutionUnit{
		{AssetName: "analytics.up"},
		{AssetName: "analytics.down"},
	}

	require.NoError(t, bindPipelinePlanExecutionDependencies(pl, units))
	assert.Empty(t, units[0].DependencyPositions)
	assert.Equal(t, []int{0}, units[1].DependencyPositions)
}

func TestPipelinePlanDoesNotBlockLocalLoadPseudoConnection(t *testing.T) {
	t.Parallel()

	_, root := writeTypeCheckWorkspace(t, "id: pipeline-uuid\nname: analytics", map[string]string{
		"orders.asset.yml": `
name: analytics.orders_export
type: load
connection: local
parameters:
  source_connection: duckdb-default
  source_table: analytics.orders
  destination_object: ./orders.csv
materialization:
  type: table
  strategy: create+replace
columns:
  - name: order_id
    type: integer
`,
	})
	stale := &pipelinePlanStalenessStub{snapshot: staleness.Snapshot{Assets: []staleness.AssetStatus{{
		AssetName: "analytics.orders_export", Status: staleness.StatusNeverBuilt,
	}}}}

	plan, apiErr := newTestPipelinePlanService(root, stale, nil).Plan(
		context.Background(), EncodeID("analytics"),
		PipelinePlanRequest{Selection: PipelinePlanSelectionRequest{Mode: PipelinePlanSelectionAll}},
	)
	require.Nil(t, apiErr)
	assert.Equal(t, PipelinePlanStatusReady, plan.Status, plan.Readiness)
	assert.Equal(t, "exact", plan.Context.ConfigurationFidelity)
	assert.NotEmpty(t, plan.Context.ConfigurationDigest)
	assert.NotContains(t, pipelinePlanIssueCodes(plan.Readiness.Blockers), "configuration_identity_unavailable")
}

func TestPipelinePlanRendersVariablesForAPIAndSQLAssets(t *testing.T) {
	t.Parallel()

	_, root := writeTypeCheckWorkspace(t, `
id: pipeline-uuid
name: earthquake_monitoring
schedule: "@hourly"
default_connections:
  duckdb: duckdb-default
variables:
  min_magnitude:
    type: integer
    default: 3
  notable_magnitude:
    type: integer
    default: 5
`, map[string]string{
		"events.asset.yml": `
name: earthquakes.events
type: api
connection: duckdb-default
materialization:
  type: table
  strategy: create+replace
parameters:
  request:
    url: https://example.test/events
    params:
      minmagnitude: "{{ var.min_magnitude }}"
  response:
    records_path: events
    fields:
      magnitude: magnitude
columns:
  - name: magnitude
    type: double
`,
		"notable_events.sql": `
/* @bruin
name: earthquakes.notable_events
type: duckdb.sql
depends:
  - earthquakes.events
materialization:
  type: table
columns:
  - name: magnitude
    type: double
@bruin */
select magnitude
from earthquakes.events
where magnitude >= {{ var.notable_magnitude }}
`,
	})
	stale := &pipelinePlanStalenessStub{}

	plan, apiErr := newTestPipelinePlanService(root, stale, nil).Plan(
		context.Background(),
		EncodeID("analytics"),
		PipelinePlanRequest{
			Selection:           PipelinePlanSelectionRequest{Mode: PipelinePlanSelectionAll},
			SkipDataStateCheck:  true,
			IncludeStageContent: true,
		},
	)
	require.Nil(t, apiErr)
	assert.Equal(t, PipelinePlanStatusReady, plan.Status, plan.Readiness)
	assert.Empty(t, plan.Readiness.Blockers)

	events := findPipelinePlanAsset(t, plan, "earthquakes.events")
	require.Len(t, events.Renders, 1)
	assert.Equal(t, AssetRenderStatusOK, events.Renders[0].Status)
	require.NotEmpty(t, events.Renders[0].Stages)
	assert.Contains(t, events.Renders[0].Stages[0].Content, "minmagnitude=3")
	assert.NotContains(t, events.Renders[0].Stages[0].Content, "{{")

	notable := findPipelinePlanAsset(t, plan, "earthquakes.notable_events")
	require.Len(t, notable.Renders, 1)
	assert.Equal(t, AssetRenderStatusOK, notable.Renders[0].Status)
	require.NotEmpty(t, notable.Renders[0].Stages)
	assert.Contains(t, notable.Renders[0].Stages[0].Content, "magnitude >= 5")
	assert.NotContains(t, notable.Renders[0].Stages[0].Content, "{{")
}

func TestPipelinePlanSnapshotUsesImmutableSourceAndTopologicalOrder(t *testing.T) {
	_, root := writeTypeCheckWorkspace(t, `
id: pipeline-uuid
name: analytics
`, map[string]string{
		"up.sql": `
/* @bruin
name: analytics.up
type: duckdb.sql
materialization:
  type: table
@bruin */
select 1 as id
`,
		"down.sql": `
/* @bruin
name: analytics.down
type: duckdb.sql
depends:
  - analytics.up
materialization:
  type: table
@bruin */
select * from analytics.up
`,
	})
	schedulerStore, err := scheduler.OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = schedulerStore.Close() })
	snapshotStore := snapshot.NewStore(schedulerStore.DB())
	deployed, created, err := snapshotStore.Deploy(
		context.Background(),
		"pipeline-uuid",
		filepath.Join(root, "analytics"),
		"test",
	)
	require.NoError(t, err)
	require.True(t, created)

	upPath := filepath.Join(root, "analytics", "assets", "up.sql")
	content, err := os.ReadFile(upPath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(upPath, []byte(strings.ReplaceAll(string(content), "select 1", "select 99")), 0o644))

	stale := &pipelinePlanStalenessStub{snapshot: staleness.Snapshot{
		DataStateToken: "snapshot-data",
		Assets: []staleness.AssetStatus{
			{AssetName: "analytics.up", Status: staleness.StatusNeverBuilt},
			{AssetName: "analytics.down", Status: staleness.StatusNeverBuilt},
		},
	}}
	service := newTestPipelinePlanService(root, stale, snapshotStore)
	plan, apiErr := service.Plan(context.Background(), EncodeID("analytics"), PipelinePlanRequest{
		Source:              PipelinePlanSourceRequest{Kind: PipelinePlanSourceSnapshot, VersionID: deployed.VersionID},
		Selection:           PipelinePlanSelectionRequest{Mode: PipelinePlanSelectionAll},
		IncludeStageContent: true,
	})
	require.Nil(t, apiErr)
	assert.Equal(t, PipelinePlanSourceSnapshot, plan.Source.Kind)
	assert.Equal(t, deployed.VersionID, plan.Source.VersionID)
	assert.Equal(t, deployed.MerkleRoot, plan.Source.MerkleRoot)
	require.Len(t, plan.Assets, 2)
	assert.Equal(t, []string{"analytics.up", "analytics.down"}, []string{plan.Assets[0].Name, plan.Assets[1].Name})

	var rendered string
	for _, stage := range plan.Assets[0].Renders[0].Stages {
		rendered += stage.Content
	}
	assert.Contains(t, rendered, "select 1")
	assert.NotContains(t, rendered, "select 99")
	require.NotNil(t, stale.parsed)
	assert.NotEqual(t, filepath.Join(root, "analytics"), filepath.Dir(stale.parsed.DefinitionFile.Path))
}

func TestScheduledPipelinePlanAppliesSnapshotVariablesBeforeRendering(t *testing.T) {
	_, root := writeTypeCheckWorkspace(t, `
id: pipeline-uuid
name: analytics
schedule: hourly
variables:
  region:
    type: string
    default: eu
  limit:
    type: integer
    default: 10
`, map[string]string{
		"report.sql": `
/* @bruin
name: analytics.report
type: duckdb.sql
materialization:
  type: table
@bruin */
select '{{ var.region }}' as region, {{ var.limit }} as row_limit
`,
	})
	schedulerStore, err := scheduler.OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = schedulerStore.Close() })
	snapshotStore := snapshot.NewStore(schedulerStore.DB())
	deployed, _, err := snapshotStore.Deploy(
		context.Background(), "pipeline-uuid", filepath.Join(root, "analytics"), "test",
	)
	require.NoError(t, err)

	// The working tree can evolve independently. Overrides are validated and
	// applied against the pinned deployment, not these newer declarations.
	pipelinePath := filepath.Join(root, "analytics", "pipeline.yml")
	workingTree, err := os.ReadFile(pipelinePath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(pipelinePath, append(workingTree, []byte(`
  future:
    type: string
    default: later
`)...), 0o644))

	executionTime := time.Date(2026, 7, 18, 10, 30, 0, 0, time.UTC)
	start := executionTime.Add(-time.Hour)
	stale := &pipelinePlanStalenessStub{snapshot: staleness.Snapshot{Assets: []staleness.AssetStatus{{
		AssetName: "analytics.report", Status: staleness.StatusNeverBuilt,
	}}}}
	planner := newTestPipelinePlanService(root, stale, snapshotStore)
	plan, apiErr := planner.Plan(context.Background(), EncodeID("analytics"), PipelinePlanRequest{
		Environment: "default", StartDate: start.Format(time.RFC3339),
		EndDate: executionTime.Format(time.RFC3339), ExecutionTime: executionTime.Format(time.RFC3339),
		Source:                 PipelinePlanSourceRequest{Kind: PipelinePlanSourceSnapshot, VersionID: deployed.VersionID},
		Selection:              PipelinePlanSelectionRequest{Mode: PipelinePlanSelectionAll},
		IncludeStageContent:    true,
		VariableOverrides:      map[string]any{"region": "private-schedule-value", "limit": float64(25)},
		VariableOverrideSource: "schedule_override",
		Scheduled:              true,
	})
	require.Nil(t, apiErr)
	assert.Equal(t, PipelinePlanStatusReady, plan.Status, plan.Readiness)
	assert.Equal(t, "wait", plan.Context.SensorMode)
	assert.Equal(t, []AssetRenderVariableProvenance{
		{Name: "limit", Source: "schedule_override"},
		{Name: "region", Source: "schedule_override"},
	}, plan.Context.VariableProvenance)
	require.NotNil(t, stale.parsed)
	assert.Equal(t, int64(25), stale.parsed.Variables.Value()["limit"])
	assert.Equal(t, "private-schedule-value", stale.parsed.Variables.Value()["region"])
	assert.Equal(t, fingerprint.AllVarsHash(fingerprint.EffectiveVars(stale.parsed, nil)), plan.Context.VariablesDigest)

	var rendered string
	for _, stage := range plan.Assets[0].Renders[0].Stages {
		rendered += stage.Content
	}
	assert.Contains(t, rendered, "'private-schedule-value'")
	assert.Contains(t, rendered, "25")

	redactedPlan, apiErr := planner.Plan(context.Background(), EncodeID("analytics"), PipelinePlanRequest{
		Environment: "default", StartDate: start.Format(time.RFC3339),
		EndDate: executionTime.Format(time.RFC3339), ExecutionTime: executionTime.Format(time.RFC3339),
		Source:                 PipelinePlanSourceRequest{Kind: PipelinePlanSourceSnapshot, VersionID: deployed.VersionID},
		Selection:              PipelinePlanSelectionRequest{Mode: PipelinePlanSelectionAll},
		VariableOverrides:      map[string]any{"region": "private-schedule-value", "limit": float64(25)},
		VariableOverrideSource: "schedule_override",
		Scheduled:              true,
	})
	require.Nil(t, apiErr)
	redactedArtifact, err := json.Marshal(redactedPlan)
	require.NoError(t, err)
	assert.NotContains(t, string(redactedArtifact), "private-schedule-value")

	_, invalid := planner.Plan(context.Background(), EncodeID("analytics"), PipelinePlanRequest{
		Source:            PipelinePlanSourceRequest{Kind: PipelinePlanSourceSnapshot, VersionID: deployed.VersionID},
		Selection:         PipelinePlanSelectionRequest{Mode: PipelinePlanSelectionAll},
		VariableOverrides: map[string]any{"future": "not-in-deployment"},
	})
	require.NotNil(t, invalid)
	assert.Equal(t, "invalid_variable_overrides", invalid.Code)
	assert.NotContains(t, invalid.Message, "not-in-deployment")
}

func TestPipelinePlanDeployedOnlyWithoutSnapshotReturnsActionableBlocker(t *testing.T) {
	_, root := writeTypeCheckWorkspace(t, "id: pipeline-uuid\nname: analytics", map[string]string{
		"report.sql": `
/* @bruin
name: analytics.report
type: duckdb.sql
@bruin */
select 1
`,
	})
	stale := &pipelinePlanStalenessStub{}
	service := newTestPipelinePlanService(root, stale, emptyPipelinePlanSnapshotStore{})
	service.deps.PolicyFor = func(string) policy.EnvironmentPolicy {
		return policy.EnvironmentPolicy{DeployedOnly: true}
	}

	plan, apiErr := service.Plan(context.Background(), EncodeID("analytics"), PipelinePlanRequest{})
	require.Nil(t, apiErr)
	assert.Equal(t, PipelinePlanStatusBlocked, plan.Status)
	assert.Equal(t, PipelinePlanSourceSnapshot, plan.Source.Kind)
	require.Len(t, plan.Readiness.Blockers, 1)
	assert.Equal(t, "deployment_required", plan.Readiness.Blockers[0].Code)
	assert.Zero(t, len(plan.Assets))
	assert.Nil(t, stale.parsed)
}

func TestPipelineDeploymentPlanIgnoresExecutionOnlyPolicyAndDataState(t *testing.T) {
	_, root := writeTypeCheckWorkspace(t, "id: pipeline-uuid\nname: analytics", map[string]string{
		"report.sql": `
/* @bruin
name: analytics.report
type: duckdb.sql
materialization:
  type: table
@bruin */
select 1 as id
`,
	})
	stale := &pipelinePlanStalenessStub{err: assert.AnError}
	service := newTestPipelinePlanService(root, stale, nil)
	service.deps.PolicyFor = func(string) policy.EnvironmentPolicy {
		return policy.EnvironmentPolicy{Protected: true, DeployedOnly: true, ConfirmDestructive: true}
	}
	service.deps.ActiveRunID = func(context.Context, string, string) (string, error) {
		return "run-active", nil
	}

	plan, apiErr := service.Plan(context.Background(), EncodeID("analytics"), PipelinePlanRequest{
		Purpose:     PipelinePlanPurposeDeployment,
		FullRefresh: true,
		Selection:   PipelinePlanSelectionRequest{Mode: PipelinePlanSelectionAll},
	})
	require.Nil(t, apiErr)
	assert.NotEqual(t, PipelinePlanStatusBlocked, plan.Status, plan.Readiness.Blockers)
	assert.Equal(t, PipelinePlanSourceWorkingTree, plan.Source.Kind)
	assert.Empty(t, plan.Readiness.ActiveRun)
	assert.NotContains(t, pipelinePlanIssueCodes(plan.Readiness.Blockers), "pipeline_already_running")
	assert.NotContains(t, pipelinePlanIssueCodes(plan.Readiness.Blockers), "data_state_unavailable")
	assert.NotContains(t, pipelinePlanIssueCodes(plan.Readiness.Blockers), "interactive_execution_protected")
	assert.NotContains(t, pipelinePlanIssueCodes(plan.Readiness.Blockers), "deployed_source_required")
	assert.NotContains(t, pipelinePlanIssueCodes(plan.Readiness.Warnings), "destructive_confirmation_required")
	assert.Nil(t, stale.parsed, "deployment review must not depend on mutable data state")
	require.Len(t, plan.Assets, 1)
}

func TestPipelinePlanAssetClosureAndActiveRunAreExplicit(t *testing.T) {
	_, root := writeTypeCheckWorkspace(t, "id: pipeline-uuid\nname: analytics", map[string]string{
		"up.sql": `
/* @bruin
name: analytics.up
type: duckdb.sql
@bruin */
select 1
`,
		"middle.sql": `
/* @bruin
name: analytics.middle
type: duckdb.sql
depends: [analytics.up]
@bruin */
select * from analytics.up
`,
		"down.sql": `
/* @bruin
name: analytics.down
type: duckdb.sql
depends: [analytics.middle]
@bruin */
select * from analytics.middle
`,
	})
	stale := &pipelinePlanStalenessStub{snapshot: staleness.Snapshot{DataStateToken: "token"}}
	service := newTestPipelinePlanService(root, stale, nil)
	service.deps.ActiveRunID = func(context.Context, string, string) (string, error) { return "run-active", nil }

	plan, apiErr := service.Plan(context.Background(), EncodeID("analytics"), PipelinePlanRequest{
		Selection: PipelinePlanSelectionRequest{
			Mode:      PipelinePlanSelectionAsset,
			AssetName: "analytics.middle",
			Scope:     "asset_with_upstreams_and_downstreams",
		},
	})
	require.Nil(t, apiErr)
	assert.Equal(t, PipelinePlanStatusBlocked, plan.Status)
	assert.Equal(t, "run-active", plan.Readiness.ActiveRun)
	require.Len(t, plan.Assets, 3)
	assert.Equal(t, []string{"analytics.up", "analytics.middle", "analytics.down"}, []string{
		plan.Assets[0].Name,
		plan.Assets[1].Name,
		plan.Assets[2].Name,
	})
	assert.Equal(t, []string{"required_upstream"}, plan.Assets[0].InclusionReasons)
	assert.Equal(t, []string{"explicit"}, plan.Assets[1].InclusionReasons)
	assert.Equal(t, []string{"selected_downstream"}, plan.Assets[2].InclusionReasons)
	assert.Contains(t, pipelinePlanIssueCodes(plan.Readiness.Blockers), "pipeline_already_running")
}

func TestPipelinePlanResolvesCustomSelectorsAndNeededIntersection(t *testing.T) {
	_, root := writeTypeCheckWorkspace(t, "id: pipeline-uuid\nname: analytics", map[string]string{
		"up.sql": `
/* @bruin
name: analytics.up
type: duckdb.sql
tags: [daily]
@bruin */
select 1 as id
`,
		"middle.sql": `
/* @bruin
name: analytics.middle
type: duckdb.sql
tags: [daily]
depends: [analytics.up]
@bruin */
select * from analytics.up
`,
		"down.sql": `
/* @bruin
name: analytics.down
type: duckdb.sql
tags: [adhoc]
depends: [analytics.middle]
@bruin */
select * from analytics.middle
`,
	})
	stale := &pipelinePlanStalenessStub{snapshot: staleness.Snapshot{
		DataStateToken: "token",
		Assets: []staleness.AssetStatus{
			{AssetName: "analytics.up", Status: staleness.StatusFresh},
			{AssetName: "analytics.middle", Status: staleness.StatusNeverBuilt},
			{AssetName: "analytics.down", Status: staleness.StatusNeverBuilt},
		},
	}}
	svc := newTestPipelinePlanService(root, stale, nil)

	matching, apiErr := svc.Plan(context.Background(), EncodeID("analytics"), PipelinePlanRequest{
		Selection: PipelinePlanSelectionRequest{Mode: PipelinePlanSelectionSelector, Selector: "tag:daily"},
	})
	require.Nil(t, apiErr)
	require.Len(t, matching.Assets, 2)
	assert.Equal(t, []string{"analytics.up", "analytics.middle"}, []string{
		matching.Assets[0].Name,
		matching.Assets[1].Name,
	})
	assert.Equal(t, "tag:daily", matching.Selection.Selector)
	assert.Equal(t, []string{"selector_match"}, matching.Assets[0].InclusionReasons)

	needed, apiErr := svc.Plan(context.Background(), EncodeID("analytics"), PipelinePlanRequest{
		Selection: PipelinePlanSelectionRequest{Mode: PipelinePlanSelectionSelectorNeeded, Selector: "tag:daily"},
	})
	require.Nil(t, apiErr)
	require.Len(t, needed.Assets, 1)
	assert.Equal(t, "analytics.middle", needed.Assets[0].Name)
	assert.Equal(t, []string{"never_built", "selector_match"}, needed.Assets[0].InclusionReasons)
	require.Len(t, needed.ExecutionUnits, 1)
	assert.Equal(t, "never_built", needed.ExecutionUnits[0].Reason)
}

func TestPipelinePlanKeepsRenderableSiblingsWhenAssetYAMLIsIncomplete(t *testing.T) {
	_, root := writeTypeCheckWorkspace(t, "id: pipeline-uuid\nname: analytics", map[string]string{
		"good.sql": `
/* @bruin
name: analytics.good
type: duckdb.sql
@bruin */
select 1 as id
`,
		"broken.asset.yml": `
name: analytics.broken
type: load
connection: duckdb-default
parameters:
  source_connection: duckdb-default
  source_table: analytics.good
materialization:
  type: table
  strategy: create+replace
`,
	})
	brokenPath := filepath.Join(root, "analytics", "assets", "broken.asset.yml")
	require.NoError(t, os.WriteFile(brokenPath, []byte("name: analytics.broken\ntype: load\nparameters:\n\tobject: x\n"), 0o644))

	stale := &pipelinePlanStalenessStub{snapshot: staleness.Snapshot{Assets: []staleness.AssetStatus{
		{AssetName: "analytics.good", Status: staleness.StatusNeverBuilt},
		{AssetName: "analytics.broken", Status: staleness.StatusNeverBuilt},
	}}}
	plan, apiErr := newTestPipelinePlanService(root, stale, nil).Plan(
		context.Background(),
		EncodeID("analytics"),
		PipelinePlanRequest{Selection: PipelinePlanSelectionRequest{Mode: PipelinePlanSelectionAll}},
	)
	require.Nil(t, apiErr)
	assert.Equal(t, PipelinePlanStatusBlocked, plan.Status)
	require.Len(t, plan.Assets, 2)

	assets := make(map[string]PipelinePlanAsset, len(plan.Assets))
	for _, asset := range plan.Assets {
		assets[asset.Name] = asset
	}
	require.Contains(t, assets, "analytics.good")
	require.Contains(t, assets, "analytics.broken")
	assert.NotEmpty(t, assets["analytics.good"].Renders, "valid sibling should retain its render preview")
	assert.Empty(t, assets["analytics.broken"].Renders, "unparseable asset must not produce an executable unit")
	assert.Contains(t, pipelinePlanIssueCodes(plan.Readiness.Blockers), "code_check_error")

	brokenCheck := findAsset(t, plan.Readiness.CodeChecks, "analytics.broken")
	assert.True(t, hasFinding(brokenCheck, typeCheckSeverityError, "Asset definition could not be parsed"),
		"expected the YAML parse failure in plan diagnostics, got %+v", brokenCheck.Findings)
	for _, unit := range plan.ExecutionUnits {
		assert.NotEqual(t, "analytics.broken", unit.AssetName)
	}
}

func TestPipelinePlanKeepsIncompleteSQLAssetScopedAndPythonRuntimeOnly(t *testing.T) {
	_, root := writeTypeCheckWorkspace(t, "id: pipeline-uuid\nname: analytics", map[string]string{
		"good.sql": `
/* @bruin
name: analytics.good
type: duckdb.sql
@bruin */
select 1 as id
`,
		"incomplete.sql": `
/* @bruin
name: analytics.incomplete_sql
type: duckdb.sql
@bruin */
select from from
`,
		"incomplete.py": `
""" @bruin
name: analytics.incomplete_python
type: python
@bruin """

def unfinished(
`,
	})
	stale := &pipelinePlanStalenessStub{snapshot: staleness.Snapshot{Assets: []staleness.AssetStatus{
		{AssetName: "analytics.good", Status: staleness.StatusNeverBuilt},
		{AssetName: "analytics.incomplete_sql", Status: staleness.StatusNeverBuilt},
		{AssetName: "analytics.incomplete_python", Status: staleness.StatusNeverBuilt},
	}}}
	plan, apiErr := newTestPipelinePlanService(root, stale, nil).Plan(
		context.Background(),
		EncodeID("analytics"),
		PipelinePlanRequest{Selection: PipelinePlanSelectionRequest{Mode: PipelinePlanSelectionAll}},
	)
	require.Nil(t, apiErr)
	assert.Equal(t, PipelinePlanStatusBlocked, plan.Status)
	require.Len(t, plan.Assets, 3)

	good := findPipelinePlanAsset(t, plan, "analytics.good")
	python := findPipelinePlanAsset(t, plan, "analytics.incomplete_python")
	assert.NotEmpty(t, good.Renders, "a broken SQL sibling must not erase valid renders")
	require.Len(t, python.Renders, 1)
	assert.Equal(t, AssetRenderStatusPartial, python.Renders[0].Status)
	assert.Contains(t, pipelinePlanIssueCodes(plan.Readiness.Warnings), "asset_render_partial")

	sqlCheck := findAsset(t, plan.Readiness.CodeChecks, "analytics.incomplete_sql")
	assert.Equal(t, typeCheckStatusError, sqlCheck.Status)
	assert.NotEmpty(t, sqlCheck.Findings)
}

func TestPipelinePlanMarksDestructiveConfirmationPolicy(t *testing.T) {
	_, root := writeTypeCheckWorkspace(t, "id: pipeline-uuid\nname: analytics", map[string]string{
		"report.sql": `
/* @bruin
name: analytics.report
type: duckdb.sql
materialization:
  type: table
@bruin */
select 1 as id
`,
	})
	stale := &pipelinePlanStalenessStub{snapshot: staleness.Snapshot{Assets: []staleness.AssetStatus{
		{AssetName: "analytics.report", Status: staleness.StatusNeverBuilt},
	}}}
	service := newTestPipelinePlanService(root, stale, nil)
	service.deps.PolicyFor = func(string) policy.EnvironmentPolicy {
		return policy.EnvironmentPolicy{ConfirmDestructive: true}
	}

	plan, apiErr := service.Plan(context.Background(), EncodeID("analytics"), PipelinePlanRequest{
		FullRefresh: true,
		Selection:   PipelinePlanSelectionRequest{Mode: PipelinePlanSelectionAll},
	})
	require.Nil(t, apiErr)
	assert.True(t, plan.Context.Destructive)
	assert.Greater(t, plan.Summary.DestructiveOperations, 0)
	assert.Contains(t, pipelinePlanIssueCodes(plan.Readiness.Warnings), "destructive_confirmation_required")
}

func TestPipelinePlanResourcesKeepExactClaimsAndFailClosed(t *testing.T) {
	t.Parallel()
	localIdentity := strings.Repeat("a", 64)
	duckIdentity := strings.Repeat("b", 64)
	warehouseIdentity := strings.Repeat("c", 64)
	assets := []PipelinePlanAsset{
		{Target: AssetRenderTarget{WriteResource: AssetRenderWriteResource{
			Kind: assetWriteResourceDuckDB, Identity: duckIdentity, Fidelity: AssetRenderFidelityExact,
		}}},
		{Target: AssetRenderTarget{WriteResource: AssetRenderWriteResource{
			Kind: assetWriteResourceNone, Fidelity: AssetRenderFidelityExact,
		}}},
		{Target: AssetRenderTarget{WriteResource: AssetRenderWriteResource{
			Kind: assetWriteResourceLocalFile, Identity: localIdentity, Fidelity: AssetRenderFidelityExact,
		}}},
		{Target: AssetRenderTarget{WriteResource: AssetRenderWriteResource{
			Kind: assetWriteResourceLocalFile, Identity: localIdentity, Fidelity: AssetRenderFidelityExact,
		}}},
		{Target: AssetRenderTarget{WriteResource: AssetRenderWriteResource{
			Kind: assetWriteResourceWarehouse, Identity: warehouseIdentity, Fidelity: AssetRenderFidelityExact,
		}}},
	}

	exact := aggregatePipelinePlanMutationResources(testExecutionContractsForTargets(assets))
	assert.Equal(t, PipelinePlanResourceIsolationResources, exact.Isolation)
	assert.Equal(t, []PipelinePlanResourceClaim{
		{Kind: assetWriteResourceDuckDB, Identity: duckIdentity},
		{Kind: assetWriteResourceLocalFile, Identity: localIdentity},
		{Kind: assetWriteResourceWarehouse, Identity: warehouseIdentity},
	}, exact.Claims)

	assets = append(assets, PipelinePlanAsset{Target: AssetRenderTarget{WriteResource: AssetRenderWriteResource{
		Kind: assetWriteResourcePipeline, Fidelity: AssetRenderFidelityRuntimeOnly,
	}}})
	conservative := aggregatePipelinePlanMutationResources(testExecutionContractsForTargets(assets))
	assert.Equal(t, PipelinePlanResourceIsolationPipeline, conservative.Isolation)
	assert.Equal(t, exact.Claims, conservative.Claims, "known exact outputs remain serialized across pipelines")

	noWrite := aggregatePipelinePlanMutationResources(testExecutionContractsForTargets([]PipelinePlanAsset{{Target: AssetRenderTarget{WriteResource: AssetRenderWriteResource{
		Kind: assetWriteResourceNone, Fidelity: AssetRenderFidelityExact,
	}}}}))
	assert.Equal(t, PipelinePlanResourceIsolationResources, noWrite.Isolation)
	assert.Empty(t, noWrite.Claims)
}

func testExecutionContractsForTargets(assets []PipelinePlanAsset) []PipelinePlanExecutionContract {
	contracts := make([]PipelinePlanExecutionContract, 0, len(assets))
	for _, asset := range assets {
		contracts = append(contracts, PipelinePlanExecutionContract{
			MutationResources: executionMutationResources(asset.Target),
		})
	}
	return contracts
}

func newTestPipelinePlanService(
	root string,
	stale PipelinePlanStaleness,
	snapshots PipelinePlanSnapshotStore,
) *PipelinePlanService {
	return NewPipelinePlanService(PipelinePlanDependencies{
		WorkspaceRoot: root,
		ConfigPath:    filepath.Join(root, ".bruin.yml"),
		Snapshots:     snapshots,
		Staleness:     stale,
		ResolvePipelineUUID: func(pipelineID string) (string, bool) {
			return "pipeline-uuid", pipelineID == EncodeID("analytics")
		},
		Now: func() time.Time {
			return time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
		},
	})
}

func pipelinePlanIssueCodes(issues []PipelinePlanIssue) []string {
	codes := make([]string, 0, len(issues))
	for _, issue := range issues {
		codes = append(codes, issue.Code)
	}
	return codes
}

func TestAggregatePythonRuntimePlanWarnings(t *testing.T) {
	t.Parallel()

	assets := []PipelinePlanAsset{
		{ID: "pipeline:python-a", Name: "analytics.python_a", Type: "python"},
		{ID: "pipeline:sql", Name: "analytics.sql", Type: "duckdb.sql"},
		{ID: "pipeline:python-b", Name: "analytics.python_b", Type: "python"},
	}
	issues := []PipelinePlanIssue{
		{
			Code:      "asset_render_partial",
			Severity:  "warning",
			Message:   "some execution details are only available at runtime",
			AssetID:   "pipeline:python-a",
			AssetName: "analytics.python_a",
		},
		{
			Code:      "asset_render_partial",
			Severity:  "warning",
			Message:   "one or more execution stages cannot be rendered statically",
			AssetID:   "pipeline:sql",
			AssetName: "analytics.sql",
		},
		{
			Code:      "asset_render_partial",
			Severity:  "warning",
			Message:   "the physical output target is only available at runtime",
			AssetID:   "pipeline:python-b",
			AssetName: "analytics.python_b",
		},
	}

	assert.Equal(t, []PipelinePlanIssue{
		{
			Code:     "python_execution_runtime_only",
			Severity: "warning",
			Message: "execution details for 2 Python assets are resolved at runtime: " +
				"analytics.python_a, analytics.python_b",
		},
		issues[1],
	}, aggregatePythonRuntimePlanWarnings(assets, issues))
}

func findPipelinePlanAsset(t *testing.T, plan PipelinePlan, name string) PipelinePlanAsset {
	t.Helper()
	for _, asset := range plan.Assets {
		if asset.Name == name {
			return asset
		}
	}
	t.Fatalf("plan asset %q not found", name)
	return PipelinePlanAsset{}
}
