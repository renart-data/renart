"use client";

import { useAtomValue } from "jotai";
import { useId, useMemo, useState } from "react";
import { AlertTriangle } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field";
import { Skeleton } from "@/components/ui/skeleton";
import { useAssetCreationProfile } from "@/hooks/use-asset-creation-profile";
import { updateAsset } from "@/lib/api-assets";
import {
  assetCreationKindForType,
  assetCreationRole,
  candidateForExistingAsset,
  roleForExistingAsset,
} from "@/lib/asset-creation-profile";
import { workspaceAtom } from "@/lib/atoms/domains/workspace";
import type { AssetCreationCandidate, AssetCreationRoleProfile, WebAsset } from "@/lib/types";

import { AssetConnectionField, resolveAssetConnectionSelection } from "./asset-connection-field";
import { WorkspaceConnectionDialog } from "./workspace-connection-dialog-lazy";

type PendingMigration = {
  value: string;
  connectionName: string;
  candidate: AssetCreationCandidate;
};

export function AssetConnectionEditor({
  asset,
  pipelineId,
}: {
  asset: WebAsset;
  pipelineId: string;
}) {
  const id = `${useId()}-asset-connection`;
  const workspace = useAtomValue(workspaceAtom);
  const kind = assetCreationKindForType(asset.type);
  const {
    profile,
    loading,
    error: profileError,
    refresh,
  } = useAssetCreationProfile(pipelineId, Boolean(kind));
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState("");
  const [newConnectionOpen, setNewConnectionOpen] = useState(false);
  const [pendingMigration, setPendingMigration] = useState<PendingMigration | null>(null);
  const capabilities = useMemo(
    () => workspace?.asset_capabilities ?? [],
    [workspace?.asset_capabilities],
  );
  const roleName =
    kind === "load"
      ? "destination"
      : kind === "sql" && !asset.materialization_type
        ? "read_target"
        : "target";
  const role = useMemo(() => {
    if (!kind) return undefined;
    return roleForExistingAsset(
      assetCreationRole(profile, kind, roleName),
      asset.type,
      capabilities,
    );
  }, [asset.type, capabilities, kind, profile, roleName]);
  const currentValue = (asset.explicit_connection ?? "").trim();
  const currentConnectionName = currentValue || (asset.connection ?? "").trim();
  const currentConnectionType = currentConnectionName
    ? workspace?.connections?.[currentConnectionName]
    : undefined;
  const currentSelection = resolveAssetConnectionSelection(
    role,
    currentValue,
    currentConnectionType,
  );
  const currentCandidate = currentSelection
    ? candidateForExistingAsset(currentSelection.candidates, asset.type, capabilities)
    : undefined;
  const currentNeedsMigration = Boolean(
    currentCandidate && currentCandidate.asset_type !== asset.type,
  );
  const sqlLineage = useMemo(() => {
    if (kind !== "sql") return { upstreams: [], downstreams: [] };

    const assetName = asset.name.trim().toLowerCase();
    const allAssets = (workspace?.pipelines ?? []).flatMap((pipeline) => pipeline.assets);
    const upstreams = (asset.upstreams ?? [])
      .map((name) => name.trim())
      .filter((name) => name && name.toLowerCase() !== assetName);
    const downstreams = allAssets
      .filter(
        (candidate) =>
          candidate.id !== asset.id &&
          (candidate.upstreams ?? []).some(
            (upstream) => upstream.trim().toLowerCase() === assetName,
          ),
      )
      .map((candidate) => candidate.name.trim())
      .filter(Boolean);

    return {
      upstreams: [...new Set(upstreams)].sort(),
      downstreams: [...new Set(downstreams)].sort(),
    };
  }, [asset.id, asset.name, asset.upstreams, kind, workspace?.pipelines]);

  if (!kind) return null;

  const persistSelection = async (value: string, confirmTypeMigration: boolean) => {
    setSaving(true);
    setSaveError("");
    try {
      await updateAsset(pipelineId, asset.id, {
        connection_selection: {
          environment: profile?.environment,
          connection: value,
          use_pipeline_default: !value,
          expected_asset_type: asset.type,
          confirm_type_migration: confirmTypeMigration,
        },
      });
    } catch (cause) {
      setSaveError(cause instanceof Error ? cause.message : "Could not update the connection.");
    } finally {
      setSaving(false);
    }
  };

  const reviewSelection = (
    value: string,
    nextRole: AssetCreationRoleProfile | undefined = role,
  ) => {
    setSaveError("");
    const selection = resolveAssetConnectionSelection(
      nextRole,
      value,
      value ? workspace?.connections?.[value] : undefined,
    );
    const candidate = selection
      ? candidateForExistingAsset(selection.candidates, asset.type, capabilities)
      : undefined;
    if (!selection || selection.incompatible || !candidate) {
      setSaveError("Choose a connection compatible with the current asset.");
      return;
    }
    if (candidate.asset_type !== asset.type) {
      setPendingMigration({ value, connectionName: selection.name, candidate });
      return;
    }
    void persistSelection(value, false);
  };

  const selectCreatedConnection = async (connectionName: string) => {
    const refreshed = await refresh();
    const refreshedRole = refreshed
      ? roleForExistingAsset(assetCreationRole(refreshed, kind, roleName), asset.type, capabilities)
      : undefined;
    reviewSelection(connectionName, refreshedRole);
  };

  const manageConnections = () => {
    const query = new URLSearchParams();
    if (profile?.environment) query.set("environment", profile.environment);
    if (currentConnectionName) query.set("connection", currentConnectionName);
    window.location.assign(`/project/connections${query.size ? `?${query.toString()}` : ""}`);
  };

  return (
    <>
      {loading && !role ? (
        <Field variant="plain">
          <FieldLabel>Connection</FieldLabel>
          <Skeleton className="h-8 w-full" />
          <FieldDescription>Loading compatible connections…</FieldDescription>
        </Field>
      ) : (
        <AssetConnectionField
          id={id}
          label="Connection"
          role={role}
          value={currentValue}
          currentConnectionType={currentConnectionType}
          disabled={saving}
          context="edit"
          onChange={reviewSelection}
          onNewConnection={() => setNewConnectionOpen(true)}
          onManageConnections={manageConnections}
          onOpenConnection={currentConnectionName ? manageConnections : undefined}
        />
      )}
      {profileError ? (
        <Alert variant="destructive">
          <AlertTriangle />
          <AlertTitle>Could not load connections</AlertTitle>
          <AlertDescription>{profileError}</AlertDescription>
        </Alert>
      ) : null}
      {saveError ? (
        <Alert variant="destructive">
          <AlertTriangle />
          <AlertTitle>Could not update connection</AlertTitle>
          <AlertDescription>{saveError}</AlertDescription>
        </Alert>
      ) : null}
      {currentNeedsMigration && currentCandidate && currentSelection ? (
        <Alert data-testid="asset-connection-mismatch">
          <AlertTriangle />
          <AlertTitle>Connection and asset type differ</AlertTitle>
          <AlertDescription className="flex flex-col items-start gap-2">
            <span>
              This connection now resolves to {currentCandidate.asset_type}; the asset is still{" "}
              {asset.type}.
            </span>
            <Button
              type="button"
              variant="outline"
              size="xs"
              onClick={() =>
                setPendingMigration({
                  value: currentValue,
                  connectionName: currentSelection.name,
                  candidate: currentCandidate,
                })
              }
            >
              Review migration
            </Button>
          </AlertDescription>
        </Alert>
      ) : null}

      {role && newConnectionOpen ? (
        <WorkspaceConnectionDialog
          open={newConnectionOpen}
          onOpenChange={setNewConnectionOpen}
          environment={profile?.environment || "default"}
          connectionTypes={role.connection_types}
          requestedConnectionType={currentConnectionType}
          onCreated={selectCreatedConnection}
        />
      ) : null}

      <AlertDialog
        open={Boolean(pendingMigration)}
        onOpenChange={(open) => {
          if (!open) setPendingMigration(null);
        }}
      >
        <AlertDialogContent data-testid="asset-connection-migration-dialog">
          <AlertDialogHeader>
            <AlertDialogTitle>Change this asset&apos;s engine?</AlertDialogTitle>
            <AlertDialogDescription>
              This changes the asset type from {asset.type} to{" "}
              {pendingMigration?.candidate.asset_type}
              {pendingMigration?.connectionName
                ? ` and uses ${pendingMigration.connectionName}`
                : ""}
              . Both values are saved together. Review dialect and runtime differences before the
              next run.
            </AlertDialogDescription>
          </AlertDialogHeader>
          {kind === "sql" &&
          pendingMigration &&
          (sqlLineage.upstreams.length > 0 || sqlLineage.downstreams.length > 0) ? (
            <Alert data-testid="asset-connection-lineage-warning">
              <AlertTriangle />
              <AlertTitle>Connected SQL assets may need to move too</AlertTitle>
              <AlertDescription>
                Pure SQL cannot query across connections. Changing only this asset can invalidate
                {sqlLineage.upstreams.length > 0
                  ? ` upstream references to ${formatAssetNames(sqlLineage.upstreams)}`
                  : ""}
                {sqlLineage.upstreams.length > 0 && sqlLineage.downstreams.length > 0 ? " and" : ""}
                {sqlLineage.downstreams.length > 0
                  ? ` downstream references from ${formatAssetNames(sqlLineage.downstreams)}`
                  : ""}
                .
              </AlertDescription>
            </Alert>
          ) : null}
          <AlertDialogFooter>
            <AlertDialogCancel disabled={saving}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              disabled={saving}
              onClick={() => {
                const migration = pendingMigration;
                setPendingMigration(null);
                if (migration) void persistSelection(migration.value, true);
              }}
            >
              Change engine
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

function formatAssetNames(names: string[]) {
  const visible = names.slice(0, 3).join(", ");
  const remaining = names.length - 3;
  return remaining > 0 ? `${visible}, and ${remaining} more` : visible;
}
