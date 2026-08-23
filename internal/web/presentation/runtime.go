package presentation

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"renart/internal/bruincompat"
	"renart/internal/sqlintelligence"
	"renart/internal/web/apperror"
	"renart/internal/web/model"
)

const RuntimeRowLimit = 1000

// ConnectionTypeLookup is the narrow part of Bruin's environment connection
// manager needed by presentation execution.
type ConnectionTypeLookup interface {
	GetConnectionType(name string) string
}

// RuntimeDataset is the fully resolved read-only query target consumed by the
// presentation runtime.
type RuntimeDataset struct {
	ID             string
	Connection     string
	ConnectionType string
	Query          string
	ColumnTypes    map[string]string
}

// RuntimeDependencies keep warehouse and cross-workspace resolution at
// explicit adapter boundaries while the execution state machine remains in the
// presentation domain.
type RuntimeDependencies struct {
	Documents           *DocumentService
	NewConnectionLookup func(context.Context, string) (ConnectionTypeLookup, error)
	ResolveAssetDataset func(context.Context, ConnectionTypeLookup, string, DatasetDefinition, string) (RuntimeDataset, error)
	RunConnectionQuery  func(context.Context, string, string, string) ([]string, []map[string]any, error)
}

type RuntimeService struct {
	deps RuntimeDependencies
}

func NewRuntimeService(deps RuntimeDependencies) *RuntimeService {
	return &RuntimeService{deps: deps}
}

// Run executes the bounded, read-only data needed to render a dashboard or
// report. Each visualization is evaluated independently so bindings on one
// visualization never change another visualization that reuses the dataset.
func (s *RuntimeService) Run(
	ctx context.Context,
	workspaceID string,
	request model.PresentationRunRequest,
) (model.PresentationRunResult, *apperror.Error) {
	if s.deps.Documents == nil {
		return model.PresentationRunResult{}, runtimeUnavailableError()
	}
	artifact, apiErr := s.deps.Documents.LoadRuntimeArtifact(ctx, workspaceID)
	if apiErr != nil {
		return model.PresentationRunResult{}, apiErr
	}
	return s.runArtifact(ctx, artifact, request)
}

// Preview validates and executes an unsaved typed snapshot without writing it
// to the workspace.
func (s *RuntimeService) Preview(
	ctx context.Context,
	workspaceID string,
	request model.PresentationPreviewRequest,
) (model.PresentationPreviewResult, *apperror.Error) {
	if s.deps.Documents == nil {
		return model.PresentationPreviewResult{}, runtimeUnavailableError()
	}
	artifact, apiErr := s.deps.Documents.PreparePreview(ctx, workspaceID, request.ExpectedRevision, request.Artifact)
	if apiErr != nil {
		return model.PresentationPreviewResult{}, apiErr
	}
	findings := FindingsToModel(artifact.Problems)
	if FirstError(artifact.Problems) != nil {
		return model.PresentationPreviewResult{
			Status:           "invalid",
			ArtifactRevision: artifact.Revision,
			Findings:         findings,
			FilterValues:     map[string]any{},
			Visualizations:   map[string]model.PresentationDatasetResult{},
		}, nil
	}
	run, apiErr := s.runArtifact(ctx, artifact, model.PresentationRunRequest{
		Environment:      request.Environment,
		FilterValues:     request.FilterValues,
		VisualizationIDs: request.VisualizationIDs,
		IncludeOptions:   request.IncludeOptions,
	})
	if apiErr != nil {
		return model.PresentationPreviewResult{}, apiErr
	}
	return model.PresentationPreviewResult{
		Status:           run.Status,
		ArtifactRevision: run.ArtifactRevision,
		Findings:         findings,
		FilterValues:     run.FilterValues,
		Visualizations:   run.Visualizations,
		Options:          run.Options,
	}, nil
}

