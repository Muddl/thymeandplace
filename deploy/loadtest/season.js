// k6 load test for the public site (docs/superpowers/specs/
// 2026-09-27-db-capacity-and-load-protection-design.md §4.6). Run it inside
// the compose network against app:8080; see README.md in this directory.
//
// Mix: 30% cold /season, 40% warm /season, 20% /ingredient, 10% /places.
// Every request sends its own X-Forwarded-For, so the per-IP limits (which
// the app applies with TRUST_PROXY_HEADERS=true) don't cap the test.
import http from "k6/http";
import exec from "k6/execution";
import { Rate, Trend } from "k6/metrics";

const BASE = __ENV.BASE_URL || "http://app:8080";
const RATE = Number(__ENV.RATE || 50); // sustained req/s (spec §8 question 1)
const SPIKE_RATE = Number(__ENV.SPIKE_RATE || 150);
const DURATION = __ENV.DURATION || "5m";

// [[lat, lng], ...]: 500 land cell centres from the live climate_cell view
// (README.md, "Coordinates").
const COORDS = JSON.parse(open("./coords.json"));
const PREFIXES = ["lo", "lis", "par", "berl", "mad", "tok", "sydn", "new", "san", "mum", "cai", "lag", "lim", "tor", "mosc", "rey", "vanc", "sing", "cape", "bost"];

const warmDuration = new Trend("season_warm_duration", true);
const coldDuration = new Trend("season_cold_duration", true);
const shedDuration = new Trend("shed_503_duration", true);
const status503 = new Rate("status_503");
const failed = new Rate("unexpected_status");

const sustainedEnd = DURATION;
export const options = {
  setupTimeout: "10m",
  scenarios: {
    sustained: {
      executor: "constant-arrival-rate", exec: "mix", rate: RATE, timeUnit: "1s", duration: DURATION,
      preAllocatedVUs: 100, maxVUs: 400,
    },
    spike: {
      executor: "constant-arrival-rate", exec: "mix", rate: SPIKE_RATE, timeUnit: "1s", duration: "1m",
      startTime: sustainedEnd, preAllocatedVUs: 300, maxVUs: 1000,
    },
    // Starts 30 s after the spike: by then the error rate must be back under 0.5%.
    recovery: {
      executor: "constant-arrival-rate", exec: "mix", rate: RATE, timeUnit: "1s", duration: "1m",
      startTime: addSeconds(sustainedEnd, 90), preAllocatedVUs: 100, maxVUs: 400,
    },
  },
  thresholds: {
    "season_warm_duration{phase:sustained}": ["p(95)<1000"],
    "season_cold_duration{phase:sustained}": ["p(95)<2000"],
    "status_503{phase:sustained}": ["rate<0.005"],
    "shed_503_duration{phase:spike}": ["p(95)<400"],
    "status_503{phase:recovery}": ["rate<0.005"],
    "unexpected_status": ["rate<0.001"],
  },
};

function addSeconds(d, s) {
  const m = /^(\d+)(s|m)$/.exec(d);
  if (!m) throw new Error("DURATION must look like 300s or 5m, got " + d);
  return Number(m[1]) * (m[2] === "m" ? 60 : 1) + s + "s";
}

function randomIP() {
  const b = () => Math.floor(Math.random() * 254) + 1;
  return `10.${b()}.${b()}.${b()}`;
}

function get(path, kind) {
  const phase = exec.scenario.name;
  const res = http.get(BASE + path, { headers: { "HX-Request": "true", "X-Forwarded-For": randomIP() }, tags: { kind, phase } });
  status503.add(res.status === 503, { phase });
  failed.add(res.status !== 200 && res.status !== 503, { phase });
  if (res.status === 503) shedDuration.add(res.timings.duration, { phase });
  return res;
}

// Neighbouring cells (whole 0.25° steps within ±1°, excluding the centre)
// give every cold request a cell nobody has asked for yet in this run.
const OFFSETS = [];
for (let dy = -4; dy <= 4; dy++) for (let dx = -4; dx <= 4; dx++) if (dx || dy) OFFSETS.push([dy * 0.25, dx * 0.25]);

function coldCoord(k) {
  const [lat, lng] = COORDS[k % COORDS.length];
  const [dy, dx] = OFFSETS[Math.floor(k / COORDS.length) % OFFSETS.length];
  return [Math.max(-89.9, Math.min(89.9, lat + dy)), ((lng + dx + 540) % 360) - 180];
}

const seasonPath = ([lat, lng]) => `/season?lat=${lat.toFixed(4)}&lng=${lng.toFixed(4)}`;

// setup warms every base coordinate (so "warm" requests hit the cache) and
// collects item links to follow for /ingredient.
export function setup() {
  const links = [];
  for (const c of COORDS) {
    const res = http.get(BASE + seasonPath(c), { headers: { "HX-Request": "true", "X-Forwarded-For": randomIP() } });
    for (const m of (res.body || "").matchAll(/href="(\/ingredient\/[^"]+)"/g)) {
      if (links.length < 5000) links.push(m[1].replace(/&amp;/g, "&"));
    }
  }
  if (links.length === 0) throw new Error("no /ingredient links found while warming; is the catalog loaded?");
  return { links };
}

// iterationInTest restarts per scenario, so each scenario takes its own
// range of cold cells (40,000 in all; the default run uses about 8,000).
const COLD_BASE = { sustained: 0, spike: 15000, recovery: 30000 };

export function mix(data) {
  const r = Math.random();
  if (r < 0.3) {
    const res = get(seasonPath(coldCoord(COLD_BASE[exec.scenario.name] + exec.scenario.iterationInTest)), "cold");
    if (res.status === 200) coldDuration.add(res.timings.duration, { phase: exec.scenario.name });
  } else if (r < 0.7) {
    const res = get(seasonPath(COORDS[Math.floor(Math.random() * COORDS.length)]), "warm");
    if (res.status === 200) warmDuration.add(res.timings.duration, { phase: exec.scenario.name });
  } else if (r < 0.9) {
    get(data.links[Math.floor(Math.random() * data.links.length)], "ingredient");
  } else {
    get("/places?q=" + PREFIXES[Math.floor(Math.random() * PREFIXES.length)], "places");
  }
}
