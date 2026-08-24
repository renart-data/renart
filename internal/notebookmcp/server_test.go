package notebookmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"renart/internal/web/model"
	"renart/internal/web/notebook"
	"renart/internal/web/presentation"
	"renart/internal/web/service"
)

type fakeBackend struct {
	mu sync.Mutex

	workspace model.WorkspaceState
	notebook  model.Notebook
	runtime   service.NotebookRuntimeSnapshot
	prepared  service.NotebookChangeSet
	applied   service.NotebookChangeSet
	run       func(context.Context, string, service.RunNotebookRequest) (service.RunNotebookResult, error)
	cancels   int
}

type nativeInteractionBackend struct {
	*fakeBackend
	notebookID string
	turnToken  string
	request    service.NotebookAgentQuestionnaireRequest
}

func (f *nativeInteractionBackend) RequestNotebookAgentQuestionnaire(
	_ context.Context,
	notebookID string,
	turnToken string,
	request service.NotebookAgentQuestionnaireRequest,
) (service.NotebookAgentInteractionResult, error) {
	f.notebookID = notebookID
	f.turnToken = turnToken
	f.request = request
	return service.NotebookAgentInteractionResult{
		Status: "answered",
		Answers: []service.NotebookAgentQuestionAnswer{{
			QuestionID: "metric",
			Values:     []string{"revenue"},
		}},
	}, nil
}

func (f *fakeBackend) Workspace(context.Context) (model.WorkspaceState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.workspace, nil
}

func (f *fakeBackend) Notebook(context.Context, string) (model.Notebook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.notebook, nil
}

func (f *fakeBackend) Runtime(context.Context, string) (service.NotebookRuntimeSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runtime, nil
}

func (f *fakeBackend) PrepareChangeSet(_ context.Context, _ string, change service.NotebookChangeSet) (service.NotebookChangePlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if change.BaseRevision != f.notebook.Revision {
		return service.NotebookChangePlan{}, fmt.Errorf("revision conflict")
	}
	normalized := change
	for index := range normalized.Operations {
		if normalized.Operations[index].Kind == service.NotebookOperationMarkdownCreate && normalized.Operations[index].BlockID == "" {
			normalized.Operations[index].BlockID = "block_generated"
		}
	}
	normalized.ExpectedRevision = "rev2"
	f.prepared = normalized
	return service.NotebookChangePlan{
		Status: "ok", ChangeSet: normalized, CanApply: true,
		Diff: []service.NotebookChangeDiff{{Path: "/secret/workspace/notebook.yml", Status: "modified", Before: "secret-before", After: "secret-after"}},
	}, nil
}

func (f *fakeBackend) ApplyChangeSet(_ context.Context, _ string, change service.NotebookChangeSet) (service.NotebookChangeApplyResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !reflect.DeepEqual(change, f.prepared) {
		return service.NotebookChangeApplyResult{}, fmt.Errorf("change differs from prepared operation")
	}
	f.applied = change
	f.notebook.Revision = change.ExpectedRevision
	return service.NotebookChangeApplyResult{Status: "ok", Notebook: f.notebook}, nil
}

func (f *fakeBackend) CheckVisualization(_ context.Context, _ string, _ service.NotebookVisualizationCheckRequest) (service.NotebookVisualizationCheckResult, error) {
	return service.NotebookVisualizationCheckResult{
		Status: "ok", CanApply: false,
		Findings: []presentation.Finding{{Code: "field-missing", Severity: "error", Message: "missing amount", Path: "encoding.y", Field: "amount"}},
	}, nil
}

func (f *fakeBackend) Run(ctx context.Context, id string, request service.RunNotebookRequest) (service.RunNotebookResult, error) {
	if f.run != nil {
		return f.run(ctx, id, request)
	}
	return service.RunNotebookResult{Status: "ok", Results: []notebook.CellRunResult{{
		CellID: "cell_sql", Name: "orders", Status: notebook.CellRunOK,
		Columns: []string{"id"}, ColumnTypes: []string{"BIGINT"}, TotalRows: 2,
	}}}, nil
}

func (f *fakeBackend) Cancel(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancels++
	return nil
}

