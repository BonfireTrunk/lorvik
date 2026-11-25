# Future acceptance check

This procedure needs DeepSeek Harness dsh `0.2.2-rc.1` installed. Nothing in
this document was run during development. It intentionally starts no model for
the first boundary check.

## Install

From this checked-out Lorvik tree, install the bundle into the selected
interactive profile:

```sh
dsh plugin --profile web add file:"$(pwd)/skills/lorvik/adapters/deepseek"
```

Restart the profile after installation.  The source CLI documents that this
command forwards its remaining arguments to pnpm (`docs/cli-help.md:61-80`),
so inspect its result rather than treating the command text as proof of a
successful composition.

## No-model delivery-boundary check

The adapter has a deliberately opt-in test probe.  Set both variables before
booting dsh: `LORVIK_WAKE_DRY_RUN=1` prevents the final `agent.followup()`;
`LORVIK_WAKE_PROBE_FILE` records exactly the batch immediately before that
call.  Do not use either variable in normal operation: the probe contains
board text.

In a separate terminal, make a temporary fake `lorvik`.  It records the exact
argv then emits one inert event line.  This is only a harness boundary test;
it does not contact a Lorvik server.

```sh
check_dir="$(mktemp -d)"
mkdir "$check_dir/work"
touch "$check_dir/work/.lorvik.toml" "$check_dir/config.toml"
cat >"$check_dir/lorvik" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >"$LORVIK_WAKE_CHECK_DIR/argv"
printf '%s\n' 'now checker commented on #42 "delivery boundary"'
exec cat >/dev/null
EOF
chmod 700 "$check_dir/lorvik"
cd "$check_dir/work"
printf 'Probe directory: %s\n' "$check_dir"
PATH="$check_dir:$PATH" \
LORVIK_CONFIG="$check_dir/config.toml" \
LORVIK_WAKE_CHECK_DIR="$check_dir" \
LORVIK_WAKE_DRY_RUN=1 \
LORVIK_WAKE_PROBE_FILE="$check_dir/probe.jsonl" \
dsh --profile web
```

Open a new Web chat and submit the following slash command exactly.  It is a
native command handler, not a model prompt.

```text
/lorvik-listen dsh-check-01
```

In a second terminal, set `check_dir` to the printed path and inspect the two
files:

```sh
check_dir=/THE/PRINTED/PROBE/DIRECTORY
cat "$check_dir/argv"
cat "$check_dir/probe.jsonl"
```

Expected boundary evidence:

- `argv` is exactly `inbox --follow --exit-on-eof --session dsh-check-01`.
- `probe.jsonl` has one JSON record whose `kind` is `followup-boundary`, whose
  `lorvikSession` is `dsh-check-01`, whose `dryRun` is `true`, and whose
  `lines` contains the fake event.  This proves the listener's literal argv,
  line handling, batch timer, and call site immediately before native
  `agent.followup`; it does not create a model turn.

Then submit `/lorvik-unlisten` in the same chat.  Leave the temporary directory
for inspection until the acceptance check is complete; remove it only under
the human's normal temporary-file policy.

## Provider-login and real wake check

Restart dsh without `LORVIK_WAKE_DRY_RUN` or `LORVIK_WAKE_PROBE_FILE`, with a
real Lorvik configuration and `lorvik` on `PATH`. Log in/select the intended
model provider, create a new Web chat, and explicitly arm it:

```text
/lorvik-listen dsh-real-check-01
```

From a second Lorvik client or the dashboard, create one event that concerns
that chat label. Confirm that, while the chat is idle, one new ordinary model
turn receives the plain-text event lines; while it is busy, confirm the event
arrives as the next follow-up rather than interrupting the current step.

While still armed, close only the Web viewer and create two separated events
that concern the same label. If the Agent remains live, both can create turns;
this verifies the documented lifetime boundary rather than a close guarantee.
This browser-close behaviour is untested by development. Reopen the viewer if
needed, run `/lorvik-unlisten`, then create one more event and verify no new
turn starts. A native batch admitted before disarm may already be running and
cannot be recalled.
