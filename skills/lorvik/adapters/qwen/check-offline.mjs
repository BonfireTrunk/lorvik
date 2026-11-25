// SPDX-License-Identifier: Apache-2.0
// Isolated adapter boundary checks: no Qwen, Lorvik, network service or model.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createHash, randomUUID } from "node:crypto";
import { mkdtempSync, mkdirSync, readFileSync, statSync, writeFileSync, existsSync } from "node:fs";
import { createConnection, createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { setTimeout as delay } from "node:timers/promises";
import { PassThrough } from "node:stream";
import { boundedLines, limits, PendingBatch } from "./delivery-buffer.mjs";

assert.equal(process.platform, "linux", "The bridge intentionally supports Linux only.");
const adapter = fileURLToPath(new URL("./lorvik-wake.ts", import.meta.url));
const root = mkdtempSync(join(tmpdir(), "lorvik-qwen-check-"));
const fake = join(root, "lorvik");
writeFileSync(fake, `#!/usr/bin/env node
const fs = require('node:fs');
const assert = require('node:assert/strict');
assert.deepEqual(process.argv.slice(2), ['inbox', '--follow', '--exit-on-eof', '--session', 'qwen-fixture']);
assert.equal(process.env.LORVIK_CONFIG, '/not-a-real-lorvik-config');
fs.writeFileSync(process.env.FIXTURE_DIR + '/child.pid', String(process.pid));
process.stdin.on('data', () => assert.fail('adapter must never write listener stdin'));
process.stdin.on('end', () => {
  fs.writeFileSync(process.env.FIXTURE_DIR + '/eof', 'yes');
  if (process.env.FIXTURE_MODE !== 'stubborn') process.exit(0);
});
process.stdin.resume();
process.on('SIGTERM', () => {
  fs.writeFileSync(process.env.FIXTURE_DIR + '/terminated', 'yes');
  if (process.env.FIXTURE_MODE !== 'stubborn') process.exit(0);
});
if (process.env.FIXTURE_MODE === 'exit') {
  process.stdout.write('final event without newline', () => process.exit(0));
} else if (process.env.FIXTURE_MODE === 'stream') {
  let i = 0;
  const emit = setInterval(() => {
    process.stdout.write('event-' + i++ + '\\n');
    if (i === 36) clearInterval(emit);
  }, 300);
} else if (process.env.FIXTURE_MODE === 'overflow') {
  for (let i = 0; i < 12; i++) process.stdout.write('event-' + i + ' ' + '\\x00'.repeat(60000) + ' "\\\\😀\\n');
} else if (process.env.FIXTURE_MODE === 'oversized') {
  process.stdout.write('x'.repeat(1024 * 1024) + '\\nnewest-good-event\\n');
} else if (process.env.FIXTURE_MODE !== 'quiet' && process.env.FIXTURE_MODE !== 'stubborn') {
  process.stdout.write('now ana com');
  setTimeout(() => process.stdout.write('mented on #42 "Fix login"\\n$(touch sentinel)\\n'), 20);
}
setInterval(() => {}, 1000);
`, { mode: 0o700 });

async function until(predicate, message, timeout = 6000) {
  const deadline = Date.now() + timeout;
  while (!predicate()) {
    assert.ok(Date.now() < deadline, message);
    await delay(20);
  }
}

async function fixture(mode, status = "delivered", options = {}) {
  const directory = mkdtempSync(join(root, "case-"));
  const home = join(directory, "home");
  const runtime = join(directory, "run");
  mkdirSync(join(home, "sessions"), { recursive: true });
  mkdirSync(runtime);
  const socketPath = join(directory, "q.sock");
  const session = randomUUID();
  const token = "fixture-token";
  const frames = [];
  const arrivals = [];
  const readCounts = [];
  const sockets = new Set();
  const server = createServer((socket) => {
    sockets.add(socket);
    socket.on("close", () => sockets.delete(socket));
    socket.on("error", () => {});
    let text = "";
    let reads = 0;
    socket.setEncoding("utf8");
    socket.on("data", (chunk) => { text += chunk; reads++; });
    socket.on("end", () => {
      const [auth, frame] = text.trim().split("\n").map(JSON.parse);
      assert.deepEqual(auth, { msgV: 1, type: "auth", token });
      assert.equal(frame.toSessionId, session);
      assert.equal(frame.priority, "next");
      assert.equal(frame.message.role, "user");
      assert.ok(Buffer.byteLength(JSON.stringify(frame)) <= limits.frameBytes);
      frames.push(frame);
      arrivals.push(Date.now());
      readCounts.push(reads);
      const outcome = typeof status === "function" ? status(frame, frames.length - 1) : status;
      if (outcome === "missing") return;
      const reply = createConnection(frame.from);
      sockets.add(reply);
      reply.on("close", () => sockets.delete(reply));
      reply.on("error", () => {});
      reply.on("connect", () => {
        const receipt = {
          msgV: 1, msgId: randomUUID(), type: "control", action: "delivery_status",
          origMsgId: outcome === "rate-limited" ? randomUUID() : frame.msgId,
          status: outcome === "rate-limited" ? "dropped" : outcome,
          ...(outcome === "rate-limited" ? { dropReason: "rate-limited", droppedMsgIds: [frame.msgId] } : {}),
        };
        const payload = JSON.stringify({ msgV: 1, type: "auth", token: frame.replyToken }) + "\n" + JSON.stringify(receipt) + "\n";
        if (options.fragmentReceipts) {
          reply.write(payload.slice(0, 13));
          setTimeout(() => reply.write(payload.slice(13, 91)), 10);
          setTimeout(() => reply.end(payload.slice(91)), 20);
        } else {
          reply.end(payload);
        }
      });
    });
  });
  await new Promise((done, reject) => { server.once("error", reject); server.listen(socketPath, done); });
  const fields = readFileSync(`/proc/${process.pid}/stat`, "utf8").split(")").at(-1).trim().split(/\s+/);
  const record = {
    schemaVersion: 1, pid: process.pid,
    procStart: readFileSync("/proc/sys/kernel/random/boot_id", "utf8").trim() + ":" + fields[19],
    pidNs: Number(statSync(`/proc/${process.pid}/ns/pid`).ino),
    sessionId: session, kind: "tui", ipcPath: socketPath,
  };
  const recordPath = join(home, "sessions", `${process.pid}.json`);
  writeFileSync(recordPath, JSON.stringify(record));
  const env = {
    ...process.env, QWEN_HOME: home, QWEN_CODE_SESSION_ID: session,
    QWEN_CODE_MESSAGING_SOCKET: socketPath, QWEN_CODE_MESSAGING_TOKEN: token,
    LORVIK_CONFIG: "/not-a-real-lorvik-config", LORVIK_SESSION: "qwen-fixture",
    LORVIK_BIN: fake, XDG_RUNTIME_DIR: runtime, FIXTURE_MODE: mode, FIXTURE_DIR: directory,
    LORVIK_WAKE_TRACE: "0",
  };
  const children = [];
  function launch(override = {}) {
    const child = spawn("bun", [adapter], { cwd: directory, env: { ...env, ...override }, stdio: ["ignore", "pipe", "pipe"] });
    let stderr = "";
    let stdout = "";
    child.stderr.on("data", (chunk) => { stderr += chunk; });
    child.stdout.on("data", (chunk) => { stdout += chunk; });
    const done = new Promise((resolve, reject) => { child.once("error", reject); child.once("close", (code, signal) => resolve({ code, signal })); });
    children.push(child);
    return { child, done, stderr: () => stderr, stdout: () => stdout };
  }
  const running = launch();
  const lock = join(runtime, `lorvik-qwen-wake-${process.getuid()}-${createHash("sha256").update(session).digest("hex").slice(0, 24)}.lock`);
  return {
    directory, frames, arrivals, readCounts, record, recordPath, running, lock, launch,
    async disconnectTarget() {
      for (const socket of sockets) socket.destroy();
      await new Promise((done) => server.close(done));
    },
    async close() {
      for (const child of children) if (child.exitCode === null && child.signalCode === null) child.kill("SIGTERM");
      await Promise.race([running.done, delay(3000).then(() => { throw new Error("bridge did not stop"); })]);
      for (const socket of sockets) socket.destroy();
      await new Promise((done) => server.close(done));
    },
  };
}

async function check(mode, status, body, options = {}) {
  const f = await fixture(mode, status, options);
  try { await body(f); }
  finally { await f.close(); }
}

{
  const input = new PassThrough();
  const output = [];
  let dropped = 0;
  boundedLines(input, 8, (line) => output.push(line), () => dropped++);
  input.write(Buffer.from('😀'));
  input.write('a\n123456789');
  input.end('\nlast');
  await new Promise((done) => input.on('end', done));
  assert.deepEqual(output, ['😀a', 'last']);
  assert.equal(dropped, 1);

  const pending = new PendingBatch();
  for (let i = 0; i < 500; i++) pending.add(`${i} ${'😀"\\'.repeat(400)}`);
  assert.ok(pending.bytes <= limits.pendingBytes);
  assert.equal(pending.dropped, 500 - pending.lines.length);
  assert.match(pending.lines.at(-1), /^499 /);
  const original = [...pending.lines];
  const first = pending.take((lines, count) => ({ content: lines.join('\n') + (count ? `\ndropped ${count}; run lorvik inbox` : '') }));
  pending.add('newest');
  pending.prepend(first.batch);
  assert.ok(pending.bytes <= limits.pendingBytes);
  assert.equal(pending.lines.at(-1), 'newest');
  assert.ok(pending.dropped >= 500 - original.length);
}

await check("batch", "delivered", async (f) => {
  await until(() => f.frames.length === 1, "fragmented events did not reach the socket");
  assert.match(f.frames[0].message.content, /now ana commented on #42 "Fix login"\n\$\(touch sentinel\)/);
  assert.equal(existsSync(join(f.directory, "sentinel")), false);
  await delay(400);
  assert.equal(f.frames.length, 1, "one 250 ms batch must be one user frame");
  assert.equal(f.running.stdout(), "");
  assert.equal(f.running.stderr(), "");
  writeFileSync(f.recordPath, JSON.stringify({ ...f.record, sessionId: randomUUID() }));
  await f.running.done;
  assert.equal(existsSync(f.lock), false);
  assert.ok(existsSync(join(f.directory, "terminated")) || existsSync(join(f.directory, "eof")));
  assert.equal((f.running.stderr().match(/session changed or ended/g) ?? []).length, 1);
});

await check("quiet", "delivered", async (f) => {
  await until(() => existsSync(join(f.directory, "child.pid")), "quiet listener did not start");
  await delay(1100);
  assert.equal(f.frames.length, 0, "silence must not make a prompt");
  const duplicate = f.launch();
  assert.notEqual((await duplicate.done).code, 0, "second bridge must fail closed");
  assert.equal(readFileSync(f.lock, "utf8").trim(), String(f.running.child.pid));
  f.running.child.kill("SIGTERM");
  await f.running.done;
  assert.equal(existsSync(f.lock), false);
});

await check("exit", "delivered", async (f) => {
  await f.running.done;
  assert.equal(f.frames.length, 1);
  assert.match(f.frames[0].message.content, /final event without newline/);
  assert.equal((f.running.stderr().match(/listener exited/g) ?? []).length, 1);
  assert.doesNotMatch(f.running.stderr(), /Unhandled|unexpected/);
  assert.equal(existsSync(f.lock), false);
});

await check("batch", "refused", async (f) => {
  await f.running.done;
  assert.equal(f.frames.length, 1);
  assert.match(f.running.stderr(), /refused/);
  assert.doesNotMatch(f.running.stderr(), /Unhandled|unexpected/);
  assert.equal(existsSync(f.lock), false);
});

await check("stubborn", "delivered", async (f) => {
  await until(() => existsSync(join(f.directory, "child.pid")), "stubborn listener did not start");
  f.running.child.kill("SIGTERM");
  await until(() => existsSync(join(f.directory, "terminated")), "listener never received SIGTERM");
  assert.equal(existsSync(f.lock), true, "lock must survive until the old listener closes");
  assert.notEqual((await f.launch().done).code, 0);
  await f.running.done;
  assert.equal(existsSync(f.lock), false);
});

await check("batch", "missing", async (f) => {
  await until(() => f.frames.length === 1, "no frame awaiting a receipt");
  f.running.child.kill("SIGTERM");
  await f.running.done;
  assert.equal(existsSync(f.lock), false);
  assert.doesNotMatch(f.running.stderr(), /Unhandled|unexpected/);
});

await check("batch", "delivered", async (f) => {
  await until(() => existsSync(join(f.directory, "child.pid")), "listener did not start before transport loss");
  await f.disconnectTarget();
  await f.running.done;
  assert.match(f.running.stderr(), /delivery failure/);
  assert.doesNotMatch(f.running.stderr(), /Unhandled|unexpected/);
  assert.equal(existsSync(f.lock), false);
});

await check("quiet", "delivered", async (f) => {
  await until(() => existsSync(join(f.directory, 'child.pid')), 'EOF listener did not start');
  const pid = Number(readFileSync(join(f.directory, 'child.pid'), 'utf8'));
  assert.equal(existsSync(join(f.directory, 'eof')), false, 'stdin was closed while listener was running');
  f.running.child.kill('SIGKILL');
  await f.running.done;
  await until(() => existsSync(join(f.directory, 'eof')), 'adapter SIGKILL did not close listener stdin');
  await until(() => {
    try { return readFileSync(`/proc/${pid}/stat`, 'utf8').split(')').at(-1).trim().startsWith('Z '); }
    catch { return true; }
  }, 'quiet listener survived EOF');
  assert.equal(existsSync(f.lock), true, 'hard-kill stale lock must remain for explicit recovery');
});

await check("stream", (_frame, index) => index === 0 ? 'rate-limited' : 'delivered', async (f) => {
  await until(() => f.frames.some((frame, index) => index > 0 && frame.message.content.includes('event-35\n')), 'paced sustained events did not drain', 18000);
  assert.equal(f.running.child.exitCode, null, 'definitive rate-limit rejection stopped the bridge');
  assert.ok(f.frames.length < 12, 'events were not coalesced');
  assert.ok(f.frames[1].message.content.includes('event-0\n'), 'definitively rejected data was not retained');
  for (let i = 1; i < f.arrivals.length; i++) assert.ok(f.arrivals[i] - f.arrivals[i - 1] >= limits.sendIntervalMs - 100, 'send attempts violated pacing');
}, { fragmentReceipts: true });

await check("overflow", 'delivered', async (f) => {
  await until(() => f.frames.length >= 4, 'escaped frames did not split and drain', 12000);
  const messages = f.frames.map((frame) => frame.message.content).join('\n');
  assert.match(messages, /8 event lines dropped; run lorvik inbox/);
  assert.ok(messages.includes('event-11 '), 'newest buffered event was lost');
  assert.ok(!messages.includes('event-0 '), 'oldest overflow data was retained');
  assert.ok(f.readCounts.some((count) => count > 1), 'large frame did not cross socket read boundaries');
}, { fragmentReceipts: true });

await check("oversized", 'delivered', async (f) => {
  await until(() => f.frames.length === 1, 'oversized line was not summarized');
  assert.match(f.frames[0].message.content, /1 event lines dropped; run lorvik inbox/);
  assert.match(f.frames[0].message.content, /newest-good-event/);
  assert.ok(Buffer.byteLength(JSON.stringify(f.frames[0])) <= limits.frameBytes);
});

console.log("Passed fake delivery/lifecycle checks, SIGKILL-to-EOF shutdown, bounded newest-line buffering, paced sustained events, definitive rate-limit recovery, escaped frame splitting and fragmented receipts.");
console.log(`Retained isolated fixtures: ${resolve(root)}`);
