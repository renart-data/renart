import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { copyFile, mkdir, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { createLiveMinio } from "./live-minio-fixture";
import { getAvailablePort, runCommand } from "./live-warehouse-matrix";

const repo = resolve(__dirname, "../../..");

export async function createLiveStorage(options: { largeS3Listing?: boolean } = {}) {
  const password = randomUUID();
  const minioPort = await getAvailablePort();
  const helperDir = resolve(__dirname, "helpers/storage-sftp");
  const helperBinary = resolve(repo, ".test-artifacts/storage-sftp-fixture");
  await runCommand(["go", "build", "-C", helperDir, "-o", helperBinary, "."]);
  const sftp = spawn(helperBinary, [], {
    env: { ...process.env, RENART_SFTP_FIXTURE_PASSWORD: password },
    stdio: ["ignore", "pipe", "pipe"],
  });
  let output = "";
  sftp.stderr.on("data", (chunk) => {
    output = (output + chunk).slice(-8000);
  });
  let minio: Awaited<ReturnType<typeof createLiveMinio>> | undefined;
  let seed: string | undefined;
  const dispose = async () => {
    if (sftp.exitCode === null && sftp.signalCode === null) {
      const stopped = new Promise<void>((done) => sftp.once("exit", () => done()));
      sftp.kill("SIGTERM");
      await stopped;
    }
    await minio?.dispose();
    if (seed) await rm(seed, { recursive: true, force: true });
  };
  try {
    const sftpPort = await new Promise<number>((done, fail) => {
      const timer = setTimeout(
        () => fail(new Error(`SFTP fixture did not start: ${output}`)),
        20000,
      );
      sftp.once("error", (error) => {
        clearTimeout(timer);
        fail(error);
      });
      sftp.once("exit", () => {
        clearTimeout(timer);
        fail(new Error(`SFTP fixture exited: ${output}`));
      });
      sftp.stdout.once("data", (chunk) => {
        clearTimeout(timer);
        const port = Number(String(chunk).trim());
        if (port > 0 && port < 65536) done(port);
        else fail(new Error("SFTP fixture returned an invalid port"));
      });
    });
    minio = await createLiveMinio(minioPort);
    await minio.mc("mb", "local/browser");
    const orders = resolve(repo, "web/tests/fixtures/storage/orders.csv");
    await minio.mc("cp", orders, "local/browser/incoming/orders.csv");
    await minio.mc("cp", orders, "local/browser/outgoing/previous.csv");
    if (options.largeS3Listing) {
      seed = await mkdtemp(join(tmpdir(), "renart-storage-seed-"));
      await mkdir(join(seed, "many-files"));
      const dates = [
        ...Array.from({ length: 520 }, (_, index) => `2024-${1000 + index}`),
        "2026-09-01",
        "2026-09-20",
        "2026-10-01",
      ];
      for (const date of dates) {
        const directory = join(seed, "my_table", `day=${date}`);
        await mkdir(directory, { recursive: true });
        await copyFile(orders, join(directory, "data.csv"));
        await copyFile(orders, join(seed, "many-files", `part-${date.replace("2024-", "")}.csv`));
      }
      await minio.mc("cp", "--recursive", `${seed}/`, "local/browser/");
    }
    return { minioPort, sftpPort, password, dispose };
  } catch (error) {
    await dispose();
    throw error;
  }
}
