// SPDX-License-Identifier: Apache-2.0

import { test, expect } from "bun:test";
import { spawn, type ChildProcess } from "node:child_process";
import { appendFileSync, chmodSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

async function until(check: () => boolean): Promise<void> {
  const deadline = Date.now() + 5000;
  while (!check()) {
    if (Date.now() > deadline) throw new Error("fake child boundary timed out");
    await Bun.sleep(10);
  }
}

function fixture(ignoreAgentStop = false) {
  const dir = mkdtempSync(join(tmpdir(), "lorvik-cursor-check-"));
  const log = join(dir, "journal");
  const events = join(dir, "events");
  const finish = join(dir, "finish");
  for (const path of [log, events, finish, join(dir, ".lorvik.toml")]) writeFileSync(path, "");
  const agent = join(dir, "fake-agent");
  writeFileSync(agent, `#!/usr/bin/env bun
import { createInterface } from 'node:readline';
import { appendFileSync, watch, readFileSync } from 'node:fs';
setInterval(()=>{}, 60_000);
const log = value => appendFileSync(process.env.JOURNAL, JSON.stringify(value)+'\\n');
const send = value => process.stdout.write(JSON.stringify({jsonrpc:'2.0',...value})+'\\n');
log({kind:'agent', args:process.argv.slice(2), label:process.env.LORVIK_SESSION, pid:process.pid});
let active;
watch(process.env.FINISH, () => { if (active) {send({id:active,result:{stopReason:'end_turn'}}); active=undefined;} });
createInterface({input:process.stdin}).on('line', line => {
 const m=JSON.parse(line);log({kind:'rpc',...m});
 if(m.method==='initialize')send({id:m.id,result:{protocolVersion:1}});
 if(m.method==='session/new')send({id:m.id,result:{sessionId:'fake-native-cursor-id'}});
 if(m.method==='session/prompt') {
  if(active)throw new Error('prompt overlap');active=m.id;
  send({method:'session/update',params:{sessionId:m.params.sessionId,update:{sessionUpdate:'agent_message_chunk',content:{type:'text',text:'agent \\u001b]8;;https://invalid.example\\u0007'}}}});
  send({method:'cursor/ask_question',id:901,params:{toolCallId:'question',questions:[]}});
  send({method:'cursor/create_plan',id:902,params:{toolCallId:'plan',plan:'test',todos:[]}});
  send({method:'session/request_permission',id:900,params:{sessionId:m.params.sessionId,toolCall:{title:'fake command'},options:[{optionId:'deny',name:'Reject once',kind:'reject_once'}]}});
 }
});
process.stdin.once('end',()=>{if(process.env.FAKE_IGNORE_STOP!=='1')process.exit();});
process.once('SIGTERM',()=>{log({kind:'agent-stopped'});if(process.env.FAKE_IGNORE_STOP!=='1')process.exit();});
`);
  const lorvik = join(dir, "fake-lorvik");
  writeFileSync(lorvik, `#!/usr/bin/env bun
import { appendFileSync, readFileSync, watch } from 'node:fs';
setInterval(()=>{}, 60_000);
const log = value => appendFileSync(process.env.JOURNAL, JSON.stringify(value)+'\\n');
let offset=0;
let stopped=false;
const stop=()=>{if(stopped)return;stopped=true;log({kind:'listener-stopped'});process.exit();};
// Install both lifetime observers before telling the parent that the fake is ready.
watch(process.env.EVENTS,()=>{const all=readFileSync(process.env.EVENTS,'utf8');const part=all.slice(offset);offset=all.length;if(part==='EXIT')process.exit(7);process.stdout.write(part);});
process.stdin.resume();
process.stdin.once('end',stop);
process.once('SIGTERM',stop);
log({kind:'listener',args:process.argv.slice(2),label:process.env.LORVIK_SESSION,config:process.env.LORVIK_CONFIG,pid:process.pid});
`);
  chmodSync(agent, 0o755); chmodSync(lorvik, 0o755);
  let stderr = "";
  const child = spawn(process.execPath, [fileURLToPath(new URL("./lorvik-wake.ts", import.meta.url)), "--agent", agent, "--lorvik", lorvik], {
    cwd: dir, env: { ...process.env, FAKE_IGNORE_STOP: ignoreAgentStop ? "1" : "0", JOURNAL: log, EVENTS: events, FINISH: finish, LORVIK_CONFIG: join(dir, "agent.toml"), LORVIK_SESSION: "cursor-boundary-check" },
    stdio: ["pipe", "pipe", "pipe"],
  });
  child.stderr.on("data", data => { stderr += data; });
  let stdout = "";
  child.stdout.on("data", data => { stdout += data; });
  const records = (): any[] => readFileSync(log, "utf8").trim().split("\n").filter(Boolean).map(line => JSON.parse(line));
  const prompts = () => records().filter(record => record.method === "session/prompt");
  const exit = new Promise<void>(resolve => child.once("close", () => resolve()));
  return { dir, events, finish, child, records, prompts, exit, stderr: () => stderr, stdout: () => stdout };
}

test("actual bridge batches capped newest data behind one busy prompt and handles permissions", async () => {
  const f = fixture();
  try {
    await until(() => f.records().some(record => record.kind === "listener"));
    expect(f.records().filter(record => record.kind === "listener")).toHaveLength(1);
    const listener = f.records().find(record => record.kind === "listener");
    expect(listener.args).toEqual(["inbox", "--follow", "--exit-on-eof", "--session", "cursor-boundary-check"]);
    expect(listener.label).toBe("cursor-boundary-check");
    expect(listener.config).toBe(join(f.dir, "agent.toml"));
    expect(f.prompts()).toHaveLength(0);
    f.child.stdin.write("human first\n");
    await until(() => f.prompts().length === 1);
    await until(() => f.stdout().includes("\\x1b]8"));
    expect(f.stdout()).not.toContain("\u001b");
    await until(() => f.records().some(record => record.id === 901 && record.result?.outcome?.outcome === "cancelled"));
    await until(() => f.records().some(record => record.id === 902 && record.result?.outcome?.outcome === "cancelled"));
    f.child.stdin.write("/permission 900 1\n");
    await until(() => f.records().some(record => record.id === 900 && record.result?.outcome?.optionId === "deny"));
    const burst = Array.from({ length: 90 }, (_, index) => `OLD-${String(index).padStart(3, "0")}: ${"x".repeat(900)}`).join("\n");
    const hostile = "NEWEST board event\u001b]8;;https://invalid.example\u0007";
    appendFileSync(f.events, `${burst}\n${"Z".repeat(70 * 1024)}\n${hostile}\n`);
    await Bun.sleep(350);
    expect(f.prompts()).toHaveLength(1);
    appendFileSync(f.finish, "done");
    await until(() => f.prompts().length === 2);
    const wake = f.prompts()[1];
    expect(wake.params.sessionId).toBe("fake-native-cursor-id");
    expect(wake.params.prompt[0].text).toContain(hostile);
    expect(wake.params.prompt[0].text).not.toContain("OLD-000");
    expect(wake.params.prompt[0].text).toContain("queued lines were dropped; run lorvik --session cursor-boundary-check inbox.");
    expect(wake.params.prompt[0].text).toContain("Run `lorvik --session cursor-boundary-check inbox`");
    expect(wake.params.prompt[0].text).toContain("This chat's Lorvik session is cursor-boundary-check");
    expect(Buffer.byteLength(wake.params.prompt[0].text)).toBeLessThanOrEqual(64 * 1024);
    expect(f.stderr()).not.toContain("\u001b");
    expect(f.records().filter(record => record.method === "session/new")).toHaveLength(1);
    expect(f.records().some(record => record.method === "authenticate" || record.method === "session/load")).toBe(false);
    f.child.stdin.write("human queued one\nhuman queued two\n");
    await Bun.sleep(50);
    expect(f.prompts()).toHaveLength(2);
    appendFileSync(f.finish, "done again");
    await until(() => f.prompts().length === 3);
    const next = f.prompts()[2].params.prompt[0].text;
    expect(next).toContain("human queued one");
    expect(next).toContain("human queued two");
    f.child.stdin.write("/quit\n");
    await f.exit;
    expect(f.prompts()).toHaveLength(3);
    expect(f.records().some(record => record.method === "session/cancel")).toBe(true);
    expect(f.records().some(record => record.kind === "listener-stopped")).toBe(true);
  } finally { f.child.kill("SIGTERM"); }
}, 10_000);

test("listener self-exit is reported once and never restarted", async () => {
  const f = fixture();
  try {
    await until(() => f.records().some(record => record.kind === "listener"));
    appendFileSync(f.events, "EXIT");
    await until(() => f.stderr().includes("Lorvik wake stopped:"));
    expect(f.stderr().split("Lorvik wake stopped:")).toHaveLength(2);
    expect(f.records().filter(record => record.kind === "listener")).toHaveLength(1);
    f.child.stdin.end();
    await f.exit;
    expect(f.prompts()).toHaveLength(0);
  } finally { f.child.kill("SIGTERM"); }
}, 10_000);

test("an oversized-only board line delivers one notice after the remaining batch window", async () => {
  const f = fixture();
  try {
    await until(() => f.records().some(record => record.kind === "listener"));
    f.child.stdin.write("hold this prompt\n");
    await until(() => f.prompts().length === 1);
    appendFileSync(f.events, `${"X".repeat(70 * 1024)}\n`);
    await Bun.sleep(50);
    appendFileSync(f.finish, "finish before debounce");
    await Bun.sleep(100);
    expect(f.prompts()).toHaveLength(1);
    await until(() => f.prompts().length === 2);
    const noticeOnly = f.prompts()[1].params.prompt[0].text;
    expect(noticeOnly).toContain("1 queued line was dropped; run lorvik --session cursor-boundary-check inbox.");
    expect(noticeOnly).not.toContain("Lorvik board event:");
    f.child.stdin.write("/quit\n");
    await f.exit;
  } finally { f.child.kill("SIGTERM"); }
}, 10_000);

test("bridge death closes listener stdin and stops the guarded ACP peer", async () => {
  const f = fixture();
  try {
    await until(() => f.records().some(record => record.kind === "listener"));
    f.child.kill("SIGKILL");
    await until(() => f.records().some(record => record.kind === "listener-stopped"));
    await until(() => f.records().some(record => record.kind === "agent-stopped"));
    expect(f.prompts()).toHaveLength(0);
  } finally { f.child.kill("SIGTERM"); }
}, 10_000);

test("normal close lets the guardian reap an ACP peer that ignores EOF and SIGTERM", async () => {
  const f = fixture(true);
  try {
    await until(() => f.records().some(record => record.kind === "listener"));
    const pid = f.records().find(record => record.kind === "agent").pid;
    f.child.stdin.write("/quit\n");
    await f.exit;
    await until(() => {
      try { process.kill(pid, 0); return false; }
      catch (error: any) { if (error.code === "ESRCH") return true; throw error; }
    });
    expect(f.records().some(record => record.kind === "listener-stopped")).toBe(true);
    expect(f.prompts()).toHaveLength(0);
  } finally { f.child.kill("SIGTERM"); }
}, 10_000);
