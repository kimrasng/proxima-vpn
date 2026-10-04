#!/usr/bin/env node
import assert from 'node:assert/strict';
import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..');
const files = [
  'config/managed_entry_dns_test.go',
  'config/managed_entry_dns_secret_test.go',
  'database/schema_managed_reconciliation_test.go',
  'services/cloudflare_dns_errors_test.go',
  'services/cloudflare_dns_records_test.go',
  'services/managed_entry_intent_db_test.go',
  'services/managed_entry_delete_db_test.go',
  'services/managed_entry_worker_store_test.go',
  'services/managed_entry_worker_lock_test.go',
  'services/managed_entry_worker_backoff_test.go',
  'services/managed_entry_reconcile_present_test.go',
  'services/managed_entry_reconcile_delete_test.go',
  'handlers/managed_entry_dns_http_integration_test.go',
  'handlers/managed_entry_registration_http_integration_test.go',
  'scheduler/managed_entry_dns_test.go',
];
const packages = ['config', 'database', 'services', 'handlers', 'scheduler'].map(
  (name) => `github.com/proximavpn/proxima-vpn/api-server/internal/${name}`,
);

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
    if (event.Action === 'output' && !event.Test && /\(cached\)/.test(event.Output ?? '')) {
      problems.push(`cached ${event.Package}`);
    }
    if (event.Action === 'pass' && event.Test) {
      testPassCount++;
      passed.add(`${event.Package}:${event.Test}`);
    }
    if (event.Action === 'pass' && !event.Test) packagePasses.add(event.Package);
  }
  for (const pkg of packages) {
    if (!packagePasses.has(pkg)) problems.push(`missing package pass ${pkg}`);
  }
  for (const test of required) {
    if (!passed.has(test)) problems.push(`missing required pass ${test}`);
  }
  const subtestCases = [
    ['services', 'TestReconcilePresent_providerErrorsClassified', 4],
    ['services', 'TestReconcilePresent_foreignRecordsFailClosed', 3],
    ['services', 'TestReconcilePresent_lostWriteResponsesConverge', 2],
    ['services', 'TestCloudflareDNSError_whenUpstreamFails', 13],
    ['services', 'TestCloudflareDNSWrites_whenCreatingUpdatingAndDeleting', 3],
    ['handlers', 'TestExplicitNodeRemovalTombstonesManagedEntryIntentThroughHTTP', 2],
    ['handlers', 'TestRegisterCreatesManagedEntryIntentForForwardingNodeThroughHTTP', 2],
  ];
  for (const [pkg, test, count] of subtestCases) {
    const prefix = `github.com/proximavpn/proxima-vpn/api-server/internal/${pkg}:${test}/`;
    const actual = [...passed].filter((name) => name.startsWith(prefix)).length;
    if (actual < count) problems.push(`missing subtest coverage ${pkg}:${test} (${actual}/${count})`);
  }
  return { problems, requiredCount: required.length, testPassCount, skipCount,
    packagePassCount: packagePasses.size, requiredTests: required.sort() };
}

function inventory() {
  const tests = files.flatMap((file) => {
    const source = readFileSync(resolve(root, 'api-server/internal', file), 'utf8');
    const names = [...source.matchAll(/^func (Test[A-Za-z0-9_]+)\(t \*testing\.T\)/gm)];
    if (names.length === 0) throw new Error(`no top-level tests in ${file}`);
    return names.map((match) => `github.com/proximavpn/proxima-vpn/api-server/internal/${file.split('/')[0]}:${match[1]}`);
  });
  if (new Set(tests).size !== tests.length) throw new Error('duplicate required test');
  return tests;
}

try {
  if (process.argv[2] === '--self-test') {
    const pkg = packages[0];
    const test = `${pkg}:TestExample`;
    const events = packages.map((Package) => ({ Package, Action: 'pass' }));
    events.push({ Package: pkg, Test: 'TestExample', Action: 'pass' });
    assert.equal(check(events, [test]).skipCount, 0);
    assert.match(check(events, [`${pkg}:TestMissing`]).problems.join(' '), /missing required pass/);
    assert.match(check([...events, { Package: pkg, Test: 'TestExample/child', Action: 'skip' }], [test]).problems.join(' '), /skipped/);
    assert.match(check([...events, { Package: pkg, Action: 'output', Output: 'ok (cached)' }], [test]).problems.join(' '), /cached/);
    assert.match(check(events.slice(1), [test]).problems.join(' '), /missing package pass/);
    console.log('Checker self-test PASS: missing, skipped child, cached, and package failure detected');
  } else {
    if (process.argv.length !== 4) throw new Error('Usage: check-phs029-results.mjs <go-jsonl> <new-summary-json> | --self-test');
    const lines = readFileSync(process.argv[2], 'utf8').trim().split('\n');
    const events = lines.map((line) => JSON.parse(line));
    const result = check(events, inventory());
    writeFileSync(process.argv[3], `${JSON.stringify({ ...result, passed: result.problems.length === 0 }, null, 2)}\n`, { flag: 'wx' });
    console.log(`Go JSON: ${result.requiredCount} required top-level tests, ${result.testPassCount} test/subtest passes, ${result.skipCount} skips, ${result.packagePassCount}/5 package passes; ${result.problems.length} problems`);
    if (result.problems.length) {
      for (const problem of result.problems) console.error(problem);
      process.exitCode = 1;
    }
  }
} catch (error) {
  console.error(`Result checker: ${error.message}`);
  process.exitCode = 1;
}
