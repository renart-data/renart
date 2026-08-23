package execution

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultTimeWindowDaily(t *testing.T) {
	now := time.Date(2026, 5, 28, 13, 14, 0, 0, time.UTC)
	window, err := DefaultTimeWindow("@daily", now)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC), window.Start)
	assert.Equal(t, time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC), window.End)
}

func TestDefaultTimeWindowHourly(t *testing.T) {
	now := time.Date(2026, 5, 28, 13, 14, 0, 0, time.UTC)
	window, err := DefaultTimeWindow("@hourly", now)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC), window.Start)
	assert.Equal(t, time.Date(2026, 5, 28, 13, 0, 0, 0, time.UTC), window.End)
}

func TestDefaultTimeWindowStandardCron(t *testing.T) {
	now := time.Date(2026, 5, 28, 13, 14, 0, 0, time.UTC)
	window, err := DefaultTimeWindow("15 */6 * * *", now)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 5, 28, 6, 15, 0, 0, time.UTC), window.Start)
	assert.Equal(t, time.Date(2026, 5, 28, 12, 15, 0, 0, time.UTC), window.End)
}

func TestDefaultTimeWindowStandardCronWithCommaHours(t *testing.T) {
	now := time.Date(2026, 5, 28, 13, 14, 0, 0, time.UTC)
	window, err := DefaultTimeWindow("0 0,12 * * *", now)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC), window.Start)
	assert.Equal(t, time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC), window.End)
}

func TestResolveTimeWindowExplicit(t *testing.T) {
	window, err := ResolveTimeWindow(
		"@daily",
		"2026-05-26T00:00:00Z",
		"2026-05-27T00:00:00Z",
		time.Date(2026, 5, 28, 13, 14, 0, 0, time.UTC),
	)
	require.NoError(t, err)
	assert.Equal(t, "2026-05-26T00:00:00Z", window.StartRFC3339())
	assert.Equal(t, "2026-05-27T00:00:00Z", window.EndRFC3339())
}

func TestResolveTimeWindowPreservesFractionalSeconds(t *testing.T) {
	window, err := ResolveTimeWindow(
		"@daily",
		"2026-05-26T00:00:00.123456789+02:00",
		"2026-05-26T00:00:01.987654321+02:00",
		time.Date(2026, 5, 28, 13, 14, 0, 0, time.UTC),
	)
	require.NoError(t, err)
	assert.Equal(t, "2026-05-25T22:00:00.123456789Z", window.StartRFC3339())
	assert.Equal(t, "2026-05-25T22:00:01.987654321Z", window.EndRFC3339())
}

func TestResolveTimeWindowRejectsIncompleteAndReversedExplicitWindows(t *testing.T) {
	now := time.Date(2026, 5, 28, 13, 14, 0, 0, time.UTC)
	_, err := ResolveTimeWindow("@daily", "2026-05-26T00:00:00Z", "", now)
	require.ErrorContains(t, err, "both start and end")

	_, err = ResolveTimeWindow("@daily", "2026-05-27T00:00:00Z", "2026-05-26T00:00:00Z", now)
	require.ErrorContains(t, err, "end time must be after start time")
}
