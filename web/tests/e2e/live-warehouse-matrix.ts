import { spawn, type ChildProcess } from "node:child_process";
import { randomUUID } from "node:crypto";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import https from "node:https";
import net from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { rootCertificates } from "node:tls";

const webDir = resolve(__dirname, "..", "..");
const repoRoot = resolve(webDir, "..");
const trinoCatalogDir = resolve(
  webDir,
  "tests",
  "fixtures",
  "multi-warehouse-workspace",
  "trino-catalog",
);
const databricksSailProxyDir = resolve(webDir, "tests", "e2e", "helpers", "databricks-sail-proxy");

const POSTGRES_IMAGE = "postgres:16-alpine";
const CLICKHOUSE_IMAGE = "clickhouse/clickhouse-server:25.8.28-alpine";
const TRINO_IMAGE = "trinodb/trino:482";
const STARROCKS_IMAGE = "starrocks/allin1-ubuntu:3.5-latest";
const MINIO_IMAGE = "quay.io/minio/minio:RELEASE.2025-06-13T11-33-47Z";
const MINIO_CLIENT_IMAGE = "quay.io/minio/mc:RELEASE.2025-05-21T01-59-54Z";
const MINIO_ACCESS_KEY = "renart";
const MINIO_SECRET_KEY = "renart-secret";
const DUCKLAKE_BUCKET = "renart-ducklake";
const PYSAIL_PACKAGE = "pysail==0.6.6";

type ManagedProcess = {
  child: ChildProcess;
  command: string;
  output: () => string;
};

export type LiveWarehouseMatrix = {
  postgresPort: number;
  clickhouseNativePort: number;
  clickhouseHTTPPort: number;
  trinoPort: number;
  starrocksMySQLPort: number;
  starrocksHTTPPort: number;
  starrocksStreamLoadPort: number;
  minioPort: number;
  databricksPort: number;
  databricksTrustBundle: string | null;
  dispose: () => Promise<void>;
};

