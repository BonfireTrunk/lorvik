// SPDX-License-Identifier: Apache-2.0

import { spawn } from "node:child_process";
import { accessSync, constants, statSync } from "node:fs";
import { delimiter, dirname, join, resolve } from "node:path";
import { createInterface } from "node:readline";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

function hasBinding(cwd: string): boolean {
  for (let dir = resolve(cwd); ; dir = dirname(dir)) {
    try {
      if (statSync(join(dir, ".lorvik.toml")).isFile()) return true;
    } catch {}
    if (dirname(dir) === dir) return false;
  }
}

function findLorvik(cwd: string, path: string | undefined): string | undefined {
  if (path === undefined) return;
  const name = process.platform === "win32" ? "lorvik.exe" : "lorvik";
  for (const dir of path.split(delimiter)) {
    const file = resolve(cwd, dir, name);
    try {
      accessSync(file, constants.X_OK);
      if (statSync(file).isFile()) return file;
    } catch {}
  }
}

export default function lorvikWake(pi: ExtensionAPI): void {
  const environment = { ...process.env };
  let sessionId: string | undefined;
  let warnedAboutConfig = false;
  let stopListener: () => Promise<void> = async () => {};

  pi.on("session_start", async (_event, ctx) => {
    const id = ctx.sessionManager.getSessionId();
    if (id === sessionId) return;
    await stopListener();
    sessionId = id;

    if (!hasBinding(ctx.cwd)) return;
    const executable = findLorvik(ctx.cwd, environment.PATH);
    if (!executable) return;
    if (!environment.LORVIK_CONFIG?.trim()) {
      if (!warnedAboutConfig && ctx.hasUI) {
        warnedAboutConfig = true;
        ctx.ui.notify(
          "Lorvik wake disabled: set LORVIK_CONFIG before starting Pi to select this agent's config.",
          "warning",
        );
      }
      return;
    }

    const child = spawn(executable, ["inbox", "--follow"], {
      cwd: ctx.cwd,
      env: { ...environment, PI_SESSION_ID: id },
      stdio: ["ignore", "pipe", "pipe"],
      detached: false,
      shell: false,
    });
    let stopped = false;
    let closed = false;
    let lastStderr = "";
    let spawnError = "";
    let lines: string[] = [];
    let batchTimer: ReturnType<typeof setTimeout> | undefined;

    const flush = () => {
      if (batchTimer) clearTimeout(batchTimer);
      batchTimer = undefined;
      if (stopped || lines.length === 0) return;
      const batch = lines;
      lines = [];
      pi.sendUserMessage(
        `Lorvik board events for this chat:\n${batch.join("\n")}\nRun lorvik inbox, then act on them as the lorvik skill says.`,
        { deliverAs: "followUp" },
      );
    };

    const stdout = createInterface({ input: child.stdout, crlfDelay: Infinity });
    const stderr = createInterface({ input: child.stderr, crlfDelay: Infinity });
    stdout.on("line", (line) => {
      if (stopped || !line.trim()) return;
      lines.push(line);
      if (!batchTimer) batchTimer = setTimeout(flush, 250);
    });
    stderr.on("line", (line) => {
      if (line.trim()) lastStderr = line;
    });
    child.on("error", (error) => {
      spawnError = error.message;
    });
    const done = new Promise<void>((finish) => {
      child.once("close", (code, signal) => {
        closed = true;
        stdout.close();
        stderr.close();
        if (!stopped) {
          flush();
          if (ctx.hasUI) {
            ctx.ui.notify(
              `Lorvik wake stopped: ${lastStderr || spawnError || `listener exited (${signal ?? code})`}`,
              "error",
            );
          }
        }
        finish();
      });
    });

    stopListener = async () => {
      stopped = true;
      if (batchTimer) clearTimeout(batchTimer);
      batchTimer = undefined;
      lines = [];
      if (closed) return;
      child.kill("SIGTERM");
      const killTimer = setTimeout(() => child.kill("SIGKILL"), 1000);
      try {
        await done;
      } finally {
        clearTimeout(killTimer);
      }
    };
  });

  pi.on("session_shutdown", async () => {
    await stopListener();
    sessionId = undefined;
  });
}
