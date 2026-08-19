package model

// PresentationArtifact is a Git-native dashboard or report definition loaded
// from a *.dashboard.yml / *.report.yml file. Runtime filter values and query
// results are intentionally absent; this DTO is the authored workspace state.
type PresentationArtifact struct {
	ID             string                      `json:"id"`
	WorkspaceID    string                      `json:"workspace_id"`
	Kind           string                      `json:"kind"`
	Version        int                         `json:"version"`
	Revision       string                      `json:"revision"`
	Title          string                      `json:"title"`
	Path           string                      `json:"path"`
	Datasets       []PresentationDataset       `json:"datasets,omitempty"`
	Filters        []PresentationFilter        `json:"filters,omitempty"`
	Visualizations []PresentationVisualization `json:"visualizations,omitempty"`
	Layout         []PresentationLayoutItem    `json:"layout,omitempty"`
	Sections       []PresentationSection       `json:"sections,omitempty"`
	Problems       []PresentationFinding       `json:"problems,omitempty"`
}

type PresentationDataset struct {
	ID         string   `json:"id"`
	Asset      string   `json:"asset,omitempty"`
	Connection string   `json:"connection,omitempty"`
	Query      string   `json:"query,omitempty"`
	Columns    []Column `json:"columns,omitempty"`
}

type PresentationFilterOptions struct {
	Values     []any  `json:"values,omitempty"`
	Dataset    string `json:"dataset,omitempty"`
	ValueField string `json:"value_field,omitempty"`
	LabelField string `json:"label_field,omitempty"`
}

type PresentationFilter struct {
	ID      string                     `json:"id"`
	Label   string                     `json:"label,omitempty"`
	Type    string                     `json:"type"`
	Default any                        `json:"default"`
	Options *PresentationFilterOptions `json:"options,omitempty"`
}

type PresentationFilterBinding struct {
	Filter   string `json:"filter"`
	Dataset  string `json:"dataset,omitempty"`
	Column   string `json:"column"`
	Operator string `json:"operator"`
}

type PresentationVisualization struct {
	ID             string                      `json:"id"`
	Dataset        string                      `json:"dataset"`
	Definition     map[string]any              `json:"definition"`
	FilterBindings []PresentationFilterBinding `json:"filter_bindings,omitempty"`
}

type PresentationLayoutItem struct {
	Visualization string `json:"visualization"`
	X             int    `json:"x,omitempty"`
	Y             int    `json:"y,omitempty"`
	Width         int    `json:"width,omitempty"`
	Height        int    `json:"height,omitempty"`
}

type PresentationSection struct {
	ID            string `json:"id"`
	Title         string `json:"title,omitempty"`
	Markdown      string `json:"markdown,omitempty"`
	Visualization string `json:"visualization,omitempty"`
	PageBreak     bool   `json:"page_break,omitempty"`
}

type PresentationFinding struct {
	Code         string `json:"code"`
	Severity     string `json:"severity"`
	Message      string `json:"message"`
	Path         string `json:"path,omitempty"`
	Field        string `json:"field,omitempty"`
	PhysicalType string `json:"physical_type,omitempty"`
}

// PresentationRunRequest contains runtime-only state for a rendered dashboard
// or report. Filter values and environment selection are deliberately not
// persisted into the Git-native artifact.
type PresentationRunRequest struct {
	Environment      string         `json:"environment,omitempty"`
	FilterValues     map[string]any `json:"filter_values,omitempty"`
	VisualizationIDs []string       `json:"visualization_ids,omitempty"`
	IncludeOptions   bool           `json:"include_options,omitempty"`
}

// PresentationRunResult is a bounded runtime projection. Results are keyed by
// visualization rather than only by dataset because two visualizations may
// bind different filters to the same underlying dataset.
type PresentationRunResult struct {
	Status           string                               `json:"status"`
	ArtifactRevision string                               `json:"artifact_revision"`
	FilterValues     map[string]any                       `json:"filter_values"`
	Visualizations   map[string]PresentationDatasetResult `json:"visualizations"`
	Options          map[string]PresentationDatasetResult `json:"options,omitempty"`
}

type PresentationDatasetResult struct {
	Dataset     string   `json:"dataset"`
	Status      string   `json:"status"`
	Columns     []string `json:"columns"`
	ColumnTypes []string `json:"column_types,omitempty"`
	Rows        [][]any  `json:"rows"`
	TotalRows   int      `json:"total_rows"`
	Truncated   bool     `json:"truncated,omitempty"`
	DurationMS  int64    `json:"duration_ms"`
	Error       string   `json:"error,omitempty"`
}
