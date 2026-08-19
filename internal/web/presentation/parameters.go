package presentation

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ParameterType is the portable control/value grammar shared by notebook
// parameters and future dashboard/report filters. UI control shape and value
// type intentionally travel together so defaults, URL values, bindings, and
// runtime overrides can be checked without executing a query.
type ParameterType string

const (
	ParameterTypeSelect      ParameterType = "select"
	ParameterTypeMultiSelect ParameterType = "multi_select"
	ParameterTypeDate        ParameterType = "date"
	ParameterTypeDateRange   ParameterType = "date_range"
	ParameterTypeNumber      ParameterType = "number"
	ParameterTypeSlider      ParameterType = "slider"
	ParameterTypeText        ParameterType = "text"
	ParameterTypeBoolean     ParameterType = "boolean"
)

// ParameterOptions constrains a value either with authored static values or a
// named dataset. Dataset-backed options are resolved by each document host;
// notebook hosts read them from an already-materialized local cell result.
type ParameterOptions struct {
	Values     []any  `yaml:"values,omitempty" json:"values,omitempty"`
	Dataset    string `yaml:"dataset,omitempty" json:"dataset,omitempty"`
	ValueField string `yaml:"value_field,omitempty" json:"value_field,omitempty"`
	LabelField string `yaml:"label_field,omitempty" json:"label_field,omitempty"`
}

// ParameterDefinition is an authored, stable, typed value declaration. Default
// is Git-tracked; current runtime values are supplied separately and never
// rewritten into the definition merely because a notebook ran.
type ParameterDefinition struct {
	ID      string            `yaml:"id" json:"id"`
	Label   string            `yaml:"label,omitempty" json:"label,omitempty"`
	Type    ParameterType     `yaml:"type" json:"type"`
	Default any               `yaml:"default" json:"default"`
	Min     *float64          `yaml:"min,omitempty" json:"min,omitempty"`
	Max     *float64          `yaml:"max,omitempty" json:"max,omitempty"`
	Step    *float64          `yaml:"step,omitempty" json:"step,omitempty"`
	Options *ParameterOptions `yaml:"options,omitempty" json:"options,omitempty"`
}

// FilterDefinition deliberately aliases the same portable value contract.
// Dashboard/report hosts add bindings and dataset-backed options, but they do
// not get a second scalar grammar.
type FilterDefinition = ParameterDefinition

type FilterBinding struct {
	Filter   string `yaml:"filter" json:"filter"`
	Dataset  string `yaml:"dataset" json:"dataset"`
	Column   string `yaml:"column" json:"column"`
	Operator string `yaml:"operator" json:"operator"`
}

var parameterIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

var supportedParameterTypes = map[ParameterType]bool{
	ParameterTypeSelect: true, ParameterTypeMultiSelect: true,
	ParameterTypeDate: true, ParameterTypeDateRange: true,
	ParameterTypeNumber: true, ParameterTypeSlider: true,
	ParameterTypeText: true, ParameterTypeBoolean: true,
}

// CheckParameterDefinitions validates authored identity, type/defaults, and
// static options. It is deterministic and side-effect free so loaders,
// transactions, MCP, dashboards, and HTTP handlers can use the same findings.
func CheckParameterDefinitions(definitions []ParameterDefinition) []Finding {
	findings := make([]Finding, 0)
	seen := make(map[string]int, len(definitions))
	for index, definition := range definitions {
		path := "parameters[" + strconv.Itoa(index) + "]"
		id := strings.TrimSpace(definition.ID)
		parameterType := ParameterType(strings.ToLower(strings.TrimSpace(string(definition.Type))))
		if !parameterIDPattern.MatchString(id) {
			findings = append(findings, Finding{
				Code: "parameter-id-invalid", Severity: "error", Path: path + ".id",
				Message: "Parameter ids must start with a lowercase letter and contain only lowercase letters, digits, and underscores.",
			})
		} else if previous, ok := seen[id]; ok {
			findings = append(findings, Finding{
				Code: "parameter-id-duplicate", Severity: "error", Path: path + ".id",
				Message: fmt.Sprintf("Parameter id %q is already used by parameters[%d].", id, previous),
			})
		} else {
			seen[id] = index
		}
		if !supportedParameterTypes[parameterType] {
			findings = append(findings, Finding{
				Code: "parameter-type-unsupported", Severity: "error", Path: path + ".type",
				Message: "Parameter type must be select, multi_select, date, date_range, number, slider, text, or boolean.",
			})
			continue
		}
		if message := parameterValueProblem(parameterType, definition.Default); message != "" {
			findings = append(findings, Finding{
				Code: "parameter-default-invalid", Severity: "error", Path: path + ".default", Message: message,
			})
		}
		findings = append(findings, checkParameterRange(definition, parameterType, path)...)
		findings = append(findings, checkParameterOptions(definition, parameterType, path)...)
	}
	sortFindings(findings)
	return findings
}

