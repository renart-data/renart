// Package presentation defines Renart's versioned visualization contract and
// static checker. It is deliberately independent of the browser renderer so
// notebook, dashboard, report, CLI, and deployment checks use identical rules.
package presentation

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
	"renart/internal/web/model"
)

const DefinitionVersionCurrent = 1

type SemanticType string

const (
	SemanticUnknown        SemanticType = "unknown"
	SemanticNumeric        SemanticType = "numeric"
	SemanticTemporal       SemanticType = "temporal"
	SemanticCategorical    SemanticType = "categorical"
	SemanticBoolean        SemanticType = "boolean"
	SemanticBinary         SemanticType = "binary"
	SemanticSemiStructured SemanticType = "semi_structured"
	SemanticGeospatial     SemanticType = "geospatial"
)

type DataSourceRef struct {
	Kind        string `json:"kind"`
	ArtifactID  string `json:"artifact_id"`
	ComponentID string `json:"component_id,omitempty"`
}

type ResolvedColumn struct {
	Name         string       `json:"name"`
	PhysicalType string       `json:"physical_type"`
	SemanticType SemanticType `json:"semantic_type"`
	Nullable     *bool        `json:"nullable,omitempty"`
}

type ResolvedSchema struct {
	Source   DataSourceRef    `json:"source"`
	Columns  []ResolvedColumn `json:"columns"`
	Complete bool             `json:"complete"`
	Sampled  bool             `json:"sampled"`
}

type FieldEncoding struct {
	Field  string `json:"field"`
	Label  string `json:"label,omitempty"`
	Format string `json:"format,omitempty"`
}

type VisualizationEncoding struct {
	X       *FieldEncoding  `json:"x,omitempty"`
	Y       []FieldEncoding `json:"y,omitempty"`
	Series  *FieldEncoding  `json:"series,omitempty"`
	Color   *FieldEncoding  `json:"color,omitempty"`
	Tooltip []FieldEncoding `json:"tooltip,omitempty"`
}

type VisualizationDefinition struct {
	Version           int                   `json:"version"`
	Type              string                `json:"type"`
	Title             string                `json:"title,omitempty"`
	Encoding          VisualizationEncoding `json:"encoding,omitempty"`
	Columns           []FieldEncoding       `json:"columns,omitempty"`
	Value             *FieldEncoding        `json:"value,omitempty"`
	Compare           *FieldEncoding        `json:"compare,omitempty"`
	Stacked           bool                  `json:"stacked,omitempty"`
	ShowLegend        *bool                 `json:"show_legend,omitempty"`
	RequireComplete   bool                  `json:"require_complete,omitempty"`
	PresentationLimit int                   `json:"presentation_limit,omitempty"`
}

type Finding struct {
	Code         string `json:"code"`
	Severity     string `json:"severity"`
	Message      string `json:"message"`
	Path         string `json:"path,omitempty"`
	Field        string `json:"field,omitempty"`
	PhysicalType string `json:"physical_type,omitempty"`
}

type CheckOptions struct {
	// Strict turns unknown source fields/types into blockers. Notebook
	// exploration uses warnings; deployable presentation artifacts use strict.
	Strict bool
}

type PresentationSchemaResolver interface {
	ResolveSource(ctx context.Context, ref DataSourceRef) (ResolvedSchema, error)
}

type PresentationTypeChecker interface {
	CheckVisualization(ctx context.Context, definition VisualizationDefinition, schema ResolvedSchema, options CheckOptions) []Finding
}

type Checker struct{}

// DecodeVisualizationDefinitionYAML parses the user-facing Definition editor
// value into the same map stored in notebook.yml. Keeping YAML decoding in the
// backend means every UI and MCP client gets identical scalar and collection
// semantics instead of maintaining a second visualization grammar.
func DecodeVisualizationDefinitionYAML(content string) (map[string]any, VisualizationDefinition, []Finding) {
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		return nil, VisualizationDefinition{}, []Finding{{
			Code: "visualization-definition-invalid", Severity: "error", Message: err.Error(),
		}}
	}
	definition, findings := DecodeVisualizationDefinition(raw)
	return raw, definition, findings
}

