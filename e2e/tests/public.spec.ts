import type { Page } from "@playwright/test";
import { axeOk, compose, emulateColorScheme, expect, isLocalOrigin, test, todayUTC, waitReady, watchCSP } from "../helpers";

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

async function assertEstimateRows(page: Page) {
  const items = page.locator("#results .item");
  await expect(items.first()).toBeVisible();
  const n = await items.count();
  expect(n).toBeGreaterThan(0);
  for (let i = 0; i < n; i++) {
    const item = items.nth(i);
    const badge = item.locator(".badge");
    if ((await badge.count()) === 0) {
      await expect(item).not.toContainText("Estimate");
      await expect(item.locator(".approx")).toHaveCount(0);
      continue;
    }
    await expect(badge).toHaveText("Estimate");
    await expect(badge).toHaveCSS("text-transform", "none");
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

test("1. search → select → results render", { tag: "@readonly" }, async ({ page }) => {
  const csp = await watchCSP(page);
  await page.goto("/");
  const box = page.getByLabel("Find your town");
  await expect(box).toBeFocused();
  await expect(page.locator("#announce")).toHaveCount(1);
  await expect(page.locator("[aria-live]")).toHaveCount(1);
  await expect(page.locator("#announce")).toHaveAttribute("role", "status");
  await expect(page.locator("main h1")).toHaveText("What's in season");
  const headerBefore = await page.locator("header.site").boundingBox();
  // Type key by key: the search input triggers on keyup, which fill() doesn't fire.
  await box.pressSequentially("lisb", { delay: 50 });
  await expect(box).toBeFocused();
  await page.getByRole("link", { name: "Lisbon, PT" }).click();
  const results = page.locator("#results");
  await expect(results).toContainText("Lisbon, PT");
  await expect(page.locator("main h1")).toHaveText("What's in season");
  await expect(results.locator("h2")).toHaveText("Lisbon, PT");
  await expect(results.locator("h3").first()).toBeVisible();
  await expect(page.getByRole("link", { name: "Thyme & Place" })).toBeVisible();
  await expect(page).toHaveURL(/\/season\?lat=-?[\d.]+&lng=-?[\d.]+&place=\d+/);
  await expect(box).not.toBeFocused();
  const announced = (await page.locator("#announce").textContent()) ?? "";
  expect(announced).toMatch(/^\d+ ingredients? in season near Lisbon/);
  expect(announced.length).toBeLessThan(80);
  expect(announced).not.toMatch(/Tomato|Peak|Estimate/);
  const headerAfter = await page.locator("header.site").boundingBox();
  expect(headerAfter).toEqual(headerBefore);
  expect(csp.events).toEqual([]);
});

test("every season row shows Estimate and keeps ≈ out of accessible names", { tag: "@readonly" }, async ({ page }) => {
  const csp = await watchCSP(page);
  await page.goto("/season?lat=38.7167&lng=-9.1333&place=1&date=2026-05-15");
  await assertEstimateRows(page);
  expect(csp.events).toEqual([]);
});

test("season page meets Almanac kickoff checks", { tag: "@readonly" }, async ({ page }) => {
  const csp = await watchCSP(page);
  const url = "/season?lat=38.7167&lng=-9.1333&place=1&date=2026-05-15";
  await page.goto(url);
  await expect(page.locator("main h1")).toHaveText("What's in season");
  await expect(page.locator("#results h2")).toHaveText("Lisbon, PT");
  const resultsTop = await page.locator("#results").evaluate((el) => el.getBoundingClientRect().top);
  const h1Top = await page.locator("main h1").evaluate((el) => el.getBoundingClientRect().top);
  expect(h1Top).toBeLessThan(resultsTop);
  await expect(page.locator("#announce")).toHaveCount(1);
  await expect(page.locator("#announce")).toHaveAttribute("role", "status");
  await expect(page.locator("[aria-live]")).toHaveCount(1);
  await assertEstimateRows(page);
  await axeOk(page, "light");
  const header = await pinHeader(page);
  const first = page.locator("#results .item a").first();
  const name = (await first.innerText()).trim();
  await first.click();
  await expect(page.locator("#results h2")).toHaveText(name);
  await assertHeaderUnmoved(page, header);
  await page.goto(url);
  await axeOk(page, "dark");
  if (test.info().project.name === "chromium") {
    // Kale's row is not edited by admin.spec.ts (Tomato harvest / Chard
    // import), so these stay stable when both files share a CI worker.
    // Chromium-only: Architect and UX agreed v1 baselines are not cross-engine.
    const kale = page.locator("#results .item").filter({ hasText: "Kale" });
    await page.setViewportSize({ width: 1280, height: 720 });
    await emulateColorScheme(page, "light");
    expect(csp.events).toEqual([]);
    await csp.ignoreDuring(async () => {
      await expect.soft(kale).toHaveScreenshot("season-estimate-light.png");
    });
    expect(csp.events).toEqual([]);
    await emulateColorScheme(page, "dark");
    expect(csp.events).toEqual([]);
    await csp.ignoreDuring(async () => {
      await expect.soft(kale).toHaveScreenshot("season-estimate-dark.png");
    });
    expect(csp.events).toEqual([]);
    await page.setViewportSize({ width: 390, height: 844 });
    await emulateColorScheme(page, "light");
    expect(csp.events).toEqual([]);
    await csp.ignoreDuring(async () => {
      await expect.soft(kale).toHaveScreenshot("season-estimate-light-390.png");
    });
    expect(csp.events).toEqual([]);
    await emulateColorScheme(page, "dark");
    expect(csp.events).toEqual([]);
    await csp.ignoreDuring(async () => {
      await expect.soft(kale).toHaveScreenshot("season-estimate-dark-390.png");
    });
  }
  expect(csp.events).toEqual([]);
});

test("/#q focuses the town search", { tag: "@readonly" }, async ({ page }) => {
  const csp = await watchCSP(page);
  await page.goto("/season?lat=38.7167&lng=-9.1333&place=1");
  await expect(page.getByLabel("Find your town")).not.toBeFocused();
  await page.goto("/#q");
  await expect(page.getByLabel("Find your town")).toBeFocused();
  expect(csp.events).toEqual([]);
});

test("keyboard focus shows a ring on the town search", { tag: "@readonly" }, async ({ page }) => {
  const csp = await watchCSP(page);
  await page.goto("/season?lat=38.7167&lng=-9.1333&place=1");
  await page.keyboard.press("Tab");
  await page.keyboard.press("Tab");
  const box = page.getByLabel("Find your town");
  await expect(box).toBeFocused();
  const outline = await box.evaluate((el) => getComputedStyle(el).outlineStyle);
  expect(outline).not.toBe("none");
  expect(csp.events).toEqual([]);
});

test("place links work without JavaScript", { tag: "@readonly" }, async ({ browser }) => {
  const context = await browser.newContext({ ignoreHTTPSErrors: true, javaScriptEnabled: false });
  const page = await context.newPage();
  const csp = await watchCSP(page);
  await page.goto("/places?q=lisb");
  await page.getByRole("link", { name: "Lisbon, PT" }).click();
  await expect(page).toHaveURL(/\/season\?lat=38\.7167&lng=-9\.1333&place=1/);
  await expect(page.locator("#results")).toContainText("Lisbon, PT");
  await expect(page.locator("main h1")).toHaveText("What's in season");
  await expect(page.locator("#results h2")).toHaveText("Lisbon, PT");
  await expect(page.getByRole("link", { name: "Thyme & Place" })).toBeVisible();
  expect(csp.events).toEqual([]);
  await context.close();
});

test("axe scan of home and season", { tag: "@readonly" }, async ({ page }) => {
  const csp = await watchCSP(page);
  await page.goto("/");
  await axeOk(page, "light");
  await axeOk(page, "dark");
  await page.goto("/season?lat=38.7167&lng=-9.1333&place=1");
  await axeOk(page, "light");
  await axeOk(page, "dark");
  expect(csp.events).toEqual([]);
});

test("home light and dark baselines", { tag: "@readonly" }, async ({ page }, info) => {
  const csp = await watchCSP(page);
  await page.goto("/");
  const shot = {
    caret: "hide" as const,
    animations: "disabled" as const,
    maxDiffPixelRatio: info.project.name === "chromium" ? 0.02 : 0,
  };
  expect(csp.events).toEqual([]);
  await csp.ignoreDuring(async () => {
    await expect(page).toHaveScreenshot("home-light.png", shot);
  });
  expect(csp.events).toEqual([]);
  await emulateColorScheme(page, "dark");
  expect(csp.events).toEqual([]);
  await csp.ignoreDuring(async () => {
    await expect(page).toHaveScreenshot("home-dark.png", shot);
  });
  expect(csp.events).toEqual([]);
});

test("a one-letter search asks for more", { tag: "@readonly" }, async ({ page }) => {
  await page.goto("/");
  const box = page.getByLabel("Find your town");
  await box.pressSequentially("l", { delay: 50 });
  await expect(page.locator("#places").getByText("Keep typing: enter at least 2 letters of your town's name.")).toBeVisible();
  await box.pressSequentially("isb", { delay: 50 });
  await expect(page.getByRole("link", { name: "Lisbon, PT" })).toBeVisible();
});

test("warm /season responds", { tag: "@readonly" }, async ({ request }) => {
  const url = `/season?lat=38.7167&lng=-9.1333&date=${todayUTC()}`;
  expect((await request.get(url)).ok()).toBeTruthy(); // warms the cache
  expect((await request.get(url)).ok()).toBeTruthy();
});

test("warm /season responds within 1 s", { tag: "@readonly" }, async ({ request }) => {
  test.skip(!isLocalOrigin(), "1 s bound is the local stack");
  const url = `/season?lat=38.7167&lng=-9.1333&date=${todayUTC()}`;
  expect((await request.get(url)).ok()).toBeTruthy(); // warms the cache
  const start = Date.now();
  const res = await request.get(url);
  expect(res.ok()).toBeTruthy();
  expect(Date.now() - start).toBeLessThan(1000);
});

test("2. geolocation denied → search fallback", { tag: "@readonly" }, async ({ browser }) => {
  const context = await browser.newContext({ ignoreHTTPSErrors: true, permissions: [] });
  const page = await context.newPage();
  const csp = await watchCSP(page);
  await page.goto("/");
  await page.getByRole("button", { name: "Use my location" }).click();
  await expect(page.getByText("Location access is off, so search for your town instead.")).toBeVisible();
  await expect(page.getByLabel("Find your town")).toBeFocused();
  expect(csp.events).toEqual([]);
  await context.close();
});

// Permissions-Policy allows geolocation for this origin only; this checks
// that "Use my location" still works under it and the CSP.
test("geolocation allowed → results for the position", { tag: "@readonly" }, async ({ browser }) => {
  const context = await browser.newContext({
    ignoreHTTPSErrors: true, permissions: ["geolocation"], geolocation: { latitude: 38.7167, longitude: -9.1333 },
  });
  const page = await context.newPage();
  const csp = await watchCSP(page);
  await page.goto("/");
  await page.getByRole("button", { name: "Use my location" }).click();
  await expect(page).toHaveURL(/\/season\?lat=38\.72&lng=-9\.13$/);
  await expect(page.locator("#results h3").first()).toBeVisible();
  expect(csp.events).toEqual([]);
  await context.close();
});

// Review Focus 2: DB outage shows the app's retry panel through Caddy.
// No date param: the browser's real local date gets stamped onto any
// htmx-driven /season request (see static/app.js), and with the server's
// clock pinned to 2026-05-15 (TP_PUBLIC_TODAY) that real date is always more
// than a day away, so the server falls back to its own pinned May date for
// every request in this suite — Madrid in May is still an uncached
// cell/month, since nothing earlier in this file visits Madrid.
test("3. Postgres stopped → retry panel → started → Try again succeeds", { tag: "@mutating" }, async ({ page, request }) => {
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
});
