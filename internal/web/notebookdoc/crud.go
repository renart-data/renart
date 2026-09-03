package notebookdoc

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/afero"

	"renart/internal/web/apperror"
	"renart/internal/web/model"
	"renart/internal/web/notebook"
	"renart/internal/web/workspacefs"
)

var (
	notebookSlugSanitizer = regexp.MustCompile(`[^a-z0-9_-]+`)
	cellNamePattern       = regexp.MustCompile(`^\w+$`)
)

type CreateRequest struct {
	Title string `json:"title"`
	Path  string `json:"path,omitempty"`
}

type CreateCellRequest struct {
	Name     string `json:"name,omitempty"`
	Language string `json:"language,omitempty"`
}

type UpdateCellRequest struct {
	Content      string `json:"content"`
	BaseRevision string `json:"base_revision,omitempty"`
}

func (s *Service) Create(req CreateRequest) (model.Notebook, *apperror.Error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "untitled"
	}
	relDir := strings.TrimSpace(req.Path)
	if relDir == "" {
		slug := strings.Trim(notebookSlugSanitizer.ReplaceAllString(strings.ToLower(title), "-"), "-")
		if slug == "" {
			slug = "untitled"
		}
		relDir = filepath.ToSlash(filepath.Join("notebooks", slug))
	}
	notebookID := workspacefs.EncodePathID(filepath.ToSlash(relDir))
	release := s.LockNotebook(notebookID)
	defer release()
	absDir, err := workspacefs.Join(s.deps.WorkspaceRoot, relDir)
	if err != nil {
		return model.Notebook{}, apiError(http.StatusBadRequest, "invalid_notebook_path", "notebook path escapes the workspace")
	}
	if _, err := os.Stat(filepath.Join(absDir, notebook.ManifestFileName)); err == nil {
		return model.Notebook{}, apiError(http.StatusConflict, "notebook_exists", fmt.Sprintf("a notebook already exists at %s", relDir))
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return model.Notebook{}, apiError(http.StatusInternalServerError, "notebook_create_failed", err.Error())
	}

	exampleID := notebook.NewCellID()
	exampleName := NextCellAutoname(&notebook.Notebook{}, s.pipelineAssetNames())
	exampleContent := fmt.Sprintf(
		"/* @bruin\nid: %q\ntype: %s\nclass: %s\n@bruin */\n\nselect 'hello' as greeting, 42 as answer\n",
		exampleID, notebook.DefaultCellType, notebook.ClassNotebook,
	)
	if err := os.WriteFile(filepath.Join(absDir, exampleName+".sql"), []byte(exampleContent), 0o644); err != nil {
		return model.Notebook{}, apiError(http.StatusInternalServerError, "notebook_create_failed", err.Error())
	}
	manifest := &notebook.Notebook{
		Version: notebook.ManifestVersionCurrent,
		Title:   title, Dir: absDir, Blocks: []notebook.Block{{Cell: exampleID}},
	}
	if err := notebook.SaveManifest(afero.NewOsFs(), manifest); err != nil {
		return model.Notebook{}, apiError(http.StatusInternalServerError, "notebook_create_failed", err.Error())
	}

	result, apiErr := s.Get(notebookID)
	if apiErr != nil {
		return model.Notebook{}, apiErr
	}
	s.pushUpdate(absDir)
	return result, nil
}

func (s *Service) Delete(notebookID string) *apperror.Error {
	release := s.LockNotebook(notebookID)
	defer release()
	nb, apiErr := s.Load(notebookID)
	if apiErr != nil {
		return apiErr
	}
	if err := os.RemoveAll(nb.Dir); err != nil {
		return apiError(http.StatusInternalServerError, "notebook_delete_failed", err.Error())
	}
	if s.deps.RemoveSession != nil {
		if err := s.deps.RemoveSession(nb.UUID); err != nil {
			return apiError(http.StatusInternalServerError, "notebook_session_delete_failed", err.Error())
		}
	}
	s.pushUpdate(nb.Dir)
	return nil
}