export async function createLiveWarehouseMatrix(
  enabledWarehouses: Iterable<string> = [
    "duckdb",
    "ducklake",
    "postgres",
    "trino",
    "clickhouse",
    "starrocks",
    "databricks",
  ],
): Promise<LiveWarehouseMatrix> {
  const enabled = new Set(enabledWarehouses);
  const suffix = randomUUID().slice(0, 8);
  const networkName = `renart-e2e-warehouse-${suffix}`;
  const postgresName = `renart-e2e-pg-${suffix}`;
  const clickhouseName = `renart-e2e-ch-${suffix}`;
  const trinoName = `renart-e2e-trino-${suffix}`;
  const starrocksName = `renart-e2e-starrocks-${suffix}`;
  const minioName = `renart-e2e-minio-${suffix}`;
  const [
    postgresPort,
    clickhouseNativePort,
    clickhouseHTTPPort,
    trinoPort,
    starrocksMySQLPort,
    starrocksHTTPPort,
    starrocksStreamLoadPort,
    minioPort,
    databricksFlightPort,
    databricksPort,
  ] = await Promise.all(Array.from({ length: 10 }, () => getAvailablePort()));

  const containers: string[] = [];
  const processes: ManagedProcess[] = [];
  let databricksTempDir: string | null = null;
  let databricksTLSDir: string | null = null;
  let databricksTrustBundle: string | null = null;
  let networkCreated = false;
  const dispose = async () => {
    for (const process of processes.reverse()) {
      await stopManagedProcess(process);
    }
    for (const container of containers.reverse()) {
      await runCommand(["docker", "rm", "-f", container], true);
    }
    if (networkCreated) {
      await runCommand(["docker", "network", "rm", networkName], true);
    }
    if (databricksTempDir) {
      await rm(databricksTempDir, { recursive: true, force: true });
    }
  };

  try {
    await runCommand(["docker", "network", "create", networkName]);
    networkCreated = true;

    await runCommand([
      "docker",
      "run",
      "--rm",
      "-d",
      "--name",
      postgresName,
      "--network",
      networkName,
      "--network-alias",
      "postgres",
      "-e",
      "POSTGRES_DB=renart_postgres",
      "-e",
      "POSTGRES_USER=postgres",
      "-e",
      "POSTGRES_PASSWORD=postgres",
      "--tmpfs",
      "/var/lib/postgresql/data:rw,noexec,nosuid,size=256m",
      "-p",
      `127.0.0.1:${postgresPort}:5432`,
      POSTGRES_IMAGE,
    ]);
    containers.push(postgresName);
    await waitForCommand([
      "docker",
      "exec",
      postgresName,
      "psql",
      "-U",
      "postgres",
      "-d",
      "renart_postgres",
      "-tAc",
      "select 1",
    ]);
    await runCommand([
      "docker",
      "exec",
      postgresName,
      "psql",
      "-U",
      "postgres",
      "-d",
      "renart_postgres",
      "-v",
      "ON_ERROR_STOP=1",
      "-c",
      "create schema if not exists analytics;",
    ]);
    await runCommand([
      "docker",
      "exec",
      postgresName,
      "psql",
      "-U",
      "postgres",
      "-d",
      "renart_postgres",
      "-v",
      "ON_ERROR_STOP=1",
      "-c",
      "create database renart_source;",
    ]);
    await runCommand([
      "docker",
      "exec",
      postgresName,
      "psql",
      "-U",
      "postgres",
      "-d",
      "renart_source",
      "-v",
      "ON_ERROR_STOP=1",
      "-c",
      `create schema if not exists analytics;
create table analytics.customer_activity_source (
  customer_id integer primary key,
  activity_score integer not null
);
insert into analytics.customer_activity_source (customer_id, activity_score) values
  (1, 5),
  (2, 7),
  (3, 3),
  (4, 4),
  (5, 6);`,
    ]);

    if (enabled.has("databricks")) {
      databricksTempDir = await mkdtemp(join(tmpdir(), "renart-databricks-sail-"));
      databricksTLSDir = join(databricksTempDir, "tls");
      const proxyBinary = join(databricksTempDir, "databricks-sail-proxy");
      await runCommand(
        [
          process.env.RENART_GO_BINARY ?? "go",
          "-C",
          databricksSailProxyDir,
          "build",
          "-o",
          proxyBinary,
          ".",
        ],
        false,
      );

      const sail = startManagedProcess(
        [
          process.env.RENART_UV_BINARY ?? "uv",
          "tool",
          "run",
          "--no-config",
          "--python",
          "3.11",
          "--from",
          PYSAIL_PACKAGE,
          "sail",
          "flight",
          "server",
          "--ip",
          "127.0.0.1",
          "--port",
          String(databricksFlightPort),
        ],
        databricksTempDir,
      );
      processes.push(sail);
      await waitForTCP(databricksFlightPort, sail);

      const proxy = startManagedProcess(
        [
          proxyBinary,
          "--listen",
          `127.0.0.1:${databricksPort}`,
          "--flight-uri",
          `grpc://127.0.0.1:${databricksFlightPort}`,
          "--tls-dir",
          databricksTLSDir,
          "--schema",
          "analytics",
        ],
        databricksTempDir,
      );
      processes.push(proxy);
      await waitForHTTPS(
        `https://127.0.0.1:${databricksPort}/healthz`,
        join(databricksTLSDir, "ca.pem"),
        proxy,
      );
      databricksTrustBundle = join(databricksTempDir, "trust-bundle.pem");
      const testCA = await readFile(join(databricksTLSDir, "ca.pem"), "utf8");
      await writeFile(
        databricksTrustBundle,
        [...rootCertificates, testCA].join("\n") + "\n",
        "utf8",
      );
    }

    if (enabled.has("clickhouse")) {
      await runCommand([
        "docker",
        "run",
        "--rm",
        "-d",
        "--name",
        clickhouseName,
        "--network",
        networkName,
        "-e",
        "CLICKHOUSE_DB=analytics",
        "-e",
        "CLICKHOUSE_USER=renart",
        "-e",
        "CLICKHOUSE_PASSWORD=renart",
        "-e",
        "CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT=1",
        "--tmpfs",
        "/var/lib/clickhouse:rw,noexec,nosuid,size=512m",
        "-p",
        `127.0.0.1:${clickhouseNativePort}:9000`,
        "-p",
        `127.0.0.1:${clickhouseHTTPPort}:8123`,
        CLICKHOUSE_IMAGE,
      ]);
      containers.push(clickhouseName);
      await waitForCommand([
        "docker",
        "exec",
        clickhouseName,
        "clickhouse-client",
        "--user",
        "renart",
        "--password",
        "renart",
        "--query",
        "select 1",
      ]);
    }

    if (enabled.has("trino")) {
      await runCommand([
        "docker",
        "run",
        "--rm",
        "-d",
        "--name",
        trinoName,
        "--network",
        networkName,
        "-p",
        `127.0.0.1:${trinoPort}:8080`,
        "--volume",
        `${trinoCatalogDir}:/etc/trino/catalog:ro`,
        TRINO_IMAGE,
      ]);
      containers.push(trinoName);
      await waitForCommand(["docker", "exec", trinoName, "trino", "--execute", "select 1"]);
      await runCommand([
        "docker",
        "exec",
        trinoName,
        "trino",
        "--catalog",
        "memory",
        "--execute",
        "create schema if not exists analytics",
      ]);
    }

    if (enabled.has("starrocks")) {
      await runCommand([
        "docker",
        "run",
        "--rm",
        "-d",
        "--name",
        starrocksName,
        "--network",
        networkName,
        "--tmpfs",
        "/data/deploy/starrocks/be/storage:rw,noexec,nosuid,size=2g",
        "-p",
        `127.0.0.1:${starrocksMySQLPort}:9030`,
        "-p",
        `127.0.0.1:${starrocksHTTPPort}:8030`,
        "-p",
        `127.0.0.1:${starrocksStreamLoadPort}:8040`,
        STARROCKS_IMAGE,
      ]);
      containers.push(starrocksName);
      await waitForCommand([
        "docker",
        "exec",
        starrocksName,
        "bash",
        "-lc",
        `mysql -P 9030 -h 127.0.0.1 -u root -N -e "show backends" | grep -q $'\\ttrue\\t'`,
      ]);
      await runCommand([
        "docker",
        "exec",
        starrocksName,
        "mysql",
        "-P",
        "9030",
        "-h",
        "127.0.0.1",
        "-u",
        "root",
        "-e",
        "create database if not exists analytics",
      ]);
    }

    if (enabled.has("ducklake")) {
      await runCommand([
        "docker",
        "run",
        "--rm",
        "-d",
        "--name",
        minioName,
        "--network",
        networkName,
        "--network-alias",
        "minio",
        "-e",
        `MINIO_ROOT_USER=${MINIO_ACCESS_KEY}`,
        "-e",
        `MINIO_ROOT_PASSWORD=${MINIO_SECRET_KEY}`,
        "--tmpfs",
        "/data:rw,noexec,nosuid,size=512m",
        "-p",
        `127.0.0.1:${minioPort}:9000`,
        MINIO_IMAGE,
        "server",
        "/data",
      ]);
      containers.push(minioName);
      await waitForHTTP(`http://127.0.0.1:${minioPort}/minio/health/live`);
      await runCommand([
        "docker",
        "run",
        "--rm",
        "--network",
        networkName,
        "--entrypoint",
        "/bin/sh",
        MINIO_CLIENT_IMAGE,
        "-c",
        `mc alias set local http://minio:9000 ${MINIO_ACCESS_KEY} ${MINIO_SECRET_KEY} >/dev/null && mc mb --ignore-existing local/${DUCKLAKE_BUCKET}`,
      ]);
    }

    return {
      postgresPort,
      clickhouseNativePort,
      clickhouseHTTPPort,
      trinoPort,
      starrocksMySQLPort,
      starrocksHTTPPort,
      starrocksStreamLoadPort,
      minioPort,
      databricksPort,
      databricksTrustBundle,
      dispose,
    };
  } catch (error) {
    await dispose();
    throw error;
  }
}

