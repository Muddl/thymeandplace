#!/usr/bin/env node
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { captureSettings, isLocalOrigin } from "./capture.mjs";

const here = dirname(fileURLToPath(import.meta.url));

const local = { trace: "retain-on-failure", screenshot: "off", video: "off" };
const remote = { trace: "off", screenshot: "off", video: "off" };

function assertEqual(got, want, msg) {
  const gs = JSON.stringify(got);
  const ws = JSON.stringify(want);
  if (gs !== ws) {
    throw new Error(`${msg}: got ${gs}, want ${ws}`);
  }
}

function assert(cond, msg) {
  if (!cond) throw new Error(msg);
}

const localURLs = [
  "https://localhost",
  "http://localhost",
  "https://localhost:8443",
  "https://localhost/season",
  "https://127.0.0.1",
  "http://127.0.0.1:8080",
];
const remoteURLs = [
  "https://example.com",
  "https://thymeand.place",
  "http://evil.example",
  "https://localhost.evil.com",
  "https://127.0.0.1.example.com",
  "https://staging.thymeand.place",
];

function selfTest() {
  for (const url of localURLs) {
    assert(isLocalOrigin(url), `${url} should be local`);
    assertEqual(captureSettings(url), local, url);
  }
  for (const url of remoteURLs) {
    assert(!isLocalOrigin(url), `${url} should not be local`);
    assertEqual(captureSettings(url), remote, url);
  }

  const prevGrep = process.env.GREP;
  const prevBase = process.env.E2E_BASE_URL;
  try {
    for (const grep of ["", "@readonly", "@mutating"]) {
      process.env.GREP = grep;
      process.env.E2E_BASE_URL = "https://example.com";
      assertEqual(captureSettings(), remote, `E2E_BASE_URL=https://example.com GREP=${grep || "unset"}`);
      assertEqual(captureSettings("https://thymeand.place"), remote, `explicit thymeand.place GREP=${grep || "unset"}`);
      process.env.E2E_BASE_URL = "https://localhost";
      assertEqual(captureSettings(), local, `E2E_BASE_URL=https://localhost GREP=${grep || "unset"}`);
    }
  } finally {
    if (prevGrep === undefined) delete process.env.GREP;
    else process.env.GREP = prevGrep;
    if (prevBase === undefined) delete process.env.E2E_BASE_URL;
    else process.env.E2E_BASE_URL = prevBase;
  }

  const configSrc = readFileSync(join(here, "playwright.config.ts"), "utf8");
  assert(configSrc.includes("captureSettings"), "playwright.config.ts must call captureSettings");
  assert(!/trace:\s*"retain-on-failure"/.test(configSrc), "playwright.config.ts must not hardcode retain-on-failure");

  const remoteWorkflow = readFileSync(join(here, "..", ".github", "workflows", "e2e-remote-config.yml"), "utf8");
  assert(!/^\s+uses:\s*\S*upload-artifact/m.test(remoteWorkflow), "e2e-remote-config.yml must not use an artifact upload action");

  console.log("check-capture self-test ok");
}

selfTest();

const target = process.env.E2E_BASE_URL;
if (target) {
  const settings = captureSettings(target);
  console.log(`captureSettings(${target}) = ${JSON.stringify(settings)} local=${isLocalOrigin(target)}`);
  if (!isLocalOrigin(target)) {
    assertEqual(settings, remote, `non-local ${target}`);
  }
}
