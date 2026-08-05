package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"

	"renart/internal/sqlintelligence"
	webmodel "renart/internal/web/model"
)

const (
	columnSourceDefinition   = "definition"
	columnSourceLiveResponse = "live_response"
	columnSourceMaterialized = "materialized"
)

const columnSourceContract = "contract"

func assetSchemaSourceProviders() []SchemaEvidenceProvider {
	return []SchemaEvidenceProvider{
		declaredSchemaSourceProvider{},
		apiDefinitionSchemaSourceProvider{},
		loadDefinitionSchemaSourceProvider{},
		seedDefinitionSchemaSourceProvider{},
		sqlDefinitionSchemaSourceProvider{},
		liveResponseSchemaSourceProvider{},
		materializedSchemaSourceProvider{},
	}
}

func columnInferenceSourcesForAsset(asset *pipeline.Asset, connectionName string) []webmodel.ColumnInferenceSource {
	if asset == nil || isSensorAssetType(asset.Type) {
		return []webmodel.ColumnInferenceSource{}
	}

	sources := make([]webmodel.ColumnInferenceSource, 0, 3)
	assetContext := AssetSchemaContext{Asset: asset, ConnectionName: connectionName}
	for _, provider := range assetSchemaSourceProviders() {
		if !provider.Matches(assetContext) {
			continue
		}
		capabilities := provider.Capabilities(assetContext)
		if capabilities.ExposeInSync {
			sources = append(sources, capabilities.Source)
		}
	}
	return sources
}

type declaredSchemaSourceProvider struct{}

func (declaredSchemaSourceProvider) ID() string { return columnSourceContract }

func (declaredSchemaSourceProvider) Matches(assetContext AssetSchemaContext) bool {
	// Imported source anchors do not own a materialized output contract, but
	// their explicitly declared columns are still a valid authoring schema for
	// Load and SQL consumers.
	return assetContext.Asset != nil && len(assetContext.Asset.Columns) > 0 && !isSensorAssetType(assetContext.Asset.Type)
}

func (declaredSchemaSourceProvider) Capabilities(AssetSchemaContext) SchemaEvidenceCapabilities {
	return SchemaEvidenceCapabilities{
		Source: webmodel.ColumnInferenceSource{
			ID: columnSourceContract, Label: "Declared columns", Category: "contract",
			Description: "The column contract committed in the asset definition.",
		},
		Stage: SchemaStageContract, Completeness: SchemaComplete, Confidence: SchemaConfidenceHigh,
	}
}

func (provider declaredSchemaSourceProvider) Observe(_ context.Context, request SchemaEvidenceRequest) (SchemaEvidence, *APIError) {
	capabilities := provider.Capabilities(request.Context)
	columns := PipelineColumnsToModelColumns(request.Context.Asset.Columns)
	scope := schemaEvidenceScopeFor(request.Context, request.Environment)
	return SchemaEvidence{
		Source: capabilities.Source, Stage: capabilities.Stage, Scope: scope,
		Completeness: capabilities.Completeness, Confidence: capabilities.Confidence,
		AssetRevision: schemaAssetRevision(request.Context.Asset), OutputIdentity: request.Context.OutputIdentity,
		ObservedAt: timeNowUTC(), Columns: columns,
	}, nil
}

func definitionCapabilities(label, description string, access SchemaEvidenceAccess) SchemaEvidenceCapabilities {
	return SchemaEvidenceCapabilities{
		Source: webmodel.ColumnInferenceSource{
			ID: columnSourceDefinition, Label: label, Category: "definition", Description: description,
		},
		Stage: SchemaStageDeclaration, Completeness: SchemaComplete, Confidence: SchemaConfidenceHigh,
		Access: access, ExposeInSync: true,
	}
}

func definitionEvidence(
	request SchemaEvidenceRequest,
	capabilities SchemaEvidenceCapabilities,
	columns []pipeline.Column,
) SchemaEvidence {
	scope := schemaEvidenceScopeFor(request.Context, request.Environment)
	return SchemaEvidence{
		Source: capabilities.Source, Stage: capabilities.Stage, Scope: scope,
		Completeness: capabilities.Completeness, Confidence: capabilities.Confidence,
		AssetRevision: schemaAssetRevision(request.Context.Asset), OutputIdentity: request.Context.OutputIdentity,
		ObservedAt: timeNowUTC(), Columns: PipelineColumnsToModelColumns(columns),
	}
}

type apiDefinitionSchemaSourceProvider struct{}

