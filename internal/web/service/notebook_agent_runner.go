package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

const (
	maxNotebookAgentEventBytes      = 4 << 20
	maxNotebookAgentPrepareFailures = 6
)

type notebookAgentStreamParser struct {
	provider         string
	sessionID        string
	toolNames        map[string]string
	sawAssistantText bool
}

func runLocalNotebookAgentProvider(
	ctx context.Context,
	request NotebookAgentProviderRunRequest,
	emit func(NotebookAgentStreamEvent),
) (NotebookAgentProviderRunResult, error) {
	binary := strings.TrimSpace(request.ProviderBinary)
	if binary == "" {
		var err error
		binary, err = defaultNotebookAgentLookPath(request.Provider)
		if err != nil {
			return NotebookAgentProviderRunResult{}, fmt.Errorf("%s is not available: %w", request.Provider, err)
		}
	}
	if strings.TrimSpace(request.RenartExecutable) == "" {
		return NotebookAgentProviderRunResult{}, errors.New("Renart could not resolve its executable for the notebook MCP server")
	}
	if err := os.MkdirAll(request.RunDir, 0o700); err != nil {
		return NotebookAgentProviderRunResult{}, fmt.Errorf("prepare isolated agent directory: %w", err)
	}

	providerCtx, stopProvider := context.WithCancel(ctx)
	defer stopProvider()
	command, err := notebookAgentCommand(providerCtx, binary, request)
	if err != nil {
		return NotebookAgentProviderRunResult{}, err
	}
	command.Dir = request.RunDir
	command.Env = isolatedNotebookAgentEnvironment(command.Env, request.RunDir)
	if strings.TrimSpace(request.TurnToken) != "" {
		command.Env = append(
			command.Env,
			"RENART_NOTEBOOK_AGENT_TURN_TOKEN="+strings.TrimSpace(request.TurnToken),
		)
	}
	command.Stdin = strings.NewReader(request.Prompt)
	configureCommandProcessTree(command)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return NotebookAgentProviderRunResult{}, fmt.Errorf("open agent output: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return NotebookAgentProviderRunResult{}, fmt.Errorf("open agent diagnostics: %w", err)
	}
	if err := command.Start(); err != nil {
		return NotebookAgentProviderRunResult{}, fmt.Errorf("start %s: %w", request.Provider, err)
	}

	var stderrBuffer boundedAgentBuffer
	var stderrWG sync.WaitGroup
	stderrWG.Add(1)
	go func() {
		defer stderrWG.Done()
		_, _ = io.Copy(&stderrBuffer, stderr)
	}()

	parser := &notebookAgentStreamParser{provider: request.Provider, toolNames: make(map[string]string)}
	var policyErr error
	prepareFailures := 0
	scanErr := scanNotebookAgentEvents(stdout, parser, func(event NotebookAgentStreamEvent) {
		if unsupportedNotebookAgentEvent(event) {
			if policyErr == nil {
				policyErr = fmt.Errorf("%s attempted to use unsupported tool %q; notebook chat only allows Renart MCP tools", providerLabel(request.Provider), event.Name)
				emit(NotebookAgentStreamEvent{
					Kind: "activity", ID: "agent-policy", Name: "agent_policy",
					Title: "Blocked an unsupported agent tool", Detail: event.Name, Status: "error",
				})
				stopProvider()
			}
			return
		}
		if isNotebookChangePreparation(event) {
			if event.Status == "complete" {
				prepareFailures = 0
			} else if event.Status == "error" {
				prepareFailures++
			}
			if event.Status == "error" && prepareFailures >= maxNotebookAgentPrepareFailures && policyErr == nil {
				policyErr = fmt.Errorf(
					"%s repeatedly submitted invalid notebook changes; start a new turn and follow the prepare tool's exact dotted operation-kind enum",
					providerLabel(request.Provider),
				)
				emit(NotebookAgentStreamEvent{
					Kind: "activity", ID: "agent-retry-limit", Name: "agent_retry_limit",
					Title: "Stopped repeated invalid changes",
					Detail: fmt.Sprintf(
						"The agent made %d failed change preparations. The tool schema lists the valid operation kinds.",
						prepareFailures,
					),
					Status: "error",
				})
				stopProvider()
				return
			}
		}
		emit(event)
	})
	waitErr := command.Wait()
	stderrWG.Wait()
	if policyErr != nil {
		return NotebookAgentProviderRunResult{SessionID: parser.sessionID}, policyErr
	}
	if scanErr != nil && waitErr == nil {
		waitErr = scanErr
	}
	if waitErr != nil {
		if ctx.Err() != nil {
			return NotebookAgentProviderRunResult{SessionID: parser.sessionID}, ctx.Err()
		}
		detail := redactNotebookAgentDiagnostic(stderrBuffer.String(), request)
		if detail == "" {
			detail = waitErr.Error()
		}
		return NotebookAgentProviderRunResult{SessionID: parser.sessionID}, fmt.Errorf("%s agent failed: %s", providerLabel(request.Provider), detail)
	}
	return NotebookAgentProviderRunResult{SessionID: parser.sessionID}, nil
}

