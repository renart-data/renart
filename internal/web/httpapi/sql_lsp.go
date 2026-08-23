package httpapi

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	webapi "renart/internal/web/api"
	"renart/internal/web/service"
)

type SQLLSPHandlers interface {
	Diagnostics(ctx context.Context, req service.SQLLSPRequest) (service.SQLLSPResponse, *service.APIError)
	Completions(ctx context.Context, req service.SQLLSPRequest) (service.SQLLSPResponse, *service.APIError)
	Definition(ctx context.Context, req service.SQLLSPRequest) (service.SQLLSPResponse, *service.APIError)
	References(ctx context.Context, req service.SQLLSPRequest) (service.SQLLSPResponse, *service.APIError)
	Rename(ctx context.Context, req service.SQLLSPRequest) (service.SQLLSPResponse, *service.APIError)
	CodeActions(ctx context.Context, req service.SQLLSPRequest) (service.SQLLSPResponse, *service.APIError)
	Hover(ctx context.Context, req service.SQLLSPRequest) (service.SQLLSPResponse, *service.APIError)
	SemanticTokens(ctx context.Context, req service.SQLLSPRequest) (service.SQLLSPResponse, *service.APIError)
	DocumentSymbols(ctx context.Context, req service.SQLLSPRequest) (service.SQLLSPResponse, *service.APIError)
	Formatting(ctx context.Context, req service.SQLLSPRequest) (service.SQLLSPResponse, *service.APIError)
	SignatureHelp(ctx context.Context, req service.SQLLSPRequest) (service.SQLLSPResponse, *service.APIError)
}

type SQLLSPAPI struct {
	Service SQLLSPHandlers
}

func RegisterSQLLSPRoutes(router chi.Router, handlers *SQLLSPAPI) {
	router.Post("/api/sql/lsp/diagnostics", handlers.HandleDiagnostics)
	router.Post("/api/sql/lsp/completions", handlers.HandleCompletions)
	router.Post("/api/sql/lsp/definition", handlers.HandleDefinition)
	router.Post("/api/sql/lsp/references", handlers.HandleReferences)
	router.Post("/api/sql/lsp/rename", handlers.HandleRename)
	router.Post("/api/sql/lsp/code-actions", handlers.HandleCodeActions)
	router.Post("/api/sql/lsp/hover", handlers.HandleHover)
	router.Post("/api/sql/lsp/semantic-tokens", handlers.HandleSemanticTokens)
	router.Post("/api/sql/lsp/document-symbols", handlers.HandleDocumentSymbols)
	router.Post("/api/sql/lsp/formatting", handlers.HandleFormatting)
	router.Post("/api/sql/lsp/signature-help", handlers.HandleSignatureHelp)
}

func (h *SQLLSPAPI) HandleDiagnostics(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, h.Service.Diagnostics)
}

func (h *SQLLSPAPI) HandleCompletions(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, h.Service.Completions)
}

func (h *SQLLSPAPI) HandleDefinition(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, h.Service.Definition)
}

func (h *SQLLSPAPI) HandleReferences(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, h.Service.References)
}

func (h *SQLLSPAPI) HandleRename(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, h.Service.Rename)
}

func (h *SQLLSPAPI) HandleCodeActions(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, h.Service.CodeActions)
}

func (h *SQLLSPAPI) HandleHover(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, h.Service.Hover)
}

func (h *SQLLSPAPI) HandleSemanticTokens(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, h.Service.SemanticTokens)
}

func (h *SQLLSPAPI) HandleDocumentSymbols(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, h.Service.DocumentSymbols)
}

func (h *SQLLSPAPI) HandleFormatting(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, h.Service.Formatting)
}

func (h *SQLLSPAPI) HandleSignatureHelp(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, h.Service.SignatureHelp)
}

func (h *SQLLSPAPI) handle(w http.ResponseWriter, r *http.Request, fn func(context.Context, service.SQLLSPRequest) (service.SQLLSPResponse, *service.APIError)) {
	req, err := decodeJSONObject[service.SQLLSPRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	result, apiErr := fn(r.Context(), req)
	if apiErr != nil {
		webapi.WriteJSON(w, apiErr.Status, map[string]any{
			"status": "error",
			"error":  map[string]string{"code": apiErr.Code, "message": apiErr.Message},
		})
		return
	}
	webapi.WriteJSON(w, http.StatusOK, result)
}
