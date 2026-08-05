package service

import (
	"context"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"

	"renart/internal/web/service/assetmeta"
)

// ColumnReconcileResult is returned by ReconcileAssetColumns: the merged columns
// plus any situations the reconciler could not resolve automatically (stale
// columns that still carry user metadata).
type ColumnReconcileResult struct {
	Columns        []WorkspaceColumn         `json:"columns"`
	ReconcileItems []assetmeta.ReconcileItem `json:"reconcile_items,omitempty"`
}

// ReconcileAssetColumns merges a freshly inferred column set into the asset's
// existing columns through the assetmeta provenance model: user-authored
// metadata (descriptions, checks, primary keys) is preserved by name, inferred
// types refresh unless the user owns them, and columns that disappeared from
// inference but still carry user metadata are flagged rather than destroyed.
// The reconciled columns and provenance are persisted to the asset file.
func (s *AssetService) ReconcileAssetColumns(ctx context.Context, assetID string, inferred []WorkspaceColumn) (ColumnReconcileResult, *APIError) {
	return s.reconcileAssetColumns(ctx, assetID, func(_ *pipeline.Asset, _ *assetmeta.RenartMeta) ([]pipeline.Column, *APIError) {
		return ModelColumnsToPipelineColumns(inferred), nil
	})
}

type columnReconcileBuilder func(*pipeline.Asset, *assetmeta.RenartMeta) ([]pipeline.Column, *APIError)

func (s *AssetService) reconcileAssetColumns(
	ctx context.Context,
	assetID string,
	buildInferred columnReconcileBuilder,
) (ColumnReconcileResult, *APIError) {
	relAssetPath, err := DecodeID(assetID)
	if err != nil {
		return ColumnReconcileResult{}, badRequestError("invalid_asset_id", "invalid asset id")
	}
	absAssetPath, err := s.resolver().JoinPath(relAssetPath)
	if err != nil {
		return ColumnReconcileResult{}, badRequestError("invalid_asset_path", err.Error())
	}

	// Serialize against interactive saves and the async SQL patch for this file.
	unlock := s.lockAssetFile(absAssetPath)
	defer unlock()

	_, parsedPipeline, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return ColumnReconcileResult{}, badRequestError("asset_resolve_failed", err.Error())
	}
	meta := assetmeta.ParseAsset(asset)
	inferred, apiErr := buildInferred(asset, &meta)
	if apiErr != nil {
		return ColumnReconcileResult{}, apiErr
	}

	final, items, next := assetmeta.ReconcileColumns(assetmeta.ColumnReconcileInput{
		Inferred: inferred,
		Current:  asset.Columns,
		Prev:     meta,
	})
	asset.Columns = final
	next.ApplyToAsset(asset)
	if apiErr := loaderMaterializationAPIError(asset); apiErr != nil {
		return ColumnReconcileResult{}, apiErr
	}

	if apiErr := s.persistAssetPreservingInferredName(asset, parsedPipeline); apiErr != nil {
		return ColumnReconcileResult{}, apiErr
	}

	s.deps.SuppressWatcher(relAssetPath)
	if s.deps.PushWorkspaceUpdateImmediate != nil {
		s.deps.PushWorkspaceUpdateImmediate(ctx, "asset.columns.updated", relAssetPath)
	}

	return ColumnReconcileResult{
		Columns:        PipelineColumnsToModelColumns(final),
		ReconcileItems: items,
	}, nil
}

// persistAssetPreservingInferredName persists an asset, stripping the name field
// that bruin re-adds for assets whose name is inferred from their path (so the
// reconcile does not introduce a spurious explicit name).
func (s *AssetService) persistAssetPreservingInferredName(asset *pipeline.Asset, parsedPipeline *pipeline.Pipeline) *APIError {
	// YAML-defined assets (api/load/ingestr/plain-yaml) round-trip through the
	// node-preserving codec so editing metadata never clobbers the request spec,
	// the sling replication config, or any other unmanaged content. bruin's
	// Persist would instead rewrite the executable file from scratch.
	if isYAMLDefinedAsset(asset) {
		return s.persistYAMLAssetPreservingInferredName(asset)
	}

	originalHadExplicitName := assetContentHasExplicitName(asset.ExecutableFile.Content)
	if err := asset.Persist(s.fs(), parsedPipeline); err != nil {
		return internalError("asset_persist_failed", err.Error())
	}
	if !originalHadExplicitName {
		if err := removePersistedAssetNameField(asset); err != nil {
			return internalError("asset_persist_failed", err.Error())
		}
	}
	return nil
}

// persistYAMLAssetPreservingInferredName writes a YAML-defined asset's
// definition file via the codec, stripping the persisted `name:` only when the
// existing file had no explicit name (inferred from the path).
func (s *AssetService) persistYAMLAssetPreservingInferredName(asset *pipeline.Asset) *APIError {
	fs := s.fs()
	definitionPath := strings.TrimSpace(asset.DefinitionFile.Path)
	existing, _ := afero.ReadFile(fs, definitionPath)
	hadExplicitName := assetContentHasExplicitName(string(existing))

	if err := persistYAMLAssetDefinition(fs, asset); err != nil {
		return internalError("asset_persist_failed", err.Error())
	}

	if !hadExplicitName && definitionPath != "" {
		current, err := afero.ReadFile(fs, definitionPath)
		if err != nil {
			return internalError("asset_persist_failed", err.Error())
		}
		stripped := removeAssetNameFieldFromContent(string(current))
		if err := afero.WriteFile(fs, definitionPath, []byte(stripped), 0o644); err != nil {
			return internalError("asset_persist_failed", err.Error())
		}
	}
	return nil
}
