package service

import (
	"context"
	"errors"
	"testing"
	"time"

	bruinexecutor "github.com/bruin-data/bruin/pkg/executor"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sensorProbeFunc func(context.Context, scheduler.TaskInstance) error

func (f sensorProbeFunc) Run(ctx context.Context, task scheduler.TaskInstance) error {
	return f(ctx, task)
}

type sensorWaitWriter func([]byte) (int, error)

func (f sensorWaitWriter) Write(value []byte) (int, error) { return f(value) }

func TestSensorWaitCancellationInterruptsLongPollingInterval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = context.WithValue(ctx, bruinexecutor.KeyPrinter, sensorWaitWriter(func(value []byte) (int, error) {
		cancel() // A cancellation delivered after the probe, as polling enters its wait.
		return len(value), nil
	}))
	attempts := 0
	op := &cancellableSensorWait{probe: sensorProbeFunc(func(context.Context, scheduler.TaskInstance) error {
		attempts++
		return errors.New("Sensor didn't return the expected result")
	})}
	task := &scheduler.AssetInstance{Asset: &pipeline.Asset{Parameters: pipeline.ParameterMap{"poke_interval": "3600", "timeout": "2h"}}}
	started := time.Now()
	require.ErrorIs(t, op.Run(ctx, task), context.Canceled)
	assert.Less(t, time.Since(started), time.Second)
	assert.Equal(t, 1, attempts)
}

func TestSensorWaitPreservesProbeErrorsSuccessAndTimeout(t *testing.T) {
	for _, scenario := range []string{"ready", "query-error", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			attempts := 0
			queryErr := errors.New("permission denied")
			op := &cancellableSensorWait{probe: sensorProbeFunc(func(context.Context, scheduler.TaskInstance) error {
				attempts++
				if scenario == "query-error" {
					return queryErr
				}
				if scenario == "ready" && attempts == 2 {
					return nil
				}
				return errors.New("Sensor didn't return the expected result")
			})}
			task := &scheduler.AssetInstance{Asset: &pipeline.Asset{Parameters: pipeline.ParameterMap{"poke_interval": "0", "timeout": "20ms"}}}
			if scenario != "timeout" {
				task.Asset.Parameters["timeout"] = "10s"
			}
			err := op.Run(context.Background(), task)
			switch scenario {
			case "ready":
				require.NoError(t, err)
				assert.Equal(t, 2, attempts)
			case "query-error":
				require.ErrorIs(t, err, queryErr)
				assert.Equal(t, 1, attempts)
			case "timeout":
				require.ErrorContains(t, err, "Sensor timed out")
				assert.NotErrorIs(t, err, context.Canceled)
			}
		})
	}
}

func TestSensorWaitRegistryOwnsOnlyWaitMode(t *testing.T) {
	for _, mode := range []string{sensorModeOnce, sensorModeSkip, sensorModeWait} {
		executors, err := buildDirectMainExecutors(&stubConnectionManager{}, nil, nil, &pipeline.Pipeline{}, nil, nil, nil, nil, "", false, false, mode)
		require.NoError(t, err)
		for _, capability := range assetAuthoringCapabilities() {
			if capability.Kind != "sensor" {
				continue
			}
			_, wrapped := executors[pipeline.AssetType(capability.Type)][scheduler.TaskInstanceTypeMain].(*cancellableSensorWait)
			assert.Equal(t, mode == sensorModeWait, wrapped, "%s %s", mode, capability.Type)
		}
	}
}
