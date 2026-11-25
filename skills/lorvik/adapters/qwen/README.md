# Lorvik wake for Qwen Code 0.26.7

Strategy: **opt-in injection into one live Ink TUI**, using its authenticated
peer inbox. The bridge runs `lorvik inbox --follow --exit-on-eof`, batches stdout lines for
about 250 ms, sends a `priority: "next"` user frame pinned to the current native
session, and waits for a `delivered` receipt. Qwen queues the message behind a
busy turn and submits it when idle. A receipt confirms **queued**, not read.

**Opt-in lifetime gap:** every batch already admitted can still run after the
chat or viewer closes. Closing a viewer is not a disarm operation: while its
owning TUI process/session/socket remains live and the bridge remains armed,
new board events keep starting turns. There is no one-turn ceiling. A held
batch or a send whose receipt timed out can also arrive after the bridge stops.
Disarming prevents new sends but cannot retract anything already inside Qwen.
Never arm it automatically or retry an uncertain send. The bridge never
resumes a closed session.

Harness claims were rechecked against the read-only `qwen-code` clone at
**v0.26.7** (`package.json:4`), rather than an installed package or `main`.
Paths below are relative to that clone. Real-harness integration remains untested.

## Install and explicitly arm

After the human chooses to install Qwen, keep this directory readable, put a
`lorvik` binary and Bun on PATH, and start in a bound Lorvik repository.
Needs a `lorvik` CLI with `inbox --follow --exit-on-eof`.
Keep `lorvik-wake.ts` and `delivery-buffer.mjs` together; the bridge imports the
local helper and has no third-party adapter dependency to install.
Export the agent config and a label private to this chat **before starting
Qwen**, so later shell tools and the listener share them:

```sh
export LORVIK_CONFIG=/absolute/path/to/agent.toml
export LORVIK_SESSION=qwen-lorvik-1
qwen
```

Use a new label for each new chat, including after `/clear` or a session switch;
relaunch Qwen with that startup label before re-arming.
Lorvik derives a session label from the native shell variable
**`QWEN_CODE_SESSION_ID`** (`internal/cli/session.go`). The adapter still uses
the explicit Lorvik label, which takes precedence over it. All Lorvik tool calls in this
chat must inherit the startup variables or supply the same config and
`--session qwen-lorvik-1`. An export in one tool call does not update Qwen's
parent environment or later tool calls.

Ask the agent to arm the bridge with this `run_shell_command` call:

```json
{
  "command": "bun /absolute/path/to/skills/lorvik/adapters/qwen/lorvik-wake.ts",
  "description": "Wait for Lorvik events for this chat",
  "is_background": true
}
```

Use Qwen's tracked background execution. Do not append `&`, use `monitor`, or
launch a headless resume. Save the background task ID returned by Qwen. Disarm
with `task_stop({"task_id":"<returned ID>"})` before ending the chat. These
tool names and arguments are verified below. No Qwen extension installation
is needed for this bridge.

The child must inherit `QWEN_CODE_SESSION_ID`,
`QWEN_CODE_MESSAGING_SOCKET`, and `QWEN_CODE_MESSAGING_TOKEN` from that live
TUI. The bridge checks the exact socket/session against a live registry record
under `$QWEN_HOME/sessions` (default `~/.qwen/sessions`) and pins its PID,
Linux boot/start identity and PID namespace. It fails closed without these
values or a live `kind: "tui"` record. This excludes unsupported frontends,
disabled messaging, bare/safe modes, and ACP records without those capabilities.
Never print the registry records or credentials into chat or logs.

A deterministic per-session lock allows one bridge/listener. It is retained
until the old listener closes, including the one-second SIGTERM-to-SIGKILL
grace period. A stale lock after a hard kill needs manual inspection: the file
contains the bridge PID. Do not remove a lock belonging to a live bridge.
Missing config, native capability, or executable requires fixing the cause
and explicitly re-arming; there is no automatic listener restart.

