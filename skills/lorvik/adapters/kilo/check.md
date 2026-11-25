# Kilo delivery-boundary checks

These checks require a human-installed Kilo 7.8.27 and an approved Lorvik
environment. **Nothing in this file was run during adapter development.** Do
not run the final section until a human accepts the closed-chat queue-admission
gap stated in [README.md](README.md).

## Installed-plugin injection boundary, without a model

Use a temporary recording wrapper around the production plugin. It checks that
the installed SDK exposes `client.v2.session.prompt`, then replaces only that
method with a recorder. The listener, commands, batching, and lifetime handlers
remain the production adapter. No prompt reaches the server or provider.

From the Lorvik repository root, create an isolated probe project:

```sh
export LORVIK_WAKE_ADAPTER="$(pwd)/skills/lorvik/adapters/kilo/lorvik-wake.ts"
export LORVIK_WAKE_CHECK_DIR="$(mktemp -d)"
mkdir "$LORVIK_WAKE_CHECK_DIR/.kilo"
touch "$LORVIK_WAKE_CHECK_DIR/.lorvik.toml" "$LORVIK_WAKE_CHECK_DIR/config.toml"
cat >"$LORVIK_WAKE_CHECK_DIR/lorvik" <<'EOF'
#!/bin/sh
printf '%s\n' "$@" "$LORVIK_SESSION" "$LORVIK_CONFIG" > "$LORVIK_WAKE_CHECK_DIR/argv.log"
printf '%s\n' "$$" > "$LORVIK_WAKE_CHECK_DIR/listener.pid"
printf '%s\n' 'now checker commented on #42 "literal $(never-execute)"'
exec cat >/dev/null
EOF
chmod 700 "$LORVIK_WAKE_CHECK_DIR/lorvik"
cat >"$LORVIK_WAKE_CHECK_DIR/probe.ts" <<'EOF'
import { appendFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const { default: wake } = await import(pathToFileURL(process.env.LORVIK_WAKE_ADAPTER!).href);
export default {
  id: "lorvik-wake-probe",
  async tui(api: any) {
    if (typeof api.client.v2.session.prompt !== "function") throw Error("Missing v2 prompt API");
    const recorder = { v2: { session: { async prompt(input: unknown) {
      appendFileSync(join(process.env.LORVIK_WAKE_CHECK_DIR!, "prompt.jsonl"), JSON.stringify(input) + "\n");
      return { data: { data: {} } };
    } } } };
    await wake.tui(new Proxy(api, {
      get(target, key, receiver) {
        return key === "client" ? recorder : Reflect.get(target, key, receiver);
      },
    }));
  },
};
EOF
python3 - <<'PY'
import json, os
from pathlib import Path
root = Path(os.environ["LORVIK_WAKE_CHECK_DIR"])
(root / ".kilo/tui.json").write_text(json.dumps({"plugin": [(root / "probe.ts").as_uri()]}))
PY
cd "$LORVIK_WAKE_CHECK_DIR"
PATH="$LORVIK_WAKE_CHECK_DIR:$PATH" LORVIK_CONFIG="$LORVIK_WAKE_CHECK_DIR/config.toml" kilo
```

Open one chat and run `/lorvik-session`, then `/lorvik-listen`. In a second
terminal, substitute the printed temporary directory and inspect:

```sh
export LORVIK_WAKE_CHECK_DIR=/THE/TEMPORARY/DIRECTORY
cat "$LORVIK_WAKE_CHECK_DIR/argv.log"
cat "$LORVIK_WAKE_CHECK_DIR/prompt.jsonl"
ps -p "$(cat "$LORVIK_WAKE_CHECK_DIR/listener.pid")" -o pid=,ppid=,args=
```

`argv.log` must contain these seven lines, with the label shown by
`/lorvik-session` and the actual temporary config path:

```text
--session
kilo-<24 lowercase hex characters>
inbox
--follow
--exit-on-eof
kilo-<24 lowercase hex characters>
/THE/TEMPORARY/DIRECTORY/config.toml
```

`prompt.jsonl` must contain one request for the displayed native `sessionID`,
with `delivery: "queue"`, `resume: true`, and `prompt.text` containing the
literal event and matching `lorvik --session <label> inbox` instruction. This
is evidence at the SDK invocation boundary. The recorder intentionally does
not test server admission or model execution. Its installed-plugin behavior
is untested until a human runs this recipe.

The listener keeps a piped stdin open for `--exit-on-eof`; do not replace it
with inherited stdin or `ignore`. For overflow, make the wrapper emit more than
1 MiB before a newline and then one short line: expect the short line plus one
omitted-line notice telling the agent to run `lorvik --session <label> inbox`.
For a stalled worker request, expect the adapter's local 10-second deadline to
disarm without retry; abort alone is not a bounded completion guarantee because
served worker RPC calls are intentionally unbounded
(`kilocode/packages/opencode/src/kilocode/util/rpc.ts:159-165`).

Run `/lorvik-unlisten`, then confirm it is gone:

```sh
if kill -0 "$(cat "$LORVIK_WAKE_CHECK_DIR/listener.pid")" 2>/dev/null; then exit 1; fi
```

Repeat `/lorvik-listen` then switch away from the chat; repeat the `kill -0`
check. Rapidly run `/lorvik-unlisten` and `/lorvik-listen`;
the second arm immediately refuses while the prior child exits, so no second
child starts. Once `/lorvik-unlisten` has completed, a later explicit
`/lorvik-listen` may start one new child.

## Logged-in delivery check

After a human logs in and explicitly accepts the lifetime gap, use an approved
disposable Lorvik board. Exit the probe project and load the production plugin
directly as the README describes, using real `lorvik` and config. Do not keep
the recording wrapper loaded. Have another actor comment on a card that concerns the
labelled chat while it is idle. Confirm one new turn appears in that same chat
and the injected text directs the agent to run `lorvik --session <label> inbox`.

Repeat while the agent is busy. Confirm its current work reaches an input
boundary before the queued notification runs. Separately test explicit disarm,
navigation, listener authentication failure, and normal TUI exit. Expect one
failure toast, no automatic listener restart, and local child cleanup. An input
already admitted by Kilo can still run after close; this check cannot prove a
viewer lease.
