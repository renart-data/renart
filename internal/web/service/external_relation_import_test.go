package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/ansisql"
	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/web/service/assetmeta"
)

type recordingExternalRelationImporter struct {
	requests []ImportDatabaseRequest
}

func (i *recordingExternalRelationImporter) ImportDatabase(_ context.Context, req ImportDatabaseRequest) ([]byte, error) {
	i.requests = append(i.requests, req)
	assetName := strings.TrimSpace(req.PreferredAssetName)
	if assetName == "" {
		assetName = "external.orders"
	}
	return json.Marshal(directImportDatabaseResponse{
		Status:       "ok",
		Preview:      req.PreviewOnly,
		PipelinePath: req.PipelinePath,
		Assets: []directImportAsset{{
			Name:    assetName,
			Path:    "analytics/assets/external/orders.asset.yml",
			Type:    "duckdb.source",
			Columns: []SQLColumn{{Name: "order_id", Type: "bigint"}},
		}},
	})
}

func TestExternalRelationImportUsesPositiveTypeCheckIdentityAndIncludesColumnsByDefault(t *testing.T) {
	t.Parallel()
	_, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"report.sql": `
/* @bruin
name: analytics.report
type: duckdb.sql
connection: duckdb-default
materialization:
  type: view
@bruin */
select order_id from external.orders
`,
	})
	provider := &stubRemoteCatalogProvider{snapshot: RemoteCatalogSnapshot{Relations: []RemoteCatalogRelation{{
		QualifiedName: "external.orders",
		ShortName:     "orders",
		SchemaName:    "external",
		ColumnsKnown:  true,
		Columns:       []SQLColumn{{Name: "order_id", Type: "bigint"}},
	}}}}
	importer := &recordingExternalRelationImporter{}
	service := NewPipelineService(root)
	service.SetRemoteCatalogProvider(provider, func() string { return "dev" })
	service.SetExternalRelationImporter(importer)
	relationID := remoteCatalogRelationID(RemoteCatalogScope{Connection: "duckdb-default", Environment: "dev"}, "external.orders")

	preview, apiErr := service.PreviewExternalRelationImport(context.Background(), EncodeID("analytics"), ExternalRelationImportRequest{
		RelationID: relationID,
	})
	require.Nil(t, apiErr)
	assert.True(t, preview.Preview)
	assert.True(t, preview.IncludeColumns)
	assert.Equal(t, relationID, preview.Relation.ID)
	assert.Equal(t, "analytics/assets/external/orders.asset.yml", preview.Asset.Path)
	require.Len(t, importer.requests, 1)
	request := importer.requests[0]
	assert.Equal(t, "analytics", request.PipelinePath)
	assert.Equal(t, "duckdb-default", request.ConnectionName)
	assert.Equal(t, "external.orders", request.PreferredAssetName)
	assert.Equal(t, "dev", request.Environment)
	assert.Equal(t, "external", request.Schema)
	assert.Equal(t, []string{"external.orders"}, request.Tables)
	assert.False(t, request.DisableColumns)
	assert.True(t, request.PreviewOnly)
	assert.True(t, request.RejectExisting)

	includeColumns := false
	imported, apiErr := service.ImportExternalRelation(context.Background(), EncodeID("analytics"), ExternalRelationImportRequest{
		RelationID:     relationID,
		IncludeColumns: &includeColumns,
	})
	require.Nil(t, apiErr)
	assert.False(t, imported.Preview)
	assert.False(t, imported.IncludeColumns)
	require.Len(t, importer.requests, 2)
	assert.True(t, importer.requests[1].DisableColumns)
	assert.False(t, importer.requests[1].PreviewOnly)
}

func TestExternalRelationImportRejectsUnknownOrStaleIdentity(t *testing.T) {
	t.Parallel()
	_, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{})
	service := NewPipelineService(root)
	service.SetRemoteCatalogProvider(&stubRemoteCatalogProvider{}, func() string { return "dev" })
	service.SetExternalRelationImporter(&recordingExternalRelationImporter{})

	_, apiErr := service.PreviewExternalRelationImport(context.Background(), EncodeID("analytics"), ExternalRelationImportRequest{
		RelationID: "relation:remote_catalog:stale",
	})
	require.NotNil(t, apiErr)
	assert.Equal(t, "external_relation_observation_changed", apiErr.Code)
}

