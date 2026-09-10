import type { DataBrowserNode } from "@/lib/generated/api-types";

// UI hint only. The server re-discovers the exact item before review and apply.
export function notebookBrowserDropReason(node: DataBrowserNode): string | undefined {
  const address = node.address;
  if (address?.source_kind === "warehouse" && node.object_kind === "table") return;
  if (address?.source_kind === "storage" && address.connection_type !== "s3")
    return "This storage connection does not support notebook snapshots yet.";
  if (node.node_type === "namespace") return "Choose an individual file inside this prefix.";
  if (
    (address?.source_kind === "storage" || address?.source_kind === "local_files") &&
    node.object_kind === "file" &&
    ["csv", "json", "jsonl", "parquet"].includes(node.format ?? "")
  ) {
    const path = address.path ?? "";
    if (!/[*?[\]]|\{[{%]/.test(path)) return;
    return "Choose a file with a literal path, without wildcards or templates.";
  }
  return "This item cannot be added as a notebook source.";
}
