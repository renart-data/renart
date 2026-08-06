package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingExternalRelationImporter struct {
	requests []ImportDatabaseRequest
}

func (i *recordingExternalRelationImporter) ImportDatabase(_ context.Context, req ImportDatabaseRequest) ([]byte, error) {
	i.requests = append(i.requests, req)
	return json.Marshal(directImportDatabaseResponse{
		Status:       "ok",
		Preview:      req.PreviewOnly,
		PipelinePath: req.PipelinePath,
		Assets: []directImportAsset{{
			Name:    "external.orders",
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
