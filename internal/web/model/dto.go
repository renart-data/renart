// Package model provides data transfer objects for the Bruin web API.
package model

import "time"

// MaterializationCapability describes one write mode Renart can safely offer
// for an asset's concrete execution path. Labels remain a frontend concern;
// the backend owns availability and field requirements.
type MaterializationCapability struct {
	Mode                    string `json:"mode"`
	Type                    string `json:"type"`
	Strategy                string `json:"strategy"`
	SupportsIncrementalKey  bool   `json:"supports_incremental_key,omitempty"`
	RequiresIncrementalKey  bool   `json:"requires_incremental_key,omitempty"`
	RequiresPrimaryKey      bool   `json:"requires_primary_key,omitempty"`
	RequiresTimeGranularity bool   `json:"requires_time_granularity,omitempty"`
	SupportsPartitionBy     bool   `json:"supports_partition_by,omitempty"`
	SupportsClusterBy       bool   `json:"supports_cluster_by,omitempty"`
}

// AssetAuthoringCapability describes a concrete seed or sensor asset that
// Renart can create and execute directly. The backend owns this list so the UI
// cannot drift from the runtime when Bruin adds or changes asset types.
type AssetAuthoringCapability struct {
	Type               string            `json:"type"`
	Kind               string            `json:"kind"`
	Variant            string            `json:"variant"`
	ConnectionTypes    []string          `json:"connection_types"`
	RequiredParameters []string          `json:"required_parameters,omitempty"`
	DefaultParameters  map[string]string `json:"default_parameters,omitempty"`
	FileTypes          []string          `json:"file_types,omitempty"`
	SupportsUpload     bool              `json:"supports_upload,omitempty"`
	SupportsURL        bool              `json:"supports_url,omitempty"`
}

// ColumnInferenceSource describes one schema observation an asset can provide.
// The backend owns these capabilities so adding a new asset kind does not
// require another frontend inference-mode switch.
type ColumnInferenceSource struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	Description    string `json:"description"`
	Category       string `json:"category"`
	MayOmitColumns bool   `json:"may_omit_columns,omitempty"`
}

// ColumnSchemaDriftItem is one difference between saved column metadata and an
// inferred schema preview.
type ColumnSchemaDriftItem struct {
	Column       string `json:"column"`
	Kind         string `json:"kind"`
	CurrentType  string `json:"current_type,omitempty"`
	InferredType string `json:"inferred_type,omitempty"`
}

// ColumnSchemaDrift summarizes the changes applying an inferred schema would
// make to the saved metadata.
type ColumnSchemaDrift struct {
	Added       int                     `json:"added"`
	Removed     int                     `json:"removed"`
	TypeChanged int                     `json:"type_changed"`
	Unchanged   int                     `json:"unchanged"`
	Items       []ColumnSchemaDriftItem `json:"items"`
}

// ColumnInferencePreview is a non-mutating schema observation and its drift
// from the asset's saved column metadata.
type ColumnInferencePreview struct {
	Status        string                `json:"status"`
	Source        ColumnInferenceSource `json:"source"`
	Columns       []Column              `json:"columns"`
	Drift         ColumnSchemaDrift     `json:"drift"`
	Notes         []string              `json:"notes,omitempty"`
	SampleRecords *int                  `json:"sample_records,omitempty"`
}

