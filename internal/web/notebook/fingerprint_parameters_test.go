package notebook

import "testing"

func TestRuntimeParameterValuesEnterCellFingerprint(t *testing.T) {
	nb := loadRunFixture(t, map[string]string{
		ManifestFileName: "version: 2\nid: 11111111-0000-0000-0000-000000000199\nparameters:\n  - id: region\n    type: text\n    default: eu\nblocks:\n  - cell: parameter01\n",
		"query.sql":      "/* @bruin\nid: parameter01\ntype: duckdb.sql\n@bruin */\nselect {{ parameter.region }} as region\n",
	})
	cell := nb.Cells[0]
	defaultFingerprint := CellFingerprint(nb, cell)
	if explicitDefault := CellFingerprintWithParameters(nb, cell, map[string]any{"region": "eu"}); explicitDefault != defaultFingerprint {
		t.Fatalf("explicit default changed fingerprint: default=%q explicit=%q", defaultFingerprint, explicitDefault)
	}
	if overridden := CellFingerprintWithParameters(nb, cell, map[string]any{"region": "us"}); overridden == defaultFingerprint {
		t.Fatal("runtime parameter override did not enter the cell fingerprint")
	}
}
