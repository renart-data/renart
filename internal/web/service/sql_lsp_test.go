package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"renart/internal/authoringdiag"
	"renart/internal/sqlintelligence"
	"renart/internal/sqllsp"
	"renart/internal/web/model"
)

func TestSQLDiagnosticParityAcrossTypeCheckHTTPAndStdio(t *testing.T) {
	parsed, typeCheckRoot := writeTypeCheckWorkspace(t, "name: a", map[string]string{
		"example_asset.sql": `
/* @bruin
name: a.example_asset
type: duckdb.sql
materialization:
  type: table
@bruin */
select a from (values (1), (2)) n(a)
`,
		"another_asset.sql": `
/* @bruin
name: a.another_asset
type: duckdb.sql
materialization:
  type: view
depends:
  - a.example_asset
@bruin */
select a, b from a.example_asset
`,
	})
	typeCheck := runTypeCheck(t, parsed, typeCheckRoot)
	typeCheckAsset := findAsset(t, typeCheck, "a.another_asset")
	typeCheckDiagnostic := findTypeCheckFindingByCode(typeCheckAsset.Findings, "unresolved-column")
	if typeCheckDiagnostic == nil {
		t.Fatalf("type-check did not report unresolved-column: %#v", typeCheckAsset.Findings)
	}

	state := model.WorkspaceState{Revision: 1, Pipelines: []model.Pipeline{{
		ID:   "pipeline",
		Name: "a",
		Assets: []model.Asset{
			{ID: "example-asset", Name: "a.example_asset", Type: "duckdb.sql", Path: "a/assets/example_asset.sql", Content: "select a from (values (1), (2)) n(a)"},
			{ID: "another-asset", Name: "a.another_asset", Type: "duckdb.sql", Path: "a/assets/another_asset.sql", Content: "select a, b from a.example_asset", Upstreams: []string{"a.example_asset"}},
		},
	}}}
	root := t.TempDir()
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: root,
		CurrentState:  func() model.WorkspaceState { return state },
	})
	httpResponse, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID: "another-asset",
		Content: "select a, b from a.example_asset",
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	httpDiagnostic := findLSPDiagnosticByCode(httpResponse.Diagnostics, "unresolved-column")
	if httpDiagnostic == nil {
		t.Fatalf("HTTP LSP did not report unresolved-column: %#v", httpResponse.Diagnostics)
	}

	graph := service.graphForState(context.Background(), state)
	server := sqllsp.NewServer(graph)
	uri := assetURI(root, state.Pipelines[0].Assets[1])
	openPayload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/didOpen",
		"params": map[string]any{"textDocument": map[string]any{
			"uri": uri, "languageId": "sql", "version": 1,
			"text": "select a, b from a.example_asset",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := server.Serve(context.Background(), bytes.NewReader(sqllsp.EncodeMessage(openPayload)), &output); err != nil {
		t.Fatal(err)
	}
	stdioDiagnostics := decodePublishedDiagnostics(t, output.Bytes())
	stdioDiagnostic := findLSPDiagnosticByCode(stdioDiagnostics, "unresolved-column")
	if stdioDiagnostic == nil {
		t.Fatalf("stdio LSP did not report unresolved-column: %#v", stdioDiagnostics)
	}

	if typeCheckDiagnostic.Message != httpDiagnostic.Message || typeCheckDiagnostic.Message != stdioDiagnostic.Message {
		t.Fatalf("diagnostic messages differ: typecheck=%q http=%q stdio=%q", typeCheckDiagnostic.Message, httpDiagnostic.Message, stdioDiagnostic.Message)
	}
	if typeCheckDiagnostic.Severity != "error" || httpDiagnostic.Severity != 1 || stdioDiagnostic.Severity != 1 {
		t.Fatalf("diagnostic severities differ: typecheck=%q http=%d stdio=%d", typeCheckDiagnostic.Severity, httpDiagnostic.Severity, stdioDiagnostic.Severity)
	}
	wantRange := sqllsp.Range{Start: sqllsp.Position{Line: 0, Character: 10}, End: sqllsp.Position{Line: 0, Character: 11}}
	if httpDiagnostic.Range != wantRange || stdioDiagnostic.Range != wantRange {
		t.Fatalf("diagnostic ranges differ: http=%#v stdio=%#v want=%#v", httpDiagnostic.Range, stdioDiagnostic.Range, wantRange)
	}
}

func TestSQLLSPServiceDoesNotTreatDuckDBCopyOptionsAsColumns(t *testing.T) {
	query := `COPY create_partitions.create_partitions
TO .data/create_all_partitions.sql
format csv
header FALSE
delimiter '\t'`
	state := model.WorkspaceState{Revision: 1, Pipelines: []model.Pipeline{{
		ID:   "pipeline",
		Name: "create_partitions",
		Assets: []model.Asset{
			{
				ID:      "source",
				Name:    "create_partitions.create_partitions",
				Type:    "duckdb.sql",
				Path:    "create_partitions/assets/create_partitions.sql",
				Content: "select 'statement' as partition",
				Columns: []model.Column{{Name: "partition", Type: "varchar"}},
			},
			{
				ID:      "copy",
				Name:    "create_partitions.export",
				Type:    "duckdb.sql",
				Path:    "create_partitions/assets/export.sql",
				Content: query,
			},
		},
	}}}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(),
		CurrentState:  func() model.WorkspaceState { return state },
	})

	response, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID: "copy",
		Content: query,
	})
	require.Nil(t, apiErr)
	for _, diagnostic := range response.Diagnostics {
		if diagnostic.Code == authoringdiag.CodeUnresolvedColumn {
			t.Fatalf("COPY format option was treated as a column: %#v", response.Diagnostics)
		}
	}
}

func TestAuthoringSchemaParityAcrossTypeCheckHTTPAndFilesystemLSP(t *testing.T) {
	parsed, root := writeTypeCheckWorkspace(t, `name: analytics
default_connections:
  duckdb: duckdb-default
`, map[string]string{
		"source.sql": `
/* @bruin
name: analytics.source
type: duckdb.sql
materialization:
  type: view
@bruin */
select 1::BIGINT as id
`,
		"mirror.asset.yml": `
name: analytics.mirror
type: load
depends:
  - analytics.source
parameters:
  source_table: analytics.source
  source_connection: duckdb-default
  destination_connection: duckdb-default
materialization:
  type: table
`,
		"report.sql": `
/* @bruin
name: analytics.report
type: duckdb.sql
materialization:
  type: view
depends:
  - analytics.mirror
@bruin */
select id, missing_column from analytics.mirror
`,
	})

	report := runTypeCheck(t, parsed, root)
	typecheckAsset := findAsset(t, report, "analytics.report")
	assert.False(t, hasFinding(typecheckAsset, typeCheckSeverityError, "Unresolved column: id"), "%+v", typecheckAsset.Findings)
	assert.True(t, hasFinding(typecheckAsset, typeCheckSeverityError, "Unresolved column: missing_column"), "%+v", typecheckAsset.Findings)

	workspace := NewWorkspaceService(root, filepath.Join(root, ".bruin.yml"))
	state, err := workspace.ComputeState(context.Background())
	require.NoError(t, err)
	var reportAsset model.Asset
	for _, candidate := range state.Pipelines[0].Assets {
		if candidate.Name == "analytics.report" {
			reportAsset = candidate
			break
		}
	}
	require.NotEmpty(t, reportAsset.ID)
	httpLSP := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: root,
		CurrentState:  func() model.WorkspaceState { return state },
	})
	httpResult, apiErr := httpLSP.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID: reportAsset.ID,
		Content: "select id, missing_column from analytics.mirror",
	})
	require.Nil(t, apiErr)
	assert.Nil(t, findLSPDiagnosticByMessage(httpResult.Diagnostics, "Unresolved column: id"))
	assert.NotNil(t, findLSPDiagnosticByMessage(httpResult.Diagnostics, "Unresolved column: missing_column"))

	filesystemGraph, err := LoadSQLLSPGraph(context.Background(), root)
	require.NoError(t, err)
	validationSchema, confidence := sqllsp.ValidationSchema(filesystemGraph)
	assert.Equal(t, "BIGINT", validationSchema["analytics.mirror"]["id"])
	assert.Equal(t, sqlintelligence.RelationKnown, confidence["analytics.mirror"])
	filesystemDiagnostics := sqllsp.NewEngine(filesystemGraph).Diagnostics(sqllsp.TextDocumentItem{
		URI:        sqllsp.FileURI(filepath.Join(root, "analytics", "assets", "report.sql")),
		LanguageID: "sql",
		Text:       "select id, missing_column from analytics.mirror",
	})
	assert.Nil(t, findLSPDiagnosticByMessage(filesystemDiagnostics, "Unresolved column: id"))
	assert.NotNil(t, findLSPDiagnosticByMessage(filesystemDiagnostics, "Unresolved column: missing_column"))
}

func TestSQLLSPServiceAllowsSelectedAssetReferencesInAdHocDocuments(t *testing.T) {
	state := model.WorkspaceState{Revision: 1, Pipelines: []model.Pipeline{{
		ID: "pipeline",
		Assets: []model.Asset{{
			ID: "report", Name: "analytics.report", Type: "duckdb.sql", Path: "analytics/assets/report.sql",
			Columns: []model.Column{{Name: "geometry"}, {Name: "properties"}, {Name: "type"}},
		}},
	}}}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(),
		CurrentState:  func() model.WorkspaceState { return state },
	})

	assetResponse, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID: "report",
		Content: "select * from analytics.report",
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if findLSPDiagnosticByCode(assetResponse.Diagnostics, "circular-dependency") == nil {
		t.Fatalf("asset document should still report self-reference: %#v", assetResponse.Diagnostics)
	}

	adhocResponse, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID:         "report",
		Content:         "select * from analytics.report",
		DocumentContext: "adhoc",
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if diagnostic := findLSPDiagnosticByCode(adhocResponse.Diagnostics, "circular-dependency"); diagnostic != nil {
		t.Fatalf("ad-hoc document inherited the context asset's self-reference rule: %#v", diagnostic)
	}

	customCheckResponse, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID:         "report",
		Content:         "select * from analytics.report",
		DocumentContext: "custom_check",
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if diagnostic := findLSPDiagnosticByCode(customCheckResponse.Diagnostics, "circular-dependency"); diagnostic != nil {
		t.Fatalf("custom check inherited the asset body's self-reference rule: %#v", diagnostic)
	}

	const scratchSQL = "select 1 as provider_id"
	assetContractResponse, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID: "report",
		Content: scratchSQL,
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if diagnostic := findLSPDiagnosticByCode(assetContractResponse.Diagnostics, authoringdiag.CodeDeclaredOutputSchemaDrift); diagnostic == nil {
		t.Fatalf("asset body should still be checked against declared output columns: %#v", assetContractResponse.Diagnostics)
	}

	for _, documentContext := range []string{"adhoc", "custom_check", "hook"} {
		response, requestErr := service.Diagnostics(context.Background(), SQLLSPRequest{
			AssetID:         "report",
			Content:         scratchSQL,
			DocumentContext: documentContext,
		})
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		for _, code := range []string{
			authoringdiag.CodeDeclaredOutputSchemaDrift,
			authoringdiag.CodeDeclaredColumnTypeDrift,
			authoringdiag.CodeDeclaredColumnNullabilityDrift,
		} {
			if diagnostic := findLSPDiagnosticByCode(response.Diagnostics, code); diagnostic != nil {
				t.Fatalf("%s document inherited the asset output contract: %#v", documentContext, diagnostic)
			}
		}
	}
}

