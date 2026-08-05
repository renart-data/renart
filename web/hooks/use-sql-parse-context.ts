"use client";

import { useEffect, useMemo, useState } from "react";

import { getSQLParseContext } from "@/lib/api-sql-discovery";
import { isQuerySensorAssetType, isSqlAssetType } from "@/lib/asset-types";
import { SchemaTable } from "@/lib/sql-schema";
import { SqlParseContextResponse, WebAsset } from "@/lib/types";

export function useSQLParseContext(
  asset: WebAsset | null,
  content: string,
  schemaTables: SchemaTable[],
  connection?: string,
) {
  // Parse context is a SQL-only concept; never hit the endpoint for
  // Python/YAML/ingestr assets.
  const isSqlAsset = Boolean(
    asset &&
    (isSqlAssetType(asset.type) ||
      isQuerySensorAssetType(asset.type) ||
      asset.path.toLowerCase().endsWith(".sql")),
  );
  const assetId = isSqlAsset ? (asset?.id ?? null) : null;
  const [data, setData] = useState<SqlParseContextResponse | null>(null);
  const hasContent = useMemo(() => content.trim().length > 0, [content]);
  const schemaPayload = useMemo(
    () =>
      schemaTables.map((table) => ({
        name: table.name,
        is_materialized: table.isMaterialized,
        columns: table.columns.map((column) => ({
          name: column.name,
          type: column.type,
          source_methods: column.sourceMethods,
        })),
      })),
    [schemaTables],
  );
  const schemaKey = useMemo(() => JSON.stringify(schemaPayload), [schemaPayload]);

  useEffect(() => {
    if (!assetId || !hasContent) {
      setData(null);
      return;
    }

    const controller = new AbortController();
    const timer = window.setTimeout(async () => {
      try {
        const response = await getSQLParseContext({
          assetId,
          content: jinjaSafeSQLForParsing(content),
          connection,
          schema: schemaPayload,
          signal: controller.signal,
        });
        if (!controller.signal.aborted) {
          setData((current) => {
            if (
              response.errors?.length &&
              !response.diagnostics?.some((diagnostic) => diagnostic.range) &&
              current &&
              !current.errors?.length
            ) {
              return {
                ...current,
                errors: response.errors,
              };
            }

            const nextSerialized = JSON.stringify(response);
            const currentSerialized = current ? JSON.stringify(current) : "";
            return nextSerialized === currentSerialized ? current : response;
          });
        }
      } catch {
        return;
      }
    }, 350);

    return () => {
      controller.abort();
      window.clearTimeout(timer);
    };
  }, [assetId, connection, content, hasContent, schemaKey, schemaPayload]);

  return data;
}

function jinjaSafeSQLForParsing(content: string) {
  return content
    .replace(/\{#[\s\S]*?#\}/g, "")
    .replace(/'\s*\{\{[\s\S]*?\}\}\s*'/g, "'__renart_jinja_value__'")
    .replace(/"\s*\{\{[\s\S]*?\}\}\s*"/g, '"__renart_jinja_value__"')
    .replace(/\{\{[\s\S]*?\}\}/g, "'__renart_jinja_value__'")
    .replace(/\{%[\s\S]*?%\}/g, "");
}
