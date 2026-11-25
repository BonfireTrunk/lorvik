# Lorvik wake for Crush 0.99.0

This adapter is an explicitly armed Linux sidecar for one already-open Crush
client/server chat. It runs `lorvik --session LABEL inbox --follow --exit-on-eof`, batches
complete output lines for about 250 ms, and submits each batch through Crush's
HTTP agent endpoint. It does not launch Crush, create or resume a session, open
an SSE viewer, or create timer-driven model turns.

## Delivery and lifetime

The sidecar POSTs an `AgentMessage` containing the native session ID, a fresh
nonempty `run_id`, and the board lines as JSON string data. The nonempty run ID
keeps a message received during another turn in Crush's queue for its own later
turn; it is not folded into the active turn. The injected message tells the
agent to read its inbox with the same explicit Lorvik label. Board text is never
evaluated as a shell command.

Crush's lifecycle hooks cannot do this job: `TurnStart`, `TurnEnd`,
`SessionStart`, and `SessionEnd` are observation-only. A hook can observe a
turn ending, but cannot receive a board event after the chat has become idle.

**Known close-race gap.** Crush detaches an accepted run from the requesting
HTTP connection and keeps it owned by the workspace. Therefore a successful
sidecar shutdown cannot retract a wake that the server has already accepted.
A presence read is only a snapshot. The sidecar is deliberately opt-in: arm it
only while this exact chat is waiting, stop it before switching or closing the
chat, and accept that every already admitted batch can still run later. It never
cancels the session or clears Crush's queue because either action can discard
human work.

One sidecar is allowed per local user, native workspace, and native session. A
private non-symlink directory anchors an `O_NOFOLLOW` lock at the fixed path
`/tmp/lorvik-crush-<uid>/<sha256>.lock`; `<sha256>` derives from workspace and
native session, deliberately not endpoint spelling. This conservatively rejects
two independently copied servers that happen to reuse native IDs, but prevents
Unix socket, TCP, `localhost`, IPv4, and IPv6 aliases from creating two
listeners for one chat. Socket aliases are resolved with `realpath`; HTTP URLs
are normalized before validation.

The lock stores sidecar and listener PID plus Linux start identity, with no
configuration or token data. A `SIGKILL` can leave it behind: before removing
only that exact lock by hand, prove both recorded processes are gone or their
current start identities differ. Listener EOF normally handles a dead sidecar
because its stdin pipe was held open with `--exit-on-eof`. The adapter never
unlinks a replacement lock file. The sidecar also
checks the nominated visible-client PID (Linux start time) and the session's
aggregate `attached_clients` count at startup and once per second. Those checks stop its
listener when the client exits or no viewer remains; they cannot identify which
of several viewers is the nominated client. Use one viewer and stop before
switching: aggregate presence can miss a session switch, so later board events
can continue to be admitted until manual stop. A `SIGKILL`, power loss, or a network failure after a request was
accepted has no recoverable acknowledgement; delivery errors stop the bridge and
do not retry.

When the listener exits on its own, the adapter drops a trailing unterminated
record, flushes complete buffered lines once if the presence guards still pass,
reports its final error once, and does not restart. Intentional stop aborts an
in-flight HTTP request, drops a local follow-up batch, and retains the lock
until listener and delivery have a local terminal. A request accepted before
abort is irrevocable, so **every admitted batch** can still execute after the
visible chat closes.

There is one in-flight request and one coalesced pending batch, capped at 1 MiB
including separator and prompt-notice reserve. It retains newest complete board
lines, bounds partial-line framing, and records dropped oversized, evicted, or
unterminated records once in the next prompt, which directs the agent to run
`lorvik inbox` for the authoritative inbox.

## Install and arm

This is source-only material. Keep `lorvik-wake.ts` at a stable absolute path
and run it with Bun after the human has installed Crush and chosen to expose an
existing shared server. No packages are installed by this adapter.

Crush enables its client/server architecture only when its launching
environment parses `CRUSH_CLIENT_SERVER` as true. The separately managed server
command is `crush server --host ...`; the tagged source accepts a Unix socket,
TCP, or HTTP form. An ordinary standalone Crush chat has no verified external
prompt-injection API, so it cannot use this adapter.

Before arming, export `LORVIK_CONFIG`, ensure `lorvik` is on `PATH`, and run in
a directory with a `.lorvik.toml` binding. Needs a `lorvik` CLI with
`inbox --follow --exit-on-eof`.
Choose a fresh Lorvik label of at
most 40 lowercase letters, digits, and hyphens. Use it on every Lorvik command
inside the target chat, including its first claim:

