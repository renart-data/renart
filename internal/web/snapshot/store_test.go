package snapshot_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bruingit "github.com/bruin-data/bruin/pkg/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"renart/internal/web/scheduler"
	"renart/internal/web/snapshot"
)

func openTestStoreWithDB(t *testing.T) (*snapshot.Store, *sql.DB) {
	t.Helper()
	schedStore, err := scheduler.OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = schedStore.Close() })
	return snapshot.NewStore(schedStore.DB()), schedStore.DB()
}

func openTestStore(t *testing.T) *snapshot.Store {
	t.Helper()
	store, _ := openTestStoreWithDB(t)
	return store
}

func writePipelineDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for relPath, content := range files {
		target := filepath.Join(dir, filepath.FromSlash(relPath))
		require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
		require.NoError(t, os.WriteFile(target, []byte(content), 0o644))
	}
	return dir
}

func TestDeployAndMaterializeRoundTrip(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	ctx := context.Background()
	dir := writePipelineDir(t, map[string]string{
		"pipeline.yml":        "id: p\nname: test\n",
		"assets/a.sql":        "select 1",
		"assets/nested/b.sql": "select 2",
	})

	deployed, created, err := store.Deploy(ctx, "p", dir, "tester")
	require.NoError(t, err)
	assert.True(t, created)
	assert.EqualValues(t, 1, deployed.Ordinal)
	assert.Len(t, deployed.Manifest, 3)

	dest := t.TempDir()
	require.NoError(t, store.Materialize(ctx, deployed.VersionID, dest))
	content, err := os.ReadFile(filepath.Join(dest, "assets", "nested", "b.sql"))
	require.NoError(t, err)
	assert.Equal(t, "select 2", string(content))
}

func TestDeployDeduplicatesIdenticalContent(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	ctx := context.Background()
	dir := writePipelineDir(t, map[string]string{"pipeline.yml": "id: p\n", "assets/a.sql": "select 1"})

	first, created, err := store.Deploy(ctx, "p", dir, "")
	require.NoError(t, err)
	require.True(t, created)

	second, created, err := store.Deploy(ctx, "p", dir, "")
	require.NoError(t, err)
	assert.False(t, created, "identical content should not create a new snapshot")
	assert.Equal(t, first.VersionID, second.VersionID)
	assert.Equal(t, first.Ordinal, second.Ordinal)

	// An edit creates a new version; the old one stays materializable.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "assets", "a.sql"), []byte("select 1, 2"), 0o644))
	third, created, err := store.Deploy(ctx, "p", dir, "")
	require.NoError(t, err)
	assert.True(t, created)
	assert.NotEqual(t, first.VersionID, third.VersionID)
	assert.EqualValues(t, 2, third.Ordinal)

	dest := t.TempDir()
	require.NoError(t, store.Materialize(ctx, first.VersionID, dest))
	content, err := os.ReadFile(filepath.Join(dest, "assets", "a.sql"))
	require.NoError(t, err)
	assert.Equal(t, "select 1", string(content))
}

func TestDeployDependencyManifestRoundTripsAndChangesDeploymentIdentity(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	ctx := context.Background()
	dir := writePipelineDir(t, map[string]string{
		"pipeline.yml": "id: consumer\n",
		"assets/a.sql": "select 1",
	})
	firstManifest := snapshot.DependencyManifest{
		Version: snapshot.DependencyManifestVersion,
		Dependencies: []snapshot.DependencyManifestItem{{
			ConsumerAssetID: "consumer:analytics.report",
			URI:             "duckdb://warehouse/raw/orders", Mode: "full",
			ProducerPipelineUUID: "producer-a",
			ProducerAssetURI:     "duckdb://warehouse/raw/orders",
		}},
	}
	first, created, err := store.DeployReviewedWithDependencies(
		ctx, "consumer", dir, "tester", "", firstManifest,
	)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, firstManifest, first.DependencyManifest)

	same, created, err := store.DeployReviewedWithDependencies(
		ctx, "consumer", dir, "tester", "", firstManifest,
	)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, first.VersionID, same.VersionID)

	secondManifest := firstManifest
	secondManifest.Dependencies = append([]snapshot.DependencyManifestItem(nil), firstManifest.Dependencies...)
	secondManifest.Dependencies[0].ProducerPipelineUUID = "producer-b"
	second, created, err := store.DeployReviewedWithDependencies(
		ctx, "consumer", dir, "tester", "", secondManifest,
	)
	require.NoError(t, err)
	assert.True(t, created, "URI ownership is part of immutable deployment identity")
	assert.NotEqual(t, first.VersionID, second.VersionID)
	assert.EqualValues(t, 2, second.Ordinal)
	assert.Equal(t, secondManifest, second.DependencyManifest)

	reloaded, err := store.Get(ctx, second.VersionID)
	require.NoError(t, err)
	assert.Equal(t, secondManifest, reloaded.DependencyManifest)
	drift, err := store.DriftWithDependencies(ctx, "consumer", dir, firstManifest)
	require.NoError(t, err)
	assert.False(t, drift.InSync)
	assert.False(t, drift.DependencyManifestInSync)
	assert.Empty(t, drift.ChangedFiles)
}

