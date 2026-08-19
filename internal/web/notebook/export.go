package notebook

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

var (
	// ErrCellResultUnavailable means a cell has no successful materialized
	// relation in the notebook session.
	ErrCellResultUnavailable = errors.New("notebook cell result is unavailable")
	// ErrCellResultStale means the session contains a result for an older
	// authored cell revision. Export never silently downloads that old object.
	ErrCellResultStale = errors.New("notebook cell result is stale")
)

// CellExportFormat is a supported notebook relation download format.
type CellExportFormat string

const (
	CellExportCSV     CellExportFormat = "csv"
	CellExportParquet CellExportFormat = "parquet"
)

// ExportCell writes the current successful relation for cellID to destination.
// Session access is serialized with runs, and the destination path must be
// chosen by the trusted caller rather than supplied by a notebook document.
func (s *SessionStore) ExportCell(
	ctx context.Context,
	nb *Notebook,
	cellID string,
	format CellExportFormat,
	destination string,
	parameterValues map[string]any,
) error {
	if nb == nil {
		return ErrCellResultUnavailable
	}
	cell := nb.CellByID(cellID)
	if cell == nil {
		return ErrCellResultUnavailable
	}
	if _, err := os.Stat(s.DBPath(nb.UUID)); err != nil {
		if os.IsNotExist(err) {
			return ErrCellResultUnavailable
		}
		return err
	}

	session, err := s.Open(nb.UUID)
	if err != nil {
		return err
	}
	defer session.Close()

	records, err := session.listCellRuns(ctx)
	if err != nil {
		return err
	}
	record, ok := records[cellID]
	if !ok || record.Status != CellRunOK || len(record.Schema) == 0 {
		return ErrCellResultUnavailable
	}
	if record.CellFingerprint != CellFingerprintWithParameters(nb, cell, parameterValues) {
		return ErrCellResultStale
	}
	objectName := CellObjectName(cellID)
	objectType, err := session.objectType(ctx, objectName)
	if err != nil {
		return err
	}
	if objectType == "" {
		return ErrCellResultUnavailable
	}

	columns := make([]string, 0, len(record.Schema))
	for _, column := range record.Schema {
		// The run record already omits Sling's internal timestamp column, so the
		// exported file matches the result schema shown in the notebook.
		if strings.TrimSpace(column.Name) != "" {
			columns = append(columns, quoteIdent(column.Name))
		}
	}
	if len(columns) == 0 {
		return ErrCellResultUnavailable
	}

	copyOptions := "FORMAT CSV, HEADER true"
	if format == CellExportParquet {
		copyOptions = "FORMAT PARQUET, COMPRESSION ZSTD"
	} else if format != CellExportCSV {
		return fmt.Errorf("unsupported notebook export format %q", format)
	}
	statement := fmt.Sprintf(
		"copy (select %s from %s) to %s (%s)",
		strings.Join(columns, ", "), quoteIdent(objectName), sqlStringLiteral(destination), copyOptions,
	)
	if err := session.client.trustedExec(ctx, statement); err != nil {
		return fmt.Errorf("export notebook result: %w", err)
	}
	return nil
}
