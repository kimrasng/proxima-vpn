'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const { check, required } = require('./check-results');

const records = required.map(name => ({ kind: 'assertion', name, status: 'pass' }));
const end = { kind: 'end', name: 'complete', status: 'pass' };
const stream = rows => rows.map(row => JSON.stringify(row)).join('\n') + '\n';

test('accepts exactly the complete inventory', () => {
  assert.deepEqual(check(stream([...records, end])), { status: 'pass', assertions: required.length });
});
for (const [name, data] of [
  ['missing', [...records.slice(1), end]],
  ['duplicate', [...records, records[0], end]],
  ['failed', [{ ...records[0], status: 'fail' }, ...records.slice(1), end]],
  ['skipped', [{ ...records[0], status: 'skip' }, ...records.slice(1), end]],
  ['missing end', records],
  ['extra after end', [...records, end, records[0]]],
  ['credential field', [{ ...records[0], api_key: 'redacted' }, ...records.slice(1), end]],
  ['credential value', [{ ...records[0], name: 'vless://secret' }, ...records.slice(1), end]],
]) {
  test(`rejects ${name}`, () => assert.throws(() => check(stream(data))));
}
test('rejects malformed and truncated input', () => {
  assert.throws(() => check(stream([...records, end]) + '{broken\n'));
  assert.throws(() => check(stream([...records, end]).trimEnd()));
});
