package notebook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"renart/internal/web/telemetry"
)

func TestUsageNotebookRecordsOneBatchOutcomeWithoutAuthoredFields(t *testing.T) {
	for _, tc := range []struct {
		name, sql string
		outcome   telemetry.Outcome
		cancel    bool
	}{
		{"success", "select 42 as secret_customer_id", telemetry.Success, false},
		{"failure", "select secret_column from missing_private_table", telemetry.Failed, false},
		{"cancel", "select 42", telemetry.Cancelled, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			nb := loadRunFixture(t, map[string]string{ManifestFileName: "id: 11111111-0000-0000-0000-000000000001\nblocks:\n  - cell: aaaa1111\n", "private.sql": "/* @bruin\nid: aaaa1111\ntype: duckdb.sql\n@bruin */\n" + tc.sql + "\n"})
			var events []telemetry.Observation
			runner := &Runner{Store: NewSessionStore(filepath.Join(t.TempDir(), "sessions")), RenameTables: realRenameTables(t), UsageTrigger: telemetry.Automatic, RecordUsage: func(o telemetry.Observation) { events = append(events, o) }}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			_, _ = runner.RunCells(ctx, nb, TopoOrder(nb), RunOptions{})
			require.Len(t, events, 1)
			require.Equal(t, tc.outcome, events[0].Outcome)
			require.Equal(t, telemetry.Automatic, events[0].Trigger)
			require.Equal(t, 1, events[0].Items)
		})
	}
}

func TestUsageFailuresDoNotChangeNotebookResults(t *testing.T) {
	for _, failure := range []string{"collector rejects", "collector unavailable", "corrupt settings", "settings is a directory"} {
		t.Run(failure, func(t *testing.T) {
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer receiver.Close()
			if failure == "collector unavailable" {
				receiver.Close()
			}
			settings := filepath.Join(t.TempDir(), "telemetry.json")
			client := telemetry.New(telemetry.Options{Version: "v0.5.8", Endpoint: receiver.URL, ConfigPath: settings, Getenv: func(string) string { return "" }})
			defer client.Close()
			_, err := client.Update(telemetry.UpdateRequest{AcknowledgeNotice: true})
			require.NoError(t, err)
			switch failure {
			case "corrupt settings":
				require.NoError(t, os.WriteFile(settings, []byte("invalid JSON"), 0600))
			case "settings is a directory":
				require.NoError(t, os.Remove(settings))
				require.NoError(t, os.Mkdir(settings, 0700))
			}
			nb := loadRunFixture(t, map[string]string{ManifestFileName: "id: 11111111-0000-0000-0000-000000000001\nblocks:\n  - cell: aaaa1111\n", "private.sql": "/* @bruin\nid: aaaa1111\ntype: duckdb.sql\n@bruin */\nselect 42 as secret_customer_id\n"})
			runner := &Runner{Store: NewSessionStore(filepath.Join(t.TempDir(), "sessions")), RenameTables: realRenameTables(t), RecordUsage: client.Record}
			// Run twice, including after a failed send. Both executions must keep
			// their original rows and status without surfacing a telemetry error.
			for range 2 {
				results, runErr := runner.RunCells(context.Background(), nb, TopoOrder(nb), RunOptions{})
				require.NoError(t, runErr)
				require.Len(t, results, 1)
				require.Equal(t, CellRunOK, results[0].Status)
				require.Empty(t, results[0].Error)
				require.Equal(t, []string{"secret_customer_id"}, results[0].Columns)
				require.EqualValues(t, 42, results[0].Rows[0][0])
				client.Flush(context.Background())
			}
		})
	}
}