// ColumnSchemaSourceSnapshot is one source observation used by a schema sync.
// Definition sources are primary; selected observed sources remain advisory.
type ColumnSchemaSourceSnapshot struct {
	Source        ColumnInferenceSource `json:"source"`
	Columns       []Column              `json:"columns"`
	Notes         []string              `json:"notes,omitempty"`
	SampleRecords *int                  `json:"sample_records,omitempty"`
	// Fresh is set for materialized-table observations when Renart can compare
	// the current pipeline fingerprint with its materialization record. It is
	// runtime evidence only and is never persisted in asset metadata.
	Fresh          *bool      `json:"fresh,omitempty"`
	Stage          string     `json:"stage,omitempty"`
	Completeness   string     `json:"completeness,omitempty"`
	Confidence     string     `json:"confidence,omitempty"`
	AssetRevision  string     `json:"asset_revision,omitempty"`
	OutputIdentity string     `json:"output_identity,omitempty"`
	Environment    string     `json:"environment,omitempty"`
	Connection     string     `json:"connection,omitempty"`
	Relation       string     `json:"relation,omitempty"`
	ObservedAt     *time.Time `json:"observed_at,omitempty"`
	Classification string     `json:"classification,omitempty"`
	ExcludedReason string     `json:"excluded_reason,omitempty"`
}

// ColumnSchemaMergeRow describes how one column compares across the inferred
// sources and the asset's currently saved metadata.
type ColumnSchemaMergeRow struct {
	Column          string `json:"column"`
	CurrentPresent  bool   `json:"current_present"`
	CurrentType     string `json:"current_type,omitempty"`
	ProposedPresent bool   `json:"proposed_present"`
	ProposedType    string `json:"proposed_type,omitempty"`
	Kind            string `json:"kind"`
	Detail          string `json:"detail"`
	Conflict        bool   `json:"conflict"`
}

// ColumnSchemaSyncResult is returned by the one-click schema sync. Safe
// additions and unknown-to-known type refinements are applied immediately;
// conflicts return the source snapshots and merge rows without writing.
type ColumnSchemaSyncResult struct {
	Status           string                       `json:"status"`
	Sources          []ColumnSchemaSourceSnapshot `json:"sources"`
	Rows             []ColumnSchemaMergeRow       `json:"rows"`
	ManagedColumns   []Column                     `json:"managed_columns"`
	CandidateColumns []Column                     `json:"candidate_columns"`
	Columns          []Column                     `json:"columns,omitempty"`
	Notes            []string                     `json:"notes,omitempty"`
}

// ColumnSchemaResolution is one explicit merge choice. Source is either a
// source capability ID or "current"; Action is "use" or "remove".
type ColumnSchemaResolution struct {
	Column string `json:"column"`
	Action string `json:"action"`
	Source string `json:"source,omitempty"`
	Type   string `json:"type,omitempty"`
}

// Asset represents a web API asset with its metadata. ContentRevision identifies
// the exact snapshot returned for a notebook cell so saves can use it as an
// optimistic-concurrency precondition.
type Asset struct {
	ID                     string                  `json:"id"`
	Name                   string                  `json:"name"`
	Type                   string                  `json:"type"`
	Path                   string                  `json:"path"`
	Content                string                  `json:"content"`
	ContentRevision        string                  `json:"content_revision,omitempty"`
	Upstreams              []string                `json:"upstreams"`
	Parameters             map[string]string       `json:"parameters,omitempty"`
	Meta                   map[string]string       `json:"meta,omitempty"`
	Columns                []Column                `json:"columns,omitempty"`
	CustomChecks           []CustomCheck           `json:"custom_checks,omitempty"`
	ColumnInferenceSources []ColumnInferenceSource `json:"column_inference_sources,omitempty"`
	// Connection is the effective target connection after applying pipeline
	// defaults; ExplicitConnection is the persisted asset-level override used by
	// metadata editors to represent the Auto state without losing runtime context.
	Connection                  string                      `json:"connection,omitempty"`
	ExplicitConnection          string                      `json:"explicit_connection,omitempty"`
	MaterializationType         string                      `json:"materialization_type,omitempty"`
	MaterializationStrategy     string                      `json:"materialization_strategy,omitempty"`
	IncrementalKey              string                      `json:"incremental_key,omitempty"`
	PartitionBy                 string                      `json:"partition_by,omitempty"`
	ClusterBy                   []string                    `json:"cluster_by,omitempty"`
	TimeGranularity             string                      `json:"time_granularity,omitempty"`
	MaterializationCapabilities []MaterializationCapability `json:"materialization_capabilities,omitempty"`
	SupportsFullRefresh         bool                        `json:"supports_full_refresh,omitempty"`
	RefreshRestricted           bool                        `json:"refresh_restricted,omitempty"`
	Owner                       string                      `json:"owner,omitempty"`
	Tags                        []string                    `json:"tags,omitempty"`
	IsMaterialized              bool                        `json:"is_materialized"`
	MaterializedAs              string                      `json:"materialized_as,omitempty"`
	RowCount                    *int64                      `json:"row_count,omitempty"`
	// Class separates production assets from notebook cells; catalog,
	// global lineage, and pipeline-side completion filter to "pipeline".
	// Empty means "pipeline" (older payloads).
	Class string `json:"class,omitempty"`
	// CellID is the durable per-cell identifier for notebook cells; it
	// survives renames (durable id = notebook UUID + ":" + cell id).
	CellID string `json:"cell_id,omitempty"`
	// ExternalRefs lists referenced table names that are not sibling cells
	// (pipeline assets or warehouse tables); notebook cells only.
	ExternalRefs []string `json:"external_refs,omitempty"`
	// ParseError is set when the asset file could not be parsed. The asset is
	// still surfaced (with its raw content) so the pipeline stays visible and the
	// user can open and fix it, rather than the whole pipeline disappearing.
	ParseError string `json:"parse_error,omitempty"`
}