func (s *Service) CloseSession(notebookID string) *apperror.Error {
	nb, apiErr := s.Load(notebookID)
	if apiErr != nil {
		return apiErr
	}
	if s.deps.RemoveSession != nil {
		if err := s.deps.RemoveSession(nb.UUID); err != nil {
			return apiError(http.StatusInternalServerError, "notebook_session_delete_failed", err.Error())
		}
	}
	return nil
}

func (s *Service) CreateCell(notebookID string, req CreateCellRequest) (model.Notebook, *apperror.Error) {
	release := s.LockNotebook(notebookID)
	defer release()
	nb, apiErr := s.Load(notebookID)
	if apiErr != nil {
		return model.Notebook{}, apiErr
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = NextCellAutoname(nb, s.pipelineAssetNames())
	}
	if !cellNamePattern.MatchString(name) {
		return model.Notebook{}, apiError(http.StatusBadRequest, "invalid_cell_name", "cell names may only contain letters, digits, and underscores")
	}
	if nb.CellByName(name) != nil {
		return model.Notebook{}, apiError(http.StatusConflict, "cell_exists", fmt.Sprintf("a cell named %q already exists", name))
	}
	if s.pipelineAssetNames()[strings.ToLower(name)] {
		return model.Notebook{}, apiError(http.StatusConflict, "cell_name_collides", fmt.Sprintf("%q is already a pipeline asset name", name))
	}

	cellID := notebook.NewCellID()
	for nb.CellByID(cellID) != nil {
		cellID = notebook.NewCellID()
	}
	ext, template := ".sql", notebook.CellFileTemplate(cellID)
	if strings.EqualFold(strings.TrimSpace(req.Language), "python") {
		ext, template = ".py", notebook.PythonCellFileTemplate(cellID)
	}
	path := filepath.Join(nb.Dir, name+ext)
	if err := os.WriteFile(path, []byte(template), 0o644); err != nil {
		return model.Notebook{}, apiError(http.StatusInternalServerError, "cell_create_failed", err.Error())
	}
	nb.Blocks = append(nb.Blocks, notebook.Block{Cell: cellID})
	if err := notebook.SaveManifest(afero.NewOsFs(), nb); err != nil {
		return model.Notebook{}, apiError(http.StatusInternalServerError, "cell_create_failed", err.Error())
	}
	s.pushUpdate(path)
	return s.Get(notebookID)
}

func (s *Service) RenameCell(notebookID, cellID, newName string) (model.Notebook, *apperror.Error) {
	release := s.LockNotebook(notebookID)
	defer release()
	nb, apiErr := s.Load(notebookID)
	if apiErr != nil {
		return model.Notebook{}, apiErr
	}
	cell := nb.CellByID(cellID)
	if cell == nil {
		return model.Notebook{}, apiError(http.StatusNotFound, "cell_not_found", "cell not found")
	}
	newName = strings.TrimSpace(newName)
	if message := notebook.ValidateCellName(nb, newName, cellID, s.pipelineAssetNames()); message != "" {
		return model.Notebook{}, apiError(http.StatusConflict, "invalid_cell_name", message)
	}
	edits, err := notebook.PlanRename(nb, cellID, newName)
	if err != nil {
		return model.Notebook{}, apiError(http.StatusBadRequest, "rename_failed", err.Error())
	}
	for _, edit := range edits {
		if edit.NewContent != "" {
			if err := os.WriteFile(edit.Path, []byte(edit.NewContent), 0o644); err != nil {
				return model.Notebook{}, apiError(http.StatusInternalServerError, "rename_failed", err.Error())
			}
		}
	}
	for _, edit := range edits {
		if edit.NewPath != "" {
			if err := os.Rename(edit.Path, edit.NewPath); err != nil {
				return model.Notebook{}, apiError(http.StatusInternalServerError, "rename_failed", err.Error())
			}
		}
	}
	s.pushUpdate(cell.Path)
	return s.Get(notebookID)
}

