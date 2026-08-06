package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoteCatalogCacheRefreshIsSingleFlightAndHonorsTTL(t *testing.T) {
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var databaseCalls atomic.Int32
	cache := NewRemoteCatalogCache(RemoteCatalogDependencies{
		Now: func() time.Time { return now },
		DiscoverDatabases: func(context.Context, string, string) ([]string, error) {
			call := databaseCalls.Add(1)
			if call == 1 {
				close(firstStarted)
				<-releaseFirst
			}
			return []string{"warehouse"}, nil
		},
		DiscoverTables: func(context.Context, string, string, string) ([]SQLDiscoveryTableItem, error) {
			return []SQLDiscoveryTableItem{{Name: "warehouse.analytics.orders", ShortName: "orders"}}, nil
		},
	})
	scope := RemoteCatalogScope{Connection: "prod", Environment: "default"}

	cache.Refresh(t.Context(), scope)
	<-firstStarted
	cache.Refresh(t.Context(), scope)
	assert.Equal(t, int32(1), databaseCalls.Load())
	close(releaseFirst)
	require.Eventually(t, func() bool {
		return len(cache.Snapshot(scope).Relations) == 1
	}, time.Second, 10*time.Millisecond)

	now = now.Add(30 * time.Second)
	cache.Refresh(t.Context(), scope)
	assert.Equal(t, int32(1), databaseCalls.Load())

	now = now.Add(31 * time.Second)
	cache.Refresh(t.Context(), scope)
	require.Eventually(t, func() bool { return databaseCalls.Load() == 2 }, time.Second, 10*time.Millisecond)
}

func TestRemoteCatalogCacheRetainsStaleSnapshotAfterRefreshFailure(t *testing.T) {
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	var fail atomic.Bool
	var calls atomic.Int32
	cache := NewRemoteCatalogCache(RemoteCatalogDependencies{
		Now: func() time.Time { return now },
		DiscoverDatabases: func(context.Context, string, string) ([]string, error) {
			calls.Add(1)
			if fail.Load() {
				return nil, errors.New("credentials expired")
			}
			return []string{"warehouse"}, nil
		},
		DiscoverTables: func(context.Context, string, string, string) ([]SQLDiscoveryTableItem, error) {
			return []SQLDiscoveryTableItem{{Name: "warehouse.analytics.orders", ShortName: "orders"}}, nil
		},
	})
	scope := RemoteCatalogScope{Connection: "prod", Environment: "default"}
	cache.Refresh(t.Context(), scope)
	require.Eventually(t, func() bool { return len(cache.Snapshot(scope).Relations) == 1 }, time.Second, 10*time.Millisecond)

	fail.Store(true)
	now = now.Add(61 * time.Second)
	cache.Refresh(t.Context(), scope)
	require.Eventually(t, func() bool {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		entry := cache.data[remoteCatalogScopeKey(scope)]
		return calls.Load() == 2 && entry != nil && !entry.refreshing && entry.lastError != nil
	}, time.Second, 10*time.Millisecond)
	snapshot := cache.Snapshot(scope)
	require.Len(t, snapshot.Relations, 1)
	assert.True(t, snapshot.Stale)

	cache.Refresh(t.Context(), scope)
	assert.Equal(t, int32(2), calls.Load(), "a failed stale refresh must be retry-limited")
}

func TestRemoteCatalogCacheBoundsRelationsAndScopesColumns(t *testing.T) {
	cache := NewRemoteCatalogCache(RemoteCatalogDependencies{
		MaxRelations: 1,
		DiscoverDatabases: func(context.Context, string, string) ([]string, error) {
			return []string{"warehouse"}, nil
		},
		DiscoverTables: func(_ context.Context, _ string, _ string, environment string) ([]SQLDiscoveryTableItem, error) {
			return []SQLDiscoveryTableItem{
				{Name: "warehouse." + environment + ".orders", ShortName: "orders"},
				{Name: "warehouse." + environment + ".users", ShortName: "users"},
			}, nil
		},
		DiscoverColumns: func(_ context.Context, _ string, table string, _ string) ([]SQLColumn, error) {
			return []SQLColumn{{Name: table + "_id", Type: "bigint"}}, nil
		},
	})
	dev := RemoteCatalogScope{Connection: "prod", Environment: "dev"}
	prod := RemoteCatalogScope{Connection: "prod", Environment: "prod"}
	cache.Refresh(t.Context(), dev)
	cache.Refresh(t.Context(), prod)
	require.Eventually(t, func() bool {
		return len(cache.Snapshot(dev).Relations) == 1 && len(cache.Snapshot(prod).Relations) == 1
	}, time.Second, 10*time.Millisecond)
	assert.False(t, cache.Snapshot(dev).Complete)
	assert.NotEqual(t, cache.Snapshot(dev).Relations[0].QualifiedName, cache.Snapshot(prod).Relations[0].QualifiedName)

	cache.RefreshColumns(t.Context(), dev, "orders")
	require.Eventually(t, func() bool {
		return cache.Snapshot(dev).Relations[0].ColumnsKnown
	}, time.Second, 10*time.Millisecond)
	assert.False(t, cache.Snapshot(prod).Relations[0].ColumnsKnown)
}

