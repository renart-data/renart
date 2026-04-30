package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/sqlparser"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyManualAssetUpstreamsPreservesTrackedInferred(t *testing.T) {
	t.Parallel()

	asset := &pipeline.Asset{
		Name: "analytics.customers",
		Meta: pipeline.EmptyStringMap{
			renartInferredUpstreamsMetaKey: "analytics.orders",
		},
		Upstreams: []pipeline.Upstream{
			{Type: "asset", Value: "analytics.manual_seed", Mode: pipeline.UpstreamModeFull},
			{Type: "asset", Value: "analytics.orders", Mode: pipeline.UpstreamModeFull},
		},
	}
	p := &pipeline.Pipeline{
		Assets: []*pipeline.Asset{
			{Name: "analytics.customers"},
			{Name: "analytics.manual_seed"},
			{Name: "analytics.orders"},
		},
	}

	applyManualAssetUpstreams(asset, p, []string{"analytics.manual_seed"})

	assert.Equal(t, []string{"analytics.manual_seed", "analytics.orders"}, upstreamValues(asset.Upstreams))
	assert.Equal(t, "analytics.orders", asset.Meta[renartInferredUpstreamsMetaKey])
}

func TestDeriveSQLAssetTypeForIngestrSourceUsesDestinationType(t *testing.T) {
	t.Parallel()

	assetType := deriveSQLAssetTypeForSource(&pipeline.Asset{
		Type:       pipeline.AssetType("ingestr"),
		Parameters: map[string]string{"destination": "duckdb"},
	}, nil, "duckdb-default")

	assert.Equal(t, "duckdb.sql", assetType)
}

func TestDeriveSQLAssetTypeForPythonSourceDoesNotUseConnectionNameAsType(t *testing.T) {
	t.Parallel()

	assetType := deriveSQLAssetTypeForSource(&pipeline.Asset{
		Type: pipeline.AssetType("python"),
	}, nil, "duckdb-default")

	assert.Equal(t, "duckdb.sql", assetType)
}

func TestReconcileSQLAssetDependenciesRemovesOnlyTrackedInferred(t *testing.T) {
	t.Parallel()

	customers := &pipeline.Asset{Name: "analytics.customers"}
	manual := &pipeline.Asset{Name: "analytics.manual_seed"}
	asset := &pipeline.Asset{
		Name: "analytics.orders_report",
		Type: pipeline.AssetTypeDuckDBQuery,
		ExecutableFile: pipeline.ExecutableFile{
			Path:    filepath.Join(t.TempDir(), "orders_report.sql"),
			Content: "select * from analytics.manual_seed",
		},
		Meta: pipeline.EmptyStringMap{
			renartInferredUpstreamsMetaKey: "analytics.customers",
		},
		Upstreams: []pipeline.Upstream{
			{Type: "asset", Value: "analytics.manual_seed", Mode: pipeline.UpstreamModeFull},
			{Type: "asset", Value: "analytics.customers", Mode: pipeline.UpstreamModeFull},
		},
	}
	p := &pipeline.Pipeline{
		Name:   "analytics",
		Assets: []*pipeline.Asset{asset, customers, manual},
	}

	parser, err := sqlparser.NewSQLParser(false)
	require.NoError(t, err)
	defer parser.Close()

	renderer := jinja.NewRendererWithYesterday("analytics", "test-run")
	require.NoError(t, reconcileSQLAssetDependencies(context.Background(), asset, p, parser, renderer))

	assert.Equal(t, []string{"analytics.manual_seed"}, upstreamValues(asset.Upstreams))
	_, ok := asset.Meta[renartInferredUpstreamsMetaKey]
	assert.False(t, ok)
}

