// SPDX-License-Identifier: Apache-2.0

import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { randomUUID } from "node:crypto";
import { statSync } from "node:fs";
import { dirname, isAbsolute, join, resolve } from "node:path";
import type { Readable, Writable } from "node:stream";
import { fileURLToPath } from "node:url";

type RPC = { jsonrpc?: string; id?: string | number; method?: string; params?: any; result?: any; error?: any };
type Child = ChildProcessWithoutNullStreams;
type Task = { automatic: boolean; rpc?: RPC; humanBytes?: number };

const MAX_FRAME_BYTES = 1024 * 1024;
const MAX_HUMAN_REQUESTS = 64;
const MAX_HUMAN_BYTES = 1024 * 1024;
const BOARD_DELAY_MS = 250;

function bytes(value: string): number { return Buffer.byteLength(value); }

function bound(cwd: string): boolean {
  for (let dir = resolve(cwd); ; dir = dirname(dir)) {
    try { if (statSync(join(dir, ".lorvik.toml")).isFile()) return true; } catch {}
    if (dirname(dir) === dir) return false;
  }
}

// This deliberately does not use readline: readline retains an unbounded partial
// line, while listener stdout is outside the proxy's trust boundary.
function framedLines(stream: Readable, limit: number, line: (value: string) => void, oversized: () => void): { finish: () => void } {
  let partial = "";
  let discarding = false;
  stream.setEncoding("utf8");
  stream.on("data", (chunk: string) => {
    let cursor = 0;
    while (cursor < chunk.length) {
      const newline = chunk.indexOf("\n", cursor);
      if (newline < 0) {
        const tail = chunk.slice(cursor);
        if (!discarding && bytes(partial) + bytes(tail) > limit) {
          partial = "";
          discarding = true;
          oversized();
        } else if (!discarding) partial += tail;
        return;
      }
      const segment = chunk.slice(cursor, newline).replace(/\r$/, "");
      cursor = newline + 1;
      if (discarding) {
        discarding = false;
        continue;
      }
      const complete = partial + segment;
      partial = "";
      if (bytes(complete) > limit) oversized();
      else line(complete);
    }
  });
  return {
    finish: () => {
      // A partial final record is not an event line. Count it once so the
      // eventual recovery instruction tells the agent to reread the inbox.
      if (partial) {
        partial = "";
        oversized();
      }
    },
  };
}

class BoardBuffer {
  private lines: string[] = [];
  private lineBytes = 0;
  private dropped = 0;

  get hasData(): boolean { return this.lines.length > 0 || this.dropped > 0; }

  drop(): void { this.dropped++; }

  add(value: string, limit: number): void {
    const size = bytes(value) + 1; // The injected event text retains its newline.
    if (size > limit) { this.dropped++; return; }
    while (this.lines.length > 0 && this.lineBytes + size > limit) {
      const old = this.lines.shift()!;
      this.lineBytes -= bytes(old) + 1;
      this.dropped++;
    }
    if (this.lineBytes + size > limit) { this.dropped++; return; }
    this.lines.push(value);
    this.lineBytes += size;
  }

  take(limit: number, label: string): string {
    const notice = () => this.dropped === 0 ? "" :
      `Lorvik wake dropped ${this.dropped} board event line(s); run lorvik --session ${label} inbox for the complete inbox.`;
    for (;;) {
      const parts = [...this.lines];
      const dropped = notice();
      if (dropped) parts.push(dropped);
      const text = parts.join("\n");
      if (bytes(text) <= limit) {
        this.lines = [];
        this.lineBytes = 0;
        this.dropped = 0;
        return text;
      }
      if (this.lines.length === 0) {
        const fallback = dropped.slice(0, limit);
        this.dropped = 0;
        return fallback;
      }
      const old = this.lines.shift()!;
      this.lineBytes -= bytes(old) + 1;
      this.dropped++;
    }
  }
}

function maskInitializeCapabilities(rpc: RPC): void {
  const capabilities = rpc.result?.agentCapabilities;
  if (!capabilities || typeof capabilities !== "object") return;
  // Goose v1.57.0 advertises load/list/delete. They would escape this
  // one-new-chat proxy, so advertise only the operations it can actually own.
  capabilities.loadSession = false;
  const sessions = capabilities.sessionCapabilities;
  if (sessions && typeof sessions === "object") {
    delete sessions.list;
    delete sessions.delete;
    delete sessions.fork;
  }
}

