package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/renart-data/golyglot/pkg/golyglot"

	"renart/internal/web/model"
)

func BenchmarkArtifactColumnLineageWideProjection(b *testing.B) {
	for _, width := range []int{10, 50, 200} {
		definition, consumerColumns, sources := artifactColumnLineageBenchmarkFixture(width)
		benchmarkArtifactColumnLineage(b, fmt.Sprintf("direct_%d", width), width, definition, consumerColumns, sources)
	}

	definition, consumerColumns, sources := artifactColumnLineageBenchmarkFixture(50)
	definition.sql = "with selected as (" + definition.sql + ") select " +
		strings.Join(artifactColumnLineageProjectionNames(50, "selected"), ", ") + " from selected"
	benchmarkArtifactColumnLineage(b, "cte_50", 50, definition, consumerColumns, sources)

	definition, consumerColumns, sources = artifactColumnLineageBenchmarkFixture(200)
	definition.sql = "select * from raw.source"
	benchmarkArtifactColumnLineage(b, "wildcard_200", 200, definition, consumerColumns, sources)
}

func benchmarkArtifactColumnLineage(
	b *testing.B,
	name string,
	width int,
	definition artifactSQLDefinition,
	consumerColumns []model.Column,
	sources []artifactLineageSource,
) {
	b.Run(name, func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			result := sqlArtifactColumnUsages(definition, consumerColumns, sources)
			if len(result) != 1 || len(result[artifactRefKey(sources[0].ref)]) != width {
				b.Fatalf("unexpected lineage result: sources=%d columns=%d", len(result), len(result[artifactRefKey(sources[0].ref)]))
			}
		}
	})
}

func artifactColumnLineageBenchmarkFixture(width int) (
	artifactSQLDefinition,
	[]model.Column,
	[]artifactLineageSource,
) {
	columns := make([]model.Column, 0, width)
	for index := range width {
		name := fmt.Sprintf("column_%03d", index)
		columns = append(columns, model.Column{Name: name, Type: "bigint"})
	}
	ref := model.ArtifactRef{Kind: artifactKindPipelineAsset, ArtifactID: "pipeline:raw.source"}
	return artifactSQLDefinition{
			ref:     model.ArtifactRef{Kind: artifactKindPipelineAsset, ArtifactID: "pipeline:analytics.wide"},
			sql:     "select " + strings.Join(artifactColumnLineageProjectionNames(width, "source"), ", ") + " from raw.source source",
			dialect: golyglot.DialectPostgreSQL,
		}, columns, []artifactLineageSource{{
			ref: ref, relationNames: []string{"raw.source"}, columns: columns,
		}}
}

func artifactColumnLineageProjectionNames(width int, qualifier string) []string {
	projections := make([]string, 0, width)
	for index := range width {
		projections = append(projections, qualifier+"."+fmt.Sprintf("column_%03d", index))
	}
	return projections
}
