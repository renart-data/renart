package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultRemoteCatalogTTL            = 60 * time.Second
	defaultRemoteCatalogRefreshTimeout = 5 * time.Second
	defaultRemoteCatalogRetryDelay     = 10 * time.Second
	defaultRemoteCatalogMaxDatabases   = 32
	defaultRemoteCatalogMaxRelations   = 2_000
	defaultRemoteCatalogMaxColumns     = 512
)

// RemoteCatalogScope identifies one credential and environment boundary. An
// empty Database means that discovery spans every database exposed by the
// connection. Secrets never enter the scope or any returned snapshot.
type RemoteCatalogScope struct {
	Connection  string
	Environment string
	Database    string
}

// RemoteCatalogRelation is positive, read-only evidence that a warehouse
// relation exists. ColumnsKnown distinguishes a confirmed empty schema from a
// relation whose columns have not been requested yet.
type RemoteCatalogRelation struct {
	QualifiedName     string
	ShortName         string
	SchemaName        string
	DatabaseName      string
	Columns           []SQLColumn
	ColumnsKnown      bool
	ColumnsObservedAt time.Time
}

// RemoteCatalogSnapshot is an immediate cache view. Complete means every
// database/table listing in the requested scope completed without hitting a
// configured cap. Absence is not used as proof that a relation does not exist,
// even for complete snapshots.
type RemoteCatalogSnapshot struct {
	Relations  []RemoteCatalogRelation
	Complete   bool
	ObservedAt time.Time
	Stale      bool
}

// RemoteCatalogProvider is deliberately non-blocking. Snapshot performs no
// I/O; Refresh and RefreshColumns only schedule single-flight, bounded work.
type RemoteCatalogProvider interface {
	Snapshot(scope RemoteCatalogScope) RemoteCatalogSnapshot
	Refresh(ctx context.Context, scope RemoteCatalogScope)
	RefreshColumns(ctx context.Context, scope RemoteCatalogScope, relation string)
}

// RemoteCatalogObserver lets the explicit discovery endpoints seed the same
// positive cache used by the LSP. Observations never claim catalog
// completeness, because the browser may request only one database or table.
type RemoteCatalogObserver interface {
	ObserveTables(scope RemoteCatalogScope, relations []SQLDiscoveryTableItem)
	ObserveColumns(scope RemoteCatalogScope, relation string, columns []SQLColumn)
}

type RemoteCatalogDependencies struct {
	DiscoverDatabases func(context.Context, string, string) ([]string, error)
	DiscoverTables    func(context.Context, string, string, string) ([]SQLDiscoveryTableItem, error)
	DiscoverColumns   func(context.Context, string, string, string) ([]SQLColumn, error)
	TTL               time.Duration
	RefreshTimeout    time.Duration
	RetryDelay        time.Duration
	MaxDatabases      int
	MaxRelations      int
	MaxColumns        int
	Now               func() time.Time
}

type remoteCatalogEntry struct {
	snapshot       RemoteCatalogSnapshot
	refreshing     bool
	lastAttempt    time.Time
	lastError      error
	columnFlights  map[string]bool
	columnAttempts map[string]time.Time
}

// RemoteCatalogCache is the process-local provider used by the HTTP SQL LSP.
// It owns no durable state; authored assets remain the deterministic graph.
type RemoteCatalogCache struct {
	deps RemoteCatalogDependencies
	mu   sync.Mutex
	data map[string]*remoteCatalogEntry
}

