package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/query"
	"github.com/spf13/afero"
)

func updateDirectAssetDependencies(ctx context.Context, asset *pipeline.Asset, p *pipeline.Pipeline, sp dependencyParser, renderer *jinja.Renderer, fs afero.Fs) error {
	if asset == nil || p == nil {
		return fmt.Errorf("pipeline and asset are required to update direct asset dependencies")
	}

	assetRenderer, err := renderer.CloneForAsset(ctx, p, asset)
	if err != nil {
		return fmt.Errorf("failed to create renderer for asset '%s': %w", asset.Name, err)
	}
	missingDeps, err := sp.GetMissingDependenciesForAsset(asset, p, assetRenderer)
	if err != nil {
		return fmt.Errorf("failed to get missing dependencies for asset '%s': %w", asset.Name, err)
	}
	if len(missingDeps) == 0 {
		return nil
	}
	for _, dep := range missingDeps {
		foundMissingUpstream := p.GetAssetByNameCaseInsensitive(dep)
		if foundMissingUpstream == nil || foundMissingUpstream.Name == asset.Name {
			continue
		}
		asset.AddUpstream(foundMissingUpstream)
	}
	return asset.Persist(fs)
}

func fillDirectColumnsFromDB(ctx context.Context, pp *directPipelineInfo, fs afero.Fs, environment string, manager config.ConnectionGetter) (string, error) {
	if pp == nil || pp.Pipeline == nil || pp.Asset == nil {
		return fillStatusFailed, fmt.Errorf("pipeline and asset are required to fill columns from DB")
	}

	var conn interface{}
	var err error
	if manager != nil {
		connName, err := pp.Pipeline.GetConnectionNameForAsset(pp.Asset)
		if err != nil {
			return fillStatusFailed, err
		}
		conn, err = resolveRuntimeConnection(manager, connName)
		if err != nil {
			return fillStatusFailed, err
		}
		if conn == nil {
			return fillStatusFailed, fmt.Errorf("failed to get connection for asset '%s'", pp.Asset.Name)
		}
	} else {
		selectedConfig, err := selectConfigEnvironment(pp.Config, environment)
		if err != nil {
			return fillStatusFailed, err
		}
		connectionManager, err := newConnectionManagerFromConfig(ctx, selectedConfig)
		if err != nil {
			return fillStatusFailed, err
		}
		connName, err := pp.Pipeline.GetConnectionNameForAsset(pp.Asset)
		if err != nil {
			return fillStatusFailed, err
		}
		conn, err = resolveRuntimeConnection(connectionManager, connName)
		if err != nil {
			return fillStatusFailed, err
		}
		if conn == nil {
			return fillStatusFailed, fmt.Errorf("failed to get connection for asset '%s'", pp.Asset.Name)
		}
	}

	querier, ok := conn.(interface {
		SelectWithSchema(context.Context, *query.Query) (*query.QueryResult, error)
	})
	if !ok {
		return fillStatusFailed, fmt.Errorf("connection for asset '%s' does not support schema introspection", pp.Asset.Name)
	}

	result, err := selectTableSchema(ctx, querier, pipeline.AssetTypeConnectionMapping[pp.Asset.Type], pp.Asset.Name)
	if err != nil {
		return fillStatusFailed, err
	}
	if result == nil || len(result.Columns) == 0 {
		return fillStatusFailed, fmt.Errorf("no columns found for asset '%s'", pp.Asset.Name)
	}

	if len(result.ColumnTypes) != len(result.Columns) {
		return fillStatusFailed, fmt.Errorf("database did not return types for every column")
	}
	for _, columnType := range result.ColumnTypes {
		if strings.TrimSpace(columnType) == "" {
			return fillStatusFailed, fmt.Errorf("database returned an unknown column type; existing declarations were kept")
		}
	}

	skipColumns := map[string]bool{"_is_current": true, "_valid_until": true, "_valid_from": true}
	existingColumns := make(map[string]pipeline.Column)
	for _, col := range pp.Asset.Columns {
		existingColumns[strings.ToLower(col.Name)] = col
	}
	if len(existingColumns) == 0 {
		columns := make([]pipeline.Column, 0, len(result.Columns))
		for i, colName := range result.Columns {
			if skipColumns[colName] {
				continue
			}
			columns = append(columns, pipeline.Column{Name: colName, Type: result.ColumnTypes[i], Checks: []pipeline.ColumnCheck{}, Upstreams: []*pipeline.UpstreamColumn{}})
		}
		pp.Asset.Columns = columns
		if err := pp.Asset.Persist(fs, pp.Pipeline); err != nil {
			return fillStatusFailed, err
		}
		return fillStatusUpdated, nil
	}

	hasChanges := false
	for i, colName := range result.Columns {
		if skipColumns[colName] {
			continue
		}
		lowerColName := strings.ToLower(colName)
		if existingCol, exists := existingColumns[lowerColName]; exists {
			if existingCol.Type != result.ColumnTypes[i] {
				for j := range pp.Asset.Columns {
					if strings.ToLower(pp.Asset.Columns[j].Name) == lowerColName {
						pp.Asset.Columns[j].Type = result.ColumnTypes[i]
						hasChanges = true
						break
					}
				}
			}
		} else {
			pp.Asset.Columns = append(pp.Asset.Columns, pipeline.Column{Name: colName, Type: result.ColumnTypes[i], Checks: []pipeline.ColumnCheck{}, Upstreams: []*pipeline.UpstreamColumn{}})
			hasChanges = true
		}
	}
	if !hasChanges {
		return fillStatusSkipped, nil
	}
	if err := pp.Asset.Persist(fs, pp.Pipeline); err != nil {
		return fillStatusFailed, err
	}
	return fillStatusUpdated, nil
}
