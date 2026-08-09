package completion_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/web/bus"
	"renart/internal/web/completion"
	"renart/internal/web/scheduler"
)

func TestOutboxRoundTripsCompleteExecutionEvidence(t *testing.T) {
	t.Parallel()
	store, schedulerStore := newOutboxStore(t)
	ctx := context.Background()
	event := completeEvent("completion-roundtrip")

	require.NoError(t, store.Enqueue(ctx, event))
	require.NoError(t, store.Enqueue(ctx, event), "an exact retry is idempotent")

	pending, err := store.ListPending(ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Positive(t, pending[0].Sequence)
	assert.False(t, pending[0].EnqueuedAt.IsZero())

	want := event
	want.WinStart = utcPointer(want.WinStart)
	want.WinEnd = utcPointer(want.WinEnd)
	want.CompletedAt = want.CompletedAt.UTC()
	want.SnapshotDir = ""
	for index := range want.Assets {
		want.Assets[index].StartedAt = utcPointer(want.Assets[index].StartedAt)
		want.Assets[index].FinishedAt = utcPointer(want.Assets[index].FinishedAt)
	}
	assert.Equal(t, want, pending[0].Event)

	var body string
	require.NoError(t, schedulerStore.DB().QueryRowContext(ctx, `
		SELECT body FROM renart_completion_outbox WHERE completion_id = ?`, event.CompletionID).Scan(&body))
	assert.NotContains(t, body, event.SnapshotDir, "ephemeral snapshot paths must never enter durable state")
}

func TestOutboxRoundTripsVersionFourExecutionContracts(t *testing.T) {
	t.Parallel()
	store, _ := newOutboxStore(t)
	ctx := context.Background()
	event := completeEvent("completion-v4")
	event.ExecutionTargetSnapshotVersion = 4
	entry := event.ExecutionTargets["analytics.orders"]
	resource := bus.ExecutionResourceClaim{
		Kind: entry.WriteResourceKind, Identity: entry.WriteResourceIdentity,
	}
	entry.ExecutionContract = bus.ExecutionContractSnapshot{
		AssetID:        entry.AssetID,
		AssetName:      "analytics.orders",
		ConnectionKeys: []string{strings.Repeat("b", 64)},
		MutationResources: bus.ExecutionResources{
			Isolation: "resources", Claims: []bus.ExecutionResourceClaim{resource},
		},
		CoordinationResources: bus.ExecutionResources{
			Isolation: "resources", Claims: []bus.ExecutionResourceClaim{resource},
		},
	}
	event.ExecutionTargets["analytics.orders"] = entry

	require.NoError(t, store.Enqueue(ctx, event))
	pending, err := store.ListPending(ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, entry.ExecutionContract, pending[0].Event.ExecutionTargets["analytics.orders"].ExecutionContract)
}

func TestOutboxAcceptsReviewedCrossPipelineUpstreamWriter(t *testing.T) {
	t.Parallel()
	store, _ := newOutboxStore(t)
	ctx := context.Background()
	event := completeEvent("completion-cross-pipeline")
	producerID := "producer-uuid:raw.orders"
	writer := bus.UpstreamWriterSnapshot{
		AssetID: producerID, TargetIdentity: strings.Repeat("c", 64),
		Fingerprint: "v3:producer", VarsHash: strings.Repeat("d", 64),
		TargetGeneration: 2, CompletionID: "producer-completion", CompletionOrdinal: 1,
		MaterializedAt: time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC),
	}
	entry := event.ExecutionTargets["analytics.orders"]
	entry.Upstreams = append(entry.Upstreams, bus.ExecutionUpstreamSnapshot{
		Type: "uri", Value: "duckdb://warehouse/raw/orders", Required: true,
		ResolvedAssetID: producerID, ProducerPipelineUUID: "producer-uuid",
		ProducerSnapshotVersionID: "producer-deployment", TargetIdentity: writer.TargetIdentity,
		ExpectedFingerprint: writer.Fingerprint, VarsHash: writer.VarsHash,
		TargetGeneration: writer.TargetGeneration, CompletionID: writer.CompletionID,
		CompletionOrdinal: writer.CompletionOrdinal,
	})
	event.ExecutionTargets["analytics.orders"] = entry
	event.Assets[0].UpstreamWriters[producerID] = writer

	require.NoError(t, store.Enqueue(ctx, event))
	pending, err := store.ListPending(ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, writer, pending[0].Event.Assets[0].UpstreamWriters[producerID])

	tampered := event
	tampered.CompletionID = "completion-cross-pipeline-tampered"
	tampered.Assets = append([]bus.AssetRun(nil), event.Assets...)
	tampered.Assets[0].UpstreamWriters = map[string]bus.UpstreamWriterSnapshot{producerID: writer}
	changed := tampered.Assets[0].UpstreamWriters[producerID]
	changed.TargetGeneration++
	tampered.Assets[0].UpstreamWriters[producerID] = changed
	require.ErrorIs(t, store.Enqueue(ctx, tampered), completion.ErrInvalidEnvelope)
}

