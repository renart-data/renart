package notebook

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	SnapshotModeFull   = "full"
	SnapshotModeSample = "sample"

	SnapshotModeMetaKey     = "renart_notebook_snapshot_mode"
	SnapshotRowLimitMetaKey = "renart_notebook_snapshot_row_limit"
)

// SQLSourceConfig is the authored execution identity for a SQL notebook cell.
// Empty Connection converts the cell back to a local DuckDB transform.
type SQLSourceConfig struct {
	Connection   string
	AssetType    string
	SnapshotMode string
	RowLimit     int64
}

// ConfigureSQLSource updates only the Bruin frontmatter of a SQL cell. The SQL
// body remains byte-for-byte unchanged, so switching execution context is an
// ordinary, narrow Git diff rather than a parser reprint.
func ConfigureSQLSource(content, cellID string, config SQLSourceConfig) (string, error) {
	content = NormalizeCellID(content, cellID, false)
	connection := strings.TrimSpace(config.Connection)
	assetType := strings.TrimSpace(config.AssetType)
	mode := strings.ToLower(strings.TrimSpace(config.SnapshotMode))
	if connection == "" {
		assetType = DefaultCellType
		mode = SnapshotModeFull
		config.RowLimit = 0
	} else {
		if assetType == "" || !strings.HasSuffix(strings.ToLower(assetType), ".sql") {
			return "", fmt.Errorf("source connection requires a SQL asset type")
		}
		if mode == "" {
			mode = SnapshotModeFull
		}
		if mode != SnapshotModeFull && mode != SnapshotModeSample {
			return "", fmt.Errorf("snapshot mode must be full or sample")
		}
		if mode == SnapshotModeSample && config.RowLimit <= 0 {
			return "", fmt.Errorf("sample snapshots require a positive row limit")
		}
	}

	lines := strings.Split(content, "\n")
	opener, closer := frontmatterLineBounds(lines)
	if opener < 0 || closer < 0 {
		return "", fmt.Errorf("notebook SQL cell has no Bruin frontmatter")
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(strings.Join(lines[opener+1:closer], "\n")), &document); err != nil {
		return "", fmt.Errorf("parse notebook cell frontmatter: %w", err)
	}
	mapping := sourceConfigMapping(&document)
	setSourceConfigScalar(mapping, "id", cellID)
	setSourceConfigScalar(mapping, "type", assetType)
	setSourceConfigScalar(mapping, "class", ClassNotebook)
	if connection == "" {
		deleteSourceConfigKey(mapping, "connection")
	} else {
		setSourceConfigScalar(mapping, "connection", connection)
	}

	meta := sourceConfigChildMapping(mapping, "meta", connection != "")
	if meta != nil {
		if connection == "" {
			deleteSourceConfigKey(meta, SnapshotModeMetaKey)
			deleteSourceConfigKey(meta, SnapshotRowLimitMetaKey)
		} else {
			setSourceConfigScalar(meta, SnapshotModeMetaKey, mode)
			if mode == SnapshotModeSample {
				setSourceConfigScalar(meta, SnapshotRowLimitMetaKey, strconv.FormatInt(config.RowLimit, 10))
			} else {
				deleteSourceConfigKey(meta, SnapshotRowLimitMetaKey)
			}
		}
		if len(meta.Content) == 0 {
			deleteSourceConfigKey(mapping, "meta")
		}
	}

	buffer := bytes.NewBuffer(nil)
	encoder := yaml.NewEncoder(buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return "", fmt.Errorf("encode notebook cell frontmatter: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	frontmatter := strings.TrimSuffix(buffer.String(), "\n")
	next := make([]string, 0, len(lines))
	next = append(next, lines[:opener+1]...)
	next = append(next, strings.Split(frontmatter, "\n")...)
	next = append(next, lines[closer:]...)
	return strings.Join(next, "\n"), nil
}

func SourceSnapshotPolicy(cell *Cell) (string, int64, error) {
	if cell == nil || cell.Asset == nil {
		return SnapshotModeFull, 0, nil
	}
	if cell.Source != nil {
		mode := strings.ToLower(strings.TrimSpace(cell.Source.Snapshot.Mode))
		if mode == "" || mode == SnapshotModeFull {
			return SnapshotModeFull, 0, nil
		}
		if mode != SnapshotModeSample || cell.Source.Snapshot.RowLimit <= 0 {
			return "", 0, fmt.Errorf("sample snapshots require a positive row limit")
		}
		return SnapshotModeSample, cell.Source.Snapshot.RowLimit, nil
	}
	if strings.TrimSpace(cell.Asset.Connection) == "" {
		return SnapshotModeFull, 0, nil
	}
	mode := strings.ToLower(strings.TrimSpace(cell.Asset.Meta[SnapshotModeMetaKey]))
	if mode == "" {
		mode = SnapshotModeFull
	}
	if mode == SnapshotModeFull {
		return mode, 0, nil
	}
	if mode != SnapshotModeSample {
		return "", 0, fmt.Errorf("snapshot mode must be full or sample")
	}
	limit, err := strconv.ParseInt(strings.TrimSpace(cell.Asset.Meta[SnapshotRowLimitMetaKey]), 10, 64)
	if err != nil || limit <= 0 {
		return "", 0, fmt.Errorf("sample snapshots require a positive row limit")
	}
	return mode, limit, nil
}

func frontmatterLineBounds(lines []string) (int, int) {
	opener := -1
	for index, line := range lines {
		if !strings.Contains(line, "@bruin") {
			continue
		}
		if opener == -1 {
			opener = index
			continue
		}
		return opener, index
	}
	return opener, -1
}

func sourceConfigMapping(document *yaml.Node) *yaml.Node {
	if document.Kind == 0 {
		document.Kind = yaml.DocumentNode
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		document.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	return document.Content[0]
}

func sourceConfigChildMapping(parent *yaml.Node, key string, create bool) *yaml.Node {
	for index := 0; index+1 < len(parent.Content); index += 2 {
		if parent.Content[index].Value == key {
			if parent.Content[index+1].Kind != yaml.MappingNode {
				if !create {
					return nil
				}
				parent.Content[index+1] = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			}
			return parent.Content[index+1]
		}
	}
	if !create {
		return nil
	}
	child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent.Content = append(parent.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, child)
	return child
}

func setSourceConfigScalar(mapping *yaml.Node, key, value string) {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			mapping.Content[index+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

func deleteSourceConfigKey(mapping *yaml.Node, key string) {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			mapping.Content = append(mapping.Content[:index], mapping.Content[index+2:]...)
			return
		}
	}
}
