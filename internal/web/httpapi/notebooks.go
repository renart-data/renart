package httpapi

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	webapi "renart/internal/web/api"
	"renart/internal/web/model"
	"renart/internal/web/service"
)

// NotebookHandlers is the service surface the notebook routes need.
type NotebookHandlers interface {
	Get(notebookID string) (model.Notebook, *service.APIError)
	Create(req service.CreateNotebookRequest) (model.Notebook, *service.APIError)
	Delete(notebookID string) *service.APIError
	CloseSession(notebookID string) *service.APIError
	CreateCell(notebookID string, req service.CreateCellRequest) (model.Notebook, *service.APIError)
	UpdateCell(notebookID, cellID string, req service.UpdateCellRequest) (model.Notebook, *service.APIError)
	RenameCell(notebookID, cellID, newName string) (model.Notebook, *service.APIError)
	DeleteCell(notebookID, cellID string) (model.Notebook, *service.APIError)
	UpdateBlocks(notebookID string, blocks []model.NotebookBlock) (model.Notebook, *service.APIError)
	UpgradeManifest(notebookID, baseRevision string) (model.Notebook, *service.APIError)
	PrepareChangeSet(notebookID string, changeSet service.NotebookChangeSet) (service.NotebookChangePlan, *service.APIError)
	ApplyChangeSet(notebookID string, changeSet service.NotebookChangeSet) (service.NotebookChangeApplyResult, *service.APIError)
	CheckVisualization(ctx context.Context, notebookID string, request service.NotebookVisualizationCheckRequest) (service.NotebookVisualizationCheckResult, *service.APIError)
	UpdateDependencies(notebookID, content string) (model.Notebook, *service.APIError)
	PlanPromoteCell(notebookID, cellID string, req service.PromoteCellRequest) (service.PromoteCellPlan, *service.APIError)
	PromoteCell(notebookID, cellID string, req service.PromoteCellRequest) (service.PromoteCellResult, *service.APIError)
	ExportCell(ctx context.Context, notebookID, cellID, format string) (service.NotebookCellExport, *service.APIError)
	Run(ctx context.Context, notebookID string, req service.RunNotebookRequest) (service.RunNotebookResult, *service.APIError)
	Runtime(notebookID string) (service.NotebookRuntimeSnapshot, *service.APIError)
	SetAutoRecompute(notebookID string, enabled bool, environment string, parameterValues map[string]any) *service.APIError
	CancelRuns(ctx context.Context, notebookID string) *service.APIError
}

// NotebookAPI exposes notebook CRUD and execution.
type NotebookAPI struct {
	Service NotebookHandlers
}

// RegisterNotebookRoutes mounts the notebook endpoints.
func RegisterNotebookRoutes(router chi.Router, handlers *NotebookAPI) {
	router.Post("/api/notebooks", handlers.HandleCreate)
	router.Get("/api/notebooks/{id}", handlers.HandleGet)
	router.Delete("/api/notebooks/{id}", handlers.HandleDelete)
	router.Delete("/api/notebooks/{id}/session", handlers.HandleCloseSession)
	router.Post("/api/notebooks/{id}/cells", handlers.HandleCreateCell)
	router.Put("/api/notebooks/{id}/cells/{cellID}", handlers.HandleUpdateCell)
	router.Post("/api/notebooks/{id}/cells/{cellID}/rename", handlers.HandleRenameCell)
	router.Delete("/api/notebooks/{id}/cells/{cellID}", handlers.HandleDeleteCell)
	router.Put("/api/notebooks/{id}/blocks", handlers.HandleUpdateBlocks)
	router.Post("/api/notebooks/{id}/upgrade", handlers.HandleUpgradeManifest)
	router.Post("/api/notebooks/{id}/changes/prepare", handlers.HandlePrepareChangeSet)
	router.Post("/api/notebooks/{id}/changes/apply", handlers.HandleApplyChangeSet)
	router.Post("/api/notebooks/{id}/visualizations/check", handlers.HandleCheckVisualization)
	router.Put("/api/notebooks/{id}/dependencies", handlers.HandleUpdateDependencies)
	router.Post("/api/notebooks/{id}/cells/{cellID}/promote/plan", handlers.HandlePlanPromoteCell)
	router.Post("/api/notebooks/{id}/cells/{cellID}/promote", handlers.HandlePromoteCell)
	router.Get("/api/notebooks/{id}/cells/{cellID}/export", handlers.HandleExportCell)
	router.Post("/api/notebooks/{id}/run", handlers.HandleRun)
	router.Get("/api/notebooks/{id}/runtime", handlers.HandleRuntime)
	router.Put("/api/notebooks/{id}/settings", handlers.HandleSettings)
	router.Post("/api/notebooks/{id}/cancel", handlers.HandleCancel)
}

func writeNotebookError(w http.ResponseWriter, apiErr *service.APIError) {
	webapi.WriteJSON(w, apiErr.Status, map[string]any{
		"status": "error",
		"error":  map[string]string{"code": apiErr.Code, "message": apiErr.Message},
	})
}

func (h *NotebookAPI) HandleGet(w http.ResponseWriter, r *http.Request) {
	nb, apiErr := h.Service.Get(chi.URLParam(r, "id"))
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "notebook": nb})
}

func (h *NotebookAPI) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var req service.CreateNotebookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	nb, apiErr := h.Service.Create(req)
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusCreated, map[string]any{"status": "ok", "notebook": nb})
}

