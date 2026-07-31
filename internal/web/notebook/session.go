package notebook

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/bruin-data/bruin/pkg/query"
)

// SessionStore manages the per-notebook DuckDB session files under
// .renart/notebooks/. Each notebook gets its own database file; cleanup is
// "delete the file" — no manifests, no orphaned warehouse objects.
type SessionStore struct {
	// Root is the directory holding session files
	// (<workspace>/.renart/notebooks).
	Root string
	// WorkspaceRoot is the base directory for relative DuckDB file references.
	WorkspaceRoot string
	// DisableFilesystemAccess applies DuckDB's LocalFileSystem policy to every
	// notebook connection. The zero value keeps access enabled.
	DisableFilesystemAccess bool

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// NewSessionStore creates a store rooted at the given directory.
func NewSessionStore(root string, workspaceRoot ...string) *SessionStore {
	store := &SessionStore{Root: root, locks: map[string]*sync.Mutex{}}
	if len(workspaceRoot) > 0 {
		store.WorkspaceRoot = workspaceRoot[0]
	}
	return store
}

// DBPath returns the session database file for a notebook UUID.
func (s *SessionStore) DBPath(notebookUUID string) string {
	return filepath.Join(s.Root, notebookUUID+".duckdb")
}

// lockFor serializes session access per notebook within this process.
func (s *SessionStore) lockFor(notebookUUID string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks == nil {
		s.locks = map[string]*sync.Mutex{}
	}
	lock, ok := s.locks[notebookUUID]
	if !ok {
		lock = &sync.Mutex{}
		s.locks[notebookUUID] = lock
	}
	return lock
}

// Session is an open handle on a notebook's DuckDB database.
type Session struct {
	NotebookUUID string
	Path         string

	client *notebookDuckDBClient
	unlock func()
}

// Open opens (creating if needed) the session database for a notebook.
// Access is serialized per notebook; Close releases the lock.
func (s *SessionStore) Open(notebookUUID string) (*Session, error) {
	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return nil, err
	}
	lock := s.lockFor(notebookUUID)
	lock.Lock()

	path := s.DBPath(notebookUUID)
	client, err := newNotebookDuckDBClient(context.Background(), path, s.WorkspaceRoot, s.DisableFilesystemAccess)
	if err != nil {
		lock.Unlock()
		return nil, fmt.Errorf("failed to open notebook session db: %w", err)
	}

	session := &Session{
		NotebookUUID: notebookUUID,
		Path:         path,
		client:       client,
		unlock:       lock.Unlock,
	}
	if err := session.ensureManifest(context.Background()); err != nil {
		session.Close()
		return nil, err
	}
	return session, nil
}

// Remove deletes a notebook's session database (and DuckDB sidecar files).
// Safe to call when no session is open.
func (s *SessionStore) Remove(notebookUUID string) error {
	lock := s.lockFor(notebookUUID)
	lock.Lock()
	defer lock.Unlock()
	return removeSessionFiles(s.DBPath(notebookUUID))
}

// Sweep removes session files whose notebook no longer exists. Called on
// startup so a kill -9 mid-session leaves at most a stale file that the
// next start cleans up.
func (s *SessionStore) Sweep(activeNotebookUUIDs map[string]bool) ([]string, error) {
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	removed := make([]string, 0)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".duckdb") {
			continue
		}
		uuid := strings.TrimSuffix(name, ".duckdb")
		if activeNotebookUUIDs[uuid] {
			continue
		}
		if err := s.Remove(uuid); err == nil {
			removed = append(removed, uuid)
		}
	}
	return removed, nil
}

// ExistingCellObjects lists the cell_* objects already materialized in a
// notebook's session DB (empty when no session file exists yet).
func (s *SessionStore) ExistingCellObjects(notebookUUID string) (map[string]bool, error) {
	if _, err := os.Stat(s.DBPath(notebookUUID)); os.IsNotExist(err) {
		return map[string]bool{}, nil
	}

	session, err := s.Open(notebookUUID)
	if err != nil {
		return nil, err
	}
	defer session.Close()

	result, err := session.Query(context.Background(), "select table_name from information_schema.tables where table_name like 'cell\\_%' escape '\\'")
	if err != nil {
		return nil, err
	}

	objects := make(map[string]bool, len(result.Rows))
	for _, row := range result.Rows {
		if len(row) == 1 {
			if name, ok := row[0].(string); ok {
				objects[name] = true
			}
		}
	}
	return objects, nil
}

