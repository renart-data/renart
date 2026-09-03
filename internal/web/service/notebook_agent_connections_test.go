package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"renart/internal/web/model"
)

func TestNotebookAgentConnectionAccessIsTurnScopedAndBounded(t *testing.T) {
	t.Parallel()

	turnToken := make(chan string, 1)
	queryText := make(chan string, 1)
	state := model.WorkspaceState{
		SelectedEnvironment: "development",
		QueryConnections: []model.WorkspaceQueryConnection{
			{Name: "duckdb-default", ConnectionType: "duckdb", AssetType: "duckdb.sql", Dialect: "duckdb"},
			{Name: "postgres-analytics", ConnectionType: "postgres", AssetType: "pg.sql", Dialect: "postgres"},
		},
	}
	service := NewNotebookAgentService(context.Background(), NotebookAgentDependencies{
		CurrentState: func() model.WorkspaceState { return state },
		LookPath: func(file string) (string, error) {
			if file == "codex" {
				return "/usr/bin/codex", nil
			}
			return "", errors.New("missing")
		},
		RunProvider: func(ctx context.Context, request NotebookAgentProviderRunRequest, _ func(NotebookAgentStreamEvent)) (NotebookAgentProviderRunResult, error) {
			turnToken <- request.TurnToken
			<-ctx.Done()
			return NotebookAgentProviderRunResult{}, ctx.Err()
		},
		DiscoverDatabases: func(_ context.Context, connection, environment string) (SQLDatabaseDiscoveryResult, *APIError) {
			if connection != "postgres-analytics" || environment != "development" {
				t.Fatalf("unexpected database discovery binding: %q %q", connection, environment)
			}
			return SQLDatabaseDiscoveryResult{Status: "ok", Databases: []string{"analytics"}}, nil
		},
		DiscoverTables: func(_ context.Context, connection, database, environment string) (SQLTableDiscoveryResult, *APIError) {
			return SQLTableDiscoveryResult{
				Status: "ok", ConnectionName: connection, Database: database,
				Tables: []SQLDiscoveryTableItem{{Name: "public.orders", ShortName: "orders", SchemaName: "public", DatabaseName: database}},
			}, nil
		},
		DiscoverColumns: func(_ context.Context, connection, table, environment string) (SQLTableColumnsResult, int) {
			return SQLTableColumnsResult{
				Status: "ok", ConnectionName: connection, Table: table,
				Columns:   []SQLColumn{{Name: "order_id", Type: "bigint"}, {Name: "amount", Type: "numeric"}},
				RawOutput: "must never reach the agent",
			}, 200
		},
		RunConnectionQuery: func(_ context.Context, connection, environment, query string) ([]string, []map[string]any, error) {
			if connection != "postgres-analytics" || environment != "development" {
				t.Fatalf("unexpected sample query binding: %q %q", connection, environment)
			}
			queryText <- query
			return []string{"order_id", "note"}, []map[string]any{
				{"order_id": 1, "note": strings.Repeat("a", maxNotebookAgentSampleValue+50)},
				{"order_id": 2, "note": "second"},
				{"order_id": 3, "note": "limit sentinel"},
			}, nil
		},
		Now: func() time.Time { return time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC) },
	})
	t.Cleanup(service.Close)

	if _, apiErr := service.StartTurn("notebook-one", StartNotebookAgentTurnRequest{
		Provider: "codex", Mode: NotebookAgentModeEdit, Message: "Analyze warehouse orders",
	}); apiErr != nil {
		t.Fatal(apiErr)
	}
	var token string
	select {
	case token = <-turnToken:
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not expose its private turn token")
	}

	listed, apiErr := service.ListQueryConnections("notebook-one", token)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if got, want := len(listed.Connections), 2; got != want {
		t.Fatalf("connection count = %d, want %d", got, want)
	}
	if !listed.Connections[0].Granted || listed.Connections[1].Granted {
		t.Fatalf("unexpected initial grants: %+v", listed.Connections)
	}
	if _, apiErr := service.DiscoverConnectionCatalog(context.Background(), "notebook-one", token, NotebookAgentConnectionCatalogRequest{
		ConnectionName: "postgres-analytics", Level: "databases",
	}); apiErr == nil || apiErr.Code != "notebook_agent_connection_access_required" {
		t.Fatalf("unapproved discovery error = %+v", apiErr)
	}

	requestResult := make(chan NotebookAgentInteractionResult, 1)
	requestError := make(chan *APIError, 1)
	requestAccess := func() {
		result, requestErr := service.RequestConnectionAccess(
			context.Background(), "notebook-one", token,
			NotebookAgentConnectionAccessRequest{
				Title: "Use analytics Postgres", ConnectionType: "postgres",
			},
		)
		requestResult <- result
		requestError <- requestErr
	}
	go requestAccess()
	pending := waitForNotebookAgentState(t, service, "notebook-one", func(snapshot NotebookAgentSnapshot) bool {
		return snapshot.Interaction != nil && snapshot.Interaction.Status == "pending"
	})
	if pending.Interaction.Kind != NotebookAgentInteractionConnection || pending.Interaction.ConnectionRequest == nil {
		t.Fatalf("unexpected connection interaction: %+v", pending.Interaction)
	}
	if _, apiErr := service.AnswerInteraction(
		"notebook-one", pending.Interaction.ID, AnswerNotebookAgentInteractionRequest{Declined: true},
	); apiErr != nil {
		t.Fatal(apiErr)
	}
	if result, requestErr := <-requestResult, <-requestError; requestErr != nil || result.Status != "declined" {
		t.Fatalf("declined request = %+v, error = %+v", result, requestErr)
	}

	go requestAccess()
	pending = waitForNotebookAgentState(t, service, "notebook-one", func(snapshot NotebookAgentSnapshot) bool {
		return snapshot.Interaction != nil && snapshot.Interaction.Status == "pending" && snapshot.Interaction.ID != pending.Interaction.ID
	})
	answered, apiErr := service.AnswerInteraction(
		"notebook-one", pending.Interaction.ID,
		AnswerNotebookAgentInteractionRequest{ConnectionName: "postgres-analytics"},
	)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if answered.Interaction.Connection == nil || !answered.Interaction.Connection.Granted {
		t.Fatalf("approved connection was not recorded: %+v", answered.Interaction)
	}
	if result, requestErr := <-requestResult, <-requestError; requestErr != nil || result.Connection == nil || result.Connection.Name != "postgres-analytics" {
		t.Fatalf("approved request = %+v, error = %+v", result, requestErr)
	}

	databases, apiErr := service.DiscoverConnectionCatalog(context.Background(), "notebook-one", token, NotebookAgentConnectionCatalogRequest{
		ConnectionName: "postgres-analytics", Level: "databases",
	})
	if apiErr != nil || len(databases.Databases) != 1 || databases.Databases[0] != "analytics" {
		t.Fatalf("database discovery = %+v, error = %+v", databases, apiErr)
	}
	columns, apiErr := service.DiscoverConnectionCatalog(context.Background(), "notebook-one", token, NotebookAgentConnectionCatalogRequest{
		ConnectionName: "postgres-analytics", Level: "columns", Table: "public.orders",
	})
	if apiErr != nil || len(columns.Columns) != 2 || columns.SuggestedSource == nil || columns.SuggestedSource.Connection != "postgres-analytics" {
		t.Fatalf("column discovery = %+v, error = %+v", columns, apiErr)
	}

	sample, apiErr := service.QueryConnectionSample(context.Background(), "notebook-one", token, NotebookAgentConnectionSampleRequest{
		ConnectionName: "postgres-analytics", Query: "select * from public.orders", Limit: 2,
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if len(sample.Rows) != 2 || !sample.Truncated || sample.SuggestedSource.Connection != "postgres-analytics" || sample.SuggestedSource.SnapshotMode != "sample" {
		t.Fatalf("unexpected bounded sample: %+v", sample)
	}
	if note, _ := sample.Rows[0]["note"].(string); len(note) > maxNotebookAgentSampleValue {
		t.Fatalf("sample value was not capped: %d bytes", len(note))
	}
	if wrapped := <-queryText; !strings.Contains(wrapped, "LIMIT 3") || !strings.Contains(wrapped, "select * from public.orders") {
		t.Fatalf("sample query was not bounded: %s", wrapped)
	}
	if _, apiErr := service.QueryConnectionSample(context.Background(), "notebook-one", token, NotebookAgentConnectionSampleRequest{
		ConnectionName: "postgres-analytics", Query: "delete from public.orders",
	}); apiErr == nil || apiErr.Code != "notebook_agent_sample_query_not_read_only" {
		t.Fatalf("write query error = %+v", apiErr)
	}

	if _, apiErr := service.Cancel("notebook-one"); apiErr != nil {
		t.Fatal(apiErr)
	}
	if _, apiErr := service.ListQueryConnections("notebook-one", token); apiErr == nil || apiErr.Code != "notebook_agent_turn_token_invalid" {
		t.Fatalf("cancelled turn retained connection access: %+v", apiErr)
	}
}

func TestNotebookAgentConnectionRequestValidationAndSampleCapping(t *testing.T) {
	t.Parallel()

	request, apiErr := validateNotebookAgentConnectionRequest(NotebookAgentConnectionAccessRequest{Title: "Read source"})
	if apiErr != nil || len(request.Capabilities) != 2 {
		t.Fatalf("default connection capabilities = %+v, error = %+v", request, apiErr)
	}
	if _, apiErr := validateNotebookAgentConnectionRequest(NotebookAgentConnectionAccessRequest{
		Title: "Read source", ConnectionType: "not-a-warehouse",
	}); apiErr == nil || apiErr.Code != "notebook_agent_connection_type_unsupported" {
		t.Fatalf("unsupported type error = %+v", apiErr)
	}

	rows := make([]map[string]any, maxNotebookAgentSampleRows+5)
	for index := range rows {
		rows[index] = map[string]any{"id": index, "payload": strings.Repeat("x", 2048)}
	}
	bounded, truncated := capNotebookAgentSample([]string{"id", "payload"}, rows, maxNotebookAgentSampleRows)
	if !truncated || len(bounded) >= len(rows) {
		t.Fatalf("sample byte cap was not enforced: rows=%d truncated=%t", len(bounded), truncated)
	}
	if encoded := wrapNotebookAgentSampleQuery("select 1;", "synapse", 11); !strings.Contains(encoded, "TOP (11)") {
		t.Fatalf("synapse sample query did not use TOP: %s", encoded)
	}
}
