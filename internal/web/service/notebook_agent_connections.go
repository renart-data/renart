package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"renart/internal/sqlintelligence"
	"renart/internal/web/model"
)

const (
	maxNotebookAgentCatalogItems   = 200
	defaultNotebookAgentSampleRows = 50
	maxNotebookAgentSampleRows     = 100
	maxNotebookAgentSampleBytes    = 128 << 10
	maxNotebookAgentSampleValue    = 8 << 10
	notebookAgentSampleTimeout     = 30 * time.Second
)

type NotebookAgentConnectionListResult struct {
	Connections []NotebookAgentQueryConnection `json:"connections"`
}

type NotebookAgentConnectionCatalogRequest struct {
	ConnectionName string `json:"connection_name"`
	Level          string `json:"level"`
	Database       string `json:"database,omitempty"`
	Table          string `json:"table,omitempty"`
}

type NotebookAgentSourceRecipe struct {
	Kind         string `json:"kind"`
	Language     string `json:"language"`
	Connection   string `json:"connection"`
	AssetType    string `json:"asset_type"`
	Content      string `json:"content"`
	SnapshotMode string `json:"snapshot_mode"`
	RowLimit     int    `json:"row_limit"`
}

type NotebookAgentConnectionCatalogResult struct {
	Connection      NotebookAgentQueryConnection `json:"connection"`
	Level           string                       `json:"level"`
	Databases       []string                     `json:"databases,omitempty"`
	Tables          []SQLDiscoveryTableItem      `json:"tables,omitempty"`
	Columns         []SQLColumn                  `json:"columns,omitempty"`
	Truncated       bool                         `json:"truncated,omitempty"`
	SuggestedSource *NotebookAgentSourceRecipe   `json:"suggested_source,omitempty"`
}

type NotebookAgentConnectionSampleRequest struct {
	ConnectionName string `json:"connection_name"`
	Query          string `json:"query"`
	Limit          int    `json:"limit,omitempty"`
}

type NotebookAgentConnectionSampleResult struct {
	Connection      NotebookAgentQueryConnection `json:"connection"`
	Columns         []string                     `json:"columns"`
	Rows            []map[string]any             `json:"rows"`
	Truncated       bool                         `json:"truncated,omitempty"`
	SuggestedSource NotebookAgentSourceRecipe    `json:"suggested_source"`
}

func (s *NotebookAgentService) RequestConnectionAccess(
	ctx context.Context,
	notebookID string,
	turnToken string,
	request NotebookAgentConnectionAccessRequest,
) (NotebookAgentInteractionResult, *APIError) {
	notebookID = strings.TrimSpace(notebookID)
	turnToken = strings.TrimSpace(turnToken)
	normalized, apiErr := validateNotebookAgentConnectionRequest(request)
	if apiErr != nil {
		return NotebookAgentInteractionResult{}, apiErr
	}
	if apiErr := s.verifyNativeConnectionTurn(notebookID, turnToken); apiErr != nil {
		return NotebookAgentInteractionResult{}, apiErr
	}

	if connection, ok := s.findNotebookAgentQueryConnection(normalized.ConnectionName); ok &&
		normalizeConnectionType(connection.ConnectionType) == "duckdb" {
		connection.Capabilities = append([]NotebookAgentConnectionCapability(nil), normalized.Capabilities...)
		connection.Granted = true
		return NotebookAgentInteractionResult{Status: "answered", Connection: &connection}, nil
	}

	now := s.deps.Now().UTC().Format(time.RFC3339Nano)
	s.mu.Lock()
	conversation, apiErr := s.nativeConversationLocked(notebookID, turnToken, true)
	if apiErr != nil {
		s.mu.Unlock()
		return NotebookAgentInteractionResult{}, apiErr
	}
	if conversation.pendingInteraction != nil {
		s.mu.Unlock()
		return NotebookAgentInteractionResult{}, &APIError{
			Status: http.StatusConflict, Code: "notebook_agent_interaction_pending",
			Message: "answer the current notebook agent question before another is opened",
		}
	}
	interactionID := s.deps.NewID()
	pending := &notebookAgentPendingInteraction{
		id: interactionID, turnID: conversation.activeTurn,
		result: make(chan NotebookAgentInteractionResult, 1),
	}
	conversation.pendingInteraction = pending
	conversation.snapshot.Interaction = &NotebookAgentInteraction{
		ID: interactionID, TurnID: conversation.activeTurn,
		Kind: NotebookAgentInteractionConnection, Status: "pending",
		Title: normalized.Title, Description: normalized.Description,
		ConnectionRequest: &normalized, CreatedAt: now,
	}
	conversation.snapshot.Revision++
	snapshot := cloneNotebookAgentSnapshot(conversation.snapshot)
	s.mu.Unlock()
	s.publish(snapshot)

	select {
	case result := <-pending.result:
		return result, nil
	case <-ctx.Done():
		s.cancelPendingInteraction(notebookID, pending.id, pending.turnID)
		return NotebookAgentInteractionResult{}, &APIError{
			Status: http.StatusRequestTimeout, Code: "notebook_agent_interaction_cancelled",
			Message: "the notebook agent connection request was cancelled",
		}
	}
}