func TestPinExternalRelationDependencyAdoptsExistingDependencyAndClearsSuppression(t *testing.T) {
	t.Parallel()
	asset := &pipeline.Asset{
		Name: "example.remote",
		Meta: pipeline.EmptyStringMap{
			assetmeta.KeyDepDrop: "a:public.accounts#full",
		},
		Upstreams: []pipeline.Upstream{{
			Type:  "asset",
			Value: "public.accounts",
			Mode:  pipeline.UpstreamModeFull,
		}},
	}

	assert.True(t, pinExternalRelationDependency(asset, "public.accounts"))
	assert.False(t, pinExternalRelationDependency(asset, "public.accounts"))
	meta := assetmeta.ParseAsset(asset)
	assert.Empty(t, meta.DepDrop)
	assert.Equal(t, []string{"a:public.accounts#full"}, meta.DepAdd)
	require.Len(t, asset.Upstreams, 1)
}

func TestExternalRelationImportUsesWarehouseValidPostgresIdentityAndObservedConnection(t *testing.T) {
	t.Parallel()
	_, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"example/remote.sql": `
/* @bruin
type: pg.sql
connection: postgres-other
materialization:
  type: view
@bruin */
select account_id from public.accounts
`,
	})
	provider := &stubRemoteCatalogProvider{snapshot: RemoteCatalogSnapshot{Relations: []RemoteCatalogRelation{{
		QualifiedName: "public.accounts",
		ShortName:     "accounts",
		SchemaName:    "public",
		DatabaseName:  "scraping_pipeline",
		ColumnsKnown:  true,
		Columns:       []SQLColumn{{Name: "account_id", Type: "bigint"}},
	}}}}
	connection := &directImportSummaryConnection{summary: &ansisql.DBDatabase{
		Name: "scraping_pipeline",
		Schemas: []*ansisql.DBSchema{{
			Name: "public",
			Tables: []*ansisql.DBTable{{
				Name:    "accounts",
				Type:    ansisql.DBTableTypeTable,
				Columns: []*ansisql.DBColumn{{Name: "account_id", Type: "bigint"}},
			}},
		}},
	}}
	executor := newCompatDirectExecutor(root, "")
	executor.newConnectionManager = func(context.Context, string) (config.ConnectionAndDetailsGetter, error) {
		return &stubConnectionManager{conn: connection, connectionType: "postgres"}, nil
	}

	service := NewPipelineService(root)
	service.SetRemoteCatalogProvider(provider, func() string { return "default" })
	service.SetExternalRelationImporter(executor)
	relationID := remoteCatalogRelationID(
		RemoteCatalogScope{Connection: "postgres-other", Environment: "default"},
		"public.accounts",
	)

	result, apiErr := service.ImportExternalRelation(context.Background(), EncodeID("analytics"), ExternalRelationImportRequest{
		RelationID: relationID,
	})
	require.Nil(t, apiErr)
	assert.Empty(t, result.Warnings)
	assert.Equal(t, "public.accounts", result.Asset.Name)

	importedPath := filepath.Join(root, "analytics", "assets", "public", "accounts.asset.yml")
	importedContent, err := os.ReadFile(importedPath)
	require.NoError(t, err)
	assert.Contains(t, string(importedContent), "name: public.accounts")
	assert.Contains(t, string(importedContent), "connection: postgres-other")

	parsed, err := NewRenartPipelineBuilder(afero.NewOsFs()).CreatePipelineFromPath(
		context.Background(),
		filepath.Join(root, "analytics"),
		pipeline.WithMutate(),
	)
	require.NoError(t, err)
	consumer := getAssetByNameCaseInsensitiveLocal(parsed, "example.remote")
	require.NotNil(t, consumer)
	require.Len(t, consumer.Upstreams, 1)
	assert.Equal(t, "public.accounts", consumer.Upstreams[0].Value)
	meta := assetmeta.ParseAsset(consumer)
	require.Len(t, meta.DepAdd, 1)
	assert.Equal(t, "a:public.accounts#full", meta.DepAdd[0])
}
