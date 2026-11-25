# Lorvik wake for Goose 1.57.0

This is an **opt-in ACP stdio proxy**, for an editor/client that starts a fresh
Goose process for each chat. It does not add wake to `goose session` or the
existing Goose terminal UI. The ordinary hooks cannot wake an already idle
chat. If your client shares one ACP process across chats or does not accept
unsolicited streamed turn updates, use the fallback below.

## Install and arm

Keep `lorvik-wake.ts` at an absolute path, with Bun, a `lorvik` CLI with
`inbox --follow --exit-on-eof`, and Goose 1.57.0 on the ACP
client's PATH. Configure that client's ACP agent
executable as `bun`, with arguments:

```json
["/absolute/path/to/skills/lorvik/adapters/goose/lorvik-wake.ts", "--arm"]
```

Set these environment entries **on the ACP subprocess**, before it starts:

```text
LORVIK_CONFIG=/absolute/path/to/this-agents-lorvik.toml
LORVIK_SESSION=goose-chat-unique-001
```

Use a fresh, unique label for every chat (up to 40 lowercase letters, digits,
and hyphens). The label is inherited by Goose's shell tool and passed explicitly
to the listener. Check `lorvik session` from the agent's shell before claiming
cards: it must show the same label. Do not override it in individual commands.
Goose exposes its native shell-session ID as `AGENT_SESSION_ID`. Lorvik
recognizes it as a harness fallback; an explicit `--session` or
`LORVIK_SESSION` takes precedence. This adapter still uses the
explicit label because the listener is a sibling of Goose, while `LORVIK_SESSION`
is inherited by both processes.
The session/new `cwd` must be absolute, with `.lorvik.toml` in it or an ancestor.
Load the Lorvik skill separately. Missing config/label rejects startup; missing
repo binding disables the listener. Omitting `--arm` runs the proxy without wake.
This is a generic executable/argv recipe; editor-specific config keys are
**UNVERIFIED** and intentionally not supplied.

Start a **new** chat through this agent entry. A second session/new, session/load,
session/list, session/fork and session/delete are rejected. Reopening a saved chat is not a wake
mechanism. Start another proxy for another chat. Stop wake by closing the ACP
connection or chat; there is no background daemon or durable queue.

## Strategy and lifecycle

The proxy starts `goose acp --with-builtin developer`, forwards ACP initialization,
human requests, agent notifications, and permission requests/responses unchanged.
It never approves a permission itself. After a successful session/new it starts
exactly one `lorvik --session LABEL inbox --follow --exit-on-eof`. It keeps the
listener's stdin pipe open without writing to or closing it until shutdown, so
the listener observes EOF if the proxy is hard-killed. It batches complete
nonempty lines for 250 ms, then submits a text-only `session/prompt` for that
session.
The injected message repeats `lorvik --session LABEL inbox` so that turn has the
same identity even if its environment changes.
Human and board prompts share a FIFO; a busy prompt completes before the next
starts. Human prompts are capped at 64 outstanding requests and 1 MiB of encoded
queue data, and are rejected rather than dropped. A cancel returns `stopReason: "cancelled"` for queued human
prompts and is forwarded to an active prompt. There is at most one board delivery
queued or active plus one coalescing pending board buffer. That buffer, including
line separators and its single drop/recovery notice, is capped at 1 MiB; it
retains newest complete lines and drops oversized or partial final lines. Each
injected prompt's result is consumed by the proxy; streamed content and tool
notifications still reach the owning client. Diagnostics go to stderr, and stdout
remains JSON-RPC. All children use argv arrays with `shell: false`; event text is
JSON data, never shell source.

The proxy masks Goose's load/list/delete capabilities and rejects load, list,
fork, and delete requests; it supports one `session/new` and a proxy-owned
`session/close` only. This prevents the client from presenting session operations
that escape the one-chat lifetime.

Client EOF, the proxy-handled `session/close` request, SIGINT, SIGTERM, or SIGHUP
revokes queued events and stops both children. Listener shutdown first ends its
stdin (the normal `--exit-on-eof` path), then escalates to SIGTERM and SIGKILL if
needed. A listener self-exit flushes
its last complete batch, reports its final stderr line once, and stays stopped.
Delivery errors are reported without retrying an ambiguous prompt. No timer
starts a model turn on a quiet board. Lorvik's own connection retries stay in the
CLI. The listener does not advance the inbox cursor; the resulting turn should
run `lorvik inbox`.

