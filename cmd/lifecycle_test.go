package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestStartupGateHoldsRequestsUntilApplicationIsReady(t *testing.T) {
	t.Parallel()
	gate := newStartupGate()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
	done := make(chan struct{})
	go func() {
		gate.ServeHTTP(response, request)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("request passed through before the application was ready")
	case <-time.After(25 * time.Millisecond):
	}

	gate.Activate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/workspace", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("request remained blocked after the application became ready")
	}
	assert.Equal(t, http.StatusNoContent, response.Code)
}

func TestStartupGateReleasesCancelledRequests(t *testing.T) {
	t.Parallel()
	gate := newStartupGate()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		gate.ServeHTTP(httptest.NewRecorder(), request)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled startup request remained blocked")
	}
}

func TestPrintRenartWelcome(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer

	printRenartWelcome(&output, "http://127.0.0.1:8080", " (HTTP/2 enabled)")

	message := output.String()
	assert.Contains(t, message, "____  _____")
	assert.Contains(t, message, "Welcome to Renart")
	assert.Contains(t, message, "Renart listening on http://127.0.0.1:8080 (HTTP/2 enabled)")
}

func TestGracefulShutdownRestoresSignalsBeforeCleanup(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	logCore, observed := observer.New(zap.InfoLevel)
	logger := zap.New(logCore)
	var signalsRestored atomic.Bool
	shutdownDone := make(chan struct{})
	restoredBeforeShutdown := make(chan bool, 1)

	stopObserver := startGracefulShutdown(
		ctx,
		func() { signalsRestored.Store(true) },
		logger,
		func() {
			restoredBeforeShutdown <- signalsRestored.Load()
			close(shutdownDone)
		},
	)
	defer stopObserver()
	cancel()
	<-shutdownDone

	assert.True(t, <-restoredBeforeShutdown)
	entries := observed.All()
	require.Len(t, entries, 1)
	assert.Equal(t, "stopping Renart", entries[0].Message)
	assert.Equal(t, "press Ctrl-C again to force exit", entries[0].ContextMap()["hint"])
}

func TestGracefulShutdownObserverStopsQuietlyDuringNormalCleanup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	logCore, observed := observer.New(zap.InfoLevel)
	logger := zap.New(logCore)
	var signalsRestored atomic.Bool
	var shutdownCalled atomic.Bool

	stopObserver := startGracefulShutdown(
		ctx,
		func() { signalsRestored.Store(true) },
		logger,
		func() { shutdownCalled.Store(true) },
	)
	stopObserver()

	assert.True(t, signalsRestored.Load())
	assert.False(t, shutdownCalled.Load())
	assert.Empty(t, observed.All())
}
