import type { ColumnSchemaMergeRow, ColumnSchemaSourceSnapshot } from "@/lib/generated/api-types";

export const CURRENT_SCHEMA_CHOICE = "current";
export const REMOVE_SCHEMA_CHOICE = "remove";
export const SCHEMA_SOURCE_CHOICE_PREFIX = "source:";

export function schemaSourceChoice(sourceID: string) {
  return `${SCHEMA_SOURCE_CHOICE_PREFIX}${sourceID}`;
}

export function defaultSchemaResolutionChoice(
  row: ColumnSchemaMergeRow,
  sources: ColumnSchemaSourceSnapshot[],
) {
  const primary =
    sources.find((snapshot) => snapshot.source.category === "definition") ?? sources[0];
  const freshComplete = sources.find(
    (snapshot) =>
      snapshot.fresh === true &&
      snapshot.classification !== "stale" &&
      snapshot.classification !== "scoped" &&
      snapshot.completeness !== "partial" &&
      sourceColumn(snapshot, row.column),
  );

  // When otherwise comparable sources disagree, a complete relation built
  // from the current asset revision is the strongest suggestion. The dialog
  // still requires an explicit apply, so this changes the recommendation, not
  // the conservative write boundary.
  if (row.kind === "source_conflict" && freshComplete) {
    return schemaSourceChoice(freshComplete.source.id);
  }
  // A known definition type changing from the saved contract is the ordinary
  // result of editing SQL and asking to sync. Prefer the new definition while
  // still keeping the old value one selectable option away.
  if (
    (row.kind === "type_conflict" || row.kind === "source_missing") &&
    primary &&
    sourceColumn(primary, row.column)
  ) {
    return schemaSourceChoice(primary.source.id);
  }
  if (row.current_present) {
    return CURRENT_SCHEMA_CHOICE;
  }
  if (row.proposed_present && primary && sourceColumn(primary, row.column)) {
    return schemaSourceChoice(primary.source.id);
  }
  return REMOVE_SCHEMA_CHOICE;
}

function sourceColumn(snapshot: ColumnSchemaSourceSnapshot, name: string) {
  const key = name.trim().toLowerCase();
  return snapshot.columns.find((column) => column.name.trim().toLowerCase() === key);
}
