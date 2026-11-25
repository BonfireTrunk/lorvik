// SPDX-License-Identifier: Apache-2.0

import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { copyFile, mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

const adapter = fileURLToPath(new URL(".", import.meta.url));
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function fixture(t, overrides = {}, { binding = true } = {}) {
  const dir = await mkdtemp(join(tmpdir(), "lorvik-copilot-check-"));
  const sdk = join(dir, "node_modules/@github/copilot-sdk");
  await mkdir(sdk, { recursive: true });
  await copyFile(join(adapter, "extension.mjs"), join(dir, "extension.mjs"));
  if (binding) await writeFile(join(dir, ".lorvik.toml"), "");
  await writeFile(join(sdk, "package.json"), JSON.stringify({ type: "module", exports: { "./extension": "./extension.mjs" } }));
  await writeFile(join(sdk, "extension.mjs"), `
import { appendFileSync } from "node:fs";
import { createInterface } from "node:readline";
export async function joinSession() {
  const handlers = new Map();
  let release;
  createInterface({ input: process.stdin }).on("line", name => {
    if (name === "release") release?.();
    else handlers.get(name)?.();
  });
  return {
    sessionId: "native-chat-1",
    on: (name, fn) => { handlers.set(name, fn); return () => handlers.delete(name); },
    send: async message => {
      appendFileSync(process.env.CHECK_LOG, JSON.stringify({ injection: message }) + "\\n");
      if (process.env.CHECK_REJECT === "1") throw new Error("injected failure");
      if (process.env.CHECK_BLOCK === "1") await new Promise(resolve => { release = resolve; });
      return "message-1";
    },
  };
}
`);
  await writeFile(join(dir, "lorvik"), `#!/usr/bin/env node
const fs = require("node:fs");
const marker = setInterval(() => {}, 1000);
const { createInterface } = require("node:readline");
createInterface({ input: fs.createReadStream(process.env.CHECK_INPUT) }).on("line", line => {
  if (line === "EXIT") { process.stderr.write("listener self-exit\\n"); process.exit(7); }
  process.stdout.write(line.replaceAll("<CR>", "\\r") + "\\n");
});
process.stdin.once("end", () => { fs.appendFileSync(process.env.CHECK_LOG, '{"stopped":true}\\n'); clearInterval(marker); process.exit(0); });
process.stdin.resume();
process.on("SIGTERM", () => { fs.appendFileSync(process.env.CHECK_LOG, '{"stopped":true}\\n'); clearInterval(marker); process.exit(0); });
fs.appendFileSync(process.env.CHECK_LOG, JSON.stringify({ listener: process.argv.slice(2), label: process.env.LORVIK_SESSION, config: process.env.LORVIK_CONFIG, pid: process.pid }) + "\\n");
`, { mode: 0o755 });
  const input = join(dir, "input");
  // A named pipe lets the test drive the real child without a server or database.
  const mkfifo = spawn("mkfifo", [input]);
  await new Promise((resolve, reject) => { mkfifo.once("error", reject); mkfifo.once("close", code => code === 0 ? resolve() : reject(new Error(`mkfifo ${code}`))); });
  const log = join(dir, "calls.jsonl");
  await writeFile(log, "");
  const child = spawn(process.execPath, [join(dir, "extension.mjs")], {
    cwd: dir,
    env: { ...process.env, PATH: `${dir}:${process.env.PATH}`, LORVIK_WAKE: "1", LORVIK_SESSION: "copilot-check", LORVIK_WAKE_SESSION_ID: "native-chat-1", LORVIK_CONFIG: join(dir, "config.toml"), CHECK_LOG: log, CHECK_INPUT: input, ...overrides },
    stdio: ["pipe", "pipe", "pipe"],
  });
  let stderr = "";
  child.stderr.on("data", (chunk) => { stderr += chunk; });
  const done = new Promise((resolve) => child.once("close", resolve));
  let writer;
  t.after(async () => {
    writer?.end();
    child.stdin.end();
    child.kill("SIGTERM");
    await done;
  });
  const calls = async () => (await readFile(log, "utf8")).trim().split("\n").filter(Boolean).map(JSON.parse);
  const waitFor = async (predicate) => {
    for (let n = 0; n < 300; n++) {
      const events = await calls();
      if (predicate(events)) return events;
      await delay(10);
    }
    throw new Error(`boundary not reached: ${JSON.stringify(await calls())}; exit=${child.exitCode}; dir=${dir}; stderr=${stderr}`);
  };
  return { child, done, dir, calls, waitFor, stderr: () => stderr, release() { child.stdin.write("release\n"); }, async emit(text) {
    if (!writer) {
      const { createWriteStream } = await import("node:fs");
      writer = createWriteStream(input);
    }
    writer.write(text);
  } };
}

test("entry joins chat, starts correctly labelled listener, batches literal board data, queues and stops", { timeout: 10000 }, async (t) => {
  const f = await fixture(t);
  let events = await f.waitFor(events => events.some(event => event.listener));
  assert.deepEqual(events[0].listener, ["inbox", "--follow", "--exit-on-eof", "--session", "copilot-check"]);
  assert.equal(events[0].label, "copilot-check");
  assert.equal(events[0].config, join(f.dir, "config.toml"));
  await delay(350);
  assert.equal((await f.calls()).filter(event => event.injection).length, 0);
  await f.emit('now ana commented #42 "$(touch SHOULD_NOT_EXIST)"\nsecond event\n');
  events = await f.waitFor(events => events.some(event => event.injection));
  const sends = events.filter(event => event.injection);
  assert.equal(sends.length, 1);
  assert.equal(sends[0].injection.mode, "enqueue");
  assert.equal(sends[0].injection.source, "system");
  assert.match(sends[0].injection.prompt, /Run lorvik --session copilot-check inbox/);
  assert.match(sends[0].injection.prompt, /\$\(touch SHOULD_NOT_EXIST\).*\nsecond event/);
  await assert.rejects(readFile(join(f.dir, "SHOULD_NOT_EXIST")));
  f.child.stdin.write("session.shutdown\n");
  await f.waitFor(events => events.some(event => event.stopped));
  assert.equal((await f.calls()).filter(event => event.listener).length, 1);
});

test("changed foreground pin leaves replacement chat disarmed", { timeout: 10000 }, async (t) => {
  const f = await fixture(t, { LORVIK_WAKE_SESSION_ID: "other-chat" });
  await delay(350);
  assert.equal((await f.calls()).length, 0);
  assert.match(f.stderr(), /foreground chat changed/);
});

test("an armed extension explains missing local prerequisites", { timeout: 10000 }, async (t) => {
  const noBinding = await fixture(t, {}, { binding: false });
  await noBinding.done;
  assert.match(noBinding.stderr(), /no .lorvik.toml/);
  const noExecutable = await fixture(t, { PATH: "/does-not-exist" });
  await noExecutable.done;
  assert.match(noExecutable.stderr(), /lorvik was not found on PATH/);
});

test("parent EOF discards a pending batch and stops listener", { timeout: 10000 }, async (t) => {
  const f = await fixture(t);
  await f.waitFor(events => events.some(event => event.listener));
  await f.emit("pending event\n");
  f.child.stdin.end();
  await f.waitFor(events => events.some(event => event.stopped));
  await delay(350);
  assert.equal((await f.calls()).filter(event => event.injection).length, 0);
});

test("extension SIGKILL closes the Lorvik stdin pipe and stops a quiet listener", { timeout: 10000 }, async (t) => {
  const f = await fixture(t);
  await f.waitFor(events => events.some(event => event.listener));
  f.child.kill("SIGKILL");
  await f.done;
  await f.waitFor(events => events.some(event => event.stopped));
});

test("self-exit is reported once and never restarted", { timeout: 10000 }, async (t) => {
  const f = await fixture(t);
  await f.waitFor(events => events.some(event => event.listener));
  await f.emit("EXIT\n");
  for (let n = 0; n < 100 && !f.stderr().includes("Lorvik wake stopped:"); n++) await delay(10);
  await delay(350);
  assert.equal(f.stderr().match(/Lorvik wake stopped:/g)?.length, 1);
  assert.match(f.stderr(), /listener self-exit/);
  assert.equal((await f.calls()).filter(event => event.listener).length, 1);
});

test("delivery rejection reports once, stops listener and does not retry accepted input", { timeout: 10000 }, async (t) => {
  const f = await fixture(t, { CHECK_REJECT: "1" });
  await f.waitFor(events => events.some(event => event.listener));
  await f.emit("event\n");
  await f.waitFor(events => events.some(event => event.stopped));
  await delay(350);
  assert.equal((await f.calls()).filter(event => event.injection).length, 1);
  assert.equal(f.stderr().match(/Lorvik wake stopped:/g)?.length, 1);
});

test("one blocked send leaves one bounded newest-line batch pending", { timeout: 10000 }, async (t) => {
  const f = await fixture(t, { CHECK_BLOCK: "1" });
  await f.waitFor(events => events.some(event => event.listener));
  await f.emit("first event\n");
  await f.waitFor(events => events.filter(event => event.injection).length === 1);
  await f.emit(`${"old ".repeat(10_000)}\n${"new ".repeat(10_000)}\nnewest event\n`);
  await delay(350);
  assert.equal((await f.calls()).filter(event => event.injection).length, 1);
  f.release();
  const events = await f.waitFor(events => events.filter(event => event.injection).length === 2);
  const second = events.filter(event => event.injection)[1].injection.prompt;
  assert.match(second, /newest event/);
  assert.match(second, /board event line.*dropped; run lorvik inbox/);
  assert.doesNotMatch(second, /old old old/);
  assert.ok(Buffer.byteLength(second) <= 64 * 1024);
  f.release();
});

test("a near-cap UTF-8 batch includes its prompt wrapper within 64 KiB", { timeout: 10000 }, async (t) => {
  const f = await fixture(t, { CHECK_BLOCK: "1" });
  await f.waitFor(events => events.some(event => event.listener));
  await f.emit("first event\n");
  await f.waitFor(events => events.filter(event => event.injection).length === 1);
  await f.emit(`${"é".repeat(31_000)}\nnewest UTF-8 event\n`);
  await delay(350);
  assert.equal((await f.calls()).filter(event => event.injection).length, 1);
  f.release();
  const events = await f.waitFor(events => events.filter(event => event.injection).length === 2);
  const prompt = events.filter(event => event.injection)[1].injection.prompt;
  assert.match(prompt, /newest UTF-8 event/);
  assert.ok(Buffer.byteLength(prompt) <= 64 * 1024);
  f.release();
});

test("overlong fragmented lines are dropped and stopping discards a pending batch", { timeout: 10000 }, async (t) => {
  const f = await fixture(t, { CHECK_BLOCK: "1" });
  await f.waitFor(events => events.some(event => event.listener));
  await f.emit("first event\n");
  await f.waitFor(events => events.filter(event => event.injection).length === 1);
  await f.emit("x".repeat(40_000));
  await f.emit(`${"y".repeat(40_000)}\nnewest retained line\n`);
  await delay(350);
  f.child.stdin.write("session.shutdown\n");
  f.release();
  await f.waitFor(events => events.some(event => event.stopped));
  await delay(350);
  assert.equal((await f.calls()).filter(event => event.injection).length, 1);
});

test("an oversized line alone delivers one counted drop notice", { timeout: 10000 }, async (t) => {
  const f = await fixture(t);
  await f.waitFor(events => events.some(event => event.listener));
  await f.emit("x".repeat(40_000));
  await f.emit(`${"y".repeat(40_000)}\n`);
  const events = await f.waitFor(events => events.some(event => event.injection));
  const prompt = events.find(event => event.injection).injection.prompt;
  assert.match(prompt, /1 board event line was dropped; run lorvik inbox/);
  assert.equal(prompt.match(/dropped; run lorvik inbox/g)?.length, 1);
  assert.ok(Buffer.byteLength(prompt) < 1024);
  assert.doesNotMatch(prompt, /xxxxxxxx|yyyyyyyy/);
});
