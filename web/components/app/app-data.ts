import {
  ArrowLeftRight,
  Box,
  BookOpen,
  Braces,
  Calendar,
  ClipboardCheck,
  Cloud,
  Cpu,
  FileCode,
  Hammer,
  LayoutDashboard,
  Network,
  Play,
  Radar,
  Sprout,
  Table2,
} from "lucide-react";

import type { AssetStaleness } from "@/lib/api-staleness";
import type { AssetKind } from "@/lib/asset-presentation";

export type { AssetKind } from "@/lib/asset-presentation";

export const integrations: Record<string, string> = {
  DuckDB: "#f59e0b",
  Stripe: "#635bff",
  ingestr: "#0ea5e9",
  dbt: "#ff694b",
  Python: "#3b82f6",
  BigQuery: "#4285f4",
  Sklearn: "#f59e0b",
  Load: "#14b8a6",
  Test: "#16a34a",
};

export const navItems = [
  { to: "/", label: "Build", icon: Hammer },
  { to: "/catalog", label: "Catalog", icon: Network },
  { to: "/notebooks", label: "Notebooks", icon: BookOpen },
  { to: "/dashboards", label: "Present", icon: LayoutDashboard },
  { to: "/runs", label: "Runs", icon: Play },
  { to: "/schedules", label: "Schedules", icon: Calendar },
] as const;

export const kindMeta = {
  sql: { label: "SQL asset", icon: FileCode, ext: ".sql", description: "Transform with a SELECT" },
  python: { label: "Python asset", icon: Cpu, ext: ".py", description: "Custom Python transform" },
  api: {
    label: "HTTP API asset",
    icon: Braces,
    ext: ".asset.yml",
    description: "Fetch records from an HTTP API",
  },
  load: {
    label: "Load source / sink",
    icon: ArrowLeftRight,
    ext: ".asset.yml",
    description: "Replicate data between connections",
  },
  seed: {
    label: "Seed asset",
    icon: Sprout,
    ext: ".asset.yml",
    description: "Load a version-controlled file into a table",
  },
  sensor: {
    label: "Sensor",
    icon: Radar,
    ext: ".asset.yml",
    description: "Wait for an external readiness condition",
  },
  source: {
    label: "Source table",
    icon: Table2,
    ext: ".source.yml",
    description: "Existing table with columns, types, and checks",
  },
  ingestr: {
    label: "External source",
    icon: Cloud,
    ext: ".asset.yml",
    description: "Stripe, GA4, S3, and other ingestr sources",
  },
  unittest: {
    label: "Unit test",
    icon: ClipboardCheck,
    ext: ".test.yml",
    description: "Mock inputs and expected output",
  },
  asset: { label: "Asset", icon: Box, ext: "", description: "Pipeline asset" },
} as const satisfies Record<
  AssetKind,
  { label: string; icon: typeof Box; ext: string; description: string }
>;

export type AppAsset = {
  id: string;
  name: string;
  kind: AssetKind;
  group: string;
  integration: string;
  description: string;
  dir?: string;
  status: "ok" | "overdue" | "unknown" | "pending" | "success" | "failed";
  materializedAt: string;
  staleness?: AssetStaleness;
  // Set when the asset file failed to parse; the node renders an error state and
  // the editor shows the message so the user can fix it in place.
  parseError?: string;
  // Projected from the active pipeline's type-check report; never persisted.
  hasTypeCheckError?: boolean;
  // Ephemeral positive warehouse observation, not an authored workspace asset.
  isExternal?: boolean;
  x: number;
  y: number;
};