func isFailedNotebookChangePreparation(event NotebookAgentStreamEvent) bool {
	return isNotebookChangePreparation(event) && event.Status == "error"
}

func isNotebookChangePreparation(event NotebookAgentStreamEvent) bool {
	if event.Kind != "activity" {
		return false
	}
	name := strings.TrimPrefix(strings.TrimSpace(event.Name), "renart_")
	name = strings.TrimPrefix(name, "mcp__renart__")
	return name == "prepare_notebook_change_set"
}

func notebookAgentCommand(ctx context.Context, binary string, request NotebookAgentProviderRunRequest) (*exec.Cmd, error) {
	mcpArguments := []string{
		"mcp", "--workspace", request.WorkspaceRoot, "--notebook", request.NotebookID,
	}
	if request.Mode == NotebookAgentModeAsk {
		mcpArguments = append(mcpArguments, "--read-only", "--no-runs")
	}
	switch request.Provider {
	case "codex":
		return codexNotebookAgentCommand(ctx, binary, request, mcpArguments), nil
	case "claude":
		return claudeNotebookAgentCommand(ctx, binary, request, mcpArguments)
	case "opencode":
		return openCodeNotebookAgentCommand(ctx, binary, request, mcpArguments)
	default:
		return nil, fmt.Errorf("unsupported notebook agent provider %q", request.Provider)
	}
}

func codexNotebookAgentCommand(ctx context.Context, binary string, request NotebookAgentProviderRunRequest, mcpArguments []string) *exec.Cmd {
	mcpArgsJSON, _ := json.Marshal(mcpArguments)
	common := []string{
		"--json",
		"--ignore-user-config",
		"--ignore-rules",
		"--skip-git-repo-check",
		"-c", `approval_policy="never"`,
		"-c", `sandbox_mode="read-only"`,
		"-c", "mcp_servers.renart.command=" + jsonString(request.RenartExecutable),
		"-c", "mcp_servers.renart.args=" + string(mcpArgsJSON),
		"-c", `mcp_servers.renart.default_tools_approval_mode="approve"`,
		"-c", `mcp_servers.renart.tool_timeout_sec=1800`,
	}
	args := []string{"exec"}
	if strings.TrimSpace(request.SessionID) == "" {
		args = append(args, common...)
		args = append(args, "-")
	} else {
		args = append(args, "resume")
		args = append(args, common...)
		args = append(args, strings.TrimSpace(request.SessionID), "-")
	}
	return exec.CommandContext(ctx, binary, args...) //nolint:gosec -- resolved local user-selected CLI
}

func claudeNotebookAgentCommand(ctx context.Context, binary string, request NotebookAgentProviderRunRequest, mcpArguments []string) (*exec.Cmd, error) {
	configPath := filepath.Join(request.RunDir, "claude-mcp.json")
	config := map[string]any{
		"mcpServers": map[string]any{
			"renart": map[string]any{
				"command": request.RenartExecutable,
				"args":    mcpArguments,
			},
		},
	}
	if err := writePrivateJSON(configPath, config); err != nil {
		return nil, fmt.Errorf("write Claude MCP configuration: %w", err)
	}
	allowed := make([]string, 0, len(notebookAgentMCPToolNames(request.Mode, request.TurnToken != "")))
	for _, tool := range notebookAgentMCPToolNames(request.Mode, request.TurnToken != "") {
		allowed = append(allowed, "mcp__renart__"+tool)
	}
	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--verbose",
		"--strict-mcp-config",
		"--mcp-config", configPath,
		"--tools", "",
		"--allowedTools", strings.Join(allowed, ","),
		"--permission-mode", "dontAsk",
		"--disable-slash-commands",
		"--no-chrome",
	}
	if strings.TrimSpace(request.SessionID) != "" {
		args = append(args, "--resume", strings.TrimSpace(request.SessionID))
	}
	return exec.CommandContext(ctx, binary, args...), nil //nolint:gosec -- resolved local user-selected CLI
}

