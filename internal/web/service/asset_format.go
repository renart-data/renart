package service

import (
	"context"
	"strings"

	"renart/internal/sqlformat"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
)

func (s *AssetService) FormatSQL(ctx context.Context, assetID string, req FormatSQLAssetRequest) (FormatSQLAssetResponse, *APIError) {
	relAssetPath, err := DecodeID(assetID)
	if err != nil {
		return FormatSQLAssetResponse{}, newAPIError(400, "invalid_asset_id", "invalid asset id")
	}
	isSQLFile := strings.HasSuffix(strings.ToLower(relAssetPath), ".sql")
	isQuerySensor := false
	if !isSQLFile {
		if s.deps.ResolveAssetByID == nil {
			return FormatSQLAssetResponse{}, newAPIError(400, "invalid_asset_type", "only SQL assets and query sensors can be formatted")
		}
		_, _, resolvedAsset, resolveErr := s.deps.ResolveAssetByID(ctx, assetID)
		if resolveErr != nil || resolvedAsset == nil || !isQuerySensorAssetType(resolvedAsset.Type) {
			return FormatSQLAssetResponse{}, newAPIError(400, "invalid_asset_type", "only SQL assets and query sensors can be formatted")
		}
		isQuerySensor = true
	}
	formattedSQL, err := sqlformat.Format(ctx, req.Content, s.sqlFormatDialect(ctx, assetID))
	if err != nil {
		return FormatSQLAssetResponse{Status: "error", AssetID: assetID, Content: req.Content, Error: err.Error()}, nil
	}
	if req.Persist != nil && !*req.Persist {
		return FormatSQLAssetResponse{Status: "ok", AssetID: assetID, Content: formattedSQL}, nil
	}
	if isQuerySensor {
		_, _, querySensor, resolveErr := s.deps.ResolveAssetByID(ctx, assetID)
		if resolveErr != nil || querySensor == nil || !isQuerySensorAssetType(querySensor.Type) {
			return FormatSQLAssetResponse{}, newAPIError(400, "asset_resolve_failed", "query sensor could not be resolved")
		}
		parameters := parameterStrings(querySensor.Parameters)
		parameters["query"] = formattedSQL
		if _, apiErr := s.Update(ctx, assetID, AssetUpdateRequest{Parameters: parameters}); apiErr != nil {
			return FormatSQLAssetResponse{}, apiErr
		}
		return FormatSQLAssetResponse{Status: "ok", AssetID: assetID, Content: formattedSQL}, nil
	}

	absAssetPath, err := s.resolver().JoinPath(relAssetPath)
	if err != nil {
		return FormatSQLAssetResponse{}, newAPIError(400, "invalid_asset_path", err.Error())
	}
	fs := s.fs()
	originalBytes, err := afero.ReadFile(fs, absAssetPath)
	if err != nil {
		return FormatSQLAssetResponse{}, newAPIError(500, "asset_read_failed", err.Error())
	}
	mergedContent := MergeExecutableContent(string(originalBytes), formattedSQL)
	if err := afero.WriteFile(fs, absAssetPath, []byte(mergedContent), 0o644); err != nil {
		return FormatSQLAssetResponse{}, newAPIError(500, "asset_write_failed", err.Error())
	}
	s.deps.SuppressWatcher(relAssetPath)
	if s.deps.PushAssetContentUpdateImmediate != nil {
		s.deps.PushAssetContentUpdateImmediate("asset.updated", relAssetPath, []string{assetID}, formattedSQL)
	} else {
		s.deps.PushWorkspaceUpdateImmediateWithChangedIDs(ctx, "asset.updated", relAssetPath, []string{assetID})
	}
	return FormatSQLAssetResponse{Status: "ok", AssetID: assetID, Content: formattedSQL}, nil
}

func (s *AssetService) sqlFormatDialect(ctx context.Context, assetID string) string {
	if s.deps.ResolveAssetByID == nil {
		return sqlformat.DialectGeneric
	}
	_, _, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil || asset == nil {
		return sqlformat.DialectGeneric
	}
	return sqlFormatDialectForAssetType(asset.Type)
}

func sqlFormatDialectForAssetType(assetType pipeline.AssetType) string {
	dialect, err := AssetTypeToDialect(assetType)
	if err != nil {
		return sqlformat.DialectGeneric
	}
	switch strings.ToLower(strings.TrimSpace(dialect)) {
	case "bigquery":
		return "bigquery"
	case "snowflake":
		return "snowflake"
	case "postgres":
		return "postgresql"
	case "redshift":
		return "redshift"
	case "trino":
		return "trino"
	case "athena":
		return "athena"
	case "clickhouse":
		return "clickhouse"
	case "databricks":
		return "databricks"
	case "tsql":
		return "tsql"
	case "duckdb":
		return "duckdb"
	default:
		return sqlformat.DialectGeneric
	}
}