func (s *NotebookAgentService) ListQueryConnections(
	notebookID string,
	turnToken string,
) (NotebookAgentConnectionListResult, *APIError) {
	if apiErr := s.verifyNativeConnectionTurn(notebookID, turnToken); apiErr != nil {
		return NotebookAgentConnectionListResult{}, apiErr
	}
	state := s.notebookAgentWorkspaceState()
	result := make([]NotebookAgentQueryConnection, 0, len(state.QueryConnections))
	for _, candidate := range state.QueryConnections {
		connection := notebookAgentQueryConnection(candidate, state.SelectedEnvironment)
		connection.Capabilities = []NotebookAgentConnectionCapability{
			NotebookAgentConnectionDiscover,
			NotebookAgentConnectionSampleQuery,
		}
		connection.Granted = normalizeConnectionType(connection.ConnectionType) == "duckdb" ||
			s.hasNotebookAgentConnectionGrant(notebookID, turnToken, connection.Name, "")
		result = append(result, connection)
	}
	return NotebookAgentConnectionListResult{Connections: result}, nil
}

func (s *NotebookAgentService) DiscoverConnectionCatalog(
	ctx context.Context,
	notebookID string,
	turnToken string,
	request NotebookAgentConnectionCatalogRequest,
) (NotebookAgentConnectionCatalogResult, *APIError) {
	request.ConnectionName = strings.TrimSpace(request.ConnectionName)
	request.Level = strings.ToLower(strings.TrimSpace(request.Level))
	request.Database = strings.TrimSpace(request.Database)
	request.Table = strings.TrimSpace(request.Table)
	if request.ConnectionName == "" {
		return NotebookAgentConnectionCatalogResult{}, badRequestError("notebook_agent_connection_required", "connection_name is required")
	}
	if request.Level != "databases" && request.Level != "tables" && request.Level != "columns" {
		return NotebookAgentConnectionCatalogResult{}, badRequestError("notebook_agent_catalog_level_invalid", "level must be databases, tables, or columns")
	}
	if request.Level == "tables" && request.Database == "" {
		return NotebookAgentConnectionCatalogResult{}, badRequestError("notebook_agent_catalog_database_required", "database is required for table discovery")
	}
	if request.Level == "columns" && request.Table == "" {
		return NotebookAgentConnectionCatalogResult{}, badRequestError("notebook_agent_catalog_table_required", "table is required for column discovery")
	}
	connection, apiErr := s.requireNotebookAgentConnectionGrant(
		notebookID, turnToken, request.ConnectionName, NotebookAgentConnectionDiscover,
	)
	if apiErr != nil {
		return NotebookAgentConnectionCatalogResult{}, apiErr
	}
	result := NotebookAgentConnectionCatalogResult{Connection: connection, Level: request.Level}
	switch request.Level {
	case "databases":
		if s.deps.DiscoverDatabases == nil {
			return NotebookAgentConnectionCatalogResult{}, notebookAgentConnectionUnavailable("database discovery")
		}
		discovered, discoveryErr := s.deps.DiscoverDatabases(ctx, connection.Name, connection.Environment)
		if discoveryErr != nil {
			return NotebookAgentConnectionCatalogResult{}, safeNotebookAgentDiscoveryError("database", discoveryErr)
		}
		result.Databases, result.Truncated = boundedNotebookAgentSlice(discovered.Databases)
	case "tables":
		if s.deps.DiscoverTables == nil {
			return NotebookAgentConnectionCatalogResult{}, notebookAgentConnectionUnavailable("table discovery")
		}
		discovered, discoveryErr := s.deps.DiscoverTables(ctx, connection.Name, request.Database, connection.Environment)
		if discoveryErr != nil {
			return NotebookAgentConnectionCatalogResult{}, safeNotebookAgentDiscoveryError("table", discoveryErr)
		}
		result.Tables, result.Truncated = boundedNotebookAgentSlice(discovered.Tables)
	case "columns":
		if s.deps.DiscoverColumns == nil {
			return NotebookAgentConnectionCatalogResult{}, notebookAgentConnectionUnavailable("column discovery")
		}
		discovered, status := s.deps.DiscoverColumns(ctx, connection.Name, request.Table, connection.Environment)
		if status < http.StatusOK || status >= http.StatusMultipleChoices || discovered.Status != "ok" {
			return NotebookAgentConnectionCatalogResult{}, badRequestError("notebook_agent_column_discovery_failed", "could not discover columns on the approved connection")
		}
		result.Columns, result.Truncated = boundedNotebookAgentSlice(discovered.Columns)
		result.SuggestedSource = notebookAgentSourceRecipe(
			connection,
			"select * from "+quoteRuntimeRelation(request.Table, connection.ConnectionType),
		)
	}
	return result, nil
}

