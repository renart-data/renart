package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	webapi "renart/internal/web/api"
	webmodel "renart/internal/web/model"
	"renart/internal/web/service"
)

type PipelineChangePublisher interface {
	WorkspaceChanged(ctx context.Context, relPath, eventType string)
}

type PipelineHandlers struct {
	Service   *service.PipelineService
	Publisher PipelineChangePublisher
}

type CreatePipelineRequest struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	Template string `json:"template,omitempty"`
}

type UpdatePipelineRequest struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

func RegisterPipelineRoutes(router chi.Router, handlers *PipelineHandlers) {
	router.Get("/api/pipelines/templates", handlers.HandlePipelineTemplates)
	router.Post("/api/pipelines", handlers.HandleCreatePipeline)
	router.Put("/api/pipelines", handlers.HandleUpdatePipeline)
	router.Get("/api/pipelines/{id}/config", handlers.HandleGetPipelineConfig)
	router.Put("/api/pipelines/{id}/config", handlers.HandleUpdatePipelineConfig)
	router.Get("/api/pipelines/{id}/python-dependencies", handlers.HandleGetPipelinePythonDependencies)
	router.Put("/api/pipelines/{id}/python-dependencies", handlers.HandleUpdatePipelinePythonDependencies)
	router.Get("/api/pipelines/{id}/type-check", handlers.HandleTypeCheckPipeline)
	router.Post("/api/pipelines/{id}/external-relations/import/preview", handlers.HandlePreviewExternalRelationImport)
	router.Post("/api/pipelines/{id}/external-relations/import", handlers.HandleImportExternalRelation)
	router.Delete("/api/pipelines/{id}", handlers.HandleDeletePipeline)
}

func (h *PipelineHandlers) HandlePipelineTemplates(w http.ResponseWriter, _ *http.Request) {
	webapi.WriteJSON(w, http.StatusOK, service.PipelineTemplatesResponse{
		Status:    "ok",
		Templates: service.PipelineTemplates(),
	})
}

func (h *PipelineHandlers) HandleTypeCheckPipeline(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	startDate := strings.TrimSpace(r.URL.Query().Get("start_date"))
	endDate := strings.TrimSpace(r.URL.Query().Get("end_date"))

	report, apiErr := h.Service.TypeCheck(r.Context(), id, startDate, endDate)
	if apiErr != nil {
		webapi.WriteJSON(w, apiErr.Status, map[string]any{
			"status": "error",
			"error":  map[string]string{"code": apiErr.Code, "message": apiErr.Message},
		})
		return
	}

	webapi.WriteJSON(w, http.StatusOK, report)
}

