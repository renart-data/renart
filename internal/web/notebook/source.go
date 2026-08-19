package notebook

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"
)

const (
	SourceDefinitionVersionCurrent = 1
	SourceKindFile                 = "file"
	SourceKindHTTP                 = "http"
	SourceCellTypeFile             = "renart.file"
	SourceCellTypeHTTP             = "renart.http"
)

// SourceSnapshotConfig makes partial data an authored choice. A source is
// either complete or explicitly sampled; preview limits never become a local
// relation that downstream cells can mistake for complete data.
type SourceSnapshotConfig struct {
	Mode     string `yaml:"mode" json:"mode"`
	RowLimit int64  `yaml:"row_limit,omitempty" json:"row_limit,omitempty"`
}

// SourceHTTPRequest intentionally matches the request document accepted by
// Renart's native HTTP asset implementation. The service executes the raw
// source definition through that existing validator/fetcher, so this summary
// type is not a second HTTP runtime contract.
type SourceHTTPRequest struct {
	URL     string            `yaml:"url" json:"url"`
	Method  string            `yaml:"method,omitempty" json:"method,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	Params  map[string]any    `yaml:"params,omitempty" json:"params,omitempty"`
	Body    any               `yaml:"body,omitempty" json:"body,omitempty"`
}

type SourceHTTPResponse struct {
	RecordsPath string            `yaml:"records_path,omitempty" json:"records_path,omitempty"`
	Fields      map[string]string `yaml:"fields,omitempty" json:"fields,omitempty"`
}

// SourceDefinition is the Renart-owned file format for non-SQL notebook data
// inputs. Unknown HTTP keys remain in Cell.Raw and are consumed by the native
// API parser; the typed fields below power validation, UI summaries, and
// deterministic creation without weakening that richer existing contract.
type SourceDefinition struct {
	Version    int                  `yaml:"version,omitempty" json:"version"`
	ID         string               `yaml:"id" json:"id"`
	Kind       string               `yaml:"kind" json:"kind"`
	Connection string               `yaml:"connection,omitempty" json:"connection,omitempty"`
	URI        string               `yaml:"uri,omitempty" json:"uri,omitempty"`
	Format     string               `yaml:"format,omitempty" json:"format,omitempty"`
	Request    SourceHTTPRequest    `yaml:"request,omitempty" json:"request,omitempty"`
	Response   SourceHTTPResponse   `yaml:"response,omitempty" json:"response,omitempty"`
	Snapshot   SourceSnapshotConfig `yaml:"snapshot" json:"snapshot"`
	Columns    []pipeline.Column    `yaml:"columns,omitempty" json:"-"`
}

func ParseSourceDefinition(content []byte) (*SourceDefinition, error) {
	var definition SourceDefinition
	if err := yaml.Unmarshal(content, &definition); err != nil {
		return nil, fmt.Errorf("parse notebook source: %w", err)
	}
	definition.Version = normalizedSourceVersion(definition.Version)
	definition.ID = strings.TrimSpace(definition.ID)
	definition.Kind = strings.ToLower(strings.TrimSpace(definition.Kind))
	definition.Connection = strings.TrimSpace(definition.Connection)
	definition.URI = strings.TrimSpace(definition.URI)
	definition.Format = strings.ToLower(strings.TrimSpace(definition.Format))
	definition.Request.URL = strings.TrimSpace(definition.Request.URL)
	definition.Request.Method = strings.ToUpper(strings.TrimSpace(definition.Request.Method))
	definition.Response.RecordsPath = strings.TrimSpace(definition.Response.RecordsPath)
	definition.Snapshot.Mode = strings.ToLower(strings.TrimSpace(definition.Snapshot.Mode))
	if definition.Snapshot.Mode == "" {
		definition.Snapshot.Mode = SnapshotModeFull
	}
	if err := ValidateSourceDefinition(&definition); err != nil {
		return nil, err
	}
	return &definition, nil
}

func ValidateSourceDefinition(definition *SourceDefinition) error {
	if definition == nil {
		return fmt.Errorf("notebook source definition is required")
	}
	if definition.Version != SourceDefinitionVersionCurrent {
		return fmt.Errorf("unsupported notebook source version %d", definition.Version)
	}
	switch definition.Kind {
	case SourceKindFile:
		if definition.URI == "" {
			return fmt.Errorf("file source uri is required")
		}
	case SourceKindHTTP:
		if definition.Request.URL == "" {
			return fmt.Errorf("HTTP source request.url is required")
		}
		if definition.Connection != "" {
			return fmt.Errorf("HTTP notebook sources do not use an object-storage connection")
		}
	default:
		return fmt.Errorf("notebook source kind must be file or http")
	}
	if definition.Snapshot.Mode != SnapshotModeFull && definition.Snapshot.Mode != SnapshotModeSample {
		return fmt.Errorf("snapshot mode must be full or sample")
	}
	if definition.Snapshot.Mode == SnapshotModeSample && definition.Snapshot.RowLimit <= 0 {
		return fmt.Errorf("sample snapshots require a positive row limit")
	}
	if definition.Snapshot.Mode == SnapshotModeFull {
		definition.Snapshot.RowLimit = 0
	}
	return nil
}

func MarshalSourceDefinition(definition SourceDefinition) ([]byte, error) {
	definition.Version = normalizedSourceVersion(definition.Version)
	if err := ValidateSourceDefinition(&definition); err != nil {
		return nil, err
	}
	content, err := yaml.Marshal(definition)
	if err != nil {
		return nil, fmt.Errorf("encode notebook source: %w", err)
	}
	return content, nil
}

func normalizedSourceVersion(version int) int {
	if version == 0 {
		return SourceDefinitionVersionCurrent
	}
	return version
}

// EnsureSourceID is the YAML-file equivalent of EnsureCellID. Existing bytes
// are untouched when an ID is already present; a legacy hand-authored source
// without one is normalized once so manifest identity remains durable.
func EnsureSourceID(filesystem afero.Fs, path string) (id string, generated bool, err error) {
	content, err := afero.ReadFile(filesystem, path)
	if err != nil {
		return "", false, err
	}
	definition, err := ParseSourceDefinition(content)
	if err != nil {
		return "", false, err
	}
	if definition.ID != "" {
		return definition.ID, false, nil
	}
	definition.ID = NewBlockID("source")
	normalized, err := MarshalSourceDefinition(*definition)
	if err != nil {
		return "", false, err
	}
	if err := afero.WriteFile(filesystem, path, normalized, 0o644); err != nil {
		return "", false, err
	}
	return definition.ID, true, nil
}

func IsSourcePath(path string) bool {
	lower := strings.ToLower(strings.TrimSpace(path))
	return strings.HasSuffix(lower, ".source.yml") || strings.HasSuffix(lower, ".source.yaml")
}

func IsSourceCell(cell *Cell) bool {
	return cell != nil && cell.Source != nil
}

func SourceCellName(filename string) string {
	lower := strings.ToLower(filename)
	for _, suffix := range []string{".source.yaml", ".source.yml"} {
		if strings.HasSuffix(lower, suffix) {
			return filename[:len(filename)-len(suffix)]
		}
	}
	return strings.TrimSuffix(filename, filepath.Ext(filename))
}

func SourceCellType(kind string) pipeline.AssetType {
	if strings.EqualFold(strings.TrimSpace(kind), SourceKindHTTP) {
		return pipeline.AssetType(SourceCellTypeHTTP)
	}
	return pipeline.AssetType(SourceCellTypeFile)
}

// NormalizeSourceDefinition forces the durable ID and produces deterministic
// YAML for semantic create/update operations.
func NormalizeSourceDefinition(content []byte, sourceID string) ([]byte, *SourceDefinition, error) {
	definition, err := ParseSourceDefinition(content)
	if err != nil {
		return nil, nil, err
	}
	definition.ID = strings.TrimSpace(sourceID)
	if definition.ID == "" {
		return nil, nil, fmt.Errorf("notebook source id is required")
	}
	normalized, err := MarshalSourceDefinition(*definition)
	if err != nil {
		return nil, nil, err
	}
	return bytes.TrimSpace(normalized), definition, nil
}
