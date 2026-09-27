# Authoring the catalog

The engine rules are in spec §12.3. These are starting points; the preview's
calibration score is the judge. Temperatures are **monthly mean** temperatures
(the average of the monthly mean minimum and mean maximum), not daytime highs.

## Parameter guidance by kind

| Kind | Field | Typical range | Notes |
|---|---|---|---|
| annual, warm-season (tomato, pepper, squash, basil, beans, corn) | frost | `tender` | |
| | grow_temp_c | [13–18, 30–35] | lower bound is the key lever; 13–14 lets cool maritime summers (London, Hobart) harvest outdoor tomatoes, squash and beans; 15 for peppers and eggplant and 16–18 for heat lovers (sweet potato 16, okra and watermelon 18) keeps them out of short cool summers (Oslo, Berlin), which the content gate checks |
| | months_to_harvest | 2–3 | from transplant to first harvest; 4 or more starts the harvest later than the guides |
| annual, cool-season (kale, cabbage, peas, lettuce, spinach, leeks) | frost | `hardy` | |
| | grow_temp_c | [4–6, 24–26] | an upper bound near 25 °C keeps hot continental summers (New York, Toronto, Minneapolis) in season, as the guides list them; 18–22 dropped those summers entirely |
| | months_to_harvest | 1–4 | 1 for quick leaf crops (spinach), 2 for most brassicas and roots, 3–4 for cauliflower, celery, leek, onion |
| cool-season, summer-picked (pea, broad bean, lettuce, radish) | grow_temp_c | [9–11, 20–25] | a lower bound above the mildest winters (Hobart's coldest month mean is about 8 °C) keeps them from being year-round in maritime climates |
| cool-season, winter-picked (Brussels sprouts, parsnip) | grow_temp_c | [0, 15] | the window is the picking season, so months_to_harvest is 1; the low upper bound keeps summer out. A long-season model ([5, 25], 5 months) scores higher but is year-round in maritime climates and fails the content gate |
| root/storage crops (potato, onion, carrot, squash) | storage_months | 3–8 | only if typically sold from storage |
| perennial_fruit, temperate (apple, pear, plum, cherry) | chill_months_min | 0–1 | months with mean < 7 °C; monthly means undercount chill in mild maritime winters (Hobart has none), so leave this low |
| | warmup_temp_c | 11–12 | spring warm-up; a climate with no month below it (the tropics, Sydney, Cape Town) never shows the fruit |
| | months_bloom_to_harvest | 1 (strawberry, raspberry) – 4 (apple, pear) | counted from the warm-up month, not from real bloom |
| | harvest_span_months | 2–5 | |
| perennial_fruit, subtropical (citrus, avocado) | frost | `tender` | frost months drop out of the harvest, and the tree winter-kills (not grown at all) where the coldest month's mean minimum is below 0 °C |
| | chill_months_min | 0 | |
| | warmup_temp_c | 19.5–20 | a hot-summer threshold; cool-summer climates never reach it |
| | harvest_span_months | 6–8 | citrus and avocado hang on the tree for months; spans of 3–4 scored far below the guides (mandarin 0.24 against 0.47 with 7) |
| cultivated_year_round | – | – | bananas, mangoes, button mushrooms, greenhouse staples sold all year |
| wild_mushroom | fruiting_temp_c | [8–12, 16–20] | |
| | min_precip_mm | 40–45 | monthly; 60 or more rules out drier maritime climates such as London |
| seafood | regions | MEOW ecoregions | months from fisheries guidance, peak where clear |

Winter-kill (ruling G, spec §12.3): a `perennial_fruit` with
`frost: tender` is not grown locally where the coldest month's mean minimum
temperature is below 0 °C. Exactly 0 °C still counts as survivable. Use
`tender` for fruit that is not grown commercially where winters freeze:
citrus, avocado, pomegranate, fig, kiwifruit and the perennial
`globe-artichoke`. Fruit that is grown in hard-winter regions (apple, pear,
plum, cherry, grape, berries) stays `hardy`, even when it is picked before
the first frost.

The 0 °C test only catches freezing winters. A mild winter that is still too
cool for a crop (Paris and Toulouse, coldest-month mean minimum about 2 °C)
passes, so citrus and avocado show there. A per-ingredient winter-minimum
field is a follow-up spec item; see `docs/calibration-baseline.md`.

Known limitation (spec §12.3): a `perennial_fruit` needs one clear month when
the mean crosses `warmup_temp_c`. In climates that never cross it (the humid
tropics) it shows nowhere. Tropical fruit sold all year (banana, pineapple,
papaya, mango) should be `cultivated_year_round`.

## Worked examples (copy the shape; tune the numbers)

```json
{ "slug": "tomato", "name": "Tomato", "category": "vegetable", "kind": "annual",
  "frost": "tender", "grow_temp_c": [13, 30], "months_to_harvest": 2, "storage_months": 0,
  "tips": { "choose": "Heavy for its size, glossy, fragrant at the stem.",
            "store": "Stem-side down at room temperature; refrigerate only when fully ripe." },
  "pairings": ["basil", "garlic"], "note": "" }

{ "slug": "kale", "name": "Kale", "category": "vegetable", "kind": "annual",
  "frost": "hardy", "grow_temp_c": [4, 25], "months_to_harvest": 2, "storage_months": 0,
  "tips": { "choose": "Crisp, deep-coloured leaves without yellowing.", "store": "Wrapped loosely in the fridge; use within 5 days." },
  "pairings": ["garlic", "lemon"], "note": "Sweeter after a frost." }

{ "slug": "apple", "name": "Apple", "category": "fruit", "kind": "perennial_fruit",
  "frost": "hardy", "chill_months_min": 0, "warmup_temp_c": 12, "months_bloom_to_harvest": 4,
  "harvest_span_months": 4, "storage_months": 5,
  "tips": { "choose": "Firm, no soft spots, smells like apple.", "store": "Cold and humid, away from leafy greens." },
  "pairings": ["pork", "cinnamon"], "note": "" }

{ "slug": "banana", "name": "Banana", "category": "fruit", "kind": "cultivated_year_round",
  "tips": { "choose": "Yellow with a few brown flecks for eating now.", "store": "Room temperature; refrigerate once ripe." },
  "pairings": [], "note": "" }

{ "slug": "chanterelle", "name": "Chanterelle", "category": "mushroom", "kind": "wild_mushroom",
  "fruiting_temp_c": [12, 20], "min_precip_mm": 40, "storage_months": 0,
  "tips": { "choose": "Firm, dry and springy, with clean edges; pass on any that are soggy, slimy or bruised.",
            "store": "Paper bag in the fridge; use within 3 days." },
  "pairings": ["butter", "thyme"], "note": "" }
```

## Finding ecoregion IDs for seafood

After the ocean dataset is live (Plan 3), list the ecoregions near the ports
where a species is landed:

```sql
SELECT ecoregion_id, name FROM ocean_ecoregion
WHERE ST_DWithin(geom, ST_SetSRID(ST_MakePoint(<lng>, <lat>), 4326)::geography, 100000)
ORDER BY name;
```

Use the months from a fisheries or sustainable-seafood seasonal guide for that
region and record the source in your working notes. `peak_months` is optional.

## Pairings

Pairings must reference slugs in the catalog (or the same import file). Keep
them to real kitchen pairings, three to six per ingredient. They are
directional: add both directions when both make sense.

## Slug conventions

Slugs are the plain, common English name, singular (`pea`, `strawberry`),
except where the name is normally plural (`brussels-sprouts`, `bean-sprouts`,
`chives`). Calibration fixtures key their months by slug, so when a published
guide uses another name, map it to the catalog slug below rather than adding
a near-duplicate ingredient.

| Slug | Also called (map these to the slug) |
|---|---|
| `zucchini` | courgette |
| `eggplant` | aubergine, brinjal |
| `bell-pepper` | capsicum, sweet pepper, peppers |
| `chili-pepper` | chilli, chile, hot pepper |
| `sweet-corn` | sweetcorn, corn on the cob |
| `green-bean` | French bean, string bean, runner bean |
| `broad-bean` | fava bean |
| `pea` | garden pea, sugar snap, mangetout, snow pea |
| `winter-squash` | pumpkin, butternut, squash (winter) |
| `beetroot` | beet, beets |
| `chard` | Swiss chard, silverbeet, rainbow chard |
| `bok-choy` | pak choi, pak choy |
| `spring-onion` | scallion, green onion |
| `globe-artichoke` | artichoke |
| `coriander` | cilantro (the leaves) |
| `mandarin` | clementine, satsuma, tangerine |
| `kiwifruit` | kiwi |
| `peach` | nectarine |
| `button-mushroom` | white, chestnut, cremini or portobello mushroom |
| `porcini` | cep, penny bun, king bolete |
| `atlantic-cod` | cod, skrei |
| `sardine` | pilchard |
| `sockeye-salmon` | red salmon |
| `southern-rock-lobster` | crayfish (Australia, New Zealand), rock lobster |
| `mahi-mahi` | dolphinfish, dorado |
| `pacific-saury` | sanma |

## Notes on the launch catalog

- Perennial vegetables (`asparagus`, `rhubarb`, `globe-artichoke`) use the
  `perennial_fruit` kind: the warm-up month starts the cutting season.
- Subtropical fruit that needs a hot summer (citrus, `avocado`,
  `pomegranate`) uses a high `warmup_temp_c` (19.5). Cool-summer climates
  (London, Oslo, San Francisco) never reach it; the bloom-to-harvest offset
  is counted from that later month. They are also `tender`, so the
  winter-kill rule keeps them out of hard-winter climates (New York,
  Toronto, Chicago, Beijing). `globe-artichoke` is `tender` for the same
  reason. Climates with a warm summer and a mild but cool winter (Paris,
  Toulouse) pass both tests, so citrus and avocado show there even though
  they are not grown commercially: a known limitation.
- `mango` is `cultivated_year_round`: the warm-up rule never showed it in
  the humid tropics (Mumbai, Bangkok, Singapore) but did in Tokyo, Madrid,
  Rome and Barcelona. It is sold all year from tropical growers.
- `grape` is `hardy` (the vine survives hard winters), with a warm-up of
  12 °C. With no chill requirement it also shows in Oslo and Stockholm
  (September–November), as do `peach` and `apricot` (from August), although none of them
  is grown commercially there. The Oslo fixture does not list them, so the
  calibration score cannot see this: a known limitation.
  `kiwifruit` is `tender` (commercial kiwifruit is not grown in hard-winter
  zones), so winter-kill keeps it out of New York, Chicago, Toronto, Oslo,
  Stockholm and Beijing. Its harvest window comes from a 13 °C warm-up.
- Temperate fruit, berries, `asparagus` and `rhubarb` need no chill months:
  their `warmup_temp_c` (8–12 °C; 11–12 for fruit and berries, 8.2 for
  `rhubarb`, the earliest spring crop) keeps them out of climates whose coldest
  month never drops below it. `fig` uses a hot-summer warm-up of 17.65 °C
  with a 1-month offset and a 4-month span: Oslo (warmest month 16.7 °C)
  never reaches it, London gets figs August–November, Barcelona July–October.
  The value sits in the middle of a narrow plateau: below about 17.5
  Barcelona's May (17.5 °C) starts the season a month early, above about 17.8
  San Francisco's June (17.8 °C) starts it a month late. It also opens a
  spring warm-up in Hong Kong (March 18.7 °C), so figs show there April–July.
  `fig` is `tender`, so winter-kill keeps it out of hard-winter cities
  (Minneapolis, New York, Toronto, Chicago, Berlin, Moscow, Seoul,
  Beijing); mild-winter London, Paris and Seattle keep their late-summer
  figs.
- Perennial herbs (thyme, rosemary, sage, oregano, mint) are modelled as
  `annual`: they are picked whenever the plant is growing.
- The rules use temperature only, so spring and autumn with the same mean
  look alike. The [0, 15] window for `brussels-sprouts` and `parsnip` also
  opens in spring: New York shows sprouts from November to April, and
  Toronto, Minneapolis and Oslo show them in spring as well as in late
  autumn. `morel` shows in spring and
  again in autumn. `chanterelle` and `porcini` start in late spring as well
  as autumn (London: chanterelle May–Oct, porcini May–Jun and Sep–Nov).
  Tune them against the calibration fixtures.

## After editing in the admin

The admin preview scores only the pairs a fixture lists, so it cannot see an
ingredient appearing in a city whose guide doesn't list it (figs in Oslo,
okra in Berlin). The negative content gates in `content/content_test.go`
check that. After any admin edit or import:

