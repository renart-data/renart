package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	notebookAgentEventType       = "notebook.agent"
	maxNotebookAgentMessageBytes = 32 << 10
	maxNotebookAgentMessages     = 100
	maxNotebookAgentActivities   = 200
	maxNotebookAgentTextBytes    = 256 << 10
	maxNotebookAgentReferences   = 12
	notebookAgentTurnTimeout     = 30 * time.Minute
)

type NotebookAgentMode string

const (
	NotebookAgentModeAsk  NotebookAgentMode = "ask"
	NotebookAgentModeEdit NotebookAgentMode = "edit"
)

type NotebookAgentProvider struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Available bool   `json:"available"`
	Path      string `json:"-"`
}

type NotebookAgentMessage struct {
	ID         string                   `json:"id"`
	TurnID     string                   `json:"turn_id"`
	Role       string                   `json:"role"`
	Content    string                   `json:"content"`
	References []NotebookAgentReference `json:"references,omitempty"`
	Status     string                   `json:"status"`
	CreatedAt  string                   `json:"created_at"`
}

// NotebookAgentReferenceRequest is the untrusted address supplied by the
// browser. Labels and connection/type context are always resolved by the
// server from the current filesystem-backed workspace state.
type NotebookAgentReferenceRequest struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// NotebookAgentReference is a bounded, credential-free context attachment for
// one user message. Cell references are restricted to the selected notebook;
// asset references may address pipeline assets in the current workspace.
type NotebookAgentReference struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
}

type NotebookAgentActivity struct {
	ID         string `json:"id"`
	TurnID     string `json:"turn_id"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	Detail     string `json:"detail,omitempty"`
	Status     string `json:"status"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
}

// NotebookAgentSnapshot is a complete, replayable view of one notebook's
// in-memory chat. Every SSE publish carries the full bounded snapshot so a
// dropped intermediate event cannot corrupt the browser's transcript.
type NotebookAgentSnapshot struct {
	Type        string                    `json:"type"`
	NotebookID  string                    `json:"notebook_id"`
	Revision    int64                     `json:"revision"`
	Status      string                    `json:"status"`
	Provider    string                    `json:"provider,omitempty"`
	Mode        NotebookAgentMode         `json:"mode,omitempty"`
	Messages    []NotebookAgentMessage    `json:"messages"`
	Activities  []NotebookAgentActivity   `json:"activities"`
	Interaction *NotebookAgentInteraction `json:"interaction,omitempty"`
	Error       string                    `json:"error,omitempty"`
	StartedAt   string                    `json:"started_at,omitempty"`
	FinishedAt  string                    `json:"finished_at,omitempty"`
}

type NotebookAgentState struct {
	Conversation NotebookAgentSnapshot   `json:"conversation"`
	Providers    []NotebookAgentProvider `json:"providers"`
}

type StartNotebookAgentTurnRequest struct {
	Provider   string                          `json:"provider"`
	Mode       NotebookAgentMode               `json:"mode"`
	Message    string                          `json:"message"`
	References []NotebookAgentReferenceRequest `json:"references,omitempty"`
}

type NotebookAgentStreamEvent struct {
	Kind        string
	ID          string
	Name        string
	Title       string
	Detail      string
	Status      string
	Text        string
	TextReplace bool
}

type NotebookAgentProviderRunRequest struct {
	Provider         string
	ProviderBinary   string
	Mode             NotebookAgentMode
	NotebookID       string
	Prompt           string
	SessionID        string
	RunDir           string
	WorkspaceRoot    string
	RenartExecutable string
	TurnToken        string
}

type NotebookAgentProviderRunResult struct {
	SessionID string
}

type NotebookAgentDependencies struct {
	WorkspaceRoot     string
	RenartExecutable  string
	ValidateNotebook  func(notebookID string) *APIError
	ResolveReferences func(notebookID string, references []NotebookAgentReferenceRequest) ([]NotebookAgentReference, *APIError)
	PublishEvent      func(payload any)
	LookPath          func(file string) (string, error)
	RunProvider       func(context.Context, NotebookAgentProviderRunRequest, func(NotebookAgentStreamEvent)) (NotebookAgentProviderRunResult, error)
	Now               func() time.Time
	NewID             func() string
}