func fixtureBackend() *fakeBackend {
	source := model.Asset{
		CellID: "cell_source", Name: "accounts_api", Type: "renart.http", Connection: "object-secret",
		NotebookSource: &model.NotebookSourceDefinition{
			Version: 1, ID: "cell_source", Kind: "http",
			URI: "https://user:password@example.test/data?signature=secret",
			Request: model.NotebookSourceRequest{
				URL: "https://user:password@example.test/data?signature=secret", Method: "POST",
				Headers: map[string]string{"authorization": "Bearer very-secret"},
				Body:    map[string]any{"api_key": "also-secret"},
			},
			Response: model.NotebookSourceResponse{RecordsPath: "data.items"},
			Snapshot: model.NotebookSourceSnapshot{Mode: "sample", RowLimit: 100},
		},
	}
	sql := model.Asset{
		CellID: "cell_sql", Name: "orders", Type: "duckdb.sql", Content: "select * from accounts_api",
		Upstreams: []string{"accounts_api"}, Columns: []model.Column{{Name: "id", Type: "BIGINT"}},
	}
	python := model.Asset{
		CellID: "cell_python", Name: "forecast", Type: notebook.PythonCellType,
		Content:   "def materialize():\n    return query('select * from orders')",
		Upstreams: []string{"orders"},
	}
	nb := model.Notebook{
		ID: "notebook_opaque", UUID: "nb_uuid", Title: "Revenue", ManifestVersion: 2, Revision: "rev1",
		Parameters: []model.NotebookParameter{{
			ID: "region", Label: "Region", Type: "select", Default: "eu",
			Options: &model.NotebookParameterOptions{Values: []any{"eu", "us"}},
		}},
		Blocks: []model.NotebookBlock{
			{Cell: "cell_source"}, {Cell: "cell_sql"}, {Cell: "cell_python"},
			{ID: "viz_sales", Visualization: &model.NotebookVisualization{ID: "viz_sales", Source: "cell_sql", Definition: map[string]any{"version": 1, "type": "line"}}},
			{Control: "region"},
			{ID: "block_notes", Markdown: "## Notes"},
		},
		Cells: []model.Asset{source, sql, python},
	}
	rows := make([][]any, 0, 80)
	for index := 0; index < 80; index++ {
		rows = append(rows, []any{index, strings.Repeat("x", 2048)})
	}
	runtime := service.NotebookRuntimeSnapshot{Results: map[string]notebook.CellRunResult{
		"cell_sql": {
			CellID: "cell_sql", Name: "orders", Status: notebook.CellRunOK,
			Columns: []string{"id", "payload"}, ColumnTypes: []string{"BIGINT", "VARCHAR"},
			Rows: rows, TotalRows: 80, Materialized: "view",
		},
		"cell_source": {
			CellID: "cell_source", Name: "accounts_api", Status: notebook.CellRunOK,
			Columns: []string{"id"}, ColumnTypes: []string{"BIGINT"}, TotalRows: 100, Sampled: true,
			Snapshot: &notebook.SnapshotRecord{
				BlockID: "cell_source", SourceKind: "http", Environment: "default",
				ImportedAt: "2026-08-12T12:00:00Z", RowCount: 100, ByteCount: 4096,
				Sampled: true, Complete: false, Schema: []notebook.TabularColumn{{Name: "id", Type: "BIGINT"}},
			},
		},
	}}
	return &fakeBackend{
		workspace: model.WorkspaceState{Notebooks: []model.Notebook{nb}}, notebook: nb, runtime: runtime,
	}
}

type protocolFixture struct {
	client *mcp.ClientSession
	close  func()
}

func connectTestServer(t *testing.T, backend Backend) protocolFixture {
	return connectTestServerWithPolicy(t, backend, Policy{})
}

func connectTestServerWithPolicy(t *testing.T, backend Backend, policy Policy) protocolFixture {
	t.Helper()
	ctx := context.Background()
	server := New(ctx, backend, "test", nil, policy)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Protocol().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		serverSession.Close()
		t.Fatal(err)
	}
	return protocolFixture{
		client: clientSession,
		close: func() {
			_ = clientSession.Close()
			_ = serverSession.Close()
		},
	}
}

func TestNativeAgentPolicyScopesNotebookAndCapabilities(t *testing.T) {
	backend := fixtureBackend()
	other := backend.notebook
	other.ID = "other_notebook"
	other.Title = "Other"
	backend.workspace.Notebooks = append(backend.workspace.Notebooks, other)

	fixture := connectTestServerWithPolicy(t, backend, Policy{
		NotebookID: "notebook_opaque",
		ReadOnly:   true,
		NoRuns:     true,
	})
	defer fixture.close()

	listed, err := fixture.client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(listed.Tools), 9; got != want {
		t.Fatalf("read-only tool count = %d, want %d", got, want)
	}
	for _, tool := range listed.Tools {
		for _, forbidden := range []string{"prepare_notebook_change_set", "apply_notebook_change_set", "run_notebook_cells"} {
			if tool.Name == forbidden {
				t.Fatalf("read-only policy exposed %q", tool.Name)
			}
		}
	}

	notebooks := callTool[ListNotebooksOutput](t, fixture.client, "list_notebooks", map[string]any{})
	if got, want := len(notebooks.Notebooks), 1; got != want || notebooks.Notebooks[0].ID != "notebook_opaque" {
		t.Fatalf("scoped notebook list = %+v", notebooks.Notebooks)
	}
	errorText := callToolError(t, fixture.client, "get_notebook_outline", map[string]any{
		"notebook_id": "other_notebook",
	})
	if !strings.Contains(errorText, "outside this agent session") {
		t.Fatalf("unexpected cross-notebook error: %s", errorText)
	}
}

