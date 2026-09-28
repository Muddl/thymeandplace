# Load test

`season.js` is a [k6](https://grafana.com/docs/k6/) script for the public site. It runs inside the compose network against `app:8080` directly, so Caddy and TLS are out of the picture (the external uptime monitor covers those) and every virtual user can send its own `X-Forwarded-For`: the app trusts that header there (`TRUST_PROXY_HEADERS=true`), so the per-IP rate limits don't cap the test.

Design and pass criteria: `docs/superpowers/specs/2026-09-27-db-capacity-and-load-protection-design.md` §4.6. Record every production run in `docs/capacity-baseline.md`.

## Scenarios

| Scenario | Rate | Duration | Pass criteria (k6 thresholds) |
|---|---|---|---|
| `sustained` | `RATE` (default 50 req/s) | `DURATION` (default 5m) | p95 < 1 s warm, < 2 s cold; < 0.5% 503s |
| `spike` | `SPIKE_RATE` (default 150 req/s) | 1m, right after `sustained` | 503s allowed, but p95 of the 503 responses < 400 ms (fast shedding) |
| `recovery` | `RATE` | 1m, starting 30 s after the spike | < 0.5% 503s |

Each iteration is 30% cold `/season` (a cell not yet requested in this run), 40% warm `/season` (a coordinate warmed during setup), 20% `/ingredient` (an item link from a setup response) and 10% `/places` (2–6 letter prefixes). Any status other than 200 or 503 counts against `unexpected_status`.

## Coordinates

`coords.json` holds 500 land cell centres from the live `climate_cell` view, generated once and committed. Generate it on the VPS after the production climate dataset is live (from `/opt/thymeandplace/deploy`):

```bash
docker compose exec -T db psql -U tp -d thymeandplace -At -c "
  SELECT json_agg(json_build_array(
           round((90 - (cell_id / 1440 + 0.5) * 0.25)::numeric, 4),
           round((-180 + (cell_id % 1440 + 0.5) * 0.25)::numeric, 4)))
  FROM (SELECT cell_id FROM climate_cell ORDER BY md5(cell_id::text) LIMIT 500) s" > loadtest/coords.json
```

`ORDER BY md5(cell_id)` gives a stable, spread-out sample, so regenerating after a climate swap gives nearly the same list. Commit the file. Cold requests step to neighbouring cells of these points, so a run has about 40,000 distinct cold cells to draw from.

## Running it

Run at a quiet time, on the production VPS, from `/opt/thymeandplace/deploy`:

```bash
docker run --rm --network thymeandplace_default -v "$PWD/loadtest:/scripts:ro" \
  grafana/k6 run /scripts/season.js
```

Override the defaults with `-e`, for example `-e RATE=30 -e DURATION=2m` for a short check, or `-e BASE_URL=http://app:8080`. k6 exits non-zero when a threshold fails.

While it runs, watch the connection count and the shedding log line in another shell:

```bash
docker compose exec db psql -U tp -d thymeandplace -c "SELECT count(*), usename, state FROM pg_stat_activity GROUP BY 2, 3"
docker compose logs -f app | grep "public DB gate full"
```

Run it again after any change to `DB_MAX_CONNS`, the VPS size or the reference data.