```sh
lorvik --session my-crush-chat next
```

Select the actual server endpoint, workspace ID, native session ID currently
shown by the Crush client, and PID of that visible client, then explicitly arm:

```sh
LORVIK_CONFIG=/absolute/path/agent.toml \
  bun /absolute/path/to/lorvik-wake.ts --arm \
  --socket /absolute/path/to/crush.sock \
  --workspace EXISTING_WORKSPACE_ID \
  --native-session VISIBLE_NATIVE_SESSION_ID \
  --session my-crush-chat --owner-pid VISIBLE_CRUSH_CLIENT_PID
```

For an existing loopback HTTP server, replace `--socket` with an origin such as
`--url http://127.0.0.1:8080`. The adapter accepts only absolute Unix sockets
or plain loopback HTTP origins, and it rejects credentials, paths, remote hosts,
and ambiguous endpoints. It passes `lorvik` as an executable plus argv with
`shell: false`.

Crush provides **no native session variable to ordinary agent shell tools** in
this release. `CRUSH_SESSION_ID` belongs to the hook runner only. The regular
bash tool creates a background shell with only working directory and blockers;
the shell inherits its process environment and appends the three Crush marker
variables. This adapter therefore requires the explicit Lorvik label above.

## Source verification

All of the following were read from the supplied `crush` clone at tag
`v0.99.0` (`e38655aac84ab3704a1a96928017e1d9ef1d0043`). Paths and line numbers
are relative to that clone.

| Verified contract | Tagged source |
| --- | --- |
| `CRUSH_CLIENT_SERVER` is the opt-in switch | `internal/cmd/root.go:252-257` |
| `crush server --host` and supported host examples | `internal/cmd/server.go:21-46` |
| Unix default socket derivation | `internal/server/server.go:26-91` |
| Release build injects the tagged version into the binary | `.goreleaser.yml:80-83` |
| List and single-session routes used to select the visible native session | `internal/server/endpoints.go:170-194` |
| Session GET route and response type | `internal/server/endpoints.go:187-194` |
| Agent POST route accepts `AgentMessage` and returns 202 | `internal/server/endpoints.go:355-365`; `internal/server/proto.go:537-561` |
| `session_id`, optional `run_id`, and prompt fields | `internal/proto/proto.go:149-170` |
| Run-ID-bearing busy input remains queued for an independent turn | `internal/agent/agent.go:440-482` |
| Accepted run lifetime is independent of the request connection | `internal/backend/agent.go:49-67`; `internal/server/proto.go:548-560` |
| `attached_clients` is a point-in-time aggregate viewer count | `internal/proto/session.go:11-30`; `internal/backend/backend.go:1087-1101` |
| Lifecycle hooks are observe-only | `docs/hooks/README.md:174-230` |
| `CRUSH_SESSION_ID` is only added to hook-runner environment | `internal/hooks/input.go:72-91` |
| Ordinary bash tool has no session environment injection | `internal/agent/tools/bash.go:303-308`; `internal/shell/background.go:89-104`; `internal/shell/shell.go:81-104` |
| Lorvik listener exits when the adapter-held stdin reaches EOF | Lorvik `internal/cli/inbox.go` |

## Validation status and fallback

Offline validation ran `bun build --no-bundle` for `lorvik-wake.ts` and
`bun test lorvik-wake.test.ts`: 17 tests and 49 assertions passed. The tests use
a fake `lorvik` child and stubbed Crush request function. They exercise this
adapter's parser, listener argv and environment, batching, literal data
handling, endpoint-alias exclusion, unsafe/replaced lock protection, self-exit,
EOF after owner-process SIGKILL, bounded coalescing/overflow, stop-abort behavior,
synchronous and post-spawn cleanup, and the exact POST request body sent to a
stubbed Crush boundary. They did not execute
Crush, start a database or server, or call a model. The future installed-harness
acceptance recipes in [check.md](check.md) were not run.

**Untested:** loading this sidecar beside an installed Crush client, real HTTP
admission, provider delivery for idle and busy sessions, viewer-close timing,
Windows and macOS behavior, and abrupt process death. The source confirms the
close-race gap, so no live test can upgrade it to a guaranteed lifetime guard.

If client/server mode or the opt-in gap is unsuitable, use the waiting fallback:
write that the card is waiting with no automatic wake and ask the human to
return to this chat when it is answered. A long-running
`lorvik --session LABEL inbox --follow --once` can wait for one board event but
does not by itself cause a later model turn. Never use a headless `crush run`
or session resume as the wake mechanism.