func (h *PipelineHandlers) HandlePreviewExternalRelationImport(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[service.ExternalRelationImportRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	result, apiErr := h.Service.PreviewExternalRelationImport(r.Context(), chi.URLParam(r, "id"), req)
	if apiErr != nil {
		webapi.WriteJSON(w, apiErr.Status, map[string]any{
			"status": "error",
			"error":  map[string]string{"code": apiErr.Code, "message": apiErr.Message},
		})
		return
	}
	webapi.WriteJSON(w, http.StatusOK, result)
}

func (h *PipelineHandlers) HandleImportExternalRelation(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[service.ExternalRelationImportRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	result, apiErr := h.Service.ImportExternalRelation(r.Context(), chi.URLParam(r, "id"), req)
	if apiErr != nil {
		webapi.WriteJSON(w, apiErr.Status, map[string]any{
			"status": "error",
			"error":  map[string]string{"code": apiErr.Code, "message": apiErr.Message},
		})
		return
	}
	if h.Publisher != nil {
		h.Publisher.WorkspaceChanged(r.Context(), result.PipelinePath, "pipeline.external-relation-imported")
	}
	webapi.WriteJSON(w, http.StatusCreated, result)
}

func (h *PipelineHandlers) HandleCreatePipeline(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[CreatePipelineRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		webapi.WriteBadRequest(w, "pipeline_path_required", "path is required")
		return
	}

	relPath, err := h.Service.Create(r.Context(), req.Path, req.Name, req.Content, req.Template)
	if err != nil {
		if strings.Contains(err.Error(), "unknown pipeline template") || strings.Contains(err.Error(), "cannot be combined") {
			webapi.WriteBadRequest(w, "invalid_pipeline_template", err.Error())
			return
		}
		if strings.Contains(err.Error(), "invalid pipeline config") || strings.Contains(err.Error(), "pipeline config must be a YAML mapping") {
			webapi.WriteBadRequest(w, "invalid_pipeline_config", err.Error())
			return
		}
		if strings.Contains(err.Error(), "invalid path") {
			webapi.WriteBadRequest(w, "invalid_pipeline_path", err.Error())
			return
		}
		if strings.Contains(err.Error(), "already exists") {
			webapi.WriteConflict(w, "pipeline_exists", err.Error())
			return
		}
		if strings.Contains(err.Error(), "mkdir") || strings.Contains(err.Error(), "permission") {
			webapi.WriteInternalError(w, "pipeline_create_failed", err.Error())
			return
		}
		webapi.WriteInternalError(w, "pipeline_write_failed", err.Error())
		return
	}

	if h.Publisher != nil {
		h.Publisher.WorkspaceChanged(r.Context(), relPath, "pipeline.created")
	}
	webapi.WriteJSON(w, http.StatusCreated, StatusResponse{Status: "ok"})
}

func (h *PipelineHandlers) HandleUpdatePipeline(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[UpdatePipelineRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}

	relPath, err := h.Service.Update(r.Context(), req.ID, req.Name, req.Content)
	if err != nil {
		message := err.Error()
		switch {
		case strings.Contains(message, "illegal base64"):
			webapi.WriteBadRequest(w, "invalid_pipeline_id", "invalid pipeline id")
		case strings.Contains(message, "invalid path"):
			webapi.WriteBadRequest(w, "invalid_pipeline_path", message)
		case strings.Contains(message, "yaml") || strings.Contains(message, "parse"):
			webapi.WriteBadRequest(w, "pipeline_parse_failed", message)
		default:
			webapi.WriteInternalError(w, "pipeline_write_failed", message)
		}
		return
	}

	if h.Publisher != nil {
		h.Publisher.WorkspaceChanged(r.Context(), relPath, "pipeline.updated")
	}
	webapi.WriteJSON(w, http.StatusOK, StatusResponse{Status: "ok"})
}

func (h *PipelineHandlers) HandleDeletePipeline(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	relPath, err := h.Service.Delete(id)
	if err != nil {
		message := err.Error()
		switch {
		case strings.Contains(message, "illegal base64"):
			webapi.WriteBadRequest(w, "invalid_pipeline_id", "invalid pipeline id")
		case strings.Contains(message, "invalid path"):
			webapi.WriteBadRequest(w, "invalid_pipeline_path", message)
		default:
			webapi.WriteInternalError(w, "pipeline_delete_failed", message)
		}
		return
	}

	if h.Publisher != nil {
		h.Publisher.WorkspaceChanged(r.Context(), relPath, "pipeline.deleted")
	}
	webapi.WriteJSON(w, http.StatusOK, StatusResponse{Status: "ok"})
}

func (h *PipelineHandlers) HandleGetPipelineConfig(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	resp, err := h.Service.GetConfig(r.Context(), id)
	if err != nil {
		h.writePipelineConfigError(w, err)
		return
	}

	webapi.WriteJSON(w, http.StatusOK, resp)
}

func (h *PipelineHandlers) HandleUpdatePipelineConfig(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	req, err := decodeJSONObject[webmodel.UpdatePipelineConfigRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}

	relPath, resp, err := h.Service.UpdateConfig(r.Context(), id, req)
	if err != nil {
		h.writePipelineConfigError(w, err)
		return
	}

	if h.Publisher != nil {
		h.Publisher.WorkspaceChanged(r.Context(), relPath, "pipeline.updated")
	}
	webapi.WriteJSON(w, http.StatusOK, resp)
}

func (h *PipelineHandlers) HandleGetPipelinePythonDependencies(w http.ResponseWriter, r *http.Request) {
	resp, err := h.Service.PythonDependencies(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.writePipelinePythonDependenciesError(w, err)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, resp)
}

func (h *PipelineHandlers) HandleUpdatePipelinePythonDependencies(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[webmodel.UpdatePipelinePythonDependenciesRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}

	relPath, resp, err := h.Service.UpdatePythonDependencies(r.Context(), chi.URLParam(r, "id"), req)
	if err != nil {
		h.writePipelinePythonDependenciesError(w, err)
		return
	}
	if h.Publisher != nil {
		h.Publisher.WorkspaceChanged(r.Context(), relPath, "pipeline.dependencies")
	}
	webapi.WriteJSON(w, http.StatusOK, resp)
}

func (h *PipelineHandlers) writePipelinePythonDependenciesError(w http.ResponseWriter, err error) {
	message := err.Error()
	switch {
	case errors.Is(err, service.ErrInvalidPythonDependency):
		webapi.WriteBadRequest(w, "pipeline_python_dependencies_invalid", message)
	case strings.Contains(message, "illegal base64"):
		webapi.WriteBadRequest(w, "invalid_pipeline_id", "invalid pipeline id")
	case strings.Contains(message, "invalid path"):
		webapi.WriteBadRequest(w, "invalid_pipeline_path", message)
	default:
		webapi.WriteInternalError(w, "pipeline_python_dependencies_failed", message)
	}
}

func (h *PipelineHandlers) writePipelineConfigError(w http.ResponseWriter, err error) {
	message := err.Error()
	switch {
	case errors.Is(err, service.ErrInvalidPipelineDefaultConnection):
		webapi.WriteBadRequest(w, "pipeline_default_connection_invalid", message)
	case strings.Contains(message, "illegal base64"):
		webapi.WriteBadRequest(w, "invalid_pipeline_id", "invalid pipeline id")
	case strings.Contains(message, "invalid path"):
		webapi.WriteBadRequest(w, "invalid_pipeline_path", message)
	case strings.Contains(message, "yaml") || strings.Contains(message, "parse") || strings.Contains(message, "variable") || strings.Contains(message, "interval modifier"):
		webapi.WriteBadRequest(w, "pipeline_config_invalid", message)
	default:
		webapi.WriteInternalError(w, "pipeline_config_failed", message)
	}
}
