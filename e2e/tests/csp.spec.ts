import { expect, test } from "@playwright/test";

// Uses Playwright's test, not the helpers fixture, so the auto cspGuard
// does not fail the case for the violation we inject on purpose.
// Firefox often logs no CSP console line; the violation event still fires.
async function watchViolations(page: import("@playwright/test").Page) {
  const consoleMsgs: string[] = [];
  page.on("console", (m) => {
    const t = m.text();
    if (t.includes("Content Security Policy") || /style-src|script-src|inline style|inline script|stylesheet/i.test(t)) {
      consoleMsgs.push(t);
    }
  });
  await page.addInitScript(() => {
    document.addEventListener("securitypolicyviolation", (e) => {
      const w = window as unknown as { __csp: string[] };
      w.__csp = w.__csp || [];
      w.__csp.push(e.effectiveDirective + " " + e.blockedURI);
    });
  });
  return {
    consoleMsgs,
    combined: async () => {
      const ev = await page.evaluate(() => (window as unknown as { __csp?: string[] }).__csp || []);
      return [...consoleMsgs, ...ev].join("\n");
    },
    count: async () => {
      const ev = await page.evaluate(() => (window as unknown as { __csp?: string[] }).__csp || []);
      return consoleMsgs.length + ev.length;
    },
  };
}

test("watchCSP still reports a real inline-script violation", { tag: "@readonly" }, async ({ page }) => {
  const seen = await watchViolations(page);
  await page.goto("/");
  await page.evaluate(() => {
    const s = document.createElement("script");
    s.textContent = "void 0";
    document.body.appendChild(s);
  });
  await expect.poll(() => seen.combined()).toMatch(/script-src|inline script/i);
});

test("watchCSP still reports a real inline style element and attribute", { tag: "@readonly" }, async ({ page }) => {
  await page.emulateMedia({ colorScheme: "dark" });
  const seen = await watchViolations(page);
  await page.goto("/");
  await page.evaluate(() => {
    const s = document.createElement("style");
    s.textContent = "body { color: red }";
    document.head.appendChild(s);
  });
  await expect.poll(() => seen.combined()).toMatch(/style-src-elem|style-src|stylesheet|inline style/i);
  const afterElem = await seen.count();
  expect(afterElem).toBeGreaterThan(0);
  await page.evaluate(() => {
    const el = document.createElement("div");
    el.setAttribute("style", "color: red");
    document.body.appendChild(el);
  });
  await expect.poll(() => seen.count()).toBeGreaterThan(afterElem);
  await expect.poll(() => seen.combined()).toMatch(/style-src-attr|style-src|stylesheet|inline style/i);
});
