#!/usr/bin/env node
import assert from 'node:assert/strict';
import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..');
const files = [
  'reality/hostname_test.go',
  'reality/listeners_test.go',
  'database/managed_endpoint_backfill_test.go',
  'database/managed_endpoint_backfill_defaults_test.go',
  'database/schema_managed_endpoints_test.go',
  'database/schema_managed_endpoints_not_applicable_test.go',
  'database/schema_managed_endpoints_upgrade_test.go',
  'database/schema_node_chains_test.go',
  'database/schema_node_chains_direct_test.go',
  'database/schema_node_chains_migration_test.go',
  'services/reality_sni_test.go',
  'services/reality_sni_db_test.go',
  'services/reality_sni_listeners_db_test.go',
  'services/reality_sni_locking_db_test.go',
  'services/reality_sni_not_applicable_db_test.go',
  'handlers/admin_node_endpoints_http_test.go',
  'handlers/admin_inbound_reality_http_test.go',
  'handlers/admin_inbound_reality_concurrency_test.go',
  'handlers/admin_node_chain_test.go',
  'handlers/admin_node_chain_conflict_test.go',
  'handlers/admin_node_chain_create_http_integration_test.go',
  'handlers/admin_node_chain_http_integration_test.go',
  'handlers/admin_node_chain_managed_http_test.go',
  'handlers/admin_node_chain_list_groups_test.go',
  'handlers/subscription_chain_test.go',
  'handlers/subscription_reality_contract_test.go',
  'handlers/subscription_reality_edges_test.go',
  'handlers/subscription_reality_managed_formats_test.go',
  'handlers/node_agent_endpoint_contract_test.go',
];
const prefix = 'github.com/proximavpn/proxima-vpn/api-server/internal/';
const packages = ['reality', 'database', 'services', 'handlers'].map((name) => prefix + name);

function inventory() {
  const names = files.flatMap((file) => {
    const source = readFileSync(resolve(root, 'api-server/internal', file), 'utf8');
    const matches = [...source.matchAll(/^func (Test[A-Za-z0-9_]+)\(t \*testing\.T\)/gm)];
    if (!matches.length) throw new Error(`no top-level tests in ${file}`);
    return matches.map((match) => `${prefix}${file.split('/')[0]}:${match[1]}`);
  });
  if (new Set(names).size !== names.length) throw new Error('duplicate required test');
  return names.sort();
}

function parseEvents(text) {
  if (!text.trim()) throw new Error('empty Go JSON stream');
  return text.trimEnd().split('\n').map((line, index) => {
    let event;
    try { event = JSON.parse(line); } catch { throw new Error(`malformed Go JSON at line ${index + 1}`); }
    if (!event || typeof event !== 'object' || Array.isArray(event) ||
        typeof event.Package !== 'string' || !event.Package ||
        !['start', 'run', 'pass', 'fail', 'skip', 'output', 'pause', 'cont'].includes(event.Action) ||
        (event.Test !== undefined && typeof event.Test !== 'string') ||
        (event.Output !== undefined && typeof event.Output !== 'string')) {
      throw new Error(`invalid Go event at line ${index + 1}`);
    }
    return event;
  });
}

function check(events, required) {
  const problems = [];
  const passed = new Set();
  const packagePasses = new Set();
  let skipCount = 0;
  let testPassCount = 0;
  for (const event of events) {
    if (!packages.includes(event.Package)) continue;
    if (event.Action === 'skip') {
      skipCount++;
      problems.push(`skipped ${event.Package} ${event.Test ?? '(package)'}`);
    }
    if (event.Action === 'fail') problems.push(`failed ${event.Package} ${event.Test ?? '(package)'}`);
    if (event.Action === 'output' && /\(cached\)/.test(event.Output ?? '')) problems.push(`cached ${event.Package}`);
    if (event.Action === 'pass' && event.Test) {
      testPassCount++;
      passed.add(`${event.Package}:${event.Test}`);
    }
    if (event.Action === 'pass' && !event.Test) packagePasses.add(event.Package);
  }
  for (const pkg of packages) if (!packagePasses.has(pkg)) problems.push(`missing package pass ${pkg}`);
  for (const name of required) if (!passed.has(name)) problems.push(`missing required pass ${name}`);
  return { passed: problems.length === 0, requiredCount: required.length, testPassCount,
    skipCount, packagePassCount: packagePasses.size, problems };
}

try {
  if (process.argv[2] === '--self-test') {
    const required = inventory();
    const events = packages.map((Package) => ({ Package, Action: 'pass' }));
    for (const name of required) {
      const [Package, Test] = name.split(':');
      events.push({ Package, Test, Action: 'pass' });
    }
    assert.equal(check(events, required).passed, true);
    assert.equal(parseEvents(JSON.stringify({ Package: packages[0], Action: 'start' })).length, 1);
    const first = events.find((event) => event.Test);
    assert.match(check(events.filter((event) => event !== first), required).problems.join(' '), /missing required pass/);
    for (const [bad, reason] of [
      [{ ...first, Action: 'skip' }, /skipped/],
      [{ ...first, Action: 'fail' }, /failed/],
      [{ Package: packages[0], Action: 'output', Output: 'ok (cached)' }, /cached/],
    ]) assert.match(check([...events, bad], required).problems.join(' '), reason);
    assert.match(check(events.filter((event) => event !== events[0]), required).problems.join(' '), /missing package pass/);
    assert.throws(() => parseEvents('{'), /malformed Go JSON/);
    assert.throws(() => parseEvents('{}'), /invalid Go event/);
    assert.throws(() => parseEvents(''), /empty Go JSON/);
    console.log(`Checker self-test PASS: ${required.length} inventoried; missing, skip, fail, cache, package omission and malformed stream rejected`);
  } else {
    if (process.argv.length !== 4) throw new Error('Usage: check-phs030-results.mjs <go-jsonl> <new-summary-json> | --self-test');
    const result = check(parseEvents(readFileSync(process.argv[2], 'utf8')), inventory());
    writeFileSync(process.argv[3], `${JSON.stringify(result, null, 2)}\n`, { flag: 'wx' });
    console.log(`Go JSON: ${result.requiredCount} required tests, ${result.testPassCount} test/subtest passes, ${result.skipCount} skips, ${result.packagePassCount}/${packages.length} package passes; ${result.problems.length} problems`);
    if (!result.passed) {
      for (const problem of result.problems) console.error(problem);
      process.exitCode = 1;
    }
  }
} catch (error) {
  console.error(`Result checker: ${error.message}`);
  process.exitCode = 1;
}