func openCodeNotebookAgentCommand(ctx context.Context, binary string, request NotebookAgentProviderRunRequest, mcpArguments []string) (*exec.Cmd, error) {
	configDir := filepath.Join(request.RunDir, "opencode-config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return nil, fmt.Errorf("prepare OpenCode configuration: %w", err)
	}
	configPath := filepath.Join(configDir, "opencode.json")
	tools := map[string]bool{
		"bash": false, "read": false, "write": false, "edit": false,
		"glob": false, "grep": false, "webfetch": false, "websearch": false,
		"task": false, "todowrite": false, "lsp": false, "skill": false,
		"renart_*": true,
	}
	config := map[string]any{
		"$schema":    "https://opencode.ai/config.json",
		"share":      "disabled",
		"snapshot":   false,
		"autoupdate": false,
		"mcp": map[string]any{
			"renart": map[string]any{
				"type": "local", "command": append([]string{request.RenartExecutable}, mcpArguments...),
				"enabled": true,
			},
		},
		"tools": tools,
		"permission": map[string]string{
			"*": "deny", "renart_*": "allow",
		},
	}
	if err := writePrivateJSON(configPath, config); err != nil {
		return nil, fmt.Errorf("write OpenCode configuration: %w", err)
	}
	args := []string{"--pure", "run", "--format", "json", "--dir", request.RunDir}
	if strings.TrimSpace(request.SessionID) != "" {
		args = append(args, "--session", strings.TrimSpace(request.SessionID))
	}
	command := exec.CommandContext(ctx, binary, args...) //nolint:gosec -- resolved local user-selected CLI
	command.Env = append(os.Environ(),
		"OPENCODE_CONFIG="+configPath,
		"OPENCODE_CONFIG_DIR="+configDir,
		"OPENCODE_DISABLE_DEFAULT_PLUGINS=true",
		"OPENCODE_DISABLE_CLAUDE_CODE=true",
		"OPENCODE_DISABLE_AUTOUPDATE=true",
		"OPENCODE_DISABLE_LSP_DOWNLOAD=true",
	)
	return command, nil
}

func notebookAgentMCPToolNames(mode NotebookAgentMode, native bool) []string {
	tools := []string{
		"search_workspace_catalog", "list_notebooks", "get_notebook_outline", "get_notebook_block",
		"get_notebook_graph", "get_notebook_diagnostics", "get_notebook_result_schema",
		"get_notebook_result_sample", "list_notebook_sources",
	}
	if native {
		tools = append(tools, "ask_user")
		if mode == NotebookAgentModeEdit {
			tools = append(tools,
				"request_connection_access", "list_query_connections",
				"discover_connection_catalog", "query_connection_sample",
			)
		}
	}
	if mode == NotebookAgentModeEdit {
		tools = append(tools,
			"prepare_notebook_change_set", "validate_notebook_change_set",
			"apply_notebook_change_set", "discard_notebook_change_set",
			"run_notebook_cells", "cancel_notebook_run", "get_notebook_run_status",
		)
	}
	return tools
}

func unsupportedNotebookAgentEvent(event NotebookAgentStreamEvent) bool {
	if event.Kind != "activity" {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(event.Name))
	name = strings.TrimPrefix(name, "functions.")
	name = strings.TrimPrefix(name, "tools.")
	switch name {
	case "bash", "shell", "command_execution", "file_change", "read", "write", "edit",
		"glob", "grep", "webfetch", "websearch", "web_search", "task":
		return true
	default:
		return false
	}
}

func isolatedNotebookAgentEnvironment(current []string, runDir string) []string {
	if len(current) == 0 {
		current = os.Environ()
	}
	result := make([]string, 0, len(current)+1)
	for _, entry := range current {
		key, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		switch strings.ToUpper(key) {
		case "PWD", "OLDPWD", "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE",
			"RENART_NOTEBOOK_AGENT_TURN_TOKEN":
			continue
		}
		result = append(result, entry)
	}
	return append(result, "PWD="+runDir)
}

