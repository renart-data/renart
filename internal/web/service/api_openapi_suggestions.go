package service

import (
	"context"
	"fmt"
	neturl "net/url"
	"sort"
	"strings"
)

// OpenAPIEndpointSuggestion is one request-URL candidate derived from an
// OpenAPI spec's paths (base server URL + path template).
type OpenAPIEndpointSuggestion struct {
	URL     string `json:"url"`
	Method  string `json:"method"`
	Summary string `json:"summary,omitempty"`
}

// OpenAPIRecordsPathSuggestion is one candidate value for `response.records_path`
// — a dot path into the selected endpoint's response schema that yields records.
type OpenAPIRecordsPathSuggestion struct {
	Path   string `json:"path"`
	Detail string `json:"detail,omitempty"`
}

// OpenAPIResponsePathSuggestion is one dot-path candidate into the selected
// endpoint's response schema for pagination cursors, next URLs, and flags.
type OpenAPIResponsePathSuggestion struct {
	Path   string `json:"path"`
	Detail string `json:"detail,omitempty"`
}

// OpenAPIQueryParameterSuggestion describes one query-string field accepted by
// the selected operation. Values contains enum candidates when the schema
// declares them (including enums reached through array items and $refs).
type OpenAPIQueryParameterSuggestion struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Type        string   `json:"type,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Values      []string `json:"values"`
}

// OpenAPISuggestionsResult feeds the API-asset editor's intellisense for
// OpenAPI-backed request URLs, query parameters, and response dot-path fields.
// renart:web
type OpenAPISuggestionsResult struct {
	Status          string                            `json:"status"`
	RequestURLs     []OpenAPIEndpointSuggestion       `json:"request_urls"`
	QueryParameters []OpenAPIQueryParameterSuggestion `json:"query_parameters"`
	RecordsPaths    []OpenAPIRecordsPathSuggestion    `json:"records_paths"`
	ResponsePaths   []OpenAPIResponsePathSuggestion   `json:"response_paths"`
	Error           string                            `json:"error,omitempty"`
}

const maxRecordsPathDepth = 4
const maxResponsePathDepth = 5

// OpenAPISuggestions fetches (cached) the OpenAPI document at openapiURL and
// derives editor completions: request URLs from the spec's paths and, when
// requestURL identifies an operation, its query parameters plus record/response
// paths. An empty openapiURL yields an empty, non-error result so the caller can
// pass through whatever the asset currently has.
func (s *SuggestionsService) OpenAPISuggestions(ctx context.Context, openapiURL, requestURL, method string) (OpenAPISuggestionsResult, *APIError) {
	openapiURL = strings.TrimSpace(openapiURL)
	result := OpenAPISuggestionsResult{
		Status:          "ok",
		RequestURLs:     []OpenAPIEndpointSuggestion{},
		QueryParameters: []OpenAPIQueryParameterSuggestion{},
		RecordsPaths:    []OpenAPIRecordsPathSuggestion{},
		ResponsePaths:   []OpenAPIResponsePathSuggestion{},
	}
	if openapiURL == "" {
		return result, nil
	}
	doc, err := fetchOpenAPIDocument(ctx, openapiURL)
	if err != nil {
		return OpenAPISuggestionsResult{}, &APIError{Status: 400, Code: "openapi_fetch_failed", Message: err.Error()}
	}
	if doc == nil {
		return result, nil
	}

	if requestURLs := doc.requestURLSuggestions(openapiURL); requestURLs != nil {
		result.RequestURLs = requestURLs
	}
	if queryParameters := doc.queryParameterSuggestions(requestURL, method); queryParameters != nil {
		result.QueryParameters = queryParameters
	}
	if recordsPaths := doc.recordsPathSuggestions(requestURL, method); recordsPaths != nil {
		result.RecordsPaths = recordsPaths
	}
	if responsePaths := doc.responsePathSuggestions(requestURL, method); responsePaths != nil {
		result.ResponsePaths = responsePaths
	}
	return result, nil
}

// baseURL resolves the server URL requests are made against: the first OpenAPI
// 3.x server, or the swagger 2.0 host/scheme/basePath, falling back to the
// origin of the spec URL itself (weather.gov serves the spec and the API from
// the same host).
func (doc *openAPIDocument) baseURL(specURL string) string {
	specOrigin := ""
	if parsed, err := neturl.Parse(strings.TrimSpace(specURL)); err == nil && parsed.Host != "" {
		specOrigin = parsed.Scheme + "://" + parsed.Host
	}

	if len(doc.Servers) > 0 {
		server := strings.TrimSpace(doc.Servers[0].URL)
		if server != "" {
			if strings.HasPrefix(server, "/") {
				return strings.TrimRight(specOrigin+server, "/")
			}
			return strings.TrimRight(server, "/")
		}
	}

	if strings.TrimSpace(doc.Host) != "" {
		scheme := "https"
		for _, candidate := range doc.Schemes {
			if strings.EqualFold(strings.TrimSpace(candidate), "https") {
				scheme = "https"
				break
			}
			if strings.TrimSpace(candidate) != "" {
				scheme = strings.ToLower(strings.TrimSpace(candidate))
			}
		}
		return strings.TrimRight(scheme+"://"+strings.TrimSpace(doc.Host)+strings.TrimSpace(doc.BasePath), "/")
	}

	return specOrigin
}

func (doc *openAPIDocument) requestURLSuggestions(specURL string) []OpenAPIEndpointSuggestion {
	base := doc.baseURL(specURL)
	paths := make([]string, 0, len(doc.Paths))
	for path := range doc.Paths {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	suggestions := make([]OpenAPIEndpointSuggestion, 0, len(paths))
	for _, path := range paths {
		method, operation := doc.preferredOperation(doc.Paths[path].Operations)
		if method == "" {
			continue
		}
		summary := strings.TrimSpace(operation.Summary)
		if summary == "" {
			summary = firstLine(operation.Description)
		}
		suggestions = append(suggestions, OpenAPIEndpointSuggestion{
			URL:     base + path,
			Method:  strings.ToUpper(method),
			Summary: summary,
		})
	}
	return suggestions
}

func (doc *openAPIDocument) queryParameterSuggestions(requestURL, method string) []OpenAPIQueryParameterSuggestion {
	spec := nativeAPISpec{}
	spec.Request.URL = strings.TrimSpace(requestURL)
	spec.Request.Method = strings.TrimSpace(method)
	if spec.Request.URL == "" {
		return nil
	}

	operation, pathItem, err := doc.operationMatch(spec, spec.Request.URL)
	if err != nil || operation == nil {
		return nil
	}
	parameters := make([]openAPIParameter, 0, len(operation.Parameters)+len(pathItem.Parameters))
	parameters = append(parameters, pathItem.Parameters...)
	parameters = append(parameters, operation.Parameters...)

	// Operation-level parameters override a path-level parameter with the same
	// name and location. A map followed by sorted output keeps this deterministic.
	byName := map[string]OpenAPIQueryParameterSuggestion{}
	for index := range parameters {
		parameter := doc.resolveParameter(&parameters[index], nil)
		if parameter == nil || !strings.EqualFold(strings.TrimSpace(parameter.In), "query") {
			continue
		}
		name := strings.TrimSpace(parameter.Name)
		if name == "" {
			continue
		}
		byName[strings.ToLower(name)] = OpenAPIQueryParameterSuggestion{
			Name:        name,
			Description: strings.TrimSpace(parameter.Description),
			Type:        doc.parameterType(parameter),
			Required:    parameter.Required,
			Values:      doc.parameterValues(parameter),
		}
	}

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]OpenAPIQueryParameterSuggestion, 0, len(names))
	for _, name := range names {
		result = append(result, byName[name])
	}
	return result
}

func (doc *openAPIDocument) resolveParameter(parameter *openAPIParameter, seen map[string]bool) *openAPIParameter {
	if parameter == nil {
		return nil
	}
	ref := strings.TrimSpace(parameter.Ref)
	if ref == "" {
		return parameter
	}
	if seen == nil {
		seen = map[string]bool{}
	}
	if seen[ref] {
		return parameter
	}
	seen[ref] = true
	if strings.HasPrefix(ref, "#/components/parameters/") {
		name := strings.TrimPrefix(ref, "#/components/parameters/")
		return doc.resolveParameter(doc.Components.Parameters[name], seen)
	}
	if strings.HasPrefix(ref, "#/parameters/") {
		name := strings.TrimPrefix(ref, "#/parameters/")
		return doc.resolveParameter(doc.Parameters[name], seen)
	}
	return parameter
}

func (doc *openAPIDocument) parameterSchema(parameter *openAPIParameter) *openAPISchema {
	if parameter == nil {
		return nil
	}
	if parameter.Schema != nil {
		return parameter.Schema
	}
	if parameter.Type == "" && parameter.Items == nil && len(parameter.Enum) == 0 {
		return nil
	}
	return &openAPISchema{Type: parameter.Type, Items: parameter.Items, Enum: parameter.Enum}
}

func (doc *openAPIDocument) parameterType(parameter *openAPIParameter) string {
	return doc.schemaTypeLabel(doc.parameterSchema(parameter), nil)
}

func (doc *openAPIDocument) schemaTypeLabel(schema *openAPISchema, seen map[*openAPISchema]bool) string {
	schema = doc.resolveSchema(schema, nil)
	if schema == nil {
		return ""
	}
	if seen == nil {
		seen = map[*openAPISchema]bool{}
	}
	if seen[schema] {
		return ""
	}
	seen[schema] = true

	types := schemaTypes(schema)
	if schemaHasType(schema, "array") && schema.Items != nil {
		if itemType := doc.schemaTypeLabel(schema.Items, seen); itemType != "" {
			return "array of " + itemType
		}
	}
	if len(types) > 0 {
		return strings.Join(types, "/")
	}

	labels := map[string]bool{}
	for _, child := range append(append(append([]*openAPISchema{}, schema.AllOf...), schema.AnyOf...), schema.OneOf...) {
		if label := doc.schemaTypeLabel(child, seen); label != "" {
			labels[label] = true
		}
	}
	result := make([]string, 0, len(labels))
	for label := range labels {
		result = append(result, label)
	}
	sort.Strings(result)
	return strings.Join(result, "/")
}

func (doc *openAPIDocument) parameterValues(parameter *openAPIParameter) []string {
	seenValues := map[string]bool{}
	seenSchemas := map[*openAPISchema]bool{}
	values := make([]string, 0)
	var collect func(*openAPISchema)
	collect = func(schema *openAPISchema) {
		schema = doc.resolveSchema(schema, nil)
		if schema == nil || seenSchemas[schema] {
			return
		}
		seenSchemas[schema] = true
		for _, raw := range schema.Enum {
			value := fmt.Sprint(raw)
			if value == "" || seenValues[value] {
				continue
			}
			seenValues[value] = true
			values = append(values, value)
		}
		collect(schema.Items)
		for _, child := range schema.AllOf {
			collect(child)
		}
		for _, child := range schema.AnyOf {
			collect(child)
		}
		for _, child := range schema.OneOf {
			collect(child)
		}
	}
	collect(doc.parameterSchema(parameter))
	if len(values) == 0 && strings.EqualFold(doc.parameterType(parameter), "boolean") {
		values = append(values, "false", "true")
	}
	sort.Strings(values)
	return values
}

// preferredOperation picks the operation a data-ingestion asset most likely
// wants: GET when present, otherwise the alphabetically-first HTTP method so the
// choice is deterministic.
func (doc *openAPIDocument) preferredOperation(methods map[string]openAPIOperation) (string, openAPIOperation) {
	if op, ok := methods["get"]; ok {
		return "get", op
	}
	candidates := make([]string, 0, len(methods))
	for method := range methods {
		if isOpenAPIMethod(method) {
			candidates = append(candidates, strings.ToLower(method))
		}
	}
	if len(candidates) == 0 {
		return "", openAPIOperation{}
	}
	sort.Strings(candidates)
	return candidates[0], methods[candidates[0]]
}

func (doc *openAPIDocument) recordsPathSuggestions(requestURL, method string) []OpenAPIRecordsPathSuggestion {
	spec := nativeAPISpec{}
	spec.Request.URL = strings.TrimSpace(requestURL)
	spec.Request.Method = strings.TrimSpace(method)
	if spec.Request.URL == "" {
		return nil
	}

	schema, err := doc.responseSchema(spec, spec.Request.URL)
	if err != nil || schema == nil {
		return nil
	}

	seen := map[string]bool{}
	suggestions := make([]OpenAPIRecordsPathSuggestion, 0, 8)
	add := func(path, detail string) {
		if seen[path] {
			return
		}
		seen[path] = true
		suggestions = append(suggestions, OpenAPIRecordsPathSuggestion{Path: path, Detail: detail})
	}

	rootDetail := "response root"
	if schemaHasType(doc.resolveSchema(schema, nil), "array") {
		rootDetail = "response root (array of records)"
	} else if len(doc.schemaProperties(schema)) > 0 {
		rootDetail = "response root (object)"
	}
	add("", rootDetail)

	doc.walkRecordsPaths(schema, nil, 0, add)
	return suggestions
}

func (doc *openAPIDocument) responsePathSuggestions(requestURL, method string) []OpenAPIResponsePathSuggestion {
	spec := nativeAPISpec{}
	spec.Request.URL = strings.TrimSpace(requestURL)
	spec.Request.Method = strings.TrimSpace(method)
	if spec.Request.URL == "" {
		return nil
	}

	schema, err := doc.responseSchema(spec, spec.Request.URL)
	if err != nil || schema == nil {
		return nil
	}

	seen := map[string]bool{}
	suggestions := make([]OpenAPIResponsePathSuggestion, 0, 16)
	add := func(path, detail string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		suggestions = append(suggestions, OpenAPIResponsePathSuggestion{Path: path, Detail: detail})
	}
	doc.walkResponsePaths(schema, nil, 0, add)
	return suggestions
}

// walkRecordsPaths surfaces every dot path whose value is an array — the shape
// records_path is meant to point at (each array element becomes one record) —
// descending through nested objects and array items up to a bounded depth.
func (doc *openAPIDocument) walkRecordsPaths(schema *openAPISchema, prefix []string, depth int, add func(path, detail string)) {
	if depth >= maxRecordsPathDepth {
		return
	}
	properties := doc.schemaProperties(schema)
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		property := doc.resolveSchema(properties[name], nil)
		path := append(append([]string{}, prefix...), name)
		joined := strings.Join(path, ".")

		if schemaHasType(property, "array") && property.Items != nil {
			item := doc.arrayItemSchema(property)
			add(joined, "array of "+recordItemLabel(doc, item))
			doc.walkRecordsPaths(item, path, depth+1, add)
			continue
		}
		if len(doc.schemaProperties(property)) > 0 {
			doc.walkRecordsPaths(property, path, depth+1, add)
		}
	}
}

func (doc *openAPIDocument) walkResponsePaths(schema *openAPISchema, prefix []string, depth int, add func(path, detail string)) {
	if depth >= maxResponsePathDepth {
		return
	}
	properties := doc.schemaProperties(schema)
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		property := doc.resolveSchema(properties[name], nil)
		path := append(append([]string{}, prefix...), name)
		joined := strings.Join(path, ".")

		if schemaHasType(property, "array") {
			add(joined, "array")
			doc.walkResponsePaths(doc.arrayItemSchema(property), path, depth+1, add)
			continue
		}
		if detail := responsePathDetail(doc, property); detail != "" {
			add(joined, detail)
		}
		if len(doc.schemaProperties(property)) > 0 {
			doc.walkResponsePaths(property, path, depth+1, add)
		}
	}
}

func responsePathDetail(doc *openAPIDocument, schema *openAPISchema) string {
	resolved := doc.resolveSchema(schema, nil)
	if resolved == nil {
		return ""
	}
	types := schemaTypes(resolved)
	if len(types) == 0 {
		if len(doc.schemaProperties(resolved)) > 0 {
			return "object"
		}
		if resolved.Items != nil {
			return "array"
		}
		return ""
	}
	return strings.Join(types, "/")
}

func recordItemLabel(doc *openAPIDocument, schema *openAPISchema) string {
	resolved := doc.resolveSchema(schema, nil)
	if resolved == nil {
		return "records"
	}
	if fields := doc.schemaProperties(resolved); len(fields) > 0 {
		return fmt.Sprintf("objects (%d fields)", len(fields))
	}
	if types := schemaTypes(resolved); len(types) > 0 {
		return types[0]
	}
	return "records"
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if index := strings.IndexAny(text, "\r\n"); index >= 0 {
		text = text[:index]
	}
	const maxSummaryLen = 120
	if len(text) > maxSummaryLen {
		text = strings.TrimSpace(text[:maxSummaryLen]) + "…"
	}
	return text
}
