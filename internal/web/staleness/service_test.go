package staleness_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"renart/internal/web/bus"
	"renart/internal/web/dependencygraph"
	"renart/internal/web/fingerprint"
	"renart/internal/web/identity"
	"renart/internal/web/matlog"
	"renart/internal/web/scheduler"
	. "renart/internal/web/staleness"
)

type fixture struct {
	store    *matlog.Store
	engine   *fingerprint.Engine
	service  *Service
	pipeline *pipeline.Pipeline
	events   *bus.Bus
	pushed   chan any
	nextRun  int
}

func sqlAsset(name, content string, upstreams ...string) *pipeline.Asset {
	asset := &pipeline.Asset{
		Name:            name,
		Type:            "duckdb.sql",
		ExecutableFile:  pipeline.ExecutableFile{Path: "/w/p/assets/" + name + ".sql", Content: content},
		Materialization: pipeline.Materialization{Type: pipeline.MaterializationTypeTable},
	}
	for _, upstream := range upstreams {
		asset.Upstreams = append(asset.Upstreams, pipeline.Upstream{Type: "asset", Value: upstream})
	}
	return asset
}

func sourceAsset(name, content string) *pipeline.Asset {
	return &pipeline.Asset{
		Name: name,
		Type: "pg.source",
		ExecutableFile: pipeline.ExecutableFile{
			Path:    "/w/p/assets/" + name + ".asset.yml",
			Content: content,
		},
	}
}

func newFixture(t *testing.T, assets ...*pipeline.Asset) *fixture {
	t.Helper()
	schedStore, err := scheduler.OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = schedStore.Close() })

	f := &fixture{
		store:  matlog.NewStore(schedStore.DB()),
		engine: fingerprint.NewEngine(),
		events: bus.New(),
		pushed: make(chan any, 16),
		pipeline: &pipeline.Pipeline{
			LegacyID:       "p",
			Name:           "test",
			DefinitionFile: pipeline.DefinitionFile{Path: "/w/p/pipeline.yml"},
			Assets:         assets,
		},
	}
	f.service = New(Dependencies{
		Store:   f.store,
		Engine:  f.engine,
		Resolve: func(ctx context.Context, uuid string) (*pipeline.Pipeline, error) { return f.pipeline, nil },
		Publish: func(event any) { f.pushed <- event },
	})
	f.service.AttachBus(f.events)
	return f
}

func TestEvaluateUsesResolvedPipelineWithoutMutatingCachedPanelState(t *testing.T) {
	f := newFixture(t, sqlAsset("source", "select 1"))
	alternate := *f.pipeline
	alternate.Assets = []*pipeline.Asset{sqlAsset("snapshot_source", "select 2")}

	result, err := f.service.Evaluate(context.Background(), Selection{
		PipelineUUID: "p",
		Environment:  "dev",
	}, &alternate)
	require.NoError(t, err)
	require.Len(t, result.Assets, 1)
	assert.Equal(t, "snapshot_source", result.Assets[0].AssetName)

	selections, snapshots := f.service.TestCacheSizes()
	assert.Zero(t, selections)
	assert.Zero(t, snapshots)
}

func TestWorkspaceFingerprintMakesConsumerStaleAfterCrossPipelineProducerEdit(t *testing.T) {
	schedStore, err := scheduler.OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = schedStore.Close() })
	store := matlog.NewStore(schedStore.DB())
	engine := fingerprint.NewEngine()

	producerAsset := sqlAsset("raw.orders", "select 1 as id")
	producerAsset.URI = "duckdb://warehouse/raw/orders"
	producer := &pipeline.Pipeline{
		LegacyID: "producer", Name: "producer",
		DefinitionFile: pipeline.DefinitionFile{Path: "/w/producer/pipeline.yml"},
		Assets:         []*pipeline.Asset{producerAsset},
	}
	consumerAsset := sqlAsset("analytics.orders", "select * from raw.orders")
	consumerAsset.Upstreams = []pipeline.Upstream{{Type: "uri", Value: producerAsset.URI}}
	consumer := &pipeline.Pipeline{
		LegacyID: "consumer", Name: "consumer",
		DefinitionFile: pipeline.DefinitionFile{Path: "/w/consumer/pipeline.yml"},
		Assets:         []*pipeline.Asset{consumerAsset},
	}
	resolveGraph := func(_ context.Context, overrides map[string]*pipeline.Pipeline) (dependencygraph.Graph, error) {
		selectedProducer := producer
		selectedConsumer := consumer
		if overrides[producer.LegacyID] != nil {
			selectedProducer = overrides[producer.LegacyID]
		}
		if overrides[consumer.LegacyID] != nil {
			selectedConsumer = overrides[consumer.LegacyID]
		}
		return dependencygraph.Resolve([]dependencygraph.PipelineInput{
			{UUID: producer.LegacyID, ID: "producer", Name: producer.Name, Parsed: selectedProducer},
			{UUID: consumer.LegacyID, ID: "consumer", Name: consumer.Name, Parsed: selectedConsumer},
		}), nil
	}
	pushed := make(chan any, 1)
	events := bus.New()
	service := New(Dependencies{
		Store: store, Engine: engine,
		Resolve: func(_ context.Context, uuid string) (*pipeline.Pipeline, error) {
			if uuid == consumer.LegacyID {
				return consumer, nil
			}
			return producer, nil
		},
		ResolveGraph: resolveGraph,
		Publish:      func(event any) { pushed <- event },
	})
	service.AttachBus(events)

	graph, err := resolveGraph(context.Background(), nil)
	require.NoError(t, err)
	targets, err := engine.WorkspaceDAG(graph, map[string]fingerprint.Vars{
		producer.LegacyID: {}, consumer.LegacyID: {},
	})
	require.NoError(t, err)
	consumerID := identity.AssetID(consumer.LegacyID, consumerAsset.Name)
	require.NoError(t, store.Record(context.Background(), matlog.Materialization{
		AssetID: consumerID, Environment: "dev", Fingerprint: string(targets[consumerID].FP),
		OwnContent: string(targets[consumerID].OwnContent), VarsHash: fingerprint.AllVarsHash(fingerprint.EffectiveVars(consumer, nil)),
		RunID: "consumer-run", MaterializedAt: time.Now().UTC(),
	}))

	selection := Selection{PipelineUUID: consumer.LegacyID, Environment: "dev"}
	before, err := service.Snapshot(context.Background(), selection)
	require.NoError(t, err)
	require.Len(t, before.Assets, 1)
	assert.Equal(t, StatusFresh, before.Assets[0].Status)

	producerAsset.ExecutableFile.Content = "select 1 as id, 2 as version"
	events.EmitAssetSaved(bus.AssetSaved{
		PipelineUUID: producer.LegacyID,
		AssetID:      identity.AssetID(producer.LegacyID, producerAsset.Name),
		AssetName:    producerAsset.Name,
	})
	select {
	case raw := <-pushed:
		event, ok := raw.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, consumer.LegacyID, event["pipeline_uuid"])
		statuses, ok := event["assets"].([]AssetStatus)
		require.True(t, ok)
		require.Len(t, statuses, 1)
		assert.Equal(t, StatusStaleUpstream, statuses[0].Status)
		assert.NotEqual(t, before.DataStateToken, event["data_state_token"])
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for downstream staleness invalidation")
	}
}

