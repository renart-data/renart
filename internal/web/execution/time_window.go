// Package execution owns shared execution-domain contracts and application
// logic below the broad service compatibility facade.
package execution

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// TimeWindow is the exact half-open interval supplied to render, planning,
// type-check, and execution paths.
type TimeWindow struct {
	Start time.Time
	End   time.Time
}

func (w TimeWindow) IsZero() bool {
	return w.Start.IsZero() || w.End.IsZero()
}

func (w TimeWindow) StartRFC3339() string {
	if w.Start.IsZero() {
		return ""
	}
	return w.Start.UTC().Format(time.RFC3339Nano)
}

func (w TimeWindow) EndRFC3339() string {
	if w.End.IsZero() {
		return ""
	}
	return w.End.UTC().Format(time.RFC3339Nano)
}

// ResolveTimeWindow validates an explicit interval or derives the previous
// completed interval from the asset/pipeline schedule.
func ResolveTimeWindow(schedule, start, end string, now time.Time) (TimeWindow, error) {
	start = strings.TrimSpace(start)
	end = strings.TrimSpace(end)
	if start != "" || end != "" {
		if start == "" || end == "" {
			return TimeWindow{}, fmt.Errorf("both start and end must be provided")
		}
		parsedStart, err := time.Parse(time.RFC3339, start)
		if err != nil {
			return TimeWindow{}, fmt.Errorf("invalid start time: %w", err)
		}
		parsedEnd, err := time.Parse(time.RFC3339, end)
		if err != nil {
			return TimeWindow{}, fmt.Errorf("invalid end time: %w", err)
		}
		if !parsedEnd.After(parsedStart) {
			return TimeWindow{}, fmt.Errorf("end time must be after start time")
		}
		return TimeWindow{Start: parsedStart.UTC(), End: parsedEnd.UTC()}, nil
	}

	return DefaultTimeWindow(schedule, now)
}

// DefaultTimeWindow resolves the previous completed interval for a schedule.
func DefaultTimeWindow(schedule string, now time.Time) (TimeWindow, error) {
	now = now.UTC()
	schedule = normalizeSchedule(schedule)
	switch schedule {
	case "@hourly":
		end := now.Truncate(time.Hour)
		return TimeWindow{Start: end.Add(-time.Hour), End: end}, nil
	case "@daily":
		end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		return TimeWindow{Start: end.AddDate(0, 0, -1), End: end}, nil
	}

	parsed, err := cron.ParseStandard(schedule)
	if err != nil {
		return DefaultTimeWindow("@daily", now)
	}

	lookback := now.AddDate(-5, 0, 0)
	previous := parsed.Next(lookback)
	if previous.After(now) {
		return DefaultTimeWindow("@daily", now)
	}

	var beforePrevious time.Time
	for i := 0; i < 200000; i++ {
		next := parsed.Next(previous)
		if next.After(now) {
			if beforePrevious.IsZero() {
				return DefaultTimeWindow("@daily", now)
			}
			return TimeWindow{Start: beforePrevious.UTC(), End: previous.UTC()}, nil
		}
		beforePrevious = previous
		previous = next
	}

	return TimeWindow{}, fmt.Errorf("schedule %q is too frequent to resolve", schedule)
}

func normalizeSchedule(schedule string) string {
	schedule = strings.TrimSpace(strings.ToLower(schedule))
	switch schedule {
	case "", "daily":
		return "@daily"
	case "hourly":
		return "@hourly"
	default:
		return schedule
	}
}