func TestNativeInteractionToolRequiresPrivateTurnBinding(t *testing.T) {
	backend := &nativeInteractionBackend{fakeBackend: fixtureBackend()}
	fixture := connectTestServerWithPolicy(t, backend, Policy{
		NotebookID:         "notebook_opaque",
		ReadOnly:           true,
		NoRuns:             true,
		NativeTurnToken:    "opaque-turn-token",
		NativeInteractions: backend,
	})
	defer fixture.close()

	listed, err := fixture.client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(listed.Tools), 10; got != want {
		t.Fatalf("native tool count = %d, want %d", got, want)
	}
	var found bool
	for _, tool := range listed.Tools {
		if tool.Name == "ask_user" {
			found = true
		}
	}
	if !found {
		t.Fatal("native agent policy did not expose ask_user")
	}

	result := callTool[service.NotebookAgentInteractionResult](t, fixture.client, "ask_user", map[string]any{
		"title": "Choose a metric",
		"questions": []map[string]any{{
			"id": "metric", "kind": "single_choice", "prompt": "Which metric?", "required": true,
			"options": []map[string]any{
				{"value": "revenue", "label": "Revenue"},
				{"value": "orders", "label": "Orders"},
			},
		}},
	})
	if result.Status != "answered" || result.Answers[0].Values[0] != "revenue" {
		t.Fatalf("unexpected native interaction result: %+v", result)
	}
	if backend.notebookID != "notebook_opaque" || backend.turnToken != "opaque-turn-token" || backend.request.Title != "Choose a metric" {
		t.Fatalf("native interaction binding was not forwarded: %+v", backend)
	}
}

func callTool[T any](t *testing.T, client *mcp.ClientSession, name string, arguments any) T {
	t.Helper()
	result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if result.IsError {
		t.Fatalf("call %s returned a tool error: %s", name, toolText(result))
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal %s output: %v", name, err)
	}
	var output T
	if err := json.Unmarshal(encoded, &output); err != nil {
		t.Fatalf("decode %s output: %v\n%s", name, err, encoded)
	}
	return output
}

func callToolError(t *testing.T, client *mcp.ClientSession, name string, arguments any) string {
	t.Helper()
	result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if !result.IsError {
		t.Fatalf("call %s unexpectedly succeeded", name)
	}
	return toolText(result)
}

