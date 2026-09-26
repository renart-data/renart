package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testClient(t *testing.T, endpoint string, mode Mode) *Client {
	t.Helper()
	c := New(Options{Version: "v0.5.7", Endpoint: endpoint, Mode: mode, ConfigPath: filepath.Join(t.TempDir(), "telemetry.json"), Getenv: func(string) string { return "" }})
	t.Cleanup(c.Close)
	return c
}
func acknowledge(t *testing.T, c *Client) {
	t.Helper()
	_, err := c.Update(UpdateRequest{AcknowledgeNotice: true})
	require.NoError(t, err)
}
func sampleObservation() Observation {
	return Observation{Name: NotebookFinished, Surface: Notebook, Outcome: Success, Trigger: Manual, Duration: 25 * time.Second, Items: 18}
}

func TestUsageDisabledPoliciesNeverSendOrCreateID(t *testing.T) {
	var requests atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(204) }))
	defer receiver.Close()
	for _, tc := range []struct{ name, version, endpoint, envKey, envValue, reason string }{
		{name: "environment", version: "v0.5.7", endpoint: receiver.URL, envKey: "RENART_TELEMETRY", envValue: "off", reason: "environment"},
		{name: "dnt", version: "v0.5.7", endpoint: receiver.URL, envKey: "DO_NOT_TRACK", envValue: "1", reason: "environment"},
		{name: "existing optout", version: "v0.5.7", endpoint: receiver.URL, envKey: "TELEMETRY_OPTOUT", envValue: "1", reason: "environment"},
		{name: "ci", version: "v0.5.7", endpoint: receiver.URL, envKey: "CI", envValue: "true", reason: "ci"},
		{name: "development", version: "dev", endpoint: receiver.URL, reason: "development"},
		{name: "no collector", version: "v0.5.7", reason: "no_collector"},
		{name: "insecure endpoint", version: "v0.5.7", endpoint: "http://example.com/collect", reason: "invalid_endpoint"},
		{name: "credentials in endpoint", version: "v0.5.7", endpoint: "https://secret@example.com/collect", reason: "invalid_endpoint"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "telemetry.json")
			c := New(Options{Version: tc.version, Endpoint: tc.endpoint, Mode: Installation, ConfigPath: path, Getenv: func(key string) string {
				if key == tc.envKey {
					return tc.envValue
				}
				return ""
			}})
			c.StartSession(Web)
			c.Record(sampleObservation())
			c.Flush(context.Background())
			c.Close()
			require.Equal(t, tc.reason, c.Status().Reason)
			_, err := os.Stat(path)
			require.True(t, os.IsNotExist(err))
			require.Zero(t, requests.Load())
		})
	}
}

func TestUsageNoticeThenAllowlistedPayload(t *testing.T) {
	bodies := make(chan []byte, 2)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		bodies <- data
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	c := testClient(t, receiver.URL, Installation)
	c.StartSession(Web)
	c.Record(sampleObservation())
	c.Flush(context.Background())
	require.Empty(t, bodies)
	require.Equal(t, "notice_required", c.Status().Reason)
	require.Empty(t, c.Status().InstallationID)
	acknowledge(t, c)
	c.Record(Observation{Name: Name("secret SQL: select * from private.orders"), Surface: Notebook})
	c.Record(sampleObservation())
	c.Flush(context.Background())
	var batch Batch
	require.NoError(t, json.Unmarshal(<-bodies, &batch))
	require.Len(t, batch.Events, 2)
	require.Equal(t, SessionStarted, batch.Events[0].Name)
	event := batch.Events[1]
	require.Equal(t, NotebookFinished, event.Name)
	require.Equal(t, "10s_to_1m", event.DurationBucket)
	require.Equal(t, "10_to_99", event.ItemCountBucket)
	require.Equal(t, c.Status().InstallationID, event.InstallationID)
	require.NotEmpty(t, event.InstallationID)
	c.StartSession(Web)
	c.Flush(context.Background())
	require.Empty(t, bodies)
	raw, err := os.ReadFile(c.opts.ConfigPath)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private.orders")
	stat, err := os.Stat(c.opts.ConfigPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), stat.Mode().Perm())
}

func TestUsageUnlinkedNeverPersistsOrSendsInstallationID(t *testing.T) {
	var received Batch
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&received))
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	c := testClient(t, receiver.URL, Unlinked)
	acknowledge(t, c)
	c.Record(sampleObservation())
	c.Flush(context.Background())
	require.Len(t, received.Events, 1)
	require.Empty(t, received.Events[0].InstallationID)
	raw, err := os.ReadFile(c.opts.ConfigPath)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "installation_id")
}

