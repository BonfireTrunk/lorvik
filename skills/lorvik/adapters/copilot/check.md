# Copilot adapter check

These installed-harness checks require Copilot CLI 1.0.105, a human login, a
reachable Lorvik service, and a Lorvik CLI containing `inbox --follow
--exit-on-eof` (`internal/cli/inbox.go`). **Nothing in this file was run against an installed
harness.** Do not install or run a harness merely to validate this repository.

Every batch already admitted by Copilot can still run after a viewer or chat
closes. If a viewer close does not stop the extension, new board events keep
starting turns until it is disarmed; this is not a one-turn ceiling.

## Offline boundary

These tests use a fake SDK and fake `lorvik`, never a harness, model, Lorvik
server, or database:

```sh
node --check skills/lorvik/adapters/copilot/extension.mjs
node --check skills/lorvik/adapters/copilot/check.test.mjs
bun build --no-bundle --target=node \
  skills/lorvik/adapters/copilot/extension.mjs \
  --outdir "$TMPDIR/lorvik-copilot-syntax-check"
node --test skills/lorvik/adapters/copilot/check.test.mjs
```

They check direct listener arguments including `--exit-on-eof`, open-stdin EOF
on extension SIGKILL, literal data delivery, `source: "system"`, `enqueue`,
250 ms batching, one blocked send plus one capped pending batch, UTF-8 prompt
size, fragmented overlong input, stop discard, and single reporting of a
listener self-exit. They cannot prove Copilot discovers the extension or that
the CLI schedules a model turn.

## Installed delivery boundary

After a human installs and logs into Copilot, copy `extension.mjs` as in the
[README](README.md), arm a fresh chat with its exact command, and do not use
`--resume`, `--continue`, another SDK client, or a timer as a wake mechanism.

In a shell tool in that chat, run:

```sh
printf '%s\n' "$LORVIK_SESSION"
printf '%s\n' "$COPILOT_AGENT_SESSION_ID"
lorvik session
```

Confirm the explicit Lorvik label and inspect the process tree: there must be
one extension-owned direct `lorvik inbox --follow --exit-on-eof --session
<label>` child, with no guardian. From another authorized participant, create
a board event for that chat. Confirm the listener line reaches the extension's
`session.send` boundary (using adapter diagnostics/instrumentation appropriate
to the installed release). Then, with the model login active, confirm an idle
chat starts one turn and a busy chat queues after its active work.

Finally test normal exit, replacement, and abrupt extension termination. The
listener must exit on its stdin EOF; no new local dispatch occurs after a
successful disarm. Test the documented closure race separately: an already
admitted batch may still run, and further events may run until actual disarm.

## Offline record

Both `node --check` commands and the Bun transpile command passed. The offline
test command passed **11 tests, 0 failed, 0 skipped**, including a notice-only
overflow batch. These checks do not establish real Copilot
behavior. No harness, model, server, database or cloned code was executed.
