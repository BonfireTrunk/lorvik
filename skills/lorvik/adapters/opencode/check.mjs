// SPDX-License-Identifier: Apache-2.0
// The plugin and reactive runtime are hand-written fakes; no harness code runs.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { appendFileSync, existsSync, mkdtempSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { pathToFileURL, fileURLToPath } from "node:url";
import { setTimeout as delay } from "node:timers/promises";

const adapter = dirname(fileURLToPath(import.meta.url));
const dir = mkdtempSync(join(tmpdir(), "lorvik-opencode-check-"));
const bin = join(dir, "bin");
mkdirSync(bin);
writeFileSync(join(dir, ".lorvik.toml"), "# fake binding\n");
writeFileSync(join(dir, "package.json"), '{"type":"module"}\n');
const pkg = join(dir, "node_modules", "@opencode", "plugin");
mkdirSync(pkg, { recursive: true });
writeFileSync(join(pkg, "package.json"), JSON.stringify({ type: "module", exports: { "./tui": "./tui.js" } }));
writeFileSync(join(pkg, "tui.js"), "export const Plugin = { define: definition => definition };\n");
const solid = join(dir, "node_modules", "solid-js");
mkdirSync(solid);
writeFileSync(join(solid, "package.json"), '{"type":"module","exports":"./index.js"}');
writeFileSync(join(solid, "index.js"), `
const roots = new Set(); let owner;
export function createRoot(fn) {
  const previous = owner; const effects = new Set(); owner = effects; roots.add(effects);
  try { return fn(() => { roots.delete(effects); effects.clear(); }); }
  finally { owner = previous; }
}
export function createComputed(fn) { owner.add(fn); fn(); }
export function transition(fn) { fn(); for (const root of roots) for (const effect of root) effect(); }
`);
await new Promise((resolve, reject) => {
  const build = spawn("bun", ["build", "--no-bundle", join(adapter, "tui.ts"), "--outdir", dir], { stdio: "inherit" });
  build.once("error", reject);
  build.once("close", code => code === 0 ? resolve() : reject(new Error(`Bun exited ${code}`)));
});
writeFileSync(join(bin, "lorvik"), `#!/usr/bin/env python3
import json, os, sys, threading, time
from pathlib import Path
root = Path(os.environ["LORVIK_FAKE_ROOT"])
path = root / str(os.getpid())
with (root / "starts").open("a") as output:
    output.write(json.dumps(dict(pid=os.getpid(), args=sys.argv[1:],
        config=os.environ.get("LORVIK_CONFIG"), id=os.environ.get("OPENCODE_SESSION_ID"),
        override=os.environ.get("LORVIK_SESSION"))) + "\\n")
def stdin_owner():
    if sys.stdin.buffer.read():
        path.with_suffix(".written").write_text("unexpected stdin data")
    path.with_suffix(".eof").write_text("EOF")
    time.sleep(0.08)
    os._exit(0)
threading.Thread(target=stdin_owner, daemon=True).start()
offset = 0
while True:
    control = path.with_suffix(".cmd")
    commands = control.read_text().splitlines() if control.exists() else []
    for raw in commands[offset:]:
        cmd = json.loads(raw)
        if "out" in cmd:
            sys.stdout.write(cmd["out"])
            sys.stdout.flush()
        if "err" in cmd:
            sys.stderr.write(cmd["err"])
            sys.stderr.flush()
        if "exit" in cmd:
            sys.exit(cmd["exit"])
        with path.with_suffix(".ack").open("a") as ack:
            ack.write("ok\\n")
    offset = len(commands)
    time.sleep(0.005)
`, { mode: 0o700 });
process.env.LORVIK_FAKE_ROOT = dir;
process.env.LORVIK_CONFIG = join(dir, "agent.toml");
process.env.LORVIK_SESSION = "explicit-chat";
process.env.PATH = bin + ":" + process.env.PATH;
delete process.env.CLAUDE_CODE_SESSION_ID;
const plugin = (await import(pathToFileURL(join(dir, "tui.js")))).default;
const { transition } = await import(pathToFileURL(join(solid, "index.js")));
const starts = () => existsSync(join(dir, "starts")) ? readFileSync(join(dir, "starts"), "utf8").trim().split("\n").map(JSON.parse) : [];
async function until(predicate, label) {
  for (let i = 0; i < 500; i++) { if (predicate()) return; await delay(10); }
  throw new Error("Timed out: " + label);
}
function context(id = "ses_fake") {
  const c = { route: { type: "session", sessionID: id }, calls: [], notices: [], commands: [], event: undefined, active: 0, maxActive: 0 };
  const ctx = {
    ui: { router: { current: () => c.route }, toast: { show: input => c.notices.push(input.message) } },
    data: { session: { get: () => ({ location: { directory: dir }, time: {} }) },
      listen: handler => { c.event = handler; return () => { c.event = undefined; }; } },
    keymap: { layer: factory => { c.commands = factory().commands; } },
    client: { session: { prompt: async (input, options) => {
      c.calls.push({ input, options }); c.active++; c.maxActive = Math.max(c.maxActive, c.active);
      try {
        if (c.reject) throw new Error("fake uncertain admission");
        if (c.hold) await c.hold(options.signal);
        return { id: "msg_fake" };
      } finally { c.active--; }
    } } },
  };
  c.cleanup = plugin.setup(ctx);
  c.arm = () => c.commands.find(cmd => cmd.slash.name === "lorvik-listen").run();
  c.stop = () => c.commands.find(cmd => cmd.slash.name === "lorvik-unlisten").run();
  c.navigate = route => transition(() => {
    for (const key of Object.keys(c.route)) delete c.route[key];
    Object.assign(c.route, route);
  });
  return c;
}
function command(child, cmd) { appendFileSync(join(dir, child.pid + ".cmd"), JSON.stringify(cmd) + "\n"); }
async function listen(c) {
  const n = starts().length; await c.arm();
  await until(() => starts().length > n, "startup: " + c.notices.join("; "));
  return starts().at(-1);
}
const eof = child => existsSync(join(dir, child.pid + ".eof"));
const active = [];
try {
  const c = context(); active.push(c);
  assert.equal(starts().length, 0);
  const child = await listen(c);
  assert.deepEqual(child.args, ["inbox", "--follow", "--exit-on-eof"]);
  assert.equal(child.id, "ses_fake"); assert.equal(child.config, process.env.LORVIK_CONFIG);
  assert.equal(child.override, "explicit-chat");
  await c.arm(); assert.equal(starts().length, 1);
  await delay(300); assert.equal(c.calls.length, 0); assert.equal(eof(child), false, JSON.stringify(c.notices));
  assert.equal(existsSync(join(dir, child.pid + ".written")), false);
  command(child, { out: 'now ana commented on #42 "$(not-a-command)' });
  command(child, { out: '`literal`"\nnow ana answered #43\n' });
  await until(() => c.calls.length === 1, "batched delivery");
  assert.match(c.calls[0].input.text, /\$\(not-a-command\)/);
  assert.match(c.calls[0].input.text, /answered #43/);
  assert.equal(c.calls[0].input.delivery, "queue"); assert.equal(c.calls[0].input.resume, true);
  c.navigate({ type: "home" }); c.navigate({ type: "session", sessionID: "ses_fake" });
  await until(() => eof(child), "brief departure closes stdin");
  assert.equal(c.calls[0].options.signal.aborted, true);
  await c.cleanup();
  console.log("PASS explicit arm, identity, EOF flag, unwritten open stdin, batching, literal payload, brief navigation revocation");

  const r = context("ses_A"); active.push(r);
  const old = await listen(r);
  r.navigate({ type: "session", sessionID: "ses_B" });
  const armB = r.arm();
  r.navigate({ type: "session", sessionID: "ses_C" });
  await armB; await until(() => eof(old), "old listener retirement");
  assert.equal(starts().filter(x => x.id === "ses_C" || x.id === "ses_B").length, 0);
  await r.cleanup();
  console.log("PASS mutable route cannot arm an unselected chat during asynchronous retirement");

  const b = context("ses_bound"); active.push(b);
  let release;
  b.hold = signal => new Promise(resolve => {
    release = resolve;
    signal.addEventListener("abort", resolve, { once: true });
  });
  const burst = await listen(b);
  command(burst, { out: "in flight\n" });
  await until(() => b.calls.length === 1, "first admission held");
  const lines = Array.from({ length: 300 }, (_, i) => `event-${i.toString().padStart(3, "0")}-${"x".repeat(1000)}\n`).join("");
  command(burst, { out: lines });
  await until(() => existsSync(join(dir, burst.pid + ".ack")) && readFileSync(join(dir, burst.pid + ".ack"), "utf8").trim().split("\n").length === 2, "burst emitted");
  await delay(300); assert.equal(b.calls.length, 1);
  b.hold = undefined; release();
  await until(() => b.calls.length === 2, "one coalesced pending batch");
  const payload = b.calls[1].input.text;
  assert.match(payload, /event-299-/); assert.doesNotMatch(payload, /event-000-/);
  const kept = payload.match(/event-\d{3}-/g).length;
  assert.match(payload, new RegExp(`dropped ${300 - kept} notification lines; run lorvik inbox`));
  assert.ok(Buffer.byteLength(payload) < 65536 + 256); assert.equal(b.maxActive, 1);
  await b.stop(); assert.equal(eof(burst), true); await b.cleanup();
  console.log("PASS one delivery in flight, one byte-capped pending batch, newest lines and exact dropped-count notice");

  const e = context("ses_exit"); active.push(e);
  const exited = await listen(e);
  command(exited, { out: "final event without newline", exit: 7 });
  await until(() => e.notices.some(n => n.startsWith("Lorvik wake stopped:")), "self-exit notice");
  assert.equal(e.calls.length, 1); assert.match(e.calls[0].input.text, /final event without newline/);
  assert.equal(e.notices.filter(n => n.startsWith("Lorvik wake stopped:")).length, 1);
  const before = starts().length; await delay(300); assert.equal(starts().length, before); await e.cleanup();
  const f = context("ses_failure"); active.push(f); f.reject = true;
  const failed = await listen(f); command(failed, { out: "uncertain\n" });
  await until(() => eof(failed), "failed admission closes pipe");
  assert.equal(f.calls.length, 1); await f.cleanup();
  console.log("PASS final batch, one exit notice, no restart, uncertain admission never retries");

  const o = context("ses_oversize"); active.push(o);
  const oversized = await listen(o);
  command(oversized, { out: "x".repeat(17000) });
  command(oversized, { out: "discarded tail\nnewest complete event\n" });
  await until(() => o.calls.length === 1, "oversized line dropped with later line retained");
  assert.match(o.calls[0].input.text, /newest complete event/);
  assert.match(o.calls[0].input.text, /dropped 1 notification lines; run lorvik inbox/);
  assert.doesNotMatch(o.calls[0].input.text, /discarded tail/);
  o.event({ details: { type: "session.archived", data: { sessionID: "ses_oversize" } } });
  await until(() => eof(oversized), "archive closes stdin");
  await o.cleanup();
  console.log("PASS fragmented oversized-line recovery and archive cleanup");

  writeFileSync(join(dir, "crash.mjs"), `
import plugin from './tui.js';
const commands=[];
plugin.setup({ ui:{router:{current:()=>({type:'session',sessionID:'ses_crash'})},toast:{show:()=>{}}},
 data:{session:{get:()=>({location:{directory:${JSON.stringify(dir)}},time:{}})},listen:()=>()=>{}},
 keymap:{layer:f=>commands.push(...f().commands)},client:{session:{prompt:()=>{throw Error('unexpected input')}}}});
await commands.find(c=>c.slash.name==='lorvik-listen').run();
setInterval(()=>{},1000);
`);
  const crash = spawn("bun", [join(dir, "crash.mjs")], { stdio: "ignore" });
  try {
    await until(() => starts().some(x => x.id === "ses_crash"), "crash fixture armed");
    const orphan = starts().find(x => x.id === "ses_crash");
    const closed = new Promise(resolve => crash.once("close", resolve));
    crash.kill("SIGKILL"); await closed;
    await until(() => eof(orphan), "adapter SIGKILL closes listener stdin");
    assert.equal(existsSync(join(dir, orphan.pid + ".written")), false);
    console.log("PASS adapter SIGKILL delivers EOF to quiet fake listener without a guardian");
  } finally { if (crash.exitCode === null && crash.signalCode === null) crash.kill("SIGKILL"); }
  console.log("Offline checks passed; fixtures retained at " + dir);
} finally {
  await Promise.allSettled(active.map(c => c.cleanup()));
}