type notebookAgentConversation struct {
	snapshot           NotebookAgentSnapshot
	cancel             context.CancelFunc
	activeTurn         string
	activeToken        string
	pendingInteraction *notebookAgentPendingInteraction
	sessions           map[string]string
	runDirs            map[string]string
}

// NotebookAgentService owns local agent process lifecycles and bounded chat
// state. Authored notebook state remains owned by NotebookService and is only
// reachable by the child agent through the scoped MCP server.
type NotebookAgentService struct {
	deps NotebookAgentDependencies

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	items  map[string]*notebookAgentConversation
	wg     sync.WaitGroup
}

func NewNotebookAgentService(ctx context.Context, deps NotebookAgentDependencies) *NotebookAgentService {
	if ctx == nil {
		ctx = context.Background()
	}
	serviceCtx, cancel := context.WithCancel(ctx)
	if deps.LookPath == nil {
		deps.LookPath = defaultNotebookAgentLookPath
	}
	if deps.RunProvider == nil {
		deps.RunProvider = runLocalNotebookAgentProvider
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.NewID == nil {
		deps.NewID = uuid.NewString
	}
	return &NotebookAgentService{
		deps: deps, ctx: serviceCtx, cancel: cancel,
		items: make(map[string]*notebookAgentConversation),
	}
}

func (s *NotebookAgentService) Providers() []NotebookAgentProvider {
	providers := []NotebookAgentProvider{
		{ID: "codex", Label: "Codex"},
		{ID: "claude", Label: "Claude Code"},
		{ID: "opencode", Label: "OpenCode"},
	}
	for index := range providers {
		path, err := s.deps.LookPath(providers[index].ID)
		if err == nil && strings.TrimSpace(path) != "" {
			if !filepath.IsAbs(path) {
				if absolute, absoluteErr := filepath.Abs(path); absoluteErr == nil {
					path = absolute
				}
			}
			providers[index].Available = true
			providers[index].Path = path
		}
	}
	return providers
}

func (s *NotebookAgentService) State(notebookID string) (NotebookAgentState, *APIError) {
	notebookID = strings.TrimSpace(notebookID)
	if apiErr := s.validateNotebook(notebookID); apiErr != nil {
		return NotebookAgentState{}, apiErr
	}
	s.mu.Lock()
	conversation := s.conversationLocked(notebookID)
	snapshot := cloneNotebookAgentSnapshot(conversation.snapshot)
	s.mu.Unlock()
	return NotebookAgentState{Conversation: snapshot, Providers: s.Providers()}, nil
}

func (s *NotebookAgentService) StartTurn(notebookID string, request StartNotebookAgentTurnRequest) (NotebookAgentSnapshot, *APIError) {
	notebookID = strings.TrimSpace(notebookID)
	if apiErr := s.validateNotebook(notebookID); apiErr != nil {
		return NotebookAgentSnapshot{}, apiErr
	}
	provider := strings.ToLower(strings.TrimSpace(request.Provider))
	mode := request.Mode
	message := strings.TrimSpace(request.Message)
	if message == "" {
		return NotebookAgentSnapshot{}, badRequestError("notebook_agent_message_required", "enter a message for the notebook agent")
	}
	if len(message) > maxNotebookAgentMessageBytes {
		return NotebookAgentSnapshot{}, badRequestError("notebook_agent_message_too_large", fmt.Sprintf("agent messages may not exceed %d bytes", maxNotebookAgentMessageBytes))
	}
	if len(request.References) > maxNotebookAgentReferences {
		return NotebookAgentSnapshot{}, badRequestError(
			"notebook_agent_too_many_references",
			fmt.Sprintf("agent messages may reference at most %d cells or assets", maxNotebookAgentReferences),
		)
	}
	references, apiErr := s.resolveReferences(notebookID, request.References)
	if apiErr != nil {
		return NotebookAgentSnapshot{}, apiErr
	}
	if mode != NotebookAgentModeAsk && mode != NotebookAgentModeEdit {
		return NotebookAgentSnapshot{}, badRequestError("notebook_agent_mode_invalid", "agent mode must be ask or edit")
	}
	providerInfo, ok := findNotebookAgentProvider(s.Providers(), provider)
	if !ok {
		return NotebookAgentSnapshot{}, badRequestError("notebook_agent_provider_invalid", "agent provider must be codex, claude, or opencode")
	}
	if !providerInfo.Available {
		return NotebookAgentSnapshot{}, &APIError{
			Status: http.StatusConflict, Code: "notebook_agent_provider_unavailable",
			Message: fmt.Sprintf("%s is not installed or is not available on PATH", providerInfo.Label),
		}
	}

	now := s.deps.Now().UTC()
	turnID := s.deps.NewID()
	turnToken := s.deps.NewID()
	s.mu.Lock()
	conversation := s.conversationLocked(notebookID)
	if conversation.snapshot.Status == "running" || conversation.snapshot.Status == "cancelling" {
		s.mu.Unlock()
		return NotebookAgentSnapshot{}, &APIError{
			Status: http.StatusConflict, Code: "notebook_agent_busy",
			Message: "wait for the current notebook agent turn to finish or stop it first",
		}
	}
	runKey := notebookAgentRunKey(provider, mode)
	runDir := conversation.runDirs[runKey]
	if runDir == "" {
		created, err := os.MkdirTemp("", "renart-notebook-agent-")
		if err != nil {
			s.mu.Unlock()
			return NotebookAgentSnapshot{}, internalError("notebook_agent_workspace_failed", err.Error())
		}
		if err := os.Chmod(created, 0o700); err != nil {
			_ = os.RemoveAll(created)
			s.mu.Unlock()
			return NotebookAgentSnapshot{}, internalError("notebook_agent_workspace_failed", err.Error())
		}
		runDir = created
		conversation.runDirs[runKey] = runDir
	}
	turnCtx, cancel := context.WithTimeout(s.ctx, notebookAgentTurnTimeout)
	conversation.cancel = cancel
	conversation.activeTurn = turnID
	conversation.activeToken = turnToken
	conversation.snapshot.Status = "running"
	conversation.snapshot.Provider = provider
	conversation.snapshot.Mode = mode
	conversation.snapshot.Error = ""
	conversation.snapshot.StartedAt = now.Format(time.RFC3339Nano)
	conversation.snapshot.FinishedAt = ""
	conversation.snapshot.Interaction = nil
	conversation.snapshot.Messages = append(conversation.snapshot.Messages, NotebookAgentMessage{
		ID: s.deps.NewID(), TurnID: turnID, Role: "user", Content: message, References: references,
		Status: "complete", CreatedAt: now.Format(time.RFC3339Nano),
	})
	conversation.snapshot.Messages = trimNotebookAgentMessages(conversation.snapshot.Messages)
	conversation.snapshot.Revision++
	sessionID := conversation.sessions[runKey]
	prompt := buildNotebookAgentPrompt(notebookID, mode, conversation.snapshot.Messages, sessionID != "")
	snapshot := cloneNotebookAgentSnapshot(conversation.snapshot)
	s.mu.Unlock()
	s.publish(snapshot)

	s.wg.Add(1)
	go func() {
		defer cancel()
		s.runTurn(turnCtx, notebookID, turnID, runKey, NotebookAgentProviderRunRequest{
			Provider: provider, Mode: mode, NotebookID: notebookID, Prompt: prompt,
			ProviderBinary: providerInfo.Path,
			SessionID:      sessionID, RunDir: runDir, WorkspaceRoot: s.deps.WorkspaceRoot,
			RenartExecutable: s.deps.RenartExecutable,
			TurnToken:        turnToken,
		})
	}()
	return snapshot, nil
}

func (s *NotebookAgentService) Cancel(notebookID string) (NotebookAgentSnapshot, *APIError) {
	notebookID = strings.TrimSpace(notebookID)
	if apiErr := s.validateNotebook(notebookID); apiErr != nil {
		return NotebookAgentSnapshot{}, apiErr
	}
	s.mu.Lock()
	conversation := s.conversationLocked(notebookID)
	if conversation.snapshot.Status != "running" && conversation.snapshot.Status != "cancelling" {
		snapshot := cloneNotebookAgentSnapshot(conversation.snapshot)
		s.mu.Unlock()
		return snapshot, nil
	}
	conversation.snapshot.Status = "cancelling"
	s.cancelPendingInteractionLocked(conversation, "cancelled")
	conversation.snapshot.Revision++
	cancel := conversation.cancel
	snapshot := cloneNotebookAgentSnapshot(conversation.snapshot)
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.publish(snapshot)
	return snapshot, nil
}

func (s *NotebookAgentService) Reset(notebookID string) (NotebookAgentSnapshot, *APIError) {
	notebookID = strings.TrimSpace(notebookID)
	if apiErr := s.validateNotebook(notebookID); apiErr != nil {
		return NotebookAgentSnapshot{}, apiErr
	}
	s.mu.Lock()
	current := s.conversationLocked(notebookID)
	if current.snapshot.Status == "running" || current.snapshot.Status == "cancelling" {
		s.mu.Unlock()
		return NotebookAgentSnapshot{}, &APIError{
			Status: http.StatusConflict, Code: "notebook_agent_busy",
			Message: "stop the current notebook agent turn before starting a new chat",
		}
	}
	runDirs := make([]string, 0, len(current.runDirs))
	for _, runDir := range current.runDirs {
		runDirs = append(runDirs, runDir)
	}
	next := newNotebookAgentConversation(notebookID)
	next.snapshot.Revision = current.snapshot.Revision + 1
	s.items[notebookID] = next
	snapshot := cloneNotebookAgentSnapshot(next.snapshot)
	s.mu.Unlock()
	for _, runDir := range runDirs {
		_ = os.RemoveAll(runDir)
	}
	s.publish(snapshot)
	return snapshot, nil
}

func (s *NotebookAgentService) Close() {
	s.cancel()
	s.mu.Lock()
	runDirs := []string{}
	for _, conversation := range s.items {
		s.cancelPendingInteractionLocked(conversation, "cancelled")
		if conversation.cancel != nil {
			conversation.cancel()
		}
		for _, runDir := range conversation.runDirs {
			runDirs = append(runDirs, runDir)
		}
	}
	s.mu.Unlock()
	s.wg.Wait()
	for _, runDir := range runDirs {
		_ = os.RemoveAll(runDir)
	}
}

func (s *NotebookAgentService) runTurn(
	ctx context.Context,
	notebookID string,
	turnID string,
	runKey string,
	request NotebookAgentProviderRunRequest,
) {
	defer s.wg.Done()
	result, err := s.deps.RunProvider(ctx, request, func(event NotebookAgentStreamEvent) {
		s.applyStreamEvent(notebookID, turnID, event)
	})

	now := s.deps.Now().UTC().Format(time.RFC3339Nano)
	s.mu.Lock()
	conversation := s.items[notebookID]
	if conversation == nil || conversation.activeTurn != turnID {
		s.mu.Unlock()
		return
	}
	if strings.TrimSpace(result.SessionID) != "" && err == nil {
		conversation.sessions[runKey] = strings.TrimSpace(result.SessionID)
	}
	s.cancelPendingInteractionLocked(conversation, "cancelled")
	conversation.cancel = nil
	conversation.activeTurn = ""
	conversation.activeToken = ""
	conversation.snapshot.FinishedAt = now
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		conversation.snapshot.Status = "cancelled"
		conversation.snapshot.Error = ""
		s.finishAssistantMessageLocked(conversation, turnID, "Stopped.", "cancelled", now)
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		conversation.snapshot.Status = "error"
		conversation.snapshot.Error = "The agent turn exceeded the 30 minute limit."
		s.finishAssistantMessageLocked(conversation, turnID, conversation.snapshot.Error, "error", now)
	case err != nil:
		conversation.snapshot.Status = "error"
		conversation.snapshot.Error = truncateNotebookAgentText(err.Error(), 16<<10)
		s.finishAssistantMessageLocked(conversation, turnID, conversation.snapshot.Error, "error", now)
	default:
		conversation.snapshot.Status = "idle"
		conversation.snapshot.Error = ""
		s.finishAssistantMessageLocked(conversation, turnID, "Done.", "complete", now)
	}
	conversation.snapshot.Revision++
	snapshot := cloneNotebookAgentSnapshot(conversation.snapshot)
	s.mu.Unlock()
	s.publish(snapshot)
}