func TestSQLLSPServiceCompletesDuckDBFileColumnsAndHonorsDisabledPolicy(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "example.csv"), []byte("id,name\n1,Ada\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := model.WorkspaceState{Revision: 1, Pipelines: []model.Pipeline{{
		ID: "pipeline",
		Assets: []model.Asset{{
			ID: "report", Name: "analytics.report", Type: "duckdb.sql", Path: "analytics/assets/report.sql",
		}},
	}}}
	newService := func(disabled bool) *SQLLSPService {
		return NewSQLLSPService(SQLLSPDependencies{
			WorkspaceRoot:           root,
			DisableFilesystemAccess: disabled,
			CurrentState:            func() model.WorkspaceState { return state },
		})
	}
	const sqlText = `select  from "./example.csv"`

	enabled := newService(false)
	completion, apiErr := enabled.Completions(context.Background(), SQLLSPRequest{
		AssetID: "report", Content: sqlText, Position: sqllsp.Position{Line: 0, Character: len("select ")},
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	labels := make([]string, 0, len(completion.Completions))
	for _, item := range completion.Completions {
		labels = append(labels, item.Label)
	}
	for _, expected := range []string{"id", "name"} {
		if !slices.Contains(labels, expected) {
			t.Fatalf("expected file column %q in completions, got %#v", expected, labels)
		}
	}
	diagnostics, apiErr := enabled.Diagnostics(context.Background(), SQLLSPRequest{AssetID: "report", Content: sqlText})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if diagnostic := findLSPDiagnosticByCode(diagnostics.Diagnostics, authoringdiag.CodeUnresolvedRelation); diagnostic != nil {
		t.Fatalf("enabled file relation was unresolved: %#v", diagnostics.Diagnostics)
	}

	disabled := newService(true)
	disabledCompletion, apiErr := disabled.Completions(context.Background(), SQLLSPRequest{
		AssetID: "report", Content: sqlText, Position: sqllsp.Position{Line: 0, Character: len("select ")},
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	for _, item := range disabledCompletion.Completions {
		if item.Label == "id" || item.Label == "name" {
			t.Fatalf("disabled file schema leaked column completion: %#v", disabledCompletion.Completions)
		}
	}
	diagnostics, apiErr = disabled.Diagnostics(context.Background(), SQLLSPRequest{AssetID: "report", Content: sqlText})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if diagnostic := findLSPDiagnosticByCode(diagnostics.Diagnostics, authoringdiag.CodeDuckDBFilesystemAccessDisabled); diagnostic == nil {
		t.Fatalf("expected disabled filesystem diagnostic, got %#v", diagnostics.Diagnostics)
	}
	if diagnostic := findLSPDiagnosticByCode(diagnostics.Diagnostics, authoringdiag.CodeUnresolvedRelation); diagnostic != nil {
		t.Fatalf("disabled policy should replace unresolved-relation noise: %#v", diagnostics.Diagnostics)
	}
}

func TestSQLLSPServiceUsesSelectedConnectionForDuckDBFileColumns(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "example.csv"), []byte("id,name\n1,Ada\n"), 0o600))
	state := model.WorkspaceState{
		Revision: 1,
		Connections: map[string]string{
			"postgres-default": "postgres",
			"duckdb-adhoc":     "duckdb",
		},
		Pipelines: []model.Pipeline{{
			ID: "pipeline",
			Assets: []model.Asset{{
				ID:         "report",
				Name:       "analytics.report",
				Type:       "pg.sql",
				Path:       "analytics/assets/report.sql",
				Connection: "postgres-default",
			}},
		}},
	}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: root,
		CurrentState:  func() model.WorkspaceState { return state },
	})
	const sqlText = `select  from "./example.csv"`

	response, apiErr := service.Completions(context.Background(), SQLLSPRequest{
		AssetID:         "report",
		Content:         sqlText,
		Connection:      "duckdb-adhoc",
		DocumentContext: "adhoc",
		Position:        sqllsp.Position{Line: 0, Character: len("select ")},
	})
	require.Nil(t, apiErr)
	labels := make([]string, 0, len(response.Completions))
	for _, item := range response.Completions {
		labels = append(labels, item.Label)
	}
	assert.Contains(t, labels, "id")
	assert.Contains(t, labels, "name")
}

