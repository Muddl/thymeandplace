import { readFileSync } from "node:fs";
import path from "node:path";
import { test } from "@playwright/test";
import { expect, isPlaywrightScreenshotStyle, pageViolations, watchCSP } from "../helpers";

const fixture = readFileSync(path.join(__dirname, "..", "fixtures", "inline-style.html"), "utf8");

// Playwright's test, not the helpers fixture: the auto cspGuard would fail
// this case for the violation we load on purpose.

test("an inline style fixture still fails the CSP watch", { tag: "@readonly" }, async ({ page, request }) => {
  const header = (await request.get("/")).headers()["content-security-policy"] ?? "";
  expect(header).toContain("style-src 'self'");
  const csp = await watchCSP(page);
  await page.route("**/__e2e/inline-style", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "text/html; charset=utf-8",
      headers: { "Content-Security-Policy": header },
      body: fixture,
    });
  });
  await page.goto("/__e2e/inline-style");
  await expect.poll(() => {
    const hit = pageViolations(csp.events).find((e) => e.directive === "style-src-elem" || e.directive === "style-src");
    return hit ? { directive: hit.directive, blockedURI: hit.blockedURI, sourceFile: hit.sourceFile, sample: hit.sample } : null;
  }).toEqual({
    directive: "style-src-elem",
    blockedURI: "inline",
    sourceFile: expect.stringContaining("/__e2e/inline-style"),
    sample: expect.any(String),
  });
  const hit = pageViolations(csp.events).find((e) => e.directive === "style-src-elem")!;
  expect(isPlaywrightScreenshotStyle(hit), JSON.stringify(hit)).toBe(false);
});

test("Playwright screenshot helpers are the only dropped style hits", { tag: "@readonly" }, async ({ page }, info) => {
  const csp = await watchCSP(page);
  await page.goto("/");
  expect(pageViolations(csp.events)).toEqual([]);
  await page.screenshot({ caret: "hide", animations: "disabled" });
  const raw = csp.events.map((e) => ({
    directive: e.directive,
    blockedURI: e.blockedURI,
    sourceFile: e.sourceFile,
    sample: e.sample,
    lineNumber: e.lineNumber,
  }));
  console.log(`csp-screenshot-events ${info.project.name} ${JSON.stringify(raw)}`);
  for (const e of csp.events) {
    expect(isPlaywrightScreenshotStyle(e), JSON.stringify(e)).toBe(true);
  }
  expect(pageViolations(csp.events)).toEqual([]);
});