func toolText(result *mcp.CallToolResult) string {
	var parts []string
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func TestProtocolSurfaceIsBoundedAndAnnotated(t *testing.T) {
	fixture := connectTestServer(t, fixtureBackend())
	defer fixture.close()
	listed, err := fixture.client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(listed.Tools), 16; got != want {
		t.Fatalf("tool count = %d, want %d", got, want)
	}
	for _, tool := range listed.Tools {
		lower := strings.ToLower(tool.Name)
		for _, forbidden := range []string{"filesystem", "shell", "git", "generic_api", "execute_sql"} {
			if strings.Contains(lower, forbidden) {
				t.Errorf("tool %q exposes forbidden capability %q", tool.Name, forbidden)
			}
		}
		if tool.Annotations == nil {
			t.Errorf("tool %q has no behavior annotations", tool.Name)
		}
	}
	apply := findTool(t, listed.Tools, "apply_notebook_change_set")
	if apply.Annotations.ReadOnlyHint || apply.Annotations.DestructiveHint == nil || !*apply.Annotations.DestructiveHint {
		t.Fatalf("apply annotations do not identify a destructive write: %+v", apply.Annotations)
	}
	run := findTool(t, listed.Tools, "run_notebook_cells")
	if run.Annotations.OpenWorldHint == nil || !*run.Annotations.OpenWorldHint {
		t.Fatalf("run annotations do not identify external/code execution: %+v", run.Annotations)
	}
	prepare := findTool(t, listed.Tools, "prepare_notebook_change_set")
	encodedSchema, err := json.Marshal(prepare.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`"enum"`, service.NotebookOperationCellUpdate,
		service.NotebookOperationCellSQLRefactor,
		service.NotebookSQLRefactorRelationRename,
		service.NotebookSQLRefactorColumnQualify,
		service.NotebookSQLRefactorRelationAlias,
		service.NotebookOperationVisualizationCreate,
		service.NotebookOperationControlCreate,
		`never guess or probe operation names`,
		`versioned visualization object`,
		`"encoding"`,
		`"y"`,
		`encoding.y and encoding.tooltip are arrays`,
		`"table","kpi","bar","line","area","scatter","pie","donut"`,
		`typed control definition`,
		`untouched SQL remains byte-identical`,
	} {
		if !strings.Contains(string(encodedSchema), required) {
			t.Fatalf("prepare schema does not teach agents %q: %s", required, encodedSchema)
		}
	}
	for _, required := range []string{
		`Prefer cell.sql.refactor`,
		`untouched SQL stays byte-identical`,
		`not Vega`,
		`encoding is singular`,
		`encoding.y is an array`,
	} {
		if !strings.Contains(prepare.Description, required) {
			t.Fatalf("prepare description does not teach agents %q: %s", required, prepare.Description)
		}
	}
}

func findTool(t *testing.T, tools []*mcp.Tool, name string) *mcp.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

func TestReadToolsRedactSourcesAndBoundSamples(t *testing.T) {
	fixture := connectTestServer(t, fixtureBackend())
	defer fixture.close()

	list := callTool[ListNotebooksOutput](t, fixture.client, "list_notebooks", map[string]any{})
	if len(list.Notebooks) != 1 || list.Notebooks[0].Revision != "rev1" {
		t.Fatalf("unexpected notebook list: %+v", list)
	}
	block := callTool[NotebookBlockOutput](t, fixture.client, "get_notebook_block", map[string]any{
		"notebook_id": "notebook_opaque", "block_id": "cell_source",
	})
	encoded, _ := json.Marshal(block)
	for _, secret := range []string{"password", "signature=", "very-secret", "also-secret", "/secret/workspace"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("source block leaked %q: %s", secret, encoded)
		}
	}
	if block.Source == nil || block.Source.RequestURL != "https://example.test/data" {
		t.Fatalf("unexpected safe source definition: %+v", block.Source)
	}
	outline := callTool[NotebookOutlineOutput](t, fixture.client, "get_notebook_outline", map[string]any{
		"notebook_id": "notebook_opaque",
	})
	if outline.Notebook.ParameterCount != 1 || len(outline.Blocks) != 6 ||
		outline.Blocks[4].ID != "control:region" || outline.Blocks[4].Kind != "control" || outline.Blocks[4].Name != "Region" {
		t.Fatalf("outline did not preserve the ordered control: %+v", outline)
	}
	control := callTool[NotebookBlockOutput](t, fixture.client, "get_notebook_block", map[string]any{
		"notebook_id": "notebook_opaque", "block_id": "control:region",
	})
	if control.Kind != "control" || control.Parameter == nil || control.Parameter.ID != "region" {
		t.Fatalf("unexpected control block: %+v", control)
	}
	graph := callTool[NotebookGraphOutput](t, fixture.client, "get_notebook_graph", map[string]any{
		"notebook_id": "notebook_opaque",
	})
	if len(graph.Nodes) != 6 {
		t.Fatalf("graph duplicated ordered cell references: %+v", graph.Nodes)
	}
	foundControl := false
	for _, node := range graph.Nodes {
		if node.ID == "control:region" && node.Kind == "control" {
			foundControl = true
		}
	}
	if !foundControl {
		t.Fatalf("graph omitted the ordered control: %+v", graph.Nodes)
	}
	sample := callTool[NotebookResultSampleOutput](t, fixture.client, "get_notebook_result_sample", map[string]any{
		"notebook_id": "notebook_opaque", "cell_id": "cell_sql", "limit": 500,
	})
	if len(sample.Rows) > maxSampleRows || !sample.Truncated {
		t.Fatalf("sample was not bounded: rows=%d truncated=%v", len(sample.Rows), sample.Truncated)
	}
	sampleJSON, _ := json.Marshal(sample.Rows)
	if len(sampleJSON) > maxSampleBytes {
		t.Fatalf("sample payload = %d bytes, exceeds %d", len(sampleJSON), maxSampleBytes)
	}
	sources := callTool[ListNotebookSourcesOutput](t, fixture.client, "list_notebook_sources", map[string]any{"notebook_id": "notebook_opaque"})
	if len(sources.Sources) != 1 || sources.Sources[0].Snapshot == nil || !sources.Sources[0].Snapshot.Sampled {
		t.Fatalf("unexpected sources: %+v", sources.Sources)
	}
}