func TestAmbiguousJoinColumnParityAcrossTypeCheckHTTPAndStdio(t *testing.T) {
	parsed, typeCheckRoot := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"users.sql": `
/* @bruin
name: analytics.users
type: duckdb.sql
columns:
  - name: id
    type: INTEGER
    primary_key: true
@bruin */
select 1 as id
`,
		"orders.sql": `
/* @bruin
name: analytics.orders
type: duckdb.sql
columns:
  - name: id
    type: INTEGER
  - name: user_id
    type: INTEGER
    foreign_key:
      table: analytics.users
      column: id
@bruin */
select 1 as id, 1 as user_id
`,
		"report.sql": `
/* @bruin
name: analytics.report
type: duckdb.sql
depends:
  - analytics.users
  - analytics.orders
@bruin */
select id from analytics.users u join analytics.orders o on u.id = o.user_id
`,
	})
	typeCheck := runTypeCheck(t, parsed, typeCheckRoot)
	typeCheckAsset := findAsset(t, typeCheck, "analytics.report")
	typeCheckDiagnostic := findTypeCheckFindingByCode(typeCheckAsset.Findings, authoringdiag.CodeUnresolvedColumn)
	if typeCheckDiagnostic == nil || !strings.Contains(typeCheckDiagnostic.Message, "Ambiguous unqualified column 'id'") {
		t.Fatalf("type-check did not report the ambiguous join column: %#v", typeCheckAsset.Findings)
	}

	root := t.TempDir()
	nullable := false
	state := model.WorkspaceState{Revision: 1, Pipelines: []model.Pipeline{{
		ID:   "pipeline",
		Name: "analytics",
		Assets: []model.Asset{
			{ID: "users", Name: "analytics.users", Type: "duckdb.sql", Path: "analytics/assets/users.sql", Content: "select 1 as id", Columns: []model.Column{{Name: "id", Type: "INTEGER", Nullable: &nullable, PrimaryKey: true}}},
			{ID: "orders", Name: "analytics.orders", Type: "duckdb.sql", Path: "analytics/assets/orders.sql", Content: "select 1 as id, 1 as user_id", Columns: []model.Column{
				{Name: "id", Type: "INTEGER"},
				{Name: "user_id", Type: "INTEGER", ForeignKey: &model.ColumnReference{Table: "analytics.users", Column: "id"}},
			}},
			{ID: "report", Name: "analytics.report", Type: "duckdb.sql", Path: "analytics/assets/report.sql", Content: "select 1", Upstreams: []string{"analytics.users", "analytics.orders"}},
		},
	}}}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: root,
		CurrentState:  func() model.WorkspaceState { return state },
	})
	const unsavedSQL = "select id from analytics.users u join analytics.orders o on u.id = o.user_id"
	httpResponse, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{AssetID: "report", Content: unsavedSQL})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	httpDiagnostic := findLSPDiagnosticByCode(httpResponse.Diagnostics, authoringdiag.CodeUnresolvedColumn)
	if httpDiagnostic == nil || !strings.Contains(httpDiagnostic.Message, "Ambiguous unqualified column 'id'") {
		t.Fatalf("HTTP LSP did not report the ambiguous join column: %#v", httpResponse.Diagnostics)
	}

	graph := service.graphForState(context.Background(), state)
	constraints := sqllsp.ValidationSchemaConstraints(graph)
	if foreignKey := constraints["analytics.orders"].Columns["user_id"].ForeignKey; foreignKey == nil || foreignKey.Table != "analytics.users" {
		t.Fatalf("web graph dropped declared constraints: %#v", constraints)
	}
	server := sqllsp.NewServer(graph)
	uri := assetURI(root, state.Pipelines[0].Assets[2])
	openPayload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/didOpen",
		"params": map[string]any{"textDocument": map[string]any{
			"uri": uri, "languageId": "sql", "version": 1, "text": unsavedSQL,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := server.Serve(context.Background(), bytes.NewReader(sqllsp.EncodeMessage(openPayload)), &output); err != nil {
		t.Fatal(err)
	}
	stdioDiagnostic := findLSPDiagnosticByCode(decodePublishedDiagnostics(t, output.Bytes()), authoringdiag.CodeUnresolvedColumn)
	if stdioDiagnostic == nil || !strings.Contains(stdioDiagnostic.Message, "Ambiguous unqualified column 'id'") {
		t.Fatal("stdio LSP did not report the ambiguous join column")
	}

	if typeCheckDiagnostic.Message != httpDiagnostic.Message || typeCheckDiagnostic.Message != stdioDiagnostic.Message {
		t.Fatalf("ambiguity messages differ: typecheck=%q http=%q stdio=%q", typeCheckDiagnostic.Message, httpDiagnostic.Message, stdioDiagnostic.Message)
	}
	wantRange := sqllsp.Range{Start: sqllsp.Position{Line: 0, Character: 7}, End: sqllsp.Position{Line: 0, Character: 9}}
	if httpDiagnostic.Range != wantRange || stdioDiagnostic.Range != wantRange {
		t.Fatalf("ambiguity ranges differ: http=%#v stdio=%#v want=%#v", httpDiagnostic.Range, stdioDiagnostic.Range, wantRange)
	}
}

func TestDeclaredOutputTypeDriftParityAcrossTypeCheckHTTPAndStdio(t *testing.T) {
	parsed, typeCheckRoot := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"typed_asset.sql": `
/* @bruin
name: analytics.typed_asset
type: duckdb.sql
materialization:
  type: table
columns:
  - name: id
    type: VARCHAR
@bruin */
select 1 as id
`,
	})
	typeCheck := runTypeCheck(t, parsed, typeCheckRoot)
	typeCheckAsset := findAsset(t, typeCheck, "analytics.typed_asset")
	typeCheckDiagnostic := findTypeCheckFindingByCode(typeCheckAsset.Findings, authoringdiag.CodeDeclaredColumnTypeDrift)
	if typeCheckDiagnostic == nil {
		t.Fatalf("type-check did not report declared output type drift: %#v", typeCheckAsset.Findings)
	}

	root := t.TempDir()
	state := model.WorkspaceState{Revision: 1, Pipelines: []model.Pipeline{{
		ID:   "pipeline",
		Name: "analytics",
		Assets: []model.Asset{{
			ID:      "typed-asset",
			Name:    "analytics.typed_asset",
			Type:    "duckdb.sql",
			Path:    "analytics/assets/typed_asset.sql",
			Content: "select 'saved value' as id",
			Columns: []model.Column{{Name: "id", Type: "VARCHAR"}},
		}},
	}}}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: root,
		CurrentState:  func() model.WorkspaceState { return state },
	})
	const unsavedSQL = "select 1 as id"
	httpResponse, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID: "typed-asset",
		Content: unsavedSQL,
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	httpDiagnostic := findLSPDiagnosticByCode(httpResponse.Diagnostics, authoringdiag.CodeDeclaredColumnTypeDrift)
	if httpDiagnostic == nil {
		t.Fatalf("HTTP LSP did not report drift from unsaved SQL: %#v", httpResponse.Diagnostics)
	}

	graph := service.graphForState(context.Background(), state)
	server := sqllsp.NewServer(graph)
	uri := assetURI(root, state.Pipelines[0].Assets[0])
	openPayload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/didOpen",
		"params": map[string]any{"textDocument": map[string]any{
			"uri": uri, "languageId": "sql", "version": 1, "text": unsavedSQL,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := server.Serve(context.Background(), bytes.NewReader(sqllsp.EncodeMessage(openPayload)), &output); err != nil {
		t.Fatal(err)
	}
	stdioDiagnostic := findLSPDiagnosticByCode(decodePublishedDiagnostics(t, output.Bytes()), authoringdiag.CodeDeclaredColumnTypeDrift)
	if stdioDiagnostic == nil {
		t.Fatal("stdio LSP did not report declared output type drift")
	}

	if typeCheckDiagnostic.Message != httpDiagnostic.Message || typeCheckDiagnostic.Message != stdioDiagnostic.Message {
		t.Fatalf("drift messages differ: typecheck=%q http=%q stdio=%q", typeCheckDiagnostic.Message, httpDiagnostic.Message, stdioDiagnostic.Message)
	}
	if typeCheckDiagnostic.Severity != "warning" || httpDiagnostic.Severity != 2 || stdioDiagnostic.Severity != 2 {
		t.Fatalf("drift severities differ: typecheck=%q http=%d stdio=%d", typeCheckDiagnostic.Severity, httpDiagnostic.Severity, stdioDiagnostic.Severity)
	}
	if httpDiagnostic.Scope != "asset" || stdioDiagnostic.Scope != "asset" {
		t.Fatalf("drift scope differs: http=%q stdio=%q", httpDiagnostic.Scope, stdioDiagnostic.Scope)
	}
}

func TestDeclaredOutputNullabilityDriftParityAcrossTypeCheckHTTPAndStdio(t *testing.T) {
	parsed, typeCheckRoot := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"nullable_asset.sql": `
/* @bruin
name: analytics.nullable_asset
type: duckdb.sql
materialization:
  type: table
columns:
  - name: id
    type: INTEGER
    nullable: false
@bruin */
select cast(null as integer) as id
`,
	})
	typeCheck := runTypeCheck(t, parsed, typeCheckRoot)
	typeCheckAsset := findAsset(t, typeCheck, "analytics.nullable_asset")
	typeCheckDiagnostic := findTypeCheckFindingByCode(typeCheckAsset.Findings, authoringdiag.CodeDeclaredColumnNullabilityDrift)
	if typeCheckDiagnostic == nil {
		t.Fatalf("type-check did not report declared output nullability drift: %#v", typeCheckAsset.Findings)
	}

	root := t.TempDir()
	notNullable := false
	state := model.WorkspaceState{Revision: 1, Pipelines: []model.Pipeline{{
		ID:   "pipeline",
		Name: "analytics",
		Assets: []model.Asset{{
			ID:      "nullable-asset",
			Name:    "analytics.nullable_asset",
			Type:    "duckdb.sql",
			Path:    "analytics/assets/nullable_asset.sql",
			Content: "select 1 as id",
			Columns: []model.Column{{Name: "id", Type: "INTEGER", Nullable: &notNullable}},
		}},
	}}}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: root,
		CurrentState:  func() model.WorkspaceState { return state },
	})
	const unsavedSQL = "select cast(null as integer) as id"
	httpResponse, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID: "nullable-asset",
		Content: unsavedSQL,
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	httpDiagnostic := findLSPDiagnosticByCode(httpResponse.Diagnostics, authoringdiag.CodeDeclaredColumnNullabilityDrift)
	if httpDiagnostic == nil {
		t.Fatalf("HTTP LSP did not report nullability drift from unsaved SQL: %#v", httpResponse.Diagnostics)
	}

	graph := service.graphForState(context.Background(), state)
	server := sqllsp.NewServer(graph)
	uri := assetURI(root, state.Pipelines[0].Assets[0])
	openPayload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/didOpen",
		"params": map[string]any{"textDocument": map[string]any{
			"uri": uri, "languageId": "sql", "version": 1, "text": unsavedSQL,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := server.Serve(context.Background(), bytes.NewReader(sqllsp.EncodeMessage(openPayload)), &output); err != nil {
		t.Fatal(err)
	}
	stdioDiagnostic := findLSPDiagnosticByCode(decodePublishedDiagnostics(t, output.Bytes()), authoringdiag.CodeDeclaredColumnNullabilityDrift)
	if stdioDiagnostic == nil {
		t.Fatal("stdio LSP did not report declared output nullability drift")
	}

	if typeCheckDiagnostic.Message != httpDiagnostic.Message || typeCheckDiagnostic.Message != stdioDiagnostic.Message {
		t.Fatalf("nullability drift messages differ: typecheck=%q http=%q stdio=%q", typeCheckDiagnostic.Message, httpDiagnostic.Message, stdioDiagnostic.Message)
	}
	if typeCheckDiagnostic.Severity != "warning" || httpDiagnostic.Severity != 2 || stdioDiagnostic.Severity != 2 {
		t.Fatalf("nullability drift severities differ: typecheck=%q http=%d stdio=%d", typeCheckDiagnostic.Severity, httpDiagnostic.Severity, stdioDiagnostic.Severity)
	}
	if httpDiagnostic.Scope != "asset" || stdioDiagnostic.Scope != "asset" {
		t.Fatalf("nullability drift scope differs: http=%q stdio=%q", httpDiagnostic.Scope, stdioDiagnostic.Scope)
	}
}

func TestDeclaredOutputNameDriftParityAcrossTypeCheckHTTPAndStdio(t *testing.T) {
	parsed, typeCheckRoot := writeTypeCheckWorkspace(t, "name: example", map[string]string{
		"range_10.sql": `
/* @bruin
name: example.range_10
type: duckdb.sql
materialization:
  type: view
columns:
  - name: range
@bruin */
select range as ronge from range(10)
`,
	})
	typeCheck := runTypeCheck(t, parsed, typeCheckRoot)
	typeCheckAsset := findAsset(t, typeCheck, "example.range_10")
	typeCheckDiagnostic := findTypeCheckFindingByCode(typeCheckAsset.Findings, authoringdiag.CodeDeclaredOutputSchemaDrift)
	if typeCheckDiagnostic == nil {
		t.Fatalf("type-check did not report declared output name drift: %#v", typeCheckAsset.Findings)
	}

	root := t.TempDir()
	state := model.WorkspaceState{Revision: 1, Pipelines: []model.Pipeline{{
		ID:   "pipeline",
		Name: "example",
		Assets: []model.Asset{{
			ID:      "range-10",
			Name:    "example.range_10",
			Type:    "duckdb.sql",
			Path:    "example/assets/example/range_10.sql",
			Content: "select range as range from range(10)",
			Columns: []model.Column{{Name: "range"}},
		}},
	}}}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: root,
		CurrentState:  func() model.WorkspaceState { return state },
	})
	const unsavedSQL = "select range as ronge from range(10)"
	httpResponse, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID: "range-10",
		Content: unsavedSQL,
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	httpDiagnostic := findLSPDiagnosticByCode(httpResponse.Diagnostics, authoringdiag.CodeDeclaredOutputSchemaDrift)
	if httpDiagnostic == nil {
		t.Fatalf("HTTP LSP did not report name drift from unsaved SQL: %#v", httpResponse.Diagnostics)
	}

	graph := service.graphForState(context.Background(), state)
	server := sqllsp.NewServer(graph)
	uri := assetURI(root, state.Pipelines[0].Assets[0])
	openPayload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/didOpen",
		"params": map[string]any{"textDocument": map[string]any{
			"uri": uri, "languageId": "sql", "version": 1, "text": unsavedSQL,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := server.Serve(context.Background(), bytes.NewReader(sqllsp.EncodeMessage(openPayload)), &output); err != nil {
		t.Fatal(err)
	}
	stdioDiagnostic := findLSPDiagnosticByCode(decodePublishedDiagnostics(t, output.Bytes()), authoringdiag.CodeDeclaredOutputSchemaDrift)
	if stdioDiagnostic == nil {
		t.Fatal("stdio LSP did not report declared output name drift")
	}

	if typeCheckDiagnostic.Message != httpDiagnostic.Message || typeCheckDiagnostic.Message != stdioDiagnostic.Message {
		t.Fatalf("drift messages differ: typecheck=%q http=%q stdio=%q", typeCheckDiagnostic.Message, httpDiagnostic.Message, stdioDiagnostic.Message)
	}
	if typeCheckDiagnostic.Severity != "warning" || httpDiagnostic.Severity != 2 || stdioDiagnostic.Severity != 2 {
		t.Fatalf("drift severities differ: typecheck=%q http=%d stdio=%d", typeCheckDiagnostic.Severity, httpDiagnostic.Severity, stdioDiagnostic.Severity)
	}
	if httpDiagnostic.Scope != "asset" || stdioDiagnostic.Scope != "asset" {
		t.Fatalf("drift scope differs: http=%q stdio=%q", httpDiagnostic.Scope, stdioDiagnostic.Scope)
	}
}

func findTypeCheckFindingByCode(findings []TypeCheckFinding, code string) *TypeCheckFinding {
	for i := range findings {
		if findings[i].Code == code {
			return &findings[i]
		}
	}
	return nil
}

func TestLSPAssetAdapterCoversEveryRegisteredHeaderDiagnostic(t *testing.T) {
	findings := make([]TypeCheckFinding, 0)
	wantCodes := map[string]bool{}
	for _, code := range authoringdiag.RegisteredTypeCheckCodes() {
		delivery, _ := authoringdiag.TypeCheckDelivery(code)
		if delivery != authoringdiag.DeliveryAssetHeader {
			continue
		}
		wantCodes[code] = true
		findings = append(findings, TypeCheckFinding{
			Code:       code,
			Source:     authoringdiag.SourceRenart,
			Severity:   typeCheckSeverityError,
			Message:    "fixture: " + code,
			Scope:      string(authoringdiag.ScopeAsset),
			Confidence: string(authoringdiag.ConfidenceHigh),
		})
	}

	diagnostics := lspAssetDiagnostics("select 1", findings)
	gotCodes := map[string]bool{}
	for _, diagnostic := range diagnostics {
		gotCodes[diagnostic.Code] = true
		if diagnostic.Scope != string(authoringdiag.ScopeAsset) {
			t.Fatalf("diagnostic %q lost asset scope: %#v", diagnostic.Code, diagnostic)
		}
	}
	if !maps.Equal(gotCodes, wantCodes) {
		t.Fatalf("asset/header adapter coverage differs: got=%v want=%v", gotCodes, wantCodes)
	}
}

func TestMissingNonSQLSchemaIsAnErrorAcrossTypecheckAndLSP(t *testing.T) {
	parsed, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"events.py": `
""" @bruin
name: analytics.events
type: python
materialization:
  type: table
@bruin """

def materialize():
    return [{"id": 1}]
`,
	})
	events := parsed.Assets[0]
	assetID := assetReportID(root, events)

	typecheck := runTypeCheck(t, parsed, root)
	typecheckFinding := findTypeCheckFindingByCode(
		findAsset(t, typecheck, events.Name).Findings,
		authoringdiag.CodeMissingDeclaredColumns,
	)
	require.NotNil(t, typecheckFinding)
	assert.Equal(t, typeCheckSeverityError, typecheckFinding.Severity)

	state := model.WorkspaceState{Revision: 1, Pipelines: []model.Pipeline{{
		ID: "analytics-pipeline",
		Assets: []model.Asset{{
			ID:      assetID,
			Name:    events.Name,
			Type:    string(events.Type),
			Path:    events.ExecutableFile.Path,
			Content: events.ExecutableFile.Content,
		}},
	}}}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: root,
		CurrentState:  func() model.WorkspaceState { return state },
		ResolveAssetByID: func(_ context.Context, requested string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			require.Equal(t, assetID, requested)
			return events.ExecutableFile.Path, parsed, events, nil
		},
	})
	httpResponse, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID: assetID,
		Content: "select 1",
	})
	require.Nil(t, apiErr)
	httpDiagnostic := findLSPDiagnosticByCode(httpResponse.Diagnostics, authoringdiag.CodeMissingDeclaredColumns)
	require.NotNil(t, httpDiagnostic)
	assert.Equal(t, 1, httpDiagnostic.Severity)
	assert.Equal(t, string(authoringdiag.ScopeAsset), httpDiagnostic.Scope)

	graph, err := LoadSQLLSPGraph(context.Background(), root)
	require.NoError(t, err)
	server := sqllsp.NewServer(graph)
	openPayload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/didOpen",
		"params": map[string]any{"textDocument": map[string]any{
			"uri": typeCheckAssetURI(root, events), "languageId": "python", "version": 1,
			"text": events.ExecutableFile.Content,
		}},
	})
	require.NoError(t, err)
	var output bytes.Buffer
	require.NoError(t, server.Serve(context.Background(), bytes.NewReader(sqllsp.EncodeMessage(openPayload)), &output))
	stdioDiagnostic := findLSPDiagnosticByCode(
		decodePublishedDiagnostics(t, output.Bytes()),
		authoringdiag.CodeMissingDeclaredColumns,
	)
	require.NotNil(t, stdioDiagnostic)
	assert.Equal(t, 1, stdioDiagnostic.Severity)
	assert.Equal(t, typecheckFinding.Message, httpDiagnostic.Message)
	assert.Equal(t, typecheckFinding.Message, stdioDiagnostic.Message)
}

