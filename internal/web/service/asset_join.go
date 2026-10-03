package service

import (
	"context"
	"strings"
)

// JoinUpstreamRequest names the asset an existing SQL asset should join.
// renart:web
type JoinUpstreamRequest struct {
	SourceAssetID string `json:"source_asset_id"`
}

// JoinUpstream makes a SQL asset read another asset of its pipeline by joining
// it in the query, so the dependency stays inferred from SQL. An empty query
// becomes the downstream starter over the source instead.
func (s *AssetService) JoinUpstream(ctx context.Context, assetID string, req JoinUpstreamRequest) (AssetMutationResponse, *APIError) {
	if s.deps.ResolveAssetByID == nil {
		return AssetMutationResponse{}, internalError("asset_resolver_unavailable", "asset resolver is unavailable")
	}
	_, targetPipeline, target, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return AssetMutationResponse{}, newAPIError(404, "asset_not_found", err.Error())
	}
	if !target.IsSQLAsset() {
		return AssetMutationResponse{}, newAPIError(400, "join_requires_sql_asset", "only a SQL asset can join another asset in its query")
	}
	sourceID := strings.TrimSpace(req.SourceAssetID)
	if sourceID == "" || sourceID == assetID {
		return AssetMutationResponse{}, newAPIError(400, "invalid_source_asset_id", "choose another asset to join")
	}
	_, sourcePipeline, source, err := s.deps.ResolveAssetByID(ctx, sourceID)
	if err != nil {
		return AssetMutationResponse{}, newAPIError(400, "invalid_source_asset_id", err.Error())
	}
	if !pipelinePathsReferToSameRoot(sourcePipeline.DefinitionFile.Path, targetPipeline.DefinitionFile.Path) {
		return AssetMutationResponse{}, newAPIError(400, "invalid_source_asset", "the joined asset must belong to the same pipeline")
	}
	for _, upstream := range target.Upstreams {
		if strings.EqualFold(strings.TrimSpace(upstream.Value), source.Name) {
			return AssetMutationResponse{}, newAPIError(409, "already_upstream", target.Name+" already depends on "+source.Name)
		}
	}

	style := projectSQLStarterStyle(targetPipeline, string(target.Type))
	if style.Dialect == "tsql" || style.Dialect == "fabric" {
		return AssetMutationResponse{}, newAPIError(400, "join_not_supported", "this dialect cannot nest the query in a CTE; add a dependency instead")
	}
	query := target.ExecutableFile.Content
	resolver := newAssetDefinitionSchemaResolver(targetPipeline)
	sourceColumns := sqlStarterSource{Name: source.Name, Columns: resolver.Available(ctx, source)}
	var next string
	if strings.TrimSpace(query) == "" {
		next = downstreamSQLStarter([]sqlStarterSource{sourceColumns}, style)
	} else {
		next = joinedSQLQuery(target.Name, query, resolver.Available(ctx, target), sourceColumns, style)
	}
	return s.Update(ctx, assetID, AssetUpdateRequest{Content: &next})
}