func (s *Service) UpdateCell(notebookID, cellID string, req UpdateCellRequest) (model.Notebook, *apperror.Error) {
	releaseCell := s.LockCell(notebookID, cellID)
	defer releaseCell()
	releaseNotebook := s.LockNotebook(notebookID)
	defer releaseNotebook()
	nb, apiErr := s.Load(notebookID)
	if apiErr != nil {
		return model.Notebook{}, apiErr
	}
	cell := nb.CellByID(cellID)
	if cell == nil {
		return model.Notebook{}, apiError(http.StatusNotFound, "cell_not_found", "cell not found")
	}
	current := cell.Raw
	if current == "" {
		if raw, err := os.ReadFile(cell.Path); err == nil {
			current = string(raw)
		}
	}
	if req.BaseRevision != "" && req.BaseRevision != notebook.ContentRevision(current) {
		return model.Notebook{}, apiError(
			http.StatusConflict, "cell_edit_conflict",
			"This cell changed after editing began. Your draft was kept; reload or reconcile the newer content before saving.",
		)
	}
	normalized := notebook.NormalizeCellID(req.Content, cellID, notebook.IsPythonCell(cell))
	if normalized == notebook.NormalizeCellID(current, cellID, notebook.IsPythonCell(cell)) {
		return s.ToModel(nb), nil
	}
	if err := os.WriteFile(cell.Path, []byte(normalized), 0o644); err != nil {
		return model.Notebook{}, apiError(http.StatusInternalServerError, "cell_update_failed", err.Error())
	}
	s.pushUpdate(cell.Path)
	if fresh, freshErr := s.Load(notebookID); freshErr == nil && s.deps.OnCellChanged != nil {
		s.deps.OnCellChanged(notebookID, fresh, cellID)
	}
	return s.Get(notebookID)
}

func (s *Service) DeleteCell(notebookID, cellID string) (model.Notebook, *apperror.Error) {
	release := s.LockNotebook(notebookID)
	defer release()
	nb, apiErr := s.Load(notebookID)
	if apiErr != nil {
		return model.Notebook{}, apiErr
	}
	cell := nb.CellByID(cellID)
	if cell == nil {
		return model.Notebook{}, apiError(http.StatusNotFound, "cell_not_found", "cell not found")
	}
	if err := os.Remove(cell.Path); err != nil && !os.IsNotExist(err) {
		return model.Notebook{}, apiError(http.StatusInternalServerError, "cell_delete_failed", err.Error())
	}
	blocks := make([]notebook.Block, 0, len(nb.Blocks))
	for _, block := range nb.Blocks {
		if block.Cell == cellID || (block.Visualization != nil && block.Visualization.Source == cellID) {
			continue
		}
		blocks = append(blocks, block)
	}
	nb.Blocks = blocks
	remaining := make([]*notebook.Cell, 0, len(nb.Cells))
	for _, candidate := range nb.Cells {
		if candidate.ID != cellID {
			remaining = append(remaining, candidate)
		}
	}
	nb.Cells = remaining
	if err := notebook.SaveManifest(afero.NewOsFs(), nb); err != nil {
		return model.Notebook{}, apiError(http.StatusInternalServerError, "cell_delete_failed", err.Error())
	}
	if s.deps.DropCellObjects != nil {
		_ = s.deps.DropCellObjects(nb.UUID, cellID)
	}
	s.pushUpdate(cell.Path)
	if s.deps.OnCellDeleted != nil {
		s.deps.OnCellDeleted(notebookID, nb.UUID, cellID)
	}
	return s.Get(notebookID)
}

func (s *Service) UpdateBlocks(notebookID string, blocks []model.NotebookBlock) (model.Notebook, *apperror.Error) {
	release := s.LockNotebook(notebookID)
	defer release()
	nb, apiErr := s.Load(notebookID)
	if apiErr != nil {
		return model.Notebook{}, apiErr
	}
	seenCells := map[string]bool{}
	seenBlocks := map[string]bool{}
	next := make([]notebook.Block, 0, len(blocks))
	for _, block := range blocks {
		converted, apiErr := normalizeBlock(nb, block, seenCells, seenBlocks)
		if apiErr != nil {
			return model.Notebook{}, apiErr
		}
		next = append(next, converted)
	}
	for _, cell := range nb.Cells {
		if !seenCells[cell.ID] {
			next = append(next, notebook.Block{Cell: cell.ID})
		}
	}
	nb.Blocks = next
	if s.deps.CheckVisualizations != nil {
		if _, blocking := s.deps.CheckVisualizations(context.Background(), nb); len(blocking) > 0 {
			return model.Notebook{}, apiError(http.StatusBadRequest, "invalid_visualization_definition", strings.Join(blocking, "; "))
		}
	}
	if err := notebook.SaveManifest(afero.NewOsFs(), nb); err != nil {
		return model.Notebook{}, apiError(http.StatusInternalServerError, "blocks_update_failed", err.Error())
	}
	s.pushUpdate(filepath.Join(nb.Dir, notebook.ManifestFileName))
	return s.Get(notebookID)
}

