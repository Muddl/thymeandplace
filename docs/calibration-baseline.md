# Calibration baseline

- Date: 2026-09-27
- Data version: `4bc01f4a2a770d63` (from `calibration_run` row 3: catalog version 983 with live datasets places v1, ocean v3, climate v4)
- Score: 0.671 over 528 compared (fixture, ingredient) pairs
- Fixtures: 15 (4 southern, 2 tropical, 3 high-latitude)

## Reference data

The data version string hashes the catalog version and the dataset row IDs, so it differs between deployments. To confirm that another stack runs the same reference data, compare these source checksums (`SELECT dataset, source_checksum FROM dataset_versions WHERE is_live`):

| Dataset | Version / table | Source checksums (sha256) |
|---|---|---|
| places | v1 `place_v1` | `cities1000.zip=527a70c6fe2c94eea72180c31693622986b6c9aec3fa72ae7f433cd6c8970136`; `admin1CodesASCII.txt=1da92a6323a5fec3176f3f743bf4cf4040fd56a876da55e46fbca23c863aa60a` |
| ocean | v3 `ocean_ecoregion_v3` | `meow.geojson=533ce94372c46865bc7afe8e2892fb0e1014cc1f8376e5d49679b7da2ab4428c`; `ne_50m_land.geojson=e874b27a51d146452be360cafb3cc50c86001074a67d534113e6534682f9826b` |
| climate | v4 `climate_cell_v4` | `TerraClimate_19912020_tmin.nc=93961a5e423c02282bc1aea2761dbdd28bb8ec1ee79ca571f4b8b1db29d42a7a`; `TerraClimate_19912020_tmax.nc=04e1293284826d5c9814336a52c389d8552523dbe3ccd3aca072e6ca17ac6f58`; `TerraClimate_19912020_ppt.nc=eae7eaa3f27c17376178af7a51e6cc5918287d99df35dc00e93ef274b174f211` |

With the same checksums and the same catalog and fixtures, production should score within 0.02 of 0.671.

## Ingredients below 0.3 and why
| Ingredient | Score | Reason / follow-up |
|---|---|---|
| pomegranate | 0.000 (1 fixture: San Francisco) | The guide lists Sep–Dec from California supply (Central Valley), while the fixture cell is cool-summer San Francisco (warmest month 18.9 °C), which never reaches the 19.5 °C hot-summer threshold. A threshold low enough for San Francisco would also show pomegranates in London (warmest month 18.1 °C). Kept as is. Follow-up: a fixture point for a hot-summer California location, or regional supply adjustments. |

Close to the line (0.30–0.35), all with the same kinds of limits:

| Ingredient | Score | Note |
|---|---|---|
| globe-artichoke | 0.306 | Mediterranean artichokes crop from winter into spring (Barcelona Jan–May, Santiago May–Oct) after a summer rest. The warm-up rule only models a spring cutting season. Autumn crops in San Francisco are also missed. |
| avocado | 0.317 | San Francisco (all year) and Hobart (Jan–Aug) list avocados that stores bring in from other regions, and the engine only models local growing. Honolulu never cools below the 19.5 °C warm-up, so the warm-up rule never starts a season there. Where avocados are grown and the rule applies, the match is good (Auckland 1.00, Santiago 0.58). |
| morel | 0.333 | One fixture (London, April only). The temperature-only rule also fruits morels in autumn. A narrower window would fit London but would drop spring in New York. |
| brussels-sprouts | 0.341 | The [0, 15] °C picking window can't tell spring from autumn, so continental cities show sprouts in April as well as in autumn. A long-season model fits better (0.60) but is year-round in maritime climates and fails the content gate. |

## Method and blind spots

The score only compares (fixture, ingredient) pairs that a fixture lists. If an ingredient shows in a city whose guide does not list it, nothing is scored, so that false positive is invisible to calibration. For example, the first tuning pass set heat-crop lower bounds to 13–17 °C. That raised the score, but it showed okra and watermelon in Berlin, Toronto and London, and peppers and eggplant in Oslo, and no guide there lists them.

The negative content gates in `content/content_test.go` cover this blind spot with synthetic climates:
- No tender fruit in the continental (New York-like) climate.
- No temperate fruit in the tropics.
- No year-round cool-season crops in maritime climates.
- No heat crops or figs in the Oslo-like cool-summer climate (`climatetest.CoolSummer`, warmest month 16.5 °C).
- No okra, watermelon, fig or other frost-tender perennial fruit in the Berlin-like climate (`climatetest.CentralEurope`, warmest month 19.5 °C, coldest-month mean minimum −1.5 °C).

After admin edits, export the catalog to `content/catalog/launch.json` and run `go test ./content/` (see `content/AUTHORING.md`, "After editing in the admin").

Heat crops and figs showing where the fixture does not list them, from the real fixture cells:

