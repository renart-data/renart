package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	webapi "renart/internal/web/api"
	"renart/internal/web/service"
)

type SQLHandlers interface {
	ColumnValues(ctx context.Context, connectionName, environment, query string) service.SQLColumnValuesResult
	Query(ctx context.Context, connectionName, environment, query string, limit int) service.SQLQueryResult
	Databases(ctx context.Context, connectionName, environment string) (service.SQLDatabaseDiscoveryResult, *service.APIError)
	Tables(ctx context.Context, connectionName, databaseName, environment string) (service.SQLTableDiscoveryResult, *service.APIError)
	TableColumns(ctx context.Context, connectionName, tableName, environment string) (service.SQLTableColumnsResult, int)
}

type SQLAPI struct {
	Service SQLHandlers
}

type SQLColumnValuesRequest struct {
	Connection  string `json:"connection"`
	Environment string `json:"environment,omitempty"`
	Query       string `json:"query"`
}

type SQLQueryRequest struct {
	Connection  string `json:"connection"`
	Environment string `json:"environment,omitempty"`
	Query       string `json:"query"`
	Limit       int    `json:"limit,omitempty"`
}

func RegisterSQLRoutes(router chi.Router, handlers *SQLAPI) {
	router.Post("/api/sql/column-values", handlers.HandleSQLColumnValues)
	router.Post("/api/sql/query", handlers.HandleSQLQuery)
	router.Get("/api/sql/databases", handlers.HandleGetSQLDatabases)
	router.Get("/api/sql/tables", handlers.HandleGetSQLTables)
	router.Get("/api/sql/table-columns", handlers.HandleGetSQLTableColumns)
}

func (h *SQLAPI) HandleSQLColumnValues(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[SQLColumnValuesRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}

	connectionName := strings.TrimSpace(req.Connection)
	if connectionName == "" {
		webapi.WriteBadRequest(w, "connection_required", "connection is required")
		return
	}

	query := strings.TrimSpace(req.Query)
	if query == "" {
		webapi.WriteBadRequest(w, "query_required", "query is required")
		return
	}

	result := h.Service.ColumnValues(r.Context(), connectionName, req.Environment, query)
	webapi.WriteJSON(w, http.StatusOK, result)
}

func (h *SQLAPI) HandleSQLQuery(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[SQLQueryRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}

	connectionName := strings.TrimSpace(req.Connection)
	if connectionName == "" {
		webapi.WriteBadRequest(w, "connection_required", "connection is required")
		return
	}

	query := strings.TrimSpace(req.Query)
	if query == "" {
		webapi.WriteBadRequest(w, "query_required", "query is required")
		return
	}

	limit := req.Limit
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}

	result := h.Service.Query(r.Context(), connectionName, req.Environment, query, limit)
	webapi.WriteJSON(w, http.StatusOK, result)
}

func (h *SQLAPI) HandleGetSQLDatabases(w http.ResponseWriter, r *http.Request) {
	connectionName := strings.TrimSpace(r.URL.Query().Get("connection"))
	environment := strings.TrimSpace(r.URL.Query().Get("environment"))
	if connectionName == "" {
		webapi.WriteBadRequest(w, "connection_required", "connection query parameter is required")
		return
	}

	result, apiErr := h.Service.Databases(r.Context(), connectionName, environment)
	if apiErr != nil {
		webapi.WriteJSON(w, apiErr.Status, map[string]any{
			"status": "error",
			"error":  map[string]string{"code": apiErr.Code, "message": apiErr.Message},
		})
		return
	}

	webapi.WriteJSON(w, http.StatusOK, result)
}

func (h *SQLAPI) HandleGetSQLTables(w http.ResponseWriter, r *http.Request) {
	connectionName := strings.TrimSpace(r.URL.Query().Get("connection"))
	environment := strings.TrimSpace(r.URL.Query().Get("environment"))
	if connectionName == "" {
		webapi.WriteBadRequest(w, "connection_required", "connection query parameter is required")
		return
	}

	databaseName := strings.TrimSpace(r.URL.Query().Get("database"))
	if databaseName == "" {
		webapi.WriteBadRequest(w, "database_required", "database query parameter is required")
		return
	}

	result, apiErr := h.Service.Tables(r.Context(), connectionName, databaseName, environment)
	if apiErr != nil {
		webapi.WriteJSON(w, apiErr.Status, map[string]any{
			"status": "error",
			"error":  map[string]string{"code": apiErr.Code, "message": apiErr.Message},
		})
		return
	}

	webapi.WriteJSON(w, http.StatusOK, result)
}

func (h *SQLAPI) HandleGetSQLTableColumns(w http.ResponseWriter, r *http.Request) {
	connectionName := strings.TrimSpace(r.URL.Query().Get("connection"))
	environment := strings.TrimSpace(r.URL.Query().Get("environment"))
	if connectionName == "" {
		webapi.WriteBadRequest(w, "connection_required", "connection query parameter is required")
		return
	}

	tableName := strings.TrimSpace(r.URL.Query().Get("table"))
	if tableName == "" {
		webapi.WriteBadRequest(w, "table_required", "table query parameter is required")
		return
	}

	result, status := h.Service.TableColumns(r.Context(), connectionName, tableName, environment)
	webapi.WriteJSON(w, status, result)
}
