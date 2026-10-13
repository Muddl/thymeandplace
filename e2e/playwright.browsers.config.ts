import { defineConfig, devices } from "@playwright/test";
import base from "./playwright.config";

// Chromium/Firefox/WebKit matrix for local #107 proofs. CI and e2e/run.sh
// still use playwright.config.ts (Chromium only) until #86 lands.
export default defineConfig({
  ...base,
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] } },
    { name: "firefox", use: { ...devices["Desktop Firefox"] } },
    { name: "webkit", use: { ...devices["Desktop Safari"] } },
  ],
});
