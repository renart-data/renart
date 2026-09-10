package presentation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"
)

const ArtifactVersionCurrent = 1

type ArtifactKind string

const (
	ArtifactKindDashboard ArtifactKind = "dashboard"
	ArtifactKindReport    ArtifactKind = "report"
)

// DatasetDefinition is a named, statically addressable presentation input. An
// asset-backed dataset uses Renart's workspace asset resolver. A query-backed
// dataset declares its connection and can optionally declare columns until the
// connection-aware SQL analyzer can infer them.
type DatasetDefinition struct {
	Asset      string          `yaml:"asset,omitempty" json:"asset,omitempty"`
	Connection string          `yaml:"connection,omitempty" json:"connection,omitempty"`
	Query      string          `yaml:"query,omitempty" json:"query,omitempty"`
	Columns    []DatasetColumn `yaml:"columns,omitempty" json:"columns,omitempty"`
}

type DatasetColumn struct {
	Name     string `yaml:"name" json:"name"`
	Type     string `yaml:"type" json:"type"`
	Nullable *bool  `yaml:"nullable,omitempty" json:"nullable,omitempty"`
}

type ArtifactVisualization struct {
	ID             string          `yaml:"id" json:"id"`
	Dataset        string          `yaml:"dataset" json:"dataset"`
	Definition     map[string]any  `yaml:"definition" json:"definition"`
	FilterBindings []FilterBinding `yaml:"filter_bindings,omitempty" json:"filter_bindings,omitempty"`
}

type DashboardLayoutItem struct {
	Visualization string `yaml:"visualization" json:"visualization"`
	X             int    `yaml:"x,omitempty" json:"x,omitempty"`
	Y             int    `yaml:"y,omitempty" json:"y,omitempty"`
	Width         int    `yaml:"width,omitempty" json:"width,omitempty"`
	Height        int    `yaml:"height,omitempty" json:"height,omitempty"`
}

type ReportSection struct {
	ID            string `yaml:"id" json:"id"`
	Title         string `yaml:"title,omitempty" json:"title,omitempty"`
	Markdown      string `yaml:"markdown,omitempty" json:"markdown,omitempty"`
	Visualization string `yaml:"visualization,omitempty" json:"visualization,omitempty"`
	PageBreak     bool   `yaml:"page_break,omitempty" json:"page_break,omitempty"`
}

// Artifact is the shared Git-native definition for dashboards and reports.
// Kind comes from the filename suffix, keeping the authored schema compact;
// Path, Revision, and Problems are derived and never serialized.
type Artifact struct {
	Version        int                          `yaml:"version" json:"version"`
	ID             string                       `yaml:"id" json:"id"`
	Title          string                       `yaml:"title" json:"title"`
	Datasets       map[string]DatasetDefinition `yaml:"datasets,omitempty" json:"datasets,omitempty"`
	Filters        []FilterDefinition           `yaml:"filters,omitempty" json:"filters,omitempty"`
	Visualizations []ArtifactVisualization      `yaml:"visualizations,omitempty" json:"visualizations,omitempty"`
	Layout         []DashboardLayoutItem        `yaml:"layout,omitempty" json:"layout,omitempty"`
	Sections       []ReportSection              `yaml:"sections,omitempty" json:"sections,omitempty"`
	Kind           ArtifactKind                 `yaml:"-" json:"kind"`
	Path           string                       `yaml:"-" json:"path"`
	Revision       string                       `yaml:"-" json:"revision"`
	Problems       []Finding                    `yaml:"-" json:"problems,omitempty"`
}

