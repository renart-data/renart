package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	webapi "renart/internal/web/api"
	"renart/internal/web/snapshot"
)

// DeployAPI exposes snapshot deploys and drift inspection.
type DeployAPI struct {
	Snapshots *snapshot.Store
	// ResolvePipeline maps the path-encoded API pipeline ID to the stable
	// UUID and the absolute pipeline directory.
	ResolvePipeline func(pipelineID string) (pipelineUUID, absDir string, ok bool)
	// ResolveDependencyManifest returns the immutable URI-owner contract and
	// the source Merkle root it was read from. The store rechecks that root in
	// the same deploy operation.
	ResolveDependencyManifest func(context.Context, string) (snapshot.DependencyManifest, string, error)
}

func RegisterDeployRoutes(router chi.Router, handlers *DeployAPI) {
	router.Post("/api/pipelines/{id}/deploy", handlers.HandleDeploy)
	router.Get("/api/pipelines/{id}/deploy/status", handlers.HandleDeployStatus)
	router.Get("/api/pipelines/{id}/snapshots", handlers.HandleListSnapshots)
	router.Get("/api/pipelines/{id}/deploy/diff", handlers.HandleDeployFileDiff)
	router.Get("/api/snapshots/{versionId}/file", handlers.HandleSnapshotFile)
}

func (h *DeployAPI) HandleDeployFileDiff(w http.ResponseWriter, r *http.Request) {
	pipelineUUID, absDir, ok := h.ResolvePipeline(chi.URLParam(r, "id"))
	if !ok {
		webapi.WriteNotFound(w, "pipeline_not_found", "pipeline not found")
		return
	}
	comparison, err := h.Snapshots.CompareFile(
		r.Context(),
		pipelineUUID,
		absDir,
		strings.TrimSpace(r.URL.Query().Get("version_id")),
		strings.TrimSpace(r.URL.Query().Get("path")),
	)
	if err != nil {
		webapi.WriteBadRequest(w, "deployment_diff_failed", err.Error())
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "diff": comparison})
}

func (h *DeployAPI) HandleDeploy(w http.ResponseWriter, r *http.Request) {
	pipelineUUID, absDir, ok := h.ResolvePipeline(chi.URLParam(r, "id"))
	if !ok {
		webapi.WriteNotFound(w, "pipeline_not_found", "pipeline not found")
		return
	}
	type deployRequest struct {
		ExpectedSourceMerkle string `json:"expected_source_merkle,omitempty"`
	}
	request := deployRequest{}
	if r.ContentLength != 0 {
		decoded, decodeErr := decodeStrictJSONObject[deployRequest](r.Body)
		if decodeErr != nil {
			webapi.WriteBadRequest(w, "invalid_request_body", decodeErr.Error())
			return
		}
		request = decoded
	}
	dependencyManifest := snapshot.EmptyDependencyManifest()
	expectedRoot := strings.TrimSpace(request.ExpectedSourceMerkle)
	if h.ResolveDependencyManifest != nil {
		resolved, sourceRoot, resolveErr := h.ResolveDependencyManifest(r.Context(), pipelineUUID)
		if resolveErr != nil {
			webapi.WriteBadRequest(w, "deployment_dependencies_invalid", resolveErr.Error())
			return
		}
		dependencyManifest = resolved
		sourceRoot = strings.TrimSpace(sourceRoot)
		if expectedRoot != "" && sourceRoot != expectedRoot {
			webapi.WriteErrorWithDetails(w, http.StatusConflict, "deployment_source_changed", "the saved pipeline source changed after review", map[string]string{
				"expected_source_merkle": expectedRoot,
				"actual_source_merkle":   sourceRoot,
			})
			return
		}
		expectedRoot = sourceRoot
	}
	deployed, created, err := h.Snapshots.DeployReviewedWithDependencies(
		r.Context(), pipelineUUID, absDir, "web", expectedRoot, dependencyManifest,
	)
	if err != nil {
		var changed *snapshot.SourceChangedError
		if errors.As(err, &changed) {
			webapi.WriteErrorWithDetails(w, http.StatusConflict, "deployment_source_changed", "the saved pipeline source changed after review", map[string]string{
				"expected_source_merkle": changed.Expected,
				"actual_source_merkle":   changed.Actual,
			})
			return
		}
		webapi.WriteInternalError(w, "deploy_failed", err.Error())
		return
	}
	message := "deployed new version"
	if !created {
		message = "already up to date with the latest snapshot"
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"created":  created,
		"message":  message,
		"snapshot": snapshotSummary(deployed),
	})
}

