package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	ExecutionTargetFidelityExact       = "exact"
	ExecutionTargetFidelityRuntimeOnly = "runtime_only"
)

var (
	ErrInvalidExecutionTargetSnapshot  = errors.New("invalid execution target snapshot")
	ErrExecutionTargetSnapshotConflict = errors.New("execution target snapshot is already persisted with different evidence")
)

func validateExecutionTargetSnapshot(snapshot ExecutionTargetSnapshot) error {
	if snapshot.Version != ExecutionTargetSnapshotVersionV1 &&
		snapshot.Version != ExecutionTargetSnapshotVersionV2 &&
		snapshot.Version != ExecutionTargetSnapshotVersionV3 &&
		snapshot.Version != ExecutionTargetSnapshotVersionV4 &&
		snapshot.Version != ExecutionTargetSnapshotVersionV5 {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidExecutionTargetSnapshot, snapshot.Version)
	}
	if snapshot.Version >= ExecutionTargetSnapshotVersionV2 {
		pipelineUUID := strings.TrimSpace(snapshot.PipelineUUID)
		if pipelineUUID == "" || pipelineUUID != snapshot.PipelineUUID {
			return fmt.Errorf("%w: version %d requires a canonical pipeline_uuid", ErrInvalidExecutionTargetSnapshot, snapshot.Version)
		}
	}
	configurationDigest := strings.TrimSpace(snapshot.ConfigurationDigest)
	configurationFidelity := strings.TrimSpace(snapshot.ConfigurationFidelity)
	if configurationDigest != snapshot.ConfigurationDigest || configurationFidelity != snapshot.ConfigurationFidelity {
		return fmt.Errorf("%w: configuration identity must be canonical", ErrInvalidExecutionTargetSnapshot)
	}
	if configurationDigest != "" || configurationFidelity != "" {
		switch configurationFidelity {
		case ExecutionTargetFidelityExact:
			if err := validateRunIdentityDigest("configuration_digest", configurationDigest); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidExecutionTargetSnapshot, err)
			}
		case ExecutionTargetFidelityRuntimeOnly:
			if configurationDigest != "" {
				return fmt.Errorf("%w: runtime-only configuration cannot claim a digest", ErrInvalidExecutionTargetSnapshot)
			}
		default:
			return fmt.Errorf("%w: unsupported configuration_fidelity %q", ErrInvalidExecutionTargetSnapshot, configurationFidelity)
		}
	}
	if len(snapshot.Entries) == 0 {
		return fmt.Errorf("%w: at least one asset entry is required", ErrInvalidExecutionTargetSnapshot)
	}
	assetIDs := make(map[string]string, len(snapshot.Entries))
	for assetName, entry := range snapshot.Entries {
		canonicalName := strings.TrimSpace(assetName)
		if canonicalName == "" || canonicalName != assetName {
			return fmt.Errorf("%w: entry key %q must be a non-empty canonical asset name", ErrInvalidExecutionTargetSnapshot, assetName)
		}
		assetID := strings.TrimSpace(entry.AssetID)
		if assetID == "" || assetID != entry.AssetID {
			return fmt.Errorf("%w: entry %q requires a canonical asset_id", ErrInvalidExecutionTargetSnapshot, assetName)
		}
		if previousName, exists := assetIDs[assetID]; exists {
			return fmt.Errorf(
				"%w: entries %q and %q share asset_id %q",
				ErrInvalidExecutionTargetSnapshot,
				previousName,
				assetName,
				assetID,
			)
		}
		assetIDs[assetID] = assetName

		targetIdentity := strings.TrimSpace(entry.TargetIdentity)
		if targetIdentity != entry.TargetIdentity {
			return fmt.Errorf("%w: entry %q has a non-canonical target_identity", ErrInvalidExecutionTargetSnapshot, assetName)
		}
		switch entry.TargetFidelity {
		case ExecutionTargetFidelityExact:
			// An exact empty identity represents an asset with no mutable target,
			// such as a sensor. Writers carry their non-empty physical identity.
		case ExecutionTargetFidelityRuntimeOnly:
			if targetIdentity != "" {
				return fmt.Errorf(
					"%w: runtime-only entry %q cannot claim a target_identity",
					ErrInvalidExecutionTargetSnapshot,
					assetName,
				)
			}
		default:
			return fmt.Errorf(
				"%w: entry %q has unsupported target_fidelity %q",
				ErrInvalidExecutionTargetSnapshot,
				assetName,
				entry.TargetFidelity,
			)
		}
		if entry.TargetWriteEvidenceRequired && (entry.TargetFidelity != ExecutionTargetFidelityExact || targetIdentity == "") {
			return fmt.Errorf(
				"%w: entry %q can require target-write evidence only for an exact non-empty target",
				ErrInvalidExecutionTargetSnapshot,
				assetName,
			)
		}
		if snapshot.Version < ExecutionTargetSnapshotVersionV3 {
			if entry.WriteResourceKind != "" || entry.WriteResourceIdentity != "" || entry.WriteResourceFidelity != "" {
				return fmt.Errorf(
					"%w: entry %q cannot contain write-resource evidence before version %d",
					ErrInvalidExecutionTargetSnapshot,
					assetName,
					ExecutionTargetSnapshotVersionV3,
				)
			}
		} else if err := validateExecutionWriteResource(assetName, entry); err != nil {
			return err
		}
		if snapshot.Version < ExecutionTargetSnapshotVersionV4 {
			if !emptyPipelineRunExecutionContract(entry.ExecutionContract) {
				return fmt.Errorf(
					"%w: entry %q cannot contain an execution contract before version %d",
					ErrInvalidExecutionTargetSnapshot,
					assetName,
					ExecutionTargetSnapshotVersionV4,
				)
			}
		} else if err := validateExecutionTargetContract(assetName, entry); err != nil {
			return err
		}
		if snapshot.Version < ExecutionTargetSnapshotVersionV5 && entry.ExternalSource {
			return fmt.Errorf(
				"%w: entry %q cannot identify an external source before version %d",
				ErrInvalidExecutionTargetSnapshot,
				assetName,
				ExecutionTargetSnapshotVersionV5,
			)
		}
		for field, value := range map[string]string{
			"fingerprint":        entry.Fingerprint,
			"own_content":        entry.OwnContent,
			"consumed_vars_hash": entry.ConsumedVarsHash,
			"vars_hash":          entry.VarsHash,
		} {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("%w: entry %q requires %s", ErrInvalidExecutionTargetSnapshot, assetName, field)
			}
		}
		if snapshot.Version >= ExecutionTargetSnapshotVersionV2 {
			switch entry.CoverageMode {
			case "marker", "union_intervals", "replace_interval":
			default:
				return fmt.Errorf(
					"%w: entry %q has unsupported coverage_mode %q",
					ErrInvalidExecutionTargetSnapshot,
					assetName,
					entry.CoverageMode,
				)
			}
			for index, upstream := range entry.Upstreams {
				if strings.TrimSpace(upstream.Type) != upstream.Type {
					return fmt.Errorf("%w: entry %q upstream %d has a non-canonical type", ErrInvalidExecutionTargetSnapshot, assetName, index)
				}
				if strings.TrimSpace(upstream.Value) == "" || strings.TrimSpace(upstream.Value) != upstream.Value {
					return fmt.Errorf("%w: entry %q upstream %d requires a canonical value", ErrInvalidExecutionTargetSnapshot, assetName, index)
				}
				if upstream.Mode != "" && upstream.Mode != "full" && upstream.Mode != "symbolic" {
					return fmt.Errorf("%w: entry %q upstream %d has an invalid mode", ErrInvalidExecutionTargetSnapshot, assetName, index)
				}
				if upstream.Required &&
					(strings.TrimSpace(upstream.ResolvedAssetID) == "" || strings.TrimSpace(upstream.TargetIdentity) == "" ||
						strings.TrimSpace(upstream.ExpectedFingerprint) == "" || strings.TrimSpace(upstream.VarsHash) == "" ||
						upstream.TargetGeneration < 1 || strings.TrimSpace(upstream.CompletionID) == "") {
					return fmt.Errorf("%w: entry %q upstream %d has incomplete prerequisite evidence", ErrInvalidExecutionTargetSnapshot, assetName, index)
				}
				if (strings.TrimSpace(upstream.ProducerPipelineUUID) == "") !=
					(strings.TrimSpace(upstream.ProducerSnapshotVersionID) == "") {
					return fmt.Errorf("%w: entry %q upstream %d has incomplete producer deployment evidence", ErrInvalidExecutionTargetSnapshot, assetName, index)
				}
			}
		}
	}
	return nil
}

