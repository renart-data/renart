package events

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHubStatsRecordImmediateFanoutAndDrops(t *testing.T) {
	t.Parallel()

	hub := NewHub()
	client := hub.Subscribe()
	defer hub.Unsubscribe(client)

	for value := 0; value < 17; value++ {
		hub.PublishImmediate(map[string]int{"value": value})
	}

	stats := hub.Stats()
	assert.Equal(t, 1, stats.Clients)
	assert.False(t, stats.Pending)
	assert.Equal(t, uint64(17), stats.Published)
	assert.Equal(t, uint64(17), stats.Broadcasts)
	assert.Equal(t, uint64(16), stats.Delivered)
	assert.Equal(t, uint64(1), stats.Dropped)
	assert.Greater(t, stats.PayloadBytes, stats.LastPayloadBytes)
	assert.Greater(t, stats.LastPayloadBytes, uint64(0))
}

func TestHubStatsRecordDebounceCoalescing(t *testing.T) {
	t.Parallel()

	hub := NewDebouncedHub(10 * time.Millisecond)
	client := hub.Subscribe()
	defer hub.Unsubscribe(client)

	hub.Publish(map[string]int{"value": 1})
	hub.Publish(map[string]int{"value": 2})

	queued := hub.Stats()
	assert.True(t, queued.Pending)
	assert.Equal(t, uint64(2), queued.Published)
	assert.Equal(t, uint64(1), queued.Coalesced)

	select {
	case payload := <-client:
		var event map[string]int
		require.NoError(t, json.Unmarshal(payload, &event))
		assert.Equal(t, 2, event["value"])
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for debounced event")
	}

	flushed := hub.Stats()
	assert.False(t, flushed.Pending)
	assert.Equal(t, uint64(1), flushed.Broadcasts)
	assert.Equal(t, uint64(1), flushed.Delivered)
}

func TestHubStatsRecordMarshalFailures(t *testing.T) {
	t.Parallel()

	hub := NewHub()
	hub.Publish(make(chan int))

	stats := hub.Stats()
	assert.Equal(t, uint64(0), stats.Published)
	assert.Equal(t, uint64(1), stats.MarshalFailures)
	assert.Equal(t, uint64(0), stats.Broadcasts)
}