| City | Initial (`38568cf`) | First tuning (`0fe6abe`) | Final |
|---|---|---|---|
| auckland-nz | fig | okra, fig | okra, fig |
| barcelona-es | okra, chili-pepper | okra, chili-pepper | okra, chili-pepper |
| berlin-de | fig | okra, watermelon, eggplant, bell-pepper, chili-pepper, fig | eggplant, bell-pepper, chili-pepper (August only) |
| hobart-au | fig | eggplant, chili-pepper, fig | eggplant, chili-pepper |
| hong-kong-hk | sweet-potato, bell-pepper, chili-pepper | same | same, plus fig (April–July) |
| honolulu-us | okra, chili-pepper | same | same |
| london-gb | fig | fig | fig |
| minneapolis-us | okra, fig | okra, watermelon, sweet-potato, chili-pepper, fig | okra, watermelon, sweet-potato, chili-pepper |
| montevideo-uy | okra, chili-pepper | same | okra, chili-pepper, eggplant, bell-pepper (their supply rows were dropped from the fixture, see below) |
| new-york-us | okra, chili-pepper, fig | okra, sweet-potato, chili-pepper, fig | okra, sweet-potato, chili-pepper |
| oslo-no | fig | eggplant, bell-pepper, chili-pepper, fig | none |
| san-francisco-us | none | watermelon | watermelon |
| santiago-cl | eggplant, chili-pepper | okra, sweet-potato, eggplant, chili-pepper | same |
| tokyo-jp | okra, watermelon, chili-pepper, fig | same | same |
| toronto-ca | fig | okra, chili-pepper, fig | chili-pepper |

