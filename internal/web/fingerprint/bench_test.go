package fingerprint

import (
	"fmt"
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
)

func benchmarkPipeline(assetCount int) *pipeline.Pipeline {
	assets := make([]*pipeline.Asset, 0, assetCount)
	for i := 0; i < assetCount; i++ {
		name := fmt.Sprintf("asset_%d", i)
		content := fmt.Sprintf(
			"select id, amount, created_at, '%s' as source from upstream_%d where created_at > '2026-01-01' group by 1, 2, 3 order by 3 desc",
			name, i)
		asset := sqlAsset(name, content)
		if i > 0 {
			asset.Upstreams = []pipeline.Upstream{{Type: "asset", Value: fmt.Sprintf("asset_%d", i-1)}}
		}
		assets = append(assets, asset)
	}
	p := testPipeline(assets...)
	return p
}

// BenchmarkDAGColdFormatter measures the first DAG computation, paying the
// native formatter cost for every asset (what a fresh server start pays once
// per pipeline).
func BenchmarkDAGColdFormatter(b *testing.B) {
	p := benchmarkPipeline(20)
	vars := Vars{"region": "eu", "limit": 100}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine := NewEngine() // cold cache each iteration
		if _, err := engine.DAG(p, vars); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDAGWarmFormatter measures recomputes with the content-keyed
// formatter cache populated — the steady-state cost of every staleness
// recompute (saves, run completions, selection switches).
func BenchmarkDAGWarmFormatter(b *testing.B) {
	p := benchmarkPipeline(20)
	vars := Vars{"region": "eu", "limit": 100}
	engine := NewEngine()
	if _, err := engine.DAG(p, vars); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.DAG(p, vars); err != nil {
			b.Fatal(err)
		}
	}
}
