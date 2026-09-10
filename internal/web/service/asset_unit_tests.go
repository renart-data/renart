package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/date"
	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/unittest"
	"renart/internal/sqlintelligence"
	webmodel "renart/internal/web/model"
)

// renart:web
type SQLUnitTestContext struct {
	Status   string                 `json:"status"`
	Tests    []webmodel.SQLUnitTest `json:"tests"`
	Revision string                 `json:"revision"`
	Inputs   []SQLUnitTestSchema    `json:"inputs"`
	Output   []WorkspaceColumn      `json:"output"`
	Fixtures []string               `json:"fixtures"`
}

type SQLUnitTestSchema struct {
	Asset   string            `json:"asset"`
	Columns []WorkspaceColumn `json:"columns"`
}

// renart:web
type SQLUnitTestRunRequest struct {
	Environment string `json:"environment,omitempty"`
	Name        string `json:"name,omitempty"`
	Revision    string `json:"revision"`
}

// renart:web
type SQLUnitTestRunResponse struct {
	Status  string              `json:"status"`
	Results []SQLUnitTestResult `json:"results"`
}

type SQLUnitTestResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

func (s *AssetService) UnitTestContext(ctx context.Context, assetID string) (SQLUnitTestContext, *APIError) {
	_, pl, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return SQLUnitTestContext{}, badRequestError("asset_resolve_failed", err.Error())
	}
	if schemaPolicyForAsset(asset).Kind != assetSchemaKindSQL {
		return SQLUnitTestContext{}, badRequestError("unit_tests_unsupported", "Unit tests require a SQL asset.")
	}
	result := SQLUnitTestContext{Status: "ok", Tests: pipelineUnitTestsToModel(asset.UnitTests), Revision: unitTestsRevision(asset.UnitTests), Inputs: []SQLUnitTestSchema{}, Output: []WorkspaceColumn{}, Fixtures: []string{}}
	for _, fixture := range pl.Fixtures {
		result.Fixtures = append(result.Fixtures, fixture.Name)
	}
	if columns, apiErr := s.inferGraphColumnsFromDefinition(ctx, pl, asset); apiErr == nil && len(columns) > 0 {
		result.Output = columns
	} else {
		result.Output = PipelineColumnsToModelColumns(asset.Columns)
	}
	query, err := s.renderAssetQuery(ctx, pl, asset)
	if err != nil {
		return result, nil
	}
	dialect, err := AssetTypeToDialect(asset.Type)
	if err != nil {
		return result, nil
	}
	tables, err := sqlintelligence.UsedTables(query, dialect)
	if err != nil {
		return result, nil
	}
	seen := map[string]bool{}
	for _, table := range tables {
		canonical := resolveInferredDependencyName(table, asset, pl)
		if seen[canonical] {
			continue
		}
		seen[canonical] = true
		input := SQLUnitTestSchema{Asset: canonical, Columns: []WorkspaceColumn{}}
		for _, candidate := range pl.Assets {
			if !strings.EqualFold(candidate.Name, canonical) {
				continue
			}
			input.Columns = PipelineColumnsToModelColumns(candidate.Columns)
			if len(input.Columns) == 0 && schemaPolicyForAsset(candidate).Kind == assetSchemaKindSQL {
				if columns, apiErr := s.inferGraphColumnsFromDefinition(ctx, pl, candidate); apiErr == nil {
					input.Columns = columns
				}
			}
			break
		}
		result.Inputs = append(result.Inputs, input)
	}
	return result, nil
}

