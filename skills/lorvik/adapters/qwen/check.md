# Qwen adapter checks

Requires **Qwen Code 0.26.7 and Lorvik with `--exit-on-eof` installed by the human** for the live
path. **No installed-harness steps in this checklist were run.** The separate
offline fixture was run with fakes, as recorded in [README.md](README.md).
No headless resume is part of this procedure.

## Before a model login: adapter delivery boundary

From the installed adapter directory, run the isolated fixture:

```sh
node /absolute/path/to/skills/lorvik/adapters/qwen/check-offline.mjs
```

It starts only the adapter and a fake executable named `lorvik`, with a fake
registry and private UNIX socket. It asserts the exact listener arguments
`inbox --follow --exit-on-eof --session qwen-fixture` and config, then observes the actual
adapter socket call: auth frame, `toSessionId`, `priority: "next"`, user
content and authenticated receipt. It also checks quiet waiting, duplicate
arming, session replacement, final partial output, refusal, and lock lifetime.
The fake listener rejects writes to stdin, stays alive while the pipe remains
open, and exits on EOF even after the adapter receives SIGKILL. It also checks
bounded newest-line/drop-count behavior, paced sustained events, definitive
grouped rate-limit recovery, serialized frame splitting with JSON escaping,
multiple socket reads, fragmented receipts and oversized partial lines.
It leaves its private temporary fixture directory for inspection.

This verifies the authored code up to its transport boundary without a model,
a real Lorvik service, or Qwen execution. It does **not** prove that an
installed Qwen TUI loaded or drained a message. There is no verified way to
obtain a live TUI's child credentials without opening that TUI; if opening it
requires authentication, continue to the logged-in step below.

## Extra step with model login: real TUI and Lorvik

With the human's existing Lorvik service, a bound project, and a configured
Qwen login, export these **before launching Qwen** in an ordinary terminal:

```sh
export LORVIK_CONFIG=/absolute/path/to/agent.toml
export LORVIK_SESSION=qwen-lorvik-1
lorvik --session "$LORVIK_SESSION" session
qwen
```

Confirm the printed Lorvik label is `qwen-lorvik-1`. Reserve it for this
chat and arrange an event on a card this label holds or created, or one
assigned to its agent. Native Qwen session IDs alone are not mapped by this
revision of Lorvik.

In that TUI ask the agent to execute this `run_shell_command` tool call.
Replace the adapter and log paths; the log must be in a private writable
directory. The trace logs only batch counts/label, not tokens or board data.

```json
{
  "command": "LORVIK_WAKE_TRACE=1 bun /absolute/path/to/skills/lorvik/adapters/qwen/lorvik-wake.ts 2>/absolute/path/to/private/wake.log",
  "description": "Arm Lorvik wake with delivery-boundary tracing",
  "is_background": true
}
```

Save the background **task ID**. Never append `&`, use `monitor`, or print
the Qwen token/registry into chat.

To verify the actual listener's command line, use another foreground shell
tool call in the same TUI. The deterministic bridge lock contains only its
PID:

```sh
bridge_pid=$(node - <<'JS'
const { createHash } = require('node:crypto');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const session = process.env.QWEN_CODE_SESSION_ID;
if (!session) throw new Error('No Qwen session ID');
const hash = createHash('sha256').update(session).digest('hex').slice(0, 24);
const lock = join(process.env.XDG_RUNTIME_DIR || '/tmp',
  'lorvik-qwen-wake-' + process.getuid() + '-' + hash + '.lock');
process.stdout.write(readFileSync(lock, 'utf8').trim());
JS
)
ps -o pid=,ppid=,args= --ppid "$bridge_pid"
```

Expect one Lorvik child with `inbox --follow --exit-on-eof --session qwen-lorvik-1`.
The same shell's `LORVIK_CONFIG` and `LORVIK_SESSION` must be the startup
values. Do not dump its environment, which includes a Qwen credential.

Leave the chat idle: no model turn or trace line should appear while quiet.
Have another board participant create one concerning event. From a separate
terminal read only the private diagnostic log:

```sh
tail -n 20 /absolute/path/to/private/wake.log
```

Expect `lorvik qwen wake: queued N event lines for qwen-lorvik-1`.
That line is written **only after Qwen returns a delivered receipt**, proving
that the listener line reached its authenticated injection call and Qwen
acknowledged queue admission. A timeout, held/refused/dropped receipt or exit
message is not successful delivery. A definitive rate-limit rejection is
coalesced and retried only after the 2100 ms pacing interval. Other failures
stop the listener. Do not automatically retry a timeout.

Confirm Qwen starts the event turn and runs
`lorvik inbox --session qwen-lorvik-1` using the inherited config. This is
the additional model-visible step that source inspection and a receipt
cannot prove. Create two events close together and check that one batch
contains both. While a harmless existing model turn is busy, create another
event and check that it waits for a follow-up turn.
Generate sustained events and verify receipt traces remain at least about
2.1 seconds apart. Exceed the pending-data cap and verify the newest lines and
one counted dropped-lines/inbox reminder arrive, rather than an oversized frame.

Disarm using Qwen's task tool with the saved task ID:

```json
{"task_id": "<returned background task ID>"}
```

The tool is `task_stop` (tagged source:
`packages/core/src/tools/task-stop.ts:233-254`; literal name:
`packages/core/src/tools/tool-names.ts:50`). Confirm the Lorvik child and
bridge lock are gone. Re-arm deliberately, then test `/clear`, a session
switch, and normal exit: the old listener must stop with no automatic restart.
A natural listener failure can generate a Qwen background-task terminal
notification. Every admitted batch can still run after chat/viewer closure,
and new events keep starting turns while the owning session stays live and the
adapter is armed. Disarm explicitly; a successful manual shutdown check does
not establish a one-turn ceiling or retract admitted batches.

The stdin behavior is defined in `internal/cli/inbox.go`. Lorvik's other
definitions are in `internal/cli/root.go`, `internal/cli/config.go` and
`internal/cli/session.go`.
