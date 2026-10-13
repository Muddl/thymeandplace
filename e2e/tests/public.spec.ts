import AxeBuilder from "@axe-core/playwright";
import type { Page } from "@playwright/test";
import { compose, expect, test, waitReady, watchCSP } from "../helpers";

function assertCSP(csp: string[]) {
  expect(csp, "Content Security Policy messages in the browser console").toEqual([]);
}

async function pinHeader(page: Page) {
  const header = page.locator("header.site");
  const box = await header.boundingBox();
  expect(box, "header.site must be painted so the stability check can fail").not.toBeNull();
  expect(box!.width).toBeGreaterThan(8);
  expect(box!.height).toBeGreaterThan(8);
  await page.evaluate(() => {
    (window as unknown as { __siteHeader?: Element | null }).__siteHeader = document.querySelector("header.site");
  });
  return box!;
}

async function assertHeaderUnmoved(page: Page, before: { x: number; y: number; width: number; height: number }) {
  const same = await page.evaluate(
    () => document.querySelector("header.site") === (window as unknown as { __siteHeader?: Element | null }).__siteHeader,
  );
  expect(same, "header.site node must not be replaced").toBe(true);
  const after = await page.locator("header.site").boundingBox();
  expect(after, "header.site must stay painted").not.toBeNull();
  expect(after).toEqual(before);
}

function defaultLinkContrastOnly(v: { id: string; nodes: { html: string }[] }) {
  // #48: unstyled <a> stays browser-default blue on the dark background.
  if (v.id !== "color-contrast") return false;
  return v.nodes.every((n) => /<a\b/i.test(n.html) && !/\bclass=/.test(n.html));
}

async function assertAxe(page: Page, scheme: "light" | "dark") {
  await page.emulateMedia({ colorScheme: scheme });
  const axe = await new AxeBuilder({ page }).analyze();
  const leftover = axe.violations.filter((v) => !(scheme === "dark" && defaultLinkContrastOnly(v)));
  expect(leftover, `${scheme} axe: ${JSON.stringify(leftover, null, 2)}`).toEqual([]);
}

async function assertEstimateRows(page: Page) {
  const items = page.locator("#results .item");
  await expect(items.first()).toBeVisible();
  const n = await items.count();
  expect(n).toBeGreaterThan(0);
  for (let i = 0; i < n; i++) {
    const item = items.nth(i);
    const badge = item.locator(".badge");
    await expect(badge).toHaveText("Estimate");
    await expect(item.locator(".approx")).toHaveAttribute("aria-hidden", "true");
    await expect(item).not.toHaveAccessibleName(/≈/);
    const box = await badge.boundingBox();
    expect(box, "Estimate badge must be painted").not.toBeNull();
    expect(box!.width, "Almanac clip-path shrinks the badge to 1px").toBeGreaterThan(16);
    expect(box!.height).toBeGreaterThan(8);
  }
  const snap = await page.locator("#results").ariaSnapshot();
  expect(snap).not.toMatch(/≈/);
  await expect(page.getByRole("link", { name: /≈/ })).toHaveCount(0);
  await expect(page.getByRole("link", { name: /how we estimate/i })).toBeVisible();
  await expect(page.getByRole("link", { name: /how we estimate/i })).not.toHaveAccessibleName(/≈/);
}

test("1. search → select → results render", { tag: ["@readonly"] }, async ({ page }) => {
  const csp = watchCSP(page);
  await page.goto("/");
  const homeHeader = await pinHeader(page);
  await page.getByLabel("Find your town").pressSequentially("lisb", { delay: 50 });
  await page.getByRole("link", { name: "Lisbon, PT" }).click();
  const results = page.locator("#results");
  await expect(results).toContainText("Lisbon, PT");
  await expect(results.locator("h3").first()).toBeVisible();
  await expect(page).toHaveURL(/\/season\?lat=38\.7167&lng=-9\.1333&place=1/);
  await assertHeaderUnmoved(page, homeHeader);
  assertCSP(csp);
});

test("every season row shows Estimate and keeps ≈ out of accessible names", { tag: ["@readonly"] }, async ({ page }) => {
  const csp = watchCSP(page);
  await page.goto("/season?lat=38.7167&lng=-9.1333&place=1&date=2026-05-15");
  await assertEstimateRows(page);
  assertCSP(csp);
});