func (apiDefinitionSchemaSourceProvider) ID() string { return columnSourceDefinition }
func (apiDefinitionSchemaSourceProvider) Matches(assetContext AssetSchemaContext) bool {
	return schemaPolicyForAsset(assetContext.Asset).Kind == assetSchemaKindAPI
}
func (apiDefinitionSchemaSourceProvider) Capabilities(assetContext AssetSchemaContext) SchemaEvidenceCapabilities {
	access := SchemaEvidenceAccess{}
	if apiDefinitionNeedsNetwork(assetContext.Asset) {
		access.Network = true
	}
	return definitionCapabilities("Asset definition", "Declared response fields or the selected OpenAPI response schema.", access)
}
func (provider apiDefinitionSchemaSourceProvider) Observe(ctx context.Context, request SchemaEvidenceRequest) (SchemaEvidence, *APIError) {
	capabilities := provider.Capabilities(request.Context)
	if apiErr := requireSchemaEvidenceAccess(capabilities.Access, request.Allow); apiErr != nil {
		return SchemaEvidence{}, apiErr
	}
	columns := apiDefinitionColumns(ctx, request.Context.Asset, request.Allow.Network)
	if len(columns) == 0 {
		return SchemaEvidence{}, badRequestError("column_inference_failed", "API asset columns could not be inferred from response.fields or OpenAPI metadata")
	}
	return definitionEvidence(request, capabilities, columns), nil
}

type loadDefinitionSchemaSourceProvider struct{}

func (loadDefinitionSchemaSourceProvider) ID() string { return columnSourceDefinition }
func (loadDefinitionSchemaSourceProvider) Matches(assetContext AssetSchemaContext) bool {
	return schemaPolicyForAsset(assetContext.Asset).Kind == assetSchemaKindLoad
}
func (loadDefinitionSchemaSourceProvider) Capabilities(AssetSchemaContext) SchemaEvidenceCapabilities {
	return definitionCapabilities("Source asset", "The declaration schema of the load asset's upstream source.", SchemaEvidenceAccess{})
}
func (provider loadDefinitionSchemaSourceProvider) Observe(ctx context.Context, request SchemaEvidenceRequest) (SchemaEvidence, *APIError) {
	capabilities := provider.Capabilities(request.Context)
	if request.Context.Service != nil {
		columns, completeness, confidence, apiErr := request.Context.Service.inferGraphSchemaFromDefinition(ctx, request.Context.Pipeline, request.Context.Asset)
		if apiErr != nil {
			return SchemaEvidence{}, apiErr
		}
		capabilities.Completeness = completeness
		capabilities.Confidence = confidence
		return definitionEvidence(request, capabilities, ModelColumnsToPipelineColumns(columns)), nil
	}
	source := resolveLoadSourceAsset(request.Context.Pipeline, request.Context.Asset)
	if source == nil {
		return SchemaEvidence{}, badRequestError("load_source_unknown", "could not resolve a source asset; declare the source as a dependency to infer columns from it")
	}
	if request.Context.ResolveDefinition == nil {
		return SchemaEvidence{}, badRequestError("load_source_no_columns", "the source asset has no declaration schema to infer from")
	}
	columns := request.Context.ResolveDefinition(ctx, source)
	if len(columns) == 0 {
		return SchemaEvidence{}, badRequestError("load_source_no_columns", "the source asset has no declaration schema to infer from")
	}
	return definitionEvidence(request, capabilities, columns), nil
}

type seedDefinitionSchemaSourceProvider struct{}

func (seedDefinitionSchemaSourceProvider) ID() string { return columnSourceDefinition }
func (seedDefinitionSchemaSourceProvider) Matches(assetContext AssetSchemaContext) bool {
	return schemaPolicyForAsset(assetContext.Asset).Kind == assetSchemaKindLocalSeed
}
func (seedDefinitionSchemaSourceProvider) Capabilities(AssetSchemaContext) SchemaEvidenceCapabilities {
	return definitionCapabilities("Seed file", "The schema Sling detects in the local seed file.", SchemaEvidenceAccess{Filesystem: true})
}
func (provider seedDefinitionSchemaSourceProvider) Observe(ctx context.Context, request SchemaEvidenceRequest) (SchemaEvidence, *APIError) {
	capabilities := provider.Capabilities(request.Context)
	if apiErr := requireSchemaEvidenceAccess(capabilities.Access, request.Allow); apiErr != nil {
		return SchemaEvidence{}, apiErr
	}
	if request.Context.Service == nil {
		return SchemaEvidence{}, badRequestError("schema_provider_unavailable", "seed schema inspection is unavailable in this authoring context")
	}
	columns, apiErr := request.Context.Service.inferSeedColumnsFromSource(ctx, request.Context.Pipeline, request.Context.Asset)
	if apiErr != nil {
		return SchemaEvidence{}, apiErr
	}
	return definitionEvidence(request, capabilities, ModelColumnsToPipelineColumns(columns)), nil
}

