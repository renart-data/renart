//go:build linux || darwin

package telemetry

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestUsagePipeSettingsFailClosedWithoutWaitingForWriter(t *testing.T) {
	c := testClient(t, "http://127.0.0.1:1/v1/events", Unlinked)
	require.NoError(t, unix.Mkfifo(c.opts.ConfigPath, 0600))
	t.Cleanup(func() { _ = os.Remove(c.opts.ConfigPath) })
	done := make(chan Status, 1)
	go func() {
		c.Record(sampleObservation())
		done <- c.Status()
	}()
	select {
	case status := <-done:
		require.Equal(t, "settings_unavailable", status.Reason)
		require.False(t, status.Active)
	case <-time.After(time.Second):
		// Release a regressed blocking reader before reporting the failure.
		writer, err := os.OpenFile(c.opts.ConfigPath, os.O_RDWR|unix.O_NONBLOCK, 0600)
		if err == nil {
			_, _ = writer.WriteString("invalid")
			_ = writer.Close()
		}
		t.Fatal("a settings pipe blocked usage reporting")
	}
	require.Empty(t, c.queue)
}