// Column represents a column in an asset.
type Column struct {
	Name          string            `json:"name"`
	SourceColumn  string            `json:"source_column,omitempty"`
	Type          string            `json:"type,omitempty"`
	Mask          string            `json:"mask,omitempty"`
	Description   string            `json:"description,omitempty"`
	Tags          []string          `json:"tags,omitempty"`
	PrimaryKey    bool              `json:"primary_key,omitempty"`
	UpdateOnMerge bool              `json:"update_on_merge,omitempty"`
	MergeSQL      string            `json:"merge_sql,omitempty"`
	Nullable      *bool             `json:"nullable,omitempty"`
	Default       string            `json:"default,omitempty"`
	Precision     *int              `json:"precision,omitempty"`
	Scale         *int              `json:"scale,omitempty"`
	Length        *int              `json:"length,omitempty"`
	Collation     string            `json:"collation,omitempty"`
	ForeignKey    *ColumnReference  `json:"foreign_key,omitempty"`
	Owner         string            `json:"owner,omitempty"`
	Domains       []string          `json:"domains,omitempty"`
	Meta          map[string]string `json:"meta,omitempty"`
	Checks        []ColumnCheck     `json:"checks,omitempty"`
}

type ColumnReference struct {
	Table  string `json:"table"`
	Column string `json:"column"`
}

// ColumnCheck represents a check on a column.
type ColumnCheck struct {
	Name        string `json:"name"`
	Value       any    `json:"value,omitempty"`
	Blocking    *bool  `json:"blocking,omitempty"`
	Description string `json:"description,omitempty"`
}

// CustomCheck represents an asset-level SQL quality check. Count is set when
// Query returns the rows that violate the assertion; otherwise Query must
// return a scalar integer matching Value.
type CustomCheck struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Value       int64  `json:"value"`
	Count       *int64 `json:"count,omitempty"`
	Blocking    *bool  `json:"blocking,omitempty"`
	Query       string `json:"query"`
	Retries     *int   `json:"retries,omitempty"`
}

// Pipeline represents a web API pipeline.
type Pipeline struct {
	ID string `json:"id"`
	// UUID is the stable identity stored in pipeline.yml (`id:`); all durable
	// records (schedules, run history, snapshots) key off it instead of ID,
	// which encodes the filesystem path.
	UUID     string  `json:"uuid,omitempty"`
	Name     string  `json:"name"`
	Path     string  `json:"path"`
	Schedule string  `json:"schedule,omitempty"`
	Assets   []Asset `json:"assets"`
}