type sqlDefinitionSchemaSourceProvider struct{}

func (sqlDefinitionSchemaSourceProvider) ID() string { return columnSourceDefinition }
func (sqlDefinitionSchemaSourceProvider) Matches(assetContext AssetSchemaContext) bool {
	return schemaPolicyForAsset(assetContext.Asset).Kind == assetSchemaKindSQL
}
func (sqlDefinitionSchemaSourceProvider) Capabilities(AssetSchemaContext) SchemaEvidenceCapabilities {
	return definitionCapabilities("SQL query", "The rendered query's output schema using declaration-only upstream columns.", SchemaEvidenceAccess{})
}
func (provider sqlDefinitionSchemaSourceProvider) Observe(ctx context.Context, request SchemaEvidenceRequest) (SchemaEvidence, *APIError) {
	capabilities := provider.Capabilities(request.Context)
	if request.Context.Service == nil {
		return SchemaEvidence{}, badRequestError("schema_provider_unavailable", "SQL projection inference is provided by the canonical authoring graph in this context")
	}
	columns, completeness, confidence, apiErr := request.Context.Service.inferGraphSchemaFromDefinition(ctx, request.Context.Pipeline, request.Context.Asset)
	if apiErr != nil {
		return SchemaEvidence{}, apiErr
	}
	capabilities.Completeness = completeness
	capabilities.Confidence = confidence
	return definitionEvidence(request, capabilities, ModelColumnsToPipelineColumns(columns)), nil
}

type liveResponseSchemaSourceProvider struct{}

func (liveResponseSchemaSourceProvider) ID() string { return columnSourceLiveResponse }

func (liveResponseSchemaSourceProvider) Matches(assetContext AssetSchemaContext) bool {
	return isAPIAsset(assetContext.Asset)
}

func (liveResponseSchemaSourceProvider) Capabilities(AssetSchemaContext) SchemaEvidenceCapabilities {
	return SchemaEvidenceCapabilities{
		Source: webmodel.ColumnInferenceSource{
			ID: columnSourceLiveResponse, Label: "Live request", Category: "observed",
			Description: "A sampled API response using the asset's current request settings.", MayOmitColumns: true,
		},
		Stage: SchemaStageRuntime, Completeness: SchemaPartial, Confidence: SchemaConfidenceMedium,
		Access: SchemaEvidenceAccess{Network: true}, ExposeInSync: true,
	}
}

func (provider liveResponseSchemaSourceProvider) Observe(ctx context.Context, request SchemaEvidenceRequest) (SchemaEvidence, *APIError) {
	capabilities := provider.Capabilities(request.Context)
	if apiErr := requireSchemaEvidenceAccess(capabilities.Access, request.Allow); apiErr != nil {
		return SchemaEvidence{}, apiErr
	}
	_, sample, apiErr := request.Context.Service.InferAPIAsset(ctx, request.Context.AssetID)
	if apiErr != nil {
		return SchemaEvidence{}, apiErr
	}
	count := sample.RecordsCount
	scope := schemaEvidenceScopeFor(request.Context, request.Environment)
	return SchemaEvidence{
		Source: capabilities.Source, Stage: capabilities.Stage, Scope: scope,
		Completeness: capabilities.Completeness, Confidence: capabilities.Confidence,
		AssetRevision: schemaAssetRevision(request.Context.Asset), OutputIdentity: request.Context.OutputIdentity,
		ObservedAt: timeNowUTC(), Columns: sample.Columns,
		Notes: append([]string(nil), sample.Warnings...), SampleRecords: &count,
	}, nil
}

type materializedSchemaSourceProvider struct{}

func (materializedSchemaSourceProvider) ID() string { return columnSourceMaterialized }

func (materializedSchemaSourceProvider) Matches(assetContext AssetSchemaContext) bool {
	return assetProducesSchemaContract(assetContext.Asset) && strings.TrimSpace(assetContext.ConnectionName) != ""
}

