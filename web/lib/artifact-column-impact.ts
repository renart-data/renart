import type { ArtifactColumnImpact, ArtifactIndex, ArtifactRef } from "./generated/api-types";

export type ColumnImpactView = ArtifactColumnImpact & {
  key: string;
  label: string;
  useLabel: string;
};

export function artifactRefKey(ref: ArtifactRef): string {
  return `${ref.kind}\u0000${ref.artifact_id}\u0000${ref.component_id ?? ""}`;
}

function artifactRefLabel(index: ArtifactIndex, ref: ArtifactRef): string {
  const artifact = index.artifacts.find(
    (candidate) => candidate.kind === ref.kind && candidate.id === ref.artifact_id,
  );
  if (!artifact) return ref.component_id || ref.artifact_id;
  if (!ref.component_id) return artifact.title;
  const component = artifact.components?.find((candidate) => candidate.id === ref.component_id);
  return `${artifact.title} / ${component?.name || component?.id || ref.component_id}`;
}

export function columnImpactsForAsset(
  index: ArtifactIndex | undefined,
  workspaceAssetID: string,
): Map<string, ColumnImpactView[]> {
  const result = new Map<string, ColumnImpactView[]>();
  if (!index) return result;
  const artifact = index.artifacts.find(
    (candidate) =>
      candidate.kind === "pipeline_asset" && candidate.workspace_id === workspaceAssetID,
  );
  if (!artifact) return result;
  const producer: ArtifactRef = { kind: artifact.kind, artifact_id: artifact.id };
  for (const impact of index.breaking_column_impacts ?? []) {
    if (artifactRefKey(impact.producer) !== artifactRefKey(producer)) continue;
    const column = impact.column.trim().toLowerCase();
    if (!column) continue;
    const useLabel = impact.consumer_column
      ? `output column ${impact.consumer_column}`
      : impact.role === "query.reference"
        ? "SQL predicate or join"
        : impact.role
          ? impact.role
          : "column reference";
    const view: ColumnImpactView = {
      ...impact,
      key: `${artifactRefKey(impact.consumer)}\u0000${impact.consumer_column ?? ""}\u0000${impact.role ?? ""}`,
      label: artifactRefLabel(index, impact.consumer),
      useLabel,
    };
    result.set(column, [...(result.get(column) ?? []), view]);
  }
  for (const impacts of result.values()) {
    impacts.sort((left, right) => {
      if (left.distance !== right.distance) return left.distance - right.distance;
      const byLabel = left.label.localeCompare(right.label);
      return byLabel || left.useLabel.localeCompare(right.useLabel);
    });
  }
  return result;
}
