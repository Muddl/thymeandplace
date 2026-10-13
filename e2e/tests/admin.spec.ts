import { promises as fs } from "node:fs";
import { enrollAndSignIn, expect, findLisbonHref, test, withDate } from "../helpers";

test.describe.configure({ mode: "serial" });

const adminCSP =
  "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'";

test("anonymous /admin redirects to sign-in and sends the admin CSP @readonly", async ({ page, request }) => {
  const res = await request.get("/admin");
  expect(res.status()).toBe(200);
  expect(res.url()).toMatch(/\/admin\/login$/);
  expect(res.headers()["content-security-policy"]).toBe(adminCSP);

  await page.goto("/admin");
  await expect(page).toHaveURL(/\/admin\/login$/);
  await expect(page.getByRole("button", { name: "Sign in with a passkey" })).toBeVisible();
});

// The e2e stack pins today to 2026-05-15 (TP_PUBLIC_TODAY). parseDate rejects
// a date more than a day from that, so ?date=<today> still yields May
// results. Tomato is not yet in season in May.
test("4. passkey login → edit → preview → apply → public page reflects change @mutating", async ({ page }) => {
  const lisbonSeason = withDate(await findLisbonHref(page));
  await page.goto(lisbonSeason);
  await expect(page.locator("#results")).not.toContainText("Tomato"); // May: not yet in season

  await enrollAndSignIn(page);
  await page.getByRole("button", { name: "Sign out" }).click();
  await page.getByRole("button", { name: "Sign in with a passkey" }).click();
  await expect(page).toHaveURL(/\/admin$/);

  await page.getByRole("link", { name: "Tomato" }).click();
  await page.getByLabel("Months from planting to harvest").fill("1");
  await page.getByRole("button", { name: "Preview changes" }).click();

  await expect(page.getByRole("heading", { name: "Calibration score" })).toBeVisible();
  await expect(page.getByText("Before 1.000 → after 0.667 (-0.333).")).toBeVisible();
  await expect(page.locator(".bars").first()).toBeVisible();
  await page.getByLabel("Apply despite lower calibration score").check();
  await page.getByRole("button", { name: "Apply" }).click();
  await expect(page.getByText("Changes applied.")).toBeVisible();

  await expect(async () => {
    await page.goto(lisbonSeason);
    await expect(page.locator("#results")).toContainText("Tomato", { timeout: 1000 });
  }).toPass({ timeout: 10_000 }); // live reload via NOTIFY, no restart
});

// Ruling E(a): the ingredient form's kind-specific fields come from a
// swapped-in partial (GET /admin/kind-fields), not a full page reload.
// This never previews or applies, so it can't disturb the catalog state
// scenario 4 depends on (Tomato's default months_to_harvest).
test("6. changing Kind swaps the season-rule fields @mutating", async ({ page }) => {
  await enrollAndSignIn(page);
  await page.goto("/admin/ingredients/tomato");
  await expect(page.getByLabel("Minimum growing mean temperature (°C)")).toBeVisible();

  await page.getByLabel("Kind").selectOption("cultivated_year_round");
  await expect(page.getByText("Year-round produce has no season rules.")).toBeVisible();
  await expect(page.getByLabel("Minimum growing mean temperature (°C)")).toHaveCount(0);
});

// Ruling E(b): Back from the preview page must not strand the cook on a
// dead end — the form it came from should still be fillable and able to
// preview again. Uses Basil (not Tomato) and never applies, so scenario 4/5
// are unaffected regardless of test order.
test("7. Back from the preview page returns to a usable, re-previewable form @mutating", async ({ page }) => {
  await enrollAndSignIn(page);
  await page.goto("/admin/ingredients/basil");
  await page.getByLabel("Months from planting to harvest").fill("1");
  await page.getByRole("button", { name: "Preview changes" }).click();
  await expect(page.getByRole("heading", { name: "Calibration score" })).toBeVisible();

  await page.goBack();
  await expect(page.getByRole("heading", { name: "Edit Basil" })).toBeVisible();
  await page.getByLabel("Months from planting to harvest").fill("1");
  await page.getByRole("button", { name: "Preview changes" }).click();
  await expect(page.getByRole("heading", { name: "Calibration score" })).toBeVisible();
});

const chard = {
  slug: "chard", name: "Chard", category: "vegetable", kind: "cultivated_year_round",
  storage_months: 0, tips: { choose: "Glossy leaves.", store: "Fridge, wrapped." }, pairings: ["kale"], note: "",
};

test("5. import → preview → apply → export matches @mutating", async ({ page }) => {
  await enrollAndSignIn(page);
  await page.goto("/admin/import");
  await page.setInputFiles('input[name="file"]', {
    name: "chard.json", mimeType: "application/json",
    buffer: Buffer.from(JSON.stringify({ format: "thymeandplace-catalog/v1", ingredients: [chard] })),
  });
  await page.getByRole("button", { name: "Preview import" }).click();
  await expect(page.getByText("1 added item")).toBeVisible();

  // Ruling E(c): a double-clicked Apply must change the catalog exactly
  // once. The Apply button disables itself on submit (static/admin.js), and
  // the server redirects a resubmit of an already-applied changeset to the
  // same success page rather than erroring.
  await page.getByRole("button", { name: "Apply" }).dblclick();
  await expect(page.getByText("Changes applied.")).toBeVisible();
  await expect(page.locator("tbody tr", { hasText: "Chard" })).toHaveCount(1);

  const [download] = await Promise.all([page.waitForEvent("download"), page.getByRole("link", { name: "Export" }).click()]);
  const doc = JSON.parse(await fs.readFile((await download.path())!, "utf8"));
  expect(doc.format).toBe("thymeandplace-catalog/v1");
  expect(doc.ingredients.find((i: { slug: string }) => i.slug === "chard")).toEqual(chard);
});
