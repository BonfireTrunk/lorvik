// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import { mkdtemp, writeFile, readFile, rm } from 'node:fs/promises';
import { spawn } from 'node:child_process';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';
import { mock, test } from 'bun:test';

let messageNumber = 0;
// Hand-written host doubles: this test must not load a dsh installation or
// execute any source from the tagged Harness checkout.
mock.module('@deepseek-ai/dsh-llm', () => ({
  createUserMessage: input => Object.freeze({
    id: `fake-message-${++messageNumber}`,
    role: 'user',
    source: Object.freeze({ ...input.source }),
    content: Object.freeze(input.content.map(block => Object.freeze({ ...block }))),
  }),
}));
mock.module('@deepseek-ai/dsh-tools', () => ({ defineTool: definition => definition }));
const { apply } = await import('./index.js');

async function fixture(body, { exitOnEOF = true } = {}) {
  const dir = await mkdtemp(join(tmpdir(), 'lorvik-dsh-check-'));
  const saved = { ...process.env };
  const record = join(dir, 'calls.jsonl');
  await writeFile(join(dir, '.lorvik.toml'), '# fake binding\n');
  const eofHandler = exitOnEOF ? "process.stdin.resume(); process.stdin.on('end', () => process.exit(0));" : '';
  await writeFile(join(dir, 'lorvik'), `#!${process.execPath}\nimport fs from 'node:fs';\nfs.appendFileSync(${JSON.stringify(record)}, JSON.stringify({args:process.argv.slice(2), config:process.env.LORVIK_CONFIG, native:process.env.DSH_SESSION_ID, cwd:process.cwd(), pid:process.pid})+'\\n');\n${eofHandler}\n${body}\n`, { mode: 0o755 });
  process.env.PATH = dir + ':' + saved.PATH;
  process.env.LORVIK_CONFIG = join(dir, 'agent.toml');
  const effects = [];
  const scoped = [];
  const listeners = new Map();
  const warnings = [];
  const delivered = [];
  const pending = [];
  const newAgent = (id = 'native-check-session') => ({
    id, session: { header: { cwd: dir } },
    ctx: { effect: fn => { scoped.push(fn()); } },
    inbox: {
      get nextTurn() { return pending; },
      remove: id => { const i = pending.findIndex(item => item.id === id); if (i < 0) return false; pending.splice(i, 1); return true; },
    },
    followup: message => { delivered.push(message); pending.push(message); },
  });
  const agent = newAgent();
  let tool;
  const commands = new Map();
  let live = agent;
  apply({
    agents: { get: () => live },
    tools: { register: value => { tool = value; } },
    commands: { register: value => commands.set(value.name, value) },
    logger: { warn: message => warnings.push(message) },
    on: (name, fn) => listeners.set(name, fn),
    effect: fn => { effects.push(fn()); },
  });
  process.env = saved;
  return {
    dir, agent, pending, delivered, warnings, listeners, scoped, commands, newAgent,
    run: args => tool.execute(args, { agent }),
    runAs: (args, subject) => tool.execute(args, { agent: subject }),
    calls: async () => {
      try { return (await readFile(record, 'utf8')).trim().split('\n').filter(Boolean).map(JSON.parse); }
      catch (error) { if (error.code === 'ENOENT') return []; throw error; }
    },
    removeLive: () => { live = undefined; },
    setLive: subject => { live = subject; },
    async close() {
      await Promise.all(effects.map(fn => fn()));
      await rm(dir, { recursive: true, force: true });
    },
  };
}

async function eventually(fn) {
  for (let i = 0; i < 150; i++) {
    if (await fn()) return;
    await delay(10);
  }
  assert.fail('delivery boundary was not reached within 1.5 seconds');
}

