import {
  ArrowLeftRight,
  Braces,
  Cpu,
  FileCode2,
  FlaskConical,
  Radar,
  Sprout,
  Table2,
  type LucideIcon,
} from "lucide-react";

import type { AssetKind, AppAsset } from "@/components/app/app-data";
import type { AppLineageCanvasAsset } from "@/components/app/lineage-canvas";

export type LabVariant = "workbench" | "lifecycle" | "studio";
export type LabArea = "build" | "operate" | "explore" | "notebooks";
export type BuildView = "canvas" | "split" | "code" | "adhoc" | "data";
export type OperateView = "overview" | "deployments" | "schedules" | "runs" | "run-detail";
export type ExploreView = "catalog" | "dashboards" | "reports";
export type SettingsView = "connections" | "environments" | "pipeline" | "project";
export type SettingsSection =
  | "general"
  | "execution"
  | "python"
  | "variables"
  | "hooks"
  | "git"
  | "appearance"
  | "security";
export type UtilityPane = "context" | "data" | "settings";

export type PaletteAsset = {
  kind: AssetKind;
  label: string;
  description: string;
  icon: LucideIcon;
  accent: string;
};

export type BrowserObjectKind = "table" | "view" | "materialized_view" | "external_table" | "file";

export type BrowserColumn = {
  name: string;
  type: string;
  description?: string;
  nullable?: boolean;
  key?: "primary" | "foreign";
  tags?: string[];
};

export type BrowserTable = {
  name: string;
  kind?: BrowserObjectKind;
  rows: string;
  size?: string;
  freshness: string;
  description?: string;
  authoredAsset?: string;
  usedBy?: string[];
  columns: BrowserColumn[];
};

export type BrowserSchema = {
  database?: string;
  name: string;
  description?: string;
  tables: BrowserTable[];
};

export type BrowserDiscoveryStatus =
  | "ready"
  | "discovering"
  | "refreshing"
  | "partial"
  | "error"
  | "empty";

export type BrowserConnection = {
  id: string;
  name: string;
  type: string;
  accent: string;
  discovery: {
    status: BrowserDiscoveryStatus;
    lastRefreshed: string;
    scope: string;
    detail?: string;
  };
  schemas: BrowserSchema[];
};

export const labEnvironments = [
  {
    id: "default",
    detail: "Interactive local development",
    policy: "Writable",
    connection: "duckdb-default",
    accent: "bg-emerald-500",
  },
  {
    id: "staging",
    detail: "Shared validation environment",
    policy: "Writable",
    connection: "analytics-warehouse",
    accent: "bg-amber-500",
  },
  {
    id: "production",
    detail: "Reviewed deployments only",
    policy: "Protected",
    connection: "analytics-warehouse",
    accent: "bg-red-500",
  },
] as const;

export const variantMeta: Record<
  LabVariant,
  { short: string; label: string; description: string }
> = {
  workbench: {
    short: "A",
    label: "Workbench rail",
    description:
      "A compact utility rail exposes data and configuration beside contextual resources.",
  },
  lifecycle: {
    short: "B",
    label: "Lifecycle",
    description: "Build, Run, and Explore stay global while one sidebar follows the workflow.",
  },
  studio: {
    short: "C",
    label: "Project studio",
    description: "Global pages stay visible while project resources remain stable beside them.",
  },
};

export const paletteAssets: PaletteAsset[] = [
  {
    kind: "source",
    label: "Source table",
    description: "External root",
    icon: Table2,
    accent: "bg-sky-500/15 text-sky-700 dark:text-sky-300",
  },
  {
    kind: "seed",
    label: "Seed",
    description: "Versioned input",
    icon: Sprout,
    accent: "bg-lime-500/15 text-lime-700 dark:text-lime-300",
  },
  {
    kind: "api",
    label: "HTTP API",
    description: "Remote records",
    icon: Braces,
    accent: "bg-violet-500/15 text-violet-700 dark:text-violet-300",
  },
  {
    kind: "sql",
    label: "SQL",
    description: "Transform",
    icon: FileCode2,
    accent: "bg-amber-500/15 text-amber-700 dark:text-amber-300",
  },
  {
    kind: "python",
    label: "Python",
    description: "Transform",
    icon: Cpu,
    accent: "bg-blue-500/15 text-blue-700 dark:text-blue-300",
  },
  {
    kind: "load",
    label: "Load",
    description: "Replicate / publish",
    icon: ArrowLeftRight,
    accent: "bg-teal-500/15 text-teal-700 dark:text-teal-300",
  },
  {
    kind: "sensor",
    label: "Sensor",
    description: "Readiness gate",
    icon: Radar,
    accent: "bg-orange-500/15 text-orange-700 dark:text-orange-300",
  },
  {
    kind: "unittest",
    label: "Unit test",
    description: "Asset validation",
    icon: FlaskConical,
    accent: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
  },
];

