# Authoring the catalog

The engine rules are in spec §12.3. These are starting points; the preview's
calibration score is the judge. Temperatures are **monthly mean** temperatures
(the average of the monthly mean minimum and mean maximum), not daytime highs.

## Parameter guidance by kind

| Kind | Field | Typical range | Notes |
|---|---|---|---|
| annual, warm-season (tomato, pepper, squash, basil, beans, corn) | frost | `tender` | |
| | grow_temp_c | [15–18, 30–32] | lower bound is the key lever |
| | months_to_harvest | 2–4 | from planting (or transplant) to first harvest |
| annual, cool-season (kale, cabbage, peas, lettuce, spinach, leeks) | frost | `hardy` | |
| | grow_temp_c | [4–7, 18–22] | upper bound stops summer "bolting" months |
| | months_to_harvest | 1–4 | |
| root/storage crops (potato, onion, carrot, squash) | storage_months | 3–8 | only if typically sold from storage |
| perennial_fruit, temperate (apple, pear, plum, cherry) | chill_months_min | 3–5 | months with mean < 7 °C |
| | warmup_temp_c | 9–12 | spring warm-up |
| | months_bloom_to_harvest | 2 (cherry) – 6 (late apple) | |
| | harvest_span_months | 1–3 | |
| perennial_fruit, subtropical (citrus, avocado) | frost | `tender` | frost months drop out of the harvest |
| | chill_months_min | 0 | |
| cultivated_year_round | – | – | bananas, button mushrooms, greenhouse staples sold all year |
| wild_mushroom | fruiting_temp_c | [8–12, 16–20] | |
| | min_precip_mm | 40–80 | monthly |
| seafood | regions | MEOW ecoregions | months from fisheries guidance, peak where clear |

Known limitation (spec §12.3): a `perennial_fruit` needs one clear month when
the mean crosses `warmup_temp_c`. In climates that never cross it (the humid
tropics) it shows nowhere. Tropical fruit sold all year (banana, pineapple,
papaya) should be `cultivated_year_round`.

## Worked examples (copy the shape; tune the numbers)

```json
{ "slug": "tomato", "name": "Tomato", "category": "vegetable", "kind": "annual",
  "frost": "tender", "grow_temp_c": [16, 30], "months_to_harvest": 3, "storage_months": 0,
  "tips": { "choose": "Heavy for its size, glossy, fragrant at the stem.",
            "store": "Stem-side down at room temperature; refrigerate only when fully ripe." },
  "pairings": ["basil", "garlic"], "note": "" }

{ "slug": "kale", "name": "Kale", "category": "vegetable", "kind": "annual",
  "frost": "hardy", "grow_temp_c": [4, 20], "months_to_harvest": 2, "storage_months": 0,
  "tips": { "choose": "Crisp, deep-coloured leaves without yellowing.", "store": "Wrapped loosely in the fridge; use within 5 days." },
  "pairings": ["garlic", "lemon"], "note": "Sweeter after a frost." }

{ "slug": "apple", "name": "Apple", "category": "fruit", "kind": "perennial_fruit",
  "frost": "hardy", "chill_months_min": 3, "warmup_temp_c": 10, "months_bloom_to_harvest": 5,
  "harvest_span_months": 3, "storage_months": 5,
  "tips": { "choose": "Firm, no soft spots, smells like apple.", "store": "Cold and humid, away from leafy greens." },
  "pairings": ["pork", "cinnamon"], "note": "" }

{ "slug": "banana", "name": "Banana", "category": "fruit", "kind": "cultivated_year_round",
  "tips": { "choose": "Yellow with a few brown flecks for eating now.", "store": "Room temperature; refrigerate once ripe." },
  "pairings": [], "note": "" }

{ "slug": "chanterelle", "name": "Chanterelle", "category": "mushroom", "kind": "wild_mushroom",
  "fruiting_temp_c": [10, 18], "min_precip_mm": 60, "storage_months": 0,
  "tips": { "choose": "Dry, firm, apricot smell; from a seller who can name the forager.",
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
- Subtropical fruit that needs a hot summer (citrus, `avocado`, `mango`,
  `pomegranate`) uses a high `warmup_temp_c` (19.5–20). Cool-summer climates
  never reach it, so these fruit do not show where they cannot ripen; the
  bloom-to-harvest offset is counted from that later month.
- `grape` and `kiwifruit` are `tender` so that a harvest does not appear in
  frost months in short-summer climates.
- Perennial herbs (thyme, rosemary, sage, oregano, mint) are modelled as
  `annual`: they are picked whenever the plant is growing.
- The rules are temperature-only, so spring and autumn with the same mean
  look alike. Wild mushrooms (`morel` especially) and overwintered crops
  (`broad-bean`, `brussels-sprouts`) can show in the "wrong" shoulder
  season; tune them against the calibration fixtures.