func scanNotebookAgentEvents(reader io.Reader, parser *notebookAgentStreamParser, emit func(NotebookAgentStreamEvent)) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), maxNotebookAgentEventBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		for _, event := range parser.parse(line) {
			emit(event)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read agent event stream: %w", err)
	}
	return nil
}

func (p *notebookAgentStreamParser) parse(line []byte) []NotebookAgentStreamEvent {
	var payload map[string]any
	if err := json.Unmarshal(line, &payload); err != nil {
		return nil
	}
	switch p.provider {
	case "codex":
		return p.parseCodex(payload)
	case "claude":
		return p.parseClaude(payload)
	case "opencode":
		return p.parseOpenCode(payload)
	default:
		return nil
	}
}

func (p *notebookAgentStreamParser) parseCodex(payload map[string]any) []NotebookAgentStreamEvent {
	typeName := stringField(payload, "type")
	if typeName == "thread.started" {
		p.sessionID = firstString(payload, "thread_id", "threadId", "id")
		return nil
	}
	if typeName == "turn.started" {
		return []NotebookAgentStreamEvent{{Kind: "activity", ID: "turn", Name: "thinking", Status: "running"}}
	}
	if typeName == "turn.completed" {
		return []NotebookAgentStreamEvent{{Kind: "activity", ID: "turn", Name: "thinking", Status: "complete"}}
	}
	if typeName == "turn.failed" || typeName == "error" {
		return []NotebookAgentStreamEvent{{Kind: "activity", ID: "turn", Name: "thinking", Status: "error"}}
	}
	if !strings.HasPrefix(typeName, "item.") {
		return nil
	}
	item := mapField(payload, "item")
	itemType := stringField(item, "type")
	itemID := firstString(item, "id", "call_id")
	status := codexItemStatus(typeName, stringField(item, "status"))
	switch itemType {
	case "agent_message":
		text := firstString(item, "text", "content")
		if text == "" {
			return nil
		}
		return []NotebookAgentStreamEvent{{Kind: "text", Text: text, TextReplace: true}}
	case "reasoning":
		return []NotebookAgentStreamEvent{{Kind: "activity", ID: itemID, Name: "reasoning", Status: status}}
	case "mcp_tool_call":
		tool := firstString(item, "tool", "name")
		if server := firstString(item, "server"); server != "" && !strings.HasPrefix(tool, "mcp__") {
			tool = server + "_" + tool
		}
		return []NotebookAgentStreamEvent{{Kind: "activity", ID: itemID, Name: tool, Status: status}}
	case "command_execution", "file_change", "plan", "web_search":
		return []NotebookAgentStreamEvent{{Kind: "activity", ID: itemID, Name: itemType, Status: status}}
	default:
		return nil
	}
}

func (p *notebookAgentStreamParser) parseClaude(payload map[string]any) []NotebookAgentStreamEvent {
	typeName := stringField(payload, "type")
	if sessionID := firstString(payload, "session_id", "sessionId"); sessionID != "" {
		p.sessionID = sessionID
	}
	switch typeName {
	case "system":
		if stringField(payload, "subtype") == "init" {
			return []NotebookAgentStreamEvent{{Kind: "activity", ID: "turn", Name: "thinking", Status: "running"}}
		}
	case "assistant":
		message := mapField(payload, "message")
		events := p.parseClaudeContent(sliceField(message, "content"), false)
		for _, event := range events {
			if event.Kind == "text" && strings.TrimSpace(event.Text) != "" {
				p.sawAssistantText = true
			}
		}
		return events
	case "user":
		message := mapField(payload, "message")
		return p.parseClaudeContent(sliceField(message, "content"), true)
	case "result":
		status := "complete"
		if stringField(payload, "subtype") != "success" || boolField(payload, "is_error") {
			status = "error"
		}
		events := []NotebookAgentStreamEvent{{Kind: "activity", ID: "turn", Name: "thinking", Status: status}}
		if text := stringField(payload, "result"); text != "" && !p.sawAssistantText {
			events = append(events, NotebookAgentStreamEvent{Kind: "text", Text: text})
		}
		return events
	}
	return nil
}

