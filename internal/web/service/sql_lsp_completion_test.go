package service

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"renart/internal/sqllsp"
	"renart/internal/web/model"
)

const nestedFunctionCompletionSQL = `SELECT
    event_id,
    magnitude,
    place,
    observed_at_ms,
    significance,
    detail_url,
    round(round(/*cursor*/))
FROM earthquakes.events
WHERE magnitude >= {{ var.notable_magnitude }}
ORDER BY magnitude DESC, observed_at_ms DESC`

func nestedFunctionCompletionService(t testing.TB) (*SQLLSPService, SQLLSPRequest) {
	t.Helper()
	parsed, root := writeTypeCheckWorkspace(t, `name: earthquakes
schedule: hourly
variables:
  notable_magnitude:
    type: integer
    default: 5
`, map[string]string{
		"events.sql": `/* @bruin
name: earthquakes.events
type: duckdb.sql
@bruin */
SELECT 'event' AS event_id, 4.2 AS magnitude, 'place' AS place,
       1000::BIGINT AS observed_at_ms, 42 AS significance, 'url' AS detail_url`,
		"notable_events.sql": `/* @bruin
name: earthquakes.notable_events
type: duckdb.sql
@bruin */
` + strings.ReplaceAll(nestedFunctionCompletionSQL, "/*cursor*/", "magnitude"),
	})
	state := model.WorkspaceState{Revision: 1, Pipelines: []model.Pipeline{{ID: "earthquakes", Name: "earthquakes"}}}
	var assetID string
	for _, asset := range parsed.Assets {
		id := assetReportID(root, asset)
		state.Pipelines[0].Assets = append(state.Pipelines[0].Assets, model.Asset{
			ID: id, Name: asset.Name, Type: string(asset.Type),
			Path: asset.ExecutableFile.Path, Content: asset.ExecutableFile.Content,
		})
		if asset.Name == "earthquakes.notable_events" {
			assetID = id
		}
	}
	require.NotEmpty(t, assetID)
	workspace := NewWorkspaceService(root, "")
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot:    root,
		CurrentState:     func() model.WorkspaceState { return state },
		ResolveAssetByID: workspace.ResolveAssetByID,
	})
	prefix, suffix, _ := strings.Cut(nestedFunctionCompletionSQL, "/*cursor*/")
	content := prefix + suffix
	return service, SQLLSPRequest{AssetID: assetID, Content: content, Position: sqllsp.PositionAt(content, len(prefix))}
}

func TestSQLLSPNestedFunctionCompletionsWithPipelineVariable(t *testing.T) {
	service, request := nestedFunctionCompletionService(t)
	response, apiErr := service.Completions(context.Background(), request)
	require.Nil(t, apiErr)
	var column, function bool
	for _, item := range response.Completions {
		column = column || item.Kind == 5 && item.Label == "magnitude"
		function = function || item.Kind == 3 && item.Label == "round"
	}
	require.True(t, column, "missing magnitude column")
	require.True(t, function, "missing round function")
}

func TestSQLLSPPythonQueryUsesRuntimeDialect(t *testing.T) {
	for _, tc := range []struct {
		name, ownConnection, override, want string
		notebook                            bool
	}{
		{"pipeline default DuckDB", "local", "", "read_parquet", false},
		{"pipeline default PostgreSQL", "pg", "", "generate_series", false},
		{"explicit connection wins", "local", "pg", "generate_series", false},
		{"unknown explicit connection stays generic", "local", "missing", "", false},
		{"unknown connection stays generic", "missing", "", "", false},
		{"notebook local session", "", "", "read_parquet", true},
		{"notebook output is not query connection", "pg", "", "read_parquet", true},
		{"notebook explicit warehouse", "", "pg", "generate_series", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asset := model.Asset{ID: "py", Name: "reader", Path: "reader.py", Type: "python", Connection: tc.ownConnection}
			state := model.WorkspaceState{Revision: 1, Connections: map[string]string{"local": "duckdb", "pg": "postgres"}}
			if tc.notebook {
				state.Notebooks = []model.Notebook{{ID: "notebook", Cells: []model.Asset{asset}}}
			} else {
				state.Pipelines = []model.Pipeline{{ID: "pipeline", Assets: []model.Asset{asset}}}
			}
			service := NewSQLLSPService(SQLLSPDependencies{WorkspaceRoot: t.TempDir(), CurrentState: func() model.WorkspaceState { return state }})
			response, apiErr := service.Completions(context.Background(), SQLLSPRequest{
				AssetID: "py", DocumentContext: "python_query", Connection: tc.override,
				Content: "SELECT * FROM ", Position: sqllsp.Position{Character: len("SELECT * FROM ")},
			})
			require.Nil(t, apiErr)
			var names []string
			for _, item := range response.Completions {
				if item.Kind == 3 {
					names = append(names, item.Label)
				}
			}
			if tc.want == "" {
				require.Empty(t, names)
			} else {
				require.Contains(t, names, tc.want)
			}
			// Request-specific dialect selection must not rewrite the workspace.
			for _, node := range service.graphForState(context.Background(), state).Assets {
				require.Empty(t, node.Dialect)
			}
		})
	}
}

