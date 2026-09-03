package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNotebookAgentServiceStreamsAndResumesScopedTurns(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	requests := []NotebookAgentProviderRunRequest{}
	published := []NotebookAgentSnapshot{}
	id := 0
	service := NewNotebookAgentService(context.Background(), NotebookAgentDependencies{
		WorkspaceRoot:    "/workspace",
		RenartExecutable: "/usr/bin/renart",
		ValidateNotebook: func(notebookID string) *APIError {
			if notebookID != "notebook-one" {
				return &APIError{Status: 404, Code: "notebook_not_found", Message: "missing"}
			}
			return nil
		},
		ResolveReferences: func(notebookID string, references []NotebookAgentReferenceRequest) ([]NotebookAgentReference, *APIError) {
			if notebookID != "notebook-one" || len(references) != 1 || references[0].ID != "cell-one" {
				t.Fatalf("unexpected reference request: %q %+v", notebookID, references)
			}
			return []NotebookAgentReference{{
				Kind: "cell", ID: "cell-one", Label: "daily_sales", Detail: "duckdb.sql",
			}}, nil
		},
		LookPath: func(file string) (string, error) {
			if file == "codex" {
				return "/usr/bin/codex", nil
			}
			return "", errors.New("missing")
		},
		NewID: func() string {
			mu.Lock()
			defer mu.Unlock()
			id++
			return "id-" + string(rune('a'+id))
		},
		Now: func() time.Time { return time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC) },
		PublishEvent: func(payload any) {
			mu.Lock()
			defer mu.Unlock()
			published = append(published, payload.(NotebookAgentSnapshot))
		},
		RunProvider: func(_ context.Context, request NotebookAgentProviderRunRequest, emit func(NotebookAgentStreamEvent)) (NotebookAgentProviderRunResult, error) {
			mu.Lock()
			requests = append(requests, request)
			call := len(requests)
			mu.Unlock()
			emit(NotebookAgentStreamEvent{Kind: "activity", ID: "tool-1", Name: "mcp__renart__get_notebook_outline", Status: "running"})
			emit(NotebookAgentStreamEvent{Kind: "activity", ID: "tool-1", Name: "mcp__renart__get_notebook_outline", Status: "complete"})
			emit(NotebookAgentStreamEvent{Kind: "text", Text: "Notebook looks good."})
			if call == 1 {
				return NotebookAgentProviderRunResult{SessionID: "session-one"}, nil
			}
			return NotebookAgentProviderRunResult{SessionID: request.SessionID}, nil
		},
	})
	t.Cleanup(service.Close)

	started, apiErr := service.StartTurn("notebook-one", StartNotebookAgentTurnRequest{
		Provider: "codex", Mode: NotebookAgentModeAsk, Message: "Explain this notebook",
		References: []NotebookAgentReferenceRequest{{Kind: "cell", ID: "cell-one"}},
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if started.Status != "running" || len(started.Messages) != 1 || started.Messages[0].Role != "user" {
		t.Fatalf("unexpected starting state: %+v", started)
	}
	if references := started.Messages[0].References; len(references) != 1 || references[0].Label != "daily_sales" {
		t.Fatalf("resolved references were not retained: %+v", references)
	}

	first := waitForNotebookAgentState(t, service, "notebook-one", func(snapshot NotebookAgentSnapshot) bool {
		return snapshot.Status == "idle"
	})
	if got := first.Messages[len(first.Messages)-1].Content; got != "Notebook looks good." {
		t.Fatalf("assistant message = %q", got)
	}
	if len(first.Activities) != 1 || first.Activities[0].Title != "Reading the notebook outline" || first.Activities[0].Status != "complete" {
		t.Fatalf("unexpected activities: %+v", first.Activities)
	}

	if _, apiErr := service.StartTurn("notebook-one", StartNotebookAgentTurnRequest{
		Provider: "codex", Mode: NotebookAgentModeAsk, Message: "Anything else?",
	}); apiErr != nil {
		t.Fatal(apiErr)
	}
	waitForNotebookAgentState(t, service, "notebook-one", func(snapshot NotebookAgentSnapshot) bool {
		return snapshot.Status == "idle" && len(snapshot.Messages) == 4
	})

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("provider requests = %d", len(requests))
	}
	if requests[0].ProviderBinary != "/usr/bin/codex" || requests[0].SessionID != "" {
		t.Fatalf("unexpected first provider request: %+v", requests[0])
	}
	if requests[1].SessionID != "session-one" {
		t.Fatalf("second turn did not resume session: %+v", requests[1])
	}
	if !strings.Contains(requests[1].Prompt, "Anything else?") ||
		strings.Contains(requests[1].Prompt, "Explain this notebook") {
		t.Fatalf("resumed prompt duplicated transcript history: %s", requests[1].Prompt)
	}
	if !strings.Contains(requests[0].Prompt, "single notebook with opaque ID \"notebook-one\"") ||
		!strings.Contains(requests[0].Prompt, "Do not prepare or apply changes") ||
		!strings.Contains(requests[0].Prompt, `cell "daily_sales" (id="cell-one", duckdb.sql)`) {
		t.Fatalf("prompt is not scoped to Ask mode: %s", requests[0].Prompt)
	}
	if len(published) < 5 || published[len(published)-1].Status != "idle" {
		t.Fatalf("missing final snapshots: %+v", published)
	}
}

