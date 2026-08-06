package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/bruin-data/bruin/pkg/config"
)

// loadDiscoverEnvName is the deterministic env-var alias under which renart
// exposes the resolved connection URI to the Sling CLI. Sling auto-detects
// connections from environment variables holding a connection URL, so
// `sling conns discover RENART_SLING_DISCOVER` resolves to whatever URI we set.
const loadDiscoverEnvName = "RENART_SLING_DISCOVER"

const maxLoadDiscoveryStreams = 500

// LoadDiscoveryStream is a single object/stream a Load connection exposes.
type LoadDiscoveryStream struct {
	Name   string `json:"name"`
	Schema string `json:"schema,omitempty"`
}

// LoadDiscoveryResult is the response of `sling conns discover` for intellisense.
type LoadDiscoveryResult struct {
	Status         string                `json:"status"`
	ConnectionName string                `json:"connection_name"`
	Pattern        string                `json:"pattern,omitempty"`
	Streams        []LoadDiscoveryStream `json:"streams"`
	Truncated      bool                  `json:"truncated,omitempty"`
	// RawOutput is retained for server-side diagnostics and tests only. Sling
	// output can echo connection details, so it must never cross the HTTP API.
	RawOutput string `json:"-"`
	Error     string `json:"error,omitempty"`
}

type LoadDependencies struct {
	WorkspaceRoot        string
	NewConnectionManager func(context.Context, string) (config.ConnectionAndDetailsGetter, error)
}

type LoadService struct {
	deps LoadDependencies
}

func NewLoadService(deps LoadDependencies) *LoadService {
	return &LoadService{deps: deps}
}

// Discover lists the streams/objects a bruin connection exposes, for editor
// intellisense, by bridging the connection to a URI and running
// `sling conns discover <alias> [--pattern …]`.
func (s *LoadService) Discover(ctx context.Context, connectionName, pattern, environment string) (LoadDiscoveryResult, *APIError) {
	connectionName = strings.TrimSpace(connectionName)
	if connectionName == "" {
		return LoadDiscoveryResult{}, &APIError{Status: http.StatusBadRequest, Code: "connection_required", Message: "connection is required"}
	}
	if s.deps.NewConnectionManager == nil {
		return LoadDiscoveryResult{}, &APIError{Status: http.StatusInternalServerError, Code: "connection_manager_unavailable", Message: "connection manager is not configured"}
	}

	manager, err := s.deps.NewConnectionManager(ctx, environment)
	if err != nil {
		return LoadDiscoveryResult{}, &APIError{Status: http.StatusInternalServerError, Code: "connection_manager_failed", Message: err.Error()}
	}

	uri, err := loadConnectionURI(manager, connectionName)
	if err != nil {
		return LoadDiscoveryResult{}, &APIError{Status: http.StatusBadRequest, Code: "connection_uri_failed", Message: err.Error()}
	}

	output, runErr := runLoadConnsDiscover(ctx, s.deps.WorkspaceRoot, uri, pattern)
	if runErr != nil {
		return LoadDiscoveryResult{
			Status:         "error",
			ConnectionName: connectionName,
			Pattern:        strings.TrimSpace(pattern),
			Streams:        []LoadDiscoveryStream{},
			RawOutput:      output,
			Error:          runErr.Error(),
		}, nil
	}

	streams := parseLoadDiscoverStreams(output)
	streams, truncated := boundedLoadDiscoveryStreams(streams)
	return LoadDiscoveryResult{
		Status:         "ok",
		ConnectionName: connectionName,
		Pattern:        strings.TrimSpace(pattern),
		Streams:        streams,
		Truncated:      truncated,
		RawOutput:      output,
	}, nil
}

func boundedLoadDiscoveryStreams(streams []LoadDiscoveryStream) ([]LoadDiscoveryStream, bool) {
	if len(streams) <= maxLoadDiscoveryStreams {
		return streams, false
	}
	return streams[:maxLoadDiscoveryStreams], true
}

func runLoadConnsDiscover(ctx context.Context, workspaceRoot, connectionURI, pattern string) (string, error) {
	args := []string{"conns", "discover", loadDiscoverEnvName, "-o", "json"}
	if trimmed := strings.TrimSpace(pattern); trimmed != "" {
		args = append(args, "--pattern", trimmed)
	}

	cmdName, cmdArgs, err := loadCommand(ctx, args, nil)
	if err != nil {
		return "", err
	}

	cmd := newStreamingCommand(ctx, cmdName, cmdArgs, workspaceRoot, nil)
	cmd.Env = append(cmd.Env, loadDiscoverEnvName+"="+connectionURI)

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	runErr := runSlingCommand(ctx, cmd)
	return buf.String(), runErr
}

// loadDiscoverPayload is the `-o json` shape sling emits: a generic table with
// column headers in Fields and one object per Rows entry.
type loadDiscoverPayload struct {
	Fields []string `json:"fields"`
	Rows   [][]any  `json:"rows"`
}

// parseLoadDiscoverStreams reads the JSON (`-o json`) output of
// `sling conns discover`, building schema-qualified stream names from the Schema
// and Name columns. sling may prefix the JSON with log lines, so the JSON object
// is located by line.
func parseLoadDiscoverStreams(output string) []LoadDiscoveryStream {
	payload, ok := decodeLoadDiscoverPayload(output)
	if !ok {
		return []LoadDiscoveryStream{}
	}

	nameIdx, schemaIdx := -1, -1
	for i, field := range payload.Fields {
		switch strings.ToLower(strings.TrimSpace(field)) {
		case "name", "stream", "table":
			nameIdx = i
		case "schema":
			schemaIdx = i
		}
	}
	if nameIdx < 0 {
		return []LoadDiscoveryStream{}
	}

	seen := make(map[string]struct{})
	streams := make([]LoadDiscoveryStream, 0, len(payload.Rows))
	for _, row := range payload.Rows {
		if nameIdx >= len(row) {
			continue
		}
		name := loadCellString(row[nameIdx])
		if name == "" {
			continue
		}
		schema := ""
		if schemaIdx >= 0 && schemaIdx < len(row) {
			schema = loadCellString(row[schemaIdx])
		}
		qualified := name
		if schema != "" && !strings.Contains(name, ".") {
			qualified = schema + "." + name
		} else if schema == "" {
			if dot := strings.LastIndex(name, "."); dot > 0 {
				schema = name[:dot]
			}
		}
		if _, ok := seen[qualified]; ok {
			continue
		}
		seen[qualified] = struct{}{}
		streams = append(streams, LoadDiscoveryStream{Name: qualified, Schema: schema})
	}

	sort.Slice(streams, func(i, j int) bool { return streams[i].Name < streams[j].Name })
	return streams
}

// decodeLoadDiscoverPayload finds and decodes the JSON object line in mixed
// log+JSON output.
func decodeLoadDiscoverPayload(output string) (loadDiscoverPayload, bool) {
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(loadAnsiEscape.ReplaceAllString(rawLine, ""))
		if !strings.HasPrefix(line, "{") || !strings.Contains(line, "\"rows\"") {
			continue
		}
		var payload loadDiscoverPayload
		if err := json.Unmarshal([]byte(line), &payload); err == nil {
			return payload, true
		}
	}
	return loadDiscoverPayload{}, false
}

func loadCellString(value any) string {
	if value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}

var loadAnsiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)
