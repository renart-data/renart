package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIAssetOpenAPIColumnsInferTypes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/openapi.yaml", r.URL.Path)
		_, _ = w.Write([]byte(`openapi: 3.0.3
paths:
  /player/{username}:
    get:
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
                properties:
                  data:
                    type: object
                    properties:
                      username:
                        type: string
                      rating:
                        type: integer
                      active:
                        type: boolean
`))
	}))
	defer server.Close()

	asset := &pipeline.Asset{
		Name: "quickstart.players",
		Type: pipeline.AssetType(apiAssetType),
		ExecutableFile: pipeline.ExecutableFile{Content: `type: api

parameters:
  openapi:
    url: ` + server.URL + `/openapi.yaml
  request:
    url: https://api.example.com/player/{{ username }}
    method: GET
  response:
    records_path: data
`},
	}

	columns := apiDefinitionColumns(context.Background(), asset, true)
	require.Len(t, columns, 3)
	byName := map[string]string{}
	for _, column := range columns {
		byName[column.Name] = column.Type
	}
	assert.Equal(t, "boolean", byName["active"])
	assert.Equal(t, "integer", byName["rating"])
	assert.Equal(t, "string", byName["username"])
}

// An API asset's `parameters:` is a nested request/response spec, which bruin's
// stock YAML reader (parameters = map[string]string) can't parse. The api-aware
// creator must still load the workbench-managed fields (columns, owner, …) from
// the file so edits like dropping a column round-trip instead of being masked by
// fresh inference in the workspace preview.
func TestAPIAwareCreatorLoadsFileColumnsDespiteNestedParameters(t *testing.T) {
	fs := afero.NewMemMapFs()
	path := "/ws/analytics/assets/weather.asset.yml"
	content := `type: api
uri: https://data.example.com/weather/alerts
parameters:
  request:
    url: https://api.weather.gov/alerts
    method: GET
  response:
    records_path: ".features"
owner: data@company.com
columns:
  - name: id
    type: string
  - name: geometry
    type: json
meta:
  renart_col_drop: geometry
depends:
  - asset: analytics.orders
    mode: symbolic
`
	require.NoError(t, afero.WriteFile(fs, path, []byte(content), 0o644))

	asset, err := apiAwareYamlTaskCreator(fs)(path)
	require.NoError(t, err)

	names := make([]string, 0, len(asset.Columns))
	for _, column := range asset.Columns {
		names = append(names, column.Name)
	}
	assert.ElementsMatch(t, []string{"id", "geometry"}, names, "file columns must load through the nested parameters spec")
	assert.Equal(t, "https://data.example.com/weather/alerts", asset.URI)
	assert.Equal(t, "data@company.com", asset.Owner)
	require.Len(t, asset.Upstreams, 1)
	assert.Equal(t, "analytics.orders", asset.Upstreams[0].Value)
	assert.Equal(t, pipeline.UpstreamModeSymbolic, asset.Upstreams[0].Mode)
}

func TestAPIAwareCreatorInfersAPIAssetWithoutExplicitType(t *testing.T) {
	fs := afero.NewMemMapFs()
	path := "/ws/example/assets/example/another_asset.asset.yml"
	content := `parameters:
  request:
    url: https://api.weather.gov/alerts
    method: GET
  response:
    records_path: ""
  pagination:
    type: next_url
    max_pages: 10
`
	require.NoError(t, afero.WriteFile(fs, path, []byte(content), 0o644))

	asset, err := apiAwareYamlTaskCreator(fs)(path)
	require.NoError(t, err)
	require.NotNil(t, asset)
	assert.Equal(t, pipeline.AssetType(apiAssetType), asset.Type)
	assert.Equal(t, content, asset.ExecutableFile.Content)
}