func findLSPDiagnosticByCode(diagnostics []sqllsp.Diagnostic, code string) *sqllsp.Diagnostic {
	for i := range diagnostics {
		if diagnostics[i].Code == code {
			return &diagnostics[i]
		}
	}
	return nil
}

func findLSPDiagnosticByMessage(diagnostics []sqllsp.Diagnostic, message string) *sqllsp.Diagnostic {
	for index := range diagnostics {
		if strings.Contains(diagnostics[index].Message, message) {
			return &diagnostics[index]
		}
	}
	return nil
}

func decodePublishedDiagnostics(t *testing.T, framed []byte) []sqllsp.Diagnostic {
	t.Helper()
	separator := bytes.Index(framed, []byte("\r\n\r\n"))
	if separator < 0 {
		t.Fatalf("invalid LSP frame: %q", framed)
	}
	var message struct {
		Params struct {
			Diagnostics []sqllsp.Diagnostic `json:"diagnostics"`
		} `json:"params"`
	}
	if err := json.Unmarshal(framed[separator+4:], &message); err != nil {
		t.Fatal(err)
	}
	return message.Params.Diagnostics
}

func TestSQLLSPServiceCompletesFromRenartWorkspaceState(t *testing.T) {
	state := model.WorkspaceState{
		Pipelines: []model.Pipeline{{
			ID:   "pipeline",
			Name: "analytics",
			Assets: []model.Asset{
				{
					ID:      "orders",
					Name:    "analytics.orders",
					Type:    "duckdb.sql",
					Path:    "analytics/assets/analytics/orders.sql",
					Content: "select 1 as order_id, 10 as total_amount",
					Columns: []model.Column{
						{Name: "order_id", Type: "integer"},
						{Name: "total_amount", Type: "integer"},
					},
				},
				{
					ID:      "report",
					Name:    "analytics.report",
					Type:    "duckdb.sql",
					Path:    "analytics/assets/analytics/report.sql",
					Content: "select o.\nfrom analytics.orders o",
				},
			},
		}},
	}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(),
		CurrentState:  func() model.WorkspaceState { return state },
	})

	response, apiErr := service.Completions(context.Background(), SQLLSPRequest{
		AssetID:  "report",
		Content:  "select o.\nfrom analytics.orders o",
		Position: sqllsp.Position{Line: 0, Character: len("select o.")},
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	labels := map[string]bool{}
	for _, item := range response.Completions {
		labels[item.Label] = true
	}
	if !labels["order_id"] || !labels["total_amount"] {
		t.Fatalf("expected Renart workspace columns in completions, got %#v", response.Completions)
	}
}

func TestSQLLSPServiceCompletesStandalonePresentationQueryOnSelectedConnection(t *testing.T) {
	state := model.WorkspaceState{
		Revision:    1,
		Connections: map[string]string{"postgres-analytics": "postgres"},
		Pipelines: []model.Pipeline{{
			ID: "pipeline",
			Assets: []model.Asset{{
				ID:         "orders",
				Name:       "analytics.orders",
				Type:       "pg.sql",
				Path:       "analytics/assets/orders.sql",
				Connection: "postgres-analytics",
				Columns: []model.Column{
					{Name: "order_id", Type: "bigint"},
					{Name: "total_amount", Type: "numeric"},
				},
			}},
		}},
	}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(),
		CurrentState:  func() model.WorkspaceState { return state },
	})
	request := SQLLSPRequest{
		AssetID:         "dashboard:sales:orders",
		Content:         "select o.\nfrom analytics.orders o",
		Connection:      "postgres-analytics",
		DocumentContext: sqlLSPDocumentContextPresentationQuery,
		Position:        sqllsp.Position{Line: 0, Character: len("select o.")},
	}

	response, apiErr := service.Completions(context.Background(), request)
	require.Nil(t, apiErr)
	labels := make([]string, 0, len(response.Completions))
	for _, item := range response.Completions {
		labels = append(labels, item.Label)
	}
	assert.Contains(t, labels, "order_id")
	assert.Contains(t, labels, "total_amount")

	_, apiErr = service.References(context.Background(), request)
	require.Nil(t, apiErr, "standalone query references must not require a borrowed asset")

	request.Connection = "missing"
	_, apiErr = service.Completions(context.Background(), request)
	require.NotNil(t, apiErr)
	assert.Equal(t, "query_connection_required", apiErr.Code)
}