func (s *NotebookAgentService) QueryConnectionSample(
	ctx context.Context,
	notebookID string,
	turnToken string,
	request NotebookAgentConnectionSampleRequest,
) (NotebookAgentConnectionSampleResult, *APIError) {
	request.ConnectionName = strings.TrimSpace(request.ConnectionName)
	request.Query = strings.TrimSpace(request.Query)
	if request.ConnectionName == "" || request.Query == "" {
		return NotebookAgentConnectionSampleResult{}, badRequestError("notebook_agent_sample_query_required", "connection_name and query are required")
	}
	if len(request.Query) > 64<<10 {
		return NotebookAgentConnectionSampleResult{}, badRequestError("notebook_agent_sample_query_too_large", "sample queries may not exceed 64 KiB")
	}
	if request.Limit <= 0 {
		request.Limit = defaultNotebookAgentSampleRows
	}
	if request.Limit > maxNotebookAgentSampleRows {
		return NotebookAgentConnectionSampleResult{}, badRequestError("notebook_agent_sample_limit_invalid", fmt.Sprintf("sample query limit may not exceed %d rows", maxNotebookAgentSampleRows))
	}
	connection, apiErr := s.requireNotebookAgentConnectionGrant(
		notebookID, turnToken, request.ConnectionName, NotebookAgentConnectionSampleQuery,
	)
	if apiErr != nil {
		return NotebookAgentConnectionSampleResult{}, apiErr
	}
	readOnly, err := sqlintelligence.IsReadOnlySingleQuery(request.Query, connection.Dialect)
	if err != nil {
		return NotebookAgentConnectionSampleResult{}, badRequestError("notebook_agent_sample_query_invalid", fmt.Sprintf("could not parse the sample query: %v", err))
	}
	if !readOnly {
		return NotebookAgentConnectionSampleResult{}, badRequestError("notebook_agent_sample_query_not_read_only", "sample queries must contain exactly one read-only result-producing SELECT")
	}
	limited := wrapNotebookAgentSampleQuery(request.Query, connection.ConnectionType, request.Limit+1)
	if s.deps.RunConnectionQuery == nil {
		return NotebookAgentConnectionSampleResult{}, notebookAgentConnectionUnavailable("sample queries")
	}
	queryCtx, cancel := context.WithTimeout(ctx, notebookAgentSampleTimeout)
	defer cancel()
	columns, rows, err := s.deps.RunConnectionQuery(queryCtx, connection.Name, connection.Environment, limited)
	if err != nil {
		if errors.Is(queryCtx.Err(), context.DeadlineExceeded) {
			return NotebookAgentConnectionSampleResult{}, &APIError{Status: http.StatusRequestTimeout, Code: "notebook_agent_sample_query_timeout", Message: "the approved connection sample exceeded 30 seconds"}
		}
		if errors.Is(queryCtx.Err(), context.Canceled) {
			return NotebookAgentConnectionSampleResult{}, &APIError{Status: http.StatusRequestTimeout, Code: "notebook_agent_sample_query_cancelled", Message: "the approved connection sample was cancelled"}
		}
		return NotebookAgentConnectionSampleResult{}, badRequestError("notebook_agent_sample_query_failed", "the approved connection could not run this sample query")
	}
	rows, truncated := capNotebookAgentSample(columns, rows, request.Limit)
	return NotebookAgentConnectionSampleResult{
		Connection: connection, Columns: columns, Rows: rows, Truncated: truncated,
		SuggestedSource: *notebookAgentSourceRecipe(connection, request.Query),
	}, nil
}

