#!/usr/bin/env node
import assert from 'node:assert/strict';
import { readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { runInNewContext } from 'node:vm';

const root = resolve(import.meta.dirname, '..');
const modulePaths = { api: 'api-server', pkg: 'pkg', node: 'node-agent' };
const nftName = 'github.com/proximavpn/proxima-vpn/node-agent/internal/relay:TestRenderNftablesSyntax';
const prefix = 'github.com/proximavpn/proxima-vpn/';

function oldFiles(phase) {
  const source = readFileSync(join(root, 'scripts', `check-phs${phase}-results.mjs`), 'utf8');
  const literal = source.match(/const files = (\[[\s\S]*?\]);/);
  if (!literal) throw new Error(`PHS-${phase} inventory unavailable`);
  const files = runInNewContext(literal[1]);
  if (!Array.isArray(files) || !files.length || files.some((file) => typeof file !== 'string'))
    throw new Error(`PHS-${phase} inventory invalid`);
  return files;
}

function sources(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? sources(path) : entry.name.endsWith('_test.go') ? [path] : [];
  });
}

function inventory(lane) {
  const moduleDir = join(root, modulePaths[lane]);
  const names = new Set();
  for (const file of sources(moduleDir)) {
    const relative = file.slice(moduleDir.length + 1);
    const packageName = `${prefix}${modulePaths[lane]}/${relative.slice(0, relative.lastIndexOf('/'))}`;
    const source = readFileSync(file, 'utf8');
    for (const match of source.matchAll(/^func (Test[A-Za-z0-9_]+)\(t \*testing\.T\)/gm))
      names.add(`${packageName}:${match[1]}`);
  }
  if (!names.size) throw new Error(`empty ${lane} test inventory`);
  if (lane === 'api') {
    for (const phase of ['029', '030']) for (const file of oldFiles(phase)) {
      const relative = `internal/${file}`;
      const source = readFileSync(join(moduleDir, relative), 'utf8');
      const packageName = `${prefix}api-server/internal/${file.split('/')[0]}`;
      const matches = [...source.matchAll(/^func (Test[A-Za-z0-9_]+)\(t \*testing\.T\)/gm)];
      if (!matches.length || matches.some((match) => !names.has(`${packageName}:${match[1]}`)))
        throw new Error(`missing PHS-${phase} inventory ${file}`);
    }
    for (const fragment of ['admin_node_chain_conflict', 'node_agent_exit_rules', 'subscription_reality',
      'managed_entry', 'reality_sni']) {
      if (!sources(join(moduleDir, 'internal')).some((file) => file.includes(fragment) &&
          /^func Test[A-Za-z0-9_]+\(t \*testing\.T\)/m.test(readFileSync(file, 'utf8'))))
        throw new Error(`missing PHS-031 coverage ${fragment}`);
    }
  }
  if (lane === 'node' && !names.has(nftName)) throw new Error('missing nft syntax test');
  if (lane === 'node' && ![...names].some((name) => name.endsWith(':TestApplyPreservesLastKnownGoodAfterFailedReplacement')))
    throw new Error('missing last-known-good test');
  return names;
}

function parse(text) {
  if (!text || !text.endsWith('\n')) throw new Error('empty or truncated Go JSON stream');
  return text.trimEnd().split('\n').map((line, index) => {
    let event;
    try { event = JSON.parse(line); } catch { throw new Error(`malformed Go JSON line ${index + 1}`); }
    if (!event || typeof event !== 'object' || Array.isArray(event) ||
        typeof event.Package !== 'string' || !event.Package ||
        !['start', 'run', 'pass', 'fail', 'skip', 'output', 'pause', 'cont'].includes(event.Action) ||
        (event.Test !== undefined && typeof event.Test !== 'string') ||
        (event.Output !== undefined && typeof event.Output !== 'string'))
      throw new Error(`invalid Go event line ${index + 1}`);
    return event;
  });
}

