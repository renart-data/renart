package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
)

type JinjaRenderDependencies struct {
	ResolveAssetByID            func(ctx context.Context, assetID string) (string, *pipeline.Pipeline, *pipeline.Asset, error)
	ResolveNotebookJinjaContext func(ctx context.Context, assetID string) (NotebookJinjaContext, bool, error)
}

type JinjaRenderService struct {
	deps JinjaRenderDependencies
}

type JinjaRenderRequest struct {
	Content   string `json:"content"`
	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`
}

type JinjaRenderSpan struct {
	StartLine    int    `json:"start_line"`
	StartColumn  int    `json:"start_column"`
	EndLine      int    `json:"end_line"`
	EndColumn    int    `json:"end_column"`
	Expression   string `json:"expression"`
	RenderedText string `json:"rendered_text"`
	Error        string `json:"error,omitempty"`
}

type JinjaRenderVariable struct {
	Name         string `json:"name"`
	Type         string `json:"type,omitempty"`
	DefaultValue any    `json:"default_value,omitempty"`
	Description  string `json:"description,omitempty"`
}

type JinjaRenderMacro struct {
	Name       string   `json:"name"`
	Parameters []string `json:"parameters"`
}

type JinjaRenderResult struct {
	Status    string                `json:"status"`
	Rendered  string                `json:"rendered,omitempty"`
	Spans     []JinjaRenderSpan     `json:"spans"`
	Variables []JinjaRenderVariable `json:"variables"`
	Macros    []JinjaRenderMacro    `json:"macros"`
	Error     string                `json:"error,omitempty"`
}

func NewJinjaRenderService(deps JinjaRenderDependencies) *JinjaRenderService {
	return &JinjaRenderService{deps: deps}
}

func (s *JinjaRenderService) Render(ctx context.Context, assetID string, req JinjaRenderRequest) (JinjaRenderResult, *APIError) {
	if s.deps.ResolveAssetByID == nil {
		return JinjaRenderResult{}, &APIError{Status: 500, Code: "resolver_missing", Message: "asset resolver is not configured"}
	}

	_, parsed, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return JinjaRenderResult{}, &APIError{Status: 400, Code: "asset_resolve_failed", Message: err.Error()}
	}

	content := req.Content
	if content == "" && asset != nil {
		content = asset.ExecutableFile.Content
	}

	renderer, err := s.previewRenderer(ctx, assetID, parsed, asset, req.StartDate, req.EndDate)
	if err != nil {
		return JinjaRenderResult{}, &APIError{Status: 400, Code: "renderer_failed", Message: err.Error()}
	}

	rendered, renderErr := renderer.Render(content)
	spans := renderJinjaExpressionSpans(renderer, content)

	result := JinjaRenderResult{
		Status:    "ok",
		Spans:     spans,
		Variables: jinjaRenderVariables(parsed),
		Macros:    jinjaRenderMacros(parsed),
	}
	if renderErr != nil {
		result.Status = "error"
		result.Error = renderErr.Error()
		return result, nil
	}

	result.Rendered = rendered
	return result, nil
}

func (s *JinjaRenderService) previewRenderer(
	ctx context.Context,
	assetID string,
	parsed *pipeline.Pipeline,
	asset *pipeline.Asset,
	start string,
	end string,
) (jinja.RendererInterface, error) {
	if s.deps.ResolveNotebookJinjaContext != nil {
		notebookContext, found, err := s.deps.ResolveNotebookJinjaContext(ctx, assetID)
		if err != nil {
			return nil, err
		}
		if found {
			return buildNotebookJinjaPreviewRenderer(start, end, notebookContext)
		}
	}
	return buildJinjaPreviewRenderer(ctx, parsed, asset, start, end)
}

func buildJinjaPreviewRenderer(ctx context.Context, parsed *pipeline.Pipeline, asset *pipeline.Asset, start, end string) (jinja.RendererInterface, error) {
	if parsed == nil || asset == nil {
		return nil, fmt.Errorf("asset or pipeline is missing")
	}

	now := time.Now().UTC()
	timeWindow, err := ResolveExecutionTimeWindow(string(parsed.Schedule), start, end, now)
	if err != nil {
		return nil, err
	}

	macroContent, err := jinja.LoadMacros(afero.NewOsFs(), parsed.MacrosPath)
	if err != nil {
		return nil, err
	}

	renderer := jinja.NewRendererWithStartEndDatesAndMacros(&timeWindow.Start, &timeWindow.End, &now, parsed.Name, "renart-preview", nil, macroContent)
	runCtx := context.WithValue(ctx, pipeline.RunConfigStartDate, timeWindow.Start)
	runCtx = context.WithValue(runCtx, pipeline.RunConfigEndDate, timeWindow.End)
	runCtx = context.WithValue(runCtx, pipeline.RunConfigExecutionDate, now)
	runCtx = context.WithValue(runCtx, pipeline.RunConfigRunID, "renart-preview")
	runCtx = context.WithValue(runCtx, pipeline.RunConfigFullRefresh, false)
	runCtx = context.WithValue(runCtx, config.EnvironmentContextKey, (*config.Environment)(nil))

	return renderer.CloneForAsset(runCtx, parsed, asset)
}

func renderJinjaExpressionSpans(renderer jinja.RendererInterface, content string) []JinjaRenderSpan {
	spans := scanJinjaExpressions(content)
	for i := range spans {
		rendered, err := renderer.Render("{{ " + spans[i].Expression + " }}")
		if err != nil {
			spans[i].Error = err.Error()
			continue
		}
		spans[i].RenderedText = strings.TrimSpace(rendered)
	}
	return spans
}

func scanJinjaExpressions(content string) []JinjaRenderSpan {
	var spans []JinjaRenderSpan
	line, col := 1, 1
	for i := 0; i < len(content); {
		if i+1 < len(content) && content[i] == '{' && content[i+1] == '{' {
			startOffset, startLine, startCol := i, line, col
			i += 2
			col += 2
			innerStart := i
			for i < len(content) {
				if i+1 < len(content) && content[i] == '}' && content[i+1] == '}' {
					inner := strings.TrimSpace(content[innerStart:i])
					i += 2
					col += 2
					spans = append(spans, JinjaRenderSpan{StartLine: startLine, StartColumn: startCol, EndLine: line, EndColumn: col, Expression: inner})
					_ = startOffset
					break
				}
				if content[i] == '\n' {
					line++
					col = 1
				} else {
					col++
				}
				i++
			}
			continue
		}

		if content[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
		i++
	}
	return spans
}

func jinjaRenderVariables(parsed *pipeline.Pipeline) []JinjaRenderVariable {
	if parsed == nil {
		return nil
	}

	result := make([]JinjaRenderVariable, 0, len(parsed.Variables))
	for name, schema := range parsed.Variables {
		variable := JinjaRenderVariable{Name: name, DefaultValue: schema["default"]}
		if typeValue, ok := schema["type"].(string); ok {
			variable.Type = typeValue
		}
		if description, ok := schema["description"].(string); ok {
			variable.Description = description
		}
		result = append(result, variable)
	}
	return result
}

var macroSignatureRE = regexp.MustCompile(`(?s)\{%[-]?\s*macro\s+([A-Za-z_]\w*)\s*\(([^)]*)\)`)

func jinjaRenderMacros(parsed *pipeline.Pipeline) []JinjaRenderMacro {
	if parsed == nil {
		return nil
	}
	var source strings.Builder
	for _, macro := range parsed.Macros {
		source.WriteString(string(macro))
		source.WriteByte('\n')
	}

	matches := macroSignatureRE.FindAllStringSubmatch(source.String(), -1)
	result := make([]JinjaRenderMacro, 0, len(matches))
	for _, match := range matches {
		params := strings.Split(strings.TrimSpace(match[2]), ",")
		cleaned := make([]string, 0, len(params))
		for _, param := range params {
			name := strings.TrimSpace(strings.SplitN(param, "=", 2)[0])
			if name != "" {
				cleaned = append(cleaned, name)
			}
		}
		result = append(result, JinjaRenderMacro{Name: match[1], Parameters: cleaned})
	}
	return result
}