func (s *RuntimeService) runArtifact(
	ctx context.Context,
	artifact *Artifact,
	request model.PresentationRunRequest,
) (model.PresentationRunResult, *apperror.Error) {
	if problem := FirstError(artifact.Problems); problem != nil {
		return model.PresentationRunResult{}, &apperror.Error{
			Status:  http.StatusUnprocessableEntity,
			Code:    "presentation_not_runnable",
			Message: fmt.Sprintf("%s: %s", problem.Path, problem.Message),
		}
	}
	if s.deps.NewConnectionLookup == nil || s.deps.RunConnectionQuery == nil {
		return model.PresentationRunResult{}, runtimeUnavailableError()
	}

	filterValues, filterFindings := ResolveParameterValues(artifact.Filters, request.FilterValues)
	if problem := FirstError(filterFindings); problem != nil {
		return model.PresentationRunResult{}, &apperror.Error{
			Status: http.StatusBadRequest, Code: "presentation_filter_invalid",
			Message: fmt.Sprintf("%s: %s", problem.Path, problem.Message),
		}
	}
	literals, err := ParameterSQLLiterals(artifact.Filters, filterValues)
	if err != nil {
		return model.PresentationRunResult{}, &apperror.Error{
			Status: http.StatusBadRequest, Code: "presentation_filter_invalid", Message: err.Error(),
		}
	}

	connections, err := s.deps.NewConnectionLookup(ctx, strings.TrimSpace(request.Environment))
	if err != nil {
		return model.PresentationRunResult{}, &apperror.Error{
			Status: http.StatusBadRequest, Code: "presentation_environment_invalid", Message: err.Error(),
		}
	}
	if connections == nil {
		return model.PresentationRunResult{}, &apperror.Error{
			Status: http.StatusInternalServerError, Code: "presentation_connection_manager_failed",
			Message: "connection manager is unavailable",
		}
	}

	requested, apiErr := requestedVisualizations(artifact, request.VisualizationIDs)
	if apiErr != nil {
		return model.PresentationRunResult{}, apiErr
	}
	result := model.PresentationRunResult{
		Status:           "ok",
		ArtifactRevision: artifact.Revision,
		FilterValues:     filterValues,
		Visualizations:   make(map[string]model.PresentationDatasetResult, len(requested)),
	}
	cache := make(map[string]model.PresentationDatasetResult)
	for _, visualization := range requested {
		dataset, resolveErr := s.resolveDataset(ctx, connections, artifact, visualization.Dataset, request.Environment)
		if resolveErr != nil {
			result.Visualizations[visualization.ID] = failedDatasetResult(visualization.Dataset, resolveErr)
			result.Status = "error"
			continue
		}
		query, renderErr := RenderQuery(
			dataset.Query,
			dataset.ConnectionType,
			artifact.Filters,
			filterValues,
			literals,
			visualization.FilterBindings,
			visualization.Dataset,
			RuntimeRowLimit+1,
		)
		if renderErr != nil {
			result.Visualizations[visualization.ID] = failedDatasetResult(visualization.Dataset, renderErr)
			result.Status = "error"
			continue
		}
		cacheKey := strings.Join([]string{dataset.Connection, strings.TrimSpace(request.Environment), query}, "\x00")
		datasetResult, ok := cache[cacheKey]
		if !ok {
			datasetResult = s.executeDataset(ctx, dataset, request.Environment, query)
			cache[cacheKey] = datasetResult
		}
		datasetResult.Dataset = visualization.Dataset
		result.Visualizations[visualization.ID] = datasetResult
		if datasetResult.Status != "ok" {
			result.Status = "error"
		}
	}

	if request.IncludeOptions {
		result.Options = make(map[string]model.PresentationDatasetResult)
		for _, filter := range artifact.Filters {
			if filter.Options == nil || strings.TrimSpace(filter.Options.Dataset) == "" {
				continue
			}
			datasetID := strings.TrimSpace(filter.Options.Dataset)
			dataset, resolveErr := s.resolveDataset(ctx, connections, artifact, datasetID, request.Environment)
			if resolveErr != nil {
				result.Options[filter.ID] = failedDatasetResult(datasetID, resolveErr)
				result.Status = "error"
				continue
			}
			query := wrapQuery(dataset.Query, dataset.ConnectionType, "", RuntimeRowLimit+1)
			cacheKey := strings.Join([]string{dataset.Connection, strings.TrimSpace(request.Environment), query}, "\x00")
			datasetResult, ok := cache[cacheKey]
			if !ok {
				datasetResult = s.executeDataset(ctx, dataset, request.Environment, query)
				cache[cacheKey] = datasetResult
			}
			datasetResult.Dataset = datasetID
			result.Options[filter.ID] = datasetResult
			if datasetResult.Status != "ok" {
				result.Status = "error"
			}
		}
	}
	return result, nil
}

