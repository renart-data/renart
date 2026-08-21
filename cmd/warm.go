package cmd

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"

	"renart/internal/web/pyintelligence"
)

// WarmCache compiles the embedded ty Python-linter WASM module into the shared
// on-disk wazero cache, then exits. SQL parsing and formatting are native Go and
// require no warmup.
func WarmCache() *cli.Command {
	return &cli.Command{
		Name:  "warm-cache",
		Usage: "compile the embedded Python intelligence module into the on-disk cache, then exit",
		Action: func(ctx context.Context, _ *cli.Command) error {
			return warmWasmCaches(ctx)
		},
	}
}

func warmWasmCaches(ctx context.Context) error {
	// ty: any call forces the runtime to compile and cache before it runs.
	if _, err := pyintelligence.Check(ctx, pyintelligence.Request{
		Path:    "warm.py",
		Content: "x = 1\n",
	}); err != nil {
		return fmt.Errorf("warm ty wasm: %w", err)
	}
	return nil
}