func checkParameterRange(definition ParameterDefinition, parameterType ParameterType, path string) []Finding {
	if parameterType != ParameterTypeSlider {
		return nil
	}
	findings := make([]Finding, 0)
	min, max, _, problem := sliderBounds(definition)
	if problem != "" {
		findings = append(findings, Finding{
			Code: "parameter-slider-range-invalid", Severity: "error", Path: path,
			Message: problem,
		})
		return findings
	}
	if value, ok := finiteNumberValue(definition.Default); ok && (value < min || value > max) {
		findings = append(findings, Finding{
			Code: "parameter-slider-default-out-of-range", Severity: "error", Path: path + ".default",
			Message: fmt.Sprintf("The slider default must be between %v and %v.", min, max),
		})
	}
	return findings
}

func sliderBounds(definition ParameterDefinition) (float64, float64, float64, string) {
	min, max, step := 0.0, 100.0, 1.0
	if definition.Min != nil {
		min = *definition.Min
	}
	if definition.Max != nil {
		max = *definition.Max
	}
	if definition.Step != nil {
		step = *definition.Step
	}
	if math.IsNaN(min) || math.IsInf(min, 0) || math.IsNaN(max) || math.IsInf(max, 0) {
		return 0, 0, 0, "Slider minimum and maximum must be finite numbers."
	}
	if max <= min {
		return 0, 0, 0, "Slider maximum must be greater than its minimum."
	}
	if math.IsNaN(step) || math.IsInf(step, 0) || step <= 0 {
		return 0, 0, 0, "Slider step must be a positive finite number."
	}
	return min, max, step, ""
}

func checkParameterOptions(definition ParameterDefinition, parameterType ParameterType, path string) []Finding {
	if definition.Options == nil {
		return nil
	}
	options := definition.Options
	findings := make([]Finding, 0)
	hasStatic := len(options.Values) > 0
	hasDataset := strings.TrimSpace(options.Dataset) != "" || strings.TrimSpace(options.ValueField) != "" || strings.TrimSpace(options.LabelField) != ""
	if hasStatic && hasDataset {
		findings = append(findings, Finding{
			Code: "parameter-options-ambiguous", Severity: "error", Path: path + ".options",
			Message: "Options must use either static values or a dataset, not both.",
		})
	}
	optionType := parameterType
	if optionType == ParameterTypeMultiSelect {
		optionType = ParameterTypeSelect
	}
	seen := map[string]bool{}
	for index, option := range options.Values {
		optionPath := path + ".options.values[" + strconv.Itoa(index) + "]"
		if message := parameterValueProblem(optionType, option); message != "" {
			findings = append(findings, Finding{
				Code: "parameter-option-invalid", Severity: "error", Path: optionPath, Message: message,
			})
			continue
		}
		key := comparableValueKey(option)
		if seen[key] {
			findings = append(findings, Finding{
				Code: "parameter-option-duplicate", Severity: "error", Path: optionPath,
				Message: "Static option values must be unique.",
			})
		}
		seen[key] = true
	}
	if hasStatic && parameterValueProblem(parameterType, definition.Default) == "" {
		defaults := []any{definition.Default}
		if parameterType == ParameterTypeMultiSelect {
			defaults, _ = valueSlice(definition.Default)
		}
		for _, value := range defaults {
			if !seen[comparableValueKey(value)] {
				findings = append(findings, Finding{
					Code: "parameter-default-not-in-options", Severity: "error", Path: path + ".default",
					Message: "The default value must be present in the static options.",
				})
				break
			}
		}
	}
	if hasDataset {
		if strings.TrimSpace(options.Dataset) == "" || strings.TrimSpace(options.ValueField) == "" {
			findings = append(findings, Finding{
				Code: "parameter-option-dataset-incomplete", Severity: "error", Path: path + ".options",
				Message: "Dataset-backed options require dataset and value_field.",
			})
		}
	}
	return findings
}

