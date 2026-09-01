package scheduler

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river/riverdriver/riversqlite"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"renart/internal/web/scheduler/storedb"
)

//go:embed storedb/migrations/*.sql
var schedulerMigrations embed.FS

type Store struct {
	db      *sql.DB
	queries *storedb.Queries
	path    string
}

const integrityStampVersion = 1

type integrityStamp struct {
	Version              int    `json:"version"`
	MigrationFingerprint string `json:"migration_fingerprint"`
	Size                 int64  `json:"size"`
	ModifiedUnixNano     int64  `json:"modified_unix_nano"`
}

var integrityMigrationFingerprint = currentIntegrityMigrationFingerprint()

// ErrStateDatabaseIntegrity marks a state database that cannot be trusted.
// Callers must preserve it for recovery rather than silently recreating it:
// state.db contains schedules, deployments, run history, and derived freshness.
var ErrStateDatabaseIntegrity = errors.New("state database integrity check failed")

func OpenStore(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("state path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	cleanClose := matchesIntegrityStamp(path)
	if err := os.Remove(integrityStampPath(path)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("invalidate state database integrity stamp: %w", err)
	}
	db, err := sql.Open(
		"sqlite",
		path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_time_format=sqlite&_timezone=UTC",
	)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db, queries: storedb.New(db), path: path}
	if !cleanClose {
		if err := verifyStateDatabaseIntegrity(context.Background(), db); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf(
				"%w for %q: %v; stop Renart and back up state.db, state.db-wal, and state.db-shm before recovery",
				ErrStateDatabaseIntegrity,
				path,
				err,
			)
		}
	}
	if err := reconcileActiveRunSlotMigration(context.Background(), db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.migrateRiver(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func reconcileActiveRunSlotMigration(ctx context.Context, db *sql.DB) error {
	var hasRunsTable, hasSlotTable bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type = 'table' AND name = 'pipeline_runs')`).Scan(&hasRunsTable); err != nil {
		return fmt.Errorf("inspect state database schema: %w", err)
	}
	if !hasRunsTable {
		return nil
	}
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type = 'table' AND name = 'pipeline_run_slots')`).Scan(&hasSlotTable); err != nil {
		return fmt.Errorf("inspect active-run slot migration: %w", err)
	}
	if hasSlotTable {
		return nil
	}

	rows, err := db.QueryContext(ctx, `
		SELECT pipeline_id, id
		FROM pipeline_runs
		WHERE status IN ('queued', 'running')
		  AND pipeline_id IN (
			SELECT pipeline_id
			FROM pipeline_runs
			WHERE status IN ('queued', 'running')
			GROUP BY pipeline_id
			HAVING COUNT(*) > 1
		  )
		ORDER BY pipeline_id,
		         CASE status WHEN 'queued' THEN 0 ELSE 1 END,
		         COALESCE(started_at, ''),
		         id`)
	if err != nil {
		return fmt.Errorf("inspect active-run slot migration conflicts: %w", err)
	}
	conflicts := make(map[string][]string)
	order := make([]string, 0)
	for rows.Next() {
		var pipelineID, runID string
		if err := rows.Scan(&pipelineID, &runID); err != nil {
			_ = rows.Close()
			return err
		}
		if _, exists := conflicts[pipelineID]; !exists {
			order = append(order, pipelineID)
		}
		conflicts[pipelineID] = append(conflicts[pipelineID], runID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if len(conflicts) == 0 {
		return nil
	}

	// Builds predating atomic admission could race HasActiveRun and persist more
	// than one active row for the same path. Blocking the migration leaves the
	// workspace with no running server and no way to cancel those rows. Keep one
	// deterministic queued-first survivor for normal startup recovery and retain
	// every conflicting row as an auditable terminal failure.
	var hasRecoveryPending bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM pragma_table_info('pipeline_runs')
			WHERE name = 'recovery_pending'
		)`).Scan(&hasRecoveryPending); err != nil {
		return fmt.Errorf("inspect active-run recovery schema: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("reconcile active-run slot migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := formatTime(time.Now().UTC())
	for _, pipelineID := range order {
		runIDs := conflicts[pipelineID]
		survivorID := runIDs[0]
		for _, runID := range runIDs[1:] {
			reason := fmt.Sprintf(
				"interrupted: duplicate active run reconciled during atomic run-slot migration; pipeline %s retained run %s for scheduler recovery",
				pipelineID,
				survivorID,
			)
			var result sql.Result
			if hasRecoveryPending {
				result, err = tx.ExecContext(ctx, `
					UPDATE pipeline_runs
					SET status = ?, finished_at = ?, error = ?, recovery_pending = 1
					WHERE id = ? AND status IN (?, ?)`,
					string(RunStatusFailed), now, reason, runID,
					string(RunStatusQueued), string(RunStatusRunning))
			} else {
				result, err = tx.ExecContext(ctx, `
					UPDATE pipeline_runs
					SET status = ?, finished_at = ?, error = ?
					WHERE id = ? AND status IN (?, ?)`,
					string(RunStatusFailed), now, reason, runID,
					string(RunStatusQueued), string(RunStatusRunning))
			}
			if err != nil {
				return fmt.Errorf("terminalize duplicate active run %s: %w", runID, err)
			}
			updated, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("confirm duplicate active run %s reconciliation: %w", runID, err)
			}
			if updated == 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE pipeline_run_steps
				SET status = ?, finished_at = ?,
				    error = CASE WHEN error IS NULL OR error = '' THEN ? ELSE error END
				WHERE run_id = ? AND finished_at IS NULL`,
				string(RunStatusFailed), now, reason, runID); err != nil {
				return fmt.Errorf("close duplicate active run %s steps: %w", runID, err)
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO pipeline_run_logs (run_id, at, line)
				VALUES (?, ?, ?)`, runID, now, reason); err != nil {
				return fmt.Errorf("record duplicate active run %s recovery: %w", runID, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit active-run slot migration reconciliation: %w", err)
	}
	return nil
}

func verifyStateDatabaseIntegrity(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA quick_check(1)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	checked := false
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return err
		}
		checked = true
		if !strings.EqualFold(strings.TrimSpace(result), "ok") {
			return errors.New(result)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !checked {
		return errors.New("SQLite returned no integrity result")
	}
	return nil
}

// DB exposes the underlying SQLite handle so sibling stores (the
// materialization log, snapshots) share the same database and migration
// lifecycle.
func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	if err := s.db.Close(); err != nil {
		return err
	}
	if err := writeIntegrityStamp(s.path); err != nil {
		return fmt.Errorf("record clean state database close: %w", err)
	}
	return nil
}

func integrityStampPath(path string) string {
	return path + ".integrity"
}

func matchesIntegrityStamp(path string) bool {
	data, err := os.ReadFile(integrityStampPath(path))
	if err != nil {
		return false
	}
	var stamp integrityStamp
	if err := json.Unmarshal(data, &stamp); err != nil ||
		stamp.Version != integrityStampVersion ||
		stamp.MigrationFingerprint != integrityMigrationFingerprint {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if walInfo, walErr := os.Stat(path + "-wal"); walErr == nil {
		if walInfo.Size() > 0 {
			return false
		}
	} else if !errors.Is(walErr, os.ErrNotExist) {
		return false
	}
	return info.Size() == stamp.Size && info.ModTime().UnixNano() == stamp.ModifiedUnixNano
}

func writeIntegrityStamp(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if walInfo, err := os.Stat(path + "-wal"); err == nil {
		// Another SQLite connection can keep a live WAL after this Store closes.
		// In that case the next opener must verify the database instead of
		// trusting a clean-close stamp from only one of its writers.
		if walInfo.Size() > 0 {
			_ = os.Remove(integrityStampPath(path))
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	stamp := integrityStamp{
		Version:              integrityStampVersion,
		MigrationFingerprint: integrityMigrationFingerprint,
		Size:                 info.Size(),
		ModifiedUnixNano:     info.ModTime().UnixNano(),
	}
	data, err := json.Marshal(stamp)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(integrityStampPath(path))+"-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	keepTemp := true
	defer func() {
		_ = temp.Close()
		if keepTemp {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, integrityStampPath(path)); err != nil {
		return err
	}
	keepTemp = false
	return nil
}

func currentIntegrityMigrationFingerprint() string {
	hash := sha256.New()
	entries, err := fs.ReadDir(schedulerMigrations, "storedb/migrations")
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			data, readErr := schedulerMigrations.ReadFile(path.Join("storedb/migrations", entry.Name()))
			if readErr != nil {
				continue
			}
			_, _ = hash.Write([]byte(entry.Name()))
			_, _ = hash.Write([]byte{0})
			_, _ = hash.Write(data)
			_, _ = hash.Write([]byte{0})
		}
	}
	if buildInfo, ok := debug.ReadBuildInfo(); ok {
		for _, dependency := range buildInfo.Deps {
			if dependency.Path != "github.com/riverqueue/river" {
				continue
			}
			_, _ = hash.Write([]byte(dependency.Path))
			_, _ = hash.Write([]byte{0})
			_, _ = hash.Write([]byte(dependency.Version))
			if dependency.Replace != nil {
				_, _ = hash.Write([]byte{0})
				_, _ = hash.Write([]byte(dependency.Replace.Path))
				_, _ = hash.Write([]byte{0})
				_, _ = hash.Write([]byte(dependency.Replace.Version))
			}
			break
		}
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func (s *Store) migrate(ctx context.Context) error {
	migrations, err := fs.Sub(schedulerMigrations, "storedb/migrations")
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.db, migrations)
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}

func (s *Store) migrateRiver(ctx context.Context) error {
	migrator, err := rivermigrate.New(riversqlite.New(s.db), nil)
	if err != nil {
		return err
	}
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	return err
}

func (s *Store) Create(ctx context.Context, run PipelineRun) (string, error) {
	for attempt := 0; attempt < 2; attempt++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return "", err
		}
		queries := s.queries.WithTx(tx)
		id, err := s.createRun(ctx, queries, run)
		if err == nil {
			err = s.claimRunSlot(ctx, tx, run, id)
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			_ = tx.Rollback()
		}
		if errors.Is(err, ErrPipelineRunActive) {
			return "", err
		}
		if !isActiveRunSlotConstraint(err) {
			return id, err
		}
		conflict, found, lookupErr := s.pipelineRunActiveError(ctx, run.PipelineID, runSlotKeys(run))
		if lookupErr != nil {
			return "", errors.Join(err, lookupErr)
		}
		if found {
			return "", conflict
		}
		if attempt == 1 {
			return "", fmt.Errorf("pipeline run admission retry exhausted after the active slot changed: %w", err)
		}
	}
	return "", errors.New("pipeline run admission retry exhausted")
}

func (s *Store) createRun(ctx context.Context, queries *storedb.Queries, run PipelineRun) (string, error) {
	if run.ID == "" {
		run.ID = uuid.NewString()
	}
	if run.Status == "" {
		run.Status = RunStatusQueued
	}
	if run.ExecutionContextResolved {
		if run.WinStart == nil || run.WinEnd == nil {
			return "", errors.New("resolved run context requires a complete increasing window")
		}
		if err := validateRunExecutionContext(RunExecutionContext{
			Environment: run.Environment,
			WinStart:    *run.WinStart,
			WinEnd:      *run.WinEnd,
			FullRefresh: run.FullRefresh,
			Backfill:    run.Backfill,
			SensorMode:  run.SensorMode,
		}); err != nil {
			return "", err
		}
	}
	err := queries.CreateRun(ctx, storedb.CreateRunParams{
		ID:                       run.ID,
		PipelineID:               run.PipelineID,
		Pipeline:                 run.Pipeline,
		Environment:              run.Environment,
		Trigger:                  string(run.Trigger),
		Status:                   string(run.Status),
		WinStart:                 nullTime(run.WinStart),
		WinEnd:                   nullTime(run.WinEnd),
		StartedAt:                nullTime(run.StartedAt),
		FinishedAt:               nullTime(run.FinishedAt),
		Error:                    stringValue(run.Error),
		LogRef:                   stringValue(run.LogRef),
		SnapshotVersionID:        stringValue(run.SnapshotVersionID),
		RiverJobID:               nullInt64(run.RiverJobID),
		FullRefresh:              boolInt64(run.FullRefresh),
		Backfill:                 boolInt64(run.Backfill),
		SensorMode:               strings.TrimSpace(run.SensorMode),
		ExecutionContextResolved: boolInt64(run.ExecutionContextResolved),
	})
	return run.ID, err
}

type runSpecExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type runSlotExecer interface {
	runSpecExecer
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func runSlotKeys(run PipelineRun) []string {
	pipelineID := strings.TrimSpace(run.PipelineID)
	pipelineUUID := strings.TrimSpace(run.PipelineUUID)
	keys := make([]string, 0, 2)
	if pipelineID != "" {
		keys = append(keys, "path:"+pipelineID)
	}
	if pipelineUUID != "" {
		keys = append(keys, "uuid:"+pipelineUUID)
	}
	return keys
}

func (s *Store) claimRunSlot(ctx context.Context, execer runSlotExecer, run PipelineRun, runID string) error {
	status := run.Status
	if status == "" {
		status = RunStatusQueued
	}
	if status != RunStatusQueued && status != RunStatusRunning {
		return nil
	}
	if ownerRunID, found, err := activeResourceRunForPipeline(ctx, execer, run, runID); err != nil {
		return err
	} else if found {
		return &PipelineRunActiveError{
			PipelineID: strings.TrimSpace(run.PipelineID), ActiveRunID: ownerRunID,
		}
	}
	slotKeys := runSlotKeys(run)
	if len(slotKeys) == 0 {
		return errors.New("active pipeline run requires a stable slot key")
	}
	for _, slotKey := range slotKeys {
		// Resolve a conflict while this write transaction still owns its SQLite
		// lock. Looking up the slot after rollback lets the owner finish between
		// the constraint and the lookup, losing the active run ID returned to the
		// caller.
		if _, err := execer.ExecContext(ctx, `
			INSERT INTO pipeline_run_slots (slot_key, run_id)
			VALUES (?, ?)
			ON CONFLICT (slot_key) DO NOTHING`, slotKey, runID); err != nil {
			return err
		}
		var ownerRunID string
		if err := execer.QueryRowContext(ctx, `
			SELECT run_id
			FROM pipeline_run_slots
			WHERE slot_key = ?`, slotKey).Scan(&ownerRunID); err != nil {
			return fmt.Errorf("resolve owner for active run slot %q: %w", slotKey, err)
		}
		if ownerRunID != runID {
			return &PipelineRunActiveError{
				PipelineID:  strings.TrimSpace(run.PipelineID),
				ActiveRunID: ownerRunID,
			}
		}
	}
	return nil
}

func (s *Store) claimRunAdmission(
	ctx context.Context,
	execer runSlotExecer,
	run PipelineRun,
	runID string,
	plan *PipelineRunPlan,
) error {
	if plan == nil || plan.Version < PipelineRunPlanVersionV2 {
		return s.claimRunSlot(ctx, execer, run, runID)
	}
	if err := validatePipelineRunPlanResources(plan.Resources); err != nil {
		return fmt.Errorf("invalid pipeline run resource claims: %w", err)
	}
	status := run.Status
	if status == "" {
		status = RunStatusQueued
	}
	if status != RunStatusQueued && status != RunStatusRunning {
		return nil
	}

	claims := plan.Resources.Claims
	if plan.Resources.Isolation == PipelineRunResourceIsolationPipeline {
		if err := s.claimRunSlot(ctx, execer, run, runID); err != nil {
			return err
		}
	} else if len(claims) > 0 {
		if ownerRunID, found, err := activeRunForSlots(ctx, execer, runSlotKeys(run), runID); err != nil {
			return err
		} else if found {
			return &PipelineRunActiveError{
				PipelineID: strings.TrimSpace(run.PipelineID), ActiveRunID: ownerRunID,
			}
		}
	}

	if _, err := execer.ExecContext(ctx, `
		INSERT INTO pipeline_run_claim_sets (
			run_id, pipeline_id, pipeline_uuid, isolation
		) VALUES (?, ?, ?, ?)`,
		runID, strings.TrimSpace(run.PipelineID), strings.TrimSpace(run.PipelineUUID), plan.Resources.Isolation,
	); err != nil {
		return err
	}
	for _, claim := range claims {
		claimKey := claim.Kind + ":" + claim.Identity
		if _, err := execer.ExecContext(ctx, `
			INSERT INTO pipeline_run_resource_claims (claim_key, run_id, kind, identity)
			VALUES (?, ?, ?, ?)
			ON CONFLICT (claim_key) DO NOTHING`, claimKey, runID, claim.Kind, claim.Identity); err != nil {
			return err
		}
		var ownerRunID string
		if err := execer.QueryRowContext(ctx, `
			SELECT run_id
			FROM pipeline_run_resource_claims
			WHERE claim_key = ?`, claimKey).Scan(&ownerRunID); err != nil {
			return fmt.Errorf("resolve owner for write-resource claim %q: %w", claim.Kind, err)
		}
		if ownerRunID != runID {
			return &PipelineRunActiveError{
				PipelineID: strings.TrimSpace(run.PipelineID), ActiveRunID: ownerRunID,
			}
		}
	}
	return nil
}

func activeRunForSlots(
	ctx context.Context,
	execer runSlotExecer,
	slotKeys []string,
	excludeRunID string,
) (string, bool, error) {
	for _, slotKey := range slotKeys {
		var ownerRunID string
		err := execer.QueryRowContext(ctx, `
			SELECT run_id FROM pipeline_run_slots WHERE slot_key = ?`, strings.TrimSpace(slotKey)).Scan(&ownerRunID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return "", false, err
		}
		if ownerRunID != strings.TrimSpace(excludeRunID) {
			return ownerRunID, true, nil
		}
	}
	return "", false, nil
}

func activeResourceRunForPipeline(
	ctx context.Context,
	execer runSlotExecer,
	run PipelineRun,
	excludeRunID string,
) (string, bool, error) {
	var ownerRunID string
	err := execer.QueryRowContext(ctx, `
		SELECT claim_set.run_id
		FROM pipeline_run_claim_sets AS claim_set
		WHERE claim_set.run_id <> ?
		  AND (claim_set.pipeline_id = ? OR claim_set.pipeline_uuid = ?)
		  AND EXISTS (
			SELECT 1
			FROM pipeline_run_resource_claims AS resource
			WHERE resource.run_id = claim_set.run_id
		  )
		ORDER BY claim_set.run_id
		LIMIT 1`,
		strings.TrimSpace(excludeRunID), strings.TrimSpace(run.PipelineID), strings.TrimSpace(run.PipelineUUID),
	).Scan(&ownerRunID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return ownerRunID, true, nil
}

func ensureRunSpecUUIDSlot(ctx context.Context, tx *sql.Tx, runID string, spec runSpecV1) error {
	pipelineUUID := strings.TrimSpace(spec.Pipeline.UUID)
	if pipelineUUID == "" {
		return nil
	}
	slotKey := "uuid:" + pipelineUUID
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO pipeline_run_slots (slot_key, run_id)
		VALUES (?, ?)
		ON CONFLICT (slot_key) DO NOTHING`, slotKey, runID); err != nil {
		return err
	}
	var ownerRunID string
	if err := tx.QueryRowContext(ctx, `
		SELECT run_id FROM pipeline_run_slots WHERE slot_key = ?`, slotKey).Scan(&ownerRunID); err != nil {
		return err
	}
	if ownerRunID != runID {
		return &PipelineRunActiveError{PipelineID: spec.Pipeline.ID, ActiveRunID: ownerRunID}
	}
	return nil
}