func TestOutboxRejectsConflictingCompletionEvidence(t *testing.T) {
	t.Parallel()
	store, _ := newOutboxStore(t)
	ctx := context.Background()
	event := completeEvent("completion-conflict")
	require.NoError(t, store.Enqueue(ctx, event))

	conflict := event
	conflict.Environment = "staging"
	err := store.Enqueue(ctx, conflict)
	require.ErrorIs(t, err, completion.ErrCompletionConflict)

	pending, err := store.ListPending(ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "prod", pending[0].Event.Environment)
}

func TestOutboxListsInInsertionOrderAndAcknowledgesOnlyAfterDispatch(t *testing.T) {
	t.Parallel()
	store, _ := newOutboxStore(t)
	ctx := context.Background()
	for _, completionID := range []string{"completion-z", "completion-a", "completion-m"} {
		require.NoError(t, store.Enqueue(ctx, completeEvent(completionID)))
	}

	pending, err := store.ListPending(ctx)
	require.NoError(t, err)
	require.Len(t, pending, 3)
	assert.Equal(t, []string{"completion-z", "completion-a", "completion-m"}, completionIDs(pending))
	assert.Less(t, pending[0].Sequence, pending[1].Sequence)
	assert.Less(t, pending[1].Sequence, pending[2].Sequence)

	dispatchErr := errors.New("derived state is unavailable")
	var attempted []string
	err = store.DispatchPending(ctx, func(event bus.RunCompleted) error {
		attempted = append(attempted, event.CompletionID)
		return dispatchErr
	})
	require.ErrorIs(t, err, dispatchErr)
	assert.Equal(t, []string{"completion-z"}, attempted)
	pending, err = store.ListPending(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"completion-z", "completion-a", "completion-m"}, completionIDs(pending))

	attempted = nil
	require.NoError(t, store.DispatchPending(ctx, func(event bus.RunCompleted) error {
		attempted = append(attempted, event.CompletionID)
		return nil
	}))
	assert.Equal(t, []string{"completion-z", "completion-a", "completion-m"}, attempted)
	pending, err = store.ListPending(ctx)
	require.NoError(t, err)
	assert.Empty(t, pending)
	require.ErrorIs(t, store.Acknowledge(ctx, "completion-z"), completion.ErrCompletionNotFound)
}

func TestOutboxDispatchToleratesConcurrentAcknowledgement(t *testing.T) {
	t.Parallel()
	store, _ := newOutboxStore(t)
	ctx := context.Background()
	event := completeEvent("completion-concurrent-ack")
	require.NoError(t, store.Enqueue(ctx, event))

	require.NoError(t, store.DispatchPending(ctx, func(delivered bus.RunCompleted) error {
		assert.Equal(t, event.CompletionID, delivered.CompletionID)
		return store.Acknowledge(ctx, delivered.CompletionID)
	}))
	pending, err := store.ListPending(ctx)
	require.NoError(t, err)
	assert.Empty(t, pending)
}