func TestCatalogSearchFindsTypedPipelineSourcesWithoutLeakingPaths(t *testing.T) {
	backend := fixtureBackend()
	columns := []model.Column{{Name: "order_id", Type: "BIGINT"}, {Name: "customer_name", Type: "VARCHAR"}}
	for index := 0; index < maxCatalogColumns+8; index++ {
		columns = append(columns, model.Column{Name: fmt.Sprintf("extra_%02d", index), Type: "INTEGER"})
	}
	asset := model.Asset{
		ID: "asset_orders", Name: "analytics.orders", URI: "renart://secret/catalog/orders",
		Type: "pg.sql", Path: "/secret/workspace/orders.sql", Connection: "postgres-other",
		Description:         "Replay-safe order history for longitudinal analysis.",
		Tags:                []string{"recommended-analysis", "historical-analysis"},
		MaterializationType: "table", MaterializationStrategy: "time_interval",
		IncrementalKey: "ordered_at", ClusterBy: []string{"region", "ordered_at"}, TimeGranularity: "timestamp",
		Columns:      columns,
		Dependencies: []model.AssetDependency{{ResolvedAssetID: "asset_raw_orders"}},
	}
	rawAsset := model.Asset{
		ID: "asset_raw_orders", Name: "raw.orders", Type: "pg.sql",
		Path: "/secret/workspace/raw_orders.sql", Connection: "postgres-other",
		MaterializationType: "table", MaterializationStrategy: "merge",
		Columns: []model.Column{{Name: "order_id", Type: "BIGINT"}},
	}
	summaryAsset := model.Asset{
		ID: "asset_order_summary", Name: "analytics.order_summary", Type: "pg.sql",
		Path: "/secret/workspace/order_summary.sql", Connection: "postgres-other",
		MaterializationType: "table", MaterializationStrategy: "truncate+insert",
		Columns:      []model.Column{{Name: "order_count", Type: "BIGINT"}},
		Dependencies: []model.AssetDependency{{ResolvedAssetID: "asset_orders"}},
	}
	backend.workspace.Pipelines = []model.Pipeline{{
		ID: "pipeline_opaque", UUID: "pipeline_uuid", Name: "Warehouse",
		Assets: []model.Asset{rawAsset, asset, summaryAsset},
	}}
	backend.workspace.QueryConnections = []model.WorkspaceQueryConnection{{
		Name: "postgres-other", ConnectionType: "postgres", AssetType: "pg.sql", Dialect: "postgres",
	}}
	backend.workspace.ArtifactIndex = service.BuildArtifactIndex(backend.workspace)

	fixture := connectTestServer(t, backend)
	defer fixture.close()
	output := callTool[CatalogSearchOutput](t, fixture.client, "search_workspace_catalog", map[string]any{
		"query": "customer_name", "kinds": []string{"pipeline_asset"}, "limit": 50,
	})
	if len(output.Matches) != 1 {
		t.Fatalf("catalog matches = %+v", output.Matches)
	}
	match := output.Matches[0]
	if !match.DataSourceEligible || match.Relation != "analytics.orders" || match.ConnectionType != "postgres" {
		t.Fatalf("unexpected catalog match: %+v", match)
	}
	if match.SuggestedSource == nil || !match.SuggestedSource.ApprovalRequired || match.SuggestedSource.SnapshotMode != "sample" {
		t.Fatalf("unexpected source recipe: %+v", match.SuggestedSource)
	}
	if match.ColumnCount != len(columns) || len(match.Columns) != maxCatalogColumns {
		t.Fatalf("catalog columns were not bounded: count=%d returned=%d", match.ColumnCount, len(match.Columns))
	}
	if match.Description != asset.Description || !reflect.DeepEqual(match.Tags, []string{"historical-analysis", "recommended-analysis"}) {
		t.Fatalf("catalog metadata = description %q tags %v", match.Description, match.Tags)
	}
	if match.Materialization == nil || match.Materialization.Strategy != "time_interval" ||
		match.Materialization.IncrementalKey != "ordered_at" ||
		!reflect.DeepEqual(match.Materialization.ClusterBy, []string{"region", "ordered_at"}) ||
		match.Materialization.TimeGranularity != "timestamp" {
		t.Fatalf("catalog materialization = %+v", match.Materialization)
	}
	if match.UpstreamCount != 1 || len(match.Upstreams) != 1 || match.Upstreams[0].Relation != "raw.orders" {
		t.Fatalf("catalog upstreams = count %d refs %+v", match.UpstreamCount, match.Upstreams)
	}
	if match.DownstreamCount != 1 || len(match.Downstreams) != 1 || match.Downstreams[0].Relation != "analytics.order_summary" {
		t.Fatalf("catalog downstreams = count %d refs %+v", match.DownstreamCount, match.Downstreams)
	}
	history := callTool[CatalogSearchOutput](t, fixture.client, "search_workspace_catalog", map[string]any{
		"query": "historical-analysis", "kinds": []string{"pipeline_asset"}, "limit": 50,
	})
	if len(history.Matches) != 1 || history.Matches[0].Relation != "analytics.orders" {
		t.Fatalf("catalog did not search descriptions/tags: %+v", history.Matches)
	}
	encoded, _ := json.Marshal(output)
	for _, hidden := range []string{"/secret/workspace", "renart://secret"} {
		if strings.Contains(string(encoded), hidden) {
			t.Fatalf("catalog output leaked %q: %s", hidden, encoded)
		}
	}
}

func TestCatalogLineageIsBoundedAndReportsFullCounts(t *testing.T) {
	target := CatalogMatch{
		ID: "target", Kind: "pipeline_asset", ArtifactKind: "pipeline_asset", ArtifactID: "target",
	}
	matches := []CatalogMatch{target}
	refs := map[string]CatalogLineageRef{
		catalogRefKey(model.ArtifactRef{Kind: "pipeline_asset", ArtifactID: "target"}): catalogLineageRef(target),
	}
	dependencies := make([]model.ArtifactDependency, 0, maxCatalogLineage+3)
	for index := 0; index < maxCatalogLineage+3; index++ {
		id := fmt.Sprintf("source-%02d", index)
		source := CatalogMatch{
			ID: id, Kind: "pipeline_asset", ArtifactKind: "pipeline_asset", ArtifactID: id,
			Title: fmt.Sprintf("Source %02d", index), Relation: fmt.Sprintf("raw.source_%02d", index),
		}
		matches = append(matches, source)
		ref := model.ArtifactRef{Kind: "pipeline_asset", ArtifactID: id}
		refs[catalogRefKey(ref)] = catalogLineageRef(source)
		dependencies = append(dependencies, model.ArtifactDependency{
			Producer: ref,
			Consumer: model.ArtifactRef{Kind: "pipeline_asset", ArtifactID: "target"},
		})
	}

	catalogAttachLineage(matches, dependencies, refs)
	if matches[0].UpstreamCount != maxCatalogLineage+3 || len(matches[0].Upstreams) != maxCatalogLineage || !matches[0].LineageTruncated {
		t.Fatalf("lineage was not bounded with a full count: %+v", matches[0])
	}
}

