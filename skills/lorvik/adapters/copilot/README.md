# Lorvik wake for GitHub Copilot CLI

Target: GitHub Copilot CLI 1.0.105, with the open Copilot SDK v1.0.28. This
opt-in project extension joins the foreground chat; it neither resumes a
closed session nor starts timer-driven model turns.

## Install and arm

Copy only `extension.mjs` to a project extension directory, for example
`.github/extensions/lorvik-wake/`. Needs a `lorvik` CLI with
`inbox --follow --exit-on-eof` (`internal/cli/inbox.go`).

Start below the repository `.lorvik.toml`, choose a fresh UUID, and use a
separate explicit Lorvik label:

```sh
lorvik_copilot_id=$(node -e 'console.log(require("node:crypto").randomUUID())')
LORVIK_CONFIG=/absolute/path/to/agent.toml \
LORVIK_SESSION="cp-$lorvik_copilot_id" \
LORVIK_WAKE_SESSION_ID="$lorvik_copilot_id" \
LORVIK_WAKE=1 \
copilot --no-auto-update --session-id="$lorvik_copilot_id"
```

`LORVIK_CONFIG`, `LORVIK_SESSION`, `LORVIK_WAKE_SESSION_ID`, and
`LORVIK_WAKE` configure this adapter, not Copilot. `LORVIK_SESSION` is its
Lorvik identity: retain it and use `lorvik --session <label>` in every shell
command. The `cp-` label is within Lorvik's 40-character limit.

The native shell/MCP session variable is `COPILOT_AGENT_SESSION_ID` (docs-only:
the [CLI changelog](https://github.com/github/copilot-cli/blob/v1.0.105/changelog.md)
at local `copilot-cli/changelog.md:2022`). Lorvik derives a session label from it
(`internal/cli/session.go`); this adapter still uses the explicit
`LORVIK_SESSION` label, which takes precedence.

`LORVIK_WAKE_SESSION_ID` must match the joined foreground session ID. A
replacement foreground chat fails the pin and remains disarmed. To disarm,
end the extension/chat and relaunch without `LORVIK_WAKE=1`; changing a shell
environment variable does not stop an already running extension.

## Strategy and bounds

The extension directly spawns one listener:

```text
lorvik inbox --follow --exit-on-eof --session <label>
```

Its stdin is an open pipe that the extension never writes or closes until
stopping. Thus the listener observes EOF even if the extension is SIGKILLed;
normal stopping closes the pipe, sends SIGTERM, then escalates to SIGKILL after
one second. There is no guardian process.

Complete listener lines are batched from the first line for about 250 ms, then
submitted with `session.send({ source: "system", mode: "enqueue" })`. The
SDK request provenance is set to application-generated context rather than a
human authorization message. Board text is literal data, never shell input.
`enqueue` waits behind busy work and can start a turn when idle.

There is at most one accepted SDK send in flight and one mutable pending
batch. The injected prompt at this extension boundary is capped at 64 KiB: 1 KiB is reserved for the
validated label and fixed wrapper, 128 bytes for one overflow notice, and the
remainder for complete board lines. New input coalesces into the pending batch;
on overflow it retains newest complete lines and adds one `N board event lines
dropped; run lorvik inbox` notice. A fragmented overlong line is dropped, and
the last listener diagnostic is capped at 4,096 characters. Stopping discards the pending
batch; it does not retract a send already accepted by Copilot.

Every batch Copilot has already admitted can still run after a chat or viewer
closes. If a viewer closes but the extension remains armed, new board events
continue admitting batches and can start turns until the adapter is disarmed.
This is not a one-turn ceiling.

## Evidence and limits

The SDK use is source-verified against the supplied v1.0.28 tag:

- `copilot-sdk/nodejs/package.json:8` targets CLI 1.0.105.
- `copilot-sdk/nodejs/src/extension.ts:114-155` supplies foreground
  `joinSession()`.
- `copilot-sdk/nodejs/src/session.ts:689,790-818,1402-1428` and
  `copilot-sdk/nodejs/src/types.ts:3851` cover `send`, subscriptions, and
  `enqueue`.
- `copilot-sdk/nodejs/src/types.ts:3793-3811` defines `source` and requires
  `"system"` for application-generated context. The accompanying
  [steering and queueing documentation](https://github.com/github/copilot-sdk/blob/v1.0.28/docs/features/steering-and-queueing.md#delivery-modes)
  explains the authorization distinction and that `send` acknowledges
  acceptance, not consumption. Its lines 53-56 also warn that remote backends
  may not preserve source metadata end-to-end; preservation is docs-only and
  untested here.

Closed-CLI behavior is docs-only and untested: [extension discovery and
foreground attachment](https://github.com/github/copilot-sdk/blob/v1.0.28/nodejs/docs/extensions.md#how-extensions-work), and SDK queueing behavior.
The offline fixture proves only this adapter’s local process and injection
boundary. It does not prove discovery, SDK bundling, real queue scheduling,
model wake, Windows behavior, or closure races. See [check.md](check.md).

If installation or verification is unavailable, use the [waiting fallback](../../reference/waiting.md): state that there is no automatic wake and ask a human to reply when the board changes.