func TestOutboxRejectsInvalidAndNoncanonicalEnvelopes(t *testing.T) {
	t.Parallel()
	store, schedulerStore := newOutboxStore(t)
	ctx := context.Background()

	invalid := completeEvent("")
	require.ErrorIs(t, store.Enqueue(ctx, invalid), completion.ErrInvalidEnvelope)
	invalid = completeEvent(" completion-id")
	require.ErrorIs(t, store.Enqueue(ctx, invalid), completion.ErrInvalidEnvelope)
	invalid = completeEvent("completion-no-time")
	invalid.CompletedAt = time.Time{}
	require.ErrorIs(t, store.Enqueue(ctx, invalid), completion.ErrInvalidEnvelope)
	invalid = completeEvent("completion-v1")
	invalid.ExecutionTargetSnapshotVersion = 1
	require.ErrorIs(t, store.Enqueue(ctx, invalid), completion.ErrInvalidEnvelope)
	invalid = completeEvent("completion-no-targets")
	invalid.ExecutionTargets = nil
	require.ErrorIs(t, store.Enqueue(ctx, invalid), completion.ErrInvalidEnvelope)
	invalid = completeEvent("completion-no-write-resource")
	entry := invalid.ExecutionTargets["analytics.orders"]
	entry.WriteResourceFidelity = ""
	invalid.ExecutionTargets["analytics.orders"] = entry
	require.ErrorIs(t, store.Enqueue(ctx, invalid), completion.ErrInvalidEnvelope)
	invalid = completeEvent("completion-invalid-quality")
	invalid.Assets[0].QualityStatus = bus.QualityStatusFailed
	invalid.Assets[0].FailedChecks = nil
	require.ErrorIs(t, store.Enqueue(ctx, invalid), completion.ErrInvalidEnvelope)
	invalid = completeEvent("completion-unsorted-quality")
	invalid.Assets[0].QualityStatus = bus.QualityStatusFailed
	invalid.Assets[0].FailedChecks = []bus.QualityCheckFailure{
		{Kind: bus.QualityCheckKindCustom, Name: "z"},
		{Kind: bus.QualityCheckKindColumn, Name: "not_null", Column: "id"},
	}
	require.ErrorIs(t, store.Enqueue(ctx, invalid), completion.ErrInvalidEnvelope)

	event := completeEvent("completion-tampered")
	require.NoError(t, store.Enqueue(ctx, event))
	_, err := schedulerStore.DB().ExecContext(ctx, `
		UPDATE renart_completion_outbox
		SET body = json_set(body, '$.future_behavior', 1)
		WHERE completion_id = ?`, event.CompletionID)
	require.NoError(t, err)
	_, err = store.ListPending(ctx)
	require.ErrorIs(t, err, completion.ErrInvalidEnvelope)
}

func newOutboxStore(t *testing.T) (*completion.Store, *scheduler.Store) {
	t.Helper()
	schedulerStore, err := scheduler.OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, schedulerStore.Close()) })
	return completion.NewStore(schedulerStore.DB()), schedulerStore
}

func completeEvent(completionID string) bus.RunCompleted {
	started := time.Date(2026, 7, 17, 10, 0, 0, 123456789, time.FixedZone("CEST", 2*60*60))
	finished := started.Add(90 * time.Second)
	windowStart := started.Add(-24 * time.Hour)
	windowEnd := started
	return bus.RunCompleted{
		RunID: "run-id", CompletionID: completionID,
		PipelineUUID: "pipeline-uuid", Environment: "prod",
		WinStart: &windowStart, WinEnd: &windowEnd, FullRefresh: true,
		CompletedAt: finished.Add(time.Second),
		Assets: []bus.AssetRun{{
			AssetID: "pipeline-uuid:analytics.orders", AssetName: "analytics.orders",
			Status: "succeeded", StartedAt: &started, FinishedAt: &finished,
			QualityStatus: bus.QualityStatusFailed,
			FailedChecks: []bus.QualityCheckFailure{{
				Kind: bus.QualityCheckKindCustom, Name: "no duplicate orders", Blocking: true,
			}},
			CompletionOrdinal: 3, HasCompletionOrdinal: true,
			TargetIdentity: "duckdb|main|analytics|orders", TargetFidelity: "exact",
			Fingerprint: "v2:fingerprint", OwnContent: "v2:own",
			ConsumedVarsHash: "consumed-vars", VarsHash: "all-vars",
			UpstreamWriters: map[string]bus.UpstreamWriterSnapshot{}, HasUpstreamWriterSnapshot: true,
		}},
		ExecutionTargetSnapshotVersion: 3,
		ExecutionPipelineUUID:          "pipeline-uuid",
		ExecutionTargets: map[string]bus.ExecutionTargetSnapshotEntry{
			"analytics.orders": {
				AssetID:                     "pipeline-uuid:analytics.orders",
				TargetIdentity:              "duckdb|main|analytics|orders",
				TargetFidelity:              "exact",
				TargetWriteEvidenceRequired: true,
				WriteResourceKind:           "duckdb_database",
				WriteResourceIdentity:       strings.Repeat("a", 64),
				WriteResourceFidelity:       "exact",
				Fingerprint:                 "v2:fingerprint", OwnContent: "v2:own",
				ConsumedVarsHash: "consumed-vars", VarsHash: "all-vars",
				Upstreams: []bus.ExecutionUpstreamSnapshot{
					{Type: "asset", Value: "raw.orders"},
					{Type: "external", Value: "s3://bucket/orders"},
				},
				CoverageMode: "union_intervals", RefreshRestricted: true,
			},
		},
		SnapshotVersionID: "snapshot-version",
		SnapshotDir:       "/ephemeral/deployed/snapshot",
	}
}

func completionIDs(pending []completion.Pending) []string {
	ids := make([]string, len(pending))
	for index, item := range pending {
		ids[index] = item.Event.CompletionID
	}
	return ids
}

func utcPointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}
