package notebook

import (
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"renart/internal/web/presentation"
)

func TestValidateDatasetBackedControlResolvesCellIDOrName(t *testing.T) {
	nb := &Notebook{
		Version: ManifestVersionCurrent,
		Cells: []*Cell{{
			ID:    "source01",
			Asset: &pipeline.Asset{Name: "regions"},
		}},
		Parameters: []presentation.ParameterDefinition{{
			ID: "region", Type: presentation.ParameterTypeSelect, Default: "de",
			Options: &presentation.ParameterOptions{Dataset: "source01", ValueField: "code"},
		}},
	}

	validate(nb)
	if len(nb.Problems) != 0 {
		t.Fatalf("cell id should resolve: %v", nb.Problems)
	}

	nb.Problems = nil
	nb.Parameters[0].Options.Dataset = "regions"
	validate(nb)
	if len(nb.Problems) != 0 {
		t.Fatalf("legacy cell name should resolve: %v", nb.Problems)
	}

	nb.Problems = nil
	nb.Parameters[0].Options.Dataset = "missing"
	validate(nb)
	if len(nb.Problems) != 1 || !strings.Contains(nb.Problems[0], `option dataset "missing"`) {
		t.Fatalf("missing cell should be reported: %v", nb.Problems)
	}
}