// EncodeVisualizationDefinitionYAML returns the canonical fragment shown by
// the Definition editor. yaml.v3 sorts string map keys, so the same parsed
// definition always produces the same reviewable text.
func EncodeVisualizationDefinitionYAML(raw map[string]any) (string, error) {
	if raw == nil {
		return "", fmt.Errorf("visualization definition is missing")
	}
	content, err := yaml.Marshal(raw)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func DecodeVisualizationDefinition(raw map[string]any) (VisualizationDefinition, []Finding) {
	if raw == nil {
		return VisualizationDefinition{}, []Finding{{
			Code: "visualization-definition-missing", Severity: "error", Message: "Visualization definition is missing.",
		}}
	}
	content, err := json.Marshal(raw)
	if err != nil {
		return VisualizationDefinition{}, []Finding{{
			Code: "visualization-definition-invalid", Severity: "error", Message: err.Error(),
		}}
	}
	var definition VisualizationDefinition
	if err := json.Unmarshal(content, &definition); err != nil {
		return VisualizationDefinition{}, []Finding{{
			Code: "visualization-definition-invalid", Severity: "error", Message: err.Error(),
		}}
	}
	findings := make([]Finding, 0)
	if definition.Version == 0 {
		findings = append(findings, Finding{
			Code: "visualization-version-required", Severity: "error", Path: "version",
			Message: fmt.Sprintf("Visualization definition must declare version %d.", DefinitionVersionCurrent),
		})
	} else if definition.Version != DefinitionVersionCurrent {
		findings = append(findings, Finding{
			Code: "visualization-version-unsupported", Severity: "error", Path: "version",
			Message: fmt.Sprintf("Visualization definition version %d is not supported.", definition.Version),
		})
	}
	definition.Type = strings.ToLower(strings.TrimSpace(definition.Type))
	if !supportedVisualizationTypes[definition.Type] {
		findings = append(findings, Finding{
			Code: "visualization-type-unsupported", Severity: "error", Path: "type",
			Message: "Visualization type must be table, kpi, bar, line, area, scatter, pie, or donut.",
		})
	}
	if definition.PresentationLimit < 0 {
		findings = append(findings, Finding{
			Code: "visualization-limit-invalid", Severity: "error", Path: "presentation_limit",
			Message: "Presentation limit cannot be negative.",
		})
	}
	return definition, findings
}

var supportedVisualizationTypes = map[string]bool{
	"table": true, "kpi": true, "bar": true, "line": true, "area": true,
	"scatter": true, "pie": true, "donut": true,
}

func (Checker) CheckVisualization(_ context.Context, definition VisualizationDefinition, schema ResolvedSchema, options CheckOptions) []Finding {
	findings := make([]Finding, 0)
	if definition.RequireComplete && (!schema.Complete || schema.Sampled) {
		findings = append(findings, Finding{
			Code: "visualization-requires-complete-data", Severity: severityForUnknown(options),
			Message: "This visualization requires complete data, but its source is sampled or completeness is unknown.",
		})
	}
	columns := make(map[string]ResolvedColumn, len(schema.Columns))
	for _, column := range schema.Columns {
		column.SemanticType = semanticTypeWithFallback(column.SemanticType, column.PhysicalType)
		columns[strings.ToLower(strings.TrimSpace(column.Name))] = column
	}

	require := func(encoding *FieldEncoding, path string, allowed ...SemanticType) {
		if encoding == nil || strings.TrimSpace(encoding.Field) == "" {
			findings = append(findings, Finding{
				Code: "visualization-field-required", Severity: "error", Path: path,
				Message: fmt.Sprintf("%s requires a field.", humanDefinitionPath(path)),
			})
			return
		}
		findings = append(findings, checkField(*encoding, path, columns, options, allowed...)...)
	}
	checkOptional := func(encoding *FieldEncoding, path string, allowed ...SemanticType) {
		if encoding != nil {
			findings = append(findings, checkField(*encoding, path, columns, options, allowed...)...)
		}
	}
	checkMany := func(encodings []FieldEncoding, path string, allowed ...SemanticType) {
		for index := range encodings {
			findings = append(findings, checkField(encodings[index], path+"["+strconv.Itoa(index)+"]", columns, options, allowed...)...)
		}
	}

	switch definition.Type {
	case "table":
		checkMany(definition.Columns, "columns")
	case "kpi":
		require(definition.Value, "value", SemanticNumeric)
		checkOptional(definition.Compare, "compare", SemanticNumeric)
	case "bar", "line", "area":
		require(definition.Encoding.X, "encoding.x")
		if len(definition.Encoding.Y) == 0 {
			require(nil, "encoding.y")
		} else {
			checkMany(definition.Encoding.Y, "encoding.y", SemanticNumeric)
		}
	case "scatter":
		require(definition.Encoding.X, "encoding.x", SemanticNumeric, SemanticTemporal)
		if len(definition.Encoding.Y) == 0 {
			require(nil, "encoding.y")
		} else {
			checkMany(definition.Encoding.Y, "encoding.y", SemanticNumeric, SemanticTemporal)
		}
	case "pie", "donut":
		require(definition.Encoding.X, "encoding.x", SemanticCategorical, SemanticBoolean, SemanticTemporal)
		if len(definition.Encoding.Y) == 0 {
			require(nil, "encoding.y")
		} else {
			checkMany(definition.Encoding.Y[:1], "encoding.y", SemanticNumeric)
		}
	}
	checkOptional(definition.Encoding.Series, "encoding.series", SemanticCategorical, SemanticBoolean)
	checkOptional(definition.Encoding.Color, "encoding.color", SemanticCategorical, SemanticBoolean)
	checkMany(definition.Encoding.Tooltip, "encoding.tooltip")
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Path == findings[j].Path {
			return findings[i].Code < findings[j].Code
		}
		return findings[i].Path < findings[j].Path
	})
	return findings
}

