package bruincompat

import (
	"fmt"

	"github.com/bruin-data/bruin/pkg/pipeline"
)

// AssetTypeToDialect mirrors Bruin's asset-to-parser dialect mapping without
// importing pkg/sqlparser, whose package-level CGo flags require its Rust
// static library even when RustSQLParser is never constructed.
func AssetTypeToDialect(assetType pipeline.AssetType) (string, error) {
	dialect, ok := ParserDialectForAssetType(assetType)
	if !ok {
		return "", fmt.Errorf("unsupported asset type %s", assetType)
	}
	return dialect, nil
}
