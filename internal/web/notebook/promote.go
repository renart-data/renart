package notebook

import (
	"fmt"
	"path/filepath"
	"strings"
)

// PromotedAsset is one pipeline asset file produced by a promotion.
type PromotedAsset struct {
	// Path is the absolute path of the new pipeline asset file.
	Path string
	// Content is the promoted file's content (pipeline frontmatter + the
	// cell's SQL body, with references to co-promoted cells rewritten).
	Content string
}

// PromotePlan is the file mutation set that turns one or more notebook cells
// into pipeline assets: the new asset files, reference rewrites in the cells
// left behind, removal of the original cell files, and their manifest blocks.
type PromotePlan struct {
	// Assets are the new pipeline asset files to write.
	Assets []PromotedAsset
	// ReferenceEdits rewrites remaining cells that referenced a promoted
	// cell so they point at the new pipeline asset name.
	ReferenceEdits []RenameEdit
	// RemoveCellPaths are the original cell files to delete.
	RemoveCellPaths []string
	// RemoveBlockIDs are the cell blocks to drop from notebook.yml.
	RemoveBlockIDs []string
	// DialectWarning is non-empty when the notebook's DuckDB dialect differs
	// from the target connection's, signalling the SQL may need review (no
	// transpiler is available to rewrite it automatically).
	DialectWarning string
}

// PromoteTarget pairs a notebook cell with the pipeline asset name it should
// become (schema.table).
type PromoteTarget struct {
	CellID     string
	TargetName string
	// AssetType and Connection optionally override the destination pipeline's
	// default for this block. Connection-bound warehouse source cells keep
	// their native execution context when promoted; local transforms leave both
	// fields empty and inherit the selected pipeline target.
	AssetType  string
	Connection string
}

// PlanPromote computes the promotion of a single cell. It is the one-cell case
// of PlanPromoteCells.
func PlanPromote(nb *Notebook, cellID, targetName, pipelineAssetsDir, assetType, targetDialect string) (*PromotePlan, error) {
	return PlanPromoteCells(nb, []PromoteTarget{{CellID: cellID, TargetName: targetName}}, pipelineAssetsDir, assetType, targetDialect)
}

// PlanPromoteCells computes the promotion of a connected set of cells into a
// pipeline. References between promoted cells are rewritten to the new asset
// names (so the assets depend on each other), and references from the cells
// left behind become external imports by the new names. assetType is the
// target connection's Bruin asset type (e.g. "bq.sql"); targetDialect is its
// SQL dialect for the mismatch check.
func PlanPromoteCells(nb *Notebook, targets []PromoteTarget, pipelineAssetsDir, assetType, targetDialect string) (*PromotePlan, error) {
	if len(targets) == 0 {
		return nil, fmt.Errorf("no cells to promote")
	}
	if assetType == "" {
		assetType = "duckdb.sql"
	}

	// Map each promoted cell's current name → its new asset name, so a
	// reference between two promoted cells follows them into the pipeline.
	promotedByID := make(map[string]*Cell, len(targets))
	nameToTarget := make(map[string]string, len(targets))
	promotedCellIDs := make(map[string]bool, len(targets))
	for _, target := range targets {
		cell := nb.CellByID(target.CellID)
		if cell == nil {
			return nil, fmt.Errorf("cell %q not found", target.CellID)
		}
		name := strings.TrimSpace(target.TargetName)
		if name == "" {
			return nil, fmt.Errorf("a target asset name is required for cell %q", cell.Asset.Name)
		}
		promotedByID[target.CellID] = cell
		nameToTarget[strings.ToLower(cell.Asset.Name)] = name
		promotedCellIDs[target.CellID] = true
	}

	plan := &PromotePlan{}
	needsDialectWarning := false

	for _, target := range targets {
		cell := promotedByID[target.CellID]
		targetName := strings.TrimSpace(target.TargetName)
		promotedType := strings.TrimSpace(target.AssetType)
		if promotedType == "" {
			promotedType = assetType
		}
		if IsPythonCell(cell) {
			promotedType = PythonCellType
		}

		// Rewrite references to the OTHER promoted cells in this cell's body.
		bodyRenames := map[string]string{}
		for lowerName, promotedName := range nameToTarget {
			if lowerName == strings.ToLower(cell.Asset.Name) {
				continue
			}
			bodyRenames[lowerName] = promotedName
		}

		plan.Assets = append(plan.Assets, PromotedAsset{
			Path:    promotedAssetFilePath(pipelineAssetsDir, targetName, cell),
			Content: promotedAssetContent(cell, targetName, promotedType, strings.TrimSpace(target.Connection), bodyRenames),
		})
		plan.RemoveCellPaths = append(plan.RemoveCellPaths, cell.Path)
		plan.RemoveBlockIDs = append(plan.RemoveBlockIDs, target.CellID)
		if !IsPythonCell(cell) && !IsSourceCell(cell) && strings.TrimSpace(cell.Asset.Connection) == "" {
			needsDialectWarning = true
		}
	}

	// Rewrite the cells left behind that referenced any promoted cell so they
	// read the new pipeline assets (now external imports) by their new names.
	for _, sibling := range nb.Cells {
		if promotedCellIDs[sibling.ID] {
			continue
		}
		renames := map[string]string{}
		for _, target := range targets {
			promoted := promotedByID[target.CellID]
			if referencesName(sibling, promoted.Asset.Name) {
				renames[strings.ToLower(promoted.Asset.Name)] = strings.TrimSpace(target.TargetName)
			}
		}
		if len(renames) == 0 {
			continue
		}
		rewritten := spliceIdentifiers(sibling.Raw, renames)
		if rewritten != sibling.Raw {
			plan.ReferenceEdits = append(plan.ReferenceEdits, RenameEdit{Path: sibling.Path, NewContent: rewritten})
		}
	}

	if needsDialectWarning && targetDialect != "" && targetDialect != "duckdb" {
		plan.DialectWarning = fmt.Sprintf(
			"these cells are written in DuckDB SQL but the target connection is %s; review the SQL before running it in the pipeline (automatic transpilation is not available)",
			targetDialect)
	}

	return plan, nil
}