func TestNotebookAgentServiceCancelsProcessAndResetsChat(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	service := NewNotebookAgentService(context.Background(), NotebookAgentDependencies{
		WorkspaceRoot:    "/workspace",
		RenartExecutable: "/usr/bin/renart",
		LookPath:         func(string) (string, error) { return "/usr/bin/codex", nil },
		RunProvider: func(ctx context.Context, _ NotebookAgentProviderRunRequest, _ func(NotebookAgentStreamEvent)) (NotebookAgentProviderRunResult, error) {
			close(started)
			<-ctx.Done()
			return NotebookAgentProviderRunResult{}, ctx.Err()
		},
	})
	t.Cleanup(service.Close)

	if _, apiErr := service.StartTurn("notebook-one", StartNotebookAgentTurnRequest{
		Provider: "codex", Mode: NotebookAgentModeEdit, Message: "Add a chart",
	}); apiErr != nil {
		t.Fatal(apiErr)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not start")
	}
	cancelling, apiErr := service.Cancel("notebook-one")
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if cancelling.Status != "cancelling" {
		t.Fatalf("cancel status = %q", cancelling.Status)
	}
	cancelled := waitForNotebookAgentState(t, service, "notebook-one", func(snapshot NotebookAgentSnapshot) bool {
		return snapshot.Status == "cancelled"
	})
	if cancelled.Messages[len(cancelled.Messages)-1].Status != "cancelled" {
		t.Fatalf("assistant cancellation not recorded: %+v", cancelled.Messages)
	}
	reset, apiErr := service.Reset("notebook-one")
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if reset.Status != "idle" || len(reset.Messages) != 0 || len(reset.Activities) != 0 {
		t.Fatalf("unexpected reset snapshot: %+v", reset)
	}
}