func runtimeUnavailableError() *apperror.Error {
	return &apperror.Error{
		Status: http.StatusNotImplemented, Code: "presentation_runtime_unavailable",
		Message: "presentation query execution is not configured",
	}
}

// FirstError returns the first error-severity presentation finding.
func FirstError(findings []Finding) *Finding {
	for index := range findings {
		if strings.EqualFold(strings.TrimSpace(findings[index].Severity), "error") {
			return &findings[index]
		}
	}
	return nil
}

func requestedVisualizations(artifact *Artifact, requestedIDs []string) ([]ArtifactVisualization, *apperror.Error) {
	byID := make(map[string]ArtifactVisualization, len(artifact.Visualizations))
	for _, visualization := range artifact.Visualizations {
		byID[visualization.ID] = visualization
	}
	if len(requestedIDs) == 0 {
		result := append([]ArtifactVisualization(nil), artifact.Visualizations...)
		sort.SliceStable(result, func(i, j int) bool { return result[i].ID < result[j].ID })
		return result, nil
	}
	seen := map[string]bool{}
	result := make([]ArtifactVisualization, 0, len(requestedIDs))
	for _, rawID := range requestedIDs {
		id := strings.TrimSpace(rawID)
		visualization, ok := byID[id]
		if !ok {
			return nil, &apperror.Error{
				Status: http.StatusBadRequest, Code: "presentation_visualization_unknown",
				Message: fmt.Sprintf("visualization %q is not declared", id),
			}
		}
		if !seen[id] {
			result = append(result, visualization)
			seen[id] = true
		}
	}
	return result, nil
}

func (s *RuntimeService) resolveDataset(
	ctx context.Context,
	connections ConnectionTypeLookup,
	artifact *Artifact,
	datasetID string,
	environment string,
) (RuntimeDataset, error) {
	datasetID = strings.TrimSpace(datasetID)
	definition, ok := artifact.Datasets[datasetID]
	if !ok {
		return RuntimeDataset{}, fmt.Errorf("dataset %q is not declared", datasetID)
	}
	columnTypes := make(map[string]string, len(definition.Columns))
	for _, column := range definition.Columns {
		columnTypes[strings.ToLower(strings.TrimSpace(column.Name))] = strings.TrimSpace(column.Type)
	}
	if strings.TrimSpace(definition.Query) != "" {
		connection := strings.TrimSpace(definition.Connection)
		connectionType := bruincompat.NormalizeConnectionType(connections.GetConnectionType(connection))
		if connectionType == "" {
			return RuntimeDataset{}, fmt.Errorf("connection %q is not configured in environment %q", connection, environment)
		}
		assetType, ok := bruincompat.QueryAssetTypeForConnectionType(connectionType)
		if !ok {
			return RuntimeDataset{}, fmt.Errorf("connection %q does not support presentation queries", connection)
		}
		readOnly, err := isReadOnlyQuery(ctx, definition.Query, assetType)
		if err != nil {
			return RuntimeDataset{}, fmt.Errorf("validate dataset %q query: %w", datasetID, err)
		}
		if !readOnly {
			return RuntimeDataset{}, fmt.Errorf("dataset %q query must be one read-only SELECT", datasetID)
		}
		return RuntimeDataset{
			ID: datasetID, Connection: connection, ConnectionType: connectionType,
			Query: strings.TrimSpace(definition.Query), ColumnTypes: columnTypes,
		}, nil
	}

	if s.deps.ResolveAssetDataset == nil {
		return RuntimeDataset{}, fmt.Errorf("asset-backed presentation datasets are not configured")
	}
	return s.deps.ResolveAssetDataset(ctx, connections, datasetID, definition, environment)
}

