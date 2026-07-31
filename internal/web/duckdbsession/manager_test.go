package duckdbsession

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/apache/arrow-adbc/go/adbc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/web/duckcoord"
)

func TestManagerSharesDatabaseAcrossConcurrentAssetsAndPersistsBothWrites(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "warehouse.duckdb")
	coordinator := duckcoord.New(duckcoord.Options{
		LockDir: t.TempDir(), RetryDelay: time.Millisecond,
	})
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	var executions atomic.Int32
	manager := newWithOptions(options{
		Coordinator: coordinator,
		BeforeExecute: func(ctx context.Context, _ string) error {
			if executions.Add(1) > 2 {
				return nil
			}
			arrived <- struct{}{}
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})

	requests := []Request{
		{
			Path: databasePath, WorkspaceRoot: root, AssetName: "analytics.left",
			SQL: "CREATE TABLE analytics.left AS SELECT 1 AS value",
		},
		{
			Path: databasePath, WorkspaceRoot: root, AssetName: "analytics.right",
			SQL: "CREATE TABLE analytics.right AS SELECT 2 AS value",
		},
	}
	results := make(chan error, len(requests))
	for _, request := range requests {
		request := request
		go func() {
			results <- manager.Execute(t.Context(), request)
		}()
	}
	for range requests {
		select {
		case <-arrived:
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent DuckDB request did not reach the shared session")
		}
	}

	manager.mu.Lock()
	require.Len(t, manager.sessions, 1)
	for _, active := range manager.sessions {
		assert.Equal(t, 2, active.refs)
	}
	manager.mu.Unlock()

	close(release)
	for range requests {
		require.NoError(t, <-results)
	}

	// The first session is closed once the overlapping batch drains. Opening a
	// new session and reading both tables proves that both catalog writes were
	// durably persisted, rather than merely visible to their original handles.
	require.NoError(t, manager.Execute(t.Context(), Request{
		Path: databasePath, WorkspaceRoot: root, AssetName: "analytics.verified",
		SQL: `CREATE TABLE analytics.verified AS
SELECT left_table.value + right_table.value AS value
FROM analytics.left AS left_table
CROSS JOIN analytics.right AS right_table`,
	}))
}

func TestManagerKeepsChildProcessLeaseUntilSharedSessionDrains(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "warehouse.duckdb")
	coordinator := duckcoord.New(duckcoord.Options{
		LockDir: t.TempDir(), RetryDelay: time.Millisecond,
	})
	arrived := make(chan struct{}, 1)
	release := make(chan struct{})
	manager := newWithOptions(options{
		Coordinator: coordinator,
		BeforeExecute: func(ctx context.Context, _ string) error {
			arrived <- struct{}{}
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})

	executionDone := make(chan error, 1)
	go func() {
		executionDone <- manager.Execute(t.Context(), Request{
			Path: databasePath, WorkspaceRoot: root, AssetName: "analytics.native",
			SQL: "CREATE TABLE analytics.native AS SELECT 1 AS value",
		})
	}()
	select {
	case <-arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("native DuckDB request did not acquire its shared session")
	}

	waited := make(chan struct{}, 1)
	acquired := make(chan *duckcoord.Lease, 1)
	go func() {
		lease, err := coordinator.Acquire(t.Context(), []string{databasePath}, duckcoord.Owner{
			Operation: "child writer",
			OnWait: func(string) {
				select {
				case waited <- struct{}{}:
				default:
				}
			},
		})
		if err != nil {
			acquired <- nil
			return
		}
		acquired <- lease
	}()
	select {
	case <-waited:
	case <-time.After(3 * time.Second):
		t.Fatal("child writer did not wait on the shared session lease")
	}
	select {
	case lease := <-acquired:
		if lease != nil {
			lease.Release()
		}
		t.Fatal("child writer acquired the database before the shared session drained")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	require.NoError(t, <-executionDone)
	select {
	case lease := <-acquired:
		require.NotNil(t, lease)
		lease.Release()
	case <-time.After(3 * time.Second):
		t.Fatal("child writer did not acquire the database after the shared session drained")
	}
}

func TestManagerCancellationReleasesDatabaseLease(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "warehouse.duckdb")
	coordinator := duckcoord.New(duckcoord.Options{
		LockDir: t.TempDir(), RetryDelay: time.Millisecond,
	})
	arrived := make(chan struct{}, 1)
	manager := newWithOptions(options{
		Coordinator: coordinator,
		BeforeExecute: func(ctx context.Context, _ string) error {
			arrived <- struct{}{}
			<-ctx.Done()
			return ctx.Err()
		},
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- manager.Execute(ctx, Request{
			Path: databasePath, WorkspaceRoot: root, AssetName: "analytics.cancelled",
			SQL: "CREATE TABLE analytics.cancelled AS SELECT 1 AS value",
		})
	}()
	select {
	case <-arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("DuckDB request did not start")
	}
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)

	acquireCtx, acquireCancel := context.WithTimeout(t.Context(), time.Second)
	defer acquireCancel()
	lease, err := coordinator.Acquire(acquireCtx, []string{databasePath}, duckcoord.Owner{})
	require.NoError(t, err)
	lease.Release()
}

