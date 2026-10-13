#!/usr/bin/env node
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

export function listTests(src, filename) {
  const tests = [];
  const re = /(?<![.\w])test(?:\.(?:only|skip|fixme))?\(/g;
  let match;
  while ((match = re.exec(src))) {
    const slice = src.slice(match.index, match.index + 1000);
    if (/^test\.(describe|beforeAll|beforeEach|afterAll|afterEach|use|extend|info|setTimeout|configure)\(/.test(slice)) {
      continue;
    }
    const titleMatch = slice.match(/test(?:\.(?:only|skip|fixme))?\(\s*(`[\s\S]*?`|'[^']*'|"[^"]*")/);
    tests.push({
      filename,
      title: titleMatch ? titleMatch[1] : "?",
      tagged: /@readonly|@mutating/.test(slice),
    });
  }
  return tests;
}

function checkDir(dir) {
  const files = readdirSync(dir).filter((f) => f.endsWith(".spec.ts"));
  let anyTag = false;
  const untagged = [];
  for (const f of files) {
    const src = readFileSync(join(dir, f), "utf8");
    if (/@readonly|@mutating/.test(src)) anyTag = true;
    for (const t of listTests(src, f)) {
      if (!t.tagged) untagged.push(`${f}: ${t.title}`);
    }
  }
  return { anyTag, untagged };
}

function selfTest() {
  const tagged = listTests(
    `test("a @readonly", async () => {});\ntest("b", { tag: "@mutating" }, async () => {});`,
    "t.ts",
  );
  if (tagged.length !== 2 || tagged.some((t) => !t.tagged)) {
    throw new Error("expected both tests tagged");
  }
  const mix = listTests(`test("a @readonly", async () => {});\ntest("bare", async () => {});`, "t.ts");
  if (mix.length !== 2 || mix[0].tagged !== true || mix[1].tagged !== false) {
    throw new Error("expected one tagged and one untagged");
  }
  const skipDescribe = listTests(`test.describe("g", () => {});\ntest("x @readonly", async () => {});`, "t.ts");
  if (skipDescribe.length !== 1) {
    throw new Error(`describe counted as a test: ${skipDescribe.length}`);
  }
  console.log("check-tags self-test ok");
}

const here = dirname(fileURLToPath(import.meta.url));
if (process.argv.includes("--self-test")) {
  selfTest();
  process.exit(0);
}

const { anyTag, untagged } = checkDir(join(here, "tests"));
if (!anyTag) {
  console.log("check-tags: no @readonly or @mutating in e2e/tests yet; skip until QA tags land");
  process.exit(0);
}
if (untagged.length > 0) {
  console.error("check-tags: untagged tests (every test needs @readonly or @mutating):");
  for (const t of untagged) console.error(`  ${t}`);
  process.exit(1);
}
console.log("check-tags: every spec test is tagged");
