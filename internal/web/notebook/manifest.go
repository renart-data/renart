package notebook

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"
	"renart/internal/web/presentation"
)

// manifest is the parsed notebook.yml.
type manifest struct {
	Version    int
	ID         string
	Title      string
	Target     string
	Parameters []presentation.ParameterDefinition
	Blocks     []Block
}

type manifestYAML struct {
	Version    int                                `yaml:"version,omitempty"`
	ID         string                             `yaml:"id,omitempty"`
	Title      string                             `yaml:"title,omitempty"`
	Target     string                             `yaml:"target,omitempty"`
	Parameters []presentation.ParameterDefinition `yaml:"parameters,omitempty"`
	Blocks     []blockYAML                        `yaml:"blocks"`
}

type markdownBlockYAML struct {
	ID      string `yaml:"id,omitempty"`
	Content string `yaml:"content"`
}

type visualizationBlockYAML struct {
	ID         string         `yaml:"id,omitempty"`
	Source     string         `yaml:"source"`
	Definition map[string]any `yaml:"definition"`
}

// blockYAML accepts both the v1 scalar markdown shape and v2's nested,
// identity-bearing markdown/visualization shape. MarshalManifest uses separate
// output structs so the selected version is always rendered unambiguously.
type blockYAML struct {
	Cell             string
	Markdown         *markdownBlockYAML
	Visualization    *visualizationBlockYAML
	legacyMarkdown   bool
	manifestKeyCount int
}

func (b *blockYAML) UnmarshalYAML(node *yaml.Node) error {
	if node == nil || node.Kind != yaml.MappingNode {
		return fmt.Errorf("notebook block must be a mapping")
	}
	*b = blockYAML{}
	for index := 0; index+1 < len(node.Content); index += 2 {
		key := node.Content[index].Value
		value := node.Content[index+1]
		switch key {
		case "cell":
			if err := value.Decode(&b.Cell); err != nil {
				return fmt.Errorf("invalid cell block: %w", err)
			}
			b.manifestKeyCount++
		case "markdown":
			if value.Kind == yaml.ScalarNode {
				b.Markdown = &markdownBlockYAML{Content: value.Value}
				b.legacyMarkdown = true
			} else {
				var markdown markdownBlockYAML
				if err := value.Decode(&markdown); err != nil {
					return fmt.Errorf("invalid markdown block: %w", err)
				}
				b.Markdown = &markdown
			}
			b.manifestKeyCount++
		case "visualization":
			var visualization visualizationBlockYAML
			if err := value.Decode(&visualization); err != nil {
				return fmt.Errorf("invalid visualization block: %w", err)
			}
			b.Visualization = &visualization
			b.manifestKeyCount++
		default:
			return fmt.Errorf("unknown notebook block kind %q", key)
		}
	}
	if b.manifestKeyCount != 1 {
		return fmt.Errorf("notebook block must contain exactly one of cell, markdown, or visualization")
	}
	return nil
}

func readManifest(filesystem afero.Fs, path string) (*manifest, error) {
	content, err := afero.ReadFile(filesystem, path)
	if err != nil {
		return nil, err
	}

	var parsed manifestYAML
	if err := yaml.Unmarshal(content, &parsed); err != nil {
		return nil, fmt.Errorf("invalid notebook manifest: %w", err)
	}
	version := parsed.Version
	if version == 0 {
		version = ManifestVersionLegacy
	}
	if version != ManifestVersionLegacy && version != ManifestVersionCurrent {
		return nil, fmt.Errorf("unsupported notebook manifest version %d", version)
	}
	if version == ManifestVersionLegacy && len(parsed.Parameters) > 0 {
		return nil, fmt.Errorf("notebook parameters require version: 2")
	}

	result := &manifest{
		Version:    version,
		ID:         parsed.ID,
		Title:      parsed.Title,
		Target:     parsed.Target,
		Parameters: append([]presentation.ParameterDefinition(nil), parsed.Parameters...),
		Blocks:     make([]Block, 0, len(parsed.Blocks)),
	}
	for index, block := range parsed.Blocks {
		switch {
		case block.Cell != "":
			result.Blocks = append(result.Blocks, Block{Cell: block.Cell})
		case block.Markdown != nil:
			if version == ManifestVersionLegacy && !block.legacyMarkdown {
				return nil, fmt.Errorf("block %d uses version 2 markdown syntax without version: 2", index+1)
			}
			if version == ManifestVersionCurrent && block.legacyMarkdown {
				return nil, fmt.Errorf("block %d uses legacy markdown syntax in a version 2 manifest", index+1)
			}
			result.Blocks = append(result.Blocks, Block{
				ID:       block.Markdown.ID,
				Markdown: block.Markdown.Content,
			})
		case block.Visualization != nil:
			if version != ManifestVersionCurrent {
				return nil, fmt.Errorf("block %d uses visualization syntax without version: 2", index+1)
			}
			result.Blocks = append(result.Blocks, Block{
				ID: block.Visualization.ID,
				Visualization: &VisualizationBlock{
					ID:         block.Visualization.ID,
					Source:     block.Visualization.Source,
					Definition: cloneDefinition(block.Visualization.Definition),
				},
			})
		default:
			return nil, fmt.Errorf("block %d has no supported content", index+1)
		}
	}
	return result, nil
}

