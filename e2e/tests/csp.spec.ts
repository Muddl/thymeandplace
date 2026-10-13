import { readFileSync } from "node:fs";
import path from "node:path";
import { test } from "@playwright/test";
import { emulateColorScheme, expect, watchCSP } from "../helpers";

const fixture = readFileSync(path.join(__dirname, "..", "fixtures", "inline-style.html"), "utf8");

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
    const elem = csp.events.find((e) => e.directive === "style-src-elem" || e.directive === "style-src");
    const attr = csp.events.find((e) => e.directive === "style-src-attr");
    if (!elem || !attr) return null;
    return {
      elem: { directive: elem.directive, blockedURI: elem.blockedURI },
      attr: { directive: attr.directive, blockedURI: attr.blockedURI },
    };
  }).toEqual({
    elem: { directive: "style-src-elem", blockedURI: "inline" },
    attr: { directive: "style-src-attr", blockedURI: "inline" },
  });
});

test("ignoreDuring drops screenshot hits and then records again", { tag: "@readonly" }, async ({ page }) => {
  const csp = await watchCSP(page);
  await page.goto("/");
  expect(csp.events).toEqual([]);
  await csp.ignoreDuring(async () => {
    await page.screenshot({ caret: "hide", animations: "disabled" });
  });
  expect(csp.events).toEqual([]);
  await emulateColorScheme(page, "dark");
  expect(csp.events).toEqual([]);
  await page.evaluate(() => {
    const el = document.createElement("p");
    el.setAttribute("style", "color: red");
    document.body.appendChild(el);
  });
  await expect.poll(() => {
    const hit = csp.events.find((e) => e.directive === "style-src-attr");
    return hit ? { directive: hit.directive, blockedURI: hit.blockedURI } : null;
  }).toEqual({ directive: "style-src-attr", blockedURI: "inline" });
});