func (s *AssetService) RunUnitTests(ctx context.Context, assetID string, request SQLUnitTestRunRequest) (SQLUnitTestRunResponse, *APIError) {
	_, pl, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return SQLUnitTestRunResponse{}, badRequestError("asset_resolve_failed", err.Error())
	}
	if schemaPolicyForAsset(asset).Kind != assetSchemaKindSQL {
		return SQLUnitTestRunResponse{}, badRequestError("unit_tests_unsupported", "Unit tests require a SQL asset.")
	}
	if request.Revision != unitTestsRevision(asset.UnitTests) {
		return SQLUnitTestRunResponse{}, &APIError{Status: 409, Code: "unit_tests_conflict", Message: "The saved tests changed. Reload them before running."}
	}
	if err := validateSQLUnitTests(pipelineUnitTestsToModel(asset.UnitTests)); err != nil {
		return SQLUnitTestRunResponse{}, badRequestError("invalid_unit_tests", err.Error())
	}
	if s.deps.RunUnitTestQuery == nil {
		return SQLUnitTestRunResponse{}, &APIError{Status: 503, Code: "unit_tests_unavailable", Message: "Unit test execution is unavailable."}
	}
	connection, err := pl.GetConnectionNameForAsset(asset)
	if err != nil {
		return SQLUnitTestRunResponse{}, badRequestError("connection_required", err.Error())
	}
	dialect, err := AssetTypeToDialect(asset.Type)
	if err != nil {
		return SQLUnitTestRunResponse{}, badRequestError("unit_tests_unsupported", err.Error())
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	response := SQLUnitTestRunResponse{Status: "ok", Results: []SQLUnitTestResult{}}
	schemas := map[string][]pipeline.Column{}
	for _, a := range pl.Assets {
		schemas[a.Name] = a.Columns
	}
	for _, test := range asset.UnitTests {
		if request.Name != "" && request.Name != test.Name {
			continue
		}
		result := SQLUnitTestResult{Name: test.Name, Status: "passed"}
		message, err := s.evaluateUnitTest(ctx, pl, asset, test, schemas, connection, dialect, request.Environment)
		if err != nil {
			result.Status, result.Message = "error", err.Error()
		} else if message != "" {
			result.Status, result.Message = "failed", message
		}
		response.Results = append(response.Results, result)
		if ctx.Err() != nil {
			break
		}
	}
	if request.Name != "" && len(response.Results) == 0 {
		return SQLUnitTestRunResponse{}, badRequestError("unit_test_not_found", "This unit test no longer exists.")
	}
	return response, nil
}