func TestUsageOptOutAcrossClientsDropsQueuedEventsAndRotatesID(t *testing.T) {
	var requests atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(204) }))
	defer receiver.Close()
	c := testClient(t, receiver.URL, Installation)
	acknowledge(t, c)
	c.Record(sampleObservation())
	firstID := c.Status().InstallationID
	other := New(c.opts)
	defer other.Close()
	disabled := false
	_, err := other.Update(UpdateRequest{Enabled: &disabled})
	require.NoError(t, err)
	c.Flush(context.Background())
	require.Zero(t, requests.Load())
	require.Empty(t, c.Status().InstallationID)
	enabled := true
	_, err = other.Update(UpdateRequest{Enabled: &enabled})
	require.NoError(t, err)
	c.Flush(context.Background())
	require.Zero(t, requests.Load())
	c.Record(sampleObservation())
	require.NotEmpty(t, c.Status().InstallationID)
	require.NotEqual(t, firstID, c.Status().InstallationID)
	c.Flush(context.Background())
	require.Equal(t, int32(1), requests.Load())
	c.Record(sampleObservation())
	_, err = other.Update(UpdateRequest{ResetIdentity: true})
	require.NoError(t, err)
	c.Flush(context.Background())
	require.Equal(t, int32(1), requests.Load())
}

func TestUsageOptOutCancelsInflightRequest(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer receiver.Close()
	c := testClient(t, receiver.URL, Installation)
	acknowledge(t, c)
	c.Record(sampleObservation())
	done := make(chan struct{})
	go func() { c.Flush(context.Background()); close(done) }()
	<-started
	disabled := false
	_, err := c.Update(UpdateRequest{Enabled: &disabled})
	require.NoError(t, err)
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("request was not cancelled")
	}
	<-done
}

func TestUsageCorruptSettingsFailClosedAndSamplesHaveNoSideEffects(t *testing.T) {
	c := testClient(t, "https://example.com/collect", Installation)
	for _, raw := range []string{"invalid", `{ "installation_id": "a secret project name" }`, `{}` + strings.Repeat(" ", 9000)} {
		require.NoError(t, os.WriteFile(c.opts.ConfigPath, []byte(raw), 0600))
		require.Equal(t, "settings_unavailable", c.Status().Reason)
		c.Record(sampleObservation())
		require.Empty(t, c.queue)
	}
	require.NoError(t, os.Remove(c.opts.ConfigPath))
	sample := c.Sample()
	require.Equal(t, "00000000-0000-4000-8000-000000000000", sample.InstallationID)
	_, err := os.Stat(c.opts.ConfigPath)
	require.True(t, os.IsNotExist(err))
}

func TestUsageConcurrentLaunchesShareOneID(t *testing.T) {
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer receiver.Close()
	c := testClient(t, receiver.URL, Installation)
	acknowledge(t, c)
	// Prevent real network in cleanup; the queue is inspected locally only.
	var clients []*Client
	for range 8 {
		clients = append(clients, New(c.opts))
	}
	var wg sync.WaitGroup
	for _, client := range clients {
		wg.Add(1)
		go func(client *Client) { defer wg.Done(); client.Record(sampleObservation()) }(client)
	}
	wg.Wait()
	id := c.Status().InstallationID
	require.NotEmpty(t, id)
	for _, client := range clients {
		require.Len(t, client.queue, 1)
		require.Equal(t, id, client.queue[0].event.InstallationID)
	}
	disabled := false
	_, err := c.Update(UpdateRequest{Enabled: &disabled})
	require.NoError(t, err)
	for _, client := range clients {
		client.Close()
	}
}

func TestUsageDoesNotFollowRedirects(t *testing.T) {
	var leaked atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer other.Close()
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer receiver.Close()
	c := testClient(t, receiver.URL, Unlinked)
	acknowledge(t, c)
	c.Record(sampleObservation())
	c.Flush(context.Background())
	require.Zero(t, leaked.Load())
}

func TestUsageReceiverFailureDropsBoundedBatchesWithoutRetry(t *testing.T) {
	var requests atomic.Int32
	var received []Event
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.LessOrEqual(t, len(body), 16<<10)
		var batch Batch
		require.NoError(t, json.Unmarshal(body, &batch))
		require.LessOrEqual(t, len(batch.Events), batchLimit)
		received = append(received, batch.Events...)
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer receiver.Close()
	c := testClient(t, receiver.URL, Unlinked)
	// Drive flushing explicitly, without races against the batching worker.
	c.cancel()
	<-c.done
	acknowledge(t, c)
	for range queueLimit * 2 {
		c.Record(sampleObservation())
	}
	require.Len(t, c.queue, queueLimit)
	for range queueLimit / batchLimit {
		c.Flush(context.Background())
	}
	require.Empty(t, c.queue)
	require.Equal(t, int32(queueLimit/batchLimit), requests.Load())
	require.Len(t, received, queueLimit)
	unique := make(map[string]bool)
	for _, event := range received {
		require.False(t, unique[event.EventID])
		unique[event.EventID] = true
	}
	c.Flush(context.Background())
	require.Equal(t, int32(queueLimit/batchLimit), requests.Load())
}

