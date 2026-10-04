// Fails when a translation key exists in the reference locale but not in every
// other one, which is how a UI-facing string ships as a raw dotted key path to
// whoever runs the panel in that language - the build and lint both pass, so
// nothing else catches it.
//
// Superfluous keys are reported but not fatal: the repository already carries
// orphans from removed features, and failing on those would block every change
// until an unrelated cleanup lands. Missing keys are the defect that reaches a
// user, and there are none today, so that half is enforced.
import { readFileSync, readdirSync } from "node:fs";
import { join, dirname, basename } from "node:path";
import { fileURLToPath } from "node:url";

const REFERENCE = "en";
const localesDir = join(dirname(fileURLToPath(import.meta.url)), "..", "src", "i18n", "locales");

function flatten(value, prefix = "") {
  const keys = new Set();
  for (const [key, child] of Object.entries(value)) {
    const path = `${prefix}${key}`;
    if (child !== null && typeof child === "object" && !Array.isArray(child)) {
      for (const nested of flatten(child, `${path}.`)) keys.add(nested);
    } else {
      keys.add(path);
    }
  }
  return keys;
}

function loadLocale(name) {
  return flatten(JSON.parse(readFileSync(join(localesDir, `${name}.json`), "utf8")));
}

const languages = readdirSync(localesDir)
  .filter((file) => file.endsWith(".json"))
  .map((file) => basename(file, ".json"));

if (!languages.includes(REFERENCE)) {
  console.error(`missing reference locale ${REFERENCE}.json in ${localesDir}`);
  process.exit(1);
}

const reference = loadLocale(REFERENCE);
let missingTotal = 0;

for (const language of languages.filter((name) => name !== REFERENCE)) {
  const keys = loadLocale(language);
  const missing = [...reference].filter((key) => !keys.has(key)).sort();
  const superfluous = [...keys].filter((key) => !reference.has(key)).sort();

  if (missing.length > 0) {
    missingTotal += missing.length;
    console.error(`${language}.json is missing ${missing.length} key(s) present in ${REFERENCE}.json:`);
    for (const key of missing) console.error(`  - ${key}`);
  }
  if (superfluous.length > 0) {
    console.warn(`${language}.json has ${superfluous.length} key(s) absent from ${REFERENCE}.json (not fatal):`);
    for (const key of superfluous) console.warn(`  ? ${key}`);
  }
}

if (missingTotal > 0) {
  console.error(`\nlocale parity failed: ${missingTotal} missing key(s)`);
  process.exit(1);
}

console.log(`locale parity ok: ${reference.size} keys across ${languages.join(", ")}`);
