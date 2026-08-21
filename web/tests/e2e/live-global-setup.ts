import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync } from "node:fs";
import { resolve } from "node:path";

// Mirrors the binary resolution in live-app-fixture.ts.
function resolveBinaryPath(): string {
  const repoRoot = resolve(__dirname, "..", "..", "..");
  const defaultBinaryPath = existsSync(resolve(repoRoot, "renart"))
    ? resolve(repoRoot, "renart")
    : resolve(repoRoot, "bruin");
  return process.env.BRUIN_E2E_BINARY || defaultBinaryPath;
}

// Compile the embedded ty Python-intelligence WASM module into the shared
// on-disk wazero cache once, before any server starts. The first cold compile takes
// tens of seconds and spikes memory; warming it here means every per-test
// server loads the compiled modules from cache instead, staying lean (the live
// suite spawns one server per test). Best-effort: a failure only forfeits the
// optimization, so it must never fail the suite.
function warmWasmCache() {
  const binaryPath = resolveBinaryPath();
  if (!existsSync(binaryPath)) {
    return;
  }
  try {
    execFileSync(binaryPath, ["debug", "warm-cache"], {
      stdio: "ignore",
      timeout: 6 * 60 * 1000,
    });
  } catch (error) {
    console.warn(`[live-e2e] WASM cache warm-up skipped: ${(error as Error).message}`);
  }
}

export default function globalSetup() {
  const tempRoot = "/dev/shm/bruin-playwright";
  mkdirSync(tempRoot, { recursive: true });
  process.env.TMPDIR = tempRoot;
  process.env.TMP = tempRoot;
  process.env.TEMP = tempRoot;

  warmWasmCache();
}
