// SPDX-License-Identifier: Apache-2.0

import { test, expect } from "bun:test";
import { spawn } from "node:child_process";
import { EventEmitter } from "node:events";
import { PassThrough } from "node:stream";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, symlinkSync, unlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { startBridge, transport, type Options, type Request } from "./lorvik-wake";

function fixture(mode = "lines") {
  const dir = mkdtempSync(join(tmpdir(), "lorvik-crush-check-"));
  const owner = spawn(process.execPath, ["-e", "setInterval(() => {}, 10000)"], { stdio: "ignore" });
  writeFileSync(join(dir, ".lorvik.toml"), "# fake binding; no server\n");
  writeFileSync(join(dir, "crush.sock"), "not a server\n");
  writeFileSync(join(dir, "lorvik"), `#!/usr/bin/env node
const { writeFileSync, writeSync } = require("node:fs");
writeFileSync(process.env.CAPTURE, JSON.stringify({ argv: process.argv.slice(2), label: process.env.LORVIK_SESSION, config: process.env.LORVIK_CONFIG }));
if (process.env.MODE !== "silent") {
  writeSync(1, 'now ana commented on #42 "Fix');
  writeSync(1, ' login"\\nnow ana commented $(never-execute)\\n');
}
if (process.env.MODE === "exit") { writeSync(2, "synthetic listener exit\\n"); process.exit(7); }
setInterval(() => {}, 10000);
`, { mode: 0o700 });
  const options: Options = {
    socket: join(dir, "crush.sock"), workspace: "workspace-1", nativeSession: "native-1",
    session: "test-crush-chat", ownerPid: owner.pid!, cwd: dir, lockDir: join(dir, "locks"),
    environment: { ...process.env, PATH: `${dir}:${process.env.PATH}`, MODE: mode, CAPTURE: join(dir, "capture.json"), LORVIK_CONFIG: "/fake/agent.toml" },
  };
  const child = Object.assign(new EventEmitter(), {
    stdin: new PassThrough(), stdout: new PassThrough(), stderr: new PassThrough(),
    kill: () => { queueMicrotask(() => child.emit("close", 0, "SIGTERM")); return true; },
  });
  const launches: any[][] = [];
  const launch = ((...args: any[]) => { launches.push(args); return child; }) as unknown as typeof spawn;
  return { dir, options, child, launch, launches, cleanup: () => { owner.kill("SIGTERM"); rmSync(dir, { recursive: true, force: true }); } };
}

function boundary() {
  const posts: { path: string; body: any }[] = [];
  let signal!: () => void;
  const delivered = new Promise<void>((resolve) => { signal = resolve; });
  const request: Request = async (method, path, body) => {
    if (method === "GET") return { id: "native-1", attached_clients: 1, is_busy: true };
    posts.push({ path, body }); signal();
  };
  return { request, posts, delivered };
}

function wait(ms: number) { return new Promise<void>((resolve) => setTimeout(resolve, ms)); }

async function waitFor(check: () => boolean, message: string) {
  for (let i = 0; i < 40; i++) { if (check()) return; await wait(50); }
  throw new Error(message);
}

test("listener label, fragmented batching, literal text and later-turn run_id reach injection boundary", async () => {
  const f = fixture(); const b = boundary();
  const bridge = await startBridge(f.options, b.request, () => {}, f.launch);
  try {
    f.child.stdout.write('now ana commented on #42 "Fix');
    f.child.stdout.write(' login"\nnow ana commented $(never-execute)\n');
    await b.delivered;
    expect(b.posts).toHaveLength(1);
    expect(b.posts[0].path).toBe("/v1/workspaces/workspace-1/agent");
    expect(b.posts[0].body.session_id).toBe("native-1");
    expect(b.posts[0].body.run_id).toMatch(/^[0-9a-f-]{36}$/);
    expect(b.posts[0].body.prompt).toContain('now ana commented on #42 "Fix login"\nnow ana commented $(never-execute)');
    expect(f.launches[0][1]).toEqual(["--session", "test-crush-chat", "inbox", "--follow", "--exit-on-eof"]);
    expect(f.launches[0][2].env.LORVIK_SESSION).toBe("test-crush-chat");
    expect(f.launches[0][2].env.LORVIK_CONFIG).toBe("/fake/agent.toml");
    expect(f.launches[0][2].shell).toBe(false);
    await expect(startBridge(f.options, b.request, () => {}, f.launch)).rejects.toThrow();
  } finally { await bridge.stop(); f.cleanup(); }
}, 5000);

