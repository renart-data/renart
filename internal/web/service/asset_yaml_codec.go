package service

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"
)

// YAML-defined assets (api, load, ingestr, plain `.asset.yml`, and any future
// kind such as dbt) all round-trip through a single node-preserving codec: on
// write, renart replaces only the keys it manages and leaves everything else in
// the file untouched — notably the API request `parameters` spec, comments, and
// any unknown keys. This is the composable seam: a new YAML asset kind needs no
// bespoke reader/writer, it just works.
//
// SQL and Python assets are NOT handled here; their definition lives in a
// `@bruin` comment block embedded in the executable file, which bruin's own
// Persist/FormatContent already round-trips.

// baseManagedYAMLAssetKeys are the top-level keys renart owns in every YAML asset
// definition. On write each is replaced from the asset (or removed when the asset
// no longer carries it); every other key in the file is preserved.
var baseManagedYAMLAssetKeys = []string{
	"name",
	"uri",
	"type",
	"description",
	"connection",
	"owner",
	"tags",
	"depends",
	"columns",
	"custom_checks",
	"hooks",
	"materialization",
	"meta",
}

// managedYAMLAssetKeys returns the managed keys for a specific asset. `parameters`
// is managed for Load, seed, and sensor assets, whose intent Renart edits as flat
// string parameters. For API assets `parameters` holds a nested request/response
// spec that Renart does NOT model, so it stays preserved-but-unmanaged — managing
// it there would delete the spec on the next write.
func managedYAMLAssetKeys(asset *pipeline.Asset) []string {
	if isLoadAsset(asset) ||
		strings.HasSuffix(strings.ToLower(string(asset.Type)), ".seed") ||
		isSensorAssetType(asset.Type) {
		return append(append([]string{}, baseManagedYAMLAssetKeys...), "parameters")
	}
	return baseManagedYAMLAssetKeys
}

// isYAMLDefinedAsset reports whether the asset's definition is a standalone YAML
// file (rather than a `@bruin` block inside a .sql/.py executable).
func isYAMLDefinedAsset(asset *pipeline.Asset) bool {
	if asset == nil {
		return false
	}
	exec := strings.ToLower(strings.TrimSpace(asset.ExecutableFile.Path))
	if strings.HasSuffix(exec, ".sql") || strings.HasSuffix(exec, ".py") {
		return false
	}
	return strings.TrimSpace(asset.DefinitionFile.Path) != ""
}

// persistYAMLAssetDefinition writes a YAML-defined asset by overlaying renart's
// managed fields onto the existing definition file, preserving all unmanaged
// content. It is the single write path for api/load/ingestr/plain-yaml assets.
func persistYAMLAssetDefinition(fs afero.Fs, asset *pipeline.Asset) error {
	if asset == nil {
		return fmt.Errorf("asset is required")
	}
	definitionPath := strings.TrimSpace(asset.DefinitionFile.Path)
	if definitionPath == "" {
		return fmt.Errorf("asset definition path is required")
	}

	existing, err := afero.ReadFile(fs, definitionPath)
	if err != nil {
		// A missing definition file is fine: we write a fresh one.
		existing = nil
	}

	merged, err := mergeYAMLAssetDefinition(existing, asset)
	if err != nil {
		return err
	}
	return afero.WriteFile(fs, definitionPath, merged, 0o644)
}

// mergeYAMLAssetDefinition overlays the asset's managed keys onto an existing
// YAML document, preserving unmanaged keys, ordering, and comments.
func mergeYAMLAssetDefinition(existing []byte, asset *pipeline.Asset) ([]byte, error) {
	canonical, err := canonicalManagedMapping(asset)
	if err != nil {
		return nil, err
	}

	var doc yaml.Node
	if strings.TrimSpace(string(existing)) != "" {
		if err := yaml.Unmarshal(existing, &doc); err != nil {
			return nil, fmt.Errorf("parse existing asset definition: %w", err)
		}
	}
	root := documentMappingNode(&doc)

	for _, key := range managedYAMLAssetKeys(asset) {
		if value, ok := canonical[key]; ok {
			setMappingValue(root, key, value)
		} else {
			deleteMappingKey(root, key)
		}
	}

	out := bytes.NewBuffer(nil)
	enc := yaml.NewEncoder(out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// canonicalManagedMapping renders the asset through bruin's own formatter and
// returns the managed top-level keys as YAML nodes, so columns, materialization
// and dependencies are written exactly as bruin would persist them.
func canonicalManagedMapping(asset *pipeline.Asset) (map[string]*yaml.Node, error) {
	formatted, err := asset.FormatContent()
	if err != nil {
		return nil, fmt.Errorf("format asset: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(formatted, &doc); err != nil {
		return nil, fmt.Errorf("parse formatted asset: %w", err)
	}
	mapping := documentMappingNode(&doc)
	managed := make(map[string]*yaml.Node)
	keys := managedYAMLAssetKeys(asset)
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key := mapping.Content[i].Value
		for _, candidate := range keys {
			if key == candidate {
				managed[key] = mapping.Content[i+1]
				break
			}
		}
	}
	return managed, nil
}

// stripYAMLTopLevelKey returns the document with a single top-level key removed,
// preserving ordering and comments of everything else. It lets callers hand the
// rest of an asset definition to a parser that can't tolerate that key's shape —
// notably bruin's stock reader, which models `parameters:` as map[string]string
// and errors on an API asset's nested request/response spec.
func stripYAMLTopLevelKey(content []byte, key string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, err
	}
	deleteMappingKey(documentMappingNode(&doc), key)

	out := bytes.NewBuffer(nil)
	enc := yaml.NewEncoder(out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// canonicalizeCreatedAPIAssetContent keeps the user-selected starter template
// but makes the semantic create request authoritative for identity. Without
// this overlay, a caller could submit kind=api with a different YAML type or
// connection than the server-reviewed creation profile.
func canonicalizeCreatedAPIAssetContent(content, connection string) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return "", fmt.Errorf("parse API asset definition: %w", err)
	}
	root := documentMappingNode(&doc)
	setMappingValue(root, "type", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: apiAssetType})
	if connection = strings.TrimSpace(connection); connection == "" {
		deleteMappingKey(root, "connection")
	} else {
		setMappingValue(root, "connection", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: connection})
	}

	out := bytes.NewBuffer(nil)
	enc := yaml.NewEncoder(out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return "", fmt.Errorf("encode API asset definition: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return out.String(), nil
}

// documentMappingNode returns the root mapping node of a YAML document, turning
// an empty/non-mapping document into an empty mapping so callers can edit it.
func documentMappingNode(doc *yaml.Node) *yaml.Node {
	if doc.Kind == 0 {
		doc.Kind = yaml.DocumentNode
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		mapping := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		doc.Content = []*yaml.Node{mapping}
	}
	return doc.Content[0]
}

// setMappingValue replaces the value for key (preserving its position) or
// appends the key when absent.
func setMappingValue(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value,
	)
}

// deleteMappingKey removes a key/value pair from a mapping if present.
func deleteMappingKey(mapping *yaml.Node, key string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}
