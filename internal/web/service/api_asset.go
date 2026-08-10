package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"
)

const apiAssetType = "api"
const maxAPIResponseBytes = 25 << 20
const apiJSONLSourceOptions = `{"format":"jsonlines","flatten":1}`

type nativeAPISpec struct {
	Name       string              `yaml:"name"`
	Type       string              `yaml:"type"`
	Connection string              `yaml:"connection"`
	Parameters *nativeAPISpec      `yaml:"parameters"`
	OpenAPI    nativeAPIOpenAPI    `yaml:"openapi"`
	OpenAPIURL string              `yaml:"openapi_url"`
	Request    nativeAPIRequest    `yaml:"request"`
	Iterate    nativeAPIIterate    `yaml:"iterate"`
	Auth       nativeAPIAuth       `yaml:"auth"`
	Pagination nativeAPIPagination `yaml:"pagination"`
	Response   nativeAPIResponse   `yaml:"response"`
	Load       nativeAPILoad       `yaml:"load"`
}

type nativeAPIOpenAPI struct {
	URL            string `yaml:"url"`
	Path           string `yaml:"path"`
	Method         string `yaml:"method"`
	OperationID    string `yaml:"operation_id"`
	ResponseStatus string `yaml:"response_status"`
	Validation     string `yaml:"validation"`
}

type nativeAPIRequest struct {
	URL     string            `yaml:"url"`
	Method  string            `yaml:"method"`
	Headers map[string]string `yaml:"headers"`
	Params  map[string]any    `yaml:"params"`
	Body    any               `yaml:"body"`
}

type nativeAPIIterate struct {
	Over []string `yaml:"over"`
	As   string   `yaml:"as"`
}