func BenchmarkSQLLSPNestedFunctionCompletions(b *testing.B) {
	service, request := nestedFunctionCompletionService(b)
	_, apiErr := service.Completions(context.Background(), request)
	require.Nil(b, apiErr)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, apiErr := service.Completions(context.Background(), request)
		if apiErr != nil {
			b.Fatal(apiErr)
		}
	}
}

// A single editor change requests completion, hover, tokens and diagnostics at
// nearly the same time. A cold revision must not infer the workspace for each.
func completionBurstState() model.WorkspaceState {
	state := sqlLSPCachingState(1, "")
	for i := 0; i < 100; i++ {
		state.Pipelines[0].Assets = append(state.Pipelines[0].Assets, model.Asset{
			ID: fmt.Sprintf("source-%d", i), Name: fmt.Sprintf("analytics.source_%d", i),
			Path: fmt.Sprintf("analytics/assets/source_%d.sql", i), Type: "duckdb.sql",
			Content: fmt.Sprintf("WITH data AS (SELECT %d AS id, 'sample' AS label) SELECT * FROM data", i),
		})
	}
	return state
}

func completeBurst(service *SQLLSPService, workers int) {
	start := make(chan struct{})
	var group sync.WaitGroup
	for i := 0; i < workers; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			service.graphForState(context.Background(), service.deps.CurrentState())
		}()
	}
	close(start)
	group.Wait()
}

func TestSQLLSPServiceCoalescesConcurrentGraphBuilds(t *testing.T) {
	previous := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(previous)
	state := completionBurstState()
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(), CurrentState: func() model.WorkspaceState { return state },
	})
	completeBurst(service, 6)
	require.Equal(t, int64(1), service.buildCount.Load(), "one revision must be inferred once, even for concurrent editor requests")
}

func TestSQLLSPServiceDoesNotCacheCanceledGraph(t *testing.T) {
	state := completionBurstState()
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(), CurrentState: func() model.WorkspaceState { return state },
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service.graphForState(ctx, state)
	graph := service.graphForState(context.Background(), state)
	engine := sqllsp.NewEngine(graph)
	const query = "SELECT source. FROM analytics.source_0 source"
	items := engine.Complete(sqllsp.TextDocumentItem{Text: query}, sqllsp.Position{Character: len("SELECT source.")})
	var found bool
	for _, item := range items {
		found = found || item.Label == "label"
	}
	require.True(t, found, "an abandoned request must not cache an incomplete inferred schema")
}

func TestSQLLSPGraphWaiterCanCancelIndependently(t *testing.T) {
	state := sqlLSPCachingState(1, "")
	entry := &sqlLSPGraphBuild{done: make(chan struct{})}
	service := NewSQLLSPService(SQLLSPDependencies{WorkspaceRoot: t.TempDir()})
	service.graphBuilds = map[int64]*sqlLSPGraphBuild{1: entry}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		service.graphForState(ctx, state)
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled completion kept waiting for another request's graph")
	}
	require.Same(t, entry, service.graphBuilds[1], "a waiter's cancellation must not cancel the shared build")
	require.Zero(t, service.buildCount.Load())
}

func TestSQLLSPOlderGraphDoesNotReplaceNewerRevision(t *testing.T) {
	service := NewSQLLSPService(SQLLSPDependencies{WorkspaceRoot: t.TempDir()})
	service.graphForState(context.Background(), sqlLSPCachingState(2, "new_column"))
	service.graphForState(context.Background(), sqlLSPCachingState(1, ""))
	service.graphForState(context.Background(), sqlLSPCachingState(2, "new_column"))
	require.Equal(t, int64(2), service.cachedRevision)
	require.Equal(t, int64(2), service.buildCount.Load(), "an older request must not evict the newer cached graph")
}

func BenchmarkSQLLSPColdGraphBurst(b *testing.B) {
	for _, workers := range []int{1, 6} {
		b.Run(fmt.Sprintf("requests=%d", workers), func(b *testing.B) {
			state := completionBurstState()
			service := NewSQLLSPService(SQLLSPDependencies{
				WorkspaceRoot: b.TempDir(), CurrentState: func() model.WorkspaceState { return state },
			})
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				state.Revision++
				completeBurst(service, workers)
			}
			b.ReportMetric(float64(service.buildCount.Load())/float64(b.N), "builds/op")
		})
	}
}
