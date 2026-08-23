package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"

	"github.com/go-chi/chi/v5"
	webapi "renart/internal/web/api"
	"renart/internal/web/service"
)

// APIError is the shared service error shape, re-exported for handlers.
type APIError = service.APIError

type ErrorResponse struct {
	Status string            `json:"status"`
	Error  ErrorResponseBody `json:"error"`
}

type ErrorResponseBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Asset request/response DTOs are defined once in the service package;
// these aliases keep the httpapi names stable for handlers and consumers.
type (
	CreateAssetRequest           = service.CreateAssetParams
	UpdateAssetRequest           = service.AssetUpdateRequest
	WorkspaceColumn              = service.WorkspaceColumn
	ColumnReconcileResult        = service.ColumnReconcileResult
	ColumnInferencePreview       = service.ColumnInferencePreview
	ColumnSchemaSyncResult       = service.ColumnSchemaSyncResult
	ColumnSchemaResolution       = service.ColumnSchemaResolution
	AssetTransaction             = service.AssetTransaction
	AssetTransactionResult       = service.AssetTransactionResult
	FormatSQLAssetRequest        = service.FormatSQLAssetRequest
	FormatSQLAssetResponse       = service.FormatSQLAssetResponse
	FormatPythonAssetRequest     = service.FormatPythonAssetRequest
	FormatPythonAssetResponse    = service.FormatPythonAssetResponse
	PythonDiagnosticsRequest     = service.PythonDiagnosticsRequest
	PythonCompletionsRequest     = service.PythonCompletionsRequest
	PythonPositionRequest        = service.PythonPositionRequest
	PythonDiagnosticsResponse    = service.PythonDiagnosticsResponse
	PythonDiagnostic             = service.PythonDiagnostic
	PythonRange                  = service.PythonRange
	PythonPosition               = service.PythonPosition
	PythonCompletionsResponse    = service.PythonCompletionsResponse
	PythonCompletion             = service.PythonCompletion
	PythonTextEdit               = service.PythonTextEdit
	PythonHoverResponse          = service.PythonHoverResponse
	PythonHover                  = service.PythonHover
	PythonSignatureHelpResponse  = service.PythonSignatureHelpResponse
	PythonSignatureHelp          = service.PythonSignatureHelp
	PythonSignature              = service.PythonSignature
	PythonSignatureParameter     = service.PythonSignatureParameter
	PythonGotoDefinitionResponse = service.PythonGotoDefinitionResponse
	PythonGotoTarget             = service.PythonGotoTarget
	PythonDepsResponse           = service.PythonDepsResponse
	AddPythonDependencyRequest   = service.AddPythonDependencyRequest
	AssetMutationResponse        = service.AssetMutationResponse
	AssetCreationProfile         = service.AssetCreationProfile
	SeedFilePreviewResponse      = service.SeedFilePreviewResponse
	StatusResponse               = service.StatusResponse
)

type AssetHandlers interface {
	AssetCreationProfile(ctx context.Context, pipelineID, environment string) (AssetCreationProfile, *APIError)
	Create(ctx context.Context, pipelineID string, req CreateAssetRequest) (AssetMutationResponse, *APIError)
	Update(ctx context.Context, assetID string, req UpdateAssetRequest) (AssetMutationResponse, *APIError)
	SeedFilePreview(ctx context.Context, assetID string) (SeedFilePreviewResponse, *APIError)
	ReplaceSeedFile(ctx context.Context, assetID, fileName string, fileBytes []byte) (AssetMutationResponse, *APIError)
	Delete(ctx context.Context, assetID string) (StatusResponse, *APIError)
	FormatSQL(ctx context.Context, assetID string, req FormatSQLAssetRequest) (FormatSQLAssetResponse, *APIError)
	FormatPython(ctx context.Context, assetID string, req FormatPythonAssetRequest) (FormatPythonAssetResponse, *APIError)
	PythonDiagnostics(ctx context.Context, assetID string, req PythonDiagnosticsRequest) (PythonDiagnosticsResponse, *APIError)
	PythonCompletions(ctx context.Context, assetID string, req PythonCompletionsRequest) (PythonCompletionsResponse, *APIError)
	PythonHover(ctx context.Context, assetID string, req PythonPositionRequest) (PythonHoverResponse, *APIError)
	PythonSignatureHelp(ctx context.Context, assetID string, req PythonPositionRequest) (PythonSignatureHelpResponse, *APIError)
	PythonGotoDefinition(ctx context.Context, assetID string, req PythonPositionRequest) (PythonGotoDefinitionResponse, *APIError)
	PythonDeps(assetID string) (PythonDepsResponse, *APIError)
	AddPythonDependency(ctx context.Context, assetID string, req AddPythonDependencyRequest) (PythonDepsResponse, *APIError)
	ApplyAssetTransaction(ctx context.Context, assetID string, tx AssetTransaction) (AssetTransactionResult, *APIError)
}