// NotebookBlock is one ordered entry of a notebook: a cell reference or a
// markdown prose block.
type NotebookBlock struct {
	Cell     string `json:"cell,omitempty"`
	Markdown string `json:"markdown,omitempty"`
}

// Notebook represents a web API notebook: a folder of class-tagged cell
// assets plus ordered presentation blocks.
type Notebook struct {
	ID string `json:"id"`
	// UUID is the stable identity stored in notebook.yml (`id:`).
	UUID     string          `json:"uuid,omitempty"`
	Title    string          `json:"title"`
	Path     string          `json:"path"`
	Target   string          `json:"target,omitempty"`
	Blocks   []NotebookBlock `json:"blocks"`
	Cells    []Asset         `json:"cells"`
	Problems []string        `json:"problems,omitempty"`
	// Dependencies are the notebook's Python package specifiers, stored in
	// pyproject.toml ([project].dependencies) and installed by uv.
	Dependencies []string `json:"dependencies,omitempty"`
	// InstalledModules are the top-level import names available to notebook
	// Python cells (virtualenv packages plus runner-injected modules).
	InstalledModules []string `json:"installed_modules,omitempty"`
}

// EnvironmentPolicy mirrors the per-environment execution rules from
// .renart/environments.yml so the UI can disable controls; enforcement
// lives in the run-dispatch chokepoint, not here.
type EnvironmentPolicy struct {
	Protected          bool `json:"protected"`
	DeployedOnly       bool `json:"deployed_only"`
	ConfirmDestructive bool `json:"confirm_destructive"`
}

// WorkspaceQueryConnection is one selected-environment connection that can
// execute ad-hoc SQL. AssetType and Dialect are backend-derived so the editor
// does not duplicate Bruin's connection-to-query-runtime mapping.
type WorkspaceQueryConnection struct {
	Name           string `json:"name"`
	ConnectionType string `json:"connection_type"`
	AssetType      string `json:"asset_type"`
	Dialect        string `json:"dialect"`
}

// WorkspaceState represents the current state of a workspace.
type WorkspaceState struct {
	Pipelines           []Pipeline                   `json:"pipelines"`
	Notebooks           []Notebook                   `json:"notebooks,omitempty"`
	Connections         map[string]string            `json:"connections"`
	QueryConnections    []WorkspaceQueryConnection   `json:"query_connections,omitempty"`
	AssetCapabilities   []AssetAuthoringCapability   `json:"asset_capabilities,omitempty"`
	SelectedEnvironment string                       `json:"selected_environment"`
	EnvironmentPolicies map[string]EnvironmentPolicy `json:"environment_policies,omitempty"`
	// Features are project-scoped feature flags from .renart/project.yml
	// (e.g. "ingestr" re-enables ingestr surfaces in the UI).
	Features  map[string]bool     `json:"features,omitempty"`
	Errors    []string            `json:"errors"`
	UpdatedAt time.Time           `json:"updated_at"`
	Metadata  map[string][]string `json:"metadata"`
	Revision  int64               `json:"revision,omitempty"`
}

// WorkspaceEvent represents an SSE event for workspace changes.
type WorkspaceEvent struct {
	Type      string         `json:"type"`
	Path      string         `json:"path,omitempty"`
	Workspace WorkspaceState `json:"workspace"`
}

// AssetMaterializationState represents the materialization state of an asset.
type AssetMaterializationState struct {
	AssetID         string `json:"asset_id"`
	IsMaterialized  bool   `json:"is_materialized"`
	MaterializedAs  string `json:"materialized_as,omitempty"`
	RowCount        *int64 `json:"row_count,omitempty"`
	Connection      string `json:"connection,omitempty"`
	DeclaredMatType string `json:"materialization_type,omitempty"`
}

// PipelineMaterializationResponse represents a pipeline materialization state response.
type PipelineMaterializationResponse struct {
	PipelineID string                      `json:"pipeline_id"`
	Assets     []AssetMaterializationState `json:"assets"`
}