test("season page meets Almanac kickoff checks", { tag: ["@readonly"] }, async ({ page }) => {
  const csp = watchCSP(page);
  const url = "/season?lat=38.7167&lng=-9.1333&place=1&date=2026-05-15";
  await page.goto(url);
  await expect(page.locator("main h1")).toHaveText("What's in season");
  await expect(page.locator("#results h2")).toHaveText("Lisbon, PT");
  const resultsTop = await page.locator("#results").evaluate((el) => el.getBoundingClientRect().top);
  const h1Top = await page.locator("main h1").evaluate((el) => el.getBoundingClientRect().top);
  expect(h1Top).toBeLessThan(resultsTop);
  await expect(page.locator("#announce")).toHaveCount(0);
  await assertEstimateRows(page);
  await assertAxe(page, "light");
  const header = await pinHeader(page);
  const first = page.locator("#results .item a").first();
  const name = (await first.innerText()).trim();
  await first.click();
  await expect(page.locator("#results h2")).toHaveText(name);
  await assertHeaderUnmoved(page, header);
  await page.goto(url);
  await assertAxe(page, "dark");
  if (test.info().project.name === "chromium") {
    await page.setViewportSize({ width: 1280, height: 720 });
    await page.emulateMedia({ colorScheme: "light" });
    await expect(page.locator("#results")).toHaveScreenshot("season-estimate-light.png");
    await page.emulateMedia({ colorScheme: "dark" });
    await expect(page.locator("#results")).toHaveScreenshot("season-estimate-dark.png");
    await page.setViewportSize({ width: 390, height: 844 });
    await page.emulateMedia({ colorScheme: "light" });
    await expect(page.locator("#results")).toHaveScreenshot("season-estimate-light-390.png");
    await page.emulateMedia({ colorScheme: "dark" });
    await expect(page.locator("#results")).toHaveScreenshot("season-estimate-dark-390.png");
  }
  assertCSP(csp);
});

test("a one-letter search asks for more", { tag: ["@readonly"] }, async ({ page }) => {
  const csp = watchCSP(page);
  await page.goto("/");
  const box = page.getByLabel("Find your town");
  await box.pressSequentially("l", { delay: 50 });
  await expect(page.getByText("Keep typing: enter at least 2 letters of your town's name.")).toBeVisible();
  await box.pressSequentially("isb", { delay: 50 });
  await expect(page.getByRole("link", { name: "Lisbon, PT" })).toBeVisible();
  assertCSP(csp);
});

// Spec §1 success criterion 1: warm-cache /season within about 1 s.
// date=2026-05-15 matches TP_PUBLIC_TODAY (see deploy/e2e.env), the server's
// pinned "today" for this stack, so the server accepts it verbatim rather
// than falling back to its own date (parseDate rejects dates more than a day
// from the server's UTC date).
test("warm /season responds within 1 s", { tag: ["@readonly"] }, async ({ request }) => {
  const url = "/season?lat=38.7167&lng=-9.1333&date=2026-05-15";
  expect((await request.get(url)).ok()).toBeTruthy(); // warms the cache
  const start = Date.now();
  const res = await request.get(url);
  expect(res.ok()).toBeTruthy();
  expect(Date.now() - start).toBeLessThan(1000);
});

test("2. geolocation denied → search fallback", { tag: ["@readonly"] }, async ({ browser }) => {
  const context = await browser.newContext({ ignoreHTTPSErrors: true, permissions: [] });
  const page = await context.newPage();
  const csp = watchCSP(page);
  await page.goto("/");
  await page.getByRole("button", { name: "Use my location" }).click();
  await expect(page.getByText("Location access is off, so search for your town instead.")).toBeVisible();
  await expect(page.getByLabel("Find your town")).toBeFocused();
  assertCSP(csp);
  await context.close();
});

// Permissions-Policy allows geolocation for this origin only; this checks
// that "Use my location" still works under it and the CSP.
test("geolocation allowed → results for the position", { tag: ["@readonly"] }, async ({ browser }) => {
  const context = await browser.newContext({
    ignoreHTTPSErrors: true, permissions: ["geolocation"], geolocation: { latitude: 38.7167, longitude: -9.1333 },
  });
  const page = await context.newPage();
  const csp = watchCSP(page);
  await page.goto("/");
  await page.getByRole("button", { name: "Use my location" }).click();
  await expect(page).toHaveURL(/\/season\?lat=38\.72&lng=-9\.13$/);
  await expect(page.locator("#results h3").first()).toBeVisible();
  assertCSP(csp);
  await context.close();
});

// Review Focus 2: DB outage shows the app's retry panel through Caddy.
// No date param: the browser's real local date gets stamped onto any
// htmx-driven /season request (see static/app.js), and with the server's
// clock pinned to 2026-05-15 (TP_PUBLIC_TODAY) that real date is always more
// than a day away, so the server falls back to its own pinned May date for
// every request in this suite — Madrid in May is still an uncached
// cell/month, since nothing earlier in this file visits Madrid.
test("3. Postgres stopped → retry panel → started → Try again succeeds", { tag: ["@mutating"] }, async ({ page, request }) => {
  const csp = watchCSP(page);
  await page.goto("/");
  compose("stop db");
  try {
    await page.goto("/season?lat=40.4165&lng=-3.7026");
    await expect(page.getByText("Couldn't load seasonal picks right now. Try again in a moment.")).toBeVisible();
    await expect(page.getByText("40.42°N, 3.70°W")).toBeVisible(); // location header stays
  } finally {
    compose("start db");
  }
  await waitReady(request);
  await page.getByRole("link", { name: "Try again" }).click();
  await expect(page.locator("#results")).toContainText("Madrid");
  await expect(page.getByText("Couldn't load seasonal picks")).toHaveCount(0);
  assertCSP(csp);
});
