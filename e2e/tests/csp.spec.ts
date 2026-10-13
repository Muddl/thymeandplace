import { expect, test } from "@playwright/test";
import { watchCSP } from "../helpers";

// Uses Playwright's test, not the helpers fixture, so the auto cspGuard
// does not fail the case for the violation we inject on purpose.
test("watchCSP still reports a real inline-script violation", { tag: "@readonly" }, async ({ page }) => {
  await page.goto("/");
  const csp = watchCSP(page);
  await page.evaluate(() => {
    const s = document.createElement("script");
    s.textContent = "void 0";
    document.body.appendChild(s);
  });
  await expect.poll(() => csp.join("\n")).toMatch(/inline script|script-src/i);
});
