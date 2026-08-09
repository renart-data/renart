CREATE TABLE IF NOT EXISTS pipeline_runs (
    id TEXT PRIMARY KEY,
    pipeline_id TEXT NOT NULL,
    pipeline TEXT NOT NULL,
    environment TEXT NOT NULL,
    trigger TEXT NOT NULL,
    status TEXT NOT NULL,
    win_start TEXT,
    win_end TEXT,
    started_at TEXT,
    finished_at TEXT,
    error TEXT,
    log_ref TEXT,
    snapshot_version_id TEXT,
    recovery_pending INTEGER NOT NULL DEFAULT 0,
    river_job_id INTEGER,
    full_refresh INTEGER NOT NULL DEFAULT 0,
    backfill INTEGER NOT NULL DEFAULT 0,
    sensor_mode TEXT NOT NULL DEFAULT '',
    execution_context_resolved INTEGER NOT NULL DEFAULT 0,
    execution_target_snapshot TEXT NOT NULL DEFAULT ''
        CHECK (execution_target_snapshot = '' OR json_valid(execution_target_snapshot))
);

CREATE INDEX IF NOT EXISTS idx_runs_pipeline_time ON pipeline_runs (pipeline_id, started_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_pipeline_runs_river_job ON pipeline_runs (river_job_id) WHERE river_job_id IS NOT NULL;
-- Private execution contracts are deliberately kept out of pipeline_runs so
-- run-list DTOs and SSE payloads cannot expose authorization or future secret
-- references by accident.
CREATE TABLE IF NOT EXISTS pipeline_run_specs (
    run_id TEXT PRIMARY KEY,
    version INTEGER NOT NULL CHECK (version > 0),
    body TEXT NOT NULL CHECK (json_valid(body)),
    created_at TEXT NOT NULL,
    FOREIGN KEY(run_id) REFERENCES pipeline_runs(id) ON DELETE CASCADE
);

-- Redacted reviewed-plan history is separate from the private RunSpec: the
-- typed units can be exposed in run details without exposing authorization.
CREATE TABLE IF NOT EXISTS pipeline_run_plans (
    run_id TEXT PRIMARY KEY,
    version INTEGER NOT NULL CHECK (version > 0),
    body TEXT NOT NULL CHECK (json_valid(body)),
    created_at TEXT NOT NULL,
    FOREIGN KEY(run_id) REFERENCES pipeline_runs(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS pipeline_run_units (
    run_id TEXT NOT NULL,
    position INTEGER NOT NULL CHECK (position >= 0),
    asset_id TEXT NOT NULL CHECK (asset_id <> ''),
    asset_name TEXT NOT NULL CHECK (asset_name <> ''),
    start_date TEXT NOT NULL,
    end_date TEXT NOT NULL,
    render_index INTEGER NOT NULL CHECK (render_index >= 0),
    reason TEXT NOT NULL CHECK (reason <> ''),
    status TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'success', 'failed', 'cancelled', 'skipped')),
    started_at TEXT,
    finished_at TEXT,
    error TEXT,
    PRIMARY KEY (run_id, position),
    FOREIGN KEY(run_id) REFERENCES pipeline_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_pipeline_run_units_run_status
    ON pipeline_run_units (run_id, status, position);

-- Pipeline-scope execution claims namespaced path and stable-UUID aliases. The
-- path alias bridges pre-upgrade active rows; UUID keeps the slot stable across
-- a rename or move.
CREATE TABLE IF NOT EXISTS pipeline_run_slots (
    slot_key TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    FOREIGN KEY(run_id) REFERENCES pipeline_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_pipeline_run_slots_run ON pipeline_run_slots (run_id);

CREATE TRIGGER IF NOT EXISTS release_pipeline_run_slot
AFTER UPDATE OF status ON pipeline_runs
WHEN OLD.status IN ('queued', 'running')
 AND NEW.status NOT IN ('queued', 'running')
BEGIN
    DELETE FROM pipeline_run_slots WHERE run_id = NEW.id;
END;

-- Version-two reviewed plans use explicit write-resource claims. A row with
-- resource isolation and no child claims is a proven no-write run.
CREATE TABLE IF NOT EXISTS pipeline_run_claim_sets (
    run_id TEXT PRIMARY KEY,
    pipeline_id TEXT NOT NULL CHECK (pipeline_id <> ''),
    pipeline_uuid TEXT NOT NULL CHECK (pipeline_uuid <> ''),
    isolation TEXT NOT NULL
        CHECK (isolation IN ('resources', 'pipeline')),
    FOREIGN KEY(run_id) REFERENCES pipeline_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_pipeline_run_claim_sets_pipeline
    ON pipeline_run_claim_sets (pipeline_uuid, pipeline_id, isolation, run_id);

CREATE TABLE IF NOT EXISTS pipeline_run_resource_claims (
    claim_key TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    kind TEXT NOT NULL
        CHECK (kind IN ('local_file', 'duckdb_database', 'warehouse_relation')),
    identity TEXT NOT NULL
        CHECK (length(identity) = 64 AND identity = lower(identity)),
    UNIQUE (run_id, kind, identity),
    FOREIGN KEY(run_id) REFERENCES pipeline_run_claim_sets(run_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_pipeline_run_resource_claims_run
    ON pipeline_run_resource_claims (run_id, kind, identity);

CREATE TRIGGER IF NOT EXISTS release_pipeline_run_resource_claims
AFTER UPDATE OF status ON pipeline_runs
WHEN OLD.status IN ('queued', 'running')
 AND NEW.status NOT IN ('queued', 'running')
BEGIN
    DELETE FROM pipeline_run_claim_sets WHERE run_id = NEW.id;
END;

-- Durable identity for each actual due/catch-up interval. River's ByArgs
-- uniqueness remains a useful active-signal guard, but is not the execution
-- ledger and expires when a queue job becomes terminal.
CREATE TABLE IF NOT EXISTS schedule_occurrences (
    occurrence_key TEXT PRIMARY KEY
        CHECK (length(occurrence_key) = 64 AND occurrence_key = lower(occurrence_key)),
    pipeline_uuid TEXT NOT NULL CHECK (pipeline_uuid <> ''),
    environment TEXT NOT NULL CHECK (environment <> ''),
    interval_start TEXT NOT NULL,
    interval_end TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'admitting', 'active', 'success', 'failed', 'cancelled')),
    current_run_id TEXT
        REFERENCES pipeline_runs(id) ON DELETE SET NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    prerequisite_plan TEXT
        CHECK (prerequisite_plan IS NULL OR json_valid(prerequisite_plan)),
    prerequisite_deadline TEXT,
    prerequisite_reason TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (pipeline_uuid, environment, interval_start, interval_end)
);

CREATE INDEX IF NOT EXISTS idx_schedule_occurrences_schedule_status
    ON schedule_occurrences (pipeline_uuid, environment, status, interval_start);

CREATE TABLE IF NOT EXISTS schedule_occurrence_attempts (
    occurrence_key TEXT NOT NULL
        REFERENCES schedule_occurrences(occurrence_key) ON DELETE CASCADE,
    attempt_no INTEGER NOT NULL CHECK (attempt_no > 0),
    run_id TEXT NOT NULL UNIQUE
        REFERENCES pipeline_runs(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    PRIMARY KEY (occurrence_key, attempt_no)
);

CREATE TRIGGER IF NOT EXISTS update_schedule_occurrence_terminal
AFTER UPDATE OF status ON pipeline_runs
WHEN NEW.status IN ('success', 'failed', 'cancelled')
BEGIN
    UPDATE schedule_occurrences
    SET status = NEW.status,
        updated_at = COALESCE(NULLIF(NEW.finished_at, ''), updated_at)
    WHERE current_run_id = NEW.id
      AND status = 'active';
END;

CREATE TABLE IF NOT EXISTS pipeline_run_logs (
    run_id TEXT NOT NULL,
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    at TEXT NOT NULL,
    line TEXT NOT NULL,
    FOREIGN KEY(run_id) REFERENCES pipeline_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_run_logs_run_seq ON pipeline_run_logs (run_id, seq);

CREATE TABLE IF NOT EXISTS pipeline_run_steps (
    run_id TEXT NOT NULL,
    asset TEXT NOT NULL,
    status TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    error TEXT,
    completion_ordinal INTEGER
        CHECK (completion_ordinal IS NULL OR completion_ordinal >= 0),
    upstream_writer_snapshot TEXT NOT NULL DEFAULT ''
        CHECK (upstream_writer_snapshot = '' OR json_valid(upstream_writer_snapshot)),
    PRIMARY KEY(run_id, asset),
    FOREIGN KEY(run_id) REFERENCES pipeline_runs(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_run_steps_run_started ON pipeline_run_steps (run_id, started_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_pipeline_run_steps_completion
    ON pipeline_run_steps (run_id, completion_ordinal)
    WHERE completion_ordinal IS NOT NULL;

CREATE TABLE IF NOT EXISTS schedule_watermarks (
    pipeline TEXT PRIMARY KEY,
    up_to TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS pipeline_schedule_settings (
    pipeline_id TEXT PRIMARY KEY,
    enabled INTEGER NOT NULL,
    updated_at TEXT NOT NULL
);

-- Durable hand-off between physical run completion and derived-state
-- consumers. The body is a strict versioned completion envelope; sequence is
-- the deterministic replay order.
CREATE TABLE IF NOT EXISTS renart_completion_outbox (
    sequence      INTEGER PRIMARY KEY AUTOINCREMENT,
    completion_id TEXT NOT NULL UNIQUE
        CHECK (completion_id <> '' AND completion_id = trim(completion_id)),
    version       INTEGER NOT NULL CHECK (version = 1),
    body          TEXT NOT NULL
        CHECK (json_valid(body))
        CHECK (json_type(body) = 'object')
        CHECK (COALESCE(json_extract(body, '$.version') = version, 0))
        CHECK (COALESCE(json_extract(body, '$.event.completion_id') = completion_id, 0)),
    enqueued_at   TEXT NOT NULL CHECK (enqueued_at <> '')
);

-- Materialization log (queried by the matlog package directly, not sqlc;
-- kept here so this file stays the full schema reference).
CREATE TABLE IF NOT EXISTS renart_materializations (
    id                INTEGER PRIMARY KEY,
    asset_id          TEXT NOT NULL,
    environment       TEXT NOT NULL,
    fingerprint       TEXT NOT NULL,
    vars_hash         TEXT NOT NULL,
    interval_start    TEXT NOT NULL DEFAULT '',
    interval_end      TEXT NOT NULL DEFAULT '',
    run_id            TEXT NOT NULL,
    materialized_at   TEXT NOT NULL,
    own_content       TEXT NOT NULL DEFAULT '',
    target_identity   TEXT NOT NULL DEFAULT '',
    target_generation INTEGER NOT NULL DEFAULT 0 CHECK (target_generation >= 0),
    completion_id     TEXT NOT NULL DEFAULT '',
    completion_ordinal INTEGER NOT NULL DEFAULT 0 CHECK (completion_ordinal >= 0),
    snapshot_version_id TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_renart_mat_lookup ON renart_materializations
    (asset_id, environment, fingerprint, vars_hash);
CREATE INDEX IF NOT EXISTS idx_renart_mat_age ON renart_materializations
    (materialized_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_renart_mat_run ON renart_materializations
    (asset_id, environment, run_id) WHERE run_id <> '';

CREATE TABLE IF NOT EXISTS renart_coverage (
    asset_id          TEXT NOT NULL,
    environment       TEXT NOT NULL,
    fingerprint       TEXT NOT NULL,
    vars_hash         TEXT NOT NULL,
    target_identity   TEXT NOT NULL DEFAULT '',
    target_generation INTEGER NOT NULL DEFAULT 0 CHECK (target_generation >= 0),
    interval_start    TEXT NOT NULL DEFAULT '',
    interval_end      TEXT NOT NULL DEFAULT '',
    materialized_at   TEXT NOT NULL,
    own_content       TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (
        asset_id, environment, fingerprint, vars_hash,
        target_identity, target_generation, interval_start
    )
);

CREATE INDEX IF NOT EXISTS idx_renart_coverage_selection ON renart_coverage
    (environment, vars_hash, asset_id);
CREATE INDEX IF NOT EXISTS idx_renart_coverage_target ON renart_coverage
    (target_identity, target_generation, asset_id, environment, vars_hash);

-- Durable identity of the successful writer currently present at each
-- physical target. Raw materialization-fact retention never prunes this row.
CREATE TABLE IF NOT EXISTS renart_latest_successful_writers (
    target_identity    TEXT PRIMARY KEY CHECK (target_identity <> ''),
    target_generation  INTEGER NOT NULL CHECK (target_generation > 0),
    asset_id           TEXT NOT NULL,
    environment        TEXT NOT NULL,
    fingerprint        TEXT NOT NULL,
    vars_hash          TEXT NOT NULL,
    run_id             TEXT NOT NULL DEFAULT '',
    materialized_at    TEXT NOT NULL,
    completion_id      TEXT NOT NULL CHECK (completion_id <> ''),
    completion_ordinal INTEGER NOT NULL CHECK (completion_ordinal >= 0),
    ambiguous          INTEGER NOT NULL DEFAULT 0 CHECK (ambiguous IN (0, 1)),
    snapshot_version_id TEXT NOT NULL DEFAULT ''
);

-- Durable fail-closed marker spanning physical execution and materialization
-- fact recording. A target with any active or dirty claim is never considered
-- fresh. Successful target-aware recording clears the matching claim and all
-- older dirty claims in the same transaction as the writer update.
CREATE TABLE IF NOT EXISTS renart_target_write_claims (
    claim_sequence  INTEGER PRIMARY KEY AUTOINCREMENT,
    target_identity TEXT NOT NULL CHECK (target_identity <> ''),
    completion_id   TEXT NOT NULL CHECK (completion_id <> ''),
    asset_id        TEXT NOT NULL CHECK (asset_id <> ''),
    state           TEXT NOT NULL CHECK (state IN ('active', 'dirty')),
    claimed_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    UNIQUE (target_identity, completion_id, asset_id)
);

CREATE INDEX IF NOT EXISTS idx_renart_target_write_claims_target_state
    ON renart_target_write_claims (target_identity, state, claim_sequence);

-- Most recent run attempt per (asset, environment), success or failure;
-- upserted so a later run overwrites the previous outcome.
CREATE TABLE IF NOT EXISTS renart_asset_runs (
    asset_id     TEXT NOT NULL,
    environment  TEXT NOT NULL,
    fingerprint  TEXT NOT NULL,
    status       TEXT NOT NULL,
    run_id       TEXT NOT NULL DEFAULT '',
    quality_status TEXT NOT NULL DEFAULT ''
        CHECK (quality_status IN ('', 'passed', 'failed')),
    failed_checks TEXT NOT NULL DEFAULT '[]',
    ran_at       TEXT NOT NULL,
    PRIMARY KEY (asset_id, environment)
);

-- Snapshot store (queried by the snapshot package directly, not sqlc).
CREATE TABLE IF NOT EXISTS renart_blobs (
    hash    TEXT PRIMARY KEY,
    content BLOB NOT NULL
);

CREATE TABLE IF NOT EXISTS renart_snapshots (
    version_id  TEXT PRIMARY KEY,
    pipeline_id TEXT NOT NULL,
    ordinal     INTEGER NOT NULL CHECK (ordinal > 0),
    merkle_root TEXT NOT NULL,
    manifest    TEXT NOT NULL,
    git_sha     TEXT,
    git_dirty   INTEGER,
    created_at  TEXT NOT NULL,
    created_by  TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_renart_snapshots_pipeline_ordinal
    ON renart_snapshots (pipeline_id, ordinal);
CREATE INDEX IF NOT EXISTS idx_renart_snapshots_pipeline
    ON renart_snapshots (pipeline_id, ordinal DESC);

CREATE TABLE IF NOT EXISTS renart_schedules (
    pipeline_id         TEXT NOT NULL,
    environment         TEXT NOT NULL,
    snapshot_version_id TEXT NOT NULL DEFAULT '',
    cron                TEXT NOT NULL,
    timezone            TEXT NOT NULL DEFAULT 'UTC',
    vars                TEXT,
    catchup_policy      TEXT NOT NULL DEFAULT 'skip',
    status              TEXT NOT NULL DEFAULT 'active',
    archived_reason     TEXT NOT NULL DEFAULT '',
    next_run_at         TEXT,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL,
    PRIMARY KEY (pipeline_id, environment)
);

-- Existing schedules predate version-controlled declarations. Keeping the
-- marker separate prevents an upgrade from copying unknown historical values
-- into Git and lets rows be adopted one at a time.
CREATE TABLE IF NOT EXISTS renart_schedule_declarations (
    pipeline_id TEXT NOT NULL,
    environment TEXT NOT NULL,
    PRIMARY KEY (pipeline_id, environment),
    FOREIGN KEY (pipeline_id, environment)
        REFERENCES renart_schedules (pipeline_id, environment) ON DELETE CASCADE
);
