package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
	"renart/internal/web/telemetry"
)

// The receiver must support schema v2 before releasing this installation-linked client.
// Custom distributions can override this with -ldflags -X renart/cmd.usageCollectorURL=...
var usageCollectorURL = "https://telemetry.getrenart.com/v1/events"

// Installation counts use one random identity per operating-system user config.
const usageMode = telemetry.Installation

func newUsageClient() *telemetry.Client {
	endpoint := usageCollectorURL
	if override := os.Getenv("RENART_TELEMETRY_ENDPOINT"); override != "" {
		endpoint = override
	}
	return telemetry.New(telemetry.Options{Version: buildVersion, Endpoint: endpoint, Mode: usageMode, AllowDevelopment: os.Getenv("RENART_TELEMETRY") == "on"})
}

func Telemetry() *cli.Command {
	return &cli.Command{
		Name: "telemetry", Category: categoryApp,
		Usage: "inspect or change usage analytics for this operating-system user",
		Commands: []*cli.Command{
			{Name: "status", Usage: "show the effective analytics policy", Action: func(_ context.Context, c *cli.Command) error {
				client := newUsageClient()
				defer client.Close()
				return json.NewEncoder(c.Writer).Encode(client.Status())
			}},
			{Name: "disable", Usage: "turn off analytics and discard the local installation ID", Action: telemetryPreference(false)},
			{Name: "enable", Usage: "allow usage analytics when a collector is configured", Action: telemetryPreference(true)},
			{Name: "reset", Usage: "discard the local installation ID (does not delete collected data)", Action: func(_ context.Context, c *cli.Command) error {
				client := newUsageClient()
				defer client.Close()
				status, err := client.Update(telemetry.UpdateRequest{ResetIdentity: true})
				if err != nil {
					return err
				}
				return json.NewEncoder(c.Writer).Encode(status)
			}},
			{Name: "sample", Usage: "print a synthetic example of the complete payload; sends nothing", Action: func(_ context.Context, c *cli.Command) error {
				client := newUsageClient()
				defer client.Close()
				encoder := json.NewEncoder(c.Writer)
				encoder.SetIndent("", "  ")
				return encoder.Encode(client.Sample())
			}},
		},
	}
}

func telemetryPreference(enabled bool) cli.ActionFunc {
	return func(_ context.Context, c *cli.Command) error {
		client := newUsageClient()
		defer client.Close()
		if enabled {
			fmt.Fprintln(c.ErrWriter, usageNotice)
		}
		status, err := client.Update(telemetry.UpdateRequest{Enabled: &enabled})
		if err != nil {
			return err
		}
		return json.NewEncoder(c.Writer).Encode(status)
	}
}

const usageNotice = "Renart can send usage counts, run outcomes and coarse version/platform information, linked by a random installation ID saved for your operating-system user. This helps count reporting installations and successful pipeline use. Installation-level daily records expire after 28 days; aggregate counts and estimates remain. It never includes your code, data, paths, credentials or raw errors. Disable with renart telemetry disable or RENART_TELEMETRY=off. Inspect the exact fields with renart telemetry sample."
