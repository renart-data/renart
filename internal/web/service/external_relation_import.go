package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"

	"renart/internal/web/service/assetmeta"
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

	relPipelinePath, absPipelinePath, decodeErr := s.resolver().DecodePipelineID(pipelineID)
	if decodeErr != nil {
		return ExternalRelationImportResult{}, &APIError{Status: http.StatusBadRequest, Code: "invalid_pipeline_id", Message: decodeErr.Error()}
	}
	includeColumns := true
	if req.IncludeColumns != nil {
		includeColumns = *req.IncludeColumns
	}
	output, importErr := s.externalImporter.ImportDatabase(ctx, ImportDatabaseRequest{
		PipelinePath:       relPipelinePath,
		ConnectionName:     relation.Connection,
		PreferredAssetName: relation.QualifiedName,
		Schema:             relation.SchemaName,
		Tables:             []string{relation.QualifiedName},
		DisableColumns:     !includeColumns,
		PreviewOnly:        preview,
		RejectExisting:     true,
		Environment:        relation.Environment,
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
	if !preview {
		warnings = append(warnings, s.bindImportedExternalRelationConsumers(ctx, absPipelinePath, *relation, asset.Name)...)
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

// bindImportedExternalRelationConsumers makes the reviewed import decision a
// durable pipeline edge. Discovery and import use the same warehouse-valid
// identity, so the pinned dependency is exact rather than a suffix mapping.
//
// Binding is deliberately connection-safe and scoped to this import. Renart
// never applies a global "drop the catalog" rule, which could connect a query
// to the wrong asset when multiple connections or catalogs expose the same
// schema/table suffix.
func (s *PipelineService) bindImportedExternalRelationConsumers(
	ctx context.Context,
	pipelinePath string,
	relation TypeCheckExternalRelation,
	importedAssetName string,
) []ExternalRelationImportWarning {
	parsed, err := s.newPipelineBuilder().CreatePipelineFromPath(ctx, pipelinePath, pipeline.WithMutate())
	if err != nil {
		return []ExternalRelationImportWarning{{
			Table:   importedAssetName,
			Warning: fmt.Sprintf("Imported the source asset, but could not reload the pipeline to attach its dependencies: %v", err),
		}}
	}

	importedAsset := getAssetByNameCaseInsensitiveLocal(parsed, importedAssetName)
	if importedAsset == nil {
		return []ExternalRelationImportWarning{{
			Table:   importedAssetName,
			Warning: "Imported the source asset, but it was not present when Renart reloaded the pipeline; its consumer dependencies were left unchanged.",
		}}
	}
	if !externalRelationMatchesImportedAsset(relation, importedAsset.Name) {
		return []ExternalRelationImportWarning{{
			Table:   importedAssetName,
			Warning: fmt.Sprintf("Imported the source asset, but %q is not the unambiguous authored name for %q; its consumer dependencies were left unchanged.", importedAsset.Name, relation.QualifiedName),
		}}
	}
	importedConnection, connectionErr := targetConnectionNameForAsset(importedAsset, parsed)
	if connectionErr != nil || !strings.EqualFold(strings.TrimSpace(importedConnection), strings.TrimSpace(relation.Connection)) {
		return []ExternalRelationImportWarning{{
			Table:   importedAssetName,
			Warning: fmt.Sprintf("Imported the source asset, but its connection %q does not match the observed connection %q; its consumer dependencies were left unchanged.", importedConnection, relation.Connection),
		}}
	}

	fs := afero.NewOsFs()
	warnings := make([]ExternalRelationImportWarning, 0)
	for _, consumerName := range relation.ReferencedByAssetNames {
		consumer := getAssetByNameCaseInsensitiveLocal(parsed, consumerName)
		if consumer == nil {
			warnings = append(warnings, ExternalRelationImportWarning{
				Table:   consumerName,
				Warning: fmt.Sprintf("Could not attach dependency %q because the referencing asset was no longer present.", importedAsset.Name),
			})
			continue
		}
		consumerConnection, err := targetConnectionNameForAsset(consumer, parsed)
		if err != nil || !strings.EqualFold(strings.TrimSpace(consumerConnection), strings.TrimSpace(relation.Connection)) {
			warnings = append(warnings, ExternalRelationImportWarning{
				Table:   consumer.Name,
				Warning: fmt.Sprintf("Did not attach dependency %q because the consumer connection %q does not match the observed connection %q.", importedAsset.Name, consumerConnection, relation.Connection),
			})
			continue
		}
		if !pinExternalRelationDependency(consumer, importedAsset.Name) {
			continue
		}

		originalHadExplicitName := assetContentHasExplicitName(consumer.ExecutableFile.Content)
		if err := consumer.Persist(fs, parsed); err != nil {
			warnings = append(warnings, ExternalRelationImportWarning{
				Table:   consumer.Name,
				Warning: fmt.Sprintf("Imported the source asset, but could not persist its dependency on %q: %v", importedAsset.Name, err),
			})
			continue
		}
		if isSQLAssetFile(consumer) && !originalHadExplicitName {
			if err := removePersistedAssetNameField(consumer); err != nil {
				warnings = append(warnings, ExternalRelationImportWarning{
					Table:   consumer.Name,
					Warning: fmt.Sprintf("Attached dependency %q, but could not restore the asset's inferred-name form: %v", importedAsset.Name, err),
				})
			}
		}
	}
	return warnings
}

func externalRelationMatchesImportedAsset(relation TypeCheckExternalRelation, importedAssetName string) bool {
	return strings.EqualFold(strings.TrimSpace(importedAssetName), strings.TrimSpace(relation.QualifiedName))
}

func pinExternalRelationDependency(asset *pipeline.Asset, upstreamName string) bool {
	if asset == nil || strings.TrimSpace(upstreamName) == "" || strings.EqualFold(strings.TrimSpace(asset.Name), strings.TrimSpace(upstreamName)) {
		return false
	}
	exists := false
	for _, upstream := range asset.Upstreams {
		if isAssetUpstream(upstream) && strings.EqualFold(strings.TrimSpace(upstream.Value), strings.TrimSpace(upstreamName)) {
			exists = true
			break
		}
	}

	upstream := pipeline.Upstream{Type: "asset", Value: strings.TrimSpace(upstreamName), Mode: pipeline.UpstreamModeFull}
	if !exists {
		asset.Upstreams = append(asset.Upstreams, upstream)
	}
	next := assetmeta.ParseAsset(asset)
	next.Version = assetmeta.SchemaVersion
	next.Generator = assetmeta.GeneratorVersion
	key := assetmeta.DependencyKey(upstream)
	matchKey := assetmeta.DependencyMatchKey(key)

	changed := !exists
	filteredDrops := next.DepDrop[:0]
	for _, existing := range next.DepDrop {
		if assetmeta.DependencyMatchKey(existing) != matchKey {
			filteredDrops = append(filteredDrops, existing)
		} else {
			changed = true
		}
	}
	next.DepDrop = filteredDrops
	for _, existing := range next.DepAdd {
		if assetmeta.DependencyMatchKey(existing) == matchKey {
			if changed {
				next.ApplyToAsset(asset)
			}
			return changed
		}
	}
	next.DepAdd = append(next.DepAdd, key)
	next.ApplyToAsset(asset)
	return true
}
