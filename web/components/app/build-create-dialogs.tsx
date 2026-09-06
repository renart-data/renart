import { useAtomValue } from "jotai";
import { AlertTriangle, CheckCircle2, FolderPlus, Plus } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { createAsset } from "@/lib/api-assets";
import {
  API_ASSET_TEMPLATES,
  buildAPIAssetTemplate,
  type APIAssetTemplateId,
} from "@/lib/api-asset-templates";
import { createPipeline, getPipelineTemplates } from "@/lib/api-pipelines";
import { selectedEnvironmentAtom, workspaceAtom } from "@/lib/atoms/domains/workspace";
import { assetCreationRole, type AssetCreationKind } from "@/lib/asset-creation-profile";
import { isLocalLoadConnection, loadTargetNeedsDestinationObject } from "@/lib/load-assets";
import type { AssetCreationCandidate, PipelineTemplateInfo } from "@/lib/types";
import { cn } from "@/lib/utils";
import { buildSuggestedAssetName } from "@/lib/workspace-shell-helpers";
import { useAssetCreationProfile } from "@/hooks/use-asset-creation-profile";

import { AssetConnectionField, resolveAssetConnectionSelection } from "./asset-connection-field";
import { AssetTypePreview } from "./asset-type-preview";
import { FilePathPicker } from "./file-path-picker";
import { LoadStreamPicker } from "./load-stream-picker";
import {
  SemanticAssetCreateFields,
  buildSemanticAssetCreatePayload,
  defaultSemanticAssetDraft,
  type SemanticAssetDraft,
  type SemanticAssetKind,
} from "./semantic-asset-create-fields";
import { TemplateCatalog } from "./template-catalog";
import { WorkspaceConnectionDialog } from "./workspace-connection-dialog";

// Asset kinds the creation dialog can produce, mapped to real backend create
// calls. Standalone: SQL/Python transforms, HTTP API, Seed, Sensor, and Load.
// Downstream assets can be SQL, Python, or Load, each depending on the source.
type AssetKindOption = {
  id: AssetCreationKind;
  label: string;
  description: string;
};

const CREATABLE_ASSETS: AssetKindOption[] = [
  { id: "sql", label: "SQL", description: "Transform with a SELECT" },
  { id: "python", label: "Python", description: "Custom Python transform" },
  {
    id: "api",
    label: "HTTP API",
    description: "Pull records from an HTTP API endpoint",
  },
  {
    id: "seed",
    label: "Seed",
    description: "Load a file into a table",
  },
  {
    id: "sensor",
    label: "Sensor",
    description: "Check an external readiness condition",
  },
  { id: "load", label: "Load", description: "Replicate data between connections" },
];

const DOWNSTREAM_ASSETS: AssetKindOption[] = [
  { id: "sql", label: "SQL", description: "select * from the upstream table" },
  { id: "python", label: "Python", description: "Read the upstream table from Python" },
  {
    id: "load",
    label: "Load",
    description: "Replicate downstream between connections",
  },
];

// A downstream asset reuses the source's prefix and appends _downstream, kept
// unique against existing names (the backend also requires a prefixed name).
function suggestDownstreamName(sourceName: string, existing: Set<string>): string {
  const parts = sourceName.split(".").filter(Boolean);
  const leaf = parts.pop() ?? "asset";
  const prefix = parts.join(".");
  const base = prefix ? `${prefix}.${leaf}_downstream` : `${leaf}_downstream`;
  if (!existing.has(base)) {
    return base;
  }
  let index = 2;
  while (existing.has(`${base}_${index}`)) {
    index += 1;
  }
  return `${base}_${index}`;
}

// suggestPrefixedAssetName seeds a unique name under an explicit prefix
// (from the canvas prefix-group the user right-clicked in).
function suggestPrefixedAssetName(
  kind: AssetCreationKind,
  prefix: string,
  existing: Set<string>,
): string {
  const base = `${prefix}.my_${kind}_asset_`;
  let index = 1;
  while (existing.has(`${base}${index}`)) {
    index += 1;
  }
  return `${base}${index}`;
}

type CreationConnectionRole = "target" | "source" | "destination";

function selectedCreationCandidate(
  candidates: AssetCreationCandidate[],
  variant: string,
): AssetCreationCandidate | undefined {
  if (variant) {
    return candidates.find((candidate) => candidate.variant === variant);
  }
  return candidates.length === 1 ? candidates[0] : undefined;
}