test("self-exit preserves final batch and reports once without restart", async () => {
  const f = fixture("exit"); const b = boundary(); const reports: string[] = [];
  let reported!: () => void;
  const noticed = new Promise<void>((resolve) => { reported = resolve; });
  const bridge = await startBridge(f.options, b.request, (message) => { reports.push(message); reported(); }, f.launch);
  try {
    f.child.stdout.write("now ana commented on #42\n");
    f.child.stderr.write("synthetic listener exit\n");
    f.child.emit("close", 7, null);
    await noticed;
    expect(b.posts).toHaveLength(1);
    expect(reports).toHaveLength(1);
    expect(reports[0]).toContain("synthetic listener exit");
    expect(reports[0]).toContain("Not restarting");
  } finally { await bridge.stop(); f.cleanup(); }
}, 5000);

test("detached chat refuses listener startup", async () => {
  const f = fixture("silent");
  try {
    await expect(startBridge(f.options, async () => ({ id: "native-1", attached_clients: 0 }), () => {}, f.launch)).rejects.toThrow("not attached");
  } finally { f.cleanup(); }
}, 5000);

test("explicit teardown discards pending batches and releases per-chat lock", async () => {
  const f = fixture("silent"); const b = boundary();
  try {
    const bridge = await startBridge(f.options, b.request, () => {}, f.launch);
    f.child.stdout.write("pending event\n");
    await bridge.stop();
    expect(b.posts).toHaveLength(0);
    const second = await startBridge(f.options, b.request, () => {}, f.launch);
    await second.stop();
  } finally { f.cleanup(); }
}, 5000);

test("synchronous listener-launch failure releases the per-chat lock", async () => {
  const f = fixture("silent"); const b = boundary();
  const failingLaunch = (() => { throw new Error("synthetic spawn failure"); }) as unknown as typeof spawn;
  try {
    await expect(startBridge(f.options, b.request, () => {}, failingLaunch)).rejects.toThrow("synthetic spawn failure");
    const retry = await startBridge(f.options, b.request, () => {}, f.launch);
    await retry.stop();
  } finally { f.cleanup(); }
}, 5000);

test("post-spawn metadata failure waits for listener termination before unlocking", async () => {
  const f = fixture("silent"); const b = boundary();
  const child = Object.assign(new EventEmitter(), {
    pid: 999_999_999, stdin: new PassThrough(), stdout: new PassThrough(), stderr: new PassThrough(),
    kill: () => { queueMicrotask(() => child.emit("close", 0, "SIGTERM")); return true; },
  });
  const brokenLaunch = (() => child) as unknown as typeof spawn;
  try {
    await expect(startBridge(f.options, b.request, () => {}, brokenLaunch)).rejects.toThrow();
    const retry = await startBridge(f.options, b.request, () => {}, f.launch);
    await retry.stop();
  } finally { f.cleanup(); }
}, 5000);

test("release does not unlink a lock file replaced during shutdown", async () => {
  const f = fixture("silent"); const b = boundary();
  try {
    const bridge = await startBridge(f.options, b.request, () => {}, f.launch);
    const lock = join(f.options.lockDir!, readdirSync(f.options.lockDir!).find((name) => name.endsWith(".lock"))!);
    unlinkSync(lock);
    writeFileSync(lock, "replacement lock\n", { mode: 0o600 });
    await bridge.stop();
    expect(readFileSync(lock, "utf8")).toBe("replacement lock\n");
  } finally { f.cleanup(); }
}, 5000);

test("transport refuses remote origins and ambiguous endpoints without making requests", () => {
  expect(() => transport({ url: "http://example.com" })).toThrow("loopback");
  expect(() => transport({ url: "http://127.0.0.1:1/path" })).toThrow("loopback");
  expect(() => transport({ socket: "/tmp/test", url: "http://127.0.0.1" })).toThrow("exactly one");
});

test("real fake listener child starts with matching label and reaches stubbed injection call", async () => {
  const f = fixture(); const b = boundary();
  const bridge = await startBridge(f.options, b.request, () => {});
  try {
    await b.delivered;
    expect(b.posts).toHaveLength(1);
    expect(b.posts[0].body.prompt).toContain('now ana commented on #42 "Fix login"\nnow ana commented $(never-execute)');
    expect(JSON.parse(readFileSync(join(f.dir, "capture.json"), "utf8"))).toEqual({
      argv: ["--session", "test-crush-chat", "inbox", "--follow", "--exit-on-eof"], label: "test-crush-chat", config: "/fake/agent.toml",
    });
  } finally { await bridge.stop(); f.cleanup(); }
}, 5000);

