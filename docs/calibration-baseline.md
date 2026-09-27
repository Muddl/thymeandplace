# Calibration baseline

- Date: 2026-09-27
- Data version: `35afd8914e7f7c9c` (from `calibration_run` row 2: catalog version 967 with live datasets places v1, ocean v3, climate v4)
- Score: 0.667 over 534 compared (fixture, ingredient) pairs
- Fixtures: 15 (4 southern, 2 tropical, 3 high-latitude)

## Ingredients below 0.3 and why
| Ingredient | Score | Reason / follow-up |
|---|---|---|
| mango | 0.000 (1 fixture: Honolulu) | Engine limit (spec §12.3, see `content/AUTHORING.md`): a `perennial_fruit` needs a month when the mean crosses `warmup_temp_c`. Honolulu's coolest month is 21.8 °C, so it never crosses the 20 °C hot-summer threshold and mango shows nowhere in the humid tropics. A 22 °C warm-up would score 0.56 to 0.89, but only because Honolulu's February (21.8) and March (22.2) fall either side of it. That fits one fixture on a knife-edge, so it was not applied. Follow-up: a tropical-fruit rule (or `cultivated_year_round` with a seasonal peak) in the engine. |
| pomegranate | 0.000 (1 fixture: San Francisco) | The guide lists Sep–Dec from California supply (Central Valley), while the fixture cell is cool-summer San Francisco (warmest month 18.9 °C), which never reaches the 19.5 °C hot-summer threshold. A threshold low enough for San Francisco would also show pomegranates in London (warmest month 18.1 °C). Kept as is. Follow-up: a fixture point for a hot-summer California location, or regional supply adjustments. |

Close to the line (0.30–0.35), all with the same kinds of limits:

| Ingredient | Score | Note |
|---|---|---|
| globe-artichoke | 0.306 | Mediterranean artichokes crop from winter into spring (Barcelona Jan–May, Santiago May–Oct) after a summer rest. The warm-up rule only models a spring cutting season. Autumn crops in San Francisco are also missed. |
| avocado | 0.317 | San Francisco (all year) and Hobart (Jan–Aug) list avocados that stores bring in from other regions, and the engine only models local growing. Honolulu never cools below the 19.5 °C warm-up (the same tropics limit as mango). Where avocados are grown and the rule applies, the match is good (Auckland 1.00, Santiago 0.58). |
| morel | 0.333 | One fixture (London, April only). The temperature-only rule also fruits morels in autumn. A narrower window would fit London but would drop spring in New York. |
| brussels-sprouts | 0.341 | The [0, 15] °C picking window can't tell spring from autumn, so continental cities show sprouts in April as well as in autumn. A long-season model fits better (0.60) but is year-round in maritime climates and fails the content gate. |

## Method and blind spots

The score only compares (fixture, ingredient) pairs that a fixture lists. If an ingredient shows in a city whose guide does not list it, nothing is scored, so that false positive is invisible to calibration. For example, tuning first set heat-crop lower bounds to 13–17 °C. That raised the score but showed okra and watermelon in Berlin, Toronto and London, and peppers and eggplant in Oslo, where no guide lists them. The negative content gates in `content/content_test.go` cover this blind spot with synthetic climates: no tender fruit in the continental (New York-like) climate, no temperate fruit in the tropics, no year-round cool-season crops in maritime climates, and no heat crops or figs in the new Oslo-like cool-summer climate (`climatetest.CoolSummer`, warmest month mean 16.5 °C).

Heat crops and figs showing where the fixture does not list them, from the real fixture cells:

| City | Initial (`38568cf`) | First tuning (`0fe6abe`) | Final |
|---|---|---|---|
| auckland-nz | fig | okra, fig | okra, fig |
| barcelona-es | okra, chili-pepper | okra, chili-pepper | okra, chili-pepper |
| berlin-de | fig | okra, watermelon, eggplant, bell-pepper, chili-pepper, fig | eggplant, bell-pepper, chili-pepper (August only), fig |
| hobart-au | fig | eggplant, chili-pepper, fig | eggplant, chili-pepper |
| hong-kong-hk | sweet-potato, bell-pepper, chili-pepper | same | same, plus fig |
| honolulu-us | okra, chili-pepper | same | same |
| london-gb | fig | fig | fig |
| minneapolis-us | okra, fig | okra, watermelon, sweet-potato, chili-pepper, fig | same |
| montevideo-uy | okra, chili-pepper | same | same |
| new-york-us | okra, chili-pepper, fig | okra, sweet-potato, chili-pepper, fig | same |
| oslo-no | fig | eggplant, bell-pepper, chili-pepper, fig | none |
| san-francisco-us | none | watermelon | watermelon |
| santiago-cl | eggplant, chili-pepper | okra, sweet-potato, eggplant, chili-pepper | same |
| tokyo-jp | okra, watermelon, chili-pepper, fig | same | same |
| toronto-ca | fig | okra, chili-pepper, fig | chili-pepper, fig |

Most of the remaining entries are plausible, because warm-summer cities (Minneapolis, New York, Tokyo, Santiago) grow these crops even though their guides don't list them. Berlin's peppers and eggplant show in August only. The fig in Hong Kong (April–July) is a side effect of the 17.65 °C fig warm-up, because Hong Kong's March (18.7 °C) crosses it.

## Initial score and tuning

The launch catalog as committed before this task (`38568cf`) scored **0.479** over the same 534 pairs on the same reference data. After the first tuning pass (`0fe6abe`), it scored **0.673**. After the false-positive fixes, it scores **0.667**. Per fixture, initial → final (mean change over the pairs each fixture lists, and the pairs that improved, worsened or stayed the same):

| Fixture | Before | First tuning | Final | Pairs up / down / same | Largest drops (initial → final) |
|---|---|---|---|---|---|
| auckland-nz | 0.488 | 0.712 | 0.712 | 32 / 1 / 17 | grape 0.67→0.25 |
| barcelona-es | 0.555 | 0.687 | 0.667 | 19 / 12 / 11 | parsnip 0.67→0.44, radish 0.78→0.58, plum 0.50→0.33, spring-onion 0.50→0.33, apricot 0.67→0.50, grape 0.17→0.00 |
| berlin-de | 0.655 | 0.731 | 0.731 | 22 / 5 / 20 | plum 1.00→0.50, blueberry 1.00→0.67, peach 1.00→0.75, apricot 0.67→0.50, sweet-corn 0.67→0.50 |
| hobart-au | 0.442 | 0.578 | 0.578 | 10 / 1 / 6 | brussels-sprouts 0.67→0.57 |
| hong-kong-hk | 0.522 | 0.751 | 0.750 | 11 / 0 / 5 | none |
| honolulu-us | 0.482 | 0.587 | 0.587 | 6 / 0 / 16 | none |
| london-gb | 0.503 | 0.618 | 0.618 | 20 / 3 / 20 | broccoli 0.83→0.71, apple 0.62→0.56, celery 0.40→0.33 |
| minneapolis-us | 0.307 | 0.638 | 0.638 | 19 / 2 / 6 | winter-squash 0.67→0.57, potato 0.56→0.55 |
| montevideo-uy | 0.503 | 0.790 | 0.792 | 28 / 1 / 11 | pea 1.00→0.44 |
| new-york-us | 0.427 | 0.713 | 0.713 | 30 / 4 / 6 | blackberry 1.00→0.50, sweet-corn 0.75→0.40, peach 1.00→0.75, strawberry 0.67→0.50 |
| oslo-no | 0.408 | 0.535 | 0.535 | 16 / 5 / 10 | strawberry 0.75→0.60, blueberry 0.33→0.25, apple 0.70→0.64, turnip 0.62→0.57, pear 0.14→0.12 |
| san-francisco-us | 0.446 | 0.653 | 0.638 | 23 / 1 / 30 | winter-squash 0.30→0.27 |
| santiago-cl | 0.557 | 0.688 | 0.678 | 14 / 3 / 13 | mandarin 0.60→0.50, apple 0.80→0.73, celery 0.22→0.20 |
| tokyo-jp | 0.321 | 0.500 | 0.478 | 11 / 1 / 5 | blueberry 0.67→0.50 |
| toronto-ca | 0.398 | 0.713 | 0.698 | 29 / 1 / 12 | brussels-sprouts 0.25→0.17 |