type AssetsAPI struct {
	Service AssetHandlers
}

const maxSeedUploadBytes int64 = 256 << 20

func RegisterAssetRoutes(router chi.Router, handlers *AssetsAPI) {
	router.Get("/api/pipelines/{id}/asset-creation-profile", handlers.HandleAssetCreationProfile)
	router.Post("/api/pipelines/{id}/assets", handlers.HandleCreateAsset)
	router.Put("/api/pipelines/{pipelineID}/assets/{assetID}", handlers.HandleUpdateAsset)
	router.Get("/api/assets/{assetID}/seed-file", handlers.HandleSeedFilePreview)
	router.Post("/api/assets/{assetID}/seed-file", handlers.HandleReplaceSeedFile)
	router.Delete("/api/pipelines/{pipelineID}/assets/{assetID}", handlers.HandleDeleteAsset)
	router.Post("/api/assets/{assetID}/format-sql", handlers.HandleFormatSQLAsset)
	router.Post("/api/assets/{assetID}/format-python", handlers.HandleFormatPythonAsset)
	router.Post("/api/assets/{assetID}/python-diagnostics", handlers.HandlePythonDiagnostics)
	router.Post("/api/assets/{assetID}/python-completions", handlers.HandlePythonCompletions)
	router.Post("/api/assets/{assetID}/python-hover", handlers.HandlePythonHover)
	router.Post("/api/assets/{assetID}/python-signature-help", handlers.HandlePythonSignatureHelp)
	router.Post("/api/assets/{assetID}/python-goto-definition", handlers.HandlePythonGotoDefinition)
	router.Get("/api/assets/{assetID}/python-deps", handlers.HandlePythonDeps)
	router.Post("/api/assets/{assetID}/python-deps", handlers.HandleAddPythonDependency)
	router.Post("/api/assets/{assetID}/transactions", handlers.HandleApplyAssetTransaction)
}

func (h *AssetsAPI) HandleAssetCreationProfile(w http.ResponseWriter, r *http.Request) {
	resp, apiErr := h.Service.AssetCreationProfile(
		r.Context(),
		chi.URLParam(r, "id"),
		r.URL.Query().Get("environment"),
	)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, resp)
}

func (h *AssetsAPI) HandleCreateAsset(w http.ResponseWriter, r *http.Request) {
	req, err := decodeCreateAssetRequest(w, r)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	resp, apiErr := h.Service.Create(r.Context(), chi.URLParam(r, "id"), req)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusCreated, resp)
}

func decodeCreateAssetRequest(w http.ResponseWriter, r *http.Request) (CreateAssetRequest, error) {
	mediaType, _, mediaTypeErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaTypeErr != nil || mediaType != "multipart/form-data" {
		return decodeJSONObject[CreateAssetRequest](w, r, 0)
	}

	var req CreateAssetRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxSeedUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		return CreateAssetRequest{}, err
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	encodedRequest := r.FormValue("request")
	if encodedRequest == "" {
		return CreateAssetRequest{}, errors.New("multipart request field is required")
	}
	if err := json.Unmarshal([]byte(encodedRequest), &req); err != nil {
		return CreateAssetRequest{}, fmt.Errorf("invalid multipart request field: %w", err)
	}

	file, header, err := r.FormFile("file")
	if errors.Is(err, http.ErrMissingFile) {
		return req, nil
	}
	if err != nil {
		return CreateAssetRequest{}, err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maxSeedUploadBytes+1))
	if err != nil {
		return CreateAssetRequest{}, err
	}
	if int64(len(contents)) > maxSeedUploadBytes {
		return CreateAssetRequest{}, fmt.Errorf("seed upload exceeds the %d MiB limit", maxSeedUploadBytes>>20)
	}
	req.SeedFileName = header.Filename
	req.SeedFileBytes = contents
	return req, nil
}

func (h *AssetsAPI) HandleUpdateAsset(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[UpdateAssetRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	resp, apiErr := h.Service.Update(r.Context(), chi.URLParam(r, "assetID"), req)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, resp)
}