func isReadOnlyQuery(ctx context.Context, query string, assetType pipeline.AssetType) (bool, error) {
	dialect, ok := bruincompat.AnalyzerDialectForAssetType(assetType)
	if !ok {
		return false, fmt.Errorf("unsupported asset type %s", assetType)
	}
	parsed, err := sqlintelligence.ParseContextWithSchemaContext(ctx, query, dialect, nil)
	if err != nil {
		return false, err
	}
	if len(parsed.Errors) > 0 {
		return false, fmt.Errorf("query could not be parsed: %s", strings.Join(parsed.Errors, "; "))
	}
	return parsed.IsReadOnlyResult, nil
}

func (s *RuntimeService) executeDataset(
	ctx context.Context,
	dataset RuntimeDataset,
	environment string,
	query string,
) model.PresentationDatasetResult {
	started := time.Now()
	columns, rows, err := s.deps.RunConnectionQuery(ctx, dataset.Connection, strings.TrimSpace(environment), query)
	duration := time.Since(started).Milliseconds()
	if err != nil {
		return model.PresentationDatasetResult{
			Dataset: dataset.ID, Status: "error", Columns: []string{}, Rows: [][]any{},
			DurationMS: duration, Error: err.Error(),
		}
	}
	if columns == nil {
		columns = []string{}
	}
	truncated := len(rows) > RuntimeRowLimit
	totalRows := len(rows)
	if truncated {
		rows = rows[:RuntimeRowLimit]
	}
	resultRows := make([][]any, 0, len(rows))
	for _, row := range rows {
		values := make([]any, len(columns))
		for index, column := range columns {
			values[index] = rowValue(row, column)
		}
		resultRows = append(resultRows, values)
	}
	columnTypes := make([]string, len(columns))
	for index, column := range columns {
		columnTypes[index] = dataset.ColumnTypes[strings.ToLower(strings.TrimSpace(column))]
	}
	return model.PresentationDatasetResult{
		Dataset: dataset.ID, Status: "ok", Columns: columns, ColumnTypes: columnTypes,
		Rows: resultRows, TotalRows: totalRows, Truncated: truncated, DurationMS: duration,
	}
}

func rowValue(row map[string]any, column string) any {
	if value, ok := row[column]; ok {
		return value
	}
	for key, value := range row {
		if strings.EqualFold(strings.TrimSpace(key), strings.TrimSpace(column)) {
			return value
		}
	}
	return nil
}

func failedDatasetResult(dataset string, err error) model.PresentationDatasetResult {
	return model.PresentationDatasetResult{
		Dataset: dataset, Status: "error", Columns: []string{}, Rows: [][]any{}, Error: err.Error(),
	}
}

// RenderQuery applies typed filter bindings and a warehouse-specific row limit
// around one read-only dataset query.
func RenderQuery(
	baseQuery string,
	connectionType string,
	definitions []FilterDefinition,
	values map[string]any,
	literals map[string]string,
	bindings []FilterBinding,
	datasetID string,
	limit int,
) (string, error) {
	definitionsByID := make(map[string]FilterDefinition, len(definitions))
	for _, definition := range definitions {
		definitionsByID[strings.TrimSpace(definition.ID)] = definition
	}
	predicates := make([]string, 0, len(bindings))
	seen := map[string]bool{}
	for _, binding := range bindings {
		bindingDataset := strings.TrimSpace(binding.Dataset)
		if bindingDataset == "" {
			bindingDataset = datasetID
		}
		if bindingDataset != datasetID {
			continue
		}
		definition, ok := definitionsByID[strings.TrimSpace(binding.Filter)]
		if !ok {
			return "", fmt.Errorf("filter %q is not declared", binding.Filter)
		}
		predicate, err := renderFilterPredicate(
			connectionType,
			binding.Column,
			strings.ToLower(strings.TrimSpace(binding.Operator)),
			definition,
			values[definition.ID],
			literals[definition.ID],
		)
		if err != nil {
			return "", fmt.Errorf("filter %q: %w", binding.Filter, err)
		}
		if !seen[predicate] {
			predicates = append(predicates, predicate)
			seen[predicate] = true
		}
	}
	return wrapQuery(baseQuery, connectionType, strings.Join(predicates, " AND "), limit), nil
}