func TestTransactionConflictClassifierIsNarrow(t *testing.T) {
	t.Parallel()

	assert.True(t, isTransactionConflict(errors.New("TransactionContext Error: Catalog write-write conflict on alter")))
	assert.True(t, isTransactionConflict(errors.New("transaction conflict: cannot update table")))
	assert.False(t, isTransactionConflict(errors.New("constraint error: duplicate key")))
	assert.False(t, isTransactionConflict(context.Canceled))
}

func TestManagerRetriesExplicitTransactionConflict(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "warehouse.duckdb")
	coordinator := duckcoord.New(duckcoord.Options{
		LockDir: t.TempDir(), RetryDelay: time.Millisecond,
	})
	var materializationAttempts atomic.Int32
	manager := newWithOptions(options{
		Coordinator: coordinator,
		RetryDelays: []time.Duration{0},
		ExecuteStatement: func(ctx context.Context, connection adbc.Connection, sqlText string) error {
			if strings.Contains(sqlText, "CREATE TABLE analytics.retry") &&
				materializationAttempts.Add(1) == 1 {
				return errors.New("TransactionContext Error: Catalog write-write conflict")
			}
			return executeStatement(ctx, connection, sqlText)
		},
	})

	require.NoError(t, manager.Execute(t.Context(), Request{
		Path: databasePath, WorkspaceRoot: root, AssetName: "analytics.retry",
		SQL: "CREATE TABLE analytics.retry AS SELECT 1 AS value",
	}))
	assert.Equal(t, int32(2), materializationAttempts.Load())

	require.NoError(t, manager.Execute(t.Context(), Request{
		Path: databasePath, WorkspaceRoot: root, AssetName: "analytics.verified",
		SQL: "CREATE TABLE analytics.verified AS SELECT * FROM analytics.retry",
	}))
}

func TestManagerDisablesLocalFilesystemBeforeMaterialization(t *testing.T) {
	root := t.TempDir()
	var statements []string
	manager := newWithOptions(options{
		ExecuteStatement: func(ctx context.Context, connection adbc.Connection, sqlText string) error {
			statements = append(statements, sqlText)
			return executeStatement(ctx, connection, sqlText)
		},
	})

	require.NoError(t, manager.Execute(t.Context(), Request{
		Path:                    filepath.Join(root, "warehouse.duckdb"),
		WorkspaceRoot:           root,
		AssetName:               "policy_check",
		SQL:                     "SELECT 1 AS value",
		DisableFilesystemAccess: true,
	}))
	require.NotEmpty(t, statements)
	assert.Equal(t, "SET disabled_filesystems = 'LocalFileSystem'", statements[0])
}
