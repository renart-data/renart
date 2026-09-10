package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type workspaceStreamReader struct {
	ch           chan []byte
	unsubscribed bool
}

func (*workspaceStreamReader) CurrentWorkspace() any                    { return map[string]any{} }
func (*workspaceStreamReader) CurrentWorkspaceLite() any                { return map[string]any{} }
func (r *workspaceStreamReader) SubscribeWorkspaceEvents() chan []byte  { return r.ch }
func (r *workspaceStreamReader) UnsubscribeWorkspaceEvents(chan []byte) { r.unsubscribed = true }

func TestWorkspaceEventsReturnWhenSubscriptionCloses(t *testing.T) {
	reader := &workspaceStreamReader{ch: make(chan []byte, 1)}
	reader.ch <- []byte(`{"type":"changed"}`)
	close(reader.ch)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&WorkspaceHandlers{Reader: reader}).HandleEvents(recorder, httptest.NewRequest("GET", "/api/events", nil).WithContext(ctx))
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("closed SSE subscription did not end the response")
	}
	require.True(t, reader.unsubscribed)
	require.Equal(t, "data: {}\n\ndata: {\"type\":\"changed\"}\n\n", recorder.Body.String())
}

type failedWorkspaceStreamWriter struct{ *httptest.ResponseRecorder }

func (*failedWorkspaceStreamWriter) Write([]byte) (int, error) { return 0, errors.New("client gone") }

func TestWorkspaceEventsReleaseSubscriptionAfterWriteFailure(t *testing.T) {
	reader := &workspaceStreamReader{ch: make(chan []byte)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&WorkspaceHandlers{Reader: reader}).HandleEvents(&failedWorkspaceStreamWriter{httptest.NewRecorder()}, httptest.NewRequest("GET", "/api/events", nil).WithContext(ctx))
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("failed SSE write did not release its subscription")
	}
	require.True(t, reader.unsubscribed)
}
