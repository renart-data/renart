// Package dependencygraph resolves Bruin dependencies across every pipeline in
// one Renart workspace. Asset names remain pipeline-local; URI producers are
// indexed workspace-wide. The resulting graph is immutable after Resolve and
// is the shared identity contract for API lineage, diagnostics, fingerprints,
// and execution prerequisites.
package dependencygraph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"

	"renart/internal/web/identity"
)

const (
	CodeDuplicateURI       = "cross-pipeline-duplicate-uri"
	CodeAmbiguousURI       = "cross-pipeline-ambiguous-uri"
	CodeUnresolvedURI      = "cross-pipeline-unresolved-uri"
	CodeInvalidProducer    = "cross-pipeline-invalid-producer"
	CodeCrossPipelineCycle = "cross-pipeline-cycle"
)

type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// PipelineInput binds one parsed Bruin pipeline to its workspace-facing IDs.
// AssetWorkspaceIDs maps the pipeline-local asset name to the encoded file ID
// used by Renart's HTTP and browser routes.
type PipelineInput struct {
	UUID              string
	ID                string
	Name              string
	Path              string
	Parsed            *pipeline.Pipeline
	AssetWorkspaceIDs map[string]string
}

type Node struct {
	PipelineUUID     string
	PipelineID       string
	PipelineName     string
	PipelinePath     string
	AssetName        string
	AssetID          string
	WorkspaceAssetID string
	URI              string
	AssetType        string
	Asset            *pipeline.Asset
	Pipeline         *pipeline.Pipeline
}

type Edge struct {
	ConsumerID    string
	ProducerID    string
	Type          string
	Value         string
	Mode          pipeline.UpstreamMode
	Resolved      bool
	CrossPipeline bool
}

type Diagnostic struct {
	AssetID          string
	WorkspaceAssetID string
	PipelineID       string
	Code             string
	Severity         Severity
	Message          string
}

type Graph struct {
	Revision        string
	Nodes           map[string]*Node
	ByWorkspaceID   map[string]*Node
	URIProducers    map[string][]*Node
	Edges           []Edge
	EdgesByConsumer map[string][]Edge
	ReverseEdges    map[string][]Edge
	Diagnostics     []Diagnostic
}

func Resolve(inputs []PipelineInput) Graph {
	graph := Graph{
		Nodes:           make(map[string]*Node),
		ByWorkspaceID:   make(map[string]*Node),
		URIProducers:    make(map[string][]*Node),
		EdgesByConsumer: make(map[string][]Edge),
		ReverseEdges:    make(map[string][]Edge),
	}
	orderedInputs := append([]PipelineInput(nil), inputs...)
	sort.SliceStable(orderedInputs, func(i, j int) bool {
		if orderedInputs[i].UUID != orderedInputs[j].UUID {
			return orderedInputs[i].UUID < orderedInputs[j].UUID
		}
		return orderedInputs[i].ID < orderedInputs[j].ID
	})

	for _, input := range orderedInputs {
		if input.Parsed == nil {
			continue
		}
		pipelineUUID := strings.TrimSpace(input.UUID)
		if pipelineUUID == "" {
			pipelineUUID = strings.TrimSpace(input.Parsed.LegacyID)
		}
		assets := append([]*pipeline.Asset(nil), input.Parsed.Assets...)
		sort.SliceStable(assets, func(i, j int) bool {
			if assets[i] == nil {
				return false
			}
			if assets[j] == nil {
				return true
			}
			return assets[i].Name < assets[j].Name
		})
		for _, asset := range assets {
			if asset == nil || strings.TrimSpace(asset.Name) == "" {
				continue
			}
			assetID := identity.AssetID(pipelineUUID, asset.Name)
			node := &Node{
				PipelineUUID:     pipelineUUID,
				PipelineID:       input.ID,
				PipelineName:     input.Name,
				PipelinePath:     input.Path,
				AssetName:        asset.Name,
				AssetID:          assetID,
				WorkspaceAssetID: input.AssetWorkspaceIDs[asset.Name],
				URI:              strings.TrimSpace(asset.URI),
				AssetType:        string(asset.Type),
				Asset:            asset,
				Pipeline:         input.Parsed,
			}
			graph.Nodes[assetID] = node
			if node.WorkspaceAssetID != "" {
				graph.ByWorkspaceID[node.WorkspaceAssetID] = node
			}
			if node.URI != "" {
				graph.URIProducers[node.URI] = append(graph.URIProducers[node.URI], node)
			}
		}
	}

	for uri, producers := range graph.URIProducers {
		sortNodes(producers)
		if len(producers) < 2 {
			continue
		}
		names := make([]string, 0, len(producers))
		for _, producer := range producers {
			names = append(names, nodeLabel(producer))
		}
		message := fmt.Sprintf("URI %q is declared by multiple assets: %s", uri, strings.Join(names, ", "))
		for _, producer := range producers {
			graph.addDiagnostic(producer, CodeDuplicateURI, SeverityError, message)
		}
	}

	nodes := make([]*Node, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		nodes = append(nodes, node)
	}
	sortNodes(nodes)
	for _, consumer := range nodes {
		for _, upstream := range consumer.Asset.Upstreams {
			edge := Edge{
				ConsumerID: consumer.AssetID,
				Type:       normalizedDependencyType(upstream.Type),
				Value:      strings.TrimSpace(upstream.Value),
				Mode:       normalizedMode(upstream.Mode),
			}
			switch edge.Type {
			case "uri":
				graph.resolveURIEdge(consumer, &edge)
			case "asset":
				producerID := identity.AssetID(consumer.PipelineUUID, edge.Value)
				if producer := graph.Nodes[producerID]; producer != nil {
					edge.ProducerID = producer.AssetID
					edge.Resolved = true
				}
			}
			graph.Edges = append(graph.Edges, edge)
			graph.EdgesByConsumer[edge.ConsumerID] = append(graph.EdgesByConsumer[edge.ConsumerID], edge)
			if edge.Resolved {
				graph.ReverseEdges[edge.ProducerID] = append(graph.ReverseEdges[edge.ProducerID], edge)
			}
		}
	}

	graph.addCrossPipelineCycleDiagnostics()
	graph.sort()
	graph.Revision = graphHash(graph)
	return graph
}

