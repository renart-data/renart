import { AlertTriangle, Database, Download, Loader2 } from "lucide-react";
import { useEffect, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldContent, FieldDescription, FieldLabel } from "@/components/ui/field";
import { createDataBrowserSource } from "@/lib/api-data-browser";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  importExternalRelation,
  previewExternalRelationImport,
  type ExternalRelationImportResult,
} from "@/lib/api-pipelines";

export function ExternalRelationImportDialog({
  pipelineId,
  relationId,
  dataBrowserSource,
  onOpenChange,
  onImported,
}: {
  pipelineId: string;
  relationId?: string | null;
  dataBrowserSource?: { object_id: string; environment: string } | null;
  onOpenChange: (open: boolean) => void;
  onImported?: (result: ExternalRelationImportResult) => void | Promise<void>;
}) {
  const [includeColumns, setIncludeColumns] = useState(true);
  const [preview, setPreview] = useState<ExternalRelationImportResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [importing, setImporting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const referenceId = dataBrowserSource?.object_id ?? relationId;

  useEffect(() => {
    if (!referenceId) {
      setPreview(null);
      setError(null);
      setIncludeColumns(true);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    const request = dataBrowserSource
      ? createDataBrowserSource(
          pipelineId,
          { ...dataBrowserSource, include_columns: includeColumns },
          true,
        )
      : previewExternalRelationImport(pipelineId, referenceId, includeColumns);
    void request
      .then((result) => {
        if (!cancelled) setPreview(result);
      })
      .catch((cause) => {
        if (!cancelled) {
          setPreview(null);
          setError(cause instanceof Error ? cause.message : "Could not preview the import.");
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [includeColumns, pipelineId, referenceId, dataBrowserSource]);

  const confirmImport = async () => {
    if (!referenceId || !preview) return;
    setImporting(true);
    setError(null);
    try {
      const result = dataBrowserSource
        ? await createDataBrowserSource(pipelineId, {
            ...dataBrowserSource,
            include_columns: includeColumns,
          })
        : await importExternalRelation(pipelineId, referenceId, includeColumns);
      await onImported?.(result);
      onOpenChange(false);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Could not import the source asset.");
    } finally {
      setImporting(false);
    }
  };

  return (
    <Dialog
      open={Boolean(referenceId)}
      onOpenChange={(open) => {
        if (!importing) onOpenChange(open);
      }}
    >
      <DialogContent className="grid max-h-[calc(100dvh-2rem)] grid-rows-[auto_minmax(0,1fr)_auto] overflow-hidden sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Database className="size-4 text-primary" />
            {dataBrowserSource ? "Create source asset" : "Import external relation"}
          </DialogTitle>
          <DialogDescription>
            Review the source asset before saving. This references the existing table; no data is
            copied or executed.
          </DialogDescription>
        </DialogHeader>

        <ScrollArea className="-mx-1 min-h-0 px-1">
          <div className="space-y-4 pb-1">
            {loading && !preview ? (
              <div className="flex items-center gap-2 py-8 text-muted-foreground">
                <Loader2 className="size-4 animate-spin" />
                Discovering the selected relation…
              </div>
            ) : null}
            {preview ? (
              <>
                <div className="flex flex-wrap items-center gap-2">
                  <Badge variant="outline">{preview.relation.connection}</Badge>
                  {preview.relation.environment ? (
                    <Badge variant="secondary">{preview.relation.environment}</Badge>
                  ) : null}
                  {preview.relation.observed_at ? (
                    <time
                      dateTime={preview.relation.observed_at}
                      title={new Date(preview.relation.observed_at).toLocaleString()}
                      className="text-xs text-muted-foreground"
                    >
                      Observed {new Date(preview.relation.observed_at).toLocaleString()}
                      {preview.relation.stale ? " · stale" : ""}
                    </time>
                  ) : null}
                  <span className="font-mono text-xs">{preview.relation.qualified_name}</span>
                </div>
                <dl className="grid gap-3 rounded-lg border bg-muted/20 p-3 sm:grid-cols-2">
                  <div className="min-w-0">
                    <dt className="text-[11px] text-muted-foreground">Asset name</dt>
                    <dd className="truncate font-mono text-xs" title={preview.asset.name}>
                      {preview.asset.name}
                    </dd>
                  </div>
                  <div className="min-w-0">
                    <dt className="text-[11px] text-muted-foreground">Asset type</dt>
                    <dd className="truncate font-mono text-xs">{preview.asset.type}</dd>
                  </div>
                  <div className="min-w-0 sm:col-span-2">
                    <dt className="text-[11px] text-muted-foreground">Proposed file</dt>
                    <dd className="truncate font-mono text-xs" title={preview.asset.path}>
                      {preview.asset.path}
                    </dd>
                  </div>
                </dl>
                <Field orientation="horizontal">
                  <Checkbox
                    id="external-relation-import-columns"
                    checked={includeColumns}
                    onCheckedChange={(checked) => setIncludeColumns(checked === true)}
                    disabled={loading || importing}
                  />
                  <FieldContent>
                    <FieldLabel htmlFor="external-relation-import-columns">
                      Import observed columns
                    </FieldLabel>
                    <FieldDescription>
                      Enabled by default so downstream SQL has version-controlled column and type
                      intelligence.
                    </FieldDescription>
                  </FieldContent>
                </Field>
                {includeColumns ? (
                  <div className="rounded-lg border">
                    <div className="border-b bg-muted/30 px-3 py-2 text-xs font-medium">
                      Columns ({preview.asset.columns.length})
                    </div>
                    {preview.asset.columns.length > 0 ? (
                      <ScrollArea viewportClassName="max-h-48">
                        <ul className="divide-y">
                          {preview.asset.columns.map((column) => (
                            <li
                              key={column.name}
                              className="flex items-center justify-between gap-3 px-3 py-1.5 font-mono text-xs"
                            >
                              <span className="min-w-0 truncate">{column.name}</span>
                              <span className="shrink-0 text-muted-foreground">
                                {column.type || "unknown"}
                              </span>
                            </li>
                          ))}
                        </ul>
                      </ScrollArea>
                    ) : (
                      <p className="px-3 py-2 text-xs text-muted-foreground">
                        The connection returned no column metadata. The asset can still be imported.
                      </p>
                    )}
                  </div>
                ) : null}
                {preview.warnings.map((warning) => (
                  <Alert key={`${warning.table}:${warning.warning}`}>
                    <AlertTriangle />
                    <AlertTitle>Import warning</AlertTitle>
                    <AlertDescription>{warning.warning}</AlertDescription>
                  </Alert>
                ))}
              </>
            ) : null}
            {error ? (
              <Alert variant="destructive">
                <AlertTriangle />
                <AlertTitle>Import unavailable</AlertTitle>
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            ) : null}
          </div>
        </ScrollArea>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={importing}>
            Cancel
          </Button>
          <Button onClick={() => void confirmImport()} disabled={!preview || loading || importing}>
            {importing ? <Loader2 className="animate-spin" /> : <Download />}
            {dataBrowserSource ? "Create source asset" : "Import asset"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
