// Package duckdbsession coordinates concurrent native SQL writes to one local
// DuckDB database.
//
// DuckDB permits concurrent writers only when they share one Database instance
// and use separate connections. A Manager keeps that instance alive for the
// duration of an overlapping batch, while holding Renart's existing
// cross-process lease so child-process writers cannot open the same file.
package duckdbsession

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/apache/arrow-adbc/go/adbc"
	"github.com/apache/arrow-adbc/go/adbc/drivermgr"
	duck "github.com/bruin-data/bruin/pkg/duckdb"
	"github.com/bruin-data/bruin/pkg/tablename"

	"renart/internal/web/adbcutil"
	"renart/internal/web/duckcoord"
)

var defaultConflictRetryDelays = []time.Duration{
	10 * time.Millisecond,
	25 * time.Millisecond,
	50 * time.Millisecond,
}

// Request describes one independently materialized asset. Path may be
// workspace-relative; Manager canonicalizes it before selecting a session.
type Request struct {
	Path                    string
	WorkspaceRoot           string
	AssetName               string
	SQL                     string
	DisableFilesystemAccess bool
	Owner                   duckcoord.Owner
}

type statementExecutor func(context.Context, adbc.Connection, string) error

// options contains narrow seams used by package tests. Production callers use
// New, which selects the ADBC DuckDB driver and the standard retry schedule.
type options struct {
	Coordinator      *duckcoord.Coordinator
	RetryDelays      []time.Duration
	BeforeExecute    func(context.Context, string) error
	ExecuteStatement statementExecutor
}

// Manager shares a Database instance between overlapping requests for the same
// canonical file. It is safe for concurrent use.
type Manager struct {
	coordinator      *duckcoord.Coordinator
	retryDelays      []time.Duration
	beforeExecute    func(context.Context, string) error
	executeStatement statementExecutor

	mu       sync.Mutex
	sessions map[string]*session
}

type session struct {
	path string

	ready   chan struct{}
	initErr error
	db      adbc.Database
	lease   *duckcoord.Lease
	refs    int

	schemaMu sync.Mutex
	schemas  map[string]struct{}
}

// New creates a shared-session manager over coordinator.
func New(coordinator *duckcoord.Coordinator) *Manager {
	return newWithOptions(options{Coordinator: coordinator})
}

func newWithOptions(opts options) *Manager {
	coordinator := opts.Coordinator
	if coordinator == nil {
		coordinator = duckcoord.New(duckcoord.Options{})
	}
	retryDelays := append([]time.Duration(nil), opts.RetryDelays...)
	if opts.RetryDelays == nil {
		retryDelays = append([]time.Duration(nil), defaultConflictRetryDelays...)
	}
	execute := opts.ExecuteStatement
	if execute == nil {
		execute = executeStatement
	}
	return &Manager{
		coordinator:      coordinator,
		retryDelays:      retryDelays,
		beforeExecute:    opts.BeforeExecute,
		executeStatement: execute,
		sessions:         make(map[string]*session),
	}
}

// Execute materializes one asset through its own ADBC connection. Concurrent
// calls for the same file share the Database instance but never a connection.
func (m *Manager) Execute(ctx context.Context, request Request) (resultErr error) {
	if m == nil {
		return fmt.Errorf("DuckDB session manager is unavailable")
	}
	path, err := duckcoord.CanonicalPath(request.WorkspaceRoot, request.Path)
	if err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("DuckDB concurrent execution requires a local file-backed database")
	}
	if strings.TrimSpace(request.SQL) == "" {
		return nil
	}

	active, err := m.acquire(ctx, path, request.Owner)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := m.release(active); resultErr == nil && closeErr != nil {
			resultErr = closeErr
		}
	}()
	if m.beforeExecute != nil {
		if err := m.beforeExecute(ctx, path); err != nil {
			return err
		}
	}

	for attempt := 0; ; attempt++ {
		err = m.executeAttempt(ctx, active, request)
		if err == nil || !isTransactionConflict(err) || attempt >= len(m.retryDelays) {
			return err
		}
		if err := waitForRetry(ctx, m.retryDelays[attempt]); err != nil {
			return err
		}
	}
}

func (m *Manager) acquire(ctx context.Context, path string, owner duckcoord.Owner) (*session, error) {
	m.mu.Lock()
	if active := m.sessions[path]; active != nil {
		active.refs++
		ready := active.ready
		m.mu.Unlock()
		select {
		case <-ready:
			if active.initErr != nil {
				_ = m.release(active)
				return nil, active.initErr
			}
			return active, nil
		case <-ctx.Done():
			_ = m.release(active)
			return nil, ctx.Err()
		}
	}

	active := &session{
		path:    path,
		ready:   make(chan struct{}),
		refs:    1,
		schemas: make(map[string]struct{}),
	}
	m.sessions[path] = active
	m.mu.Unlock()

	active.initErr = m.initialize(ctx, active, owner)
	close(active.ready)
	if active.initErr != nil {
		_ = m.release(active)
		return nil, active.initErr
	}
	return active, nil
}

