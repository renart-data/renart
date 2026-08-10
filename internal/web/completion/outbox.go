// Package completion persists completed-run events before they are dispatched
// to derived-state consumers. The outbox is intentionally independent of the
// scheduler and materialization recorder so both normal execution and startup
// recovery can use the same durable hand-off.
package completion

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"renart/internal/web/bus"
)

const (
	envelopeVersionV1       = 1
	targetSnapshotVersionV2 = 2
	targetSnapshotVersionV3 = 3
	targetSnapshotVersionV4 = 4
	targetSnapshotVersionV5 = 5
)

var (
	ErrInvalidEnvelope    = errors.New("invalid completion outbox envelope")
	ErrCompletionConflict = errors.New("completion id already has different evidence")
	ErrCompletionNotFound = errors.New("completion is not pending")
)

// Store uses the scheduler's state database. Migrations are owned by the
// scheduler store, so callers must construct this after scheduler.OpenStore.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// Pending is a durable completion awaiting derived-state dispatch. Sequence
// is assigned by SQLite and makes replay order independent of wall-clock ties.
type Pending struct {
	Sequence   int64
	EnqueuedAt time.Time
	Event      bus.RunCompleted
}

// Enqueue persists the canonical, path-independent completion envelope. An
// exact retry is idempotent; reusing a completion ID with different durable
// evidence fails closed.
func (s *Store) Enqueue(ctx context.Context, event bus.RunCompleted) error {
	if s == nil || s.db == nil {
		return errors.New("completion outbox store is not initialized")
	}
	body, err := marshalEvent(event)
	if err != nil {
		return err
	}
	completionID := event.CompletionID

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin completion outbox enqueue: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO renart_completion_outbox
			(completion_id, version, body, enqueued_at)
		VALUES (?, ?, ?, ?)`,
		completionID,
		envelopeVersionV1,
		string(body),
		formatTime(time.Now().UTC()),
	)
	if err != nil {
		return fmt.Errorf("enqueue completion %s: %w", completionID, err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect completion %s enqueue: %w", completionID, err)
	}
	if inserted == 1 {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit completion %s enqueue: %w", completionID, err)
		}
		return nil
	}

	var version int
	var existing string
	if err := tx.QueryRowContext(ctx, `
		SELECT version, body
		FROM renart_completion_outbox
		WHERE completion_id = ?`, completionID).Scan(&version, &existing); err != nil {
		return fmt.Errorf("load existing completion %s: %w", completionID, err)
	}
	if version != envelopeVersionV1 || existing != string(body) {
		return fmt.Errorf("%w: %s", ErrCompletionConflict, completionID)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit idempotent completion %s enqueue: %w", completionID, err)
	}
	return nil
}

// ListPending returns all pending completions in insertion order. Every row is
// decoded strictly and checked for canonical encoding before it can be replayed.
func (s *Store) ListPending(ctx context.Context) ([]Pending, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("completion outbox store is not initialized")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT sequence, completion_id, version, body, enqueued_at
		FROM renart_completion_outbox
		ORDER BY sequence`)
	if err != nil {
		return nil, fmt.Errorf("list pending completions: %w", err)
	}
	defer rows.Close()

	pending := make([]Pending, 0)
	for rows.Next() {
		var (
			sequence     int64
			completionID string
			version      int
			body         string
			enqueuedAt   string
		)
		if err := rows.Scan(&sequence, &completionID, &version, &body, &enqueuedAt); err != nil {
			return nil, fmt.Errorf("scan pending completion: %w", err)
		}
		event, err := unmarshalEvent(completionID, version, body)
		if err != nil {
			return nil, fmt.Errorf("decode pending completion %s: %w", completionID, err)
		}
		queuedAt, err := time.Parse(time.RFC3339Nano, enqueuedAt)
		if err != nil {
			return nil, fmt.Errorf("decode pending completion %s enqueue time: %w", completionID, err)
		}
		pending = append(pending, Pending{
			Sequence: sequence, EnqueuedAt: queuedAt.UTC(), Event: event,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending completions: %w", err)
	}
	return pending, nil
}

// Acknowledge removes a completion after every derived-state consumer has
// accepted it. Callers should normally use DispatchPending, which enforces
// that ordering structurally.
func (s *Store) Acknowledge(ctx context.Context, completionID string) error {
	if s == nil || s.db == nil {
		return errors.New("completion outbox store is not initialized")
	}
	if err := validateCompletionID(completionID); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM renart_completion_outbox
		WHERE completion_id = ?`, completionID)
	if err != nil {
		return fmt.Errorf("acknowledge completion %s: %w", completionID, err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect completion %s acknowledgement: %w", completionID, err)
	}
	if deleted != 1 {
		return fmt.Errorf("%w: %s", ErrCompletionNotFound, completionID)
	}
	return nil
}

// DispatchPending replays completions deterministically and acknowledges each
// one only after dispatch succeeds. It stops at the first failure, preserving
// that completion and every later row for a future retry.
func (s *Store) DispatchPending(
	ctx context.Context,
	dispatch func(bus.RunCompleted) error,
) error {
	if dispatch == nil {
		return errors.New("completion dispatch function is required")
	}
	pending, err := s.ListPending(ctx)
	if err != nil {
		return err
	}
	for _, item := range pending {
		if err := dispatch(item.Event); err != nil {
			return fmt.Errorf("dispatch completion %s: %w", item.Event.CompletionID, err)
		}
		if err := s.Acknowledge(ctx, item.Event.CompletionID); err != nil && !errors.Is(err, ErrCompletionNotFound) {
			return err
		}
		// Another process may have listed and successfully dispatched the same
		// idempotent envelope before this acknowledgement. A missing row here is
		// therefore a completed race, not a replay failure.
	}
	return nil
}

type envelopeV1 struct {
	Version int            `json:"version"`
	Event   completedRunV1 `json:"event"`
}

type completedRunV1 struct {
	RunID                          string                       `json:"run_id"`
	CompletionID                   string                       `json:"completion_id"`
	PipelineUUID                   string                       `json:"pipeline_uuid"`
	Environment                    string                       `json:"environment"`
	WinStart                       *time.Time                   `json:"win_start"`
	WinEnd                         *time.Time                   `json:"win_end"`
	FullRefresh                    bool                         `json:"full_refresh"`
	CompletedAt                    time.Time                    `json:"completed_at"`
	Assets                         []assetRunV1                 `json:"assets"`
	ExecutionTargetSnapshotVersion int                          `json:"execution_target_snapshot_version"`
	ExecutionPipelineUUID          string                       `json:"execution_pipeline_uuid"`
	ExecutionTargets               map[string]executionTargetV1 `json:"execution_targets"`
	SnapshotVersionID              string                       `json:"snapshot_version_id"`
}

type assetRunV1 struct {
	AssetID                   string                      `json:"asset_id"`
	AssetName                 string                      `json:"asset_name"`
	Status                    string                      `json:"status"`
	QualityStatus             bus.QualityStatus           `json:"quality_status,omitempty"`
	FailedChecks              []qualityCheckFailureV1     `json:"failed_checks,omitempty"`
	StartedAt                 *time.Time                  `json:"started_at"`
	FinishedAt                *time.Time                  `json:"finished_at"`
	CompletionOrdinal         int64                       `json:"completion_ordinal"`
	HasCompletionOrdinal      bool                        `json:"has_completion_ordinal"`
	TargetIdentity            string                      `json:"target_identity"`
	TargetFidelity            string                      `json:"target_fidelity"`
	Fingerprint               string                      `json:"fingerprint"`
	OwnContent                string                      `json:"own_content"`
	ConsumedVarsHash          string                      `json:"consumed_vars_hash"`
	VarsHash                  string                      `json:"vars_hash"`
	UpstreamWriters           map[string]upstreamWriterV1 `json:"upstream_writers"`
	HasUpstreamWriterSnapshot bool                        `json:"has_upstream_writer_snapshot"`
}

type qualityCheckFailureV1 struct {
	Kind     bus.QualityCheckKind `json:"kind"`
	Name     string               `json:"name"`
	Column   string               `json:"column,omitempty"`
	Blocking bool                 `json:"blocking,omitempty"`
}

type upstreamWriterV1 struct {
	AssetID           string    `json:"asset_id"`
	TargetIdentity    string    `json:"target_identity"`
	Fingerprint       string    `json:"fingerprint"`
	VarsHash          string    `json:"vars_hash"`
	TargetGeneration  int64     `json:"target_generation"`
	CompletionID      string    `json:"completion_id"`
	CompletionOrdinal int64     `json:"completion_ordinal"`
	MaterializedAt    time.Time `json:"materialized_at"`
}

type executionTargetV1 struct {
	AssetID                     string                `json:"asset_id"`
	ExternalSource              bool                  `json:"external_source,omitempty"`
	TargetIdentity              string                `json:"target_identity"`
	TargetFidelity              string                `json:"target_fidelity"`
	TargetWriteEvidenceRequired bool                  `json:"target_write_evidence_required,omitempty"`
	WriteResourceKind           string                `json:"write_resource_kind,omitempty"`
	WriteResourceIdentity       string                `json:"write_resource_identity,omitempty"`
	WriteResourceFidelity       string                `json:"write_resource_fidelity,omitempty"`
	ExecutionContract           *executionContractV1  `json:"execution_contract,omitempty"`
	Fingerprint                 string                `json:"fingerprint"`
	OwnContent                  string                `json:"own_content"`
	ConsumedVarsHash            string                `json:"consumed_vars_hash"`
	VarsHash                    string                `json:"vars_hash"`
	Upstreams                   []executionUpstreamV1 `json:"upstreams"`
	CoverageMode                string                `json:"coverage_mode"`
	RefreshRestricted           bool                  `json:"refresh_restricted"`
}

type executionContractV1 struct {
	AssetID               string               `json:"asset_id"`
	AssetName             string               `json:"asset_name"`
	ConnectionKeys        []string             `json:"connection_keys"`
	MutationResources     executionResourcesV1 `json:"mutation_resources"`
	CoordinationResources executionResourcesV1 `json:"coordination_resources"`
}

type executionResourcesV1 struct {
	Isolation string                     `json:"isolation"`
	Claims    []executionResourceClaimV1 `json:"claims"`
}

type executionResourceClaimV1 struct {
	Kind     string `json:"kind"`
	Identity string `json:"identity"`
}

type executionUpstreamV1 struct {
	Type                      string `json:"type"`
	Value                     string `json:"value"`
	Mode                      string `json:"mode,omitempty"`
	ResolvedAssetID           string `json:"resolved_asset_id,omitempty"`
	Required                  bool   `json:"required,omitempty"`
	ProducerPipelineUUID      string `json:"producer_pipeline_uuid,omitempty"`
	ProducerSnapshotVersionID string `json:"producer_snapshot_version_id,omitempty"`
	TargetIdentity            string `json:"target_identity,omitempty"`
	ExpectedFingerprint       string `json:"expected_fingerprint,omitempty"`
	VarsHash                  string `json:"vars_hash,omitempty"`
	TargetGeneration          int64  `json:"target_generation,omitempty"`
	CompletionID              string `json:"completion_id,omitempty"`
	CompletionOrdinal         int64  `json:"completion_ordinal,omitempty"`
}

func marshalEvent(event bus.RunCompleted) ([]byte, error) {
	if err := validateEvent(event); err != nil {
		return nil, err
	}
	envelope := envelopeV1{Version: envelopeVersionV1, Event: eventToV1(event)}
	body, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("%w: encode: %v", ErrInvalidEnvelope, err)
	}
	return body, nil
}

func unmarshalEvent(completionID string, version int, body string) (bus.RunCompleted, error) {
	if err := validateCompletionID(completionID); err != nil {
		return bus.RunCompleted{}, err
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.DisallowUnknownFields()
	var envelope envelopeV1
	if err := decoder.Decode(&envelope); err != nil {
		return bus.RunCompleted{}, fmt.Errorf("%w: decode: %v", ErrInvalidEnvelope, err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return bus.RunCompleted{}, err
	}
	if version != envelopeVersionV1 || envelope.Version != envelopeVersionV1 || version != envelope.Version {
		return bus.RunCompleted{}, fmt.Errorf(
			"%w: unsupported or inconsistent version column=%d body=%d",
			ErrInvalidEnvelope,
			version,
			envelope.Version,
		)
	}
	if envelope.Event.CompletionID != completionID {
		return bus.RunCompleted{}, fmt.Errorf(
			"%w: completion id column %q does not match body %q",
			ErrInvalidEnvelope,
			completionID,
			envelope.Event.CompletionID,
		)
	}
	event := eventFromV1(envelope.Event)
	canonical, err := marshalEvent(event)
	if err != nil {
		return bus.RunCompleted{}, err
	}
	if !bytes.Equal(canonical, []byte(body)) {
		return bus.RunCompleted{}, fmt.Errorf("%w: body is not canonical", ErrInvalidEnvelope)
	}
	return event, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: trailing content: %v", ErrInvalidEnvelope, err)
	}
	return fmt.Errorf("%w: trailing JSON value", ErrInvalidEnvelope)
}

func validateCompletionID(completionID string) error {
	if completionID == "" || strings.TrimSpace(completionID) != completionID {
		return fmt.Errorf("%w: completion_id must be non-empty and canonical", ErrInvalidEnvelope)
	}
	return nil
}

func validateEvent(event bus.RunCompleted) error {
	if err := validateCompletionID(event.CompletionID); err != nil {
		return err
	}
	if event.CompletedAt.IsZero() {
		return fmt.Errorf("%w: completed_at is required", ErrInvalidEnvelope)
	}
	if (event.WinStart == nil) != (event.WinEnd == nil) {
		return fmt.Errorf("%w: execution window must be complete", ErrInvalidEnvelope)
	}
	pipelineUUID := strings.TrimSpace(event.PipelineUUID)
	if pipelineUUID == "" || pipelineUUID != event.PipelineUUID {
		return fmt.Errorf("%w: pipeline_uuid must be non-empty and canonical", ErrInvalidEnvelope)
	}
	if event.ExecutionTargetSnapshotVersion != targetSnapshotVersionV2 &&
		event.ExecutionTargetSnapshotVersion != targetSnapshotVersionV3 &&
		event.ExecutionTargetSnapshotVersion != targetSnapshotVersionV4 &&
		event.ExecutionTargetSnapshotVersion != targetSnapshotVersionV5 {
		return fmt.Errorf(
			"%w: execution target snapshot version %d is not a supported self-contained version",
			ErrInvalidEnvelope,
			event.ExecutionTargetSnapshotVersion,
		)
	}
	if event.ExecutionPipelineUUID != pipelineUUID {
		return fmt.Errorf("%w: execution pipeline identity does not match completion", ErrInvalidEnvelope)
	}
	if len(event.ExecutionTargets) == 0 {
		return fmt.Errorf("%w: execution target snapshot is empty", ErrInvalidEnvelope)
	}
	if len(event.Assets) == 0 {
		return fmt.Errorf("%w: at least one terminal asset is required", ErrInvalidEnvelope)
	}

	assetIDs := make(map[string]string, len(event.ExecutionTargets))
	commonVarsHash := ""
	for assetName, entry := range event.ExecutionTargets {
		if strings.TrimSpace(assetName) == "" || strings.TrimSpace(assetName) != assetName {
			return fmt.Errorf("%w: execution target key %q is not canonical", ErrInvalidEnvelope, assetName)
		}
		if strings.TrimSpace(entry.AssetID) == "" || strings.TrimSpace(entry.AssetID) != entry.AssetID {
			return fmt.Errorf("%w: execution target %q has no canonical asset_id", ErrInvalidEnvelope, assetName)
		}
		if entry.AssetID != pipelineUUID+":"+assetName {
			return fmt.Errorf("%w: execution target %q has a mismatched asset_id", ErrInvalidEnvelope, assetName)
		}
		if previous, duplicate := assetIDs[entry.AssetID]; duplicate {
			return fmt.Errorf(
				"%w: execution targets %q and %q share asset_id %q",
				ErrInvalidEnvelope,
				previous,
				assetName,
				entry.AssetID,
			)
		}
		assetIDs[entry.AssetID] = assetName
		if err := validateExecutionTarget(assetName, entry, event.ExecutionTargetSnapshotVersion); err != nil {
			return err
		}
		if commonVarsHash == "" {
			commonVarsHash = entry.VarsHash
		} else if entry.VarsHash != commonVarsHash {
			return fmt.Errorf("%w: execution targets have inconsistent vars_hash values", ErrInvalidEnvelope)
		}
	}

	seenRuns := make(map[string]struct{}, len(event.Assets))
	seenOrdinals := make(map[int64]string, len(event.Assets))
	for _, asset := range event.Assets {
		if _, duplicate := seenRuns[asset.AssetName]; duplicate {
			return fmt.Errorf("%w: completed asset %q appears more than once", ErrInvalidEnvelope, asset.AssetName)
		}
		seenRuns[asset.AssetName] = struct{}{}
		entry, ok := event.ExecutionTargets[asset.AssetName]
		if !ok || entry.AssetID != asset.AssetID {
			return fmt.Errorf("%w: completed asset %q is absent from execution targets", ErrInvalidEnvelope, asset.AssetName)
		}
		switch asset.Status {
		case "succeeded", "failed", "cancelled":
		default:
			return fmt.Errorf("%w: completed asset %q has non-terminal status %q", ErrInvalidEnvelope, asset.AssetName, asset.Status)
		}
		if err := validateQualityOutcome(asset); err != nil {
			return err
		}
		if asset.FinishedAt == nil || asset.FinishedAt.IsZero() || !asset.HasCompletionOrdinal || asset.CompletionOrdinal < 0 {
			return fmt.Errorf("%w: completed asset %q has incomplete terminal coordinates", ErrInvalidEnvelope, asset.AssetName)
		}
		if previous, duplicate := seenOrdinals[asset.CompletionOrdinal]; duplicate {
			return fmt.Errorf(
				"%w: completed assets %q and %q share completion ordinal %d",
				ErrInvalidEnvelope,
				previous,
				asset.AssetName,
				asset.CompletionOrdinal,
			)
		}
		seenOrdinals[asset.CompletionOrdinal] = asset.AssetName
		if asset.TargetIdentity != entry.TargetIdentity || asset.TargetFidelity != entry.TargetFidelity ||
			asset.Fingerprint != entry.Fingerprint || asset.OwnContent != entry.OwnContent ||
			asset.ConsumedVarsHash != entry.ConsumedVarsHash || asset.VarsHash != entry.VarsHash {
			return fmt.Errorf("%w: completed asset %q does not match execution targets", ErrInvalidEnvelope, asset.AssetName)
		}
		if asset.Status == "succeeded" {
			if !asset.HasUpstreamWriterSnapshot || asset.UpstreamWriters == nil {
				return fmt.Errorf("%w: completed asset %q has no upstream writer snapshot", ErrInvalidEnvelope, asset.AssetName)
			}
			if err := validateUpstreamWriters(asset, entry, event.ExecutionTargets); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateQualityOutcome(asset bus.AssetRun) error {
	switch asset.QualityStatus {
	case "":
		if len(asset.FailedChecks) > 0 {
			return fmt.Errorf("%w: completed asset %q has failures without a quality status", ErrInvalidEnvelope, asset.AssetName)
		}
		return nil
	case bus.QualityStatusPassed:
		if len(asset.FailedChecks) > 0 {
			return fmt.Errorf("%w: completed asset %q passed quality checks but carries failures", ErrInvalidEnvelope, asset.AssetName)
		}
		return nil
	case bus.QualityStatusFailed:
		if len(asset.FailedChecks) == 0 {
			return fmt.Errorf("%w: completed asset %q failed quality checks without an identity", ErrInvalidEnvelope, asset.AssetName)
		}
	default:
		return fmt.Errorf("%w: completed asset %q has invalid quality status %q", ErrInvalidEnvelope, asset.AssetName, asset.QualityStatus)
	}

	previous := ""
	for _, failure := range asset.FailedChecks {
		name := strings.TrimSpace(failure.Name)
		column := strings.TrimSpace(failure.Column)
		if name == "" || name != failure.Name || column != failure.Column {
			return fmt.Errorf("%w: completed asset %q has a non-canonical failed check", ErrInvalidEnvelope, asset.AssetName)
		}
		switch failure.Kind {
		case bus.QualityCheckKindCustom:
			if column != "" {
				return fmt.Errorf("%w: completed asset %q custom check %q has a column", ErrInvalidEnvelope, asset.AssetName, name)
			}
		case bus.QualityCheckKindColumn:
			if column == "" {
				return fmt.Errorf("%w: completed asset %q column check %q has no column", ErrInvalidEnvelope, asset.AssetName, name)
			}
		default:
			return fmt.Errorf("%w: completed asset %q has invalid failed check kind %q", ErrInvalidEnvelope, asset.AssetName, failure.Kind)
		}
		key := string(failure.Kind) + "\x00" + column + "\x00" + name
		if previous != "" && key <= previous {
			return fmt.Errorf("%w: completed asset %q failed checks are duplicated or unsorted", ErrInvalidEnvelope, asset.AssetName)
		}
		previous = key
	}
	return nil
}

func validateUpstreamWriters(
	asset bus.AssetRun,
	consumer bus.ExecutionTargetSnapshotEntry,
	targets map[string]bus.ExecutionTargetSnapshotEntry,
) error {
	allowed := make(map[string]bus.ExecutionTargetSnapshotEntry, len(consumer.Upstreams))
	reviewed := make(map[string]bus.ExecutionUpstreamSnapshot)
	for _, upstream := range consumer.Upstreams {
		if upstream.Required && strings.TrimSpace(upstream.ResolvedAssetID) != "" {
			allowed[upstream.ResolvedAssetID] = bus.ExecutionTargetSnapshotEntry{
				AssetID: upstream.ResolvedAssetID, TargetIdentity: upstream.TargetIdentity,
				TargetFidelity: "exact",
			}
			reviewed[upstream.ResolvedAssetID] = upstream
			continue
		}
		if entry, ok := targets[upstream.Value]; ok {
			allowed[entry.AssetID] = entry
		}
	}
	for upstreamID, writer := range asset.UpstreamWriters {
		target, ok := allowed[upstreamID]
		if !ok {
			return fmt.Errorf("%w: completed asset %q captured non-upstream writer %q", ErrInvalidEnvelope, asset.AssetName, upstreamID)
		}
		if writer.AssetID != upstreamID || target.AssetID != upstreamID || target.TargetFidelity != "exact" ||
			writer.TargetIdentity != target.TargetIdentity || strings.TrimSpace(writer.TargetIdentity) != writer.TargetIdentity {
			return fmt.Errorf("%w: completed asset %q has mismatched upstream writer %q", ErrInvalidEnvelope, asset.AssetName, upstreamID)
		}
		if strings.TrimSpace(writer.Fingerprint) == "" || strings.TrimSpace(writer.VarsHash) == "" ||
			strings.TrimSpace(writer.CompletionID) == "" || strings.TrimSpace(writer.CompletionID) != writer.CompletionID ||
			writer.TargetGeneration <= 0 || writer.CompletionOrdinal < 0 || writer.MaterializedAt.IsZero() {
			return fmt.Errorf("%w: completed asset %q has incomplete upstream writer %q", ErrInvalidEnvelope, asset.AssetName, upstreamID)
		}
		if expected, ok := reviewed[upstreamID]; ok &&
			(writer.Fingerprint != expected.ExpectedFingerprint || writer.VarsHash != expected.VarsHash ||
				writer.TargetGeneration != expected.TargetGeneration || writer.CompletionID != expected.CompletionID ||
				writer.CompletionOrdinal != expected.CompletionOrdinal) {
			return fmt.Errorf("%w: completed asset %q has changed cross-pipeline writer %q", ErrInvalidEnvelope, asset.AssetName, upstreamID)
		}
	}
	return nil
}

func validateExecutionTarget(assetName string, entry bus.ExecutionTargetSnapshotEntry, version int) error {
	if strings.TrimSpace(entry.TargetIdentity) != entry.TargetIdentity {
		return fmt.Errorf("%w: execution target %q has a non-canonical target_identity", ErrInvalidEnvelope, assetName)
	}
	switch entry.TargetFidelity {
	case "exact":
	case "runtime_only":
		if entry.TargetIdentity != "" {
			return fmt.Errorf("%w: runtime-only execution target %q claims an identity", ErrInvalidEnvelope, assetName)
		}
	default:
		return fmt.Errorf("%w: execution target %q has unsupported fidelity %q", ErrInvalidEnvelope, assetName, entry.TargetFidelity)
	}
	if entry.TargetWriteEvidenceRequired && (entry.TargetFidelity != "exact" || entry.TargetIdentity == "") {
		return fmt.Errorf("%w: execution target %q requires write evidence without an exact target", ErrInvalidEnvelope, assetName)
	}
	if version < targetSnapshotVersionV3 {
		if entry.WriteResourceKind != "" || entry.WriteResourceIdentity != "" || entry.WriteResourceFidelity != "" {
			return fmt.Errorf("%w: execution target %q contains write-resource evidence before version %d", ErrInvalidEnvelope, assetName, targetSnapshotVersionV3)
		}
	} else if err := validateExecutionWriteResource(assetName, entry); err != nil {
		return err
	}
	if version < targetSnapshotVersionV4 {
		if !bus.ExecutionContractIsEmpty(entry.ExecutionContract) {
			return fmt.Errorf(
				"%w: execution target %q contains an execution contract before version %d",
				ErrInvalidEnvelope,
				assetName,
				targetSnapshotVersionV4,
			)
		}
	} else if err := bus.ValidateExecutionContract(assetName, entry); err != nil {
		return fmt.Errorf(
			"%w: execution target %q has an invalid execution contract: %v",
			ErrInvalidEnvelope,
			assetName,
			err,
		)
	}
	if version < targetSnapshotVersionV5 && entry.ExternalSource {
		return fmt.Errorf(
			"%w: execution target %q identifies an external source before version %d",
			ErrInvalidEnvelope,
			assetName,
			targetSnapshotVersionV5,
		)
	}
	for field, value := range map[string]string{
		"fingerprint":        entry.Fingerprint,
		"own_content":        entry.OwnContent,
		"consumed_vars_hash": entry.ConsumedVarsHash,
		"vars_hash":          entry.VarsHash,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: execution target %q requires %s", ErrInvalidEnvelope, assetName, field)
		}
	}
	switch entry.CoverageMode {
	case "marker", "union_intervals", "replace_interval":
	default:
		return fmt.Errorf("%w: execution target %q has unsupported coverage_mode %q", ErrInvalidEnvelope, assetName, entry.CoverageMode)
	}
	for index, upstream := range entry.Upstreams {
		if strings.TrimSpace(upstream.Type) != upstream.Type ||
			strings.TrimSpace(upstream.Value) == "" || strings.TrimSpace(upstream.Value) != upstream.Value {
			return fmt.Errorf("%w: execution target %q upstream %d is not canonical", ErrInvalidEnvelope, assetName, index)
		}
		if upstream.Mode != "" && upstream.Mode != "full" && upstream.Mode != "symbolic" {
			return fmt.Errorf("%w: execution target %q upstream %d has an invalid mode", ErrInvalidEnvelope, assetName, index)
		}
		if upstream.Required &&
			(strings.TrimSpace(upstream.ResolvedAssetID) == "" || strings.TrimSpace(upstream.TargetIdentity) == "" ||
				strings.TrimSpace(upstream.ExpectedFingerprint) == "" || strings.TrimSpace(upstream.VarsHash) == "" ||
				upstream.TargetGeneration < 1 || strings.TrimSpace(upstream.CompletionID) == "") {
			return fmt.Errorf("%w: execution target %q upstream %d has incomplete prerequisite evidence", ErrInvalidEnvelope, assetName, index)
		}
		if (strings.TrimSpace(upstream.ProducerPipelineUUID) == "") !=
			(strings.TrimSpace(upstream.ProducerSnapshotVersionID) == "") {
			return fmt.Errorf("%w: execution target %q upstream %d has incomplete producer deployment evidence", ErrInvalidEnvelope, assetName, index)
		}
	}
	return nil
}

func validateExecutionWriteResource(assetName string, entry bus.ExecutionTargetSnapshotEntry) error {
	kind := strings.TrimSpace(entry.WriteResourceKind)
	identity := strings.TrimSpace(entry.WriteResourceIdentity)
	fidelity := strings.TrimSpace(entry.WriteResourceFidelity)
	if kind != entry.WriteResourceKind || identity != entry.WriteResourceIdentity || fidelity != entry.WriteResourceFidelity {
		return fmt.Errorf("%w: execution target %q write resource is not canonical", ErrInvalidEnvelope, assetName)
	}
	switch fidelity {
	case "exact":
		switch kind {
		case "none":
			if identity != "" {
				return fmt.Errorf("%w: execution target %q no-write resource claims an identity", ErrInvalidEnvelope, assetName)
			}
		case "local_file", "duckdb_database", "warehouse_relation":
			if len(identity) != 64 {
				return fmt.Errorf("%w: execution target %q write resource requires a SHA-256 identity", ErrInvalidEnvelope, assetName)
			}
			for _, char := range identity {
				if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
					return fmt.Errorf("%w: execution target %q write resource requires a lowercase SHA-256 identity", ErrInvalidEnvelope, assetName)
				}
			}
		default:
			return fmt.Errorf("%w: execution target %q has unsupported exact write-resource kind %q", ErrInvalidEnvelope, assetName, kind)
		}
	case "runtime_only":
		if kind != "pipeline" || identity != "" {
			return fmt.Errorf("%w: execution target %q runtime write resource must be pipeline-scoped", ErrInvalidEnvelope, assetName)
		}
	default:
		return fmt.Errorf("%w: execution target %q has unsupported write-resource fidelity %q", ErrInvalidEnvelope, assetName, fidelity)
	}
	return nil
}

func eventToV1(event bus.RunCompleted) completedRunV1 {
	assets := make([]assetRunV1, len(event.Assets))
	for index, asset := range event.Assets {
		upstreamWriters := make(map[string]upstreamWriterV1, len(asset.UpstreamWriters))
		for upstreamID, writer := range asset.UpstreamWriters {
			upstreamWriters[upstreamID] = upstreamWriterV1{
				AssetID: writer.AssetID, TargetIdentity: writer.TargetIdentity,
				Fingerprint: writer.Fingerprint, VarsHash: writer.VarsHash,
				TargetGeneration: writer.TargetGeneration, CompletionID: writer.CompletionID,
				CompletionOrdinal: writer.CompletionOrdinal, MaterializedAt: writer.MaterializedAt.UTC(),
			}
		}
		failedChecks := make([]qualityCheckFailureV1, len(asset.FailedChecks))
		for failureIndex, failure := range asset.FailedChecks {
			failedChecks[failureIndex] = qualityCheckFailureV1{
				Kind: failure.Kind, Name: failure.Name, Column: failure.Column, Blocking: failure.Blocking,
			}
		}
		assets[index] = assetRunV1{
			AssetID: asset.AssetID, AssetName: asset.AssetName, Status: asset.Status,
			QualityStatus: asset.QualityStatus, FailedChecks: failedChecks,
			StartedAt: utcTimePointer(asset.StartedAt), FinishedAt: utcTimePointer(asset.FinishedAt),
			CompletionOrdinal: asset.CompletionOrdinal, HasCompletionOrdinal: asset.HasCompletionOrdinal,
			TargetIdentity: asset.TargetIdentity, TargetFidelity: asset.TargetFidelity,
			Fingerprint: asset.Fingerprint, OwnContent: asset.OwnContent,
			ConsumedVarsHash: asset.ConsumedVarsHash, VarsHash: asset.VarsHash,
			UpstreamWriters: upstreamWriters, HasUpstreamWriterSnapshot: asset.HasUpstreamWriterSnapshot,
		}
	}
	targets := make(map[string]executionTargetV1, len(event.ExecutionTargets))
	for assetName, target := range event.ExecutionTargets {
		upstreams := make([]executionUpstreamV1, len(target.Upstreams))
		for index, upstream := range target.Upstreams {
			upstreams[index] = executionUpstreamV1{
				Type: upstream.Type, Value: upstream.Value, Mode: upstream.Mode,
				ResolvedAssetID: upstream.ResolvedAssetID, Required: upstream.Required,
				ProducerPipelineUUID:      upstream.ProducerPipelineUUID,
				ProducerSnapshotVersionID: upstream.ProducerSnapshotVersionID,
				TargetIdentity:            upstream.TargetIdentity, ExpectedFingerprint: upstream.ExpectedFingerprint,
				VarsHash: upstream.VarsHash, TargetGeneration: upstream.TargetGeneration,
				CompletionID: upstream.CompletionID, CompletionOrdinal: upstream.CompletionOrdinal,
			}
		}
		var executionContract *executionContractV1
		if !bus.ExecutionContractIsEmpty(target.ExecutionContract) {
			contract := executionContractToV1(target.ExecutionContract)
			executionContract = &contract
		}
		targets[assetName] = executionTargetV1{
			AssetID: target.AssetID, ExternalSource: target.ExternalSource,
			TargetIdentity: target.TargetIdentity,
			TargetFidelity: target.TargetFidelity, TargetWriteEvidenceRequired: target.TargetWriteEvidenceRequired,
			WriteResourceKind: target.WriteResourceKind, WriteResourceIdentity: target.WriteResourceIdentity,
			WriteResourceFidelity: target.WriteResourceFidelity,
			ExecutionContract:     executionContract,
			Fingerprint:           target.Fingerprint,
			OwnContent:            target.OwnContent, ConsumedVarsHash: target.ConsumedVarsHash,
			VarsHash: target.VarsHash, Upstreams: upstreams, CoverageMode: target.CoverageMode,
			RefreshRestricted: target.RefreshRestricted,
		}
	}
	return completedRunV1{
		RunID: event.RunID, CompletionID: event.CompletionID,
		PipelineUUID: event.PipelineUUID, Environment: event.Environment,
		WinStart: utcTimePointer(event.WinStart), WinEnd: utcTimePointer(event.WinEnd),
		FullRefresh: event.FullRefresh, CompletedAt: event.CompletedAt.UTC(), Assets: assets,
		ExecutionTargetSnapshotVersion: event.ExecutionTargetSnapshotVersion,
		ExecutionPipelineUUID:          event.ExecutionPipelineUUID, ExecutionTargets: targets,
		SnapshotVersionID: event.SnapshotVersionID,
	}
}

func eventFromV1(event completedRunV1) bus.RunCompleted {
	assets := make([]bus.AssetRun, len(event.Assets))
	for index, asset := range event.Assets {
		upstreamWriters := make(map[string]bus.UpstreamWriterSnapshot, len(asset.UpstreamWriters))
		for upstreamID, writer := range asset.UpstreamWriters {
			upstreamWriters[upstreamID] = bus.UpstreamWriterSnapshot{
				AssetID: writer.AssetID, TargetIdentity: writer.TargetIdentity,
				Fingerprint: writer.Fingerprint, VarsHash: writer.VarsHash,
				TargetGeneration: writer.TargetGeneration, CompletionID: writer.CompletionID,
				CompletionOrdinal: writer.CompletionOrdinal, MaterializedAt: writer.MaterializedAt.UTC(),
			}
		}
		failedChecks := make([]bus.QualityCheckFailure, len(asset.FailedChecks))
		for failureIndex, failure := range asset.FailedChecks {
			failedChecks[failureIndex] = bus.QualityCheckFailure{
				Kind: failure.Kind, Name: failure.Name, Column: failure.Column, Blocking: failure.Blocking,
			}
		}
		assets[index] = bus.AssetRun{
			AssetID: asset.AssetID, AssetName: asset.AssetName, Status: asset.Status,
			QualityStatus: asset.QualityStatus, FailedChecks: failedChecks,
			StartedAt: utcTimePointer(asset.StartedAt), FinishedAt: utcTimePointer(asset.FinishedAt),
			CompletionOrdinal: asset.CompletionOrdinal, HasCompletionOrdinal: asset.HasCompletionOrdinal,
			TargetIdentity: asset.TargetIdentity, TargetFidelity: asset.TargetFidelity,
			Fingerprint: asset.Fingerprint, OwnContent: asset.OwnContent,
			ConsumedVarsHash: asset.ConsumedVarsHash, VarsHash: asset.VarsHash,
			UpstreamWriters: upstreamWriters, HasUpstreamWriterSnapshot: asset.HasUpstreamWriterSnapshot,
		}
	}
	targets := make(map[string]bus.ExecutionTargetSnapshotEntry, len(event.ExecutionTargets))
	for assetName, target := range event.ExecutionTargets {
		upstreams := make([]bus.ExecutionUpstreamSnapshot, len(target.Upstreams))
		for index, upstream := range target.Upstreams {
			upstreams[index] = bus.ExecutionUpstreamSnapshot{
				Type: upstream.Type, Value: upstream.Value, Mode: upstream.Mode,
				ResolvedAssetID: upstream.ResolvedAssetID, Required: upstream.Required,
				ProducerPipelineUUID:      upstream.ProducerPipelineUUID,
				ProducerSnapshotVersionID: upstream.ProducerSnapshotVersionID,
				TargetIdentity:            upstream.TargetIdentity, ExpectedFingerprint: upstream.ExpectedFingerprint,
				VarsHash: upstream.VarsHash, TargetGeneration: upstream.TargetGeneration,
				CompletionID: upstream.CompletionID, CompletionOrdinal: upstream.CompletionOrdinal,
			}
		}
		var executionContract bus.ExecutionContractSnapshot
		if target.ExecutionContract != nil {
			executionContract = executionContractFromV1(*target.ExecutionContract)
		}
		targets[assetName] = bus.ExecutionTargetSnapshotEntry{
			AssetID: target.AssetID, ExternalSource: target.ExternalSource,
			TargetIdentity: target.TargetIdentity,
			TargetFidelity: target.TargetFidelity, TargetWriteEvidenceRequired: target.TargetWriteEvidenceRequired,
			WriteResourceKind: target.WriteResourceKind, WriteResourceIdentity: target.WriteResourceIdentity,
			WriteResourceFidelity: target.WriteResourceFidelity,
			ExecutionContract:     executionContract,
			Fingerprint:           target.Fingerprint,
			OwnContent:            target.OwnContent, ConsumedVarsHash: target.ConsumedVarsHash,
			VarsHash: target.VarsHash, Upstreams: upstreams, CoverageMode: target.CoverageMode,
			RefreshRestricted: target.RefreshRestricted,
		}
	}
	return bus.RunCompleted{
		RunID: event.RunID, CompletionID: event.CompletionID,
		PipelineUUID: event.PipelineUUID, Environment: event.Environment,
		WinStart: utcTimePointer(event.WinStart), WinEnd: utcTimePointer(event.WinEnd),
		FullRefresh: event.FullRefresh, CompletedAt: event.CompletedAt.UTC(), Assets: assets,
		ExecutionTargetSnapshotVersion: event.ExecutionTargetSnapshotVersion,
		ExecutionPipelineUUID:          event.ExecutionPipelineUUID, ExecutionTargets: targets,
		SnapshotVersionID: event.SnapshotVersionID,
	}
}

func executionContractToV1(contract bus.ExecutionContractSnapshot) executionContractV1 {
	return executionContractV1{
		AssetID:        contract.AssetID,
		AssetName:      contract.AssetName,
		ConnectionKeys: append([]string(nil), contract.ConnectionKeys...),
		MutationResources: executionResourcesToV1(
			contract.MutationResources,
		),
		CoordinationResources: executionResourcesToV1(
			contract.CoordinationResources,
		),
	}
}

func executionResourcesToV1(resources bus.ExecutionResources) executionResourcesV1 {
	claims := make([]executionResourceClaimV1, 0, len(resources.Claims))
	for _, claim := range resources.Claims {
		claims = append(claims, executionResourceClaimV1{
			Kind: claim.Kind, Identity: claim.Identity,
		})
	}
	return executionResourcesV1{Isolation: resources.Isolation, Claims: claims}
}

func executionContractFromV1(contract executionContractV1) bus.ExecutionContractSnapshot {
	return bus.ExecutionContractSnapshot{
		AssetID:        contract.AssetID,
		AssetName:      contract.AssetName,
		ConnectionKeys: append([]string(nil), contract.ConnectionKeys...),
		MutationResources: executionResourcesFromV1(
			contract.MutationResources,
		),
		CoordinationResources: executionResourcesFromV1(
			contract.CoordinationResources,
		),
	}
}

func executionResourcesFromV1(resources executionResourcesV1) bus.ExecutionResources {
	claims := make([]bus.ExecutionResourceClaim, 0, len(resources.Claims))
	for _, claim := range resources.Claims {
		claims = append(claims, bus.ExecutionResourceClaim{
			Kind: claim.Kind, Identity: claim.Identity,
		})
	}
	return bus.ExecutionResources{Isolation: resources.Isolation, Claims: claims}
}

func utcTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