export const initialLabAssets: AppLineageCanvasAsset[] = [
  {
    id: "source-accounts",
    name: "raw.accounts",
    displayName: "accounts",
    group: "raw",
    prefix: "raw",
    kind: "source",
    integration: "Postgres",
    description: "Existing source table",
    status: "unknown",
    materializedAt: "External source",
    x: 0,
    y: 0,
  },
  {
    id: "seed-regions",
    name: "raw.regions",
    displayName: "regions",
    group: "raw",
    prefix: "raw",
    kind: "seed",
    integration: "DuckDB",
    description: "Versioned region lookup",
    status: "success",
    materializedAt: "12 min ago",
    x: 0,
    y: 160,
  },
  {
    id: "stg-accounts",
    name: "staging.accounts",
    displayName: "accounts",
    group: "staging",
    prefix: "staging",
    kind: "sql",
    integration: "DuckDB",
    description: "Normalize account records",
    status: "success",
    materializedAt: "8 min ago",
    upstreams: ["source-accounts"],
    x: 300,
    y: 40,
  },
  {
    id: "stg-subscriptions",
    name: "staging.subscriptions",
    displayName: "subscriptions",
    group: "staging",
    prefix: "staging",
    kind: "python",
    integration: "Python",
    description: "Enrich subscription lifecycle",
    status: "success",
    materializedAt: "8 min ago",
    upstreams: ["source-accounts"],
    x: 300,
    y: 180,
  },
  {
    id: "mart-health",
    name: "analytics.customer_health",
    displayName: "customer_health",
    group: "analytics",
    prefix: "analytics",
    kind: "sql",
    integration: "DuckDB",
    description: "Account health model",
    status: "overdue",
    materializedAt: "Yesterday, 09:12",
    upstreams: ["stg-accounts", "stg-subscriptions", "seed-regions"],
    x: 650,
    y: 80,
  },
  {
    id: "mart-retention",
    name: "analytics.retention_daily",
    displayName: "retention_daily",
    group: "analytics",
    prefix: "analytics",
    kind: "sql",
    integration: "DuckDB",
    description: "Daily retention metrics",
    status: "success",
    materializedAt: "6 min ago",
    upstreams: ["stg-accounts", "stg-subscriptions"],
    x: 650,
    y: 240,
  },
];