func TestDeployRejectsInvalidFullDependencyManifest(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	dir := writePipelineDir(t, map[string]string{
		"pipeline.yml": "id: consumer\n",
		"assets/a.sql": "select 1",
	})
	_, created, err := store.DeployReviewedWithDependencies(
		context.Background(), "consumer", dir, "tester", "",
		snapshot.DependencyManifest{
			Version: snapshot.DependencyManifestVersion,
			Dependencies: []snapshot.DependencyManifestItem{{
				ConsumerAssetID: "consumer:analytics.report",
				URI:             "duckdb://warehouse/raw/orders", Mode: "full",
			}},
		},
	)
	require.ErrorContains(t, err, "has no producer pipeline")
	assert.False(t, created)
}

func TestDeployReviewedRejectsSavedSourceChangedAfterReview(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	ctx := context.Background()
	dir := writePipelineDir(t, map[string]string{
		"pipeline.yml": "id: p\n",
		"assets/a.sql": "select 1",
	})
	manifest, err := snapshot.CollectManifestHashes(dir)
	require.NoError(t, err)
	reviewedRoot := snapshot.ManifestRoot(manifest)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "assets", "a.sql"), []byte("select 2"), 0o644))
	_, created, err := store.DeployReviewed(ctx, "p", dir, "tester", reviewedRoot)
	require.ErrorIs(t, err, snapshot.ErrSourceChanged)
	assert.False(t, created)

	deployments, listErr := store.List(ctx, "p")
	require.NoError(t, listErr)
	assert.Empty(t, deployments, "a stale review must not create a deployment")
}

func TestCompareFileReturnsExactDeploymentAndSavedContents(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	ctx := context.Background()
	dir := writePipelineDir(t, map[string]string{
		"pipeline.yml": "id: p\n",
		"assets/a.sql": "select 1\n",
	})
	deployed, _, err := store.Deploy(ctx, "p", dir, "tester")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "assets", "a.sql"), []byte("select 2\n"), 0o644))

	comparison, err := store.CompareFile(ctx, "p", dir, deployed.VersionID, "assets/a.sql")
	require.NoError(t, err)
	assert.Equal(t, "changed", comparison.Status)
	assert.Equal(t, "select 1\n", comparison.Before)
	assert.Equal(t, "select 2\n", comparison.After)
	assert.True(t, comparison.BeforeExists)
	assert.True(t, comparison.AfterExists)

	_, err = store.CompareFile(ctx, "p", dir, deployed.VersionID, "../outside.sql")
	require.ErrorContains(t, err, "escapes destination")
}

func TestLatestUsesDeploymentOrdinal(t *testing.T) {
	t.Parallel()
	store, db := openTestStoreWithDB(t)
	ctx := context.Background()
	createdAt := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	for ordinal, versionID := range []string{"version-a", "version-z"} {
		_, err := db.ExecContext(ctx, `
			INSERT INTO renart_snapshots
				(version_id, pipeline_id, ordinal, merkle_root, manifest, git_dirty, created_at)
			VALUES (?, 'pipeline', ?, 'root', '{}', 0, ?)`, versionID, ordinal+1, createdAt)
		require.NoError(t, err)
	}

	latest, err := store.Latest(ctx, "pipeline")
	require.NoError(t, err)
	require.NotNil(t, latest)
	assert.Equal(t, "version-z", latest.VersionID)
	assert.EqualValues(t, 2, latest.Ordinal)
}

