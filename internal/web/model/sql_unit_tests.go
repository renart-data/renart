package model

// SQLUnitTest describes fixture inputs and assertions, never executable SQL.
type SQLUnitTest struct {
	Name          string              `json:"name"`
	Description   string              `json:"description,omitempty"`
	Inputs        []SQLUnitTestInput  `json:"inputs,omitempty"`
	Fixtures      []string            `json:"fixtures,omitempty"`
	Variables     map[string]any      `json:"variables,omitempty"`
	ExecutionTime string              `json:"execution_time,omitempty"`
	Expected      SQLUnitTestExpected `json:"expected"`
}

type SQLUnitTestInput struct {
	Asset string           `json:"asset"`
	Rows  []map[string]any `json:"rows"`
}

type SQLUnitTestExpected struct {
	Rows  []map[string]any                  `json:"rows,omitzero"`
	Count *int64                            `json:"count,omitempty"`
	Match string                            `json:"match,omitempty"`
	Order string                            `json:"order,omitempty"`
	CTEs  map[string]SQLUnitTestCTEExpected `json:"ctes,omitempty"`
}

type SQLUnitTestCTEExpected struct {
	Rows  []map[string]any `json:"rows,omitzero"`
	Count *int64           `json:"count,omitempty"`
	Match string           `json:"match,omitempty"`
	Order string           `json:"order,omitempty"`
}
