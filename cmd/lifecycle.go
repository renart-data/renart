package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"

	"go.uber.org/zap"
)

// startupGate lets the HTTP server accept connections as soon as its listener
// is bound without exposing routes backed by a partially initialized project.
// Requests that arrive during startup wait for activation, preserving the
// existing contract that a request made after the listening line eventually
// reaches the real application rather than receiving a transient error.
type startupGate struct {
	ready    chan struct{}
	handler  http.Handler
	activate sync.Once
}

func newStartupGate() *startupGate {
	return &startupGate{ready: make(chan struct{})}
}

func (g *startupGate) Activate(handler http.Handler) {
	g.activate.Do(func() {
		g.handler = handler
		close(g.ready)
	})
}

func (g *startupGate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	select {
	case <-g.ready:
		g.handler.ServeHTTP(w, r)
	case <-r.Context().Done():
	}
}

const renartASCII = ` ____  _____ _   _    _    ____ _____
|  _ \| ____| \ | |  / \  |  _ \_   _|
| |_) |  _| |  \| | / _ \ | |_) || |
|  _ <| |___| |\  |/ ___ \|  _ < | |
|_| \_\_____|_| \_/_/   \_\_| \_\|_|`

func printRenartWelcome(out io.Writer, appURL, detail string) {
	_, _ = fmt.Fprintln(out, renartASCII)
	_, _ = fmt.Fprintln(out, "Welcome to Renart, your local data pipeline IDE.")
	_, _ = fmt.Fprintf(out, "Renart listening on %s%s\n", appURL, detail)
}

// startGracefulShutdown restores the default signal behavior as soon as the
// first signal cancels ctx. That lets the first Ctrl-C drain schedulers and
// remove discovery files while a second Ctrl-C still force-exits promptly.
func startGracefulShutdown(
	ctx context.Context,
	restoreSignals func(),
	logger *zap.Logger,
	shutdown func(),
) func() {
	stopped := make(chan struct{})
	done := make(chan struct{})
	var stopOnce sync.Once
	var restoreOnce sync.Once
	restore := func() {
		restoreOnce.Do(restoreSignals)
	}
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			restore()
			logger.Info(
				"stopping Renart",
				zap.String("hint", "press Ctrl-C again to force exit"),
			)
			shutdown()
		case <-stopped:
		}
	}()
	return func() {
		if ctx.Err() == nil {
			stopOnce.Do(func() {
				close(stopped)
			})
		}
		<-done
		restore()
	}
}
