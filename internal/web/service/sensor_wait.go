package service

import (
	"context"
	"fmt"
	"io"
	"time"

	bruinexecutor "github.com/bruin-data/bruin/pkg/executor"
	"github.com/bruin-data/bruin/pkg/helpers"
	"github.com/bruin-data/bruin/pkg/scheduler"
)

// The pinned upstream sensors sleep without observing cancellation. Keep their
// warehouse-specific single probe, but own the wait here. Never leave a probe
// running in a detached goroutine after returning to the execution finalizer.
type cancellableSensorWait struct {
	probe bruinexecutor.Operator
}

func (s *cancellableSensorWait) Run(ctx context.Context, task scheduler.TaskInstance) error {
	timeout := helpers.GetSensorTimeout(task.GetAsset())
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	interval := time.Duration(helpers.GetPokeInterval(ctx, task.GetAsset())) * time.Second
	if interval <= 0 {
		interval = time.Millisecond
	}
	finished := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return fmt.Errorf("Sensor timed out after %s", timeout)
	}
	for {
		if waitCtx.Err() != nil {
			return finished()
		}
		err := s.probe.Run(waitCtx, task)
		if waitCtx.Err() != nil {
			return finished()
		}
		// This exact unready sentinel is shared by the pinned ANSI SQL, BigQuery,
		// Redshift and S3 one-shot operators. Query/authentication failures must
		// fail immediately, not become another polling attempt.
		if err == nil || err.Error() != "Sensor didn't return the expected result" {
			return err
		}
		if printer, ok := ctx.Value(bruinexecutor.KeyPrinter).(io.Writer); ok {
			_, _ = fmt.Fprintf(printer, "Sensor is not ready; checking again in %s.\n", interval)
		}
		timer := time.NewTimer(interval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return finished()
		case <-timer.C:
		}
	}
}