Before tuning, 11 ingredients scored below 0.3. After tuning, only the two in the table above do. Parameters of 48 ingredients were changed, one ingredient at a time. A change was kept when its own score rose and `go test ./content/` still passed. Other cities could still lose on individual pairs, as the table shows. Only catalog parameters changed, and the fixtures were not edited. The main changes:

- **Warm-season annuals** (bell pepper, chili, eggplant, okra, sweet potato, watermelon): lower bounds of 18–20 °C kept them out of most temperate cities. The first pass used 13–17 °C. The final values are 15 °C for peppers and eggplant, 16 °C for sweet potato and 18 °C for okra and watermelon, with 2–3 months to harvest. The 18 °C bound was chosen to keep these crops out of cool-summer cities, and it cost 0.006 overall. Scores went from 0.196 to 0.636 (bell pepper), 0.067 to 0.544 (chili), 0.227 to 0.662 (eggplant), 0.375 to 0.521 (okra) and 0.189 to 0.527 (watermelon). Watermelon now misses Toronto (the guide lists Jul–Sep), because only two months are above 18 °C there. Tomato, cucumber, sweet corn and winter squash got smaller adjustments of the same kind.
- **Cool-season annuals** (broccoli, cauliflower, cabbage, kale, leek, carrot, beetroot, spinach, turnip, celery, bok choy, onion, spring onion, chard, lettuce, radish, potato): an upper bound of 18–22 °C removed the whole summer in New York, Toronto and Minneapolis, where the guides list these crops. The bound is now 24–26 °C, which drove most of the gains in Minneapolis, New York and Toronto (for example, broccoli 0.415 → 0.800 and spinach 0.380 → 0.820). The trade-off is Barcelona. Its warmest month is August at 24.6 °C, just under 25, so broccoli, cabbage, cauliflower, turnip, spring onion and radish are now year-round there, while the guide has a summer gap. Broccoli went 0.57→0.50, cabbage 0.62→0.50, spring onion 0.50→0.33 and radish 0.78→0.58. Leek, chard, lettuce, beetroot and celery improved in Barcelona, so its fixture score still rose overall (0.555 → 0.667).
- **Winter-picked crops** (brussels sprouts, parsnip): the picking window is now [0, 15] °C, and parsnip is sold from storage for 3 months.
- **Temperate fruit and berries** (apple, pear, plum, cherry, apricot, peach, grape, strawberry, raspberry, blueberry, blackberry): the warm-up is now 12 °C (11 for cherry), with shorter bloom-to-harvest offsets and longer spans. Their scores went from 0.34–0.54 to 0.56–0.72. Known misses:
  - Grape dropped in Barcelona (0.17 → 0.00, engine Jun–Aug against a guide Sep–Dec) and Auckland (0.67 → 0.25). A later offset would push the harvest past the guides elsewhere.
  - Apple and pear ripen late in Oslo. Apple is harvested Oct–Jan, then sold from storage until June, against a guide with Aug–Dec and Jan–Apr (0.64). Pear is harvested Oct–Dec, against a guide with Aug–Oct (0.12). Oslo warms past 12 °C only in June, and a single offset cannot suit both Oslo and the milder cities.
- **Fig**: the warm-up went from 15 to 17.65 °C, with a 1-month offset and a 4-month span (0.338 → 0.700). The first pass kept the 15 °C warm-up with a longer span, which scored the same 0.700 but showed figs in Oslo Sep–Dec and London Sep–Dec. Now Oslo has none, London has Aug–Nov and Barcelona Jul–Oct, which matches its guide exactly. Other options were checked. A 17 °C warm-up with a 2-month offset scored 0.633 and ran London into December. A 17 °C warm-up with a 1-month offset scored 0.542, with Hobart on a 0.1 °C edge and Barcelona a month early. 17.65 sits in the middle of the 17.5–17.8 plateau, between Barcelona's May (17.5 °C) and San Francisco's June (17.8 °C).
- **Citrus and avocado**: harvest spans went up to 6–8 months. The warm-up stays at 19.5 °C, so citrus still doesn't show in cool-summer climates.
- **Perennial vegetables**: rhubarb's warm-up is now 8.2 °C and globe artichoke's is 12 °C.