func TestNotebookAgentQuestionnaireBlocksAndResumesNativeTurn(t *testing.T) {
	t.Parallel()

	var agent *NotebookAgentService
	providerResult := make(chan NotebookAgentInteractionResult, 1)
	agent = NewNotebookAgentService(context.Background(), NotebookAgentDependencies{
		LookPath: func(file string) (string, error) {
			if file == "codex" {
				return "/usr/bin/codex", nil
			}
			return "", errors.New("missing")
		},
		RunProvider: func(ctx context.Context, request NotebookAgentProviderRunRequest, emit func(NotebookAgentStreamEvent)) (NotebookAgentProviderRunResult, error) {
			if request.TurnToken == "" {
				return NotebookAgentProviderRunResult{}, errors.New("missing turn token")
			}
			result, apiErr := agent.RequestQuestionnaire(
				ctx,
				request.NotebookID,
				request.TurnToken,
				NotebookAgentQuestionnaireRequest{
					Title: "Choose a metric",
					Questions: []NotebookAgentQuestion{{
						ID: "metric", Kind: NotebookAgentQuestionSingle,
						Prompt: "Which metric should the chart use?", Required: true,
						Options: []NotebookAgentQuestionOption{
							{Value: "revenue", Label: "Revenue", Recommended: true},
							{Value: "orders", Label: "Orders"},
						},
					}},
				},
			)
			if apiErr != nil {
				return NotebookAgentProviderRunResult{}, apiErr
			}
			providerResult <- result
			emit(NotebookAgentStreamEvent{Kind: "text", Text: "Using the selected metric."})
			return NotebookAgentProviderRunResult{}, nil
		},
	})
	t.Cleanup(agent.Close)

	if _, apiErr := agent.StartTurn("notebook-one", StartNotebookAgentTurnRequest{
		Provider: "codex", Mode: NotebookAgentModeEdit, Message: "Add a useful chart",
	}); apiErr != nil {
		t.Fatal(apiErr)
	}
	pending := waitForNotebookAgentState(t, agent, "notebook-one", func(snapshot NotebookAgentSnapshot) bool {
		return snapshot.Interaction != nil && snapshot.Interaction.Status == "pending"
	})
	if pending.Interaction.Title != "Choose a metric" || len(pending.Interaction.Questions) != 1 {
		t.Fatalf("unexpected pending interaction: %+v", pending.Interaction)
	}
	if _, apiErr := agent.RequestQuestionnaire(
		context.Background(),
		"notebook-one",
		"wrong-token",
		NotebookAgentQuestionnaireRequest{Title: "No", Questions: []NotebookAgentQuestion{{ID: "q", Kind: NotebookAgentQuestionText, Prompt: "No"}}},
	); apiErr == nil || apiErr.Code != "notebook_agent_turn_token_invalid" {
		t.Fatalf("invalid token error = %+v", apiErr)
	}
	if _, apiErr := agent.AnswerInteraction(
		"notebook-one",
		pending.Interaction.ID,
		AnswerNotebookAgentInteractionRequest{Answers: []NotebookAgentQuestionAnswer{{
			QuestionID: "metric", Values: []string{"missing"},
		}}},
	); apiErr == nil || apiErr.Code != "notebook_agent_interaction_answer_invalid" {
		t.Fatalf("invalid answer error = %+v", apiErr)
	}
	answered, apiErr := agent.AnswerInteraction(
		"notebook-one",
		pending.Interaction.ID,
		AnswerNotebookAgentInteractionRequest{Answers: []NotebookAgentQuestionAnswer{{
			QuestionID: "metric", Values: []string{"revenue"},
		}}},
	)
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if answered.Interaction == nil || answered.Interaction.Status != "answered" {
		t.Fatalf("answer was not recorded: %+v", answered.Interaction)
	}

	select {
	case result := <-providerResult:
		if result.Status != "answered" || len(result.Answers) != 1 || result.Answers[0].Values[0] != "revenue" {
			t.Fatalf("provider received unexpected result: %+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not resume after the answer")
	}
	completed := waitForNotebookAgentState(t, agent, "notebook-one", func(snapshot NotebookAgentSnapshot) bool {
		return snapshot.Status == "idle"
	})
	if completed.Interaction == nil || completed.Interaction.Status != "answered" {
		t.Fatalf("completed snapshot lost the interaction: %+v", completed.Interaction)
	}
}

func TestNotebookAgentEditPromptForbidsOperationNameProbing(t *testing.T) {
	prompt := buildNotebookAgentPrompt("notebook-one", NotebookAgentModeEdit, []NotebookAgentMessage{{
		Role: "user", Content: "Add a line chart",
	}}, false)
	for _, required := range []string{
		"dotted operation-kind enum",
		"visualization.create",
		"never probe guessed operation names",
		"search the workspace catalog",
		"direct lineage",
		"truncate-and-replace",
		"first import or explicit refresh must be reviewed",
		"one corrected retry",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("edit prompt is missing %q: %s", required, prompt)
		}
	}
}

