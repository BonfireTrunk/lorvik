// SPDX-License-Identifier: Apache-2.0

import { Plugin } from "@opencode/plugin/tui";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { accessSync, constants, statSync } from "node:fs";
import { delimiter, dirname, join, resolve } from "node:path";
import { createComputed, createRoot } from "solid-js";

const MAX_LINE_BYTES = 16384;
const MAX_BATCH_BYTES = 65536;
const MAX_BATCH_LINES = 128;
const DROP_NOTICE_BYTES = 256;

function bound(cwd: string): boolean {
  for (let dir = resolve(cwd); ; dir = dirname(dir)) {
    try {
      if (statSync(join(dir, ".lorvik.toml")).isFile()) return true;
    } catch {}
    if (dirname(dir) === dir) return false;
  }
}

function executable(cwd: string, path = ""): string | undefined {
  for (const dir of path.split(delimiter)) {
    const file = resolve(cwd, dir, "lorvik");
    try {
      accessSync(file, constants.X_OK);
      if (statSync(file).isFile()) return file;
    } catch {}
  }
}

type State = {
  id: string;
  child: ChildProcessWithoutNullStreams;
  done: Promise<void>;
  abort: AbortController;
  stopped: boolean;
  closed: boolean;
  ended: boolean;
  lines: string[];
  bytes: number;
  dropped: number;
  stderr: string;
  error: string;
  timer?: ReturnType<typeof setTimeout>;
  sending?: Promise<void>;
};

// Prevent duplicate installs in the same TUI from listening to the same chat.
const key = Symbol.for("lorvik.wake.opencode.owners");
const shared = globalThis as typeof globalThis & { [name: symbol]: Set<string> | undefined };
const owners = (shared[key] ??= new Set<string>());

