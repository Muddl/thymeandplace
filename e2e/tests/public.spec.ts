import type { Page, Request } from "@playwright/test";
import { axeOk, compose, expect, test, waitReady, watchCSP } from "../helpers";

test("1. search → select → results render", { tag: "@readonly" }, async ({ page }) => {
  const csp = watchCSP(page);
  await page.goto("/");
  const box = page.getByLabel("Find your town");
  await expect(box).toBeFocused();
  await expect(page.locator("#announce")).toHaveCount(1);
  await expect(page.locator("[aria-live]")).toHaveCount(1);
  await expect(page.locator("#announce")).toHaveAttribute("role", "status");
  await expect(page.locator("main h1")).toBeVisible();
  const headerBefore = await page.locator("header.site").boundingBox();
  // Type key by key: the search input triggers on keyup, which fill() doesn't fire.
  await box.pressSequentially("lisb", { delay: 50 });
  await expect(box).toBeFocused();
  await page.getByRole("link", { name: "Lisbon, PT" }).click();
  const results = page.locator("#results");
  await expect(results).toContainText("Lisbon, PT");
  await expect(results.locator("h3").first()).toBeVisible();
  await expect(page.getByRole("link", { name: "Thyme & Place" })).toBeVisible();
  await expect(page).toHaveURL(/\/season\?lat=38\.7167&lng=-9\.1333&place=1/);
  await expect(box).not.toBeFocused();
  const announced = (await page.locator("#announce").textContent()) ?? "";
  expect(announced).toMatch(/^\d+ ingredients? in season near Lisbon/);
  expect(announced.length).toBeLessThan(80);
  expect(announced).not.toMatch(/Tomato|Peak|Estimate/);
  const headerAfter = await page.locator("header.site").boundingBox();
  expect(headerAfter).toEqual(headerBefore);
  expect(csp).toEqual([]);
});

test("/#q focuses the town search", { tag: "@readonly" }, async ({ page }) => {
  const csp = watchCSP(page);
  await page.goto("/season?lat=38.7167&lng=-9.1333&place=1");
  await expect(page.getByLabel("Find your town")).not.toBeFocused();
  await page.goto("/#q");
  await expect(page.getByLabel("Find your town")).toBeFocused();
  expect(csp).toEqual([]);
});

test("keyboard focus shows a ring on the town search", { tag: "@readonly" }, async ({ page }) => {
  const csp = watchCSP(page);
  await page.goto("/season?lat=38.7167&lng=-9.1333&place=1");
  await page.keyboard.press("Tab");
  await page.keyboard.press("Tab");
  const box = page.getByLabel("Find your town");
  await expect(box).toBeFocused();
  const outline = await box.evaluate((el) => getComputedStyle(el).outlineStyle);
  expect(outline).not.toBe("none");
  expect(csp).toEqual([]);
});

test("place links work without JavaScript", { tag: "@readonly" }, async ({ browser }) => {
  const context = await browser.newContext({ ignoreHTTPSErrors: true, javaScriptEnabled: false });
  const page = await context.newPage();
  const csp = watchCSP(page);
  await page.goto("/places?q=lisb");
  await page.getByRole("link", { name: "Lisbon, PT" }).click();
  await expect(page).toHaveURL(/\/season\?lat=38\.7167&lng=-9\.1333&place=1/);
  await expect(page.locator("#results")).toContainText("Lisbon, PT");
  await expect(page.getByRole("link", { name: "Thyme & Place" })).toBeVisible();
  expect(csp).toEqual([]);
  await context.close();
});

test("axe scan of home and season", { tag: "@readonly" }, async ({ page }) => {
  const csp = watchCSP(page);
  await page.goto("/");
  await axeOk(page, "light");
  await axeOk(page, "dark");
  await page.goto("/season?lat=38.7167&lng=-9.1333&place=1");
  await axeOk(page, "light");
  await axeOk(page, "dark");
  expect(csp).toEqual([]);
});

