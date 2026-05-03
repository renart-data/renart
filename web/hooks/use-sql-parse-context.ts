"use client";

import { useEffect, useMemo, useState } from "react";

import { getSQLParseContext } from "@/lib/api";
import { SchemaTable } from "@/lib/sql-schema";
import { SqlParseContextResponse, WebAsset } from "@/lib/types";

export function useSQLParseContext(
  asset: WebAsset | null,
  content: string,
  schemaTables: SchemaTable[],
) {
  const assetId = asset?.id ?? null;
  const [data, setData] = useState<SqlParseContextResponse | null>(null);
  const hasContent = useMemo(() => content.trim().length > 0, [content]);
  const schemaPayload = useMemo(
    () =>
      schemaTables.map((table) => ({
        name: table.name,
        columns: table.columns.map((column) => ({
          name: column.name,
          type: column.type,
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
          schema: schemaPayload,
          signal: controller.signal,
        });
        if (!controller.signal.aborted) {
          setData((current) => {
            if (
              response.errors?.length &&
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
  }, [assetId, content, hasContent, schemaKey, schemaPayload]);

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
