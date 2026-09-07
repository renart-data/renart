package events

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHubDisconnectsSlowClientWithoutLosingHealthyClientEvents(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	slow, healthy := hub.Subscribe(), hub.Subscribe()
	defer hub.Unsubscribe(slow)
	defer hub.Unsubscribe(healthy)
	for value := 0; value < 20; value++ {
		hub.PublishImmediate(value)
		select {
		case payload := <-healthy:
			var got int
			require.NoError(t, json.Unmarshal(payload, &got))
			require.Equal(t, value, got)
		default:
			t.Fatal("healthy client lost an event")
		}
	}
	require.Equal(t, 1, hub.ClientCount(), "overflow must disconnect, not silently desynchronize")
	for value := 0; value < 16; value++ {
		payload, ok := <-slow
		require.True(t, ok)
		var got int
		require.NoError(t, json.Unmarshal(payload, &got))
		require.Equal(t, value, got)
	}
	select {
	case _, ok := <-slow:
		require.False(t, ok, "buffered prefix must end in EOF so EventSource reconnects")
	default:
		t.Fatal("overflowed subscription remained open")
	}
	require.Equal(t, uint64(1), hub.Stats().Dropped)
}

func TestHubUnsubscribeIsIdempotentWhilePublishing(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	client := hub.Subscribe()
	var publisher sync.WaitGroup
	publisher.Add(1)
	go func() {
		defer publisher.Done()
		for value := 0; value < 100; value++ {
			hub.PublishImmediate(value)
		}
	}()
	require.NotPanics(t, func() {
		hub.Unsubscribe(client)
		hub.Unsubscribe(client)
	})
	publisher.Wait()
	require.Zero(t, hub.ClientCount())
}

func TestHubStatsRecordImmediateFanoutAndDrops(t *testing.T) {
	t.Parallel()

	hub := NewHub()
	client := hub.Subscribe()
	defer hub.Unsubscribe(client)

	for value := 0; value < 17; value++ {
		hub.PublishImmediate(map[string]int{"value": value})
	}

	stats := hub.Stats()
	assert.Equal(t, 0, stats.Clients)
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
