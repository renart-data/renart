package notebookmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"renart/internal/web/model"
	"renart/internal/web/notebook"
	"renart/internal/web/service"
)

const (
	maxBlockContentBytes = 256 << 10
	maxSampleRows        = 50
	maxSampleBytes       = 64 << 10
	maxSampleValueBytes  = 8 << 10
)

// Server owns the protocol adapter and its process-local prepared edits and
// asynchronous run registry. It never owns authored notebook state.
type Server struct {
	backend Backend
	mcp     *mcp.Server
	changes *changeStore
	runs    *runStore
	policy  Policy
}

// Policy narrows an MCP server to the capabilities granted by its launcher.
// The zero value preserves the public, workspace-wide MCP command's existing
// behavior. Native notebook chat uses an explicit policy so an agent cannot
// cross the selected notebook or gain write/run tools while in Ask mode.
type Policy struct {
	NotebookID string
	ReadOnly   bool
	NoRuns     bool
}

// New constructs a single-workspace notebook MCP server.
func New(ctx context.Context, backend Backend, version string, logger *slog.Logger, policies ...Policy) *Server {
	if strings.TrimSpace(version) == "" {
		version = "dev"
	}
	var policy Policy
	if len(policies) > 0 {
		policy = policies[0]
		policy.NotebookID = strings.TrimSpace(policy.NotebookID)
	}
	protocol := mcp.NewServer(
		&mcp.Implementation{Name: "renart-notebooks", Version: version},
		&mcp.ServerOptions{
			Instructions: "Use only these semantic tools to inspect, edit, and run Renart notebooks. Prepare and validate a change before applying it. Never infer filesystem paths or credentials from notebook metadata.",
			Logger:       logger,
			Capabilities: &mcp.ServerCapabilities{},
		},
	)
	server := &Server{
		backend: backend,
		mcp:     protocol,
		changes: newChangeStore(),
		runs:    newRunStore(ctx, backend),
		policy:  policy,
	}
	server.registerTools()
	return server
}

// Protocol exposes the SDK server so cmd and protocol tests can choose a
// transport without gaining access to the notebook backend.
func (s *Server) Protocol() *mcp.Server { return s.mcp }

func (s *Server) registerTools() {
	mcp.AddTool(s.mcp, readTool("list_notebooks", "List notebooks in this Renart workspace."), s.listNotebooks)
	mcp.AddTool(s.mcp, readTool("get_notebook_outline", "Get ordered block identities and a notebook-wide revision."), s.getOutline)
	mcp.AddTool(s.mcp, readTool("get_notebook_block", "Read one notebook block by durable ID. Source credentials are omitted."), s.getBlock)
	mcp.AddTool(s.mcp, readTool("get_notebook_graph", "Get notebook dataflow and presentation dependencies by durable ID."), s.getGraph)
	mcp.AddTool(s.mcp, readTool("get_notebook_diagnostics", "Get structural, parse, and visualization diagnostics for a notebook."), s.getDiagnostics)
	mcp.AddTool(s.mcp, readTool("get_notebook_result_schema", "Get the last observed schema and completeness for one notebook cell."), s.getResultSchema)
	mcp.AddTool(s.mcp, readTool("get_notebook_result_sample", "Get at most 50 rows and 64 KiB from a previously produced notebook result."), s.getResultSample)
	mcp.AddTool(s.mcp, readTool("list_notebook_sources", "List credential-free source definitions, schemas, and snapshot provenance."), s.listSources)

	if !s.policy.ReadOnly {
		mcp.AddTool(s.mcp, readTool("prepare_notebook_change_set", "Normalize and stage a bounded semantic notebook change without writing files."), s.prepareChangeSet)
		mcp.AddTool(s.mcp, readTool("validate_notebook_change_set", "Revalidate a prepared notebook change against the current filesystem revision."), s.validateChangeSet)
		mcp.AddTool(s.mcp, changeTool("apply_notebook_change_set", "Atomically apply the exact prepared notebook change after revision validation.", true, false), s.applyChangeSet)
		mcp.AddTool(s.mcp, readTool("discard_notebook_change_set", "Discard a process-local prepared notebook change."), s.discardChangeSet)
	}

	if !s.policy.NoRuns && !s.policy.ReadOnly {
		mcp.AddTool(s.mcp, runTool("run_notebook_cells", "Start an explicit notebook run. Python requires allow_python=true."), s.runNotebook)
		mcp.AddTool(s.mcp, changeTool("cancel_notebook_run", "Cancel the selected asynchronous notebook run.", false, true), s.cancelRun)
		mcp.AddTool(s.mcp, readTool("get_notebook_run_status", "Get bounded status and result summaries for an MCP-started notebook run."), s.getRunStatus)
	}
}