// recordRun simulates a completed run exactly as the matlog recorder does: it
// fingerprints the current pipeline, derives the *achieved* fingerprint for the
// named assets (folding in each upstream's last-recorded fingerprint for
// upstreams not part of this run), and writes coverage rows. Recording only a
// downstream therefore captures the stale upstream it actually read.
func (f *fixture) recordRun(t *testing.T, environment string, window *Interval, assetNames ...string) {
	t.Helper()
	f.nextRun++
	runID := fmt.Sprintf("run-%d", f.nextRun)
	vars := fingerprint.EffectiveVars(f.pipeline, nil)
	results, err := f.engine.DAG(f.pipeline, vars)
	require.NoError(t, err)
	varsHash := fingerprint.AllVarsHash(vars)

	succeeded := make(map[string]bool, len(assetNames))
	for _, name := range assetNames {
		succeeded[identity.AssetID("p", name)] = true
	}
	assetIDs := make([]string, 0, len(results))
	for id := range results {
		assetIDs = append(assetIDs, id)
	}
	latest, err := f.store.LatestFingerprint(context.Background(), assetIDs, environment)
	require.NoError(t, err)
	achieved, err := f.engine.AchievedFingerprints(f.pipeline, results, succeeded, func(id string) (fingerprint.Fingerprint, bool) {
		fp, ok := latest[id]
		return fingerprint.Fingerprint(fp), ok
	})
	require.NoError(t, err)

	for _, name := range assetNames {
		assetID := identity.AssetID("p", name)
		result := results[assetID]
		m := matlog.Materialization{
			AssetID:        assetID,
			Environment:    environment,
			Fingerprint:    string(achieved[assetID]),
			OwnContent:     string(result.OwnContent),
			VarsHash:       varsHash,
			RunID:          runID,
			MaterializedAt: time.Now().UTC(),
		}
		if window != nil {
			m.IntervalStart = &window.Start
			m.IntervalEnd = &window.End
		}
		require.NoError(t, f.store.Record(context.Background(), m))
		// The real recorder also upserts a "succeeded" run attempt at the target
		// fingerprint alongside the coverage fact; mirror that here.
		require.NoError(t, f.store.RecordRun(context.Background(), matlog.AssetRunRecord{
			AssetID:     assetID,
			Environment: environment,
			Fingerprint: string(result.FP),
			Status:      "succeeded",
			RunID:       runID,
			RanAt:       time.Now().UTC(),
		}))
	}
}

// recordRunAttempt upserts the latest run attempt (any outcome) for the named
// assets at their current fingerprint, exactly as the matlog recorder does for a
// failed run — no coverage fact is written.
func (f *fixture) recordRunAttempt(t *testing.T, environment, status string, assetNames ...string) {
	t.Helper()
	f.nextRun++
	runID := fmt.Sprintf("run-%d", f.nextRun)
	vars := fingerprint.EffectiveVars(f.pipeline, nil)
	results, err := f.engine.DAG(f.pipeline, vars)
	require.NoError(t, err)
	for _, name := range assetNames {
		assetID := identity.AssetID("p", name)
		require.NoError(t, f.store.RecordRun(context.Background(), matlog.AssetRunRecord{
			AssetID:     assetID,
			Environment: environment,
			Fingerprint: string(results[assetID].FP),
			Status:      status,
			RunID:       runID,
			RanAt:       time.Now().UTC(),
		}))
	}
}

func (f *fixture) enableTargetAware(targets map[string]PhysicalTarget) {
	f.service.TestSetResolveTargets(func(
		ctx context.Context,
		selection Selection,
		parsed *pipeline.Pipeline,
	) (map[string]PhysicalTarget, error) {
		return targets, nil
	})
}

func (f *fixture) recordTargetRun(
	t *testing.T,
	environment string,
	targetIdentity string,
	window *Interval,
	assetName string,
) matlog.Materialization {
	return f.recordTargetRunFromSource(t, environment, targetIdentity, window, assetName, "")
}

func (f *fixture) recordTargetRunFromSnapshot(
	t *testing.T,
	environment string,
	targetIdentity string,
	window *Interval,
	assetName string,
	snapshotVersionID string,
) matlog.Materialization {
	t.Helper()
	require.NotEmpty(t, snapshotVersionID)
	return f.recordTargetRunFromSource(
		t,
		environment,
		targetIdentity,
		window,
		assetName,
		snapshotVersionID,
	)
}

func (f *fixture) recordTargetRunFromSource(
	t *testing.T,
	environment string,
	targetIdentity string,
	window *Interval,
	assetName string,
	snapshotVersionID string,
) matlog.Materialization {
	t.Helper()
	f.nextRun++
	runID := fmt.Sprintf("target-run-%d", f.nextRun)
	vars := fingerprint.EffectiveVars(f.pipeline, nil)
	results, err := f.engine.DAG(f.pipeline, vars)
	require.NoError(t, err)
	assetID := identity.AssetID("p", assetName)
	result, ok := results[assetID]
	require.True(t, ok)
	materializedAt := time.Date(2026, 7, 1, f.nextRun, 0, 0, 0, time.UTC)
	materialization := matlog.Materialization{
		AssetID:           assetID,
		Environment:       environment,
		Fingerprint:       string(result.FP),
		OwnContent:        string(result.OwnContent),
		VarsHash:          fingerprint.AllVarsHash(vars),
		RunID:             runID,
		SnapshotVersionID: snapshotVersionID,
		TargetIdentity:    targetIdentity,
		CompletionID:      runID,
		CompletionOrdinal: 0,
		MaterializedAt:    materializedAt,
	}
	if window != nil {
		materialization.IntervalStart = &window.Start
		materialization.IntervalEnd = &window.End
	}
	require.NoError(t, f.store.Record(context.Background(), materialization))
	require.NoError(t, f.store.RecordRun(context.Background(), matlog.AssetRunRecord{
		AssetID:     assetID,
		Environment: environment,
		Fingerprint: string(result.FP),
		Status:      "succeeded",
		RunID:       runID,
		RanAt:       materializedAt,
	}))
	return materialization
}

func (f *fixture) statuses(t *testing.T, environment string, start, end *time.Time) map[string]AssetStatus {
	t.Helper()
	statuses, err := f.service.Statuses(context.Background(), Selection{
		PipelineUUID: "p",
		Environment:  environment,
		Start:        start,
		End:          end,
	})
	require.NoError(t, err)
	byName := make(map[string]AssetStatus, len(statuses))
	for _, status := range statuses {
		byName[status.AssetName] = status
	}
	return byName
}