// validateActiveRunSpecSlotBinding anchors the private stable UUID to durable
// admission state that the JSON body cannot rewrite. Legacy and conservative
// runs use path/UUID slots; v2 resource-isolated runs use a bound claim set.
func (s *Store) validateActiveRunSpecSlotBinding(ctx context.Context, run PipelineRun, spec runSpecV1) error {
	var claimedPipelineID, claimedPipelineUUID, isolation string
	err := s.db.QueryRowContext(ctx, `
		SELECT pipeline_id, pipeline_uuid, isolation
		FROM pipeline_run_claim_sets
		WHERE run_id = ?`, run.ID).Scan(&claimedPipelineID, &claimedPipelineUUID, &isolation)
	if err == nil {
		if claimedPipelineID != strings.TrimSpace(run.PipelineID) ||
			claimedPipelineID != strings.TrimSpace(spec.Pipeline.ID) {
			return errors.New("run spec pipeline path does not match active resource claims")
		}
		if claimedPipelineUUID != strings.TrimSpace(spec.Pipeline.UUID) {
			return errors.New("run spec stable pipeline UUID does not match active resource claims")
		}
		switch isolation {
		case PipelineRunResourceIsolationResources:
			return nil
		case PipelineRunResourceIsolationPipeline:
			// The claim set binds the reviewed resources; pipeline isolation
			// additionally depends on both legacy-compatible slot aliases below.
		default:
			return errors.New("active resource claims have an invalid isolation mode")
		}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	expectedPath := "path:" + strings.TrimSpace(run.PipelineID)
	var hasPath bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM pipeline_run_slots
			WHERE slot_key = ? AND run_id = ?
		)`, expectedPath, run.ID).Scan(&hasPath); err != nil {
		return err
	}
	if !hasPath {
		return errors.New("run spec pipeline path does not match active run slot")
	}

	expectedUUID := strings.TrimSpace(spec.Pipeline.UUID)
	rows, err := s.db.QueryContext(ctx, `
		SELECT slot_key
		FROM pipeline_run_slots
		WHERE run_id = ? AND slot_key LIKE 'uuid:%'
		ORDER BY slot_key`, run.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var uuidSlots []string
	for rows.Next() {
		var slotKey string
		if err := rows.Scan(&slotKey); err != nil {
			return err
		}
		uuidSlots = append(uuidSlots, slotKey)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if expectedUUID == "" {
		if len(uuidSlots) != 0 {
			return errors.New("run spec omits the stable pipeline UUID owned by the active run slot")
		}
		return nil
	}
	if len(uuidSlots) != 1 || uuidSlots[0] != "uuid:"+expectedUUID {
		return errors.New("run spec stable pipeline UUID does not match active run slot")
	}
	return nil
}

func (s *Store) insertRunSpec(ctx context.Context, execer runSpecExecer, runID string, spec runSpecV1) error {
	body, err := marshalRunSpec(spec)
	if err != nil {
		return err
	}
	if _, err = execer.ExecContext(ctx, `
		INSERT INTO pipeline_run_specs (run_id, version, body, created_at)
		VALUES (?, ?, ?, ?)`, runID, spec.Version, string(body), formatTime(time.Now().UTC())); err != nil {
		return err
	}
	if spec.SelectionDetails == nil {
		return nil
	}
	units := make([]PipelineRunExecutionUnit, 0, len(spec.SelectionDetails.Units))
	for _, unit := range spec.SelectionDetails.Units {
		units = append(units, PipelineRunExecutionUnit{
			AssetID: unit.AssetID, AssetName: unit.AssetName,
			StartDate: unit.Start.UTC().Format(time.RFC3339Nano),
			EndDate:   unit.End.UTC().Format(time.RFC3339Nano),
			Reason:    unit.Reason,
		})
	}
	return s.insertRunUnits(ctx, execer, runID, units)
}

func (s *Store) insertRunPlan(ctx context.Context, execer runSpecExecer, runID string, plan PipelineRunPlan) error {
	body, err := marshalPipelineRunPlan(plan)
	if err != nil {
		return err
	}
	_, err = execer.ExecContext(ctx, `
		INSERT INTO pipeline_run_plans (run_id, version, body, created_at)
		VALUES (?, ?, ?, ?)`, runID, plan.Version, string(body), formatTime(time.Now().UTC()))
	if err != nil {
		return err
	}
	return s.insertRunUnits(ctx, execer, runID, plan.ExecutionUnits)
}

func (s *Store) insertRunUnits(ctx context.Context, execer runSpecExecer, runID string, units []PipelineRunExecutionUnit) error {
	for position, unit := range units {
		if _, err := execer.ExecContext(ctx, `
			INSERT INTO pipeline_run_units (
				run_id, position, asset_id, asset_name, start_date, end_date,
				render_index, reason, status
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			runID, position, unit.AssetID, unit.AssetName, unit.StartDate, unit.EndDate,
			unit.RenderIndex, unit.Reason, string(PipelineRunUnitQueued)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CreateWithSpec(ctx context.Context, run PipelineRun, spec runSpecV1) (string, error) {
	return s.createWithSpecAndPlan(ctx, run, spec, nil)
}

// CreateWithSpecAndPlan atomically admits a scheduled run together with the
// private RunSpec, redacted plan artifact, and exact execution units. A worker
// can therefore never observe an executable scheduled run without its plan.
func (s *Store) CreateWithSpecAndPlan(
	ctx context.Context,
	run PipelineRun,
	spec runSpecV1,
	plan PipelineRunPlan,
) (string, error) {
	return s.createWithSpecAndPlan(ctx, run, spec, &plan)
}

func (s *Store) createWithSpecAndPlan(
	ctx context.Context,
	run PipelineRun,
	spec runSpecV1,
	plan *PipelineRunPlan,
) (string, error) {
	if err := spec.validate(); err != nil {
		return "", err
	}
	if err := validateRunSpecAdmissionBinding(run, spec); err != nil {
		return "", err
	}
	if plan != nil {
		if err := validateRunPlanAdmissionBinding(run, spec, *plan); err != nil {
			return "", err
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return "", err
		}
		queries := s.queries.WithTx(tx)
		id, err := s.createRun(ctx, queries, run)
		if err == nil {
			err = s.claimRunAdmission(ctx, tx, run, id, plan)
		}
		if err == nil {
			err = s.insertRunSpec(ctx, tx, id, spec)
		}
		if err == nil && plan != nil {
			err = s.insertRunPlan(ctx, tx, id, *plan)
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			_ = tx.Rollback()
		}
		if errors.Is(err, ErrPipelineRunActive) {
			return "", err
		}
		if !isActiveRunSlotConstraint(err) {
			return id, err
		}
		conflict, found, lookupErr := s.pipelineRunActiveError(ctx, run.PipelineID, runSlotKeys(run))
		if lookupErr != nil {
			return "", errors.Join(err, lookupErr)
		}
		if found {
			return "", conflict
		}
		if attempt == 1 {
			return "", fmt.Errorf("pipeline run admission retry exhausted after the active slot changed: %w", err)
		}
	}
	return "", errors.New("pipeline run admission retry exhausted")
}

func (s *Store) GetRunSpec(ctx context.Context, runID string) (runSpecV1, bool, error) {
	var version int
	var body string
	err := s.db.QueryRowContext(ctx, `
		SELECT version, body
		FROM pipeline_run_specs
		WHERE run_id = ?`, strings.TrimSpace(runID)).Scan(&version, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return runSpecV1{}, false, nil
	}
	if err != nil {
		return runSpecV1{}, false, err
	}
	spec, err := unmarshalRunSpec(version, []byte(body))
	if err != nil {
		return runSpecV1{}, true, &invalidRunSpecError{RunID: runID, Err: err}
	}
	return spec, true, nil
}

// GetRunPlan returns the immutable, redacted plan reviewed at admission. Runs
// admitted outside the plan-confirmation path, including legacy and scheduled
// runs, do not have one yet.
func (s *Store) GetRunPlan(ctx context.Context, runID string) (PipelineRunPlan, bool, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return PipelineRunPlan{}, false, errors.New("run id is required")
	}
	var version int
	var body string
	err := s.db.QueryRowContext(ctx, `
		SELECT version, body
		FROM pipeline_run_plans
		WHERE run_id = ?`, runID).Scan(&version, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return PipelineRunPlan{}, false, nil
	}
	if err != nil {
		return PipelineRunPlan{}, false, err
	}
	plan, err := unmarshalPipelineRunPlan(version, []byte(body))
	if err != nil {
		return PipelineRunPlan{}, true, &invalidRunPlanError{RunID: runID, Err: err}
	}
	return plan, true, nil
}

func (s *Store) ListRunUnits(ctx context.Context, runID string) ([]PipelineRunUnit, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, errors.New("run id is required")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT position, asset_id, asset_name, start_date, end_date, render_index,
		       reason, status, started_at, finished_at, error
		FROM pipeline_run_units
		WHERE run_id = ?
		ORDER BY position`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	units := make([]PipelineRunUnit, 0)
	for rows.Next() {
		var unit PipelineRunUnit
		var startDate, endDate, status string
		var startedAt, finishedAt, unitError sql.NullString
		if err := rows.Scan(
			&unit.Position, &unit.AssetID, &unit.AssetName, &startDate, &endDate,
			&unit.RenderIndex, &unit.Reason, &status, &startedAt, &finishedAt, &unitError,
		); err != nil {
			return nil, err
		}
		unit.StartDate = startDate
		unit.EndDate = endDate
		unit.Status = PipelineRunUnitStatus(status)
		if !validPipelineRunUnitStatus(unit.Status) {
			return nil, fmt.Errorf("pipeline run %s unit %d has invalid status %q", runID, unit.Position, status)
		}
		unit.StartedAt = parseOptionalTime(startedAt)
		unit.FinishedAt = parseOptionalTime(finishedAt)
		unit.Error = unitError.String
		units = append(units, unit)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return units, nil
}

func parseOptionalTime(value sql.NullString) *time.Time {
	if !value.Valid || strings.TrimSpace(value.String) == "" {
		return nil
	}
	parsed := parseTimeValue(value.String)
	if parsed.IsZero() {
		return nil
	}
	return &parsed
}

func validPipelineRunUnitStatus(status PipelineRunUnitStatus) bool {
	switch status {
	case PipelineRunUnitQueued, PipelineRunUnitRunning, PipelineRunUnitSuccess,
		PipelineRunUnitFailed, PipelineRunUnitCancelled, PipelineRunUnitSkipped:
		return true
	default:
		return false
	}
}

func terminalPipelineRunUnitStatus(status PipelineRunUnitStatus) bool {
	switch status {
	case PipelineRunUnitSuccess, PipelineRunUnitFailed, PipelineRunUnitCancelled, PipelineRunUnitSkipped:
		return true
	default:
		return false
	}
}

func (s *Store) UpdateRunUnit(ctx context.Context, runID string, event PipelineRunUnitEvent) error {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return errors.New("run id is required")
	}
	if event.Position < 0 {
		return errors.New("pipeline run unit position cannot be negative")
	}
	if event.Status != PipelineRunUnitRunning && !terminalPipelineRunUnitStatus(event.Status) {
		return fmt.Errorf("invalid pipeline run unit event status %q", event.Status)
	}
	now := time.Now().UTC()
	if event.Status == PipelineRunUnitRunning {
		startedAt := now
		if event.StartedAt != nil && !event.StartedAt.IsZero() {
			startedAt = event.StartedAt.UTC()
		}
		result, err := s.db.ExecContext(ctx, `
			UPDATE pipeline_run_units
			SET status = ?, started_at = COALESCE(started_at, ?)
			WHERE run_id = ? AND position = ? AND status IN (?, ?)`,
			string(PipelineRunUnitRunning), formatTime(startedAt), runID, event.Position,
			string(PipelineRunUnitQueued), string(PipelineRunUnitRunning))
		return expectOneRunUnitUpdate(result, err, runID, event.Position, event.Status)
	}
	finishedAt := now
	if event.FinishedAt != nil && !event.FinishedAt.IsZero() {
		finishedAt = event.FinishedAt.UTC()
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE pipeline_run_units
		SET status = ?, finished_at = ?, error = ?
		WHERE run_id = ? AND position = ? AND status IN (?, ?)`,
		string(event.Status), formatTime(finishedAt), stringValue(event.Error), runID, event.Position,
		string(PipelineRunUnitQueued), string(PipelineRunUnitRunning))
	return expectOneRunUnitUpdate(result, err, runID, event.Position, event.Status)
}