Runtime writes are limited to:

- `${XDG_RUNTIME_DIR || "/tmp"}/lorvik-qwen-wake-<uid>-<session-hash>.lock`,
  where the hash is the first 24 hex characters of SHA-256 of the native Qwen
  session ID. Created with mode `0600`, it contains only the bridge PID and
  a newline, not credentials.
- `os.tmpdir()/lorvik-qwen-wake-*/`, a private `0700` temporary directory
  containing `reply.sock` (explicitly `0600`). On Linux `os.tmpdir()` normally
  resolves to `/tmp`, with the process's temporary-directory environment taking
  precedence. The receipt token stays in memory, not a file.

Normal cleanup removes the reply directory/socket and removes the lock after
the listener closes. SIGKILL can leave the lock and reply directory/socket.
For stale-file recovery, inspect the lock PID, process identity/command and
socket ownership; first establish that both the old bridge and its listener
are gone. Remove only that identified lock and reply directory before manually
re-arming. Do not delete by wildcard or remove a lock held by a live process;
PID reuse makes a bare PID lookup insufficient evidence.

## Delivery, cleanup and limitations

Board text is data: the adapter uses `spawn` with an argument array and
`shell: false`, then JSON over a UNIX socket. Qwen's peer path bypasses
slash-command, shell and path preprocessing. The model message reminds the
agent to run `lorvik inbox --session <label>` using its inherited config.
The listener leaves the stored inbox cursor untouched; normal inbox reads
advance it. Load the Lorvik skill separately.

There is at most one delivery in flight plus one coalescing pending batch,
capped at **256 KiB / 4096 lines**. Overflow evicts the oldest lines, retains
the newest fitting lines, and adds one counted dropped-lines notice asking
for `lorvik inbox`. Individual stdout lines over **64 KiB** are discarded and
counted, including unterminated lines. Stderr retains at most a 4 KiB line.
Pending data is split into complete-line frames capped at **512 KiB after
JSON serialization**, including metadata and escaping. This also fits Qwen's
1 MiB UTF-16 line limit. No promise-chain backlog accumulates.

Attempts are paced at least **2100 ms** apart, below Qwen's sustained sender
allowance (0.5 messages/second; burst 30) and global allowance (1/second;
burst 32). Other senders can still consume the global allowance. A definitive
`dropped` / `rate-limited` receipt merges the rejected data back into the same
bounded pending batch and schedules another paced attempt; newer data can
replace its oldest lines under the cap. Grouped drop receipts are supported.
These data-dependent timers make no model calls while there is no event data.

The private reply socket authenticates receipts, caps each line at **64 KiB**
and accepts at most **8 connections**. Only `delivered` is successful admission.
Other statuses (except definitive rate-limit rejection), transport errors or a
**15-second** missing receipt stop the listener and report once on stderr.
The receipt wait exceeds the normal five-second trailing drop-receipt delay;
receipt-budget exhaustion can delay receipts longer, so no timeout is retried.
Receipts are best-effort: this timeout is an uncertain outcome, not proof that
Qwen rejected the event. Explicit inbound `hold`/`refuse` policy still wins
over own-child credentials. Resolve held events in Qwen before re-arming to
avoid duplicates.

On listener self-exit the final complete or partial line is drained, with the
same bounds and pacing, before one
exit diagnostic, then the bridge stays stopped. A local one-second process
check stops the listener on record/socket/session replacement or owner exit.
Signals clear pending batches, close sockets, and terminate the child. These
housekeeping timers do not submit prompts. Listener stdin is an unwritten pipe
kept open for the listener's lifetime. It is closed only when stopping, and
`--exit-on-eof` also ends the listener when SIGKILL closes the adapter's file
descriptors. No parent-death guardian is needed. Hard-killing the bridge can
bypass file cleanup and leave a stale lock/reply directory; the quiet listener
still exits on EOF. Graceful disarm is the supported path.
The implementation supports Linux only.
Qwen's actual managed-shell stop escalates a process-group termination after
200 ms, faster than this bridge's own one-second child grace period. That
integration path is untested and can also leave a stale lock if the bridge is
killed before cleanup completes; inspect the PID before any manual recovery.