func readTool(name, description string) *mcp.Tool {
	closed := false
	return &mcp.Tool{
		Name: name, Description: description,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closed},
	}
}

func changeTool(name, description string, destructive, idempotent bool) *mcp.Tool {
	closed := false
	return &mcp.Tool{
		Name: name, Description: description,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: false, DestructiveHint: &destructive,
			IdempotentHint: idempotent, OpenWorldHint: &closed,
		},
	}
}

func runTool(name, description string) *mcp.Tool {
	open, destructive := true, true
	return &mcp.Tool{
		Name: name, Description: description,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: false, DestructiveHint: &destructive, OpenWorldHint: &open,
		},
	}
}

func (s *Server) listNotebooks(ctx context.Context, _ *mcp.CallToolRequest, _ EmptyInput) (*mcp.CallToolResult, ListNotebooksOutput, error) {
	state, err := s.backend.Workspace(ctx)
	if err != nil {
		return nil, ListNotebooksOutput{}, err
	}
	result := ListNotebooksOutput{SchemaVersion: SchemaVersion, Notebooks: make([]NotebookSummary, 0, len(state.Notebooks))}
	for _, nb := range state.Notebooks {
		if s.policy.NotebookID != "" && nb.ID != s.policy.NotebookID {
			continue
		}
		result.Notebooks = append(result.Notebooks, summarizeNotebook(nb))
	}
	sort.Slice(result.Notebooks, func(i, j int) bool {
		if result.Notebooks[i].Title == result.Notebooks[j].Title {
			return result.Notebooks[i].ID < result.Notebooks[j].ID
		}
		return result.Notebooks[i].Title < result.Notebooks[j].Title
	})
	return nil, result, nil
}

func (s *Server) getOutline(ctx context.Context, _ *mcp.CallToolRequest, input NotebookInput) (*mcp.CallToolResult, NotebookOutlineOutput, error) {
	nb, err := s.loadNotebook(ctx, input.NotebookID)
	if err != nil {
		return nil, NotebookOutlineOutput{}, err
	}
	cells := cellsByID(nb)
	blocks := make([]BlockSummary, 0, len(nb.Blocks))
	for _, block := range nb.Blocks {
		summary := BlockSummary{ID: block.ID}
		switch {
		case block.Cell != "":
			summary.ID = block.Cell
			summary.Kind = "cell"
			if cell, ok := cells[block.Cell]; ok {
				summary.Name = cell.Name
				summary.Connection = cell.Connection
				summary.Language = cellLanguage(cell)
				if cell.NotebookSource != nil {
					summary.Kind = "source"
				}
			}
		case block.Visualization != nil:
			summary.Kind = "visualization"
			summary.Source = block.Visualization.Source
		default:
			summary.Kind = "markdown"
		}
		blocks = append(blocks, summary)
	}
	return nil, NotebookOutlineOutput{
		SchemaVersion: SchemaVersion, Notebook: summarizeNotebook(nb),
		Parameters: cloneNotebookParameterDTOs(nb.Parameters), Blocks: blocks,
	}, nil
}