func TestNotebookAgentDetectsFailedChangePreparation(t *testing.T) {
	for _, event := range []NotebookAgentStreamEvent{
		{Kind: "activity", Name: "renart_prepare_notebook_change_set", Status: "error"},
		{Kind: "activity", Name: "mcp__renart__prepare_notebook_change_set", Status: "error"},
	} {
		if !isFailedNotebookChangePreparation(event) {
			t.Fatalf("failed preparation was not detected: %+v", event)
		}
	}
	for _, event := range []NotebookAgentStreamEvent{
		{Kind: "activity", Name: "renart_prepare_notebook_change_set", Status: "complete"},
		{Kind: "activity", Name: "renart_get_notebook_outline", Status: "error"},
		{Kind: "text", Name: "renart_prepare_notebook_change_set", Status: "error"},
	} {
		if isFailedNotebookChangePreparation(event) {
			t.Fatalf("unrelated event was treated as a failed preparation: %+v", event)
		}
	}
	if !isNotebookChangePreparation(NotebookAgentStreamEvent{
		Kind: "activity", Name: "renart_prepare_notebook_change_set", Status: "complete",
	}) {
		t.Fatal("successful preparation was not recognized for retry-counter reset")
	}
}

func TestNotebookAgentProviderCommandsOnlyExposeScopedMCP(t *testing.T) {
	t.Parallel()

	runDir := t.TempDir()
	base := NotebookAgentProviderRunRequest{
		Mode: NotebookAgentModeAsk, NotebookID: "notebook-one", RunDir: runDir,
		WorkspaceRoot: "/workspace", RenartExecutable: "/usr/bin/renart",
		TurnToken: "opaque-turn-token",
	}

	codex := base
	codex.Provider = "codex"
	command, err := notebookAgentCommand(context.Background(), "/usr/bin/codex", codex)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(command.Args, " ")
	for _, expected := range []string{
		"--ignore-user-config", `sandbox_mode="read-only"`, "--notebook", "--read-only", "--no-runs",
		`mcp_servers.renart.env_vars=["RENART_NOTEBOOK_AGENT_TURN_TOKEN"]`,
	} {
		if !strings.Contains(joined, expected) {
			t.Errorf("Codex command misses %q: %s", expected, joined)
		}
	}
	if strings.Contains(joined, base.TurnToken) {
		t.Fatalf("opaque turn token leaked into Codex argv: %s", joined)
	}
	if strings.Contains(joined, "current request") {
		t.Fatalf("prompt leaked into Codex argv: %s", joined)
	}

	claude := base
	claude.Provider = "claude"
	command, err = notebookAgentCommand(context.Background(), "/usr/bin/claude", claude)
	if err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(command.Args, " ")
	for _, expected := range []string{"--strict-mcp-config", "--tools  --allowedTools", "mcp__renart__search_workspace_catalog", "mcp__renart__get_notebook_outline", "--permission-mode dontAsk"} {
		if !strings.Contains(joined, expected) {
			t.Errorf("Claude command misses %q: %s", expected, joined)
		}
	}
	assertPrivateMCPConfig(t, filepath.Join(runDir, "claude-mcp.json"), "mcpServers")

	openCode := base
	openCode.Provider = "opencode"
	command, err = notebookAgentCommand(context.Background(), "/usr/bin/opencode", openCode)
	if err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(command.Args, " ")
	if !strings.Contains(joined, "--pure run --format json") {
		t.Fatalf("unexpected OpenCode command: %s", joined)
	}
	configPath := filepath.Join(runDir, "opencode-config", "opencode.json")
	assertPrivateMCPConfig(t, configPath, "mcp")
	encoded, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"bash": false`) || !strings.Contains(string(encoded), `"renart_*": true`) {
		t.Fatalf("OpenCode built-ins were not disabled: %s", encoded)
	}
}

func TestNotebookAgentProviderBoundaryRejectsGenericToolsAndHidesWorkspaceContext(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"command_execution", "file_change", "web_search", "bash", "Read"} {
		if !unsupportedNotebookAgentEvent(NotebookAgentStreamEvent{Kind: "activity", Name: name}) {
			t.Errorf("generic tool %q was not rejected", name)
		}
	}
	for _, name := range []string{"reasoning", "plan", "mcp__renart__get_notebook_outline", "renart_run_notebook_cells"} {
		if unsupportedNotebookAgentEvent(NotebookAgentStreamEvent{Kind: "activity", Name: name}) {
			t.Errorf("notebook activity %q was rejected", name)
		}
	}

	environment := isolatedNotebookAgentEnvironment([]string{
		"PATH=/usr/bin", "HOME=/home/test", "PWD=/workspace", "OLDPWD=/workspace-parent",
		"GIT_DIR=/workspace/.git", "GIT_WORK_TREE=/workspace", "OPENAI_API_KEY=secret",
	}, "/tmp/agent")
	joined := strings.Join(environment, "\n")
	for _, hidden := range []string{"PWD=/workspace", "OLDPWD=", "GIT_DIR=", "GIT_WORK_TREE="} {
		if strings.Contains(joined, hidden) {
			t.Errorf("isolated environment retained %q: %s", hidden, joined)
		}
	}
	for _, kept := range []string{"PWD=/tmp/agent", "PATH=/usr/bin", "HOME=/home/test", "OPENAI_API_KEY=secret"} {
		if !strings.Contains(joined, kept) {
			t.Errorf("isolated environment misses %q: %s", kept, joined)
		}
	}
}

func TestNotebookAgentStreamParsersNormalizeProviderEvents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		provider   string
		lines      []string
		sessionID  string
		wantText   string
		wantTool   string
		wantStatus string
	}{
		{
			name: "codex", provider: "codex", sessionID: "thread-1", wantText: "Finished", wantTool: "renart_get_notebook_outline", wantStatus: "complete",
			lines: []string{
				`{"type":"thread.started","thread_id":"thread-1"}`,
				`{"type":"item.started","item":{"id":"call-1","type":"mcp_tool_call","server":"renart","tool":"get_notebook_outline"}}`,
				`{"type":"item.completed","item":{"id":"call-1","type":"mcp_tool_call","server":"renart","tool":"get_notebook_outline","status":"completed"}}`,
				`{"type":"item.completed","item":{"id":"message-1","type":"agent_message","text":"Finished"}}`,
			},
		},
		{
			name: "claude", provider: "claude", sessionID: "session-1", wantText: "Finished", wantTool: "mcp__renart__get_notebook_outline", wantStatus: "complete",
			lines: []string{
				`{"type":"system","subtype":"init","session_id":"session-1"}`,
				`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call-1","name":"mcp__renart__get_notebook_outline"}]}}`,
				`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call-1"}]}}`,
				`{"type":"assistant","message":{"content":[{"type":"text","text":"Finished"}]}}`,
				`{"type":"result","subtype":"success","result":"Finished","session_id":"session-1"}`,
			},
		},
		{
			name: "opencode", provider: "opencode", sessionID: "session-1", wantText: "Finished", wantTool: "renart_get_notebook_outline", wantStatus: "complete",
			lines: []string{
				`{"type":"step_start","sessionID":"session-1"}`,
				`{"type":"tool_use","sessionID":"session-1","part":{"callID":"call-1","tool":"renart_get_notebook_outline","state":{"status":"completed"}}}`,
				`{"type":"text","sessionID":"session-1","part":{"text":"Finished"}}`,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parser := &notebookAgentStreamParser{provider: test.provider, toolNames: map[string]string{}}
			events := []NotebookAgentStreamEvent{}
			for _, line := range test.lines {
				events = append(events, parser.parse([]byte(line))...)
			}
			if parser.sessionID != test.sessionID {
				t.Fatalf("session id = %q", parser.sessionID)
			}
			var text string
			var tool NotebookAgentStreamEvent
			for _, event := range events {
				if event.Kind == "text" {
					text += event.Text
				}
				if event.Kind == "activity" && event.Name == test.wantTool {
					tool = event
				}
			}
			if text != test.wantText {
				t.Fatalf("text = %q, want %q", text, test.wantText)
			}
			if tool.Status != test.wantStatus {
				t.Fatalf("tool event = %+v", tool)
			}
		})
	}
}

func waitForNotebookAgentState(t *testing.T, service *NotebookAgentService, notebookID string, ready func(NotebookAgentSnapshot) bool) NotebookAgentSnapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state, apiErr := service.State(notebookID)
		if apiErr != nil {
			t.Fatal(apiErr)
		}
		if ready(state.Conversation) {
			return state.Conversation
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for notebook agent state")
	return NotebookAgentSnapshot{}
}

func assertPrivateMCPConfig(t *testing.T, path, rootKey string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("config permissions = %o", info.Mode().Perm())
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(encoded, &config); err != nil {
		t.Fatal(err)
	}
	if config[rootKey] == nil {
		t.Fatalf("config misses %q: %s", rootKey, encoded)
	}
}
