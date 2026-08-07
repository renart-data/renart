package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"testing"
	"time"

	bruinexecutor "github.com/bruin-data/bruin/pkg/executor"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

var directAssetLogANSI = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestDirectAssetLogWriterPrefixesEveryLogicalLine(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	asset := &pipeline.Asset{Name: "analytics.orders"}
	pl := &pipeline.Pipeline{Assets: []*pipeline.Asset{asset}}
	writer := newDirectAssetLogWriter(&output, pl, asset)
	writer.now = func() time.Time {
		return time.Date(2026, time.July, 14, 12, 34, 56, 0, time.UTC)
	}

	n, err := writer.Write([]byte("first"))
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	_, err = writer.Write([]byte(" line\n>> second\n\nthird"))
	require.NoError(t, err)
	require.NoError(t, writer.Flush())

	plain := directAssetLogANSI.ReplaceAllString(output.String(), "")
	assert.Equal(t, ""+
		"[12:34:56] [analytics.orders] >> first line\n"+
		"[12:34:56] [analytics.orders] >> second\n"+
		"[12:34:56] [analytics.orders] >> \n"+
		"[12:34:56] [analytics.orders] >> third", plain)
}

func TestDirectAssetLogWriterStreamsCompletePrefixedLineAsSingleChunk(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	var chunks [][]byte
	stream := &streamCaptureWriter{
		buffer: &output,
		onChunk: func(chunk []byte) {
			chunks = append(chunks, append([]byte(nil), chunk...))
		},
	}
	asset := &pipeline.Asset{Name: "example.api"}
	pl := &pipeline.Pipeline{Assets: []*pipeline.Asset{asset}}
	writer := newDirectAssetLogWriter(stream, pl, asset)
	writer.now = func() time.Time {
		return time.Date(2026, time.July, 14, 12, 34, 56, 0, time.UTC)
	}

	_, err := writer.Write([]byte("Fetched https://api.weather.gov/alerts?area=CA\n"))
	require.NoError(t, err)
	require.Len(t, chunks, 1)
	plain := directAssetLogANSI.ReplaceAll(chunks[0], nil)
	assert.Equal(t, "[12:34:56] [example.api] >> Fetched https://api.weather.gov/alerts?area=CA\n", string(plain))
}

func TestDirectAssetLogWriterRemovesNestedSlingTimestamps(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	asset := &pipeline.Asset{Name: "example.load"}
	pl := &pipeline.Pipeline{Assets: []*pipeline.Asset{asset}}
	writer := newDirectAssetLogWriter(&output, pl, asset)
	writer.now = func() time.Time {
		return time.Date(2026, time.July, 14, 12, 59, 52, 0, time.UTC)
	}

	_, err := writer.Write([]byte("\x1b[90m12:59PM\x1b[0m \x1b[31mWRN\x1b[0m could not parse DEBUGINFOD_URLS\n" +
		"\x1b[90m12:59PM\x1b[0m \x1b[32mINF\x1b[0m Sling CLI | https://slingdata.io\n" +
		"12:59PM application timestamp stays\n"))
	require.NoError(t, err)

	plain := directAssetLogANSI.ReplaceAllString(output.String(), "")
	assert.Equal(t, ""+
		"[12:59:52] [example.load] >> WRN could not parse DEBUGINFOD_URLS\n"+
		"[12:59:52] [example.load] >> INF Sling CLI | https://slingdata.io\n"+
		"[12:59:52] [example.load] >> 12:59PM application timestamp stays\n", plain)
}

func TestDirectAssetColorIndexFollowsPipelineOrder(t *testing.T) {
	t.Parallel()

	first := &pipeline.Asset{Name: "analytics.first"}
	second := &pipeline.Asset{Name: "analytics.second"}
	pl := &pipeline.Pipeline{Assets: []*pipeline.Asset{first, second}}

	assert.Equal(t, 0, directAssetColorIndex(pl, first))
	assert.Equal(t, 1, directAssetColorIndex(pl, second))
}

type testAssetLoggingOperator struct{}

func (testAssetLoggingOperator) Run(ctx context.Context, _ scheduler.TaskInstance) error {
	writer, ok := ctx.Value(bruinexecutor.KeyPrinter).(io.Writer)
	if !ok {
		return fmt.Errorf("task-local printer is missing")
	}
	_, err := fmt.Fprint(writer, "operator line one\noperator line two\n")
	return err
}

func TestRunDirectTaskUsesAssetLocalWriter(t *testing.T) {
	t.Parallel()

	asset := &pipeline.Asset{Name: "analytics.orders", Type: pipeline.AssetTypeDuckDBQuery}
	pl := &pipeline.Pipeline{Name: "analytics", Assets: []*pipeline.Asset{asset}}
	runScheduler := scheduler.NewScheduler(zap.NewNop().Sugar(), pl, "test-run")
	pending := runScheduler.GetTaskInstancesByStatus(scheduler.Pending)
	require.Len(t, pending, 1)

	seq := &bruinexecutor.Sequential{TaskTypeMap: map[pipeline.AssetType]bruinexecutor.Config{
		pipeline.AssetTypeDuckDBQuery: {
			scheduler.TaskInstanceTypeMain: testAssetLoggingOperator{},
		},
	}}
	var output bytes.Buffer
	printer := &streamCaptureWriter{buffer: &output}
	executor := &HybridBruinExecutor{}

	err := executor.runDirectTask(context.Background(), pl, pending[0], nil, nil, seq, nil, printer)
	require.NoError(t, err)
	plain := directAssetLogANSI.ReplaceAllString(output.String(), "")
	assert.Regexp(t, `\[\d{2}:\d{2}:\d{2}\] \[analytics\.orders\] >> operator line one\n`, plain)
	assert.Regexp(t, `\[\d{2}:\d{2}:\d{2}\] \[analytics\.orders\] >> operator line two\n`, plain)
}
