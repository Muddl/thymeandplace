import { test, expect } from "@playwright/test";
import { compose, waitReady } from "../helpers";

test("1. search → select → results render", async ({ page }) => {
  await page.goto("/");
  // Type key by key: the search input triggers on keyup, which fill() doesn't fire.
  await page.getByLabel("Find your town").pressSequentially("lisb", { delay: 50 });
  await page.getByRole("link", { name: "Lisbon, PT" }).click();
  const results = page.locator("#results");
  await expect(results).toContainText("Lisbon, PT");
  await expect(results.locator("h3").first()).toBeVisible();
  await expect(page).toHaveURL(/\/season\?lat=38\.7167&lng=-9\.1333&place=1/);
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
  await page.goto("/");
  await page.getByRole("button", { name: "Use my location" }).click();
  await expect(page.getByText("Location access is off, so search for your town instead.")).toBeVisible();
  await expect(page.getByLabel("Find your town")).toBeFocused();
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