func (h *NotebookAPI) HandleDelete(w http.ResponseWriter, r *http.Request) {
	if apiErr := h.Service.Delete(chi.URLParam(r, "id")); apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (h *NotebookAPI) HandleCloseSession(w http.ResponseWriter, r *http.Request) {
	if apiErr := h.Service.CloseSession(chi.URLParam(r, "id")); apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (h *NotebookAPI) HandleCreateCell(w http.ResponseWriter, r *http.Request) {
	var req service.CreateCellRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	nb, apiErr := h.Service.CreateCell(chi.URLParam(r, "id"), req)
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusCreated, map[string]any{"status": "ok", "notebook": nb})
}

func (h *NotebookAPI) HandleUpdateCell(w http.ResponseWriter, r *http.Request) {
	var req service.UpdateCellRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	nb, apiErr := h.Service.UpdateCell(chi.URLParam(r, "id"), chi.URLParam(r, "cellID"), req)
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "notebook": nb})
}

// UpdateDependenciesRequest carries the newline-separated dependency list.
type UpdateDependenciesRequest struct {
	Content string `json:"content"`
}

func (h *NotebookAPI) HandleUpdateDependencies(w http.ResponseWriter, r *http.Request) {
	var req UpdateDependenciesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	nb, apiErr := h.Service.UpdateDependencies(chi.URLParam(r, "id"), req.Content)
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "notebook": nb})
}

// RenameCellRequest carries the new display name for a cell.
type RenameCellRequest struct {
	Name string `json:"name"`
}

func (h *NotebookAPI) HandleRenameCell(w http.ResponseWriter, r *http.Request) {
	var req RenameCellRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	nb, apiErr := h.Service.RenameCell(chi.URLParam(r, "id"), chi.URLParam(r, "cellID"), req.Name)
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "notebook": nb})
}

func (h *NotebookAPI) HandleDeleteCell(w http.ResponseWriter, r *http.Request) {
	nb, apiErr := h.Service.DeleteCell(chi.URLParam(r, "id"), chi.URLParam(r, "cellID"))
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "notebook": nb})
}

// UpdateBlocksRequest replaces the notebook's ordered blocks.
type UpdateBlocksRequest struct {
	Blocks []model.NotebookBlock `json:"blocks"`
}

func (h *NotebookAPI) HandleUpdateBlocks(w http.ResponseWriter, r *http.Request) {
	var req UpdateBlocksRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	nb, apiErr := h.Service.UpdateBlocks(chi.URLParam(r, "id"), req.Blocks)
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "notebook": nb})
}

func (h *NotebookAPI) HandleUpgradeManifest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BaseRevision string `json:"base_revision,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	nb, apiErr := h.Service.UpgradeManifest(chi.URLParam(r, "id"), req.BaseRevision)
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "notebook": nb})
}

func (h *NotebookAPI) HandlePrepareChangeSet(w http.ResponseWriter, r *http.Request) {
	var changeSet service.NotebookChangeSet
	if err := json.NewDecoder(r.Body).Decode(&changeSet); err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	plan, apiErr := h.Service.PrepareChangeSet(chi.URLParam(r, "id"), changeSet)
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, plan)
}

func (h *NotebookAPI) HandleApplyChangeSet(w http.ResponseWriter, r *http.Request) {
	var changeSet service.NotebookChangeSet
	if err := json.NewDecoder(r.Body).Decode(&changeSet); err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	result, apiErr := h.Service.ApplyChangeSet(chi.URLParam(r, "id"), changeSet)
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, result)
}

func (h *NotebookAPI) HandleCheckVisualization(w http.ResponseWriter, r *http.Request) {
	var request service.NotebookVisualizationCheckRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	result, apiErr := h.Service.CheckVisualization(r.Context(), chi.URLParam(r, "id"), request)
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, result)
}

func (h *NotebookAPI) HandlePromoteCell(w http.ResponseWriter, r *http.Request) {
	var req service.PromoteCellRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	result, apiErr := h.Service.PromoteCell(chi.URLParam(r, "id"), chi.URLParam(r, "cellID"), req)
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, result)
}

func (h *NotebookAPI) HandlePlanPromoteCell(w http.ResponseWriter, r *http.Request) {
	var req service.PromoteCellRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	result, apiErr := h.Service.PlanPromoteCell(chi.URLParam(r, "id"), chi.URLParam(r, "cellID"), req)
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, result)
}

func (h *NotebookAPI) HandleExportCell(w http.ResponseWriter, r *http.Request) {
	export, apiErr := h.Service.ExportCell(
		r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "cellID"), r.URL.Query().Get("format"),
	)
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	defer export.Cleanup()

	file, err := os.Open(export.Path)
	if err != nil {
		webapi.WriteInternalError(w, "notebook_export_failed", err.Error())
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		webapi.WriteInternalError(w, "notebook_export_failed", err.Error())
		return
	}
	w.Header().Set("Content-Type", export.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": export.Filename}))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, export.Filename, info.ModTime(), file)
}

func (h *NotebookAPI) HandleRun(w http.ResponseWriter, r *http.Request) {
	var req service.RunNotebookRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	if req.Environment == "" {
		req.Environment = r.URL.Query().Get("environment")
	}
	result, apiErr := h.Service.Run(r.Context(), chi.URLParam(r, "id"), req)
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, result)
}

func (h *NotebookAPI) HandleRuntime(w http.ResponseWriter, r *http.Request) {
	snapshot, apiErr := h.Service.Runtime(chi.URLParam(r, "id"))
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, snapshot)
}

func (h *NotebookAPI) HandleSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AutoRecompute   bool           `json:"auto_recompute"`
		Environment     string         `json:"environment,omitempty"`
		ParameterValues map[string]any `json:"parameter_values,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	if apiErr := h.Service.SetAutoRecompute(chi.URLParam(r, "id"), req.AutoRecompute, req.Environment, req.ParameterValues); apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *NotebookAPI) HandleCancel(w http.ResponseWriter, r *http.Request) {
	if apiErr := h.Service.CancelRuns(r.Context(), chi.URLParam(r, "id")); apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
