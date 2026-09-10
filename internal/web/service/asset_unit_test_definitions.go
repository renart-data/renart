package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bruin-data/bruin/pkg/date"
	"github.com/bruin-data/bruin/pkg/pipeline"
	webmodel "renart/internal/web/model"
)

func pipelineUnitTestsToModel(tests []pipeline.UnitTest) []webmodel.SQLUnitTest {
	result := make([]webmodel.SQLUnitTest, 0, len(tests))
	for _, t := range tests {
		inputs := make([]webmodel.SQLUnitTestInput, 0, len(t.Inputs))
		for _, in := range t.Inputs {
			rows := in.Rows
			if rows == nil {
				rows = []map[string]any{}
			}
			inputs = append(inputs, webmodel.SQLUnitTestInput{Asset: in.Asset, Rows: rows})
		}
		ctes := map[string]webmodel.SQLUnitTestCTEExpected{}
		for name, e := range t.Expected.CTEs {
			ctes[name] = webmodel.SQLUnitTestCTEExpected{Rows: e.Rows, Count: e.Count, Match: e.Match, Order: e.Order}
		}
		result = append(result, webmodel.SQLUnitTest{Name: t.Name, Description: t.Description, Inputs: inputs, Fixtures: t.Fixtures, Variables: t.Variables, ExecutionTime: t.ExecutionTime, Expected: webmodel.SQLUnitTestExpected{Rows: t.Expected.Rows, Count: t.Expected.Count, Match: t.Expected.Match, Order: t.Expected.Order, CTEs: ctes}})
	}
	return result
}

func modelUnitTestsToPipeline(tests []webmodel.SQLUnitTest) []pipeline.UnitTest {
	result := make([]pipeline.UnitTest, 0, len(tests))
	for _, t := range tests {
		inputs := make([]pipeline.UnitTestInput, 0, len(t.Inputs))
		for _, in := range t.Inputs {
			inputs = append(inputs, pipeline.UnitTestInput{Asset: in.Asset, Rows: in.Rows})
		}
		ctes := map[string]pipeline.UnitTestCTEExpected{}
		for name, e := range t.Expected.CTEs {
			ctes[name] = pipeline.UnitTestCTEExpected{Rows: unitTestCanonicalRows(e.Rows), Count: unitTestEmptyRowsCount(e.Rows, e.Count, e.Match), Match: e.Match, Order: e.Order}
		}
		result = append(result, pipeline.UnitTest{Name: t.Name, Description: t.Description, Inputs: inputs, Fixtures: t.Fixtures, Variables: t.Variables, ExecutionTime: t.ExecutionTime, Expected: pipeline.UnitTestExpected{Rows: unitTestCanonicalRows(t.Expected.Rows), Count: unitTestEmptyRowsCount(t.Expected.Rows, t.Expected.Count, t.Expected.Match), Match: t.Expected.Match, Order: t.Expected.Order, CTEs: ctes}})
	}
	return result
}

func unitTestCanonicalRows(rows []map[string]any) []map[string]any {
	if len(rows) == 0 {
		return nil
	}
	return rows
}

// Bruin's canonical YAML omits empty row slices. Preserve an exact empty
// assertion as count: 0 so saving/reloading cannot erase its meaning.
func unitTestEmptyRowsCount(rows []map[string]any, count *int64, match string) *int64 {
	if rows != nil && len(rows) == 0 && count == nil && match == "exact" {
		zero := int64(0)
		return &zero
	}
	return count
}

func unitTestsRevision(tests []pipeline.UnitTest) string {
	encoded, _ := json.Marshal(pipelineUnitTestsToModel(tests))
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func validateSQLUnitTests(tests []webmodel.SQLUnitTest) error {
	if len(tests) > 50 {
		return fmt.Errorf("an asset supports at most 50 unit tests")
	}
	encoded, err := json.Marshal(tests)
	if err != nil || len(encoded) > 256<<10 {
		return fmt.Errorf("unit test fixtures must fit within 256 KiB of JSON values")
	}
	names := map[string]bool{}
	for _, t := range tests {
		name := strings.TrimSpace(t.Name)
		if name == "" || name != t.Name || len(name) > 128 || names[name] {
			return fmt.Errorf("unit test names must be unique, non-empty, and at most 128 characters")
		}
		names[name] = true
		if t.ExecutionTime != "" {
			if _, err := date.ParseTime(t.ExecutionTime); err != nil {
				return fmt.Errorf("test %q: execution_time must be a date or timestamp", name)
			}
		}
		inputs := map[string]bool{}
		for _, in := range t.Inputs {
			if strings.TrimSpace(in.Asset) == "" || inputs[in.Asset] || len(in.Rows) > 500 {
				return fmt.Errorf("test %q: inputs need unique asset names and at most 500 rows each", name)
			}
			inputs[in.Asset] = true
		}
		if len(t.Expected.Rows) == 0 && unitTestEmptyRowsCount(t.Expected.Rows, t.Expected.Count, t.Expected.Match) == nil && len(t.Expected.CTEs) == 0 {
			return fmt.Errorf("test %q needs expected rows, a row count, or a CTE assertion", name)
		}
		if err := validateUnitTestExpectation(t.Expected.Rows, t.Expected.Count, t.Expected.Match, t.Expected.Order); err != nil {
			return fmt.Errorf("test %q: %w", name, err)
		}
		for cte, e := range t.Expected.CTEs {
			if strings.TrimSpace(cte) == "" || (len(e.Rows) == 0 && unitTestEmptyRowsCount(e.Rows, e.Count, e.Match) == nil) {
				return fmt.Errorf("test %q: CTE assertions need a name and expected rows or count", name)
			}
			if err := validateUnitTestExpectation(e.Rows, e.Count, e.Match, e.Order); err != nil {
				return fmt.Errorf("test %q, CTE %q: %w", name, cte, err)
			}
		}
	}
	return nil
}

func validateUnitTestExpectation(rows []map[string]any, count *int64, match, order string) error {
	if match != "" && match != "subset" && match != "exact" {
		return fmt.Errorf("match must be subset or exact")
	}
	if order != "" && order != "any" && order != "strict" {
		return fmt.Errorf("order must be any or strict")
	}
	if count != nil && (*count < 0 || *count > 5000) {
		return fmt.Errorf("count must be between 0 and 5000")
	}
	if len(rows) > 500 {
		return fmt.Errorf("expectations support at most 500 rows")
	}
	return nil
}
