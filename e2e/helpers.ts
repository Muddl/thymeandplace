import { execSync } from "node:child_process";
import path from "node:path";
import AxeBuilder from "@axe-core/playwright";
import { APIRequestContext, Page, expect, test as base } from "@playwright/test";

const deploy = path.resolve(__dirname, "..", "deploy");

export type CSPEvent = {
  directive: string;
  blockedURI: string;
  sourceFile: string;
  sample: string;
  lineNumber: number;
};

export type CSPWatch = {
  console: string[];
  events: CSPEvent[];
  ignoreDuring<T>(fn: () => Promise<T>): Promise<T>;
};

const watches = new WeakMap<Page, CSPWatch>();

// WebKit injects <style>body {}</style> on every screenshot, so wrap
// toHaveScreenshot in ignoreDuring.
export async function watchCSP(page: Page): Promise<CSPWatch> {
  const existing = watches.get(page);
  if (existing) return existing;
  let recording = true;
  const watch: CSPWatch = {
    console: [],
    events: [],
    async ignoreDuring(fn) {
      recording = false;
      try {
        return await fn();
      } finally {
        recording = true;
      }
    },
  };
  page.on("console", (m) => {
    if (!recording) return;
    const t = m.text();
    if (t.includes("Content-Security-Policy") || t.includes("Content Security Policy")) {
      watch.console.push(t);
    }
  });
  try {
    await page.exposeFunction("__tpCspPush", (hit: CSPEvent) => {
      if (recording) watch.events.push(hit);
    });
    await page.addInitScript(() => {
      document.addEventListener("securitypolicyviolation", (e) => {
        const w = window as unknown as { __tpCspPush?: (h: CSPEvent) => void };
        w.__tpCspPush?.({
          directive: e.effectiveDirective,
          blockedURI: e.blockedURI,
          sourceFile: e.sourceFile,
          sample: e.sample,
          lineNumber: e.lineNumber,
        });
      });
    });
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    if (!/JavaScript is disabled|javaScriptEnabled/i.test(msg)) throw err;
  }
  watches.set(page, watch);
  return watch;
}

export const test = base.extend<{ cspGuard: void }>({
  cspGuard: [
    async ({ page }, use) => {
      const seen = await watchCSP(page);
      await use();
      expect(seen.events, "securitypolicyviolation events").toEqual([]);
    },
    { auto: true },
  ],
});

export { expect };

function defaultLinkContrastOnly(v: { id: string; nodes: { html: string }[] }) {
  // #48: unstyled <a> stays browser-default blue on the dark background.
  if (v.id !== "color-contrast") return false;
  return v.nodes.every((n) => /<a\b/i.test(n.html) && !/\bclass=/.test(n.html));
}

export async function axeOk(page: Page, scheme: "light" | "dark") {
  await page.emulateMedia({ colorScheme: scheme });
  const results = await new AxeBuilder({ page }).analyze();
  const leftover = results.violations.filter((v) => !(scheme === "dark" && defaultLinkContrastOnly(v)));
  expect(leftover, `${scheme} axe: ${JSON.stringify(leftover, null, 2)}`).toEqual([]);
}

export function compose(args: string): string {
  return execSync(`docker compose -p thymeandplace-e2e -f compose.yaml -f compose.e2e.yaml ${args}`, {
    cwd: deploy,
    env: {
      ...process.env,
      TP_ENV_DIR: "./e2e",
      // Git Bash on Windows (MSYS) rewrites an argument that looks like an
      // absolute Unix path (e.g. "/app/admin" in --entrypoint /app/admin)
      // into a Windows path before docker ever sees it. This opts out; it's
      // a no-op on Linux CI.
      MSYS_NO_PATHCONV: "1",
    },
    stdio: ["ignore", "pipe", "pipe"],
  }).toString();
}

export async function waitReady(request: APIRequestContext) {
  for (let i = 0; i < 90; i++) {
    try {
      const r = await request.get("/readyz");
      if (r.ok()) return;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 1000));
  }
  throw new Error("stack never became ready");
}

// enrollAndSignIn registers a virtual passkey via a fresh enrollment link and leaves the page signed in.
// The link comes from `admin enroll`, run as a one-off container from the
// app image (a fresh container, not `exec` into the running "app" service,
// so the --entrypoint override below can swap /app/server for /app/admin).
export async function enrollAndSignIn(page: Page) {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("WebAuthn.enable");
  await cdp.send("WebAuthn.addVirtualAuthenticator", {
    options: {
      protocol: "ctap2", transport: "internal", hasResidentKey: true,
      hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true,
    },
  });
  const out = compose("run --rm -T --entrypoint /app/admin app enroll");
  const link = out.match(/https:\/\/localhost\/admin\/enroll\?token=\S+/)?.[0];
  if (!link) throw new Error("no enrollment link in:\n" + out);
  await page.goto(link);
  await page.getByLabel("Name for this passkey").fill("E2E key");
  await page.getByRole("button", { name: "Register passkey" }).click();
  await expect(page).toHaveURL(/\/admin$/);
}
