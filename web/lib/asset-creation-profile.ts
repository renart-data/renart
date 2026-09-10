import {
  isAPIAssetType,
  isLoadAssetType,
  isPythonAssetType,
  isSeedAssetType,
  isSensorAssetType,
  isSqlAssetType,
} from "@/lib/asset-types";
import type {
  AssetAuthoringCapability,
  AssetCreationCandidate,
  AssetCreationProfile,
  AssetCreationRoleProfile,
} from "@/lib/types";

export type AssetCreationKind = "sql" | "python" | "api" | "load" | "seed" | "sensor";
export type AssetCreationRole = "target" | "read_target" | "source" | "destination";

export function assetCreationKindForType(assetType: string): AssetCreationKind | null {
  const normalized = assetType.trim().toLowerCase();
  if (isSqlAssetType(normalized)) return "sql";
  if (isPythonAssetType(normalized)) return "python";
  if (isAPIAssetType(normalized)) return "api";
  if (isLoadAssetType(normalized)) return "load";
  if (isSeedAssetType(normalized)) return "seed";
  if (isSensorAssetType(normalized)) return "sensor";
  return null;
}

export function assetCreationRole(
  profile: AssetCreationProfile | null,
  kind: AssetCreationKind,
  role: AssetCreationRole,
) {
  return profile?.kinds
    .find((candidate) => candidate.kind === kind)
    ?.roles.find((candidate) => candidate.role === role);
}

export function candidateForExistingAsset(
  candidates: AssetCreationCandidate[],
  assetType: string,
  capabilities: AssetAuthoringCapability[],
) {
  const variant = existingAssetVariant(assetType, capabilities);
  if (variant) return candidates.find((candidate) => candidate.variant === variant);
  if (candidates.length === 1) return candidates[0];
  return candidates.find((candidate) => candidate.asset_type === assetType);
}

export function roleForExistingAsset(
  role: AssetCreationRoleProfile | undefined,
  assetType: string,
  capabilities: AssetAuthoringCapability[],
): AssetCreationRoleProfile | undefined {
  if (!role) return undefined;
  const selectCandidates = (candidates: AssetCreationCandidate[] | undefined) =>
    candidates?.filter(
      (candidate) => candidateForExistingAsset([candidate], assetType, capabilities) !== undefined,
    ) ?? [];
  const connections = role.connections
    .map((connection) => ({
      ...connection,
      candidates: selectCandidates(connection.candidates),
    }))
    .filter((connection) => connection.candidates.length > 0);
  const defaultCandidates = selectCandidates(role.default.candidates);
  const defaultValue =
    role.default.status === "resolved" && defaultCandidates.length === 0
      ? {
          ...role.default,
          status: "incompatible",
          reason: "The pipeline default does not support this asset variant.",
          candidates: [],
        }
      : { ...role.default, candidates: defaultCandidates };
  const connectionTypeCandidates = Object.fromEntries(
    Object.entries(role.connection_type_candidates ?? {})
      .map(([connectionType, candidates]) => [connectionType, selectCandidates(candidates)])
      .filter(([, candidates]) => candidates.length > 0),
  );
  const connectionTypes = role.connection_types.filter(
    (connectionType) => (connectionTypeCandidates[connectionType.type_name]?.length ?? 0) > 0,
  );
  return {
    ...role,
    connections,
    default: defaultValue,
    connection_types: connectionTypes,
    connection_type_candidates: connectionTypeCandidates,
  };
}

function existingAssetVariant(assetType: string, capabilities: AssetAuthoringCapability[]) {
  const normalized = assetType.trim().toLowerCase();
  const capability = capabilities.find(
    (candidate) => candidate.type.trim().toLowerCase() === normalized,
  );
  if (capability?.variant) return capability.variant;
  if (isSeedAssetType(normalized)) return "file";
  if (normalized.endsWith(".sensor.query")) return "query";
  if (normalized.endsWith(".sensor.table")) return "table";
  if (normalized.includes(".sensor.key")) return "key";
  return "";
}