func (s *NotebookAgentService) applyStreamEvent(notebookID, turnID string, event NotebookAgentStreamEvent) {
	now := s.deps.Now().UTC().Format(time.RFC3339Nano)
	s.mu.Lock()
	conversation := s.items[notebookID]
	if conversation == nil || conversation.activeTurn != turnID {
		s.mu.Unlock()
		return
	}
	switch event.Kind {
	case "text":
		message := s.assistantMessageLocked(conversation, turnID, now)
		if event.TextReplace {
			message.Content = truncateNotebookAgentText(event.Text, maxNotebookAgentTextBytes)
		} else {
			message.Content = truncateNotebookAgentText(message.Content+event.Text, maxNotebookAgentTextBytes)
		}
		message.Status = "streaming"
	case "activity":
		activityID := strings.TrimSpace(event.ID)
		if activityID == "" {
			activityID = s.deps.NewID()
		} else {
			activityID = turnID + ":" + activityID
		}
		index := -1
		for candidate := range conversation.snapshot.Activities {
			if conversation.snapshot.Activities[candidate].ID == activityID {
				index = candidate
				break
			}
		}
		status := strings.TrimSpace(event.Status)
		if status == "" {
			status = "running"
		}
		if index < 0 {
			conversation.snapshot.Activities = append(conversation.snapshot.Activities, NotebookAgentActivity{
				ID: activityID, TurnID: turnID, Kind: strings.TrimSpace(event.Name),
				Title: notebookAgentActivityTitle(event), Detail: strings.TrimSpace(event.Detail),
				Status: status, StartedAt: now,
			})
			index = len(conversation.snapshot.Activities) - 1
		} else {
			activity := &conversation.snapshot.Activities[index]
			if strings.TrimSpace(event.Name) != "" {
				activity.Kind = strings.TrimSpace(event.Name)
			}
			if title := notebookAgentActivityTitle(event); title != "" {
				activity.Title = title
			}
			if strings.TrimSpace(event.Detail) != "" {
				activity.Detail = strings.TrimSpace(event.Detail)
			}
			activity.Status = status
		}
		if status == "complete" || status == "error" || status == "cancelled" {
			conversation.snapshot.Activities[index].FinishedAt = now
		}
		conversation.snapshot.Activities = trimNotebookAgentActivities(conversation.snapshot.Activities)
	}
	conversation.snapshot.Revision++
	snapshot := cloneNotebookAgentSnapshot(conversation.snapshot)
	s.mu.Unlock()
	s.publish(snapshot)
}

