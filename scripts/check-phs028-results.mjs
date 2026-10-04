#!/usr/bin/env node
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import assert from 'node:assert/strict';

const root = resolve(import.meta.dirname, '..');
const assigned = [
  'api-server/internal/database/schema.go',
  'api-server/internal/database/schema_node_chains_migration_test.go',
  'api-server/internal/handlers/admin_node.go',
  'api-server/internal/handlers/admin_node_chain_create.go',
  'api-server/internal/handlers/admin_node_chain_create_http_integration_test.go',
  'api-server/internal/handlers/node_agent.go',
  'api-server/internal/services/xray_config.go',
];

function git(...args) {
  return execFileSync('git', args, { cwd: root, encoding: 'utf8' }).trimEnd();
}

function captureBaseline(dir) {
  if (existsSync(dir)) throw new Error(`baseline already exists: ${dir}`);
  const untracked = git('ls-files', '--others', '--exclude-standard').split('\n')
    .filter((path) => path && path !== 'scripts/check-phs028-results.mjs' && !path.startsWith('.omo/start-work/evidence/PHS-028/'));
  const status = git('status', '--porcelain=v1', '-uall').split('\n')
    .filter((line) => line && !line.endsWith(' scripts/check-phs028-results.mjs') && !line.includes('.omo/start-work/evidence/PHS-028/'));
  const hashes = Object.fromEntries(assigned.map((path) => [path, {
    sha256: createHash('sha256').update(readFileSync(join(root, path))).digest('hex'),
    bytes: statSync(join(root, path)).size,
  }]));
  const record = {
    head: git('rev-parse', 'HEAD'),
    status,
    trackedDiffSummary: git('diff', '--stat'),
    untracked,
    assignedFileHashes: hashes,
    excluded: ['scripts/check-phs028-results.mjs (baseline capture tool)', '.omo/start-work/evidence/PHS-028/ (output)', 'file contents and environment values'],
  };
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, 'worktree-manifest.json'), `${JSON.stringify(record, null, 2)}\n`, { flag: 'wx' });
  console.log(`Captured HEAD, ${status.length} status entries, ${untracked.length} untracked paths, ${assigned.length} hashes`);
}

function checkCommand(command, events) {
  const problems = [];
  if (command.exitCode !== 0) problems.push(`${command.name}: exit ${command.exitCode}`);
  if (!Array.isArray(command.required) || command.required.length === 0) {
    problems.push(`${command.name}: no required tests declared`);
    return problems;
  }
  for (const { package: pkg, test } of command.required) {
    if (!pkg || !test || test.includes('/')) {
      problems.push(`${command.name}: invalid required package/test`);
      continue;
    }
    const related = events.filter((event) => event.Package === pkg &&
      (event.Test === test || event.Test?.startsWith(`${test}/`)));
    if (related.some((event) => event.Action === 'skip')) problems.push(`${command.name}: skipped ${pkg} ${test} or subtest`);
    if (related.some((event) => event.Action === 'fail')) problems.push(`${command.name}: failed ${pkg} ${test} or subtest`);
    if (!related.some((event) => event.Test === test && event.Action === 'pass')) {
      problems.push(`${command.name}: missing pass ${pkg} ${test}`);
    }
    const packageEvents = events.filter((event) => event.Package === pkg && !event.Test);
    if (!packageEvents.some((event) => event.Action === 'pass')) problems.push(`${command.name}: missing package pass ${pkg}`);
    if (packageEvents.some((event) => event.Action === 'fail')) problems.push(`${command.name}: package failed ${pkg}`);
    if (packageEvents.some((event) => event.Action === 'output' && /\(cached\)/.test(event.Output ?? ''))) {
      problems.push(`${command.name}: cached package ${pkg}`);
    }
  }
  return problems;
}

function selfTest() {
  const command = { name: 'fixture', exitCode: 0, required: [{ package: 'example/pkg', test: 'TestRequired' }] };
  const pass = [
    { Package: 'example/pkg', Test: 'TestRequired', Action: 'pass' },
    { Package: 'example/pkg', Action: 'pass' },
  ];
  assert.deepEqual(checkCommand(command, pass), []);
  assert.match(checkCommand(command, pass.slice(1)).join(' '), /missing pass/);
  assert.match(checkCommand(command, [...pass, { Package: 'example/pkg', Test: 'TestRequired/child', Action: 'skip' }]).join(' '), /skipped/);
  assert.match(checkCommand(command, [...pass, { Package: 'example/pkg', Action: 'output', Output: 'ok example/pkg (cached)\n' }]).join(' '), /cached/);
  assert.match(checkCommand({ ...command, exitCode: 1 }, pass).join(' '), /exit 1/);
  assert.match(checkCommand({ ...command, required: [] }, pass).join(' '), /no required tests/);
  console.log('Checker self-test PASS: passing, missing, skipped subtest, cached, nonzero exit, empty requirements');
}

function checkManifest(file) {
  const manifest = JSON.parse(readFileSync(file, 'utf8'));
  if (!Array.isArray(manifest.commands) || manifest.commands.length === 0) throw new Error('no commands recorded');
  const problems = [];
  for (const command of manifest.commands) {
    if (!command.name || !command.file || resolve(join(file, '..'), command.file) === file) {
      problems.push('invalid command evidence path/name');
      continue;
    }
    let events;
    try {
      const lines = readFileSync(resolve(join(file, '..'), command.file), 'utf8').trim().split('\n');
      events = lines.map((line) => JSON.parse(line));
    } catch (error) {
      problems.push(`${command.name}: unreadable or invalid JSON evidence: ${error.message}`);
      continue;
    }
    problems.push(...checkCommand(command, events));
  }
  if (problems.length) throw new Error(problems.join('\n'));
  console.log(`PASS: ${manifest.commands.length} commands and all named required tests`);
}

try {
  if (process.argv[2] === '--capture-baseline' && process.argv.length === 4) {
    captureBaseline(resolve(process.argv[3]));
  } else if (process.argv[2] === '--self-test' && process.argv.length === 3) {
    selfTest();
  } else if (process.argv.length === 3) {
    checkManifest(resolve(process.argv[2]));
  } else {
    throw new Error('Usage: node scripts/check-phs028-results.mjs --self-test | --capture-baseline <new-directory> | <manifest.json>');
  }
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
}