func (s *Server) getBlock(ctx context.Context, _ *mcp.CallToolRequest, input NotebookBlockInput) (*mcp.CallToolResult, NotebookBlockOutput, error) {
	nb, err := s.loadNotebook(ctx, input.NotebookID)
	if err != nil {
		return nil, NotebookBlockOutput{}, err
	}
	blockID := strings.TrimSpace(input.BlockID)
	if blockID == "" {
		return nil, NotebookBlockOutput{}, fmt.Errorf("block_id is required")
	}
	for _, cell := range nb.Cells {
		if cell.CellID != blockID {
			continue
		}
		output := NotebookBlockOutput{
			SchemaVersion: SchemaVersion, NotebookID: nb.ID, Revision: nb.Revision,
			ID: cell.CellID, Kind: "cell", Name: cell.Name, AssetType: cell.Type,
			Connection: cell.Connection, Columns: cell.Columns,
			Upstreams:    append([]string(nil), cell.Upstreams...),
			ExternalRefs: append([]string(nil), cell.ExternalRefs...),
		}
		if cell.NotebookSource != nil {
			output.Kind = "source"
			safe := safeSourceDefinition(*cell.NotebookSource)
			output.Source = &safe
		} else {
			output.Content, output.Truncated = truncateUTF8(cell.Content, maxBlockContentBytes)
		}
		return nil, output, nil
	}
	for _, block := range nb.Blocks {
		if block.ID != blockID {
			continue
		}
		if block.Visualization != nil {
			visualization := *block.Visualization
			if encoded, marshalErr := json.Marshal(visualization); marshalErr == nil && len(encoded) > maxBlockContentBytes {
				return nil, NotebookBlockOutput{
					SchemaVersion: SchemaVersion, NotebookID: nb.ID, Revision: nb.Revision,
					ID: blockID, Kind: "visualization", Truncated: true,
				}, nil
			}
			return nil, NotebookBlockOutput{
				SchemaVersion: SchemaVersion, NotebookID: nb.ID, Revision: nb.Revision,
				ID: blockID, Kind: "visualization", Visualization: &visualization,
			}, nil
		}
		content, truncated := truncateUTF8(block.Markdown, maxBlockContentBytes)
		return nil, NotebookBlockOutput{
			SchemaVersion: SchemaVersion, NotebookID: nb.ID, Revision: nb.Revision,
			ID: blockID, Kind: "markdown", Content: content, Truncated: truncated,
		}, nil
	}
	return nil, NotebookBlockOutput{}, fmt.Errorf("notebook block %q was not found", blockID)
}

func (s *Server) getGraph(ctx context.Context, _ *mcp.CallToolRequest, input NotebookInput) (*mcp.CallToolResult, NotebookGraphOutput, error) {
	nb, err := s.loadNotebook(ctx, input.NotebookID)
	if err != nil {
		return nil, NotebookGraphOutput{}, err
	}
	result := NotebookGraphOutput{
		SchemaVersion: SchemaVersion, NotebookID: nb.ID, Revision: nb.Revision,
		Nodes: []GraphNode{}, Edges: []GraphEdge{}, ExternalRelations: []ExternalRelation{},
	}
	byName := make(map[string]string, len(nb.Cells))
	for _, cell := range nb.Cells {
		kind := cellLanguage(cell)
		if cell.NotebookSource != nil {
			kind = "source"
		}
		result.Nodes = append(result.Nodes, GraphNode{ID: cell.CellID, Name: cell.Name, Kind: kind, Produces: true})
		byName[strings.ToLower(cell.Name)] = cell.CellID
	}
	for _, cell := range nb.Cells {
		external := map[string]bool{}
		for _, upstream := range cell.Upstreams {
			if producer, ok := byName[strings.ToLower(upstream)]; ok {
				result.Edges = append(result.Edges, GraphEdge{Producer: producer, Consumer: cell.CellID})
			} else if strings.TrimSpace(upstream) != "" {
				external[upstream] = true
			}
		}
		for _, relation := range cell.ExternalRefs {
			if strings.TrimSpace(relation) != "" {
				external[relation] = true
			}
		}
		relations := make([]string, 0, len(external))
		for relation := range external {
			relations = append(relations, relation)
		}
		sort.Strings(relations)
		for _, relation := range relations {
			result.ExternalRelations = append(result.ExternalRelations, ExternalRelation{Consumer: cell.CellID, Relation: relation})
		}
	}
	for _, block := range nb.Blocks {
		switch {
		case block.Visualization != nil:
			result.Nodes = append(result.Nodes, GraphNode{ID: block.ID, Kind: "visualization", Produces: false})
			result.Edges = append(result.Edges, GraphEdge{Producer: block.Visualization.Source, Consumer: block.ID})
		default:
			result.Nodes = append(result.Nodes, GraphNode{ID: block.ID, Kind: "markdown", Produces: false})
		}
	}
	return nil, result, nil
}

