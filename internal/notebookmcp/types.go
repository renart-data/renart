// Package notebookmcp adapts Renart's notebook domain services to a bounded,
// workspace-scoped MCP surface. It intentionally exposes semantic notebook
// operations rather than filesystem paths, arbitrary SQL, or generic HTTP.
package notebookmcp

import (
	"context"

	"renart/internal/web/model"
	"renart/internal/web/service"
)

const SchemaVersion = 1

// Backend is the protocol-independent notebook boundary used by MCP. The cmd
// package supplies either a client for the owning Renart server or an embedded
// service implementation when no server is running.
type Backend interface {
	Workspace(context.Context) (model.WorkspaceState, error)
	Notebook(context.Context, string) (model.Notebook, error)
	Runtime(context.Context, string) (service.NotebookRuntimeSnapshot, error)
	PrepareChangeSet(context.Context, string, service.NotebookChangeSet) (service.NotebookChangePlan, error)
	ApplyChangeSet(context.Context, string, service.NotebookChangeSet) (service.NotebookChangeApplyResult, error)
	CheckVisualization(context.Context, string, service.NotebookVisualizationCheckRequest) (service.NotebookVisualizationCheckResult, error)
	Run(context.Context, string, service.RunNotebookRequest) (service.RunNotebookResult, error)
	Cancel(context.Context, string) error
}

type EmptyInput struct{}

type NotebookInput struct {
	NotebookID string `json:"notebook_id" jsonschema:"The opaque notebook ID returned by list_notebooks."`
}

type NotebookBlockInput struct {
	NotebookID string `json:"notebook_id" jsonschema:"The opaque notebook ID returned by list_notebooks."`
	BlockID    string `json:"block_id" jsonschema:"The durable cell, markdown, or visualization block ID."`
}

type NotebookCellInput struct {
	NotebookID string `json:"notebook_id" jsonschema:"The opaque notebook ID returned by list_notebooks."`
	CellID     string `json:"cell_id" jsonschema:"The durable data-producing cell or source ID."`
}

type NotebookSummary struct {
	ID              string `json:"id"`
	UUID            string `json:"uuid"`
	Title           string `json:"title"`
	Revision        string `json:"revision"`
	ManifestVersion int    `json:"manifest_version"`
	BlockCount      int    `json:"block_count"`
	CellCount       int    `json:"cell_count"`
	ParameterCount  int    `json:"parameter_count"`
	ProblemCount    int    `json:"problem_count"`
}

type ListNotebooksOutput struct {
	SchemaVersion int               `json:"schema_version"`
	Notebooks     []NotebookSummary `json:"notebooks"`
}

type BlockSummary struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Name       string `json:"name,omitempty"`
	Language   string `json:"language,omitempty"`
	Connection string `json:"connection,omitempty"`
	Source     string `json:"source,omitempty"`
}

type NotebookOutlineOutput struct {
	SchemaVersion int                       `json:"schema_version"`
	Notebook      NotebookSummary           `json:"notebook"`
	Parameters    []model.NotebookParameter `json:"parameters,omitempty"`
	Blocks        []BlockSummary            `json:"blocks"`
}

// SafeSourceDefinition excludes request headers, parameters, and bodies. Those
// values may contain credentials and are unnecessary for notebook reasoning.
type SafeSourceDefinition struct {
	Kind        string `json:"kind"`
	Connection  string `json:"connection,omitempty"`
	URI         string `json:"uri,omitempty"`
	Format      string `json:"format,omitempty"`
	RequestURL  string `json:"request_url,omitempty"`
	Method      string `json:"method,omitempty"`
	RecordsPath string `json:"records_path,omitempty"`
	Snapshot    string `json:"snapshot"`
	RowLimit    int64  `json:"row_limit,omitempty"`
}