export const browserConnections: BrowserConnection[] = [
  {
    id: "postgres-production",
    name: "postgres-production",
    type: "Postgres",
    accent: "bg-blue-500",
    discovery: {
      status: "ready",
      lastRefreshed: "2 minutes ago",
      scope: "2 databases · metadata only",
    },
    schemas: [
      {
        database: "app",
        name: "public",
        description: "Customer-facing application data",
        tables: [
          {
            name: "accounts",
            kind: "table",
            rows: "128k",
            size: "84 MB",
            freshness: "Observed 2m ago",
            description: "One row per customer account and its current commercial plan.",
            authoredAsset: "raw.accounts",
            usedBy: ["staging.accounts", "staging.subscriptions"],
            columns: [
              {
                name: "account_id",
                type: "uuid",
                description: "Stable account identifier",
                nullable: false,
                key: "primary",
              },
              { name: "company_name", type: "varchar", nullable: false },
              { name: "plan", type: "varchar", description: "Current billing plan" },
              { name: "created_at", type: "timestamptz", nullable: false },
            ],
          },
          {
            name: "subscription_events",
            kind: "table",
            rows: "2.4m",
            size: "1.8 GB",
            freshness: "Observed 2m ago",
            description: "Append-only subscription lifecycle events.",
            usedBy: ["staging.subscriptions"],
            columns: [
              { name: "event_id", type: "uuid", nullable: false, key: "primary" },
              { name: "account_id", type: "uuid", nullable: false, key: "foreign" },
              { name: "event_type", type: "varchar", nullable: false },
              { name: "occurred_at", type: "timestamptz", nullable: false },
            ],
          },
          {
            name: "billing_contacts",
            kind: "view",
            rows: "91k",
            freshness: "Observed 6m ago",
            description: "Primary billing contact derived from account preferences.",
            columns: [
              { name: "account_id", type: "uuid", key: "foreign" },
              { name: "email", type: "varchar", tags: ["PII"] },
              { name: "country", type: "varchar" },
            ],
          },
        ],
      },
      {
        database: "app",
        name: "audit",
        description: "Operational change history",
        tables: [
          {
            name: "changes",
            kind: "table",
            rows: "8.1m",
            size: "4.2 GB",
            freshness: "Observed 4m ago",
            description: "Append-only audit events emitted by the application.",
            columns: [
              { name: "object_id", type: "uuid" },
              { name: "operation", type: "varchar" },
              { name: "changed_at", type: "timestamptz" },
            ],
          },
        ],
      },
      {
        database: "billing",
        name: "billing",
        description: "Billing service data",
        tables: [
          {
            name: "invoices",
            kind: "table",
            rows: "684k",
            size: "390 MB",
            freshness: "Observed 2m ago",
            description: "Issued invoices with settlement state and account ownership.",
            usedBy: ["finance.invoice_summary"],
            columns: [
              { name: "invoice_id", type: "uuid", nullable: false, key: "primary" },
              { name: "account_id", type: "uuid", nullable: false, key: "foreign" },
              { name: "amount", type: "numeric(18,2)", nullable: false },
              { name: "status", type: "varchar", nullable: false },
              { name: "issued_at", type: "timestamptz", nullable: false },
            ],
          },
        ],
      },
    ],
  },
  {
    id: "project-files",
    name: "Project files",
    type: "Local files",
    accent: "bg-slate-500",
    discovery: {
      status: "ready",
      lastRefreshed: "just now",
      scope: "example/data · project-scoped files",
      detail: "Only files inside the project and explicitly allowed local roots are visible.",
    },
    schemas: [
      {
        database: "example",
        name: "data",
        description: "Versioned and local datasets inside example/data",
        tables: [
          {
            name: "accounts.csv",
            kind: "file",
            rows: "128k",
            size: "22 MB",
            freshness: "Modified 14m ago",
            description: "Account export used to bootstrap local development.",
            usedBy: ["raw.accounts", "Account exploration notebook"],
            columns: [
              { name: "account_id", type: "varchar", nullable: false, key: "primary" },
              { name: "company_name", type: "varchar", nullable: false },
              { name: "plan", type: "varchar" },
              { name: "created_at", type: "timestamp", nullable: false },
            ],
          },
          {
            name: "product_events.parquet",
            kind: "file",
            rows: "2.4m",
            size: "186 MB",
            freshness: "Modified 38m ago",
            description: "Columnar product-event sample for local pipeline development.",
            usedBy: ["raw.product_events"],
            columns: [
              { name: "event_id", type: "varchar", nullable: false, key: "primary" },
              { name: "account_id", type: "varchar", nullable: false, key: "foreign" },
              { name: "event_name", type: "varchar", nullable: false },
              { name: "received_at", type: "timestamp", nullable: false },
            ],
          },
        ],
      },
      {
        database: "example",
        name: "fixtures/finance",
        description: "Small checked-in fixtures used by tests and demos",
        tables: [
          {
            name: "invoices.jsonl",
            kind: "file",
            rows: "1.8k",
            size: "940 KB",
            freshness: "Modified yesterday",
            description: "Synthetic invoice fixture with one JSON object per line.",
            columns: [
              { name: "invoice_id", type: "varchar", nullable: false, key: "primary" },
              { name: "account_id", type: "varchar", nullable: false, key: "foreign" },
              { name: "amount", type: "decimal(18,2)", nullable: false },
              { name: "issued_at", type: "timestamp", nullable: false },
            ],
          },
        ],
      },
    ],
  },
  {
    id: "warehouse",
    name: "analytics-warehouse",
    type: "Snowflake",
    accent: "bg-sky-400",
    discovery: {
      status: "ready",
      lastRefreshed: "9 minutes ago",
      scope: "ANALYTICS database · metadata only",
    },
    schemas: [
      {
        database: "RENART",
        name: "ANALYTICS",
        description: "Reviewed analytics serving models",
        tables: [
          {
            name: "CUSTOMER_HEALTH",
            kind: "materialized_view",
            rows: "126k",
            size: "61 MB",
            freshness: "Observed 9m ago",
            description: "Latest health score and risk classification per account.",
            authoredAsset: "analytics.customer_health",
            usedBy: ["Customer health dashboard", "Weekly retention report"],
            columns: [
              { name: "ACCOUNT_ID", type: "VARCHAR", nullable: false, key: "primary" },
              {
                name: "HEALTH_SCORE",
                type: "NUMBER",
                description: "Composite score from 0 to 100",
              },
              { name: "RISK_BAND", type: "VARCHAR" },
            ],
          },
          {
            name: "RETENTION_DAILY",
            kind: "view",
            rows: "64k",
            freshness: "Observed 9m ago",
            description: "Daily retained-account counts by signup cohort.",
            authoredAsset: "analytics.retention_daily",
            usedBy: ["Weekly retention report"],
            columns: [
              { name: "COHORT_DATE", type: "DATE" },
              { name: "DAY_NUMBER", type: "NUMBER" },
              { name: "RETAINED_ACCOUNTS", type: "NUMBER" },
            ],
          },
        ],
      },
    ],
  },
  {
    id: "lake",
    name: "event-lake",
    type: "S3",
    accent: "bg-orange-500",
    discovery: {
      status: "partial",
      lastRefreshed: "just now",
      scope: "s3://event-lake/events · object listing",
      detail: "The archive/ prefix was skipped because this role cannot list it.",
    },
    schemas: [
      {
        database: "s3://event-lake",
        name: "events",
        tables: [
          {
            name: "product_events/*.parquet",
            kind: "external_table",
            rows: "3.8 GB",
            freshness: "Listed just now",
            description: "Partitioned raw product events stored as Parquet objects.",
            columns: [
              { name: "event_name", type: "string" },
              { name: "anonymous_id", type: "string" },
              { name: "received_at", type: "timestamp" },
            ],
          },
        ],
      },
    ],
  },
  {
    id: "duckdb-default",
    name: "duckdb-default",
    type: "DuckDB",
    accent: "bg-amber-500",
    discovery: {
      status: "ready",
      lastRefreshed: "4 minutes ago",
      scope: "Local file · all schemas",
    },
    schemas: [
      {
        database: "analytics.duckdb",
        name: "main",
        tables: [
          {
            name: "notebook_snapshots",
            kind: "table",
            rows: "18k",
            freshness: "Local · updated 4m ago",
            description: "Locally materialized notebook source snapshots.",
            columns: [
              { name: "snapshot_id", type: "varchar" },
              { name: "created_at", type: "timestamp" },
              { name: "row_count", type: "bigint" },
            ],
          },
        ],
      },
    ],
  },
  {
    id: "finance-readonly",
    name: "finance-readonly",
    type: "Trino",
    accent: "bg-fuchsia-500",
    discovery: {
      status: "error",
      lastRefreshed: "31 minutes ago",
      scope: "finance catalog · metadata only",
      detail: "The last refresh lost its session. Last-known-good metadata is still available.",
    },
    schemas: [
      {
        database: "finance",
        name: "reporting",
        tables: [
          {
            name: "monthly_close",
            kind: "view",
            rows: "42k",
            freshness: "Last observed 31m ago",
            description: "Monthly finance close positions exposed through Trino.",
            columns: [
              { name: "period", type: "date" },
              { name: "account", type: "varchar" },
              { name: "amount", type: "decimal(18,2)" },
            ],
          },
        ],
      },
    ],
  },
  {
    id: "clickhouse-sandbox",
    name: "clickhouse-sandbox",
    type: "ClickHouse",
    accent: "bg-yellow-500",
    discovery: {
      status: "empty",
      lastRefreshed: "12 minutes ago",
      scope: "sandbox database · metadata only",
      detail: "The connection succeeded, but this role currently sees no tables.",
    },
    schemas: [],
  },
];