export function NewAssetDialog({
  open,
  onOpenChange,
  pipelineId,
  pipelineName,
  existingAssetNames,
  downstreamSource,
  namePrefix,
  initialExecutableContent,
  initialConnection,
  initialKind,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  pipelineId?: string;
  pipelineName?: string;
  existingAssetNames: Set<string>;
  downstreamSource?: { id: string; name: string; connection?: string } | null;
  namePrefix?: string | null;
  initialExecutableContent?: string | null;
  initialConnection?: string | null;
  initialKind?: AssetCreationKind;
  onCreated?: (assetId: string) => void;
}) {
  const [kind, setKind] = useState<AssetCreationKind>("sql");
  const [name, setName] = useState("");
  const [connection, setConnection] = useState("");
  const [sourceConnection, setSourceConnection] = useState("");
  const [sourceTable, setSourceTable] = useState("");
  const [destinationObject, setDestinationObject] = useState("");
  const [apiTemplate, setAPITemplate] = useState<APIAssetTemplateId>("openapi");
  const [openapiSpecURL, setOpenAPISpecURL] = useState("");
  const [sensorVariant, setSensorVariant] = useState("");
  const [semanticDraft, setSemanticDraft] = useState<SemanticAssetDraft>(() =>
    defaultSemanticAssetDraft("seed", [], {}),
  );
  const [newConnectionRole, setNewConnectionRole] = useState<CreationConnectionRole | null>(null);
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState("");
  const [kindPickerExpanded, setKindPickerExpanded] = useState(true);
  const resetModeRef = useRef<string | null>(null);

  const workspace = useAtomValue(workspaceAtom);
  const environment = useAtomValue(selectedEnvironmentAtom);
  const {
    profile,
    loading: profileLoading,
    error: profileError,
    refresh: refreshProfile,
  } = useAssetCreationProfile(pipelineId, open);
  const semanticCapabilities = useMemo(
    () => workspace?.asset_capabilities ?? [],
    [workspace?.asset_capabilities],
  );
  const semanticConnections = useMemo(() => workspace?.connections ?? {}, [workspace?.connections]);

  const isDownstream = Boolean(downstreamSource);
  const options = isDownstream ? DOWNSTREAM_ASSETS : CREATABLE_ASSETS;
  const selected = options.find((option) => option.id === kind) ?? options[0];
  const targetRoleName = selected.id === "load" ? "destination" : "target";
  const targetRole = assetCreationRole(profile, selected.id, targetRoleName);
  const sourceRole = assetCreationRole(profile, selected.id, "source");
  const currentConnectionType = connection
    ? (workspace?.connections?.[connection] ?? undefined)
    : undefined;
  const targetSelection = useMemo(
    () => resolveAssetConnectionSelection(targetRole, connection, currentConnectionType),
    [connection, currentConnectionType, targetRole],
  );
  const candidate = useMemo(
    () => selectedCreationCandidate(targetSelection?.candidates ?? [], sensorVariant),
    [sensorVariant, targetSelection?.candidates],
  );
  const sourceSelection = useMemo(
    () =>
      resolveAssetConnectionSelection(
        sourceRole,
        sourceConnection,
        sourceConnection ? workspace?.connections?.[sourceConnection] : undefined,
      ),
    [sourceConnection, sourceRole, workspace?.connections],
  );
  const targetConnectionProfile = targetRole?.connections.find(
    (item) => item.name === targetSelection?.name,
  );
  const targetNeedsDestinationObject = loadTargetNeedsDestinationObject(
    targetConnectionProfile?.category ?? "",
  );
  const isDownstreamSQL = isDownstream && selected.id === "sql";

  // Seed a unique, prefixed name suggestion (the backend requires a prefix).
  const suggestedName = useMemo(() => {
    if (isDownstream && downstreamSource) {
      return suggestDownstreamName(downstreamSource.name, existingAssetNames);
    }
    if (namePrefix) {
      return suggestPrefixedAssetName(selected.id, namePrefix, existingAssetNames);
    }
    return buildSuggestedAssetName(selected.id, existingAssetNames, pipelineName);
  }, [isDownstream, downstreamSource, namePrefix, selected.id, existingAssetNames, pipelineName]);

  // Reset to a valid kind whenever the dialog (or its mode) opens.
  useEffect(() => {
    if (!open) {
      resetModeRef.current = null;
      return;
    }
    const resetMode = isDownstream ? "downstream" : "standalone";
    if (resetModeRef.current === resetMode) return;
    resetModeRef.current = resetMode;
    setKind(initialKind ?? "sql");
    setConnection(initialConnection?.trim() || downstreamSource?.connection?.trim() || "");
    setSourceConnection(downstreamSource?.connection?.trim() || "");
    setSourceTable("");
    setDestinationObject("");
    setAPITemplate("openapi");
    setOpenAPISpecURL("");
    setSensorVariant("");
    setKindPickerExpanded(true);
    setNewConnectionRole(null);
    setSemanticDraft(defaultSemanticAssetDraft("seed", semanticCapabilities, semanticConnections));
    setError("");
  }, [
    downstreamSource?.connection,
    initialConnection,
    initialKind,
    isDownstream,
    open,
    semanticCapabilities,
    semanticConnections,
  ]);
  useEffect(() => {
    if (open) {
      setName(suggestedName);
    }
  }, [open, suggestedName]);

  const semanticKind: SemanticAssetKind | null =
    selected.id === "seed" || selected.id === "sensor" ? selected.id : null;
  useEffect(() => {
    if (!open || !targetSelection || targetSelection.incompatible) return;
    const candidates = targetSelection.candidates;
    if (selected.id === "sensor") {
      const nextVariant = candidates.some((item) => item.variant === sensorVariant)
        ? sensorVariant
        : (candidates[0]?.variant ?? "");
      if (nextVariant !== sensorVariant) setSensorVariant(nextVariant);
    }
    const nextCandidate = selectedCreationCandidate(
      candidates,
      selected.id === "sensor"
        ? candidates.some((item) => item.variant === sensorVariant)
          ? sensorVariant
          : (candidates[0]?.variant ?? "")
        : "",
    );
    if (!semanticKind || !nextCandidate) return;
    setSemanticDraft((current) => ({
      ...current,
      assetType: nextCandidate.asset_type,
      connection,
    }));
  }, [connection, open, selected.id, semanticKind, sensorVariant, targetSelection]);

  const openConnectionDialog = (role: CreationConnectionRole) => {
    setNewConnectionRole(role);
  };

  const manageConnections = () => {
    onOpenChange(false);
    window.location.assign("/project/connections");
  };

  const newConnectionProfile = newConnectionRole
    ? assetCreationRole(profile, selected.id, newConnectionRole)
    : undefined;

  const selectCreatedConnection = async (connectionName: string) => {
    if (!pipelineId || !newConnectionRole) return;
    await refreshProfile();
    if (newConnectionRole === "source") {
      setSourceConnection(connectionName);
    } else {
      setConnection(connectionName);
    }
  };

  const create = async () => {
    const trimmed = name.trim();
    if (!trimmed) {
      setError("Asset name is required.");
      return;
    }
    if (!pipelineId) {
      setError("Select a pipeline before creating an asset.");
      return;
    }
    if (existingAssetNames.has(trimmed)) {
      setError(`An asset named "${trimmed}" already exists.`);
      return;
    }
    if (!targetSelection || targetSelection.incompatible || !candidate) {
      setError("Choose a compatible connection and asset variant.");
      return;
    }
    const semanticResult = semanticKind
      ? buildSemanticAssetCreatePayload(
          semanticKind,
          { ...semanticDraft, assetType: candidate.asset_type, connection },
          semanticCapabilities,
          trimmed,
        )
      : null;
    if (semanticResult?.error) {
      setError(semanticResult.error);
      return;
    }
    if (selected.id === "load") {
      if (!sourceConnection.trim()) {
        setError("A source connection is required for a Load asset.");
        return;
      }
      if (!sourceSelection || sourceSelection.incompatible) {
        setError("Choose a compatible source connection for this Load asset.");
        return;
      }
      if (!isDownstream && !sourceTable.trim()) {
        setError("A source table or object is required for a Load asset.");
        return;
      }
    }
    if (selected.id === "load" && targetNeedsDestinationObject && !destinationObject.trim()) {
      setError("This target connection requires a destination object or file path.");
      return;
    }
    if (selected.id === "api" && apiTemplate === "openapi" && !openapiSpecURL.trim()) {
      setError("An OpenAPI spec URL is required for this API source.");
      return;
    }

    let input: Parameters<typeof createAsset>[1] = {
      name: trimmed,
      kind: selected.id as "sql" | "python" | "api" | "load" | "seed" | "sensor",
      connection,
      environment: profile?.environment || environment || undefined,
      use_pipeline_default: !connection,
      ...(isDownstream && downstreamSource ? { source_asset_id: downstreamSource.id } : {}),
    };
    if (selected.id === "sql" && initialExecutableContent?.trim()) {
      input = { ...input, executable_content: initialExecutableContent };
    }
    if (selected.id === "api") {
      input = {
        ...input,
        content: buildAPIAssetTemplate(apiTemplate, connection, openapiSpecURL),
      };
    }
    if (selected.id === "load") {
      input = {
        ...input,
        parameters: {
          source_connection: sourceConnection.trim(),
          ...(isDownstream ? {} : { source_table: sourceTable.trim() }),
          ...(targetNeedsDestinationObject && destinationObject.trim()
            ? { destination_object: destinationObject.trim() }
            : {}),
        },
      };
    }
    let seedFile: File | undefined;
    if (semanticResult?.payload) {
      const { seedFile: payloadFile, parameters } = semanticResult.payload;
      seedFile = payloadFile;
      input = {
        ...input,
        parameters,
        ...(selected.id === "sensor" ? { variant: candidate.variant } : {}),
      };
    }
    setCreating(true);
    setError("");
    try {
      const response = await createAsset(pipelineId, input, seedFile ? { seedFile } : undefined);
      onOpenChange(false);
      if (response.asset_id) {
        onCreated?.(response.asset_id);
      }
    } catch (caught) {
      setError(String(caught));
    } finally {
      setCreating(false);
    }
  };

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="flex max-h-[90dvh] min-w-0 flex-col overflow-hidden sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <Plus className="size-4 text-primary" />
              {isDownstream ? "New downstream asset" : "New asset"}
            </DialogTitle>
            <DialogDescription>
              {isDownstream && downstreamSource ? (
                <>
                  Depends on <span className="font-mono">{downstreamSource.name}</span>.
                </>
              ) : (
                <>
                  Create an asset in{" "}
                  {pipelineName ? (
                    <span className="font-mono">{pipelineName}</span>
                  ) : (
                    "this pipeline"
                  )}
                  .
                </>
              )}
            </DialogDescription>
          </DialogHeader>
          <ScrollArea className="min-h-0 min-w-0 flex-1" viewportClassName="p-1">
            <div className="grid min-w-0 gap-5">
              <div className="min-w-0">
                <div
                  className={cn(
                    "grid min-w-0 transition-[grid-template-rows,opacity] duration-300 ease-out motion-reduce:transition-none",
                    kindPickerExpanded
                      ? "grid-rows-[1fr] opacity-100"
                      : "pointer-events-none grid-rows-[0fr] opacity-0",
                  )}
                  aria-hidden={!kindPickerExpanded}
                  inert={!kindPickerExpanded}
                >
                  <div className="min-h-0 min-w-0 overflow-hidden p-1">
                    <ToggleGroup
                      type="single"
                      variant="outline"
                      value={selected.id}
                      onValueChange={(nextKind) => {
                        setKindPickerExpanded(false);
                        if (!nextKind) return;
                        const next = nextKind as AssetCreationKind;
                        setKind(next);
                        setSensorVariant("");
                        setConnection(
                          next === "sql" && initialConnection?.trim()
                            ? initialConnection.trim()
                            : (next === "sql" || next === "python") && downstreamSource?.connection
                              ? downstreamSource.connection
                              : "",
                        );
                        if (next === "load") {
                          setSourceConnection(downstreamSource?.connection?.trim() || "");
                        }
                        if (next === "seed" || next === "sensor") {
                          setSemanticDraft(
                            defaultSemanticAssetDraft(
                              next,
                              semanticCapabilities,
                              semanticConnections,
                            ),
                          );
                        }
                      }}
                      className="grid w-full min-w-0 grid-cols-2 items-stretch gap-2 sm:grid-cols-3"
                    >
                      {options.map((option) => (
                        <ToggleGroupItem
                          key={option.id}
                          value={option.id}
                          aria-label={option.label}
                          className="h-24 w-full min-w-0 flex-col items-start justify-start overflow-hidden p-3 text-left data-[state=on]:border-primary data-[state=on]:ring-1 data-[state=on]:ring-primary"
                        >
                          <AssetTypePreview type={option.id} className="h-8 max-w-14 shrink-0" />
                          <div className="grid w-full min-w-0 gap-0.5">
                            <div data-slot="asset-kind-label" className="truncate font-medium">
                              {option.label}
                            </div>
                            <div
                              data-slot="asset-kind-description"
                              className="truncate text-xs text-muted-foreground"
                              title={option.description}
                            >
                              {option.description}
                            </div>
                          </div>
                        </ToggleGroupItem>
                      ))}
                    </ToggleGroup>
                  </div>
                </div>
                <div
                  className={cn(
                    "grid min-w-0 transition-[grid-template-rows,opacity] duration-300 ease-out motion-reduce:transition-none",
                    kindPickerExpanded
                      ? "pointer-events-none grid-rows-[0fr] opacity-0"
                      : "grid-rows-[1fr] opacity-100",
                  )}
                  aria-hidden={kindPickerExpanded}
                  inert={kindPickerExpanded}
                >
                  <div className="min-h-0 min-w-0 overflow-hidden p-1">
                    <div className="flex min-w-0 items-center gap-2 rounded-md border bg-muted/20 px-2.5 py-2">
                      <AssetTypePreview type={selected.id} className="h-8 w-12 shrink-0" />
                      <div className="min-w-0 flex-1">
                        <div className="text-xs font-medium">{selected.label}</div>
                        <div className="truncate text-[11px] text-muted-foreground">
                          {selected.description}
                        </div>
                      </div>
                      <Button
                        type="button"
                        variant="ghost"
                        size="xs"
                        className="shrink-0"
                        onClick={() => setKindPickerExpanded(true)}
                      >
                        Change type
                      </Button>
                    </div>
                  </div>
                </div>
              </div>
              <Field variant="plain">
                <FieldLabel htmlFor="new-asset-name">Asset name</FieldLabel>
                <Input
                  id="new-asset-name"
                  className="font-mono"
                  placeholder="analytics.my_asset"
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" && !creating) {
                      void create();
                    }
                  }}
                  autoFocus
                />
                <FieldDescription>
                  Use a <span className="font-mono">prefix.name</span> to group it under{" "}
                  <span className="font-mono">assets/prefix/</span>.
                </FieldDescription>
              </Field>
              {profileLoading && !profile ? (
                <div className="grid gap-2" aria-label="Loading compatible connections">
                  <Skeleton className="h-7 w-full" />
                  <Skeleton className="h-14 w-full" />
                </div>
              ) : null}
              {profileError ? (
                <Alert variant="destructive">
                  <AlertTriangle />
                  <AlertTitle>Could not load connections</AlertTitle>
                  <AlertDescription>{profileError}</AlertDescription>
                </Alert>
              ) : null}
              {selected.id === "load" ? (
                <FieldGroup>
                  {isDownstream ? (
                    <Field variant="plain">
                      <FieldLabel htmlFor="new-load-source-connection">
                        Source connection
                      </FieldLabel>
                      <Input
                        id="new-load-source-connection"
                        value={sourceConnection}
                        className="font-mono"
                        disabled
                      />
                      <FieldDescription>
                        Uses the selected upstream asset&apos;s effective target connection.
                      </FieldDescription>
                    </Field>
                  ) : (
                    <>
                      <AssetConnectionField
                        id="new-load-source-connection"
                        label="Source connection"
                        role={sourceRole}
                        value={sourceConnection}
                        currentConnectionType={workspace?.connections?.[sourceConnection]}
                        onChange={setSourceConnection}
                        onNewConnection={() => openConnectionDialog("source")}
                        onManageConnections={manageConnections}
                      />
                      <Field variant="plain">
                        <FieldLabel htmlFor="new-load-source-table">
                          {isLocalLoadConnection(sourceConnection)
                            ? "Source file"
                            : "Source table or object"}
                        </FieldLabel>
                        {isLocalLoadConnection(sourceConnection) ? (
                          <FilePathPicker
                            id="new-load-source-table"
                            variant="field"
                            ariaLabel="Choose source file"
                            placeholder="data/orders.csv"
                            value={sourceTable}
                            onCommit={setSourceTable}
                          />
                        ) : (
                          <LoadStreamPicker
                            id="new-load-source-table"
                            value={sourceTable}
                            connection={sourceSelection?.name ?? sourceConnection}
                            environment={profile?.environment || environment || undefined}
                            placeholder="public.orders or path/to/object"
                            ariaLabel="Source table or object"
                            variant="field"
                            onCommit={setSourceTable}
                          />
                        )}
                      </Field>
                    </>
                  )}
                </FieldGroup>
              ) : null}
              {!isDownstreamSQL ? (
                <AssetConnectionField
                  id="new-asset-connection"
                  label={
                    selected.id === "sensor"
                      ? "Connection to check"
                      : selected.id === "api" || selected.id === "load"
                        ? "Destination connection"
                        : "Target connection"
                  }
                  role={targetRole}
                  value={connection}
                  currentConnectionType={currentConnectionType}
                  onChange={setConnection}
                  onNewConnection={() => openConnectionDialog(targetRoleName)}
                  onManageConnections={manageConnections}
                />
              ) : !targetSelection || targetSelection.incompatible || !candidate ? (
                <Alert variant="destructive">
                  <AlertTriangle />
                  <AlertTitle>SQL downstream unavailable</AlertTitle>
                  <AlertDescription>
                    A SQL downstream must use the upstream asset&apos;s warehouse. Choose Python or
                    Load when the data needs to move to another connection.
                  </AlertDescription>
                </Alert>
              ) : null}
              {selected.id === "sensor" && (targetSelection?.candidates.length ?? 0) > 0 ? (
                <Field variant="plain">
                  <FieldLabel htmlFor="new-sensor-variant">Condition</FieldLabel>
                  <Select value={sensorVariant} onValueChange={setSensorVariant}>
                    <SelectTrigger id="new-sensor-variant" className="w-full">
                      <SelectValue placeholder="Choose a condition" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectGroup>
                        {targetSelection?.candidates.map((item) => (
                          <SelectItem key={item.asset_type} value={item.variant ?? item.asset_type}>
                            {item.variant === "query"
                              ? "Query returns true"
                              : item.variant === "table"
                                ? "Table exists"
                                : item.variant === "key"
                                  ? "Object key exists"
                                  : item.operator}
                          </SelectItem>
                        ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                </Field>
              ) : null}
              {selected.id === "api" ? (
                <FieldGroup>
                  <Field variant="plain">
                    <FieldLabel htmlFor="new-api-template">API source</FieldLabel>
                    <Select
                      value={apiTemplate}
                      onValueChange={(value) => setAPITemplate(value as APIAssetTemplateId)}
                    >
                      <SelectTrigger id="new-api-template" className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          {API_ASSET_TEMPLATES.map((template) => (
                            <SelectItem key={template.id} value={template.id}>
                              {template.label}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <FieldDescription>
                      {
                        API_ASSET_TEMPLATES.find((template) => template.id === apiTemplate)
                          ?.description
                      }
                    </FieldDescription>
                  </Field>
                  {apiTemplate === "openapi" ? (
                    <Field variant="plain">
                      <FieldLabel htmlFor="new-api-openapi-url">OpenAPI spec URL</FieldLabel>
                      <Input
                        id="new-api-openapi-url"
                        type="url"
                        className="font-mono"
                        value={openapiSpecURL}
                        placeholder="https://api.example.com/openapi.json"
                        onChange={(event) => setOpenAPISpecURL(event.target.value)}
                      />
                      <FieldDescription>
                        Renart uses the spec for endpoint, parameter, and response-field
                        suggestions.
                      </FieldDescription>
                    </Field>
                  ) : null}
                </FieldGroup>
              ) : null}
              {semanticKind ? (
                <SemanticAssetCreateFields
                  kind={semanticKind}
                  capabilities={semanticCapabilities}
                  value={semanticDraft}
                  onChange={setSemanticDraft}
                />
              ) : null}
              {selected.id === "load" && targetNeedsDestinationObject ? (
                <Field variant="plain">
                  <FieldLabel htmlFor="new-load-destination-object">Destination object</FieldLabel>
                  {isLocalLoadConnection(targetSelection?.name ?? "") ? (
                    <FilePathPicker
                      id="new-load-destination-object"
                      variant="field"
                      ariaLabel="Choose destination file"
                      placeholder="data/orders.csv"
                      value={destinationObject}
                      onCommit={setDestinationObject}
                    />
                  ) : (
                    <LoadStreamPicker
                      id="new-load-destination-object"
                      value={destinationObject}
                      connection={targetSelection?.name ?? connection}
                      environment={profile?.environment || environment || undefined}
                      placeholder="path/to/object"
                      ariaLabel="Destination object"
                      mode="destination"
                      variant="field"
                      onCommit={setDestinationObject}
                    />
                  )}
                </Field>
              ) : null}
              {error ? (
                <Alert variant="destructive">
                  <AlertTriangle />
                  <AlertTitle>Could not create asset</AlertTitle>
                  <AlertDescription>{error}</AlertDescription>
                </Alert>
              ) : null}
            </div>
          </ScrollArea>
          <DialogFooter>
            <Button variant="outline" onClick={() => onOpenChange(false)} disabled={creating}>
              Cancel
            </Button>
            <Button
              onClick={() => void create()}
              disabled={creating || !pipelineId || profileLoading || !candidate}
            >
              {creating ? (
                <Spinner data-icon="inline-start" />
              ) : (
                <CheckCircle2 data-icon="inline-start" />
              )}
              Create
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      {newConnectionRole && newConnectionProfile ? (
        <WorkspaceConnectionDialog
          open
          onOpenChange={(nextOpen) => {
            if (!nextOpen) setNewConnectionRole(null);
          }}
          environment={profile?.environment || environment || "default"}
          connectionTypes={newConnectionProfile.connection_types}
          requestedConnectionType={
            newConnectionRole === "source"
              ? sourceSelection?.connectionType
              : targetSelection?.connectionType
          }
          onCreated={selectCreatedConnection}
        />
      ) : null}
    </>
  );
}

// NewPipelineDialog creates either an empty pipeline or one of the backend-owned
// demo scaffolds. The workspace SSE update then lists it and the page navigates
// onto it.
export function NewPipelineDialog({
  open,
  onOpenChange,
  existingPaths,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  existingPaths: Set<string>;
  onCreated: (path: string) => void;
}) {
  const [path, setPath] = useState("");
  const [name, setName] = useState("");
  const [templateId, setTemplateId] = useState("blank");
  const [templates, setTemplates] = useState<PipelineTemplateInfo[]>([]);
  const [templatesLoading, setTemplatesLoading] = useState(false);
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!open) return;

    let active = true;
    setPath("");
    setName("");
    setTemplateId("blank");
    setError("");
    setTemplatesLoading(true);
    void getPipelineTemplates()
      .then((response) => {
        if (active) setTemplates(response.templates);
      })
      .catch((caught) => {
        if (active) setError(`Could not load pipeline templates: ${String(caught)}`);
      })
      .finally(() => {
        if (active) setTemplatesLoading(false);
      });

    return () => {
      active = false;
    };
  }, [open]);

  const templateCatalogItems = useMemo(
    () =>
      templates.map((template) => ({
        id: template.id,
        title: template.title,
        description: template.description,
        category: template.category,
        offline: template.offline,
        features: template.features,
        assetNames: template.asset_names,
      })),
    [templates],
  );

  const chooseTemplate = (template: PipelineTemplateInfo) => {
    const previous = templates.find((candidate) => candidate.id === templateId);
    const blank = template.id === "blank";
    setPath((current) =>
      !current || current === previous?.suggested_path
        ? blank
          ? ""
          : template.suggested_path
        : current,
    );
    setName((current) =>
      !current || current === previous?.title ? (blank ? "" : template.title) : current,
    );
    setTemplateId(template.id);
  };

  const create = async () => {
    const trimmedPath = path.trim().replace(/^\/+|\/+$/g, "");
    if (!trimmedPath) {
      setError("Pipeline directory is required.");
      return;
    }
    if (/\s/.test(trimmedPath) || trimmedPath.includes("..")) {
      setError("Use a relative directory path without spaces.");
      return;
    }
    if (
      [...existingPaths].some(
        (existing) => existing === trimmedPath || existing.startsWith(`${trimmedPath}/`),
      )
    ) {
      setError(`A pipeline already exists at "${trimmedPath}".`);
      return;
    }
    setCreating(true);
    setError("");
    try {
      await createPipeline({
        path: trimmedPath,
        name: name.trim() || undefined,
        template: templateId,
      });
      onOpenChange(false);
      onCreated(trimmedPath);
    } catch (caught) {
      setError(String(caught));
    } finally {
      setCreating(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[min(88vh,46rem)] flex-col overflow-hidden sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Plus className="size-4 text-primary" />
            New pipeline
          </DialogTitle>
          <DialogDescription>
            Start with an empty canvas or a runnable demo that showcases a Renart workflow. Every
            option creates ordinary pipeline files in your repository.
          </DialogDescription>
        </DialogHeader>
        <ScrollArea className="-mx-1 min-h-0 flex-1 px-1">
          <div className="grid gap-5 pb-1">
            <FieldGroup>
              <Field variant="plain">
                <FieldLabel>Starter</FieldLabel>
                <FieldDescription>
                  Offline starters need no network access. Network starters call a service or may
                  install dependencies.
                </FieldDescription>
                <TemplateCatalog
                  items={templateCatalogItems}
                  selectedId={templateId}
                  ariaLabel="Pipeline starter"
                  loading={templatesLoading}
                  onSelect={(item) => {
                    const template = templates.find((candidate) => candidate.id === item.id);
                    if (template) chooseTemplate(template);
                  }}
                />
              </Field>
              <Field variant="plain">
                <FieldLabel htmlFor="new-pipeline-path">Directory</FieldLabel>
                <Input
                  id="new-pipeline-path"
                  className="font-mono"
                  placeholder="marketing_pipeline"
                  value={path}
                  onChange={(event) => setPath(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" && !creating) {
                      void create();
                    }
                  }}
                  autoFocus
                />
              </Field>
              <Field variant="plain">
                <FieldLabel htmlFor="new-pipeline-name">Pipeline name (optional)</FieldLabel>
                <Input
                  id="new-pipeline-name"
                  placeholder="Marketing"
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" && !creating) {
                      void create();
                    }
                  }}
                />
              </Field>
            </FieldGroup>
            {error ? (
              <Alert variant="destructive">
                <AlertTriangle />
                <AlertTitle>Could not create pipeline</AlertTitle>
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            ) : null}
          </div>
        </ScrollArea>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={creating}>
            Cancel
          </Button>
          <Button onClick={() => void create()} disabled={creating || templatesLoading}>
            {creating ? <Spinner className="size-4" /> : <CheckCircle2 className="size-4" />}Create
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// NewFolderDialog asks for a folder (prefix) name and chains into the asset
// dialog: folders are asset-name prefixes (assets/<folder>/), so a folder
// appears once its first asset is created inside it.
export function NewFolderDialog({
  open,
  onOpenChange,
  pipelineName,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  pipelineName?: string;
  onConfirm: (prefix: string) => void;
}) {
  const [folder, setFolder] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    if (open) {
      setFolder("");
      setError("");
    }
  }, [open]);

  const confirm = () => {
    const trimmed = folder.trim().replace(/^\.+|\.+$/g, "");
    if (!trimmed) {
      setError("Folder name is required.");
      return;
    }
    if (!/^[a-z0-9_]+(\.[a-z0-9_]+)*$/i.test(trimmed)) {
      setError("Use letters, digits and underscores; separate nested folders with dots.");
      return;
    }
    onConfirm(trimmed);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <FolderPlus className="size-4 text-primary" />
            New folder
          </DialogTitle>
          <DialogDescription>
            Folders group assets under <span className="font-mono">assets/&lt;folder&gt;/</span>
            {pipelineName ? (
              <>
                {" "}
                in <span className="font-mono">{pipelineName}</span>
              </>
            ) : null}
            . The folder is created together with its first asset.
          </DialogDescription>
        </DialogHeader>
        <div className="grid gap-2">
          <Label htmlFor="new-folder-name">Folder name</Label>
          <Input
            id="new-folder-name"
            className="font-mono"
            placeholder="analytics"
            value={folder}
            onChange={(event) => setFolder(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                confirm();
              }
            }}
            autoFocus
          />
          {error ? (
            <div className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-500/25 dark:bg-red-500/10 dark:text-red-300">
              {error}
            </div>
          ) : null}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button onClick={confirm}>
            <FolderPlus className="size-4" />
            Choose first asset
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