func checkField(encoding FieldEncoding, path string, columns map[string]ResolvedColumn, options CheckOptions, allowed ...SemanticType) []Finding {
	field := strings.TrimSpace(encoding.Field)
	if field == "" {
		return []Finding{{Code: "visualization-field-required", Severity: "error", Path: path + ".field", Message: "Field name is required."}}
	}
	column, ok := columns[strings.ToLower(field)]
	if !ok {
		return []Finding{{
			Code: "visualization-field-missing", Severity: severityForUnknown(options), Path: path + ".field", Field: field,
			Message: fmt.Sprintf("Column %q does not exist in the referenced data.", field),
		}}
	}
	semantic := semanticTypeWithFallback(column.SemanticType, column.PhysicalType)
	findings := make([]Finding, 0)
	if semantic == SemanticUnknown {
		findings = append(findings, Finding{
			Code: "visualization-field-type-unknown", Severity: severityForUnknown(options), Path: path + ".field",
			Field: field, PhysicalType: column.PhysicalType,
			Message: fmt.Sprintf("The type of column %q is unknown.", field),
		})
	} else if len(allowed) > 0 && !containsSemanticType(allowed, semantic) {
		findings = append(findings, Finding{
			Code: "visualization-field-type-incompatible", Severity: "error", Path: path + ".field",
			Field: field, PhysicalType: column.PhysicalType,
			Message: fmt.Sprintf("Column %q has type %s; this encoding expects %s.", field, semantic, joinSemanticTypes(allowed)),
		})
	}
	if strings.TrimSpace(encoding.Format) != "" {
		format := strings.ToLower(strings.TrimSpace(encoding.Format))
		if (format == "currency" || format == "percent" || format == "number") && semantic != SemanticNumeric {
			findings = append(findings, Finding{
				Code: "visualization-format-incompatible", Severity: "error", Path: path + ".format",
				Field: field, PhysicalType: column.PhysicalType,
				Message: fmt.Sprintf("Format %q requires a numeric column.", format),
			})
		}
		if (format == "date" || format == "datetime") && semantic != SemanticTemporal {
			findings = append(findings, Finding{
				Code: "visualization-format-incompatible", Severity: "error", Path: path + ".format",
				Field: field, PhysicalType: column.PhysicalType,
				Message: fmt.Sprintf("Format %q requires a temporal column.", format),
			})
		}
	}
	return findings
}

func SemanticTypeForPhysicalType(physicalType string) SemanticType {
	value := strings.ToLower(strings.TrimSpace(physicalType))
	if value == "" {
		return SemanticUnknown
	}
	base := value
	if index := strings.IndexAny(base, "(<["); index >= 0 {
		base = strings.TrimSpace(base[:index])
	}
	switch {
	case containsTypeToken(base, "int", "int2", "int4", "int8", "integer", "tinyint", "smallint", "bigint", "hugeint", "utinyint", "usmallint", "uinteger", "ubigint", "decimal", "numeric", "number", "real", "float", "double", "money"):
		return SemanticNumeric
	case containsTypeToken(base, "date", "time", "timetz", "timestamp", "timestamptz", "datetime", "interval"):
		return SemanticTemporal
	case containsTypeToken(base, "bool", "boolean"):
		return SemanticBoolean
	case containsTypeToken(base, "blob", "binary", "varbinary", "bytea"):
		return SemanticBinary
	case containsTypeToken(base, "json", "jsonb", "struct", "map", "list", "array", "variant", "object") || strings.HasSuffix(value, "[]"):
		return SemanticSemiStructured
	case containsTypeToken(base, "geometry", "geography"):
		return SemanticGeospatial
	case containsTypeToken(base, "varchar", "char", "character", "string", "text", "uuid", "enum"):
		return SemanticCategorical
	default:
		return SemanticUnknown
	}
}

func ColumnsFromModel(columns []model.Column) []ResolvedColumn {
	result := make([]ResolvedColumn, 0, len(columns))
	for _, column := range columns {
		physicalType := strings.TrimSpace(column.Type)
		result = append(result, ResolvedColumn{
			Name: column.Name, PhysicalType: physicalType,
			SemanticType: SemanticTypeForPhysicalType(physicalType), Nullable: column.Nullable,
		})
	}
	return result
}

func severityForUnknown(options CheckOptions) string {
	if options.Strict {
		return "error"
	}
	return "warning"
}

func semanticTypeWithFallback(semantic SemanticType, physical string) SemanticType {
	if semantic != "" && semantic != SemanticUnknown {
		return semantic
	}
	return SemanticTypeForPhysicalType(physical)
}

func containsSemanticType(values []SemanticType, target SemanticType) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsTypeToken(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

func joinSemanticTypes(values []SemanticType) string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = string(value)
	}
	return strings.Join(parts, " or ")
}

func humanDefinitionPath(path string) string {
	return strings.ReplaceAll(path, ".", " ")
}
