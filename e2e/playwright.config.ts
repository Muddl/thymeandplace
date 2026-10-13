import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./tests",
  fullyParallel: false,
  workers: 1, // tests stop the database and edit the shared catalog
  timeout: 60_000,
  retries: 0,
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL || "https://localhost",
    ignoreHTTPSErrors: true, // Caddy's internal CA
    trace: "retain-on-failure",
  },
  expect: { toHaveScreenshot: { animations: "disabled", maxDiffPixelRatio: 0.02 } },
  snapshotPathTemplate: "tests/baselines/{arg}{ext}",
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }], // CDP WebAuthn is Chromium-only
});
