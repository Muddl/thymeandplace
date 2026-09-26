import { execSync } from "node:child_process";
import path from "node:path";
import { APIRequestContext, Page, expect } from "@playwright/test";

const deploy = path.resolve(__dirname, "..", "deploy");

export function compose(args: string): string {
  return execSync(`docker compose -f compose.yaml -f compose.e2e.yaml ${args}`, {
    cwd: deploy,
    env: {
      ...process.env,
      TP_ENV_FILE: "./e2e.env",
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
