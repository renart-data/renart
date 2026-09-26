import { execFile, spawn, type ChildProcess } from "node:child_process";
import { constants } from "node:fs";
import { access, mkdir, mkdtemp, rm } from "node:fs/promises";
import { join, resolve } from "node:path";
import { promisify } from "node:util";

const exec = promisify(execFile);
const repo = resolve(__dirname, "../../..");
// The exact source commits behind the former container tags. MinIO's community
// distribution is now source-only; no mutable tags or third-party mirrors.
const versions = {
  minio: "v0.0.0-20250613113347-a6c538c5a113",
  mc: "v0.0.0-20250521015954-f71ad84bcf0f",
};
const toolRoot =
  process.env.RENART_E2E_STORAGE_TOOL_DIR ?? resolve(repo, ".test-artifacts/storage-tools");
let toolsReady: Promise<Record<keyof typeof versions, string>> | undefined;

function buildTools() {
  return (toolsReady ??= (async () => {
    const binaries = {} as Record<keyof typeof versions, string>;
    for (const name of ["minio", "mc"] as const) {
      const dir = join(
        toolRoot,
        `${process.platform}-${process.arch}`,
        `${name}-${versions[name]}`,
      );
      const binary = join(dir, name + (process.platform === "win32" ? ".exe" : ""));
      try {
        await access(binary, constants.X_OK);
      } catch {
        await mkdir(dir, { recursive: true });
        await exec(
          process.env.RENART_GO_BINARY ?? "go",
          ["install", "-p", "1", `github.com/minio/${name}@${versions[name]}`],
          {
            cwd: repo,
            env: { ...process.env, GOBIN: dir },
            timeout: 10 * 60_000,
            maxBuffer: 8 * 1024 * 1024,
          },
        );
      }
      binaries[name] = binary;
    }
    return binaries;
  })());
}

async function stop(child: ChildProcess) {
  if (!child.pid || child.exitCode !== null || child.signalCode !== null) return;
  await new Promise<void>((done) => {
    const timer = setTimeout(() => child.kill("SIGKILL"), 5000);
    child.once("exit", () => {
      clearTimeout(timer);
      done();
    });
    child.kill("SIGTERM");
  });
}

export async function createLiveMinio(port: number) {
  const binaries = await buildTools();
  const root = await mkdtemp(join(toolRoot, "run-"));
  const child = spawn(
    binaries.minio,
    [
      "--certs-dir",
      join(root, "certs"),
      "server",
      "--address",
      `127.0.0.1:${port}`,
      join(root, "data"),
    ],
    {
      env: {
        ...process.env,
        MINIO_ROOT_USER: "renart",
        MINIO_ROOT_PASSWORD: "renart-secret",
        MINIO_BROWSER: "off",
        MINIO_UPDATE: "off",
      },
      stdio: ["ignore", "pipe", "pipe"],
    },
  );
  let output = "";
  let spawnError: Error | undefined;
  child.on("error", (error) => {
    spawnError = error;
  });
  const append = (chunk: unknown) => {
    output = (output + String(chunk)).slice(-8000);
  };
  child.stdout.on("data", append);
  child.stderr.on("data", append);
  const dispose = async () => {
    await stop(child);
    await rm(root, { recursive: true, force: true });
  };
  const mc = async (...args: string[]) => {
    await exec(binaries.mc, ["--config-dir", join(root, "mc"), ...args], {
      timeout: 60_000,
      maxBuffer: 8 * 1024 * 1024,
    });
  };
  try {
    const deadline = Date.now() + 30_000;
    while (true) {
      if (spawnError) throw spawnError;
      if (child.exitCode !== null || child.signalCode !== null || Date.now() >= deadline) {
        throw new Error(`MinIO fixture did not become ready: ${output}`);
      }
      try {
        // Liveness can return 200 before the bucket metadata and IAM are ready.
        const response = await fetch(`http://127.0.0.1:${port}/minio/health/cluster`, {
          signal: AbortSignal.timeout(1000),
        });
        await response.body?.cancel();
        if (response.ok) break;
      } catch {
        /* The process may still be starting. */
      }
      await new Promise((done) => setTimeout(done, 100));
    }
    await mc("alias", "set", "local", `http://127.0.0.1:${port}`, "renart", "renart-secret");
    return { mc, dispose };
  } catch (error) {
    await dispose();
    throw error;
  }
}
