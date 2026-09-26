import { useUsageAnalytics } from "@/hooks/use-usage-analytics";
import { useState } from "react";
import { Info } from "lucide-react";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field";
import { fetchJSON } from "@/lib/api-core";
import type { UsageAnalyticsStatus, UsageEvent } from "@/lib/generated/api-types";
import { SettingsCard, SettingsStatus } from "./settings-form-parts";

const reasons: Record<string, string> = {
  enabled: "Usage analytics is on.",
  disabled: "Usage analytics is off.",
  environment: "Disabled by an environment opt-out. This takes priority over the setting below.",
  ci: "Disabled in continuous integration.",
  development: "Disabled in development builds.",
  no_collector: "Collection is inactive: this build has no analytics collector configured.",
  invalid_endpoint: "Collection is inactive: the configured collector address is invalid.",
  settings_unavailable: "Collection is inactive: Renart couldn't read your usage settings.",
  notice_required: "No usage events are sent until you've seen the first-run notice.",
};

function PrivacyDescription({ mode }: { mode: UsageAnalyticsStatus["mode"] }) {
  return (
    <>
      Renart can send run outcomes, rough counts and timings, and its version and platform.
      {mode === "installation"
        ? " A random installation ID helps measure first successful runs and returning installations. This is pseudonymous usage data."
        : " Events aren't linked to a persistent installation ID."}{" "}
      Your code, data, paths, project and connection names, credentials and raw errors aren't
      included.
    </>
  );
}

export function UsageAnalyticsNotice() {
  const { data, saveError, busy, update } = useUsageAnalytics();
  if (!data?.notice_required) return null;
  return (
    <div className="shrink-0 px-2 pt-2 md:px-4">
      <Alert>
        <Info />
        <AlertTitle>Usage analytics</AlertTitle>
        <AlertDescription>
          <p>
            <PrivacyDescription mode={data.mode} />
          </p>
          <p>
            You can turn this off now or later in Settings → General. Renart works the same either
            way.
          </p>
          <div className="flex flex-wrap gap-2">
            <Button
              size="sm"
              variant="outline"
              disabled={busy}
              onClick={() => void update({ enabled: false })}
            >
              Turn off analytics
            </Button>
            <Button
              size="sm"
              disabled={busy}
              onClick={() => void update({ acknowledge_notice: true })}
            >
              Got it
            </Button>
          </div>
          {saveError ? <p role="alert">{saveError}</p> : null}
        </AlertDescription>
      </Alert>
    </div>
  );
}

export function UsageAnalyticsSettings() {
  const { data, error, saveError, busy, update } = useUsageAnalytics();
  const [sample, setSample] = useState<UsageEvent | null>(null);
  const [sampleError, setSampleError] = useState<string | null>(null);
  const inspect = async () => {
    setSampleError(null);
    try {
      setSample(await fetchJSON<UsageEvent>("/api/telemetry/sample", { cache: "no-store" }));
    } catch (error) {
      setSampleError(error instanceof Error ? error.message : "Could not load the example.");
    }
  };
  return (
    <SettingsCard title="Usage analytics" description="Saved for your user, across all projects.">
      <FieldGroup>
        <SettingsStatus
          message={saveError ?? (error ? "Could not load usage settings." : null)}
          tone="error"
        />
        <Field orientation="horizontal">
          <FieldContent>
            <FieldLabel htmlFor="usage-analytics-enabled">Allow usage analytics</FieldLabel>
            <FieldDescription>
              {data ? <PrivacyDescription mode={data.mode} /> : "Loading usage settings…"}
            </FieldDescription>
          </FieldContent>
          <Switch
            id="usage-analytics-enabled"
            checked={data?.enabled ?? false}
            disabled={!data || busy}
            onCheckedChange={(enabled) => void update({ enabled })}
          />
        </Field>
        {data ? (
          <p role="status" className="text-xs text-muted-foreground">
            {reasons[data.reason] ?? "Collection is inactive."}
          </p>
        ) : null}
        <p className="text-xs text-muted-foreground">
          You can also use <code>renart telemetry disable</code> or{" "}
          <code>RENART_TELEMETRY=off</code>. Environment opt-outs always win.
        </p>
        <div className="flex flex-wrap gap-2">
          <Button size="sm" variant="outline" onClick={() => void inspect()}>
            Inspect example payload
          </Button>
          {data?.mode === "installation" && data.installation_id ? (
            <Button
              size="sm"
              variant="outline"
              disabled={busy}
              onClick={() => void update({ reset_identity: true })}
            >
              Reset installation ID
            </Button>
          ) : null}
        </div>
        {data?.mode === "installation" ? (
          <p className="text-xs text-muted-foreground">
            Resetting discards the current ID and queued events. It doesn't delete data already
            received by the collector.
          </p>
        ) : null}
        {sampleError ? <SettingsStatus message={sampleError} tone="error" /> : null}
        {sample ? (
          <div className="flex min-w-0 flex-col gap-2">
            <p className="text-xs text-muted-foreground">
              Synthetic example. Opening this preview sends nothing to the collector.
            </p>
            <pre
              aria-label="Example usage analytics payload"
              className="max-h-80 overflow-auto rounded-md border bg-muted p-3 text-xs"
            >
              {JSON.stringify(sample, null, 2)}
            </pre>
          </div>
        ) : null}
      </FieldGroup>
    </SettingsCard>
  );
}
