# Reference data license register

One row per source file the ingest pipeline downloads (`internal/ingest/*.go`, `Sources()`). Core spec §5: no reference dataset goes live unless its license is recorded and reviewed. This register is where "reviewed" is written down: the terms that apply to the distribution we actually fetch, the obligations they place on us, where the evidence is kept, and who confirmed it when.

The rows for climate, places and the Natural Earth land mask start from what the code (`License()`) and core spec §5 record. Their terms URL, evidence and confirmation columns are filled in during the MEOW review below, so every row ends up with the same level of evidence.

**A row marked `UNRESOLVED` blocks its dataset.** The matching `License()` starts with `UNRESOLVED:` (`ingest.LicenseUnresolved`), so `ingest <dataset> approve-license` refuses it and `validate`/`swap` report `license unresolved`, even if `license_reviewed` is set by hand.

| Dataset | File | Distribution URL | Licensor | License | Terms URL | Obligations | Evidence | Confirmed on | Confirmed by |
|---|---|---|---|---|---|---|---|---|---|
| climate | `TerraClimate_19912020_tmin.nc` | `CLIMATE_SOURCE_BASE` (default `internal/ingest/climate.go` `DefaultClimateBase`) | TerraClimate authors (Abatzoglou et al., 2018) | CC0 1.0 (as recorded) | to add | None required; citation requested. About page line: see `Climate.Attribution()` | to add under `docs/licenses/terraclimate/` | not yet | not yet |
| climate | `TerraClimate_19912020_tmax.nc` | as above | as above | as above | as above | as above | as above | not yet | not yet |
| climate | `TerraClimate_19912020_ppt.nc` | as above | as above | as above | as above | as above | as above | not yet | not yet |
| ocean | `meow.geojson` | `OCEAN_SOURCE_URL` (default `DefaultOceanURL`: Esri Oceans ArcGIS Online item `74b6ac5c8fc24dcb8abaad6428a5dfa4`) | **UNRESOLVED** | **UNRESOLVED**: the ArcGIS item states no licence | **UNRESOLVED** | **UNRESOLVED**. Until confirmed, the About page carries the citation only (`Ocean.Attribution()`) | `docs/licenses/meow/` (empty) | — | — |
| ocean | `ne_50m_land.geojson` | `OCEAN_LAND_URL` (default `DefaultLandURL`) | Natural Earth | Public domain (as recorded) | to add | None required. Credited as a courtesy (`Ocean.Attribution()`) | to add under `docs/licenses/natural-earth/` | not yet | not yet |
| places | `cities1000.zip` | `https://download.geonames.org/export/dump/cities1000.zip` | GeoNames | CC BY 4.0 (as recorded) | to add | Attribution on the About page and in the site footer (`Places.Attribution()`) | to add under `docs/licenses/geonames/` | not yet | not yet |
| places | `admin1CodesASCII.txt` | `https://download.geonames.org/export/dump/admin1CodesASCII.txt` | GeoNames | as above | as above | as above | as above | not yet | not yet |

## Resolving MEOW

The procedure is in `docs/superpowers/specs/2026-09-27-reference-data-provenance-design.md` §4.2. In short:

1. Find the authoritative publisher and distribution of Marine Ecoregions of the World (Spalding et al., 2007, *BioScience*; The Nature Conservancy / WWF), and check other distributors too. This register makes no claim about anyone's terms.
2. Record the terms that apply to the distribution we would actually fetch. If they cover only the licensor's own download, the Esri item isn't covered and `DefaultOceanURL` must change. A replacement must keep `ECO_CODE_X` IDs 1–232.
3. If the terms are unclear, describe our intended use to the licensor and keep their written reply.
4. Store the evidence under `docs/licenses/meow/` (a snapshot of the terms page or the reply, personal contact details removed) and fill in the MEOW row above.
5. In one resolution commit, replace `Ocean.License()` (no `UNRESOLVED:` marker) and `Ocean.Attribution()` with what the terms say, update `internal/web/views/about.templ`, this register, core spec §5 and `docs/operations.md`, and drop the marker check in `internal/ingest/license_test.go`.
6. In production: `ingest ocean fetch`, read the printed license, then `approve-license --version N --yes`, then `all --version N`.

If the terms forbid our use, or there's no answer by the launch deadline, launch without seafood instead (`docs/operations.md`, "Launching without seafood"), and record here that the fallback was chosen.

## Launch decision

**2026-09-28: launch without seafood.** The MEOW terms aren't confirmed, so the site launches with `FEATURE_SEAFOOD` off: no ocean lookups, no seafood on any public page, and no marine credits on the About page, because no marine data is shown. The ocean pipeline, the seafood items in `content/catalog/launch.json`, and the seafood engine code all stay in place. MEOW stays `UNRESOLVED` above. To bring seafood back, resolve MEOW as above, then follow "Turning seafood on" in `docs/operations.md`.

If the confirmed terms are non-commercial, check them against any planned donation link before turning seafood on, and if in doubt, ask the licensor for written permission.