func TestSQLLSPServiceCompletesQuerySensorFromParameterSQL(t *testing.T) {
	state := model.WorkspaceState{
		Pipelines: []model.Pipeline{{
			ID:   "pipeline",
			Name: "analytics",
			Assets: []model.Asset{
				{
					ID:   "orders",
					Name: "analytics.orders",
					Type: "duckdb.sql",
					Path: "analytics/assets/analytics/orders.sql",
					Columns: []model.Column{
						{Name: "order_id", Type: "integer"},
						{Name: "total_amount", Type: "integer"},
					},
				},
				{
					ID:   "orders-ready",
					Name: "analytics.orders_ready",
					Type: "duckdb.sensor.query",
					Path: "analytics/assets/analytics/orders_ready.asset.yml",
					Parameters: map[string]string{
						"query": "select o.\nfrom analytics.orders o",
					},
				},
			},
		}},
	}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(),
		CurrentState:  func() model.WorkspaceState { return state },
	})

	response, apiErr := service.Completions(context.Background(), SQLLSPRequest{
		AssetID: "orders-ready",
		Position: sqllsp.Position{
			Line:      0,
			Character: len("select o."),
		},
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	labels := map[string]bool{}
	for _, item := range response.Completions {
		labels[item.Label] = true
	}
	if !labels["order_id"] || !labels["total_amount"] {
		t.Fatalf("expected workspace columns for query sensor SQL, got %#v", response.Completions)
	}
}

func TestSQLLSPServiceCompletesCustomCheckOnNonSQLAsset(t *testing.T) {
	state := model.WorkspaceState{
		Connections: map[string]string{"duckdb-default": "duckdb"},
		Pipelines: []model.Pipeline{{
			ID:   "pipeline",
			Name: "analytics",
			Assets: []model.Asset{{
				ID:         "regions-api",
				Name:       "analytics.regions_api",
				Type:       "api",
				Path:       "analytics/assets/analytics/regions_api.asset.yml",
				Connection: "duckdb-default",
				Columns: []model.Column{
					{Name: "region_id", Type: "integer"},
					{Name: "region_name", Type: "string"},
				},
			}},
		}},
	}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(),
		CurrentState:  func() model.WorkspaceState { return state },
	})

	response, apiErr := service.Completions(context.Background(), SQLLSPRequest{
		AssetID:         "regions-api",
		Content:         "select r.\nfrom analytics.regions_api r",
		DocumentContext: "custom_check",
		Position:        sqllsp.Position{Line: 0, Character: len("select r.")},
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	labels := map[string]bool{}
	for _, item := range response.Completions {
		labels[item.Label] = true
	}
	if !labels["region_id"] || !labels["region_name"] {
		t.Fatalf("expected custom-check columns for a non-SQL asset, got %#v", response.Completions)
	}
}

func TestSQLLSPServiceCompletesInferredRenartAssetColumns(t *testing.T) {
	state := model.WorkspaceState{
		Pipelines: []model.Pipeline{{
			ID:   "pipeline",
			Name: "analytics",
			Assets: []model.Asset{
				{
					ID:      "orders",
					Name:    "analytics.orders",
					Type:    "duckdb.sql",
					Path:    "analytics/assets/analytics/orders.sql",
					Content: "select 1 as order_id, 10 as total_amount",
				},
				{
					ID:      "report",
					Name:    "analytics.report",
					Type:    "duckdb.sql",
					Path:    "analytics/assets/analytics/report.sql",
					Content: "select o.\nfrom analytics.orders o",
				},
			},
		}},
	}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(),
		CurrentState:  func() model.WorkspaceState { return state },
	})

	response, apiErr := service.Completions(context.Background(), SQLLSPRequest{
		AssetID:  "report",
		Content:  "select o.\nfrom analytics.orders o",
		Position: sqllsp.Position{Line: 0, Character: len("select o.")},
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	labels := map[string]bool{}
	for _, item := range response.Completions {
		labels[item.Label] = true
	}
	if !labels["order_id"] || !labels["total_amount"] {
		t.Fatalf("expected inferred Renart workspace columns in completions, got %#v", response.Completions)
	}
}

func TestSQLLSPServiceCompletesEmptyProjectionForPythonQueryDocument(t *testing.T) {
	state := model.WorkspaceState{
		Revision: 1,
		Pipelines: []model.Pipeline{{
			ID:   "pipeline",
			Name: "analytics",
			Assets: []model.Asset{
				{
					ID:      "orders",
					Name:    "analytics.orders",
					Type:    "duckdb.sql",
					Path:    "analytics/assets/analytics/orders.sql",
					Content: "select 100 as order_id, 1 as customer_id, 42 as total_amount",
				},
				{
					ID:   "python-report",
					Name: "analytics.python_report",
					Type: "python",
					Path: "analytics/assets/analytics/python_report.py",
				},
			},
		}},
	}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(),
		CurrentState:  func() model.WorkspaceState { return state },
	})

	response, apiErr := service.Completions(context.Background(), SQLLSPRequest{
		AssetID: "python-report",
		Content: "select *, from analytics.orders",
		Position: sqllsp.Position{
			Line:      0,
			Character: len("select *, "),
		},
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	labels := map[string]bool{}
	for _, item := range response.Completions {
		labels[item.Label] = true
	}
	if !labels["order_id"] || !labels["customer_id"] {
		t.Fatalf("expected inferred columns at the empty Python SQL projection, got %#v", response.Completions)
	}
}

func TestSQLLSPServiceDiagnosesUnqualifiedColumnMissingFromInferredUpstream(t *testing.T) {
	state := model.WorkspaceState{
		Pipelines: []model.Pipeline{{
			ID:   "pipeline",
			Name: "a",
			Assets: []model.Asset{
				{
					ID:      "example-asset",
					Name:    "a.example_asset",
					Type:    "duckdb.sql",
					Path:    "a/assets/example_asset.sql",
					Content: "select a from (values (1), (2)) n(a)",
				},
				{
					ID:        "another-asset",
					Name:      "a.another_asset",
					Type:      "duckdb.sql",
					Path:      "a/assets/another_asset.sql",
					Content:   "select a, b from a.example_asset",
					Upstreams: []string{"a.example_asset"},
				},
			},
		}},
	}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(),
		CurrentState:  func() model.WorkspaceState { return state },
	})

	response, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID: "another-asset",
		Content: "select a, b from a.example_asset",
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}

	unresolved := make([]sqllsp.Diagnostic, 0)
	for _, diagnostic := range response.Diagnostics {
		if diagnostic.Code == "unresolved-column" {
			unresolved = append(unresolved, diagnostic)
		}
	}
	if len(unresolved) != 1 {
		t.Fatalf("expected exactly one unresolved-column diagnostic, got %#v", response.Diagnostics)
	}
	diagnostic := unresolved[0]
	if diagnostic.Message != "Unresolved column: b" {
		t.Fatalf("unexpected unresolved-column diagnostic: %#v", diagnostic)
	}
	if diagnostic.Range != (sqllsp.Range{
		Start: sqllsp.Position{Line: 0, Character: 10},
		End:   sqllsp.Position{Line: 0, Character: 11},
	}) {
		t.Fatalf("unexpected diagnostic range for b: %#v", diagnostic.Range)
	}

	qualified, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID: "another-asset",
		Content: "select e.b from a.example_asset e",
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	unresolved = unresolved[:0]
	for _, diagnostic := range qualified.Diagnostics {
		if diagnostic.Code == "unresolved-column" {
			unresolved = append(unresolved, diagnostic)
		}
	}
	if len(unresolved) != 1 || unresolved[0].Message != "Unresolved column: b" {
		t.Fatalf("expected the qualified diagnostic to be deduplicated, got %#v", qualified.Diagnostics)
	}
}

func TestSQLLSPServiceTreatsNonSQLAssetsAsDeclaredRelations(t *testing.T) {
	state := model.WorkspaceState{
		Pipelines: []model.Pipeline{{
			ID:   "pipeline",
			Name: "example",
			Assets: []model.Asset{
				{
					ID:   "api",
					Name: "example.my_api_asset_1",
					Type: "api",
					Path: "example/assets/my_api_asset_1.asset.yml",
					Columns: []model.Column{
						{Name: "id", Type: "string"},
						{Name: "status", Type: "string"},
					},
				},
				{
					ID:      "report",
					Name:    "example.report",
					Type:    "duckdb.sql",
					Path:    "example/assets/report.sql",
					Content: "select * from example.my_api_asset_1",
				},
			},
		}},
	}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(),
		CurrentState:  func() model.WorkspaceState { return state },
	})

	diagnostics, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID: "report",
		Content: "select * from example.my_api_asset_1",
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	for _, diagnostic := range diagnostics.Diagnostics {
		if diagnostic.Code == "unresolved-relation" {
			t.Fatalf("expected API asset relation to resolve, got diagnostics %#v", diagnostics.Diagnostics)
		}
	}

	completions, apiErr := service.Completions(context.Background(), SQLLSPRequest{
		AssetID:  "report",
		Content:  "select api.\nfrom example.my_api_asset_1 api",
		Position: sqllsp.Position{Line: 0, Character: len("select api.")},
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	labels := map[string]bool{}
	for _, item := range completions.Completions {
		labels[item.Label] = true
	}
	if !labels["id"] || !labels["status"] {
		t.Fatalf("expected API asset columns in completions, got %#v", completions.Completions)
	}
}

func TestSQLLSPServiceWarnsForCrossConnectionReference(t *testing.T) {
	state := model.WorkspaceState{
		Pipelines: []model.Pipeline{{
			ID:   "pipeline",
			Name: "analytics",
			Assets: []model.Asset{
				{
					ID:         "orders",
					Name:       "analytics.orders",
					Type:       "pg.sql",
					Path:       "analytics/assets/analytics/orders.sql",
					Connection: "postgres-default",
				},
				{
					ID:         "report",
					Name:       "analytics.report",
					Type:       "duckdb.sql",
					Path:       "analytics/assets/analytics/report.sql",
					Content:    "select * from analytics.orders",
					Connection: "duckdb-default",
				},
			},
		}},
	}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(),
		CurrentState:  func() model.WorkspaceState { return state },
	})

	response, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID: "report",
		Content: "select * from analytics.orders",
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	for _, diagnostic := range response.Diagnostics {
		if diagnostic.Code == "cross-connection-reference" {
			if diagnostic.Severity != 2 {
				t.Fatalf("expected warning severity, got %#v", diagnostic)
			}
			return
		}
	}
	t.Fatalf("expected cross-connection diagnostic, got %#v", response.Diagnostics)
}

func TestSQLLSPServiceUsesRequestConnectionForEmbeddedQuery(t *testing.T) {
	state := model.WorkspaceState{
		Connections: map[string]string{
			"duckdb-default":   "duckdb",
			"postgres-default": "postgres",
		},
		Pipelines: []model.Pipeline{{
			ID:   "pipeline",
			Name: "analytics",
			Assets: []model.Asset{
				{
					ID:         "orders",
					Name:       "analytics.orders",
					Type:       "pg.sql",
					Path:       "analytics/assets/analytics/orders.sql",
					Connection: "postgres-default",
				},
				{
					ID:         "task",
					Name:       "analytics.task",
					Type:       "python",
					Path:       "analytics/assets/analytics/task.py",
					Connection: "duckdb-default",
				},
			},
		}},
	}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(),
		CurrentState:  func() model.WorkspaceState { return state },
	})

	response, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID:    "task",
		Content:    "select * from analytics.orders",
		Connection: "postgres-default",
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	for _, diagnostic := range response.Diagnostics {
		if diagnostic.Code == "cross-connection-reference" {
			t.Fatalf("request connection should match the referenced asset: %#v", response.Diagnostics)
		}
	}
}

func TestGraphWithDocumentConnectionOverridesConnectionAndDialect(t *testing.T) {
	t.Parallel()

	uri := sqllsp.URI("file:///workspace/analytics/report.sql")
	graph := sqllsp.CanonicalGraph{Assets: []sqllsp.AssetNode{{
		ID:         "report",
		URI:        uri,
		Connection: "duckdb-default",
		Dialect:    "duckdb",
	}}}

	overridden := graphWithDocumentConnection(
		graph,
		uri,
		"databricks-default",
		map[string]string{"databricks-default": "databricks"},
	)

	require.Len(t, overridden.Assets, 1)
	assert.Equal(t, "databricks-default", overridden.Assets[0].Connection)
	assert.Equal(t, "databricks", overridden.Assets[0].Dialect)
	assert.Equal(t, "duckdb-default", graph.Assets[0].Connection, "cached graph must remain unchanged")
	assert.Equal(t, "duckdb", graph.Assets[0].Dialect, "cached graph must remain unchanged")
}

