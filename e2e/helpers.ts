import { execSync } from "node:child_process";
import path from "node:path";
import AxeBuilder from "@axe-core/playwright";
import { APIRequestContext, Page, expect, test as base } from "@playwright/test";

const deploy = path.resolve(__dirname, "..", "deploy");

// watchCSP collects the page's browser console messages about Content
// Security Policy. The site sends a strict CSP (internal/web/middleware.go),
// so any such message means a page uses something the policy blocks.
export function watchCSP(page: Page): string[] {
  const seen: string[] = [];
  page.on("console", (m) => {
    if (m.text().includes("Content Security Policy")) seen.push(m.text());
  });
  return seen;
}

// test is Playwright's test with a guard that fails any test whose page
// logged a CSP message. Pages from a context a test makes itself need their
// own watchCSP.
export const test = base.extend<{ cspGuard: void }>({
  cspGuard: [
    async ({ page }, use) => {
      const seen = watchCSP(page);
      await use();
      expect(seen, "Content Security Policy messages in the browser console").toEqual([]);
    },
    { auto: true },
  ],
});

export { expect };

export async function axeOk(page: Page) {
  const results = await new AxeBuilder({ page }).analyze();
  expect(results.violations, JSON.stringify(results.violations, null, 2)).toEqual([]);
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
