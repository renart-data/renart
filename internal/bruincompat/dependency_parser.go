package bruincompat

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/pipeline"

	"renart/internal/sqlintelligence"
)

// DependencyParser supplies the subset of Bruin's parser contract Renart uses
// for dependency reconciliation, backed by the shared pure-Go Golyglot parser.
type DependencyParser struct {
	ctx context.Context
}

func NewDependencyParser(ctx context.Context) *DependencyParser {
	if ctx == nil {
		ctx = context.Background()
	}
	return &DependencyParser{ctx: ctx}
}

func (p *DependencyParser) Start() error { return nil }

func (p *DependencyParser) Close() error { return nil }

func (p *DependencyParser) UsedTables(query, dialect string) ([]string, error) {
	return sqlintelligence.UsedTablesContext(p.ctx, query, dialect)
}

func (p *DependencyParser) GetMissingDependenciesForAsset(asset *pipeline.Asset, pl *pipeline.Pipeline, renderer jinja.RendererInterface) ([]string, error) {
	if err := p.Start(); err != nil {
		return []string{}, fmt.Errorf("failed to start SQL parser: %w", err)
	}

	dialect, err := AssetTypeToDialect(asset.Type)
	if err != nil {
		return []string{}, nil
	}

	renderedQuery, err := renderer.Render(mergeMacrosWithQuery(asset.ExecutableFile.Content, pl.Macros))
	if err != nil {
		return []string{}, fmt.Errorf("failed to render the query before parsing the SQL")
	}

	tables, err := p.UsedTables(renderedQuery, dialect)
	if err != nil {
		return []string{}, fmt.Errorf("failed to get used tables: %w", err)
	}
	if len(tables) == 0 && len(asset.Upstreams) == 0 {
		return []string{}, nil
	}

	pipelineAssetNames := make(map[string]bool, len(pl.Assets))
	for _, candidate := range pl.Assets {
		pipelineAssetNames[strings.ToLower(candidate.Name)] = true
	}

	usedTableNames := make(map[string]string, len(tables))
	for _, table := range tables {
		usedTableNames[strings.ToLower(table)] = table
	}

	dependencyNames := make(map[string]bool, len(asset.Upstreams))
	for _, upstream := range asset.Upstreams {
		if upstream.Type == "asset" {
			dependencyNames[strings.ToLower(upstream.Value)] = true
		}
	}

	normalizedTables := make([]string, 0, len(usedTableNames))
	for normalized := range usedTableNames {
		normalizedTables = append(normalizedTables, normalized)
	}
	sort.Strings(normalizedTables)

	missing := make([]string, 0)
	for _, normalized := range normalizedTables {
		actual := usedTableNames[normalized]
		if normalized == asset.Name || actual == asset.Name || dependencyNames[normalized] || !pipelineAssetNames[normalized] {
			continue
		}
		missing = append(missing, actual)
	}
	return missing, nil
}

func mergeMacrosWithQuery(query string, macros []pipeline.Macro) string {
	if len(macros) == 0 {
		return query
	}

	var builder strings.Builder
	for _, macro := range macros {
		builder.WriteString(string(macro))
		builder.WriteByte('\n')
	}
	builder.WriteByte('\n')
	builder.WriteString(query)
	return builder.String()
}