func TestDeployRepairsCorruptContentInsteadOfReusingIt(t *testing.T) {
	t.Parallel()
	store, db := openTestStoreWithDB(t)
	ctx := context.Background()
	dir := writePipelineDir(t, map[string]string{
		"pipeline.yml": "id: p\n",
		"assets/a.sql": "select 1",
	})
	first, created, err := store.Deploy(ctx, "p", dir, "")
	require.NoError(t, err)
	require.True(t, created)
	hash := first.Manifest["assets/a.sql"]
	_, err = db.ExecContext(ctx, `UPDATE renart_blobs SET content = ? WHERE hash = ?`, []byte("corrupt"), hash)
	require.NoError(t, err)

	second, created, err := store.Deploy(ctx, "p", dir, "")
	require.NoError(t, err)
	assert.True(t, created, "an invalid deployment must not be returned as a no-op")
	assert.NotEqual(t, first.VersionID, second.VersionID)
	assert.Equal(t, first.MerkleRoot, second.MerkleRoot)
	_, err = store.Validate(ctx, second.VersionID, "p")
	require.NoError(t, err)
	_, err = store.Validate(ctx, first.VersionID, "p")
	require.NoError(t, err, "repairing a content-addressed blob also restores older manifests")
}

func TestDeploySkipsJunk(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	dir := writePipelineDir(t, map[string]string{
		"pipeline.yml":              "id: p\n",
		"assets/a.sql":              "select 1",
		"data.duckdb":               "binary",
		".renart/state.db":          "local",
		"__pycache__/x.cpython.pyc": "cache",
		"assets/__pycache__/y.pyc":  "cache",
		"logs/runs/foo/run.json":    "log",
		".git/objects/aa/bb":        "git",
		"shared/helpers.py":         "def x(): pass",
	})

	deployed, _, err := store.Deploy(context.Background(), "p", dir, "")
	require.NoError(t, err)
	paths := make([]string, 0, len(deployed.Manifest))
	for path := range deployed.Manifest {
		paths = append(paths, path)
	}
	assert.ElementsMatch(t, []string{"pipeline.yml", "assets/a.sql", "shared/helpers.py"}, paths)
}

func TestCollectManifestHashesMatchesDeployManifest(t *testing.T) {
	t.Parallel()
	dir := writePipelineDir(t, map[string]string{
		"pipeline.yml":             "id: p\n",
		"assets/a.sql":             "select 1",
		"assets/seed.csv":          "id,name\n1,Ada\n",
		"assets/nested/b.py":       "print('hello')\n",
		"assets/local.duckdb":      "not source",
		"assets/__pycache__/b.pyc": "not source",
	})

	contentManifest, _, err := snapshot.CollectManifest(dir)
	require.NoError(t, err)
	hashOnlyManifest, err := snapshot.CollectManifestHashes(dir)
	require.NoError(t, err)
	assert.Equal(t, contentManifest, hashOnlyManifest)
	assert.Equal(t, snapshot.ManifestRoot(contentManifest), snapshot.ManifestRoot(hashOnlyManifest))
}

func TestCopyPipelineSourceForExecutionCopiesCanonicalManifest(t *testing.T) {
	t.Parallel()
	dir := writePipelineDir(t, map[string]string{
		"pipeline.yml":             "id: p\n",
		"assets/a.sql":             "select 1",
		"assets/seed.csv":          "id,name\n1,Ada\n",
		"assets/local.duckdb":      "not source",
		"assets/__pycache__/a.pyc": "not source",
	})
	expected, err := snapshot.CollectManifestHashes(dir)
	require.NoError(t, err)

	dest := filepath.Join(t.TempDir(), "run")
	require.NoError(t, os.MkdirAll(dest, 0o755))
	copied, err := snapshot.CopyPipelineSourceForExecution(dir, dest)
	require.NoError(t, err)
	assert.Equal(t, expected, copied)
	assert.Equal(t, snapshot.ManifestRoot(expected), snapshot.ManifestRoot(copied))
	require.FileExists(t, filepath.Join(dest, "pipeline.yml"))
	require.FileExists(t, filepath.Join(dest, "assets", "seed.csv"))
	require.DirExists(t, filepath.Join(dest, ".git"))
	assert.NoFileExists(t, filepath.Join(dest, "assets", "local.duckdb"))
	assert.NoFileExists(t, filepath.Join(dest, "assets", "__pycache__", "a.pyc"))
}

