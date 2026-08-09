package service

import (
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"
)

func TestPersistYAMLAssetDefinitionPreservesCanonicalLoadConfig(t *testing.T) {
	fs := afero.NewMemMapFs()
	definition := `name: example.thing
type: load
connection: duckdb-default
parameters:
  source_connection: postgres-default
  source_table: public.thing
custom_key: keep-me
`
	if err := afero.WriteFile(fs, "/p/assets/thing.asset.yml", []byte(definition), 0o644); err != nil {
		t.Fatal(err)
	}

	asset := &pipeline.Asset{
		Name:           "example.thing",
		URI:            "duckdb://warehouse/example/thing",
		Type:           "load",
		Connection:     "duckdb-default",
		DefinitionFile: pipeline.TaskDefinitionFile{Path: "/p/assets/thing.asset.yml"},
		ExecutableFile: pipeline.ExecutableFile{Path: "/p/assets/thing.asset.yml"},
		Parameters: pipeline.ParameterMap{
			loadParamSourceConnection: "postgres-default",
			loadParamSourceTable:      "public.thing",
		},
		Upstreams: []pipeline.Upstream{{Type: "asset", Value: "example.upstream", Mode: pipeline.UpstreamModeFull}},
		Materialization: pipeline.Materialization{
			Type:     pipeline.MaterializationTypeTable,
			Strategy: pipeline.MaterializationStrategyCreateReplace,
		},
	}

	if err := persistYAMLAssetDefinition(fs, asset); err != nil {
		t.Fatalf("persist: %v", err)
	}

	// The canonical single-file definition gets managed fields while preserving
	// unrelated content.
	def, _ := afero.ReadFile(fs, "/p/assets/thing.asset.yml")
	var parsed map[string]any
	if err := yaml.Unmarshal(def, &parsed); err != nil {
		t.Fatalf("definition not valid yaml: %v", err)
	}
	if parsed["custom_key"] != "keep-me" {
		t.Errorf("custom key dropped:\n%s", def)
	}
	if parsed["connection"] != "duckdb-default" {
		t.Errorf("connection not written:\n%s", def)
	}
	if _, ok := parsed["depends"]; !ok {
		t.Errorf("depends not written:\n%s", def)
	}
	if _, ok := parsed["materialization"]; !ok {
		t.Errorf("materialization not written:\n%s", def)
	}
	if parsed["uri"] != "duckdb://warehouse/example/thing" {
		t.Errorf("uri not written:\n%s", def)
	}
}

func TestMergeYAMLAssetDefinitionPreservesUnmanagedKeys(t *testing.T) {
	// An API asset whose file carries a nested `parameters` spec, a `run`
	// pointer and an unknown key alongside renart-managed columns.
	existing := `name: example.my_api_asset_2
type: api
run: my_api_asset_2.api.yml
parameters:
  url: https://example.com/api
  response:
    fields:
      - id
      - name
custom_key: keep-me
columns:
  - name: id
    type: INTEGER
`
	asset := &pipeline.Asset{
		Name:           "example.my_api_asset_2",
		Type:           "api",
		ExecutableFile: pipeline.ExecutableFile{Path: "/x/my_api_asset_2.asset.yml"},
		Columns: []pipeline.Column{
			{Name: "id", Type: "INTEGER"},
			{Name: "email", Type: "VARCHAR"},
		},
		Owner: "data@example.com",
		Meta:  pipeline.EmptyStringMap{"renart_v": "1"},
		CustomChecks: []pipeline.CustomCheck{{
			Name:  "no duplicates",
			Count: int64Pointer(0),
			Query: "select id from example.my_api_asset_2 group by id having count(*) > 1",
		}},
	}

	merged, err := mergeYAMLAssetDefinition([]byte(existing), asset)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal(merged, &parsed); err != nil {
		t.Fatalf("merged output not valid yaml: %v\n%s", err, merged)
	}

	// Unmanaged content survives untouched.
	if _, ok := parsed["parameters"]; !ok {
		t.Errorf("parameters spec was dropped:\n%s", merged)
	}
	if parsed["run"] != "my_api_asset_2.api.yml" {
		t.Errorf("run pointer was dropped, got %v:\n%s", parsed["run"], merged)
	}
	if parsed["custom_key"] != "keep-me" {
		t.Errorf("unknown key was dropped:\n%s", merged)
	}

	// Managed content reflects the asset.
	if parsed["owner"] != "data@example.com" {
		t.Errorf("owner not written: %v", parsed["owner"])
	}
	cols, ok := parsed["columns"].([]any)
	if !ok || len(cols) != 2 {
		t.Fatalf("expected 2 columns, got %v:\n%s", parsed["columns"], merged)
	}
	checks, ok := parsed["custom_checks"].([]any)
	if !ok || len(checks) != 1 {
		t.Fatalf("expected 1 custom check, got %v:\n%s", parsed["custom_checks"], merged)
	}
}

func int64Pointer(value int64) *int64 {
	return &value
}

func TestMergeYAMLAssetDefinitionManagesLoadFlatParameters(t *testing.T) {
	// A flat-parameter Load asset: renart owns `parameters`, so an edited
	// source_table must be written while unrelated keys survive.
	existing := `name: example.move_users
type: load
connection: duckdb_default
parameters:
  source_connection: postgres_prod
  source_table: public.users
custom_key: keep-me
`
	asset := &pipeline.Asset{
		Name:           "example.move_users",
		Type:           "load",
		Connection:     "warehouse",
		ExecutableFile: pipeline.ExecutableFile{Path: "/x/move_users.asset.yml"},
		Parameters: pipeline.ParameterMap{
			"source_connection": "postgres_prod",
			"source_table":      "public.customers", // edited
		},
	}

	merged, err := mergeYAMLAssetDefinition([]byte(existing), asset)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	var parsed struct {
		Parameters map[string]string `yaml:"parameters"`
		Connection string            `yaml:"connection"`
		CustomKey  string            `yaml:"custom_key"`
	}
	if err := yaml.Unmarshal(merged, &parsed); err != nil {
		t.Fatalf("invalid yaml: %v\n%s", err, merged)
	}
	if parsed.Parameters["source_table"] != "public.customers" {
		t.Errorf("source_table not updated: %q\n%s", parsed.Parameters["source_table"], merged)
	}
	if parsed.Connection != "warehouse" {
		t.Errorf("connection not updated: %q", parsed.Connection)
	}
	if parsed.CustomKey != "keep-me" {
		t.Errorf("unmanaged key dropped:\n%s", merged)
	}
}

func TestMergeYAMLAssetDefinitionClearsRemovedKeys(t *testing.T) {
	existing := `name: example.thing
type: api
uri: duckdb://warehouse/example/thing
owner: old@example.com
tags:
  - daily
depends:
  - upstream.one
`
	// The asset no longer carries owner/tags/depends.
	asset := &pipeline.Asset{
		Name:           "example.thing",
		Type:           "api",
		ExecutableFile: pipeline.ExecutableFile{Path: "/x/thing.asset.yml"},
	}

	merged, err := mergeYAMLAssetDefinition([]byte(existing), asset)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal(merged, &parsed); err != nil {
		t.Fatalf("invalid yaml: %v", err)
	}
	for _, key := range []string{"uri", "owner", "tags", "depends"} {
		if _, ok := parsed[key]; ok {
			t.Errorf("expected %q to be cleared:\n%s", key, merged)
		}
	}
	if !strings.Contains(string(merged), "name: example.thing") {
		t.Errorf("name missing:\n%s", merged)
	}
}
