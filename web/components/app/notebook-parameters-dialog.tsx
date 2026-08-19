"use client";

import { Plus, SlidersHorizontal } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

import {
  AuthoredControlEditor,
  AuthoredControlValueField,
} from "@/components/app/authored-control";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { FieldError, FieldGroup } from "@/components/ui/field";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { authoredControlDefinitionsProblem } from "@/lib/authored-controls";
import type { NotebookParameter } from "@/lib/generated/api-types";

function cloneParameters(parameters: NotebookParameter[]) {
  return JSON.parse(JSON.stringify(parameters)) as NotebookParameter[];
}

function parameterValuesFrom(parameters: NotebookParameter[], current: Record<string, unknown>) {
  return Object.fromEntries(
    parameters.map((parameter) => [
      parameter.id,
      current[parameter.id] === undefined ? parameter.default : current[parameter.id],
    ]),
  );
}

export function NotebookParametersDialog({
  open,
  onOpenChange,
  parameters,
  values,
  onSaveDefinitions,
  onSaveValues,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  parameters: NotebookParameter[];
  values: Record<string, unknown>;
  onSaveDefinitions: (parameters: NotebookParameter[]) => Promise<void>;
  onSaveValues: (values: Record<string, unknown>) => Promise<void>;
}) {
  const [tab, setTab] = useState("values");
  const [draftParameters, setDraftParameters] = useState<NotebookParameter[]>([]);
  const [draftValues, setDraftValues] = useState<Record<string, unknown>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const wasOpenRef = useRef(false);

  useEffect(() => {
    if (open && !wasOpenRef.current) {
      const definitions = cloneParameters(parameters);
      setDraftParameters(definitions);
      setDraftValues(parameterValuesFrom(definitions, values));
      setTab(parameters.length === 0 ? "definitions" : "values");
      setError("");
    }
    wasOpenRef.current = open;
  }, [open, parameters, values]);

  const validationError = useMemo(
    () => authoredControlDefinitionsProblem(draftParameters),
    [draftParameters],
  );

  const saveDefinitions = async () => {
    if (validationError) {
      setError(validationError);
      return;
    }
    setBusy(true);
    setError("");
    try {
      await onSaveDefinitions(draftParameters);
      setDraftValues(parameterValuesFrom(draftParameters, {}));
      setTab("values");
    } catch (saveError) {
      setError(String(saveError));
    } finally {
      setBusy(false);
    }
  };

  const saveValues = async () => {
    setBusy(true);
    setError("");
    try {
      await onSaveValues(draftValues);
      onOpenChange(false);
    } catch (saveError) {
      setError(String(saveError));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[min(760px,calc(100vh-2rem))] min-w-0 flex-col overflow-hidden sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <SlidersHorizontal />
            Notebook controls
          </DialogTitle>
          <DialogDescription>
            Defaults are version-controlled. Current values are local to this Renart session.
          </DialogDescription>
        </DialogHeader>

        <Tabs value={tab} onValueChange={setTab} className="min-h-0 flex-1">
          <TabsList>
            <TabsTrigger value="values">Values</TabsTrigger>
            <TabsTrigger value="definitions">Definitions</TabsTrigger>
          </TabsList>

          <TabsContent value="values" className="min-h-0 flex-1 overflow-hidden">
            <ScrollArea className="h-full max-h-[52vh] pr-3">
              {draftParameters.length === 0 ? (
                <div className="rounded-lg border border-dashed p-6 text-center">
                  <p className="text-sm font-medium">No controls yet</p>
                  <p className="mt-1 text-xs text-muted-foreground">
                    Add a typed control, then use it from SQL, Python, sources, and charts.
                  </p>
                  <Button
                    className="mt-4"
                    size="sm"
                    variant="outline"
                    onClick={() => setTab("definitions")}
                  >
                    Add control
                  </Button>
                </div>
              ) : (
                <FieldGroup className="py-1">
                  {draftParameters.map((parameter) => (
                    <AuthoredControlValueField
                      key={parameter.id}
                      control={parameter}
                      value={draftValues[parameter.id] ?? parameter.default}
                      idScope="notebook-control-runtime"
                      onChange={(value) =>
                        setDraftValues((current) => ({ ...current, [parameter.id]: value }))
                      }
                    />
                  ))}
                </FieldGroup>
              )}
            </ScrollArea>
          </TabsContent>

          <TabsContent value="definitions" className="min-h-0 flex-1 overflow-hidden">
            <ScrollArea className="h-full max-h-[52vh] pr-3">
              <div className="flex flex-col gap-3 py-1">
                {draftParameters.map((parameter, index) => (
                  <AuthoredControlEditor
                    key={`${parameter.id}-${index}`}
                    control={parameter}
                    idPrefix={`notebook-control-${index}`}
                    onChange={(next) =>
                      setDraftParameters((current) =>
                        current.map((candidate, candidateIndex) =>
                          candidateIndex === index ? next : candidate,
                        ),
                      )
                    }
                    onRename={(id) =>
                      setDraftParameters((current) =>
                        current.map((candidate, candidateIndex) =>
                          candidateIndex === index ? { ...candidate, id } : candidate,
                        ),
                      )
                    }
                    onDelete={() =>
                      setDraftParameters((current) =>
                        current.filter((_, candidate) => candidate !== index),
                      )
                    }
                  />
                ))}

                <Button
                  type="button"
                  variant="outline"
                  className="w-full border-dashed"
                  onClick={() => {
                    const existing = new Set(draftParameters.map((parameter) => parameter.id));
                    let suffix = draftParameters.length + 1;
                    let id = `control_${suffix}`;
                    while (existing.has(id)) {
                      suffix += 1;
                      id = `control_${suffix}`;
                    }
                    setDraftParameters((current) => [
                      ...current,
                      { id, label: "", type: "text", default: "" },
                    ]);
                  }}
                >
                  <Plus />
                  Add control
                </Button>
              </div>
            </ScrollArea>
          </TabsContent>
        </Tabs>

        {error || (tab === "definitions" && validationError) ? (
          <FieldError>{error || validationError}</FieldError>
        ) : null}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          {tab === "definitions" ? (
            <Button onClick={() => void saveDefinitions()} disabled={busy || !!validationError}>
              Save controls
            </Button>
          ) : (
            <Button
              onClick={() => void saveValues()}
              disabled={busy || draftParameters.length === 0}
            >
              Apply values
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