function check(events, packages, required, replacement) {
  const problems = [];
  const expected = new Set(packages);
  const testedPackages = new Set([...required].map((name) => name.split(':')[0]));
  const passed = new Set();
  const seenPackages = new Set();
  const packagePasses = new Set();
  const noTestPackages = new Set();
  const noTestOutput = new Set(events.filter((event) => event.Action === 'output' &&
    !event.Test && /\[no test files\]/.test(event.Output ?? '')).map((event) => event.Package));
  let testPassCount = 0;
  let skipCount = 0;
  for (const event of events) {
    const name = `${event.Package}:${event.Test}`;
    if (!expected.has(event.Package)) problems.push('unexpected package');
    seenPackages.add(event.Package);
    if (event.Action === 'fail') problems.push(`failed ${name}`);
    if (event.Action === 'output' && /\(cached\)/.test(event.Output ?? '')) problems.push('cached result');
    if (event.Action === 'skip') {
      if (!event.Test && !testedPackages.has(event.Package) && noTestOutput.has(event.Package))
        noTestPackages.add(event.Package);
      else {
        skipCount++;
        if (name !== replacement) problems.push(`skipped ${name}`);
      }
    }
    if (event.Action === 'pass') {
      if (event.Test) { passed.add(name); testPassCount++; }
      else packagePasses.add(event.Package);
    }
  }
  if (!expected.size) problems.push('empty package list');
  for (const pkg of expected) {
    if (!seenPackages.has(pkg)) problems.push(`missing package ${pkg}`);
    if (testedPackages.has(pkg) && !packagePasses.has(pkg)) problems.push(`missing package pass ${pkg}`);
    if (!testedPackages.has(pkg) && !packagePasses.has(pkg) && !noTestPackages.has(pkg))
      problems.push(`missing package result ${pkg}`);
  }
  for (const name of required) if (!passed.has(name) && name !== replacement) problems.push(`missing required pass ${name}`);
  if (replacement && !events.some((event) => event.Action === 'skip' &&
      `${event.Package}:${event.Test}` === replacement)) problems.push('missing named capability skip');
  return { passed: problems.length === 0, requiredCount: required.size, testPassCount,
    skipCount, noTestPackageCount: noTestPackages.size, packagePassCount: packagePasses.size,
    expectedPackages: expected.size, problems };
}

const browserBatches = [
  ['admin.spec.ts', 'admin', true],
  ['node-chains.spec.ts', 'admin', true],
  ['admin-subscription-domains.spec.ts', 'admin', true],
  ['user-portal.spec.ts', 'user-portal', false],
  ['user-subscription-domains.spec.ts', 'user-portal', false],
  [null, 'admin', false],
];
const mockSpecs = ['managed-chain-lifecycle.admin.spec.ts', 'managed-node-chains.admin.spec.ts', 'node-endpoints.admin.spec.ts'];

function checkBrowser(reports, inventoryFiles) {
  const required = [...browserBatches.slice(0, -1).map(([file]) => file), ...mockSpecs];
  const problems = [];
  if (!reports.length || inventoryFiles.length !== required.length ||
      inventoryFiles.some((file) => !required.includes(file)) || new Set(inventoryFiles).size !== required.length)
    problems.push('browser inventory or report count mismatch');
  const assigned = new Set();
  let total = 0, passedTests = 0, skipped = 0, failed = 0;
  for (const [index, report] of reports.entries()) {
    if (!report || !Array.isArray(report.suites) || !report.stats ||
        !Number.isInteger(report.stats.expected) || !Number.isInteger(report.stats.unexpected) ||
        !Number.isInteger(report.stats.skipped)) throw new Error(`malformed browser report ${index}`);
    const files = new Set();
    let setupCount = 0, reportPasses = 0;
    const visit = (suite, parent = '') => {
      if (!suite || (suite.specs !== undefined && !Array.isArray(suite.specs)) ||
          (suite.suites !== undefined && !Array.isArray(suite.suites)))
        throw new Error(`malformed browser suite ${index}`);
      const file = suite.file ? suite.file.split(/[\\/]/).at(-1) : parent;
      for (const spec of suite.specs ?? []) {
        if (!Array.isArray(spec.tests) || !spec.tests.length) throw new Error(`malformed browser spec ${index}`);
        for (const test of spec.tests) {
          if (typeof test.projectName !== 'string' || typeof test.status !== 'string' ||
              !Array.isArray(test.results)) throw new Error(`malformed browser test ${index}`);
          total++;
          if (file === 'auth.setup.ts') setupCount++;
          else files.add(file);
          const expectedProject = file === 'auth.setup.ts' ? 'setup' :
            mockSpecs.includes(file) ? 'admin' : browserBatches.find(([name]) => name === file)?.[1];
          if (!expectedProject || test.projectName !== expectedProject)
            problems.push(`unexpected browser assignment ${index}: ${file}`);
          if (test.status === 'skipped') skipped++;
          else if (test.status !== 'expected' || test.results.length !== 1 || test.results[0]?.status !== 'passed') failed++;
          else { passedTests++; reportPasses++; }
        }
      }
      for (const child of suite.suites ?? []) visit(child, file);
    };
    for (const suite of report.suites) visit(suite);
    const mock = [...files].some((file) => mockSpecs.includes(file));
    const admin = [...files].some((file) => browserBatches.slice(0, 3).some(([name]) => name === file));
    const portal = [...files].some((file) => browserBatches.slice(3, 5).some(([name]) => name === file));
    if (!files.size || (mock && (files.size !== mockSpecs.length || mockSpecs.some((file) => !files.has(file)))) ||
        (admin && portal) || (mock && (admin || portal))) problems.push(`mixed or missing browser lane ${index}`);
    if (setupCount !== (admin ? 1 : 0)) problems.push(`missing or unexpected auth setup ${index}`);
    for (const file of files) {
      if (assigned.has(file)) problems.push(`duplicate spec assignment ${file}`);
      assigned.add(file);
    }
    if (report.stats.expected !== reportPasses || report.stats.unexpected !== 0 || report.stats.skipped !== 0)
      problems.push(`unexpected browser stats ${index}`);
  }
  if (assigned.size !== required.length || required.some((file) => !assigned.has(file))) problems.push('missing required browser spec');
  const passed = problems.length === 0 && total > 0 && total === passedTests && skipped === 0 && failed === 0;
  return { passed, total, passedTests, skipped, failed, files: assigned.size, requiredFiles: required.length, problems };
}