func (s *NotebookAgentService) nativeConversationLocked(
	notebookID string,
	turnToken string,
	editOnly bool,
) (*notebookAgentConversation, *APIError) {
	conversation := s.items[strings.TrimSpace(notebookID)]
	if conversation == nil || conversation.snapshot.Status != "running" || conversation.activeTurn == "" || conversation.activeToken != strings.TrimSpace(turnToken) {
		return nil, &APIError{Status: http.StatusConflict, Code: "notebook_agent_turn_token_invalid", Message: "this native capability does not belong to the active notebook turn"}
	}
	if editOnly && conversation.snapshot.Mode != NotebookAgentModeEdit {
		return nil, &APIError{Status: http.StatusForbidden, Code: "notebook_agent_edit_mode_required", Message: "live connection access is available only in Edit mode"}
	}
	return conversation, nil
}

func (s *NotebookAgentService) verifyNativeConnectionTurn(notebookID, turnToken string) *APIError {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, apiErr := s.nativeConversationLocked(notebookID, turnToken, true)
	return apiErr
}

func (s *NotebookAgentService) requireNotebookAgentConnectionGrant(
	notebookID string,
	turnToken string,
	connectionName string,
	capability NotebookAgentConnectionCapability,
) (NotebookAgentQueryConnection, *APIError) {
	if apiErr := s.verifyNativeConnectionTurn(notebookID, turnToken); apiErr != nil {
		return NotebookAgentQueryConnection{}, apiErr
	}
	state := s.notebookAgentWorkspaceState()
	connection, ok := findNotebookAgentQueryConnection(state, connectionName)
	if !ok {
		return NotebookAgentQueryConnection{}, badRequestError("notebook_agent_connection_not_found", fmt.Sprintf("query connection %q is not configured in the current environment", connectionName))
	}
	connection.Capabilities = []NotebookAgentConnectionCapability{capability}
	if normalizeConnectionType(connection.ConnectionType) == "duckdb" {
		connection.Granted = true
		return connection, nil
	}
	s.mu.Lock()
	conversation, apiErr := s.nativeConversationLocked(notebookID, turnToken, true)
	if apiErr == nil {
		grant := conversation.connectionGrants[connection.Name]
		if grant.ConnectionName == "" || grant.TurnID != conversation.activeTurn || grant.Environment != connection.Environment || !notebookAgentGrantHasCapability(grant, capability) || !s.deps.Now().Before(grant.ExpiresAt) {
			apiErr = &APIError{Status: http.StatusForbidden, Code: "notebook_agent_connection_access_required", Message: fmt.Sprintf("request user approval for connection %q before using it", connection.Name)}
		} else {
			connection.Capabilities = append([]NotebookAgentConnectionCapability(nil), grant.Capabilities...)
			connection.Granted = true
		}
	}
	s.mu.Unlock()
	return connection, apiErr
}

func (s *NotebookAgentService) hasNotebookAgentConnectionGrant(notebookID, turnToken, connectionName string, capability NotebookAgentConnectionCapability) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	conversation, apiErr := s.nativeConversationLocked(notebookID, turnToken, true)
	if apiErr != nil {
		return false
	}
	grant := conversation.connectionGrants[connectionName]
	return grant.ConnectionName != "" && grant.TurnID == conversation.activeTurn &&
		(capability == "" || notebookAgentGrantHasCapability(grant, capability)) &&
		s.deps.Now().Before(grant.ExpiresAt)
}