// MaterializationInfo is internal state for pipeline materialization info.
type MaterializationInfo struct {
	AssetName       string
	Connection      string
	IsMaterialized  bool
	MaterializedAs  string
	RowCount        *int64
	DeclaredMatType string
}

// DBObjectInfo represents database object metadata.
type DBObjectInfo struct {
	Schema        string
	Name          string
	QualifiedName string
	Kind          string
}

// CreatePipelineRequest is the request body for creating a pipeline.
type CreatePipelineRequest struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

// UpdatePipelineRequest is the request body for updating a pipeline.
type UpdatePipelineRequest struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

type PipelineConfigConnection struct {
	Platform string `json:"platform"`
	Name     string `json:"name"`
}

type PipelineReferencedConnection struct {
	Name   string   `json:"name"`
	Assets []string `json:"assets"`
}

type PipelineConfigNotification struct {
	Enabled    bool   `json:"enabled"`
	Channel    string `json:"channel,omitempty"`
	Connection string `json:"connection,omitempty"`
	Success    bool   `json:"success"`
	Failure    bool   `json:"failure"`
}

type PipelineConfigDefaults struct {
	RerunCooldown  *int   `json:"rerun_cooldown,omitempty"`
	StartOffsetRaw string `json:"start_offset_raw,omitempty"`
	EndOffsetRaw   string `json:"end_offset_raw,omitempty"`
}

type PipelineConfigVariable struct {
	Name         string         `json:"name"`
	Type         string         `json:"type"`
	DefaultValue any            `json:"default_value"`
	Description  string         `json:"description,omitempty"`
	Extra        map[string]any `json:"extra,omitempty"`
}

type PipelineConfigResponse struct {
	Status                     string                         `json:"status"`
	ID                         string                         `json:"id"`
	Path                       string                         `json:"path"`
	Name                       string                         `json:"name"`
	Schedule                   string                         `json:"schedule,omitempty"`
	StartDate                  string                         `json:"start_date,omitempty"`
	Owner                      string                         `json:"owner,omitempty"`
	Tags                       []string                       `json:"tags"`
	Domains                    []string                       `json:"domains"`
	DefaultConnections         []PipelineConfigConnection     `json:"default_connections"`
	InferredDefaultConnections []PipelineConfigConnection     `json:"inferred_default_connections"`
	ReferencedConnections      []PipelineReferencedConnection `json:"referenced_connections"`
	Catchup                    bool                           `json:"catchup"`
	MetadataPushBigQuery       bool                           `json:"metadata_push_bigquery"`
	Retries                    int                            `json:"retries"`
	Concurrency                int                            `json:"concurrency"`
	MaxActiveSteps             *int                           `json:"max_active_steps,omitempty"`
	NotificationsSlack         PipelineConfigNotification     `json:"notifications_slack"`
	NotificationsTeams         PipelineConfigNotification     `json:"notifications_teams"`
	Defaults                   PipelineConfigDefaults         `json:"defaults"`
	Variables                  []PipelineConfigVariable       `json:"variables"`
	YAML                       string                         `json:"yaml"`
}

type UpdatePipelineConfigRequest struct {
	Name                 string                     `json:"name"`
	Schedule             string                     `json:"schedule"`
	StartDate            string                     `json:"start_date"`
	Owner                string                     `json:"owner"`
	Tags                 []string                   `json:"tags"`
	Domains              []string                   `json:"domains"`
	DefaultConnections   []PipelineConfigConnection `json:"default_connections"`
	Catchup              bool                       `json:"catchup"`
	MetadataPushBigQuery bool                       `json:"metadata_push_bigquery"`
	Retries              int                        `json:"retries"`
	Concurrency          int                        `json:"concurrency"`
	MaxActiveSteps       *int                       `json:"max_active_steps,omitempty"`
	NotificationsSlack   PipelineConfigNotification `json:"notifications_slack"`
	NotificationsTeams   PipelineConfigNotification `json:"notifications_teams"`
	Defaults             PipelineConfigDefaults     `json:"defaults"`
	Variables            []PipelineConfigVariable   `json:"variables"`
}