func (s *NotebookAgentService) assistantMessageLocked(conversation *notebookAgentConversation, turnID, now string) *NotebookAgentMessage {
	for index := range conversation.snapshot.Messages {
		message := &conversation.snapshot.Messages[index]
		if message.TurnID == turnID && message.Role == "assistant" {
			return message
		}
	}
	conversation.snapshot.Messages = append(conversation.snapshot.Messages, NotebookAgentMessage{
		ID: s.deps.NewID(), TurnID: turnID, Role: "assistant", Status: "streaming", CreatedAt: now,
	})
	conversation.snapshot.Messages = trimNotebookAgentMessages(conversation.snapshot.Messages)
	return &conversation.snapshot.Messages[len(conversation.snapshot.Messages)-1]
}

func (s *NotebookAgentService) finishAssistantMessageLocked(conversation *notebookAgentConversation, turnID, fallback, status, now string) {
	message := s.assistantMessageLocked(conversation, turnID, now)
	if strings.TrimSpace(message.Content) == "" {
		message.Content = fallback
	}
	message.Status = status
}

func (s *NotebookAgentService) validateNotebook(notebookID string) *APIError {
	if notebookID == "" {
		return badRequestError("notebook_id_required", "notebook id is required")
	}
	if s.deps.ValidateNotebook != nil {
		return s.deps.ValidateNotebook(notebookID)
	}
	return nil
}

