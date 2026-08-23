// Package events provides SSE pub/sub functionality for the Bruin web server.
package events

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

// HubStats is a monotonic view of SSE fan-out. Clients and Pending are gauges;
// the remaining fields are cumulative counters for the lifetime of the hub.
type HubStats struct {
	Clients          int
	Pending          bool
	Published        uint64
	MarshalFailures  uint64
	Broadcasts       uint64
	Coalesced        uint64
	Delivered        uint64
	Dropped          uint64
	PayloadBytes     uint64
	LastPayloadBytes uint64
}

// Hub manages SSE client subscriptions and message broadcasting.
// It supports optional event coalescing: rapid publishes within a debounce
// window are merged so only the latest payload is sent.
type Hub struct {
	mu      sync.RWMutex
	clients map[chan []byte]struct{}

	published        atomic.Uint64
	marshalFailures  atomic.Uint64
	broadcasts       atomic.Uint64
	coalesced        atomic.Uint64
	delivered        atomic.Uint64
	dropped          atomic.Uint64
	payloadBytes     atomic.Uint64
	lastPayloadBytes atomic.Uint64

	// Debounce support: pending holds the latest event during a debounce
	// window. When debounce <= 0, events are published immediately.
	debounceMu sync.Mutex
	debounce   time.Duration
	pending    []byte
	timer      *time.Timer
}

// NewHub creates a new SSE hub with no debounce (immediate publishing).
func NewHub() *Hub {
	return &Hub{clients: make(map[chan []byte]struct{})}
}

// NewDebouncedHub creates an SSE hub that coalesces rapid publishes.
// Events published within the debounce window are merged: only the
// latest event is sent once the window expires.
func NewDebouncedHub(debounce time.Duration) *Hub {
	return &Hub{
		clients:  make(map[chan []byte]struct{}),
		debounce: debounce,
	}
}

// Subscribe returns a channel that receives published events.
// The caller must call Unsubscribe when done to prevent leaks.
func (h *Hub) Subscribe() chan []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan []byte, 16)
	h.clients[ch] = struct{}{}
	return ch
}

// Unsubscribe removes a client channel from the hub and closes it.
func (h *Hub) Unsubscribe(ch chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, ch)
	close(ch)
}

// Publish broadcasts a message to all subscribed clients.
// If the hub was created with NewDebouncedHub, the message is coalesced:
// only the latest message within the debounce window is actually sent.
func (h *Hub) Publish(v any) {
	payload, err := json.Marshal(v)
	if err != nil {
		h.marshalFailures.Add(1)
		return
	}
	h.recordPayload(payload)

	if h.debounce <= 0 {
		h.broadcast(payload)
		return
	}

	h.debounceMu.Lock()
	defer h.debounceMu.Unlock()

	if h.pending != nil {
		h.coalesced.Add(1)
	}
	h.pending = payload

	if h.timer != nil {
		h.timer.Stop()
	}
	h.timer = time.AfterFunc(h.debounce, h.flush)
}

// PublishImmediate broadcasts a message immediately, bypassing any debounce.
// Use for events that must be delivered without delay (e.g. handler-triggered).
func (h *Hub) PublishImmediate(v any) {
	payload, err := json.Marshal(v)
	if err != nil {
		h.marshalFailures.Add(1)
		return
	}
	h.recordPayload(payload)
	h.broadcast(payload)
}

func (h *Hub) recordPayload(payload []byte) {
	size := uint64(len(payload))
	h.published.Add(1)
	h.payloadBytes.Add(size)
	h.lastPayloadBytes.Store(size)
}

func (h *Hub) flush() {
	h.debounceMu.Lock()
	payload := h.pending
	h.pending = nil
	h.timer = nil
	h.debounceMu.Unlock()

	if payload != nil {
		h.broadcast(payload)
	}
}

func (h *Hub) broadcast(payload []byte) {
	h.broadcasts.Add(1)
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.clients {
		select {
		case ch <- payload:
			h.delivered.Add(1)
		default:
			// A full client buffer cannot block filesystem reconciliation for
			// every other subscriber. The drop stays observable so a caller can
			// decide whether the client/buffer budget needs attention.
			h.dropped.Add(1)
		}
	}
}

// ClientCount returns the number of currently subscribed clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Stats returns a race-free snapshot of event coalescing and fan-out. It does
// not expose client channels or pending payload content.
func (h *Hub) Stats() HubStats {
	h.debounceMu.Lock()
	pending := h.pending != nil
	h.debounceMu.Unlock()

	return HubStats{
		Clients:          h.ClientCount(),
		Pending:          pending,
		Published:        h.published.Load(),
		MarshalFailures:  h.marshalFailures.Load(),
		Broadcasts:       h.broadcasts.Load(),
		Coalesced:        h.coalesced.Load(),
		Delivered:        h.delivered.Load(),
		Dropped:          h.dropped.Load(),
		PayloadBytes:     h.payloadBytes.Load(),
		LastPayloadBytes: h.lastPayloadBytes.Load(),
	}
}
