# Lighthouse baseline

Lighthouse scores for the four public page types. Add a dated row after any change to the layout, assets or headers, and before launch, run it once more against production.

## How to run

Run it against the Compose stack behind Caddy, so compression, TLS and the security headers are the real ones. Bring the stack up and seed it as `e2e/run.sh` does, then:

```bash
npm i -g lighthouse@13
for u in / '/season?lat=38.7167&lng=-9.1333' '/ingredient/kale?cell=295883' /about/data; do
  lighthouse "https://localhost$u" --chrome-flags="--headless=new --ignore-certificate-errors" \
    --only-categories=performance,accessibility,best-practices,seo --output=json --output-path=./lh.json --quiet
  jq -r '[.categories[] | "\(.id)=\((.score*100)|round)"] | join(" ")' lh.json
done
```

The ingredient URL's `cell` comes from the Lisbon season page (any ingredient link on it). Against production, drop `--ignore-certificate-errors` and use `https://thymeand.place`.

The scores below use Lighthouse's default mobile emulation (simulated slow 4G, 4× CPU slowdown).

## Results

| Date | Where | Page | Perf | A11y | Best practices | SEO | FCP / LCP / TBT / CLS |
|---|---|---|---|---|---|---|---|
| 2026-09-28 | local stack, before fixes | all four | 99–100 | 100 | 96 | 91 | 0.8 s / 1.1–1.3 s / 0–100 ms / 0 |
| 2026-09-28 | local stack | home | 100 | 100 | 100 | 100 | 0.8 s / 1.2 s / 0 ms / 0 |
| 2026-09-28 | local stack | season (Lisbon) | 100 | 100 | 100 | 100 | 0.8 s / 1.1 s / 0 ms / 0 |
| 2026-09-28 | local stack | ingredient | 100 | 100 | 100 | 100 | 0.8 s / 1.1 s / 0 ms / 0 |
| 2026-09-28 | local stack | About | 100 | 100 | 100 | 100 | 0.8 s / 1.1 s / 0 ms / 0 |

The first run found, and this change fixed:

- a console error from the missing `/favicon.ico` (there's now an SVG icon, and `/favicon.ico` redirects to it);
- no meta description on any page (each page type now has its own);
- a 24-hour cache on versioned assets (`?v=` URLs are now cached for a year and marked `immutable`).

## Known and accepted

- **Render-blocking stylesheet.** `app.css` is about 1 KB compressed. Inlining it would need `'unsafe-inline'` or a hash in `style-src`, and it isn't worth weakening the CSP for.
- **Trusted Types** (reported as informational). htmx swaps HTML through `innerHTML`, so `require-trusted-types-for 'script'` would need a pass-through policy, which defeats its purpose. The CSP already blocks inline and eval'd script.
- **HSTS.** The local Caddyfile doesn't send it. Production does (`max-age=31536000; includeSubDomains`). Adding `preload` and submitting to hstspreload.org is a one-way commitment for the whole domain, so it's left to the owner.
- **CSP, COOP and clickjacking audits** show as "not applicable". Lighthouse 13 reports that when it finds nothing wrong: the headers are present (`SecurityHeaders` in `internal/web/middleware.go`).
- **Lighthouse color scheme.** These scores are the default light scheme. Dark-scheme contrast is `e2e/tests/contrast.spec.ts`, which runs axe-core's `color-contrast` rule on the four public page types in both schemes.
