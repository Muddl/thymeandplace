# Thyme & Place - Public-origin security headers and client hardening - Design

- **Status:** Proposed
- **Date:** 2026-09-27
- **Source:** Architectural audit, 2026-09-27 (findings F03, F15)
- **Related issues:** [#3](https://github.com/Muddl/thymeandplace/issues/3) (F03), [#15](https://github.com/Muddl/thymeandplace/issues/15) (F15)
- **Amends:** core spec §10.4 ("Headers on `/admin`" becomes site-wide headers plus admin additions), §11.5 (HTMX runs with eval and script tags disabled), §13.8 (header tests)

---

## 1. Problem

**F03: public pages send no security headers, and they share an origin with `/admin`.**

- The admin handler sets a strict policy in `securityHeaders` (`internal/admin/admin.go:102-113`):
  - CSP: `default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'`
  - `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `Cache-Control: no-store`, `X-Robots-Tag: noindex` and `X-Content-Type-Options: nosniff`
- The public handler chain sets none of these (`internal/web/server.go:91-97`). Nor does `web.Common` (`:102-109`). Caddy adds only `Strict-Transport-Security` and removes `Server` (`deploy/Caddyfile:7-10`).
- The public HTMX config (`internal/web/views/models.go:31`) leaves htmx 2.0.4's defaults, `allowEval: true` and `allowScriptTags: true`. The vendored `static/htmx.min.js` contains both defaults. The htmx 2.0.4 docs, checked via Context7, say `allowEval` gates event filters, `hx-on` and `js:` prefixes in `hx-vals`/`hx-headers`, and `allowScriptTags` makes htmx execute `<script>` tags in swapped content.
- `/admin` is on the same origin as the public site (`cmd/server/app.go:145-164`).

There is no XSS today. Templ escapes by default, and public data comes from the owner's catalog and GeoNames place names. But any future XSS on a public page would run on the same origin as `/admin`. It could fetch an admin page and read the CSRF token from the `csrf-token` meta tag or the `<body hx-headers>` (`internal/admin/views/layout.templ:12`, `:19`). It could then drive admin actions while the owner is signed in. `__Host-` cookies and `SameSite=Strict` don't stop same-origin script.

The audit checked by grep that no `.templ` file has inline `<script>`, `style=` attributes, `on*` handlers or `hx-on`. The public config already sets `includeIndicatorStyles: false`, so htmx won't inject a `<style>` element. A strict CSP with no `'unsafe-inline'` therefore fits the public pages as they are.

**F15: the retry link bypasses templ's URL sanitizer.**

- Error panels build their retry link from the request URI: `views.LoadError(label, r.URL.RequestURI())` at `internal/web/season.go:114` and `:124`, `internal/web/ingredient.go:18`, `:39` and `:46`, and `internal/web/server.go:95`.
- `internal/web/views/season.templ:20` renders it as `href={ templ.SafeURL(retryURL) }` and `hx-get={ retryURL }`. `templ.SafeURL` tells templ to skip its URL sanitizer.
- It is safe today: after `ServeMux` routing, `RequestURI` is a path plus a query, and the value is HTML-escaped as an attribute. But this is the only link built from request input, and it has the least protection.

## 2. Goals and non-goals

Goals:

1. Every response on the site carries a strict CSP, `nosniff`, framing protection, a referrer policy and a permissions policy. That includes the public pages, static files, 404s, 429s and panic pages.
2. The admin area keeps its stricter additions: `no-store`, `noindex` and `no-referrer`.
3. HTMX runs with eval and script-tag execution off, and requests limited to the same origin, on both the public and admin sides.
4. No link built from request input bypasses templ's URL sanitizer.
5. Tests fail if a template later adds something the CSP would block.

Non-goals:

- Moving `/admin` to its own origin. §4.5 describes it as a follow-up.
- Subresource Integrity for static files (they're first-party and served from the same origin).
- CSP reporting endpoints (they'd need storage and privacy review; see §8 question 2).
- HSTS changes. It stays in Caddy.

## 3. Decision log

| Decision | Chosen | Rejected and why |
|---|---|---|
| Where headers are set | In the app, as one `SecurityHeaders` middleware inside `web.Common`, so it wraps public and admin alike. Admin overrides and adds its own values | Caddy `header` directives (the headers are then missing in `httptest` and in any non-Caddy run, so tests can't check them). Per-handler (the 404, 429 and panic paths are easy to miss) |
| Public CSP | The admin policy without `data:` in `img-src`, plus `object-src 'none'` and `font-src 'self'` | Admin's exact policy (`data:` images aren't used on public pages). A policy with `'unsafe-inline'` (defeats the purpose; nothing needs it) |
| Referrer policy (public) | `same-origin` | `no-referrer` (no benefit over `same-origin` for same-origin links). The browser default `strict-origin-when-cross-origin` (fine, but the explicit value documents intent) |
| Framing | `frame-ancestors 'none'` and `X-Frame-Options: DENY` | Allowing framing (no embedding use case) |
| Permissions policy | `geolocation=(self), camera=(), microphone=(), payment=(), usb=()` | No header (a future embed or third-party script could ask for more) |
| Cross-origin isolation headers | `Cross-Origin-Opener-Policy: same-origin` and `Cross-Origin-Resource-Policy: same-origin` | `Cross-Origin-Embedder-Policy` (no need for cross-origin isolation, and it can break future embeds) |
| HTMX config | Add `"allowEval":false,"allowScriptTags":false,"selfRequestsOnly":true` to both configs | Keep the defaults (the 2.0.4 docs list `selfRequestsOnly` as default `false` while the vendored build has `true`, so set it explicitly) |
| Retry URL | Handlers pass a URL rebuilt from validated values. The template uses `templ.URL(...)`, which sanitizes | Keep `RequestURI` and only switch to `templ.URL` (sanitized, but it still echoes raw query values) |

## 4. Design

### 4.1 `SecurityHeaders` middleware

A new function in `internal/web/middleware.go`. After the package-layering refactor (`2026-09-27-package-layering-design.md`, "S10") it moves to `internal/httpx` with the other shared middleware.

```go
// SecurityHeaders sets the site-wide response headers before calling next.
// Handlers may override them (admin sets a stricter policy).
func SecurityHeaders(next http.Handler) http.Handler
```

It sets these with `Header().Set`, before `next.ServeHTTP`:

| Header | Value |
|---|---|
| `Content-Security-Policy` | `default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'` |
| `X-Content-Type-Options` | `nosniff` |
| `X-Frame-Options` | `DENY` |
| `Referrer-Policy` | `same-origin` |
| `Permissions-Policy` | `geolocation=(self), camera=(), microphone=(), payment=(), usb=()` |
| `Cross-Origin-Opener-Policy` | `same-origin` |
| `Cross-Origin-Resource-Policy` | `same-origin` |

`web.Common` (`internal/web/server.go:102-109`) becomes `RequestID(AccessLog(SecurityHeaders(Recover(next))))`. The headers are in place before any handler runs, so a panic's 500 carries them too. `http.Error` keeps existing headers.

`admin.securityHeaders` (`internal/admin/admin.go:102-113`) keeps setting its own CSP (with `img-src 'self' data:`), `Referrer-Policy: no-referrer`, `Cache-Control: no-store` and `X-Robots-Tag: noindex`. `Set` replaces the site-wide values. It no longer needs to set `X-Frame-Options` and `nosniff`, but keeping them is harmless and stays explicit.

Static files keep `Cache-Control: public, max-age=86400` (`internal/web/server.go:148-153`). The new headers don't affect caching.

WebAuthn on `/admin` needs no permissions-policy entry. The `publickey-credentials-get` and `publickey-credentials-create` features default to `self`, and this policy doesn't list them.

### 4.2 HTMX configuration

- Public (`internal/web/views/models.go:31`): add `"allowEval":false,"allowScriptTags":false,"selfRequestsOnly":true`.
- Admin (`internal/admin/views/models.go:9`): the same three keys.

Nothing in either UI depends on the disabled features:
- The public search uses `hx-trigger="keyup changed delay:250ms, search"`, which has no bracket filters.
- Admin `hx-headers` is plain JSON built by `csrfHeaders` (`internal/admin/views/models.go:18-21`).
- `static/app.js` and `static/admin.js` are loaded as files, not as swapped inline scripts.

### 4.3 Retry URLs (F15)

- `views.LoadError(label, retryURL string)` renders `href={ templ.URL(retryURL) }`. `hx-get` keeps the plain string. HTMX already limits requests to the same origin (§4.2).
- `/season` passes `seasonRetryURL(lat, lng, place, date)`. It is built like `seasonURL` (`internal/web/places.go:11-13`) from the parsed floats, the numeric place ID when present, and the validated date. Unlike `seasonURL` it writes the floats exactly, not with `%.4f`, so rounding can't move the retry into a neighbouring 0.25° cell.
- `/ingredient` passes `ingredientURL(slug, cell, date)` (`internal/web/season.go:88-94`), using the snapshot slug and validated cell.
- The early `/ingredient` failure, before `cell` is parsed (`ingredient.go:18`), and the panic handler (`server.go:95`) validate the query the way the handler would and rebuild the URL from the parsed values: `seasonRetryURL` for `/season` with a valid `lat`/`lng`, `ingredientURL` for `/ingredient/<slug>` with a valid `cell`. If the query doesn't validate they fall back to the escaped path (`/season`, `/ingredient/<slug>`), or `/` for any other path. A panic retry then reloads the page without echoing any raw query value. A bare path alone is not enough: `/season` and `/ingredient/<slug>` with no query are 400s.
- Other `templ.SafeURL` uses in public views, whose URLs are built from server values (`places.templ:7`, `ingredient.templ:5` and `:35`, `season.templ:50`), switch to `templ.URL` too. This costs nothing and leaves one rule: no `SafeURL` in `internal/web/views`.

### 4.4 Guarding against regressions

- A unit test in `internal/web/views` renders every public component with representative view models. It fails if the HTML contains `<script` without a `src`, a `style=` attribute, an `on[a-z]+=` attribute or `hx-on`. `internal/admin/views` gets the same test.
- The e2e helpers fail a test on any browser console message containing `Content Security Policy`.

### 4.5 Follow-up: admin on its own origin

This isn't part of this design, but here is what it would take:
- Serve `/admin` only at `admin.thymeand.place`: a second Caddy site block, and the app routes by `Host`.
- Set `ADMIN_ORIGIN=https://admin.thymeand.place` and keep `ADMIN_RP_ID=thymeand.place`. An RP ID may be a registrable suffix of the origin, so existing passkeys keep working. That should be checked against go-webauthn's origin validation when this is picked up.
- `__Host-` cookies are host-only, so admin cookies would never be sent to the public origin. A public XSS could then no longer read admin pages, because they'd be cross-origin.
- Cost: one more certificate and DNS record, and an enrollment link on the new host.

Recommended: after launch, once §4.1 is in place (§8 question 1).

## 5. Implementation outline

1. `internal/web/middleware.go`: `SecurityHeaders`. `internal/web/server.go`: add it to `Common`.
2. `internal/web/views/models.go` and `internal/admin/views/models.go`: the HTMX config keys.
3. `internal/web/views/season.templ`: the `LoadError` retry link through `templ.URL`. Replace `templ.SafeURL` in the other public views. Regenerate templ.
4. `internal/web/season.go`, `ingredient.go` and `server.go`: build retry URLs as in §4.3.
5. Template lint tests (§4.4) and header tests (§6).
6. `e2e/helpers.ts`: `watchCSP` records `securitypolicyviolation` events (listener installed with `page.addInitScript`) and CSP console text as a backup. There is no console-text filter and no field-based exemption. The auto `cspGuard` fails a test that fired a page event. Home baselines call `ignoreDuring` only around each `toHaveScreenshot`, not `emulateMedia`, and assert the event list is empty after the pause. WebKit still injects `<style>body {}</style>` on every screenshot, including with `caret: 'initial'` and `animations: 'allow'`. `e2e/tests/csp.spec.ts` serves a fixture with an inline `<style>` and a `style` attribute under the public CSP and asserts the watch still reports both on every engine.
7. Core spec amendments: §10.4 "Headers" becomes "Headers on every response: …; `/admin` adds `no-store`, `noindex` and `no-referrer`"; §11.5 notes the HTMX settings.

## 6. Testing

- **Headers (`internal/web/server_test.go`, httptest on `web.Common(mux)` built like `cmd/server/app.go`):** a table of `/`, `/season?...` (200 and 400), `/ingredient/...` (404), `/static/app.js`, an unknown path, a rate-limited request (429) and a panicking test route (500).
  - Each response has the seven headers from §4.1.
  - Static files keep their `Cache-Control`.
- **Admin (`internal/admin`):** `GET /admin/login` has the admin CSP (with `data:`), `no-store`, `noindex` and `no-referrer`. The existing `securityHeaders` test keeps passing.
- **Retry URLs (`internal/web/season_test.go`, `ingredient_test.go`):**
  - A `/season` request with extra junk parameters and a DB outage renders a retry link with exactly `lat`, `lng`, `place` and `date` from validated values.
  - A panic on `/ingredient/x?cell=javascript:alert(1)` renders a retry link of `/ingredient/x`.
- **Template lint:** as in §4.4, one test per views package.
- **E2E (Playwright):** the existing public flows run under the CSP with `watchCSP` / `cspGuard`: search, geolocation allowed and denied, ingredient, back. So do the admin flows: passkey login with the virtual authenticator, edit, preview, apply, import and export. A `securitypolicyviolation` event fails the run. Screenshot tests pause the watch for the `toHaveScreenshot` call instead of filtering events by field, then assert an empty event list. `emulateMedia` stays outside that pause. A fixture page with its own `<style>` and `style` attribute still fails the watch on Chromium, Firefox and WebKit.

## 7. Rollout, operations and rollback

- **Ship before launch.** No migration or config change.
- **Check after deploy:** `curl -sI https://thymeand.place/ | grep -i -e content-security -e x-frame -e referrer -e permissions` shows the headers. Open the site with devtools and confirm there are no CSP errors in the console.
- **`docs/operations.md`, "Reverse proxy trust and logging":** add that the app sets the security headers and Caddy sets only HSTS. Adding a third-party script, font, image host or embed means changing `SecurityHeaders`.
- **Rollback:** redeploy the previous image. There is no state to undo.

## 8. Open questions for the owner

1. **Admin on its own origin (§4.5).** Recommended: yes, after launch, as its own issue.
2. **CSP violation reports.** Collecting them needs an endpoint that stores browser-sent data, which touches the privacy promises in core spec §15. Recommended: no reporting. Rely on the e2e `securitypolicyviolation` guard.
3. **HSTS preload.** `includeSubDomains` is already set in `deploy/Caddyfile:8`. Recommended: don't submit to the preload list until the domain and any subdomains (including a future admin host) are settled, because leaving the list is slow.
