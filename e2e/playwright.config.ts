import { defineConfig, devices } from "@playwright/test";

const baseURL = process.env.E2E_BASE_URL || "https://localhost";
const localTLS = /^https?:\/\/(localhost|127\.0\.0\.1)(:|\/|$)/.test(baseURL);
const grep = process.env.GREP || "";
const browser = process.env.BROWSER || "chromium";
const shard = (process.env.SHARD || "1/1").replace("/", "-of-");

if (!localTLS && grep !== "@readonly") {
  throw new Error(
    `Refusing ${baseURL} unless GREP=@readonly (got GREP=${grep || "unset"}). Mutating tests must not run against a remote origin.`,
  );
}

export default defineConfig({
  testDir: "./tests",
  // One worker per job: tests stop the database and edit the shared catalog.
  // fullyParallel lets `--shard` split individual tests across CI jobs
  // (each job has its own stack). With it false, Playwright assigns a whole
  // file to the shard of its first test, so both spec files can land on
  // shard 1 and shard 2 runs nothing.
  fullyParallel: true,
  workers: 1,
  timeout: 60_000,
  retries: 0,
  reporter: process.env.CI
    ? [["list"], ["blob", { outputDir: "blob-report", fileName: `report-${browser}-${shard}.zip` }]]
    : [["list"], ["html", { open: "never" }]],
  use: {
    baseURL,
    ignoreHTTPSErrors: localTLS, // Caddy's internal CA on the compose stack
    trace: "retain-on-failure",
  },
  expect: { toHaveScreenshot: { animations: "disabled", maxDiffPixelRatio: 0.02 } },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
    {
      name: "firefox",
      use: { ...devices["Desktop Firefox"] },
      testIgnore: "**/admin.spec.ts", // CDP WebAuthn is Chromium-only
    },
    {
      name: "webkit",
      use: { ...devices["Desktop Safari"] },
      testIgnore: "**/admin.spec.ts", // CDP WebAuthn is Chromium-only
    },
  ],
});
