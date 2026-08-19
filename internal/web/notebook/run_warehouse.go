package notebook

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (r *Runner) runWarehouseSQLSource(
	ctx context.Context,
	session *Session,
	nb *Notebook,
	cell *Cell,
	result CellRunResult,
	startedAt time.Time,
	opts RunOptions,
) CellRunResult {
	for _, upstream := range cell.Asset.Upstreams {
		if nb.CellByName(upstream.Value) != nil {
			result.Error = fmt.Sprintf("warehouse source cell %q cannot reference local notebook cell %q; snapshot the warehouse data first and join it in a local SQL cell", cell.Asset.Name, upstream.Value)
			result.DurationMS = time.Since(startedAt).Milliseconds()
			return result
		}
	}
	if r.WarehouseExecutor == nil {
		result.Error = "warehouse source execution is not available in this environment"
		result.DurationMS = time.Since(startedAt).Milliseconds()
		return result
	}
	content := strings.TrimSpace(cell.Asset.ExecutableFile.Content)
	if content == "" {
		result.Error = "cell is empty"
		result.DurationMS = time.Since(startedAt).Milliseconds()
		return result
	}
	if r.RenderSQL != nil {
		rendered, err := r.RenderSQL(content)
		if err != nil {
			result.Error = "could not render Jinja: " + err.Error()
			result.DurationMS = time.Since(startedAt).Milliseconds()
			return result
		}
		content = strings.TrimSpace(rendered)
	}
	output, err := r.WarehouseExecutor.Execute(ctx, ExecuteBlockInput{
		Notebook: nb, Cell: cell, Environment: r.Environment, SQL: content,
		Refresh: opts.RefreshImports, ParameterValues: r.ParameterValues,
	})
	return r.publishSourceOutput(ctx, session, cell, result, startedAt, content, output, err)
}
