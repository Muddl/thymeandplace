# Capacity baseline

Evidence for issue #1's pre-launch acceptance criteria (`docs/superpowers/specs/2026-09-27-db-capacity-and-load-protection-design.md` §4.5–4.6). Each run on the production VPS adds a dated section below. Nothing has been recorded yet: both measurements need the production reference data to be live.

## Settings under test

| Item | Value |
|---|---|
| Server pool | 16 max, 2 min (`DB_MAX_CONNS` overrides the max) |
| Public DB slots / gate wait | 12 (three quarters of the pool) / 250 ms |
| Shed response | 503 + `Retry-After: 5` |
| Per-cell input cache | 20,000 entries |
| Pass criteria | sustained: p95 < 1 s warm, < 2 s cold, < 0.5% 503s; spike: p95 of 503s < 400 ms; recovery: < 0.5% 503s within 30 s |

## Ocean lookup cost (§4.5)

Target: p95 ≤ 25 ms. If it's higher, open a follow-up issue for subdividing the ocean polygons at ingest.

For each point, run from `/opt/thymeandplace/deploy` (the query is `NearbyEcoregions` in `internal/store/handwritten.go`):

```bash
docker compose exec -T db psql -U tp -d thymeandplace -c "
  EXPLAIN (ANALYZE, BUFFERS)
  SELECT ecoregion_id FROM ocean_ecoregion
  WHERE ST_DWithin(geom, ST_SetSRID(ST_MakePoint(<lng>, <lat>), 4326)::geography, 100000)"
```

Points: the 10 coastal and 4 inland spot-check points in `internal/ingest/ocean.go` (`NewOcean`), plus 10 more coastal points. Record each point's execution time, then p50 and p95.

| Date | Ocean version | Points | p50 | p95 | Follow-up issue |
|---|---|---|---|---|---|
| — | — | — | — | — | — |

## Load test runs (§4.6)

For each run, record: date, VPS plan (vCPU/RAM), `DB_MAX_CONNS`, reference data versions, `RATE`/`SPIKE_RATE`/`DURATION`, and k6's summary for each scenario (p95 warm and cold, 503 rate, p95 of 503s), the peak `pg_stat_activity` count for the app, and whether every threshold passed.

| Date | VPS | Pool | Sustained p95 warm / cold | Sustained 503s | Spike p95 of 503s | Recovery 503s | Pass |
|---|---|---|---|---|---|---|---|
| — | — | — | — | — | — | — | — |
