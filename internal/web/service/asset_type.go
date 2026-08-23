package service

import (
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"renart/internal/bruincompat"
)

func sqlAssetTypeForIngestrDestination(destination string) (string, bool) {
	assetType, ok := pipeline.IngestrTypeConnectionMapping[normalizeConnectionType(destination)]
	if !ok {
		return "", false
	}

	return string(assetType), true
}

func sqlAssetTypeForConnectionName(parsedPipeline *pipeline.Pipeline, connectionName string) (string, bool) {
	if parsedPipeline == nil || strings.TrimSpace(connectionName) == "" {
		return "", false
	}

	for connectionType, configuredName := range parsedPipeline.DefaultConnections {
		if strings.EqualFold(strings.TrimSpace(configuredName), strings.TrimSpace(connectionName)) {
			return sqlAssetTypeForConnectionType(connectionType)
		}
	}

	return "", false
}

func sqlAssetTypeForConnectionType(connectionType string) (string, bool) {
	assetType, ok := queryAssetTypeForConnectionType(connectionType)
	if !ok {
		return "", false
	}
	return string(assetType), true
}

func queryAssetTypeForConnectionType(connectionType string) (pipeline.AssetType, bool) {
	return bruincompat.QueryAssetTypeForConnectionType(connectionType)
}

func sourceAssetTypeForConnectionType(connectionType string) (pipeline.AssetType, bool) {
	return bruincompat.SourceAssetTypeForConnectionType(connectionType)
}

func convertDirectSourceTypeToQueryType(sourceType pipeline.AssetType) pipeline.AssetType {
	connectionType, ok := pipeline.AssetTypeConnectionMapping[sourceType]
	if !ok {
		return sourceType
	}
	queryType, ok := queryAssetTypeForConnectionType(connectionType)
	if !ok {
		return sourceType
	}
	return queryType
}

func isQueryAssetType(assetType pipeline.AssetType) bool {
	return bruincompat.IsQueryAssetType(assetType)
}

func isSourceAssetType(assetType pipeline.AssetType) bool {
	return bruincompat.IsSourceAssetType(assetType)
}

func normalizeConnectionType(connectionType string) string {
	return bruincompat.NormalizeConnectionType(connectionType)
}
