"use client";

import type { ComponentType, SVGProps } from "react";
import AmazonRedshiftIcon from "~icons/simple-icons/amazonredshift";
import AmazonS3Icon from "~icons/simple-icons/amazons3";
import ApacheDorisIcon from "~icons/simple-icons/apachedoris";
import ClickHouseIcon from "~icons/simple-icons/clickhouse";
import DatabricksIcon from "~icons/simple-icons/databricks";
import DuckDBIcon from "~icons/simple-icons/duckdb";
import GoogleBigQueryIcon from "~icons/simple-icons/googlebigquery";
import GoogleCloudIcon from "~icons/simple-icons/googlecloud";
import MicrosoftSQLServerIcon from "~icons/simple-icons/microsoftsqlserver";
import MySQLIcon from "~icons/simple-icons/mysql";
import OracleIcon from "~icons/simple-icons/oracle";
import PostgreSQLIcon from "~icons/simple-icons/postgresql";
import SnowflakeIcon from "~icons/simple-icons/snowflake";
import SQLiteIcon from "~icons/simple-icons/sqlite";
import TrinoIcon from "~icons/simple-icons/trino";

import { cn } from "@/lib/utils";

type BrandIcon = ComponentType<SVGProps<SVGSVGElement>>;

const connectionBrandIcons: Partial<Record<string, BrandIcon>> = {
  bigquery: GoogleBigQueryIcon,
  clickhouse: ClickHouseIcon,
  databricks: DatabricksIcon,
  doris: ApacheDorisIcon,
  duckdb: DuckDBIcon,
  gcs: GoogleCloudIcon,
  mssql: MicrosoftSQLServerIcon,
  mysql: MySQLIcon,
  oracle: OracleIcon,
  postgres: PostgreSQLIcon,
  redshift: AmazonRedshiftIcon,
  s3: AmazonS3Icon,
  snowflake: SnowflakeIcon,
  sqlite: SQLiteIcon,
  trino: TrinoIcon,
};

export function normalizeConnectionType(connectionType?: string | null) {
  const normalized = (connectionType ?? "").trim().toLowerCase();
  const aliases: Record<string, string> = {
    postgresql: "postgres",
    pg: "postgres",
    google_cloud_platform: "bigquery",
    gcp: "bigquery",
    duckdb_file: "duckdb",
    motherduck: "duckdb",
    synapse: "mssql",
    fabric: "mssql",
    planetscale_mysql: "mysql",
    vitess: "mysql",
    google_cloud_storage: "gcs",
    local_file: "file",
  };
  return aliases[normalized] ?? (normalized || "default");
}

export function friendlyConnectionType(connectionType?: string | null) {
  const normalized = normalizeConnectionType(connectionType);
  const labels: Record<string, string> = {
    bigquery: "BigQuery",
    clickhouse: "ClickHouse",
    databricks: "Databricks",
    doris: "Apache Doris",
    duckdb: "DuckDB",
    file: "Local file",
    gcs: "Google Cloud Storage",
    mssql: "SQL Server",
    mysql: "MySQL",
    oracle: "Oracle",
    postgres: "PostgreSQL",
    redshift: "Redshift",
    s3: "Amazon S3",
    snowflake: "Snowflake",
    sqlite: "SQLite",
    starrocks: "StarRocks",
    trino: "Trino",
    vertica: "Vertica",
  };
  if (labels[normalized]) return labels[normalized];
  return (connectionType ?? "Connection").trim() || "Connection";
}

export function ConnectionTypeIcon({
  connectionType,
  className,
}: {
  connectionType?: string | null;
  className?: string;
}) {
  const engine = normalizeConnectionType(connectionType);
  return (
    <span
      aria-hidden="true"
      data-connection-engine={engine}
      className={cn(
        "connection-type-icon relative grid size-7 shrink-0 place-items-center rounded-[22%] border border-current/20 bg-current/10",
        className,
      )}
    >
      <ConnectionGlyph engine={engine} />
    </span>
  );
}

function ConnectionGlyph({ engine }: { engine: string }) {
  const BrandIcon = connectionBrandIcons[engine];
  if (BrandIcon) {
    return <BrandIcon className="relative size-[58%] fill-current" />;
  }

  switch (engine) {
    case "starrocks":
      return <BarsGlyph />;
    case "file":
      return <FileGlyph />;
    default:
      return <DatabaseGlyph />;
  }
}

function DatabaseGlyph() {
  return (
    <svg viewBox="0 0 32 32" className="relative size-[70%]" fill="none">
      <g className="stroke-current" strokeWidth="1.6">
        <ellipse cx="16" cy="10" rx="7.5" ry="3.2" className="fill-current" fillOpacity=".14" />
        <path d="M8.5 10v6c0 1.8 3.4 3.2 7.5 3.2s7.5-1.4 7.5-3.2v-6" />
        <path d="M8.5 16v6c0 1.8 3.4 3.2 7.5 3.2s7.5-1.4 7.5-3.2v-6" />
      </g>
    </svg>
  );
}

function BarsGlyph() {
  return (
    <svg viewBox="0 0 32 32" className="relative size-[70%]" fill="none">
      <g className="fill-current">
        <rect x="8" y="9" width="3" height="14" rx=".8" opacity=".45" />
        <rect x="13" y="9" width="3" height="14" rx=".8" opacity=".7" />
        <rect x="18" y="9" width="3" height="14" rx=".8" />
        <rect x="23" y="9" width="2" height="14" rx=".8" opacity=".3" />
      </g>
    </svg>
  );
}

function FileGlyph() {
  return (
    <svg viewBox="0 0 32 32" className="relative size-[70%]" fill="none">
      <g className="stroke-current" strokeWidth="1.6" strokeLinejoin="round">
        <path d="M10 7.5h8l4 4V25H10Z" className="fill-current" fillOpacity=".08" />
        <path d="M18 7.5V12h4M13 16h6M13 20h6" strokeLinecap="round" />
      </g>
    </svg>
  );
}