func (m *Manager) initialize(ctx context.Context, active *session, owner duckcoord.Owner) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	lease, err := m.coordinator.Acquire(ctx, []string{active.path}, owner)
	if err != nil {
		return err
	}

	database, err := openDuckDBDatabase(ctx, active.path)
	if err != nil {
		lease.Release()
		return fmt.Errorf("open shared DuckDB database %q: %w", active.path, err)
	}
	active.db = database
	active.lease = lease
	return nil
}

func (m *Manager) release(active *session) error {
	if active == nil {
		return nil
	}
	m.mu.Lock()
	active.refs--
	if active.refs > 0 {
		m.mu.Unlock()
		return nil
	}
	if current := m.sessions[active.path]; current == active {
		delete(m.sessions, active.path)
	}
	database := active.db
	lease := active.lease
	m.mu.Unlock()

	var closeErr error
	if database != nil {
		closeErr = database.Close()
	}
	if lease != nil {
		lease.Release()
	}
	if closeErr != nil {
		return fmt.Errorf("close shared DuckDB database %q: %w", active.path, closeErr)
	}
	return nil
}

func (m *Manager) executeAttempt(ctx context.Context, active *session, request Request) (resultErr error) {
	connection, err := active.db.Open(ctx)
	if err != nil {
		return fmt.Errorf("open DuckDB asset connection: %w", err)
	}
	defer func() {
		if closeErr := connection.Close(); resultErr == nil && closeErr != nil {
			resultErr = fmt.Errorf("close DuckDB asset connection: %w", closeErr)
		}
	}()

	if request.DisableFilesystemAccess {
		if err := m.executeStatement(ctx, connection, "SET disabled_filesystems = 'LocalFileSystem'"); err != nil {
			return fmt.Errorf("disable DuckDB local filesystem access: %w", err)
		}
	}
	if root := cleanWorkspaceRoot(request.WorkspaceRoot); root != "" {
		escaped := strings.ReplaceAll(root, "'", "''")
		if err := m.executeStatement(ctx, connection, "SET file_search_path = '"+escaped+"'"); err != nil {
			return fmt.Errorf("set DuckDB workspace search path: %w", err)
		}
	}
	if err := active.ensureSchema(ctx, connection, request.AssetName, m.executeStatement); err != nil {
		return err
	}
	if err := m.executeStatement(ctx, connection, request.SQL); err != nil {
		return fmt.Errorf("execute DuckDB materialization: %w", err)
	}
	return nil
}

func (s *session) ensureSchema(
	ctx context.Context,
	connection adbc.Connection,
	assetName string,
	execute statementExecutor,
) error {
	schemaName, ok := tablename.SchemaToCreate(assetName, strings.ToLower)
	if !ok {
		return nil
	}

	s.schemaMu.Lock()
	defer s.schemaMu.Unlock()
	if _, exists := s.schemas[schemaName]; exists {
		return nil
	}
	if err := execute(ctx, connection, "CREATE SCHEMA IF NOT EXISTS "+schemaName); err != nil {
		return fmt.Errorf("create or ensure DuckDB schema %q: %w", schemaName, err)
	}
	s.schemas[schemaName] = struct{}{}
	return nil
}

func executeStatement(ctx context.Context, connection adbc.Connection, sqlText string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	statement, err := connection.NewStatement()
	if err != nil {
		return err
	}
	defer statement.Close()
	if err := statement.SetSqlQuery(sqlText); err != nil {
		return err
	}
	stopWatching, err := adbcutil.WatchStatementCancellation(ctx, statement)
	if err != nil {
		return err
	}
	reader, _, executeErr := statement.ExecuteQuery(ctx)
	stopWatching()
	if reader != nil {
		reader.Release()
	}
	if executeErr != nil {
		return executeErr
	}
	return nil
}

func openDuckDBDatabase(ctx context.Context, path string) (adbc.Database, error) {
	if err := duck.EnsureADBCDriverInstalled(ctx); err != nil {
		return nil, fmt.Errorf("ensure DuckDB ADBC driver: %w", err)
	}
	var driver drivermgr.Driver
	database, err := driver.NewDatabase(map[string]string{
		"driver": "duckdb",
		"path":   path,
	})
	if err != nil {
		return nil, err
	}
	return database, nil
}

func isTransactionConflict(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "transaction conflict") ||
		strings.Contains(message, "catalog write-write conflict") ||
		(strings.Contains(message, "transactioncontext error") && strings.Contains(message, "conflict"))
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func cleanWorkspaceRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	return filepath.Clean(root)
}
