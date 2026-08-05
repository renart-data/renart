"use client";

import { ArrowUpRight, Plus, Settings } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectSeparator,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import type {
  AssetCreationCandidate,
  AssetCreationConnection,
  AssetCreationRoleProfile,
} from "@/lib/types";

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

  return (
    <Field variant="plain" className="min-w-0">
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <div className="flex min-w-0 items-center gap-2">
        <div className="min-w-0 flex-1">
          <Select
            value={value || PIPELINE_DEFAULT_VALUE}
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
          >
            <SelectTrigger
              id={id}
              className="min-w-0 w-full overflow-hidden [&_[data-slot=select-value]]:min-w-0 [&_[data-slot=select-value]]:overflow-hidden"
            >
              <SelectValue placeholder={role ? "Choose a connection" : "Loading connections…"} />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                <SelectLabel>Available connections</SelectLabel>
                {role?.allow_default ? (
                  <SelectItem value={PIPELINE_DEFAULT_VALUE} disabled={!defaultSelectable}>
                    {pipelineDefaultLabel(role)}
                  </SelectItem>
                ) : null}
                {currentIsMissing ? (
                  <SelectItem value={value} aria-label={value} disabled>
                    <span className="flex min-w-0 items-center gap-2">
                      <span className="truncate">{value}</span>
                      <Badge variant="destructive" size="xs" aria-hidden="true">
                        incompatible
                      </Badge>
                    </span>
                  </SelectItem>
                ) : null}
                {role?.connections.map((connection) => (
                  <SelectItem
                    key={connection.name}
                    value={connection.name}
                    aria-label={connection.name}
                  >
                    <ConnectionOption connection={connection} />
                  </SelectItem>
                ))}
              </SelectGroup>
              <SelectSeparator />
              <SelectGroup>
                <SelectItem value={NEW_CONNECTION_VALUE} disabled={!role?.connection_types.length}>
                  <Plus />
                  New connection…
                </SelectItem>
                <SelectItem value={MANAGE_CONNECTIONS_VALUE}>
                  <Settings />
                  Manage connections…
                </SelectItem>
              </SelectGroup>
            </SelectContent>
          </Select>
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

function ConnectionOption({ connection }: { connection: AssetCreationConnection }) {
  return (
    <span className="flex min-w-0 items-center gap-2 pr-5">
      <span className="truncate">{connection.name}</span>
      <Badge variant="outline" size="xs" aria-hidden="true">
        {friendlyConnectionType(connection.connection_type)}
      </Badge>
    </span>
  );
}

function pipelineDefaultLabel(role: AssetCreationRoleProfile) {
  switch (role.default.status) {
    case "resolved":
      return `Pipeline default — ${role.default.connection} (${friendlyConnectionType(role.default.connection_type ?? "")})`;
    case "ambiguous":
      return "Pipeline default — ambiguous";
    case "incompatible":
      return "Pipeline default — incompatible";
    default:
      return "Pipeline default — not configured";
  }
}

function friendlyConnectionType(connectionType: string) {
  const names: Record<string, string> = {
    aws: "AWS",
    google_cloud_platform: "BigQuery",
    mssql: "SQL Server",
    postgres: "PostgreSQL",
    s3: "S3",
  };
  return names[connectionType] ?? connectionType;
}