func (s *NotebookAgentService) resolveReferences(
	notebookID string,
	references []NotebookAgentReferenceRequest,
) ([]NotebookAgentReference, *APIError) {
	if len(references) == 0 {
		return nil, nil
	}
	if s.deps.ResolveReferences == nil {
		return nil, badRequestError(
			"notebook_agent_references_unavailable",
			"cell and asset references are unavailable for this notebook",
		)
	}
	return s.deps.ResolveReferences(notebookID, references)
}

func (s *NotebookAgentService) conversationLocked(notebookID string) *notebookAgentConversation {
	conversation := s.items[notebookID]
	if conversation == nil {
		conversation = newNotebookAgentConversation(notebookID)
		s.items[notebookID] = conversation
	}
	return conversation
}

func (s *NotebookAgentService) publish(snapshot NotebookAgentSnapshot) {
	if s.deps.PublishEvent != nil {
		s.deps.PublishEvent(snapshot)
	}
}

func newNotebookAgentConversation(notebookID string) *notebookAgentConversation {
	return &notebookAgentConversation{
		snapshot: NotebookAgentSnapshot{
			Type: notebookAgentEventType, NotebookID: notebookID, Status: "idle",
			Messages: []NotebookAgentMessage{}, Activities: []NotebookAgentActivity{},
		},
		sessions: make(map[string]string),
		runDirs:  make(map[string]string),
	}
}