// BindInlineRunExecutionUnits durably records a full-pipeline unit selection
// that could only be resolved after parsing the working tree. The transaction
// completes before any unit can transition to running, so an interrupted
// inline execution never has physical work without its exact unit ledger.
func (s *Store) BindInlineRunExecutionUnits(
	ctx context.Context,
	runID string,
	units []RunSelectionUnit,
) error {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return errors.New("run id is required")
	}
	if len(units) == 0 {
		return errors.New("inline execution units are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var (
		version int
		body    string
		status  string
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT specs.version, specs.body, runs.status
		FROM pipeline_run_specs AS specs
		JOIN pipeline_runs AS runs ON runs.id = specs.run_id
		WHERE specs.run_id = ?`, runID).Scan(&version, &body, &status); err != nil {
		return err
	}
	spec, err := unmarshalRunSpec(version, []byte(body))
	if err != nil {
		return &invalidRunSpecError{RunID: runID, Err: err}
	}
	if spec.Dispatch != runDispatchInlineStreaming {
		return errors.New("run is not an inline-streaming execution")
	}
	if RunStatus(status) != RunStatusRunning {
		return fmt.Errorf("inline run %s cannot bind execution units from status %s", runID, status)
	}
	if spec.Version != runSpecVersionV1 ||
		spec.Selection != runSelectionAll ||
		spec.SelectionDetails != nil {
		return fmt.Errorf("inline run %s already has an exact execution selection", runID)
	}
	if err := applyInlineRunSelection(&spec, RunSelection{
		Mode:  RunSelectionAll,
		Units: units,
	}); err != nil {
		return fmt.Errorf("normalize inline execution units: %w", err)
	}
	updatedBody, err := marshalRunSpec(spec)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE pipeline_run_specs
		SET version = ?, body = ?
		WHERE run_id = ? AND version = ?`,
		spec.Version, string(updatedBody), runID, version)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return fmt.Errorf("inline run %s execution selection changed while binding units", runID)
	}
	executionUnits := make([]PipelineRunExecutionUnit, 0, len(spec.SelectionDetails.Units))
	for _, unit := range spec.SelectionDetails.Units {
		executionUnits = append(executionUnits, PipelineRunExecutionUnit{
			AssetID: unit.AssetID, AssetName: unit.AssetName,
			StartDate: unit.Start.UTC().Format(time.RFC3339Nano),
			EndDate:   unit.End.UTC().Format(time.RFC3339Nano),
			Reason:    unit.Reason,
		})
	}
	if err := s.insertRunUnits(ctx, tx, runID, executionUnits); err != nil {
		return err
	}
	return tx.Commit()
}

