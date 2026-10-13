import { execSync } from "node:child_process";
import path from "node:path";
import AxeBuilder from "@axe-core/playwright";
import { APIRequestContext, Page, expect, test as base } from "@playwright/test";

const deploy = path.resolve(__dirname, "..", "deploy");

export type CSPEvent = { directive: string; blockedURI: string };
export type CSPWatch = { console: string[]; events: CSPEvent[]; stop(): void };

// watchCSP records Content-Security-Policy hits. The primary signal is
// securitypolicyviolation (registered in addInitScript so it is in place
// before page script). Console text is a backup and matches both
// "Content-Security-Policy" and "Content Security Policy". A clean page
// fires no violation events. There is no console-text filter (#107).
// stop() ends recording so a later Playwright screenshot (WebKit injects
// an inline <style>) is not counted as a page violation.
export async function watchCSP(page: Page): Promise<CSPWatch> {
  const tagged = page as Page & { __cspWatch?: CSPWatch };
  if (tagged.__cspWatch) return tagged.__cspWatch;
  let live = true;
  const watch: CSPWatch = {
    console: [],
    events: [],
    stop() {
      live = false;
    },
  };
  page.on("console", (m) => {
    if (!live) return;
    const t = m.text();
    if (t.includes("Content-Security-Policy") || t.includes("Content Security Policy")) {
      watch.console.push(t);
    }
  });
  try {
    await page.exposeFunction("__tpCspPush", (hit: CSPEvent) => {
      if (live) watch.events.push(hit);
    });
    await page.addInitScript(() => {
      document.addEventListener("securitypolicyviolation", (e) => {
        const w = window as unknown as { __tpCspPush?: (h: { directive: string; blockedURI: string }) => void };
        if (w.__tpCspPush) w.__tpCspPush({ directive: e.effectiveDirective, blockedURI: e.blockedURI });
      });
    });
  } catch {
    // javaScriptEnabled: false pages cannot install the event bridge.
  }
  tagged.__cspWatch = watch;
  return watch;
}

// test is Playwright's test with a guard that fails any test whose page
// fired a securitypolicyviolation. Pages from a context a test makes
// itself need their own watchCSP.
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
  // HTMX writes class="" on processed links; that is still unstyled.
  if (v.id !== "color-contrast") return false;
  return v.nodes.every((n) => /<a\b/i.test(n.html) && !/\bclass="[^"]+"/.test(n.html));
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