func (g *Graph) resolveURIEdge(consumer *Node, edge *Edge) {
	if edge.Value == "" {
		g.addDiagnostic(consumer, CodeUnresolvedURI, severityForMode(edge.Mode), "URI dependency cannot be empty")
		return
	}
	producers := g.URIProducers[edge.Value]
	switch len(producers) {
	case 0:
		g.addDiagnostic(
			consumer,
			CodeUnresolvedURI,
			severityForMode(edge.Mode),
			fmt.Sprintf("Cross-pipeline URI dependency %q does not resolve to an asset in this workspace", edge.Value),
		)
		return
	case 1:
		producer := producers[0]
		if isSensorType(producer.AssetType) {
			g.addDiagnostic(
				consumer,
				CodeInvalidProducer,
				severityForMode(edge.Mode),
				fmt.Sprintf("URI %q resolves to sensor %s; sensors cannot provide materialization coverage", edge.Value, nodeLabel(producer)),
			)
			return
		}
		edge.ProducerID = producer.AssetID
		edge.Resolved = true
		edge.CrossPipeline = producer.PipelineUUID != consumer.PipelineUUID
	default:
		names := make([]string, 0, len(producers))
		for _, producer := range producers {
			names = append(names, nodeLabel(producer))
		}
		g.addDiagnostic(
			consumer,
			CodeAmbiguousURI,
			severityForMode(edge.Mode),
			fmt.Sprintf("Cross-pipeline URI dependency %q is ambiguous: %s", edge.Value, strings.Join(names, ", ")),
		)
	}
}

func (g *Graph) addCrossPipelineCycleDiagnostics() {
	adjacency := make(map[string][]string, len(g.Nodes))
	for _, edge := range g.Edges {
		if !edge.Resolved || edge.Mode == pipeline.UpstreamModeSymbolic {
			continue
		}
		adjacency[edge.ConsumerID] = append(adjacency[edge.ConsumerID], edge.ProducerID)
	}
	for nodeID := range adjacency {
		sort.Strings(adjacency[nodeID])
	}
	for _, component := range stronglyConnectedComponents(g.Nodes, adjacency) {
		if len(component) < 2 {
			continue
		}
		pipelines := make(map[string]struct{})
		labels := make([]string, 0, len(component))
		for _, nodeID := range component {
			node := g.Nodes[nodeID]
			pipelines[node.PipelineUUID] = struct{}{}
			labels = append(labels, nodeLabel(node))
		}
		if len(pipelines) < 2 {
			continue
		}
		sort.Strings(labels)
		message := "Full dependencies form a cross-pipeline cycle: " + strings.Join(labels, " -> ")
		for _, nodeID := range component {
			g.addDiagnostic(g.Nodes[nodeID], CodeCrossPipelineCycle, SeverityError, message)
		}
	}
}

func stronglyConnectedComponents(nodes map[string]*Node, adjacency map[string][]string) [][]string {
	index := 0
	indices := make(map[string]int, len(nodes))
	lowLink := make(map[string]int, len(nodes))
	onStack := make(map[string]bool, len(nodes))
	stack := make([]string, 0, len(nodes))
	components := make([][]string, 0)
	orderedIDs := make([]string, 0, len(nodes))
	for nodeID := range nodes {
		orderedIDs = append(orderedIDs, nodeID)
	}
	sort.Strings(orderedIDs)
	for _, nodeID := range orderedIDs {
		indices[nodeID] = -1
	}

	var visit func(string)
	visit = func(nodeID string) {
		indices[nodeID] = index
		lowLink[nodeID] = index
		index++
		stack = append(stack, nodeID)
		onStack[nodeID] = true

		for _, next := range adjacency[nodeID] {
			if indices[next] == -1 {
				visit(next)
				lowLink[nodeID] = min(lowLink[nodeID], lowLink[next])
			} else if onStack[next] {
				lowLink[nodeID] = min(lowLink[nodeID], indices[next])
			}
		}
		if lowLink[nodeID] != indices[nodeID] {
			return
		}
		component := make([]string, 0)
		for len(stack) > 0 {
			last := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[last] = false
			component = append(component, last)
			if last == nodeID {
				break
			}
		}
		sort.Strings(component)
		components = append(components, component)
	}

	for _, nodeID := range orderedIDs {
		if indices[nodeID] == -1 {
			visit(nodeID)
		}
	}
	return components
}