type NotebookBlockOutput struct {
	SchemaVersion int                          `json:"schema_version"`
	NotebookID    string                       `json:"notebook_id"`
	Revision      string                       `json:"revision"`
	ID            string                       `json:"id"`
	Kind          string                       `json:"kind"`
	Name          string                       `json:"name,omitempty"`
	AssetType     string                       `json:"asset_type,omitempty"`
	Connection    string                       `json:"connection,omitempty"`
	Content       string                       `json:"content,omitempty"`
	Truncated     bool                         `json:"truncated,omitempty"`
	Columns       []model.Column               `json:"columns,omitempty"`
	Upstreams     []string                     `json:"upstreams,omitempty"`
	ExternalRefs  []string                     `json:"external_refs,omitempty"`
	Source        *SafeSourceDefinition        `json:"source,omitempty"`
	Visualization *model.NotebookVisualization `json:"visualization,omitempty"`
}

type GraphNode struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Produces bool   `json:"produces_relation"`
}

type GraphEdge struct {
	Producer string `json:"producer"`
	Consumer string `json:"consumer"`
}

type ExternalRelation struct {
	Consumer string `json:"consumer"`
	Relation string `json:"relation"`
}

type NotebookGraphOutput struct {
	SchemaVersion     int                `json:"schema_version"`
	NotebookID        string             `json:"notebook_id"`
	Revision          string             `json:"revision"`
	Nodes             []GraphNode        `json:"nodes"`
	Edges             []GraphEdge        `json:"edges"`
	ExternalRelations []ExternalRelation `json:"external_relations"`
}

type Diagnostic struct {
	Code       string `json:"code"`
	Severity   string `json:"severity"`
	Message    string `json:"message"`
	BlockID    string `json:"block_id,omitempty"`
	Path       string `json:"definition_path,omitempty"`
	Field      string `json:"field,omitempty"`
	SourceKind string `json:"source_kind"`
}

type NotebookDiagnosticsOutput struct {
	SchemaVersion int          `json:"schema_version"`
	NotebookID    string       `json:"notebook_id"`
	Revision      string       `json:"revision"`
	Diagnostics   []Diagnostic `json:"diagnostics"`
}

type ResultColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type NotebookResultSchemaOutput struct {
	SchemaVersion int            `json:"schema_version"`
	NotebookID    string         `json:"notebook_id"`
	CellID        string         `json:"cell_id"`
	Status        string         `json:"status"`
	Columns       []ResultColumn `json:"columns"`
	RowCount      int64          `json:"row_count"`
	Sampled       bool           `json:"sampled"`
	Complete      bool           `json:"complete"`
	Materialized  string         `json:"materialized_as,omitempty"`
}

type ResultSampleInput struct {
	NotebookID string `json:"notebook_id"`
	CellID     string `json:"cell_id"`
	Limit      int    `json:"limit,omitempty" jsonschema:"Maximum rows to return; values above 50 are reduced to 50."`
}

type NotebookResultSampleOutput struct {
	SchemaVersion int            `json:"schema_version"`
	NotebookID    string         `json:"notebook_id"`
	CellID        string         `json:"cell_id"`
	Columns       []ResultColumn `json:"columns"`
	Rows          [][]any        `json:"rows"`
	ReturnedRows  int            `json:"returned_rows"`
	TotalRows     int64          `json:"total_rows"`
	Truncated     bool           `json:"truncated"`
	Sampled       bool           `json:"sampled"`
}

type SourceSnapshot struct {
	Environment string         `json:"environment,omitempty"`
	Connection  string         `json:"connection,omitempty"`
	ImportedAt  string         `json:"imported_at,omitempty"`
	RowCount    int64          `json:"row_count"`
	ByteCount   int64          `json:"byte_count"`
	Complete    bool           `json:"complete"`
	Sampled     bool           `json:"sampled"`
	Schema      []ResultColumn `json:"schema,omitempty"`
}

type NotebookSourceSummary struct {
	CellID     string               `json:"cell_id"`
	Name       string               `json:"name"`
	Definition SafeSourceDefinition `json:"definition"`
	Snapshot   *SourceSnapshot      `json:"snapshot,omitempty"`
}

type ListNotebookSourcesOutput struct {
	SchemaVersion int                     `json:"schema_version"`
	NotebookID    string                  `json:"notebook_id"`
	Sources       []NotebookSourceSummary `json:"sources"`
}