// promotedAssetFilePath returns the new executable asset's file path under the
// pipeline assets dir, encoding the target name's schema prefix as
// subdirectories (for example marts.orders -> assets/marts/orders.sql).
// Python cells retain .py; source definitions are replaced by the service with
// a standalone .asset.yml after the shared graph/reference plan is built.
func promotedAssetFilePath(pipelineAssetsDir, targetName string, cell *Cell) string {
	parts := strings.Split(strings.TrimSpace(targetName), ".")
	leaf := parts[len(parts)-1]
	extension := ".sql"
	if IsPythonCell(cell) {
		extension = ".py"
	}
	segments := make([]string, 0, len(parts)+1)
	segments = append(segments, pipelineAssetsDir)
	segments = append(segments, parts[:len(parts)-1]...)
	segments = append(segments, leaf+extension)
	return filepath.Join(segments...)
}

// promotedAssetContent builds a fresh SQL or Python pipeline asset frontmatter
// (name = the real target, materialized as a table, ID retained for
// traceability) followed by the executable body. bodyRenames rewrites
// references to co-promoted cells.
func promotedAssetContent(cell *Cell, targetName, assetType, connection string, bodyRenames map[string]string) string {
	body := strings.TrimRight(cell.Asset.ExecutableFile.Content, "\n")
	if len(bodyRenames) > 0 {
		body = strings.TrimRight(spliceIdentifiers(body, bodyRenames), "\n")
	}
	var builder strings.Builder
	if IsPythonCell(cell) {
		builder.WriteString("\"\"\" @bruin\n")
	} else {
		builder.WriteString("/* @bruin\n")
	}
	builder.WriteString("name: " + targetName + "\n")
	builder.WriteString("type: " + assetType + "\n")
	if cell.ID != "" {
		builder.WriteString("id: " + cell.ID + "\n")
	}
	if connection != "" {
		builder.WriteString("connection: " + connection + "\n")
	}
	builder.WriteString("materialization:\n  type: table\n")
	if IsPythonCell(cell) {
		builder.WriteString("@bruin \"\"\"\n\n")
	} else {
		builder.WriteString("@bruin */\n\n")
	}
	builder.WriteString(body)
	builder.WriteString("\n")
	return builder.String()
}