// DropCellObjects removes a cell's materialized view/table from the
// session DB (best-effort; missing session is fine).
func (s *SessionStore) DropCellObjects(notebookUUID, cellID string) error {
	if _, err := os.Stat(s.DBPath(notebookUUID)); os.IsNotExist(err) {
		return nil
	}
	session, err := s.Open(notebookUUID)
	if err != nil {
		return err
	}
	defer session.Close()
	object := quoteIdent(CellObjectName(cellID))
	ctx := context.Background()
	if err := session.Exec(ctx, "drop view if exists "+object); err != nil {
		return err
	}
	return session.Exec(ctx, "drop table if exists "+object)
}

func removeSessionFiles(path string) error {
	for _, candidate := range []string{path, path + ".wal", path + ".tmp"} {
		if err := os.Remove(candidate); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// Close releases the session handle and the per-notebook lock.
func (s *Session) Close() {
	if s.client != nil {
		s.client.close()
		s.client = nil
	}
	if s.unlock != nil {
		s.unlock()
		s.unlock = nil
	}
}

// Exec runs a statement without returning rows.
func (s *Session) Exec(ctx context.Context, sql string) error {
	return s.client.exec(ctx, sql)
}

// Query runs a statement and returns the full result.
func (s *Session) Query(ctx context.Context, sql string) (*query.QueryResult, error) {
	return s.client.query(ctx, sql)
}

// objectType returns the information_schema.tables table_type of an object
// in the session DB ("VIEW", "BASE TABLE", …), or "" when it does not exist.
func (s *Session) objectType(ctx context.Context, objectName string) (string, error) {
	result, err := s.Query(ctx, fmt.Sprintf(
		"select table_type from information_schema.tables where table_name = %s",
		sqlStringLiteral(objectName)))
	if err != nil {
		return "", err
	}
	if len(result.Rows) == 0 || len(result.Rows[0]) == 0 {
		return "", nil
	}
	if value, ok := result.Rows[0][0].(string); ok {
		return value, nil
	}
	return "", nil
}

// manifest bookkeeping for imported sources (the import cache).
const importManifestTable = "__renart_imports"

func (s *Session) ensureManifest(ctx context.Context) error {
	return s.Exec(ctx, fmt.Sprintf(
		`create table if not exists %s (ref varchar primary key, object_name varchar, imported_at timestamp, row_count bigint, complete boolean)`,
		importManifestTable))
}

// ImportRecord describes one cached upstream import in the session DB.
type ImportRecord struct {
	Ref        string `json:"ref"`
	ObjectName string `json:"object_name"`
	ImportedAt string `json:"imported_at"`
	RowCount   int64  `json:"row_count"`
	Complete   bool   `json:"complete"`
}

// lookupImport returns the manifest record for ref, if present.
func (s *Session) lookupImport(ctx context.Context, ref string) (*ImportRecord, error) {
	result, err := s.Query(ctx, fmt.Sprintf(
		`select ref, object_name, cast(imported_at as varchar), row_count, complete from %s where ref = %s`,
		importManifestTable, sqlStringLiteral(ref)))
	if err != nil {
		return nil, err
	}
	if len(result.Rows) == 0 {
		return nil, nil
	}
	row := result.Rows[0]
	record := &ImportRecord{}
	record.Ref, _ = row[0].(string)
	record.ObjectName, _ = row[1].(string)
	record.ImportedAt, _ = row[2].(string)
	switch v := row[3].(type) {
	case int64:
		record.RowCount = v
	case int32:
		record.RowCount = int64(v)
	case float64:
		record.RowCount = int64(v)
	}
	record.Complete, _ = row[4].(bool)
	return record, nil
}

func (s *Session) recordImport(ctx context.Context, record ImportRecord) error {
	return s.Exec(ctx, fmt.Sprintf(
		`insert or replace into %s values (%s, %s, now(), %d, %t)`,
		importManifestTable,
		sqlStringLiteral(record.Ref),
		sqlStringLiteral(record.ObjectName),
		record.RowCount,
		record.Complete))
}

// CellObjectName is the machine name a cell materializes as inside its
// notebook session DB. It hangs off the durable cell ID, never the display
// name (invariants 1 and 4).
func CellObjectName(cellID string) string {
	return "cell_" + cellID
}

var objectNameSanitizer = regexp.MustCompile(`[^a-z0-9_]+`)

// ImportObjectName is the machine name an imported upstream is cached as.
func ImportObjectName(ref string) string {
	return "src_" + objectNameSanitizer.ReplaceAllString(strings.ToLower(ref), "_")
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func sqlStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
