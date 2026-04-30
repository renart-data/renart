import { chromium } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdtemp, mkdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";

const repoRoot = path.resolve(import.meta.dirname, "..", "..");
const webRoot = path.resolve(import.meta.dirname, "..");
const docsPublicDir = path.join(repoRoot, "docs", "public");
const port = Number(process.env.RENART_SCREENSHOT_PORT ?? "18173");
const goBinary = process.env.GO_BIN ?? "/usr/local/go/bin/go";
const baseURL = `http://127.0.0.1:${port}`;
const workspaceDir = await mkdtemp(path.join(tmpdir(), "renart-docs-screenshots-"));

let server;
let browser;

try {
  await run("git", ["init"], workspaceDir);
  await mkdir(docsPublicDir, { recursive: true });

  server = spawn(
    goBinary,
    ["run", ".", "web", workspaceDir, "--port", String(port), "--static-dir", path.join(webRoot, "dist"), "--watch-mode", "fsnotify"],
    { cwd: repoRoot, detached: true, stdio: ["ignore", "pipe", "pipe"] }
  );

  let serverOutput = "";
  server.stdout.on("data", (chunk) => {
    serverOutput += chunk.toString();
  });
  server.stderr.on("data", (chunk) => {
    serverOutput += chunk.toString();
  });

  await waitForServer(baseURL, () => serverOutput);

  browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 1440, height: 960 }, deviceScaleFactor: 1 });
  await page.goto(`${baseURL}/onboarding`, { waitUntil: "networkidle" });
  await page.getByTestId("workspace-onboarding").screenshot({
    path: path.join(docsPublicDir, "quickstart-onboarding.png"),
  });

  await page.getByTestId("onboarding-quickstart-choice").click();
  await page.waitForURL(/\/(?:\?.*)?$/, { timeout: 120_000 });
  await page.waitForFunction(
    async () => {
      const response = await fetch("/api/onboarding/state");
      const state = await response.json();
      return state.active === false;
    },
    undefined,
    { timeout: 60_000 }
  );
  await page.evaluate(() => {
    window.localStorage.setItem("renart-quickstart-tour-dismissed", "true");
    window.localStorage.removeItem("renart-quickstart-tour-environments");
  });
  const workspace = await fetch(`${baseURL}/api/workspace`).then((response) => response.json());
  const pipeline = workspace.pipelines.find((candidate) => candidate.name === "quickstart");
  const asset = pipeline?.assets.find((candidate) => candidate.name === "quickstart.player_stats");
  if (!pipeline || !asset) {
    throw new Error("Quickstart workspace did not include quickstart.player_stats.");
  }
  const pipelineId = pipeline.id;
  const assetId = asset.id;
  await page.goto(`${baseURL}/?pipeline=${encodeURIComponent(pipelineId)}&asset=${encodeURIComponent(assetId)}`, {
    waitUntil: "domcontentloaded",
  });
  await page.getByTestId("editor-asset-name").getByText("quickstart.player_stats").waitFor({ timeout: 60_000 });
  await page.locator(".monaco-editor").waitFor({ timeout: 60_000 });
  await page.screenshot({
    path: path.join(docsPublicDir, "quickstart-workspace.png"),
    fullPage: true,
  });

  console.log("Captured docs screenshots:");
  console.log(path.join(docsPublicDir, "quickstart-onboarding.png"));
  console.log(path.join(docsPublicDir, "quickstart-workspace.png"));
} finally {
  await browser?.close().catch(() => undefined);
  if (server) {
    killServerGroup(server, "SIGTERM");
    await new Promise((resolve) => setTimeout(resolve, 500));
    killServerGroup(server, "SIGKILL");
  }
  await rm(workspaceDir, { recursive: true, force: true }).catch(() => undefined);
}

function killServerGroup(child, signal) {
  if (!child.pid) {
    return;
  }
  try {
    process.kill(-child.pid, signal);
  } catch {
    try {
      child.kill(signal);
    } catch {
      // Server already exited.
    }
  }
}

async function waitForServer(url, getOutput) {
  const deadline = Date.now() + 120_000;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url);
      if (response.ok) {
        return;
      }
    } catch {
      // Server is still starting.
    }
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  throw new Error(`Renart server did not start in time.\n${getOutput()}`);
}

async function run(command, args, cwd) {
  await new Promise((resolve, reject) => {
    const child = spawn(command, args, { cwd, stdio: "inherit" });
    child.on("error", reject);
    child.on("exit", (code) => {
      if (code === 0) {
        resolve();
      } else {
        reject(new Error(`${command} ${args.join(" ")} exited with ${code}`));
      }
    });
  });
}