test('explicit arm batches literal lines at followup boundary and disarm cancels owned queue', { timeout: 5000 }, async () => {
  const f = await fixture(`fs.writeSync(1, 'now ana comm'); setTimeout(() => fs.writeSync(1, 'ented on #42 \\"Fix login\\"\\nnow $(touch should-not-exist)\\n'), 15); setInterval(() => {}, 1000);`);
  try {
    assert.equal((await f.calls()).length, 0);
    await f.run({ action: 'arm', session: 'brisk-otter-abcd' });
    await f.run({ action: 'arm', session: 'brisk-otter-abcd' });
    await eventually(() => f.delivered.length === 1);
    const [call] = await f.calls();
    assert.deepEqual(call.args, ['inbox', '--follow', '--exit-on-eof', '--session', 'brisk-otter-abcd']);
    assert.equal(call.native, f.agent.id);
    assert.equal(call.config, join(f.dir, 'agent.toml'));
    assert.equal(call.cwd, f.dir);
    assert.equal((await f.calls()).length, 1);
    assert.match(f.delivered[0].content[0].text, /now ana commented on #42/);
    assert.match(f.delivered[0].content[0].text, /\$\(touch should-not-exist\)/);
    assert.equal(f.delivered[0].role, 'user');
    assert.equal(f.delivered[0].source.kind, 'lorvik-wake');
    assert.equal(typeof f.delivered[0].source.listenerId, 'string');
    assert.ok(f.delivered[0].id);
    assert.ok(Object.isFrozen(f.delivered[0]));
    await assert.rejects(readFile(join(f.dir, 'should-not-exist')), { code: 'ENOENT' });
    f.pending.push({ id: 'human-message' });
    await f.run({ action: 'disarm' });
    assert.deepEqual(f.pending, [{ id: 'human-message' }]);
    assert.throws(() => process.kill(call.pid, 0), { code: 'ESRCH' });
    assert.equal(f.warnings.length, 0);
  } finally { await f.close(); }
});

test('self-exit reports once, flushes complete lines, and does not restart on arm', { timeout: 5000 }, async () => {
  const f = await fixture(`fs.writeSync(1, 'now finished\\n'); fs.writeSync(2, 'fake rejection\\n'); process.exit(3);`);
  try {
    await f.run({ action: 'arm', session: 'test-session' });
    await eventually(() => f.warnings.length === 1);
    assert.equal(f.delivered.length, 1);
    assert.match(f.warnings[0], /fake rejection/);
    assert.match(await f.run({ action: 'arm', session: 'test-session' }), /^Stopped/);
    assert.equal((await f.calls()).length, 1);
    assert.equal(f.warnings.length, 1);
  } finally { await f.close(); }
});

test('agent scope disposal drops the pending batch and kills listener', { timeout: 5000 }, async () => {
  const f = await fixture(`fs.writeSync(1, 'now drop this\\n'); setInterval(() => {}, 1000);`);
  try {
    await f.run({ action: 'arm', session: 'test-session' });
    await eventually(async () => (await f.calls()).length === 1);
    await Promise.all(f.scoped.map(fn => fn()));
    await delay(300);
    assert.equal(f.delivered.length, 0);
    const [call] = await f.calls();
    assert.throws(() => process.kill(call.pid, 0), { code: 'ESRCH' });
    assert.equal(f.warnings.length, 0);
  } finally { await f.close(); }
});

test('same-id recreation cannot overlap a listener still stopping after disposal', { timeout: 5000 }, async () => {
  const f = await fixture(`process.stdin.resume(); process.stdin.once('end', () => setTimeout(() => process.exit(0), 250)); setInterval(() => {}, 1000);`, { exitOnEOF: false });
  try {
    await f.run({ action: 'arm', session: 'test-session' });
    await eventually(async () => (await f.calls()).length === 1);
    f.listeners.get('agent/disposed')({ agent: f.agent });
    const replacement = f.newAgent();
    f.setLive(replacement);
    assert.match(await f.runAs({ action: 'arm', session: 'test-session' }, replacement), /^Stopped/);
    assert.equal((await f.calls()).length, 1);
    await delay(300);
    await f.runAs({ action: 'arm', session: 'test-session' }, replacement);
    await eventually(async () => (await f.calls()).length === 2);
    f.listeners.get('agent/disposed')({ agent: f.agent });
    assert.match(await f.runAs({ action: 'arm', session: 'test-session' }, replacement), /^Armed/);
  } finally { await f.close(); }
});

test('unregistered agent cannot receive a followup', { timeout: 5000 }, async () => {
  const f = await fixture(`fs.writeSync(1, 'now stale\\n'); setInterval(() => {}, 1000);`);
  try {
    await f.run({ action: 'arm', session: 'test-session' });
    f.removeLive();
    await eventually(async () => (await f.calls()).length === 1);
    await delay(300);
    assert.equal(f.delivered.length, 0);
  } finally { await f.close(); }
});

test('one oversized unterminated stdout frame yields one bounded recovery notice', { timeout: 5000 }, async () => {
  const f = await fixture(`fs.writeSync(1, 'x'.repeat(70 * 1024)); process.exit(0);`);
  try {
    await f.run({ action: 'arm', session: 'test-session' });
    await eventually(() => f.delivered.length === 1);
    assert.match(f.delivered[0].content[0].text, /Notice: 1 board event line\(s\) were dropped/);
    assert.match(f.delivered[0].content[0].text, /Run lorvik --session test-session inbox/);
  } finally { await f.close(); }
});

test('short unterminated final stdout frame is dropped rather than promoted', { timeout: 5000 }, async () => {
  const f = await fixture(`fs.writeSync(1, 'partial board event'); process.exit(0);`);
  try {
    await f.run({ action: 'arm', session: 'test-session' });
    await eventually(() => f.delivered.length === 1);
    assert.match(f.delivered[0].content[0].text, /Notice: 1 board event line\(s\) were dropped/);
    assert.doesNotMatch(f.delivered[0].content[0].text, /partial board event/);
  } finally { await f.close(); }
});

test('many complete lines coalesce within 1 MiB, retaining newest lines with one notice', { timeout: 5000 }, async () => {
  const f = await fixture(`for (let i = 0; i < 24; i++) fs.writeSync(1, 'line-' + i + ':' + 'x'.repeat(48 * 1024) + '\\n'); setInterval(() => {}, 1000);`);
  try {
    await f.run({ action: 'arm', session: 'test-session' });
    await eventually(() => f.delivered.length === 1);
    const text = f.delivered[0].content[0].text;
    assert.ok(Buffer.byteLength(text, 'utf8') <= 1024 * 1024);
    assert.match(text, /Notice: [1-9][0-9]* board event line\(s\) were dropped/);
    assert.doesNotMatch(text, /line-0:/);
    assert.match(text, /line-23:/);
  } finally { await f.close(); }
});

test('production adapter keeps actual listener stdin alive until owner SIGKILL', { timeout: 5000 }, async () => {
  const dir = await mkdtemp(join(tmpdir(), 'lorvik-dsh-owner-'));
  const marker = join(dir, 'stdin-eof');
  const record = join(dir, 'listener.json');
  const ready = join(dir, 'owner-ready');
  const listener = join(dir, 'lorvik');
  const owner = join(dir, 'owner.mjs');
  let listenerPid;
  let ownerChild;
  try {
    await writeFile(join(dir, '.lorvik.toml'), '# fake binding\n');
    await writeFile(listener, `#!${process.execPath}\nimport { writeFileSync } from 'node:fs';\nwriteFileSync(${JSON.stringify(record)}, JSON.stringify({ args: process.argv.slice(2), pid: process.pid }));\nprocess.stdin.resume();\nprocess.stdin.on('end', () => { writeFileSync(${JSON.stringify(marker)}, 'eof'); process.exit(0); });\nsetInterval(() => {}, 1000);\n`, { mode: 0o755 });
    await writeFile(owner, `import { mock } from 'bun:test';\nimport { writeFileSync } from 'node:fs';\nimport { join } from 'node:path';\nmock.module('@deepseek-ai/dsh-llm', () => ({ createUserMessage: input => Object.freeze({ id: 'owner-message', role: 'user', source: Object.freeze({ ...input.source }), content: Object.freeze(input.content) }) }));\nmock.module('@deepseek-ai/dsh-tools', () => ({ defineTool: definition => definition }));\nconst { apply } = await import(process.argv[2]);\nconst dir = process.argv[3];\nprocess.env.PATH = dir + ':' + process.env.PATH;\nprocess.env.LORVIK_CONFIG = join(dir, 'agent.toml');\nconst pending = []; let tool; const agent = { id: 'owner-session', session: { header: { cwd: dir } }, ctx: { effect: callback => callback() }, inbox: { get nextTurn() { return pending; }, remove: () => false }, followup: message => pending.push(message) };\napply({ agents: { get: () => agent }, tools: { register: value => { tool = value; } }, commands: { register: () => {} }, logger: { warn: () => {} }, on: () => {}, effect: callback => callback() });\nawait tool.execute({ action: 'arm', session: 'owner-check' }, { agent });\nwriteFileSync(process.argv[4], 'ready');\nsetInterval(() => {}, 1000);\n`);
    ownerChild = spawn(process.execPath, [owner, pathToFileURL(resolve('skills/lorvik/adapters/deepseek/index.js')).href, dir, ready], { stdio: 'ignore' });
    await eventually(async () => (await readFile(ready, 'utf8').catch(() => '')) === 'ready');
    await eventually(async () => Boolean(await readFile(record, 'utf8').catch(() => '')));
    const launched = JSON.parse(await readFile(record, 'utf8'));
    listenerPid = launched.pid;
    assert.deepEqual(launched.args, ['inbox', '--follow', '--exit-on-eof', '--session', 'owner-check']);
    await delay(50);
    assert.doesNotThrow(() => process.kill(listenerPid, 0));
    assert.equal(await readFile(marker, 'utf8').catch(() => ''), '');
    process.kill(ownerChild.pid, 'SIGKILL');
    await eventually(async () => (await readFile(marker, 'utf8').catch(() => '')) === 'eof');
    await eventually(() => {
      try { process.kill(listenerPid, 0); return false; }
      catch (error) { return error.code === 'ESRCH'; }
    });
  } finally {
    if (ownerChild && ownerChild.exitCode === null && !ownerChild.killed) {
      try { process.kill(ownerChild.pid, 'SIGKILL'); } catch {}
    }
    if (ownerChild && ownerChild.exitCode === null) {
      await Promise.race([new Promise(resolveClose => ownerChild.once('close', resolveClose)), delay(200)]);
    }
    if (listenerPid) { try { process.kill(listenerPid, 'SIGKILL'); } catch {} }
    await rm(dir, { recursive: true, force: true });
  }
});

test('quiet listener causes no model call and plugin cleanup stops it', { timeout: 5000 }, async () => {
  const f = await fixture('setInterval(() => {}, 1000);');
  let call;
  try {
    await assert.rejects(f.run({ action: 'arm', session: 'bad;label' }), /session label/);
    const result = f.commands.get('lorvik-listen').handler({ agent: f.agent, rawInput: ' test-session' });
    assert.equal(result.kind, 'success');
    await eventually(async () => (await f.calls()).length === 1);
    [call] = await f.calls();
    await delay(300);
    assert.equal(f.delivered.length, 0);
  } finally { await f.close(); }
  assert.throws(() => process.kill(call.pid, 0), { code: 'ESRCH' });
});