Knife edges (thresholds within a few tenths of a degree of a fixture city's monthly mean, which may flip with the next climate data version):
- **Cool-season upper bound 25 °C:** New York's July is 24.76 °C, 0.24 under the bound, so New York shows cool-season crops through the summer. Barcelona's August is 24.6 °C.
- **Okra and watermelon lower bound 18 °C:** Toronto's June mean is just under 18 °C (shown as 18.0). If it tips over, Toronto regains watermelon (listed Jul–Sep) and gains okra (not listed).
- **Rhubarb:** moved from 8 to 8.2 °C, because Hobart's coldest month (7.98 °C) sat 0.02 under 8. The score is unchanged either way. 8.2 keeps Hobart's spring season (Sep–Nov) with a 0.2 °C margin, and Minneapolis's April (8.3 °C) still starts its season in May.
- **Fig 17.65 °C:** margins of about 0.15 °C to Barcelona's May and San Francisco's June.

Parameter sweeps against the fixtures sometimes found higher scores with implausible parameters, such as frost-hardy sweet potato or potato, or a fig that ripens four months after an 11 °C warm-up, which would put figs in Oslo from October to February. Those were not applied, so the remaining gap comes from engine and fixture limits, not overfitting. Tokyo (0.478) and Oslo (0.535) are the weakest fixtures.
- **Tokyo:** the guide lists greenhouse strawberries from January to May (0.12) and short, split seasons for onion, broccoli, carrot and potato (0.08–0.33), which temperature-only rules don't reproduce.
- **Oslo:** the short summer (warmest month 16.7 °C) gives only three months above winter squash's 13 °C lower bound. That is too few for its 3 months to harvest (0.00 against the guide's Sep–Nov), and pear and kale also miss (0.12 and 0.20).

## Other checks

- Every land ingredient is in season at 2 or more of the 15 fixture locations.
- Seafood is not scored, because no fixture lists seafood. Many seafood items are in season at none or only one of the fixture coasts, which is expected for regional fisheries:
  - None: american-lobster, barramundi, hilsa, oyster, scallop, snoek, sockeye-salmon, spot-prawn, stone-crab.
  - One: anchovy, blue-crab, dungeness-crab, hoki, mahi-mahi, pacific-saury, sardine, southern-rock-lobster.

  Follow-up: `oyster` and `scallop` have no North Sea region (ecoregion 25), so London and Oslo never show them, although both are fished or farmed in the North Sea. Add North Sea regions from a fisheries seasonal guide.

## How this was measured

- Staging was the local dev Postgres (`deploy/compose.dev.yaml`) with real reference data: GeoNames places (v1), MEOW ecoregions clipped to the Natural Earth land mask (v3), and TerraClimate 1991–2020 climatology (v4, ingested 2026-09-27). Validate passed all 12 reference-city spot checks, with 365,300 land cells.
- The import took the same path as `/admin/import`: `changeset.ImportChangeset`, then `Service.Preview`, then `Service.Apply` with actor `owner (baseline)`.
  - The first apply added all 101 items with no validation problems, and the drift check recorded `calibration_run` row 1 (0.6727).
  - The final apply changed 7 items (preview delta −0.0055, no override needed).
  - The drift check (`calibrun.Drift.RunOnce`) then recorded row 2: score 0.6672, 534 compared, data version `35afd8914e7f7c9c`.
  - The committed `content/catalog/launch.json` matches the live catalog item for item.
- The data version hashes the catalog version and the live dataset row IDs, so production will have a different string. Compare the score instead. With the same reference data, production should score within 0.02 of 0.667.