func emptyPipelineRunExecutionContract(contract PipelineRunExecutionContract) bool {
	return contract.AssetID == "" &&
		contract.AssetName == "" &&
		len(contract.ConnectionKeys) == 0 &&
		contract.MutationResources.Isolation == "" &&
		len(contract.MutationResources.Claims) == 0 &&
		contract.CoordinationResources.Isolation == "" &&
		len(contract.CoordinationResources.Claims) == 0
}

func validateExecutionTargetContract(
	assetName string,
	entry ExecutionTargetSnapshotEntry,
) error {
	contract := entry.ExecutionContract
	if contract.AssetID != entry.AssetID || contract.AssetName != assetName {
		return fmt.Errorf(
			"%w: entry %q execution contract has mismatched asset identity",
			ErrInvalidExecutionTargetSnapshot,
			assetName,
		)
	}
	if err := validatePipelineRunExecutionContracts(
		[]PipelineRunExecutionContract{contract},
		[]PipelineRunExecutionUnit{{AssetID: entry.AssetID, AssetName: assetName}},
	); err != nil {
		return fmt.Errorf("%w: entry %q execution contract: %v", ErrInvalidExecutionTargetSnapshot, assetName, err)
	}
	expectedMutation := executionTargetEntryMutationResources(entry)
	if !equalPipelineRunPlanResources(expectedMutation, contract.MutationResources) {
		return fmt.Errorf(
			"%w: entry %q execution contract mutation resources do not match its target",
			ErrInvalidExecutionTargetSnapshot,
			assetName,
		)
	}
	if contract.MutationResources.Isolation == PipelineRunResourceIsolationPipeline {
		if contract.CoordinationResources.Isolation != PipelineRunResourceIsolationPipeline {
			return fmt.Errorf(
				"%w: entry %q pipeline-isolated mutation must remain pipeline-isolated at runtime",
				ErrInvalidExecutionTargetSnapshot,
				assetName,
			)
		}
		return nil
	}
	if contract.CoordinationResources.Isolation != PipelineRunResourceIsolationResources {
		return fmt.Errorf(
			"%w: entry %q exact mutation resources require resource coordination",
			ErrInvalidExecutionTargetSnapshot,
			assetName,
		)
	}
	coordination := make(map[string]struct{}, len(contract.CoordinationResources.Claims))
	for _, claim := range contract.CoordinationResources.Claims {
		coordination[claim.Kind+"\x00"+claim.Identity] = struct{}{}
	}
	for _, claim := range contract.MutationResources.Claims {
		if _, exists := coordination[claim.Kind+"\x00"+claim.Identity]; !exists {
			return fmt.Errorf(
				"%w: entry %q runtime coordination omits a mutation resource",
				ErrInvalidExecutionTargetSnapshot,
				assetName,
			)
		}
	}
	return nil
}