Qwen's managed background shells have no silence timeout. A natural bridge
exit in a still-live chat can produce Qwen's normal terminal background-task
notification that may result in model work. Shutdown cancellation suppresses that
notification. There are no periodic model turns while the board is quiet.

For boundary verification only, set the adapter's own
`LORVIK_WAKE_TRACE=1`: each `delivered` receipt writes a line containing the
batch size and Lorvik label to stderr. It never logs the token or board text.
`LORVIK_BIN` optionally selects an executable path, used by the offline fixture.
Neither is a Qwen configuration key.

If the required live messaging capability is absent, **no automatic wake** is
available through this bridge. Say so in chat and on the waiting card, then
ask the human to return here after the board changes. A foreground
`lorvik inbox --follow --once --session "$LORVIK_SESSION"` can wait for one
batch if its shell timeout permits. It is waiting, not a polling loop.

## Alternatives checked

`monitor` exists, but is unsuitable under Lorvik's no-timer-turn rule.
Its `idle_timeout_ms` argument is optional; the effective finite timeout is
mandatory: **300000 ms default, 600000 ms maximum**, positive integers only.
Silence expiry stops the command and emits a terminal notification. The UI
drains notifications into model work while idle. **Inference from that path:**
re-arming at the maximum creates roughly six expiry turns/hour while quiet
(twelve/hour at default), unless notifications coalesce or other admission
conditions prevent a turn. One expiry is not a fixed dollar amount or exactly
one provider request: token cost depends on model, context and ensuing work.

Hooks are `UserPromptSubmit`, `Stop`, `SessionStart` and `SessionEnd`;
`BeforeAgent` and `AfterAgent` are absent. `UserPromptSubmit` can add context
to an already submitted turn; `Stop` operates on an existing turn's end.
Extensions contribute MCP/LSP, context, commands, skills, agents, workflows,
hooks and channels. Channels host separate ACP services or daemon workers,
rather than a Pi-style callback into an independently running Ink TUI.

ACP has an owning stdio client and addressed session prompts; attaching that
transport to an unrelated existing TUI is **UNVERIFIED**. It is not needed
because the child inbox already addresses this live chat. A common next-turn
hook architecture could serve both forks after adapting hook names, but would
forfeit Qwen's verified live wake capability. The selected adapters differ.

## Tagged-source evidence

