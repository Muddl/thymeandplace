# Thyme & Place - Reference data provenance and licensing - Design

- **Status:** Proposed
- **Date:** 2026-09-27
- **Source:** Architectural audit, 2026-09-27 (findings F05, F11)
- **Related issues:** [#5](https://github.com/Muddl/thymeandplace/issues/5) (F05), [#11](https://github.com/Muddl/thymeandplace/issues/11) (F11)
- **Amends:** core spec §5 (the MEOW row is filled in from the license register; a Natural Earth row is added; a version whose license is marked unresolved can't be approved), §7 (fetch checks source pins and transport; transform checks raw files; a review carries over only for identical bytes), §13.6 (new pipeline tests), §16 (new rows for source pins and plain HTTP). §2 and the §16 "Launch catalog" row change only if the no-seafood fallback (§4.3) is used.

This spec has two parts. Part A (F05) must ship before launch, and most of it is a task for the owner, not code. Part B (F11) comes after launch.

## 1. Problem

### 1.1 F05: the ocean dataset's license is unresolved

- The recorded license says the source "states no licence: confirm terms with the original publishers (The Nature Conservancy / WWF) before approving" (`internal/ingest/ocean.go:68-73`). The source is an Esri Oceans ArcGIS Online item that is queried live (`internal/ingest/ocean.go:36-39`). `docs/operations.md:41` gives the same instruction, and so did the plan (`docs/superpowers/plans/2026-09-25-thymeandplace-03-reference-data.md:3273`).
- Nothing enforces that instruction. `ApproveLicense` refuses only an empty license (`internal/ingest/pipeline.go:253-263`), and first-deploy step 6 tells the operator to fetch, approve and run `all` for every dataset, ocean included (`docs/operations.md:20-25`).
- Once any version is approved, later fetches take over that approval automatically when the dataset, license text and source URLs match (`internal/ingest/pipeline.go:120-129`, query at `internal/store/queries/ingest.sql:29-33`). An approval given before the terms are known carries forward, and nobody has to decide again.
- The About page makes a licensing claim that nothing backs up: "distributed by UNEP-WCMC; used under its data licence" (`internal/web/views/about.templ:21`). The pipeline doesn't fetch from UNEP-WCMC, and no license has been confirmed. The About test checks only for the dataset's name (`internal/web/server_test.go:19-24`), so CI passes anyway.
- The ocean dataset also downloads Natural Earth land polygons (`internal/ingest/ocean.go:41-43`). The core spec §5 table has no row for them, and the About page doesn't mention them.

Core spec §5 says no reference dataset can go live unless its license is recorded and reviewed. Today nobody can honestly complete a review of the MEOW license.

### 1.2 F11: sources are fetched without integrity pins

- TerraClimate comes over plain HTTP: `DefaultClimateBase = "http://thredds.northwestknowledge.net:8080/..."` (`internal/ingest/climate.go:32-34`).
- The Natural Earth land mask comes from the mutable `master` branch (`internal/ingest/ocean.go:43`).
- MEOW is a live feature-service query (`internal/ingest/ocean.go:38-39`), so its content can change under the same URL.
- `download` computes a sha256 (`internal/ingest/pipeline.go:426-452`). `Fetch` stores it as `sha256:file=hex;...` in `source_checksum` (`internal/ingest/pipeline.go:111-119`, `:126-128`). Nothing ever compares that value with an expected one.
- `cmd/ingest/main.go:108` builds `&http.Client{Timeout: 2 * time.Hour}` with no `CheckRedirect`. According to the documented default policy, the client stops after 10 consecutive requests. The policy doesn't mention scheme, so nothing stops an HTTPS source from redirecting to HTTP.
- When `Transform` pulls raw files back from the bucket, it doesn't check them against the recorded checksum (`internal/ingest/pipeline.go:180-188`).
- A review carries over to a new fetch without comparing content (`internal/store/queries/ingest.sql:29-33`), and tests assert that carry-over (`internal/ingest/swap_test.go:89-93`, `:134-137`).
- The only content checks are validation spot checks. Climate is checked at 12 cities within 4 °C (`internal/ingest/climate.go:47-55`, `:150`). Ocean is checked for 225-240 regions plus 14 points (`internal/ingest/ocean.go:52-61`, `:256-270`). These catch gross corruption, not a subtly altered file.
- Known-good checksums for every current source are already on record from staging (`docs/calibration-baseline.md:12-16`).

## 2. Goals and non-goals

Goals:
1. No ocean version can be approved or go live until its terms are confirmed and recorded. The About page states exactly what those terms require.
2. One written license register covers every source file, with evidence and dates.
3. Where a source's content is fixed, fetch refuses any file whose sha256 differs from the pin, and records nothing.
4. Plain HTTP is allowed only for pinned sources. No HTTPS-to-HTTP redirect is followed.
5. An approval covers exact bytes: it carries over only when the checksums match too. Fetch and `approve-license` show what changed compared with the live version.
6. Raw files are verified before they are transformed.

Non-goals:
- Saying what any license says. This spec records no terms. It only defines how they get recorded and enforced.
- Checking publisher-supplied checksum or signature files. We pin hashes we computed ourselves. Whether any publisher offers such files was not checked.
- Re-verifying the transformed CSV at load time. Our own code builds it from verified inputs, and it lives in the same private bucket. We accept this risk.
- Scheduled refreshes (sub-project 3), changes to transforms or validation thresholds, and schema migrations.

## 3. Decision log

| Decision | Chosen | Rejected and why |
|---|---|---|
| Gate on the unresolved license | `Ocean.License()` starts with an `UNRESOLVED:` marker. `ApproveLicense` and `Validate` refuse any license that contains it | Documentation only (the situation today, and runbook step 6 says the opposite); removing ocean from the CLI (would break real-data staging) |
| Where terms are recorded | A `docs/reference-data-licenses.md` register, evidence under `docs/licenses/`, and a summary in `License()` | The `License()` string alone (no room for evidence, date or obligations); new `dataset_versions` columns (a migration for text that rarely changes) |
| About-page attribution | Stays hand-written in templ. A test ties it to a new `Dataset.Attribution()` | Generating it from `dataset_versions.license` (would publish internal review notes); leaving it untested (that is how today's mismatch got through) |
| If the terms don't allow our use | The owner chooses. In order of preference: another MEOW distribution with the same `ECO_CODE_X` IDs, launch without seafood (§4.3), or delay launch | Launching on the unconfirmed data (the risk this spec exists to remove) |
| Pin mechanism | An expected sha256 on each `Source`. The pin belongs to the content, not the URL, so it still applies when an env var overrides the URL | Per-URL pins (a mirror with the same bytes would fail); pins set by env var (unreviewed config could change the content) |
| Unpinnable sources | Must declare an `Unpinned` reason. A test requires every default source to have exactly one of a pin or a reason | A silent default of "unpinned" |
| Plain HTTP | Allowed only when the source is pinned. HTTPS-to-HTTP redirects are refused | Requiring HTTPS everywhere (THREDDS HTTPS is unverified, and a pin already gives integrity); leaving it as today |
| When a review carries over | Same license, same URLs and same checksums | Today's license plus URLs (content can change silently); never carrying over (re-approving identical bytes is busywork and breaks re-runs) |
| Natural Earth reference | Raw URL at a full commit SHA | `master` (mutable); a tag (the repo owner can move it); a copy in this repo (bloats the repo and gains nothing over a pin) |
| MEOW pin | Pin the reviewed response if the live query is byte-stable. If it isn't, serve a reviewed copy from our own bucket (§4.4) | Leaving it unpinned (every fetch would show "changed", which is noise, not signal); a copy in this repo (terms not yet known; changing the data would need an image rebuild) |
| Withdrawing an ocean version | SQL that repoints the view to `_v0` and clears `is_live`, `license_reviewed` and `promoted_at` | A new CLI step (used at most once) |
| Schema | No migration. The `source_checksum` format stays as it is | New columns (nothing needs them) |

## 4. Design

### 4.1 License gate and register (F05, code)

- Add `const LicenseUnresolved = "UNRESOLVED:"` to `internal/ingest/pipeline.go`. If `dv.License` contains it, `ApproveLicense` returns `license terms not confirmed; see docs/reference-data-licenses.md`. `Validate` (`pipeline.go:275-279`) adds the failure `license unresolved` even when `license_reviewed` is true. A row approved by hand-written SQL therefore still can't go live.
- `Ocean.License()` becomes `"UNRESOLVED: Marine Ecoregions of the World (Spalding et al., 2007) via Esri ArcGIS Online item 74b6ac5c8fc24dcb8abaad6428a5dfa4; terms not yet confirmed (docs/reference-data-licenses.md); land mask: Natural Earth 1:50m land (recorded as public domain)"`. Because the text changes, no earlier ocean approval can carry over.
- Add `Attribution() []string` to the `Dataset` interface. Each entry is one plain-text credit line that must appear on the About page. Climate and places return the lines already on the page. Until the terms are resolved, ocean returns only the MEOW citation plus a Natural Earth credit (§8 Q5).
- `internal/web/views/about.templ:21`: remove "distributed by UNEP-WCMC; used under its data licence". The line keeps the citation only until the register says otherwise.
- `docs/reference-data-licenses.md` (new) has one row per source file, with these columns: dataset, file, distribution URL, licensor, license name, terms URL, obligations (the exact attribution text and any restrictions), evidence location, confirmed on, confirmed by. The MEOW row reads `UNRESOLVED`. The other rows start from what the code and core spec §5 record today. The owner adds each terms URL and date during the MEOW review.

### 4.2 Resolving the MEOW license (F05, owner task)

The code change in §4.1 doesn't depend on how this turns out.

1. Find the authoritative publisher and distribution of MEOW. Start from Spalding et al. (2007, *BioScience*) and The Nature Conservancy / WWF, and also check other distributors, including UNEP-WCMC, which the About page already names. This spec makes no claim about any of their terms.
2. Record the terms that apply to the distribution we would actually fetch. If they cover only the licensor's own download, the Esri item isn't covered, and the source must switch to that download (step 5).
3. If the terms are unclear, describe our intended use to the licensor (§8 Q2) and keep their written reply.
4. Store the evidence under `docs/licenses/meow/`: a snapshot of the terms page or the publisher's reply, with personal contact details removed. Fill in the register row.
5. Make one resolution commit. It replaces `Ocean.License()` with the confirmed terms (no marker) and `Ocean.Attribution()` with the attribution the terms require, and updates `about.templ`, the register, the core spec §5 table and `docs/operations.md`. If the distribution changes, it also updates `DefaultOceanURL`. The new source must keep `ECO_CODE_X` IDs 1-232. The launch catalog's seafood regions use those IDs, the content gate checks that they fall in 1-232 (`content/content_test.go:50-54`), and ocean validation checks that each one exists (`internal/ingest/ocean.go:271-286`).
6. In production: `ingest ocean fetch`, read the printed license, then `approve-license --version N --yes`, then `all --version N`.

| Outcome | Action |
|---|---|
| The terms allow our use of the distribution we fetch | Steps 4-6 |
| The terms allow only a different distribution with the same IDs | Steps 4-6 plus the new URL. The GeoJSON transform stays if the format is the same. A different format (for example a shapefile) needs its own design note |
| The terms forbid our use, or there is no answer by the owner's deadline (§8 Q1) | §4.3 |

### 4.3 Fallback: launch without seafood

If no ocean version is promoted, the `ocean_ecoregion` view stays on the empty `ocean_ecoregion_v0` table (`migrations/00002_reference.sql:27-33`). No ecoregion is ever in reach, so every seafood item is out of season everywhere (`internal/season/rules.go:138-158`). The seafood group is then left out with no message, which is the core spec §11.6 "Inland" row (`internal/web/season.go:215-218`). This needs no code change.

The catalog is the catch. Import checks every seafood region against the live ecoregions (`internal/changeset/changeset.go:118-139`), so importing `content/catalog/launch.json`, with its 21 seafood items, would be rejected. Instead, the owner imports a derived file that drops the seafood items and the one land pairing that points at one of them (dill to atlantic-cod). This filter was run against `launch.json` with jq 1.8.1: 101 ingredients became 80, with no seafood left.

```bash
jq '([.ingredients[] | select(.kind == "seafood") | .slug]) as $sea
    | .ingredients |= map(select(.kind != "seafood") | .pairings -= $sea)' \
  content/catalog/launch.json > /tmp/launch-no-seafood.json
```

`launch.json` itself doesn't change, so `go test ./content/` still gates the full catalog (`content/content_test.go:62-64`). Calibration fixtures list no seafood, so the calibration score isn't affected. On the About page, the seafood paragraph (`internal/web/views/about.templ:14`) becomes one line saying seafood isn't covered yet. The MEOW citation stays in the sources list. To turn seafood on later: resolve the license, ingest ocean, then import `launch.json`. Import only upserts (`internal/changeset/changeset.go:40-46`), so this adds the seafood items and the pairing back.

If an ocean version was already approved or made live in production before the terms are resolved, withdraw it. From `/opt/thymeandplace/deploy`, run `docker compose exec db psql -U tp -d thymeandplace`:

```sql
BEGIN;
CREATE OR REPLACE VIEW ocean_ecoregion AS SELECT * FROM ocean_ecoregion_v0;
UPDATE dataset_versions SET is_live = false, license_reviewed = false, promoted_at = NULL
 WHERE dataset = 'ocean';
SELECT pg_notify('data_version_changed', 'dataset');
COMMIT;
```

This does the same as `point()` (`internal/ingest/pipeline.go:369-401`). Clearing `promoted_at` stops `ingest ocean rollback` from returning to a withdrawn version (`internal/store/queries/ingest.sql:21-24`). Seafood items that were already imported stay in the catalog but never show. Admin edits to those items fail the ecoregion check until ocean is back. If the terms also forbid keeping copies, delete `raw/ocean/` and `transformed/ocean/` from the bucket and drop each `ocean_ecoregion_v{N}` table. Nightly dumps age out within the 28-day retention (core spec §14).

### 4.4 Source pins and transport (F11)

```go
type Source struct {
	URL, File string
	SHA256    string // expected hex sha256 of the file; empty only when Unpinned is set
	Unpinned  string // why this file's content can't be pinned; empty when SHA256 is set
}
```

| Source | URL after this change | Pin |
|---|---|---|
| `TerraClimate_19912020_{tmin,tmax,ppt}.nc` | THREDDS, over HTTPS if the server offers it (§8 Q3) | The three values in `docs/calibration-baseline.md:16` |
| `ne_50m_land.geojson` | `raw.githubusercontent.com/nvkelso/natural-earth-vector/<full commit SHA>/geojson/ne_50m_land.geojson` | `e874b27a…` (baseline). Pick the commit whose file hashes to this value, so live data doesn't change. If no commit matches, pin the current file and re-run calibration |
| `meow.geojson` | Whatever §4.2 settles on | `533ce943…` (baseline), if the source stays the ArcGIS query and that query proves byte-stable (below) |
| `cities1000.zip`, `admin1CodesASCII.txt` | Unchanged (HTTPS) | Unpinned: "GeoNames regenerates its dumps at fixed, unversioned URLs" |

- **Fetch.** After each download, `Fetch` compares the sha256 with `SHA256`. On a mismatch it fails with the file name, the expected and actual hashes, and "live data unchanged; if the publisher changed this file on purpose, review it and update the pin in `internal/ingest/<dataset>.go`". The check runs inside the download loop (`pipeline.go:112-119`), before `CreateDatasetVersion` (`:126`), so no row is written and nothing is uploaded.
- **Env overrides.** Pins still apply when a URL is overridden (`OCEAN_SOURCE_URL`, `OCEAN_LAND_URL`, `CLIMATE_SOURCE_BASE`; `cmd/ingest/main.go:28-40`). A mirror that serves the same bytes works. Using different content takes a reviewed code change.
- **Transport.** `ingest.CheckTransport(ds) error` requires every source to use `https://`, or `http://` or `bucket:` with a pin. `cmd/ingest` calls it before any step that fetches. Tests use `httptest` servers directly and don't go through this check.
- **Redirects.** `ingest.NewHTTPClient(timeout)` sets a `CheckRedirect` that refuses a redirect when `via[0]` was HTTPS and the next request isn't. It also keeps the 10-request limit itself, because a non-nil `CheckRedirect` replaces the default policy.
- **MEOW byte stability.** At implementation time, fetch the query twice, at least a day apart, and compare the bytes. The response is about 1 MB (plan 03, `:2307`). If the bytes differ while the data is the same, copy the reviewed `raw/ocean/v{N}/meow.geojson` to `pinned/ocean/meow.geojson` in the bucket and make the source `bucket:pinned/ocean/meow.geojson`. `Fetch` reads that scheme with `Blob.GetFile` and still checks the pin.

### 4.5 A review covers content

```sql
-- name: LicenseReviewedBefore :one
SELECT EXISTS (
  SELECT 1 FROM dataset_versions
  WHERE dataset = @dataset AND license = @license AND source_url = @source_url
    AND source_checksum = @source_checksum AND license_reviewed
);
```

A new `Pipeline.ReviewSummary(ctx, v)` prints the license and, for each file, its URL, its sha256, whether it's pinned, and how it compares with the live version: `same as live vK`, `changed (live vK: abcd…)` or `no live version`. `fetch` prints it in place of the current source list (`cmd/ingest/main.go:122-129`). `approve-license --version N` without `--yes` now connects, prints the summary, and exits 2 with "re-run with --yes to approve". Without `--version` it still exits 2 before connecting (`cmd/ingest/main.go:77-80`). With `--yes`, the summary is printed again as the record. Because GeoNames is unpinned, every places refresh now needs `approve-license` (§8 Q4).

### 4.6 Raw files verified before transform

After `Transform` has made sure the raw files are local (`internal/ingest/pipeline.go:180-188`), it hashes each one and compares the result with the version's recorded `source_checksum`. On a mismatch it fails with "raw file differs from what was fetched and reviewed". This also covers the climate NetCDF files that the Python container reads, because `ingest climate transform` runs first and exits 3 only after the check. The cost is hashing about 3 GB of NetCDF each time the Go transform runs for climate (`docs/operations.md:48`), which is seconds to tens of seconds on the VPS.

### 4.7 What was and wasn't verified

- Verified through Context7:
  - Go's `http.Client` default redirect policy is a limit of 10 consecutive requests, and a non-nil `CheckRedirect` is called in its place (`net/http` docs for go1.25.3; Context7 had no 1.26 page).
  - GitHub documents commit-ID file links as permanent. The documentation shows this for `github.com/.../blob/<sha>/` URLs; that `raw.githubusercontent.com` treats refs the same way was not verified separately. The sha256 pin makes this matter less.
  - Postgres delivers `NOTIFY` only when the transaction commits (Postgres 17 docs).
- Verified locally: the §4.3 jq filter.
- Not verified:
  - Whether THREDDS serves HTTPS (§8 Q3).
  - Whether the ArcGIS query is byte-stable (§4.4).
  - Any license terms.

## 5. Implementation outline

Part A (F05, before launch):
1. `internal/ingest/pipeline.go`: add `LicenseUnresolved`, the `ApproveLicense` refusal, the `Validate` failure, and `Attribution() []string` on `Dataset`.
2. `internal/ingest/ocean.go`, `climate.go`, `places.go`: the marker in `Ocean.License()` and `Attribution()` for each dataset.
3. `internal/web/views/about.templ`: remove the UNEP-WCMC claim and add the Natural Earth credit. Then run `templ generate` (regenerates `about_templ.go`).
4. `internal/web/about_test.go` (new): the attribution test (§6).
5. `docs/reference-data-licenses.md` (new) and `docs/licenses/` (new). In `docs/operations.md`: rewrite "Ocean dataset: two sources, two licenses" (`:37-44`), add a note to step 6 (`:20-25`), and add a "Withdrawing a dataset" section with the §4.3 SQL.
6. The owner works through §4.2, then makes the resolution commit (step 5) and runs the production ingest, or applies §4.3.

Part B (F11, after launch):
7. `internal/ingest/pipeline.go`: add the `Source` fields, the pin check in `Fetch`, a `bucket:` branch next to `download`, raw-file verification in `Transform`, and `ReviewSummary`.
8. `internal/ingest/fetch.go` (new): `CheckTransport`, `NewHTTPClient`, and `parseChecksums` for the existing `sha256:file=hex;...` format.
9. `internal/ingest/climate.go`, `ocean.go`, `places.go`: add pins and reasons, replace the Natural Earth URL with the commit URL, and add the MEOW pin or the `bucket:` fallback.
10. Change the positional `Source{url, file}` literals in `internal/ingest/places_test.go:60`, `:119-122` and `internal/ingest/climate_test.go:44` to keyed fields. They stop compiling once `Source` has four fields.
11. `internal/store/queries/ingest.sql`: update the query, then run `sqlc generate` (`internal/store/ingest.sql.go`). `Fetch` passes the checksum.
12. `cmd/ingest/main.go`: use `NewHTTPClient` and `CheckTransport`, print the summary on fetch, and add the `approve-license` summary flow.
13. `deploy/env.example:24-28`: note that URL overrides keep the content pin. Update `docs/operations.md` (§7) and the core spec sections listed under "Amends".

## 6. Testing

- `TestApproveLicenseRefusesUnresolved` (`dbtest.New`, `internal/ingest`): a places-shaped dataset whose license contains the marker. `ApproveLicense` fails. After `UPDATE ... SET license_reviewed = true`, `Validate` still reports `license unresolved`, and `Swap` leaves the live view unchanged.
- `TestAboutCarriesDatasetAttributions` (`internal/web`, existing `httptest` harness): renders `/about/data`, strips tags, unescapes entities and collapses whitespace. It then checks that every `Attribution()` line of `NewPlaces()`, `NewOcean("")` and `NewClimate("")` appears, and that the page doesn't contain "used under its data licence".
- `TestDefaultSourcesDeclareProvenance` (table-driven, no DB): every default source has exactly one of a 64-hex `SHA256` or a non-empty `Unpinned`, and every dataset has a non-empty `License()` and `Attribution()`.
- `TestFetchRejectsPinMismatch` and `TestFetchAcceptsPin` (`dbtest` + `serveFiles`): on a mismatch, the error names the file, `dataset_versions` has no row, and the FSBlob bucket has no `raw/` key.
- `TestReviewCarriesOverOnlyForSameBytes`: approve v1. Serve changed bytes: v2 isn't reviewed. Serve v1's bytes again: v3 is. The existing carry-over assertions in `swap_test.go` still pass, because they serve identical bytes.
- `TestCheckTransport` (table): HTTPS unpinned is allowed. HTTP pinned and `bucket:` pinned are allowed. HTTP unpinned and `bucket:` unpinned are refused.
- `TestRedirectPolicy`: an `httptest.NewTLSServer` that redirects to an `httptest.NewServer` URL is refused. HTTPS to HTTPS is followed. 11 redirects are refused.
- `TestTransformRejectsTamperedRaw`: after fetch, overwrite the raw file in the bucket and delete the local copy. `Transform` must fail.
- `TestReviewSummary`: `same`, `changed` and `no live version` lines against a live v1.
- `cmd/ingest/main_test.go`: `approve-license` without `--version` still exits 2 without touching the DB.
- CI has no network. The pin values are checked once by hand at implementation time: `curl` each pinned URL and compare its `sha256sum` with `docs/calibration-baseline.md`.

## 7. Rollout, operations and rollback

Part A:
- Check production first: `SELECT id, license, license_reviewed, is_live, promoted_at FROM dataset_versions WHERE dataset = 'ocean'`. If any row is approved or promoted, apply the §4.3 withdrawal SQL before launch, whatever the outcome of the review.
- Deploy the marker. From then on, `ingest ocean approve-license` fails in every environment until the resolution commit lands. Local staging keeps the ocean version that is already live there. devseed writes the `_v0` tables directly (`internal/devdata/reference.go:91`), so e2e isn't affected.
- `docs/operations.md`: point step 6 at the register, rewrite the ocean section, add "Withdrawing a dataset", and add this pre-launch checklist line: "MEOW row in `docs/reference-data-licenses.md` is confirmed, or the no-seafood fallback is in place".
- Rollback: revert the code. The register and evidence files stay.

Part B:
- Deploy with `docker compose --profile tools build ingest`. The next climate and ocean fetches should match their pins, because the bytes are the same as in the baseline. Every places refresh then needs `approve-license`.
- `docs/operations.md` gets a new section, "When a pinned source changes". The fetch fails and live data is untouched. Download the file by hand and compare it with the archived raw copy in the bucket. If the change is legitimate, update the pin in a commit that says why, re-run calibration, then fetch, approve and swap.
- Rollback: revert the code. There's no migration and `source_checksum` keeps its format, so every existing row stays valid. Rolling back data is still `ingest <dataset> rollback`.

## 8. Open questions for the owner

1. **Deadline and fallback.** By what date must MEOW be resolved, and what happens if it isn't? Recommended: first try another MEOW distribution with explicit terms and the same IDs. If there's still nothing a week before launch, launch without seafood (§4.3) rather than delay.
2. **Intended use to declare to the licensor.** Some terms treat commercial and non-commercial use differently. Recommended: describe the plan as it is (a free public site, and whether ads or revenue are planned), record it in the register, and review again if it changes.
3. **HTTPS for TerraClimate.** Whether `thredds.northwestknowledge.net` serves the same files over HTTPS wasn't verified. Recommended: check once from the VPS. If the files there hash to the pins, switch the default base to HTTPS. If not, keep HTTP, which is safe because the files are pinned.
4. **Places refreshes need re-approval every time.** Recommended: accept this. Refreshes are manual and rare, and the summary shows exactly what changed.
5. **Natural Earth credit.** The code records the land mask as public domain. Should the About page credit it anyway? Recommended: yes, as a courtesy line.
