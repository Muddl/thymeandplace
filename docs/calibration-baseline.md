# Calibration baseline

- Date: 2026-09-27
- Data version: `2415700dbc9aa534` (from `calibration_run`: catalog version 910 with live datasets places v1, ocean v3, climate v4)
- Score: 0.673 over 534 compared (fixture, ingredient) pairs
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

## Initial score and tuning

The launch catalog as committed before this task (`38568cf`) scored **0.479** over the same 534 pairs on the same reference data. After tuning, it scores **0.673**. Per fixture:

| Fixture | Before | After |
|---|---|---|
| auckland-nz | 0.488 | 0.712 |
| barcelona-es | 0.555 | 0.687 |
| berlin-de | 0.655 | 0.731 |
| hobart-au | 0.442 | 0.578 |
| hong-kong-hk | 0.522 | 0.751 |
| honolulu-us | 0.482 | 0.587 |
| london-gb | 0.503 | 0.618 |
| minneapolis-us | 0.307 | 0.638 |
| montevideo-uy | 0.503 | 0.790 |
| new-york-us | 0.427 | 0.713 |
| oslo-no | 0.408 | 0.535 |
| san-francisco-us | 0.446 | 0.653 |
| santiago-cl | 0.557 | 0.688 |
| tokyo-jp | 0.321 | 0.500 |
| toronto-ca | 0.398 | 0.713 |

Before tuning, 11 ingredients scored below 0.3. After tuning, only the two in the table above do. Parameters of 48 ingredients were changed, one ingredient at a time, and a change was kept only when its own score rose, the other cities stayed about the same, and `go test ./content/` still passed. Only catalog parameters changed, and the fixtures were not edited. The main changes:

- **Warm-season annuals** (bell pepper, chili, eggplant, okra, sweet potato, watermelon): lower bounds of 18–20 °C kept them out of most temperate cities. They now use 13–17 °C with 2–3 months to harvest. For example, bell pepper went from 0.196 to 0.686 and chili pepper from 0.067 to 0.681. Tomato, cucumber, sweet corn and winter squash got smaller adjustments of the same kind.
- **Cool-season annuals** (broccoli, cauliflower, cabbage, kale, leek, carrot, beetroot, spinach, turnip, celery, bok choy, onion, spring onion, chard, lettuce, radish, potato): an upper bound of 18–22 °C removed the whole summer in New York, Toronto and Minneapolis, where the guides list these crops. The bound is now 24–26 °C. For example, broccoli went from 0.415 to 0.800 and spinach from 0.380 to 0.820. This change also drove most of the gains in Minneapolis, New York and Toronto.
- **Winter-picked crops** (brussels sprouts, parsnip): the picking window is now [0, 15] °C, and parsnip is sold from storage for 3 months.
- **Temperate fruit and berries** (apple, pear, plum, cherry, apricot, peach, grape, strawberry, raspberry, blueberry, blackberry): the warm-up is now 12 °C (11 for cherry) with shorter bloom-to-harvest offsets and longer spans. Fig keeps its 15 °C warm-up and gets a longer span. The 0.34–0.54 range went to 0.56–0.72.
- **Citrus and avocado**: harvest spans went up to 6–8 months. The warm-up stays at 19.5 °C, so citrus still doesn't show in cool-summer climates.
- **Perennial vegetables**: rhubarb's warm-up is now 8 °C and globe artichoke's is 12 °C.

Parameter sweeps against the fixtures sometimes found higher scores with implausible parameters, such as frost-hardy sweet potato or potato, or a fig that ripens four months after an 11 °C warm-up, which would put figs in Oslo from October to February. Those were not applied, so the remaining gap is honest engine and fixture limits rather than overfitting. Tokyo (0.500) and Oslo (0.535) are the weakest fixtures. Tokyo's guide lists greenhouse strawberries from January to May (0.12) and short, split seasons for onion, broccoli, carrot and potato (0.08–0.33), which temperature-only rules don't reproduce. In Oslo, the short summer (warmest month 16.7 °C) gives only three months above winter squash's 13 °C lower bound, too few for its 3 months to harvest (0.00 against the guide's Sep–Nov), and pear and kale also miss (0.12 and 0.20).

## Other checks

- Every land ingredient is in season at 2 or more of the 15 fixture locations.
- Seafood is not scored, because no fixture lists seafood. These are in season at none or only one of the fixture coasts, which is expected for regional fisheries: american-lobster, barramundi, hilsa, oyster, scallop, snoek, sockeye-salmon, spot-prawn, stone-crab (none) and anchovy, blue-crab, dungeness-crab, hoki, mahi-mahi, pacific-saury, sardine, southern-rock-lobster (one). Follow-up: `oyster` and `scallop` have no North Sea region (ecoregion 25), so London and Oslo never show them, although both are fished or farmed in the North Sea. Add North Sea regions from a fisheries seasonal guide.

## How this was measured

- Staging was the local dev Postgres (`deploy/compose.dev.yaml`) with real reference data: GeoNames places (v1), MEOW ecoregions clipped to the Natural Earth land mask (v3), and TerraClimate 1991–2020 climatology (v4, ingested 2026-09-27; validate passed all 12 reference-city spot checks, 365,300 land cells).
- The import took the same path as `/admin/import` (`changeset.ImportChangeset`, then `Service.Preview`, then `Service.Apply` with actor `owner (baseline)`). All 101 items showed as Added, and there were no validation problems. The drift check (`calibrun.Drift.RunOnce`) then recorded `calibration_run` row 1: score 0.6727, 534 compared, data version `2415700dbc9aa534`.
- The data version hashes the catalog version and the live dataset row IDs, so production will have a different string. Compare the score instead. With the same reference data, production should score within 0.02 of 0.673.