// DiscoverArtifacts finds dashboard.yml, report.yml, and their named
// *.dashboard.yml / *.report.yml forms. Generated and hidden trees are skipped
// under the same rules as notebook discovery.
func DiscoverArtifacts(filesystem afero.Fs, root string) ([]string, error) {
	paths := make([]string, 0)
	err := afero.Walk(filesystem, root, func(path string, info fs.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // one unreadable subtree must not hide the workspace
		}
		if info.IsDir() {
			name := info.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "venv" || name == "__pycache__") {
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := artifactKindForPath(path); ok {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

func artifactKindForPath(path string) (ArtifactKind, bool) {
	name := strings.ToLower(filepath.Base(path))
	switch {
	case name == "dashboard.yml" || strings.HasSuffix(name, ".dashboard.yml"):
		return ArtifactKindDashboard, true
	case name == "report.yml" || strings.HasSuffix(name, ".report.yml"):
		return ArtifactKindReport, true
	default:
		return "", false
	}
}

func LoadArtifact(filesystem afero.Fs, path string) (*Artifact, error) {
	content, err := afero.ReadFile(filesystem, path)
	if err != nil {
		return nil, err
	}
	return DecodeArtifact(path, content)
}

// DecodeArtifact parses an authored draft without writing it. Editors use this
// before CAS persistence so malformed YAML never replaces the last good file.
func DecodeArtifact(path string, content []byte) (*Artifact, error) {
	kind, ok := artifactKindForPath(path)
	if !ok {
		return nil, fmt.Errorf("presentation artifact path must end in dashboard.yml or report.yml")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	var artifact Artifact
	if err := decoder.Decode(&artifact); err != nil {
		return nil, fmt.Errorf("invalid %s definition: %w", kind, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("invalid %s definition: multiple YAML documents are not supported", kind)
		}
		return nil, fmt.Errorf("invalid %s definition: %w", kind, err)
	}
	artifact.Kind = kind
	artifact.Path = path
	artifact.Revision = artifactRevision(content)
	artifact.Problems = CheckArtifactDefinition(artifact)
	return &artifact, nil
}

// MarshalArtifact produces deterministic, reviewable YAML. Runtime-derived
// fields never enter the file.
func MarshalArtifact(artifact Artifact) ([]byte, error) {
	artifact.Kind = ""
	artifact.Path = ""
	artifact.Revision = ""
	artifact.Problems = nil
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(artifact); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func CheckArtifactDefinition(artifact Artifact) []Finding {
	findings := make([]Finding, 0)
	if artifact.Version != ArtifactVersionCurrent {
		findings = append(findings, Finding{
			Code: "presentation-version-unsupported", Severity: "error", Path: "version",
			Message: fmt.Sprintf("Presentation definition must declare version %d.", ArtifactVersionCurrent),
		})
	}
	if !parameterIDPattern.MatchString(strings.TrimSpace(artifact.ID)) {
		findings = append(findings, Finding{
			Code: "presentation-id-invalid", Severity: "error", Path: "id",
			Message: "Presentation id must start with a lowercase letter and contain only lowercase letters, digits, and underscores.",
		})
	}
	if strings.TrimSpace(artifact.Title) == "" {
		findings = append(findings, Finding{
			Code: "presentation-title-required", Severity: "error", Path: "title", Message: "Presentation title is required.",
		})
	}

	datasetIDs := sortedDatasetIDs(artifact.Datasets)
	for _, id := range datasetIDs {
		definition := artifact.Datasets[id]
		path := "datasets." + id
		if !parameterIDPattern.MatchString(strings.TrimSpace(id)) {
			findings = append(findings, Finding{
				Code: "presentation-dataset-id-invalid", Severity: "error", Path: path,
				Message: "Dataset ids must use lowercase snake case.",
			})
		}
		hasAsset := strings.TrimSpace(definition.Asset) != ""
		hasQuery := strings.TrimSpace(definition.Query) != ""
		if hasAsset == hasQuery {
			findings = append(findings, Finding{
				Code: "presentation-dataset-source-invalid", Severity: "error", Path: path,
				Message: "A dataset must declare exactly one of asset or query.",
			})
		}
		if hasQuery && strings.TrimSpace(definition.Connection) == "" {
			findings = append(findings, Finding{
				Code: "presentation-dataset-connection-required", Severity: "error", Path: path + ".connection",
				Message: "A query dataset requires a named connection.",
			})
		}
		if hasAsset && strings.TrimSpace(definition.Connection) != "" {
			findings = append(findings, Finding{
				Code: "presentation-dataset-connection-unexpected", Severity: "error", Path: path + ".connection",
				Message: "An asset-backed dataset derives its connection from the asset.",
			})
		}
		seenColumns := map[string]bool{}
		for index, column := range definition.Columns {
			columnPath := path + ".columns[" + strconv.Itoa(index) + "]"
			name := strings.TrimSpace(column.Name)
			if name == "" || strings.TrimSpace(column.Type) == "" {
				findings = append(findings, Finding{
					Code: "presentation-dataset-column-invalid", Severity: "error", Path: columnPath,
					Message: "Declared dataset columns require both name and type.",
				})
			}
			key := strings.ToLower(name)
			if name != "" && seenColumns[key] {
				findings = append(findings, Finding{
					Code: "presentation-dataset-column-duplicate", Severity: "error", Path: columnPath + ".name", Field: name,
					Message: fmt.Sprintf("Dataset column %q is declared more than once.", name),
				})
			}
			seenColumns[key] = true
		}
	}

	for _, finding := range CheckParameterDefinitions(artifact.Filters) {
		finding.Path = strings.Replace(finding.Path, "parameters[", "filters[", 1)
		findings = append(findings, finding)
	}
	for index, filter := range artifact.Filters {
		if filter.Options != nil && strings.TrimSpace(filter.Options.Dataset) != "" {
			if _, ok := artifact.Datasets[strings.TrimSpace(filter.Options.Dataset)]; !ok {
				findings = append(findings, Finding{
					Code: "filter-option-dataset-missing", Severity: "error",
					Path:    "filters[" + strconv.Itoa(index) + "].options.dataset",
					Message: fmt.Sprintf("Option dataset %q is not declared.", filter.Options.Dataset),
				})
			}
		}
	}

	visualizations := make(map[string]bool, len(artifact.Visualizations))
	filters := make(map[string]bool, len(artifact.Filters))
	for _, filter := range artifact.Filters {
		filters[strings.TrimSpace(filter.ID)] = true
	}
	for index, visualization := range artifact.Visualizations {
		firstFinding := len(findings)
		path := "visualizations[" + strconv.Itoa(index) + "]"
		id := strings.TrimSpace(visualization.ID)
		if !parameterIDPattern.MatchString(id) {
			findings = append(findings, Finding{
				Code: "presentation-visualization-id-invalid", Severity: "error", Path: path + ".id",
				Message: "Visualization ids must use lowercase snake case.",
			})
		} else if visualizations[id] {
			findings = append(findings, Finding{
				Code: "presentation-visualization-id-duplicate", Severity: "error", Path: path + ".id",
				Message: fmt.Sprintf("Visualization id %q is already used.", id),
			})
		}
		visualizations[id] = true
		if _, ok := artifact.Datasets[strings.TrimSpace(visualization.Dataset)]; !ok {
			findings = append(findings, Finding{
				Code: "presentation-visualization-dataset-missing", Severity: "error", Path: path + ".dataset",
				Message: fmt.Sprintf("Dataset %q is not declared.", visualization.Dataset),
			})
		}
		_, definitionFindings := DecodeVisualizationDefinition(visualization.Definition)
		for _, finding := range definitionFindings {
			finding.Path = joinFindingPath(path+".definition", finding.Path)
			findings = append(findings, finding)
		}
		for bindingIndex, binding := range visualization.FilterBindings {
			bindingPath := path + ".filter_bindings[" + strconv.Itoa(bindingIndex) + "]"
			if !filters[strings.TrimSpace(binding.Filter)] {
				findings = append(findings, Finding{
					Code: "filter-binding-filter-missing", Severity: "error", Path: bindingPath + ".filter",
					Message: fmt.Sprintf("Filter %q is not declared.", binding.Filter),
				})
			}
			dataset := strings.TrimSpace(binding.Dataset)
			if dataset == "" {
				dataset = strings.TrimSpace(visualization.Dataset)
			}
			if dataset != strings.TrimSpace(visualization.Dataset) {
				findings = append(findings, Finding{
					Code: "filter-binding-dataset-mismatch", Severity: "error", Path: bindingPath + ".dataset",
					Message: fmt.Sprintf(
						"Filter bindings for visualization %q must target its dataset %q.",
						visualization.ID,
						visualization.Dataset,
					),
				})
			}
			if _, ok := artifact.Datasets[dataset]; !ok {
				findings = append(findings, Finding{
					Code: "filter-binding-dataset-missing", Severity: "error", Path: bindingPath + ".dataset",
					Message: fmt.Sprintf("Dataset %q is not declared.", dataset),
				})
			}
			if strings.TrimSpace(binding.Column) == "" || strings.TrimSpace(binding.Operator) == "" {
				findings = append(findings, Finding{
					Code: "filter-binding-incomplete", Severity: "error", Path: bindingPath,
					Message: "A filter binding requires column and operator.",
				})
			}
		}
		for i := firstFinding; i < len(findings); i++ {
			findings[i].VisualizationID = visualization.ID
		}
	}

	// Visualization identity is captured by the producer, never reconstructed
	// from an indexed display path by navigation consumers.
	switch artifact.Kind {
	case ArtifactKindDashboard:
		if len(artifact.Sections) > 0 {
			findings = append(findings, Finding{
				Code: "dashboard-sections-unsupported", Severity: "error", Path: "sections",
				Message: "Dashboard definitions use layout, not report sections.",
			})
		}
		placed := map[string]bool{}
		for index, item := range artifact.Layout {
			path := "layout[" + strconv.Itoa(index) + "]"
			id := strings.TrimSpace(item.Visualization)
			if !visualizations[id] {
				findings = append(findings, Finding{
					Code: "dashboard-layout-visualization-missing", Severity: "error", Path: path + ".visualization",
					Message: fmt.Sprintf("Visualization %q is not declared.", item.Visualization),
				})
			} else if placed[id] {
				findings = append(findings, Finding{
					Code: "dashboard-layout-visualization-duplicate", Severity: "error", Path: path + ".visualization",
					Message: fmt.Sprintf("Visualization %q appears in the layout more than once.", id),
				})
			}
			placed[id] = true
			if item.X < 0 || item.Y < 0 || item.Width < 0 || item.Height < 0 {
				findings = append(findings, Finding{
					Code: "dashboard-layout-bounds-invalid", Severity: "error", Path: path,
					Message: "Dashboard layout coordinates and dimensions cannot be negative.",
				})
			}
		}
	case ArtifactKindReport:
		if len(artifact.Layout) > 0 {
			findings = append(findings, Finding{
				Code: "report-layout-unsupported", Severity: "error", Path: "layout",
				Message: "Report definitions use ordered sections, not dashboard layout.",
			})
		}
		sections := map[string]bool{}
		for index, section := range artifact.Sections {
			path := "sections[" + strconv.Itoa(index) + "]"
			id := strings.TrimSpace(section.ID)
			if !parameterIDPattern.MatchString(id) {
				findings = append(findings, Finding{
					Code: "report-section-id-invalid", Severity: "error", Path: path + ".id",
					Message: "Report section ids must use lowercase snake case.",
				})
			} else if sections[id] {
				findings = append(findings, Finding{
					Code: "report-section-id-duplicate", Severity: "error", Path: path + ".id",
					Message: fmt.Sprintf("Report section id %q is already used.", id),
				})
			}
			sections[id] = true
			hasMarkdown := strings.TrimSpace(section.Markdown) != ""
			hasVisualization := strings.TrimSpace(section.Visualization) != ""
			if hasMarkdown == hasVisualization {
				findings = append(findings, Finding{
					Code: "report-section-content-invalid", Severity: "error", Path: path,
					Message: "A report section must contain exactly one of markdown or visualization.",
				})
			}
			if hasVisualization && !visualizations[strings.TrimSpace(section.Visualization)] {
				findings = append(findings, Finding{
					Code: "report-section-visualization-missing", Severity: "error", Path: path + ".visualization",
					Message: fmt.Sprintf("Visualization %q is not declared.", section.Visualization),
				})
			}
		}
	default:
		findings = append(findings, Finding{
			Code: "presentation-kind-unsupported", Severity: "error", Message: fmt.Sprintf("Presentation kind %q is not supported.", artifact.Kind),
		})
	}

	sortFindings(findings)
	return findings
}

// CheckArtifact applies the shared strict-or-exploratory schema rules to every
// filter and visualization in a dashboard/report. Dataset resolution remains a
// host concern; the checker receives only portable resolved schemas.
func (Checker) CheckArtifact(
	ctx context.Context,
	artifact Artifact,
	datasets map[string]ResolvedSchema,
	options CheckOptions,
) []Finding {
	findings := append([]Finding(nil), CheckArtifactDefinition(artifact)...)
	for _, id := range sortedDatasetIDs(artifact.Datasets) {
		if _, ok := datasets[id]; !ok {
			findings = append(findings, Finding{
				Code: "presentation-dataset-schema-unresolved", Severity: severityForUnknown(options), Path: "datasets." + id,
				Message: fmt.Sprintf("The schema for dataset %q could not be resolved.", id),
			})
		}
	}

	optionFindings := CheckFilterBindings(artifact.Filters, datasets, nil, options)
	for _, finding := range optionFindings {
		if strings.HasPrefix(finding.Path, "parameters[") {
			// Definition findings are already emitted by CheckArtifactDefinition.
			continue
		}
		findings = append(findings, finding)
	}

	for index, visualization := range artifact.Visualizations {
		firstFinding := len(findings)
		path := "visualizations[" + strconv.Itoa(index) + "]"
		schema, schemaOK := datasets[strings.TrimSpace(visualization.Dataset)]
		definition, decodeFindings := DecodeVisualizationDefinition(visualization.Definition)
		if len(decodeFindings) == 0 && schemaOK {
			for _, finding := range (Checker{}).CheckVisualization(ctx, definition, schema, options) {
				finding.Path = joinFindingPath(path+".definition", finding.Path)
				findings = append(findings, finding)
			}
		}

		normalizedBindings := make([]FilterBinding, len(visualization.FilterBindings))
		for bindingIndex, binding := range visualization.FilterBindings {
			normalizedBindings[bindingIndex] = binding
			if strings.TrimSpace(normalizedBindings[bindingIndex].Dataset) == "" {
				normalizedBindings[bindingIndex].Dataset = visualization.Dataset
			}
		}
		for _, finding := range CheckFilterBindings(artifact.Filters, datasets, normalizedBindings, options) {
			if !strings.HasPrefix(finding.Path, "filter_bindings[") {
				continue
			}
			finding.Path = joinFindingPath(path, finding.Path)
			findings = append(findings, finding)
		}
		for i := firstFinding; i < len(findings); i++ {
			findings[i].VisualizationID = visualization.ID
		}
	}

	findings = dedupeFindings(findings)
	sortFindings(findings)
	return findings
}

func dedupeFindings(findings []Finding) []Finding {
	seen := make(map[string]bool, len(findings))
	result := make([]Finding, 0, len(findings))
	for _, finding := range findings {
		key := finding.Code + "\x00" + finding.Severity + "\x00" + finding.Path + "\x00" + finding.Field + "\x00" + finding.Message
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, finding)
	}
	return result
}

func sortedDatasetIDs(datasets map[string]DatasetDefinition) []string {
	ids := make([]string, 0, len(datasets))
	for id := range datasets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func joinFindingPath(prefix, suffix string) string {
	if strings.TrimSpace(suffix) == "" {
		return prefix
	}
	return prefix + "." + suffix
}

func artifactRevision(content []byte) string {
	sum := sha256.Sum256(content)
	return "v1:" + hex.EncodeToString(sum[:])
}