export function createProxy(options: {
  agent: Child;
  input: Readable;
  output: Writable;
  launchListener: (cwd: string) => Child;
  warn: (message: string) => void;
  armed: boolean;
  sessionLabel?: string;
}) {
  const { agent, input, output, launchListener, warn } = options;
  const privatePrefix = `lorvik-${randomUUID()}-`;
  const label = options.sessionLabel ?? "the configured Lorvik session label";
  const prefix = "Lorvik board events for this chat:\n";
  const suffix = `\nRun lorvik --session ${label} inbox, then act on them as the lorvik skill says.`;
  const boardContentLimit = MAX_FRAME_BYTES - bytes(prefix) - bytes(suffix);
  let serial = 0;
  let closed = false;
  let created = false;
  let setup: { id: string | number; cwd: string } | undefined;
  let initializeID: string | number | undefined;
  let sessionId: string | undefined;
  let listener: Child | undefined;
  let listenerClosed = false;
  let listenerWarned = false;
  let stopped = false;
  let stderr = "";
  let batchTimer: ReturnType<typeof setTimeout> | undefined;
  let queue: Task[] = [];
  let active: Task | undefined;
  let boardQueued = false;
  let humanOutstanding = 0;
  let humanOutstandingBytes = 0;
  const pendingBoard = new BoardBuffer();
  let clientPausedForAgent = false;
  let agentPausedForClient = false;
  const writeAgent = (rpc: RPC) => {
    if (agent.stdin.write(JSON.stringify(rpc) + "\n")) return;
    if (clientPausedForAgent) return;
    clientPausedForAgent = true;
    input.pause();
    agent.stdin.once("drain", () => {
      clientPausedForAgent = false;
      if (!closed) input.resume();
    });
  };
  const writeClient = (rpc: RPC) => {
    if (output.write(JSON.stringify(rpc) + "\n")) return;
    if (agentPausedForClient) return;
    agentPausedForClient = true;
    agent.stdout.pause();
    output.once("drain", () => {
      agentPausedForClient = false;
      if (!closed) agent.stdout.resume();
    });
  };
  const reject = (rpc: RPC, message: string, code = -32602) => {
    if (rpc.id !== undefined) writeClient({ jsonrpc: "2.0", id: rpc.id, error: { code, message } });
  };
  const prompt = (text: string): RPC => ({
    jsonrpc: "2.0", id: privatePrefix + ++serial, method: "session/prompt",
    params: { sessionId, prompt: [{ type: "text", text }] },
  });
  const pump = () => {
    if (closed || active || queue.length === 0) return;
    active = queue.shift()!;
    if (active.automatic) {
      // The marker is deliberately rendered only when it reaches dispatch.
      // Events arriving while it waits behind a human prompt coalesce into this
      // one pending delivery rather than creating a second automatic task.
      active.rpc = prompt(prefix + pendingBoard.take(boardContentLimit, label) + suffix);
      warn("Lorvik: delivering board events to the open ACP chat");
    }
    writeAgent(active.rpc!);
  };
  const scheduleBoard = () => {
    if (closed || stopped || !sessionId || boardQueued || !pendingBoard.hasData) return;
    boardQueued = true;
    queue.push({ automatic: true });
    pump();
  };
  const flushBoard = () => {
    if (batchTimer) clearTimeout(batchTimer);
    batchTimer = undefined;
    scheduleBoard();
  };
  const admitBoardLine = (line: string) => {
    if (stopped || closed || !line.trim()) return;
    pendingBoard.add(line, boardContentLimit);
    if (!batchTimer) batchTimer = setTimeout(flushBoard, BOARD_DELAY_MS);
  };
  const dropBoardLine = () => {
    if (stopped || closed) return;
    pendingBoard.drop();
    if (!batchTimer) batchTimer = setTimeout(flushBoard, BOARD_DELAY_MS);
  };
  const terminate = (child: Child, eofFirst = false): Promise<void> => {
    if (child.exitCode !== null || child.signalCode !== null) return Promise.resolve();
    return new Promise((done) => {
      const termTimer = setTimeout(() => child.kill("SIGTERM"), eofFirst ? 1000 : 0);
      const killTimer = setTimeout(() => child.kill("SIGKILL"), eofFirst ? 2000 : 1000);
      child.once("close", () => { clearTimeout(termTimer); clearTimeout(killTimer); done(); });
      if (eofFirst) child.stdin.end();
      else child.kill("SIGTERM");
    });
  };
  let stopping: Promise<void> | undefined;
  const stop = (): Promise<void> => {
    if (stopping) return stopping;
    closed = true;
    stopped = true;
    if (batchTimer) clearTimeout(batchTimer);
    batchTimer = undefined;
    queue = [];
    input.pause();
    stopping = Promise.all([terminate(agent), ...(listener && !listenerClosed ? [terminate(listener, true)] : [])]).then(() => {});
    return stopping;
  };
  const startListener = (cwd: string) => {
    if (!options.armed || !bound(cwd)) return;
    try { listener = launchListener(cwd); } catch { warn("Lorvik wake stopped: could not launch listener"); return; }
    const listenerOutput = framedLines(listener.stdout, MAX_FRAME_BYTES, admitBoardLine, dropBoardLine);
    framedLines(listener.stderr, 64 * 1024, (line) => { if (line.trim()) stderr = line.slice(-8192); }, () => { stderr = "listener stderr line exceeded 64 KiB"; });
    listener.on("error", () => { stderr = "listener could not start"; });
    listener.stdin.on("error", () => {});
    listener.once("close", (code, signal) => {
      listenerClosed = true;
      listenerOutput.finish();
      if (!stopped && !closed) {
        flushBoard();
        if (!listenerWarned) {
          listenerWarned = true;
          warn(`Lorvik wake stopped: ${stderr || `listener exited (${signal ?? code})`}`);
        }
      }
    });
  };
  const cancelQueuedHumanPrompts = () => {
    const retained: Task[] = [];
    for (const task of queue) {
      if (task.automatic) retained.push(task);
      else {
        humanOutstanding--;
        humanOutstandingBytes -= task.humanBytes!;
        writeClient({ jsonrpc: "2.0", id: task.rpc!.id, result: { stopReason: "cancelled" } });
      }
    }
    queue = retained;
  };
  const handleClientLine = (line: string) => {
    if (closed) return;
    let rpc: RPC;
    try { rpc = JSON.parse(line); if (!rpc || typeof rpc !== "object" || Array.isArray(rpc)) throw Error(); }
    catch { warn("Lorvik ACP proxy stopped: invalid client JSON"); void stop(); return; }
    if (typeof rpc.id === "string" && rpc.id.startsWith(privatePrefix)) { reject(rpc, "Reserved request ID"); return; }
    if (rpc.method === "initialize" && rpc.id !== undefined) initializeID = rpc.id;
    if (rpc.method === "session/load" || rpc.method === "session/fork" || rpc.method === "session/delete" || rpc.method === "session/list") {
      reject(rpc, "Lorvik proxy supports one new chat per process; loading, listing, forking, and deleting are disabled");
      return;
    }
    if (rpc.method === "session/new") {
      if (created || setup || rpc.id === undefined || !isAbsolute(rpc.params?.cwd ?? "")) { reject(rpc, "Use a fresh proxy process for each chat and an absolute cwd"); return; }
      setup = { id: rpc.id, cwd: rpc.params.cwd };
    }
    if (rpc.method === "session/close") {
      if (rpc.params?.sessionId !== sessionId) { reject(rpc, "Unknown chat"); return; }
      if (rpc.id !== undefined) writeClient({ jsonrpc: "2.0", id: rpc.id, result: {} });
      void stop();
      return;
    }
    if (rpc.method === "session/cancel") {
      if (!sessionId || rpc.params?.sessionId !== sessionId) return;
      cancelQueuedHumanPrompts();
      if (active) writeAgent(rpc);
      return;
    }
    if (rpc.method === "session/prompt") {
      if (!sessionId || rpc.params?.sessionId !== sessionId || rpc.id === undefined) { reject(rpc, "Prompt must target the newly created chat"); return; }
      const humanBytes = bytes(line) + 1;
      if (humanOutstanding >= MAX_HUMAN_REQUESTS || humanOutstandingBytes + humanBytes > MAX_HUMAN_BYTES) {
        reject(rpc, "Lorvik proxy human prompt queue is full", -32000);
        return;
      }
      humanOutstanding++;
      humanOutstandingBytes += humanBytes;
      queue.push({ rpc, automatic: false, humanBytes });
      pump();
    } else writeAgent(rpc);
  };
  framedLines(input, MAX_FRAME_BYTES, handleClientLine, () => { warn("Lorvik ACP proxy stopped: client JSON line exceeds 1 MiB"); void stop(); });
  framedLines(agent.stdout, MAX_FRAME_BYTES, (line) => {
    if (closed) return;
    let rpc: RPC;
    try { rpc = JSON.parse(line); if (!rpc || typeof rpc !== "object" || Array.isArray(rpc)) throw Error(); }
    catch { warn("Lorvik ACP proxy stopped: invalid agent JSON"); void stop(); return; }
    const response = rpc.id !== undefined && !rpc.method;
    if (response && initializeID !== undefined && rpc.id === initializeID) maskInitializeCapabilities(rpc);
    if (response && setup && rpc.id === setup.id) {
      const pending = setup;
      setup = undefined;
      if (!rpc.error && typeof rpc.result?.sessionId === "string") {
        sessionId = rpc.result.sessionId;
        created = true;
        startListener(pending.cwd);
      }
    }
    if (response && active && rpc.id === active.rpc?.id) {
      const finished = active;
      active = undefined;
      if (!finished.automatic) {
        humanOutstanding--;
        humanOutstandingBytes -= finished.humanBytes!;
        writeClient(rpc);
      } else if (rpc.error) warn("Lorvik wake delivery failed; not retried. Read lorvik inbox manually.");
      if (finished.automatic) {
        boardQueued = false;
        if (pendingBoard.hasData) flushBoard();
      }
      pump();
    } else writeClient(rpc);
  }, () => { warn("Lorvik ACP proxy stopped: agent JSON line exceeds 1 MiB"); void stop(); });
  input.once("end", () => { if (!closed) void stop(); });
  input.once("close", () => { if (!closed) void stop(); });
  input.once("error", () => { if (!closed) void stop(); });
  agent.once("close", () => { if (!closed) { warn("Lorvik ACP connection ended"); void stop(); } });
  agent.on("error", () => { warn("Lorvik ACP proxy stopped: Goose could not start"); void stop(); });
  agent.stdin.on("error", () => { if (!closed) void stop(); });
  output.on("error", () => { void stop(); });
  output.once("close", () => { if (!closed) void stop(); });
  return { stop };
}