async function waitForTCP(port: number, process?: ManagedProcess) {
  const deadline = Date.now() + 180_000;
  let lastError: unknown;
  while (Date.now() < deadline) {
    throwIfExited(process);
    try {
      await new Promise<void>((resolveConnection, rejectConnection) => {
        const socket = net.createConnection({ host: "127.0.0.1", port });
        socket.setTimeout(1_000);
        socket.once("connect", () => {
          socket.destroy();
          resolveConnection();
        });
        socket.once("timeout", () => {
          socket.destroy();
          rejectConnection(new Error(`Timed out connecting to 127.0.0.1:${port}`));
        });
        socket.once("error", rejectConnection);
      });
      return;
    } catch (error) {
      lastError = error;
      await new Promise((resolveDelay) => setTimeout(resolveDelay, 500));
    }
  }
  throw processError(process, lastError ?? new Error(`Timed out waiting for 127.0.0.1:${port}`));
}

async function waitForHTTPS(url: string, caPath: string, process: ManagedProcess) {
  const deadline = Date.now() + 180_000;
  let lastError: unknown;
  while (Date.now() < deadline) {
    throwIfExited(process);
    try {
      const ca = await readFile(caPath);
      await new Promise<void>((resolveRequest, rejectRequest) => {
        const request = https.get(url, { ca, timeout: 2_000 }, (response) => {
          response.resume();
          if (response.statusCode === 200) {
            resolveRequest();
            return;
          }
          rejectRequest(new Error(`${url} returned ${response.statusCode}`));
        });
        request.once("timeout", () => request.destroy(new Error(`${url} timed out`)));
        request.once("error", rejectRequest);
      });
      return;
    } catch (error) {
      lastError = error;
      await new Promise((resolveDelay) => setTimeout(resolveDelay, 500));
    }
  }
  throw processError(process, lastError ?? new Error(`Timed out waiting for ${url}`));
}

