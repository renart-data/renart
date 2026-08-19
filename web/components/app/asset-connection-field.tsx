"use client";

import { ArrowUpRight, Plus, Settings } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import type { AssetCreationCandidate, AssetCreationRoleProfile } from "@/lib/types";

import { ConnectionSelect, type ConnectionSelectGroup } from "./connection-select";

const PIPELINE_DEFAULT_VALUE = "__pipeline_default__";
const NEW_CONNECTION_VALUE = "__new_connection__";
const MANAGE_CONNECTIONS_VALUE = "__manage_connections__";

export type AssetConnectionSelection = {
  name: string;
  connectionType: string;
  candidates: AssetCreationCandidate[];
  portabilityWarnings: string[];
  isDefault: boolean;
  incompatible: boolean;
};

export function resolveAssetConnectionSelection(
  role: AssetCreationRoleProfile | undefined,
  value: string,
  currentConnectionType?: string,
): AssetConnectionSelection | null {
  if (!role) return null;
  if (!value) {
    if (role.default.status !== "resolved" || !role.default.connection) return null;
    return {
      name: role.default.connection,
      connectionType: role.default.connection_type ?? "",
      candidates: role.default.candidates ?? [],
      portabilityWarnings:
        role.connections
          .find((connection) => connection.name === role.default.connection)
          ?.portability_warnings?.map((warning) => warning.message) ?? [],
      isDefault: true,
      incompatible: false,
    };
  }
  const connection = role.connections.find((candidate) => candidate.name === value);
  if (connection) {
    return {
      name: connection.name,
      connectionType: connection.connection_type,
      candidates: connection.candidates,
      portabilityWarnings: connection.portability_warnings?.map((warning) => warning.message) ?? [],
      isDefault: false,
      incompatible: false,
    };
  }
  return {
    name: value,
    connectionType: currentConnectionType ?? "unknown",
    candidates: [],
    portabilityWarnings: [],
    isDefault: false,
    incompatible: true,
  };
}

export function AssetConnectionField({
  id,
  label,
  role,
  value,
  currentConnectionType,
  disabled = false,
  context = "create",
  onChange,
  onNewConnection,
  onManageConnections,
  onOpenConnection,
}: {
  id: string;
  label: string;
  role?: AssetCreationRoleProfile;
  value: string;
  currentConnectionType?: string;
  disabled?: boolean;
  context?: "create" | "edit";
  onChange: (value: string) => void;
  onNewConnection: () => void;
  onManageConnections: () => void;
  onOpenConnection?: () => void;
}) {
  const selection = resolveAssetConnectionSelection(role, value, currentConnectionType);
  const defaultSelectable = role?.default.status === "resolved";
  const currentIsMissing = Boolean(value && !role?.connections.some((item) => item.name === value));
  const groups: ConnectionSelectGroup[] = [
    {
      label: "Available connections",
      options: [
        ...(role?.allow_default
          ? [
              {
                value: PIPELINE_DEFAULT_VALUE,
                label: pipelineDefaultLabel(role),
                connectionType: role.default.connection_type,
                detail: role.default.reason,
                disabled: !defaultSelectable,
              },
            ]
          : []),
        ...(currentIsMissing
          ? [
              {
                value,
                label: value,
                connectionType: currentConnectionType,
                badge: "incompatible",
                badgeVariant: "destructive" as const,
                disabled: true,
              },
            ]
          : []),
        ...(role?.connections.map((connection) => ({
          value: connection.name,
          label: connection.name,
          connectionType: connection.connection_type,
        })) ?? []),
      ],
    },
    {
      options: [
        {
          value: NEW_CONNECTION_VALUE,
          label: "New connection…",
          icon: Plus,
          disabled: !role?.connection_types.length,
        },
        { value: MANAGE_CONNECTIONS_VALUE, label: "Manage connections…", icon: Settings },
      ],
    },
  ];

  return (
    <Field variant="plain" className="min-w-0">
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <div className="flex min-w-0 items-center gap-2">
        <div className="min-w-0 flex-1">
          <ConnectionSelect
            value={value || PIPELINE_DEFAULT_VALUE}
            groups={groups}
            id={id}
            disabled={!role || disabled}
            onValueChange={(nextValue) => {
              if (nextValue === NEW_CONNECTION_VALUE) {
                onNewConnection();
                return;
              }
              if (nextValue === MANAGE_CONNECTIONS_VALUE) {
                onManageConnections();
                return;
              }
              onChange(nextValue === PIPELINE_DEFAULT_VALUE ? "" : nextValue);
            }}
            className="w-full"
            placeholder={role ? "Choose a connection" : "Loading connections…"}
          />
        </div>
        {onOpenConnection && selection ? (
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                type="button"
                variant="outline"
                size="icon"
                aria-label={`Go to connection ${selection.name}`}
                disabled={disabled}
                onClick={onOpenConnection}
              >
                <ArrowUpRight />
              </Button>
            </TooltipTrigger>
            <TooltipContent>Go to connection</TooltipContent>
          </Tooltip>
        ) : null}
      </div>
      {!role ? (
        <FieldDescription>
          Loading compatible connections and the pipeline default.
        </FieldDescription>
      ) : selection?.incompatible ? (
        <FieldDescription className="text-destructive">
          {context === "edit"
            ? "This connection cannot be used by the current asset. Choose a compatible connection."
            : "This connection cannot create this asset type. Choose another connection explicitly."}
        </FieldDescription>
      ) : role.default.status !== "resolved" && !value ? (
        <FieldDescription className="text-destructive">
          {role.default.reason || "Choose a compatible connection."}
        </FieldDescription>
      ) : selection?.portabilityWarnings.length ? (
        <FieldDescription>{selection.portabilityWarnings.join(" ")}</FieldDescription>
      ) : (
        <FieldDescription>
          Only connections supported for this role in the selected environment are shown.
        </FieldDescription>
      )}
    </Field>
  );
}

function pipelineDefaultLabel(role: AssetCreationRoleProfile) {
  switch (role.default.status) {
    case "resolved":
      return `Pipeline default — ${role.default.connection}`;
    case "ambiguous":
      return "Pipeline default — ambiguous";
    case "incompatible":
      return "Pipeline default — incompatible";
    default:
      return "Pipeline default — not configured";
  }
}