function browserSelfTest() {
  const report = (file, project, setup = false) => {
    const test = (projectName) => ({ projectName, status: 'expected', results: [{ status: 'passed' }] });
    const suites = [{ file, specs: [{ tests: [test(project)] }], suites: [] }];
    if (setup) suites.push({ file: 'auth.setup.ts', specs: [{ tests: [test('setup')] }], suites: [] });
    return { suites, stats: { expected: setup ? 2 : 1, unexpected: 0, skipped: 0 } };
  };
  const reports = browserBatches.map(([file, project, setup]) => file ? report(file, project, setup) : {
    suites: mockSpecs.map((name) => report(name, project).suites[0]),
    stats: { expected: mockSpecs.length, unexpected: 0, skipped: 0 },
  });
  const inventoryFiles = [...browserBatches.slice(0, -1).map(([file]) => file), ...mockSpecs];
  assert.equal(checkBrowser(reports, inventoryFiles).passed, true);
  const actualShape = structuredClone(reports);
  for (const item of actualShape) for (const suite of item.suites) delete suite.suites;
  actualShape[0].suites = [{ file: 'admin.spec.ts', suites: actualShape[0].suites }];
  assert.equal(checkBrowser(actualShape, inventoryFiles).passed, true);
  const grouped = structuredClone(reports);
  grouped[0].suites.push(grouped[1].suites[0]);
  grouped[0].stats.expected++;
  grouped.splice(1, 1);
  assert.equal(checkBrowser(grouped, inventoryFiles).passed, true);
  assert.equal(checkBrowser([...reports, reports[0]], inventoryFiles).passed, false);
  const altered = (index, change) => reports.map((item, i) => i === index ? change(structuredClone(item)) : item);
  for (const bad of [
    altered(0, (item) => { item.suites.pop(); return item; }),
    altered(1, (item) => { item.suites[0].file = 'admin.spec.ts'; return item; }),
    altered(2, (item) => { item.suites.pop(); return item; }),
    altered(3, (item) => { item.suites.push(report('auth.setup.ts', 'setup').suites[0]); return item; }),
    altered(4, (item) => { item.suites[0].specs[0].tests[0].status = 'skipped'; return item; }),
    altered(5, (item) => { item.stats.unexpected = 1; return item; }),
    altered(5, (item) => { item.suites[0].file = 'admin.spec.ts'; return item; }),
    altered(3, (item) => { item.suites[0].specs[0].tests[0].projectName = 'admin'; return item; }),
    altered(0, (item) => { item.suites[0].specs[0].tests[0].results[0].status = 'failed'; return item; }),
  ]) assert.equal(checkBrowser(bad, inventoryFiles).passed, false);
  assert.equal(checkBrowser(reports, inventoryFiles.slice(1)).passed, false);
  assert.throws(() => checkBrowser(altered(0, () => ({})), inventoryFiles), /malformed/);
  assert.throws(() => checkBrowser(altered(0, (item) => { delete item.stats.expected; return item; }), inventoryFiles), /malformed/);
  for (const key of ['specs', 'suites']) for (const value of [null, {}, 'invalid'])
    assert.throws(() => checkBrowser(altered(0, (item) => { item.suites[0][key] = value; return item; }), inventoryFiles), /malformed browser suite/);
}