func TestNeverBuiltThenFresh(t *testing.T) {
	t.Parallel()
	f := newFixture(t, sqlAsset("a", "select 1"))

	assert.Equal(t, StatusNeverBuilt, f.statuses(t, "dev", nil, nil)["a"].Status)

	f.recordRun(t, "dev", nil, "a")
	assert.Equal(t, StatusFresh, f.statuses(t, "dev", nil, nil)["a"].Status)
}

func TestSourceAssetHasNoFreshnessStateAndConsumerCanBecomeFresh(t *testing.T) {
	t.Parallel()
	source := sourceAsset("external.orders", "name: external.orders\ntype: pg.source\ncolumns:\n  - name: id\n    type: bigint\n")
	consumer := sqlAsset("analytics.orders", "select * from external.orders", source.Name)
	f := newFixture(t, source, consumer)

	before := f.statuses(t, "dev", nil, nil)
	assert.Equal(t, StatusExternal, before[source.Name].Status)
	assert.Nil(t, before[source.Name].LastMaterializedAt)
	assert.Empty(t, before[source.Name].LastRunStatus)
	assert.Equal(t, StatusNeverBuilt, before[consumer.Name].Status)

	// A source declaration is the consumer's observed read contract. It has no
	// materialization fact of its own, but it must not prevent a successful
	// consumer build from reaching its target fingerprint.
	f.recordRun(t, "dev", nil, consumer.Name)
	afterBuild := f.statuses(t, "dev", nil, nil)
	assert.Equal(t, StatusExternal, afterBuild[source.Name].Status)
	assert.Equal(t, StatusFresh, afterBuild[consumer.Name].Status)

	// Editing the declaration still cascades to consumers while the source
	// itself remains outside Renart's freshness model.
	source.ExecutableFile.Content += "  - name: created_at\n    type: timestamp\n"
	afterEdit := f.statuses(t, "dev", nil, nil)
	assert.Equal(t, StatusExternal, afterEdit[source.Name].Status)
	assert.Equal(t, StatusStaleUpstream, afterEdit[consumer.Name].Status)
}

func TestLegacySnapshotTokenIsDeterministicAndPreservesFreshness(t *testing.T) {
	t.Parallel()
	f := newFixture(t, sqlAsset("a", "select 1"))
	selection := Selection{PipelineUUID: "p", Environment: "dev"}

	before, err := f.service.Snapshot(context.Background(), selection)
	require.NoError(t, err)
	require.Len(t, before.Assets, 1)
	assert.Equal(t, TargetFidelityLegacy, before.Assets[0].TargetFidelity)
	assert.Equal(t, StatusNeverBuilt, before.Assets[0].Status)

	f.recordRun(t, "dev", nil, "a")
	first, err := f.service.Snapshot(context.Background(), selection)
	require.NoError(t, err)
	assert.Equal(t, StatusFresh, first.Assets[0].Status)
	assert.NotEqual(t, before.DataStateToken, first.DataStateToken)

	// Repeating the same marker build changes diagnostic run/timestamp state but
	// not the legacy coverage needed by this selection.
	f.recordRun(t, "dev", nil, "a")
	second, err := f.service.Snapshot(context.Background(), selection)
	require.NoError(t, err)
	assert.Equal(t, first.DataStateToken, second.DataStateToken)
}

func TestSensorRemainsVolatileAfterSuccessfulRun(t *testing.T) {
	t.Parallel()
	sensor := &pipeline.Asset{
		Name:           "ready",
		Type:           pipeline.AssetTypeDuckDBQuerySensor,
		DefinitionFile: pipeline.TaskDefinitionFile{Path: "/w/p/assets/ready.asset.yml"},
		Parameters:     pipeline.ParameterMap{"query": "select 1"},
	}
	f := newFixture(t, sensor)

	before := f.statuses(t, "dev", nil, nil)["ready"]
	assert.Equal(t, StatusVolatile, before.Status)
	assert.True(t, before.Volatile)

	f.recordRun(t, "dev", nil, "ready")
	after := f.statuses(t, "dev", nil, nil)["ready"]
	assert.Equal(t, StatusVolatile, after.Status)
	assert.True(t, after.Volatile)
	assert.NotNil(t, after.LastMaterializedAt)
	assert.False(t, VerifiableByNameForTest(sensor))
}

// State 1: you edited the asset, ran that exact edit, and it failed. Base status
// stays stale_edited, but the failed run is on the current content.
func TestEditedThenRunFailedOnCurrentContent(t *testing.T) {
	t.Parallel()
	f := newFixture(t, sqlAsset("a", "select 1"))
	f.recordRun(t, "dev", nil, "a") // fresh at the original content

	f.pipeline.Assets[0].ExecutableFile.Content = "select 1, 2" // edit
	f.recordRunAttempt(t, "dev", "failed", "a")                 // run the edit → fails

	s := f.statuses(t, "dev", nil, nil)["a"]
	assert.Equal(t, StatusStaleEdited, s.Status)
	assert.Equal(t, "failed", s.LastRunStatus)
	assert.True(t, s.LastRunOnCurrentContent, "the failing run was on the edited content")
}

// State 2: you edited the asset but have not run it since. Base is stale_edited,
// and the last run (the old success) is not on the current content.
func TestEditedButNotRunSinceEdit(t *testing.T) {
	t.Parallel()
	f := newFixture(t, sqlAsset("a", "select 1"))
	f.recordRun(t, "dev", nil, "a") // fresh at the original content

	f.pipeline.Assets[0].ExecutableFile.Content = "select 1, 2" // edit, no re-run

	s := f.statuses(t, "dev", nil, nil)["a"]
	assert.Equal(t, StatusStaleEdited, s.Status)
	assert.Equal(t, "succeeded", s.LastRunStatus)
	assert.False(t, s.LastRunOnCurrentContent, "the last run was the pre-edit build")
}

// State 3: the content is unchanged (still fresh from an earlier build), but the
// most recent run at that same content failed.
func TestUnchangedContentButLastRunFailed(t *testing.T) {
	t.Parallel()
	f := newFixture(t, sqlAsset("a", "select 1"))
	f.recordRun(t, "dev", nil, "a")             // fresh
	f.recordRunAttempt(t, "dev", "failed", "a") // re-run same content → fails

	s := f.statuses(t, "dev", nil, nil)["a"]
	assert.Equal(t, StatusFresh, s.Status, "coverage still proves an earlier build")
	assert.Equal(t, "failed", s.LastRunStatus)
	assert.True(t, s.LastRunOnCurrentContent)
}