func TestDriftReport(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	ctx := context.Background()
	dir := writePipelineDir(t, map[string]string{
		"pipeline.yml": "id: p\n",
		"assets/a.sql": "select 1",
		"assets/b.sql": "select 2",
	})

	report, err := store.Drift(ctx, "p", dir)
	require.NoError(t, err)
	assert.False(t, report.HasSnapshot)

	deployed, _, err := store.Deploy(ctx, "p", dir, "")
	require.NoError(t, err)

	report, err = store.Drift(ctx, "p", dir)
	require.NoError(t, err)
	assert.True(t, report.InSync)
	assert.True(t, report.Executable)
	assert.Empty(t, report.IntegrityError)
	assert.Equal(t, deployed.VersionID, report.VersionID)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "assets", "a.sql"), []byte("select 99"), 0o644))
	require.NoError(t, os.Remove(filepath.Join(dir, "assets", "b.sql")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "assets", "c.sql"), []byte("select 3"), 0o644))

	report, err = store.Drift(ctx, "p", dir)
	require.NoError(t, err)
	assert.False(t, report.InSync)
	assert.Equal(t, []string{"assets/a.sql"}, report.ChangedFiles)
	assert.Equal(t, []string{"assets/c.sql"}, report.AddedFiles)
	assert.Equal(t, []string{"assets/b.sql"}, report.RemovedFiles)
}

func TestMaterializeForExecutionSatisfiesRepoDiscovery(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	ctx := context.Background()
	dir := writePipelineDir(t, map[string]string{
		"pipeline.yml":           "id: p\n",
		"assets/games.asset.yml": "type: ingestr\n",
	})
	deployed, _, err := store.Deploy(ctx, "p", dir, "")
	require.NoError(t, err)

	dest := filepath.Join(t.TempDir(), "run")
	require.NoError(t, os.MkdirAll(dest, 0o755))
	require.NoError(t, store.MaterializeForExecution(ctx, deployed.VersionID, dest))

	// Bruin's ingestr/python operators walk up from the asset path looking
	// for a .git entry; the dummy directory must make the snapshot root
	// discoverable as the repo root.
	repo, err := bruingit.FindRepoFromPath(filepath.Join(dest, "assets"))
	require.NoError(t, err, "ingestr repo discovery must succeed inside a materialized snapshot")
	resolved, err := filepath.EvalSymlinks(repo.Path)
	require.NoError(t, err)
	expected, err := filepath.EvalSymlinks(dest)
	require.NoError(t, err)
	assert.Equal(t, expected, resolved)

	// The snapshot files themselves are intact next to the shim.
	_, err = os.Stat(filepath.Join(dest, "assets", "games.asset.yml"))
	require.NoError(t, err)
}

func TestMaterializeRejectsInvalidDestinationBeforeSnapshotLookup(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	for _, destDir := range []string{"", ".", filepath.Join("relative", "run")} {
		err := store.Materialize(context.Background(), "missing", destDir)
		require.ErrorContains(t, err, "materialization destination")
		assert.NotContains(t, err.Error(), "load metadata")
	}
}

