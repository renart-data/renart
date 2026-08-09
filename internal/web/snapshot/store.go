// Package snapshot stores immutable deployed versions of pipeline source
// files. Scheduled runs execute snapshots; build mode keeps running the
// working tree. Files are content-addressed in renart_blobs and described
// by a per-snapshot manifest, so unchanged files cost nothing across
// versions.
package snapshot

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"renart/internal/web/identity"
)

var ErrSourceChanged = errors.New("snapshot: saved source changed after review")

const DependencyManifestVersion = 1

// DependencyManifest is the immutable cross-pipeline URI ownership contract
// reviewed with one deployment. It intentionally contains no producer source
// bytes or runtime freshness evidence; scheduled planning resolves the bound
// producer UUID against that environment's deployment later.
type DependencyManifest struct {
	Version      int                      `json:"version"`
	Dependencies []DependencyManifestItem `json:"dependencies"`
}

type DependencyManifestItem struct {
	ConsumerAssetID      string `json:"consumer_asset_id"`
	URI                  string `json:"uri"`
	Mode                 string `json:"mode"`
	ProducerPipelineUUID string `json:"producer_pipeline_uuid,omitempty"`
	ProducerAssetURI     string `json:"producer_asset_uri,omitempty"`
}

func EmptyDependencyManifest() DependencyManifest {
	return DependencyManifest{Version: DependencyManifestVersion, Dependencies: []DependencyManifestItem{}}
}

type SourceChangedError struct {
	Expected string
	Actual   string
}

func (e *SourceChangedError) Error() string {
	return fmt.Sprintf("%s: expected %s, got %s", ErrSourceChanged, e.Expected, e.Actual)
}

func (e *SourceChangedError) Unwrap() error { return ErrSourceChanged }

// Snapshot is one deployed version of a pipeline.
type Snapshot struct {
	VersionID          string             `json:"version_id"`
	PipelineUUID       string             `json:"pipeline_id"`
	Ordinal            int64              `json:"ordinal"`
	MerkleRoot         string             `json:"merkle_root"`
	Manifest           map[string]string  `json:"manifest"` // relpath -> blob hash
	DependencyManifest DependencyManifest `json:"dependency_manifest"`
	GitSHA             string             `json:"git_sha,omitempty"`
	GitDirty           bool               `json:"git_dirty"`
	CreatedAt          time.Time          `json:"created_at"`
	CreatedBy          string             `json:"created_by,omitempty"`
}

// DriftReport compares the working tree against the latest deployed
// snapshot.
type DriftReport struct {
	HasSnapshot              bool      `json:"has_snapshot"`
	Executable               bool      `json:"executable"`
	IntegrityError           string    `json:"integrity_error,omitempty"`
	InSync                   bool      `json:"in_sync"`
	DependencyManifestInSync bool      `json:"dependency_manifest_in_sync"`
	DependencyManifestError  string    `json:"dependency_manifest_error,omitempty"`
	VersionID                string    `json:"version_id,omitempty"`
	Ordinal                  int64     `json:"ordinal,omitempty"`
	SourceMerkle             string    `json:"source_merkle"`
	CreatedAt                time.Time `json:"created_at,omitempty"`
	ChangedFiles             []string  `json:"changed_files,omitempty"`
	AddedFiles               []string  `json:"added_files,omitempty"`
	RemovedFiles             []string  `json:"removed_files,omitempty"`
	SnapshotCount            int       `json:"snapshot_count"`
}

// skipDirNames are never snapshotted (VCS internals, caches, local state).
var skipDirNames = map[string]struct{}{
	".git": {}, ".github": {}, ".vscode": {}, "node_modules": {}, "__pycache__": {},
	".venv": {}, "venv": {}, ".renart": {}, "logs": {}, ".pytest_cache": {}, ".ruff_cache": {},
}