func executionTargetEntryMutationResources(
	entry ExecutionTargetSnapshotEntry,
) PipelineRunPlanResources {
	if entry.WriteResourceFidelity != ExecutionTargetFidelityExact {
		return PipelineRunPlanResources{
			Isolation: PipelineRunResourceIsolationPipeline,
			Claims:    []PipelineRunResourceClaim{},
		}
	}
	if entry.WriteResourceKind == "none" {
		return PipelineRunPlanResources{
			Isolation: PipelineRunResourceIsolationResources,
			Claims:    []PipelineRunResourceClaim{},
		}
	}
	return PipelineRunPlanResources{
		Isolation: PipelineRunResourceIsolationResources,
		Claims: []PipelineRunResourceClaim{{
			Kind: entry.WriteResourceKind, Identity: entry.WriteResourceIdentity,
		}},
	}
}

func validateExecutionWriteResource(assetName string, entry ExecutionTargetSnapshotEntry) error {
	kind := strings.TrimSpace(entry.WriteResourceKind)
	identity := strings.TrimSpace(entry.WriteResourceIdentity)
	fidelity := strings.TrimSpace(entry.WriteResourceFidelity)
	if kind != entry.WriteResourceKind || identity != entry.WriteResourceIdentity || fidelity != entry.WriteResourceFidelity {
		return fmt.Errorf("%w: entry %q write resource must be canonical", ErrInvalidExecutionTargetSnapshot, assetName)
	}
	switch fidelity {
	case ExecutionTargetFidelityExact:
		switch kind {
		case "none":
			if identity != "" {
				return fmt.Errorf("%w: entry %q no-write resource cannot claim an identity", ErrInvalidExecutionTargetSnapshot, assetName)
			}
		case PipelineRunResourceKindLocalFile,
			PipelineRunResourceKindDuckDBDatabase,
			PipelineRunResourceKindWarehouse:
			if err := validateRunIdentityDigest("write_resource_identity", identity); err != nil {
				return fmt.Errorf("%w: entry %q: %v", ErrInvalidExecutionTargetSnapshot, assetName, err)
			}
		default:
			return fmt.Errorf("%w: entry %q has unsupported exact write-resource kind %q", ErrInvalidExecutionTargetSnapshot, assetName, kind)
		}
	case ExecutionTargetFidelityRuntimeOnly:
		if kind != "pipeline" || identity != "" {
			return fmt.Errorf("%w: entry %q runtime write resource must be pipeline-scoped", ErrInvalidExecutionTargetSnapshot, assetName)
		}
	default:
		return fmt.Errorf("%w: entry %q has unsupported write-resource fidelity %q", ErrInvalidExecutionTargetSnapshot, assetName, fidelity)
	}
	return nil
}