func (s *Server) getDiagnostics(ctx context.Context, _ *mcp.CallToolRequest, input NotebookInput) (*mcp.CallToolResult, NotebookDiagnosticsOutput, error) {
	nb, err := s.loadNotebook(ctx, input.NotebookID)
	if err != nil {
		return nil, NotebookDiagnosticsOutput{}, err
	}
	result := NotebookDiagnosticsOutput{
		SchemaVersion: SchemaVersion, NotebookID: nb.ID, Revision: nb.Revision, Diagnostics: []Diagnostic{},
	}
	for _, problem := range nb.Problems {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{
			Code: "notebook-problem", Severity: "error", Message: problem, SourceKind: "notebook",
		})
	}
	for _, cell := range nb.Cells {
		if strings.TrimSpace(cell.ParseError) != "" {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Code: "cell-parse-error", Severity: "error", Message: cell.ParseError,
				BlockID: cell.CellID, SourceKind: "cell",
			})
		}
	}
	for _, block := range nb.Blocks {
		if block.Visualization == nil {
			continue
		}
		checked, checkErr := s.backend.CheckVisualization(ctx, nb.ID, service.NotebookVisualizationCheckRequest{
			Source: block.Visualization.Source, Definition: block.Visualization.Definition,
		})
		if checkErr != nil {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Code: "visualization-check-failed", Severity: "error", Message: checkErr.Error(),
				BlockID: block.ID, SourceKind: "visualization",
			})
			continue
		}
		for _, finding := range checked.Findings {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Code: finding.Code, Severity: finding.Severity, Message: finding.Message,
				BlockID: block.ID, Path: finding.Path, Field: finding.Field, SourceKind: "visualization",
			})
		}
	}
	return nil, result, nil
}

func (s *Server) getResultSchema(ctx context.Context, _ *mcp.CallToolRequest, input NotebookCellInput) (*mcp.CallToolResult, NotebookResultSchemaOutput, error) {
	if _, err := s.loadCell(ctx, input.NotebookID, input.CellID); err != nil {
		return nil, NotebookResultSchemaOutput{}, err
	}
	runtime, err := s.backend.Runtime(ctx, input.NotebookID)
	if err != nil {
		return nil, NotebookResultSchemaOutput{}, err
	}
	result, ok := runtime.Results[input.CellID]
	if !ok {
		return nil, NotebookResultSchemaOutput{}, fmt.Errorf("cell %q has no recorded result; run it first", input.CellID)
	}
	complete := !result.Sampled
	if result.Snapshot != nil {
		complete = result.Snapshot.Complete
	}
	return nil, NotebookResultSchemaOutput{
		SchemaVersion: SchemaVersion, NotebookID: input.NotebookID, CellID: input.CellID,
		Status: result.Status, Columns: resultColumns(result.Columns, result.ColumnTypes),
		RowCount: result.TotalRows, Sampled: result.Sampled, Complete: complete, Materialized: result.Materialized,
	}, nil
}

func (s *Server) getResultSample(ctx context.Context, _ *mcp.CallToolRequest, input ResultSampleInput) (*mcp.CallToolResult, NotebookResultSampleOutput, error) {
	if _, err := s.loadCell(ctx, input.NotebookID, input.CellID); err != nil {
		return nil, NotebookResultSampleOutput{}, err
	}
	runtime, err := s.backend.Runtime(ctx, input.NotebookID)
	if err != nil {
		return nil, NotebookResultSampleOutput{}, err
	}
	result, ok := runtime.Results[input.CellID]
	if !ok {
		return nil, NotebookResultSampleOutput{}, fmt.Errorf("cell %q has no recorded result; run it first", input.CellID)
	}
	limit := input.Limit
	if limit <= 0 || limit > maxSampleRows {
		limit = maxSampleRows
	}
	rows, byteTruncated := boundedRows(result.Rows, limit, maxSampleBytes)
	return nil, NotebookResultSampleOutput{
		SchemaVersion: SchemaVersion, NotebookID: input.NotebookID, CellID: input.CellID,
		Columns: resultColumns(result.Columns, result.ColumnTypes), Rows: rows,
		ReturnedRows: len(rows), TotalRows: result.TotalRows,
		Truncated: byteTruncated || len(rows) < len(result.Rows) || result.TotalRows > int64(len(rows)),
		Sampled:   result.Sampled,
	}, nil
}

