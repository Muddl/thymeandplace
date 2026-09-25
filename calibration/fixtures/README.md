# Calibration fixtures

One JSON file per location, taken from a published seasonal guide.
Format: see `internal/calibration/calibration.go` (`Fixture`).
Every file must record its `source` (guide title and URL).
Fixtures are embedded into the binary and are used only by the admin
preview and the daily drift check. They are never shown on public pages.
