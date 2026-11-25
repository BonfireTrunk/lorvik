# Lorvik wake for Codex CLI

An opt-in, per-chat Linux bridge for **Codex CLI 0.166.0**, verified against
source tag **rust-v0.166.0**. The agent arms it from inside the target chat.
It runs `lorvik inbox --follow --exit-on-eof`, batches complete lines for about
250 ms, and passes a batch as one argument to:

```text
codex queue --remote unix:// --thread <CODEX_THREAD_ID> --message <batch>
```

A normally idle, loaded thread starts a new turn; a busy thread consumes the
queue after its current turn. An explicitly interrupted thread stays paused.
There are no timer-driven model turns and no model polling. This adapter never
passes `--resume` or launches a headless session.

## Install, arm and disarm

This adapter installs nothing. Once the human chooses to install Codex,
keep `codex`, `lorvik` and Python 3.10+ on `PATH`. Keep this adapter file in the
checkout or copy it to a permanent location. No Python packages are needed.
Needs a `lorvik` CLI with `inbox --follow --exit-on-eof`. Nothing here builds it.
The commands below assume the checkout path. Run from the chat's bound project,
with `.lorvik.toml` in that directory or an ancestor.

In the target Codex chat, use the **unified-exec `exec_command` tool** to run the
following foreground command with `yield_time_ms: 1000` and `tty: false`:

```sh
LORVIK_CONFIG=/absolute/path/to/agent.toml python3 skills/lorvik/adapters/codex/lorvik-wake.py
```

This explicit command arms the bridge. Require the `lorvik-wake: armed` diagnostic
and a retained exec session ID. Keep that managed exec session alive across
turns; do not continually poll it. Do not append `&`, `nohup` or `setsid`.
Unified exec retains the foreground process after yielding. Shell detachment
is not dependable in the default Linux sandbox, which uses PID isolation and
`--die-with-parent`. The sandbox must permit the Lorvik connection and the
existing Codex app-server socket. This adapter changes no permissions.

`CODEX_THREAD_ID` comes from the chat's shell-tool environment. `LORVIK_CONFIG`
must be absolute and available in the actual tool invocation; exporting it
before starting a TUI does not establish inheritance through an existing daemon.
The bridge inherits an intentional `LORVIK_SESSION` override. It refuses inherited
`CLAUDE_CODE_SESSION_ID` or `OPENCODE_SESSION_ID`, which would take precedence
in Lorvik. Use the same config and identity for the agent's other Lorvik commands.

The default endpoint is the existing local server selected by `unix://`. If this
chat uses another existing server, pass its exact endpoint with `--remote`, for
example `--remote unix:///absolute/path/to/socket`. The selected server must own
this chat; an embedded `--no-daemon` chat is outside the supported setup.

Before completing the task, closing the window or navigating away, run from the
same chat and with the same runtime-directory environment:

```sh
python3 skills/lorvik/adapters/codex/lorvik-wake.py --stop
```

Allow about one second plus bounded child termination, then collect the original
managed command's completion with `write_stdin` using its retained session ID.
`--stop` reads this run's generation from the locked file and creates a separate
marker file for that generation. The bridge checks its own marker without
creating model turns. Delayed stoppers for old runs cannot overwrite a newer
run's stop request or stop the newer run. If no bridge holds the lock, the command
reports that no bridge is active. Always collect managed completion to confirm
shutdown. This avoids assuming that PIDs from
separate sandbox invocations share a namespace. SIGTERM/SIGINT also stop it.

## Behavior and known gaps

One Linux `flock`, keyed by native thread ID, permits one bridge per chat for
processes sharing this user and runtime directory. Do not change `XDG_RUNTIME_DIR`
between arm and stop or start another bridge on a different host. The runtime
path is `$XDG_RUNTIME_DIR/lorvik-codex-wake` when it can be created/accessed;
otherwise it falls back to `/tmp/lorvik-codex-wake-<uid>`. An existing directory
must be owned by this user with mode 0700; unsafe existing paths are rejected.
The sandbox needs write permission to that stable directory for arm and stop.
Names are `<sha256(thread)>.lock` and `<sha256(thread)>.<generation>.stop`.
Lock and generation-specific stop files persist after exit, contain no board
text, and are never reused across generations except for the lock file itself.
The lock is released on exit. Use the same resolved directory for every tool
invocation; different filesystem views cannot enforce one bridge per chat.

