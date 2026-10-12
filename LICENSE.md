# License

Copyright © 2026 Kaiser Wade. All rights reserved.

## This repository

Everything in this repository is proprietary unless it's listed under "Third-party code and data" below. That includes the source code, templates, stylesheets, scripts, tests, documentation, and the catalog content: the ingredient entries, tips, notes, pairings and season rules in `content/`, and the test catalogs in `testdata/`.

No license is granted to use, copy, modify, publish, distribute, sublicense, sell or host any of it, in whole or in part, without the copyright holder's prior written permission, except where the law allows it regardless. Access to the repository, for example as a collaborator, doesn't by itself grant any of these rights.

The calibration fixtures in `calibration/fixtures/` record, for each city, the months that a published seasonal guide lists; each file cites its guide. The guides belong to their publishers. The fixtures are used only to score the engine's estimates, and the site doesn't republish them.

THE SOFTWARE AND CONTENT ARE PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED. IN NO EVENT SHALL THE COPYRIGHT HOLDER BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY ARISING FROM THEIR USE.

## Third-party code and data

These keep their own licenses, and nothing above changes them.

| Component | Where | License |
|---|---|---|
| htmx 2.0.4 | `static/htmx.min.js` | BSD 2-Clause, © Big Sky Software (see `static/README.md`) |
| Abril Fatface, Libre Franklin, Barlow, Barlow Condensed, Bevan, Archivo | `static/design/fonts/` | SIL Open Font License 1.1 (see `static/design/fonts/LICENSES.txt`) |
| Tomato plate cut from A. W. Livingston's Sons, *Annual of True Blue Seeds* (1897) | `static/design/art/` | Public domain; USDA National Agricultural Library scan, credited in the site footer (see `docs/design/README.md`) |
| Tomato bush woodcut cut from Johnson & Stokes, *Garden & Farm Manual* (1890) | `static/design/art/` | Public domain; Biodiversity Heritage Library scan, credited in the site footer (see `docs/design/README.md`) |
| Go modules | `go.mod`, `go.sum`; compiled into the binaries | Each module's own license, shipped with its source |
| Python packages | `pipeline/transform/requirements*.txt` | Each package's own license |
| Container images | `deploy/Dockerfile.*`, `deploy/compose.yaml` (Postgres/PostGIS, Caddy, Go, Python and others) | Each image's own licenses |
| TerraClimate climate normals | Downloaded by `cmd/ingest climate` | CC0 1.0, as recorded |
| GeoNames place names | Downloaded by `cmd/ingest places` | CC BY 4.0: credited on every page and on the About page |
| Natural Earth land polygons | Downloaded by `cmd/ingest ocean` | Public domain, as recorded |
| Marine Ecoregions of the World | Downloaded by `cmd/ingest ocean` | **Unresolved**; not used while seafood is off |

The terms, obligations and evidence for each dataset are recorded in `docs/reference-data-licenses.md`, which is the authority for reference data. If a dataset's terms change, that register changes first.