func TestPreparedChangeAppliesExactNormalizedOperations(t *testing.T) {
	backend := fixtureBackend()
	fixture := connectTestServer(t, backend)
	defer fixture.close()
	prepared := callTool[PreparedChangeOutput](t, fixture.client, "prepare_notebook_change_set", map[string]any{
		"notebook_id": "notebook_opaque", "base_revision": "rev1",
		"operations": []map[string]any{{"kind": "markdown.create", "content": "## Analysis", "position": "end"}},
	})
	if prepared.PreparedID == "" || prepared.ExpectedRevision != "rev2" || prepared.Operations[0].BlockID != "block_generated" {
		t.Fatalf("unexpected prepared change: %+v", prepared)
	}
	preparedJSON, _ := json.Marshal(prepared)
	if strings.Contains(string(preparedJSON), "/secret/workspace") || strings.Contains(string(preparedJSON), "secret-before") {
		t.Fatalf("prepared output leaked backend diff bytes: %s", preparedJSON)
	}
	validated := callTool[PreparedChangeOutput](t, fixture.client, "validate_notebook_change_set", map[string]any{"prepared_id": prepared.PreparedID})
	if !validated.CanApply || validated.ExpectedRevision != prepared.ExpectedRevision {
		t.Fatalf("unexpected validation: %+v", validated)
	}
	applied := callTool[ApplyChangeOutput](t, fixture.client, "apply_notebook_change_set", map[string]any{"prepared_id": prepared.PreparedID})
	if !applied.Applied || applied.Notebook.Revision != "rev2" {
		t.Fatalf("unexpected apply result: %+v", applied)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if !reflect.DeepEqual(backend.applied, backend.prepared) {
		t.Fatalf("applied change differs from reviewed normalized change\nprepared: %#v\napplied: %#v", backend.prepared, backend.applied)
	}
}

func TestMCPRejectsDeleteAndSourceUpdateOperations(t *testing.T) {
	fixture := connectTestServer(t, fixtureBackend())
	defer fixture.close()
	for _, kind := range []string{service.NotebookOperationCellDelete, service.NotebookOperationSourceUpdate} {
		message := callToolError(t, fixture.client, "prepare_notebook_change_set", map[string]any{
			"notebook_id": "notebook_opaque", "base_revision": "rev1",
			"operations": []map[string]any{{"kind": kind, "cell_id": "cell_sql"}},
		})
		if !strings.Contains(message, "does not equal any of") {
			t.Fatalf("unexpected rejection for %s: %s", kind, message)
		}
		if !strings.Contains(message, service.NotebookOperationVisualizationCreate) {
			t.Fatalf("rejection does not return the valid operation kinds: %s", message)
		}
	}
}

func TestMCPVisualizationSchemaRejectsVegaShapedEncoding(t *testing.T) {
	fixture := connectTestServer(t, fixtureBackend())
	defer fixture.close()
	message := callToolError(t, fixture.client, "prepare_notebook_change_set", map[string]any{
		"notebook_id": "notebook_opaque", "base_revision": "rev1",
		"operations": []map[string]any{{
			"kind": service.NotebookOperationVisualizationCreate,
			"visualization": map[string]any{
				"source": "cell_sql",
				"definition": map[string]any{
					"version": 1,
					"type":    "line",
					"encoding": map[string]any{
						"x": map[string]any{"field": "created_at"},
						"y": map[string]any{"field": "revenue"},
					},
				},
			},
		}},
	})
	if !strings.Contains(strings.ToLower(message), "array") {
		t.Fatalf("unexpected visualization schema error: %s", message)
	}
}

func TestMCPVisualizationSchemaRejectsUnknownChartType(t *testing.T) {
	fixture := connectTestServer(t, fixtureBackend())
	defer fixture.close()
	message := callToolError(t, fixture.client, "prepare_notebook_change_set", map[string]any{
		"notebook_id": "notebook_opaque", "base_revision": "rev1",
		"operations": []map[string]any{{
			"kind": service.NotebookOperationVisualizationCreate,
			"visualization": map[string]any{
				"source": "cell_sql",
				"definition": map[string]any{
					"version": 1,
					"type":    "point",
				},
			},
		}},
	})
	if !strings.Contains(message, `does not equal any of`) || !strings.Contains(message, `scatter`) {
		t.Fatalf("unexpected visualization type error: %s", message)
	}
}

func TestRunRequiresPythonApprovalAndReturnsAsyncStatus(t *testing.T) {
	backend := fixtureBackend()
	fixture := connectTestServer(t, backend)
	defer fixture.close()
	message := callToolError(t, fixture.client, "run_notebook_cells", map[string]any{
		"notebook_id": "notebook_opaque", "all": true,
	})
	if !strings.Contains(message, "allow_python=true") {
		t.Fatalf("unexpected Python approval error: %s", message)
	}
	accepted := callTool[RunAcceptedOutput](t, fixture.client, "run_notebook_cells", map[string]any{
		"notebook_id": "notebook_opaque", "all": true, "allow_python": true,
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		status := callTool[RunStatusOutput](t, fixture.client, "get_notebook_run_status", map[string]any{"run_id": accepted.RunID})
		if status.Status == "succeeded" {
			if len(status.Results) != 1 || status.Results[0].Columns[0].Type != "BIGINT" {
				t.Fatalf("unexpected run results: %+v", status.Results)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not finish: %+v", status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNativeAgentRunRequiresUserApprovalForNewRemoteSources(t *testing.T) {
	backend := fixtureBackend()
	remote := model.Asset{
		CellID: "cell_remote", Name: "warehouse_orders", Type: "pg.sql",
		Connection: "postgres-other", Content: "select * from analytics.orders",
	}
	backend.notebook.Cells = append(backend.notebook.Cells, remote)
	backend.notebook.Blocks = append(backend.notebook.Blocks, model.NotebookBlock{Cell: remote.CellID})
	backend.workspace.Notebooks = []model.Notebook{backend.notebook}
	fixture := connectTestServerWithPolicy(t, backend, Policy{
		NotebookID: "notebook_opaque", RequireSourceApproval: true,
	})
	defer fixture.close()

	message := callToolError(t, fixture.client, "run_notebook_cells", map[string]any{
		"notebook_id": "notebook_opaque", "cells": []string{remote.CellID},
	})
	if !strings.Contains(message, "requires user approval") || !strings.Contains(message, "postgres-other") {
		t.Fatalf("unexpected source approval error: %s", message)
	}

	backend.mu.Lock()
	backend.runtime.Results[remote.CellID] = notebook.CellRunResult{
		CellID: remote.CellID, Name: remote.Name, Status: notebook.CellRunOK,
		Snapshot: &notebook.SnapshotRecord{BlockID: remote.CellID, Connection: remote.Connection, Complete: true},
	}
	backend.mu.Unlock()
	accepted := callTool[RunAcceptedOutput](t, fixture.client, "run_notebook_cells", map[string]any{
		"notebook_id": "notebook_opaque", "cells": []string{remote.CellID},
	})
	if accepted.RunID == "" {
		t.Fatal("approved cached source did not start")
	}

	message = callToolError(t, fixture.client, "run_notebook_cells", map[string]any{
		"notebook_id": "notebook_opaque", "cells": []string{remote.CellID}, "refresh_sources": true,
	})
	if !strings.Contains(message, "explicit refresh") {
		t.Fatalf("unexpected refresh approval error: %s", message)
	}
}

func TestCancelNotebookRunPropagatesToBackend(t *testing.T) {
	backend := fixtureBackend()
	started := make(chan struct{})
	backend.run = func(ctx context.Context, _ string, _ service.RunNotebookRequest) (service.RunNotebookResult, error) {
		close(started)
		<-ctx.Done()
		return service.RunNotebookResult{Status: "cancelled"}, nil
	}
	fixture := connectTestServer(t, backend)
	defer fixture.close()
	accepted := callTool[RunAcceptedOutput](t, fixture.client, "run_notebook_cells", map[string]any{
		"notebook_id": "notebook_opaque", "cells": []string{"cell_sql"},
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("run did not start")
	}
	status := callTool[RunStatusOutput](t, fixture.client, "cancel_notebook_run", map[string]any{"run_id": accepted.RunID})
	if status.Status != "cancelling" && status.Status != "cancelled" {
		t.Fatalf("unexpected cancellation status: %+v", status)
	}
	backend.mu.Lock()
	cancels := backend.cancels
	backend.mu.Unlock()
	if cancels != 1 {
		t.Fatalf("backend cancel calls = %d, want 1", cancels)
	}
}

func TestValidatePreparedChangeDetectsHumanRevisionConflict(t *testing.T) {
	backend := fixtureBackend()
	fixture := connectTestServer(t, backend)
	defer fixture.close()
	prepared := callTool[PreparedChangeOutput](t, fixture.client, "prepare_notebook_change_set", map[string]any{
		"notebook_id": "notebook_opaque", "base_revision": "rev1",
		"operations": []map[string]any{{"kind": "cell.update", "cell_id": "cell_sql", "content": "select 2"}},
	})
	backend.mu.Lock()
	backend.notebook.Revision = "human-revision"
	backend.mu.Unlock()
	message := callToolError(t, fixture.client, "validate_notebook_change_set", map[string]any{"prepared_id": prepared.PreparedID})
	if !strings.Contains(message, "revision conflict") {
		t.Fatalf("unexpected conflict message: %s", message)
	}
}

func TestPreparedChangeExpiry(t *testing.T) {
	store := newChangeStore()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	stored, err := store.put("notebook", service.NotebookChangePlan{})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(preparedChangeTTL + time.Second)
	if _, err := store.get(stored.id); err == nil {
		t.Fatal("expired prepared change remained available")
	} else if !strings.Contains(err.Error(), "expired") {
		t.Fatalf("unexpected expiry error: %v", err)
	}
}

type serviceBackend struct {
	root      string
	service   *service.NotebookService
	workspace *service.WorkspaceService
}

func (b serviceBackend) Workspace(ctx context.Context) (model.WorkspaceState, error) {
	return b.workspace.ComputeState(ctx)
}

func (b serviceBackend) Notebook(_ context.Context, id string) (model.Notebook, error) {
	result, apiErr := b.service.Get(id)
	if apiErr != nil {
		return model.Notebook{}, apiErr
	}
	return result, nil
}

func (b serviceBackend) Runtime(_ context.Context, id string) (service.NotebookRuntimeSnapshot, error) {
	result, apiErr := b.service.Runtime(id)
	if apiErr != nil {
		return service.NotebookRuntimeSnapshot{}, apiErr
	}
	return result, nil
}

func (b serviceBackend) PrepareChangeSet(_ context.Context, id string, change service.NotebookChangeSet) (service.NotebookChangePlan, error) {
	result, apiErr := b.service.PrepareChangeSet(id, change)
	if apiErr != nil {
		return service.NotebookChangePlan{}, apiErr
	}
	return result, nil
}

func (b serviceBackend) ApplyChangeSet(_ context.Context, id string, change service.NotebookChangeSet) (service.NotebookChangeApplyResult, error) {
	result, apiErr := b.service.ApplyChangeSet(id, change)
	if apiErr != nil {
		return service.NotebookChangeApplyResult{}, apiErr
	}
	return result, nil
}

func (b serviceBackend) CheckVisualization(ctx context.Context, id string, request service.NotebookVisualizationCheckRequest) (service.NotebookVisualizationCheckResult, error) {
	result, apiErr := b.service.CheckVisualization(ctx, id, request)
	if apiErr != nil {
		return service.NotebookVisualizationCheckResult{}, apiErr
	}
	return result, nil
}

func (b serviceBackend) Run(ctx context.Context, id string, request service.RunNotebookRequest) (service.RunNotebookResult, error) {
	result, apiErr := b.service.Run(ctx, id, request)
	if apiErr != nil {
		return service.RunNotebookResult{}, apiErr
	}
	return result, nil
}

func (b serviceBackend) Cancel(ctx context.Context, id string) error {
	if apiErr := b.service.CancelRuns(ctx, id); apiErr != nil {
		return apiErr
	}
	return nil
}

func TestProtocolAppliesRealNotebookTransaction(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	workspace := service.NewWorkspaceService(root, "")
	notebookService := service.NewNotebookService(service.NotebookDependencies{
		WorkspaceRoot: root,
		CurrentState: func() model.WorkspaceState {
			state, _ := workspace.ComputeState(context.Background())
			return state
		},
	})
	created, apiErr := notebookService.Create(service.CreateNotebookRequest{Title: "Agent review"})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	fixture := connectTestServer(t, serviceBackend{root: root, service: notebookService, workspace: workspace})
	defer fixture.close()

	outline := callTool[NotebookOutlineOutput](t, fixture.client, "get_notebook_outline", map[string]any{"notebook_id": created.ID})
	if outline.Notebook.Revision != created.Revision || len(outline.Blocks) != 1 {
		t.Fatalf("unexpected real outline: %+v", outline)
	}
	prepared := callTool[PreparedChangeOutput](t, fixture.client, "prepare_notebook_change_set", map[string]any{
		"notebook_id":   created.ID,
		"base_revision": created.Revision,
		"operations": []map[string]any{
			{"kind": "cell.create", "name": "agent_totals", "language": "sql", "content": "select 42::bigint as total\n"},
			{"kind": "markdown.create", "content": "## Agent finding", "position": "end"},
		},
	})
	if !prepared.CanApply || len(prepared.Operations) != 2 || prepared.Operations[0].CellID == "" || prepared.Operations[1].BlockID == "" {
		t.Fatalf("real service did not normalize the change: %+v", prepared)
	}
	callTool[PreparedChangeOutput](t, fixture.client, "validate_notebook_change_set", map[string]any{"prepared_id": prepared.PreparedID})
	applied := callTool[ApplyChangeOutput](t, fixture.client, "apply_notebook_change_set", map[string]any{"prepared_id": prepared.PreparedID})
	if !applied.Applied || applied.Notebook.Revision != prepared.ExpectedRevision || applied.Notebook.CellCount != 2 {
		t.Fatalf("unexpected real apply: %+v", applied)
	}
	if _, err := os.Stat(filepath.Join(root, "notebooks", "agent-review", "agent_totals.sql")); err != nil {
		t.Fatalf("semantic apply did not create the SQL cell: %v", err)
	}
}