func TestMaterializeForPipelineExecutionValidatesOwnershipBeforeWriting(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	ctx := context.Background()
	dir := writePipelineDir(t, map[string]string{"pipeline.yml": "id: p\n"})
	deployed, _, err := store.Deploy(ctx, "pipeline-a", dir, "")
	require.NoError(t, err)

	dest := filepath.Join(t.TempDir(), "run")
	err = store.MaterializeForPipelineExecution(ctx, deployed.VersionID, "pipeline-b", dest)
	require.ErrorContains(t, err, "belongs to pipeline pipeline-a")
	_, statErr := os.Stat(filepath.Join(dest, "pipeline.yml"))
	assert.ErrorIs(t, statErr, os.ErrNotExist)
	_, statErr = os.Stat(filepath.Join(dest, ".git"))
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestMaterializeRejectsEscapingPaths(t *testing.T) {
	t.Parallel()
	store, db := openTestStoreWithDB(t)
	ctx := context.Background()
	dir := writePipelineDir(t, map[string]string{"pipeline.yml": "id: p\n"})
	deployed, _, err := store.Deploy(ctx, "p", dir, "")
	require.NoError(t, err)

	// Forge a manifest entry that tries to escape the destination.
	_, err = db.ExecContext(ctx,
		`UPDATE renart_snapshots SET manifest = ? WHERE version_id = ?`,
		`{"../escape.txt": "deadbeef"}`, deployed.VersionID)
	require.NoError(t, err)
	err = store.Materialize(ctx, deployed.VersionID, t.TempDir())
	require.Error(t, err)
}

func TestValidateRejectsWrongPipelineAndCorruptBlob(t *testing.T) {
	t.Parallel()
	store, db := openTestStoreWithDB(t)
	ctx := context.Background()
	dir := writePipelineDir(t, map[string]string{
		"pipeline.yml": "id: p\n",
		"assets/a.sql": "select 1",
	})
	deployed, _, err := store.Deploy(ctx, "pipeline-a", dir, "")
	require.NoError(t, err)

	_, err = store.Validate(ctx, deployed.VersionID, "pipeline-b")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "belongs to pipeline pipeline-a")

	hash := deployed.Manifest["assets/a.sql"]
	_, err = db.ExecContext(ctx, `UPDATE renart_blobs SET content = ? WHERE hash = ?`, []byte("tampered"), hash)
	require.NoError(t, err)

	_, err = store.Validate(ctx, deployed.VersionID, "pipeline-a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blob hash mismatch")
	assert.Error(t, store.MaterializeForExecution(ctx, deployed.VersionID, t.TempDir()))

	report, err := store.Drift(ctx, "pipeline-a", dir)
	require.NoError(t, err)
	assert.True(t, report.InSync)
	assert.False(t, report.Executable)
	assert.Contains(t, report.IntegrityError, "blob hash mismatch")
}

func TestValidateRejectsManifestThatDoesNotMatchRecordedRoot(t *testing.T) {
	t.Parallel()
	store, db := openTestStoreWithDB(t)
	ctx := context.Background()
	dir := writePipelineDir(t, map[string]string{
		"pipeline.yml": "id: p\n",
		"assets/a.sql": "select 1",
	})
	deployed, _, err := store.Deploy(ctx, "pipeline-a", dir, "")
	require.NoError(t, err)
	pipelineHash := deployed.Manifest["pipeline.yml"]
	_, err = db.ExecContext(ctx, `UPDATE renart_snapshots SET manifest = ? WHERE version_id = ?`,
		`{"pipeline.yml":"`+pipelineHash+`"}`, deployed.VersionID)
	require.NoError(t, err)

	_, err = store.Validate(ctx, deployed.VersionID, "pipeline-a")
	require.ErrorContains(t, err, "manifest root mismatch")
}