// BindQueuedRunExecutionUnits durably records the exact unit ledger for a
// planless queued run whose source only revealed parallel execution after the
// worker parsed it. Admission already holds the conservative pipeline slot;
// this transaction completes before any unit can transition to running.
func (s *Store) BindQueuedRunExecutionUnits(
	ctx context.Context,
	runID string,
	units []PipelineRunExecutionUnit,
) error {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return errors.New("run id is required")
	}
	if len(units) == 0 {
		return errors.New("queued execution units are required")
	}
	if len(units) > maxRunSelectionUnits {
		return fmt.Errorf("queued execution units exceed the %d unit limit", maxRunSelectionUnits)
	}
	for position, unit := range units {
		if err := validatePipelineRunExecutionUnit(unit); err != nil {
			return fmt.Errorf("queued execution unit %d: %w", position, err)
		}
		if err := validatePipelineRunExecutionDependencies(position, unit.DependencyPositions); err != nil {
			return fmt.Errorf("queued execution unit %d: %w", position, err)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var (
		version                  int
		body                     string
		status                   string
		winStart                 sql.NullString
		winEnd                   sql.NullString
		executionContextResolved bool
		retainedPlans            int
		retainedUnits            int
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT specs.version, specs.body, runs.status, runs.win_start, runs.win_end,
		       runs.execution_context_resolved,
		       (SELECT COUNT(*) FROM pipeline_run_plans WHERE run_id = runs.id),
		       (SELECT COUNT(*) FROM pipeline_run_units WHERE run_id = runs.id)
		FROM pipeline_run_specs AS specs
		JOIN pipeline_runs AS runs ON runs.id = specs.run_id
		WHERE specs.run_id = ?`, runID).Scan(
		&version,
		&body,
		&status,
		&winStart,
		&winEnd,
		&executionContextResolved,
		&retainedPlans,
		&retainedUnits,
	); err != nil {
		return err
	}
	spec, err := unmarshalRunSpec(version, []byte(body))
	if err != nil {
		return &invalidRunSpecError{RunID: runID, Err: err}
	}
	if spec.Dispatch != runDispatchRiver {
		return errors.New("run is not a queued River execution")
	}
	if RunStatus(status) != RunStatusRunning {
		return fmt.Errorf("queued run %s cannot bind execution units from status %s", runID, status)
	}
	if !executionContextResolved || !winStart.Valid || !winEnd.Valid {
		return fmt.Errorf("queued run %s cannot bind units before its execution context", runID)
	}
	if spec.Version != runSpecVersionV1 ||
		spec.Selection != runSelectionAll ||
		spec.SelectionDetails != nil {
		return fmt.Errorf("queued run %s already has an exact execution selection", runID)
	}
	if retainedPlans != 0 || retainedUnits != 0 {
		return fmt.Errorf("queued run %s already has a durable execution plan or units", runID)
	}
	effectiveStart := parseTimeValue(winStart.String)
	effectiveEnd := parseTimeValue(winEnd.String)
	if effectiveStart.IsZero() || effectiveEnd.IsZero() || !effectiveStart.Before(effectiveEnd) {
		return fmt.Errorf("queued run %s has an invalid effective execution window", runID)
	}
	for position, unit := range units {
		start, _ := time.Parse(time.RFC3339Nano, strings.TrimSpace(unit.StartDate))
		end, _ := time.Parse(time.RFC3339Nano, strings.TrimSpace(unit.EndDate))
		if start.Before(effectiveStart) || end.After(effectiveEnd) {
			return fmt.Errorf(
				"queued execution unit %d falls outside run %s's effective window",
				position,
				runID,
			)
		}
	}
	if err := s.insertRunUnits(ctx, tx, runID, units); err != nil {
		return err
	}
	return tx.Commit()
}

func expectOneRunUnitUpdate(result sql.Result, err error, runID string, position int, status PipelineRunUnitStatus) error {
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return fmt.Errorf("pipeline run %s unit %d cannot transition to %s", runID, position, status)
	}
	return nil
}

func (s *Store) SetRunSpecIfMissing(ctx context.Context, runID string, spec runSpecV1) (runSpecV1, error) {
	body, err := marshalRunSpec(spec)
	if err != nil {
		return runSpecV1{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return runSpecV1{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `
		INSERT INTO pipeline_run_specs (run_id, version, body, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (run_id) DO NOTHING`, runID, spec.Version, string(body), formatTime(time.Now().UTC()))
	if err != nil {
		return runSpecV1{}, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return runSpecV1{}, err
	}
	if inserted == 1 {
		if err := ensureRunSpecUUIDSlot(ctx, tx, runID, spec); err != nil {
			return runSpecV1{}, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE pipeline_runs
			SET full_refresh = ?, backfill = ?, sensor_mode = ?
			WHERE id = ? AND status = ? AND execution_context_resolved = 0`,
			boolInt64(spec.Requested.FullRefresh), boolInt64(spec.Requested.Backfill), strings.TrimSpace(spec.Requested.SensorMode),
			runID, string(RunStatusQueued)); err != nil {
			return runSpecV1{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return runSpecV1{}, err
	}
	persisted, found, err := s.GetRunSpec(ctx, runID)
	if err != nil {
		return runSpecV1{}, err
	}
	if !found {
		return runSpecV1{}, fmt.Errorf("pipeline run %s spec was not persisted", runID)
	}
	return persisted, nil
}

func isActiveRunSlotConstraint(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) &&
		(sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE || sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY) &&
		strings.Contains(sqliteErr.Error(), "pipeline_run_slots.slot_key")
}

func (s *Store) pipelineRunActiveError(ctx context.Context, pipelineID string, slotKeys []string) (*PipelineRunActiveError, bool, error) {
	for _, slotKey := range slotKeys {
		var activeRunID string
		err := s.db.QueryRowContext(ctx, `
			SELECT run_id
			FROM pipeline_run_slots
			WHERE slot_key = ?`, strings.TrimSpace(slotKey)).Scan(&activeRunID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		return &PipelineRunActiveError{
			PipelineID:  strings.TrimSpace(pipelineID),
			ActiveRunID: activeRunID,
		}, true, nil
	}
	return nil, false, nil
}

// RunExecutionContext is the normalized context that will be used by the
// executor. It is persisted synchronously before the first asset starts so a
// process crash cannot change recovery's materialization semantics.
type RunExecutionContext struct {
	Environment string
	WinStart    time.Time
	WinEnd      time.Time
	FullRefresh bool
	Backfill    bool
	SensorMode  string
}

func (s *Store) SetRunExecutionContext(ctx context.Context, runID string, execution RunExecutionContext) error {
	if strings.TrimSpace(runID) == "" {
		return errors.New("run id is required")
	}
	if err := validateRunExecutionContext(execution); err != nil {
		return err
	}
	updated, err := s.queries.SetRunExecutionContext(ctx, storedb.SetRunExecutionContextParams{
		Environment: strings.TrimSpace(execution.Environment),
		WinStart:    stringValue(formatTime(execution.WinStart)),
		WinEnd:      stringValue(formatTime(execution.WinEnd)),
		FullRefresh: boolInt64(execution.FullRefresh),
		Backfill:    boolInt64(execution.Backfill),
		SensorMode:  strings.TrimSpace(execution.SensorMode),
		ID:          runID,
	})
	if err != nil {
		return err
	}
	if updated != 1 {
		return fmt.Errorf("pipeline run %s was not found", runID)
	}
	return nil
}

func validateRunExecutionContext(execution RunExecutionContext) error {
	if execution.WinStart.IsZero() || execution.WinEnd.IsZero() || !execution.WinStart.Before(execution.WinEnd) {
		return errors.New("resolved run context requires a complete increasing window")
	}
	if execution.FullRefresh && execution.Backfill {
		return errors.New("full refresh and backfill are mutually exclusive")
	}
	switch strings.TrimSpace(execution.SensorMode) {
	case "once", "wait", "skip":
		return nil
	default:
		return fmt.Errorf("resolved run context has invalid sensor mode %q", execution.SensorMode)
	}
}

// SetRunRiverJob links a run created before queue insertion (manual/API runs)
// to the River job that owns its execution.
func (s *Store) SetRunRiverJob(ctx context.Context, runID string, riverJobID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.setRunRiverJob(ctx, s.queries.WithTx(tx), runID, riverJobID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) setRunRiverJob(ctx context.Context, queries *storedb.Queries, runID string, riverJobID int64) error {
	// River's SQLite sequence may reuse an ID after finalized jobs are pruned.
	// A terminal run can safely release that historical link; active links still
	// retain the unique-index guard and fail closed if state is inconsistent.
	if err := queries.ReleaseTerminalRunRiverJob(ctx, storedb.ReleaseTerminalRunRiverJobParams{
		RiverJobID: sql.NullInt64{Int64: riverJobID, Valid: true},
		ID:         runID,
	}); err != nil {
		return err
	}
	updated, err := queries.SetRunRiverJob(ctx, storedb.SetRunRiverJobParams{
		ID:         runID,
		RiverJobID: sql.NullInt64{Int64: riverJobID, Valid: true},
	})
	if err != nil {
		return err
	}
	if updated != 1 {
		return fmt.Errorf("pipeline run %s was not found", runID)
	}
	return nil
}

func (s *Store) RunIDForRiverJob(ctx context.Context, riverJobID int64) (string, bool, error) {
	if riverJobID == 0 {
		return "", false, nil
	}
	var runID string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM pipeline_runs WHERE river_job_id = ?`, riverJobID).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return runID, true, nil
}

// SetRunSnapshotVersion records which deployed snapshot a run executed.
func (s *Store) SetRunSnapshotVersion(ctx context.Context, runID, versionID string) error {
	return s.queries.SetRunSnapshotVersion(ctx, storedb.SetRunSnapshotVersionParams{
		SnapshotVersionID: stringValue(versionID),
		ID:                runID,
	})
}

func (s *Store) MarkRunning(ctx context.Context, id string, at time.Time) error {
	return s.queries.MarkRunRunning(ctx, storedb.MarkRunRunningParams{
		Status:    string(RunStatusRunning),
		StartedAt: stringValue(formatTime(at)),
		ID:        id,
	})
}

func (s *Store) AppendLog(ctx context.Context, id string, line LogLine) error {
	if line.At.IsZero() {
		line.At = time.Now().UTC()
	}
	return s.queries.AppendRunLog(ctx, storedb.AppendRunLogParams{RunID: id, At: formatTime(line.At), Line: line.Line})
}

type InterruptedStateRecovery struct {
	RunIDs             []string
	RiverJobsCancelled int64
	RiverJobsRequeued  int64
}

type interruptedRiverJob struct {
	id      int64
	attempt int
	kind    string
	args    string
}

// ReconcileInterruptedState repairs scheduler state left mid-flight by a
// previous process. The caller must hold the workspace scheduler lock and must
// invoke this before starting River workers, making every River job currently
// marked running unambiguously abandoned.
//
// Renart runs already marked running are failed, as are queued rows that no
// longer have a runnable River job. Available/pending/retryable/scheduled jobs
// are preserved. A claimed schedule signal that has not admitted a run yet is
// returned to River unchanged instead of losing its exact interval/revision.
// This covers both the v2 signal kind and legacy scheduled pipeline-run jobs.
// Open steps are closed, derived-state replay is marked pending, and admitted
// abandoned jobs are terminalized as cancelled in the same SQLite transaction.
func (s *Store) ReconcileInterruptedState(ctx context.Context, reason string) (InterruptedStateRecovery, error) {
	var recovery InterruptedStateRecovery
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return recovery, err
	}
	defer func() { _ = tx.Rollback() }()
	nowTime := time.Now().UTC()
	now := formatTime(nowTime)
	riverNow := formatRiverTime(nowTime)

	// River's SQLite state updates bind scheduled_at as time.Time. Without an
	// explicit modernc time format, a snooze can be stored as time.Time.String
	// (including a timezone name and monotonic suffix), which SQLite cannot
	// compare with its canonical timestamps. Older recovery code also wrote
	// RFC3339 timestamps, which SQLite can parse but River's lexical due-time
	// comparison cannot order against its space-separated timestamps. Return
	// only those otherwise-live, non-River timestamps to the queue while
	// preserving their arguments and attempt.
	result, err := tx.ExecContext(ctx, `
		UPDATE river_job
		SET state = ?, scheduled_at = ?, finalized_at = NULL
		WHERE queue = ?
		  AND kind IN (?, ?)
		  AND state IN (?, ?, ?)
		  AND (
			julianday(scheduled_at) IS NULL
			OR CAST(scheduled_at AS TEXT) NOT GLOB '????-??-?? ??:??:??*'
		  )`,
		string(rivertype.JobStateAvailable), riverNow,
		pipelineRunQueue, pipelineRunJobKind, scheduleSignalJobKind,
		string(rivertype.JobStateAvailable), string(rivertype.JobStateRetryable), string(rivertype.JobStateScheduled),
	)
	if err != nil {
		return recovery, fmt.Errorf("repair malformed River retry timestamps: %w", err)
	}
	repairedRetryTimestamps, err := result.RowsAffected()
	if err != nil {
		return recovery, fmt.Errorf("count repaired River retry timestamps: %w", err)
	}
	recovery.RiverJobsRequeued += repairedRetryTimestamps

	jobRows, err := tx.QueryContext(ctx, `
		SELECT id, attempt, kind, json(args)
		FROM river_job
		WHERE queue = ?
		  AND kind IN (?, ?, ?)
		  AND state = ?
		ORDER BY id`,
		pipelineRunQueue, pipelineRunJobKind, scheduleSignalJobKind, housekeepingJobKind,
		string(rivertype.JobStateRunning),
	)
	if err != nil {
		return recovery, err
	}
	var jobs []interruptedRiverJob
	for jobRows.Next() {
		var job interruptedRiverJob
		if scanErr := jobRows.Scan(&job.id, &job.attempt, &job.kind, &job.args); scanErr != nil {
			_ = jobRows.Close()
			return recovery, scanErr
		}
		jobs = append(jobs, job)
	}
	if closeErr := jobRows.Close(); closeErr != nil {
		return recovery, closeErr
	}
	if err := jobRows.Err(); err != nil {
		return recovery, err
	}

	// Link every pre-upgrade queued run to an extant runnable River job before
	// deciding whether the run is orphaned. New manual admissions persist this
	// link atomically, but old available jobs can survive an upgrade.
	if _, err := tx.ExecContext(ctx, `
		UPDATE pipeline_runs AS run
		SET river_job_id = (
			SELECT job.id
			FROM river_job AS job
			WHERE job.queue = ?
			  AND job.kind = ?
			  AND job.state IN (?, ?, ?, ?, ?)
			  AND json_extract(job.args, '$.run_id') = run.id
			ORDER BY CASE job.state WHEN ? THEN 0 ELSE 1 END, job.id
			LIMIT 1
		)
		WHERE run.status = ?
		  AND run.river_job_id IS NULL
		  AND EXISTS (
			SELECT 1
			FROM river_job AS job
			WHERE job.queue = ?
			  AND job.kind = ?
			  AND job.state IN (?, ?, ?, ?, ?)
			  AND json_extract(job.args, '$.run_id') = run.id
		  )`,
		pipelineRunQueue, pipelineRunJobKind,
		string(rivertype.JobStateAvailable), string(rivertype.JobStatePending), string(rivertype.JobStateRetryable), string(rivertype.JobStateScheduled), string(rivertype.JobStateRunning),
		string(rivertype.JobStateRunning), string(RunStatusQueued),
		pipelineRunQueue, pipelineRunJobKind,
		string(rivertype.JobStateAvailable), string(rivertype.JobStatePending), string(rivertype.JobStateRetryable), string(rivertype.JobStateScheduled), string(rivertype.JobStateRunning),
	); err != nil {
		return recovery, err
	}

	requeuedJobs := make(map[int64]struct{})
	// Legacy manual/API runs were created before their River job was inserted.
	// If an older process died during that handoff, recover the link from its
	// durable arguments. New admissions persist run, spec, job, and link in one
	// transaction, but keep this decoder until pre-upgrade jobs have drained.
	for _, job := range jobs {
		if job.kind == scheduleSignalJobKind {
			result, err := tx.ExecContext(ctx, `
				UPDATE river_job
				SET state = ?,
				    attempt = CASE WHEN attempt > 0 THEN attempt - 1 ELSE 0 END,
				    scheduled_at = ?,
				    finalized_at = NULL
				WHERE id = ? AND state = ?`,
				string(rivertype.JobStateAvailable), riverNow, job.id,
				string(rivertype.JobStateRunning))
			if err != nil {
				return recovery, err
			}
			rowsAffected, err := result.RowsAffected()
			if err != nil {
				return recovery, err
			}
			if rowsAffected == 1 {
				requeuedJobs[job.id] = struct{}{}
				recovery.RiverJobsRequeued++
			}
			continue
		}
		if job.kind != pipelineRunJobKind {
			continue
		}
		var args pipelineRunJobArgs
		if err := json.Unmarshal([]byte(job.args), &args); err != nil {
			return recovery, fmt.Errorf("decode interrupted River job %d arguments: %w", job.id, err)
		}
		if strings.TrimSpace(args.RunID) == "" {
			var linked bool
			if err := tx.QueryRowContext(ctx, `
				SELECT EXISTS(SELECT 1 FROM pipeline_runs WHERE river_job_id = ?)`, job.id).Scan(&linked); err != nil {
				return recovery, err
			}
			if linked {
				continue
			}
			result, err := tx.ExecContext(ctx, `
				UPDATE river_job
				SET state = ?,
				    attempt = CASE WHEN attempt > 0 THEN attempt - 1 ELSE 0 END,
				    scheduled_at = ?,
				    finalized_at = NULL
				WHERE id = ? AND state = ?`,
				string(rivertype.JobStateAvailable), riverNow, job.id, string(rivertype.JobStateRunning))
			if err != nil {
				return recovery, err
			}
			rowsAffected, err := result.RowsAffected()
			if err != nil {
				return recovery, err
			}
			if rowsAffected == 1 {
				requeuedJobs[job.id] = struct{}{}
				recovery.RiverJobsRequeued++
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE pipeline_runs
			SET river_job_id = ?
			WHERE id = ? AND river_job_id IS NULL`, job.id, args.RunID); err != nil {
			return recovery, err
		}
		// Rows admitted by builds predating durable execution context have the
		// request in River but only migration defaults in pipeline_runs. Preserve
		// that best-known request metadata for diagnostics. The resolved flag stays
		// false, so startup never treats it as effective context for fact replay.
		if _, err := tx.ExecContext(ctx, `
			UPDATE pipeline_runs
			SET full_refresh = ?, backfill = ?, sensor_mode = ?
			WHERE id = ?
			  AND execution_context_resolved = 0
			  AND status IN (?, ?)
			  AND NOT EXISTS (SELECT 1 FROM pipeline_run_specs WHERE run_id = pipeline_runs.id)`,
			boolInt64(args.FullRefresh), boolInt64(args.Backfill), strings.TrimSpace(args.SensorMode), args.RunID,
			string(RunStatusQueued), string(RunStatusRunning),
		); err != nil {
			return recovery, err
		}
	}

	runRows, err := tx.QueryContext(ctx, `
		SELECT run.id
		FROM pipeline_runs AS run
		WHERE run.status = ?
		   OR (run.status = ? AND NOT EXISTS (
			SELECT 1
			FROM river_job AS job
			WHERE job.id = run.river_job_id
			  AND job.queue = ?
			  AND job.kind = ?
			  AND job.state IN (?, ?, ?, ?)
		   ))
		ORDER BY COALESCE(started_at, ''), id`,
		string(RunStatusRunning), string(RunStatusQueued), pipelineRunQueue,
		pipelineRunJobKind, string(rivertype.JobStateAvailable), string(rivertype.JobStatePending),
		string(rivertype.JobStateRetryable), string(rivertype.JobStateScheduled),
	)
	if err != nil {
		return recovery, err
	}
	for runRows.Next() {
		var id string
		if scanErr := runRows.Scan(&id); scanErr != nil {
			_ = runRows.Close()
			return recovery, scanErr
		}
		recovery.RunIDs = append(recovery.RunIDs, id)
	}
	if closeErr := runRows.Close(); closeErr != nil {
		return recovery, closeErr
	}
	if err := runRows.Err(); err != nil {
		return recovery, err
	}

	for _, runID := range recovery.RunIDs {
		if err := finishOpenRunSteps(ctx, tx, runID, RunStatusFailed, nowTime, reason); err != nil {
			return recovery, err
		}
		if err := finishOpenRunUnits(ctx, tx, runID, RunStatusFailed, nowTime, reason); err != nil {
			return recovery, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE pipeline_runs
			SET status = ?, finished_at = ?, error = ?, recovery_pending = 1
			WHERE id = ? AND status IN (?, ?)`,
			string(RunStatusFailed), now, reason, runID, string(RunStatusQueued), string(RunStatusRunning)); err != nil {
			return recovery, err
		}
	}

	for _, job := range jobs {
		if _, requeued := requeuedJobs[job.id]; requeued {
			continue
		}
		errorData, marshalErr := json.Marshal(rivertype.AttemptError{
			At:      nowTime,
			Attempt: job.attempt,
			Error:   reason,
		})
		if marshalErr != nil {
			return recovery, marshalErr
		}
		result, updateErr := tx.ExecContext(ctx, `
			UPDATE river_job
			SET state = ?,
			    finalized_at = ?,
			    errors = jsonb_insert(COALESCE(errors, jsonb('[]')), '$[#]', jsonb(?))
			WHERE id = ? AND state = ?`,
			string(rivertype.JobStateCancelled), riverNow, string(errorData), job.id,
			string(rivertype.JobStateRunning),
		)
		if updateErr != nil {
			return recovery, updateErr
		}
		rowsAffected, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return recovery, rowsErr
		}
		recovery.RiverJobsCancelled += rowsAffected
	}

	if err := tx.Commit(); err != nil {
		return InterruptedStateRecovery{}, err
	}
	return recovery, nil
}

// PendingRunRecoveries returns interrupted runs whose persisted terminal steps
// have not yet been replayed into derived state.
func (s *Store) PendingRunRecoveries(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM pipeline_runs
		WHERE recovery_pending = 1
		ORDER BY COALESCE(finished_at, started_at, '') ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// MarkRunRecoveryReplayed acknowledges derived-state replay. If the process
// dies before this write, the next startup replays the run again; downstream
// stores therefore make replay idempotent by run ID.
func (s *Store) MarkRunRecoveryReplayed(ctx context.Context, runID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE pipeline_runs SET recovery_pending = 0 WHERE id = ?`, runID)
	return err
}

func (s *Store) Finish(ctx context.Context, id string, status RunStatus, runErr error) error {
	message := ""
	if runErr != nil {
		message = runErr.Error()
	}
	finishedAt := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := finishOpenRunUnits(ctx, tx, id, status, finishedAt, message); err != nil {
		return err
	}
	if err := s.queries.WithTx(tx).FinishRun(ctx, storedb.FinishRunParams{
		Status:     string(status),
		FinishedAt: stringValue(formatTime(finishedAt)),
		Error:      stringValue(message),
		ID:         id,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

// FinishScheduledSuccess commits a successful scheduled run and its progress
// marker together. A crash or SQLite error must leave both unfinished so the
// interval can be retried, never a successful run with a stale watermark.
func (s *Store) FinishScheduledSuccess(ctx context.Context, id, watermark string, upTo time.Time) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("run id is required")
	}
	watermark = strings.TrimSpace(watermark)
	if watermark == "" {
		return errors.New("schedule watermark key is required")
	}
	if upTo.IsZero() {
		return errors.New("schedule watermark time is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	queries := s.queries.WithTx(tx)
	result, err := tx.ExecContext(ctx, `
		UPDATE pipeline_runs
		SET status = ?, finished_at = ?, error = NULL
		WHERE id = ? AND status IN (?, ?)`,
		string(RunStatusSuccess), formatTime(time.Now().UTC()), id,
		string(RunStatusQueued), string(RunStatusRunning),
	)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return fmt.Errorf("active pipeline run %s was not found", id)
	}
	if err := queries.SetScheduleWatermark(ctx, storedb.SetScheduleWatermarkParams{
		Pipeline: watermark,
		UpTo:     formatTime(upTo),
	}); err != nil {
		return err
	}
	return tx.Commit()
}

// FinalizeExecution closes every still-open step, the run row, and (when
// provided) the schedule watermark in one transaction. A terminal run can
// therefore never become visible with a running step or an advanced watermark
// that was committed separately.
func (s *Store) FinalizeExecution(
	ctx context.Context,
	id string,
	status RunStatus,
	at time.Time,
	runErr error,
	watermark string,
	watermarkUpTo *time.Time,
) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("run id is required")
	}
	if !isTerminalRunStatus(status) {
		return fmt.Errorf("cannot finalize run with non-terminal status %q", status)
	}
	if at.IsZero() {
		return errors.New("run completion time is required")
	}
	message := ""
	if runErr != nil {
		message = runErr.Error()
	}
	watermark = strings.TrimSpace(watermark)
	if (watermark == "") != (watermarkUpTo == nil) {
		return errors.New("schedule watermark key and time must be provided together")
	}
	if watermarkUpTo != nil && watermarkUpTo.IsZero() {
		return errors.New("schedule watermark time is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := finishOpenRunSteps(ctx, tx, id, status, at, message); err != nil {
		return err
	}
	if err := finishOpenRunUnits(ctx, tx, id, status, at, message); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE pipeline_runs
		SET status = ?, finished_at = ?, error = ?
		WHERE id = ? AND status IN (?, ?)`,
		string(status), formatTime(at), stringValue(message), id,
		string(RunStatusQueued), string(RunStatusRunning),
	)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return fmt.Errorf("active pipeline run %s was not found", id)
	}
	if watermark != "" {
		if err := s.queries.WithTx(tx).SetScheduleWatermark(ctx, storedb.SetScheduleWatermarkParams{
			Pipeline: watermark,
			UpTo:     formatTime(*watermarkUpTo),
		}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func finishOpenRunUnits(ctx context.Context, execer runSlotExecer, runID string, status RunStatus, at time.Time, message string) error {
	if strings.TrimSpace(runID) == "" {
		return errors.New("run id is required")
	}
	if !isTerminalRunStatus(status) {
		return fmt.Errorf("cannot finish open execution units with non-terminal status %q", status)
	}
	if at.IsZero() {
		return errors.New("execution unit completion time is required")
	}
	if status == RunStatusSuccess {
		var open int
		if err := execer.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM pipeline_run_units
			WHERE run_id = ? AND status IN (?, ?)`,
			runID, string(PipelineRunUnitQueued), string(PipelineRunUnitRunning)).Scan(&open); err != nil {
			return err
		}
		if open > 0 {
			return fmt.Errorf("pipeline run %s cannot succeed with %d unfinished execution units", runID, open)
		}
		return nil
	}
	unitStatus := PipelineRunUnitFailed
	if status == RunStatusCancelled {
		unitStatus = PipelineRunUnitCancelled
	}
	if _, err := execer.ExecContext(ctx, `
		UPDATE pipeline_run_units
		SET status = ?, finished_at = ?,
		    error = CASE WHEN ? = '' THEN error WHEN error IS NULL OR error = '' THEN ? ELSE error END
		WHERE run_id = ? AND status = ?`,
		string(unitStatus), formatTime(at), message, message, runID, string(PipelineRunUnitRunning)); err != nil {
		return err
	}
	_, err := execer.ExecContext(ctx, `
		UPDATE pipeline_run_units
		SET status = ?, finished_at = ?,
		    error = CASE WHEN ? = '' THEN error WHEN error IS NULL OR error = '' THEN ? ELSE error END
		WHERE run_id = ? AND status = ?`,
		string(PipelineRunUnitSkipped), formatTime(at), message, message, runID, string(PipelineRunUnitQueued))
	return err
}

func (s *Store) List(ctx context.Context, filter RunFilter) (RunList, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	params := runFilterParams(filter, limit, offset)
	total, err := s.queries.CountRuns(ctx, storedb.CountRunsParams{
		PipelineID:  params.PipelineID,
		Environment: params.Environment,
		Status:      params.Status,
		QueryLike:   params.QueryLike,
	})
	if err != nil {
		return RunList{}, err
	}
	rows, err := s.queries.ListRuns(ctx, params)
	if err != nil {
		return RunList{}, err
	}
	runs, err := runsFromDB(rows)
	if err != nil {
		return RunList{}, err
	}
	return RunList{Runs: runs, Total: int(total), Limit: limit, Offset: offset}, nil
}

func runFilterParams(filter RunFilter, limit, offset int) storedb.ListRunsParams {
	queryLike := ""
	if query := strings.TrimSpace(filter.Query); query != "" {
		queryLike = "%" + strings.ToLower(query) + "%"
	}
	return storedb.ListRunsParams{
		PipelineID:  filter.PipelineID,
		Environment: filter.Environment,
		Status:      string(filter.Status),
		QueryLike:   queryLike,
		Limit:       int64(limit),
		Offset:      int64(offset),
	}
}

func (s *Store) Get(ctx context.Context, id string) (PipelineRun, []LogLine, []PipelineRunStep, error) {
	row, err := s.queries.GetRun(ctx, id)
	if err != nil {
		return PipelineRun{}, nil, nil, err
	}
	logRows, err := s.queries.ListRunLogs(ctx, id)
	if err != nil {
		return PipelineRun{}, nil, nil, err
	}
	logs := make([]LogLine, 0, len(logRows))
	for _, item := range logRows {
		logs = append(logs, LogLine{At: parseTimeValue(item.At), Line: item.Line})
	}
	steps, err := s.ListSteps(ctx, id)
	if err != nil {
		return PipelineRun{}, nil, nil, err
	}
	run, err := runFromDB(row)
	if err != nil {
		return PipelineRun{}, nil, nil, err
	}
	return run, logs, steps, nil
}

func (s *Store) UpsertStep(ctx context.Context, step PipelineRunStep) error {
	if strings.TrimSpace(step.Asset) == "" {
		return nil
	}
	if strings.TrimSpace(step.RunID) == "" {
		return errors.New("run id is required")
	}
	if step.CompletionOrdinal != nil && *step.CompletionOrdinal < 0 {
		return errors.New("completion ordinal must not be negative")
	}
	if step.CompletionOrdinal != nil && !isTerminalRunStatus(step.Status) {
		return errors.New("completion ordinal requires a terminal step status")
	}
	if !isTerminalRunStatus(step.Status) {
		return upsertRunStep(ctx, s.queries, step)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var existing sql.NullInt64
	err = tx.QueryRowContext(ctx, `
		SELECT completion_ordinal
		FROM pipeline_run_steps
		WHERE run_id = ? AND asset = ?`, step.RunID, step.Asset).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if existing.Valid {
		if step.CompletionOrdinal != nil && *step.CompletionOrdinal != existing.Int64 {
			return fmt.Errorf(
				"step %s completion ordinal is already %d, not %d",
				step.Asset,
				existing.Int64,
				*step.CompletionOrdinal,
			)
		}
		ordinal := existing.Int64
		step.CompletionOrdinal = &ordinal
	} else if step.CompletionOrdinal == nil {
		var ordinal int64
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(completion_ordinal) + 1, 0)
			FROM pipeline_run_steps
			WHERE run_id = ?`, step.RunID).Scan(&ordinal); err != nil {
			return err
		}
		step.CompletionOrdinal = &ordinal
	}
	if err := upsertRunStep(ctx, s.queries.WithTx(tx), step); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertRunStep(ctx context.Context, queries *storedb.Queries, step PipelineRunStep) error {
	upstreamWriters, err := marshalUpstreamWriterSnapshot(
		step.UpstreamWriters,
		step.HasUpstreamWriterSnapshot,
	)
	if err != nil {
		return err
	}
	updated, err := queries.UpsertRunStep(ctx, storedb.UpsertRunStepParams{
		RunID:                  step.RunID,
		Asset:                  step.Asset,
		Status:                 string(step.Status),
		StartedAt:              nullTime(step.StartedAt),
		FinishedAt:             nullTime(step.FinishedAt),
		Error:                  stringValue(step.Error),
		CompletionOrdinal:      nullInt64(step.CompletionOrdinal),
		UpstreamWriterSnapshot: upstreamWriters,
	})
	if err != nil {
		return err
	}
	if updated != 1 {
		return fmt.Errorf(
			"%w for step %s in run %s",
			ErrUpstreamWriterSnapshotConflict,
			step.Asset,
			step.RunID,
		)
	}
	return nil
}

func (s *Store) ListSteps(ctx context.Context, runID string) ([]PipelineRunStep, error) {
	rows, err := s.queries.ListRunSteps(ctx, runID)
	if err != nil {
		return nil, err
	}
	return stepsFromDB(rows)
}

func (s *Store) FinishOpenSteps(ctx context.Context, runID string, status RunStatus, at time.Time, runErr error) error {
	message := ""
	if runErr != nil {
		message = runErr.Error()
	}
	return finishOpenRunSteps(ctx, s.db, runID, status, at, message)
}

// FinishOpenUnits closes a retained execution-unit ledger without changing the
// parent run. Startup recovery uses it for runs reconciled by builds that
// predate unit-aware orphan cleanup.
func (s *Store) FinishOpenUnits(ctx context.Context, runID string, status RunStatus, at time.Time, runErr error) error {
	message := ""
	if runErr != nil {
		message = runErr.Error()
	}
	return finishOpenRunUnits(ctx, s.db, runID, status, at, message)
}

type runStepExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func finishOpenRunSteps(ctx context.Context, execer runStepExecer, runID string, status RunStatus, at time.Time, message string) error {
	if strings.TrimSpace(runID) == "" {
		return errors.New("run id is required")
	}
	if !isTerminalRunStatus(status) {
		return fmt.Errorf("cannot finish open steps with non-terminal status %q", status)
	}
	if at.IsZero() {
		return errors.New("step completion time is required")
	}
	_, err := execer.ExecContext(ctx, `
		WITH base AS MATERIALIZED (
			SELECT COALESCE(MAX(completion_ordinal), -1) + 1 AS next_ordinal
			FROM pipeline_run_steps
			WHERE run_id = ?
		), ordered AS MATERIALIZED (
			SELECT
				asset,
				ROW_NUMBER() OVER (ORDER BY COALESCE(started_at, ''), asset) - 1 AS offset
			FROM pipeline_run_steps
			WHERE run_id = ? AND finished_at IS NULL
		)
		UPDATE pipeline_run_steps
		SET status = ?,
			finished_at = ?,
			error = CASE WHEN ? = '' THEN error WHEN error IS NULL OR error = '' THEN ? ELSE error END,
			completion_ordinal = COALESCE(
				completion_ordinal,
				(SELECT base.next_ordinal + ordered.offset
				 FROM base, ordered
				 WHERE ordered.asset = pipeline_run_steps.asset)
			)
		WHERE run_id = ? AND finished_at IS NULL`,
		runID,
		runID,
		string(status),
		formatTime(at),
		message,
		message,
		runID,
	)
	return err
}

func isTerminalRunStatus(status RunStatus) bool {
	switch status {
	case RunStatusSuccess, RunStatusFailed, RunStatusCancelled:
		return true
	default:
		return false
	}
}

func (s *Store) HasActiveRun(ctx context.Context, pipelineID string) (bool, error) {
	count, err := s.queries.CountActiveRuns(ctx, storedb.CountActiveRunsParams{
		PipelineID:    pipelineID,
		QueuedStatus:  string(RunStatusQueued),
		RunningStatus: string(RunStatusRunning),
	})
	return count > 0, err
}

// ActiveRunID returns the owner of the atomic path/UUID run slot. Checking
// both aliases keeps pre-RunSpec path-only rows visible while newer runs are
// protected against pipeline renames by the stable UUID slot.
func (s *Store) ActiveRunID(ctx context.Context, pipelineID, pipelineUUID string) (string, error) {
	active, found, err := s.pipelineRunActiveError(ctx, pipelineID, runSlotKeys(PipelineRun{
		PipelineID:   strings.TrimSpace(pipelineID),
		PipelineUUID: strings.TrimSpace(pipelineUUID),
	}))
	if err != nil || !found {
		return "", err
	}
	return active.ActiveRunID, nil
}

// ConflictingRunID previews the same conservative-slot and exact-resource
// conflicts enforced transactionally by admission. It is advisory only: the
// write transaction remains the final authority if state changes after review.
func (s *Store) ConflictingRunID(
	ctx context.Context,
	pipelineID string,
	pipelineUUID string,
	resources PipelineRunPlanResources,
) (string, error) {
	if err := validatePipelineRunPlanResources(resources); err != nil {
		return "", err
	}
	run := PipelineRun{
		PipelineID: strings.TrimSpace(pipelineID), PipelineUUID: strings.TrimSpace(pipelineUUID),
	}
	if resources.Isolation == PipelineRunResourceIsolationPipeline || len(resources.Claims) > 0 {
		if ownerRunID, found, err := activeRunForSlots(ctx, s.db, runSlotKeys(run), ""); err != nil {
			return "", err
		} else if found {
			return ownerRunID, nil
		}
	}
	if resources.Isolation == PipelineRunResourceIsolationPipeline {
		if ownerRunID, found, err := activeResourceRunForPipeline(ctx, s.db, run, ""); err != nil {
			return "", err
		} else if found {
			return ownerRunID, nil
		}
	}
	for _, claim := range resources.Claims {
		var ownerRunID string
		err := s.db.QueryRowContext(ctx, `
			SELECT run_id
			FROM pipeline_run_resource_claims
			WHERE claim_key = ?`, claim.Kind+":"+claim.Identity).Scan(&ownerRunID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return "", err
		}
		return ownerRunID, nil
	}
	return "", nil
}

func (s *Store) LastInterval(ctx context.Context, pipeline string) (time.Time, bool, error) {
	raw, err := s.queries.GetScheduleWatermark(ctx, pipeline)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return parseTimeValue(raw), true, nil
}

func (s *Store) SetInterval(ctx context.Context, pipeline string, upTo time.Time) error {
	return s.queries.SetScheduleWatermark(ctx, storedb.SetScheduleWatermarkParams{Pipeline: pipeline, UpTo: formatTime(upTo)})
}

func (s *Store) ScheduleEnabled(ctx context.Context, pipelineID string) (bool, bool, error) {
	enabled, err := s.queries.GetScheduleEnabled(ctx, pipelineID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return enabled != 0, true, nil
}

func (s *Store) SetScheduleEnabled(ctx context.Context, pipelineID string, enabled bool) error {
	value := 0
	if enabled {
		value = 1
	}
	return s.queries.SetScheduleEnabled(ctx, storedb.SetScheduleEnabledParams{
		PipelineID: pipelineID,
		Enabled:    int64(value),
		UpdatedAt:  formatTime(time.Now().UTC()),
	})
}

func (s *Store) UpsertEnvSchedule(ctx context.Context, schedule EnvSchedule) error {
	now := time.Now().UTC()
	if schedule.CreatedAt.IsZero() {
		schedule.CreatedAt = now
	}
	varsJSON := ""
	storedVariables := schedule.Vars
	if schedule.DeclarationManaged {
		storedVariables = storedScheduleVariables(schedule.Vars, schedule.SecretRefs)
	}
	if len(storedVariables) > 0 {
		encoded, err := json.Marshal(storedVariables)
		if err != nil {
			return err
		}
		varsJSON = string(encoded)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	queries := s.queries.WithTx(tx)
	if err := queries.UpsertEnvSchedule(ctx, storedb.UpsertEnvScheduleParams{
		PipelineID:        schedule.PipelineUUID,
		Environment:       schedule.Environment,
		SnapshotVersionID: schedule.SnapshotVersionID,
		Cron:              schedule.Cron,
		Timezone:          schedule.Timezone,
		Vars:              stringValue(varsJSON),
		CatchupPolicy:     string(schedule.CatchupPolicy),
		Status:            string(schedule.Status),
		ArchivedReason:    schedule.ArchivedReason,
		CreatedAt:         formatTime(schedule.CreatedAt),
		UpdatedAt:         formatTime(now),
	}); err != nil {
		return err
	}
	if schedule.DeclarationManaged {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO renart_schedule_declarations (pipeline_id, environment)
			VALUES (?, ?)
			ON CONFLICT(pipeline_id, environment) DO NOTHING`,
			schedule.PipelineUUID, schedule.Environment,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type envSchedulePinExpectation struct {
	Environment               string
	ExpectedSnapshotVersionID string
}

func (s *Store) PromoteEnvSchedulePins(
	ctx context.Context,
	pipelineUUID string,
	snapshotVersionID string,
	selections []envSchedulePinExpectation,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	updatedAt := formatTime(time.Now().UTC())
	for _, selection := range selections {
		result, err := tx.ExecContext(ctx, `
			UPDATE renart_schedules
			SET snapshot_version_id = ?, updated_at = ?
			WHERE pipeline_id = ? AND environment = ?
			  AND snapshot_version_id = ? AND status <> ?`,
			snapshotVersionID, updatedAt, pipelineUUID, selection.Environment,
			selection.ExpectedSnapshotVersionID, string(ScheduleStatusArchived),
		)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("schedule %s changed after deployment review", selection.Environment)
		}
	}
	return tx.Commit()
}

func (s *Store) ListEnvSchedules(ctx context.Context) ([]EnvSchedule, error) {
	rows, err := s.queries.ListEnvSchedules(ctx)
	if err != nil {
		return nil, err
	}
	managed, err := s.declarationManagedScheduleKeys(ctx)
	if err != nil {
		return nil, err
	}
	schedules := make([]EnvSchedule, 0, len(rows))
	for _, row := range rows {
		schedule := envScheduleFromDB(row)
		if _, ok := managed[scheduleDeclarationKey(schedule.PipelineUUID, schedule.Environment)]; ok {
			schedule.DeclarationManaged = true
			schedule.Vars, schedule.SecretRefs = splitStoredScheduleVariables(schedule.Vars)
		}
		schedules = append(schedules, schedule)
	}
	return schedules, nil
}

func (s *Store) GetEnvSchedule(ctx context.Context, pipelineUUID, environment string) (EnvSchedule, bool, error) {
	row, err := s.queries.GetEnvSchedule(ctx, storedb.GetEnvScheduleParams{PipelineID: pipelineUUID, Environment: environment})
	if errors.Is(err, sql.ErrNoRows) {
		return EnvSchedule{}, false, nil
	}
	if err != nil {
		return EnvSchedule{}, false, err
	}
	schedule := envScheduleFromDB(row)
	managed, err := s.envScheduleDeclarationManaged(ctx, pipelineUUID, environment)
	if err != nil {
		return EnvSchedule{}, false, err
	}
	if managed {
		schedule.DeclarationManaged = true
		schedule.Vars, schedule.SecretRefs = splitStoredScheduleVariables(schedule.Vars)
	}
	return schedule, true, nil
}

func scheduleDeclarationKey(pipelineUUID, environment string) string {
	return pipelineUUID + "\x00" + environment
}

func (s *Store) declarationManagedScheduleKeys(ctx context.Context) (map[string]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT pipeline_id, environment FROM renart_schedule_declarations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]struct{})
	for rows.Next() {
		var pipelineUUID, environment string
		if err := rows.Scan(&pipelineUUID, &environment); err != nil {
			return nil, err
		}
		result[scheduleDeclarationKey(pipelineUUID, environment)] = struct{}{}
	}
	return result, rows.Err()
}

func (s *Store) envScheduleDeclarationManaged(ctx context.Context, pipelineUUID, environment string) (bool, error) {
	var marker int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM renart_schedule_declarations
		WHERE pipeline_id = ? AND environment = ?`, pipelineUUID, environment,
	).Scan(&marker)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) SetEnvScheduleStatus(ctx context.Context, pipelineUUID, environment string, status ScheduleStatus, archivedReason string) error {
	return s.queries.SetEnvScheduleStatus(ctx, storedb.SetEnvScheduleStatusParams{
		Status:         string(status),
		ArchivedReason: archivedReason,
		UpdatedAt:      formatTime(time.Now().UTC()),
		PipelineID:     pipelineUUID,
		Environment:    environment,
	})
}

func (s *Store) SetEnvScheduleNextRun(ctx context.Context, pipelineUUID, environment string, nextRunAt *time.Time) error {
	return s.queries.SetEnvScheduleNextRun(ctx, storedb.SetEnvScheduleNextRunParams{
		NextRunAt:   nullTime(nextRunAt),
		PipelineID:  pipelineUUID,
		Environment: environment,
	})
}

func (s *Store) CountEnvSchedules(ctx context.Context) (int64, error) {
	return s.queries.CountEnvSchedules(ctx)
}

func envScheduleFromDB(row storedb.RenartSchedule) EnvSchedule {
	schedule := EnvSchedule{
		PipelineUUID:      row.PipelineID,
		Environment:       row.Environment,
		SnapshotVersionID: row.SnapshotVersionID,
		Cron:              row.Cron,
		Timezone:          row.Timezone,
		CatchupPolicy:     CatchupPolicy(row.CatchupPolicy),
		Status:            ScheduleStatus(row.Status),
		ArchivedReason:    row.ArchivedReason,
		NextRunAt:         parseNullTime(row.NextRunAt),
		CreatedAt:         parseTimeValue(row.CreatedAt),
		UpdatedAt:         parseTimeValue(row.UpdatedAt),
	}
	if raw := stringFromNull(row.Vars); raw != "" {
		_ = json.Unmarshal([]byte(raw), &schedule.Vars)
	}
	return schedule
}

func nullTime(value *time.Time) sql.NullString {
	if value == nil || value.IsZero() {
		return sql.NullString{}
	}
	return stringValue(formatTime(*value))
}

func stringValue(value string) sql.NullString {
	return sql.NullString{String: value, Valid: true}
}

func stringFromNull(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func parseNullTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	parsed := parseTimeValue(value.String)
	return &parsed
}

func parseTimeValue(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

// formatRiverTime mirrors riversqlite's timestamp encoding. River compares
// timestamps lexically in SQLite, so RFC3339 and other parseable encodings are
// not interchangeable here.
func formatRiverTime(value time.Time) string {
	return value.UTC().Round(time.Millisecond).Format("2006-01-02 15:04:05.999")
}

func runsFromDB(rows []storedb.PipelineRun) ([]PipelineRun, error) {
	runs := make([]PipelineRun, 0, len(rows))
	for _, row := range rows {
		run, err := runFromDB(row)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func runFromDB(row storedb.PipelineRun) (PipelineRun, error) {
	targetSnapshot, err := unmarshalExecutionTargetSnapshot(row.ExecutionTargetSnapshot)
	if err != nil {
		return PipelineRun{}, fmt.Errorf("load execution target snapshot for run %s: %w", row.ID, err)
	}
	return PipelineRun{
		ID:                       row.ID,
		PipelineID:               row.PipelineID,
		RiverJobID:               int64FromNull(row.RiverJobID),
		Pipeline:                 row.Pipeline,
		Environment:              row.Environment,
		Trigger:                  RunTrigger(row.Trigger),
		Status:                   RunStatus(row.Status),
		WinStart:                 parseNullTime(row.WinStart),
		WinEnd:                   parseNullTime(row.WinEnd),
		StartedAt:                parseNullTime(row.StartedAt),
		FinishedAt:               parseNullTime(row.FinishedAt),
		Error:                    stringFromNull(row.Error),
		LogRef:                   stringFromNull(row.LogRef),
		SnapshotVersionID:        stringFromNull(row.SnapshotVersionID),
		FullRefresh:              row.FullRefresh != 0,
		Backfill:                 row.Backfill != 0,
		SensorMode:               row.SensorMode,
		ExecutionContextResolved: row.ExecutionContextResolved != 0,
		ExecutionTargetSnapshot:  targetSnapshot,
	}, nil
}

func boolInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func nullInt64(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

func int64FromNull(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func stepsFromDB(rows []storedb.PipelineRunStep) ([]PipelineRunStep, error) {
	steps := make([]PipelineRunStep, 0, len(rows))
	for _, row := range rows {
		upstreamWriters, hasUpstreamWriters, err := unmarshalUpstreamWriterSnapshot(row.UpstreamWriterSnapshot)
		if err != nil {
			return nil, fmt.Errorf(
				"load upstream writer snapshot for step %s in run %s: %w",
				row.Asset,
				row.RunID,
				err,
			)
		}
		steps = append(steps, PipelineRunStep{
			RunID:                     row.RunID,
			Asset:                     row.Asset,
			Status:                    RunStatus(row.Status),
			StartedAt:                 parseNullTime(row.StartedAt),
			FinishedAt:                parseNullTime(row.FinishedAt),
			Error:                     stringFromNull(row.Error),
			CompletionOrdinal:         int64FromNull(row.CompletionOrdinal),
			UpstreamWriters:           upstreamWriters,
			HasUpstreamWriterSnapshot: hasUpstreamWriters,
		})
	}
	return steps, nil
}

func statusFromResult(result RunResult) (RunStatus, error) {
	switch strings.ToLower(strings.TrimSpace(result.Status)) {
	case "", "ok", "success", "succeeded":
		return RunStatusSuccess, nil
	case "cancelled", "canceled":
		if result.Error == "" {
			result.Error = "pipeline run was cancelled"
		}
		return RunStatusCancelled, errors.New(result.Error)
	}
	if result.Error == "" {
		result.Error = fmt.Sprintf("pipeline run finished with status %s", result.Status)
	}
	return RunStatusFailed, errors.New(result.Error)
}