// ResolveParameterValues applies runtime overrides to authored defaults and
// validates every resulting value. Unknown overrides are errors rather than
// silently ignored typos.
func ResolveParameterValues(definitions []ParameterDefinition, overrides map[string]any) (map[string]any, []Finding) {
	findings := CheckParameterDefinitions(definitions)
	resolved := make(map[string]any, len(definitions))
	byID := make(map[string]ParameterDefinition, len(definitions))
	for _, definition := range definitions {
		id := strings.TrimSpace(definition.ID)
		byID[id] = definition
		resolved[id] = cloneParameterValue(definition.Default)
	}
	for rawID, value := range overrides {
		id := strings.TrimSpace(rawID)
		definition, ok := byID[id]
		if !ok {
			findings = append(findings, Finding{
				Code: "parameter-override-unknown", Severity: "error", Path: "parameter_values." + id,
				Message: fmt.Sprintf("Runtime value references unknown parameter %q.", id),
			})
			continue
		}
		parameterType := ParameterType(strings.ToLower(strings.TrimSpace(string(definition.Type))))
		if message := parameterValueProblem(parameterType, value); message != "" {
			findings = append(findings, Finding{
				Code: "parameter-value-invalid", Severity: "error", Path: "parameter_values." + id, Message: message,
			})
			continue
		}
		if parameterType == ParameterTypeSlider {
			min, max, _, problem := sliderBounds(definition)
			numeric, _ := finiteNumberValue(value)
			if problem == "" && (numeric < min || numeric > max) {
				findings = append(findings, Finding{
					Code: "parameter-value-out-of-range", Severity: "error", Path: "parameter_values." + id,
					Message: fmt.Sprintf("Runtime value for %q must be between %v and %v.", id, min, max),
				})
				continue
			}
		}
		if definition.Options != nil && len(definition.Options.Values) > 0 && !valueAllowed(parameterType, value, definition.Options.Values) {
			findings = append(findings, Finding{
				Code: "parameter-value-not-in-options", Severity: "error", Path: "parameter_values." + id,
				Message: fmt.Sprintf("Runtime value for %q is not present in its static options.", id),
			})
			continue
		}
		resolved[id] = cloneParameterValue(value)
	}
	sortFindings(findings)
	return resolved, findings
}

// CheckFilterBindings validates dataset-backed options and typed dataset
// bindings on top of the shared parameter grammar.
func CheckFilterBindings(definitions []FilterDefinition, datasets map[string]ResolvedSchema, bindings []FilterBinding, options CheckOptions) []Finding {
	findings := CheckParameterDefinitions(definitions)
	byID := make(map[string]FilterDefinition, len(definitions))
	for index, definition := range definitions {
		byID[strings.TrimSpace(definition.ID)] = definition
		if definition.Options == nil || strings.TrimSpace(definition.Options.Dataset) == "" {
			continue
		}
		path := "filters[" + strconv.Itoa(index) + "].options"
		schema, ok := datasets[strings.TrimSpace(definition.Options.Dataset)]
		if !ok {
			findings = append(findings, Finding{
				Code: "filter-option-dataset-missing", Severity: severityForUnknown(options), Path: path + ".dataset",
				Message: fmt.Sprintf("Option dataset %q could not be resolved.", definition.Options.Dataset),
			})
			continue
		}
		findings = append(findings, checkFilterOptionField(definition, schema, definition.Options.ValueField, path+".value_field", true, options)...)
		if strings.TrimSpace(definition.Options.LabelField) != "" {
			findings = append(findings, checkFilterOptionField(definition, schema, definition.Options.LabelField, path+".label_field", false, options)...)
		}
	}
	for index, binding := range bindings {
		path := "filter_bindings[" + strconv.Itoa(index) + "]"
		definition, ok := byID[strings.TrimSpace(binding.Filter)]
		if !ok {
			findings = append(findings, Finding{
				Code: "filter-binding-filter-missing", Severity: "error", Path: path + ".filter",
				Message: fmt.Sprintf("Filter %q is not declared.", binding.Filter),
			})
			continue
		}
		schema, ok := datasets[strings.TrimSpace(binding.Dataset)]
		if !ok {
			findings = append(findings, Finding{
				Code: "filter-binding-dataset-missing", Severity: severityForUnknown(options), Path: path + ".dataset",
				Message: fmt.Sprintf("Dataset %q could not be resolved.", binding.Dataset),
			})
			continue
		}
		column, exists := resolvedColumn(schema, binding.Column)
		if !exists {
			findings = append(findings, Finding{
				Code: "filter-binding-column-missing", Severity: severityForUnknown(options), Path: path + ".column", Field: binding.Column,
				Message: fmt.Sprintf("Column %q does not exist in dataset %q.", binding.Column, binding.Dataset),
			})
			continue
		}
		operator := strings.ToLower(strings.TrimSpace(binding.Operator))
		if !filterOperatorAllowed(definition.Type, operator) {
			findings = append(findings, Finding{
				Code: "filter-binding-operator-incompatible", Severity: "error", Path: path + ".operator",
				Message: fmt.Sprintf("Operator %q is not valid for a %s filter.", binding.Operator, definition.Type),
			})
		}
		if !filterColumnCompatible(definition.Type, semanticTypeWithFallback(column.SemanticType, column.PhysicalType)) {
			findings = append(findings, Finding{
				Code: "filter-binding-type-incompatible", Severity: "error", Path: path + ".column", Field: binding.Column, PhysicalType: column.PhysicalType,
				Message: fmt.Sprintf("Column %q has type %s, which is incompatible with a %s filter.", binding.Column, column.SemanticType, definition.Type),
			})
		}
	}
	sortFindings(findings)
	return findings
}