const invokedAsProgram = process.argv.some(arg => arg === fileURLToPath(import.meta.url));

if (invokedAsProgram) {
  const armed = process.argv.slice(2).includes("--arm");
  if (process.argv.slice(2).some((arg) => arg !== "--arm")) throw Error("Usage: lorvik-wake.ts [--arm]");
  const environment = { ...process.env };
  if (!environment.LORVIK_CONFIG?.trim() || !/^[a-z0-9][a-z0-9-]{0,39}$/.test(environment.LORVIK_SESSION ?? "")) {
    throw Error("Set LORVIK_CONFIG and a unique LORVIK_SESSION before starting the ACP client");
  }
  const agent = spawn("goose", ["acp", "--with-builtin", "developer"], { env: environment, shell: false, detached: false, stdio: ["pipe", "pipe", "pipe"] });
  agent.stderr.pipe(process.stderr, { end: false });
  const proxy = createProxy({
    agent, input: process.stdin, output: process.stdout, armed,
    sessionLabel: environment.LORVIK_SESSION,
    warn: (message) => process.stderr.write(message + "\n"),
    // Keep stdin open but untouched. inbox --exit-on-eof owns abrupt owner-death cleanup.
    launchListener: (cwd) => spawn("lorvik", ["--session", environment.LORVIK_SESSION!, "inbox", "--follow", "--exit-on-eof"], { cwd, env: environment, shell: false, detached: false, stdio: ["pipe", "pipe", "pipe"] }),
  });
  for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"] as const) process.on(signal, () => { void proxy.stop(); });
}