func (p *notebookAgentStreamParser) parseClaudeContent(content []any, toolResults bool) []NotebookAgentStreamEvent {
	events := []NotebookAgentStreamEvent{}
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		switch stringField(block, "type") {
		case "text":
			if text := stringField(block, "text"); text != "" {
				events = append(events, NotebookAgentStreamEvent{Kind: "text", Text: text})
			}
		case "tool_use":
			id := stringField(block, "id")
			name := stringField(block, "name")
			p.toolNames[id] = name
			events = append(events, NotebookAgentStreamEvent{Kind: "activity", ID: id, Name: name, Status: "running"})
		case "tool_result":
			if !toolResults {
				continue
			}
			id := firstString(block, "tool_use_id", "id")
			status := "complete"
			if boolField(block, "is_error") {
				status = "error"
			}
			events = append(events, NotebookAgentStreamEvent{Kind: "activity", ID: id, Name: p.toolNames[id], Status: status})
		}
	}
	return events
}

func (p *notebookAgentStreamParser) parseOpenCode(payload map[string]any) []NotebookAgentStreamEvent {
	if sessionID := firstString(payload, "sessionID", "session_id", "sessionId"); sessionID != "" {
		p.sessionID = sessionID
	}
	typeName := stringField(payload, "type")
	part := mapField(payload, "part")
	switch typeName {
	case "step_start":
		return []NotebookAgentStreamEvent{{Kind: "activity", ID: "turn", Name: "thinking", Status: "running"}}
	case "step_finish":
		return []NotebookAgentStreamEvent{{Kind: "activity", ID: "turn", Name: "thinking", Status: "complete"}}
	case "text":
		text := firstString(part, "text")
		if text == "" {
			text = stringField(payload, "text")
		}
		if text != "" {
			return []NotebookAgentStreamEvent{{Kind: "text", Text: text}}
		}
	case "tool_use":
		id := firstString(part, "callID", "call_id", "id")
		name := firstString(part, "tool", "name")
		state := mapField(part, "state")
		status := normalizeAgentActivityStatus(stringField(state, "status"))
		if status == "" {
			status = "running"
		}
		return []NotebookAgentStreamEvent{{Kind: "activity", ID: id, Name: name, Status: status}}
	case "error":
		return []NotebookAgentStreamEvent{{Kind: "activity", ID: "turn", Name: "thinking", Status: "error"}}
	}
	return nil
}

func codexItemStatus(eventType, status string) string {
	if normalized := normalizeAgentActivityStatus(status); normalized != "" {
		return normalized
	}
	switch eventType {
	case "item.completed":
		return "complete"
	case "item.started", "item.updated":
		return "running"
	default:
		return "running"
	}
}

func normalizeAgentActivityStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "complete", "success", "succeeded", "done":
		return "complete"
	case "failed", "error":
		return "error"
	case "cancelled", "canceled":
		return "cancelled"
	case "pending", "queued", "in_progress", "running", "started":
		return "running"
	default:
		return ""
	}
}

func writePrivateJSON(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o600)
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

type boundedAgentBuffer struct {
	data []byte
}

func (b *boundedAgentBuffer) Write(payload []byte) (int, error) {
	const limit = 64 << 10
	if len(payload) >= limit {
		b.data = append(b.data[:0], payload[len(payload)-limit:]...)
		return len(payload), nil
	}
	if overflow := len(b.data) + len(payload) - limit; overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:len(b.data)-overflow]
	}
	b.data = append(b.data, payload...)
	return len(payload), nil
}

func (b *boundedAgentBuffer) String() string { return string(b.data) }

func redactNotebookAgentDiagnostic(value string, request NotebookAgentProviderRunRequest) string {
	value = strings.TrimSpace(value)
	for _, sensitive := range []string{request.WorkspaceRoot, request.RunDir, request.RenartExecutable} {
		if strings.TrimSpace(sensitive) != "" {
			value = strings.ReplaceAll(value, sensitive, "<local path>")
		}
	}
	return truncateNotebookAgentText(value, 16<<10)
}

func providerLabel(provider string) string {
	switch provider {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude Code"
	case "opencode":
		return "OpenCode"
	default:
		return provider
	}
}

func mapField(value map[string]any, key string) map[string]any {
	result, _ := value[key].(map[string]any)
	return result
}

func sliceField(value map[string]any, key string) []any {
	result, _ := value[key].([]any)
	return result
}

func stringField(value map[string]any, key string) string {
	result, _ := value[key].(string)
	return result
}

func boolField(value map[string]any, key string) bool {
	result, _ := value[key].(bool)
	return result
}

func firstString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if result := stringField(value, key); result != "" {
			return result
		}
	}
	return ""
}

func execLookPath(file string) (string, error) { return exec.LookPath(file) }
