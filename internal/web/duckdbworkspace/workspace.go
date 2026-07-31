package duckdbworkspace

import (
	"context"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bruin-data/bruin/pkg/config"
	duck "github.com/bruin-data/bruin/pkg/duckdb"
	"github.com/bruin-data/bruin/pkg/query"
)

// Client makes DuckDB file references resolve from one workspace without
// changing the process working directory. DuckDB connections are ephemeral, so
// the search path is set in the same multi-statement query as the user SQL.
type Client struct {
	*duck.Client
	workspaceRoot           string
	disableFilesystemAccess bool
}

// WrapClient applies workspace-relative file resolution to a Bruin DuckDB
// client. An empty workspace root leaves the client unchanged.
func WrapClient(client *duck.Client, workspaceRoot string) duck.DuckDBClient {
	return WrapClientWithFilesystemAccess(client, workspaceRoot, true)
}

// WrapClientWithFilesystemAccess applies both the workspace search path and
// the process-wide DuckDB local-filesystem policy to every ephemeral query
// connection.
func WrapClientWithFilesystemAccess(client *duck.Client, workspaceRoot string, filesystemAccessEnabled bool) duck.DuckDBClient {
	root := cleanWorkspaceRoot(workspaceRoot)
	if client == nil || (root == "" && filesystemAccessEnabled) {
		return client
	}
	return &Client{Client: client, workspaceRoot: root, disableFilesystemAccess: !filesystemAccessEnabled}
}

func (c *Client) RunQueryWithoutResult(ctx context.Context, q *query.Query) error {
	return c.Client.RunQueryWithoutResult(ctx, c.withWorkspace(q, false))
}

func (c *Client) Select(ctx context.Context, q *query.Query) ([][]interface{}, error) {
	return c.Client.Select(ctx, c.withWorkspace(q, false))
}

func (c *Client) SelectWithSchema(ctx context.Context, q *query.Query) (*query.QueryResult, error) {
	resultQuery := q != nil && query.IsLikelyResultQuery(q.String())
	return c.Client.SelectWithSchema(ctx, c.withWorkspace(q, resultQuery))
}

func (c *Client) withWorkspace(q *query.Query, keepResultDispatch bool) *query.Query {
	if q == nil {
		return nil
	}
	clone := *q
	settings := make([]string, 0, 2)
	if c.disableFilesystemAccess {
		settings = append(settings, "SET disabled_filesystems = 'LocalFileSystem';")
	}
	if c.workspaceRoot != "" {
		root := strings.ReplaceAll(c.workspaceRoot, "'", "''")
		settings = append(settings, "SET file_search_path = '"+root+"';")
	}
	if len(settings) == 0 {
		return &clone
	}
	prefix := ""
	if keepResultDispatch {
		// Bruin dispatches SelectWithSchema by the first statement. Keep a
		// result-producing statement first while DuckDB returns the final
		// statement's rows and schema.
		prefix = "select null where false;\n"
	}
	clone.Query = prefix + strings.Join(settings, "\n") + "\n" + q.Query
	return &clone
}

type manager struct {
	config.ConnectionAndDetailsGetter
	workspaceRoot           string
	disableFilesystemAccess bool

	mu      sync.Mutex
	clients map[string]*Client
}

// WrapManager applies workspace-relative file resolution to DuckDB
// connections while transparently preserving every other connection.
func WrapManager(base config.ConnectionAndDetailsGetter, workspaceRoot string) config.ConnectionAndDetailsGetter {
	return WrapManagerWithFilesystemAccess(base, workspaceRoot, true)
}

// WrapManagerWithFilesystemAccess applies the local-filesystem policy only to
// DuckDB connections while transparently preserving every other connection.
func WrapManagerWithFilesystemAccess(base config.ConnectionAndDetailsGetter, workspaceRoot string, filesystemAccessEnabled bool) config.ConnectionAndDetailsGetter {
	root := cleanWorkspaceRoot(workspaceRoot)
	if base == nil || (root == "" && filesystemAccessEnabled) {
		return base
	}
	return &manager{
		ConnectionAndDetailsGetter: base,
		workspaceRoot:              root,
		disableFilesystemAccess:    !filesystemAccessEnabled,
		clients:                    make(map[string]*Client),
	}
}

func (m *manager) GetConnection(name string) any {
	raw := m.ConnectionAndDetailsGetter.GetConnection(name)
	return m.wrapConnection(name, raw)
}

func (m *manager) ResolveConnection(name string) (any, error) {
	if resolver, ok := m.ConnectionAndDetailsGetter.(config.ConnectionResolver); ok {
		raw, err := resolver.ResolveConnection(name)
		if err != nil {
			return nil, err
		}
		return m.wrapConnection(name, raw), nil
	}
	return m.GetConnection(name), nil
}

func (m *manager) wrapConnection(name string, raw any) any {
	if !strings.EqualFold(m.ConnectionAndDetailsGetter.GetConnectionType(name), "duckdb") {
		return raw
	}
	client, ok := raw.(*duck.Client)
	if !ok || client == nil {
		return raw
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if wrapped, ok := m.clients[name]; ok && wrapped.Client == client {
		return wrapped
	}
	wrapped := &Client{
		Client:                  client,
		workspaceRoot:           m.workspaceRoot,
		disableFilesystemAccess: m.disableFilesystemAccess,
	}
	m.clients[name] = wrapped
	return wrapped
}

var _ config.ConnectionResolver = (*manager)(nil)

func cleanWorkspaceRoot(workspaceRoot string) string {
	trimmed := strings.TrimSpace(workspaceRoot)
	if trimmed == "" {
		return ""
	}
	return filepath.Clean(trimmed)
}
