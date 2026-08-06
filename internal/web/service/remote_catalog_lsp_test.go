package service

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/sqllsp"
	"renart/internal/web/model"
)

type stubRemoteCatalogProvider struct {
	mu             sync.Mutex
	snapshot       RemoteCatalogSnapshot
	refreshScopes  []RemoteCatalogScope
	columnRequests []string
}

func (p *stubRemoteCatalogProvider) Snapshot(RemoteCatalogScope) RemoteCatalogSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return cloneRemoteCatalogSnapshot(p.snapshot)
}

func (p *stubRemoteCatalogProvider) Refresh(_ context.Context, scope RemoteCatalogScope) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refreshScopes = append(p.refreshScopes, scope)
}

func (p *stubRemoteCatalogProvider) RefreshColumns(_ context.Context, _ RemoteCatalogScope, relation string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.columnRequests = append(p.columnRequests, relation)
}

func TestSQLLSPServiceCompletesRemoteCatalogRelationsAndColumns(t *testing.T) {
	provider := &stubRemoteCatalogProvider{snapshot: RemoteCatalogSnapshot{Relations: []RemoteCatalogRelation{{
		QualifiedName: "warehouse.analytics.orders",
		ShortName:     "orders",
		SchemaName:    "analytics",
		DatabaseName:  "warehouse",
		ColumnsKnown:  true,
		Columns: []SQLColumn{
			{Name: "order_id", Type: "bigint"},
			{Name: "created_at", Type: "timestamp"},
		},
	}}}}
	state := model.WorkspaceState{
		SelectedEnvironment: "dev",
		Connections:         map[string]string{"warehouse": "postgres"},
		Pipelines: []model.Pipeline{{
			ID: "pipeline",
			Assets: []model.Asset{{
				ID:         "report",
				Name:       "analytics.report",
				Type:       "postgres.sql",
				Connection: "warehouse",
				Path:       "analytics/assets/analytics/report.sql",
				Content:    "select * from warehouse.analytics.orders",
			}},
		}},
	}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(),
		CurrentState:  func() model.WorkspaceState { return state },
		RemoteCatalog: provider,
	})

	relations, apiErr := service.Completions(t.Context(), SQLLSPRequest{
		AssetID:  "report",
		Content:  "select * from ",
		Position: sqllsp.Position{Character: len("select * from ")},
	})
	require.Nil(t, apiErr)
	remote := completionByLabel(relations.Completions, "warehouse.analytics.orders")
	require.NotNil(t, remote)
	assert.Equal(t, "remote warehouse relation", remote.Detail)
	assert.NotNil(t, completionByLabel(relations.Completions, "analytics.orders"))

	columns, apiErr := service.Completions(t.Context(), SQLLSPRequest{
		AssetID:  "report",
		Content:  "select o.\nfrom analytics.orders o",
		Position: sqllsp.Position{Character: len("select o.")},
	})
	require.Nil(t, apiErr)
	assert.NotNil(t, completionByLabel(columns.Completions, "order_id"))
	assert.NotNil(t, completionByLabel(columns.Completions, "created_at"))

	diagnostics, apiErr := service.Diagnostics(t.Context(), SQLLSPRequest{
		AssetID: "report",
		Content: "select o.order_id from analytics.orders o",
	})
	require.Nil(t, apiErr)
	for _, diagnostic := range diagnostics.Diagnostics {
		assert.NotEqual(t, "unresolved-relation", diagnostic.Code, diagnostic.Message)
		assert.NotEqual(t, "unresolved-column", diagnostic.Code, diagnostic.Message)
	}

	provider.mu.Lock()
	require.NotEmpty(t, provider.refreshScopes)
	assert.Equal(t, "dev", provider.refreshScopes[0].Environment)
	provider.mu.Unlock()
}

func TestRemoteCatalogOverlayPreservesAuthoredCollisionAndCachedGraph(t *testing.T) {
	base := sqllsp.CanonicalGraph{
		Relations: []sqllsp.RelationNode{{ID: "local", Name: "analytics.orders", AssetID: "orders"}},
		Schemas: []sqllsp.SchemaLayer{{
			RelationID:   "local",
			SourceKind:   "declared",
			Completeness: "complete",
			Columns:      []sqllsp.ColumnInfo{{Name: "local_id", Type: "bigint"}},
		}},
	}
	snapshot := RemoteCatalogSnapshot{Relations: []RemoteCatalogRelation{
		{QualifiedName: "analytics.orders", ColumnsKnown: true, Columns: []SQLColumn{{Name: "remote_id"}}},
		{QualifiedName: "warehouse.analytics.events", ColumnsKnown: true, Columns: []SQLColumn{{Name: "event_id"}}},
	}}
	overlay := graphWithRemoteCatalogSnapshot(base, RemoteCatalogScope{Connection: "prod"}, snapshot)

	assert.Len(t, base.Relations, 1, "the revision-cached graph must remain unchanged")
	assert.Len(t, base.Schemas, 1)
	require.Len(t, overlay.Relations, 2)
	assert.Equal(t, "analytics.orders", overlay.Relations[0].Name)
	assert.Equal(t, "warehouse.analytics.events", overlay.Relations[1].Name)
	engine := sqllsp.NewEngine(overlay)
	items := engine.Complete(
		sqllsp.TextDocumentItem{Text: "select o.\nfrom analytics.orders o"},
		sqllsp.Position{Character: len("select o.")},
	)
	assert.NotNil(t, completionByLabel(items, "local_id"))
	assert.Nil(t, completionByLabel(items, "remote_id"))
}

func TestRemoteCatalogOverlayRequiresDatabaseForAmbiguousSchemaAlias(t *testing.T) {
	snapshot := RemoteCatalogSnapshot{Relations: []RemoteCatalogRelation{
		{
			QualifiedName: "warehouse_a.analytics.orders",
			ShortName:     "orders",
			SchemaName:    "analytics",
			DatabaseName:  "warehouse_a",
		},
		{
			QualifiedName: "warehouse_b.analytics.orders",
			ShortName:     "orders",
			SchemaName:    "analytics",
			DatabaseName:  "warehouse_b",
		},
	}}
	overlay := graphWithRemoteCatalogSnapshot(
		sqllsp.CanonicalGraph{},
		RemoteCatalogScope{Connection: "prod"},
		snapshot,
	)

	require.Len(t, overlay.Relations, 2)
	engine := sqllsp.NewEngine(overlay)
	diagnostics := engine.Diagnostics(sqllsp.TextDocumentItem{Text: "select * from analytics.orders"})
	require.NotEmpty(t, diagnostics)
	assert.Equal(t, "unresolved-relation", diagnostics[0].Code)

	diagnostics = engine.Diagnostics(sqllsp.TextDocumentItem{Text: "select * from warehouse_a.analytics.orders"})
	for _, diagnostic := range diagnostics {
		assert.NotEqual(t, "unresolved-relation", diagnostic.Code, diagnostic.Message)
	}
}

func completionByLabel(items []sqllsp.CompletionItem, label string) *sqllsp.CompletionItem {
	for index := range items {
		if items[index].Label == label {
			return &items[index]
		}
	}
	return nil
}