func TestFreshAssetRetainsFailedQualityOutcome(t *testing.T) {
	t.Parallel()
	f := newFixture(t, sqlAsset("a", "select 1"))
	f.recordRun(t, "dev", nil, "a")

	results, err := f.engine.DAG(f.pipeline, fingerprint.EffectiveVars(f.pipeline, nil))
	require.NoError(t, err)
	checkedAt := time.Now().UTC().Add(time.Second)
	require.NoError(t, f.store.RecordRun(context.Background(), matlog.AssetRunRecord{
		AssetID:       identity.AssetID("p", "a"),
		Environment:   "dev",
		Fingerprint:   string(results[identity.AssetID("p", "a")].FP),
		Status:        "succeeded",
		RunID:         "quality-run",
		RanAt:         checkedAt,
		QualityStatus: bus.QualityStatusFailed,
		FailedChecks: []bus.QualityCheckFailure{{
			Kind: bus.QualityCheckKindCustom, Name: "no invalid rows", Blocking: true,
		}},
	}))

	s := f.statuses(t, "dev", nil, nil)["a"]
	assert.Equal(t, StatusFresh, s.Status, "the successful write remains reusable")
	assert.Equal(t, "succeeded", s.LastRunStatus)
	assert.Equal(t, bus.QualityStatusFailed, s.QualityStatus)
	assert.True(t, s.QualityOnCurrentContent)
	assert.Equal(t, "quality-run", s.QualityRunID)
	assert.Equal(t, checkedAt, *s.QualityCheckedAt)
	assert.Equal(t, []bus.QualityCheckFailure{{
		Kind: bus.QualityCheckKindCustom, Name: "no invalid rows", Blocking: true,
	}}, s.FailedChecks)

	f.pipeline.Assets[0].ExecutableFile.Content = "select 2"
	edited := f.statuses(t, "dev", nil, nil)["a"]
	assert.False(t, edited.QualityOnCurrentContent, "an old failure must not be blamed on edited SQL")
}

func TestEditFlipsAssetAndCone(t *testing.T) {
	t.Parallel()
	f := newFixture(t,
		sqlAsset("a", "select 1"),
		sqlAsset("b", "select * from a", "a"),
		sqlAsset("c", "select 2"),
	)
	f.recordRun(t, "dev", nil, "a", "b", "c")
	require.Equal(t, StatusFresh, f.statuses(t, "dev", nil, nil)["a"].Status)

	// Edit a: own content changes, b inherits, c untouched.
	f.pipeline.Assets[0].ExecutableFile.Content = "select 1, 2"

	statuses := f.statuses(t, "dev", nil, nil)
	assert.Equal(t, StatusStaleEdited, statuses["a"].Status)
	assert.Equal(t, StatusStaleUpstream, statuses["b"].Status)
	assert.Equal(t, StatusFresh, statuses["c"].Status)
}

func TestMaterializingDownstreamOnStaleUpstreamStaysStale(t *testing.T) {
	t.Parallel()
	// A -> B -> C. Edit B, then materialize only C (without rebuilding B). C read
	// B's old physical table, so it must stay stale; rebuilding B afterwards does
	// not retroactively make C fresh either. Freshness is over the lineage
	// actually consumed, not over current definitions.
	f := newFixture(t,
		sqlAsset("a", "select 1"),
		sqlAsset("b", "select * from a", "a"),
		sqlAsset("c", "select * from b", "b"),
	)
	f.recordRun(t, "dev", nil, "a", "b", "c")
	require.Equal(t, StatusFresh, f.statuses(t, "dev", nil, nil)["c"].Status)

	// Edit B: B is stale_edited, C inherits stale_upstream.
	f.pipeline.Assets[1].ExecutableFile.Content = "select a.id from a"
	statuses := f.statuses(t, "dev", nil, nil)
	require.Equal(t, StatusStaleEdited, statuses["b"].Status)
	require.Equal(t, StatusStaleUpstream, statuses["c"].Status)

	// Materialize only C. It reads the un-rebuilt B, so it stays stale — the run
	// was a data no-op for freshness purposes.
	f.recordRun(t, "dev", nil, "c")
	statuses = f.statuses(t, "dev", nil, nil)
	assert.Equal(t, StatusStaleEdited, statuses["b"].Status, "B unchanged by materializing C")
	assert.Equal(t, StatusStaleUpstream, statuses["c"].Status, "C built on old B must stay stale")

	// Now materialize B. B goes fresh, but C's table was physically built from
	// old-B rows, so C remains stale until it is itself rerun.
	f.recordRun(t, "dev", nil, "b")
	statuses = f.statuses(t, "dev", nil, nil)
	assert.Equal(t, StatusFresh, statuses["b"].Status)
	assert.Equal(t, StatusStaleUpstream, statuses["c"].Status, "rebuilding B does not retroactively refresh C")

	// Finally rerun C against fresh B: now everything is current.
	f.recordRun(t, "dev", nil, "c")
	assert.Equal(t, StatusFresh, f.statuses(t, "dev", nil, nil)["c"].Status)
}

func TestCommentEditStaysFresh(t *testing.T) {
	t.Parallel()
	f := newFixture(t, sqlAsset("a", "select 1"))
	f.recordRun(t, "dev", nil, "a")

	f.pipeline.Assets[0].ExecutableFile.Content = "-- explain the query\nselect   1"
	assert.Equal(t, StatusFresh, f.statuses(t, "dev", nil, nil)["a"].Status)
}

func TestEnvironmentSwitchKeepsIndependentStatus(t *testing.T) {
	t.Parallel()
	f := newFixture(t, sqlAsset("a", "select 1"))
	f.recordRun(t, "prod", nil, "a")

	assert.Equal(t, StatusFresh, f.statuses(t, "prod", nil, nil)["a"].Status)
	assert.Equal(t, StatusNeverBuilt, f.statuses(t, "staging", nil, nil)["a"].Status)
	// Toggling back to a previously built env shows fresh again — the bug
	// the old reset-flags idea would have had.
	assert.Equal(t, StatusFresh, f.statuses(t, "prod", nil, nil)["a"].Status)
}

func TestTargetAwareStalenessDoesNotResurrectHistoricalGeneration(t *testing.T) {
	t.Parallel()
	asset := intervalAwareAsset("a")
	f := newFixture(t, asset)
	const target = "renart-physical-target-v1:orders"
	targets := map[string]PhysicalTarget{"p:a": {Identity: target, Exact: true}}
	f.enableTargetAware(targets)
	day := func(d int) time.Time { return time.Date(2026, 7, 1+d, 0, 0, 0, 0, time.UTC) }
	wide := Interval{Start: day(0), End: day(10)}

	// Generation one contains broad coverage for source variant A.
	f.recordTargetRun(t, "dev", target, &wide, "a")
	start, end := wide.Start, wide.End
	require.Equal(t, StatusFresh, f.statuses(t, "dev", &start, &end)["a"].Status)

	// Variant B overwrites the same physical target and advances its generation.
	asset.ExecutableFile.Content = "select * from events where active"
	f.recordTargetRun(t, "dev", target, &wide, "a")
	require.Equal(t, StatusFresh, f.statuses(t, "dev", &start, &end)["a"].Status)

	// Returning the source to A must not reactivate generation one's matching
	// fingerprint and broad interval coverage.
	asset.ExecutableFile.Content = "select * from events"
	assert.Equal(t, StatusStaleEdited, f.statuses(t, "dev", &start, &end)["a"].Status)

	// Rebuilding only one day starts generation three. The old nine days from
	// generation one remain audit history, not reusable freshness.
	oneDay := Interval{Start: day(0), End: day(1)}
	f.recordTargetRun(t, "dev", target, &oneDay, "a")
	status := f.statuses(t, "dev", &start, &end)["a"]
	assert.Equal(t, StatusPartial, status.Status)
	require.Len(t, status.Gaps, 1)
	assert.Equal(t, day(1), status.Gaps[0].Start)
	assert.Equal(t, day(10), status.Gaps[0].End)
}

