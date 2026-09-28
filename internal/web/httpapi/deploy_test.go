package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"renart/internal/web/httpapi"
	"renart/internal/web/scheduler"
	"renart/internal/web/snapshot"
)

func TestReviewedDeployRejectsSourceChangedAfterPlan(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schedulerStore, err := scheduler.OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = schedulerStore.Close() })
	snapshotStore := snapshot.NewStore(schedulerStore.DB())
	pipelineDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(pipelineDir, "pipeline.yml"), []byte("id: pipeline\n"), 0o644))
	manifest, err := snapshot.CollectManifestHashes(pipelineDir)
	require.NoError(t, err)
	reviewedRoot := snapshot.ManifestRoot(manifest)
	require.NoError(t, os.WriteFile(filepath.Join(pipelineDir, "pipeline.yml"), []byte("id: pipeline\nname: changed\n"), 0o644))

	router := chi.NewRouter()
	httpapi.RegisterDeployRoutes(router, &httpapi.DeployAPI{
		Snapshots: snapshotStore,
		ResolvePipeline: func(string) (string, string, bool) {
			return "pipeline", pipelineDir, true
		},
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/pipelines/pipeline/deploy", strings.NewReader(
		`{"expected_source_merkle":"`+reviewedRoot+`"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusConflict, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"code":"deployment_source_changed"`)
	deployments, err := snapshotStore.List(ctx, "pipeline")
	require.NoError(t, err)
	assert.Empty(t, deployments)
}

func TestReviewedDeployReturnsStableOrdinal(t *testing.T) {
	t.Parallel()
	schedulerStore, err := scheduler.OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = schedulerStore.Close() })
	snapshotStore := snapshot.NewStore(schedulerStore.DB())
	pipelineDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(pipelineDir, "pipeline.yml"), []byte("id: pipeline\n"), 0o644))
	manifest, err := snapshot.CollectManifestHashes(pipelineDir)
	require.NoError(t, err)
	reviewedRoot := snapshot.ManifestRoot(manifest)

	router := chi.NewRouter()
	httpapi.RegisterDeployRoutes(router, &httpapi.DeployAPI{
		Snapshots: snapshotStore,
		ResolvePipeline: func(string) (string, string, bool) {
			return "pipeline", pipelineDir, true
		},
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/pipelines/pipeline/deploy", strings.NewReader(
		`{"expected_source_merkle":"`+reviewedRoot+`"}`,
	))
	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"ordinal":1`)
}

type fakeDeploySchedules struct {
	ownerErr   error
	promoteErr error
	requests   []scheduler.PromoteEnvSchedulesRequest
}

func (f *fakeDeploySchedules) RequireOwner() error { return f.ownerErr }

func (f *fakeDeploySchedules) PromoteEnvSchedules(_ context.Context, _ string, req scheduler.PromoteEnvSchedulesRequest) ([]scheduler.EnvSchedule, error) {
	f.requests = append(f.requests, req)
	if f.promoteErr != nil {
		return nil, f.promoteErr
	}
	return []scheduler.EnvSchedule{{Environment: "production", SnapshotVersionID: req.SnapshotVersionID}}, nil
}

func deployWithSchedules(t *testing.T, schedules *fakeDeploySchedules) (*httptest.ResponseRecorder, *snapshot.Store) {
	t.Helper()
	schedulerStore, err := scheduler.OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = schedulerStore.Close() })
	snapshotStore := snapshot.NewStore(schedulerStore.DB())
	pipelineDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(pipelineDir, "pipeline.yml"), []byte("id: pipeline\n"), 0o644))

	router := chi.NewRouter()
	httpapi.RegisterDeployRoutes(router, &httpapi.DeployAPI{
		Snapshots: snapshotStore,
		ResolvePipeline: func(string) (string, string, bool) {
			return "pipeline", pipelineDir, true
		},
		Schedules: schedules,
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/pipelines/pipeline/deploy", strings.NewReader(
		`{"schedules":[{"environment":"production","expected_snapshot_version_id":""}]}`,
	))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	return recorder, snapshotStore
}

func TestDeployUpdatesSelectedSchedules(t *testing.T) {
	t.Parallel()
	schedules := &fakeDeploySchedules{}
	recorder, snapshotStore := deployWithSchedules(t, schedules)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	deployments, err := snapshotStore.List(context.Background(), "pipeline")
	require.NoError(t, err)
	require.Len(t, deployments, 1)
	require.Len(t, schedules.requests, 1)
	assert.Equal(t, deployments[0].VersionID, schedules.requests[0].SnapshotVersionID)
	assert.Equal(t, "production", schedules.requests[0].Schedules[0].Environment)
	assert.Contains(t, recorder.Body.String(), `"schedules":[`)
	assert.NotContains(t, recorder.Body.String(), `"schedule_error"`)
}

func TestDeployKeepsTheDeploymentWhenSchedulesCannotMove(t *testing.T) {
	t.Parallel()
	recorder, snapshotStore := deployWithSchedules(t, &fakeDeploySchedules{
		promoteErr: errors.New("schedule production changed after deployment review"),
	})

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	assert.Contains(t, recorder.Body.String(), `"schedule_error":"schedule production changed after deployment review"`)
	deployments, err := snapshotStore.List(context.Background(), "pipeline")
	require.NoError(t, err)
	assert.Len(t, deployments, 1)
}

func TestDeployWithSchedulesRequiresTheSchedulerOwner(t *testing.T) {
	t.Parallel()
	schedules := &fakeDeploySchedules{ownerErr: scheduler.ErrSchedulerNotOwner}
	recorder, snapshotStore := deployWithSchedules(t, schedules)

	assert.Equal(t, http.StatusConflict, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"code":"scheduler_not_owner"`)
	deployments, err := snapshotStore.List(context.Background(), "pipeline")
	require.NoError(t, err)
	assert.Empty(t, deployments)
	assert.Empty(t, schedules.requests)
}