func (materializedSchemaSourceProvider) Capabilities(AssetSchemaContext) SchemaEvidenceCapabilities {
	return SchemaEvidenceCapabilities{
		Source: webmodel.ColumnInferenceSource{
			ID: columnSourceMaterialized, Label: "Current table", Category: "observed",
			Description: "The schema currently reported by the asset's warehouse relation.",
		},
		Stage: SchemaStageMaterialized, Completeness: SchemaComplete, Confidence: SchemaConfidenceHigh,
		Access: SchemaEvidenceAccess{Warehouse: true}, ExposeInSync: true,
	}
}

func (provider materializedSchemaSourceProvider) Observe(ctx context.Context, request SchemaEvidenceRequest) (SchemaEvidence, *APIError) {
	capabilities := provider.Capabilities(request.Context)
	if apiErr := requireSchemaEvidenceAccess(capabilities.Access, request.Allow); apiErr != nil {
		return SchemaEvidence{}, apiErr
	}
	assetContext := request.Context
	columns, _, apiErr := assetContext.Service.inferMaterializedAssetColumns(ctx, assetContext.Pipeline, assetContext.Asset, request.Environment)
	if apiErr == nil && (isAPIAsset(assetContext.Asset) || isLoadAsset(assetContext.Asset)) {
		columns, _ = withoutSlingLoadedAtColumn(columns)
	}
	scope := schemaEvidenceScopeFor(assetContext, request.Environment)
	return SchemaEvidence{
		Source: capabilities.Source, Stage: capabilities.Stage, Scope: scope,
		Completeness: capabilities.Completeness, Confidence: capabilities.Confidence,
		OutputIdentity: request.Context.OutputIdentity, ObservedAt: timeNowUTC(), Columns: columns,
	}, apiErr
}

func columnInferenceSourcesForPipelineAsset(asset *pipeline.Asset, parsedPipeline *pipeline.Pipeline) []webmodel.ColumnInferenceSource {
	connectionName := ""
	if parsedPipeline != nil {
		connectionName, _ = targetConnectionNameForAsset(asset, parsedPipeline)
	}
	return columnInferenceSourcesForAsset(asset, connectionName)
}

// PreviewAssetColumns observes one advertised schema source without mutating
// the asset, then compares it with the saved metadata.
func (s *AssetService) PreviewAssetColumns(
	ctx context.Context,
	assetID string,
	sourceID string,
	environment string,
) (webmodel.ColumnInferencePreview, *APIError) {
	_, parsedPipeline, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return webmodel.ColumnInferencePreview{}, badRequestError("asset_resolve_failed", err.Error())
	}

	sources := columnInferenceSourcesForPipelineAsset(asset, parsedPipeline)
	var source *webmodel.ColumnInferenceSource
	for index := range sources {
		if sources[index].ID == strings.TrimSpace(sourceID) {
			source = &sources[index]
			break
		}
	}
	if source == nil {
		return webmodel.ColumnInferencePreview{}, badRequestError("unsupported_column_source", "this schema source is not available for the asset")
	}

	evidence, apiErr := s.observeAssetColumnSource(
		ctx,
		assetID,
		parsedPipeline,
		asset,
		*source,
		environment,
	)
	if apiErr != nil {
		return webmodel.ColumnInferencePreview{}, apiErr
	}

	return webmodel.ColumnInferencePreview{
		Status:        "ok",
		Source:        *source,
		Columns:       evidence.Columns,
		Drift:         compareContractWithEvidence(asset.Columns, evidence),
		Notes:         evidence.Notes,
		SampleRecords: evidence.SampleRecords,
	}, nil
}

func (s *AssetService) observeAssetColumnSource(
	ctx context.Context,
	assetID string,
	parsedPipeline *pipeline.Pipeline,
	asset *pipeline.Asset,
	source webmodel.ColumnInferenceSource,
	environment string,
) (SchemaEvidence, *APIError) {
	connectionName := ""
	if parsedPipeline != nil {
		connectionName, _ = targetConnectionNameForAsset(asset, parsedPipeline)
	}
	assetContext := AssetSchemaContext{
		Service: s, AssetID: assetID, Pipeline: parsedPipeline, Asset: asset, ConnectionName: connectionName,
		OutputIdentity: s.schemaEvidenceOutputIdentity(parsedPipeline, asset, environment),
	}
	resolver := newAssetDefinitionSchemaResolver(parsedPipeline)
	assetContext.ResolveDefinition = resolver.Available
	for _, provider := range assetSchemaSourceProviders() {
		if provider.ID() != source.ID || !provider.Matches(assetContext) {
			continue
		}
		return provider.Observe(ctx, SchemaEvidenceRequest{
			Context: assetContext, Environment: environment,
			Allow: SchemaEvidenceAccess{Filesystem: true, Network: true, Warehouse: true},
		})
	}
	return SchemaEvidence{}, badRequestError("unsupported_column_source", fmt.Sprintf("unknown schema source %q", source.ID))
}

