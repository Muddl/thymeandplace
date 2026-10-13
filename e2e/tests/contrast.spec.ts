import type { Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { expect, isLocalOrigin, test } from "../helpers";

// These URLs depend on the server's TP_PUBLIC_TODAY pin, so the spec is not prod-safe (#99).
const pinnedToday = process.env.TP_PUBLIC_TODAY;
const pages = [
  "/",
  `/season?lat=38.7167&lng=-9.1333&date=${pinnedToday}`,
  `/ingredient/tomato?cell=295883&date=${pinnedToday}`,
  `/ingredient/banana?cell=295883&date=${pinnedToday}`,
  "/about/data",
];

const schemeBg = { light: "#fbfaf6", dark: "#141814" } as const;

async function open(page: Page, path: string, scheme: "light" | "dark") {
  await page.goto(path);
  await page.emulateMedia({ colorScheme: scheme });
  const applied = await page.evaluate((want) => {
    return {
      media: matchMedia(`(prefers-color-scheme: ${want})`).matches,
      bg: getComputedStyle(document.documentElement).getPropertyValue("--bg").trim(),
    };
  }, scheme);
  expect(applied.media, `prefers-color-scheme: ${scheme} after ${path}`).toBe(true);
  expect(applied.bg, `--bg after ${path}`).toBe(schemeBg[scheme]);
}

function parseColor(color: string): { r: number; g: number; b: number } {
  const rgb = color.match(/rgba?\(\s*([\d.]+)\s*,\s*([\d.]+)\s*,\s*([\d.]+)/);
  if (rgb) return { r: Number(rgb[1]), g: Number(rgb[2]), b: Number(rgb[3]) };
  const srgb = color.match(/color\(srgb\s+([\d.]+)\s+([\d.]+)\s+([\d.]+)/);
  if (srgb) return { r: Number(srgb[1]) * 255, g: Number(srgb[2]) * 255, b: Number(srgb[3]) * 255 };
  throw new Error(`unparsed color: ${color}`);
}

function contrastRatio(a: string, b: string): number {
  const lin = (c: number) => {
    const x = c / 255;
    return x <= 0.04045 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4;
  };
  const lum = (color: string) => {
    const { r, g, b } = parseColor(color);
    return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
  };
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

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
  test.describe(`${scheme} scheme`, () => {
    test.use({ colorScheme: scheme });

    test(`public pages pass axe color-contrast in ${scheme} scheme @mutating`, async ({ page }) => {
      test.skip(!isLocalOrigin() || !process.env.TP_PUBLIC_TODAY, "needs a local target with TP_PUBLIC_TODAY");
      for (const path of pages) {
        await open(page, path, scheme);
        const results = await new AxeBuilder({ page }).withRules(["color-contrast"]).analyze();
        expect(contrastFailures(results.violations), `${scheme} ${path}`).toEqual([]);
      }

      await open(page, "/", scheme);
      await page.getByLabel("Find your town").pressSequentially("lisb", { delay: 50 });
      await expect(page.getByRole("link", { name: "Lisbon, PT" })).toBeVisible();
      const results = await new AxeBuilder({ page }).withRules(["color-contrast"]).analyze();
      expect(contrastFailures(results.violations), `${scheme} search results`).toEqual([]);

      const controls = await page.evaluate(() => {
        const style = (sel: string) => {
          const cs = getComputedStyle(document.querySelector(sel)!);
          return { bg: cs.backgroundColor, border: cs.borderTopColor };
        };
        return { input: style(".finder input"), button: style(".finder button") };
      });
      expect(contrastRatio(controls.input.border, controls.input.bg), `${scheme} input border`).toBeGreaterThanOrEqual(3);
      expect(contrastRatio(controls.button.border, controls.button.bg), `${scheme} button border`).toBeGreaterThanOrEqual(3);

      await open(page, `/ingredient/banana?cell=295883&date=${pinnedToday}`, scheme);
      const year = page.locator(".s-year").first();
      await expect(year).toBeVisible();
      const yearColors = await year.evaluate((el) => {
        const cs = getComputedStyle(el);
        return { color: cs.color, bg: cs.backgroundColor };
      });
      expect(contrastRatio(yearColors.color, yearColors.bg), `${scheme} year-round bar`).toBeGreaterThanOrEqual(4.5);
    });
  });
}
