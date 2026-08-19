package notebook

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// TabularColumn is the transport-neutral schema of a notebook relation. Type
// preserves the source physical type instead of inferring it from row values.
type TabularColumn struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable *bool  `json:"nullable,omitempty"`
}

// SnapshotProvenance explains the exact origin and policy of a local notebook
// snapshot without carrying connection credentials.
type SnapshotProvenance struct {
	SourceKind            string    `json:"source_kind"`
	Environment           string    `json:"environment,omitempty"`
	Connection            string    `json:"connection,omitempty"`
	DefinitionFingerprint string    `json:"definition_fingerprint"`
	SourceFingerprint     string    `json:"source_fingerprint,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
	Warnings              []string  `json:"warnings,omitempty"`
}

// TabularArtifact is a typed, bounded transfer result that has not yet been
// published under its durable notebook relation name.
type TabularArtifact struct {
	Path       string             `json:"path"`
	Schema     []TabularColumn    `json:"schema"`
	RowCount   int64              `json:"row_count"`
	ByteCount  int64              `json:"byte_count"`
	Complete   bool               `json:"complete"`
	Sampled    bool               `json:"sampled"`
	Provenance SnapshotProvenance `json:"provenance"`
	Cleanup    func() error       `json:"-"`
}

// ValidateForPublication rejects ambiguous partial data. A relation is either
// complete or explicitly sampled; accidental truncation is never publishable.
func (artifact TabularArtifact) ValidateForPublication() error {
	if artifact.Complete == artifact.Sampled {
		return fmt.Errorf("tabular artifact must be exactly one of complete or sampled")
	}
	if len(artifact.Schema) == 0 {
		return fmt.Errorf("tabular artifact has no schema")
	}
	if artifact.RowCount < 0 || artifact.ByteCount < 0 {
		return fmt.Errorf("tabular artifact sizes cannot be negative")
	}
	for _, column := range artifact.Schema {
		if strings.TrimSpace(column.Name) == "" || strings.TrimSpace(column.Type) == "" {
			return fmt.Errorf("tabular artifact schema requires column names and physical types")
		}
	}
	return nil
}

type AnalyzeBlockInput struct {
	Notebook    *Notebook
	Cell        *Cell
	Environment string
}

type BlockAnalysis struct {
	Kind       string          `json:"kind"`
	Dialect    string          `json:"dialect,omitempty"`
	Connection string          `json:"connection,omitempty"`
	Schema     []TabularColumn `json:"schema,omitempty"`
	Problems   []string        `json:"problems,omitempty"`
}

type ExecuteBlockInput struct {
	Notebook        *Notebook
	Cell            *Cell
	Environment     string
	SQL             string
	Refresh         bool
	ParameterValues map[string]any
}

type BlockOutput struct {
	Relation string           `json:"relation,omitempty"`
	Artifact *TabularArtifact `json:"artifact,omitempty"`
	Logs     string           `json:"logs,omitempty"`
	Cleanup  func() error     `json:"-"`
}

// NotebookBlockExecutor analyzes and runs one block role. It does not choose
// DAG order, publish session objects, or write authored notebook files.
type NotebookBlockExecutor interface {
	Analyze(ctx context.Context, input AnalyzeBlockInput) (BlockAnalysis, error)
	Execute(ctx context.Context, input ExecuteBlockInput) (BlockOutput, error)
}

type SnapshotRequest struct {
	NotebookID            string
	BlockID               string
	Environment           string
	Connection            string
	Query                 string
	DefinitionFingerprint string
	Mode                  string // full | sample
	RowLimit              int64
}

// NotebookTransferService creates a typed staging artifact. The runner owns
// validation and the atomic swap into the live notebook session.
type NotebookTransferService interface {
	Snapshot(ctx context.Context, request SnapshotRequest) (TabularArtifact, error)
}
