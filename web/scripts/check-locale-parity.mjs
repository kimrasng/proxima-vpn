// Check locale parity and translation keys referenced by source. Runtime
// templates are checked against the English key family and all languages;
// finite value sets are asserted below to catch an omitted enum member.
import { readFileSync, readdirSync } from "node:fs";
import { join, dirname, basename } from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";

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
const localeKeys = new Map(languages.map((name) => [name, loadLocale(name)]));
let missingTotal = 0;

for (const language of languages.filter((name) => name !== REFERENCE)) {
  const keys = localeKeys.get(language);
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

const sourceRoot = join(localesDir, "..", "..");
const missingSourceKeys = new Map();
const dynamicPatterns = new Map();
const indirectKeys = new Map();

function noteMissing(key, location, missing) {
  const locations = missingSourceKeys.get(key) ?? [];
  locations.push(`${location} (${missing.join(", ")})`);
  missingSourceKeys.set(key, locations);
}

// These contracts are finite enums shared with the API/UI. Pattern matching
// alone only checks keys that happen to exist, not omitted members. The list
// intentionally names only dynamic families that exist in the current source.
const requiredDynamicKeys = {
  "admin.nodes.wizard.os.*": ["debian", "rhel", "alpine"],
  "admin.nodes.wizard.osHint.*": ["debian", "rhel", "alpine"],
  "admin.nodes.wizard.roles.*": ["exit", "relay", "both"],
  "admin.nodes.wizard.roles.*Hint": ["exit", "relay", "both"],
  "admin.nodes.wizard.presets.*": ["standard", "web_alt", "high_port", "custom"],
  "admin.nodes.wizard.presetHint.*": ["standard", "web_alt", "high_port", "custom"],
  "admin.dashboard.window**": ["Today", "Week", "Month"],
  "admin.routeManagement.*": ["createBatch", "previewPorts", "online", "offline", "unknown", "enabled", "disabled"],
  "admin.nodes.editSections.labelLang.*": ["ko", "en", "zh"],
  "admin.plans.editor.units.*": ["day", "week", "month"],
  "admin.plans.editor.*": ["active", "inactive"],
  "admin.subscriptionDomains.health.*": ["healthy", "warning", "failed", "unknown"],
  "admin.users.status.*": ["active", "suspended", "expired", "pending", "disabled"],
  "admin.orderDetail.outcome.*": ["received", "granted", "duplicate", "ignored", "failed"],
  "user.plan.promotion.rejected.*": ["not_found", "inactive", "outside_window", "plan_not_eligible", "below_minimum", "not_first_purchase", "user_not_allowed", "overall_cap_reached", "user_cap_reached"],
  "admin.nodes.endpoints.states.*": ["unconfigured", "pending", "ready", "conflict", "error", "deleting", "deleted", "valid", "not_applicable"],
  "admin.nodeEvents.severity.*": ["info", "success", "warning", "error"],
  "admin.nodeEvents.range*": ["All", "24h", "7d", "30d"],
  "admin.plans.features.langTab.*": ["ko", "en", "zh"],
  "admin.plans.features.textLang.*": ["ko", "en", "zh"],
  "admin.nodes.role.*": ["exit", "relay", "both", "published", "relayOnly"],
  "admin.nodes.status*": ["Online", "Offline"],
  "admin.nodeChains.*": ["tcp", "udp", "tcp_udp", "enabled", "disabled", "enable", "disable"],
  "admin.routeManagement.issues.*": ["directPrivate", "disabled", "unassigned", "entryOffline", "exitOffline", "dnsPending", "sniPending", "healthFailed"],
  "admin.routeManagement.publication.*": ["all", "assigned", "unassigned"],
  "admin.routeManagement.nodeViews.*": ["all", "entry", "exit"],
  "admin.routeManagement.nodeViews.*Hint": ["all", "entry", "exit"],
  "admin.dashboard.alert.*_desc": ["node_offline", "node_xray_down", "node_shaping_failed", "node_cpu", "node_memory", "node_disk"],
  "admin.dashboard.alert.*": ["node_offline", "node_xray_down", "node_shaping_failed", "node_cpu", "node_memory", "node_disk", "pending_requests"],
  "admin.dashboard.issue.*": ["offline", "xray_down", "shaping_failed", "cpu", "memory", "disk"],
  "admin.routeManagement.serverWarnings.*": ["vless_reality_only", "payload_rate_not_wire_rate"],
  "admin.orders.status.*": ["pending", "paid", "cancelled", "expired"],
  "user.plan.order.status.*": ["pending", "paid", "cancelled", "expired"],
  "admin.userDetail.window.*": ["today", "week", "month"],
  "admin.uuidEvictions.*": ["pending", "confirmed"],
};

// Open-ended server values retain an explicit fallback in the UI. These
// families still require at least one defined translation in every locale.
const serverProvidedDynamicKeys = new Set([
  "admin.userDetail.reason.*",
  "admin.nodeEvents.severity.*",
]);

function checkDynamicFamilies() {
  for (const [pattern, locations] of dynamicPatterns) {
    if (!Object.hasOwn(requiredDynamicKeys, pattern) && !serverProvidedDynamicKeys.has(pattern)) {
      noteMissing(`unverified dynamic key ${pattern}`, locations.join("; "), languages);
      continue;
    }
    // Adjacent template expressions form one runtime suffix (for example
    // window${firstChar}${rest} -> windowToday), not two independent wildcards.
    const pieces = pattern.split(/\*+/);
    const rx = new RegExp(`^${pieces.map((piece) => piece.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join(".+")}$`);
    const family = [...reference].filter((key) => rx.test(key));
    if (family.length === 0) {
      noteMissing(pattern, locations.join("; "), languages);
    }
    for (const key of family) {
      const missing = languages.filter((name) => !localeKeys.get(name).has(key));
      if (missing.length) noteMissing(key, locations.join("; "), missing);
    }
  }
  for (const [pattern, values] of Object.entries(requiredDynamicKeys)) {
    if (!dynamicPatterns.has(pattern)) continue;
    for (const value of values) {
      const key = pattern.replace(/\*+/g, value);
      const missing = languages.filter((name) => !localeKeys.get(name).has(key));
      if (missing.length) noteMissing(key, dynamicPatterns.get(pattern).join("; "), missing);
    }
  }
}

function locationFor(node, source, path) {
  return `${path.slice(sourceRoot.length + 1)}:${source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1}`;
}

function scanSource(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      scanSource(path);
    } else if (/\.(tsx|ts)$/.test(entry.name) && !entry.name.endsWith(".d.ts")) {
      const text = readFileSync(path, "utf8");
      const source = ts.createSourceFile(path, text, ts.ScriptTarget.Latest, true,
        entry.name.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS);
      const visit = (node) => {
        if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === "t") {
          const arg = node.arguments[0];
          const location = `${path.slice(sourceRoot.length + 1)}:${source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1}`;
          if (arg && (ts.isStringLiteral(arg) || ts.isNoSubstitutionTemplateLiteral(arg))) {
            const key = arg.text;
            const missing = languages.filter((name) => !localeKeys.get(name).has(key));
            if (missing.length > 0) noteMissing(key, location, missing);
          } else if (arg && ts.isTemplateExpression(arg)) {
            const pattern = arg.head.text + arg.templateSpans.map((span) => `*${span.literal.text}`).join("");
            const locations = dynamicPatterns.get(pattern) ?? [];
            locations.push(location);
            dynamicPatterns.set(pattern, locations);
          }
        }
        if (ts.isPropertyAssignment(node) && node.name && (
          (ts.isIdentifier(node.name) && node.name.text === "labelKey") ||
          (ts.isStringLiteral(node.name) && node.name.text === "labelKey")
        ) && ts.isStringLiteral(node.initializer)) {
          indirectKeys.set(node.initializer.text, locationFor(node, source, path));
        }
        // Literal values in breadcrumb/help lookup tables are passed to t()
        // indirectly, so they need the same cross-locale check.
        if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) &&
          ["staticPageLabels", "nestedLeafLabels", "helpTopicKeys"].includes(node.name.text) && node.initializer) {
          const collect = (value) => {
            if (ts.isStringLiteral(value) && value.text.includes(".")) {
              indirectKeys.set(value.text, locationFor(value, source, path));
            }
            ts.forEachChild(value, collect);
          };
          collect(node.initializer);
        }
        ts.forEachChild(node, visit);
      };
      visit(source);
    }
  }
}

scanSource(sourceRoot);
for (const [key, location] of indirectKeys) {
  const missing = languages.filter((name) => !localeKeys.get(name).has(key));
  if (missing.length) noteMissing(key, location, missing);
}
checkDynamicFamilies();
if (missingSourceKeys.size > 0) {
  console.error(`Source references ${missingSourceKeys.size} key(s) absent from locale resources:`);
  for (const [key, locations] of missingSourceKeys) {
    console.error(`  - ${key}: ${locations.join("; ")}`);
  }
  missingTotal += missingSourceKeys.size;
}

if (missingTotal > 0) {
  console.error(`\nlocale validation failed: ${missingTotal} missing key(s)`);
  process.exit(1);
}

console.log(`locale validation ok: ${reference.size} keys across ${languages.join(", ")} and static/dynamic source references (${dynamicPatterns.size} dynamic families)`);
