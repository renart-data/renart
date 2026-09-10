import { spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { resolve } from "node:path";
import { getAvailablePort, runCommand, waitForHTTP } from "./live-warehouse-matrix";

const repo = resolve(__dirname, "../../..");
const minioImage = "quay.io/minio/minio:RELEASE.2025-06-13T11-33-47Z";
const mcImage = "quay.io/minio/mc:RELEASE.2025-05-21T01-59-54Z";

export async function createLiveStorage(options: { largeS3Listing?: boolean } = {}) {
  const suffix = randomUUID().slice(0, 8);
  const container = `renart-e2e-storage-${suffix}`;
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
  let started = false;
  const dispose = async () => {
    if (sftp.exitCode === null && sftp.signalCode === null) {
      const stopped = new Promise<void>((done) => sftp.once("exit", () => done()));
      sftp.kill("SIGTERM");
      await stopped;
    }
    if (started) await runCommand(["docker", "rm", "-f", container], true);
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
    await runCommand([
      "docker",
      "run",
      "--rm",
      "-d",
      "--name",
      container,
      "-e",
      "MINIO_ROOT_USER=renart",
      "-e",
      "MINIO_ROOT_PASSWORD=renart-secret",
      "--tmpfs",
      "/data:rw,noexec,nosuid,size=128m",
      "-p",
      `127.0.0.1:${minioPort}:9000`,
      minioImage,
      "server",
      "/data",
    ]);
    started = true;
    await waitForHTTP(`http://127.0.0.1:${minioPort}/minio/health/live`);
    await runCommand([
      "docker",
      "run",
      "--rm",
      "--network",
      `container:${container}`,
      "--mount",
      `type=bind,src=${resolve(repo, "web/tests/fixtures/storage")},dst=/fixture,readonly`,
      "--entrypoint",
      "/bin/sh",
      mcImage,
      "-c",
      "set -e; mc alias set local http://127.0.0.1:9000 renart renart-secret >/dev/null; mc mb local/browser; mc cp /fixture/orders.csv local/browser/incoming/orders.csv; mc cp /fixture/orders.csv local/browser/outgoing/previous.csv" +
        (options.largeS3Listing
          ? "; mkdir -p /seed/many-files; i=1000; while [ $i -lt 1520 ]; do mkdir -p /seed/my_table/day=2024-$i; cp /fixture/orders.csv /seed/my_table/day=2024-$i/data.csv; cp /fixture/orders.csv /seed/many-files/part-$i.csv; i=$((i+1)); done; for date in 2026-09-01 2026-09-20 2026-10-01; do mkdir -p /seed/my_table/day=$date; cp /fixture/orders.csv /seed/my_table/day=$date/data.csv; cp /fixture/orders.csv /seed/many-files/part-$date.csv; done; mc cp --recursive /seed/ local/browser/ >/dev/null"
          : ""),
    ]);
    return { minioPort, sftpPort, password, dispose };
  } catch (error) {
    await dispose();
    throw error;
  }
}
