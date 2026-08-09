package dependencygraph

import (
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func graphPipeline(uuid, id, name string, assets ...*pipeline.Asset) PipelineInput {
	workspaceIDs := make(map[string]string, len(assets))
	for _, asset := range assets {
		workspaceIDs[asset.Name] = id + "/" + asset.Name
	}
	return PipelineInput{
		UUID: uuid,
		ID:   id,
		Name: name,
		Parsed: &pipeline.Pipeline{
			LegacyID: uuid,
			Name:     name,
			Assets:   assets,
		},
		AssetWorkspaceIDs: workspaceIDs,
	}
}

func graphAsset(name, uri string, upstreams ...pipeline.Upstream) *pipeline.Asset {
	return &pipeline.Asset{Name: name, URI: uri, Type: pipeline.AssetTypeDuckDBQuery, Upstreams: upstreams}
}

func TestResolveKeepsAssetNamesLocalAndResolvesURIWorkspaceWide(t *testing.T) {
	t.Parallel()
	leftOrders := graphAsset("orders", "duckdb://warehouse/raw/orders")
	rightOrders := graphAsset("orders", "")
	consumer := graphAsset("daily", "", pipeline.Upstream{
		Type: "uri", Value: leftOrders.URI, Mode: pipeline.UpstreamModeFull,
	})

	graph := Resolve([]PipelineInput{
		graphPipeline("left-uuid", "left", "raw", leftOrders),
		graphPipeline("right-uuid", "right", "analytics", rightOrders, consumer),
	})

	require.Len(t, graph.EdgesByConsumer["right-uuid:daily"], 1)
	edge := graph.EdgesByConsumer["right-uuid:daily"][0]
	assert.True(t, edge.Resolved)
	assert.True(t, edge.CrossPipeline)
	assert.Equal(t, "left-uuid:orders", edge.ProducerID)
	assert.Equal(t, "left/orders", graph.Nodes[edge.ProducerID].WorkspaceAssetID)
	assert.Empty(t, graph.Diagnostics)
}

func TestResolveReportsDuplicateAndUnresolvedURIsByMode(t *testing.T) {
	t.Parallel()
	first := graphAsset("first", "warehouse://orders")
	second := graphAsset("second", "warehouse://orders")
	full := graphAsset("full", "", pipeline.Upstream{Type: "uri", Value: "warehouse://missing", Mode: pipeline.UpstreamModeFull})
	symbolic := graphAsset("symbolic", "", pipeline.Upstream{Type: "uri", Value: "warehouse://symbolic", Mode: pipeline.UpstreamModeSymbolic})
	ambiguous := graphAsset("ambiguous", "", pipeline.Upstream{Type: "uri", Value: "warehouse://orders", Mode: pipeline.UpstreamModeFull})

	graph := Resolve([]PipelineInput{
		graphPipeline("producer-a", "producer-a", "producer-a", first),
		graphPipeline("producer-b", "producer-b", "producer-b", second),
		graphPipeline("consumer", "consumer", "consumer", full, symbolic, ambiguous),
	})

	byCodeAndAsset := make(map[string]Diagnostic)
	for _, diagnostic := range graph.Diagnostics {
		byCodeAndAsset[diagnostic.Code+"/"+diagnostic.AssetID] = diagnostic
	}
	assert.Equal(t, SeverityError, byCodeAndAsset[CodeDuplicateURI+"/producer-a:first"].Severity)
	assert.Equal(t, SeverityError, byCodeAndAsset[CodeDuplicateURI+"/producer-b:second"].Severity)
	assert.Equal(t, SeverityError, byCodeAndAsset[CodeUnresolvedURI+"/consumer:full"].Severity)
	assert.Equal(t, SeverityWarning, byCodeAndAsset[CodeUnresolvedURI+"/consumer:symbolic"].Severity)
	assert.Equal(t, SeverityError, byCodeAndAsset[CodeAmbiguousURI+"/consumer:ambiguous"].Severity)
}

func TestResolveIgnoresSymbolicEdgesForCrossPipelineCycles(t *testing.T) {
	t.Parallel()
	left := graphAsset("left", "warehouse://left", pipeline.Upstream{Type: "uri", Value: "warehouse://right", Mode: pipeline.UpstreamModeFull})
	right := graphAsset("right", "warehouse://right", pipeline.Upstream{Type: "uri", Value: "warehouse://left", Mode: pipeline.UpstreamModeFull})

	graph := Resolve([]PipelineInput{
		graphPipeline("left-pipeline", "left", "left", left),
		graphPipeline("right-pipeline", "right", "right", right),
	})
	assert.Equal(t, 2, diagnosticCount(graph.Diagnostics, CodeCrossPipelineCycle))

	right.Upstreams[0].Mode = pipeline.UpstreamModeSymbolic
	graph = Resolve([]PipelineInput{
		graphPipeline("left-pipeline", "left", "left", left),
		graphPipeline("right-pipeline", "right", "right", right),
	})
	assert.Zero(t, diagnosticCount(graph.Diagnostics, CodeCrossPipelineCycle))
}

func TestResolveRejectsSensorProducers(t *testing.T) {
	t.Parallel()
	sensor := graphAsset("gate", "warehouse://gate")
	sensor.Type = pipeline.AssetTypePostgresQuerySensor
	consumer := graphAsset("consumer", "", pipeline.Upstream{Type: "uri", Value: sensor.URI, Mode: pipeline.UpstreamModeFull})
	graph := Resolve([]PipelineInput{
		graphPipeline("sensor-pipeline", "sensor", "sensor", sensor),
		graphPipeline("consumer-pipeline", "consumer", "consumer", consumer),
	})
	require.Len(t, graph.EdgesByConsumer["consumer-pipeline:consumer"], 1)
	assert.False(t, graph.EdgesByConsumer["consumer-pipeline:consumer"][0].Resolved)
	assert.Equal(t, 1, diagnosticCount(graph.Diagnostics, CodeInvalidProducer))
}

func diagnosticCount(diagnostics []Diagnostic, code string) int {
	count := 0
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			count++
		}
	}
	return count
}