func cloneNotebookAgentSnapshot(snapshot NotebookAgentSnapshot) NotebookAgentSnapshot {
	clone := snapshot
	clone.Messages = make([]NotebookAgentMessage, len(snapshot.Messages))
	copy(clone.Messages, snapshot.Messages)
	for index := range clone.Messages {
		clone.Messages[index].References = append(
			[]NotebookAgentReference(nil),
			snapshot.Messages[index].References...,
		)
	}
	clone.Activities = make([]NotebookAgentActivity, len(snapshot.Activities))
	copy(clone.Activities, snapshot.Activities)
	if snapshot.Interaction != nil {
		interaction := *snapshot.Interaction
		interaction.Questions = cloneNotebookAgentQuestions(snapshot.Interaction.Questions)
		interaction.Answers = append(
			[]NotebookAgentQuestionAnswer(nil),
			snapshot.Interaction.Answers...,
		)
		for index := range interaction.Answers {
			interaction.Answers[index].Values = append(
				[]string(nil),
				snapshot.Interaction.Answers[index].Values...,
			)
		}
		clone.Interaction = &interaction
	}
	return clone
}

func trimNotebookAgentMessages(messages []NotebookAgentMessage) []NotebookAgentMessage {
	if len(messages) <= maxNotebookAgentMessages {
		return messages
	}
	return append([]NotebookAgentMessage(nil), messages[len(messages)-maxNotebookAgentMessages:]...)
}

func trimNotebookAgentActivities(activities []NotebookAgentActivity) []NotebookAgentActivity {
	if len(activities) <= maxNotebookAgentActivities {
		return activities
	}
	return append([]NotebookAgentActivity(nil), activities[len(activities)-maxNotebookAgentActivities:]...)
}

func truncateNotebookAgentText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	if limit <= 1 {
		return value[:limit]
	}
	return value[:limit-1] + "…"
}

func findNotebookAgentProvider(providers []NotebookAgentProvider, id string) (NotebookAgentProvider, bool) {
	for _, provider := range providers {
		if provider.ID == id {
			return provider, true
		}
	}
	return NotebookAgentProvider{}, false
}

func notebookAgentRunKey(provider string, mode NotebookAgentMode) string {
	return provider + ":" + string(mode)
}