func (s *NotebookAgentService) validateNotebookAgentConnectionAnswerLocked(
	notebookID string,
	conversation *notebookAgentConversation,
	connectionName string,
) (NotebookAgentQueryConnection, NotebookAgentConnectionGrant, *APIError) {
	if connectionName == "" {
		return NotebookAgentQueryConnection{}, NotebookAgentConnectionGrant{}, badRequestError("notebook_agent_connection_required", "choose or create a connection to approve")
	}
	request := conversation.snapshot.Interaction.ConnectionRequest
	if request == nil {
		return NotebookAgentQueryConnection{}, NotebookAgentConnectionGrant{}, &APIError{Status: http.StatusConflict, Code: "notebook_agent_connection_request_invalid", Message: "the pending connection request is incomplete"}
	}
	state := s.notebookAgentWorkspaceState()
	connection, ok := findNotebookAgentQueryConnection(state, connectionName)
	if !ok {
		return NotebookAgentQueryConnection{}, NotebookAgentConnectionGrant{}, badRequestError("notebook_agent_connection_not_found", fmt.Sprintf("query connection %q is not configured in environment %q", connectionName, state.SelectedEnvironment))
	}
	if request.ConnectionName != "" && !strings.EqualFold(request.ConnectionName, connection.Name) {
		return NotebookAgentQueryConnection{}, NotebookAgentConnectionGrant{}, badRequestError("notebook_agent_connection_mismatch", fmt.Sprintf("the agent requested connection %q", request.ConnectionName))
	}
	if request.ConnectionType != "" && normalizeConnectionType(connection.ConnectionType) != request.ConnectionType {
		return NotebookAgentQueryConnection{}, NotebookAgentConnectionGrant{}, badRequestError("notebook_agent_connection_type_mismatch", fmt.Sprintf("choose a %s query connection", request.ConnectionType))
	}
	connection.Capabilities = append([]NotebookAgentConnectionCapability(nil), request.Capabilities...)
	connection.Granted = true
	grant := NotebookAgentConnectionGrant{
		NotebookID: notebookID, TurnID: conversation.activeTurn,
		ConnectionName: connection.Name, Environment: connection.Environment,
		Capabilities: append([]NotebookAgentConnectionCapability(nil), request.Capabilities...),
		ExpiresAt:    s.deps.Now().Add(notebookAgentTurnTimeout),
	}
	return connection, grant, nil
}

func validateNotebookAgentConnectionRequest(request NotebookAgentConnectionAccessRequest) (NotebookAgentConnectionAccessRequest, *APIError) {
	request.Title = strings.TrimSpace(request.Title)
	request.Description = strings.TrimSpace(request.Description)
	request.ConnectionName = strings.TrimSpace(request.ConnectionName)
	request.ConnectionType = normalizeConnectionType(request.ConnectionType)
	if request.Title == "" || len(request.Title) > 120 {
		return NotebookAgentConnectionAccessRequest{}, badRequestError("notebook_agent_connection_title_invalid", "connection request title must contain 1 to 120 characters")
	}
	if len(request.Description) > 1<<10 || len(request.ConnectionName) > 160 {
		return NotebookAgentConnectionAccessRequest{}, badRequestError("notebook_agent_connection_request_invalid", "connection request description or name is too long")
	}
	if strings.TrimSpace(request.ConnectionType) == "default" {
		request.ConnectionType = ""
	}
	if request.ConnectionType != "" {
		if _, ok := queryAssetTypeForConnectionType(request.ConnectionType); !ok {
			return NotebookAgentConnectionAccessRequest{}, badRequestError("notebook_agent_connection_type_unsupported", fmt.Sprintf("connection type %q cannot run notebook SQL", request.ConnectionType))
		}
	}
	if len(request.Capabilities) == 0 {
		request.Capabilities = []NotebookAgentConnectionCapability{
			NotebookAgentConnectionDiscover,
			NotebookAgentConnectionSampleQuery,
		}
	}
	seen := make(map[NotebookAgentConnectionCapability]struct{}, len(request.Capabilities))
	normalized := make([]NotebookAgentConnectionCapability, 0, len(request.Capabilities))
	for _, capability := range request.Capabilities {
		switch capability {
		case NotebookAgentConnectionDiscover, NotebookAgentConnectionSampleQuery:
		default:
			return NotebookAgentConnectionAccessRequest{}, badRequestError("notebook_agent_connection_capability_invalid", "connection capabilities must be discover or sample_query")
		}
		if _, exists := seen[capability]; !exists {
			seen[capability] = struct{}{}
			normalized = append(normalized, capability)
		}
	}
	request.Capabilities = normalized
	return request, nil
}

func (s *NotebookAgentService) notebookAgentWorkspaceState() model.WorkspaceState {
	if s.deps.CurrentState == nil {
		return model.WorkspaceState{}
	}
	return s.deps.CurrentState()
}

func (s *NotebookAgentService) findNotebookAgentQueryConnection(name string) (NotebookAgentQueryConnection, bool) {
	return findNotebookAgentQueryConnection(s.notebookAgentWorkspaceState(), name)
}

