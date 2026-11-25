# Crush adapter checks

These checks require an installed Crush 0.99.0 and, for the live section, a
human-authorized existing shared server and Lorvik board. **Nothing in this file
was run while preparing the adapter.** Do not run a harness command
without that.

The expected version command is backed by the tagged source's version wiring,
`internal/cmd/root.go:219-222`, and its release build injection,
`.goreleaser.yml:80-83`. The metadata commands below use the tagged workspace
list route, `internal/server/endpoints.go:47-51`, and session-list route,
`internal/server/endpoints.go:170-176`.

## Installed server boundary, before model login

After the human has installed Crush and explicitly chosen an already-running
client/server backend, first confirm its version and inspect only the existing
metadata. These commands do not start a provider turn, but they do contact the
existing server:

```sh
crush --version
export CRUSH_SOCKET=/absolute/path/to/crush.sock
curl --fail --silent --show-error --unix-socket "$CRUSH_SOCKET" http://localhost/v1/workspaces
export CRUSH_WORKSPACE_ID=THE_EXISTING_WORKSPACE_ID
curl --fail --silent --show-error --unix-socket "$CRUSH_SOCKET" \
  "http://localhost/v1/workspaces/$CRUSH_WORKSPACE_ID/sessions"
export CRUSH_NATIVE_SESSION=THE_VISIBLE_NATIVE_SESSION_ID
curl --fail --silent --show-error --unix-socket "$CRUSH_SOCKET" \
  "http://localhost/v1/workspaces/$CRUSH_WORKSPACE_ID/sessions/$CRUSH_NATIVE_SESSION"
```

Require a response whose `id` matches `CRUSH_NATIVE_SESSION` and whose
`attached_clients` is positive. The value is aggregate viewer presence, so use
one visible client while checking. Find the PID of that client by a human
inspection of the existing TUI process, set `CRUSH_CLIENT_PID` to it, choose a
fresh `LORVIK_LABEL`:

```sh
export LORVIK_CONFIG=/absolute/path/to/agent.toml
export LORVIK_LABEL=my-crush-chat
export CRUSH_CLIENT_PID=THE_VISIBLE_CRUSH_CLIENT_PID
```

Before sending anything to the agent endpoint, run this recording probe from
the Lorvik repository root. It uses the production bridge and real session-presence
GETs against the existing Crush server, but intercepts its POST as local JSON.
Its fake listener records label/config argv and emits one literal event. It
cannot start a provider turn:

```sh
export LORVIK_WAKE_ADAPTER="$(pwd)/skills/lorvik/adapters/crush/lorvik-wake.ts"
export LORVIK_WAKE_CHECK_DIR="$(mktemp -d)"
touch "$LORVIK_WAKE_CHECK_DIR/.lorvik.toml"
cat >"$LORVIK_WAKE_CHECK_DIR/lorvik" <<'EOF'
#!/bin/sh
printf '%s\n' "$@" "$LORVIK_SESSION" "$LORVIK_CONFIG" > "$LORVIK_WAKE_CHECK_DIR/argv.log"
printf '%s\n' 'now checker commented on #42 "literal $(never-execute)"'
exec cat >/dev/null
EOF
chmod 700 "$LORVIK_WAKE_CHECK_DIR/lorvik"
cat >"$LORVIK_WAKE_CHECK_DIR/probe.ts" <<'EOF'
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const { startBridge, transport } = await import(pathToFileURL(process.env.LORVIK_WAKE_ADAPTER!).href);
const root = process.env.LORVIK_WAKE_CHECK_DIR!;
const options = {
  socket: process.env.CRUSH_SOCKET,
  workspace: process.env.CRUSH_WORKSPACE_ID!,
  nativeSession: process.env.CRUSH_NATIVE_SESSION!,
  session: process.env.LORVIK_LABEL!,
  ownerPid: Number(process.env.CRUSH_CLIENT_PID),
  cwd: root,
  environment: { ...process.env, PATH: `${root}:${process.env.PATH}` },
};
const read = transport(options);
let captured!: () => void;
const reached = new Promise<void>(resolve => { captured = resolve; });
const bridge = await startBridge(options, async (method: string, path: string, body: unknown) => {
  if (method === "GET") return read(method, path);
  if (method !== "POST") throw Error("Unexpected method");
  writeFileSync(join(root, "request.json"), JSON.stringify({ method, path, body }, null, 2));
  captured();
});
const timeout = setTimeout(() => { console.error("Probe timed out"); process.exitCode = 1; void bridge.stop(); }, 10000);
try { await Promise.race([reached, bridge.done]); }
finally { clearTimeout(timeout); await bridge.stop(); }
EOF
bun "$LORVIK_WAKE_CHECK_DIR/probe.ts"
cat "$LORVIK_WAKE_CHECK_DIR/argv.log"
cat "$LORVIK_WAKE_CHECK_DIR/request.json"
```

Require `argv.log` to show `--session`, the chosen label, `inbox`, `--follow`,
`--exit-on-eof`,
the same label, and the original config path, each on its own line.
`request.json` must show `POST /v1/workspaces/WORKSPACE_ID/agent`, the exact
native `session_id`, a nonempty unique `run_id`, and the literal event in
`prompt`. This records the injection invocation without sending it. Transport,
provider execution, and real board filtering remain separate acceptance work.
Do not use a headless `crush run` or a session-resume command.

## Logged-in model delivery

Only after model login and real board interaction are authorized, use the
already visible chat and its same explicit label for every Lorvik action:

```sh
export LORVIK_WAKE_ADAPTER=/absolute/path/to/skills/lorvik/adapters/crush/lorvik-wake.ts
bun "$LORVIK_WAKE_ADAPTER" --arm \
  --socket "$CRUSH_SOCKET" --workspace "$CRUSH_WORKSPACE_ID" \
  --native-session "$CRUSH_NATIVE_SESSION" --session "$LORVIK_LABEL" \
  --owner-pid "$CRUSH_CLIENT_PID"
```

In the agent's existing shell, use:

```sh
lorvik --session "$LORVIK_LABEL" inbox
```

Have a second human add one event that concerns this chat. Confirm it produces
one later turn in `CRUSH_NATIVE_SESSION`, whose injected text asks for the
inbox command above. Then add a relevant event during a busy turn and confirm
the agent receives a separate later turn rather than interruption of the busy
turn. Finally, stop the sidecar before closing or switching the chat; test its
self-exit reporting and no-restart behavior.

Exercise a close immediately after board events as a negative check: every
already admitted batch may still run. Record that as the documented opt-in
gap, never as a passed lifecycle guarantee. Inspect the existing UI before
re-arming because the adapter deliberately never clears accepted or human input.