func buildNotebookAgentPrompt(notebookID string, mode NotebookAgentMode, messages []NotebookAgentMessage, resumed bool) string {
	capability := "Inspect the selected notebook and answer the user's question. You may search the credential-free workspace catalog when broader metadata context is relevant. Use ask_user only for genuinely missing user intent after inspection. Do not prepare or apply changes and do not run cells."
	if mode == NotebookAgentModeEdit {
		capability = "Complete the requested notebook task end to end. Inspect first and search the workspace catalog when the task needs existing data. Use ask_user only for genuinely missing user intent after inspection, never as a substitute for reading the notebook or catalog. When choosing among catalog sources, compare their descriptions, tags, direct lineage, and declared materialization policy: prefer retained append, merge, or replay-safe window history for historical analysis, and do not mistake a truncate-and-replace shortlist or current view for history. Prepare and validate semantic changes, then apply them. Follow the prepare tool's dotted operation-kind enum exactly (for example visualization.create or cell.sql.refactor); never probe guessed operation names or batch speculative retries. Prefer cell.sql.refactor over replacing a whole SQL cell when the requested change is a supported relation rename, column qualification, or relation alias edit; it preserves every untouched source byte. For visualizations, follow the typed prepare-tool schema exactly: this is Renart's definition grammar, not Vega, and uses version 1, encoding (singular), and array-valued y encodings. A catalog match may include a suggested sample source recipe: use that recipe through cell.create, and do not widen it to a full snapshot unless the user explicitly asks. A newly added non-DuckDB source can be configured by the agent, but its first import or explicit refresh must be reviewed and run by the user in Renart. If a tool fails, use its returned valid values before one corrected retry. Run only the cells needed to verify the result, and report what changed or what awaits source approval."
	}
	var history strings.Builder
	start := 0
	if resumed && len(messages) > 0 {
		start = len(messages) - 1
	} else if len(messages) > 12 {
		start = len(messages) - 12
	}
	for _, message := range messages[start:] {
		if strings.TrimSpace(message.Content) == "" {
			continue
		}
		fmt.Fprintf(&history, "%s: %s\n", strings.ToUpper(message.Role), truncateNotebookAgentText(message.Content, 16<<10))
		if len(message.References) > 0 {
			history.WriteString("REFERENCED CONTEXT (resolve these exact targets with Renart tools before acting):\n")
			for _, reference := range message.References {
				fmt.Fprintf(
					&history,
					"- %s %q (id=%q%s)\n",
					reference.Kind,
					reference.Label,
					reference.ID,
					notebookAgentReferenceDetail(reference.Detail),
				)
			}
		}
		history.WriteString("\n")
	}
	contextLabel := "Conversation"
	if resumed {
		contextLabel = "Current request for the resumed session"
	}
	return fmt.Sprintf(`You are Renart's notebook assistant, working on the single notebook with opaque ID %q.

Use only the Renart MCP tools for notebook context, edits, and execution. Do not use shell, filesystem, Git, web, or generic coding tools. Never guess workspace paths or credentials. Treat tool results as authoritative and use durable block IDs. Do not reveal hidden reasoning; expose only concise progress through tool calls and give a clear final response.

Capability for this turn: %s

%s (the final USER entry is the current request):
%s`, notebookID, capability, contextLabel, history.String())
}

func notebookAgentReferenceDetail(detail string) string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return ""
	}
	return ", " + detail
}

func notebookAgentActivityTitle(event NotebookAgentStreamEvent) string {
	if title := strings.TrimSpace(event.Title); title != "" {
		return title
	}
	name := strings.TrimPrefix(strings.TrimSpace(event.Name), "renart_")
	name = strings.TrimPrefix(name, "mcp__renart__")
	switch name {
	case "search_workspace_catalog":
		return "Searching the workspace catalog"
	case "list_notebooks":
		return "Finding the notebook"
	case "get_notebook_outline":
		return "Reading the notebook outline"
	case "get_notebook_block":
		return "Reading a notebook block"
	case "get_notebook_graph":
		return "Tracing notebook dependencies"
	case "get_notebook_diagnostics":
		return "Checking notebook diagnostics"
	case "get_notebook_result_schema":
		return "Reading a result schema"
	case "get_notebook_result_sample":
		return "Inspecting result rows"
	case "list_notebook_sources":
		return "Inspecting notebook sources"
	case "ask_user":
		return "Waiting for your answer"
	case "prepare_notebook_change_set":
		return "Preparing notebook changes"
	case "validate_notebook_change_set":
		return "Validating notebook changes"
	case "apply_notebook_change_set":
		return "Applying notebook changes"
	case "discard_notebook_change_set":
		return "Discarding prepared changes"
	case "run_notebook_cells":
		return "Running notebook cells"
	case "get_notebook_run_status":
		return "Checking the notebook run"
	case "cancel_notebook_run":
		return "Stopping the notebook run"
	case "reasoning", "thinking":
		return "Thinking"
	case "command_execution":
		return "Using an isolated agent command"
	case "file_change":
		return "Updating isolated agent state"
	case "plan":
		return "Planning the notebook task"
	case "agent_policy":
		return "Blocked an unsupported agent tool"
	case "agent_retry_limit":
		return "Stopped repeated invalid changes"
	case "":
		return "Working"
	default:
		return strings.ReplaceAll(name, "_", " ")
	}
}

func defaultNotebookAgentLookPath(file string) (string, error) {
	envName := "RENART_" + strings.ToUpper(strings.ReplaceAll(file, "-", "_")) + "_BINARY"
	if configured := strings.TrimSpace(os.Getenv(envName)); configured != "" {
		if filepath.IsAbs(configured) {
			if info, err := os.Stat(configured); err == nil && !info.IsDir() {
				return configured, nil
			}
			return "", fmt.Errorf("configured %s does not name an executable file", envName)
		}
		return execLookPath(configured)
	}
	return execLookPath(file)
}