func TestSQLLSPServiceFindsReferencesAcrossWorkspaceAssets(t *testing.T) {
	state := model.WorkspaceState{
		Pipelines: []model.Pipeline{{
			ID:   "pipeline",
			Name: "analytics",
			Assets: []model.Asset{
				{
					ID:      "orders",
					Name:    "analytics.orders",
					Type:    "duckdb.sql",
					Path:    "analytics/assets/orders.sql",
					Content: "select 1 as order_id",
				},
				{
					ID:      "report",
					Name:    "analytics.report",
					Type:    "duckdb.sql",
					Path:    "analytics/assets/report.sql",
					Content: "select * from analytics.orders",
				},
				{
					ID:      "downstream",
					Name:    "analytics.downstream",
					Type:    "duckdb.sql",
					Path:    "analytics/assets/downstream.sql",
					Content: `select * from {{ ref("analytics.orders") }}`,
				},
				{
					ID:   "orders-ready",
					Name: "analytics.orders_ready",
					Type: "duckdb.sensor.query",
					Path: "analytics/assets/orders_ready.asset.yml",
					Parameters: map[string]string{
						"query": "select count(*) > 0 from analytics.orders",
					},
				},
			},
		}},
	}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(),
		CurrentState:  func() model.WorkspaceState { return state },
	})

	response, apiErr := service.References(context.Background(), SQLLSPRequest{
		AssetID:  "report",
		Content:  "select * from analytics.orders",
		Position: sqllsp.Position{Line: 0, Character: len("select * from analytics.ord")},
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if len(response.Locations) != 3 {
		t.Fatalf("expected references in report, downstream, and query sensor assets, got %#v", response.Locations)
	}
	foundReport := false
	foundDownstream := false
	foundSensor := false
	for _, location := range response.Locations {
		if strings.Contains(string(location.URI), "report.sql") {
			foundReport = true
		}
		if strings.Contains(string(location.URI), "downstream.sql") {
			foundDownstream = true
		}
		if strings.Contains(string(location.URI), "orders_ready.asset.yml") {
			foundSensor = true
		}
	}
	if !foundReport || !foundDownstream || !foundSensor {
		t.Fatalf("expected report, downstream, and query sensor references, got %#v", response.Locations)
	}
}

func sqlLSPCachingState(revision int64, extraColumn string) model.WorkspaceState {
	orders := model.Asset{
		ID:      "orders",
		Name:    "analytics.orders",
		Type:    "duckdb.sql",
		Path:    "analytics/assets/orders.sql",
		Content: "select 1 as order_id",
		Columns: []model.Column{{Name: "order_id", Type: "integer"}},
	}
	if extraColumn != "" {
		orders.Columns = append(orders.Columns, model.Column{Name: extraColumn, Type: "integer"})
	}
	return model.WorkspaceState{
		Revision: revision,
		Pipelines: []model.Pipeline{{
			ID:   "pipeline",
			Name: "analytics",
			Assets: []model.Asset{
				orders,
				{
					ID:      "report",
					Name:    "analytics.report",
					Type:    "duckdb.sql",
					Path:    "analytics/assets/report.sql",
					Content: "select o.\nfrom analytics.orders o",
				},
			},
		}},
	}
}

// The graph is derived only from workspace state, so it must be built once per
// Revision and reused across the many requests a single edit session fires.
func TestSQLLSPServiceCachesGraphByRevision(t *testing.T) {
	current := sqlLSPCachingState(1, "")
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(),
		CurrentState:  func() model.WorkspaceState { return current },
	})

	completionReq := SQLLSPRequest{
		AssetID:  "report",
		Content:  "select o.\nfrom analytics.orders o",
		Position: sqllsp.Position{Line: 0, Character: len("select o.")},
	}
	if _, apiErr := service.Completions(context.Background(), completionReq); apiErr != nil {
		t.Fatal(apiErr)
	}
	if _, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{AssetID: "report", Content: completionReq.Content}); apiErr != nil {
		t.Fatal(apiErr)
	}
	workspaceGraph, err := service.WorkspaceGraph(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(workspaceGraph.Assets) != 2 {
		t.Fatalf("expected the planner-facing workspace graph to reuse both state assets, got %#v", workspaceGraph.Assets)
	}
	if got := service.buildCount.Load(); got != 1 {
		t.Fatalf("expected a single graph build across editor and planner requests, got %d", got)
	}

	// A revision bump with a changed schema must invalidate the cache and surface
	// the new column.
	current = sqlLSPCachingState(2, "extra_col")
	resp, apiErr := service.Completions(context.Background(), completionReq)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	labels := map[string]bool{}
	for _, item := range resp.Completions {
		labels[item.Label] = true
	}
	if !labels["extra_col"] {
		t.Fatalf("expected new column surfaced after revision bump, got %#v", resp.Completions)
	}
	if got := service.buildCount.Load(); got != 2 {
		t.Fatalf("expected exactly one rebuild after revision bump, got build count %d", got)
	}
}

func TestSQLLSPServiceCachesAssetHeaderDiagnosticsByRevision(t *testing.T) {
	parsed, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"reader.sql": `
/* @bruin
name: analytics.reader
type: duckdb.sql
materialization:
  type: view
depends:
  - analytics.missing
@bruin */
select 1 as id
`,
	})
	reader := parsed.Assets[0]
	assetID := assetReportID(root, reader)
	current := model.WorkspaceState{Revision: 1, Pipelines: []model.Pipeline{{
		ID:   "analytics-pipeline",
		Name: "analytics",
		Assets: []model.Asset{{
			ID:      assetID,
			Name:    reader.Name,
			Type:    string(reader.Type),
			Path:    reader.ExecutableFile.Path,
			Content: reader.ExecutableFile.Content,
		}},
	}}}
	resolverCalls := 0
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: root,
		CurrentState:  func() model.WorkspaceState { return current },
		ResolveAssetByID: func(_ context.Context, requested string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			resolverCalls++
			if requested != assetID {
				return "", nil, nil, fmt.Errorf("unexpected asset %q", requested)
			}
			return reader.ExecutableFile.Path, parsed, reader, nil
		},
	})

	request := SQLLSPRequest{AssetID: assetID, Content: reader.ExecutableFile.Content}
	for attempt := 0; attempt < 2; attempt++ {
		response, apiErr := service.Diagnostics(context.Background(), request)
		if apiErr != nil {
			t.Fatal(apiErr)
		}
		diagnostic := findLSPDiagnosticByCode(response.Diagnostics, "missing-dependency")
		if diagnostic == nil {
			t.Fatalf("expected missing dependency asset diagnostic, got %#v", response.Diagnostics)
		}
		if diagnostic.Scope != "asset" || diagnostic.Range.Start != (sqllsp.Position{}) {
			t.Fatalf("expected range-honest asset/header diagnostic, got %#v", diagnostic)
		}
	}
	if resolverCalls != 1 {
		t.Fatalf("expected one full pipeline check for a saved revision, got %d", resolverCalls)
	}

	current.Revision = 2
	if _, apiErr := service.Diagnostics(context.Background(), request); apiErr != nil {
		t.Fatal(apiErr)
	}
	if resolverCalls != 2 {
		t.Fatalf("expected revision bump to invalidate asset diagnostics, got %d resolver calls", resolverCalls)
	}
}

func TestSQLLSPServicePublishesWorkspaceDependencyDiagnosticsAtAssetHeader(t *testing.T) {
	parsed, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"reader.sql": `
/* @bruin
name: analytics.reader
type: duckdb.sql
@bruin */
select 1 as id
`,
	})
	reader := parsed.Assets[0]
	assetID := assetReportID(root, reader)
	state := model.WorkspaceState{
		Revision: 1,
		Pipelines: []model.Pipeline{{
			ID: "analytics-pipeline",
			Assets: []model.Asset{{
				ID: assetID, Name: reader.Name, Type: string(reader.Type),
				Path: reader.ExecutableFile.Path, Content: reader.ExecutableFile.Content,
			}},
		}},
		DependencyDiagnostics: []model.WorkspaceDependencyDiagnostic{{
			AssetID: assetID, PipelineID: "analytics-pipeline",
			Code: "cross-pipeline-unresolved-uri", Severity: "warning",
			Message: "Cross-pipeline URI dependency does not resolve",
		}},
	}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: root,
		CurrentState:  func() model.WorkspaceState { return state },
		ResolveAssetByID: func(_ context.Context, requested string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			if requested != assetID {
				return "", nil, nil, fmt.Errorf("unexpected asset %q", requested)
			}
			return reader.ExecutableFile.Path, parsed, reader, nil
		},
	})

	response, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID: assetID,
		Content: reader.ExecutableFile.Content,
	})
	require.Nil(t, apiErr)
	diagnostic := findLSPDiagnosticByCode(response.Diagnostics, "cross-pipeline-unresolved-uri")
	require.NotNil(t, diagnostic)
	assert.Equal(t, 2, diagnostic.Severity)
	assert.Equal(t, "asset", diagnostic.Scope)
	assert.Equal(t, sqllsp.Position{}, diagnostic.Range.Start)
}

func TestSQLLSPServiceRetriesAssetDiagnosticsAfterCancellation(t *testing.T) {
	parsed, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"reader.sql": `
/* @bruin
name: analytics.reader
type: duckdb.sql
@bruin */
select 1 as id
`,
	})
	reader := parsed.Assets[0]
	assetID := assetReportID(root, reader)
	resolverCalls := 0
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: root,
		ResolveAssetByID: func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			resolverCalls++
			return reader.ExecutableFile.Path, parsed, reader, nil
		},
	})

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := service.cachedAssetFindings(canceled, 1, "analytics", assetID); ok {
		t.Fatal("canceled asset diagnostics unexpectedly became cacheable")
	}
	if _, ok := service.cachedAssetFindings(context.Background(), 1, "analytics", assetID); !ok {
		t.Fatal("active request did not retry canceled asset diagnostics")
	}
	if resolverCalls != 2 {
		t.Fatalf("expected canceled build plus one retry, got %d resolver calls", resolverCalls)
	}
}

func TestSQLLSPServiceSingleFlightsConcurrentAssetDiagnostics(t *testing.T) {
	parsed, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"reader.sql": `
/* @bruin
name: analytics.reader
type: duckdb.sql
@bruin */
select 1 as id
`,
	})
	reader := parsed.Assets[0]
	assetID := assetReportID(root, reader)
	resolverStarted := make(chan struct{})
	releaseResolver := make(chan struct{})
	resolverCalls := 0
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: root,
		ResolveAssetByID: func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			resolverCalls++
			close(resolverStarted)
			<-releaseResolver
			return reader.ExecutableFile.Path, parsed, reader, nil
		},
	})

	firstResult := make(chan bool, 1)
	go func() {
		_, ok := service.cachedAssetFindings(context.Background(), 1, "analytics", assetID)
		firstResult <- ok
	}()
	<-resolverStarted

	waiterCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, ok := service.cachedAssetFindings(waiterCtx, 1, "analytics", assetID); ok {
		t.Fatal("canceled waiter unexpectedly received in-flight findings")
	}

	thirdResult := make(chan bool, 1)
	go func() {
		_, ok := service.cachedAssetFindings(context.Background(), 1, "analytics", assetID)
		thirdResult <- ok
	}()
	close(releaseResolver)
	if !<-firstResult || !<-thirdResult {
		t.Fatal("active requests did not receive single-flight findings")
	}
	if resolverCalls != 1 {
		t.Fatalf("expected one resolver call for concurrent requests, got %d", resolverCalls)
	}
}