func (g *Graph) addDiagnostic(node *Node, code string, severity Severity, message string) {
	if node == nil {
		return
	}
	g.Diagnostics = append(g.Diagnostics, Diagnostic{
		AssetID:          node.AssetID,
		WorkspaceAssetID: node.WorkspaceAssetID,
		PipelineID:       node.PipelineID,
		Code:             code,
		Severity:         severity,
		Message:          message,
	})
}

func (g *Graph) sort() {
	sort.SliceStable(g.Edges, func(i, j int) bool { return edgeKey(g.Edges[i]) < edgeKey(g.Edges[j]) })
	for consumerID := range g.EdgesByConsumer {
		sort.SliceStable(g.EdgesByConsumer[consumerID], func(i, j int) bool {
			return edgeKey(g.EdgesByConsumer[consumerID][i]) < edgeKey(g.EdgesByConsumer[consumerID][j])
		})
	}
	for producerID := range g.ReverseEdges {
		sort.SliceStable(g.ReverseEdges[producerID], func(i, j int) bool {
			return edgeKey(g.ReverseEdges[producerID][i]) < edgeKey(g.ReverseEdges[producerID][j])
		})
	}
	sort.SliceStable(g.Diagnostics, func(i, j int) bool {
		left, right := g.Diagnostics[i], g.Diagnostics[j]
		if left.AssetID != right.AssetID {
			return left.AssetID < right.AssetID
		}
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		return left.Message < right.Message
	})
}

func graphHash(graph Graph) string {
	type nodeHash struct {
		AssetID          string `json:"asset_id"`
		WorkspaceAssetID string `json:"workspace_asset_id,omitempty"`
		PipelineID       string `json:"pipeline_id"`
		URI              string `json:"uri,omitempty"`
		AssetType        string `json:"asset_type"`
	}
	type edgeHash struct {
		ConsumerID string `json:"consumer_id"`
		ProducerID string `json:"producer_id,omitempty"`
		Type       string `json:"type"`
		Value      string `json:"value"`
		Mode       string `json:"mode"`
	}
	nodes := make([]nodeHash, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		nodes = append(nodes, nodeHash{
			AssetID: node.AssetID, WorkspaceAssetID: node.WorkspaceAssetID,
			PipelineID: node.PipelineID, URI: node.URI, AssetType: node.AssetType,
		})
	}
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].AssetID < nodes[j].AssetID })
	edges := make([]edgeHash, 0, len(graph.Edges))
	for _, edge := range graph.Edges {
		edges = append(edges, edgeHash{
			ConsumerID: edge.ConsumerID, ProducerID: edge.ProducerID,
			Type: edge.Type, Value: edge.Value, Mode: edge.Mode.String(),
		})
	}
	encoded, _ := json.Marshal(struct {
		Nodes []nodeHash `json:"nodes"`
		Edges []edgeHash `json:"edges"`
	}{Nodes: nodes, Edges: edges})
	digest := sha256.Sum256(encoded)
	return "renart-dependency-graph-v1:" + hex.EncodeToString(digest[:])
}

func normalizedDependencyType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "asset"
	}
	return value
}

func normalizedMode(mode pipeline.UpstreamMode) pipeline.UpstreamMode {
	if mode == pipeline.UpstreamModeSymbolic {
		return mode
	}
	return pipeline.UpstreamModeFull
}

func severityForMode(mode pipeline.UpstreamMode) Severity {
	if mode == pipeline.UpstreamModeSymbolic {
		return SeverityWarning
	}
	return SeverityError
}

func isSensorType(assetType string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(assetType)), ".sensor.")
}

func nodeLabel(node *Node) string {
	if node == nil {
		return "unknown asset"
	}
	pipelineName := strings.TrimSpace(node.PipelineName)
	if pipelineName == "" {
		pipelineName = strings.TrimSpace(node.PipelinePath)
	}
	if pipelineName == "" {
		return node.AssetName
	}
	return pipelineName + "/" + node.AssetName
}

func sortNodes(nodes []*Node) {
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].PipelineID != nodes[j].PipelineID {
			return nodes[i].PipelineID < nodes[j].PipelineID
		}
		return nodes[i].AssetName < nodes[j].AssetName
	})
}

func edgeKey(edge Edge) string {
	return edge.ConsumerID + "\x00" + edge.Type + "\x00" + edge.Value + "\x00" + edge.Mode.String() + "\x00" + edge.ProducerID
}