func checkFilterOptionField(definition FilterDefinition, schema ResolvedSchema, field, path string, valueField bool, options CheckOptions) []Finding {
	column, ok := resolvedColumn(schema, field)
	if !ok {
		return []Finding{{
			Code: "filter-option-field-missing", Severity: severityForUnknown(options), Path: path, Field: field,
			Message: fmt.Sprintf("Option field %q does not exist in dataset %q.", field, definition.Options.Dataset),
		}}
	}
	semantic := semanticTypeWithFallback(column.SemanticType, column.PhysicalType)
	if valueField && !filterColumnCompatible(definition.Type, semantic) {
		return []Finding{{
			Code: "filter-option-field-type-incompatible", Severity: "error", Path: path, Field: field, PhysicalType: column.PhysicalType,
			Message: fmt.Sprintf("Option field %q is incompatible with a %s filter.", field, definition.Type),
		}}
	}
	if !valueField && semantic != SemanticCategorical && semantic != SemanticUnknown {
		return []Finding{{
			Code: "filter-option-label-type-incompatible", Severity: "error", Path: path, Field: field, PhysicalType: column.PhysicalType,
			Message: fmt.Sprintf("Option label field %q must be categorical.", field),
		}}
	}
	return nil
}

func resolvedColumn(schema ResolvedSchema, name string) (ResolvedColumn, bool) {
	for _, column := range schema.Columns {
		if strings.EqualFold(strings.TrimSpace(column.Name), strings.TrimSpace(name)) {
			return column, true
		}
	}
	return ResolvedColumn{}, false
}

func filterOperatorAllowed(parameterType ParameterType, operator string) bool {
	allowed := map[ParameterType]map[string]bool{
		ParameterTypeSelect:      {"equals": true, "not_equals": true},
		ParameterTypeMultiSelect: {"in": true, "not_in": true},
		ParameterTypeDate:        {"equals": true, "before": true, "after": true, "on_or_before": true, "on_or_after": true},
		ParameterTypeDateRange:   {"between": true},
		ParameterTypeNumber:      {"equals": true, "not_equals": true, "lt": true, "lte": true, "gt": true, "gte": true},
		ParameterTypeSlider:      {"equals": true, "not_equals": true, "lt": true, "lte": true, "gt": true, "gte": true},
		ParameterTypeText:        {"equals": true, "not_equals": true, "contains": true, "starts_with": true},
		ParameterTypeBoolean:     {"equals": true},
	}
	return allowed[parameterType][operator]
}

func filterColumnCompatible(parameterType ParameterType, semantic SemanticType) bool {
	if semantic == SemanticUnknown {
		return true
	}
	switch parameterType {
	case ParameterTypeDate, ParameterTypeDateRange:
		return semantic == SemanticTemporal
	case ParameterTypeNumber, ParameterTypeSlider:
		return semantic == SemanticNumeric
	case ParameterTypeBoolean:
		return semantic == SemanticBoolean
	case ParameterTypeSelect, ParameterTypeMultiSelect, ParameterTypeText:
		return semantic == SemanticCategorical || semantic == SemanticBoolean
	default:
		return false
	}
}