func TestFilesystemStdioLSPPublishesTypecheckAssetDiagnostics(t *testing.T) {
	parsed, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"reader.sql": `
/* @bruin
name: analytics.reader
type: duckdb.sql
materialization:
  type: view
depends:
  - analytics.missing
@bruin */
select 1 as id
`,
	})
	graph, err := LoadSQLLSPGraph(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	reader := parsed.Assets[0]
	uri := typeCheckAssetURI(root, reader)
	server := sqllsp.NewServer(graph)
	openPayload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "textDocument/didOpen",
		"params": map[string]any{"textDocument": map[string]any{
			"uri": uri, "languageId": "sql", "version": 1,
			"text": reader.ExecutableFile.Content,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := server.Serve(context.Background(), bytes.NewReader(sqllsp.EncodeMessage(openPayload)), &output); err != nil {
		t.Fatal(err)
	}
	diagnostics := decodePublishedDiagnostics(t, output.Bytes())
	diagnostic := findLSPDiagnosticByCode(diagnostics, "missing-dependency")
	if diagnostic == nil {
		t.Fatalf("expected stdio missing dependency diagnostic, got %#v", diagnostics)
	}
	if diagnostic.Scope != "asset" || diagnostic.Range.Start != (sqllsp.Position{}) {
		t.Fatalf("expected range-honest stdio asset/header diagnostic, got %#v", diagnostic)
	}
}

// A Revision of 0 signals an unmanaged/initial state, which must never be cached
// so callers cannot be served a stale graph.
func TestSQLLSPServiceDoesNotCacheUnversionedState(t *testing.T) {
	current := sqlLSPCachingState(0, "")
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(),
		CurrentState:  func() model.WorkspaceState { return current },
	})

	req := SQLLSPRequest{AssetID: "report", Content: "select o.\nfrom analytics.orders o"}
	if _, apiErr := service.Diagnostics(context.Background(), req); apiErr != nil {
		t.Fatal(apiErr)
	}
	if _, apiErr := service.Diagnostics(context.Background(), req); apiErr != nil {
		t.Fatal(apiErr)
	}
	if got := service.buildCount.Load(); got != 2 {
		t.Fatalf("expected a rebuild per request for unversioned state, got build count %d", got)
	}
}