func (h *DeployAPI) HandleDeployStatus(w http.ResponseWriter, r *http.Request) {
	pipelineUUID, absDir, ok := h.ResolvePipeline(chi.URLParam(r, "id"))
	if !ok {
		webapi.WriteNotFound(w, "pipeline_not_found", "pipeline not found")
		return
	}
	var report snapshot.DriftReport
	var err error
	if h.ResolveDependencyManifest == nil {
		report, err = h.Snapshots.Drift(r.Context(), pipelineUUID, absDir)
	} else {
		dependencyManifest, _, resolveErr := h.ResolveDependencyManifest(r.Context(), pipelineUUID)
		if resolveErr != nil {
			report, err = h.Snapshots.Drift(r.Context(), pipelineUUID, absDir)
			report.InSync = false
			report.DependencyManifestInSync = false
			report.DependencyManifestError = resolveErr.Error()
		} else {
			report, err = h.Snapshots.DriftWithDependencies(r.Context(), pipelineUUID, absDir, dependencyManifest)
		}
	}
	if err != nil {
		webapi.WriteInternalError(w, "deploy_status_failed", err.Error())
		return
	}
	webapi.WriteJSON(w, http.StatusOK, report)
}

func (h *DeployAPI) HandleListSnapshots(w http.ResponseWriter, r *http.Request) {
	pipelineUUID, _, ok := h.ResolvePipeline(chi.URLParam(r, "id"))
	if !ok {
		webapi.WriteNotFound(w, "pipeline_not_found", "pipeline not found")
		return
	}
	snapshots, err := h.Snapshots.List(r.Context(), pipelineUUID)
	if err != nil {
		webapi.WriteInternalError(w, "snapshot_list_failed", err.Error())
		return
	}
	summaries := make([]map[string]any, 0, len(snapshots))
	for _, item := range snapshots {
		summaries = append(summaries, snapshotSummary(item))
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"snapshots": summaries})
}

// HandleSnapshotFile serves one file's deployed content (the per-asset diff
// view's "deployed side").
func (h *DeployAPI) HandleSnapshotFile(w http.ResponseWriter, r *http.Request) {
	versionID := chi.URLParam(r, "versionId")
	relPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if relPath == "" {
		webapi.WriteBadRequest(w, "path_required", "path query parameter is required")
		return
	}
	deployed, err := h.Snapshots.Get(r.Context(), versionID)
	if err != nil {
		webapi.WriteNotFound(w, "snapshot_not_found", "snapshot not found")
		return
	}
	hash, ok := deployed.Manifest[relPath]
	if !ok {
		webapi.WriteNotFound(w, "file_not_in_snapshot", "file is not part of this snapshot")
		return
	}
	content, err := h.Snapshots.BlobContent(r.Context(), hash)
	if err != nil {
		webapi.WriteInternalError(w, "blob_read_failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(content)
}

func snapshotSummary(item snapshot.Snapshot) map[string]any {
	return map[string]any{
		"version_id":  item.VersionID,
		"pipeline_id": item.PipelineUUID,
		"ordinal":     item.Ordinal,
		"merkle_root": item.MerkleRoot,
		"file_count":  len(item.Manifest),
		"git_sha":     item.GitSHA,
		"git_dirty":   item.GitDirty,
		"created_at":  item.CreatedAt,
		"created_by":  item.CreatedBy,
	}
}