func NewRemoteCatalogCache(deps RemoteCatalogDependencies) *RemoteCatalogCache {
	if deps.TTL <= 0 {
		deps.TTL = defaultRemoteCatalogTTL
	}
	if deps.RefreshTimeout <= 0 {
		deps.RefreshTimeout = defaultRemoteCatalogRefreshTimeout
	}
	if deps.RetryDelay <= 0 {
		deps.RetryDelay = defaultRemoteCatalogRetryDelay
	}
	if deps.MaxDatabases <= 0 {
		deps.MaxDatabases = defaultRemoteCatalogMaxDatabases
	}
	if deps.MaxRelations <= 0 {
		deps.MaxRelations = defaultRemoteCatalogMaxRelations
	}
	if deps.MaxColumns <= 0 {
		deps.MaxColumns = defaultRemoteCatalogMaxColumns
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &RemoteCatalogCache{deps: deps, data: map[string]*remoteCatalogEntry{}}
}

func (c *RemoteCatalogCache) Snapshot(scope RemoteCatalogScope) RemoteCatalogSnapshot {
	scope = normalizeRemoteCatalogScope(scope)
	if scope.Connection == "" {
		return RemoteCatalogSnapshot{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.data[remoteCatalogScopeKey(scope)]
	if entry == nil {
		return RemoteCatalogSnapshot{}
	}
	result := cloneRemoteCatalogSnapshot(entry.snapshot)
	if !result.ObservedAt.IsZero() {
		result.Stale = c.deps.Now().Sub(result.ObservedAt) >= c.deps.TTL
	}
	return result
}

func (c *RemoteCatalogCache) Refresh(ctx context.Context, scope RemoteCatalogScope) {
	scope = normalizeRemoteCatalogScope(scope)
	if scope.Connection == "" || c.deps.DiscoverDatabases == nil || c.deps.DiscoverTables == nil {
		return
	}
	key := remoteCatalogScopeKey(scope)
	now := c.deps.Now()

	c.mu.Lock()
	entry := c.entryLocked(key)
	fresh := entry.snapshot.Complete &&
		!entry.snapshot.ObservedAt.IsZero() &&
		now.Sub(entry.snapshot.ObservedAt) < c.deps.TTL
	retryingTooSoon := !entry.lastAttempt.IsZero() &&
		now.Sub(entry.lastAttempt) < c.deps.RetryDelay &&
		(entry.lastError != nil || !entry.snapshot.Complete)
	if fresh || entry.refreshing || retryingTooSoon {
		c.mu.Unlock()
		return
	}
	entry.refreshing = true
	entry.lastAttempt = now
	c.mu.Unlock()

	go c.refresh(contextWithoutCancellation(ctx), key, scope)
}

func (c *RemoteCatalogCache) RefreshColumns(ctx context.Context, scope RemoteCatalogScope, relation string) {
	scope = normalizeRemoteCatalogScope(scope)
	relation = strings.TrimSpace(relation)
	if scope.Connection == "" || relation == "" || c.deps.DiscoverColumns == nil {
		return
	}
	key := remoteCatalogScopeKey(scope)
	now := c.deps.Now()

	c.mu.Lock()
	entry := c.data[key]
	if entry == nil {
		c.mu.Unlock()
		return
	}
	index := remoteCatalogRelationIndex(entry.snapshot.Relations, relation)
	if index < 0 {
		c.mu.Unlock()
		return
	}
	qualified := entry.snapshot.Relations[index].QualifiedName
	flightKey := strings.ToLower(qualified)
	fresh := entry.snapshot.Relations[index].ColumnsKnown &&
		!entry.snapshot.Relations[index].ColumnsObservedAt.IsZero() &&
		now.Sub(entry.snapshot.Relations[index].ColumnsObservedAt) < c.deps.TTL
	retryingTooSoon := !entry.columnAttempts[flightKey].IsZero() &&
		now.Sub(entry.columnAttempts[flightKey]) < c.deps.RetryDelay
	if fresh || entry.columnFlights[flightKey] || retryingTooSoon {
		c.mu.Unlock()
		return
	}
	entry.columnFlights[flightKey] = true
	entry.columnAttempts[flightKey] = now
	c.mu.Unlock()

	go c.refreshColumns(contextWithoutCancellation(ctx), key, scope, qualified, flightKey)
}

func (c *RemoteCatalogCache) ObserveTables(scope RemoteCatalogScope, relations []SQLDiscoveryTableItem) {
	scope = normalizeRemoteCatalogScope(scope)
	if scope.Connection == "" || len(relations) == 0 {
		return
	}
	key := remoteCatalogScopeKey(scope)
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entryLocked(key)
	byName := make(map[string]int, len(entry.snapshot.Relations))
	for index, relation := range entry.snapshot.Relations {
		byName[strings.ToLower(relation.QualifiedName)] = index
	}
	for _, relation := range relations {
		if len(entry.snapshot.Relations) >= c.deps.MaxRelations {
			break
		}
		qualified := strings.TrimSpace(relation.Name)
		if qualified == "" {
			continue
		}
		if _, exists := byName[strings.ToLower(qualified)]; exists {
			continue
		}
		short := strings.TrimSpace(relation.ShortName)
		if short == "" {
			short = remoteCatalogShortName(qualified)
		}
		entry.snapshot.Relations = append(entry.snapshot.Relations, RemoteCatalogRelation{
			QualifiedName: qualified,
			ShortName:     short,
			SchemaName:    strings.TrimSpace(relation.SchemaName),
			DatabaseName:  strings.TrimSpace(relation.DatabaseName),
		})
		byName[strings.ToLower(qualified)] = len(entry.snapshot.Relations) - 1
	}
	sort.Slice(entry.snapshot.Relations, func(i, j int) bool {
		return strings.ToLower(entry.snapshot.Relations[i].QualifiedName) < strings.ToLower(entry.snapshot.Relations[j].QualifiedName)
	})
	entry.snapshot.ObservedAt = c.deps.Now()
	entry.snapshot.Complete = false
}

func (c *RemoteCatalogCache) ObserveColumns(scope RemoteCatalogScope, relation string, columns []SQLColumn) {
	scope = normalizeRemoteCatalogScope(scope)
	relation = strings.TrimSpace(relation)
	if scope.Connection == "" || relation == "" {
		return
	}
	if len(columns) > c.deps.MaxColumns {
		columns = columns[:c.deps.MaxColumns]
	}
	key := remoteCatalogScopeKey(scope)
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entryLocked(key)
	index := remoteCatalogRelationIndex(entry.snapshot.Relations, relation)
	if index < 0 {
		if len(entry.snapshot.Relations) >= c.deps.MaxRelations {
			return
		}
		entry.snapshot.Relations = append(entry.snapshot.Relations, RemoteCatalogRelation{
			QualifiedName: relation,
			ShortName:     remoteCatalogShortName(relation),
		})
		index = len(entry.snapshot.Relations) - 1
	}
	entry.snapshot.Relations[index].Columns = append([]SQLColumn(nil), columns...)
	entry.snapshot.Relations[index].ColumnsKnown = true
	entry.snapshot.Relations[index].ColumnsObservedAt = c.deps.Now()
	entry.snapshot.ObservedAt = c.deps.Now()
}

func (c *RemoteCatalogCache) refresh(parent context.Context, key string, scope RemoteCatalogScope) {
	ctx, cancel := context.WithTimeout(parent, c.deps.RefreshTimeout)
	defer cancel()

	snapshot, err := c.loadSnapshot(ctx, scope)

	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entryLocked(key)
	entry.refreshing = false
	entry.lastError = err
	if err != nil {
		return
	}
	mergeRemoteCatalogColumns(&snapshot, entry.snapshot)
	entry.snapshot = snapshot
}

func (c *RemoteCatalogCache) loadSnapshot(ctx context.Context, scope RemoteCatalogScope) (RemoteCatalogSnapshot, error) {
	databaseNames := []string{scope.Database}
	if scope.Database == "" {
		var err error
		databaseNames, err = c.deps.DiscoverDatabases(ctx, scope.Connection, scope.Environment)
		if err != nil {
			return RemoteCatalogSnapshot{}, err
		}
	}
	databaseNames = compactSortedStrings(databaseNames)
	complete := true
	if len(databaseNames) > c.deps.MaxDatabases {
		databaseNames = databaseNames[:c.deps.MaxDatabases]
		complete = false
	}

	relations := make([]RemoteCatalogRelation, 0)
	seen := map[string]struct{}{}
	successfulListings := 0
	var listingErrors []error
	for _, database := range databaseNames {
		items, err := c.deps.DiscoverTables(ctx, scope.Connection, database, scope.Environment)
		if err != nil {
			complete = false
			listingErrors = append(listingErrors, err)
			continue
		}
		successfulListings++
		for _, item := range items {
			qualified := strings.TrimSpace(item.Name)
			if qualified == "" {
				continue
			}
			normalized := strings.ToLower(qualified)
			if _, exists := seen[normalized]; exists {
				continue
			}
			seen[normalized] = struct{}{}
			short := strings.TrimSpace(item.ShortName)
			if short == "" {
				short = remoteCatalogShortName(qualified)
			}
			relations = append(relations, RemoteCatalogRelation{
				QualifiedName: qualified,
				ShortName:     short,
				SchemaName:    strings.TrimSpace(item.SchemaName),
				DatabaseName:  strings.TrimSpace(item.DatabaseName),
			})
			if len(relations) >= c.deps.MaxRelations {
				complete = false
				break
			}
		}
		if len(relations) >= c.deps.MaxRelations {
			break
		}
	}
	if len(databaseNames) > 0 && successfulListings == 0 && len(listingErrors) > 0 {
		return RemoteCatalogSnapshot{}, errors.Join(listingErrors...)
	}
	sort.Slice(relations, func(i, j int) bool {
		return strings.ToLower(relations[i].QualifiedName) < strings.ToLower(relations[j].QualifiedName)
	})
	return RemoteCatalogSnapshot{
		Relations:  relations,
		Complete:   complete,
		ObservedAt: c.deps.Now(),
	}, nil
}

func (c *RemoteCatalogCache) refreshColumns(parent context.Context, key string, scope RemoteCatalogScope, qualified, flightKey string) {
	ctx, cancel := context.WithTimeout(parent, c.deps.RefreshTimeout)
	defer cancel()
	columns, err := c.deps.DiscoverColumns(ctx, scope.Connection, qualified, scope.Environment)
	if len(columns) > c.deps.MaxColumns {
		columns = columns[:c.deps.MaxColumns]
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entryLocked(key)
	delete(entry.columnFlights, flightKey)
	if err != nil {
		return
	}
	index := remoteCatalogRelationIndex(entry.snapshot.Relations, qualified)
	if index < 0 {
		return
	}
	entry.snapshot.Relations[index].Columns = append([]SQLColumn(nil), columns...)
	entry.snapshot.Relations[index].ColumnsKnown = true
	entry.snapshot.Relations[index].ColumnsObservedAt = c.deps.Now()
}

func (c *RemoteCatalogCache) entryLocked(key string) *remoteCatalogEntry {
	entry := c.data[key]
	if entry == nil {
		entry = &remoteCatalogEntry{
			columnFlights:  map[string]bool{},
			columnAttempts: map[string]time.Time{},
		}
		c.data[key] = entry
	}
	if entry.columnFlights == nil {
		entry.columnFlights = map[string]bool{}
	}
	if entry.columnAttempts == nil {
		entry.columnAttempts = map[string]time.Time{}
	}
	return entry
}

func normalizeRemoteCatalogScope(scope RemoteCatalogScope) RemoteCatalogScope {
	return RemoteCatalogScope{
		Connection:  strings.TrimSpace(scope.Connection),
		Environment: strings.TrimSpace(scope.Environment),
		Database:    strings.TrimSpace(scope.Database),
	}
}

func remoteCatalogScopeKey(scope RemoteCatalogScope) string {
	return strings.ToLower(scope.Connection) + "\x00" + strings.ToLower(scope.Environment) + "\x00" + strings.ToLower(scope.Database)
}

func contextWithoutCancellation(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}

func compactSortedStrings(values []string) []string {
	seen := make(map[string]string, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; !exists {
			seen[key] = trimmed
		}
	}
	result := make([]string, 0, len(seen))
	for _, value := range seen {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i]) < strings.ToLower(result[j]) })
	return result
}

func remoteCatalogRelationIndex(relations []RemoteCatalogRelation, name string) int {
	name = strings.TrimSpace(name)
	if name == "" {
		return -1
	}
	for index := range relations {
		if strings.EqualFold(relations[index].QualifiedName, name) {
			return index
		}
	}
	match := -1
	for index := range relations {
		relation := relations[index]
		schemaQualified := ""
		if schema := strings.TrimSpace(relation.SchemaName); schema != "" {
			if short := strings.TrimSpace(relation.ShortName); short != "" {
				schemaQualified = schema + "." + short
			}
		}
		if !strings.EqualFold(relation.ShortName, name) && !strings.EqualFold(schemaQualified, name) {
			continue
		}
		if match >= 0 {
			return -1
		}
		match = index
	}
	return match
}

func remoteCatalogShortName(name string) string {
	parts := strings.Split(strings.TrimSpace(name), ".")
	return strings.Trim(parts[len(parts)-1], "`\"'")
}

func mergeRemoteCatalogColumns(next *RemoteCatalogSnapshot, previous RemoteCatalogSnapshot) {
	if next == nil || len(previous.Relations) == 0 {
		return
	}
	byName := make(map[string]RemoteCatalogRelation, len(previous.Relations))
	for _, relation := range previous.Relations {
		byName[strings.ToLower(relation.QualifiedName)] = relation
	}
	for index := range next.Relations {
		previousRelation, ok := byName[strings.ToLower(next.Relations[index].QualifiedName)]
		if !ok || !previousRelation.ColumnsKnown {
			continue
		}
		next.Relations[index].Columns = append([]SQLColumn(nil), previousRelation.Columns...)
		next.Relations[index].ColumnsKnown = true
		next.Relations[index].ColumnsObservedAt = previousRelation.ColumnsObservedAt
	}
}

func cloneRemoteCatalogSnapshot(snapshot RemoteCatalogSnapshot) RemoteCatalogSnapshot {
	result := snapshot
	result.Relations = make([]RemoteCatalogRelation, len(snapshot.Relations))
	for index, relation := range snapshot.Relations {
		result.Relations[index] = relation
		result.Relations[index].Columns = append([]SQLColumn(nil), relation.Columns...)
	}
	return result
}
