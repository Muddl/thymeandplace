# Calibration fixtures

One JSON file per location, taken from a published seasonal guide.
Format: see `internal/calibration/calibration.go` (`Fixture`).
Every file must record its `source` (guide title and URL).
Fixtures are embedded into the binary and are used only by the admin
preview and the daily drift check. They are never shown on public pages.

## Field seasons, not year-round supply

Fixtures model when a crop is harvested outdoors near the location, because
that is what the engine estimates. Rows that describe year-round supply
(greenhouses, imports, national wholesale) are left out, and every other
month is kept as published:

- `oslo-no`: cucumber, lettuce and tomato were listed all 12 months
  (Norwegian greenhouse production); dropped.
- `hong-kong-hk` replaced San José, Costa Rica, whose chart listed nearly
  everything all year.
- `montevideo-uy`: the MGAP / Mercado Modelo calendar is a wholesale-supply
  chart. Tomato, bell pepper, eggplant, cucumber, basil and sweet corn are
  frost-tender warm-season crops listed all 12 months, including the
  Montevideo winter (coldest-month mean 10 °C), when none of them grow
  outdoors there; dropped. Year-round rows for storage crops (potato,
  onion, garlic, winter squash), cool-season crops and fruit were kept.