func TestPruneProtectsLatestPinnedRunAndPendingCompletionDeployments(t *testing.T) {
	t.Parallel()
	store, db := openTestStoreWithDB(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(-1, 0, 0).Format(time.RFC3339Nano)
	for ordinal, versionID := range []string{
		"pinned", "run-referenced", "plan-prerequisite-referenced", "waiting-prerequisite-referenced", "completion-referenced", "deletable", "latest",
	} {
		hash := "hash-" + versionID
		_, err := db.ExecContext(ctx, `
			INSERT INTO renart_blobs (hash, content) VALUES (?, ?)`, hash, []byte(versionID))
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `
			INSERT INTO renart_snapshots (
				version_id, pipeline_id, ordinal, merkle_root, manifest, git_dirty, created_at
			) VALUES (?, 'pipeline', ?, ?, json_object('pipeline.yml', ?), 0, ?)`,
			versionID, ordinal+1, "root-"+versionID, hash, old,
		)
		require.NoError(t, err)
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO renart_schedules (
			pipeline_id, environment, snapshot_version_id, cron, timezone,
			catchup_policy, status, created_at, updated_at
		) VALUES ('pipeline', 'prod', 'pinned', '@daily', 'UTC', 'skip', 'active', ?, ?)`, old, old)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO pipeline_runs (
			id, pipeline_id, pipeline, environment, trigger, status, finished_at, snapshot_version_id
		) VALUES ('run', 'pipeline', 'pipeline', 'prod', 'manual', 'success', ?, 'run-referenced')`, old)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO pipeline_runs (
			id, pipeline_id, pipeline, environment, trigger, status, finished_at
		) VALUES ('plan-run', 'consumer', 'consumer', 'prod', 'manual', 'success', ?)`, old)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO pipeline_run_plans (run_id, version, body, created_at)
		VALUES ('plan-run', 3, ?, ?)`,
		`{"prerequisites":[{"producer_snapshot_version_id":"plan-prerequisite-referenced"}]}`,
		old,
	)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO schedule_occurrences (
			occurrence_key, pipeline_uuid, environment, interval_start, interval_end,
			status, prerequisite_plan, prerequisite_deadline, prerequisite_reason,
			created_at, updated_at
		) VALUES (?, 'consumer', 'prod', ?, ?, 'pending', ?, ?, 'waiting', ?, ?)`,
		strings.Repeat("a", 64), old, now.Format(time.RFC3339Nano),
		`{"prerequisites":[{"producer_snapshot_version_id":"waiting-prerequisite-referenced"}]}`,
		now.Add(time.Hour).Format(time.RFC3339Nano), old, old,
	)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO renart_completion_outbox (completion_id, version, body, enqueued_at)
		VALUES ('completion', 1, ?, ?)`,
		`{"version":1,"event":{"completion_id":"completion","run_id":"run","snapshot_version_id":"completion-referenced"}}`,
		old,
	)
	require.NoError(t, err)

	result, err := store.Prune(ctx, snapshot.RetentionPolicy{
		OlderThan: now.AddDate(0, 0, -90), MinimumPerPipeline: 0,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 1, result.Snapshots)
	assert.EqualValues(t, 1, result.Blobs)
	for _, versionID := range []string{"pinned", "run-referenced", "plan-prerequisite-referenced", "waiting-prerequisite-referenced", "completion-referenced", "latest"} {
		_, err := store.Get(ctx, versionID)
		require.NoError(t, err)
	}
	_, err = store.Get(ctx, "deletable")
	assert.ErrorIs(t, err, sql.ErrNoRows)
	var deletableBlobCount int
	require.NoError(t, db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM renart_blobs WHERE hash = 'hash-deletable'`).Scan(&deletableBlobCount))
	assert.Zero(t, deletableBlobCount)
}

func TestPruneKeepsPerPipelineFloorAndLatestDeployment(t *testing.T) {
	t.Parallel()
	store, db := openTestStoreWithDB(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(-1, 0, 0).Format(time.RFC3339Nano)
	for ordinal := 1; ordinal <= 4; ordinal++ {
		versionID := fmt.Sprintf("version-%d", ordinal)
		_, err := db.ExecContext(ctx, `
			INSERT INTO renart_snapshots (
				version_id, pipeline_id, ordinal, merkle_root, manifest, git_dirty, created_at
			) VALUES (?, 'pipeline', ?, ?, '{}', 0, ?)`, versionID, ordinal, versionID, old)
		require.NoError(t, err)
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO renart_snapshots (
			version_id, pipeline_id, ordinal, merkle_root, manifest, git_dirty, created_at
		) VALUES ('only', 'other', 1, 'only', '{}', 0, ?)`, old)
	require.NoError(t, err)

	result, err := store.Prune(ctx, snapshot.RetentionPolicy{
		OlderThan: now.AddDate(0, 0, -90), MinimumPerPipeline: 2,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 2, result.Snapshots)
	items, err := store.List(ctx, "pipeline")
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, []string{"version-4", "version-3"}, []string{items[0].VersionID, items[1].VersionID})
	_, err = store.Get(ctx, "only")
	require.NoError(t, err, "the sole and therefore latest deployment is protected")
}