func TestSQLLSPServiceExposesEditorFeatureEndpoints(t *testing.T) {
	state := model.WorkspaceState{
		Pipelines: []model.Pipeline{{
			ID:   "pipeline",
			Name: "analytics",
			Assets: []model.Asset{
				{
					ID:      "orders",
					Name:    "analytics.orders",
					Type:    "duckdb.sql",
					Path:    "analytics/assets/orders.sql",
					Content: "select 1 as order_id, 10 as total_amount",
					Columns: []model.Column{
						{Name: "order_id", Type: "integer"},
						{Name: "total_amount", Type: "integer"},
					},
				},
				{
					ID:      "report",
					Name:    "analytics.report",
					Type:    "duckdb.sql",
					Path:    "analytics/assets/report.sql",
					Content: "select o.order_id from analytics.orders o",
				},
			},
		}},
	}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(),
		CurrentState:  func() model.WorkspaceState { return state },
	})

	tokens, apiErr := service.SemanticTokens(context.Background(), SQLLSPRequest{
		AssetID: "report",
		Content: "select o.order_id from analytics.orders o",
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if tokens.Tokens == nil || len(tokens.Tokens.Data) == 0 || tokens.TokenLegend == nil {
		t.Fatalf("expected semantic token data and legend, got %#v", tokens)
	}

	symbols, apiErr := service.DocumentSymbols(context.Background(), SQLLSPRequest{
		AssetID: "report",
		Content: "select o.order_id from analytics.orders o",
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if len(symbols.Symbols) == 0 {
		t.Fatalf("expected document symbols, got %#v", symbols)
	}

	signature, apiErr := service.SignatureHelp(context.Background(), SQLLSPRequest{
		AssetID:  "report",
		Content:  "insert into analytics.orders values (1, ",
		Position: sqllsp.Position{Line: 0, Character: len("insert into analytics.orders values (1, ")},
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if signature.Signature == nil || signature.Signature.ActiveParameter != 1 {
		t.Fatalf("expected insert signature help, got %#v", signature)
	}

	formatting, apiErr := service.Formatting(context.Background(), SQLLSPRequest{
		AssetID: "report",
		Content: "select  o.order_id  from analytics.orders o",
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if formatting.Edit == nil || len(formatting.Edit.Changes) == 0 {
		t.Fatalf("expected formatting edit, got %#v", formatting)
	}
}

func TestSQLLSPServiceReportsWhyTemplatedRenameIsUnavailable(t *testing.T) {
	state := model.WorkspaceState{
		Pipelines: []model.Pipeline{{
			ID:   "pipeline",
			Name: "analytics",
			Assets: []model.Asset{{
				ID:      "report",
				Name:    "analytics.report",
				Type:    "duckdb.sql",
				Path:    "analytics/assets/report.sql",
				Content: "select o.order_id from {{ ref(\"analytics.orders\") }} o",
			}},
		}},
	}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(),
		CurrentState:  func() model.WorkspaceState { return state },
	})

	response, apiErr := service.Rename(context.Background(), SQLLSPRequest{
		AssetID:  "report",
		Content:  "select o.order_id from {{ ref(\"analytics.orders\") }} o",
		Position: sqllsp.Position{Line: 0, Character: len("select o")},
		NewName:  "ord",
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if response.Status != "error" || response.Error == "" {
		t.Fatalf("expected an error status with a reason, got %#v", response)
	}
	if response.Edit != nil {
		t.Fatalf("expected no edit for a templated document, got %#v", response.Edit)
	}
}

func notebookLSPState() model.WorkspaceState {
	return model.WorkspaceState{
		Pipelines: []model.Pipeline{{
			ID:   "pipeline",
			Name: "analytics",
			Assets: []model.Asset{{
				ID:      "orders",
				Name:    "analytics.orders",
				Type:    "duckdb.sql",
				Path:    "analytics/assets/orders.sql",
				Content: "select 1 as order_id, 10 as total_amount",
				Columns: []model.Column{
					{Name: "order_id", Type: "integer"},
					{Name: "total_amount", Type: "integer"},
				},
			}},
		}},
		Notebooks: []model.Notebook{
			{
				ID:    "nb1",
				Title: "Revenue",
				Path:  "notebooks/revenue",
				Cells: []model.Asset{
					{
						ID:      "nb1-base",
						Name:    "base_stats",
						Type:    "duckdb.sql",
						Path:    "notebooks/revenue/cells/base_stats.sql",
						Content: "select 1 as metric_day, 2 as metric_value",
						Class:   "notebook",
						CellID:  "uuid1:base_stats",
					},
					{
						ID:           "nb1-summary",
						Name:         "summary",
						Type:         "duckdb.sql",
						Path:         "notebooks/revenue/cells/summary.sql",
						Content:      "select b.metric_value from base_stats b",
						Class:        "notebook",
						CellID:       "uuid1:summary",
						ExternalRefs: []string{"raw.events"},
					},
				},
			},
			{
				ID:    "nb2",
				Title: "Other",
				Path:  "notebooks/other",
				Cells: []model.Asset{{
					ID:      "nb2-cell",
					Name:    "other_cell",
					Type:    "duckdb.sql",
					Path:    "notebooks/other/cells/other_cell.sql",
					Content: "select 1 as other_metric",
					Class:   "notebook",
					CellID:  "uuid2:other_cell",
				}},
			},
		},
	}
}

func notebookLSPService(t *testing.T, state model.WorkspaceState) *SQLLSPService {
	t.Helper()
	return NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(),
		CurrentState:  func() model.WorkspaceState { return state },
	})
}

func TestSQLLSPServiceCompletesSiblingNotebookCellColumns(t *testing.T) {
	service := notebookLSPService(t, notebookLSPState())

	// base_stats declares no columns in state, so they are inferred from its
	// SQL; the sibling cell should still see them behind the alias.
	response, apiErr := service.Completions(context.Background(), SQLLSPRequest{
		AssetID:  "nb1-summary",
		Content:  "select b.\nfrom base_stats b",
		Position: sqllsp.Position{Line: 0, Character: len("select b.")},
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	labels := map[string]bool{}
	for _, item := range response.Completions {
		labels[item.Label] = true
	}
	if !labels["metric_day"] || !labels["metric_value"] {
		t.Fatalf("expected sibling cell columns in completions, got %#v", response.Completions)
	}

	// Pipeline assets stay visible from notebook cells.
	pipelineResponse, apiErr := service.Completions(context.Background(), SQLLSPRequest{
		AssetID:  "nb1-summary",
		Content:  "select o.\nfrom analytics.orders o",
		Position: sqllsp.Position{Line: 0, Character: len("select o.")},
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	pipelineLabels := map[string]bool{}
	for _, item := range pipelineResponse.Completions {
		pipelineLabels[item.Label] = true
	}
	if !pipelineLabels["order_id"] || !pipelineLabels["total_amount"] {
		t.Fatalf("expected pipeline asset columns in notebook completions, got %#v", pipelineResponse.Completions)
	}
}

func TestSQLLSPServiceCompletesCTEAfterLeadingVizDirectiveInNotebook(t *testing.T) {
	service := notebookLSPService(t, notebookLSPState())
	query := `/* @viz(line, x: count, y: count_star()) */
with preagg as (
  select 1::bigint as count, 2::bigint as count_star
)
select
from preagg`
	response, apiErr := service.Completions(context.Background(), SQLLSPRequest{
		AssetID:  "nb1-summary",
		Content:  query,
		Position: sqllsp.PositionAt(query, strings.Index(query, "\nfrom preagg")),
	})
	require.Nil(t, apiErr)
	labels := make([]string, 0, len(response.Completions))
	for _, item := range response.Completions {
		labels = append(labels, item.Label)
	}
	assert.Contains(t, labels, "count")
	assert.Contains(t, labels, "count_star")
}

func TestSQLLSPServiceNotebookDiagnosticsResolveSiblingsAndExternalRefs(t *testing.T) {
	service := notebookLSPService(t, notebookLSPState())

	response, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID: "nb1-summary",
		Content: "select b.metric_value from base_stats b join raw.events e on e.id = b.metric_day",
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	for _, diagnostic := range response.Diagnostics {
		if diagnostic.Code == "unresolved-relation" {
			t.Fatalf("expected sibling cell and external ref to resolve, got %#v", diagnostic)
		}
	}
}

func TestSQLLSPServiceDoesNotExposeLocalSiblingRelationsToWarehouseSourceCell(t *testing.T) {
	state := notebookLSPState()
	state.Connections = map[string]string{"postgres-other": "postgres"}
	for index := range state.Notebooks[0].Cells {
		if state.Notebooks[0].Cells[index].ID == "nb1-summary" {
			state.Notebooks[0].Cells[index].Type = "pg.sql"
			state.Notebooks[0].Cells[index].Connection = "postgres-other"
			state.Notebooks[0].Cells[index].ExternalRefs = nil
		}
	}
	service := notebookLSPService(t, state)
	response, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID: "nb1-summary",
		Content: "select b.metric_value from base_stats b",
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if !hasDiagnosticCode(response.Diagnostics, "unresolved-relation") {
		t.Fatalf("remote source cell incorrectly saw a local notebook relation: %#v", response.Diagnostics)
	}
}

func TestSQLLSPServiceScopesNotebookCellsToTheirNotebook(t *testing.T) {
	service := notebookLSPService(t, notebookLSPState())

	// A cell from another notebook must not resolve.
	crossNotebook, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID: "nb1-summary",
		Content: "select * from other_cell",
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if !hasDiagnosticCode(crossNotebook.Diagnostics, "unresolved-relation") {
		t.Fatalf("expected another notebook's cell to stay unresolved, got %#v", crossNotebook.Diagnostics)
	}

	// Pipeline asset requests must not see notebook cells.
	fromPipeline, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID: "orders",
		Content: "select * from base_stats",
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if !hasDiagnosticCode(fromPipeline.Diagnostics, "unresolved-relation") {
		t.Fatalf("expected notebook cells to be invisible to pipeline assets, got %#v", fromPipeline.Diagnostics)
	}
}

func TestSQLLSPServiceInfersColumnsThroughSelectStarChains(t *testing.T) {
	state := model.WorkspaceState{
		Pipelines: []model.Pipeline{{
			ID:   "pipeline",
			Name: "analytics",
			Assets: []model.Asset{
				{
					ID:      "base",
					Name:    "analytics.base",
					Type:    "duckdb.sql",
					Path:    "analytics/assets/base.sql",
					Content: "select 1 as x, 2 as y",
				},
				{
					ID:      "mid",
					Name:    "analytics.mid",
					Type:    "duckdb.sql",
					Path:    "analytics/assets/mid.sql",
					Content: "select * from analytics.base",
				},
				{
					ID:      "report",
					Name:    "analytics.report",
					Type:    "duckdb.sql",
					Path:    "analytics/assets/report.sql",
					Content: "select m.x from analytics.mid m",
				},
			},
		}},
	}
	service := notebookLSPService(t, state)

	// mid's columns exist only via inference *through* base's inferred columns:
	// a single inference pass cannot see them.
	response, apiErr := service.Completions(context.Background(), SQLLSPRequest{
		AssetID:  "report",
		Content:  "select m.\nfrom analytics.mid m",
		Position: sqllsp.Position{Line: 0, Character: len("select m.")},
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	labels := map[string]bool{}
	for _, item := range response.Completions {
		labels[item.Label] = true
	}
	if !labels["x"] || !labels["y"] {
		t.Fatalf("expected columns to propagate through the select * chain, got %#v", response.Completions)
	}
}

func TestSQLLSPServiceInfersColumnsThroughChainsDeeperThanTheRoundCap(t *testing.T) {
	// A select * chain deeper than maxInferenceRounds, listed downstream-first
	// so per-round propagation alone cannot resolve it: only the topological
	// ordering by declared upstreams lets one round walk the whole chain.
	assets := []model.Asset{{
		ID:      "base",
		Name:    "analytics.base",
		Type:    "duckdb.sql",
		Path:    "analytics/assets/base.sql",
		Content: "select 1 as x, 2 as y",
	}}
	previous := "analytics.base"
	for i := 1; i <= 6; i++ {
		name := fmt.Sprintf("analytics.c%d", i)
		assets = append([]model.Asset{{
			ID:        fmt.Sprintf("c%d", i),
			Name:      name,
			Type:      "duckdb.sql",
			Path:      fmt.Sprintf("analytics/assets/c%d.sql", i),
			Content:   "select * from " + previous,
			Upstreams: []string{previous},
		}}, assets...)
		previous = name
	}
	state := model.WorkspaceState{
		Pipelines: []model.Pipeline{{ID: "pipeline", Name: "analytics", Assets: assets}},
	}
	service := notebookLSPService(t, state)

	response, apiErr := service.Completions(context.Background(), SQLLSPRequest{
		AssetID:  "c6",
		Content:  "select t.\nfrom analytics.c6 t",
		Position: sqllsp.Position{Line: 0, Character: len("select t.")},
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	labels := map[string]bool{}
	for _, item := range response.Completions {
		labels[item.Label] = true
	}
	if !labels["x"] || !labels["y"] {
		t.Fatalf("expected columns to survive a 6-hop select * chain, got %#v", response.Completions)
	}
}

func TestSQLLSPServiceInfersColumnsThroughNotebookCellChains(t *testing.T) {
	state := notebookLSPState()
	state.Notebooks[0].Cells = append(state.Notebooks[0].Cells, model.Asset{
		ID:      "nb1-star",
		Name:    "star_stats",
		Type:    "duckdb.sql",
		Path:    "notebooks/revenue/cells/star_stats.sql",
		Content: "select * from base_stats",
		Class:   "notebook",
		CellID:  "uuid1:star_stats",
	})
	service := notebookLSPService(t, state)

	// star_stats copies base_stats via select *, and base_stats' columns are
	// themselves inferred — two inference hops inside one notebook.
	response, apiErr := service.Completions(context.Background(), SQLLSPRequest{
		AssetID:  "nb1-summary",
		Content:  "select s.\nfrom star_stats s",
		Position: sqllsp.Position{Line: 0, Character: len("select s.")},
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	labels := map[string]bool{}
	for _, item := range response.Completions {
		labels[item.Label] = true
	}
	if !labels["metric_day"] || !labels["metric_value"] {
		t.Fatalf("expected sibling cell columns through a select * chain, got %#v", response.Completions)
	}
}

func TestSQLLSPServiceFindsReferencesFromNotebookCells(t *testing.T) {
	service := notebookLSPService(t, notebookLSPState())

	content := "select o.order_id from analytics.orders o"
	response, apiErr := service.References(context.Background(), SQLLSPRequest{
		AssetID:            "nb1-summary",
		Content:            content,
		Position:           sqllsp.Position{Line: 0, Character: len("select o.order_id from analytics.or")},
		IncludeDeclaration: true,
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if len(response.Locations) == 0 {
		t.Fatalf("expected references to the pipeline asset from a notebook cell, got %#v", response)
	}
}

func hasDiagnosticCode(diagnostics []sqllsp.Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func variableTemplateSQLLSPService(t *testing.T) (*SQLLSPService, SQLLSPRequest) {
	t.Helper()
	const query = `select 42 as answer
where 42 >= {{ var.notable_magnitude }}
order by answer`
	parsed, root := writeTypeCheckWorkspace(t, `
name: earthquakes
variables:
  notable_magnitude:
    type: integer
    default: 5
`, map[string]string{
		"notable_events.sql": `
/* @bruin
name: earthquakes.notable_events
type: duckdb.sql
@bruin */
` + query,
	})
	if len(parsed.Assets) != 1 {
		t.Fatalf("expected one parsed asset, got %d", len(parsed.Assets))
	}
	asset := parsed.Assets[0]
	assetID := assetReportID(root, asset)
	state := model.WorkspaceState{
		Revision: 1,
		Pipelines: []model.Pipeline{{
			ID:   "earthquakes",
			Name: "earthquakes",
			Assets: []model.Asset{{
				ID:      assetID,
				Name:    asset.Name,
				Type:    string(asset.Type),
				Path:    asset.ExecutableFile.Path,
				Content: asset.ExecutableFile.Content,
			}},
		}},
	}
	return NewSQLLSPService(SQLLSPDependencies{
			WorkspaceRoot: root,
			CurrentState:  func() model.WorkspaceState { return state },
			ResolveAssetByID: func(_ context.Context, requested string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
				if requested != assetID {
					return "", nil, nil, fmt.Errorf("unexpected asset %q", requested)
				}
				return asset.ExecutableFile.Path, parsed, asset, nil
			},
		}),
		SQLLSPRequest{AssetID: assetID, Content: asset.ExecutableFile.Content}
}

func TestSQLLSPServiceProjectsPipelineVariablesForLiveDocuments(t *testing.T) {
	service, request := variableTemplateSQLLSPService(t)
	_, doc, apiErr := service.graphAndDocument(context.Background(), request)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if doc.Projection == nil {
		t.Fatal("expected live Jinja document to carry a rendered SQL projection")
	}
	if strings.Contains(doc.Projection.RenderedSQL, "{{") ||
		!strings.Contains(doc.Projection.RenderedSQL, "42 >= 5") {
		t.Fatalf("unexpected live Jinja projection: %q", doc.Projection.RenderedSQL)
	}
}

func TestSQLLSPServiceDoesNotSendRenderedVariablesToValidatorAsJinja(t *testing.T) {
	service, request := variableTemplateSQLLSPService(t)
	response, apiErr := service.Diagnostics(context.Background(), request)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	for _, diagnostic := range response.Diagnostics {
		if diagnostic.Source == "polyglot" {
			t.Fatalf("valid variable template produced a Polyglot diagnostic: %#v", diagnostic)
		}
	}
}

func TestSQLLSPServiceReportsNativeSyntaxDiagnostics(t *testing.T) {
	state := model.WorkspaceState{
		Pipelines: []model.Pipeline{{
			ID:   "pipeline",
			Name: "analytics",
			Assets: []model.Asset{{
				ID:      "report",
				Name:    "analytics.report",
				Type:    "duckdb.sql",
				Path:    "analytics/assets/report.sql",
				Content: "select 1 as order_id",
			}},
		}},
	}
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(),
		CurrentState:  func() model.WorkspaceState { return state },
	})

	invalid, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID: "report",
		Content: "select from from",
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	found := false
	for _, diagnostic := range invalid.Diagnostics {
		if diagnostic.Source == "polyglot" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a polyglot syntax diagnostic for invalid SQL, got %#v", invalid.Diagnostics)
	}

	valid, apiErr := service.Diagnostics(context.Background(), SQLLSPRequest{
		AssetID: "report",
		Content: "select 1 as order_id",
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	for _, diagnostic := range valid.Diagnostics {
		if diagnostic.Source == "polyglot" {
			t.Fatalf("expected no polyglot diagnostics for valid SQL, got %#v", diagnostic)
		}
	}
}
