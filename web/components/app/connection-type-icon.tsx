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
  starrocks: StarRocksIcon,
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
    local_files: "file",
    "local file": "file",
    "local files": "file",
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
    sftp: "SFTP",
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

// The installed Simple Icons set has no StarRocks mark. Keep the symbol from
// https://www.starrocks.io/21782839.fs1.hubspotusercontent-na1.net/hubfs/21782839/starrocks-logo.svg
// locally, without the wordmark, using the same currentColor sizing as other brands.
function StarRocksIcon(props: SVGProps<SVGSVGElement>) {
  return (
    <svg viewBox="0 0 36 36" fill="currentColor" fillRule="evenodd" clipRule="evenodd" {...props}>
      <path d="M12.1201 15.8241C9.88035 13.9836 7.4143 11.9529 6.09963 10.869C5.61543 10.4724 5.28494 10.1991 5.18134 10.1158C4.54737 9.58931 4.05079 8.66231 5.12504 8.025C5.59516 7.74656 13.5896 3.16556 16.5392 1.46175C17.5747 .863248 18.8516 .862123 19.8898 1.46175C21.3013 2.27681 25.5364 4.71862 25.5364 4.71862C25.9817 4.97512 25.9817 5.61694 25.5364 5.87344L12.3171 13.4469C11.4394 13.9537 11.3391 15.1806 12.1201 15.8241Z" />
      <path d="M15.424 21.1954L10.0409 29.3916C9.65296 29.9828 8.87035 30.1684 8.25722 29.8146L4.69046 27.7576C3.65618 27.1602 3.01489 26.0566 3.01489 24.8596V11.1824C3.01489 10.1193 3.52218 9.1287 4.35996 8.50488C3.40225 9.24232 3.78286 10.0686 4.41683 10.5957C4.50241 10.6643 12.4163 17.1786 15.1582 19.4359C15.6841 19.8685 15.7978 20.6262 15.424 21.1954Z" />
      <path d="M24.3136 20.2019C26.5534 22.0424 29.0194 24.073 30.3341 25.157C30.8183 25.5535 31.1488 25.8269 31.2524 25.9102C31.8863 26.4367 32.3829 27.3637 31.3087 28.001C30.8385 28.2794 22.8441 32.8604 19.8945 34.5642C18.859 35.1627 17.5821 35.1638 16.5439 34.5642C15.1324 33.7492 10.8973 31.3073 10.8973 31.3073C10.452 31.0508 10.452 30.409 10.8973 30.1525L24.1166 22.5796C24.9943 22.0722 25.0946 20.8454 24.3136 20.2019Z" />
      <path d="M21.0097 14.8303L26.3927 6.63407C26.7807 6.04288 27.5633 5.85725 28.1764 6.21107L31.7432 8.26813C32.7774 8.8655 33.4187 9.96913 33.4187 11.1661V24.8433C33.4187 25.907 32.9114 26.897 32.0737 27.5208C33.0314 26.7834 32.6508 25.9571 32.0168 25.43C31.9312 25.3614 24.0173 18.8471 21.2754 16.5903C20.7495 16.1572 20.6358 15.3995 21.0097 14.8303Z" />
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