func (s *Server) listSources(ctx context.Context, _ *mcp.CallToolRequest, input NotebookInput) (*mcp.CallToolResult, ListNotebookSourcesOutput, error) {
	nb, err := s.loadNotebook(ctx, input.NotebookID)
	if err != nil {
		return nil, ListNotebookSourcesOutput{}, err
	}
	runtime, runtimeErr := s.backend.Runtime(ctx, input.NotebookID)
	if runtimeErr != nil {
		return nil, ListNotebookSourcesOutput{}, runtimeErr
	}
	result := ListNotebookSourcesOutput{SchemaVersion: SchemaVersion, NotebookID: nb.ID, Sources: []NotebookSourceSummary{}}
	for _, cell := range nb.Cells {
		var definition SafeSourceDefinition
		switch {
		case cell.NotebookSource != nil:
			definition = safeSourceDefinition(*cell.NotebookSource)
		case strings.TrimSpace(cell.Connection) != "" && strings.HasSuffix(strings.ToLower(cell.Type), ".sql"):
			definition = SafeSourceDefinition{
				Kind: "warehouse_sql", Connection: cell.Connection,
				Snapshot: sourceSnapshotMode(cell), RowLimit: sourceSnapshotLimit(cell),
			}
		default:
			continue
		}
		summary := NotebookSourceSummary{CellID: cell.CellID, Name: cell.Name, Definition: definition}
		if observed, ok := runtime.Results[cell.CellID]; ok && observed.Snapshot != nil {
			snapshot := observed.Snapshot
			summary.Snapshot = &SourceSnapshot{
				Environment: snapshot.Environment, Connection: snapshot.Connection,
				ImportedAt: snapshot.ImportedAt, RowCount: snapshot.RowCount, ByteCount: snapshot.ByteCount,
				Complete: snapshot.Complete, Sampled: snapshot.Sampled,
				Schema: tabularResultColumns(snapshot.Schema),
			}
		}
		result.Sources = append(result.Sources, summary)
	}
	return nil, result, nil
}

func (s *Server) loadNotebook(ctx context.Context, notebookID string) (model.Notebook, error) {
	notebookID = strings.TrimSpace(notebookID)
	if notebookID == "" {
		return model.Notebook{}, fmt.Errorf("notebook_id is required")
	}
	if s.policy.NotebookID != "" && notebookID != s.policy.NotebookID {
		return model.Notebook{}, fmt.Errorf("notebook %q is outside this agent session", notebookID)
	}
	return s.backend.Notebook(ctx, notebookID)
}

func (s *Server) loadCell(ctx context.Context, notebookID, cellID string) (model.Asset, error) {
	nb, err := s.loadNotebook(ctx, notebookID)
	if err != nil {
		return model.Asset{}, err
	}
	for _, cell := range nb.Cells {
		if cell.CellID == cellID {
			return cell, nil
		}
	}
	return model.Asset{}, fmt.Errorf("notebook cell %q was not found", cellID)
}

func summarizeNotebook(nb model.Notebook) NotebookSummary {
	return NotebookSummary{
		ID: nb.ID, UUID: nb.UUID, Title: nb.Title, Revision: nb.Revision,
		ManifestVersion: nb.ManifestVersion, BlockCount: len(nb.Blocks), CellCount: len(nb.Cells),
		ParameterCount: len(nb.Parameters),
		ProblemCount:   len(nb.Problems),
	}
}

