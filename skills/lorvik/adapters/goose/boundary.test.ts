// SPDX-License-Identifier: Apache-2.0

import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { chmodSync, existsSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const adapter = resolve(dirname(fileURLToPath(import.meta.url)), "lorvik-wake.ts");
const sleep = (milliseconds: number) => new Promise<void>(resolve => setTimeout(resolve, milliseconds));
async function until(check: () => boolean): Promise<void> {
  const end = Date.now() + 5000;
  while (!check()) {
    if (Date.now() > end) throw Error("Boundary check timed out");
    await sleep(10);
  }
}

type Options = { listener?: "events" | "quiet" | "overflow" | "bursts" | "partial"; holdAutomatic?: boolean };

function fixture(options: Options = {}) {
  const dir = mkdtempSync(join(tmpdir(), "lorvik-goose-boundary-"));
  const log = join(dir, "calls.jsonl");
  const label = "goose-boundary-001";
  const node = process.execPath;
  const listenerMode = options.listener ?? "events";
  writeFileSync(join(dir, ".lorvik.toml"), "");
  const common = "const fs=require('node:fs');const log=x=>fs.appendFileSync(process.env.PROBE_LOG,JSON.stringify(x)+'\\n');\n";
  const goose = "#!" + node + "\n" + common + [
    "log({kind:'goose-start',args:process.argv.slice(2),label:process.env.LORVIK_SESSION});",
    "const rl=require('node:readline').createInterface({input:process.stdin});process.stdin.resume();",
    "const send=x=>fs.writeSync(1,JSON.stringify(x)+'\\n');const keepalive=setInterval(()=>{},1000);let busy,automatic;",
    "rl.on('line',line=>{const rpc=JSON.parse(line);log({kind:'goose-rpc',rpc});",
    "if(rpc.method==='initialize')send({jsonrpc:'2.0',id:rpc.id,result:{protocolVersion:1,agentCapabilities:{loadSession:true,sessionCapabilities:{list:{},delete:{},fork:{},close:{}}}}});",
    "if(rpc.method==='session/new')send({jsonrpc:'2.0',id:rpc.id,result:{sessionId:'native-goose-session'}});",
    "if(rpc.method==='session/prompt'){const text=rpc.params.prompt[0].text;if(text==='busy')busy=rpc.id;else if(text.startsWith('Lorvik board events')){if(process.env.HOLD_AUTOMATIC==='1')automatic=rpc.id;else send({jsonrpc:'2.0',id:rpc.id,result:{stopReason:'end_turn'}})}else send({jsonrpc:'2.0',id:rpc.id,result:{stopReason:'end_turn'}})}",
    "if(rpc.method==='test/release'){if(busy){send({jsonrpc:'2.0',id:busy,result:{stopReason:'end_turn'}});busy=undefined}else if(automatic){send({jsonrpc:'2.0',id:automatic,result:{stopReason:'end_turn'}});automatic=undefined}}});",
    "rl.once('close',()=>{clearInterval(keepalive);log({kind:'goose-eof'});process.exit(0)});",
    "process.on('SIGTERM',()=>{clearInterval(keepalive);log({kind:'goose-stop'});process.exit(0)});",
  ].join("\n");
  const listenerOutput = listenerMode === "events"
    ? "fs.writeSync(1,'now ana commented on #42 \"Fix login\"\\nnow bob commented $(touch NEVER-EVALUATE)\\n');"
    : listenerMode === "overflow"
      ? "fs.writeSync(1,'old event\\n');setTimeout(()=>{const stale=Array.from({length:1300},(_,i)=>'stale-'+i+'-'+('y'.repeat(900))).join('\\n');fs.writeSync(1,stale+'\\n'+'X'.repeat(1024*1024+1)+'\\nnewest event\\n')},500);"
      : listenerMode === "bursts"
        ? "fs.writeSync(1,'early event\\n');setTimeout(()=>fs.writeSync(1,Array.from({length:700},(_,i)=>'first-'+i+'-'+('x'.repeat(900))).join('\\n')+'\\n'),400);setTimeout(()=>fs.writeSync(1,Array.from({length:700},(_,i)=>'second-'+i+'-'+('y'.repeat(900))).join('\\n')+'\\nnewest held-human event\\n'),600);"
      : listenerMode === "partial"
        ? "fs.writeSync(1,'partial final event');process.exit(7);"
      : "";
  const lorvik = "#!" + node + "\n" + common + [
    "log({kind:'listener-start',args:process.argv.slice(2),label:process.env.LORVIK_SESSION,cwd:process.cwd()});",
    "process.stdin.resume();process.stdin.once('end',()=>{log({kind:'listener-eof'});process.exit(0)});",
    listenerOutput,
    "setInterval(()=>{},1000);process.on('SIGTERM',()=>{log({kind:'listener-stop'});process.exit(0)});",
  ].join("\n");
  for (const [name, source] of [["goose", goose], ["lorvik", lorvik]]) {
    writeFileSync(join(dir, name), source);
    chmodSync(join(dir, name), 0o755);
  }
  const runtime = process.env.LORVIK_WAKE_TEST_RUNTIME ?? node;
  const child = spawn(runtime, [adapter, "--arm"], {
    cwd: dir,
    env: { ...process.env, PATH: dir + ":" + process.env.PATH, PROBE_LOG: log, LORVIK_CONFIG: join(dir, "agent.toml"), LORVIK_SESSION: label, HOLD_AUTOMATIC: options.holdAutomatic ? "1" : "0" },
    stdio: ["pipe", "pipe", "pipe"], shell: false,
  });
  let out = "";
  let err = "";
  child.stdout.on("data", chunk => { out += chunk; });
  child.stderr.on("data", chunk => { err += chunk; });
  const done = new Promise<void>(resolve => child.once("close", () => resolve()));
  const records = () => existsSync(log) ? readFileSync(log, "utf8").split("\n").flatMap((line) => {
    try { return line ? [JSON.parse(line)] : []; } catch { return []; }
  }) : [];
  const responses = () => out.split("\n").filter(Boolean).map(line => JSON.parse(line));
  const send = (rpc: any) => child.stdin.write(JSON.stringify({ jsonrpc: "2.0", ...rpc }) + "\n");
  const prompts = () => records().filter(x => x.kind === "goose-rpc" && x.rpc.method === "session/prompt");
  const ready = async () => {
    await sleep(100);
    send({ id: 1, method: "initialize", params: { protocolVersion: 1, clientCapabilities: {} } });
    await until(() => {
      if (err) throw Error(`initialize failed: ${err}`);
      return responses().some(x => x.id === 1);
    });
    send({ id: 2, method: "session/new", params: { cwd: dir, mcpServers: [] } });
    await until(() => responses().some(x => x.id === 2));
  };
  const close = async () => {
    child.stdin.end();
    await Promise.race([done, sleep(3000)]);
    if (child.exitCode === null && child.signalCode === null) child.kill("SIGTERM");
    await done;
  };
  return { dir, child, done, records, responses, send, prompts, ready, close, errors: () => err };
}

test("masks unsupported capabilities and cancels queued human prompts before forwarding active cancel", { timeout: 9000 }, async () => {
  const f = fixture();
  try {
    await f.ready();
    await until(() => f.records().some(x => x.kind === "listener-start"));
    assert.deepEqual(f.records().find(x => x.kind === "listener-start").args, ["--session", "goose-boundary-001", "inbox", "--follow", "--exit-on-eof"]);
    assert.equal(existsSync(join(f.dir, "NEVER-EVALUATE")), false);
    const init = f.responses().find(x => x.id === 1);
    assert.equal(init.result.agentCapabilities.loadSession, false);
    assert.deepEqual(init.result.agentCapabilities.sessionCapabilities, { close: {} });
    f.send({ id: 3, method: "session/prompt", params: { sessionId: "native-goose-session", prompt: [{ type: "text", text: "busy" }] } });
    f.send({ id: 4, method: "session/prompt", params: { sessionId: "native-goose-session", prompt: [{ type: "text", text: "queued" }] } });
    await until(() => f.prompts().length === 1);
    f.send({ method: "session/cancel", params: { sessionId: "native-goose-session" } });
    await until(() => f.responses().some(x => x.id === 4));
    assert.deepEqual(f.responses().find(x => x.id === 4).result, { stopReason: "cancelled" });
    assert.equal(f.records().filter(x => x.kind === "goose-rpc" && x.rpc.method === "session/cancel").length, 1);
    f.send({ id: 5, method: "session/list", params: {} });
    await until(() => f.responses().some(x => x.id === 5));
    assert.equal(f.records().some(x => x.kind === "goose-rpc" && x.rpc.method === "session/list"), false);
    f.send({ method: "test/release" });
    await until(() => f.responses().some(x => x.id === 3));
    assert.equal(f.prompts().some(x => x.rpc.id === 4), false);
  } finally { await f.close(); }
});

test("keeps one automatic delivery in flight and sends newest pending events with one overflow notice", { timeout: 10000 }, async () => {
  const f = fixture({ listener: "overflow", holdAutomatic: true });
  try {
    await f.ready();
    await until(() => f.prompts().length === 1);
    assert.ok(f.prompts()[0].rpc.params.prompt[0].text.includes("old event"));
    await sleep(650);
    assert.equal(f.prompts().length, 1);
    f.send({ method: "test/release" });
    await until(() => f.prompts().length === 2);
    const second = f.prompts()[1].rpc.params.prompt[0].text;
    assert.ok(second.includes("newest event"));
    assert.match(second, /dropped [1-9][0-9]* board event line\(s\)/);
    assert.equal(second.includes("stale-0-"), false);
    assert.ok(Buffer.byteLength(second) <= 1024 * 1024);
  } finally { await f.close(); }
});

test("rejects a human prompt above the aggregate queued-byte cap without dropping accepted prompts", { timeout: 9000 }, async () => {
  const f = fixture({ listener: "quiet" });
  try {
    await f.ready();
    f.send({ id: 3, method: "session/prompt", params: { sessionId: "native-goose-session", prompt: [{ type: "text", text: "busy" }] } });
    await until(() => f.prompts().some(x => x.rpc.id === 3));
    const large = "z".repeat(600 * 1024);
    f.send({ id: 4, method: "session/prompt", params: { sessionId: "native-goose-session", prompt: [{ type: "text", text: large }] } });
    f.send({ id: 5, method: "session/prompt", params: { sessionId: "native-goose-session", prompt: [{ type: "text", text: large }] } });
    await until(() => f.responses().some(x => x.id === 5));
    assert.equal(f.responses().find(x => x.id === 5).error.code, -32000);
    assert.equal(f.prompts().some(x => x.rpc.id === 4), false);
  } finally { await f.close(); }
});

test("a board marker waiting behind a human prompt renders one coalesced newest batch only when dispatched", { timeout: 10000 }, async () => {
  const f = fixture({ listener: "bursts" });
  try {
    await f.ready();
    f.send({ id: 3, method: "session/prompt", params: { sessionId: "native-goose-session", prompt: [{ type: "text", text: "busy" }] } });
    await until(() => f.prompts().some(x => x.rpc.id === 3));
    await sleep(800);
    const boardPrompts = () => f.prompts().filter(x => x.rpc.params.prompt[0].text.startsWith("Lorvik board events"));
    assert.equal(boardPrompts().length, 0);
    f.send({ method: "test/release" });
    await until(() => boardPrompts().length === 1);
    const text = boardPrompts()[0].rpc.params.prompt[0].text;
    assert.ok(text.includes("newest held-human event"));
    assert.ok(Buffer.byteLength(text) <= 1024 * 1024);
    await sleep(350);
    assert.equal(boardPrompts().length, 1);
  } finally { await f.close(); }
});

test("quiet listener exits on proxy SIGKILL through its untouched stdin EOF pipe", { timeout: 9000 }, async () => {
  const f = fixture({ listener: "quiet" });
  try {
    await f.ready();
    await until(() => f.records().some(x => x.kind === "listener-start"));
    await sleep(150);
    assert.equal(f.records().some(x => x.kind === "listener-eof"), false);
    f.child.kill("SIGKILL");
    await f.done;
    await until(() => f.records().some(x => x.kind === "listener-eof") && f.records().some(x => x.kind === "goose-eof"));
  } finally {
    if (f.child.exitCode === null && f.child.signalCode === null) await f.close();
  }
});

test("self-exit drops a short unterminated listener fragment and injects one recovery notice", { timeout: 9000 }, async () => {
  const f = fixture({ listener: "partial" });
  try {
    await f.ready();
    await until(() => f.prompts().length === 1);
    const text = f.prompts()[0].rpc.params.prompt[0].text;
    assert.equal(text.includes("partial final event"), false);
    assert.equal((text.match(/dropped 1 board event line\(s\)/g) ?? []).length, 1);
  } finally { await f.close(); }
});
