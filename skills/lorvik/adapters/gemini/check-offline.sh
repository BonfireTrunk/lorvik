#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
set -eu

adapter_tmp=$(mktemp -d)
export LORVIK_CHECK_DIR="$adapter_tmp"
export LORVIK_CHECK_HOOK="$PWD/skills/lorvik/adapters/gemini/lorvik-before-agent.mjs"
mkdir "$adapter_tmp/bin"
cat >"$adapter_tmp/bin/lorvik" <<'JS'
#!/usr/bin/env node
const assert = require('node:assert/strict');
const fs = require('node:fs');
const args = process.argv.slice(2);
const base = ['--json', 'inbox', '--peek', '--session', 'gemini-lorvik-1'];
assert.equal(process.env.LORVIK_CONFIG, '/tmp/lorvik-hook-fixture.toml');
fs.appendFileSync(process.env.LORVIK_CHECK_DIR + '/calls.jsonl', JSON.stringify(args) + '\n');
if (process.env.LORVIK_CHECK_MODE === 'failure') {
  assert.deepEqual(args, base);
  process.stderr.write('fixture failure\n');
  process.exit(7);
}
if (process.env.LORVIK_CHECK_MODE === 'invalid') {
  console.log(JSON.stringify({ events: {}, cursor: '9', remaining: 0 }));
} else if (process.env.LORVIK_CHECK_MODE === 'bounded') {
  console.log(JSON.stringify({ events: Array.from({ length: 300 }, (_, i) => ({
    id: String(i), wake: true, payload: { title: 'event-' + i, comment: 'é"\\'.repeat(400) },
  })), cursor: '300', remaining: 0 }));
} else if (process.env.LORVIK_CHECK_MODE === 'oversized-page') {
  console.log(JSON.stringify({ events: [{ id: '9', wake: true, payload: { comment: 'x'.repeat(3 * 1024 * 1024) } }], cursor: '9', remaining: 0 }));
} else if (process.env.LORVIK_CHECK_MODE === 'empty') {
  console.log(JSON.stringify({ events: [], cursor: '9', remaining: 0 }));
} else if (args.length === base.length) {
  assert.deepEqual(args, base);
  console.log(JSON.stringify({ events: [{ id: '8', wake: false }], cursor: '8', remaining: 1 }));
} else {
  assert.deepEqual(args, [...base, '--from', '8']);
  console.log(JSON.stringify({ events: [{ id: '9', wake: true, payload: { title: 'Fix login' } }], cursor: '9', remaining: 0 }));
}
JS
chmod 700 "$adapter_tmp/bin/lorvik"
cat >"$adapter_tmp/check.mjs" <<'JS'
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
const directory = process.env.LORVIK_CHECK_DIR;
const input = { cwd: process.cwd(), session_id: 'ignored-native-id', hook_event_name: 'BeforeAgent', prompt: 'continue' };
const env = { ...process.env, PATH: directory + '/bin:' + process.env.PATH,
  LORVIK_CONFIG: '/tmp/lorvik-hook-fixture.toml', LORVIK_SESSION: 'gemini-lorvik-1' };
function invoke(mode, event = input, override = {}) {
  const run = spawnSync(process.execPath, [process.env.LORVIK_CHECK_HOOK], {
    input: JSON.stringify(event), encoding: 'utf8', timeout: 20000,
    env: { ...env, LORVIK_CHECK_MODE: mode, ...override },
  });
  assert.equal(run.error, undefined);
  assert.equal(run.status, 0);
  assert.equal(run.stderr, '');
  return JSON.parse(run.stdout);
}
const delivered = invoke('pagination');
assert.equal(delivered.hookSpecificOutput.hookEventName, 'BeforeAgent');
assert.match(delivered.hookSpecificOutput.additionalContext, /Fix login/);
assert.doesNotMatch(delivered.hookSpecificOutput.additionalContext, /"id":"8"/);
assert.match(delivered.hookSpecificOutput.additionalContext, /did not advance/);
assert.equal(readFileSync(directory + '/calls.jsonl', 'utf8').trim().split('\n').length, 2);
assert.deepEqual(invoke('empty'), {});
assert.match(invoke('invalid').systemMessage, /invalid inbox shape/);
assert.match(invoke('failure').systemMessage, /fixture failure/);
const bounded = invoke('bounded').hookSpecificOutput.additionalContext;
assert.match(bounded, /event-299/);
assert.doesNotMatch(bounded, /"id":"0"/);
assert.match(bounded, /Dropped \d+ board event lines.*run lorvik inbox/);
assert.ok(Buffer.byteLength(bounded) < 70 * 1024);
assert.match(invoke('oversized-page').systemMessage, /exceeded the 2 MiB page limit; run lorvik inbox/);
const before = readFileSync(directory + '/calls.jsonl', 'utf8');
assert.deepEqual(invoke('pagination', { ...input, hook_event_name: 'AfterAgent' }), {});
assert.match(invoke('pagination', input, { LORVIK_SESSION: '' }).systemMessage, /set LORVIK_SESSION/);
assert.equal(readFileSync(directory + '/calls.jsonl', 'utf8'), before);
console.log('Hook JSON, explicit session/config, pagination, newest-event byte cap, oversized page, quiet, invalid data, failure and inactive gates passed.');
JS
node "$adapter_tmp/check.mjs"