async function waitForCommand(args: string[]) {
  const deadline = Date.now() + 180_000;
  let lastError: unknown;
  while (Date.now() < deadline) {
    try {
      await runCommand(args);
      return;
    } catch (error) {
      lastError = error;
      await new Promise((resolveDelay) => setTimeout(resolveDelay, 500));
    }
  }
  throw lastError ?? new Error(`Timed out waiting for ${args.join(" ")}`);
}

async function waitForHTTP(url: string) {
  const deadline = Date.now() + 180_000;
  let lastError: unknown;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url);
      if (response.ok) return;
      lastError = new Error(`${url} returned ${response.status}`);
    } catch (error) {
      lastError = error;
    }
    await new Promise((resolveDelay) => setTimeout(resolveDelay, 500));
  }
  throw lastError ?? new Error(`Timed out waiting for ${url}`);
}

function getAvailablePort() {
  return new Promise<number>((resolvePort, reject) => {
    const server = net.createServer();
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      if (!address || typeof address === "string") {
        server.close();
        reject(new Error("Could not allocate a live warehouse port."));
        return;
      }
      server.close(() => resolvePort(address.port));
    });
    server.on("error", reject);
  });
}

function runCommand(args: string[], allowFailure = false) {
  return new Promise<void>((resolveRun, rejectRun) => {
    const child = spawn(args[0], args.slice(1), {
      cwd: repoRoot,
      env: process.env,
      stdio: "pipe",
    });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk) => {
      stdout += String(chunk);
    });
    child.stderr.on("data", (chunk) => {
      stderr += String(chunk);
    });
    child.on("exit", (code) => {
      if (code === 0 || allowFailure) {
        resolveRun();
        return;
      }
      rejectRun(
        new Error(
          [stderr.trim(), stdout.trim(), `${args.join(" ")} failed with exit code ${code}`]
            .filter(Boolean)
            .join("\n"),
        ),
      );
    });
    child.on("error", rejectRun);
  });
}

function startManagedProcess(args: string[], cwd: string): ManagedProcess {
  const child = spawn(args[0], args.slice(1), {
    cwd,
    env: process.env,
    stdio: "pipe",
  });
  let output = "";
  const appendOutput = (chunk: unknown) => {
    output = (output + String(chunk)).slice(-200_000);
  };
  child.stdout?.on("data", appendOutput);
  child.stderr?.on("data", appendOutput);
  return {
    child,
    command: args.join(" "),
    output: () => output.trim(),
  };
}

function throwIfExited(process?: ManagedProcess) {
  if (process && (process.child.exitCode !== null || process.child.signalCode !== null)) {
    throw processError(process, new Error("process exited before becoming ready"));
  }
}

function processError(process: ManagedProcess | undefined, cause: unknown) {
  if (!process) return cause instanceof Error ? cause : new Error(String(cause));
  return new Error(
    [
      cause instanceof Error ? cause.message : String(cause),
      process.output(),
      `Command: ${process.command}`,
    ]
      .filter(Boolean)
      .join("\n"),
  );
}

async function stopManagedProcess(process: ManagedProcess) {
  if (process.child.exitCode !== null || process.child.signalCode !== null) return;
  process.child.kill("SIGTERM");
  if (await waitForProcessExit(process.child, 10_000)) return;
  process.child.kill("SIGKILL");
  await waitForProcessExit(process.child, 5_000);
}

function waitForProcessExit(child: ChildProcess, timeoutMs: number) {
  if (child.exitCode !== null || child.signalCode !== null) return Promise.resolve(true);
  return new Promise<boolean>((resolveExit) => {
    const onExit = () => {
      clearTimeout(timer);
      resolveExit(true);
    };
    const timer = setTimeout(() => {
      child.off("exit", onExit);
      resolveExit(false);
    }, timeoutMs);
    child.once("exit", onExit);
  });
}