func normalizeBlock(
	nb *notebook.Notebook,
	block model.NotebookBlock,
	seenCells map[string]bool,
	seenBlocks map[string]bool,
) (notebook.Block, *apperror.Error) {
	if block.Cell != "" {
		if block.Visualization != nil || block.Markdown != "" || block.Control != "" || block.ID != "" {
			return notebook.Block{}, apiError(http.StatusBadRequest, "invalid_notebook_block", "a cell block cannot also contain presentation content")
		}
		if nb.CellByID(block.Cell) == nil {
			return notebook.Block{}, apiError(http.StatusBadRequest, "unknown_cell", fmt.Sprintf("block references unknown cell %q", block.Cell))
		}
		if seenCells[block.Cell] {
			return notebook.Block{}, apiError(http.StatusBadRequest, "duplicate_cell_block", fmt.Sprintf("cell %q appears more than once", block.Cell))
		}
		seenCells[block.Cell] = true
		return notebook.Block{Cell: block.Cell}, nil
	}
	if block.Control != "" {
		if nb.Version < notebook.ManifestVersionCurrent {
			return notebook.Block{}, apiError(http.StatusConflict, "notebook_upgrade_required", "upgrade this notebook before placing controls")
		}
		if block.Visualization != nil || block.Markdown != "" || block.ID != "" {
			return notebook.Block{}, apiError(http.StatusBadRequest, "invalid_notebook_block", "a control block cannot also contain other presentation content")
		}
		controlID := strings.TrimSpace(block.Control)
		known := false
		for _, parameter := range nb.Parameters {
			if parameter.ID == controlID {
				known = true
				break
			}
		}
		if !known {
			return notebook.Block{}, apiError(http.StatusBadRequest, "unknown_notebook_control", fmt.Sprintf("control references unknown parameter %q", controlID))
		}
		stableID := "control:" + controlID
		if seenBlocks[stableID] {
			return notebook.Block{}, apiError(http.StatusBadRequest, "duplicate_notebook_block", fmt.Sprintf("control %q appears more than once", controlID))
		}
		seenBlocks[stableID] = true
		return notebook.Block{Control: controlID}, nil
	}
	if block.Visualization != nil {
		if nb.Version < notebook.ManifestVersionCurrent {
			return notebook.Block{}, apiError(http.StatusConflict, "notebook_upgrade_required", "upgrade this notebook before adding structured visualizations")
		}
		id := strings.TrimSpace(block.ID)
		visualizationID := strings.TrimSpace(block.Visualization.ID)
		if id != "" && visualizationID != "" && id != visualizationID {
			return notebook.Block{}, apiError(http.StatusBadRequest, "invalid_notebook_block", "visualization block ids do not match")
		}
		if id == "" {
			id = visualizationID
		}
		if id == "" {
			id = notebook.NewBlockID("viz")
		}
		if seenBlocks[id] {
			return notebook.Block{}, apiError(http.StatusBadRequest, "duplicate_notebook_block", fmt.Sprintf("block id %q appears more than once", id))
		}
		seenBlocks[id] = true
		source := strings.TrimSpace(block.Visualization.Source)
		if nb.CellByID(source) == nil {
			return notebook.Block{}, apiError(http.StatusBadRequest, "unknown_visualization_source", fmt.Sprintf("visualization %q references unknown source cell %q", id, source))
		}
		if len(block.Visualization.Definition) == 0 {
			return notebook.Block{}, apiError(http.StatusBadRequest, "invalid_visualization_definition", fmt.Sprintf("visualization %q has no definition", id))
		}
		return notebook.Block{ID: id, Visualization: &notebook.VisualizationBlock{
			ID: id, Source: source, Definition: cloneStringAnyMap(block.Visualization.Definition),
		}}, nil
	}
	id := strings.TrimSpace(block.ID)
	if nb.Version >= notebook.ManifestVersionCurrent {
		if id == "" {
			id = notebook.NewBlockID("md")
		}
		if seenBlocks[id] {
			return notebook.Block{}, apiError(http.StatusBadRequest, "duplicate_notebook_block", fmt.Sprintf("block id %q appears more than once", id))
		}
		seenBlocks[id] = true
	}
	return notebook.Block{ID: id, Markdown: block.Markdown}, nil
}