type legacyManifestYAML struct {
	ID     string            `yaml:"id,omitempty"`
	Title  string            `yaml:"title,omitempty"`
	Target string            `yaml:"target,omitempty"`
	Blocks []legacyBlockYAML `yaml:"blocks"`
}

type legacyBlockYAML struct {
	Cell     string `yaml:"cell,omitempty"`
	Markdown string `yaml:"markdown,omitempty"`
}

type manifestV2YAML struct {
	Version    int                                `yaml:"version"`
	ID         string                             `yaml:"id,omitempty"`
	Title      string                             `yaml:"title,omitempty"`
	Target     string                             `yaml:"target,omitempty"`
	Parameters []presentation.ParameterDefinition `yaml:"parameters,omitempty"`
	Blocks     []v2BlockYAML                      `yaml:"blocks"`
}

type v2BlockYAML struct {
	Cell          string                  `yaml:"cell,omitempty"`
	Markdown      *markdownBlockYAML      `yaml:"markdown,omitempty"`
	Visualization *visualizationBlockYAML `yaml:"visualization,omitempty"`
}

// MarshalManifest serializes a notebook's manifest deterministically: fixed
// key order, two-space indent, block order exactly as given. Legacy notebooks
// remain byte-shape compatible until an explicit upgrade; new/v2 notebooks use
// identity-bearing nested presentation blocks.
func MarshalManifest(nb *Notebook) ([]byte, error) {
	if nb == nil {
		return nil, fmt.Errorf("cannot marshal a nil notebook")
	}
	version := nb.Version
	if version == 0 {
		version = ManifestVersionLegacy
	}

	var doc any
	switch version {
	case ManifestVersionLegacy:
		legacy := legacyManifestYAML{
			ID:     nb.UUID,
			Title:  persistedNotebookTitle(nb),
			Target: nb.Target,
			Blocks: make([]legacyBlockYAML, 0, len(nb.Blocks)),
		}
		for _, block := range nb.Blocks {
			if block.Visualization != nil {
				return nil, fmt.Errorf("visualization block %q requires notebook manifest version 2", block.StableID())
			}
			legacy.Blocks = append(legacy.Blocks, legacyBlockYAML{Cell: block.Cell, Markdown: block.Markdown})
		}
		doc = legacy
	case ManifestVersionCurrent:
		current := manifestV2YAML{
			Version:    ManifestVersionCurrent,
			ID:         nb.UUID,
			Title:      persistedNotebookTitle(nb),
			Target:     nb.Target,
			Parameters: append([]presentation.ParameterDefinition(nil), nb.Parameters...),
			Blocks:     make([]v2BlockYAML, 0, len(nb.Blocks)),
		}
		for index, block := range nb.Blocks {
			switch {
			case block.Cell != "":
				current.Blocks = append(current.Blocks, v2BlockYAML{Cell: block.Cell})
			case block.Visualization != nil:
				id := block.ID
				if id == "" {
					id = block.Visualization.ID
				}
				if id == "" {
					return nil, fmt.Errorf("visualization block %d is missing an id", index+1)
				}
				current.Blocks = append(current.Blocks, v2BlockYAML{Visualization: &visualizationBlockYAML{
					ID:         id,
					Source:     block.Visualization.Source,
					Definition: cloneDefinition(block.Visualization.Definition),
				}})
			default:
				if block.ID == "" {
					return nil, fmt.Errorf("markdown block %d is missing an id", index+1)
				}
				current.Blocks = append(current.Blocks, v2BlockYAML{Markdown: &markdownBlockYAML{
					ID: block.ID, Content: block.Markdown,
				}})
			}
		}
		doc = current
	default:
		return nil, fmt.Errorf("unsupported notebook manifest version %d", version)
	}

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(doc); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func persistedNotebookTitle(nb *Notebook) string {
	if nb.Title == filepath.Base(nb.Dir) {
		return ""
	}
	return nb.Title
}

// UpgradeManifestV2 upgrades only the manifest-owned structure. IDs for
// existing markdown blocks are derived from the durable notebook ID and block
// position, so retrying a failed migration produces the same Git diff. Legacy
// @viz comments remain readable and are migrated separately when structured
// visualization authoring ships.
func UpgradeManifestV2(filesystem afero.Fs, nb *Notebook) (bool, error) {
	if nb == nil {
		return false, fmt.Errorf("cannot upgrade a nil notebook")
	}
	if nb.Version >= ManifestVersionCurrent {
		return false, nil
	}

	next := *nb
	next.Version = ManifestVersionCurrent
	next.Blocks = make([]Block, len(nb.Blocks))
	for index, block := range nb.Blocks {
		next.Blocks[index] = block
		if block.Cell == "" && block.Visualization == nil {
			next.Blocks[index].ID = migratedBlockID(nb.UUID, "md", index)
		}
	}
	if err := SaveManifest(filesystem, &next); err != nil {
		return false, err
	}
	nb.Version = next.Version
	nb.Blocks = next.Blocks
	return true, nil
}

func migratedBlockID(notebookID, kind string, index int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("notebook-block-v2\x00%s\x00%s\x00%d", notebookID, kind, index)))
	return kind + "_" + hex.EncodeToString(sum[:6])
}

