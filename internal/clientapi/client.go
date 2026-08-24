package clientapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"renart/internal/web/model"
	"renart/internal/web/service"
)

// Client talks to a running renart server's API for one workspace. APIBase
// carries the project addressing (mount or unprefixed), so every method just
// appends the path relative to /api.
type Client struct {
	// APIBase is the API prefix for this workspace, without trailing slash
	// (http://127.0.0.1:8080/api/projects/<id> or http://…/api).
	APIBase string
	// Token authenticates state-changing requests independent of the Origin
	// check; empty means rely on the no-Origin allowance.
	Token string
	// ServerVersion is filled by Health for the version-skew warning.
	ServerVersion string
	// WorkspaceRoot is the root the server reports for this API base.
	WorkspaceRoot string

	httpClient *http.Client
}

// HealthResponse mirrors GET /api/health.
type HealthResponse struct {
	Status        string `json:"status"`
	Version       string `json:"version"`
	WorkspaceRoot string `json:"workspace_root"`
	ProjectID     string `json:"project_id"`
}

// StreamDone is the terminal event of a materialize stream.
type StreamDone struct {
	Status   string `json:"status"`
	Error    string `json:"error"`
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
}

// FromEnv builds a client from RENART_SERVER / RENART_TOKEN — the override
// used by anything that wants to pin the CLI to a specific server. Returns
// nil when the variable is unset.
func FromEnv() *Client {
	base := strings.TrimSpace(os.Getenv("RENART_SERVER"))
	if base == "" {
		return nil
	}
	base = strings.TrimRight(base, "/")
	if !strings.Contains(base, "/api") {
		base += "/api"
	}
	return &Client{
		APIBase:    base,
		Token:      strings.TrimSpace(os.Getenv("RENART_TOKEN")),
		httpClient: &http.Client{},
	}
}

// Discover returns a client when a live server has workspaceRoot open:
// it reads .renart/server.json and verifies it with a fast health check
// (never blocking long — a dead server must fall back to embedded mode
// quickly). Returns nil when there is no usable server; the error is only
// diagnostic (stale file, mismatched root) and safe to ignore.
func Discover(ctx context.Context, workspaceRoot string) (*Client, error) {
	file, err := ReadServerFile(workspaceRoot)
	if file == nil || err != nil {
		return nil, err
	}

	client := &Client{
		APIBase:    strings.TrimRight(file.APIBaseURL, "/"),
		Token:      file.Token,
		httpClient: &http.Client{},
	}
	if client.APIBase == "" {
		return nil, fmt.Errorf("stale server.json: no api_base_url")
	}

	healthCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	health, err := client.Health(healthCtx)
	if err != nil {
		return nil, fmt.Errorf("server from server.json is not responding (stale file?): %w", err)
	}
	if !sameDir(health.WorkspaceRoot, workspaceRoot) {
		return nil, fmt.Errorf("server at %s serves %s, not %s", client.APIBase, health.WorkspaceRoot, workspaceRoot)
	}
	return client, nil
}

// sameDir compares two directory paths with symlinks resolved.
func sameDir(a, b string) bool {
	resolve := func(p string) string {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			return resolved
		}
		return filepath.Clean(p)
	}
	return resolve(a) == resolve(b)
}

// Health fetches /api/health and records the server version and root.
func (c *Client) Health(ctx context.Context) (HealthResponse, error) {
	var health HealthResponse
	if err := c.getJSON(ctx, "/health", &health); err != nil {
		return HealthResponse{}, err
	}
	c.ServerVersion = health.Version
	c.WorkspaceRoot = health.WorkspaceRoot
	return health, nil
}

// Workspace fetches the parsed workspace state for target resolution.
func (c *Client) Workspace(ctx context.Context) (model.WorkspaceState, error) {
	var state model.WorkspaceState
	err := c.getJSON(ctx, "/workspace", &state)
	return state, err
}

func (c *Client) WorkspaceConfig(ctx context.Context) (service.WorkspaceConfigResponse, error) {
	var result service.WorkspaceConfigResponse
	err := c.getJSON(ctx, "/config", &result)
	return result, err
}

func (c *Client) InitializeLocalVault(
	ctx context.Context,
	passphrase string,
) (service.WorkspaceConfigResponse, error) {
	return c.updateLocalVault(ctx, "/config/secrets/vault/initialize", passphrase)
}

func (c *Client) UnlockLocalVault(
	ctx context.Context,
	passphrase string,
) (service.WorkspaceConfigResponse, error) {
	return c.updateLocalVault(ctx, "/config/secrets/vault/unlock", passphrase)
}

func (c *Client) LockLocalVault(ctx context.Context) (service.WorkspaceConfigResponse, error) {
	return c.updateLocalVault(ctx, "/config/secrets/vault/lock", "")
}

func (c *Client) ChangeLocalVaultPassphrase(
	ctx context.Context,
	passphrase string,
) (service.WorkspaceConfigResponse, error) {
	return c.updateLocalVault(ctx, "/config/secrets/vault/change-passphrase", passphrase)
}

func (c *Client) updateLocalVault(
	ctx context.Context,
	path string,
	passphrase string,
) (service.WorkspaceConfigResponse, error) {
	var result service.WorkspaceConfigResponse
	err := c.postJSON(ctx, path, map[string]string{"passphrase": passphrase}, &result)
	return result, err
}