func parameterValueProblem(parameterType ParameterType, value any) string {
	if value == nil {
		return "A typed default value is required."
	}
	switch parameterType {
	case ParameterTypeSelect, ParameterTypeText:
		if _, ok := value.(string); !ok {
			return fmt.Sprintf("A %s value must be a string.", parameterType)
		}
	case ParameterTypeMultiSelect:
		values, ok := valueSlice(value)
		if !ok {
			return "A multi_select value must be a list of strings."
		}
		for _, item := range values {
			if _, ok := item.(string); !ok {
				return "A multi_select value must be a list of strings."
			}
		}
	case ParameterTypeDate:
		text, ok := value.(string)
		if !ok || !validISODate(text) {
			return "A date value must use YYYY-MM-DD."
		}
	case ParameterTypeDateRange:
		values, ok := valueSlice(value)
		if !ok || len(values) != 2 {
			return "A date_range value must contain exactly two YYYY-MM-DD values."
		}
		start, startOK := values[0].(string)
		end, endOK := values[1].(string)
		if !startOK || !endOK || !validISODate(start) || !validISODate(end) {
			return "A date_range value must contain exactly two YYYY-MM-DD values."
		}
		if start > end {
			return "A date_range start cannot be after its end."
		}
	case ParameterTypeNumber, ParameterTypeSlider:
		if !isFiniteNumber(value) {
			return "A number value must be a finite JSON number."
		}
	case ParameterTypeBoolean:
		if _, ok := value.(bool); !ok {
			return "A boolean value must be true or false."
		}
	default:
		return "The parameter type is not supported."
	}
	return ""
}

func valueAllowed(parameterType ParameterType, value any, options []any) bool {
	allowed := make(map[string]bool, len(options))
	for _, option := range options {
		allowed[comparableValueKey(option)] = true
	}
	if parameterType != ParameterTypeMultiSelect {
		return allowed[comparableValueKey(value)]
	}
	values, ok := valueSlice(value)
	if !ok {
		return false
	}
	for _, item := range values {
		if !allowed[comparableValueKey(item)] {
			return false
		}
	}
	return true
}

func valueSlice(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		return typed, true
	case []string:
		result := make([]any, len(typed))
		for index := range typed {
			result[index] = typed[index]
		}
		return result, true
	default:
		return nil, false
	}
}

func isFiniteNumber(value any) bool {
	_, ok := finiteNumberValue(value)
	return ok
}

func finiteNumberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int8:
		return float64(typed), true
	case int16:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint8:
		return float64(typed), true
	case uint16:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
	case float32:
		return float64(typed), !float32IsInvalid(typed)
	case float64:
		return typed, !math.IsNaN(typed) && !math.IsInf(typed, 0)
	default:
		return 0, false
	}
}

func float32IsInvalid(value float32) bool {
	return math.IsNaN(float64(value)) || math.IsInf(float64(value), 0)
}

func validISODate(value string) bool {
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

func comparableValueKey(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%T:%v", value, value)
	}
	return string(encoded)
}

func cloneParameterValue(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var cloned any
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return value
	}
	return cloned
}

func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Path == findings[j].Path {
			return findings[i].Code < findings[j].Code
		}
		return findings[i].Path < findings[j].Path
	})
}

// ParameterSQLLiterals returns safe, portable SQL fragments for Jinja's
// `parameter.<id>` map. Strings are escaped as SQL literals; collections are
// rendered for `IN (...)` and `BETWEEN ...` use, never as raw user fragments.
func ParameterSQLLiterals(definitions []ParameterDefinition, values map[string]any) (map[string]string, error) {
	result := make(map[string]string, len(definitions))
	for _, definition := range definitions {
		id := strings.TrimSpace(definition.ID)
		value, ok := values[id]
		if !ok {
			value = definition.Default
		}
		literal, err := parameterSQLLiteral(definition.Type, value)
		if err != nil {
			return nil, fmt.Errorf("parameter %q: %w", id, err)
		}
		result[id] = literal
	}
	return result, nil
}

func parameterSQLLiteral(parameterType ParameterType, value any) (string, error) {
	if message := parameterValueProblem(parameterType, value); message != "" {
		return "", fmt.Errorf("%s", message)
	}
	switch parameterType {
	case ParameterTypeSelect, ParameterTypeText:
		return quoteSQLString(value.(string)), nil
	case ParameterTypeMultiSelect:
		values, _ := valueSlice(value)
		if len(values) == 0 {
			return "NULL", nil
		}
		parts := make([]string, len(values))
		for index, item := range values {
			parts[index] = quoteSQLString(item.(string))
		}
		return strings.Join(parts, ", "), nil
	case ParameterTypeDate:
		return "CAST(" + quoteSQLString(value.(string)) + " AS DATE)", nil
	case ParameterTypeDateRange:
		values, _ := valueSlice(value)
		return "CAST(" + quoteSQLString(values[0].(string)) + " AS DATE) AND CAST(" + quoteSQLString(values[1].(string)) + " AS DATE)", nil
	case ParameterTypeNumber, ParameterTypeSlider:
		return fmt.Sprint(value), nil
	case ParameterTypeBoolean:
		if value.(bool) {
			return "TRUE", nil
		}
		return "FALSE", nil
	default:
		return "", fmt.Errorf("unsupported parameter type %q", parameterType)
	}
}

func quoteSQLString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