| Claim | `qwen-code` v0.26.7 source |
| --- | --- |
| Shell session ID export and launch environment | `packages/core/src/services/shellContextEnv.ts:115-125`; `packages/core/src/services/shellExecutionService.ts:1010-1020` |
| Own-child socket/token capability and export | `packages/cli/src/peerMessaging/env.ts:15-38`; `packages/cli/src/peerMessaging/peer-messaging.ts:417-454` |
| Registry home, identity and wire contract | `docs/users/features/cross-session-protocol.md:24-68`, `:104-113`, `:147-218` |
| User-frame validation and receipt meanings | `packages/core/src/ipc/peer-frames.ts:242-282`; `docs/users/features/cross-session-protocol.md:225-274` |
| Admission limits apply to own-child credentials | `packages/core/src/ipc/peer-admission.ts:101-108`, `:255-277`, `:302-314`; `packages/core/src/ipc/inbound-gate.ts:699-709` |
| Grouped drops and trailing receipt delay | `packages/core/src/ipc/peer-frames.ts:126-144`, `:448-471`; `packages/core/src/ipc/peer-drop-reports.ts:55-64`, `:192-237`, `:340-361` |
| UTF-16 frame limit and overflow enforcement | `packages/core/src/ipc/peer-frames.ts:38-52`; `docs/users/features/cross-session-protocol.md:147-150`; `packages/core/src/ipc/uds-inbox.ts:427-432`, `:883-887` |
| Idle-only queue drain; pin recheck; model submission | `packages/cli/src/ui/AppContainer.tsx:369-388`, `:493-545`, `:2883-2895` |
| Explicit policy overrides own-process admission | `packages/core/src/ipc/inbound-gate.ts:588-601` |
| Shutdown cleanup, expiring unread input; changed-session guard | `packages/cli/src/peerMessaging/peer-messaging.ts:692-724`, `:1032-1059`; `packages/cli/src/ui/startInteractiveUI.tsx:527-541` |
| Messaging unavailable in bare/safe mode | `packages/cli/src/peerMessaging/enabled.ts:69-93` |
| Managed background shell and literal `task_stop` name | `docs/developers/tools/shell.md:18-26`, `:79-85`; `packages/core/src/tools/task-stop.ts:233-254`; `packages/core/src/tools/tool-names.ts:50` |
| No background silence timer; completion/shutdown notifications | `packages/core/src/tools/shell.ts:4235-4465`; `packages/core/src/services/backgroundShellRegistry.ts:529-603`, `:647-675` |
| Managed process-group stop escalation, 200 ms | `packages/core/src/services/shellExecutionService.ts:41`, `:1828-1834` |
| Monitor bounds, optional argument, validation | `packages/core/src/tools/monitor.ts:67-70`, `:816-827`, `:864-873` |
| Expiry notification and queue-to-model drain | `packages/core/src/services/monitorRegistry.ts:489-504`, `:551-600`; `packages/cli/src/ui/hooks/use-llm-stream.ts:7219-7255`, `:7401-7420` |
| Hook names and output context | `packages/core/src/hooks/types.ts:24-69`, `:926-938`, `:1028-1042` |
| Extension manifest and channel hosting | `packages/core/src/extension/extensionManager.ts:201-223`; `docs/developers/channel-plugins.md:3-26` |
| ACP owning stdio connection and session prompt | `packages/cli/src/acp-integration/acpAgent.ts:3190-3205`, `:3267-3285`, `:7223`, `:7353` |

Lorvik references: `--follow`/`--once` and the EOF behavior in
`internal/cli/inbox.go`; `--session` in `internal/cli/root.go`;
`LORVIK_CONFIG` in `internal/cli/config.go`; explicit session precedence
in `internal/cli/session.go`.

## Verification status

Passed `bun build --no-bundle skills/lorvik/adapters/qwen/lorvik-wake.ts
--outfile "$TMPDIR/lorvik-qwen-wake-check.mjs"` and
`node --check skills/lorvik/adapters/qwen/check-offline.mjs`.
These are syntax/transpilation checks, not full TypeScript type checking.

The separate `check-offline.mjs` fixture passed with a fake registry, UNIX
socket, receipts and CLI: explicit label/config, fragmented 250 ms batch,
quiet wait, duplicate bridge, session swap, final partial output, refusal,
and retaining the lock while a stubborn child needs SIGKILL.
It also passed SIGKILL-to-stdin-EOF lifetime, 36 sustained events at 300 ms
spacing with paced/grouped rate-limit recovery, newest-line byte/count caps,
escaped frame splitting below 512 KiB across socket reads, fragmented receipts,
and oversized unterminated-line handling. `delivery-buffer.mjs` is authored
adapter code and is covered by that same fixture.
Pending-receipt shutdown and target-socket loss also passed without unhandled
promise rejections. A synthetic post-listen receipt-server error was reviewed
in code but was not injected by the fixture.

Live Qwen receipt handling, actual Go CLI EOF behavior, busy-turn deferral, model login, real Lorvik
connectivity and shutdown races are **UNTESTED**. No harness executable or
cloned harness code, model, Lorvik server or database was run. The unrun human
verification procedure is [check.md](check.md).