func cloneDefinition(definition map[string]any) map[string]any {
	if definition == nil {
		return map[string]any{}
	}
	result := make(map[string]any, len(definition))
	for key, value := range definition {
		result[key] = value
	}
	return result
}

// SaveManifest writes the notebook manifest, skipping the write when the
// on-disk content is already identical.
func SaveManifest(filesystem afero.Fs, nb *Notebook) error {
	content, err := MarshalManifest(nb)
	if err != nil {
		return err
	}

	path := filepath.Join(nb.Dir, ManifestFileName)
	if existing, readErr := afero.ReadFile(filesystem, path); readErr == nil && bytes.Equal(existing, content) {
		return nil
	}
	return afero.WriteFile(filesystem, path, content, 0o644)
}

// reconcileBlocks merges the manifest's ordered blocks with the cells found
// on disk: blocks referencing missing cells are dropped (with a problem note),
// and cells absent from the blocks are appended in filename order. Returns the
// effective block list and the cells ordered by appearance.
func reconcileBlocks(blocks []Block, cells []*Cell, problems *[]string) ([]Block, []*Cell) {
	cellByID := make(map[string]*Cell, len(cells))
	for _, cell := range cells {
		cellByID[cell.ID] = cell
	}

	resultBlocks := make([]Block, 0, len(blocks)+len(cells))
	orderedCells := make([]*Cell, 0, len(cells))
	referenced := make(map[string]bool, len(cells))

	for _, block := range blocks {
		if block.Cell == "" {
			resultBlocks = append(resultBlocks, block)
			continue
		}
		cell, ok := cellByID[block.Cell]
		if !ok {
			*problems = append(*problems, fmt.Sprintf("notebook.yml references unknown cell %q", block.Cell))
			continue
		}
		if referenced[block.Cell] {
			*problems = append(*problems, fmt.Sprintf("notebook.yml lists cell %q more than once", block.Cell))
			continue
		}
		referenced[block.Cell] = true
		resultBlocks = append(resultBlocks, block)
		orderedCells = append(orderedCells, cell)
	}

	unreferenced := make([]*Cell, 0)
	for _, cell := range cells {
		if !referenced[cell.ID] {
			unreferenced = append(unreferenced, cell)
		}
	}
	sort.Slice(unreferenced, func(i, j int) bool {
		return filepath.Base(unreferenced[i].Path) < filepath.Base(unreferenced[j].Path)
	})
	for _, cell := range unreferenced {
		resultBlocks = append(resultBlocks, Block{Cell: cell.ID})
		orderedCells = append(orderedCells, cell)
	}

	return resultBlocks, orderedCells
}