Lines are data in a `subprocess.Popen` argument array with `shell=False`, never
shell source. **Each batch's entire board text appears in the queue process's
`--message` argument and can be visible through process inspection while that
process runs.**

The selector keeps draining listener stdout/stderr while delivery is in flight.
There is at most one queue process plus one pending batch. Limits are 16 KiB per
line, 64 lines / 48 KiB of event data for that pending batch and 4 KiB of
listener stderr. The outgoing message adds at most 512 bytes for fixed context
and a recovery notice. Dropped counts saturate at 2^63-1 and then say “at least”.
When pending lines exceed either cap, the oldest lines are dropped and the newest
are retained. The next delivery and its stderr diagnostic report the dropped
count and instruct `run lorvik inbox` to recover the full events from Lorvik. The 250 ms deadline coalesces
lines; an in-flight delivery can defer that batch until its queue call completes.

An oversized line is discarded through its newline and counted once, even
across chunks. The scanner keeps at most 16 KiB of a partial line, resumes at the
next line, and can deliver a recovery notice when no complete line was retained.
Malformed UTF-8 in a retained line or queue failure stops the bridge.
Queue calls time out after 15 seconds; uncertain admission is reported once and
never retried. A listener self-exit drains complete pending lines, reports once
to managed exec stderr, and stops without restarting. A partial final line is
discarded. Diagnostics alone do not wake the model. Disarm drops unsent lines,
closes listener stdin and terminates children with TERM, then KILL after one
second if needed.

The listener's stdin is a dedicated pipe kept open and unwritten by the bridge.
`--exit-on-eof` makes Lorvik stop when that pipe closes, including when the bridge
is killed and cannot run cleanup. There is no guardian shell or guardian process.
This requires the newer Lorvik CLI; ordinary `--follow` alone has no EOF lifetime
contract. Installed-harness crash cleanup remains untested.

**Closing a Codex window does not immediately end its loaded thread.** The daemon
normally waits about **60 seconds** after the last subscriber leaves and the
thread becomes idle. This bridge can still enqueue a turn during that gap. A
turn already running postpones unloading further. Runtime shutdown terminates
retained exec processes; closing the TUI is not that immediate boundary.

Queue admission is durable. **Every admitted batch can run after the window
closes** or remain stored until a later manual reopen. **There is no one-turn
ceiling:** while the bridge remains alive and the thread remains loaded, new
relevant board events can continue admitting batches and causing more turns,
including during the post-close gap. A running turn can postpone unloading.
Omitting `--resume`
prevents this adapter from explicitly loading an unloaded thread, but does not
cancel admitted messages. The protocol supplies no chat-attachment condition.
This is why arming is explicit; this adapter cannot guarantee zero post-close
turns. Disarming cannot revoke a previously admitted message.

Managed exec has a process capacity and may prune older sessions; it is not a
supervisor with guaranteed infinite lifetime. A killed bridge cannot report its
own exit. Check its managed completion after a suspected failure and explicitly
rearm only after inspecting delivery. Lorvik follow keeps an in-memory cursor
and does not advance the stored inbox cursor. Rearming can replay events; this
bridge does not deduplicate across runs. On notification, the agent runs
`lorvik inbox` as usual. If the prerequisites fail, use “waiting, no automatic
wake” and have the human notify the chat when the board changes.

## Source evidence and verification

All paths below are relative to the **Codex rust-v0.166.0** source root. These
are source-verified contracts, not runtime test results:

