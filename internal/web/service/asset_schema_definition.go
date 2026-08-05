package service

import (
	"context"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"

	"renart/internal/web/service/assetmeta"
)

type assetDefinitionSchemaResolver struct {
	pipeline *pipeline.Pipeline
	visiting map[*pipeline.Asset]bool
	forecast map[string][]pipeline.Column
}

func newAssetDefinitionSchemaResolver(pp *pipeline.Pipeline) *assetDefinitionSchemaResolver {
	return &assetDefinitionSchemaResolver{
		pipeline: pp, visiting: make(map[*pipeline.Asset]bool),
		forecast: make(map[string][]pipeline.Column),
	}
}

func newAssetDefinitionSchemaResolverWithForecast(pp *pipeline.Pipeline, forecast map[string][]pipeline.Column) *assetDefinitionSchemaResolver {
	resolver := newAssetDefinitionSchemaResolver(pp)
	for name, columns := range forecast {
		resolver.forecast[strings.ToLower(strings.TrimSpace(name))] = append([]pipeline.Column(nil), columns...)
	}
	return resolver
}

// Available returns the best schema available from the committed definition.
// Explicit columns win; generated providers are consulted only when the asset
// has no declaration.
func (r *assetDefinitionSchemaResolver) Available(ctx context.Context, asset *pipeline.Asset) []pipeline.Column {
	return pipelineColumnsForSchemaEvidence(r.AvailableEvidence(ctx, asset))
}

// AvailableEvidence is the pure authoring entry point shared by type-check and
// LSP graph construction. It never opens a warehouse, performs network I/O, or
// executes user code.
func (r *assetDefinitionSchemaResolver) AvailableEvidence(ctx context.Context, asset *pipeline.Asset) SchemaEvidence {
	if asset == nil {
		return SchemaEvidence{}
	}
	if len(asset.Columns) == 0 {
		return r.GeneratedEvidence(ctx, asset)
	}
	assetContext := r.contextFor(asset)
	for _, provider := range assetSchemaSourceProviders() {
		if provider.ID() != columnSourceContract || !provider.Matches(assetContext) {
			continue
		}
		evidence, apiErr := provider.Observe(ctx, SchemaEvidenceRequest{Context: assetContext})
		if apiErr == nil {
			return evidence
		}
	}
	return SchemaEvidence{}
}

// Generated derives a schema without treating the target asset's own columns
// as inference. Recursive providers (currently Load) may still consume an
// upstream asset's declared or generated definition schema.
func (r *assetDefinitionSchemaResolver) Generated(ctx context.Context, asset *pipeline.Asset) []pipeline.Column {
	return pipelineColumnsForSchemaEvidence(r.GeneratedEvidence(ctx, asset))
}

func (r *assetDefinitionSchemaResolver) GeneratedEvidence(ctx context.Context, asset *pipeline.Asset) SchemaEvidence {
	if asset == nil || r.visiting[asset] {
		return SchemaEvidence{}
	}
	r.visiting[asset] = true
	defer delete(r.visiting, asset)

	assetContext := r.contextFor(asset)
	for _, provider := range assetSchemaSourceProviders() {
		if provider.ID() != columnSourceDefinition || !provider.Matches(assetContext) {
			continue
		}
		capabilities := provider.Capabilities(assetContext)
		if capabilities.Stage != SchemaStageDeclaration || requireSchemaEvidenceAccess(capabilities.Access, SchemaEvidenceAccess{}) != nil {
			continue
		}
		evidence, apiErr := provider.Observe(ctx, SchemaEvidenceRequest{Context: assetContext})
		if apiErr != nil {
			continue
		}
		columns := cleanDerivedSchemaColumns(pipelineColumnsForSchemaEvidence(evidence), asset)
		if len(columns) == 0 {
			continue
		}
		evidence.Columns = PipelineColumnsToModelColumns(columns)
		return evidence
	}
	return SchemaEvidence{}
}

func (r *assetDefinitionSchemaResolver) contextFor(asset *pipeline.Asset) AssetSchemaContext {
	connectionName := ""
	if r.pipeline != nil {
		connectionName, _ = targetConnectionNameForAsset(asset, r.pipeline)
	}
	return AssetSchemaContext{
		Pipeline: r.pipeline, Asset: asset, ConnectionName: connectionName,
		ResolveDefinition: r.availableForLoad,
	}
}

func (r *assetDefinitionSchemaResolver) availableForLoad(ctx context.Context, asset *pipeline.Asset) []pipeline.Column {
	if columns := r.Available(ctx, asset); len(columns) > 0 {
		return columns
	}
	return append([]pipeline.Column(nil), r.forecast[strings.ToLower(strings.TrimSpace(asset.Name))]...)
}

func cleanDerivedSchemaColumns(columns []pipeline.Column, asset *pipeline.Asset) []pipeline.Column {
	result := make([]pipeline.Column, 0, len(columns))
	seen := make(map[string]struct{}, len(columns))
	dropped := make(map[string]bool)
	if asset != nil {
		for _, name := range assetmeta.Parse(asset.Meta).ColDrop {
			dropped[strings.ToLower(strings.TrimSpace(name))] = true
		}
	}
	for _, column := range columns {
		name := strings.TrimSpace(column.Name)
		key := strings.ToLower(name)
		if key == "" || dropped[key] {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		column.Name = name
		result = append(result, column)
	}
	return result
}

// assetProducesSchemaContract reports whether an asset creates a relation whose
// output columns can be consumed downstream. Sensors and imported source
// anchors intentionally have no output contract of their own.
func assetProducesSchemaContract(asset *pipeline.Asset) bool {
	return schemaPolicyForAsset(asset).ProducesContract
}

func assetSchemaAutomaticallyInferable(ctx context.Context, pp *pipeline.Pipeline, asset *pipeline.Asset) bool {
	return assetSchemaAutomaticallyInferableWithVisited(ctx, pp, asset, make(map[*pipeline.Asset]bool))
}

func assetSchemaAutomaticallyInferableWithVisited(
	ctx context.Context,
	pp *pipeline.Pipeline,
	asset *pipeline.Asset,
	visiting map[*pipeline.Asset]bool,
) bool {
	if asset == nil || visiting[asset] {
		return false
	}
	if len(asset.Columns) > 0 {
		return true
	}
	visiting[asset] = true
	defer delete(visiting, asset)

	switch schemaPolicyForAsset(asset).Kind {
	case assetSchemaKindSQL:
		return true
	case assetSchemaKindAPI:
		return len(apiDefinitionColumns(ctx, asset, false)) > 0
	case assetSchemaKindLoad:
		return assetSchemaAutomaticallyInferableWithVisited(ctx, pp, resolveLoadSourceAsset(pp, asset), visiting)
	default:
		return false
	}
}