test("global native-chat lock rejects endpoint aliases and unsafe lock paths", async () => {
  const f = fixture("silent"); const b = boundary();
  try {
    const first = await startBridge(f.options, b.request, () => {}, f.launch);
    const alias = { ...f.options, url: "http://127.0.0.1:9", socket: undefined };
    await expect(startBridge(alias, b.request, () => {}, f.launch)).rejects.toThrow();
    await first.stop();
    const unsafe = join(f.dir, "unsafe-locks");
    writeFileSync(unsafe, "not a directory\n");
    await expect(startBridge({ ...f.options, lockDir: unsafe }, b.request, () => {}, f.launch)).rejects.toThrow();
    const target = join(f.dir, "private-locks");
    const link = join(f.dir, "lock-link");
    mkdirSync(target, { mode: 0o700 });
    symlinkSync(target, link);
    await expect(startBridge({ ...f.options, lockDir: link }, b.request, () => {}, f.launch)).rejects.toThrow();
  } finally { f.cleanup(); }
}, 5000);

test("owner SIGKILL closes a silent listener through stdin EOF", async () => {
  const f = fixture("silent"); const b = boundary();
  const eofMarker = join(f.dir, "listener-eof");
  f.options.environment.EOF_MARKER = eofMarker;
  const code = "const { writeFileSync } = require('node:fs'); const hold = setInterval(() => {}, 1000); process.stdin.resume(); process.stdin.on('end', () => { writeFileSync(process.env.EOF_MARKER, 'eof'); clearInterval(hold); process.exit(0); });";
  const launch = ((_: string, _args: readonly string[], opts: any) => spawn(process.execPath, ["-e", code], opts)) as unknown as typeof spawn;
  try {
    const bridge = await startBridge(f.options, b.request, () => {}, launch);
    await wait(200);
    expect(bridge.child.exitCode).toBeNull();
    expect(() => readFileSync(eofMarker, "utf8")).toThrow();
    process.kill(f.options.ownerPid, "SIGKILL");
    await Promise.race([bridge.done, new Promise((_, reject) => setTimeout(() => reject(new Error("bridge did not stop")), 4000))]);
    expect(bridge.child.exitCode).toBe(0);
    expect(readFileSync(eofMarker, "utf8")).toBe("eof");
  } finally { try { process.kill(f.options.ownerPid, "SIGKILL"); } catch {} rmSync(f.dir, { recursive: true, force: true }); }
}, 7000);

test("sidecar SIGKILL closes the production listener through its held stdin pipe", async () => {
  const f = fixture("silent");
  const marker = join(f.dir, "sidecar-eof");
  const listenerPID = join(f.dir, "listener-pid");
  writeFileSync(join(f.dir, "lorvik"), "#!/bin/sh\nprintf '%s' \"$$\" > \"$LISTENER_PID\"\ncat >/dev/null\nprintf eof > \"$EOF_MARKER\"\n", { mode: 0o700 });
  chmodSync(join(f.dir, "lorvik"), 0o700);
  const adapter = pathToFileURL(resolve("skills/lorvik/adapters/crush/lorvik-wake.ts")).href;
  const hostCode = "const { startBridge } = await import(process.env.ADAPTER); await startBridge(JSON.parse(process.env.BRIDGE_OPTIONS), async (method) => method === 'GET' ? { id: 'native-1', attached_clients: 1 } : undefined); setInterval(() => {}, 1000);";
  const host = spawn(process.execPath, ["-e", hostCode], {
    cwd: f.dir,
    env: { ...f.options.environment, ADAPTER: adapter, LISTENER_PID: listenerPID, EOF_MARKER: marker, BRIDGE_OPTIONS: JSON.stringify({ ...f.options, environment: { ...f.options.environment, LISTENER_PID: listenerPID, EOF_MARKER: marker } }) },
    stdio: "ignore",
  });
  try {
    await waitFor(() => { try { return readFileSync(listenerPID, "utf8") !== ""; } catch { return false; } }, "listener did not start");
    const pid = Number(readFileSync(listenerPID, "utf8"));
    expect(() => process.kill(pid, 0)).not.toThrow();
    expect(() => readFileSync(marker, "utf8")).toThrow();
    process.kill(host.pid!, "SIGKILL");
    await waitFor(() => { try { return readFileSync(marker, "utf8") === "eof"; } catch { return false; } }, "listener did not receive EOF");
    await waitFor(() => { try { process.kill(pid, 0); return false; } catch { return true; } }, "listener did not exit after EOF");
    expect(() => process.kill(pid, 0)).toThrow();
  } finally {
    try { process.kill(host.pid!, "SIGKILL"); } catch {}
    try { process.kill(f.options.ownerPid, "SIGKILL"); } catch {}
    rmSync(f.dir, { recursive: true, force: true });
  }
}, 7000);