// PipelinePythonDependenciesResponse is the editable Python environment shared
// by assets in one pipeline. Path is workspace-relative and points to the
// canonical pyproject.toml even before that file is created.
type PipelinePythonDependenciesResponse struct {
	Status       string   `json:"status"`
	PipelineID   string   `json:"pipeline_id"`
	Path         string   `json:"path"`
	Dependencies []string `json:"dependencies"`
}

type UpdatePipelinePythonDependenciesRequest struct {
	Dependencies []string `json:"dependencies"`
}

// CreateAssetRequest is the request body for creating an asset.
type CreateAssetRequest struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

// UpdateAssetRequest is the request body for updating an asset.
type UpdateAssetRequest struct {
	Type                *string           `json:"type,omitempty"`
	Content             *string           `json:"content,omitempty"`
	MaterializationType *string           `json:"materialization_type,omitempty"`
	Meta                map[string]string `json:"meta,omitempty"`
}

// UpdateAssetColumnsRequest is the request body for updating asset columns.
type UpdateAssetColumnsRequest struct {
	Columns []Column `json:"columns"`
}

// RunRequest is the request body for running commands.
type RunRequest struct {
	PipelineID  string `json:"pipeline_id"`
	AssetPath   string `json:"asset_path"`
	Environment string `json:"environment"`
}

// OperationMetadata describes the typed backend operation behind a response.
type OperationMetadata struct {
	Type           string   `json:"type"`
	Target         string   `json:"target,omitempty"`
	PipelineID     string   `json:"pipeline_id,omitempty"`
	AssetPath      string   `json:"asset_path,omitempty"`
	RunScope       string   `json:"run_scope,omitempty"`
	AssetPaths     []string `json:"asset_paths,omitempty"`
	ConnectionName string   `json:"connection_name,omitempty"`
	Query          string   `json:"query,omitempty"`
	Limit          string   `json:"limit,omitempty"`
	Environment    string   `json:"environment,omitempty"`
	StartDate      string   `json:"start_date,omitempty"`
	EndDate        string   `json:"end_date,omitempty"`
	Operation      string   `json:"operation,omitempty"`
	TargetPath     string   `json:"target_path,omitempty"`
	ConfigFile     string   `json:"config_file,omitempty"`
}

// CommandResult represents the result of a command execution.
type CommandResult struct {
	Status    string            `json:"status"`
	Operation OperationMetadata `json:"operation"`
	Output    string            `json:"output"`
	ExitCode  int               `json:"exit_code"`
	Error     string            `json:"error,omitempty"`
	Attempts  int               `json:"attempts,omitempty"`
	Retryable bool              `json:"retryable,omitempty"`
}

// InspectResult represents the result of an asset inspection.
type InspectResult struct {
	Status                              string            `json:"status"`
	Columns                             []string          `json:"columns"`
	Rows                                []map[string]any  `json:"rows"`
	RawOutput                           string            `json:"raw_output"`
	Operation                           OperationMetadata `json:"operation"`
	Error                               string            `json:"error,omitempty"`
	Info                                string            `json:"info,omitempty"`
	MissingUpstreamAssetIDs             []string          `json:"missing_upstream_asset_ids,omitempty"`
	MissingUpstreamAssetNames           []string          `json:"missing_upstream_asset_names,omitempty"`
	MissingUpstreamAssetsMaterializable bool              `json:"missing_upstream_assets_materializable,omitempty"`
	Attempts                            int               `json:"attempts,omitempty"`
	Retryable                           bool              `json:"retryable,omitempty"`
}

// InferColumnsResult represents the result of column inference.
type InferColumnsResult struct {
	Status    string            `json:"status"`
	Columns   []Column          `json:"columns"`
	RawOutput string            `json:"raw_output"`
	Operation OperationMetadata `json:"operation"`
	Error     string            `json:"error,omitempty"`
}