test("home light and dark baselines", { tag: "@readonly" }, async ({ page }) => {
  const csp = watchCSP(page);
  await page.goto("/");
  await expect(page).toHaveScreenshot("home-light.png", { caret: "hide", animations: "disabled" });
  await page.emulateMedia({ colorScheme: "dark" });
  await expect(page).toHaveScreenshot("home-dark.png", { caret: "hide", animations: "disabled" });
  expect(csp).toEqual([]);
});

test("a one-letter search asks for more", async ({ page }) => {
  await page.goto("/");
  const box = page.getByLabel("Find your town");
  await box.pressSequentially("l", { delay: 50 });
  await expect(page.locator("#places").getByText("Keep typing: enter at least 2 letters of your town's name.")).toBeVisible();
  await box.pressSequentially("isb", { delay: 50 });
  await expect(page.getByRole("link", { name: "Lisbon, PT" })).toBeVisible();
});

// Spec §1 success criterion 1: warm-cache /season within about 1 s.
// date=2026-05-15 matches TP_PUBLIC_TODAY (see deploy/e2e.env), the server's
// pinned "today" for this stack, so the server accepts it verbatim rather
// than falling back to its own date (parseDate rejects dates more than a day
// from the server's UTC date).
test("warm /season responds within 1 s", async ({ request }) => {
  const url = "/season?lat=38.7167&lng=-9.1333&date=2026-05-15";
  expect((await request.get(url)).ok()).toBeTruthy(); // warms the cache
  const start = Date.now();
  const res = await request.get(url);
  expect(res.ok()).toBeTruthy();
  expect(Date.now() - start).toBeLessThan(1000);
});

test("2. geolocation denied → search fallback", async ({ browser }) => {
  const context = await browser.newContext({ ignoreHTTPSErrors: true, permissions: [] });
  const page = await context.newPage();
  const csp = watchCSP(page);
  await page.goto("/");
  await page.getByRole("button", { name: "Use my location" }).click();
  await expect(page.getByText("Location access is off, so search for your town instead.")).toBeVisible();
  await expect(page.getByLabel("Find your town")).toBeFocused();
  expect(csp).toEqual([]);
  await context.close();
});

// Permissions-Policy allows geolocation for this origin only; this checks
// that "Use my location" still works under it and the CSP.
test("geolocation allowed → results for the position", async ({ browser }) => {
  const context = await browser.newContext({
    ignoreHTTPSErrors: true, permissions: ["geolocation"], geolocation: { latitude: 38.7167, longitude: -9.1333 },
  });
  const page = await context.newPage();
  const csp = watchCSP(page);
  await page.goto("/");
  await page.getByRole("button", { name: "Use my location" }).click();
  await expect(page).toHaveURL(/\/season\?lat=38\.72&lng=-9\.13$/);
  await expect(page.locator("#results h3").first()).toBeVisible();
  expect(csp).toEqual([]);
  await context.close();
});

