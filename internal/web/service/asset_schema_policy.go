package service

import (
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
)

type assetSchemaKind string

const (
	assetSchemaKindNone       assetSchemaKind = "none"
	assetSchemaKindSQL        assetSchemaKind = "sql"
	assetSchemaKindAPI        assetSchemaKind = "api"
	assetSchemaKindLoad       assetSchemaKind = "load"
	assetSchemaKindLocalSeed  assetSchemaKind = "local_seed"
	assetSchemaKindRemoteSeed assetSchemaKind = "remote_seed"
	assetSchemaKindPython     assetSchemaKind = "python"
	assetSchemaKindIngestr    assetSchemaKind = "ingestr"
	assetSchemaKindGeneric    assetSchemaKind = "generic"
)

// assetSchemaPolicy is the single asset-kind classification consumed by
// authoring diagnostics and schema-evidence providers. Provider-specific code
// collects facts; this profile decides whether an output contract exists and
// which declaration family can describe it.
type assetSchemaPolicy struct {
	Kind             assetSchemaKind
	ProducesContract bool
}

func schemaPolicyForAsset(asset *pipeline.Asset) assetSchemaPolicy {
	if asset == nil || isSensorAssetType(asset.Type) || isSourceAssetType(asset.Type) {
		return assetSchemaPolicy{Kind: assetSchemaKindNone}
	}
	if isAPIAsset(asset) {
		return assetSchemaPolicy{Kind: assetSchemaKindAPI, ProducesContract: true}
	}
	if isLoadAsset(asset) {
		return assetSchemaPolicy{Kind: assetSchemaKindLoad, ProducesContract: true}
	}
	if asset.Type == pipeline.AssetTypeIngestr {
		return assetSchemaPolicy{Kind: assetSchemaKindIngestr, ProducesContract: true}
	}

	typeName := strings.ToLower(strings.TrimSpace(string(asset.Type)))
	if strings.HasSuffix(typeName, ".seed") {
		seedPath, _ := asset.Parameters.GetString("path")
		if isRemoteSchemaInput(seedPath) {
			return assetSchemaPolicy{Kind: assetSchemaKindRemoteSeed, ProducesContract: true}
		}
		return assetSchemaPolicy{Kind: assetSchemaKindLocalSeed, ProducesContract: true}
	}
	if _, err := AssetTypeToDialect(asset.Type); err == nil {
		return assetSchemaPolicy{
			Kind: assetSchemaKindSQL,
			ProducesContract: asset.Materialization.Type == pipeline.MaterializationTypeTable ||
				asset.Materialization.Type == pipeline.MaterializationTypeView,
		}
	}
	if asset.Type == pipeline.AssetTypePython {
		return assetSchemaPolicy{
			Kind: assetSchemaKindPython,
			ProducesContract: asset.Materialization.Type == pipeline.MaterializationTypeTable ||
				asset.Materialization.Type == pipeline.MaterializationTypeView,
		}
	}
	return assetSchemaPolicy{
		Kind: assetSchemaKindGeneric,
		ProducesContract: asset.Materialization.Type == pipeline.MaterializationTypeTable ||
			asset.Materialization.Type == pipeline.MaterializationTypeView,
	}
}

func isRemoteSchemaInput(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}