func cloneNotebookParameterDTOs(parameters []model.NotebookParameter) []model.NotebookParameter {
	if len(parameters) == 0 {
		return nil
	}
	encoded, err := json.Marshal(parameters)
	if err != nil {
		return append([]model.NotebookParameter(nil), parameters...)
	}
	var result []model.NotebookParameter
	if err := json.Unmarshal(encoded, &result); err != nil {
		return append([]model.NotebookParameter(nil), parameters...)
	}
	return result
}

func cellsByID(nb model.Notebook) map[string]model.Asset {
	result := make(map[string]model.Asset, len(nb.Cells))
	for _, cell := range nb.Cells {
		result[cell.CellID] = cell
	}
	return result
}

func cellLanguage(cell model.Asset) string {
	if strings.EqualFold(strings.TrimSpace(cell.Type), notebook.PythonCellType) || strings.HasSuffix(strings.ToLower(cell.Type), ".py") {
		return "python"
	}
	return "sql"
}

func sourceSnapshotMode(cell model.Asset) string {
	mode := strings.TrimSpace(cell.Meta[notebook.SnapshotModeMetaKey])
	if mode == "" {
		return notebook.SnapshotModeFull
	}
	return mode
}

func sourceSnapshotLimit(cell model.Asset) int64 {
	if sourceSnapshotMode(cell) != notebook.SnapshotModeSample {
		return 0
	}
	var limit int64
	_, _ = fmt.Sscan(cell.Meta[notebook.SnapshotRowLimitMetaKey], &limit)
	return limit
}

func safeSourceDefinition(source model.NotebookSourceDefinition) SafeSourceDefinition {
	return SafeSourceDefinition{
		Kind: source.Kind, Connection: source.Connection, URI: safeURI(source.URI), Format: source.Format,
		RequestURL: safeURI(source.Request.URL), Method: source.Request.Method,
		RecordsPath: source.Response.RecordsPath, Snapshot: source.Snapshot.Mode, RowLimit: source.Snapshot.RowLimit,
	}
}

func safeURI(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if filepath.IsAbs(trimmed) {
		return "<local-file>"
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" {
		return trimmed
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func resultColumns(names, types []string) []ResultColumn {
	result := make([]ResultColumn, 0, len(names))
	for index, name := range names {
		physicalType := "UNKNOWN"
		if index < len(types) && strings.TrimSpace(types[index]) != "" {
			physicalType = types[index]
		}
		result = append(result, ResultColumn{Name: name, Type: physicalType})
	}
	return result
}

func tabularResultColumns(columns []notebook.TabularColumn) []ResultColumn {
	result := make([]ResultColumn, 0, len(columns))
	for _, column := range columns {
		result = append(result, ResultColumn{Name: column.Name, Type: column.Type})
	}
	return result
}

func truncateUTF8(value string, maxBytes int) (string, bool) {
	if len(value) <= maxBytes {
		return value, false
	}
	cut := maxBytes
	for cut > 0 && (value[cut]&0xc0) == 0x80 {
		cut--
	}
	return value[:cut], true
}

func boundedRows(rows [][]any, limit, byteLimit int) ([][]any, bool) {
	result := make([][]any, 0, min(limit, len(rows)))
	used := 2
	for _, row := range rows {
		if len(result) >= limit {
			return result, true
		}
		sanitized := make([]any, len(row))
		for index, value := range row {
			sanitized[index] = boundedValue(value)
		}
		encoded, err := json.Marshal(sanitized)
		if err != nil {
			sanitized = []any{"<unserializable row>"}
			encoded, _ = json.Marshal(sanitized)
		}
		if used+len(encoded)+1 > byteLimit {
			return result, true
		}
		result = append(result, sanitized)
		used += len(encoded) + 1
	}
	return result, false
}

func boundedValue(value any) any {
	if text, ok := value.(string); ok {
		bounded, truncated := truncateUTF8(text, maxSampleValueBytes)
		if truncated {
			return bounded + "…"
		}
		return bounded
	}
	encoded, err := json.Marshal(value)
	if err == nil && len(encoded) > maxSampleValueBytes {
		return "<value omitted: exceeds 8 KiB>"
	}
	return value
}