// Review Focus 2: DB outage shows the app's retry panel through Caddy.
// No date param: the browser's real local date gets stamped onto any
// htmx-driven /season request (see static/app.js), and with the server's
// clock pinned to 2026-05-15 (TP_PUBLIC_TODAY) that real date is always more
// than a day away, so the server falls back to its own pinned May date for
// every request in this suite — Madrid in May is still an uncached
// cell/month, since nothing earlier in this file visits Madrid.
test("3. Postgres stopped → retry panel → started → Try again succeeds", async ({ page, request }) => {
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

const searchUnavailable = "Search isn't available right now. Try again in a moment.";
const loadError = "Couldn't load seasonal picks right now. Try again in a moment.";

async function searchLisbon(page: Page) {
  await page.goto("/");
  await page.getByLabel("Find your town").pressSequentially("lisb", { delay: 50 });
  await expect(page.getByRole("link", { name: "Lisbon, PT" })).toBeVisible();
}

function isHX(pathPrefix: string, request: Request) {
  return request.method() === "GET" && request.url().includes(pathPrefix) && request.headers()["hx-request"] === "true";
}

async function htmxTimeoutMs(page: Page) {
  const timeoutMs = await page.evaluate(() => (window as unknown as { htmx: { config: { timeout: number } } }).htmx.config.timeout);
  expect(timeoutMs).toBeGreaterThan(0);
  return timeoutMs;
}

test("places abort writes search unavailable to #announce and #places", { tag: "@readonly" }, async ({ page }) => {
  await page.route("**/places*", async (route) => {
    if (!isHX("/places", route.request())) {
      await route.continue();
      return;
    }
    await route.abort("connectionrefused");
  });
  await page.goto("/");
  await page.getByLabel("Find your town").pressSequentially("lisb", { delay: 50 });
  await expect(page.locator("#announce")).toHaveText(searchUnavailable);
  await expect(page.locator("#places")).toContainText(searchUnavailable);
  await expect(page.locator("#places")).not.toHaveAttribute("aria-busy", "true");
});

test("season abort writes load error to #announce and #results", { tag: "@readonly" }, async ({ page }) => {
  await searchLisbon(page);
  await page.route("**/season*", async (route) => {
    if (!isHX("/season", route.request())) {
      await route.continue();
      return;
    }
    await route.abort("connectionrefused");
  });
  await page.getByRole("link", { name: "Lisbon, PT" }).click();
  await expect(page.locator("#announce")).toHaveText(loadError);
  await expect(page.locator("#results")).toContainText(loadError);
  await expect(page.locator("#results").getByRole("link", { name: "Try again" })).toHaveCount(0);
  await expect(page.locator("#results")).not.toHaveAttribute("aria-busy", "true");
});

test("places timeout writes search unavailable to #announce and #places", { tag: "@readonly" }, async ({ page }) => {
  await page.goto("/");
  const timeoutMs = await htmxTimeoutMs(page);
  await page.route("**/places*", async (route) => {
    if (!isHX("/places", route.request())) {
      await route.continue();
      return;
    }
    await new Promise((r) => setTimeout(r, timeoutMs + 1500));
    await route.abort("timedout");
  });
  await page.getByLabel("Find your town").pressSequentially("lisb", { delay: 50 });
  await expect(page.locator("#announce")).toHaveText(searchUnavailable, { timeout: timeoutMs + 5000 });
  await expect(page.locator("#places")).toContainText(searchUnavailable);
  await expect(page.locator("#places")).not.toHaveAttribute("aria-busy", "true");
});

test("season timeout writes load error to #announce and #results", { tag: "@readonly" }, async ({ page }) => {
  await searchLisbon(page);
  const timeoutMs = await htmxTimeoutMs(page);
  await page.route("**/season*", async (route) => {
    if (!isHX("/season", route.request())) {
      await route.continue();
      return;
    }
    await new Promise((r) => setTimeout(r, timeoutMs + 1500));
    await route.abort("timedout");
  });
  await page.getByRole("link", { name: "Lisbon, PT" }).click();
  await expect(page.locator("#announce")).toHaveText(loadError, { timeout: timeoutMs + 5000 });
  await expect(page.locator("#results")).toContainText(loadError);
  await expect(page.locator("#results").getByRole("link", { name: "Try again" })).toHaveCount(0);
  await expect(page.locator("#results")).not.toHaveAttribute("aria-busy", "true");
});

test("season 500 swaps the server fragment and announces it", { tag: "@readonly" }, async ({ page }) => {
  const fragment =
    `<div class="panel warn" role="alert">` +
    `<p>${loadError}</p>` +
    `<p><a href="/season?lat=38.7167&lng=-9.1333&place=1" hx-get="/season?lat=38.7167&lng=-9.1333&place=1" hx-target="#results">Try again</a></p>` +
    `</div>`;
  await searchLisbon(page);
  await page.route("**/season*", async (route) => {
    if (!isHX("/season", route.request())) {
      await route.continue();
      return;
    }
    await route.fulfill({ status: 500, contentType: "text/html; charset=utf-8", body: fragment });
  });
  await page.getByRole("link", { name: "Lisbon, PT" }).click();
  await expect(page.locator("#results")).toContainText(loadError);
  await expect(page.locator("#results").getByRole("link", { name: "Try again" })).toBeVisible();
  await expect(page.locator("#announce")).toHaveText(loadError);
  await expect(page.locator("#results")).not.toHaveAttribute("aria-busy", "true");
});