type PrepareChangeSetInput struct {
	NotebookID   string                      `json:"notebook_id"`
	BaseRevision string                      `json:"base_revision"`
	Operations   []service.NotebookOperation `json:"operations"`
}

type PreparedOperation struct {
	Kind          string                       `json:"kind"`
	CellID        string                       `json:"cell_id,omitempty"`
	BlockID       string                       `json:"block_id,omitempty"`
	Name          string                       `json:"name,omitempty"`
	Language      string                       `json:"language,omitempty"`
	Connection    string                       `json:"connection,omitempty"`
	AssetType     string                       `json:"asset_type,omitempty"`
	SnapshotMode  string                       `json:"snapshot_mode,omitempty"`
	RowLimit      int64                        `json:"row_limit,omitempty"`
	Content       string                       `json:"content,omitempty"`
	Visualization *model.NotebookVisualization `json:"visualization,omitempty"`
	Source        *SafeSourceDefinition        `json:"source,omitempty"`
	Parameters    []model.NotebookParameter    `json:"parameters,omitempty"`
	Position      string                       `json:"position,omitempty"`
	AfterBlockID  string                       `json:"after_block_id,omitempty"`
}

type PreparedDiff struct {
	Status  string `json:"status"`
	Subject string `json:"subject"`
}

type PreparedChangeOutput struct {
	SchemaVersion    int                 `json:"schema_version"`
	PreparedID       string              `json:"prepared_id"`
	NotebookID       string              `json:"notebook_id"`
	BaseRevision     string              `json:"base_revision"`
	ExpectedRevision string              `json:"expected_revision"`
	Operations       []PreparedOperation `json:"operations"`
	Diff             []PreparedDiff      `json:"diff"`
	Problems         []string            `json:"problems,omitempty"`
	BlockingProblems []string            `json:"blocking_problems,omitempty"`
	CanApply         bool                `json:"can_apply"`
	ExpiresAt        string              `json:"expires_at"`
}

type PreparedChangeInput struct {
	PreparedID string `json:"prepared_id"`
}

type ApplyChangeOutput struct {
	SchemaVersion int             `json:"schema_version"`
	Notebook      NotebookSummary `json:"notebook"`
	Applied       bool            `json:"applied"`
}

type DiscardChangeOutput struct {
	SchemaVersion int    `json:"schema_version"`
	PreparedID    string `json:"prepared_id"`
	Discarded     bool   `json:"discarded"`
}

type RunNotebookInput struct {
	NotebookID     string   `json:"notebook_id"`
	All            bool     `json:"all,omitempty"`
	From           string   `json:"from,omitempty"`
	Cells          []string `json:"cells,omitempty"`
	Environment    string   `json:"environment,omitempty"`
	RefreshSources bool     `json:"refresh_sources,omitempty"`
	StartDate      string   `json:"start_date,omitempty"`
	EndDate        string   `json:"end_date,omitempty"`
	AllowPython    bool     `json:"allow_python,omitempty" jsonschema:"Must be true when the selected execution may run Python code."`
}

type RunAcceptedOutput struct {
	SchemaVersion int    `json:"schema_version"`
	RunID         string `json:"run_id"`
	NotebookID    string `json:"notebook_id"`
	Status        string `json:"status"`
}

type RunInput struct {
	RunID string `json:"run_id"`
}

type RunCellSummary struct {
	CellID     string         `json:"cell_id"`
	Name       string         `json:"name"`
	Status     string         `json:"status"`
	Error      string         `json:"error,omitempty"`
	Columns    []ResultColumn `json:"columns,omitempty"`
	RowCount   int64          `json:"row_count"`
	Sampled    bool           `json:"sampled"`
	DurationMS int64          `json:"duration_ms"`
}

type RunStatusOutput struct {
	SchemaVersion int              `json:"schema_version"`
	RunID         string           `json:"run_id"`
	NotebookID    string           `json:"notebook_id"`
	Status        string           `json:"status"`
	Error         string           `json:"error,omitempty"`
	StartedAt     string           `json:"started_at,omitempty"`
	FinishedAt    string           `json:"finished_at,omitempty"`
	Results       []RunCellSummary `json:"results,omitempty"`
}
