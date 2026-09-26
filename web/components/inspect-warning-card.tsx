"use client";

import { AlertTriangle } from "lucide-react";
import { ReactNode } from "react";

import { Alert, AlertTitle, AlertDescription } from "@/components/ui/alert";

/**
 * Shown when a preview/inspect/query fails (syntax error, missing table, …).
 * Failures here are a routine part of editing, so the card stays quiet: same
 * neutral surface as InspectInfoCard, with the amber icon as the only
 * severity cue instead of a full amber wash.
 */
export function InspectWarningCard({
  message,
  testId,
  actions,
}: {
  message: string;
  testId?: string;
  actions?: ReactNode;
}) {
  return (
    <Alert className="max-w-2xl" data-testid={testId}>
      <AlertTriangle className="text-amber-500" />
      <AlertTitle className="text-foreground">Preview failed</AlertTitle>
      <AlertDescription className="whitespace-pre-wrap font-mono text-xs leading-5 text-muted-foreground">
        {message}
      </AlertDescription>
      {actions ? <div className="mt-3">{actions}</div> : null}
    </Alert>
  );
}