func TestTargetAwareStalenessFollowsSelectedPhysicalTarget(t *testing.T) {
	t.Parallel()
	f := newFixture(t, sqlAsset("a", "select 1"))
	targets := map[string]PhysicalTarget{
		"p:a": {Identity: "renart-physical-target-v1:a", Exact: true},
	}
	f.enableTargetAware(targets)

	f.recordTargetRun(t, "dev", targets["p:a"].Identity, nil, "a")
	assert.Equal(t, StatusFresh, f.statuses(t, "dev", nil, nil)["a"].Status)

	// The same source routed to a new target has no reusable evidence there.
	targets["p:a"] = PhysicalTarget{Identity: "renart-physical-target-v1:b", Exact: true}
	assert.Equal(t, StatusNeverBuilt, f.statuses(t, "dev", nil, nil)["a"].Status)
	f.recordTargetRun(t, "dev", targets["p:a"].Identity, nil, "a")
	assert.Equal(t, StatusFresh, f.statuses(t, "dev", nil, nil)["a"].Status)

	// Distinct exact targets prove physical isolation, so switching back to A
	// can reuse A's still-current writer and coverage.
	targets["p:a"] = PhysicalTarget{Identity: "renart-physical-target-v1:a", Exact: true}
	assert.Equal(t, StatusFresh, f.statuses(t, "dev", nil, nil)["a"].Status)
}

func TestTargetAwareStalenessFailsClosedForUnresolvedTarget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		target *PhysicalTarget
	}{
		{name: "missing"},
		{name: "runtime only", target: &PhysicalTarget{Exact: false}},
		{name: "empty exact", target: &PhysicalTarget{Exact: true}},
		{name: "non canonical identity", target: &PhysicalTarget{Identity: " target ", Exact: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, sqlAsset("a", "select 1"))
			// Legacy evidence exists, but target-aware mode must never infer a
			// current physical target from it.
			f.recordRun(t, "dev", nil, "a")
			targets := map[string]PhysicalTarget{}
			if tt.target != nil {
				targets["p:a"] = *tt.target
			}
			f.enableTargetAware(targets)
			assert.Equal(t, StatusNeverBuilt, f.statuses(t, "dev", nil, nil)["a"].Status)
		})
	}
}

func TestTargetAwareStalenessFailsClosedForAmbiguousWriter(t *testing.T) {
	t.Parallel()
	f := newFixture(t, sqlAsset("a", "select 1"))
	const target = "renart-physical-target-v1:shared"
	f.enableTargetAware(map[string]PhysicalTarget{"p:a": {Identity: target, Exact: true}})
	first := f.recordTargetRun(t, "dev", target, nil, "a")

	conflict := first
	conflict.Fingerprint = "v1:conflicting-output"
	conflict.RunID = "other-run"
	conflict.CompletionID = "other-completion"
	require.ErrorIs(t, f.store.Record(context.Background(), conflict), matlog.ErrTargetWriterAmbiguous)

	snapshot, err := f.service.Snapshot(context.Background(), Selection{PipelineUUID: "p", Environment: "dev"})
	require.NoError(t, err)
	require.Len(t, snapshot.Assets, 1)
	assert.Equal(t, StatusNeverBuilt, snapshot.Assets[0].Status)
	require.NotNil(t, snapshot.Assets[0].LatestOutput)
	assert.True(t, snapshot.Assets[0].LatestOutput.Ambiguous)
}

func TestTargetAwareStalenessFailsClosedForDisplacedWriter(t *testing.T) {
	t.Parallel()
	f := newFixture(t, sqlAsset("a", "select 1"))
	const target = "renart-physical-target-v1:shared"
	f.enableTargetAware(map[string]PhysicalTarget{"p:a": {Identity: target, Exact: true}})
	first := f.recordTargetRun(t, "dev", target, nil, "a")
	require.Equal(t, StatusFresh, f.statuses(t, "dev", nil, nil)["a"].Status)

	displacing := first
	displacing.AssetID = "other:b"
	displacing.Environment = "prod"
	displacing.Fingerprint = "v1:other"
	displacing.OwnContent = "v1:other-own"
	displacing.RunID = "other-run"
	displacing.CompletionID = "other-completion"
	displacing.MaterializedAt = first.MaterializedAt.Add(time.Hour)
	require.NoError(t, f.store.Record(context.Background(), displacing))

	assert.Equal(t, StatusNeverBuilt, f.statuses(t, "dev", nil, nil)["a"].Status)
}

func TestTargetAwareStalenessUsesCurrentGenerationOwnContent(t *testing.T) {
	t.Parallel()
	f := newFixture(t,
		sqlAsset("a", "select 1"),
		sqlAsset("b", "select * from a", "a"),
	)
	const target = "renart-physical-target-v1:b"
	f.enableTargetAware(map[string]PhysicalTarget{
		"p:a": {Identity: "renart-physical-target-v1:a", Exact: true},
		"p:b": {Identity: target, Exact: true},
	})
	f.recordTargetRun(t, "dev", target, nil, "b")

	// B's own definition is unchanged, while its Merkle fingerprint changes
	// with A. Classification must use B's own content from the target's current
	// generation, not an arbitrary historical row.
	f.pipeline.Assets[0].ExecutableFile.Content = "select 2"
	assert.Equal(t, StatusStaleUpstream, f.statuses(t, "dev", nil, nil)["b"].Status)
}

func TestTargetAwareStalenessClassifiesSelectedVariablesFromCurrentWriter(t *testing.T) {
	t.Parallel()
	f := newFixture(t, sqlAsset("a", "select '{{ var.region }}'"))
	f.pipeline.Variables = pipeline.Variables{
		"region": map[string]any{"type": "string", "default": "eu"},
	}
	const target = "renart-physical-target-v1:variables"
	f.enableTargetAware(map[string]PhysicalTarget{"p:a": {Identity: target, Exact: true}})
	f.recordTargetRun(t, "dev", target, nil, "a")

	statuses, err := f.service.Statuses(context.Background(), Selection{
		PipelineUUID: "p",
		Environment:  "dev",
		VarOverrides: map[string]any{"region": "us"},
	})
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.Equal(t, StatusStaleUpstream, statuses[0].Status,
		"a different variables hash has no current coverage but is not an own-definition edit")
}