test("one in-flight delivery coalesces later batches into one bounded follow-up", async () => {
  const f = fixture("silent");
  const posts: any[] = [];
  let first!: () => void;
  const firstSeen = new Promise<void>((resolve) => { first = resolve; });
  let releaseFirst!: () => void;
  const firstBlocked = new Promise<void>((resolve) => { releaseFirst = resolve; });
  const request: Request = async (method, _path, body) => {
    if (method === "GET") return { id: "native-1", attached_clients: 1 };
    posts.push(body); first();
    if (posts.length === 1) await firstBlocked;
  };
  const bridge = await startBridge(f.options, request, () => {}, f.launch);
  try {
    f.child.stdout.write("first\n");
    await firstSeen;
    f.child.stdout.write("second\n");
    await wait(300);
    f.child.stdout.write("third\n");
    await wait(300);
    expect(posts).toHaveLength(1);
    releaseFirst();
    await wait(350);
    expect(posts).toHaveLength(2);
    expect(posts[1].prompt).toContain("second\nthird");
  } finally { await bridge.stop(); f.cleanup(); }
}, 5000);

test("overflow retains newest full line and includes one inbox notice", async () => {
  const f = fixture("silent"); const b = boundary();
  const bridge = await startBridge(f.options, b.request, () => {}, f.launch);
  try {
    f.child.stdout.write(`${"a".repeat(600_000)}\n${"b".repeat(600_000)}\n`);
    await b.delivered;
    expect(b.posts).toHaveLength(1);
    expect(b.posts[0].body.prompt).toContain("1 board line(s) were dropped");
    expect(b.posts[0].body.prompt).toContain("b".repeat(100));
    expect(b.posts[0].body.prompt).not.toContain("a".repeat(100));
  } finally { await bridge.stop(); f.cleanup(); }
}, 5000);

test("prompt reserve still evicts an older near-limit line for the newest line", async () => {
  const f = fixture("silent"); const b = boundary();
  const bridge = await startBridge(f.options, b.request, () => {}, f.launch);
  try {
    f.child.stdout.write(`${"a".repeat(1_040_000)}\n${"b".repeat(6_000)}\n`);
    await b.delivered;
    expect(b.posts[0].body.prompt).toContain("b".repeat(100));
    expect(b.posts[0].body.prompt).not.toContain("a".repeat(100));
    expect(b.posts[0].body.prompt).toContain("1 board line(s) were dropped");
  } finally { await bridge.stop(); f.cleanup(); }
}, 5000);

test("listener EOF drops a short unterminated partial line with one inbox notice", async () => {
  const f = fixture("silent"); const b = boundary();
  const bridge = await startBridge(f.options, b.request, () => {}, f.launch);
  try {
    f.child.stdout.write("final event without newline");
    f.child.emit("close", 0, null);
    await b.delivered;
    expect(b.posts).toHaveLength(1);
    expect(b.posts[0].body.prompt).toContain("1 board line(s) were dropped");
    expect(b.posts[0].body.prompt).not.toContain("final event without newline");
  } finally { await bridge.stop(); f.cleanup(); }
}, 5000);

test("stop aborts an uncertain request and discards a queued follow-up", async () => {
  const f = fixture("silent");
  const posts: any[] = [];
  let started!: () => void;
  const startedPost = new Promise<void>((resolve) => { started = resolve; });
  const request: Request = async (method, _path, body, signal) => {
    if (method === "GET") return { id: "native-1", attached_clients: 1 };
    posts.push(body); started();
    await new Promise<void>((_resolve, reject) => signal?.addEventListener("abort", () => reject(new Error("aborted")), { once: true }));
  };
  const bridge = await startBridge(f.options, request, () => {}, f.launch);
  try {
    f.child.stdout.write("first\n");
    await startedPost;
    f.child.stdout.write("queued\n");
    await wait(300);
    await bridge.stop("test stop");
    expect(posts).toHaveLength(1);
  } finally { f.cleanup(); }
}, 5000);