func renderFilterPredicate(
	connectionType string,
	column string,
	operator string,
	definition FilterDefinition,
	value any,
	literal string,
) (string, error) {
	identifier := quoteIdentifier(column, connectionType)
	switch operator {
	case "equals":
		return identifier + " = " + literal, nil
	case "not_equals":
		return identifier + " <> " + literal, nil
	case "in", "not_in":
		values, ok := value.([]any)
		if !ok {
			if stringsValue, stringsOK := value.([]string); stringsOK {
				values = make([]any, len(stringsValue))
				for index := range stringsValue {
					values[index] = stringsValue[index]
				}
			} else {
				return "", fmt.Errorf("%s requires a list value", operator)
			}
		}
		if len(values) == 0 {
			if operator == "not_in" {
				return "1 = 1", nil
			}
			return "1 = 0", nil
		}
		keyword := " IN ("
		if operator == "not_in" {
			keyword = " NOT IN ("
		}
		return identifier + keyword + literal + ")", nil
	case "between":
		return identifier + " BETWEEN " + literal, nil
	case "before", "lt":
		return identifier + " < " + literal, nil
	case "after", "gt":
		return identifier + " > " + literal, nil
	case "on_or_before", "lte":
		return identifier + " <= " + literal, nil
	case "on_or_after", "gte":
		return identifier + " >= " + literal, nil
	case "contains", "starts_with":
		text, ok := value.(string)
		if !ok || definition.Type != ParameterTypeText {
			return "", fmt.Errorf("%s requires a text value", operator)
		}
		pattern := escapeLikePattern(text)
		if operator == "contains" {
			pattern = "%" + pattern + "%"
		} else {
			pattern += "%"
		}
		return identifier + " LIKE '" + escapeSQLLiteral(pattern) + "' ESCAPE '!'", nil
	default:
		return "", fmt.Errorf("operator %q is not supported", operator)
	}
}

func quoteIdentifier(identifier, connectionType string) string {
	identifier = strings.TrimSpace(strings.Trim(identifier, "`\"[]"))
	switch bruincompat.NormalizeConnectionType(connectionType) {
	case "google_cloud_platform":
		return "`" + strings.ReplaceAll(identifier, "`", "\\`") + "`"
	case "databricks", "mysql", "doris", "vitess", "planetscale_mysql", "clickhouse", "starrocks":
		return "`" + strings.ReplaceAll(identifier, "`", "``") + "`"
	case "mssql", "synapse", "fabric":
		return "[" + strings.ReplaceAll(identifier, "]", "]]") + "]"
	default:
		return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
	}
}

func escapeLikePattern(value string) string {
	value = strings.ReplaceAll(value, "!", "!!")
	value = strings.ReplaceAll(value, "%", "!%")
	return strings.ReplaceAll(value, "_", "!_")
}

func escapeSQLLiteral(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func wrapQuery(baseQuery, connectionType, predicate string, limit int) string {
	baseQuery = strings.TrimRight(strings.TrimSpace(baseQuery), "; \n\r\t")
	where := ""
	if strings.TrimSpace(predicate) != "" {
		where = "\nWHERE " + predicate
	}
	switch bruincompat.NormalizeConnectionType(connectionType) {
	case "mssql", "synapse", "fabric":
		return fmt.Sprintf("SELECT TOP (%d) * FROM (\n%s\n) AS renart_presentation_dataset%s", limit, baseQuery, where)
	case "oracle":
		return fmt.Sprintf("SELECT * FROM (\n%s\n) renart_presentation_dataset%s\nFETCH FIRST %d ROWS ONLY", baseQuery, where, limit)
	default:
		return fmt.Sprintf("SELECT * FROM (\n%s\n) AS renart_presentation_dataset%s\nLIMIT %d", baseQuery, where, limit)
	}
}