func findNotebookAgentQueryConnection(state model.WorkspaceState, name string) (NotebookAgentQueryConnection, bool) {
	name = strings.TrimSpace(name)
	for _, candidate := range state.QueryConnections {
		if strings.EqualFold(strings.TrimSpace(candidate.Name), name) {
			return notebookAgentQueryConnection(candidate, state.SelectedEnvironment), true
		}
	}
	return NotebookAgentQueryConnection{}, false
}

func notebookAgentQueryConnection(candidate model.WorkspaceQueryConnection, environment string) NotebookAgentQueryConnection {
	return NotebookAgentQueryConnection{
		Name:           strings.TrimSpace(candidate.Name),
		ConnectionType: normalizeConnectionType(candidate.ConnectionType),
		AssetType:      strings.TrimSpace(candidate.AssetType), Dialect: strings.TrimSpace(candidate.Dialect),
		Environment: strings.TrimSpace(environment),
	}
}

func notebookAgentGrantHasCapability(grant NotebookAgentConnectionGrant, capability NotebookAgentConnectionCapability) bool {
	for _, candidate := range grant.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}

func notebookAgentSourceRecipe(connection NotebookAgentQueryConnection, query string) *NotebookAgentSourceRecipe {
	return &NotebookAgentSourceRecipe{
		Kind: "cell.create", Language: "sql", Connection: connection.Name,
		AssetType: connection.AssetType, Content: strings.TrimSpace(query),
		SnapshotMode: "sample", RowLimit: 500,
	}
}

func wrapNotebookAgentSampleQuery(query, connectionType string, limit int) string {
	query = strings.TrimRight(strings.TrimSpace(query), "; \n\r\t")
	switch normalizeConnectionType(connectionType) {
	case "mssql", "synapse", "fabric":
		return fmt.Sprintf("SELECT TOP (%d) * FROM (\n%s\n) AS renart_agent_sample", limit, query)
	case "oracle":
		return fmt.Sprintf("SELECT * FROM (\n%s\n) renart_agent_sample\nFETCH FIRST %d ROWS ONLY", query, limit)
	default:
		return fmt.Sprintf("SELECT * FROM (\n%s\n) AS renart_agent_sample\nLIMIT %d", query, limit)
	}
}

func boundedNotebookAgentSlice[T any](values []T) ([]T, bool) {
	if len(values) <= maxNotebookAgentCatalogItems {
		return append([]T(nil), values...), false
	}
	return append([]T(nil), values[:maxNotebookAgentCatalogItems]...), true
}

func capNotebookAgentSample(columns []string, input []map[string]any, limit int) ([]map[string]any, bool) {
	if len(columns) == 0 && len(input) > 0 {
		for name := range input[0] {
			columns = append(columns, name)
		}
		sort.Strings(columns)
	}
	rows := make([]map[string]any, 0, min(len(input), limit))
	truncated := len(input) > limit
	for _, inputRow := range input {
		if len(rows) >= limit {
			truncated = true
			break
		}
		row := make(map[string]any, len(columns))
		for _, column := range columns {
			row[column] = capNotebookAgentSampleValue(inputRow[column])
		}
		candidate := append(rows, row)
		encoded, _ := json.Marshal(struct {
			Columns []string         `json:"columns"`
			Rows    []map[string]any `json:"rows"`
		}{Columns: columns, Rows: candidate})
		if len(encoded) > maxNotebookAgentSampleBytes {
			truncated = true
			break
		}
		rows = candidate
	}
	return rows, truncated
}

func capNotebookAgentSampleValue(value any) any {
	if text, ok := value.(string); ok {
		return truncateNotebookAgentText(text, maxNotebookAgentSampleValue)
	}
	encoded, err := json.Marshal(value)
	if err == nil && len(encoded) <= maxNotebookAgentSampleValue {
		return value
	}
	if err != nil {
		return "[value unavailable]"
	}
	return fmt.Sprintf("[value omitted: %d bytes]", len(encoded))
}

func notebookAgentConnectionUnavailable(capability string) *APIError {
	return &APIError{Status: http.StatusServiceUnavailable, Code: "notebook_agent_connection_capability_unavailable", Message: capability + " is unavailable for native notebook agents"}
}

func safeNotebookAgentDiscoveryError(level string, apiErr *APIError) *APIError {
	if apiErr != nil && (apiErr.Code == "connection_type_not_supported" || apiErr.Code == "connection_not_found") {
		return &APIError{Status: apiErr.Status, Code: apiErr.Code, Message: apiErr.Message}
	}
	return badRequestError("notebook_agent_"+level+"_discovery_failed", "could not discover "+level+" metadata on the approved connection")
}