func TestUsageSlowReceiverCannotHoldShutdown(t *testing.T) {
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer receiver.Close()
	c := testClient(t, receiver.URL, Unlinked)
	acknowledge(t, c)
	c.Record(sampleObservation())
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * requestTimeout):
		t.Fatal("analytics held shutdown beyond its request timeout")
	}
	require.Empty(t, c.queue)
}

func TestUsageResetDoesNotCountAnotherSession(t *testing.T) {
	var received []Event
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch Batch
		require.NoError(t, json.NewDecoder(r.Body).Decode(&batch))
		received = append(received, batch.Events...)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	c := testClient(t, receiver.URL, Installation)
	c.StartSession(Web)
	acknowledge(t, c)
	c.Flush(context.Background())
	require.Len(t, received, 1)
	require.Equal(t, SessionStarted, received[0].Name)
	firstID := c.Status().InstallationID
	_, err := c.Update(UpdateRequest{ResetIdentity: true})
	require.NoError(t, err)
	require.Empty(t, c.Status().InstallationID)
	c.StartSession(Web)
	c.Flush(context.Background())
	require.Len(t, received, 1)
	c.Record(sampleObservation())
	c.Flush(context.Background())
	require.Len(t, received, 2)
	require.Equal(t, NotebookFinished, received[1].Name)
	require.NotEqual(t, firstID, received[1].InstallationID)
}

func TestUsageRecordDoesNotWaitForSettingsUpdate(t *testing.T) {
	c := testClient(t, "http://127.0.0.1:1/v1/events", Unlinked)
	acknowledge(t, c)
	c.mu.Lock()
	done := make(chan struct{})
	go func() { c.Record(sampleObservation()); close(done) }()
	select {
	case <-done:
		c.mu.Unlock()
	case <-time.After(time.Second):
		c.mu.Unlock()
		<-done
		t.Fatal("recording usage waited for a settings operation")
	}
	require.Empty(t, c.queue)
}

func TestUsageRecordDoesNotWaitForSlowCollector(t *testing.T) {
	started := make(chan struct{})
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	}))
	defer receiver.Close()
	c := testClient(t, receiver.URL, Unlinked)
	// Control the one request explicitly, so the worker cannot flush a second.
	c.cancel()
	<-c.done
	acknowledge(t, c)
	c.Record(sampleObservation())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	flushed := make(chan struct{})
	go func() { c.Flush(ctx); close(flushed) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("collector never received the request")
	}
	recorded := make(chan struct{})
	go func() {
		for range queueLimit * 2 {
			c.Record(sampleObservation())
		}
		close(recorded)
	}()
	select {
	case <-recorded:
	case <-time.After(time.Second):
		t.Fatal("recording usage waited for the collector")
	}
	c.mu.Lock()
	queued := len(c.queue)
	c.mu.Unlock()
	require.Equal(t, queueLimit, queued)
	cancel()
	<-flushed
	// Do not make a final send to the single-request test handler.
	disabled := false
	_, err := c.Update(UpdateRequest{Enabled: &disabled})
	require.NoError(t, err)
}

func TestUsageConnectionAndTLSFailuresDropEvents(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	unreachable := "http://" + listener.Addr().String()
	require.NoError(t, listener.Close())
	tlsReceiver := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("untrusted TLS connection should fail before sending an event")
	}))
	tlsReceiver.Config.ErrorLog = log.New(io.Discard, "", 0)
	tlsReceiver.StartTLS()
	defer tlsReceiver.Close()
	for name, endpoint := range map[string]string{"connection refused": unreachable, "untrusted TLS": tlsReceiver.URL} {
		t.Run(name, func(t *testing.T) {
			c := testClient(t, endpoint, Unlinked)
			acknowledge(t, c)
			c.Record(sampleObservation())
			c.Flush(context.Background())
			c.mu.Lock()
			queued := len(c.queue)
			c.mu.Unlock()
			require.Zero(t, queued, "failed events must not become a retry backlog")
		})
	}
}

func TestUsageShutdownDoesNotCloseApplicationHTTPConnections(t *testing.T) {
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.RemoteAddr)
	}))
	defer receiver.Close()
	application := &http.Client{Timeout: time.Second}
	defer application.CloseIdleConnections()
	remoteAddress := func() string {
		response, err := application.Get(receiver.URL)
		require.NoError(t, err)
		body, err := io.ReadAll(response.Body)
		require.NoError(t, response.Body.Close())
		require.NoError(t, err)
		return string(body)
	}
	before := remoteAddress()
	c := testClient(t, receiver.URL, Unlinked)
	acknowledge(t, c)
	c.Record(sampleObservation())
	c.Close()
	require.Equal(t, before, remoteAddress(), "telemetry shutdown closed an unrelated reusable connection")
}