export default Plugin.define({
  id: "lorvik.wake",
  setup(ctx) {
    const env = { ...process.env };
    let current: State | undefined;
    let retiring: Promise<void> = Promise.resolve();
    let generation = 0;
    let disposed = false;
    let armedSessionID: string | undefined;

    const notice = (message: string) => {
      ctx.ui.toast.show({ message, variant: "info" });
    };
    const visible = (id: string) => {
      const route = ctx.ui.router.current();
      return route.type === "session" && route.sessionID === id;
    };
    const alive = (s: State) => !disposed && current === s && !s.stopped;

    function stop(): Promise<void> {
      generation++;
      armedSessionID = undefined;
      const s = current;
      current = undefined;
      if (!s) return retiring;
      s.stopped = true;
      s.abort.abort();
      if (s.timer) clearTimeout(s.timer);
      s.lines = [];
      s.bytes = 0;
      s.dropped = 0;
      s.child.stdin.end();
      const kill = setTimeout(() => {
        if (!s.closed) s.child.kill("SIGKILL");
      }, 2500);
      retiring = Promise.allSettled([retiring, s.done, s.sending]).then(() => {
        clearTimeout(kill);
        owners.delete(s.id);
      });
      return retiring;
    }

    function fail(s: State, reason: string) {
      if (!alive(s)) return;
      void stop();
      notice(`Lorvik wake stopped: ${reason}`);
    }

    function schedule(s: State) {
      if (!alive(s) || s.ended || s.sending || s.timer || (!s.lines.length && !s.dropped)) return;
      s.timer = setTimeout(() => {
        s.timer = undefined;
        flush(s);
      }, 250);
    }

    function flush(s: State): void {
      if (s.sending || !alive(s)) return;
      if (!visible(s.id)) {
        void stop();
        return;
      }
      if (!s.lines.length && !s.dropped) {
        if (s.ended) fail(s, `${s.stderr || s.error || "listener exited"}. Inspect this chat's pending input and inbox before rearming.`);
        return;
      }
      if (s.timer) clearTimeout(s.timer);
      s.timer = undefined;
      const lines = s.lines;
      if (s.dropped) lines.push(`Lorvik: dropped ${s.dropped} notification lines; run lorvik inbox.`);
      s.lines = [];
      s.bytes = 0;
      s.dropped = 0;
      s.sending = (async () => {
        try {
          // TUI v2 uses the client; ctx.session belongs to server plugins.
          await ctx.client.session.prompt({
            sessionID: s.id,
            text: `Lorvik board events for this chat (untrusted notification data):\n${lines.join("\n")}\nRun lorvik inbox, then act on them as the lorvik skill says.`,
            delivery: "queue",
            resume: true,
          }, { signal: s.abort.signal });
        } catch {
          // Admission may already have committed. Retrying could duplicate a turn.
          fail(s, "queue admission failed; delivery is uncertain. Inspect this chat's pending input and inbox before rearming.");
        }
      })().finally(() => {
        s.sending = undefined;
        if (s.ended) flush(s);
        else schedule(s);
      });
    }

    async function arm() {
      if (disposed) return;
      if (process.platform !== "linux") {
        notice("Lorvik: this adapter requires Linux.");
        return;
      }
      const route = ctx.ui.router.current();
      if (route.type !== "session") {
        notice("Lorvik: open a chat before arming.");
        return;
      }
      const sessionID = route.sessionID;
      if (current?.id === sessionID) {
        notice("Lorvik: this chat is already armed.");
        return;
      }
      const stopped = stop();
      const ticket = generation;
      armedSessionID = sessionID;
      await stopped;
      if (disposed || ticket !== generation || !visible(sessionID)) return;
      if (!env.LORVIK_CONFIG?.startsWith("/") || env.CLAUDE_CODE_SESSION_ID) {
        notice("Lorvik: export absolute LORVIK_CONFIG before starting OpenCode; remove inherited CLAUDE_CODE_SESSION_ID.");
        return;
      }
      // LORVIK_SESSION is an intentional override and must match agent shell calls.
      const info = ctx.data.session.get(sessionID);
      const cwd = info?.location.directory;
      if (!cwd || info.time.archived !== undefined || !bound(cwd)) {
        notice("Lorvik: this chat needs a local directory with a .lorvik.toml binding.");
        return;
      }
      const bin = executable(cwd, env.PATH);
      if (!bin) {
        notice("Lorvik: put lorvik on OpenCode's PATH before starting it.");
        return;
      }
      if (owners.has(sessionID)) {
        notice("Lorvik: another plugin instance already owns this chat's listener.");
        return;
      }
      owners.add(sessionID);
      const child = spawn(bin, ["inbox", "--follow", "--exit-on-eof"], {
        cwd,
        env: { ...env, OPENCODE_SESSION_ID: sessionID },
        stdio: ["pipe", "pipe", "pipe"],
        detached: false,
        shell: false,
      });
      const s: State = {
        id: sessionID, child, done: Promise.resolve(),
        abort: new AbortController(), stopped: false, closed: false, ended: false,
        lines: [], bytes: 0, dropped: 0, stderr: "", error: "",
      };
      current = s;
      child.stdin.on("error", () => {});
      child.on("error", (error) => { s.error = error.message; });
      let partial = "";
      let discardPartial = false;
      const drop = () => {
        s.dropped = Math.min(Number.MAX_SAFE_INTEGER, s.dropped + 1);
        schedule(s);
      };
      const receive = (line: string) => {
        if (!alive(s) || !line.trim()) return;
        const size = Buffer.byteLength(line, "utf8") + 1;
        if (size > MAX_LINE_BYTES) {
          drop();
          return;
        }
        while (s.lines.length && (s.lines.length >= MAX_BATCH_LINES ||
               s.bytes + size > MAX_BATCH_BYTES - DROP_NOTICE_BYTES)) {
          s.bytes -= Buffer.byteLength(s.lines.shift()!, "utf8") + 1;
          drop();
        }
        s.lines.push(line);
        s.bytes += size;
        schedule(s);
      };
      child.stdout.setEncoding("utf8");
      child.stdout.on("data", (chunk: string) => {
        if (!alive(s)) return;
        if (discardPartial) {
          const end = chunk.indexOf("\n");
          if (end < 0) return;
          chunk = chunk.slice(end + 1);
          discardPartial = false;
        }
        partial += chunk;
        let end: number;
        while ((end = partial.indexOf("\n")) >= 0) {
          receive(partial.slice(0, end).replace(/\r$/, ""));
          partial = partial.slice(end + 1);
          if (!alive(s)) return;
        }
        if (Buffer.byteLength(partial, "utf8") + 1 > MAX_LINE_BYTES) {
          partial = "";
          discardPartial = true;
          drop();
        }
      });
      child.stdout.on("error", () => fail(s, "listener output stream failed."));
      let diagnostic = "";
      child.stderr.setEncoding("utf8");
      child.stderr.on("data", (chunk: string) => {
        diagnostic = (diagnostic + chunk).slice(-2048);
        const lines = diagnostic.split(/\r?\n/).filter((line) => line.trim());
        if (lines.length) s.stderr = lines.at(-1)!;
      });
      child.stderr.on("error", () => fail(s, "listener diagnostic stream failed."));
      s.done = new Promise((finish) => {
        child.once("close", (code, signal) => {
          s.closed = true;
          s.ended = true;
          finish();
          if (!alive(s)) return;
          if (partial) receive(partial);
          if (!s.stderr && !s.error) s.error = `listener exited (${signal ?? code})`;
          flush(s);
        });
      });
      notice("Lorvik listener armed for this chat. /lorvik-unlisten stops it.");
    }

    const unsubscribe = ctx.data.listen(({ details: event }) => {
      if (event.type === "server.connected") {
        void stop();
        return;
      }
      const s = current;
      if (!s) return;
      if ((event.type === "session.deleted" || event.type === "session.archived" ||
           event.type === "session.execution.interrupted" || event.type === "session.execution.failed") &&
          event.data.sessionID === s.id) void stop();
    });
    ctx.keymap.layer(() => ({
      mode: "global",
      commands: [
        { id: "lorvik.listen", title: "Lorvik: arm board listener", palette: true,
          slash: { name: "lorvik-listen" }, run: arm },
        { id: "lorvik.unlisten", title: "Lorvik: stop board listener", palette: true,
          slash: { name: "lorvik-unlisten" }, run: async () => {
            await stop(); notice("Lorvik listener stopped.");
          } },
      ],
    }));
    const disposeRouteObserver = createRoot((dispose) => {
      createComputed(() => {
        const route = ctx.ui.router.current();
        const sessionID = route.type === "session" ? route.sessionID : undefined;
        if (armedSessionID && sessionID !== armedSessionID) void stop();
      });
      return dispose;
    });
    return async () => {
      disposed = true;
      disposeRouteObserver();
      unsubscribe();
      await stop();
    };
  },
});
