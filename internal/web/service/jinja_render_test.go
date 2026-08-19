package service

import (
	"context"
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"renart/internal/web/presentation"
)

func TestJinjaRenderUsesNotebookParameterContext(t *testing.T) {
	asset := &pipeline.Asset{Type: pipeline.AssetType("duckdb.sql")}
	parsed := &pipeline.Pipeline{Name: "synthetic-notebook", Assets: []*pipeline.Asset{asset}}
	service := NewJinjaRenderService(JinjaRenderDependencies{
		ResolveAssetByID: func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "notebooks/demo/query.sql", parsed, asset, nil
		},
		ResolveNotebookJinjaContext: func(context.Context, string) (NotebookJinjaContext, bool, error) {
			return NotebookJinjaContext{
				Definitions: []presentation.ParameterDefinition{
					{ID: "region", Type: presentation.ParameterTypeText, Default: "eu"},
					{ID: "enabled", Type: presentation.ParameterTypeBoolean, Default: false},
				},
				Values: map[string]any{"region": "north'west", "enabled": true},
			}, true, nil
		},
	})

	result, apiErr := service.Render(context.Background(), "cell-id", JinjaRenderRequest{
		Content: "select {{ parameter.region }} as region{% if parameters.enabled %}, 1 as enabled{% endif %}",
	})
	if apiErr != nil {
		t.Fatalf("render returned API error: %+v", apiErr)
	}
	if result.Status != "ok" {
		t.Fatalf("render failed: %+v", result)
	}
	if !strings.Contains(result.Rendered, "'north''west'") || !strings.Contains(result.Rendered, "1 as enabled") {
		t.Fatalf("notebook parameter context was not rendered: %q", result.Rendered)
	}
	if len(result.Spans) != 1 || result.Spans[0].RenderedText != "'north''west'" {
		t.Fatalf("unexpected expression previews: %+v", result.Spans)
	}
}