1. Export the catalog from `/admin/export` and save it over
   `content/catalog/launch.json`, so the repo matches what's live.
2. Run `go test ./content/ -count=1`. If a gate fails, fix the parameters
   in the admin (or the file, then import it again) until it passes.
3. Commit the file.

## Calibration tuning (2026-09-27)

The numbers above were tuned against the 15 calibration fixtures on real
TerraClimate normals (score 0.480 before, 0.671 after over 528 pairs; see
`docs/calibration-baseline.md`). Only catalog parameters changed; the
fixtures are ground truth and were not edited. What moved, and why:

- Warm-season annuals: lower bounds of 18–20 °C left peppers, eggplant,
  chili, okra, sweet potato and watermelon out of most temperate cities.
  13–18 °C with 2–3 months to harvest matches the guides. The lower bounds
  were first set to 13–17 °C, which showed okra and watermelon in Berlin,
  Toronto and London and peppers and eggplant in Oslo, cities whose guides do
  not list them (so the score could not see it). They were raised to 15 °C
  (peppers, eggplant) and 18 °C (okra, watermelon), costing 0.006.
- Cool-season annuals: an upper bound of 18–22 °C removed the whole summer
  in New York, Toronto and Minneapolis, where the guides list broccoli,
  cabbage, carrots and beets from June or July. The bound is now 24–26 °C.
- Temperate fruit and berries: a 12 °C warm-up (11 for cherry) with a
  shorter bloom-to-harvest offset and a longer span matches the guides'
  harvest windows better than a 10 °C warm-up with long offsets.
- Citrus and avocado: longer harvest spans (6–8 months). The warm-up stays
  at 19.5 °C, because lowering it would show citrus in cool-summer climates.
- Parameters were kept within plausible growing ranges even where a sweep
  found a higher score (for example frost-hardy sweet potato or potato, or
  a fig that ripens four months after an 11 °C warm-up, which would put
  figs in Oslo from October to February).
- Knife edges: a threshold within about 0.1 °C of a fixture city's monthly
  mean flips on the next data version. `rhubarb` moved from 8 to 8.2 °C
  (Hobart's coldest month is 7.98 °C, Minneapolis's April 8.3 °C). The
  cool-season upper bound of 25 °C sits just above New York's July
  (24.8 °C) and Barcelona's August (24.6 °C), so both show those crops all
  summer. The remaining low scorers
  are listed with reasons in the baseline document.
