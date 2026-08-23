package service

import (
	"time"

	webexecution "renart/internal/web/execution"
)

// ExecutionTimeWindow remains the service-facade name while execution-domain
// consumers migrate incrementally.
type ExecutionTimeWindow = webexecution.TimeWindow

func ResolveExecutionTimeWindow(schedule, start, end string, now time.Time) (ExecutionTimeWindow, error) {
	return webexecution.ResolveTimeWindow(schedule, start, end, now)
}

func DefaultExecutionTimeWindow(schedule string, now time.Time) (ExecutionTimeWindow, error) {
	return webexecution.DefaultTimeWindow(schedule, now)
}
