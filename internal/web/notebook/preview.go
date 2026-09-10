package notebook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"renart/internal/web/model"
	"renart/internal/web/preview"
)

var ErrPreviewExpired = errors.New("saved preview expired or was replaced; run the cell explicitly to create a new preview")

// CellPreviewResult is an immutable prefix of a cell's saved execution sample.
// renart:web
// renart:web-name NotebookCellPreviewResult
type CellPreviewResult struct {
	Columns     []string               `json:"columns"`
	ColumnTypes []string               `json:"column_types,omitempty"`
	Rows        [][]any                `json:"rows"`
	Preview     *model.PreviewMetadata `json:"preview"`
}

// Private payload in the existing session DB. Logical retention is at most
// four 2 MiB row samples per notebook; DuckDB may retain reusable disk pages.
type retainedPreview struct {
	CellPreviewResult
	Epoch        string            `json:"epoch"`
	Environment  string            `json:"environment"`
	Fingerprints map[string]string `json:"fingerprints"`
}

func previewFingerprints(nb *Notebook, cell *Cell, params map[string]any) map[string]string {
	fingerprints := map[string]string{}
	var visit func(*Cell)
	visit = func(c *Cell) {
		if c == nil {
			return
		}
		if _, ok := fingerprints[c.ID]; ok {
			return
		}
		fingerprints[c.ID] = CellFingerprintWithParameters(nb, c, params)
		for _, upstream := range c.Asset.Upstreams {
			visit(nb.CellByName(upstream.Value))
		}
	}
	visit(cell)
	return fingerprints
}

func (s *Session) invalidatePreviews(ctx context.Context, nb *Notebook, cells []*Cell) error {
	changed := map[string]bool{}
	for _, cell := range cells {
		changed[cell.ID] = true
	}
	ids := []string{}
	for _, cell := range nb.Cells {
		for id := range previewFingerprints(nb, cell, nil) {
			if changed[id] {
				ids = append(ids, sqlStringLiteral(cell.ID))
				break
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return s.Exec(ctx, "delete from __renart_preview_rows where cell_id in ("+strings.Join(ids, ",")+")")
}

func (s *Session) retainPreview(ctx context.Context, epoch string, nb *Notebook, cell *Cell, result *CellRunResult, environment string, params map[string]any, initialLimit int) error {
	rows, meta := preview.BoundRows(result.Rows, preview.MaxRows, result.TotalRows > int64(len(result.Rows)))
	saved := retainedPreview{
		CellPreviewResult: CellPreviewResult{Columns: result.Columns, ColumnTypes: result.ColumnTypes, Rows: rows, Preview: meta},
		Epoch:             epoch, Environment: environment, Fingerprints: previewFingerprints(nb, cell, params),
	}
	// Python dependency resolution can change its effective environment during
	// Run. Bind to published fingerprints, not the pre-run loader snapshot.
	records, err := s.listCellRuns(ctx)
	if err != nil {
		return err
	}
	for id := range saved.Fingerprints {
		if record, ok := records[id]; ok {
			saved.Fingerprints[id] = record.CellFingerprint
		}
	}
	saved.Fingerprints[cell.ID] = result.Fingerprint
	payload, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	if err = s.Exec(ctx, fmt.Sprintf("insert or replace into __renart_preview_rows values (%s, now(), %s)", sqlStringLiteral(cell.ID), sqlStringLiteral(string(payload)))); err != nil {
		return err
	}
	if err = s.Exec(ctx, `delete from __renart_preview_rows where created_at < now() - interval 30 minute
or cell_id not in (select cell_id from __renart_preview_rows order by created_at desc, cell_id limit 4)`); err != nil {
		return err
	}
	initial := saved.prefix(initialLimit)
	result.Rows, result.Preview = initial.Rows, initial.Preview
	return nil
}

func (saved retainedPreview) prefix(limit int) CellPreviewResult {
	limit = preview.NormalizeLimit(limit)
	result := saved.CellPreviewResult
	meta := *saved.Preview
	result.Preview = &meta
	result.Rows = saved.Rows[:min(limit, len(saved.Rows))]
	meta.Limit, meta.ReturnedRows = limit, len(result.Rows)
	meta.HasMore = len(saved.Rows) > len(result.Rows) || saved.Preview.HasMore
	meta.NextLimit, meta.Continuation = 0, "none"
	if len(saved.Rows) > len(result.Rows) {
		meta.Continuation, meta.Reason = "snapshot", ""
		meta.NextLimit = min(len(saved.Rows), limit+max(100, limit))
	}
	return result
}

func (s *Session) savedPreview(ctx context.Context, cellID string) (*retainedPreview, error) {
	rows, err := s.Query(ctx, "select payload from __renart_preview_rows where cell_id = "+sqlStringLiteral(cellID)+" and created_at >= now() - interval 30 minute")
	if err != nil {
		return nil, err
	}
	if len(rows.Rows) != 1 {
		return nil, ErrPreviewExpired
	}
	var saved retainedPreview
	decoder := json.NewDecoder(strings.NewReader(stringValue(rows.Rows[0][0])))
	decoder.UseNumber() // Preserve integer/decimal wire values across expansions.
	if err := decoder.Decode(&saved); err != nil {
		return nil, ErrPreviewExpired
	}
	if saved.Preview == nil {
		return nil, ErrPreviewExpired
	}
	return &saved, nil
}

// ReadPreview never queries a cell view, executes user SQL, or invokes a runner.
// Load current authored inputs after acquiring the same lock used by execution.
func (store *SessionStore) ReadPreview(ctx context.Context, notebookUUID, cellID, resultID, environment string, limit int, current func() (*Notebook, map[string]any, error)) (CellPreviewResult, error) {
	session, err := store.open(ctx, notebookUUID, true)
	if err != nil {
		return CellPreviewResult{}, err
	}
	defer session.Close()
	saved, err := session.savedPreview(ctx, cellID)
	if err != nil {
		return CellPreviewResult{}, err
	}
	if saved.Epoch != store.previewEpoch || saved.Preview.ResultID != resultID || saved.Environment != environment {
		return CellPreviewResult{}, ErrPreviewExpired
	}
	nb, params, err := current()
	if err != nil {
		return CellPreviewResult{}, err
	}
	if nb == nil || nb.UUID != notebookUUID || nb.CellByID(cellID) == nil {
		return CellPreviewResult{}, ErrPreviewExpired
	}
	now := previewFingerprints(nb, nb.CellByID(cellID), params)
	if len(now) != len(saved.Fingerprints) {
		return CellPreviewResult{}, ErrPreviewExpired
	}
	for id, fingerprint := range now {
		if fingerprint != saved.Fingerprints[id] {
			return CellPreviewResult{}, ErrPreviewExpired
		}
	}
	objectType, err := session.objectType(ctx, CellObjectName(cellID))
	if err != nil || objectType == "" {
		return CellPreviewResult{}, ErrPreviewExpired
	}
	return saved.prefix(limit), nil
}
