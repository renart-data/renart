package notebook

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// CellOptionResult is a bounded, typed option projection from one durable
// notebook cell relation. It is runtime state, never authored into notebook.yml.
type CellOptionResult struct {
	Columns     []string
	ColumnTypes []string
	Rows        [][]any
	TotalRows   int
	Truncated   bool
}

// ReadCellOptions reads distinct values from an already-materialized notebook
// cell. It never executes the producer cell or reaches its source warehouse.
func (s *SessionStore) ReadCellOptions(
	ctx context.Context,
	notebookUUID string,
	cellID string,
	valueField string,
	labelField string,
	limit int,
) (CellOptionResult, error) {
	valueField = strings.TrimSpace(valueField)
	labelField = strings.TrimSpace(labelField)
	if valueField == "" {
		return CellOptionResult{}, fmt.Errorf("value field is required")
	}
	if limit <= 0 {
		return CellOptionResult{}, fmt.Errorf("option limit must be positive")
	}
	if _, err := os.Stat(s.DBPath(notebookUUID)); err != nil {
		if os.IsNotExist(err) {
			return CellOptionResult{}, ErrCellResultUnavailable
		}
		return CellOptionResult{}, err
	}

	session, err := s.Open(notebookUUID)
	if err != nil {
		return CellOptionResult{}, err
	}
	defer session.Close()

	objectName := CellObjectName(cellID)
	objectType, err := session.objectType(ctx, objectName)
	if err != nil {
		return CellOptionResult{}, err
	}
	if objectType == "" {
		return CellOptionResult{}, ErrCellResultUnavailable
	}

	fields := []string{quoteIdent(valueField)}
	if labelField != "" && !strings.EqualFold(labelField, valueField) {
		fields = append(fields, quoteIdent(labelField))
	}
	order := make([]string, len(fields))
	for index := range fields {
		order[index] = strconv.Itoa(index + 1)
	}
	queryText := fmt.Sprintf(
		"select distinct %s from %s where %s is not null order by %s limit %d",
		strings.Join(fields, ", "),
		quoteIdent(objectName),
		quoteIdent(valueField),
		strings.Join(order, ", "),
		limit+1,
	)
	result, err := session.Query(ctx, queryText)
	if err != nil {
		return CellOptionResult{}, errors.New(normalizeDuckDBError(err))
	}
	rows := normalizeRows(result.Rows)
	totalRows := len(rows)
	truncated := totalRows > limit
	if truncated {
		rows = rows[:limit]
	}
	return CellOptionResult{
		Columns:     append([]string(nil), result.Columns...),
		ColumnTypes: append([]string(nil), result.ColumnTypes...),
		Rows:        rows,
		TotalRows:   totalRows,
		Truncated:   truncated,
	}, nil
}