type nativeAPIAuth struct {
	Type     string `yaml:"type"`
	Token    string `yaml:"token"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Name     string `yaml:"name"`
	Value    string `yaml:"value"`
	In       string `yaml:"in"`
}

type nativeAPIPagination struct {
	Type          string `yaml:"type"`
	MaxPages      int    `yaml:"max_pages"`
	PageParam     string `yaml:"page_param"`
	StartPage     int    `yaml:"start_page"`
	OffsetParam   string `yaml:"offset_param"`
	StartOffset   int    `yaml:"start_offset"`
	LimitParam    string `yaml:"limit_param"`
	Limit         int    `yaml:"limit"`
	CursorParam   string `yaml:"cursor_param"`
	StartCursor   string `yaml:"start_cursor"`
	CursorPath    string `yaml:"cursor_path"`
	NextURLPath   string `yaml:"next_url_path"`
	NextURLHeader string `yaml:"next_url_header"`
	HasMorePath   string `yaml:"has_more_path"`
}

type nativeAPIResponse struct {
	RecordsPath string            `yaml:"records_path"`
	Fields      map[string]string `yaml:"fields"`
}

type nativeAPILoad struct {
	Destination string `yaml:"destination"`
	Target      string `yaml:"target"`
	Object      string `yaml:"object"`
	Mode        string `yaml:"mode"`
}

type nativeAPIAssetDefinition struct {
	Name       string            `yaml:"name"`
	URI        string            `yaml:"uri"`
	Type       string            `yaml:"type"`
	Connection string            `yaml:"connection"`
	Meta       map[string]string `yaml:"meta"`
	Parameters map[string]any    `yaml:"parameters"`
}

type APIRecordsPathSample struct {
	Path   string `json:"path"`
	Detail string `json:"detail,omitempty"`
}

type APIInferResult struct {
	Status       string                 `json:"status"`
	RequestURL   string                 `json:"request_url"`
	RecordsPath  string                 `json:"records_path"`
	RecordsCount int                    `json:"records_count"`
	RecordsPaths []APIRecordsPathSample `json:"records_paths"`
	Columns      []WorkspaceColumn      `json:"columns"`
	Warnings     []string               `json:"warnings"`
}

type apiColumnSample struct {
	typ      string
	nullable bool
}

func isAPIAssetType(assetType string) bool {
	return strings.EqualFold(strings.TrimSpace(assetType), apiAssetType)
}

func isAPIAssetDefinition(content []byte, definition nativeAPIAssetDefinition) bool {
	if isAPIAssetType(definition.Type) {
		return true
	}
	if strings.TrimSpace(definition.Type) != "" {
		return false
	}
	if hasAPIParameterShape(definition.Parameters) {
		return true
	}

	var spec nativeAPISpec
	if err := yaml.Unmarshal(content, &spec); err != nil {
		return false
	}
	return hasNativeAPISpecShape(spec)
}

func hasAPIParameterShape(parameters map[string]any) bool {
	if len(parameters) == 0 {
		return false
	}
	for _, key := range []string{"openapi", "openapi_url", "request", "response", "pagination", "auth", "iterate"} {
		if _, ok := parameters[key]; ok {
			return true
		}
	}
	return false
}

func hasNativeAPISpecShape(spec nativeAPISpec) bool {
	if spec.Parameters != nil && hasNativeAPISpecShape(*spec.Parameters) {
		return true
	}
	if strings.TrimSpace(spec.OpenAPI.URL) != "" || strings.TrimSpace(spec.OpenAPIURL) != "" || strings.TrimSpace(spec.OpenAPI.Path) != "" || strings.TrimSpace(spec.OpenAPI.OperationID) != "" {
		return true
	}
	if strings.TrimSpace(spec.Request.URL) != "" || strings.TrimSpace(spec.Request.Method) != "" || len(spec.Request.Headers) > 0 || len(spec.Request.Params) > 0 || spec.Request.Body != nil {
		return true
	}
	if strings.TrimSpace(spec.Response.RecordsPath) != "" || len(spec.Response.Fields) > 0 {
		return true
	}
	if strings.TrimSpace(spec.Pagination.Type) != "" || strings.TrimSpace(spec.Auth.Type) != "" || strings.TrimSpace(spec.Iterate.As) != "" || len(spec.Iterate.Over) > 0 {
		return true
	}
	return false
}

func isAPIAsset(asset *pipeline.Asset) bool {
	return asset != nil && isAPIAssetType(string(asset.Type))
}

func isAPIExecutablePath(path string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(path)), ".api.yml") ||
		strings.HasSuffix(strings.ToLower(strings.TrimSpace(path)), ".api.yaml")
}

func apiRunFilePathForDefinition(definitionPath, assetName string) string {
	dir := filepath.Dir(definitionPath)
	base := strings.TrimSuffix(filepath.Base(definitionPath), filepath.Ext(definitionPath))
	base = strings.TrimSuffix(base, ".asset")
	if strings.TrimSpace(base) == "" || base == "." {
		base = assetNameLeafPath(assetName)
	}
	return filepath.Join(dir, base+".api.yml")
}

func apiAwareYamlTaskCreator(fs afero.Fs) pipeline.TaskCreator {
	stock := pipeline.CreateTaskFromYamlDefinition(fs)
	return func(filePath string) (*pipeline.Asset, error) {
		content, err := afero.ReadFile(fs, filePath)
		if err != nil {
			return nil, err
		}
		var definition nativeAPIAssetDefinition
		if err := yaml.Unmarshal(content, &definition); err != nil {
			return nil, err
		}
		if !isAPIAssetDefinition(content, definition) {
			return stock(filePath)
		}

		absPath, err := filepath.Abs(filePath)
		if err != nil {
			absPath = filePath
		}
		asset := &pipeline.Asset{
			Name:       strings.TrimSpace(definition.Name),
			URI:        strings.TrimSpace(definition.URI),
			Type:       pipeline.AssetType(apiAssetType),
			Connection: strings.TrimSpace(definition.Connection),
			Meta:       definition.Meta,
			ExecutableFile: pipeline.ExecutableFile{
				Name:    filepath.Base(absPath),
				Path:    absPath,
				Content: string(content),
			},
		}
		// The narrow API definition struct intentionally omits the fields renart's
		// asset workbench edits (typed dependencies, columns, owner, tags,
		// description, materialization).
		// Bruin's stock reader parses them, but it models `parameters:` as flat
		// map[string]string and errors on an API asset's nested request/response
		// spec — which would silently drop these fields (so a dropped column, an
		// edited owner, etc. never round-trips). Parse them from a copy of the file
		// with `parameters` stripped so they load and survive a save.
		if stripped, stripErr := stripYAMLTopLevelKey(content, "parameters"); stripErr == nil {
			if metaAsset, metaErr := pipeline.ConvertYamlToTask(stripped); metaErr == nil && metaAsset != nil {
				asset.Upstreams = metaAsset.Upstreams
				asset.Columns = metaAsset.Columns
				asset.Owner = metaAsset.Owner
				asset.Tags = metaAsset.Tags
				asset.Description = metaAsset.Description
				asset.Materialization = metaAsset.Materialization
				asset.CustomChecks = metaAsset.CustomChecks
			}
		}
		return asset, nil
	}
}

func defaultAPIAssetContent(assetName string) string {
	return `type: api

materialization:
  type: table
  strategy: create+replace

parameters:
  openapi:
    url: https://api.weather.gov/openapi.json
    validation: warn

  request:
    url: https://api.weather.gov/alerts/active?area=CA
    method: GET
    headers:
      Accept: application/geo+json
      User-Agent: Renart/alpha (https://getrenart.com)

  response:
    records_path: features
`
}

func apiSummaryParameters(content string, asset *pipeline.Asset, pl *pipeline.Pipeline) map[string]string {
	spec, connectionName, err := parseNativeAPIAssetSpec(content, asset, pl)
	if err != nil {
		return nil
	}
	params := map[string]string{}
	if strings.TrimSpace(spec.Request.URL) != "" {
		params["url"] = strings.TrimSpace(spec.Request.URL)
	}
	if strings.TrimSpace(connectionName) != "" {
		params["target"] = strings.TrimSpace(connectionName)
	} else if strings.TrimSpace(spec.Load.Target) != "" {
		params["target"] = strings.TrimSpace(spec.Load.Target)
	}
	if asset != nil && strings.TrimSpace(asset.Name) != "" {
		params["object"] = strings.TrimSpace(asset.Name)
	} else if strings.TrimSpace(spec.Load.Object) != "" {
		params["object"] = strings.TrimSpace(spec.Load.Object)
	}
	if len(params) == 0 {
		return nil
	}
	return params
}

func apiConnectionNameForAsset(asset *pipeline.Asset, pl *pipeline.Pipeline) (string, error) {
	if asset == nil {
		return "", errors.New("api asset is required")
	}
	content := asset.ExecutableFile.Content
	if strings.TrimSpace(content) == "" && strings.TrimSpace(asset.ExecutableFile.Path) != "" {
		bytes, err := os.ReadFile(asset.ExecutableFile.Path)
		if err != nil {
			return "", err
		}
		content = string(bytes)
	}
	_, connectionName, err := parseNativeAPIAssetSpec(content, asset, pl)
	return connectionName, err
}

func parseNativeAPIAssetSpec(content string, asset *pipeline.Asset, pl *pipeline.Pipeline) (nativeAPISpec, string, error) {
	var raw nativeAPISpec
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		return nativeAPISpec{}, "", err
	}
	spec := raw
	if raw.Parameters != nil {
		spec = *raw.Parameters
		if strings.TrimSpace(spec.OpenAPI.URL) == "" && strings.TrimSpace(spec.OpenAPIURL) == "" {
			spec.OpenAPI = raw.OpenAPI
			spec.OpenAPIURL = raw.OpenAPIURL
		}
	}

	connectionName := strings.TrimSpace(raw.Connection)
	if connectionName == "" && asset != nil {
		connectionName = strings.TrimSpace(asset.Connection)
	}
	if connectionName == "" {
		connectionName = strings.TrimSpace(spec.Load.Target)
	}
	if connectionName == "" && pl != nil {
		destination := strings.TrimSpace(spec.Load.Destination)
		if destination != "" {
			if conn := strings.TrimSpace(pl.DefaultConnections[destination]); conn != "" {
				connectionName = conn
			}
		}
		if connectionName == "" {
			if conn, connErr := defaultPipelineTargetConnection(pl); connErr == nil {
				connectionName = conn
			}
		}
	}
	return spec, connectionName, nil
}

func apiTargetObjectName(asset *pipeline.Asset, spec nativeAPISpec) string {
	if asset != nil && strings.TrimSpace(asset.Name) != "" {
		return strings.TrimSpace(asset.Name)
	}
	return strings.TrimSpace(spec.Load.Object)
}

// apiDefinitionColumns derives the declarative API output contract. Authoring
// callers pass allowNetwork=false: response.fields remains usable without I/O,
// while an external OpenAPI document is only fetched by an explicit schema
// observation that opted into network access.
func apiDefinitionColumns(ctx context.Context, asset *pipeline.Asset, allowNetwork bool) []pipeline.Column {
	if asset == nil {
		return nil
	}
	content := asset.ExecutableFile.Content
	if strings.TrimSpace(content) == "" {
		return nil
	}
	spec, _, err := parseNativeAPIAssetSpec(content, asset, nil)
	if err != nil {
		return nil
	}
	if allowNetwork {
		if columns, openAPIErr := apiOpenAPIColumns(ctx, spec); openAPIErr == nil && len(columns) > 0 {
			return columns
		}
	}
	if len(spec.Response.Fields) == 0 {
		return nil
	}
	fieldNames := sortedFieldNames(spec.Response.Fields)
	columns := make([]pipeline.Column, 0, len(fieldNames))
	for _, fieldName := range fieldNames {
		columns = append(columns, pipeline.Column{Name: fieldName})
	}
	return columns
}

func apiDefinitionNeedsNetwork(asset *pipeline.Asset) bool {
	if asset == nil || strings.TrimSpace(asset.ExecutableFile.Content) == "" {
		return false
	}
	spec, _, err := parseNativeAPIAssetSpec(asset.ExecutableFile.Content, asset, nil)
	if err != nil {
		return false
	}
	return apiOpenAPIURL(spec) != "" && len(spec.Response.Fields) == 0
}

func (s *AssetService) InferAPIAsset(ctx context.Context, assetID string) (int, APIInferResult, *APIError) {
	_, parsedPipeline, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return 0, APIInferResult{}, badRequestError("asset_resolve_failed", err.Error())
	}
	if !isAPIAsset(asset) {
		return 0, APIInferResult{}, badRequestError("unsupported_asset_type", "api-infer is supported for api assets only")
	}

	content := asset.ExecutableFile.Content
	if strings.TrimSpace(content) == "" && strings.TrimSpace(asset.ExecutableFile.Path) != "" {
		bytes, readErr := os.ReadFile(asset.ExecutableFile.Path)
		if readErr != nil {
			return 0, APIInferResult{}, badRequestError("asset_read_failed", readErr.Error())
		}
		content = string(bytes)
	}
	spec, _, err := parseNativeAPIAssetSpec(content, asset, parsedPipeline)
	if err != nil {
		return 0, APIInferResult{}, badRequestError("api_spec_parse_failed", err.Error())
	}
	if strings.TrimSpace(spec.Request.URL) == "" {
		return 0, APIInferResult{}, badRequestError("api_request_url_required", "api asset request.url is required")
	}

	pipelineName := ""
	if parsedPipeline != nil {
		pipelineName = parsedPipeline.Name
	}
	renderer := jinja.NewRendererWithYesterday(pipelineName, "web-api-infer")
	if parsedPipeline != nil {
		if assetRenderer, cloneErr := renderer.CloneForAsset(ctx, parsedPipeline, asset); cloneErr == nil {
			if concreteRenderer, ok := assetRenderer.(*jinja.Renderer); ok {
				renderer = concreteRenderer
			}
		}
	}
	decoded, requestURL, err := sampleAPIAssetResponse(ctx, renderer, spec)
	if err != nil {
		return 0, APIInferResult{}, badRequestError("api_sample_failed", err.Error())
	}
	records := recordsAtPath(decoded, spec.Response.RecordsPath)
	columns := workspaceColumnsFromSampleRecords(records)
	warnings := make([]string, 0, 1)
	if path := strings.TrimSpace(spec.Response.RecordsPath); path != "" && valueAtPath(decoded, path) == nil {
		warnings = append(warnings, fmt.Sprintf("response.records_path %q did not resolve in the sampled response", path))
	}
	return http.StatusOK, APIInferResult{
		Status: "ok", RequestURL: requestURL, RecordsPath: spec.Response.RecordsPath,
		RecordsCount: len(records), RecordsPaths: sampleRecordsPathSuggestions(decoded),
		Columns: columns, Warnings: warnings,
	}, nil
}

func (e *HybridBruinExecutor) runAPIAsset(ctx context.Context, pl *pipeline.Pipeline, asset *pipeline.Asset, renderer *jinja.Renderer, manager config.ConnectionGetter, onChunk func([]byte)) ([]byte, error) {
	writer := &streamCaptureWriter{buffer: bytes.NewBuffer(nil), onChunk: onChunk}
	if asset == nil {
		return nil, errors.New("api asset is required")
	}
	if renderer == nil {
		return nil, errors.New("api asset renderer is required")
	}
	clonedRenderer, err := renderer.CloneForAsset(ctx, pl, asset)
	if err != nil {
		return nil, fmt.Errorf("build API asset renderer: %w", err)
	}
	concreteRenderer, ok := clonedRenderer.(*jinja.Renderer)
	if !ok {
		return nil, errors.New("API asset renderer has an unsupported implementation")
	}
	renderer = concreteRenderer

	specPath := asset.ExecutableFile.Path
	if strings.TrimSpace(specPath) == "" {
		specPath = asset.DefinitionFile.Path
	}
	if strings.TrimSpace(specPath) == "" {
		return nil, errors.New("api asset spec path is required")
	}

	content := asset.ExecutableFile.Content
	if strings.TrimSpace(content) == "" {
		bytes, err := os.ReadFile(specPath)
		if err != nil {
			return nil, err
		}
		content = string(bytes)
	}

	spec, connectionName, err := parseNativeAPIAssetSpec(content, asset, pl)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(spec.Request.URL) == "" {
		return nil, errors.New("api asset request.url is required")
	}
	if strings.TrimSpace(connectionName) == "" {
		return nil, errors.New("api asset connection is required when it cannot be inferred from pipeline default_connections")
	}
	targetObject := apiTargetObjectName(asset, spec)
	if targetObject == "" {
		return nil, errors.New("api asset target object could not be inferred from asset name")
	}
	targetConn, connectionWarning, err := loadConnectionURIWithWarning(manager, connectionName)
	if err != nil {
		return nil, err
	}
	writeSlingConnectionWarning(writer, connectionWarning)

	cmdDir := e.workspaceRoot
	if pipelineRoot, err := findPipelineRootForAsset(specPath); err == nil {
		cmdDir = pipelineRoot
	}
	tmpDir, err := os.MkdirTemp("", "renart-api-asset-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	jsonlPath := filepath.Join(tmpDir, assetPathSafeName(asset.Name)+".jsonl")
	count, err := writeAPIAssetJSONL(ctx, renderer, spec, jsonlPath, writer)
	if err != nil {
		return writer.buffer.Bytes(), err
	}
	_, _ = fmt.Fprintf(writer, "Fetched %d records from API asset %s.\n", count, asset.Name)

	args := []string{
		"run",
		"--src-stream",
		"file://" + filepath.ToSlash(jsonlPath),
		"--src-options",
		apiJSONLSourceOptions,
		"--tgt-conn",
		targetConn,
		"--tgt-object",
		targetObject,
	}
	materializationArgs, err := slingMaterializationArgs(ctx, asset)
	if err != nil {
		return writer.buffer.Bytes(), err
	}
	args = append(args, materializationArgs...)
	targetOptions, err := slingTargetOptionsArgs(manager, connectionName, nil)
	if err != nil {
		return writer.buffer.Bytes(), err
	}
	args = append(args, targetOptions...)
	args, connectionEnv := slingCommandConnectionEnv(args)
	cmdName, cmdArgs, err := loadCommand(ctx, args, writer)
	if err != nil {
		return writer.buffer.Bytes(), err
	}
	cmd := newStreamingCommand(ctx, cmdName, cmdArgs, cmdDir, writer)
	cmd.Env = append(cmd.Env, connectionEnv...)
	lease, err := e.acquireDuckDBConnections(ctx, manager, []string{connectionName}, directTaskLeaseOwner(ctx, pl, asset), writer)
	if err != nil {
		return writer.buffer.Bytes(), err
	}
	defer lease.Release()
	if err := runStreamingCommand(ctx, cmd, writer); err != nil {
		return writer.buffer.Bytes(), err
	}
	return writer.buffer.Bytes(), nil
}

func writeAPIAssetJSONL(ctx context.Context, renderer *jinja.Renderer, spec nativeAPISpec, path string, output io.Writer) (int, error) {
	file, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = file.Close() }()
	encoder := json.NewEncoder(file)

	items := spec.Iterate.Over
	if len(items) == 0 {
		items = []string{""}
	}
	itemName := strings.TrimSpace(spec.Iterate.As)
	if itemName == "" {
		itemName = "item"
	}

	client := &http.Client{Timeout: 30 * time.Second}
	if start, ok := ctx.Value(pipeline.RunConfigStartDate).(time.Time); ok {
		renderer.SetContextValue("start_year", start.Format("2006"))
		renderer.SetContextValue("start_month", start.Format("01"))
	}
	validationMode, err := apiOpenAPIValidationMode(spec)
	if err != nil {
		return 0, err
	}
	var openAPIValidator *apiOpenAPIValidator
	if validationMode != "off" {
		openAPIValidator, err = newAPIOpenAPIValidator(ctx, spec)
		if err != nil {
			if validationMode == "error" {
				return 0, err
			}
			warning := "OpenAPI validation skipped: " + err.Error()
			addExecutionWarning(ctx, warning)
			if output != nil {
				_, _ = fmt.Fprintf(output, "WARNING: %s\n", warning)
			}
		}
	}
	count := 0
	for _, item := range items {
		renderer.SetContextValue(itemName, item)
		renderer.SetContextValue("item", item)
		baseURL, err := renderer.Render(spec.Request.URL)
		if err != nil {
			return count, err
		}
		method := strings.TrimSpace(spec.Request.Method)
		if method == "" {
			method = http.MethodGet
		}
		baseParams, err := renderedRequestParams(renderer, spec.Request.Params)
		if err != nil {
			return count, err
		}
		pageState := newAPIPaginationState(spec.Pagination)
		nextURL := strings.TrimSpace(baseURL)
		for {
			requestURL, pageParams, err := pageState.request(nextURL, baseParams)
			if err != nil {
				return count, err
			}
			req, displayURL, err := newAPIHTTPRequest(ctx, renderer, spec, method, requestURL, pageParams)
			if err != nil {
				return count, err
			}

			resp, body, err := doAPIRequest(ctx, client, req, displayURL)
			if err != nil {
				return count, err
			}
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				return count, fmt.Errorf("api request to %s failed with status %d: %s", displayURL, resp.StatusCode, strings.TrimSpace(string(body)))
			}
			if output != nil {
				_, _ = fmt.Fprintf(output, "Fetched %s\n", displayURL)
			}

			var decoded any
			if err := json.Unmarshal(body, &decoded); err != nil {
				return count, err
			}
			if openAPIValidator != nil {
				if err := openAPIValidator.Validate(decoded, displayURL); err != nil {
					if validationMode == "error" {
						return count, err
					}
					warning := err.Error()
					addExecutionWarning(ctx, warning)
					if output != nil {
						_, _ = fmt.Fprintf(output, "WARNING: %s\n", warning)
					}
				}
			}
			records := recordsAtPath(decoded, spec.Response.RecordsPath)
			for _, record := range records {
				var out map[string]any
				if len(spec.Response.Fields) > 0 {
					mapped := make(map[string]any, len(spec.Response.Fields))
					for name, fieldPath := range spec.Response.Fields {
						mapped[name] = valueAtPath(record, fieldPath)
					}
					out = mapped
				} else if object, ok := record.(map[string]any); ok {
					out = object
				} else {
					out = map[string]any{"value": record}
				}
				if err := encoder.Encode(out); err != nil {
					return count, err
				}
				count++
			}

			next, ok, err := pageState.next(resp, decoded, req.URL.String(), len(records))
			if err != nil {
				return count, err
			}
			if !ok {
				break
			}
			nextURL = next
		}
	}
	return count, nil
}

func sampleAPIAssetResponse(ctx context.Context, renderer *jinja.Renderer, spec nativeAPISpec) (any, string, error) {
	itemName := strings.TrimSpace(spec.Iterate.As)
	if itemName == "" {
		itemName = "item"
	}
	item := ""
	if len(spec.Iterate.Over) > 0 {
		item = spec.Iterate.Over[0]
	}
	renderer.SetContextValue(itemName, item)
	renderer.SetContextValue("item", item)

	requestURL, err := renderer.Render(spec.Request.URL)
	if err != nil {
		return nil, "", err
	}
	params, err := renderedRequestParams(renderer, spec.Request.Params)
	if err != nil {
		return nil, "", err
	}
	pageState := newAPIPaginationState(spec.Pagination)
	requestURL, params, err = pageState.request(requestURL, params)
	if err != nil {
		return nil, "", err
	}
	method := strings.TrimSpace(spec.Request.Method)
	if method == "" {
		method = http.MethodGet
	}
	req, displayURL, err := newAPIHTTPRequest(ctx, renderer, spec, method, requestURL, params)
	if err != nil {
		return nil, "", err
	}
	resp, body, err := doAPIRequest(ctx, &http.Client{Timeout: 30 * time.Second}, req, displayURL)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("api request to %s failed with status %d: %s", displayURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	// UseNumber keeps the source JSON literal, so column inference can tell
	// `10` (integer) from `10.0` (float) — float64 collapses both to a whole
	// number and would infer integer for a float column whose sampled values
	// happen to be whole.
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, "", err
	}
	return decoded, displayURL, nil
}

func doAPIRequest(ctx context.Context, client *http.Client, req *http.Request, displayURL string) (*http.Response, []byte, error) {
	const attempts = 3
	for attempt := 1; attempt <= attempts; attempt++ {
		current := req
		if attempt > 1 {
			current = req.Clone(ctx)
			// Clone copies the already-consumed Body reader; rewind it so a
			// retried request does not go out with an empty body.
			if req.GetBody != nil {
				rewound, bodyErr := req.GetBody()
				if bodyErr != nil {
					return nil, nil, bodyErr
				}
				current.Body = rewound
			}
		}
		resp, err := client.Do(current)
		if err != nil {
			// url.Error's message embeds the full request URL, credentials
			// included; rewrap with the redacted display URL instead.
			var urlErr *neturl.Error
			if errors.As(err, &urlErr) {
				return nil, nil, fmt.Errorf("api request to %s failed: %w", displayURL, urlErr.Err)
			}
			return nil, nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseBytes+1))
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, nil, readErr
		}
		if len(body) > maxAPIResponseBytes {
			return nil, nil, fmt.Errorf("api response from %s exceeds the %d MiB per-page limit", displayURL, maxAPIResponseBytes>>20)
		}
		retryable := strings.EqualFold(req.Method, http.MethodGet) && (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500)
		if !retryable || attempt == attempts {
			return resp, body, nil
		}
		delay := apiRetryDelay(resp.Header.Get("Retry-After"), attempt)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, nil, errors.New("api request retries exhausted")
}

func apiRetryDelay(retryAfter string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && seconds >= 0 {
		delay := time.Duration(seconds) * time.Second
		if delay > 5*time.Second {
			return 5 * time.Second
		}
		return delay
	}
	if at, err := http.ParseTime(strings.TrimSpace(retryAfter)); err == nil {
		delay := time.Until(at)
		if delay < 0 {
			return 0
		}
		if delay > 5*time.Second {
			return 5 * time.Second
		}
		return delay
	}
	return time.Duration(attempt) * 100 * time.Millisecond
}

func sampleRecordsPathSuggestions(decoded any) []APIRecordsPathSample {
	suggestions := make([]APIRecordsPathSample, 0, 8)
	seen := map[string]bool{}
	add := func(path, detail string) {
		if seen[path] {
			return
		}
		seen[path] = true
		suggestions = append(suggestions, APIRecordsPathSample{Path: path, Detail: detail})
	}
	add("", sampleValueDetail(decoded))
	walkSampleRecordsPaths(decoded, nil, 0, add)
	return suggestions
}

func walkSampleRecordsPaths(value any, prefix []string, depth int, add func(path, detail string)) {
	if depth >= maxRecordsPathDepth {
		return
	}
	object, ok := value.(map[string]any)
	if !ok {
		return
	}
	names := make([]string, 0, len(object))
	for name := range object {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		child := object[name]
		path := append(append([]string{}, prefix...), name)
		joined := strings.Join(path, ".")
		switch typed := child.(type) {
		case []any:
			add(joined, fmt.Sprintf("array (%d items)", len(typed)))
			if len(typed) > 0 {
				walkSampleRecordsPaths(typed[0], path, depth+1, add)
			}
		case map[string]any:
			walkSampleRecordsPaths(typed, path, depth+1, add)
		}
	}
}

func sampleValueDetail(value any) string {
	switch typed := value.(type) {
	case []any:
		return fmt.Sprintf("response root (array, %d items)", len(typed))
	case map[string]any:
		return fmt.Sprintf("response root (object, %d fields)", len(typed))
	default:
		return fmt.Sprintf("response root (%T)", value)
	}
}

func workspaceColumnsFromSampleRecords(records []any) []WorkspaceColumn {
	if len(records) == 0 {
		return nil
	}
	samples := map[string]apiColumnSample{}
	for _, record := range records {
		object, ok := record.(map[string]any)
		if !ok {
			samples["value"] = mergeColumnSample(samples["value"], record)
			continue
		}
		for name, value := range object {
			samples[name] = mergeColumnSample(samples[name], value)
		}
	}
	names := make([]string, 0, len(samples))
	for name := range samples {
		names = append(names, name)
	}
	sort.Strings(names)
	columns := make([]WorkspaceColumn, 0, len(names))
	for _, name := range names {
		sample := samples[name]
		nullable := sample.nullable
		columns = append(columns, WorkspaceColumn{Name: name, Type: sample.typ, Nullable: &nullable})
	}
	return columns
}

func mergeColumnSample(sample apiColumnSample, value any) apiColumnSample {
	if value == nil {
		sample.nullable = true
		if sample.typ == "" {
			sample.typ = "json"
		}
		return sample
	}
	nextType := sampleColumnType(value)
	if sample.typ == "" || sample.typ == nextType {
		sample.typ = nextType
		return sample
	}
	if sample.typ == "integer" && nextType == "float" || sample.typ == "float" && nextType == "integer" {
		sample.typ = "float"
		return sample
	}
	sample.typ = "json"
	return sample
}

func sampleColumnType(value any) string {
	switch typed := value.(type) {
	case bool:
		return "boolean"
	case json.Number:
		if strings.ContainsAny(typed.String(), ".eE") {
			return "float"
		}
		return "integer"
	case float64:
		if math.Trunc(typed) == typed {
			return "integer"
		}
		return "float"
	case string:
		if _, err := time.Parse(time.RFC3339, typed); err == nil {
			return "timestamp"
		}
		if _, err := time.Parse("2006-01-02", typed); err == nil {
			return "date"
		}
		return "string"
	case []any, map[string]any:
		return "json"
	default:
		return "json"
	}
}

// sensitiveQueryParamNames are normalized (lower-case alphanumeric only) so
// provider-specific spellings such as X-Amz-Security-Token and
// X-Goog-Signature cannot bypass redaction with punctuation or casing.
var sensitiveQueryParamNames = map[string]bool{
	"accesstoken":    true,
	"apikey":         true,
	"apitoken":       true,
	"auth":           true,
	"authorization":  true,
	"clientsecret":   true,
	"googleaccessid": true,
	"key":            true,
	"keypairid":      true,
	"password":       true,
	"policy":         true,
	"secret":         true,
	"securitytoken":  true,
	"sig":            true,
	"signature":      true,
	"token":          true,
}

var sensitiveQueryParamSuffixes = []string{
	"accessid",
	"accesstoken",
	"apikey",
	"apitoken",
	"clientsecret",
	"credential",
	"credentials",
	"password",
	"secret",
	"securitytoken",
	"signature",
	"token",
}

func normalizedQueryParamName(name string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, name)
}

func isSensitiveQueryParamName(name, authQueryParam string) bool {
	normalized := normalizedQueryParamName(name)
	if normalized == "" {
		return false
	}
	if configured := normalizedQueryParamName(authQueryParam); configured != "" && normalized == configured {
		return true
	}
	if sensitiveQueryParamNames[normalized] {
		return true
	}
	for _, suffix := range sensitiveQueryParamSuffixes {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

// redactedURLString renders a URL for run output, error messages, and API
// responses: the configured query auth parameter and well-known sensitive
// parameter names keep their name but have their value replaced with
// REDACTED (chosen over *** because it survives query encoding unmangled).
// Run output is persisted in run history, so the real URL must never be
// printed once auth has been applied to it.
func redactedURLString(u *neturl.URL, authQueryParam string) string {
	if u == nil {
		return ""
	}
	clone := *u
	changed := false
	if clone.User != nil {
		clone.User = neturl.User("REDACTED")
		changed = true
	}
	query := u.Query()
	for name := range query {
		if isSensitiveQueryParamName(name, authQueryParam) {
			query.Set(name, "REDACTED")
			changed = true
		}
	}
	if !changed {
		return u.String()
	}
	clone.RawQuery = query.Encode()
	return clone.String()
}

// apiAuthQueryParamName returns the query parameter that applyAPIAuth will
// write the credential into, or "" when auth does not use the query string.
func apiAuthQueryParamName(auth nativeAPIAuth) string {
	switch strings.ToLower(strings.TrimSpace(auth.Type)) {
	case "api_key", "apikey":
	default:
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(auth.In)) {
	case "query", "param", "params":
		return strings.TrimSpace(auth.Name)
	default:
		return ""
	}
}

// newAPIHTTPRequest builds the outgoing request and a redacted display URL.
// Callers must print the display URL, never req.URL, because applyAPIAuth may
// have written the credential into the query string.
func newAPIHTTPRequest(ctx context.Context, renderer *jinja.Renderer, spec nativeAPISpec, method, requestURL string, params map[string]string) (*http.Request, string, error) {
	finalURL, err := urlWithQueryParams(requestURL, params)
	if err != nil {
		return nil, "", err
	}

	body, hasBody, err := renderedRequestBody(renderer, spec.Request.Body)
	if err != nil {
		return nil, "", err
	}
	var bodyReader io.Reader
	if hasBody {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, finalURL, bodyReader)
	if err != nil {
		return nil, "", err
	}

	for key, value := range spec.Request.Headers {
		renderedValue, renderErr := renderer.Render(value)
		if renderErr != nil {
			return nil, "", renderErr
		}
		req.Header.Set(key, renderedValue)
	}
	if hasBody && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if err := applyAPIAuth(renderer, req, spec.Auth); err != nil {
		return nil, "", err
	}
	return req, redactedURLString(req.URL, apiAuthQueryParamName(spec.Auth)), nil
}

func renderedRequestParams(renderer *jinja.Renderer, params map[string]any) (map[string]string, error) {
	if len(params) == 0 {
		return nil, nil
	}
	rendered := make(map[string]string, len(params))
	for key, value := range params {
		text, err := renderedScalar(renderer, value)
		if err != nil {
			return nil, err
		}
		rendered[key] = text
	}
	return rendered, nil
}

func renderedRequestBody(renderer *jinja.Renderer, body any) ([]byte, bool, error) {
	if body == nil {
		return nil, false, nil
	}
	rendered, err := renderTemplateValue(renderer, body)
	if err != nil {
		return nil, false, err
	}
	if text, ok := rendered.(string); ok {
		return []byte(text), true, nil
	}
	encoded, err := json.Marshal(rendered)
	if err != nil {
		return nil, false, err
	}
	return encoded, true, nil
}

func renderTemplateValue(renderer *jinja.Renderer, value any) (any, error) {
	switch typed := value.(type) {
	case string:
		return renderer.Render(typed)
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			rendered, err := renderTemplateValue(renderer, item)
			if err != nil {
				return nil, err
			}
			out = append(out, rendered)
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			rendered, err := renderTemplateValue(renderer, item)
			if err != nil {
				return nil, err
			}
			out[key] = rendered
		}
		return out, nil
	case map[any]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			rendered, err := renderTemplateValue(renderer, item)
			if err != nil {
				return nil, err
			}
			out[fmt.Sprint(key)] = rendered
		}
		return out, nil
	default:
		return typed, nil
	}
}

func renderedScalar(renderer *jinja.Renderer, value any) (string, error) {
	if value == nil {
		return "", nil
	}
	if text, ok := value.(string); ok {
		return renderer.Render(text)
	}
	return fmt.Sprint(value), nil
}

func applyAPIAuth(renderer *jinja.Renderer, req *http.Request, auth nativeAPIAuth) error {
	authType := strings.ToLower(strings.TrimSpace(auth.Type))
	if authType == "" || authType == "none" {
		return nil
	}
	switch authType {
	case "bearer":
		token, err := renderer.Render(auth.Token)
		if err != nil {
			return err
		}
		if strings.TrimSpace(token) != "" {
			req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
		}
	case "basic":
		username, err := renderer.Render(auth.Username)
		if err != nil {
			return err
		}
		password, err := renderer.Render(auth.Password)
		if err != nil {
			return err
		}
		req.SetBasicAuth(username, password)
	case "api_key", "apikey":
		name := strings.TrimSpace(auth.Name)
		if name == "" {
			return errors.New("api auth.name is required for api_key auth")
		}
		value, err := renderer.Render(auth.Value)
		if err != nil {
			return err
		}
		switch strings.ToLower(strings.TrimSpace(auth.In)) {
		case "query", "param", "params":
			query := req.URL.Query()
			query.Set(name, value)
			req.URL.RawQuery = query.Encode()
		case "", "header":
			req.Header.Set(name, value)
		default:
			return fmt.Errorf("unsupported api auth.in %q", auth.In)
		}
	default:
		return fmt.Errorf("unsupported api auth.type %q", auth.Type)
	}
	return nil
}

func urlWithQueryParams(rawURL string, params map[string]string) (string, error) {
	if len(params) == 0 {
		return rawURL, nil
	}
	parsed, err := neturl.Parse(rawURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	for key, value := range params {
		if strings.TrimSpace(key) == "" {
			continue
		}
		query.Set(key, value)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

type apiPaginationState struct {
	spec   nativeAPIPagination
	kind   string
	page   int
	offset int
	cursor string
	count  int
}

func newAPIPaginationState(spec nativeAPIPagination) *apiPaginationState {
	kind := strings.ToLower(strings.TrimSpace(spec.Type))
	state := &apiPaginationState{spec: spec, kind: kind, page: spec.StartPage, offset: spec.StartOffset, cursor: strings.TrimSpace(spec.StartCursor)}
	if state.page <= 0 {
		state.page = 1
	}
	return state
}

func (s *apiPaginationState) request(rawURL string, baseParams map[string]string) (string, map[string]string, error) {
	params := cloneStringMap(baseParams)
	switch s.kind {
	case "", "none":
		return rawURL, params, nil
	case "page", "page_number", "page-number":
		params = ensureStringMap(params)
		param := strings.TrimSpace(s.spec.PageParam)
		if param == "" {
			param = "page"
		}
		params[param] = fmt.Sprint(s.page)
		s.applyLimit(params)
	case "offset":
		params = ensureStringMap(params)
		param := strings.TrimSpace(s.spec.OffsetParam)
		if param == "" {
			param = "offset"
		}
		params[param] = fmt.Sprint(s.offset)
		s.applyLimit(params)
	case "cursor":
		params = ensureStringMap(params)
		param := strings.TrimSpace(s.spec.CursorParam)
		if param == "" {
			param = "cursor"
		}
		if s.cursor != "" {
			params[param] = s.cursor
		}
		s.applyLimit(params)
	case "next_url", "next-url":
		// The next URL is authoritative and usually already contains its query.
	default:
		return "", nil, fmt.Errorf("unsupported api pagination.type %q", s.spec.Type)
	}
	return rawURL, params, nil
}

func (s *apiPaginationState) next(resp *http.Response, decoded any, currentURL string, records int) (string, bool, error) {
	s.count++
	if s.kind == "" || s.kind == "none" {
		return "", false, nil
	}
	if s.maxPagesReached() {
		return "", false, nil
	}
	if s.spec.HasMorePath != "" {
		if hasMore, ok := valueAtPath(decoded, s.spec.HasMorePath).(bool); ok && !hasMore {
			return "", false, nil
		}
	}
	if records == 0 && (s.kind == "page" || s.kind == "page_number" || s.kind == "page-number" || s.kind == "offset") {
		return "", false, nil
	}

	switch s.kind {
	case "page", "page_number", "page-number":
		s.page++
		return currentURL, true, nil
	case "offset":
		s.offset += records
		if s.spec.Limit > 0 {
			s.offset = s.spec.StartOffset + s.count*s.spec.Limit
		}
		return currentURL, true, nil
	case "cursor":
		if strings.TrimSpace(s.spec.CursorPath) == "" {
			return "", false, nil
		}
		nextCursor := strings.TrimSpace(fmt.Sprint(valueAtPath(decoded, s.spec.CursorPath)))
		if nextCursor == "" || nextCursor == "<nil>" || nextCursor == s.cursor {
			return "", false, nil
		}
		s.cursor = nextCursor
		return currentURL, true, nil
	case "next_url", "next-url":
		next := strings.TrimSpace(nextURLFromResponse(resp, decoded, s.spec))
		if next == "" {
			return "", false, nil
		}
		return resolveNextAPIURL(currentURL, next), true, nil
	default:
		return "", false, fmt.Errorf("unsupported api pagination.type %q", s.spec.Type)
	}
}

func (s *apiPaginationState) applyLimit(params map[string]string) {
	if s.spec.Limit <= 0 {
		return
	}
	param := strings.TrimSpace(s.spec.LimitParam)
	if param == "" {
		param = "limit"
	}
	params[param] = fmt.Sprint(s.spec.Limit)
}

func (s *apiPaginationState) maxPagesReached() bool {
	maxPages := s.spec.MaxPages
	if maxPages <= 0 {
		maxPages = 100
	}
	return s.count >= maxPages
}

func nextURLFromResponse(resp *http.Response, decoded any, spec nativeAPIPagination) string {
	if path := strings.TrimSpace(spec.NextURLPath); path != "" {
		if value := valueAtPath(decoded, path); value != nil {
			return fmt.Sprint(value)
		}
	}
	headerName := strings.TrimSpace(spec.NextURLHeader)
	if headerName == "" {
		return ""
	}
	value := strings.TrimSpace(resp.Header.Get(headerName))
	if strings.EqualFold(headerName, "link") || strings.Contains(value, `rel="next"`) {
		return nextURLFromLinkHeader(value)
	}
	return value
}

func nextURLFromLinkHeader(value string) string {
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start >= 0 && end > start {
			return strings.TrimSpace(part[start+1 : end])
		}
	}
	return ""
}

func resolveNextAPIURL(currentURL, next string) string {
	parsedNext, err := neturl.Parse(next)
	if err != nil || parsedNext.IsAbs() {
		return next
	}
	parsedCurrent, err := neturl.Parse(currentURL)
	if err != nil {
		return next
	}
	return parsedCurrent.ResolveReference(parsedNext).String()
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func ensureStringMap(input map[string]string) map[string]string {
	if input != nil {
		return input
	}
	return map[string]string{}
}

func sortedFieldNames(fields map[string]string) []string {
	if len(fields) == 0 {
		return nil
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func recordsAtPath(input any, path string) []any {
	value := valueAtPath(input, path)
	if value == nil {
		return nil
	}
	if records, ok := value.([]any); ok {
		return records
	}
	return []any{value}
}

func valueAtPath(input any, path string) any {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return input
	}
	current := input
	for _, part := range strings.Split(trimmed, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[part]
	}
	return current
}

func assetPathSafeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "asset"
	}
	name = strings.ReplaceAll(name, ".", "_")
	name = strings.ReplaceAll(name, string(filepath.Separator), "_")
	return name
}