func TestUpdateAPIAssetColumnsPreservesNestedRequestSpec(t *testing.T) {
	workspaceRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceRoot, ".git"), 0o755))
	pipelineRoot := filepath.Join(workspaceRoot, "analytics")
	require.NoError(t, os.MkdirAll(filepath.Join(pipelineRoot, "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte("name: analytics\n"), 0o644))
	assetPath := filepath.Join(pipelineRoot, "assets/orders.asset.yml")
	require.NoError(t, os.WriteFile(assetPath, []byte(`name: analytics.orders
type: api
materialization:
  type: table
  strategy: merge
parameters:
  request:
    url: https://api.example.com/orders
    headers:
      X-Example: keep-me
  response:
    records_path: data
columns:
  - name: old
    type: string
`), 0o644))

	resolver := NewWorkspaceResolver(workspaceRoot, func(ctx context.Context, pipelinePath string) (*pipeline.Pipeline, error) {
		return NewRenartPipelineBuilder(afero.NewOsFs()).CreatePipelineFromPath(ctx, pipelinePath, pipeline.WithMutate())
	})
	service := NewAssetService(AssetDependencies{
		Fs: afero.NewOsFs(), WorkspaceRoot: workspaceRoot, ResolveAssetByID: resolver.ResolveAssetByID,
		SuppressWatcher: func(string) {}, PushWorkspaceUpdateImmediate: func(context.Context, string, string) {},
	})
	status, apiErr := service.UpdateAssetColumns(context.Background(), EncodeID("analytics/assets/orders.asset.yml"), []any{
		map[string]any{"name": "id", "type": "integer", "primary_key": true},
		map[string]any{"name": "updated_at", "type": "timestamp"},
	})
	require.Nil(t, apiErr)
	assert.Equal(t, "ok", status.Status)

	content, err := os.ReadFile(assetPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "url: https://api.example.com/orders")
	assert.Contains(t, string(content), "X-Example: keep-me")
	assert.Contains(t, string(content), "records_path: data")
	assert.Contains(t, string(content), "primary_key: true")
	assert.NotContains(t, string(content), "name: old")
}

func TestWorkspaceServiceLoadsAPIShapedAssetWithoutExplicitType(t *testing.T) {
	workspaceRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceRoot, ".git"), 0o755))
	pipelineRoot := filepath.Join(workspaceRoot, "example")
	assetsRoot := filepath.Join(pipelineRoot, "assets", "example")
	require.NoError(t, os.MkdirAll(assetsRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte("name: example\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(assetsRoot, "another_asset.asset.yml"), []byte(`parameters:
  request:
    url: https://api.weather.gov/alerts
    method: GET
  response:
    records_path: ""
  pagination:
    type: next_url
    max_pages: 10
`), 0o644))

	service := NewWorkspaceService(workspaceRoot, "")
	state, err := service.ComputeState(context.Background())
	require.NoError(t, err)
	assert.Empty(t, state.Errors)
	require.Len(t, state.Pipelines, 1)
	require.Len(t, state.Pipelines[0].Assets, 1)
	asset := state.Pipelines[0].Assets[0]
	assert.Equal(t, apiAssetType, asset.Type)
	assert.Equal(t, "example/assets/example/another_asset.asset.yml", asset.Path)
	assert.Empty(t, asset.ParseError)
}

// When an API asset carries no declared columns, the workspace preview falls back
// to spec inference — but a column the user explicitly dropped must not reappear.
func TestAPIInferredColumnsForDisplayRespectsDrops(t *testing.T) {
	asset := &pipeline.Asset{
		Type: pipeline.AssetType(apiAssetType),
		Meta: pipeline.EmptyStringMap{"renart_col_drop": "b"},
		ExecutableFile: pipeline.ExecutableFile{Content: `type: api
parameters:
  request:
    url: https://api.example.com/x
  response:
    fields:
      a: string
      b: string
      c: string
`},
	}

	names := make([]string, 0)
	for _, column := range newAssetDefinitionSchemaResolver(&pipeline.Pipeline{Assets: []*pipeline.Asset{asset}}).Generated(context.Background(), asset) {
		names = append(names, column.Name)
	}
	assert.ElementsMatch(t, []string{"a", "c"}, names, "dropped column must not reappear via inference fallback")
}

func TestInferAPIAssetSamplesResponseColumnsAndRecordPaths(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/search", r.URL.Path)
		_, _ = w.Write([]byte(`{"data":[{"id":1,"active":true,"score":10.0,"created_at":"2026-07-09T08:00:00Z"}]}`))
	}))
	defer server.Close()

	workspaceRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceRoot, ".git"), 0o755))
	pipelineRoot := filepath.Join(workspaceRoot, "analytics")
	assetsRoot := filepath.Join(pipelineRoot, "assets")
	require.NoError(t, os.MkdirAll(assetsRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte("name: analytics\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(assetsRoot, "people.asset.yml"), []byte(`name: analytics.people
type: api

parameters:
  request:
    url: `+server.URL+`/search
  response:
    records_path: data
`), 0o644))

	resolver := NewWorkspaceResolver(workspaceRoot, func(ctx context.Context, pipelinePath string) (*pipeline.Pipeline, error) {
		return NewRenartPipelineBuilder(afero.NewOsFs()).CreatePipelineFromPath(ctx, pipelinePath, pipeline.WithMutate())
	})
	service := NewAssetService(AssetDependencies{
		WorkspaceRoot:    workspaceRoot,
		ResolveAssetByID: resolver.ResolveAssetByID,
	})

	status, body, apiErr := service.InferAPIAsset(context.Background(), EncodeID("analytics/assets/people.asset.yml"))
	require.Nil(t, apiErr)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "data", body.RecordsPath)
	assert.Equal(t, 1, body.RecordsCount)

	paths := body.RecordsPaths
	assert.True(t, containsSampleRecordsPath(paths, "data"))

	columns := body.Columns
	byName := map[string]string{}
	for _, column := range columns {
		byName[column.Name] = column.Type
	}
	assert.Equal(t, "boolean", byName["active"])
	assert.Equal(t, "timestamp", byName["created_at"])
	assert.Equal(t, "integer", byName["id"])
	// A whole-valued float literal must stay float: `10.0` samples must not
	// narrow the column to integer.
	assert.Equal(t, "float", byName["score"])
}

func TestWriteAPIAssetJSONLValidatesResponseAgainstOpenAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi.yaml":
			_, _ = w.Write([]byte(`openapi: 3.0.3
paths:
  /player/{username}:
    get:
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
                properties:
                  data:
                    type: object
                    required: [username, rating]
                    properties:
                      username:
                        type: string
                      rating:
                        type: integer
`))
		case "/player/Hikaru":
			_, _ = w.Write([]byte(`{"data":{"username":"Hikaru","rating":"not-an-integer"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	spec := nativeAPISpec{
		OpenAPI:  nativeAPIOpenAPI{URL: server.URL + "/openapi.yaml", Validation: "error"},
		Request:  nativeAPIRequest{URL: server.URL + "/player/Hikaru", Method: http.MethodGet},
		Response: nativeAPIResponse{RecordsPath: "data"},
	}
	_, err := writeAPIAssetJSONL(context.Background(), jinja.NewRendererWithYesterday("quickstart", "test"), spec, filepath.Join(t.TempDir(), "players.jsonl"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api response does not match OpenAPI schema")
	assert.Contains(t, err.Error(), "$.data.rating expected integer")
}

func TestWriteAPIAssetJSONLWarnsByDefaultOnOpenAPIMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi.yaml":
			_, _ = w.Write([]byte(`openapi: 3.0.3
paths:
  /players:
    get:
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
                properties:
                  data:
                    type: array
                    items:
                      type: object
                      required: [rating]
                      properties:
                        rating:
                          type: integer
`))
		case "/players":
			_, _ = w.Write([]byte(`{"data":[{"rating":"not-an-integer"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx, warnings := withExecutionWarnings(context.Background())
	var output bytes.Buffer
	spec := nativeAPISpec{
		OpenAPI:  nativeAPIOpenAPI{URL: server.URL + "/openapi.yaml"},
		Request:  nativeAPIRequest{URL: server.URL + "/players"},
		Response: nativeAPIResponse{RecordsPath: "data"},
	}
	count, err := writeAPIAssetJSONL(ctx, jinja.NewRendererWithYesterday("quickstart", "test"), spec, filepath.Join(t.TempDir(), "players.jsonl"), &output)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	assert.Contains(t, output.String(), "WARNING:")
	assert.Len(t, warnings.snapshot(), 1)
	assert.Contains(t, warnings.snapshot()[0], "expected integer")
}

func TestWriteAPIAssetJSONLRejectsInvalidOpenAPIValidationMode(t *testing.T) {
	spec := nativeAPISpec{
		OpenAPI: nativeAPIOpenAPI{Validation: "sometimes"},
		Request: nativeAPIRequest{URL: "https://api.example.com/items"},
	}
	_, err := writeAPIAssetJSONL(context.Background(), jinja.NewRendererWithYesterday("quickstart", "test"), spec, filepath.Join(t.TempDir(), "items.jsonl"), nil)
	require.ErrorContains(t, err, "openapi.validation must be off, warn, or error")
}

func TestWriteAPIAssetJSONLRendersExecutionWindowParams(t *testing.T) {
	start := time.Date(2026, 7, 9, 8, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 9, 9, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "2026-07-09T08:00:00.000000Z", r.URL.Query().Get("updated_since"))
		assert.Equal(t, "2026-07-09T09:00:00.000000Z", r.URL.Query().Get("updated_before"))
		_, _ = w.Write([]byte(`{"data":[{"id":1}]}`))
	}))
	defer server.Close()

	renderer := jinja.NewRendererWithStartEndDates(&start, &end, &end, "quickstart", "test", nil)
	spec := nativeAPISpec{
		Request: nativeAPIRequest{
			URL: server.URL,
			Params: map[string]any{
				"updated_since":  "{{ start_timestamp }}",
				"updated_before": "{{ end_timestamp }}",
			},
		},
		Response: nativeAPIResponse{RecordsPath: "data"},
	}
	count, err := writeAPIAssetJSONL(context.Background(), renderer, spec, filepath.Join(t.TempDir(), "records.jsonl"), nil)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestWriteAPIAssetJSONLAllowsNullOptionalOpenAPIFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi.yaml":
			_, _ = w.Write([]byte(`openapi: 3.0.3
paths:
  /alerts:
    get:
      responses:
        "200":
          content:
            application/json:
              schema:
                type: object
                properties:
                  features:
                    type: array
                    items:
                      type: object
                      properties:
                        id:
                          type: string
                        properties:
                          type: object
                          properties:
                            description:
                              type: string
`))
		case "/alerts":
			_, _ = w.Write([]byte(`{"features":[{"id":"alert-1","properties":{"description":null}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	jsonlPath := filepath.Join(t.TempDir(), "alerts.jsonl")
	spec := nativeAPISpec{
		OpenAPI:  nativeAPIOpenAPI{URL: server.URL + "/openapi.yaml"},
		Request:  nativeAPIRequest{URL: server.URL + "/alerts", Method: http.MethodGet},
		Response: nativeAPIResponse{RecordsPath: "features"},
	}
	count, err := writeAPIAssetJSONL(context.Background(), jinja.NewRendererWithYesterday("quickstart", "test"), spec, jsonlPath, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	content, err := os.ReadFile(jsonlPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "properties")
	assert.Contains(t, string(content), `"description":null`)
}

func TestWriteAPIAssetJSONLPreservesTopLevelNullsForSling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"features":[{"id":"alert-1","geometry":null,"properties":{"severity":"Severe"}},{"id":"alert-2","properties":{"severity":"Moderate"}}]}`))
	}))
	defer server.Close()

	jsonlPath := filepath.Join(t.TempDir(), "alerts.jsonl")
	spec := nativeAPISpec{
		Request: nativeAPIRequest{URL: server.URL},
		Response: nativeAPIResponse{
			RecordsPath: "features",
			Fields: map[string]string{
				"geometry":   "geometry",
				"id":         "id",
				"properties": "properties",
			},
		},
	}
	count, err := writeAPIAssetJSONL(context.Background(), jinja.NewRendererWithYesterday("quickstart", "test"), spec, jsonlPath, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	file, err := os.Open(jsonlPath)
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	records := make([]map[string]any, 0, 2)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record map[string]any
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &record))
		records = append(records, record)
	}
	require.NoError(t, scanner.Err())
	require.Len(t, records, 2)
	geometry, exists := records[0]["geometry"]
	assert.True(t, exists)
	assert.Nil(t, geometry, "an explicit source null must remain JSON null")
	geometry, exists = records[1]["geometry"]
	assert.True(t, exists)
	assert.Nil(t, geometry, "a missing mapped column must remain JSON null")
	assert.Equal(t, map[string]any{"severity": "Severe"}, records[0]["properties"])
}

func TestWriteAPIAssetJSONLSupportsRequestBodyAndAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "true", r.URL.Query().Get("include"))

		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "Ada", body["name"])

		_, _ = w.Write([]byte(`{"data":[{"id":1,"name":"Ada"}]}`))
	}))
	defer server.Close()

	spec := nativeAPISpec{
		Request: nativeAPIRequest{
			URL:    server.URL + "/search",
			Method: http.MethodPost,
			Params: map[string]any{"include": true},
			Body: map[string]any{
				"name": "{{ item }}",
			},
		},
		Iterate: nativeAPIIterate{Over: []string{"Ada"}},
		Auth:    nativeAPIAuth{Type: "bearer", Token: "test-token"},
		Response: nativeAPIResponse{
			RecordsPath: "data",
			Fields:      map[string]string{"id": "id", "name": "name"},
		},
	}

	jsonlPath := filepath.Join(t.TempDir(), "people.jsonl")
	count, err := writeAPIAssetJSONL(context.Background(), jinja.NewRendererWithYesterday("quickstart", "test"), spec, jsonlPath, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	content, err := os.ReadFile(jsonlPath)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":1,"name":"Ada"}`, string(content))
}

// Run output is persisted in run history, so URLs printed there must never
// carry query-string credentials — neither the configured auth parameter nor
// well-known sensitive names placed directly in request.params.
func TestWriteAPIAssetJSONLRedactsQueryCredentialsInOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "secret-token", r.URL.Query().Get("api_token"), "the real request must carry the credential")
		assert.Equal(t, "param-secret", r.URL.Query().Get("access_token"))
		_, _ = w.Write([]byte(`{"data":[{"id":1}]}`))
	}))
	defer server.Close()

	spec := nativeAPISpec{
		Request: nativeAPIRequest{
			URL:    server.URL + "/deals",
			Params: map[string]any{"access_token": "param-secret", "cursor_hint": "keep-me"},
		},
		Auth:     nativeAPIAuth{Type: "api_key", Name: "api_token", Value: "secret-token", In: "query"},
		Response: nativeAPIResponse{RecordsPath: "data"},
	}

	var output bytes.Buffer
	count, err := writeAPIAssetJSONL(context.Background(), jinja.NewRendererWithYesterday("quickstart", "test"), spec, filepath.Join(t.TempDir(), "deals.jsonl"), &output)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	assert.Contains(t, output.String(), "api_token=REDACTED")
	assert.Contains(t, output.String(), "access_token=REDACTED")
	assert.Contains(t, output.String(), "cursor_hint=keep-me")
	assert.NotContains(t, output.String(), "secret-token")
	assert.NotContains(t, output.String(), "param-secret")
}

func TestRedactedURLStringMasksSignedURLCredentials(t *testing.T) {
	t.Parallel()

	raw := "https://user:password@example.test/object.csv?" +
		"X-Amz-Algorithm=AWS4-HMAC-SHA256&" +
		"X-Amz-Credential=aws-credential&" +
		"X-Amz-Signature=aws-signature&" +
		"X-Amz-Security-Token=aws-session&" +
		"GoogleAccessId=google-access&" +
		"X-Goog-Signature=google-signature&" +
		"cursor_hint=keep-me"
	parsed, err := url.Parse(raw)
	require.NoError(t, err)

	redacted := redactedURLString(parsed, "")
	assert.Contains(t, redacted, "X-Amz-Algorithm=AWS4-HMAC-SHA256")
	assert.Contains(t, redacted, "cursor_hint=keep-me")
	assert.NotContains(t, redacted, "user")
	assert.NotContains(t, redacted, "password")
	for _, secret := range []string{
		"aws-credential", "aws-signature", "aws-session", "google-access", "google-signature",
	} {
		assert.NotContains(t, redacted, secret)
	}
	assert.Equal(t, 6, strings.Count(redacted, "REDACTED"))
}

// Error messages surface in the UI and run history like output does; a failed
// request must not echo query credentials back through the error string.
func TestWriteAPIAssetJSONLRedactsQueryCredentialsInErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer server.Close()

	spec := nativeAPISpec{
		Request:  nativeAPIRequest{URL: server.URL + "/deals"},
		Auth:     nativeAPIAuth{Type: "api_key", Name: "api_token", Value: "secret-token", In: "query"},
		Response: nativeAPIResponse{RecordsPath: "data"},
	}

	_, err := writeAPIAssetJSONL(context.Background(), jinja.NewRendererWithYesterday("quickstart", "test"), spec, filepath.Join(t.TempDir(), "deals.jsonl"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api_token=REDACTED")
	assert.NotContains(t, err.Error(), "secret-token")
}

func TestWriteAPIAssetJSONLPaginatesByPageNumber(t *testing.T) {
	requestedPages := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		requestedPages = append(requestedPages, page)
		switch page {
		case "1":
			_, _ = w.Write([]byte(`{"data":[{"id":1}],"pagination":{"has_next_page":true}}`))
		case "2":
			_, _ = w.Write([]byte(`{"data":[{"id":2}],"pagination":{"has_next_page":false}}`))
		default:
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	spec := nativeAPISpec{
		Request:    nativeAPIRequest{URL: server.URL + "/items"},
		Response:   nativeAPIResponse{RecordsPath: "data", Fields: map[string]string{"id": "id"}},
		Pagination: nativeAPIPagination{Type: "page_number", PageParam: "page", StartPage: 1, HasMorePath: "pagination.has_next_page", MaxPages: 5},
	}

	jsonlPath := filepath.Join(t.TempDir(), "items.jsonl")
	count, err := writeAPIAssetJSONL(context.Background(), jinja.NewRendererWithYesterday("quickstart", "test"), spec, jsonlPath, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
	assert.Equal(t, []string{"1", "2"}, requestedPages)
	content, err := os.ReadFile(jsonlPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "1")
	assert.Contains(t, string(content), "2")
}

func TestWriteAPIAssetJSONLPaginatesByLinkHeader(t *testing.T) {
	serverURL := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/items" {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Query().Get("page") {
		case "":
			w.Header().Set("Link", `<`+serverURL+`/items?page=2>; rel="next"`)
			_, _ = w.Write([]byte(`{"items":[{"id":1}]}`))
		case "2":
			_, _ = w.Write([]byte(`{"items":[{"id":2}]}`))
		default:
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	}))
	serverURL = server.URL
	defer server.Close()

	spec := nativeAPISpec{
		Request:    nativeAPIRequest{URL: server.URL + "/items"},
		Response:   nativeAPIResponse{RecordsPath: "items", Fields: map[string]string{"id": "id"}},
		Pagination: nativeAPIPagination{Type: "next_url", NextURLHeader: "Link", MaxPages: 5},
	}

	jsonlPath := filepath.Join(t.TempDir(), "items.jsonl")
	count, err := writeAPIAssetJSONL(context.Background(), jinja.NewRendererWithYesterday("quickstart", "test"), spec, jsonlPath, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
	content, err := os.ReadFile(jsonlPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "1")
	assert.Contains(t, string(content), "2")
}

func TestHybridBruinExecutorRunsAPIAssetThroughLoadWithBruinTargetConnection(t *testing.T) {
	workspaceRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceRoot, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceRoot, "duckdb-files"), 0o755))
	fakeUv := filepath.Join(workspaceRoot, "fake-uv")
	require.NoError(t, os.WriteFile(fakeUv, []byte("#!/bin/sh\nprintf 'uv %s loaded_at=%s target=%s\\n' \"$*\" \"$SLING_LOADED_AT_COLUMN\" \"$RENART_SLING_TARGET\"\n"), 0o755))
	t.Setenv("RENART_UV_BINARY", fakeUv)
	t.Setenv("RENART_SLING_BINARY", "")
	t.Setenv("SLING_BINARY", "")
	t.Setenv("RENART_SLING_PACKAGE", "sling-test-package")

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		assert.Equal(t, "/player/Hikaru", r.URL.Path)
		assert.Equal(t, "2700", r.URL.Query().Get("min_rating"))
		_, _ = w.Write([]byte(`{"username":"Hikaru","name":"Hikaru Nakamura"}`))
	}))
	defer server.Close()

	pipelineRoot := filepath.Join(workspaceRoot, "quickstart")
	require.NoError(t, os.MkdirAll(filepath.Join(pipelineRoot, "assets/quickstart"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspaceRoot, ".bruin.yml"), []byte("environments:\n  default:\n    connections:\n      duckdb:\n        - name: duckdb-default\n          path: duckdb-files/chess.duckdb\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte(`name: quickstart
default_connections:
  duckdb: duckdb-default
variables:
  min_rating:
    type: integer
    default: 2700
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "assets/quickstart/players.asset.yml"), []byte(`type: api

parameters:
  request:
    url: `+server.URL+`/player/{{ username }}
    method: GET
    params:
      min_rating: "{{ var.min_rating }}"

  iterate:
    as: username
    over:
      - Hikaru

  response:
    fields:
      username: username
      name: name

custom_checks:
  - name: check wiring
    value: 0
    query: select 0
`), 0o644))

	executor := NewHybridBruinExecutor(
		workspaceRoot,
		"bruin",
		nil,
		func() *pipeline.Builder {
			return NewRenartPipelineBuilder(afero.NewOsFs())
		},
	)
	output, err := executor.RunAsset(context.Background(), RunAssetRequest{AssetPath: "quickstart/assets/quickstart/players.asset.yml", Environment: "default"}, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, requestCount, "scheduler check tasks must not rerun the API main request")
	assert.Contains(t, string(output), "Fetched 1 records from API asset quickstart.players")
	assert.Contains(t, string(output), "uv tool run --no-config --python 3.11 --from sling-test-package python -c")
	assert.Contains(t, string(output), "run --src-stream file://")
	assert.Contains(t, string(output), ".jsonl")
	assert.Contains(t, string(output), "--src-options "+apiJSONLSourceOptions)
	assert.Contains(t, string(output), "--tgt-conn RENART_SLING_TARGET")
	assert.Contains(t, string(output), "target=duckdb:///")
	assert.Contains(t, string(output), "/duckdb-files/chess.duckdb")
	assert.Contains(t, string(output), "--tgt-object quickstart.players")
	assert.Contains(t, string(output), "loaded_at=false")
	assert.NotContains(t, string(output), "--mode full-refresh")
}