function selfTest() {
  browserSelfTest();
  const pkg = `${prefix}node-agent/internal/relay`;
  const name = `${pkg}:TestExample`;
  const events = [{ Package: pkg, Action: 'start' }, { Package: pkg, Test: 'TestExample', Action: 'pass' },
    { Package: pkg, Action: 'pass' }];
  const good = () => check(events, [pkg], new Set([name]));
  assert.equal(good().passed, true);
  for (const [event, reason] of [
    [{ Package: pkg, Test: 'TestExample/child', Action: 'skip' }, /skipped/],
    [{ Package: pkg, Test: 'TestExample', Action: 'fail' }, /failed/],
    [{ Package: pkg, Action: 'output', Output: 'ok (cached)' }, /cached/],
    [{ Package: 'other', Action: 'pass' }, /unexpected package/],
  ]) assert.match(check([...events, event], [pkg], new Set([name])).problems.join(' '), reason);
  assert.match(check(events.slice(1), [pkg], new Set([name, `${pkg}:TestMissing`])).problems.join(' '), /missing required pass/);
  assert.match(check(events.slice(0, 2), [pkg], new Set([name])).problems.join(' '), /missing package pass/);
  assert.match(check(events, [pkg, 'absent'], new Set([name])).problems.join(' '), /missing package/);
  assert.match(check([{ Package: pkg, Action: 'skip' }], [pkg], new Set()).problems.join(' '), /skipped/);
  assert.equal(check([...events, { Package: pkg, Test: 'TestExample', Action: 'skip' }], [pkg], new Set([name]), name).passed, true);
  assert.throws(() => parse(''), /empty/);
  assert.throws(() => parse('{}\n'), /invalid/);
  assert.throws(() => parse('{\n'), /malformed/);
  assert.throws(() => parse(JSON.stringify(events[0])), /truncated/);
  for (const lane of Object.keys(modulePaths)) inventory(lane);
  console.log('PHS-031 checker self-test PASS');
}

try {
  if (process.argv.length === 3 && process.argv[2] === '--self-test') selfTest();
  else if (process.argv[2] === 'browser') {
    const [, , , specDir, destination, ...paths] = process.argv;
    if (!specDir || !destination || !paths.length)
      throw new Error('Usage: check-phs031-results.mjs browser <spec-dir> <NEW_JSON> <real-reports...> <mock-report>');
    const reports = paths.map((file) => JSON.parse(readFileSync(file, 'utf8')));
    const inventoryFiles = readdirSync(specDir).filter((file) => file.endsWith('.spec.ts'));
    const result = checkBrowser(reports, inventoryFiles);
    writeFileSync(destination, `${JSON.stringify(result, null, 2)}\n`, { flag: 'wx' });
    console.log(`Browser: ${result.passedTests}/${result.total} passes, ${result.files}/${result.requiredFiles} spec files, ${result.problems.length} problems`);
    if (!result.passed) process.exitCode = 1;
  }
  else {
    const [, , lane, stream, list, destination, nftStream] = process.argv;
    if (!modulePaths[lane] || !stream || !list || !destination ||
        (lane === 'node' ? !nftStream || process.argv.length !== 7 : process.argv.length !== 6))
      throw new Error('Usage: check-phs031-results.mjs <api|pkg|node> <jsonl> <go-list> <NEW_JSON> [nft-jsonl] | --self-test');
    const packages = readFileSync(list, 'utf8').trim().split('\n');
    const required = inventory(lane);
    let replacement;
    let nft;
    if (lane === 'node') {
      const nftPkg = nftName.split(':')[0];
      nft = check(parse(readFileSync(nftStream, 'utf8')), [nftPkg], new Set([nftName]));
      if (nft.passed) replacement = nftName;
    }
    const result = check(parse(readFileSync(stream, 'utf8')), packages, required, replacement);
    if (nft && !nft.passed) result.problems.push('nft capability lane failed');
    result.passed = result.problems.length === 0;
    if (nft) result.nft = nft;
    writeFileSync(destination, `${JSON.stringify(result, null, 2)}\n`, { flag: 'wx' });
    console.log(`${lane}: ${result.testPassCount} passes, ${result.skipCount} capability skips, ${result.packagePassCount}/${result.expectedPackages} packages, ${result.problems.length} problems`);
    if (!result.passed) {
      for (const problem of result.problems) console.error(problem);
      process.exitCode = 1;
    }
  }
} catch (error) {
  console.error(`PHS-031 checker: ${error.message}`);
  process.exitCode = 1;
}
