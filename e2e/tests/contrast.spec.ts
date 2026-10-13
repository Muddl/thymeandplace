import AxeBuilder from "@axe-core/playwright";
import { expect, test } from "../helpers";

// date=2026-05-15 matches TP_PUBLIC_TODAY (see deploy/e2e.env).
const pages = [
  "/",
  "/season?lat=38.7167&lng=-9.1333&date=2026-05-15",
  "/ingredient/tomato?cell=295883&date=2026-05-15",
  "/about/data",
];

function contrastFailures(violations: { id: string; nodes: { html: string; target: unknown[]; failureSummary?: string }[] }[]) {
  return violations
    .filter((v) => v.id === "color-contrast")
    .flatMap((v) =>
      v.nodes.map((n) => ({
        target: n.target,
        html: n.html.replace(/\s+/g, " ").trim().slice(0, 160),
        summary: n.failureSummary,
      })),
    );
}

for (const scheme of ["light", "dark"] as const) {
  test(`public pages pass axe color-contrast in ${scheme} scheme`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme });
    for (const path of pages) {
      await page.goto(path);
      const results = await new AxeBuilder({ page }).withRules(["color-contrast"]).analyze();
      expect(contrastFailures(results.violations), `${scheme} ${path}`).toEqual([]);
    }

    await page.goto("/");
    await page.getByLabel("Find your town").pressSequentially("lisb", { delay: 50 });
    await expect(page.getByRole("link", { name: "Lisbon, PT" })).toBeVisible();
    const results = await new AxeBuilder({ page }).withRules(["color-contrast"]).analyze();
    expect(contrastFailures(results.violations), `${scheme} search results`).toEqual([]);
  });
}
