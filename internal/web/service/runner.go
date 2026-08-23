// Package service provides the business logic layer for the Bruin web server.
package service

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"time"
)

type RunAssetRequest struct {
	AssetPath   string
	Environment string
	SensorMode  string
	StartDate   string
	EndDate     string
	AssetEvent  func(ExecutionAssetEvent) error
	FullRefresh bool
	// BeforeTargetWrite is called by operators whose successful main task does
	// not itself prove that a declared output was written. It must return before
	// the physical write begins so target fencing remains fail-closed.
	BeforeTargetWrite func(assetName string) error
	// OnTargetsResolved must succeed synchronously after effective execution
	// context and target resolution but before the first task starts.
	OnTargetsResolved func(ExecutionTargetSnapshot) error
}

type QueryAssetRequest struct {
	AssetPath   string
	Limit       string
	Environment string
	ConfigFile  string
	Output      string
	StartDate   string
	EndDate     string
}

type QueryConnectionRequest struct {
	ConnectionName string
	Query          string
	Environment    string
	Output         string
	LogicalSchema  bool
}

type FormatAssetRequest struct {
	AssetPath   string
	UseSQLFluff bool
}

type PatchRequest struct {
	Operation  string
	TargetPath string
}

type ImportDatabaseRequest struct {
	PipelinePath   string
	ConnectionName string
	// PreferredAssetName lets a reviewed single-table import preserve the exact
	// warehouse-qualified identity when that name is valid for the connection's
	// Bruin platform. The generated file path remains schema/table based.
	PreferredAssetName string
	Schema             string
	Schemas            []string
	Tables             []string
	DisableColumns     bool
	PreviewOnly        bool
	RejectExisting     bool
	Environment        string
	ConfigFilePath     string
}

// BruinCommandExecutor executes Bruin operations through typed methods.
// Implementations can shell out to the Bruin CLI today and later call the
// corresponding Go APIs directly.
type BruinCommandExecutor interface {
	RunAsset(ctx context.Context, req RunAssetRequest, onChunk func([]byte)) ([]byte, error)
	RunPipeline(ctx context.Context, req RunPipelineRequest, onChunk func([]byte)) ([]byte, error)
	QueryAsset(ctx context.Context, req QueryAssetRequest) ([]byte, error)
	QueryConnection(ctx context.Context, req QueryConnectionRequest) ([]byte, error)
	FormatAsset(ctx context.Context, req FormatAssetRequest) ([]byte, error)
	ApplyPatch(ctx context.Context, req PatchRequest) ([]byte, error)
	ImportDatabase(ctx context.Context, req ImportDatabaseRequest) ([]byte, error)
	RunWithRetry(ctx context.Context, req QueryAssetRequest, retries int, initialDelay time.Duration) ([]byte, error, int)
}

func IsDuckDBLockError(err error, output []byte) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error() + "\n" + string(output))
	return strings.Contains(message, "could not set lock on file") ||
		strings.Contains(message, "conflicting lock is held")
}

type streamCaptureWriter struct {
	mu      sync.Mutex
	buffer  *bytes.Buffer
	onChunk func([]byte)
}

func (w *streamCaptureWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, err := w.buffer.Write(p); err != nil {
		return 0, err
	}

	if w.onChunk != nil {
		chunk := append([]byte(nil), p...)
		w.onChunk(chunk)
	}

	return len(p), nil
}

var _ io.Writer = (*streamCaptureWriter)(nil)
