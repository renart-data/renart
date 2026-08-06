package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type ExternalRelationImportRequest struct {
	RelationID     string `json:"relation_id"`
	IncludeColumns *bool  `json:"include_columns,omitempty"`
}

type ExternalRelationImportAsset struct {
	Name    string      `json:"name"`
	Path    string      `json:"path"`
	Type    string      `json:"type"`
	Columns []SQLColumn `json:"columns"`
}

type ExternalRelationImportWarning struct {
	Table   string `json:"table"`
	Warning string `json:"warning"`
}

type ExternalRelationImportResult struct {
	Status         string                          `json:"status"`
	Preview        bool                            `json:"preview"`
	Relation       TypeCheckExternalRelation       `json:"relation"`
	Asset          ExternalRelationImportAsset     `json:"asset"`
	IncludeColumns bool                            `json:"include_columns"`
	Warnings       []ExternalRelationImportWarning `json:"warnings"`
	PipelinePath   string                          `json:"-"`
}

func (s *PipelineService) PreviewExternalRelationImport(ctx context.Context, pipelineID string, req ExternalRelationImportRequest) (ExternalRelationImportResult, *APIError) {
	return s.externalRelationImport(ctx, pipelineID, req, true)
}

func (s *PipelineService) ImportExternalRelation(ctx context.Context, pipelineID string, req ExternalRelationImportRequest) (ExternalRelationImportResult, *APIError) {
	return s.externalRelationImport(ctx, pipelineID, req, false)
}

func (s *PipelineService) externalRelationImport(
	ctx context.Context,
	pipelineID string,
	req ExternalRelationImportRequest,
	preview bool,
) (ExternalRelationImportResult, *APIError) {
	if s.externalImporter == nil {
		return ExternalRelationImportResult{}, &APIError{
			Status:  http.StatusNotImplemented,
			Code:    "external_relation_import_unavailable",
			Message: "external relation import is not available",
		}
	}
	relationID := strings.TrimSpace(req.RelationID)
	if relationID == "" {
		return ExternalRelationImportResult{}, &APIError{
			Status:  http.StatusBadRequest,
			Code:    "external_relation_required",
			Message: "relation_id is required",
		}
	}

	report, reportErr := s.TypeCheck(ctx, pipelineID, "", "")
	if reportErr != nil {
		return ExternalRelationImportResult{}, reportErr
	}
	var relation *TypeCheckExternalRelation
	for index := range report.ExternalRelations {
		if report.ExternalRelations[index].ID == relationID {
			relation = &report.ExternalRelations[index]
			break
		}
	}
	if relation == nil {
		return ExternalRelationImportResult{}, &APIError{
			Status:  http.StatusConflict,
			Code:    "external_relation_observation_changed",
			Message: "the external relation is no longer positively observed or referenced; run type-check again",
		}
	}

	relPipelinePath, _, decodeErr := s.resolver().DecodePipelineID(pipelineID)
	if decodeErr != nil {
		return ExternalRelationImportResult{}, &APIError{Status: http.StatusBadRequest, Code: "invalid_pipeline_id", Message: decodeErr.Error()}
	}
	includeColumns := true
	if req.IncludeColumns != nil {
		includeColumns = *req.IncludeColumns
	}
	output, importErr := s.externalImporter.ImportDatabase(ctx, ImportDatabaseRequest{
		PipelinePath:   relPipelinePath,
		ConnectionName: relation.Connection,
		Schema:         relation.SchemaName,
		Tables:         []string{relation.QualifiedName},
		DisableColumns: !includeColumns,
		PreviewOnly:    preview,
		RejectExisting: true,
		Environment:    relation.Environment,
	})
	if importErr != nil {
		status := http.StatusBadRequest
		code := "external_relation_import_failed"
		if strings.Contains(strings.ToLower(importErr.Error()), "already exists") {
			status = http.StatusConflict
			code = "external_relation_import_collision"
		}
		return ExternalRelationImportResult{}, &APIError{Status: status, Code: code, Message: importErr.Error()}
	}
	var imported directImportDatabaseResponse
	if err := json.Unmarshal(output, &imported); err != nil {
		return ExternalRelationImportResult{}, &APIError{
			Status:  http.StatusInternalServerError,
			Code:    "external_relation_import_response_invalid",
			Message: fmt.Sprintf("decode external relation import response: %v", err),
		}
	}
	if len(imported.Assets) != 1 {
		return ExternalRelationImportResult{}, &APIError{
			Status:  http.StatusConflict,
			Code:    "external_relation_import_selection_changed",
			Message: fmt.Sprintf("expected one proposed asset, got %d", len(imported.Assets)),
		}
	}
	asset := imported.Assets[0]
	warnings := make([]ExternalRelationImportWarning, 0, len(imported.Warnings))
	for _, warning := range imported.Warnings {
		warnings = append(warnings, ExternalRelationImportWarning(warning))
	}
	return ExternalRelationImportResult{
		Status:   "ok",
		Preview:  preview,
		Relation: *relation,
		Asset: ExternalRelationImportAsset{
			Name:    asset.Name,
			Path:    asset.Path,
			Type:    asset.Type,
			Columns: append([]SQLColumn(nil), asset.Columns...),
		},
		IncludeColumns: includeColumns,
		Warnings:       warnings,
		PipelinePath:   relPipelinePath,
	}, nil
}
