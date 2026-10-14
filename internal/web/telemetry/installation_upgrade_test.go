package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInstallationUpgradeRequiresCurrentNoticeAndPreservesOptOut(t *testing.T) {
	for _, tc := range []struct {
		name, saved string
		optedOut    bool
	}{
		{"legacy default", `{"notice_seen":true,"generation":"old"}`, false},
		{"legacy enabled", `{"enabled":true,"notice_seen":true,"generation":"old"}`, false},
		{"legacy disabled", `{"enabled":false,"notice_seen":true,"generation":"old"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var batches []Batch
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var b Batch
				require.NoError(t, json.NewDecoder(r.Body).Decode(&b))
				batches = append(batches, b)
				w.WriteHeader(202)
			}))
			defer receiver.Close()
			c := testClient(t, receiver.URL, Installation)
			require.NoError(t, os.WriteFile(c.opts.ConfigPath, []byte(tc.saved), 0600))
			c.StartSession(Web)
			c.Record(sampleObservation())
			c.Flush(context.Background())
			require.Empty(t, batches)
			require.Empty(t, c.Status().InstallationID)
			if tc.optedOut {
				require.Equal(t, "disabled", c.Status().Reason)
				require.False(t, c.Status().NoticeRequired)
				return
			}
			require.True(t, c.Status().NoticeRequired)
			require.Equal(t, 2, c.Sample().SchemaVersion)
			require.Empty(t, c.Status().InstallationID, "sample must not create an identity")
			acknowledge(t, c)
			c.Flush(context.Background())
			require.Len(t, batches, 1)
			require.Equal(t, 2, batches[0].SchemaVersion)
			require.Len(t, batches[0].Events, 1)
			require.Equal(t, 2, batches[0].Events[0].SchemaVersion)
			first := batches[0].Events[0].InstallationID
			require.NotEmpty(t, first)
			require.NotEqual(t, c.Sample().InstallationID, first)
			c.Close()
			// A new process / version using the same user config keeps the identity.
			opts := c.opts
			opts.Version = "v0.5.12"
			next := New(opts)
			defer next.Close()
			require.False(t, next.Status().NoticeRequired)
			next.StartSession(Terminal)
			next.Flush(context.Background())
			require.Len(t, batches, 2)
			require.Equal(t, first, batches[1].Events[0].InstallationID)
			require.Equal(t, "v0.5.12", batches[1].Events[0].Version)
		})
	}
}

func TestLegacyUnlinkedNoticeStillUsesSchemaOne(t *testing.T) {
	var b Batch
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&b))
		w.WriteHeader(202)
	}))
	defer receiver.Close()
	c := testClient(t, receiver.URL, Unlinked)
	require.NoError(t, os.WriteFile(c.opts.ConfigPath, []byte(`{"notice_seen":true,"generation":"old"}`), 0600))
	c.StartSession(Web)
	c.Flush(context.Background())
	require.Equal(t, 1, b.SchemaVersion)
	require.Equal(t, 1, b.Events[0].SchemaVersion)
	require.Empty(t, b.Events[0].InstallationID)
}

func TestSavedInstallationIDMustMatchWireContract(t *testing.T) {
	for _, id := range []string{
		"AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA",
		"{aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa}",
		"aaaaaaaa-aaaa-4aaa-0aaa-aaaaaaaaaaaa",
	} {
		c := testClient(t, "http://127.0.0.1:1/v1/events", Installation)
		value := preferences{NoticeSeen: true, NoticeVersion: installationNoticeVersion, InstallationID: id}
		raw, err := json.Marshal(value)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(c.opts.ConfigPath, raw, 0600))
		c.StartSession(Web)
		require.Equal(t, "settings_unavailable", c.Status().Reason)
		require.Empty(t, c.queue)
	}
}