The heat crops that remain show in hot-summer cities (Minneapolis, New York, Tokyo, Santiago), where they can be grown even though the guides don't list them. Berlin's peppers and eggplant show in August only. Figs are now `frost-tender`, so the winter-kill rule removes them from every hard-winter city (Minneapolis, New York, Toronto, Chicago, Berlin, Moscow, Seoul, Beijing). An earlier version of this document called figs in Minneapolis and New York plausible, which was wrong. Figs remain in mild-winter London, Paris and Seattle, and in Hong Kong as a side effect of the 17.65 °C warm-up (Hong Kong's March is 18.7 °C).

## Initial score and tuning

Scored against the current fixtures, the launch catalog as committed before this task (`38568cf`) scores **0.480** over 528 pairs, on the same reference data.
- **First tuning pass** (`0fe6abe`): 0.673 over the original 534 pairs.
- **False-positive fixes** (`11de65b`): 0.667 over 534 pairs.
- **Final** (Ruling H: mango year-round, fig frost-tender, 6 supply rows dropped from the Montevideo fixture): **0.671** over 528 pairs.

Per fixture, initial → final, both scored against the current fixtures. The pair counts and drops cover annual, perennial and mushroom pairs; year-round items (mango) are left out.

| Fixture | Initial | Final | Pairs up / down / same | Largest drops (initial → final) |
|---|---|---|---|---|
| auckland-nz | 0.488 | 0.712 | 32 / 1 / 17 | grape 0.67→0.25 |
| barcelona-es | 0.555 | 0.667 | 19 / 12 / 11 | parsnip 0.67→0.44, radish 0.78→0.58, plum 0.50→0.33, spring-onion 0.50→0.33, apricot 0.67→0.50, grape 0.17→0.00 |
| berlin-de | 0.655 | 0.731 | 22 / 5 / 20 | plum 1.00→0.50, blueberry 1.00→0.67, peach 1.00→0.75, apricot 0.67→0.50, sweet-corn 0.67→0.50 |
| hobart-au | 0.442 | 0.578 | 10 / 1 / 6 | brussels-sprouts 0.67→0.57 |
| hong-kong-hk | 0.522 | 0.750 | 11 / 0 / 5 | none |
| honolulu-us | 0.482 | 0.616 | 6 / 0 / 15 | none (mango 0.00→0.75 not counted) |
| london-gb | 0.503 | 0.618 | 20 / 3 / 20 | broccoli 0.83→0.71, apple 0.62→0.56, celery 0.40→0.33 |
| minneapolis-us | 0.307 | 0.638 | 19 / 2 / 6 | winter-squash 0.67→0.57, potato 0.56→0.55 |
| montevideo-uy | 0.526 | 0.846 | 24 / 1 / 9 | pea 1.00→0.44 |
| new-york-us | 0.427 | 0.713 | 30 / 4 / 6 | blackberry 1.00→0.50, sweet-corn 0.75→0.40, peach 1.00→0.75, strawberry 0.67→0.50 |
| oslo-no | 0.408 | 0.535 | 16 / 5 / 10 | strawberry 0.75→0.60, blueberry 0.33→0.25, apple 0.70→0.64, turnip 0.62→0.57, pear 0.14→0.12 |
| san-francisco-us | 0.446 | 0.638 | 23 / 1 / 30 | winter-squash 0.30→0.27 |
| santiago-cl | 0.557 | 0.678 | 14 / 3 / 13 | mandarin 0.60→0.50, apple 0.80→0.73, celery 0.22→0.20 |
| tokyo-jp | 0.321 | 0.478 | 11 / 1 / 5 | blueberry 0.67→0.50 |
| toronto-ca | 0.398 | 0.698 | 29 / 1 / 12 | brussels-sprouts 0.25→0.17 |

Before tuning, 11 ingredients scored below 0.3. Now only pomegranate does. Parameters of 49 ingredients were changed (the first pass changed 48 one at a time; mango then became year-round). A change was kept when its own score rose and `go test ./content/` still passed. Other cities could still lose on individual pairs, as the table shows. The main changes:

- **Warm-season annuals** (bell pepper, chili, eggplant, okra, sweet potato, watermelon): lower bounds of 18–20 °C had kept them out of most temperate cities. The final bounds are 15 °C for peppers and eggplant, 16 °C for sweet potato and 18 °C for okra and watermelon, with 2–3 months to harvest. The first pass used 13–17 °C, and raising it to these values cost 0.006 to keep these crops out of cool-summer cities. Scores went 0.196→0.636 (bell pepper), 0.067→0.544 (chili), 0.227→0.662 (eggplant), 0.375→0.521 (okra) and 0.189→0.527 (watermelon). Watermelon misses Toronto (the guide lists Jul–Sep), because only two months are above 18 °C there. Tomato, cucumber, sweet corn and winter squash got smaller adjustments of the same kind.
- **Cool-season annuals** (broccoli, cauliflower, cabbage, kale, leek, carrot, beetroot, spinach, turnip, celery, bok choy, onion, spring onion, chard, lettuce, radish, potato): an upper bound of 18–22 °C removed the whole summer in New York, Toronto and Minneapolis, where the guides list these crops. The bound is now 24–26 °C (broccoli 0.415 → 0.800, spinach 0.380 → 0.820). The trade-off is Barcelona. Its August mean is 24.6 °C, just under 25, so broccoli, cabbage, cauliflower, turnip, spring onion and radish are now year-round there, while the guide has a summer gap: broccoli 0.57→0.50, cabbage 0.62→0.50, spring onion 0.50→0.33, radish 0.78→0.58. Leek, chard, lettuce, beetroot and celery improved in Barcelona, so its fixture score still rose overall.
- **Winter-picked crops** (brussels sprouts, parsnip): the picking window is now [0, 15] °C, and parsnip is sold from storage for 3 months.
- **Temperate fruit and berries** (apple, pear, plum, cherry, apricot, peach, grape, strawberry, raspberry, blueberry, blackberry): the warm-up is now 12 °C (11 for cherry), with shorter bloom-to-harvest offsets and longer spans. Scores went from 0.34–0.54 to 0.56–0.72. Two known misses:
  - Grape dropped in Barcelona (0.17 → 0.00: the engine shows Jun–Aug, the guide Sep–Dec) and in Auckland (0.67 → 0.25).
  - Apple and pear ripen late in Oslo. Apple is harvested Oct–Jan and then sold from storage until June, while the guide lists Aug–Dec and Jan–Apr (0.64). Pear is harvested Oct–Dec against a guide of Aug–Oct (0.12). Oslo warms past 12 °C only in June, and one offset can't suit Oslo and the milder cities.
- **Fig**: the warm-up is 17.65 °C with a 1-month offset and a 4-month span, and fig is `frost-tender` (0.338 → 0.700). Oslo has none, London gets figs Aug–Nov and Barcelona Jul–Oct (its guide exactly). 17.65 sits in the middle of the plateau between Barcelona's May (17.5 °C) and San Francisco's June (17.8 °C). A 17 °C warm-up scored 0.542–0.633 and put Hobart on a 0.1 °C edge. Making fig frost-tender left the score unchanged.
- **Mango** is `cultivated_year_round` (0.000 → 0.750, Honolulu). The warm-up rule never showed it in the humid tropics but did in Tokyo, Madrid, Rome and Barcelona.
- **Citrus and avocado**: harvest spans went up to 6–8 months. The warm-up stays at 19.5 °C.
- **Perennial vegetables**: rhubarb's warm-up is now 8.2 °C and globe artichoke's is 12 °C.
- **Montevideo fixture**: the MGAP / Mercado Modelo chart lists tomato, bell pepper, eggplant, cucumber, basil and sweet corn in all 12 months, which reflects wholesale supply. Those 6 rows were dropped, following the field-season principle already applied to Oslo's greenhouse rows and to San José (see `calibration/fixtures/README.md`). No other months changed, and 34 slugs still match. Montevideo's score went 0.792 → 0.846, and the overall pair count went 534 → 528.

Knife edges (thresholds within a few tenths of a degree of a fixture city's monthly mean, which may flip with a new climate data version):
- **Cool-season upper bound 25 °C:** New York's July is 24.76 °C, 0.24 under the bound, so New York shows cool-season crops all summer. Barcelona's August is 24.6 °C.
- **Okra and watermelon lower bound 18 °C:** Toronto's June mean is just under 18 °C (18.0 when rounded). If it tips over, Toronto regains watermelon (listed Jul–Sep) and gains okra (not listed).
- **Rhubarb:** moved from 8 to 8.2 °C, because Hobart's coldest month (7.98 °C) sat 0.02 under 8. The score is unchanged either way. 8.2 keeps Hobart's spring season (Sep–Nov) with a 0.2 °C margin, and Minneapolis's April (8.3 °C) still starts its season in May.
- **Fig 17.65 °C:** margins of about 0.15 °C to Barcelona's May and San Francisco's June.

Parameter sweeps against the fixtures sometimes found higher scores with implausible parameters, such as frost-hardy sweet potato or potato. They were not applied. Tokyo (0.478) and Oslo (0.535) are the weakest fixtures.
- **Tokyo:** the guide lists greenhouse strawberries from January to May (0.12) and short, split seasons for onion, broccoli, carrot and potato (0.08–0.33).
- **Oslo:** only three months are above winter squash's 13 °C lower bound, too few for its 3 months to harvest (0.00 against the guide's Sep–Nov), and pear and kale also miss (0.12 and 0.20).

## Known limitations

These come from the engine rules or the data, not from tuning, and were checked on the live cells of the 15 fixture cities and 56 more:

- **Citrus and avocado in Paris and Toulouse.** Their coldest-month mean minimum is about 2 °C, above the 0 °C winter-kill threshold, and their summers pass the 19.5 °C warm-up. So lemon, orange, mandarin, avocado and pomegranate show there, although none of them is grown commercially. The threshold stays at 0 °C (Ruling G, spec §12.3). Follow-up spec item: a per-ingredient winter-minimum field, so citrus can require a milder winter than kiwifruit or fig.
- **Peach, apricot and grape in Oslo and Stockholm** (between August and November). These fruit need no chill months, and a 12 °C warm-up is reached even in short summers. The Oslo fixture doesn't list them, so the score can't see it.
- **No temperate fruit in Sydney, Cape Town or Los Angeles.** No month there is cold enough for the mean to cross the 11–12 °C warm-up, so apple, pear, stone fruit and berries never start a season, although all three regions grow them. It is the same warm-up limit that affected mango in the tropics.
- **Seafood coverage gaps.** Several coastal cities are within reach of marine ecoregions that no catalog species uses, so they show no seafood at all: Montevideo, Santiago, Hong Kong, Singapore, Shanghai, Buenos Aires, São Paulo, Lima, Mumbai, Karachi, Bangkok, Jakarta, Manila, Seoul, Osaka, Los Angeles, Perth, Brisbane, Istanbul, Athens, Dubai, Lagos and Stockholm. In London and Oslo, `oyster` and `scallop` have no North Sea region (ecoregion 25), although both are fished or farmed there. Follow-up: add regions from regional fisheries guides.
- **Spring/autumn symmetry for wild mushrooms.** The fruiting rule uses temperature and rain only, so `morel`, `chanterelle` and `porcini` show in spring as well as autumn wherever both seasons pass (for example Paris morel Mar–Apr and Oct–Nov, porcini Apr–May and Oct–Nov). The same symmetry affects the [0, 15] °C window for brussels sprouts and parsnip.
- **Seafood is not scored**, because no fixture lists seafood.

## How this was measured

- Staging was the local dev Postgres (`deploy/compose.dev.yaml`) with the reference data above. TerraClimate v4 was ingested on 2026-09-27: validate passed all 12 reference-city spot checks, with 365,300 land cells.
- Imports took the same path as `/admin/import` (`changeset.ImportChangeset`, then `Service.Preview`, then `Service.Apply` with actor `owner (baseline)`), with no validation problems. After each apply, the drift check (`calibrun.Drift.RunOnce`) recorded a `calibration_run` row:

  | Row | Score | Pairs | What was applied |
  |---|---|---|---|
  | 1 | 0.6727 | 534 | first pass: all 101 items added |
  | 2 | 0.6672 | 534 | false-positive fixes: 7 items changed |
  | 3 | 0.6707 | 528 | final: mango and fig changed, with the new Montevideo fixture; data version `4bc01f4a2a770d63` |

- The committed `content/catalog/launch.json` matches the live catalog item for item.
