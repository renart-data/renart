package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	root := flag.String("root", ".", "repository root")
	output := flag.String("output", "web/lib/generated/api-types.ts", "output path relative to the repository root")
	check := flag.Bool("check", false, "fail when the generated output differs instead of writing it")
	flag.Parse()

	absRoot, err := filepath.Abs(*root)
	if err != nil {
		fatalf("resolve repository root: %v", err)
	}
	generated, err := generateAPITypeScript(absRoot)
	if err != nil {
		fatalf("generate API types: %v", err)
	}
	outputPath := *output
	if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(absRoot, outputPath)
	}
	if *check {
		current, readErr := os.ReadFile(outputPath)
		if readErr != nil {
			fatalf("read generated API types: %v", readErr)
		}
		if string(current) != generated {
			fatalf("generated API types are stale; run `corepack pnpm --dir web generate:api-types`")
		}
		return
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		fatalf("create generated API type directory: %v", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(outputPath), ".api-types-*.ts")
	if err != nil {
		fatalf("create temporary API type output: %v", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.WriteString(generated); err != nil {
		_ = temporary.Close()
		fatalf("write generated API types: %v", err)
	}
	if err := temporary.Close(); err != nil {
		fatalf("close generated API types: %v", err)
	}
	if err := os.Rename(temporaryPath, outputPath); err != nil {
		fatalf("replace generated API types: %v", err)
	}
}

func fatalf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "apitypes: "+format+"\n", args...)
	os.Exit(1)
}
