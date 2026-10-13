import { expect, test } from "@playwright/test";
import { watchCSP } from "../helpers";

// These cases do not run on pull-request CI until #86 lands (e2e is
// main-only and Chromium-only in the config on this branch).

function hasDirective(events: { directive: string }[], want: "style-src-elem" | "style-src-attr" | "script-src") {
  const dirs = events.map((e) => e.directive);
  if (want === "script-src") return dirs.some((d) => d === "script-src" || d === "script-src-elem");
  return dirs.includes(want) || dirs.includes("style-src");
}

// Uses Playwright's test, not the helpers fixture, so the auto cspGuard
// does not fail the case for the violation we inject on purpose.
test("watchCSP still reports a real inline-script violation", { tag: "@readonly" }, async ({ page }) => {
  const csp = await watchCSP(page);
  await page.goto("/");
  expect(csp.events).toEqual([]);
  await page.evaluate(() => {
    const s = document.createElement("script");
    s.textContent = "void 0";
    document.body.appendChild(s);
  });
  await expect.poll(() => hasDirective(csp.events, "script-src")).toBe(true);
});

test("watchCSP still reports a real inline style element and attribute", { tag: "@readonly" }, async ({ page }) => {
  const csp = await watchCSP(page);
  await page.goto("/");
  expect(csp.events).toEqual([]);
  await page.evaluate(() => {
    const s = document.createElement("style");
    s.textContent = "body { color: red }";
    document.head.appendChild(s);
  });
  await expect.poll(() => hasDirective(csp.events, "style-src-elem")).toBe(true);
  const afterElem = csp.events.length;
  await page.evaluate(() => {
    const el = document.createElement("div");
    el.setAttribute("style", "color: red");
    document.body.appendChild(el);
  });
  await expect.poll(() => hasDirective(csp.events.slice(afterElem), "style-src-attr")).toBe(true);
});