func (s *Service) UpgradeManifest(notebookID, baseRevision string) (model.Notebook, *apperror.Error) {
	release := s.LockNotebook(notebookID)
	defer release()
	nb, apiErr := s.Load(notebookID)
	if apiErr != nil {
		return model.Notebook{}, apiErr
	}
	if baseRevision != "" && baseRevision != nb.Revision {
		return model.Notebook{}, apiError(
			http.StatusConflict, "notebook_edit_conflict",
			"This notebook changed after the upgrade was prepared. Reload it before upgrading.",
		)
	}
	changed, err := notebook.UpgradeManifestV2(afero.NewOsFs(), nb)
	if err != nil {
		return model.Notebook{}, apiError(http.StatusInternalServerError, "notebook_upgrade_failed", err.Error())
	}
	if changed {
		s.pushUpdate(filepath.Join(nb.Dir, notebook.ManifestFileName))
	}
	return s.Get(notebookID)
}

func (s *Service) pipelineAssetNames() map[string]bool {
	if s.deps.PipelineAssetNames == nil {
		return map[string]bool{}
	}
	return s.deps.PipelineAssetNames()
}

func (s *Service) pushUpdate(path string) {
	if s.deps.PushWorkspaceUpdate != nil {
		s.deps.PushWorkspaceUpdate(path)
	}
}

func apiError(status int, code, message string) *apperror.Error {
	return &apperror.Error{Status: status, Code: code, Message: message}
}

var cellNameAdjectives = [...]string{
	"amber", "brisk", "calm", "clever", "cozy", "crisp", "eager", "gentle",
	"golden", "happy", "hidden", "kind", "lively", "lucid", "merry", "nimble",
	"quiet", "rapid", "ready", "silver", "smooth", "steady", "sunny", "swift",
	"tidy", "vivid", "warm", "wise", "bright", "fresh", "playful", "soft",
}

var cellNameNouns = [...]string{
	"badger", "beacon", "brook", "cedar", "comet", "coral", "dune", "ember",
	"fern", "fox", "grove", "harbor", "heron", "hill", "iris", "lake",
	"maple", "meadow", "moon", "otter", "pine", "river", "robin", "sparrow",
	"stone", "summit", "tiger", "valley", "willow", "wren", "orchid", "pebble",
}

func NextCellAutoname(nb *notebook.Notebook, pipelineAssetNames map[string]bool) string {
	var random [8]byte
	seed := uint64(time.Now().UnixNano())
	if _, err := cryptorand.Read(random[:]); err == nil {
		seed = binary.LittleEndian.Uint64(random[:])
	}
	return CellAutonameFromSeed(nb, pipelineAssetNames, seed)
}

func CellAutonameFromSeed(nb *notebook.Notebook, pipelineAssetNames map[string]bool, seed uint64) string {
	total := len(cellNameAdjectives) * len(cellNameNouns)
	start := int(seed % uint64(total))
	for attempt := 0; attempt < total; attempt++ {
		index := (start + attempt) % total
		candidate := cellNameAdjectives[index/len(cellNameNouns)] + "_" + cellNameNouns[index%len(cellNameNouns)]
		if notebook.ValidateCellName(nb, candidate, "", pipelineAssetNames) == "" {
			return candidate
		}
	}
	base := cellNameAdjectives[start/len(cellNameNouns)] + "_" + cellNameNouns[start%len(cellNameNouns)]
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s_%d", base, suffix)
		if notebook.ValidateCellName(nb, candidate, "", pipelineAssetNames) == "" {
			return candidate
		}
	}
}