func executionTargetSnapshotResources(
	snapshot ExecutionTargetSnapshot,
	executionUnits []PipelineRunExecutionUnit,
) (PipelineRunPlanResources, error) {
	if snapshot.Version < ExecutionTargetSnapshotVersionV3 {
		return PipelineRunPlanResources{}, fmt.Errorf(
			"%w: version %d has no write-resource evidence",
			ErrInvalidExecutionTargetSnapshot,
			snapshot.Version,
		)
	}
	result := PipelineRunPlanResources{
		Isolation: PipelineRunResourceIsolationResources,
		Claims:    []PipelineRunResourceClaim{},
	}
	selectedEntries := make(map[string]ExecutionTargetSnapshotEntry, len(executionUnits))
	selectedAssetIDs := make(map[string]string, len(executionUnits))
	for _, unit := range executionUnits {
		assetName := unit.AssetName
		if previousID, exists := selectedAssetIDs[assetName]; exists {
			if previousID != unit.AssetID {
				return PipelineRunPlanResources{}, fmt.Errorf(
					"%w: planned asset %q has conflicting asset ids %q and %q",
					ErrInvalidExecutionTargetSnapshot,
					assetName,
					previousID,
					unit.AssetID,
				)
			}
			continue
		}
		entry, exists := snapshot.Entries[assetName]
		if !exists {
			return PipelineRunPlanResources{}, fmt.Errorf(
				"%w: planned asset %q is missing from the execution target snapshot",
				ErrInvalidExecutionTargetSnapshot,
				assetName,
			)
		}
		if entry.AssetID != unit.AssetID {
			return PipelineRunPlanResources{}, fmt.Errorf(
				"%w: planned asset %q has asset id %q in the execution target snapshot, expected %q",
				ErrInvalidExecutionTargetSnapshot,
				assetName,
				entry.AssetID,
				unit.AssetID,
			)
		}
		selectedAssetIDs[assetName] = unit.AssetID
		selectedEntries[assetName] = entry
	}
	seen := make(map[string]struct{})
	for _, entry := range selectedEntries {
		if entry.WriteResourceFidelity == ExecutionTargetFidelityExact && entry.WriteResourceKind == "none" {
			continue
		}
		if entry.WriteResourceFidelity != ExecutionTargetFidelityExact {
			result.Isolation = PipelineRunResourceIsolationPipeline
			continue
		}
		claim := PipelineRunResourceClaim{Kind: entry.WriteResourceKind, Identity: entry.WriteResourceIdentity}
		key := claim.Kind + "\x00" + claim.Identity
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result.Claims = append(result.Claims, claim)
	}
	sort.Slice(result.Claims, func(i, j int) bool {
		if result.Claims[i].Kind == result.Claims[j].Kind {
			return result.Claims[i].Identity < result.Claims[j].Identity
		}
		return result.Claims[i].Kind < result.Claims[j].Kind
	})
	return result, nil
}