func TestRemoteCatalogCacheAcceptsPositiveEndpointObservations(t *testing.T) {
	cache := NewRemoteCatalogCache(RemoteCatalogDependencies{})
	scope := RemoteCatalogScope{Connection: "warehouse", Environment: "dev"}
	cache.ObserveTables(scope, []SQLDiscoveryTableItem{{
		Name:         "catalog.analytics.orders",
		ShortName:    "orders",
		SchemaName:   "analytics",
		DatabaseName: "catalog",
	}})
	cache.ObserveColumns(scope, "catalog.analytics.orders", []SQLColumn{{Name: "order_id", Type: "bigint"}})

	snapshot := cache.Snapshot(scope)
	require.Len(t, snapshot.Relations, 1)
	assert.False(t, snapshot.Complete)
	assert.True(t, snapshot.Relations[0].ColumnsKnown)
	assert.Equal(t, []SQLColumn{{Name: "order_id", Type: "bigint"}}, snapshot.Relations[0].Columns)
	assert.Empty(t, cache.Snapshot(RemoteCatalogScope{Connection: "warehouse", Environment: "prod"}).Relations)
}

func TestRemoteCatalogCacheRefreshesIncompleteEndpointObservation(t *testing.T) {
	var calls atomic.Int32
	cache := NewRemoteCatalogCache(RemoteCatalogDependencies{
		DiscoverDatabases: func(context.Context, string, string) ([]string, error) {
			calls.Add(1)
			return []string{"catalog"}, nil
		},
		DiscoverTables: func(context.Context, string, string, string) ([]SQLDiscoveryTableItem, error) {
			return []SQLDiscoveryTableItem{{Name: "catalog.analytics.orders", ShortName: "orders"}}, nil
		},
	})
	scope := RemoteCatalogScope{Connection: "warehouse", Environment: "dev"}
	cache.ObserveTables(scope, []SQLDiscoveryTableItem{{
		Name:         "catalog.analytics.events",
		ShortName:    "events",
		SchemaName:   "analytics",
		DatabaseName: "catalog",
	}})

	cache.Refresh(t.Context(), scope)
	require.Eventually(t, func() bool {
		return calls.Load() == 1 && cache.Snapshot(scope).Complete
	}, time.Second, 10*time.Millisecond)
}

func TestRemoteCatalogCacheMatchesSchemaAliasAndRetryLimitsColumnFailures(t *testing.T) {
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	var requested atomic.Value
	cache := NewRemoteCatalogCache(RemoteCatalogDependencies{
		Now:        func() time.Time { return now },
		RetryDelay: 10 * time.Second,
		DiscoverColumns: func(_ context.Context, _ string, table string, _ string) ([]SQLColumn, error) {
			requested.Store(table)
			calls.Add(1)
			return nil, errors.New("warehouse unavailable")
		},
	})
	scope := RemoteCatalogScope{Connection: "warehouse", Environment: "dev"}
	cache.ObserveTables(scope, []SQLDiscoveryTableItem{{
		Name:         "catalog.analytics.orders",
		ShortName:    "orders",
		SchemaName:   "analytics",
		DatabaseName: "catalog",
	}})

	cache.RefreshColumns(t.Context(), scope, "analytics.orders")
	require.Eventually(t, func() bool {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		entry := cache.data[remoteCatalogScopeKey(scope)]
		return calls.Load() == 1 && entry != nil && !entry.columnFlights["catalog.analytics.orders"]
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, "catalog.analytics.orders", requested.Load())

	cache.RefreshColumns(t.Context(), scope, "analytics.orders")
	assert.Equal(t, int32(1), calls.Load(), "failed column discovery must not retry on every LSP request")

	now = now.Add(11 * time.Second)
	cache.RefreshColumns(t.Context(), scope, "analytics.orders")
	require.Eventually(t, func() bool { return calls.Load() == 2 }, time.Second, 10*time.Millisecond)
}