func (s *AssetService) evaluateUnitTest(ctx context.Context, pl *pipeline.Pipeline, asset *pipeline.Asset, test pipeline.UnitTest, schemas map[string][]pipeline.Column, connection, dialect, environment string) (string, error) {
	inputs, err := unittest.ResolveFixtures(pl.Fixtures, test)
	if err != nil {
		return "", err
	}
	test.Inputs = append([]pipeline.UnitTestInput(nil), inputs...)
	rendered, err := renderSQLUnitTest(ctx, pl, asset, test)
	if err != nil {
		return "", err
	}
	// Match the normal asset editor's same-schema resolution for unqualified
	// SQL relations and fixture names, without guessing across namespaces.
	used, err := sqlintelligence.UsedTables(rendered, dialect)
	if err != nil {
		return "", err
	}
	mapping := map[string]string{}
	for _, table := range used {
		canonical := resolveInferredDependencyName(table, asset, pl)
		if canonical != table {
			mapping[table] = canonical
		}
		if len(schemas[canonical]) == 0 {
			if candidate := getAssetByNameCaseInsensitiveLocal(pl, canonical); candidate != nil && schemaPolicyForAsset(candidate).Kind == assetSchemaKindSQL {
				if columns, apiErr := s.inferGraphColumnsFromDefinition(ctx, pl, candidate); apiErr == nil {
					schemas[canonical] = ModelColumnsToPipelineColumns(columns)
				}
			}
		}
	}
	if len(mapping) > 0 {
		rendered, err = sqlintelligence.RenameTables(rendered, dialect, mapping)
		if err != nil {
			return "", err
		}
	}
	for i := range test.Inputs {
		test.Inputs[i].Asset = resolveInferredDependencyName(test.Inputs[i].Asset, asset, pl)
	}
	base, err := unittest.BuildWarehouseQuery(unitTestRewriter{}, dialect, rendered, test, schemas)
	if err != nil {
		return "", err
	}
	var failures []string
	check := func(sql string, expected pipeline.UnitTestExpected, label string) error {
		if _, _, _, err := unitTestSelect(sql, dialect); err != nil {
			return err
		}
		tables, err := sqlintelligence.UsedTables(sql, dialect)
		if err != nil || len(tables) != 0 {
			return fmt.Errorf("the compiled test still references an unmocked relation")
		}
		bounded, err := boundedUnitTestQuery(sql, dialect)
		if err != nil {
			return err
		}
		columns, rows, err := s.deps.RunUnitTestQuery(ctx, connection, environment, bounded)
		if err != nil {
			return err
		}
		if len(rows) > 5000 {
			return fmt.Errorf("unit test output exceeds 5000 rows; narrow the fixture or query")
		}
		values := make([][]any, len(rows))
		for i, row := range rows {
			values[i] = make([]any, len(columns))
			for j, column := range columns {
				values[i][j] = row[column]
			}
		}
		comparison := unittest.CompareResult(expected, columns, values)
		if !comparison.Passed {
			failures = append(failures, label+": "+comparison.Message)
		}
		return nil
	}
	if test.Expected.Rows != nil || test.Expected.Count != nil || len(test.Expected.CTEs) == 0 {
		if err := check(base, test.Expected, "Output"); err != nil {
			return "", err
		}
	}
	names := make([]string, 0, len(test.Expected.CTEs))
	for name := range test.Expected.CTEs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		query, err := (unitTestRewriter{}).SelectFromCTE(base, dialect, name)
		if err != nil {
			return "", err
		}
		e := test.Expected.CTEs[name]
		if err := check(query, pipeline.UnitTestExpected{Rows: e.Rows, Count: e.Count, Match: e.Match, Order: e.Order}, "CTE "+name); err != nil {
			return "", err
		}
	}
	return strings.Join(failures, "\n"), nil
}

func renderSQLUnitTest(ctx context.Context, pl *pipeline.Pipeline, asset *pipeline.Asset, test pipeline.UnitTest) (string, error) {
	copyPipeline := *pl
	copyPipeline.Variables = make(pipeline.Variables, len(pl.Variables)+len(test.Variables))
	for name, definition := range pl.Variables {
		copyPipeline.Variables[name] = definition
	}
	for name, value := range test.Variables {
		definition := map[string]any{}
		for key, old := range pl.Variables[name] {
			definition[key] = old
		}
		definition["default"] = value
		copyPipeline.Variables[name] = definition
	}
	// Tests are reproducible unless they deliberately use an unfrozen SQL
	// clock. execution_time also pins Jinja's dates and run context.
	execution := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	if test.ExecutionTime != "" {
		var err error
		execution, err = date.ParseTime(test.ExecutionTime)
		if err != nil {
			return "", err
		}
	}
	start, end := execution.AddDate(0, 0, -1), execution
	ctx = context.WithValue(ctx, pipeline.RunConfigStartDate, start)
	ctx = context.WithValue(ctx, pipeline.RunConfigEndDate, end)
	ctx = context.WithValue(ctx, pipeline.RunConfigExecutionDate, execution)
	ctx = context.WithValue(ctx, pipeline.RunConfigRunID, "unit-test")
	renderer := jinja.NewRendererWithStartEndDatesAndMacros(&start, &end, &execution, pl.Name, "unit-test", copyPipeline.Variables.Value(), "")
	forAsset, err := renderer.CloneForAsset(ctx, &copyPipeline, asset)
	if err != nil {
		return "", err
	}
	return forAsset.Render(mergeAssetMacrosWithQuery(asset.ExecutableFile.Content, pl.Macros))
}