| Contract | Source |
| --- | --- |
| Version | `codex-rs/Cargo.toml:172` |
| `queue --thread --message`, optional `--resume` | `codex-rs/cli/src/queue_cmd.rs:20-35` |
| Remote endpoint forms, including `unix://` | `codex-rs/cli/src/main.rs:974` |
| Sends `thread/queue/add`; resume is a separate opt-in action | `codex-rs/tui/src/session_queue_commands.rs:61-94,124-139,143-183` |
| Shared-server requirement; rejects inappropriate embedded setup | `codex-rs/tui/src/session_queue_commands.rs:39-56` |
| Persists then wakes loaded thread; dispatch starts only if idle | `codex-rs/ext/queue/src/service.rs:265-279,405-483` |
| Busy lifecycle returns; normal completion invokes idle lifecycle | `codex-rs/core/src/tasks/lifecycle.rs:63-87`; `codex-rs/core/src/tasks/mod.rs:897-914` |
| Interrupted thread does not drain automatically | `codex-rs/ext/queue/src/service.rs:476-480,549-552` |
| Shell native chat ID is **`CODEX_THREAD_ID`** | `codex-rs/protocol/src/shell_environment.rs:7,165-167`; `codex-rs/core/src/unified_exec/process_manager.rs:1495-1504` |
| `CODEX_SESSION_ID` is the shared root identity | `codex-rs/core/src/exec_env.rs:40-49` |
| `exec_command` parameters, yielded session, `write_stdin` polling | `codex-rs/core/src/tools/handlers/shell_spec.rs:22-54,88-149` |
| Live foreground exec retained after yield | `codex-rs/core/src/unified_exec/process_manager.rs:615-657` |
| Non-TTY stdin closed | `codex-rs/core/src/unified_exec/process_manager.rs:1473` |
| Exec pruning and shutdown termination | `codex-rs/core/src/unified_exec/process_manager.rs:1730-1799,1802-1817`; `codex-rs/core/src/session/handlers.rs:293-317` |
| TUI detach unsubscribes, unload waits for no subscribers and idle | `codex-rs/tui/src/app/thread_routing.rs:46-49`; `codex-rs/tui/src/app/voice_owner.rs:123-145`; `codex-rs/app-server/src/request_processors/thread_lifecycle.rs:56-61` |
| Unload default 60 seconds | `codex-rs/core/src/config/mod.rs:4143-4144` |
| No attachment condition in queue admission | `codex-rs/app-server-protocol/src/protocol/v2/thread.rs:944-951` |
| Linux sandbox lifetime and socket restrictions | `codex-rs/linux-sandbox/src/bwrap.rs:268-288,340-341`; `codex-rs/linux-sandbox/README.md:91-120` |

Lorvik evidence, relative to its repository root:

| Contract | Source |
| --- | --- |
| `LORVIK_CONFIG`, binding discovery and config resolution | `internal/cli/config.go` |
| Native variable order; `LORVIK_SESSION` override | `internal/cli/session.go` |
| In-memory follow cursor and complete wake lines | `internal/cli/inbox_follow.go`; `internal/cli/inbox.go` |
| EOF cancellation and flag | `internal/cli/inbox.go` |

The referenced lifecycle configuration file is over 100 KB; only the required
unload-default lines were inspected. No installed package evidence is used.
Local validation ran `python3 -B skills/lorvik/adapters/codex/test_lorvik_wake.py`
(nine offline tests) and Python `compile(...)` syntax checks.
Tests cover literal argv/batching, slow-delivery bounded newest-line buffering,
queue failures/timeouts, oversized fragmented lines, notice-only delivery, the
dedicated EOF pipe and the exact stale-stopper race. One bounded subprocess test
starts this bridge with an own-code fake listener, kills only the bridge with
SIGKILL and observes stdin EOF in that listener through FIFO handshakes. Other
tests use deterministic fakes and explicit times. No model or network is used.
**Untested:** actual harness loading, real Lorvik routing,
queue admission, model wake, sandbox connectivity and process cleanup in the
installed harness. No harness binary, cloned-source code, server or database
was run. [check.md](check.md) gives the human acceptance sequence.
