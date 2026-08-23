import { test as base, type TestInfo } from "@playwright/test";
import { spawn } from "node:child_process";
import { cpSync, existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import http from "node:http";
import net from "node:net";
import { open } from "node:fs/promises";
import { join, resolve } from "node:path";
import { randomUUID } from "node:crypto";

export type LiveApp = {
  baseURL: string;
  workspaceDir: string;
};

export type LivePostgres = {
  host: string;
  port: number;
  user: string;
  password: string;
  database: string;
};

type LiveAppTimings = {
  fixture: string;
  workspaceSetupMs: number;
  serverStartupMs?: number;
  testBodyMs?: number;
  serverTeardownMs: number;
  serverReady: boolean;
};

const retryTestTimeoutExtensionMs = 60_000;

export function timeoutForRetry(
  testInfo: Pick<TestInfo, "retry">,
  timeoutMs: number,
  retryExtensionMs = timeoutMs,
) {
  return testInfo.retry > 0 ? timeoutMs + retryExtensionMs : timeoutMs;
}

const webDir = resolve(__dirname, "..", "..");
const repoRoot = resolve(webDir, "..");
const defaultBinaryPath = existsSync(resolve(repoRoot, "renart"))
  ? resolve(repoRoot, "renart")
  : resolve(repoRoot, "bruin");
const binaryPath = process.env.BRUIN_E2E_BINARY || defaultBinaryPath;
const host = process.env.BRUIN_E2E_HOST || "127.0.0.1";
const staticDir = resolve(webDir, "dist");
const e2eWorkspaceRoot = resolve(repoRoot, ".playwright-live-workspaces");
const postgresLockPath = resolve(e2eWorkspaceRoot, "postgres.lock");

export const liveTest = base.extend<{
  fixtureName: string;
  isolateUserConfig: boolean;
  liveAppEnv: Record<string, string | undefined>;
  liveApp: LiveApp;
  livePostgres: LivePostgres | null;
}>({
  fixtureName: ["basic-workspace", { option: true }],
  isolateUserConfig: [false, { option: true }],
  liveAppEnv: [{}, { option: true }],
  livePostgres: [
    async ({ fixtureName }, use) => {
      if (
        fixtureName !== "empty-workspace-postgres" &&
        fixtureName !== "notebook-postgres-workspace"
      ) {
        await use(null);
        return;
      }

      const postgres = await createLivePostgres();
      try {
        await use(postgres.connection);
      } finally {
        await postgres.dispose();
      }
    },
    { timeout: 120000 },
  ],
  page: async ({ page }, use, testInfo) => {
    // Retries collect a trace after a full failed server lifecycle, which is
    // noticeably slower on constrained CI runners. Keep the first attempt's
    // feedback loop tight while giving retries enough end-to-end headroom.
    if (testInfo.retry > 0) {
      testInfo.setTimeout(timeoutForRetry(testInfo, testInfo.timeout, retryTestTimeoutExtensionMs));
    }

    const networkEvents: Array<Record<string, unknown>> = [];
    const requestStartedAt = new WeakMap<object, number>();

    const recordEvent = (event: Record<string, unknown>) => {
      networkEvents.push({
        timestamp: new Date().toISOString(),
        ...event,
      });
    };

    const onRequest = (request: Parameters<typeof page.on>[1] extends never ? never : any) => {
      requestStartedAt.set(request, Date.now());
      recordEvent({
        type: "request",
        method: request.method(),
        url: request.url(),
        resourceType: request.resourceType(),
        headers: request.headers(),
        postData: request.postData() ?? undefined,
      });
    };

    const onResponse = (response: Parameters<typeof page.on>[1] extends never ? never : any) => {
      const request = response.request();
      const startedAt = requestStartedAt.get(request);
      recordEvent({
        type: "response",
        method: request.method(),
        url: response.url(),
        resourceType: request.resourceType(),
        status: response.status(),
        statusText: response.statusText(),
        ok: response.ok(),
        durationMs: startedAt ? Date.now() - startedAt : undefined,
        headers: response.headers(),
      });
    };

    const onRequestFailed = (
      request: Parameters<typeof page.on>[1] extends never ? never : any,
    ) => {
      const startedAt = requestStartedAt.get(request);
      recordEvent({
        type: "requestfailed",
        method: request.method(),
        url: request.url(),
        resourceType: request.resourceType(),
        durationMs: startedAt ? Date.now() - startedAt : undefined,
        failure: request.failure()?.errorText ?? "unknown",
      });
    };

    page.on("request", onRequest);
    page.on("response", onResponse);
    page.on("requestfailed", onRequestFailed);

    try {
      await use(page);
    } finally {
      page.off("request", onRequest);
      page.off("response", onResponse);
      page.off("requestfailed", onRequestFailed);

      if (testInfo.status !== testInfo.expectedStatus) {
        const networkLogPath = testInfo.outputPath("network-requests.json");
        writeFileSync(networkLogPath, JSON.stringify(networkEvents, null, 2), "utf8");
        await testInfo.attach("network-requests", {
          path: networkLogPath,
          contentType: "application/json",
        });
      }
    }
  },
  liveApp: async ({ fixtureName, isolateUserConfig, livePostgres, liveAppEnv }, use, testInfo) => {
    const fixtureStartedAt = Date.now();
    const timings: LiveAppTimings = {
      fixture: fixtureName,
      workspaceSetupMs: 0,
      serverTeardownMs: 0,
      serverReady: false,
    };
    void livePostgres;
    if (!existsSync(binaryPath)) {
      throw new Error(
        `Renart binary not found at ${binaryPath}. Build it first or set BRUIN_E2E_BINARY.`,
      );
    }

    const fixtureRoot = resolve(webDir, "tests", "fixtures", fixtureName);
    mkdirSync(e2eWorkspaceRoot, { recursive: true });
    const workspaceDir = mkdtempSync(join(e2eWorkspaceRoot, "renart-e2e-"));
    cpSync(fixtureRoot, workspaceDir, { recursive: true });
    mkdirSync(join(workspaceDir, ".git"));
    mkdirSync(join(workspaceDir, "duckdb-files"));
    const configPath = join(workspaceDir, ".bruin.yml");
    if (fixtureName === "notebook-postgres-workspace" && livePostgres) {
      writeFileSync(
        configPath,
        `default_environment: default
environments:
  default:
    connections:
      postgres:
        - name: postgres-orders
          host: ${livePostgres.host}
          port: ${livePostgres.port}
          username: ${livePostgres.user}
          password: ${livePostgres.password}
          database: ${livePostgres.database}
          schema: analytics
          ssl_mode: disable
        - name: postgres-customers
          host: ${livePostgres.host}
          port: ${livePostgres.port}
          username: ${livePostgres.user}
          password: ${livePostgres.password}
          database: ${livePostgres.database}
          schema: analytics
          ssl_mode: disable
`,
        "utf8",
      );
    }
    if (!existsSync(configPath) && !fixtureName.startsWith("empty-workspace")) {
      writeFileSync(
        configPath,
        "environments:\n  default:\n    connections:\n      duckdb:\n        - name: duckdb-default\n          path: duckdb-files/local.db\n",
        "utf8",
      );
    }

    const port = await getAvailablePort();
    const baseURL = `http://${host}:${port}`;
    timings.workspaceSetupMs = Date.now() - fixtureStartedAt;
    const serverStartedAt = Date.now();
    const child = spawn(
      binaryPath,
      [
        "web",
        "--host",
        `${host}`,
        "--port",
        String(port),
        "--static-dir",
        staticDir,
        "--watch-mode",
        "poll",
        "--no-open",
        workspaceDir,
      ],
      {
        cwd: repoRoot,
        // Point the global project registry into the throwaway workspace so
        // test runs never touch (or leave temp entries in) the user's real
        // ~/.config/renart/projects.json. It dies with the workspace dir.
        env: {
          ...process.env,
          ...(isolateUserConfig
            ? { XDG_CONFIG_HOME: join(workspaceDir, ".renart", "config") }
            : {}),
          RENART_PROJECTS_REGISTRY: join(workspaceDir, ".renart", "projects.json"),
          ...liveAppEnv,
        },
        stdio: "inherit",
      },
    );

    try {
      await waitForServer(baseURL);
      timings.serverReady = true;
      timings.serverStartupMs = Date.now() - serverStartedAt;
      const bodyStartedAt = Date.now();
      try {
        await use({ baseURL, workspaceDir });
      } finally {
        timings.testBodyMs = Date.now() - bodyStartedAt;
      }
    } finally {
      if (timings.serverStartupMs === undefined) {
        timings.serverStartupMs = Date.now() - serverStartedAt;
      }
      const teardownStartedAt = Date.now();
      try {
        child.kill("SIGTERM");
        await waitForExit(child);
        await removeDirectoryWithRetry(workspaceDir);
      } finally {
        timings.serverTeardownMs = Date.now() - teardownStartedAt;
        await testInfo.attach("live-app-timings", {
          body: Buffer.from(JSON.stringify(timings)),
          contentType: "application/json",
        });
      }
    }
  },
});

export const liveServerBinaryPath = binaryPath;
export const liveServerStaticDir = staticDir;
export const liveServerHost = host;
export const liveServerRepoRoot = repoRoot;

export type SpawnedServer = {
  baseURL: string;
  child: ReturnType<typeof spawn>;
};

// startLiveServer boots a Renart server against an existing workspace directory.
// Used by tests that need to restart the server (e.g. crash recovery), which
// the single-server liveApp fixture does not cover.
export async function startLiveServer(
  workspaceDir: string,
  environment: Record<string, string | undefined> = {},
): Promise<SpawnedServer> {
  if (!existsSync(binaryPath)) {
    throw new Error(
      `Renart binary not found at ${binaryPath}. Build it first or set BRUIN_E2E_BINARY.`,
    );
  }
  const port = await getAvailablePort();
  const baseURL = `http://${host}:${port}`;
  const child = spawn(
    binaryPath,
    [
      "web",
      "--host",
      host,
      "--port",
      String(port),
      "--static-dir",
      staticDir,
      "--watch-mode",
      "poll",
      "--no-open",
      workspaceDir,
    ],
    {
      cwd: repoRoot,
      env: {
        ...process.env,
        RENART_PROJECTS_REGISTRY: join(workspaceDir, ".renart", "projects.json"),
        ...environment,
      },
      stdio: "inherit",
    },
  );
  await waitForServer(baseURL);
  return { baseURL, child };
}

export async function stopLiveServer(server: SpawnedServer, signal: NodeJS.Signals = "SIGTERM") {
  server.child.kill(signal);
  await waitForExit(server.child);
}

export async function createLivePostgres() {
  mkdirSync(e2eWorkspaceRoot, { recursive: true });
  const releasePostgresLock = await acquireFileLock(postgresLockPath);
  const hostPort = await getAvailablePort();
  const containerName = `renart-e2e-pg-${randomUUID().slice(0, 8)}`;
  const database = "bruin";
  const user = "postgres";
  const password = "postgres";

  try {
    await runCommand([
      "docker",
      "run",
      "--rm",
      "-d",
      "--name",
      containerName,
      "-e",
      `POSTGRES_DB=${database}`,
      "-e",
      `POSTGRES_USER=${user}`,
      "-e",
      `POSTGRES_PASSWORD=${password}`,
      "--tmpfs",
      "/var/lib/postgresql/data:rw,noexec,nosuid,size=64m",
      "-p",
      `${hostPort}:5432`,
      "postgres:16-alpine",
    ]);
    await waitForPostgres(containerName, user);
    await ensurePostgresDatabase(containerName, user, database);
    await waitForPostgresDatabase(containerName, user, database);
    await runCommand([
      "docker",
      "exec",
      containerName,
      "psql",
      "-U",
      user,
      "-d",
      database,
      "-c",
      [
        "create schema if not exists analytics;",
        "create table if not exists analytics.orders (order_id int primary key, order_total numeric);",
        "create table if not exists analytics.customers (customer_id int primary key, customer_name text);",
        "insert into analytics.orders (order_id, order_total) values (1, 10.5), (2, 22.0) on conflict do nothing;",
        "insert into analytics.customers (customer_id, customer_name) values (1, 'Ada'), (2, 'Grace') on conflict do nothing;",
      ].join(" "),
    ]);

    return {
      connection: {
        host,
        port: hostPort,
        user,
        password,
        database,
      } satisfies LivePostgres,
      async dispose() {
        await runCommand(["docker", "rm", "-f", containerName], { allowFailure: true });
      },
    };
  } catch (error) {
    await runCommand(["docker", "rm", "-f", containerName], { allowFailure: true });
    throw error;
  } finally {
    await releasePostgresLock();
  }
}

function waitForServer(baseURL: string) {
  const deadline = Date.now() + 60000;

  return new Promise<void>((resolveReady, reject) => {
    const attempt = () => {
      const request = http.get(baseURL, (response) => {
        response.resume();
        if ((response.statusCode ?? 500) < 500) {
          resolveReady();
          return;
        }

        if (Date.now() > deadline) {
          reject(new Error(`Timed out waiting for Renart at ${baseURL}`));
          return;
        }

        setTimeout(attempt, 250);
      });

      request.on("error", () => {
        if (Date.now() > deadline) {
          reject(new Error(`Timed out waiting for Renart at ${baseURL}`));
          return;
        }

        setTimeout(attempt, 250);
      });
    };

    attempt();
  });
}

function waitForExit(child: ReturnType<typeof spawn>) {
  return new Promise<void>((resolveDone) => {
    // `child.killed` only confirms that Node successfully sent a signal; the
    // process may still be alive and holding the workspace scheduler lock.
    if (child.exitCode !== null || child.signalCode !== null) {
      resolveDone();
      return;
    }

    const timer = setTimeout(() => {
      if (child.exitCode === null) {
        child.kill("SIGKILL");
      }
    }, 5000);

    child.once("exit", () => {
      clearTimeout(timer);
      resolveDone();
    });
  });
}

async function removeDirectoryWithRetry(path: string) {
  let lastError: unknown = null;
  for (let attempt = 0; attempt < 5; attempt += 1) {
    try {
      rmSync(path, { recursive: true, force: true });
      return;
    } catch (error) {
      lastError = error;
      await new Promise((resolveDelay) => setTimeout(resolveDelay, 100 * (attempt + 1)));
    }
  }
  throw lastError;
}

function getAvailablePort() {
  return new Promise<number>((resolvePort, reject) => {
    const server = net.createServer();
    server.listen(0, `${host}`, () => {
      const address = server.address();
      if (!address || typeof address === "string") {
        server.close();
        reject(new Error("Could not allocate a port for live E2E tests."));
        return;
      }

      const { port } = address;
      server.close(() => resolvePort(port));
    });
    server.on("error", reject);
  });
}

async function waitForPostgres(containerName: string, user: string) {
  const deadline = Date.now() + 120000;
  let consecutiveSuccesses = 0;

  while (Date.now() < deadline) {
    try {
      await runCommand([
        "docker",
        "exec",
        containerName,
        "psql",
        "-U",
        user,
        "-d",
        "postgres",
        "-c",
        "select 1;",
      ]);
      consecutiveSuccesses += 1;
      if (consecutiveSuccesses >= 2) {
        return;
      }
      await new Promise((resolveDelay) => setTimeout(resolveDelay, 500));
    } catch {
      consecutiveSuccesses = 0;
      await new Promise((resolveDelay) => setTimeout(resolveDelay, 500));
    }
  }

  throw new Error(`Timed out waiting for Postgres in container ${containerName}`);
}

async function ensurePostgresDatabase(containerName: string, user: string, database: string) {
  const deadline = Date.now() + 120000;

  while (Date.now() < deadline) {
    try {
      await runCommand([
        "docker",
        "exec",
        containerName,
        "psql",
        "-U",
        user,
        "-d",
        "postgres",
        "-c",
        `create database ${database};`,
      ]);
      return;
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      if (message.includes(`database "${database}" already exists`)) {
        return;
      }
      await new Promise((resolveDelay) => setTimeout(resolveDelay, 500));
    }
  }

  throw new Error(`Timed out creating Postgres database ${database}`);
}

async function waitForPostgresDatabase(containerName: string, user: string, database: string) {
  const deadline = Date.now() + 120000;

  while (Date.now() < deadline) {
    try {
      await runCommand([
        "docker",
        "exec",
        containerName,
        "psql",
        "-U",
        user,
        "-d",
        database,
        "-c",
        "select 1;",
      ]);
      return;
    } catch {
      await new Promise((resolveDelay) => setTimeout(resolveDelay, 500));
    }
  }

  throw new Error(`Timed out waiting for Postgres database ${database}`);
}

function runCommand(args: string[], options?: { allowFailure?: boolean }) {
  return new Promise<void>((resolveRun, rejectRun) => {
    const child = spawn(args[0], args.slice(1), {
      cwd: repoRoot,
      env: process.env,
      stdio: "pipe",
    });

    let stderr = "";
    child.stderr.on("data", (chunk) => {
      stderr += String(chunk);
    });

    child.on("exit", (code) => {
      if (code === 0 || options?.allowFailure) {
        resolveRun();
        return;
      }
      rejectRun(new Error(stderr || `${args.join(" ")} failed with exit code ${code}`));
    });
    child.on("error", rejectRun);
  });
}

async function acquireFileLock(lockPath: string) {
  const deadline = Date.now() + 120000;

  while (Date.now() < deadline) {
    try {
      const handle = await open(lockPath, "wx");
      return async () => {
        await handle.close().catch(() => undefined);
        rmSync(lockPath, { force: true });
      };
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "EEXIST") {
        throw error;
      }

      await new Promise((resolveDelay) => setTimeout(resolveDelay, 250));
    }
  }

  throw new Error(`Timed out waiting for lock ${lockPath}`);
}