func TestTargetAwareSnapshotExposesSelectedTargetAndLatestOutput(t *testing.T) {
	t.Parallel()
	f := newFixture(t, sqlAsset("a", "select 1"))
	const target = "renart-physical-target-v1:output"
	f.enableTargetAware(map[string]PhysicalTarget{
		"p:a": {Identity: target, Exact: true},
	})
	recorded := f.recordTargetRun(t, "dev", target, nil, "a")

	snapshot, err := f.service.Snapshot(context.Background(), Selection{
		PipelineUUID: "p",
		Environment:  "dev",
	})
	require.NoError(t, err)
	require.NotEmpty(t, snapshot.DataStateToken)
	require.Len(t, snapshot.Assets, 1)
	status := snapshot.Assets[0]
	assert.Equal(t, TargetFidelityExact, status.TargetFidelity)
	assert.Equal(t, target, status.TargetIdentity)
	require.NotNil(t, status.LatestOutput)
	assert.Equal(t, target, status.LatestOutput.TargetIdentity)
	assert.EqualValues(t, 1, status.LatestOutput.TargetGeneration)
	assert.Equal(t, "p:a", status.LatestOutput.WriterAssetID)
	assert.Equal(t, "dev", status.LatestOutput.WriterEnvironment)
	assert.Equal(t, recorded.Fingerprint, status.LatestOutput.Fingerprint)
	assert.Equal(t, recorded.VarsHash, status.LatestOutput.VarsHash)
	assert.Equal(t, recorded.RunID, status.LatestOutput.RunID)
	assert.Equal(t, recorded.MaterializedAt, status.LatestOutput.MaterializedAt)
	assert.Equal(t, recorded.CompletionID, status.LatestOutput.CompletionID)
	assert.Equal(t, recorded.CompletionOrdinal, status.LatestOutput.CompletionOrdinal)
	assert.False(t, status.LatestOutput.Ambiguous)
}

func TestTargetAwareStalenessDistinguishesDeployedSourceFromWorkingTreeEdit(t *testing.T) {
	t.Parallel()
	asset := sqlAsset("a", "select 1")
	f := newFixture(t, asset)
	const target = "renart-physical-target-v1:deployment-drift"
	f.enableTargetAware(map[string]PhysicalTarget{
		"p:a": {Identity: target, Exact: true},
	})

	f.recordTargetRunFromSnapshot(t, "dev", target, nil, "a", "snapshot-old")
	asset.ExecutableFile.Content = "select 2"

	status := f.statuses(t, "dev", nil, nil)["a"]
	assert.Equal(t, StatusStaleDeployment, status.Status)
	require.NotNil(t, status.LatestOutput)
	assert.Equal(t, "snapshot-old", status.LatestOutput.SnapshotVersionID)

	// Once the saved version itself writes the target, later edits are ordinary
	// working-tree drift and must keep the established Edited classification.
	f.recordTargetRun(t, "dev", target, nil, "a")
	require.Equal(t, StatusFresh, f.statuses(t, "dev", nil, nil)["a"].Status)
	asset.ExecutableFile.Content = "select 3"
	assert.Equal(t, StatusStaleEdited, f.statuses(t, "dev", nil, nil)["a"].Status)
}

func TestTargetAwareDataStateTokenTracksGenerationButNotEquivalentRerunMetadata(t *testing.T) {
	t.Parallel()
	asset := sqlAsset("a", "select 1")
	f := newFixture(t, asset)
	const target = "renart-physical-target-v1:token-generation"
	f.enableTargetAware(map[string]PhysicalTarget{
		"p:a": {Identity: target, Exact: true},
	})

	f.recordTargetRun(t, "dev", target, nil, "a")
	first, err := f.service.Snapshot(context.Background(), Selection{PipelineUUID: "p", Environment: "dev"})
	require.NoError(t, err)

	// A same-variant rerun changes the diagnostic run/completion/time fields but
	// keeps the writer generation and needed selection unchanged.
	f.recordTargetRun(t, "dev", target, nil, "a")
	equivalent, err := f.service.Snapshot(context.Background(), Selection{PipelineUUID: "p", Environment: "dev"})
	require.NoError(t, err)
	assert.Equal(t, first.DataStateToken, equivalent.DataStateToken)
	require.NotNil(t, equivalent.Assets[0].LatestOutput)
	assert.NotEqual(t, first.Assets[0].LatestOutput.RunID, equivalent.Assets[0].LatestOutput.RunID)
	assert.EqualValues(t, 1, equivalent.Assets[0].LatestOutput.TargetGeneration)

	// A different variant advances the physical generation, and returning to A
	// advances it again rather than resurrecting generation one's state.
	asset.ExecutableFile.Content = "select 2"
	f.recordTargetRun(t, "dev", target, nil, "a")
	variantB, err := f.service.Snapshot(context.Background(), Selection{PipelineUUID: "p", Environment: "dev"})
	require.NoError(t, err)
	assert.NotEqual(t, equivalent.DataStateToken, variantB.DataStateToken)

	asset.ExecutableFile.Content = "select 1"
	f.recordTargetRun(t, "dev", target, nil, "a")
	returnedA, err := f.service.Snapshot(context.Background(), Selection{PipelineUUID: "p", Environment: "dev"})
	require.NoError(t, err)
	assert.NotEqual(t, first.DataStateToken, returnedA.DataStateToken)
	require.NotNil(t, returnedA.Assets[0].LatestOutput)
	assert.EqualValues(t, 3, returnedA.Assets[0].LatestOutput.TargetGeneration)
}

