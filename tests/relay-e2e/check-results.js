#!/usr/bin/env node
'use strict';

const fs = require('node:fs');

const required = [
  'topology', 'profile_a', 'profile_b', 'agents_ack', 'entry_forward_only',
  'tcp_a', 'tcp_b', 'udp_a', 'udp_b', 'unused_port', 'swapped_port_a',
  'swapped_port_b', 'cross_credentials_a', 'cross_credentials_b',
  'uuid_swap_a', 'uuid_swap_b', 'direct_a', 'direct_b',
  'legacy_api_409', 'legacy_protected_drop', 'legacy_fresh_denial',
  'legacy_recovery', 'api_fetch_lkg', 'invalid_nft_lkg',
];
const forbidden = /(?:uuid|token|password|secret|api.?key|private.?key|public.?key|short.?id|subscription|database.?url|credential|vless:\/\/|postgres:\/\/|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/i;

function check(text) {
  if (!text.endsWith('\n')) throw new Error('truncated result stream');
  const lines = text.trimEnd().split('\n');
  const seen = new Set();
  let ended = false;
  for (const line of lines) {
    let record;
    try { record = JSON.parse(line); } catch { throw new Error('malformed JSON record'); }
    if (!record || typeof record !== 'object' || Array.isArray(record) ||
        Object.keys(record).some(key => !['name', 'status', 'kind'].includes(key)) ||
        Object.entries(record).some(([key, value]) => key !== 'name' && forbidden.test(`${key}:${value}`)))
      throw new Error('malformed or sensitive record');
    if (record.kind === 'end') {
      if (ended || record.status !== 'pass' || record.name !== 'complete' ||
          Object.keys(record).length !== 3) throw new Error('invalid end record');
      ended = true;
      continue;
    }
    if (ended || record.kind !== 'assertion' || record.status !== 'pass' ||
        !required.includes(record.name) || seen.has(record.name) ||
        Object.keys(record).length !== 3) throw new Error('unexpected assertion');
    seen.add(record.name);
  }
  if (!ended || seen.size !== required.length) throw new Error('incomplete run');
  return { status: 'pass', assertions: seen.size };
}

if (require.main === module) {
  try {
    const result = check(fs.readFileSync(process.argv[2], 'utf8'));
    process.stdout.write(`${JSON.stringify(result)}\n`);
  } catch (error) {
    process.stderr.write(`result check failed: ${error.message}\n`);
    process.exitCode = 1;
  }
}
module.exports = { check, required };
