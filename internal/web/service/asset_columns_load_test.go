package service

import (
	"context"
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
)

func TestResolveLoadSourceAssetBySourceTable(t *testing.T) {
	source := &pipeline.Asset{
		Name:    "raw.users",
		Columns: []pipeline.Column{{Name: "id", Type: "INTEGER"}, {Name: "email", Type: "VARCHAR"}},
	}
	other := &pipeline.Asset{Name: "raw.orders"}
	loadAsset := &pipeline.Asset{
		Name:       "staging.users",
		Type:       "load",
		Parameters: pipeline.ParameterMap{"source_table": "raw.users"},
	}
	pl := &pipeline.Pipeline{Assets: []*pipeline.Asset{source, other, loadAsset}}

	got := resolveLoadSourceAsset(pl, loadAsset)
	if got != source {
		t.Fatalf("expected source raw.users, got %+v", got)
	}
}

func TestResolveLoadSourceAssetBySingleUpstream(t *testing.T) {
	source := &pipeline.Asset{
		Name:    "raw.events",
		Columns: []pipeline.Column{{Name: "ts", Type: "TIMESTAMP"}},
	}
	loadAsset := &pipeline.Asset{
		Name:      "staging.events",
		Type:      "load",
		Upstreams: []pipeline.Upstream{{Type: "asset", Value: "raw.events"}},
	}
	pl := &pipeline.Pipeline{Assets: []*pipeline.Asset{source, loadAsset}}

	if got := resolveLoadSourceAsset(pl, loadAsset); got != source {
		t.Fatalf("expected source raw.events, got %+v", got)
	}
}

func TestInferLoadColumnsFromUpstream(t *testing.T) {
	source := &pipeline.Asset{
		Name:    "raw.users",
		Columns: []pipeline.Column{{Name: "id", Type: "INTEGER"}, {Name: "", Type: "skip"}, {Name: "email", Type: "VARCHAR"}},
	}
	loadAsset := &pipeline.Asset{
		Name:       "staging.users",
		Type:       "load",
		Parameters: pipeline.ParameterMap{"source_table": "raw.users"},
	}
	pl := &pipeline.Pipeline{Assets: []*pipeline.Asset{source, loadAsset}}

	columns := newAssetDefinitionSchemaResolver(pl).Available(context.Background(), loadAsset)
	if len(columns) != 2 {
		t.Fatalf("expected 2 columns (blank-name skipped), got %d: %+v", len(columns), columns)
	}
	if columns[0].Name != "id" || columns[1].Name != "email" {
		t.Errorf("unexpected columns: %+v", columns)
	}
}

func TestInferLoadColumnsFromUpstreamNoSource(t *testing.T) {
	loadAsset := &pipeline.Asset{Name: "staging.users", Type: "load"}
	pl := &pipeline.Pipeline{Assets: []*pipeline.Asset{loadAsset}}
	if columns := newAssetDefinitionSchemaResolver(pl).Available(context.Background(), loadAsset); len(columns) != 0 {
		t.Fatalf("expected no schema when no source asset can be resolved, got %+v", columns)
	}
}