func TestTargetAwareDataStateTokenChangesForClaimAndCoverageExpansion(t *testing.T) {
	t.Parallel()
	asset := intervalAwareAsset("a")
	f := newFixture(t, asset)
	const target = "renart-physical-target-v1:token-coverage"
	f.enableTargetAware(map[string]PhysicalTarget{
		"p:a": {Identity: target, Exact: true},
	})
	day := func(d int) time.Time { return time.Date(2026, 7, 1+d, 0, 0, 0, 0, time.UTC) }
	selection := Selection{
		PipelineUUID: "p",
		Environment:  "dev",
		Start:        timePointer(day(0)),
		End:          timePointer(day(10)),
	}

	firstWindow := Interval{Start: day(0), End: day(1)}
	f.recordTargetRun(t, "dev", target, &firstWindow, "a")
	first, err := f.service.Snapshot(context.Background(), selection)
	require.NoError(t, err)

	secondWindow := Interval{Start: day(1), End: day(2)}
	f.recordTargetRun(t, "dev", target, &secondWindow, "a")
	expanded, err := f.service.Snapshot(context.Background(), selection)
	require.NoError(t, err)
	assert.NotEqual(t, first.DataStateToken, expanded.DataStateToken)

	claim := matlog.TargetWriteClaim{
		TargetIdentity: target,
		CompletionID:   "pending-completion",
		AssetID:        "p:a",
		ClaimedAt:      day(3),
	}
	require.NoError(t, f.store.ClaimTargetWrite(context.Background(), claim))
	claimed, err := f.service.Snapshot(context.Background(), selection)
	require.NoError(t, err)
	assert.NotEqual(t, expanded.DataStateToken, claimed.DataStateToken)
	require.Nil(t, claimed.Assets[0].LatestOutput)
	assert.Equal(t, StatusNeverBuilt, claimed.Assets[0].Status)
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func intervalAwareAsset(name string) *pipeline.Asset {
	asset := sqlAsset(name, "select * from events")
	asset.Materialization.Strategy = pipeline.MaterializationStrategyTimeInterval
	asset.Materialization.IncrementalKey = "ts"
	return asset
}

func TestPartialCoverageReportsGaps(t *testing.T) {
	t.Parallel()
	f := newFixture(t, intervalAwareAsset("a"))
	day := func(d int) time.Time { return time.Date(2026, 6, 1+d, 0, 0, 0, 0, time.UTC) }

	// Cover days 0-20 of a 30-day selection.
	f.recordRun(t, "dev", &Interval{Start: day(0), End: day(20)}, "a")

	start, end := day(0), day(30)
	status := f.statuses(t, "dev", &start, &end)["a"]
	assert.Equal(t, StatusPartial, status.Status)
	assert.True(t, status.IntervalAware)
	assert.True(t, status.BackfillSafe)
	assert.InDelta(t, (20 * 24 * time.Hour).Seconds(), status.CoveredSeconds, 1)
	assert.InDelta(t, (30 * 24 * time.Hour).Seconds(), status.TotalSeconds, 1)
	require.Len(t, status.Gaps, 1)
	assert.Equal(t, day(20), status.Gaps[0].Start)
	assert.Equal(t, day(30), status.Gaps[0].End)

	// Filling the gap turns it fresh.
	f.recordRun(t, "dev", &Interval{Start: day(20), End: day(30)}, "a")
	status = f.statuses(t, "dev", &start, &end)["a"]
	assert.Equal(t, StatusFresh, status.Status)
	assert.Empty(t, status.Gaps)
}

func TestUnbuiltSelectedRangeReadsAsZeroPartialNotStale(t *testing.T) {
	t.Parallel()
	f := newFixture(t, intervalAwareAsset("a"))
	day := func(d int) time.Time { return time.Date(2026, 6, 1+d, 0, 0, 0, 0, time.UTC) }

	// Build days 10-20, then look at the disjoint range 0-10: the
	// definition is unchanged, so this must read as "0/10 days built" with
	// the whole range as the gap — not as a stale_* fingerprint mismatch.
	f.recordRun(t, "dev", &Interval{Start: day(10), End: day(20)}, "a")

	start, end := day(0), day(10)
	status := f.statuses(t, "dev", &start, &end)["a"]
	assert.Equal(t, StatusPartial, status.Status)
	assert.Zero(t, status.CoveredSeconds)
	require.Len(t, status.Gaps, 1)
	assert.Equal(t, day(0), status.Gaps[0].Start)
	assert.Equal(t, day(10), status.Gaps[0].End)

	// Switching the selector back to the built range reads fresh again.
	builtStart, builtEnd := day(10), day(20)
	assert.Equal(t, StatusFresh, f.statuses(t, "dev", &builtStart, &builtEnd)["a"].Status)

	// An actual edit still reports stale_edited, not partial.
	f.pipeline.Assets[0].ExecutableFile.Content = "select * from events, more_events"
	assert.Equal(t, StatusStaleEdited, f.statuses(t, "dev", &builtStart, &builtEnd)["a"].Status)
}

func TestRunCompletedEventRecomputesAndPublishes(t *testing.T) {
	t.Parallel()
	f := newFixture(t, sqlAsset("a", "select 1"))
	require.Equal(t, StatusNeverBuilt, f.statuses(t, "dev", nil, nil)["a"].Status)

	f.recordRun(t, "dev", nil, "a")
	f.events.EmitRunCompleted(bus.RunCompleted{PipelineUUID: "p", Environment: "dev", CompletedAt: time.Now()})

	select {
	case event := <-f.pushed:
		payload, ok := event.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "staleness.updated", payload["type"])
		assert.NotEmpty(t, payload["data_state_token"])
		statuses, ok := payload["assets"].([]AssetStatus)
		require.True(t, ok)
		require.Len(t, statuses, 1)
		assert.Equal(t, StatusFresh, statuses[0].Status)
	case <-time.After(5 * time.Second):
		t.Fatal("no staleness.updated event published")
	}
}

func TestTargetWriteChangedEventPublishesFailClosedSnapshot(t *testing.T) {
	t.Parallel()
	f := newFixture(t, sqlAsset("a", "select 1"))
	const target = "renart-physical-target-v1:claim-event"
	f.enableTargetAware(map[string]PhysicalTarget{
		"p:a": {Identity: target, Exact: true},
	})
	f.recordTargetRun(t, "dev", target, nil, "a")
	selection := Selection{PipelineUUID: "p", Environment: "dev"}
	fresh, err := f.service.Snapshot(context.Background(), selection)
	require.NoError(t, err)
	require.Len(t, fresh.Assets, 1)
	require.Equal(t, StatusFresh, fresh.Assets[0].Status)

	claim := matlog.TargetWriteClaim{
		TargetIdentity: target,
		CompletionID:   "pending-completion",
		AssetID:        "p:a",
		ClaimedAt:      time.Now().UTC(),
	}
	require.NoError(t, f.store.ClaimTargetWrite(context.Background(), claim))
	f.events.EmitTargetWriteChanged(bus.TargetWriteChanged{PipelineUUID: "p", AssetID: "p:a"})

	select {
	case event := <-f.pushed:
		payload, ok := event.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "staleness.updated", payload["type"])
		assert.NotEqual(t, fresh.DataStateToken, payload["data_state_token"])
		statuses, ok := payload["assets"].([]AssetStatus)
		require.True(t, ok)
		require.Len(t, statuses, 1)
		assert.Equal(t, StatusNeverBuilt, statuses[0].Status)
		assert.Nil(t, statuses[0].LatestOutput)
	case <-time.After(5 * time.Second):
		t.Fatal("no fail-closed staleness.updated event published for target claim")
	}
}

func TestVerificationDowngradesToMissing(t *testing.T) {
	t.Parallel()
	schedStore, err := scheduler.OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = schedStore.Close() })

	store := matlog.NewStore(schedStore.DB())
	engine := fingerprint.NewEngine()
	p := &pipeline.Pipeline{
		LegacyID:       "p",
		Name:           "test",
		DefinitionFile: pipeline.DefinitionFile{Path: "/w/p/pipeline.yml"},
		Assets:         []*pipeline.Asset{sqlAsset("a", "select 1")},
	}

	pushed := make(chan any, 16)
	verifyCalls := make(chan []string, 2)
	service := New(Dependencies{
		Store:   store,
		Engine:  engine,
		Resolve: func(ctx context.Context, uuid string) (*pipeline.Pipeline, error) { return p, nil },
		Publish: func(event any) { pushed <- event },
		Verify: func(ctx context.Context, selection Selection, assetNames []string) (map[string]bool, error) {
			verifyCalls <- assetNames
			return map[string]bool{"a": false}, nil
		},
	})

	vars := fingerprint.EffectiveVars(p, nil)
	results, err := engine.DAG(p, vars)
	require.NoError(t, err)
	result := results["p:a"]
	require.NoError(t, store.Record(context.Background(), matlog.Materialization{
		AssetID: "p:a", Environment: "dev",
		Fingerprint: string(result.FP), OwnContent: string(result.OwnContent),
		VarsHash: fingerprint.AllVarsHash(vars), RunID: "run", MaterializedAt: time.Now().UTC(),
	}))

	selection := Selection{PipelineUUID: "p", Environment: "dev"}
	statuses, err := service.Statuses(context.Background(), selection)
	require.NoError(t, err)
	assert.Equal(t, StatusFresh, statuses[0].Status)

	select {
	case names := <-verifyCalls:
		assert.Equal(t, []string{"a"}, names)
	case <-time.After(5 * time.Second):
		t.Fatal("verifier never called")
	}

	// The verification republish downgrades the asset to missing.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-pushed:
			payload := event.(map[string]any)
			statuses := payload["assets"].([]AssetStatus)
			if statuses[0].Status == StatusMissing {
				return
			}
		case <-deadline:
			t.Fatal("asset never downgraded to missing")
		}
	}
}

