import { expect, test } from "../helpers";

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

const viewports = [
  { name: "1280", width: 1280, height: 800 },
  { name: "390", width: 390, height: 844 },
] as const;
const schemes = ["light", "dark"] as const;

for (const scheme of schemes) {
  for (const vp of viewports) {
    test(`public controls and type match the theme in ${scheme} at ${vp.name} @readonly`, async ({ page }) => {
      await page.setViewportSize({ width: vp.width, height: vp.height });
      await page.emulateMedia({ colorScheme: scheme });
      await page.goto("/");
      const applied = await page.evaluate(
        (want) => matchMedia(`(prefers-color-scheme: ${want})`).matches,
        scheme,
      );
      if (!applied) test.info().skip(true, `browser did not apply prefers-color-scheme: ${scheme}`);

      const home = await page.evaluate(() => {
        const cs = (el: Element | null) => {
          if (!el) throw new Error("missing element");
          return getComputedStyle(el);
        };
        const root = cs(document.documentElement);
        const body = cs(document.body);
        const button = cs(document.querySelector(".finder button"));
        const input = cs(document.querySelector(".finder input"));
        const brand = cs(document.querySelector(".brand"));
        const about = cs(document.querySelector("footer.site a"));
        return {
          colorScheme: root.colorScheme,
          font: root.getPropertyValue("--font").trim(),
          fontSizeToken: root.getPropertyValue("--font-size").trim(),
          space: root.getPropertyValue("--space-6").trim(),
          linkToken: root.getPropertyValue("--link").trim(),
          bodyFont: body.fontFamily,
          bodySize: body.fontSize,
          bodyLine: body.lineHeight,
          buttonFont: button.fontFamily,
          buttonSize: button.fontSize,
          buttonBg: button.backgroundColor,
          buttonBorder: button.borderTopColor,
          inputFont: input.fontFamily,
          inputSize: input.fontSize,
          inputBg: input.backgroundColor,
          inputBorder: input.borderTopColor,
          brandColor: brand.color,
          brandDecoration: brand.textDecorationLine,
          aboutColor: about.color,
          aboutDecoration: about.textDecorationLine,
          bodyColor: body.color,
          bodyBg: body.backgroundColor,
        };
      });

      expect(home.colorScheme, `${scheme} color-scheme`).toBe(scheme);
      expect(home.font.length, "--font").toBeGreaterThan(0);
      expect(home.fontSizeToken, "--font-size").toBe("16px");
      expect(home.space, "--space-6").not.toBe("");
      expect(home.linkToken, "--link").not.toBe("");
      expect(parseFloat(home.bodySize), "base size").toBeGreaterThanOrEqual(16);
      expect(parseFloat(home.bodyLine) / parseFloat(home.bodySize), "line-height").toBeCloseTo(1.5, 1);
      expect(home.buttonFont, "button font-family").toBe(home.bodyFont);
      expect(home.buttonSize, "button font-size").toBe(home.bodySize);
      expect(home.inputFont, "input font-family").toBe(home.bodyFont);
      expect(home.inputSize, "input font-size").toBe(home.bodySize);
      expect(contrastRatio(home.inputBorder, home.inputBg), `${scheme} input border`).toBeGreaterThanOrEqual(3);
      expect(home.aboutDecoration, "body link underline").toContain("underline");
      expect(home.brandDecoration, "brand underline").not.toContain("underline");
      expect(home.brandColor, "brand stays ink").toBe(home.bodyColor);
      expect(contrastRatio(home.aboutColor, home.bodyBg), `${scheme} footer link`).toBeGreaterThanOrEqual(4.5);

      const aboutLink = page.locator("footer.site a");
      await aboutLink.focus();
      const aboutOutline = await aboutLink.evaluate((el) => getComputedStyle(el).outlineStyle);
      expect(aboutOutline, "link focus-visible").not.toBe("none");

      const geo = page.getByRole("button", { name: "Use my location" });
      await geo.focus();
      const buttonFocus = await geo.evaluate((el) => {
        const cs = getComputedStyle(el);
        return { outline: cs.outlineStyle, outlineWidth: cs.outlineWidth, bg: cs.backgroundColor };
      });
      expect(buttonFocus.outline, "button focus-visible").not.toBe("none");
      expect(parseFloat(buttonFocus.outlineWidth), "button focus width").toBeGreaterThanOrEqual(2);

      const disabled = await geo.evaluate((el) => {
        const btn = el as HTMLButtonElement;
        btn.disabled = true;
        const cs = getComputedStyle(btn);
        const out = { opacity: cs.opacity, cursor: cs.cursor };
        btn.disabled = false;
        return out;
      });
      expect(Number(disabled.opacity), "disabled opacity").toBeLessThan(1);
      expect(disabled.cursor, "disabled cursor").toBe("not-allowed");
    });
  }
}

test("heading scale and ingredient links in light @readonly", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.emulateMedia({ colorScheme: "light" });
  await page.goto("/ingredient/tomato?cell=295883");
  const type = await page.evaluate(() => {
    const cs = (el: Element | null) => {
      if (!el) throw new Error("missing element");
      return getComputedStyle(el);
    };
    const body = cs(document.body);
    const h2 = cs(document.querySelector("h2"));
    const h3 = cs(document.querySelector("h3"));
    const back = cs(document.querySelector(".ingredient a"));
    return {
      body: parseFloat(body.fontSize),
      h2: parseFloat(h2.fontSize),
      h3: parseFloat(h3.fontSize),
      backDecoration: back.textDecorationLine,
      backColor: back.color,
      bg: body.backgroundColor,
    };
  });
  expect(type.h2).toBeGreaterThan(type.h3);
  expect(type.h3).toBeGreaterThan(type.body);
  expect(type.backDecoration).toContain("underline");
  expect(contrastRatio(type.backColor, type.bg)).toBeGreaterThanOrEqual(4.5);
});