func validateExecutionTargetResourceBinding(
	ctx context.Context,
	tx *sql.Tx,
	runID string,
	snapshot ExecutionTargetSnapshot,
) error {
	var version int
	var body string
	err := tx.QueryRowContext(ctx, `
		SELECT version, body
		FROM pipeline_run_plans
		WHERE run_id = ?`, runID).Scan(&version, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	plan, err := unmarshalPipelineRunPlan(version, []byte(body))
	if err != nil {
		return fmt.Errorf("load pipeline run plan for resource validation: %w", err)
	}
	if plan.Version < PipelineRunPlanVersionV2 {
		return nil
	}
	actual, err := executionTargetSnapshotResources(snapshot, plan.ExecutionUnits)
	if err != nil {
		return err
	}
	if !equalPipelineRunPlanResources(actual, plan.Resources) {
		return fmt.Errorf(
			"%w: execution write resources do not match the reviewed plan",
			ErrInvalidExecutionTargetSnapshot,
		)
	}
	if plan.Version >= PipelineRunPlanVersionV3 {
		if snapshot.Version < ExecutionTargetSnapshotVersionV4 {
			return fmt.Errorf(
				"%w: execution snapshot has no reviewed runtime contracts",
				ErrInvalidExecutionTargetSnapshot,
			)
		}
		actualContracts := make([]PipelineRunExecutionContract, 0, len(plan.ExecutionContracts))
		for _, reviewed := range plan.ExecutionContracts {
			entry, exists := snapshot.Entries[reviewed.AssetName]
			if !exists || entry.AssetID != reviewed.AssetID {
				return fmt.Errorf(
					"%w: execution contract for %q is missing from the target snapshot",
					ErrInvalidExecutionTargetSnapshot,
					reviewed.AssetName,
				)
			}
			actualContracts = append(actualContracts, entry.ExecutionContract)
		}
		if !equalPipelineRunExecutionContracts(actualContracts, plan.ExecutionContracts) {
			return fmt.Errorf(
				"%w: execution runtime contracts do not match the reviewed plan",
				ErrInvalidExecutionTargetSnapshot,
			)
		}
	}

	var isolation string
	if err := tx.QueryRowContext(ctx, `
		SELECT isolation
		FROM pipeline_run_claim_sets
		WHERE run_id = ?`, runID).Scan(&isolation); err != nil {
		return fmt.Errorf("load admitted write-resource claims: %w", err)
	}
	persisted := PipelineRunPlanResources{Isolation: isolation, Claims: []PipelineRunResourceClaim{}}
	rows, err := tx.QueryContext(ctx, `
		SELECT kind, identity
		FROM pipeline_run_resource_claims
		WHERE run_id = ?
		ORDER BY kind, identity`, runID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var claim PipelineRunResourceClaim
		if err := rows.Scan(&claim.Kind, &claim.Identity); err != nil {
			return err
		}
		persisted.Claims = append(persisted.Claims, claim)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !equalPipelineRunPlanResources(persisted, plan.Resources) {
		return errors.New("admitted write-resource claims do not match the durable run plan")
	}
	return nil
}

func marshalExecutionTargetSnapshot(snapshot ExecutionTargetSnapshot) ([]byte, error) {
	if err := validateExecutionTargetSnapshot(snapshot); err != nil {
		return nil, err
	}
	return json.Marshal(snapshot)
}

func unmarshalExecutionTargetSnapshot(body string) (*ExecutionTargetSnapshot, error) {
	if strings.TrimSpace(body) == "" {
		return nil, nil
	}
	var snapshot ExecutionTargetSnapshot
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, fmt.Errorf("%w: decode: %v", ErrInvalidExecutionTargetSnapshot, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return nil, fmt.Errorf("%w: trailing content: %v", ErrInvalidExecutionTargetSnapshot, err)
	}
	if err := validateExecutionTargetSnapshot(snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// SetRunExecutionTargetSnapshot atomically captures immutable target and
// fingerprint evidence for an admitted run. Exact retries are accepted, while
// a second, different snapshot cannot rewrite recovery provenance.
func (s *Store) SetRunExecutionTargetSnapshot(ctx context.Context, runID string, snapshot ExecutionTargetSnapshot) error {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return errors.New("run id is required")
	}
	body, err := marshalExecutionTargetSnapshot(snapshot)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var status, existing string
	err = tx.QueryRowContext(ctx, `
		SELECT status, execution_target_snapshot
		FROM pipeline_runs
		WHERE id = ?`, runID).Scan(&status, &existing)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("pipeline run %s was not found", runID)
	}
	if err != nil {
		return err
	}
	if status != string(RunStatusQueued) && status != string(RunStatusRunning) {
		return fmt.Errorf("pipeline run %s is already terminal", runID)
	}
	if err := validateExecutionTargetResourceBinding(ctx, tx, runID, snapshot); err != nil {
		return err
	}
	if existing != "" {
		persisted, err := unmarshalExecutionTargetSnapshot(existing)
		if err != nil {
			return fmt.Errorf("load execution target snapshot for run %s: %w", runID, err)
		}
		persistedBody, err := marshalExecutionTargetSnapshot(*persisted)
		if err != nil {
			return fmt.Errorf("load execution target snapshot for run %s: %w", runID, err)
		}
		if string(persistedBody) == string(body) {
			return tx.Commit()
		}
		return fmt.Errorf("%w for run %s", ErrExecutionTargetSnapshotConflict, runID)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE pipeline_runs
		SET execution_target_snapshot = ?
		WHERE id = ?
		  AND status IN (?, ?)
		  AND execution_target_snapshot = ''
		  AND NOT EXISTS (
			SELECT 1 FROM pipeline_run_steps WHERE run_id = pipeline_runs.id
		  )`,
		string(body), runID, string(RunStatusQueued), string(RunStatusRunning),
	)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		var stepStarted bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM pipeline_run_steps WHERE run_id = ?)`, runID).Scan(&stepStarted); err != nil {
			return err
		}
		if stepStarted {
			return fmt.Errorf("cannot capture execution target snapshot for run %s after the first step started", runID)
		}
		return fmt.Errorf("capture execution target snapshot for run %s: concurrent run state change", runID)
	}
	return tx.Commit()
}