func TestUnavailableVerificationKeepsRecordedCoverageFresh(t *testing.T) {
	t.Parallel()
	f := newFixture(t, sqlAsset("a", "select 1"))
	f.recordRun(t, "dev", nil, "a")
	verifyCalls := make(chan struct{}, 1)
	f.service.TestSetVerify(func(
		context.Context,
		Selection,
		[]string,
	) (map[string]bool, error) {
		verifyCalls <- struct{}{}
		// An omitted asset is unknown, not confirmed absent. This is how a
		// locked local vault or temporarily unavailable warehouse is reported.
		return map[string]bool{}, nil
	})

	assert.Equal(t, StatusFresh, f.statuses(t, "dev", nil, nil)["a"].Status)
	select {
	case <-verifyCalls:
	case <-time.After(5 * time.Second):
		t.Fatal("verifier never called")
	}
	assert.Equal(t, StatusFresh, f.statuses(t, "dev", nil, nil)["a"].Status)
}

func TestSuccessfulRunClearsRememberedMissingVerification(t *testing.T) {
	t.Parallel()
	f := newFixture(t, sqlAsset("a", "select 1"))
	f.recordRun(t, "dev", nil, "a")
	present := false
	f.service.TestSetVerify(func(
		context.Context,
		Selection,
		[]string,
	) (map[string]bool, error) {
		return map[string]bool{"a": present}, nil
	})

	assert.Equal(t, StatusFresh, f.statuses(t, "dev", nil, nil)["a"].Status)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case event := <-f.pushed:
			payload := event.(map[string]any)
			statuses := payload["assets"].([]AssetStatus)
			if statuses[0].Status == StatusMissing {
				goto missingObserved
			}
		case <-deadline:
			t.Fatal("asset never became missing")
		}
	}

missingObserved:
	present = true
	f.recordRun(t, "dev", nil, "a")
	f.events.EmitRunCompleted(bus.RunCompleted{
		PipelineUUID: "p",
		Environment:  "dev",
		CompletedAt:  time.Now().UTC(),
		Assets: []bus.AssetRun{{
			AssetID:   "p:a",
			AssetName: "a",
			Status:    "succeeded",
		}},
	})

	deadline = time.After(5 * time.Second)
	for {
		select {
		case event := <-f.pushed:
			payload := event.(map[string]any)
			statuses := payload["assets"].([]AssetStatus)
			if statuses[0].Status == StatusFresh {
				return
			}
		case <-deadline:
			t.Fatal("successful run did not clear the remembered missing status")
		}
	}
}

func TestLoadAssetNotDowngradedToMissing(t *testing.T) {
	t.Parallel()
	schedStore, err := scheduler.OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = schedStore.Close() })

	store := matlog.NewStore(schedStore.DB())
	engine := fingerprint.NewEngine()
	load := &pipeline.Asset{
		Name:       "example.to_csv",
		Type:       "load",
		Connection: "local",
		Parameters: pipeline.ParameterMap{
			"source_connection":  "duckdb-default",
			"source_table":       "example.orders",
			"destination_object": "./out.csv",
		},
		ExecutableFile: pipeline.ExecutableFile{
			Path:    "/w/p/assets/to_csv.asset.yml",
			Content: "type: load\nconnection: local\nparameters:\n  source_connection: duckdb-default\n  source_table: example.orders\n  destination_object: ./out.csv\n",
		},
	}
	p := &pipeline.Pipeline{
		LegacyID:       "p",
		Name:           "test",
		DefinitionFile: pipeline.DefinitionFile{Path: "/w/p/pipeline.yml"},
		Assets:         []*pipeline.Asset{load},
	}

	pushed := make(chan any, 16)
	service := New(Dependencies{
		Store:   store,
		Engine:  engine,
		Resolve: func(ctx context.Context, uuid string) (*pipeline.Pipeline, error) { return p, nil },
		Publish: func(event any) { pushed <- event },
		// The warehouse has no table named after the load asset (it wrote a csv).
		Verify: func(ctx context.Context, selection Selection, assetNames []string) (map[string]bool, error) {
			return map[string]bool{"example.to_csv": false}, nil
		},
	})

	vars := fingerprint.EffectiveVars(p, nil)
	results, err := engine.DAG(p, vars)
	require.NoError(t, err)
	result, ok := results["p:example.to_csv"]
	require.True(t, ok)
	require.NoError(t, store.Record(context.Background(), matlog.Materialization{
		AssetID: "p:example.to_csv", Environment: "dev",
		Fingerprint: string(result.FP), OwnContent: string(result.OwnContent),
		VarsHash: fingerprint.AllVarsHash(vars), RunID: "run", MaterializedAt: time.Now().UTC(),
	}))

	selection := Selection{PipelineUUID: "p", Environment: "dev"}
	statuses, err := service.Statuses(context.Background(), selection)
	require.NoError(t, err)
	require.Len(t, statuses, 1)
	assert.Equal(t, StatusFresh, statuses[0].Status)

	// The verifier reports it missing and republishes, but a load asset must stay
	// fresh — its freshness rests on the run fact, not a warehouse-table lookup.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-pushed:
			payload := event.(map[string]any)
			republished := payload["assets"].([]AssetStatus)
			assert.Equal(t, StatusFresh, republished[0].Status)
		case <-deadline:
			return
		}
	}
}

func TestVerificationThrottledPerSession(t *testing.T) {
	t.Parallel()
	f := newFixture(t, sqlAsset("a", "select 1"))
	calls := 0
	f.service.TestSetVerify(func(ctx context.Context, selection Selection, assetNames []string) (map[string]bool, error) {
		calls++
		return map[string]bool{"a": true}, nil
	})
	f.recordRun(t, "dev", nil, "a")

	f.statuses(t, "dev", nil, nil)
	f.statuses(t, "dev", nil, nil)
	time.Sleep(100 * time.Millisecond)
	assert.LessOrEqual(t, calls, 1)
}
