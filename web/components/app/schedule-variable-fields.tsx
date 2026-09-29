"use client";

import { useEffect, useState } from "react";

import { Field, FieldDescription, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { getPipelineConfig } from "@/lib/api-pipelines";
import type { PipelineConfigVariable } from "@/lib/generated/api-types";
import {
  variableDefaultLabel,
  type ScheduleVariableMode,
  type ScheduleVariableRows,
} from "@/lib/schedule-variable-overrides";

// Loads the variables a pipeline declares so a schedule can override them by
// name instead of as a JSON document.
export function usePipelineVariables(pipelineId: string | undefined) {
  const [state, setState] = useState<{
    pipelineId?: string;
    variables: PipelineConfigVariable[];
    error?: string;
  }>({ variables: [] });
  useEffect(() => {
    if (!pipelineId) return;
    let cancelled = false;
    getPipelineConfig(pipelineId)
      .then((config) => {
        if (!cancelled) setState({ pipelineId, variables: config.variables ?? [] });
      })
      .catch((cause: unknown) => {
        if (!cancelled)
          setState({
            pipelineId,
            variables: [],
            error: cause instanceof Error ? cause.message : "Variables could not be loaded.",
          });
      });
    return () => {
      cancelled = true;
    };
  }, [pipelineId]);
  const current = state.pipelineId === pipelineId;
  return {
    variables: current ? state.variables : [],
    loading: Boolean(pipelineId) && !current,
    error: current ? state.error : undefined,
  };
}

export function ScheduleVariableFields({
  idPrefix,
  variables,
  loading,
  error,
  rows,
  onRowsChange,
}: {
  idPrefix: string;
  variables: PipelineConfigVariable[];
  loading: boolean;
  error?: string;
  rows: ScheduleVariableRows;
  onRowsChange: (rows: ScheduleVariableRows) => void;
}) {
  if (loading) return <Skeleton className="h-16" />;
  if (error) return <FieldDescription className="text-destructive">{error}</FieldDescription>;
  if (variables.length === 0) {
    return <FieldDescription>This pipeline declares no variables.</FieldDescription>;
  }
  return (
    <div className="min-w-0 divide-y rounded-md border">
      {variables.map((variable) => {
        const row = rows[variable.name] ?? { mode: "default", text: "" };
        const inputId = `${idPrefix}-${variable.name}`;
        const update = (next: Partial<typeof row>) =>
          onRowsChange({ ...rows, [variable.name]: { ...row, ...next } });
        return (
          <Field
            key={variable.name}
            className="grid min-w-0 gap-2 px-3 py-2 sm:grid-cols-[minmax(0,1fr)_10rem_minmax(0,1.2fr)] sm:items-center"
          >
            <div className="min-w-0">
              <FieldLabel htmlFor={inputId} className="truncate font-mono text-xs">
                {variable.name}
              </FieldLabel>
              <p className="truncate text-[11px] text-muted-foreground">
                {variable.type || "string"} · default {variableDefaultLabel(variable)}
              </p>
            </div>
            <Select
              value={row.mode}
              onValueChange={(mode) => update({ mode: mode as ScheduleVariableMode })}
            >
              <SelectTrigger size="sm" className="w-full" aria-label={`${variable.name} source`}>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="default">Pipeline default</SelectItem>
                <SelectItem value="value">Value</SelectItem>
                <SelectItem value="environment">Environment variable</SelectItem>
              </SelectContent>
            </Select>
            <Input
              id={inputId}
              className="h-8 min-w-0 font-mono text-xs"
              value={row.mode === "default" ? "" : row.text}
              disabled={row.mode === "default"}
              placeholder={
                row.mode === "environment"
                  ? "API_TOKEN"
                  : row.mode === "value"
                    ? variableDefaultLabel(variable)
                    : ""
              }
              onChange={(event) => update({ text: event.target.value })}
              spellCheck={false}
            />
          </Field>
        );
      })}
    </div>
  );
}