func withoutSlingLoadedAtColumn(columns []WorkspaceColumn) ([]WorkspaceColumn, bool) {
	filtered := make([]WorkspaceColumn, 0, len(columns))
	removed := false
	for _, column := range columns {
		if strings.EqualFold(strings.TrimSpace(column.Name), slingLoadedAtColumn) {
			removed = true
			continue
		}
		filtered = append(filtered, column)
	}
	if !removed {
		return columns, false
	}
	return filtered, true
}

func compareColumnSchemas(current []pipeline.Column, inferred []WorkspaceColumn) webmodel.ColumnSchemaDrift {
	result := webmodel.ColumnSchemaDrift{Items: []webmodel.ColumnSchemaDriftItem{}}
	currentByName := make(map[string]pipeline.Column, len(current))
	seen := make(map[string]struct{}, len(inferred))
	for _, column := range current {
		currentByName[strings.ToLower(strings.TrimSpace(column.Name))] = column
	}

	for _, column := range inferred {
		key := strings.ToLower(strings.TrimSpace(column.Name))
		if key == "" {
			continue
		}
		seen[key] = struct{}{}
		existing, ok := currentByName[key]
		if !ok {
			result.Added++
			result.Items = append(result.Items, webmodel.ColumnSchemaDriftItem{
				Column: column.Name, Kind: "added", InferredType: workspaceColumnType(column),
			})
			continue
		}
		if equivalentPipelineWorkspaceColumnType(existing, column) {
			result.Unchanged++
			continue
		}
		result.TypeChanged++
		result.Items = append(result.Items, webmodel.ColumnSchemaDriftItem{
			Column: column.Name, Kind: "type_changed", CurrentType: existing.SQLType(), InferredType: workspaceColumnType(column),
		})
	}

	for _, column := range current {
		key := strings.ToLower(strings.TrimSpace(column.Name))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		result.Removed++
		result.Items = append(result.Items, webmodel.ColumnSchemaDriftItem{
			Column: column.Name, Kind: "removed", CurrentType: column.SQLType(),
		})
	}
	return result
}

func equivalentColumnType(left, right string) bool {
	if equivalent, comparable, err := sqlintelligence.StrictDataTypesEquivalent(context.Background(), left, right, "generic"); err == nil && comparable {
		return equivalent
	}
	return canonicalColumnType(left) == canonicalColumnType(right)
}

func workspaceColumnType(column WorkspaceColumn) string {
	return (&pipeline.Column{
		Type: column.Type, Precision: column.Precision, Scale: column.Scale, Length: column.Length,
	}).SQLType()
}

func equivalentPipelineWorkspaceColumnType(left pipeline.Column, right WorkspaceColumn) bool {
	if !equivalentColumnType(left.SQLType(), workspaceColumnType(right)) {
		return false
	}
	return strings.TrimSpace(left.Collation) == "" || strings.TrimSpace(right.Collation) == "" ||
		strings.EqualFold(strings.TrimSpace(left.Collation), strings.TrimSpace(right.Collation))
}

func equivalentWorkspaceColumnType(left, right WorkspaceColumn) bool {
	if !equivalentColumnType(workspaceColumnType(left), workspaceColumnType(right)) {
		return false
	}
	return strings.TrimSpace(left.Collation) == "" || strings.TrimSpace(right.Collation) == "" ||
		strings.EqualFold(strings.TrimSpace(left.Collation), strings.TrimSpace(right.Collation))
}

func canonicalColumnType(value string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
	switch normalized {
	case "char", "character", "character varying", "nvarchar", "string", "text", "varchar":
		return "string"
	case "bool", "boolean":
		return "boolean"
	case "int", "int4", "int32", "integer":
		return "integer"
	case "bigint", "int8", "int64":
		return "bigint"
	case "decimal", "number", "numeric":
		return "decimal"
	case "datetime", "timestamp", "timestamp without time zone":
		return "datetime"
	case "json", "jsonb", "object", "variant":
		return "json"
	default:
		return normalized
	}
}