// RenderAsset previews the saved asset through the same read-only service used
// by the Build editor. The server owns the decoded path and preview run ID.
func (c *Client) RenderAsset(ctx context.Context, assetID string, request service.AssetRenderRequest) (service.AssetRenderResult, error) {
	var result service.AssetRenderResult
	err := c.postJSON(ctx, "/assets/"+url.PathEscape(assetID)+"/render", request, &result)
	return result, err
}

// RenderPipelineAsset previews an asset by stable pipeline identity and asset
// name, allowing the server to resolve either the saved working tree or one
// exact deployment without accepting a client-owned filesystem root.
func (c *Client) RenderPipelineAsset(ctx context.Context, pipelineID string, request service.PipelineAssetRenderRequest) (service.AssetRenderResult, error) {
	var result service.AssetRenderResult
	err := c.postJSON(ctx, "/pipelines/"+url.PathEscape(pipelineID)+"/assets/render", request, &result)
	return result, err
}

// PlanPipeline resolves a read-only execution plan through the same service
// used by the Build review surface.
func (c *Client) PlanPipeline(ctx context.Context, pipelineID string, request service.PipelinePlanRequest) (service.PipelinePlan, error) {
	var result service.PipelinePlan
	err := c.postJSON(ctx, "/pipelines/"+url.PathEscape(pipelineID)+"/plan", request, &result)
	return result, err
}

// MaterializePipelineStream runs a whole pipeline through the server,
// forwarding output chunks as they stream.
func (c *Client) MaterializePipelineStream(ctx context.Context, pipelineID string, query url.Values, onChunk func(string)) (StreamDone, error) {
	return c.stream(ctx, "/pipelines/"+url.PathEscape(pipelineID)+"/materialize/stream", query, onChunk)
}

// MaterializeAssetStream runs one asset (or its scope cone) through the
// server, forwarding output chunks as they stream.
func (c *Client) MaterializeAssetStream(ctx context.Context, assetID string, query url.Values, onChunk func(string)) (StreamDone, error) {
	return c.stream(ctx, "/assets/"+url.PathEscape(assetID)+"/materialize/stream", query, onChunk)
}

// BuildStalePipelineStream rebuilds the stale plan selected by the server.
// The optional upstream_of query narrows the plan to one asset's transitive
// upstreams for `renart run --refresh-upstreams`.
func (c *Client) BuildStalePipelineStream(ctx context.Context, pipelineID string, query url.Values, onChunk func(string)) (StreamDone, error) {
	return c.stream(ctx, "/pipelines/"+url.PathEscape(pipelineID)+"/build-stale/stream", query, onChunk)
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.APIBase+path, nil)
	if err != nil {
		return err
	}
	c.authorize(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("GET %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) postJSON(ctx context.Context, path string, input, out any) error {
	return c.postJSONHeaders(ctx, path, input, out, nil)
}

func (c *Client) postJSONHeaders(
	ctx context.Context,
	path string,
	input,
	out any,
	headers map[string]string,
) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.APIBase+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.authorize(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var envelope struct {
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &envelope) == nil && envelope.Error != nil && strings.TrimSpace(envelope.Error.Message) != "" {
			if strings.TrimSpace(envelope.Error.Code) != "" {
				return fmt.Errorf("POST %s: %s: %s: %s", path, resp.Status, envelope.Error.Code, envelope.Error.Message)
			}
			return fmt.Errorf("POST %s: %s: %s", path, resp.Status, envelope.Error.Message)
		}
		return fmt.Errorf("POST %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) stream(ctx context.Context, path string, query url.Values, onChunk func(string)) (StreamDone, error) {
	target := c.APIBase + path
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, nil)
	if err != nil {
		return StreamDone{}, err
	}
	c.authorize(req)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return StreamDone{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return StreamDone{}, fmt.Errorf("POST %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}

	var done *StreamDone
	err = parseSSE(resp.Body, func(event string, data []byte) {
		switch event {
		case "output":
			var payload struct {
				Chunk string `json:"chunk"`
			}
			if json.Unmarshal(data, &payload) == nil && payload.Chunk != "" && onChunk != nil {
				onChunk(payload.Chunk)
			}
		case "done":
			var payload StreamDone
			if json.Unmarshal(data, &payload) == nil {
				done = &payload
			}
		}
	})
	if err != nil {
		return StreamDone{}, err
	}
	if done == nil {
		return StreamDone{}, fmt.Errorf("stream ended without a done event (server stopped?)")
	}
	return *done, nil
}

func (c *Client) authorize(req *http.Request) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
}

// parseSSE reads a text/event-stream body and invokes handle once per event
// with its concatenated data payload.
func parseSSE(r io.Reader, handle func(event string, data []byte)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	event := ""
	var data []byte
	flush := func() {
		if len(data) > 0 || event != "" {
			handle(event, data)
		}
		event = ""
		data = nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			chunk := strings.TrimPrefix(line, "data:")
			chunk = strings.TrimPrefix(chunk, " ")
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, chunk...)
		}
	}
	flush()
	return scanner.Err()
}
