import { defineConfig, devices } from "@playwright/test";

const liveWorkers = Number.parseInt(process.env.RENART_E2E_LIVE_WORKERS ?? "1", 10);
const liveOutputDir = process.env.RENART_E2E_OUTPUT_DIR ?? "/dev/shm/bruin-playwright/test-results";

export default defineConfig({
  testDir: "./tests/e2e",
  testMatch: "**/*.live.spec.ts",
  outputDir: liveOutputDir,
  reporter: [
    ["list"],
    ["./tests/e2e/live-timing-reporter.ts", { outputDir: liveOutputDir, slowest: 50 }],
  ],
  globalSetup: "./tests/e2e/live-global-setup.ts",
  fullyParallel: false,
  workers: Number.isFinite(liveWorkers) && liveWorkers > 0 ? liveWorkers : 1,
  retries: process.env.CI ? 2 : 1,
  use: {
    trace: "on-first-retry",
    video: "retain-on-failure",
  },
  projects: [
    {
      name: "chromium-live",
      use: { ...devices["Desktop Chrome"] },
    },
    {
      name: "mobile-chrome-live",
      use: { ...devices["Pixel 7"] },
    },
  ],
});
