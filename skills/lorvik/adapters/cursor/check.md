# Cursor Agent CLI verification

This recipe requires Cursor Agent CLI and a Lorvik CLI build with `inbox --follow
--exit-on-eof` to be installed. **Nothing in the installed-harness portion of
this recipe was run while developing this adapter.** It must be performed by a human
who has chosen to install and authenticate the harness. Do not use `agent
--resume`, `session/load`, or a timer loop in any step.

## Offline adapter boundary

These commands run only the adapter with its fake ACP peer and fake `lorvik` in
temporary directories. They do not run Cursor Agent CLI, contact a Lorvik
server, or start a model turn:

```sh
bun build --no-bundle --target=node \
  skills/lorvik/adapters/cursor/lorvik-wake.ts \
  --outdir "$TMPDIR/lorvik-cursor-syntax-check"
bun test skills/lorvik/adapters/cursor/lorvik-wake.test.ts
```

The test proves the boundary up to `session/prompt`: a fake listener's literal
lines are batched into one queued ACP prompt after an existing prompt finishes,
the listener receives stdin EOF after normal close and bridge SIGKILL, and
cleanup reaches the fake ACP peer. It cannot prove Cursor accepts, schedules,
or cancels that prompt.

## Authenticate before the installed-harness checks

After the offline boundary check, authenticate through one documented path:

```sh
agent login
```

Alternatively set `CURSOR_API_KEY` or `CURSOR_AUTH_TOKEN` before launch. The
bridge uses cached credentials and does not call ACP `authenticate` itself.
Even creating the real ACP session can require authentication; the offline
fixture above is the only login-free delivery-boundary proof.

## Installed-harness setup boundary

From a repository with `.lorvik.toml`, start an owned bridge with the desired
agent configuration:

```sh
LORVIK_CONFIG=/absolute/path/to/agent.toml \
LORVIK_SESSION=cursor-live-check \
bun /absolute/path/to/skills/lorvik/adapters/cursor/lorvik-wake.ts
```

Before entering any task prompt, confirm the bridge prints one Lorvik listener
with `--follow --exit-on-eof --session cursor-live-check` and one fresh Cursor ACP session. Do not send
an ACP prompt or a board event in this step: those need authenticated model
access and belong to the next section. The offline test above is the only
no-login proof of delivery up to the adapter's `session/prompt` call.

## Authentication and live model delivery

With authentication already established, submit a harmless prompt. In the
opened chat, ask the agent to
run:

```sh
lorvik --session cursor-live-check session
```

Confirm that the CLI reports that label. This verifies the explicit Lorvik
identity used by agent tool calls; it does not assert an undocumented Cursor
shell-session environment variable.

From another authorised participant, create one board event that concerns this
chat. Confirm the idle bridge prints a new turn containing the event lines and
that the turn runs `lorvik --session cursor-live-check inbox`. While a human
prompt is actively running, create a second concerned event. Confirm it produces
no overlapping `session/prompt` and appears after the active turn returns.
Confirm a quiet board starts no turns.

Check `/quit`, Ctrl-C, normal ACP exit, listener self-exit, and killing the
bridge while quiet. The listener must stop by stdin EOF; a self-exit must be
reported once and never restarted. Race `/quit` with event delivery and record
which already accepted batches Cursor runs. Every admitted batch can still run
after the chat or viewer closes; while the adapter remains armed, new events
can keep starting turns. That is the documented opt-in gap, not a pass
condition that may be concealed. Record the installed
`agent --version` alongside the result; no numeric Cursor version was assumed by
this adapter.

## Offline record

The Bun transpile command passed, and the full offline fixture passed
**5 tests, 0 failed, 32 expectations**. Nothing in the installed-harness or authentication sections was run.
Bun transpilation is not full TypeScript type checking.