func TestAssetServiceReconcileSQLAssetDependenciesPersistsInferredUpstreams(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	pipelineRoot := filepath.Join(workspaceRoot, "analytics")
	assetsRoot := filepath.Join(pipelineRoot, "assets")
	require.NoError(t, os.MkdirAll(assetsRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte(strings.TrimSpace(`
name: analytics
schedule: daily
start_date: "2024-01-01"
default_connections:
  duckdb: duckdb-default
`)+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(assetsRoot, "customers.sql"), []byte(strings.TrimSpace(`
/* @bruin
name: analytics.customers
type: duckdb.sql
materialization:
  type: view
@bruin */

select *
from analytics.orders
`)+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(assetsRoot, "orders.sql"), []byte(strings.TrimSpace(`
/* @bruin
name: analytics.orders
type: duckdb.sql
materialization:
  type: view
@bruin */

select 1 as order_id
`)+"\n"), 0o644))

	buildPipeline := func(ctx context.Context, pipelinePath string) (*pipeline.Pipeline, error) {
		osFS := afero.NewOsFs()
		builder := pipeline.NewBuilder(
			BuilderConfig,
			pipeline.CreateTaskFromYamlDefinition(osFS),
			pipeline.CreateTaskFromFileComments(osFS),
			osFS,
			nil,
		)
		return builder.CreatePipelineFromPath(ctx, pipelinePath, pipeline.WithMutate())
	}

	resolveAssetByID := func(ctx context.Context, assetID string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
		relAssetPath, err := DecodeID(assetID)
		if err != nil {
			return "", nil, nil, err
		}

		assetPath, err := SafeJoin(workspaceRoot, relAssetPath)
		if err != nil {
			return "", nil, nil, err
		}

		pipelinePath := filepath.Dir(filepath.Dir(assetPath))
		parsedPipeline, err := buildPipeline(ctx, pipelinePath)
		if err != nil {
			return "", nil, nil, err
		}

		normalizedTarget := filepath.ToSlash(relAssetPath)
		for _, asset := range parsedPipeline.Assets {
			currentPath := asset.ExecutableFile.Path
			if currentPath == "" {
				currentPath = asset.DefinitionFile.Path
			}

			relCurrent, relErr := filepath.Rel(workspaceRoot, currentPath)
			if relErr != nil {
				continue
			}
			if filepath.ToSlash(relCurrent) == normalizedTarget {
				return normalizedTarget, parsedPipeline, asset, nil
			}
		}

		return "", nil, nil, ErrAssetNotFound
	}

	service := NewAssetService(AssetDependencies{
		WorkspaceRoot:    workspaceRoot,
		ResolveAssetByID: resolveAssetByID,
	})

	require.NoError(t, service.reconcileSQLAssetDependencies(context.Background(), "analytics/assets/customers.sql"))

	content, err := os.ReadFile(filepath.Join(assetsRoot, "customers.sql"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "depends:\n  - analytics.orders")
	assert.Contains(t, string(content), "renart_inferred_upstreams: analytics.orders")

	_, parsedPipeline, asset, err := resolveAssetByID(context.Background(), EncodeID("analytics/assets/customers.sql"))
	require.NoError(t, err)
	require.NotNil(t, parsedPipeline)
	assert.Equal(t, []string{"analytics.orders"}, upstreamValues(asset.Upstreams))
	assert.Equal(t, "analytics.orders", asset.Meta[renartInferredUpstreamsMetaKey])
}

func TestAssetServiceUpdatePersistsManualUpstreamsInHeader(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	pipelineRoot := filepath.Join(workspaceRoot, "analytics")
	assetsRoot := filepath.Join(pipelineRoot, "assets")
	require.NoError(t, os.MkdirAll(assetsRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte(strings.TrimSpace(`
name: analytics
schedule: daily
start_date: "2024-01-01"
default_connections:
  duckdb: duckdb-default
`)+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(assetsRoot, "customers.sql"), []byte(strings.TrimSpace(`
/* @bruin
name: analytics.customers
type: duckdb.sql
materialization:
  type: view
@bruin */

select 1 as customer_id
`)+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(assetsRoot, "manual_seed.sql"), []byte(strings.TrimSpace(`
/* @bruin
name: analytics.manual_seed
type: duckdb.sql
materialization:
  type: view
@bruin */

select 1 as seed_id
`)+"\n"), 0o644))

	buildPipeline := func(ctx context.Context, pipelinePath string) (*pipeline.Pipeline, error) {
		osFS := afero.NewOsFs()
		builder := pipeline.NewBuilder(
			BuilderConfig,
			pipeline.CreateTaskFromYamlDefinition(osFS),
			pipeline.CreateTaskFromFileComments(osFS),
			osFS,
			nil,
		)
		return builder.CreatePipelineFromPath(ctx, pipelinePath, pipeline.WithMutate())
	}

	resolveAssetByID := func(ctx context.Context, assetID string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
		relAssetPath, err := DecodeID(assetID)
		if err != nil {
			return "", nil, nil, err
		}

		assetPath, err := SafeJoin(workspaceRoot, relAssetPath)
		if err != nil {
			return "", nil, nil, err
		}

		pipelinePath := filepath.Dir(filepath.Dir(assetPath))
		parsedPipeline, err := buildPipeline(ctx, pipelinePath)
		if err != nil {
			return "", nil, nil, err
		}

		normalizedTarget := filepath.ToSlash(relAssetPath)
		for _, asset := range parsedPipeline.Assets {
			currentPath := asset.ExecutableFile.Path
			if currentPath == "" {
				currentPath = asset.DefinitionFile.Path
			}

			relCurrent, relErr := filepath.Rel(workspaceRoot, currentPath)
			if relErr != nil {
				continue
			}
			if filepath.ToSlash(relCurrent) == normalizedTarget {
				return normalizedTarget, parsedPipeline, asset, nil
			}
		}

		return "", nil, nil, ErrAssetNotFound
	}

	service := NewAssetService(AssetDependencies{
		WorkspaceRoot:    workspaceRoot,
		ResolveAssetByID: resolveAssetByID,
		SuppressWatcher:  func(string) {},
		PushWorkspaceUpdateImmediateWithChangedIDs: func(context.Context, string, string, []string) {},
	})

	content := "select 1 as customer_id"
	_, apiErr := service.Update(context.Background(), EncodeID("analytics/assets/customers.sql"), AssetUpdateRequest{
		Content:   &content,
		Upstreams: []string{"analytics.manual_seed"},
	})
	require.Nil(t, apiErr)

	fileContent, err := os.ReadFile(filepath.Join(assetsRoot, "customers.sql"))
	require.NoError(t, err)
	assert.Contains(t, string(fileContent), "depends:\n  - analytics.manual_seed")

	_, _, asset, err := resolveAssetByID(context.Background(), EncodeID("analytics/assets/customers.sql"))
	require.NoError(t, err)
	assert.Equal(t, []string{"analytics.manual_seed"}, upstreamValues(asset.Upstreams))
}

func TestAssetServiceCreateReconcilesSQLDependenciesImmediately(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	pipelineRoot := filepath.Join(workspaceRoot, "analytics")
	assetsRoot := filepath.Join(pipelineRoot, "assets")
	require.NoError(t, os.MkdirAll(assetsRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte(strings.TrimSpace(`
name: analytics
schedule: daily
start_date: "2024-01-01"
default_connections:
  duckdb: duckdb-default
`)+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(assetsRoot, "orders.sql"), []byte(strings.TrimSpace(`
/* @bruin
name: analytics.orders
type: duckdb.sql
materialization:
  type: view
@bruin */

select 1 as order_id
`)+"\n"), 0o644))

	buildPipeline := func(ctx context.Context, pipelinePath string) (*pipeline.Pipeline, error) {
		osFS := afero.NewOsFs()
		builder := pipeline.NewBuilder(
			BuilderConfig,
			pipeline.CreateTaskFromYamlDefinition(osFS),
			pipeline.CreateTaskFromFileComments(osFS),
			osFS,
			nil,
		)
		return builder.CreatePipelineFromPath(ctx, pipelinePath, pipeline.WithMutate())
	}

	resolveAssetByID := func(ctx context.Context, assetID string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
		relAssetPath, err := DecodeID(assetID)
		if err != nil {
			return "", nil, nil, err
		}

		assetPath, err := SafeJoin(workspaceRoot, relAssetPath)
		if err != nil {
			return "", nil, nil, err
		}

		pipelinePath := filepath.Dir(filepath.Dir(assetPath))
		parsedPipeline, err := buildPipeline(ctx, pipelinePath)
		if err != nil {
			return "", nil, nil, err
		}

		normalizedTarget := filepath.ToSlash(relAssetPath)
		for _, asset := range parsedPipeline.Assets {
			currentPath := asset.ExecutableFile.Path
			if currentPath == "" {
				currentPath = asset.DefinitionFile.Path
			}

			relCurrent, relErr := filepath.Rel(workspaceRoot, currentPath)
			if relErr != nil {
				continue
			}
			if filepath.ToSlash(relCurrent) == normalizedTarget {
				return normalizedTarget, parsedPipeline, asset, nil
			}
		}

		return "", nil, nil, ErrAssetNotFound
	}

	service := NewAssetService(AssetDependencies{
		WorkspaceRoot:                workspaceRoot,
		ResolveAssetByID:             resolveAssetByID,
		DefaultAssetContent:          DefaultAssetContent,
		DerivedAssetContent:          DefaultDerivedSQLAssetContent,
		EnsurePythonRequirements:     func(string, string, string) error { return nil },
		SuppressWatcher:              func(string) {},
		PushWorkspaceUpdateImmediate: func(context.Context, string, string) {},
	})

	response, apiErr := service.Create(context.Background(), EncodeID("analytics"), CreateAssetParams{
		SourceAssetID: EncodeID("analytics/assets/orders.sql"),
	})
	require.Nil(t, apiErr)
	require.Equal(t, "ok", response["status"])
	require.Equal(t, "analytics/assets/analytics_orders_child_1.sql", response["asset_path"])

	assetPath := filepath.Join(workspaceRoot, filepath.FromSlash(response["asset_path"]))
	content, err := os.ReadFile(assetPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "depends:\n  - analytics.orders")
	assert.Contains(t, string(content), "renart_inferred_upstreams: analytics.orders")

	_, _, asset, err := resolveAssetByID(context.Background(), response["asset_id"])
	require.NoError(t, err)
	assert.Equal(t, []string{"analytics.orders"}, upstreamValues(asset.Upstreams))
	assert.Equal(t, "analytics.orders", asset.Meta[renartInferredUpstreamsMetaKey])
	assert.Equal(t, "analytics.orders_child_1", asset.Name)
}

func TestAssetServiceUpdateReconcilesSQLDependenciesImmediately(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	pipelineRoot := filepath.Join(workspaceRoot, "analytics")
	assetsRoot := filepath.Join(pipelineRoot, "assets")
	require.NoError(t, os.MkdirAll(assetsRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte(strings.TrimSpace(`
name: analytics
schedule: daily
start_date: "2024-01-01"
default_connections:
  duckdb: duckdb-default
`)+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(assetsRoot, "orders.sql"), []byte(strings.TrimSpace(`
/* @bruin
name: analytics.orders
type: duckdb.sql
materialization:
  type: view
@bruin */

select 1 as order_id
`)+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(assetsRoot, "customers.sql"), []byte(strings.TrimSpace(`
/* @bruin
name: analytics.customers
type: duckdb.sql
materialization:
  type: view
depends:
  - analytics.manual_seed
@bruin */

select 1 as customer_id
`)+"\n"), 0o644))

	buildPipeline := func(ctx context.Context, pipelinePath string) (*pipeline.Pipeline, error) {
		osFS := afero.NewOsFs()
		builder := pipeline.NewBuilder(
			BuilderConfig,
			pipeline.CreateTaskFromYamlDefinition(osFS),
			pipeline.CreateTaskFromFileComments(osFS),
			osFS,
			nil,
		)
		return builder.CreatePipelineFromPath(ctx, pipelinePath, pipeline.WithMutate())
	}

	resolveAssetByID := func(ctx context.Context, assetID string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
		relAssetPath, err := DecodeID(assetID)
		if err != nil {
			return "", nil, nil, err
		}

		assetPath, err := SafeJoin(workspaceRoot, relAssetPath)
		if err != nil {
			return "", nil, nil, err
		}

		pipelinePath := filepath.Dir(filepath.Dir(assetPath))
		parsedPipeline, err := buildPipeline(ctx, pipelinePath)
		if err != nil {
			return "", nil, nil, err
		}

		normalizedTarget := filepath.ToSlash(relAssetPath)
		for _, asset := range parsedPipeline.Assets {
			currentPath := asset.ExecutableFile.Path
			if currentPath == "" {
				currentPath = asset.DefinitionFile.Path
			}

			relCurrent, relErr := filepath.Rel(workspaceRoot, currentPath)
			if relErr != nil {
				continue
			}
			if filepath.ToSlash(relCurrent) == normalizedTarget {
				return normalizedTarget, parsedPipeline, asset, nil
			}
		}

		return "", nil, nil, ErrAssetNotFound
	}

	service := NewAssetService(AssetDependencies{
		WorkspaceRoot:    workspaceRoot,
		ResolveAssetByID: resolveAssetByID,
		SuppressWatcher:  func(string) {},
		PushWorkspaceUpdateImmediateWithChangedIDs: func(context.Context, string, string, []string) {},
	})

	content := "select *\nfrom analytics.orders\n"
	_, apiErr := service.Update(context.Background(), EncodeID("analytics/assets/customers.sql"), AssetUpdateRequest{
		Content: &content,
	})
	require.Nil(t, apiErr)

	_, _, asset, err := resolveAssetByID(context.Background(), EncodeID("analytics/assets/customers.sql"))
	require.NoError(t, err)
	assert.Equal(t, []string{"analytics.manual_seed", "analytics.orders"}, upstreamValues(asset.Upstreams))
	assert.Equal(t, "analytics.orders", asset.Meta[renartInferredUpstreamsMetaKey])
}

func TestReconcileSQLAssetDependenciesResolvesSameSchemaUnqualifiedNames(t *testing.T) {
	t.Parallel()

	orders := &pipeline.Asset{Name: "analytics.orders"}
	asset := &pipeline.Asset{
		Name: "analytics.customers",
		Type: pipeline.AssetTypeDuckDBQuery,
		ExecutableFile: pipeline.ExecutableFile{
			Path:    filepath.Join(t.TempDir(), "customers.sql"),
			Content: "select * from orders",
		},
	}
	p := &pipeline.Pipeline{
		Name:   "analytics",
		Assets: []*pipeline.Asset{asset, orders},
	}

	parser, err := sqlparser.NewSQLParser(false)
	require.NoError(t, err)
	defer parser.Close()

	renderer := jinja.NewRendererWithYesterday("analytics", "test-run")
	require.NoError(t, reconcileSQLAssetDependencies(context.Background(), asset, p, parser, renderer))

	assert.Equal(t, []string{"analytics.orders"}, upstreamValues(asset.Upstreams))
	assert.Equal(t, "analytics.orders", asset.Meta[renartInferredUpstreamsMetaKey])
}

func TestDeriveDownstreamAssetName_PreservesPrefix(t *testing.T) {
	t.Parallel()

	pl := &pipeline.Pipeline{
		Assets: []*pipeline.Asset{
			{Name: "marts.orders"},
			{Name: "marts.orders_child_1"},
		},
	}

	assert.Equal(t, "marts.orders_child_2", deriveDownstreamAssetName("marts.orders", pl))
}

func upstreamValues(upstreams []pipeline.Upstream) []string {
	values := make([]string, 0, len(upstreams))
	for _, upstream := range upstreams {
		values = append(values, upstream.Value)
	}
	return values
}
