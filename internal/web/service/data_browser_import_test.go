package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"renart/internal/web/dataaddress"
	"renart/internal/web/databrowser"
)

func TestDataBrowserImportUsesResolvedIdentityWithoutChangingConsumers(t *testing.T) {
	_, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{})
	s := NewPipelineService(root)
	importer := &recordingExternalRelationImporter{}
	s.SetExternalRelationImporter(importer)
	object := databrowser.Object{
		ID: "resolved-object", Kind: "table", ConnectionName: "warehouse", ConnectionType: "duckdb",
		Environment: "dev", ReferenceText: "external.orders", Name: "orders",
		Address: &dataaddress.Address{SourceKind: "warehouse", Connection: "warehouse", ConnectionType: "duckdb", Schema: "external", Name: "orders"},
	}
	result, apiErr := s.ImportDataBrowserSource(context.Background(), EncodeID("analytics"), object, true, true)
	require.Nil(t, apiErr)
	require.True(t, result.Preview)
	require.Len(t, importer.requests, 1)
	req := importer.requests[0]
	require.Equal(t, "dev", req.Environment)
	require.Equal(t, "external.orders", req.PreferredAssetName)
	require.Equal(t, []string{"external.orders"}, req.Tables)
	require.Equal(t, "external", req.Schema)
	require.True(t, req.RejectExisting)
	require.True(t, req.PreviewOnly)
}

func TestDataBrowserSourceRejectsNonWarehouseAndUnsupportedConnections(t *testing.T) {
	s := NewPipelineService(t.TempDir())
	importer := &recordingExternalRelationImporter{}
	s.SetExternalRelationImporter(importer)
	for _, object := range []databrowser.Object{
		{Kind: "file"},
		{Kind: "table", ConnectionType: "stripe", ConnectionName: "stripe", Environment: "dev", ReferenceText: "orders", Address: &dataaddress.Address{SourceKind: "warehouse"}},
		{Kind: "table", ConnectionType: "duckdb"},
	} {
		_, apiErr := s.ImportDataBrowserSource(context.Background(), EncodeID("analytics"), object, true, true)
		require.NotNil(t, apiErr)
	}
	require.Empty(t, importer.requests)
}
