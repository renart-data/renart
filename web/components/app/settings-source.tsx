import { useState } from "react";
import { Alert, AlertTitle, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import type { WorkspaceConfigResponse } from "@/lib/types";

// A parent config may be intentionally shared. Require an explicit choice before
// writing outside this project, and keep policy/secret sidecar ownership visible.
export function useSettingsSource(config: WorkspaceConfigResponse | null) {
  const [acknowledged, setAcknowledged] = useState(false);
  const inherited = Boolean(config?.configuration_inherited);
  return {
    canWrite: !inherited || acknowledged,
    notice: inherited ? (
      <Alert>
        <AlertTitle>Shared configuration outside this project</AlertTitle>
        <AlertDescription>
          <p>
            Connection definitions are saved to{" "}
            <code className="break-all">{config?.configuration_path}</code>. Changes can affect
            other projects that use this file. Renart guardrails and secret bindings stay in this
            project's <code>.renart</code> directory.
          </p>
          {!acknowledged ? (
            <Button variant="outline" size="sm" onClick={() => setAcknowledged(true)}>
              Allow editing shared configuration
            </Button>
          ) : (
            <p>Editing shared configuration is enabled for this editor.</p>
          )}
        </AlertDescription>
      </Alert>
    ) : null,
  };
}
