package clientapi

import (
	"context"
	"net/url"

	"renart/internal/web/model"
	"renart/internal/web/service"
)

type notebookEnvelope struct {
	Status   string         `json:"status"`
	Notebook model.Notebook `json:"notebook"`
}

// Notebook loads one notebook through the owning Renart server.
func (c *Client) Notebook(ctx context.Context, notebookID string) (model.Notebook, error) {
	var envelope notebookEnvelope
	err := c.getJSON(ctx, "/notebooks/"+url.PathEscape(notebookID), &envelope)
	return envelope.Notebook, err
}

// NotebookRuntime loads restart-safe result summaries and staleness.
func (c *Client) NotebookRuntime(ctx context.Context, notebookID string) (service.NotebookRuntimeSnapshot, error) {
	var result service.NotebookRuntimeSnapshot
	err := c.getJSON(ctx, "/notebooks/"+url.PathEscape(notebookID)+"/runtime", &result)
	return result, err
}

// PrepareNotebookChangeSet validates and normalizes a semantic notebook edit.
func (c *Client) PrepareNotebookChangeSet(
	ctx context.Context,
	notebookID string,
	changeSet service.NotebookChangeSet,
) (service.NotebookChangePlan, error) {
	var result service.NotebookChangePlan
	err := c.postJSON(ctx, "/notebooks/"+url.PathEscape(notebookID)+"/changes/prepare", changeSet, &result)
	return result, err
}

// ApplyNotebookChangeSet commits an exact previously prepared semantic edit.
func (c *Client) ApplyNotebookChangeSet(
	ctx context.Context,
	notebookID string,
	changeSet service.NotebookChangeSet,
) (service.NotebookChangeApplyResult, error) {
	var result service.NotebookChangeApplyResult
	err := c.postJSON(ctx, "/notebooks/"+url.PathEscape(notebookID)+"/changes/apply", changeSet, &result)
	return result, err
}

// CheckNotebookVisualization runs the shared static/runtime presentation checker.
func (c *Client) CheckNotebookVisualization(
	ctx context.Context,
	notebookID string,
	request service.NotebookVisualizationCheckRequest,
) (service.NotebookVisualizationCheckResult, error) {
	var result service.NotebookVisualizationCheckResult
	err := c.postJSON(ctx, "/notebooks/"+url.PathEscape(notebookID)+"/visualizations/check", request, &result)
	return result, err
}

// RunNotebook executes an explicit notebook selection through its owning server.
func (c *Client) RunNotebook(
	ctx context.Context,
	notebookID string,
	request service.RunNotebookRequest,
) (service.RunNotebookResult, error) {
	var result service.RunNotebookResult
	err := c.postJSON(ctx, "/notebooks/"+url.PathEscape(notebookID)+"/run", request, &result)
	return result, err
}

// CancelNotebookRun cancels active manual/automatic work for one notebook.
func (c *Client) CancelNotebookRun(ctx context.Context, notebookID string) error {
	var result map[string]string
	return c.postJSON(ctx, "/notebooks/"+url.PathEscape(notebookID)+"/cancel", map[string]any{}, &result)
}

// RequestNotebookAgentQuestionnaire blocks a native notebook MCP tool until
// the owning browser answers or cancels the turn. The turn token is scoped to
// this one interaction channel and is not part of the JSON payload.
func (c *Client) RequestNotebookAgentQuestionnaire(
	ctx context.Context,
	notebookID string,
	turnToken string,
	request service.NotebookAgentQuestionnaireRequest,
) (service.NotebookAgentInteractionResult, error) {
	var envelope struct {
		Result service.NotebookAgentInteractionResult `json:"result"`
	}
	err := c.postJSONHeaders(
		ctx,
		"/notebooks/"+url.PathEscape(notebookID)+"/agent/native/questionnaire",
		request,
		&envelope,
		notebookAgentTurnHeaders(turnToken),
	)
	return envelope.Result, err
}

func (c *Client) RequestNotebookAgentConnectionAccess(
	ctx context.Context,
	notebookID string,
	turnToken string,
	request service.NotebookAgentConnectionAccessRequest,
) (service.NotebookAgentInteractionResult, error) {
	var envelope struct {
		Result service.NotebookAgentInteractionResult `json:"result"`
	}
	err := c.postJSONHeaders(
		ctx,
		"/notebooks/"+url.PathEscape(notebookID)+"/agent/native/connections/request",
		request,
		&envelope,
		notebookAgentTurnHeaders(turnToken),
	)
	return envelope.Result, err
}

func (c *Client) ListNotebookAgentQueryConnections(
	ctx context.Context,
	notebookID string,
	turnToken string,
) (service.NotebookAgentConnectionListResult, error) {
	var envelope struct {
		Result service.NotebookAgentConnectionListResult `json:"result"`
	}
	err := c.postJSONHeaders(
		ctx,
		"/notebooks/"+url.PathEscape(notebookID)+"/agent/native/connections/list",
		struct{}{},
		&envelope,
		notebookAgentTurnHeaders(turnToken),
	)
	return envelope.Result, err
}

func (c *Client) DiscoverNotebookAgentConnectionCatalog(
	ctx context.Context,
	notebookID string,
	turnToken string,
	request service.NotebookAgentConnectionCatalogRequest,
) (service.NotebookAgentConnectionCatalogResult, error) {
	var envelope struct {
		Result service.NotebookAgentConnectionCatalogResult `json:"result"`
	}
	err := c.postJSONHeaders(
		ctx,
		"/notebooks/"+url.PathEscape(notebookID)+"/agent/native/connections/discover",
		request,
		&envelope,
		notebookAgentTurnHeaders(turnToken),
	)
	return envelope.Result, err
}

func (c *Client) QueryNotebookAgentConnectionSample(
	ctx context.Context,
	notebookID string,
	turnToken string,
	request service.NotebookAgentConnectionSampleRequest,
) (service.NotebookAgentConnectionSampleResult, error) {
	var envelope struct {
		Result service.NotebookAgentConnectionSampleResult `json:"result"`
	}
	err := c.postJSONHeaders(
		ctx,
		"/notebooks/"+url.PathEscape(notebookID)+"/agent/native/connections/query",
		request,
		&envelope,
		notebookAgentTurnHeaders(turnToken),
	)
	return envelope.Result, err
}

func notebookAgentTurnHeaders(turnToken string) map[string]string {
	return map[string]string{"X-Renart-Agent-Turn-Token": turnToken}
}