func TestHybridBruinExecutorPassesDatabricksPayloadForAPIAsset(t *testing.T) {
	workspaceRoot := t.TempDir()
	fakeSling := filepath.Join(workspaceRoot, "fake-sling")
	require.NoError(t, os.WriteFile(fakeSling, []byte("#!/bin/sh\nprintf 'target=%s\\nargs=%s\\n' \"$RENART_SLING_TARGET\" \"$*\"\n"), 0o755))
	t.Setenv("RENART_SLING_BINARY", fakeSling)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"id":1}]}`))
	}))
	defer server.Close()

	content := `type: api
connection: databricks-default
parameters:
  request:
    url: ` + server.URL + `
  response:
    records_path: items
`
	asset := &pipeline.Asset{
		Name:       "quickstart.items",
		Type:       pipeline.AssetType(apiAssetType),
		Connection: "databricks-default",
		Materialization: pipeline.Materialization{
			Type:     pipeline.MaterializationTypeTable,
			Strategy: pipeline.MaterializationStrategyCreateReplace,
		},
		ExecutableFile: pipeline.ExecutableFile{
			Path:    filepath.Join(workspaceRoot, "quickstart", "assets", "items.asset.yml"),
			Content: content,
		},
	}
	pl := &pipeline.Pipeline{Name: "quickstart", Assets: []*pipeline.Asset{asset}}
	executor := NewHybridBruinExecutor(workspaceRoot, "bruin", nil, nil)

	output, err := executor.runAPIAsset(
		context.Background(),
		pl,
		asset,
		jinja.NewRendererWithYesterday("quickstart", "test"),
		databricksPATTestManager(),
		nil,
	)
	require.NoError(t, err)

	var payload, args string
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "target=") {
			payload = strings.TrimPrefix(line, "target=")
		}
		if strings.HasPrefix(line, "args=") {
			args = strings.TrimPrefix(line, "args=")
		}
	}
	require.NotEmpty(t, payload)
	require.NotEmpty(t, args)
	parsed := requireDatabricksSlingPayload(t, payload)
	assert.Equal(t, "/sql/1.0/warehouses/test-warehouse", parsed.Path)
	assert.Contains(t, args, `--tgt-options {"use_bulk":false}`)
	assert.NotContains(t, args, "test-token")
}

func TestSlingIntegrationMergesNullableAPIJSONColumn(t *testing.T) {
	if os.Getenv("RENART_RUN_SLING_INTEGRATION") != "1" {
		t.Skip("set RENART_RUN_SLING_INTEGRATION=1 to run the real Sling integration")
	}

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount++
		if requestCount == 1 {
			_, _ = w.Write([]byte(`{"features":[{"id":"alert-1","geometry":{"type":"Point","coordinates":[1,2]}},{"id":"alert-2","geometry":{"type":"Point","coordinates":[3,4]}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"features":[{"id":"alert-1","geometry":null},{"id":"alert-2","geometry":null}]}`))
	}))
	defer server.Close()

	workspaceRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceRoot, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceRoot, "duckdb-files"), 0o755))
	pipelineRoot := filepath.Join(workspaceRoot, "quickstart")
	require.NoError(t, os.MkdirAll(filepath.Join(pipelineRoot, "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspaceRoot, ".bruin.yml"), []byte("environments:\n  default:\n    connections:\n      duckdb:\n        - name: duckdb-default\n          path: duckdb-files/test.duckdb\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte("name: quickstart\ndefault_connections:\n  duckdb: duckdb-default\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "assets/alerts.asset.yml"), []byte(`name: quickstart.alerts
type: api
materialization:
  type: table
  strategy: merge
parameters:
  request:
    url: `+server.URL+`
  response:
    records_path: features
columns:
  - name: id
    type: string
    primary_key: true
  - name: geometry
    type: json
`), 0o644))

	executor := NewHybridBruinExecutor(
		workspaceRoot,
		"bruin",
		nil,
		func() *pipeline.Builder { return NewRenartPipelineBuilder(afero.NewOsFs()) },
	)
	request := RunAssetRequest{AssetPath: "quickstart/assets/alerts.asset.yml", Environment: "default"}
	fullRefreshRequest := request
	fullRefreshRequest.FullRefresh = true
	firstOutput, err := executor.RunAsset(context.Background(), fullRefreshRequest, nil)
	require.NoError(t, err, string(firstOutput))

	databasePath := filepath.Join(workspaceRoot, "duckdb-files/test.duckdb")
	queryTarget := func() string {
		var queryOutput bytes.Buffer
		commandName, commandArgs, commandErr := loadCommand(context.Background(), []string{
			"run",
			"--src-conn", "duckdb:///" + filepath.ToSlash(databasePath),
			"--src-stream", "select id, geometry from quickstart.alerts order by id",
			"--stdout",
		}, &queryOutput)
		require.NoError(t, commandErr)
		queryWriter := &streamCaptureWriter{buffer: &queryOutput}
		command := newStreamingCommand(context.Background(), commandName, commandArgs, workspaceRoot, queryWriter)
		require.NoError(t, runStreamingCommand(context.Background(), command, queryWriter), queryOutput.String())
		return queryOutput.String()
	}

	initialRows := queryTarget()
	assert.Contains(t, initialRows, `""type"":""Point""`, "nested geometry must remain a JSON field")

	secondOutput, err := executor.RunAsset(context.Background(), request, nil)
	require.NoError(t, err, string(secondOutput))
	assert.Equal(t, 2, requestCount)
	mergedRows := queryTarget()
	assert.Contains(t, mergedRows, "alert-1")
	assert.Contains(t, mergedRows, "alert-2")
}

func containsSampleRecordsPath(paths []APIRecordsPathSample, want string) bool {
	for _, path := range paths {
		if path.Path == want {
			return true
		}
	}
	return false
}