// skipFileExtensions excludes local database artifacts that live next to
// pipelines but are data, not source.
var skipFileExtensions = map[string]struct{}{
	".db": {}, ".duckdb": {}, ".sqlite": {}, ".sqlite3": {}, ".wal": {}, ".tmp": {},
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

const timeLayout = time.RFC3339Nano

const maxFileComparisonBytes int64 = 2 << 20

type FileComparison struct {
	Path         string `json:"path"`
	Status       string `json:"status"`
	Before       string `json:"before,omitempty"`
	After        string `json:"after,omitempty"`
	BeforeExists bool   `json:"before_exists"`
	AfterExists  bool   `json:"after_exists"`
	Binary       bool   `json:"binary"`
	TooLarge     bool   `json:"too_large"`
}

type RetentionPolicy struct {
	OlderThan          time.Time
	MinimumPerPipeline int
}

type PruneResult struct {
	Snapshots int64
	Blobs     int64
}

func hashBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// ManifestRoot derives the snapshot identity from the sorted manifest with
// length-prefixed parts (no concatenation ambiguity). Planning and deployment
// must use this same identity so a saved-working-tree preview cannot drift from
// the source version Deploy will later persist.
func ManifestRoot(manifest map[string]string) string {
	paths := make([]string, 0, len(manifest))
	for path := range manifest {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	hasher := sha256.New()
	writePart := func(part string) {
		var lengthBuf [8]byte
		n := len(part)
		for i := 7; i >= 0; i-- {
			lengthBuf[i] = byte(n)
			n >>= 8
		}
		hasher.Write(lengthBuf[:])
		hasher.Write([]byte(part))
	}
	for _, path := range paths {
		writePart(path)
		writePart(manifest[path])
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// merkleRoot remains the package-local spelling used by the snapshot store.
// Keep it as a wrapper so older tests and internal call sites stay readable.
func merkleRoot(manifest map[string]string) string {
	return ManifestRoot(manifest)
}

func walkManifestFiles(pipelineDir string, visit func(path, relativePath string) error) error {
	return filepath.WalkDir(pipelineDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if _, skip := skipDirNames[entry.Name()]; skip && path != pipelineDir {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		if _, skip := skipFileExtensions[strings.ToLower(filepath.Ext(path))]; skip {
			return nil
		}
		rel, relErr := filepath.Rel(pipelineDir, path)
		if relErr != nil {
			return relErr
		}
		return visit(path, filepath.ToSlash(rel))
	})
}

// CollectManifest walks the pipeline directory and returns relpath -> blob
// hash plus the file contents keyed by hash. Deploy uses this content-retaining
// variant because it persists every source blob.
func CollectManifest(pipelineDir string) (map[string]string, map[string][]byte, error) {
	manifest := make(map[string]string)
	contents := make(map[string][]byte)

	walkErr := walkManifestFiles(pipelineDir, func(path, relativePath string) error {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		hash := hashBytes(data)
		manifest[relativePath] = hash
		contents[hash] = data
		return nil
	})
	if walkErr != nil {
		return nil, nil, walkErr
	}
	return manifest, contents, nil
}

// CollectManifestHashes returns the exact same content-addressed manifest as
// CollectManifest while streaming each file through SHA-256. Read-only render
// and drift callers use it so large seed/source files are never retained in
// memory merely to compute the canonical source identity.
func CollectManifestHashes(pipelineDir string) (map[string]string, error) {
	manifest := make(map[string]string)
	err := walkManifestFiles(pipelineDir, func(path, relativePath string) error {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		hasher := sha256.New()
		_, copyErr := io.Copy(hasher, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		manifest[relativePath] = hex.EncodeToString(hasher.Sum(nil))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return manifest, nil
}

// CopyPipelineSourceForExecution copies the same source file set used by
// deployment manifests into an isolated execution directory. It hashes the
// bytes actually written, so callers can bind a confirmed working-tree plan to
// an immutable copy without retaining large seed files in memory.
func CopyPipelineSourceForExecution(pipelineDir, destDir string) (map[string]string, error) {
	manifest := make(map[string]string)
	err := walkManifestFiles(pipelineDir, func(path, relativePath string) error {
		target := filepath.Join(destDir, filepath.FromSlash(relativePath))
		relTarget, err := filepath.Rel(destDir, target)
		if err != nil || relTarget == ".." || strings.HasPrefix(relTarget, ".."+string(filepath.Separator)) {
			return fmt.Errorf("snapshot: invalid source path %q", relativePath)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}

		source, err := os.Open(path)
		if err != nil {
			return err
		}
		info, err := source.Stat()
		if err != nil {
			_ = source.Close()
			return err
		}
		destination, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			_ = source.Close()
			return err
		}

		hasher := sha256.New()
		_, copyErr := io.Copy(io.MultiWriter(destination, hasher), source)
		closeDestinationErr := destination.Close()
		closeSourceErr := source.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeDestinationErr != nil {
			return closeDestinationErr
		}
		if closeSourceErr != nil {
			return closeSourceErr
		}
		manifest[relativePath] = hex.EncodeToString(hasher.Sum(nil))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(destDir, ".git"), 0o755); err != nil {
		return nil, err
	}
	return manifest, nil
}

// Deploy snapshots the pipeline directory. When the content is identical to
// the latest snapshot it is a no-op and returns that snapshot with
// created=false.
func (s *Store) Deploy(ctx context.Context, pipelineUUID, pipelineDir, createdBy string) (Snapshot, bool, error) {
	return s.DeployReviewedWithDependencies(ctx, pipelineUUID, pipelineDir, createdBy, "", EmptyDependencyManifest())
}

// DeployReviewed snapshots the saved pipeline directory only if its exact
// source identity still matches the reviewed Merkle root. An empty expected
// root preserves the CLI/internal deploy contract.
func (s *Store) DeployReviewed(ctx context.Context, pipelineUUID, pipelineDir, createdBy, expectedRoot string) (Snapshot, bool, error) {
	return s.DeployReviewedWithDependencies(
		ctx, pipelineUUID, pipelineDir, createdBy, expectedRoot, EmptyDependencyManifest(),
	)
}

// DeployReviewedWithDependencies persists source and the reviewed URI-owner
// manifest as one immutable deployment. An ownership change creates a new
// deployment even when the pipeline's source Merkle root is unchanged.
func (s *Store) DeployReviewedWithDependencies(
	ctx context.Context,
	pipelineUUID string,
	pipelineDir string,
	createdBy string,
	expectedRoot string,
	dependencyManifest DependencyManifest,
) (Snapshot, bool, error) {
	if strings.TrimSpace(pipelineUUID) == "" {
		return Snapshot{}, false, errors.New("snapshot: pipeline UUID is required")
	}
	dependencyManifest, err := normalizeDependencyManifest(pipelineUUID, dependencyManifest)
	if err != nil {
		return Snapshot{}, false, err
	}
	manifest, contents, err := CollectManifest(pipelineDir)
	if err != nil {
		return Snapshot{}, false, err
	}
	if len(manifest) == 0 {
		return Snapshot{}, false, fmt.Errorf("snapshot: no files found under %s", pipelineDir)
	}
	root := merkleRoot(manifest)
	expectedRoot = strings.TrimSpace(expectedRoot)
	if expectedRoot != "" && root != expectedRoot {
		return Snapshot{}, false, &SourceChangedError{Expected: expectedRoot, Actual: root}
	}

	latest, err := s.Latest(ctx, pipelineUUID)
	if err != nil {
		return Snapshot{}, false, err
	}
	if latest != nil && latest.MerkleRoot == root && dependencyManifestsEqual(latest.DependencyManifest, dependencyManifest) {
		if _, err := s.Validate(ctx, latest.VersionID, pipelineUUID); err == nil {
			return *latest, false, nil
		}
	}

	gitSHA, gitDirty := gitState(pipelineDir)
	snapshot := Snapshot{
		VersionID:          uuid.NewString(),
		PipelineUUID:       pipelineUUID,
		MerkleRoot:         root,
		Manifest:           manifest,
		DependencyManifest: dependencyManifest,
		GitSHA:             gitSHA,
		GitDirty:           gitDirty,
		CreatedAt:          time.Now().UTC(),
		CreatedBy:          createdBy,
	}

	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return Snapshot{}, false, err
	}
	dependencyManifestJSON, err := json.Marshal(dependencyManifest)
	if err != nil {
		return Snapshot{}, false, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Snapshot{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(ordinal), 0) + 1 FROM renart_snapshots WHERE pipeline_id = ?`,
		pipelineUUID,
	).Scan(&snapshot.Ordinal); err != nil {
		return Snapshot{}, false, err
	}

	for hash, content := range contents {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO renart_blobs (hash, content) VALUES (?, ?) ON CONFLICT (hash) DO UPDATE SET content = excluded.content`,
			hash, content); err != nil {
			return Snapshot{}, false, err
		}
	}

	dirty := 0
	if snapshot.GitDirty {
		dirty = 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO renart_snapshots (version_id, pipeline_id, ordinal, merkle_root, manifest, dependency_manifest, git_sha, git_dirty, created_at, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snapshot.VersionID, snapshot.PipelineUUID, snapshot.Ordinal, snapshot.MerkleRoot, string(manifestJSON),
		string(dependencyManifestJSON), snapshot.GitSHA, dirty, snapshot.CreatedAt.Format(timeLayout), snapshot.CreatedBy); err != nil {
		return Snapshot{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return Snapshot{}, false, err
	}
	if _, err := s.Validate(ctx, snapshot.VersionID, pipelineUUID); err != nil {
		return Snapshot{}, false, fmt.Errorf("validate deployed snapshot %s: %w", snapshot.VersionID, err)
	}
	return snapshot, true, nil
}

func (s *Store) Latest(ctx context.Context, pipelineUUID string) (*Snapshot, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT version_id, pipeline_id, ordinal, merkle_root, manifest, dependency_manifest, git_sha, git_dirty, created_at, created_by
		FROM renart_snapshots WHERE pipeline_id = ?
		ORDER BY ordinal DESC LIMIT 1`, pipelineUUID)
	snapshot, err := scanSnapshot(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (s *Store) Get(ctx context.Context, versionID string) (Snapshot, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT version_id, pipeline_id, ordinal, merkle_root, manifest, dependency_manifest, git_sha, git_dirty, created_at, created_by
		FROM renart_snapshots WHERE version_id = ?`, versionID)
	return scanSnapshot(row)
}

// Validate verifies that a snapshot belongs to the expected pipeline and that
// every manifest entry resolves to the content-addressed blob it names. An
// empty pipelineUUID skips the ownership check.
func (s *Store) Validate(ctx context.Context, versionID, pipelineUUID string) (Snapshot, error) {
	snapshot, err := s.validatedSnapshot(ctx, versionID, pipelineUUID)
	if err != nil {
		return Snapshot{}, err
	}
	validatedHashes := make(map[string]struct{}, len(snapshot.Manifest))
	for _, relPath := range sortedManifestPaths(snapshot.Manifest) {
		expectedHash := snapshot.Manifest[relPath]
		if _, ok := validatedHashes[expectedHash]; ok {
			continue
		}
		if _, err := s.validatedBlobContent(ctx, snapshot.VersionID, relPath, expectedHash); err != nil {
			return Snapshot{}, err
		}
		validatedHashes[expectedHash] = struct{}{}
	}
	return snapshot, nil
}

// ValidateMetadata performs the cheap, blob-free part of snapshot validation:
// version lookup, pipeline ownership, manifest shape, and Merkle identity.
// Runtime materialization still verifies every referenced blob before use.
func (s *Store) ValidateMetadata(ctx context.Context, versionID, pipelineUUID string) (Snapshot, error) {
	return s.validatedSnapshot(ctx, versionID, pipelineUUID)
}

func (s *Store) List(ctx context.Context, pipelineUUID string) ([]Snapshot, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT version_id, pipeline_id, ordinal, merkle_root, manifest, dependency_manifest, git_sha, git_dirty, created_at, created_by
		FROM renart_snapshots WHERE pipeline_id = ?
		ORDER BY ordinal DESC`, pipelineUUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	snapshots := make([]Snapshot, 0)
	for rows.Next() {
		snapshot, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, rows.Err()
}

// Prune removes old deployments only when they are outside the per-pipeline
// floor and unreferenced by schedules, retained runs, or pending completion
// evidence. The latest deployment is protected explicitly even when the floor
// is configured as zero. Orphaned content-addressed blobs are removed in the
// same transaction after snapshot metadata changes.
func (s *Store) Prune(ctx context.Context, policy RetentionPolicy) (PruneResult, error) {
	if s == nil || s.db == nil {
		return PruneResult{}, errors.New("snapshot store is not initialized")
	}
	if policy.OlderThan.IsZero() {
		return PruneResult{}, errors.New("snapshot retention cutoff is required")
	}
	if policy.MinimumPerPipeline < 0 {
		return PruneResult{}, errors.New("snapshot retention minimum per pipeline cannot be negative")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PruneResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	deletedSnapshots, err := tx.ExecContext(ctx, `
		WITH ranked_snapshots AS (
			SELECT version_id,
			       pipeline_id,
			       ordinal,
			       created_at,
			       ROW_NUMBER() OVER (
				   PARTITION BY pipeline_id
				   ORDER BY ordinal DESC, version_id DESC
			       ) AS retention_rank,
			       MAX(ordinal) OVER (PARTITION BY pipeline_id) AS latest_ordinal
			FROM renart_snapshots
		),
		retention_candidates AS (
			SELECT snapshot.version_id
			FROM ranked_snapshots AS snapshot
			WHERE snapshot.created_at < ?
			  AND snapshot.retention_rank > ?
			  AND snapshot.ordinal < snapshot.latest_ordinal
			  AND NOT EXISTS (
				  SELECT 1 FROM renart_schedules AS schedule
				  WHERE schedule.snapshot_version_id = snapshot.version_id
			  )
			  AND NOT EXISTS (
				  SELECT 1 FROM pipeline_runs AS run
				  WHERE run.snapshot_version_id = snapshot.version_id
			  )
			  AND NOT EXISTS (
				  SELECT 1
				  FROM pipeline_run_plans AS plan,
				       json_each(plan.body, '$.prerequisites') AS prerequisite
				  WHERE json_extract(prerequisite.value, '$.producer_snapshot_version_id') = snapshot.version_id
			  )
			  AND NOT EXISTS (
				  SELECT 1 FROM renart_completion_outbox AS outbox
				  WHERE json_extract(outbox.body, '$.event.snapshot_version_id') = snapshot.version_id
			  )
		)
		DELETE FROM renart_snapshots
		WHERE version_id IN (SELECT version_id FROM retention_candidates)`,
		policy.OlderThan.UTC().Format(timeLayout),
		policy.MinimumPerPipeline,
	)
	if err != nil {
		return PruneResult{}, fmt.Errorf("prune snapshots: %w", err)
	}
	result := PruneResult{}
	if result.Snapshots, err = deletedSnapshots.RowsAffected(); err != nil {
		return PruneResult{}, fmt.Errorf("count pruned snapshots: %w", err)
	}

	deletedBlobs, err := tx.ExecContext(ctx, `
		DELETE FROM renart_blobs
		WHERE NOT EXISTS (
			SELECT 1
			FROM renart_snapshots AS snapshot, json_each(snapshot.manifest) AS entry
			WHERE entry.value = renart_blobs.hash
		)`)
	if err != nil {
		return PruneResult{}, fmt.Errorf("prune snapshot blobs: %w", err)
	}
	if result.Blobs, err = deletedBlobs.RowsAffected(); err != nil {
		return PruneResult{}, fmt.Errorf("count pruned snapshot blobs: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return PruneResult{}, fmt.Errorf("commit snapshot retention: %w", err)
	}
	return result, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSnapshot(row rowScanner) (Snapshot, error) {
	var snapshot Snapshot
	var manifestJSON, dependencyManifestJSON, createdAt string
	var gitSHA, createdBy sql.NullString
	var gitDirty sql.NullInt64
	if err := row.Scan(&snapshot.VersionID, &snapshot.PipelineUUID, &snapshot.Ordinal, &snapshot.MerkleRoot,
		&manifestJSON, &dependencyManifestJSON, &gitSHA, &gitDirty, &createdAt, &createdBy); err != nil {
		return Snapshot{}, err
	}
	if err := json.Unmarshal([]byte(manifestJSON), &snapshot.Manifest); err != nil {
		return Snapshot{}, err
	}
	if err := json.Unmarshal([]byte(dependencyManifestJSON), &snapshot.DependencyManifest); err != nil {
		return Snapshot{}, err
	}
	normalized, err := normalizeDependencyManifest(snapshot.PipelineUUID, snapshot.DependencyManifest)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.DependencyManifest = normalized
	snapshot.GitSHA = gitSHA.String
	snapshot.GitDirty = gitDirty.Int64 != 0
	snapshot.CreatedBy = createdBy.String
	if parsed, err := time.Parse(timeLayout, createdAt); err == nil {
		snapshot.CreatedAt = parsed
	}
	return snapshot, nil
}

// BlobContent returns the content of one stored file.
func (s *Store) BlobContent(ctx context.Context, hash string) ([]byte, error) {
	var content []byte
	err := s.db.QueryRowContext(ctx, `SELECT content FROM renart_blobs WHERE hash = ?`, hash).Scan(&content)
	return content, err
}

// CompareFile returns the exact saved-working-tree and deployment contents for
// one canonical manifest path. Large and binary files remain identifiable but
// are not copied into the JSON response.
func (s *Store) CompareFile(
	ctx context.Context,
	pipelineUUID string,
	pipelineDir string,
	versionID string,
	relPath string,
) (FileComparison, error) {
	relPath = strings.TrimSpace(relPath)
	if err := validateManifestPath(relPath); err != nil {
		return FileComparison{}, err
	}
	comparison := FileComparison{Path: relPath}

	var deployed *Snapshot
	var err error
	if strings.TrimSpace(versionID) != "" {
		item, loadErr := s.ValidateMetadata(ctx, versionID, pipelineUUID)
		if loadErr != nil {
			return FileComparison{}, loadErr
		}
		deployed = &item
	} else {
		deployed, err = s.Latest(ctx, pipelineUUID)
		if err != nil {
			return FileComparison{}, err
		}
	}
	if deployed != nil {
		if hash, ok := deployed.Manifest[relPath]; ok {
			comparison.BeforeExists = true
			var size int64
			if err := s.db.QueryRowContext(ctx, `SELECT length(content) FROM renart_blobs WHERE hash = ?`, hash).Scan(&size); err != nil {
				return FileComparison{}, err
			}
			if size > maxFileComparisonBytes {
				comparison.TooLarge = true
			} else {
				content, readErr := s.validatedBlobContent(ctx, deployed.VersionID, relPath, hash)
				if readErr != nil {
					return FileComparison{}, readErr
				}
				comparison.Binary = strings.Contains(string(content), "\x00")
				if !comparison.Binary {
					comparison.Before = string(content)
				}
			}
		}
	}

	workingPath := filepath.Join(pipelineDir, filepath.FromSlash(relPath))
	if err := ensureMaterializeTarget(filepath.Clean(pipelineDir), workingPath); err != nil {
		return FileComparison{}, err
	}
	info, statErr := os.Stat(workingPath)
	if statErr == nil {
		if !info.Mode().IsRegular() {
			return FileComparison{}, fmt.Errorf("snapshot: comparison path %q is not a regular file", relPath)
		}
		comparison.AfterExists = true
		if info.Size() > maxFileComparisonBytes {
			comparison.TooLarge = true
		} else {
			content, readErr := os.ReadFile(workingPath)
			if readErr != nil {
				return FileComparison{}, readErr
			}
			comparison.Binary = comparison.Binary || strings.Contains(string(content), "\x00")
			if !comparison.Binary {
				comparison.After = string(content)
			}
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return FileComparison{}, statErr
	}

	switch {
	case !comparison.BeforeExists && comparison.AfterExists:
		comparison.Status = "added"
	case comparison.BeforeExists && !comparison.AfterExists:
		comparison.Status = "removed"
	case comparison.BeforeExists && comparison.AfterExists && comparison.Before == comparison.After && !comparison.Binary && !comparison.TooLarge:
		comparison.Status = "unchanged"
	default:
		comparison.Status = "changed"
	}
	return comparison, nil
}

// Materialize writes a snapshot's files into destDir. The destination must be
// absolute so an absent path can never turn a materialization into writes in
// the server's current working directory.
func (s *Store) Materialize(ctx context.Context, versionID, destDir string) error {
	return s.materialize(ctx, versionID, "", destDir)
}

func (s *Store) materialize(ctx context.Context, versionID, pipelineUUID, destDir string) error {
	destDir, err := validateMaterializeDestination(destDir)
	if err != nil {
		return err
	}
	snapshot, err := s.validatedSnapshot(ctx, versionID, pipelineUUID)
	if err != nil {
		return err
	}
	for _, relPath := range sortedManifestPaths(snapshot.Manifest) {
		expectedHash := snapshot.Manifest[relPath]
		content, err := s.validatedBlobContent(ctx, snapshot.VersionID, relPath, expectedHash)
		if err != nil {
			return err
		}
		target := filepath.Join(destDir, filepath.FromSlash(relPath))
		if err := ensureMaterializeTarget(destDir, target); err != nil {
			return fmt.Errorf("snapshot %s: manifest path %q: %w", snapshot.VersionID, relPath, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) validatedSnapshot(ctx context.Context, versionID, pipelineUUID string) (Snapshot, error) {
	versionID = strings.TrimSpace(versionID)
	if versionID == "" {
		return Snapshot{}, errors.New("snapshot: version ID is required")
	}
	snapshot, err := s.Get(ctx, versionID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("snapshot %s: load metadata: %w", versionID, err)
	}
	if expected := strings.TrimSpace(pipelineUUID); expected != "" && snapshot.PipelineUUID != expected {
		return Snapshot{}, fmt.Errorf("snapshot %s belongs to pipeline %s, not %s", versionID, snapshot.PipelineUUID, expected)
	}
	if len(snapshot.Manifest) == 0 {
		return Snapshot{}, fmt.Errorf("snapshot %s: manifest is empty", versionID)
	}
	if actualRoot := merkleRoot(snapshot.Manifest); actualRoot != snapshot.MerkleRoot {
		return Snapshot{}, fmt.Errorf("snapshot %s: manifest root mismatch: expected %s, got %s", versionID, snapshot.MerkleRoot, actualRoot)
	}
	if _, err := normalizeDependencyManifest(snapshot.PipelineUUID, snapshot.DependencyManifest); err != nil {
		return Snapshot{}, fmt.Errorf("snapshot %s: %w", versionID, err)
	}

	for relPath := range snapshot.Manifest {
		if err := validateManifestPath(relPath); err != nil {
			return Snapshot{}, fmt.Errorf("snapshot %s: %w", versionID, err)
		}
	}
	return snapshot, nil
}

func normalizeDependencyManifest(pipelineUUID string, manifest DependencyManifest) (DependencyManifest, error) {
	pipelineUUID = strings.TrimSpace(pipelineUUID)
	if manifest.Version != DependencyManifestVersion {
		return DependencyManifest{}, fmt.Errorf(
			"dependency manifest version must be %d", DependencyManifestVersion,
		)
	}
	result := DependencyManifest{
		Version:      DependencyManifestVersion,
		Dependencies: make([]DependencyManifestItem, 0, len(manifest.Dependencies)),
	}
	seen := make(map[string]struct{}, len(manifest.Dependencies))
	for index, item := range manifest.Dependencies {
		item.ConsumerAssetID = strings.TrimSpace(item.ConsumerAssetID)
		item.URI = strings.TrimSpace(item.URI)
		item.Mode = strings.ToLower(strings.TrimSpace(item.Mode))
		item.ProducerPipelineUUID = strings.TrimSpace(item.ProducerPipelineUUID)
		item.ProducerAssetURI = strings.TrimSpace(item.ProducerAssetURI)
		consumerPipelineUUID, _, ok := identity.SplitAssetID(item.ConsumerAssetID)
		if !ok || consumerPipelineUUID != pipelineUUID {
			return DependencyManifest{}, fmt.Errorf(
				"dependency manifest item %d has a consumer outside pipeline %s", index, pipelineUUID,
			)
		}
		if item.URI == "" {
			return DependencyManifest{}, fmt.Errorf("dependency manifest item %d has an empty URI", index)
		}
		if item.Mode != "full" && item.Mode != "symbolic" {
			return DependencyManifest{}, fmt.Errorf("dependency manifest item %d has invalid mode %q", index, item.Mode)
		}
		if item.Mode == "full" && item.ProducerPipelineUUID == "" {
			return DependencyManifest{}, fmt.Errorf("full dependency manifest item %d has no producer pipeline", index)
		}
		if item.ProducerPipelineUUID == "" {
			if item.ProducerAssetURI != "" {
				return DependencyManifest{}, fmt.Errorf("dependency manifest item %d has a producer URI without a producer pipeline", index)
			}
		} else if item.ProducerAssetURI != item.URI {
			return DependencyManifest{}, fmt.Errorf("dependency manifest item %d does not bind its exact producer URI", index)
		}
		key := item.ConsumerAssetID + "\x00" + item.URI + "\x00" + item.Mode
		if _, duplicate := seen[key]; duplicate {
			return DependencyManifest{}, fmt.Errorf("dependency manifest item %d is duplicated", index)
		}
		seen[key] = struct{}{}
		result.Dependencies = append(result.Dependencies, item)
	}
	sort.Slice(result.Dependencies, func(i, j int) bool {
		left, right := result.Dependencies[i], result.Dependencies[j]
		if left.ConsumerAssetID != right.ConsumerAssetID {
			return left.ConsumerAssetID < right.ConsumerAssetID
		}
		if left.URI != right.URI {
			return left.URI < right.URI
		}
		if left.Mode != right.Mode {
			return left.Mode < right.Mode
		}
		return left.ProducerPipelineUUID < right.ProducerPipelineUUID
	})
	return result, nil
}

func dependencyManifestsEqual(left, right DependencyManifest) bool {
	if left.Version != right.Version || len(left.Dependencies) != len(right.Dependencies) {
		return false
	}
	for index := range left.Dependencies {
		if left.Dependencies[index] != right.Dependencies[index] {
			return false
		}
	}
	return true
}

func (s *Store) validatedBlobContent(ctx context.Context, versionID, relPath, expectedHash string) ([]byte, error) {
	content, err := s.BlobContent(ctx, expectedHash)
	if err != nil {
		return nil, fmt.Errorf("snapshot %s: blob %s for %s: %w", versionID, expectedHash, relPath, err)
	}
	if actualHash := hashBytes(content); actualHash != expectedHash {
		return nil, fmt.Errorf("snapshot %s: blob hash mismatch for %s: expected %s, got %s", versionID, relPath, expectedHash, actualHash)
	}
	return content, nil
}

func sortedManifestPaths(manifest map[string]string) []string {
	paths := make([]string, 0, len(manifest))
	for relPath := range manifest {
		paths = append(paths, relPath)
	}
	sort.Strings(paths)
	return paths
}

func validateMaterializeDestination(destDir string) (string, error) {
	if strings.TrimSpace(destDir) == "" {
		return "", errors.New("snapshot: materialization destination is required")
	}
	if !filepath.IsAbs(destDir) {
		return "", fmt.Errorf("snapshot: materialization destination must be absolute: %q", destDir)
	}
	return filepath.Clean(destDir), nil
}

func ensureMaterializeTarget(destDir, target string) error {
	relPath, err := filepath.Rel(destDir, target)
	if err != nil {
		return fmt.Errorf("resolve destination: %w", err)
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return errors.New("escapes destination")
	}
	return nil
}

func validateManifestPath(relPath string) error {
	if relPath == "" || strings.Contains(relPath, `\`) {
		return fmt.Errorf("invalid manifest path %q", relPath)
	}
	platformPath := filepath.FromSlash(relPath)
	cleaned := filepath.Clean(platformPath)
	if filepath.IsAbs(platformPath) || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("manifest path %q escapes destination", relPath)
	}
	if filepath.ToSlash(cleaned) != relPath {
		return fmt.Errorf("manifest path %q is not canonical", relPath)
	}
	return nil
}

// MaterializeForExecution writes a snapshot into destDir and prepares the
// directory for the executor: Bruin's ingestr and Python operators locate
// the repo root by walking up to a `.git` entry (existence is all that is
// checked — no git commands run against it), and the temp directory lives
// outside the workspace repository, so a dummy `.git` directory is created
// at the root. Without it, ingestr assets fail with "no git repository
// found".
func (s *Store) MaterializeForExecution(ctx context.Context, versionID, destDir string) error {
	return s.materializeForExecution(ctx, versionID, "", destDir)
}

// MaterializeForPipelineExecution is the ownership-aware executor path used
// when a run names both its pipeline and deployment. It validates ownership,
// metadata, and each blob while writing each file, without a separate full
// validation pass or retaining the whole snapshot in memory.
func (s *Store) MaterializeForPipelineExecution(ctx context.Context, versionID, pipelineUUID, destDir string) error {
	return s.materializeForExecution(ctx, versionID, pipelineUUID, destDir)
}

func (s *Store) materializeForExecution(ctx context.Context, versionID, pipelineUUID, destDir string) error {
	if err := s.materialize(ctx, versionID, pipelineUUID, destDir); err != nil {
		return err
	}
	return os.MkdirAll(filepath.Join(destDir, ".git"), 0o755)
}

// Drift compares the working tree against the latest deployed snapshot.
func (s *Store) Drift(ctx context.Context, pipelineUUID, pipelineDir string) (DriftReport, error) {
	return s.drift(ctx, pipelineUUID, pipelineDir, nil)
}

// DriftWithDependencies includes URI-owner changes in the deployment identity
// shown by the UI, even when no file inside the consumer pipeline changed.
func (s *Store) DriftWithDependencies(
	ctx context.Context,
	pipelineUUID string,
	pipelineDir string,
	dependencyManifest DependencyManifest,
) (DriftReport, error) {
	normalized, err := normalizeDependencyManifest(pipelineUUID, dependencyManifest)
	if err != nil {
		return DriftReport{}, err
	}
	return s.drift(ctx, pipelineUUID, pipelineDir, &normalized)
}

func (s *Store) drift(
	ctx context.Context,
	pipelineUUID string,
	pipelineDir string,
	dependencyManifest *DependencyManifest,
) (DriftReport, error) {
	snapshots, err := s.List(ctx, pipelineUUID)
	if err != nil {
		return DriftReport{}, err
	}
	manifest, err := CollectManifestHashes(pipelineDir)
	if err != nil {
		return DriftReport{}, err
	}
	report := DriftReport{
		SnapshotCount: len(snapshots), SourceMerkle: merkleRoot(manifest),
		DependencyManifestInSync: dependencyManifest == nil,
	}
	if len(snapshots) == 0 {
		for path := range manifest {
			report.AddedFiles = append(report.AddedFiles, path)
		}
		sort.Strings(report.AddedFiles)
		return report, nil
	}
	latest := snapshots[0]
	if dependencyManifest != nil {
		report.DependencyManifestInSync = dependencyManifestsEqual(latest.DependencyManifest, *dependencyManifest)
	}
	report.HasSnapshot = true
	report.VersionID = latest.VersionID
	report.Ordinal = latest.Ordinal
	report.CreatedAt = latest.CreatedAt
	if _, err := s.Validate(ctx, latest.VersionID, pipelineUUID); err != nil {
		report.IntegrityError = err.Error()
	} else {
		report.Executable = true
	}

	if report.SourceMerkle == latest.MerkleRoot && report.DependencyManifestInSync {
		report.InSync = true
		return report, nil
	}

	for path, hash := range manifest {
		deployedHash, ok := latest.Manifest[path]
		switch {
		case !ok:
			report.AddedFiles = append(report.AddedFiles, path)
		case deployedHash != hash:
			report.ChangedFiles = append(report.ChangedFiles, path)
		}
	}
	for path := range latest.Manifest {
		if _, ok := manifest[path]; !ok {
			report.RemovedFiles = append(report.RemovedFiles, path)
		}
	}
	sort.Strings(report.AddedFiles)
	sort.Strings(report.ChangedFiles)
	sort.Strings(report.RemovedFiles)
	return report, nil
}

// gitState best-effort records the commit the deploy was cut from.
func gitState(dir string) (sha string, dirty bool) {
	revParse := exec.Command("git", "rev-parse", "HEAD")
	revParse.Dir = dir
	if out, err := revParse.Output(); err == nil {
		sha = strings.TrimSpace(string(out))
	}
	status := exec.Command("git", "status", "--porcelain")
	status.Dir = dir
	if out, err := status.Output(); err == nil {
		dirty = strings.TrimSpace(string(out)) != ""
	}
	return sha, dirty
}