func (h *AssetsAPI) HandleReplaceSeedFile(w http.ResponseWriter, r *http.Request) {
	fileName, fileBytes, err := decodeSeedFileUpload(w, r)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_seed_upload", err.Error())
		return
	}
	resp, apiErr := h.Service.ReplaceSeedFile(
		r.Context(),
		chi.URLParam(r, "assetID"),
		fileName,
		fileBytes,
	)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, resp)
}

func (h *AssetsAPI) HandleSeedFilePreview(w http.ResponseWriter, r *http.Request) {
	resp, apiErr := h.Service.SeedFilePreview(r.Context(), chi.URLParam(r, "assetID"))
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, resp)
}

func decodeSeedFileUpload(w http.ResponseWriter, r *http.Request) (string, []byte, error) {
	mediaType, _, mediaTypeErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaTypeErr != nil || mediaType != "multipart/form-data" {
		return "", nil, errors.New("multipart/form-data is required")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSeedUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		return "", nil, err
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if errors.Is(err, http.ErrMissingFile) {
		return "", nil, errors.New("seed file is required")
	}
	if err != nil {
		return "", nil, err
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maxSeedUploadBytes+1))
	if err != nil {
		return "", nil, err
	}
	if int64(len(contents)) > maxSeedUploadBytes {
		return "", nil, fmt.Errorf("seed upload exceeds the %d MiB limit", maxSeedUploadBytes>>20)
	}
	return header.Filename, contents, nil
}

func (h *AssetsAPI) HandleApplyAssetTransaction(w http.ResponseWriter, r *http.Request) {
	tx, err := decodeJSONObject[AssetTransaction](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	resp, apiErr := h.Service.ApplyAssetTransaction(r.Context(), chi.URLParam(r, "assetID"), tx)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, resp)
}

func (h *AssetsAPI) HandleDeleteAsset(w http.ResponseWriter, r *http.Request) {
	resp, apiErr := h.Service.Delete(r.Context(), chi.URLParam(r, "assetID"))
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, resp)
}

func (h *AssetsAPI) HandleFormatSQLAsset(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[FormatSQLAssetRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	resp, apiErr := h.Service.FormatSQL(r.Context(), chi.URLParam(r, "assetID"), req)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, resp)
}

func (h *AssetsAPI) HandleFormatPythonAsset(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[FormatPythonAssetRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	resp, apiErr := h.Service.FormatPython(r.Context(), chi.URLParam(r, "assetID"), req)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, resp)
}

func (h *AssetsAPI) HandlePythonDiagnostics(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[PythonDiagnosticsRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	resp, apiErr := h.Service.PythonDiagnostics(r.Context(), chi.URLParam(r, "assetID"), req)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, resp)
}

func (h *AssetsAPI) HandlePythonCompletions(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[PythonCompletionsRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	resp, apiErr := h.Service.PythonCompletions(r.Context(), chi.URLParam(r, "assetID"), req)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, resp)
}

func (h *AssetsAPI) HandlePythonHover(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[PythonPositionRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	resp, apiErr := h.Service.PythonHover(r.Context(), chi.URLParam(r, "assetID"), req)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, resp)
}

func (h *AssetsAPI) HandlePythonSignatureHelp(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[PythonPositionRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	resp, apiErr := h.Service.PythonSignatureHelp(r.Context(), chi.URLParam(r, "assetID"), req)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, resp)
}

func (h *AssetsAPI) HandlePythonGotoDefinition(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[PythonPositionRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	resp, apiErr := h.Service.PythonGotoDefinition(r.Context(), chi.URLParam(r, "assetID"), req)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, resp)
}

func (h *AssetsAPI) HandlePythonDeps(w http.ResponseWriter, r *http.Request) {
	resp, apiErr := h.Service.PythonDeps(chi.URLParam(r, "assetID"))
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, resp)
}

func (h *AssetsAPI) HandleAddPythonDependency(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[AddPythonDependencyRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	resp, apiErr := h.Service.AddPythonDependency(r.Context(), chi.URLParam(r, "assetID"), req)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, resp)
}

func writeAPIError(w http.ResponseWriter, apiErr *APIError) {
	webapi.WriteJSON(w, apiErr.Status, ErrorResponse{
		Status: "error",
		Error: ErrorResponseBody{
			Code:    apiErr.Code,
			Message: apiErr.Message,
		},
	})
}