export function canDropAsset(kind: AssetKind, target: "root" | "downstream" | "gate" | "test") {
  if (target === "root") {
    return ["source", "seed", "api", "ingestr", "sql", "python", "load", "sensor"].includes(kind);
  }
  if (target === "downstream") {
    return ["sql", "python", "load"].includes(kind);
  }
  if (target === "gate") {
    return kind === "sensor";
  }
  return kind === "unittest";
}

export function createDroppedAsset(
  palette: PaletteAsset,
  sequence: number,
  upstream?: AppAsset,
): AppLineageCanvasAsset {
  const slug = `${palette.kind}_${sequence}`;
  const prefix = upstream ? "analytics" : palette.kind === "source" ? "raw" : "staging";
  return {
    id: `mock-${slug}`,
    name: `${prefix}.${slug}`,
    displayName: slug,
    group: prefix,
    prefix,
    kind: palette.kind,
    integration:
      palette.kind === "python" ? "Python" : palette.kind === "source" ? "Postgres" : "DuckDB",
    description: `New ${palette.label.toLowerCase()} from the design study`,
    status: "pending",
    materializedAt: palette.kind === "source" ? "External source" : "Not built",
    upstreams: upstream ? [upstream.id] : undefined,
    x: 760,
    y: 320,
  };
}