**Close gap:** an ACP client must close the connection or send session/close when
the visible chat closes. If it merely hides the tab while retaining the process,
the proxy cannot observe that: new board events continue to be admitted and may
deliver turns until that connection closes. There is no one-turn ceiling: every
batch admitted before a close can execute. `--exit-on-eof` makes the listener leave when an adapter-only
SIGKILL closes its stdin pipe, but cannot retract a prompt already written to
Goose. For these reasons this ships only with explicit
`--arm`, not automatic global installation. Client UI compatibility with a turn
that has no client-owned pending prompt is **UNTESTED**. No attachment to another
already-running Goose process is claimed.

## Verified sources

The supplied clone is exactly `v1.57.0` (`git describe --tags --exact-match
HEAD`). These source facts support the adapter:

| Verified contract | Tagged source |
| --- | --- |
| `goose acp` is a stdio ACP agent; it accepts comma-delimited `--with-builtin` and has a separate scheduler flag. | `crates/goose-cli/src/cli.rs:833-848`; `crates/goose/src/acp/server.rs:2624-2642` |
| Goose initialization advertises load, list, delete, and close session operations; the adapter masks unsupported operations before returning initialization to its client. | `crates/goose/src/acp/server.rs:1720-1738` |
| ACP dispatch accepts initialize, new session, prompt, cancel, close, delete and fork. | `crates/goose/src/acp/server/dispatch.rs:27-146`, `404-470` |
| New-session responses carry the native session ID; prompt responses arrive only after the stream is forwarded and the active run is cleared. This makes the proxy's one-at-a-time FIFO busy-safe. | `crates/goose/src/acp/server/dispatch.rs:44-76`; `crates/goose/src/acp/server.rs:2183-2285` |
| A close request marks that ACP session closed, cancels its active run and removes its in-memory agent. | `crates/goose/src/acp/server.rs:2539-2562` |
| Developer shell commands receive `AGENT_SESSION_ID` when a session ID exists; Lorvik gives explicit `LORVIK_SESSION` precedence over a recognized Goose session variable. | `crates/goose/src/agents/platform_extensions/developer/shell.rs:616-632`; `internal/cli/session.go` |
| `UserPromptSubmit` fires only while a prompt is being processed; `Stop` can delay the current turn, with a cap. Hooks cannot observe a board event arriving after the turn has already ended. | `crates/goose/src/agents/state_machine/ops_steer.rs:80-96`; `crates/goose/src/agents/state_machine/ops_stop_hook.rs:80-118`; `documentation/docs/guides/context-engineering/hooks.md:131-158,242-259` |
| Lorvik resolves `$LORVIK_CONFIG` as its explicit user-config path. | `internal/cli/config.go` |
| Lorvik `--follow` prints one line per wake event and performs no quiet-board polling; it starts from its stored cursor, and `--exit-on-eof` consumes inherited stdin until EOF and cancels its follow context. | `internal/cli/inbox_follow.go`; `internal/cli/inbox.go` |

The proxy's JSON-RPC forwarding and single-chat restrictions are adapter
behavior, not an API supplied by Goose. It requires an ACP client that permits a
fresh proxy process per visible chat and renders a proxy-initiated prompt after
the client has no outstanding prompt. That client behavior is **UNVERIFIED**.

## Checks and fallback

See [check.md](check.md) for reproducible fake-process boundary checks and the
extra logged-in check. Bun syntax transpilation passed. The Node 24 fake-process
boundary test passed six checks for the adapter entrypoint, cancellation/capability
masking, bounded batching/coalescing, EOF cleanup, and partial-record recovery. The test also supports setting
`LORVIK_WAKE_TEST_RUNTIME` to an absolute Bun path when validating Bun's
entrypoint. Node 24 is an alternative runtime for this type-strippable script;
it is not required for normal Bun installation. Actual Goose ACP/provider
delivery, a live Lorvik board, editor rendering/closure behavior, and Windows
are **UNTESTED**. No model session, server, or database was started during
development.

For ordinary Goose CLI or an incompatible ACP client, write a card comment and
tell the human: `waiting for <answer>; no automatic wake: tell me in this chat
once it is answered`. If the shell tool permits blocking waits, run
`lorvik inbox --follow --once` with the same config and explicit label. That waits
without model polling; it is not autonomous idle wake. Never use headless
`goose run --resume` as the wake mechanism.
