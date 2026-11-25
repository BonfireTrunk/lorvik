# Lorvik wake for Kilo Code CLI 7.8.27

This opt-in Kilo TUI plugin keeps one `lorvik inbox --follow` child for an
explicitly armed displayed chat. It batches complete output lines for 250 ms,
then submits one v2 input with `delivery: "queue"`. Kilo durably admits that
input and wakes an idle session; queued inputs are promoted one at a time after
the active turn. It never launches `kilo run`, resumes a closed session from a
separate process, or uses a timer to submit a model turn.

## Install and arm

Keep `lorvik-wake.ts` and `identity.ts` together. Add the absolute file URL to
the `plugin` array in `.kilo/tui.json`, preserving existing entries:

```json
{
  "plugin": ["file:///absolute/path/to/skills/lorvik/adapters/kilo/lorvik-wake.ts"]
}
```

Export `LORVIK_CONFIG` before Kilo starts, put `lorvik` on `PATH`, and open a
directory bound by `.lorvik.toml`. The plugin refuses to arm otherwise.
Needs a `lorvik` CLI with `inbox --follow --exit-on-eof`.

1. Open the target chat and run `/lorvik-session`. It shows the explicit
   Lorvik label `kilo-<24 lowercase hex characters>` derived from that Kilo
   session ID.
2. Require every Lorvik command in this chat to include `--session <label>`
   before the agent claims or creates work.
3. Run `/lorvik-listen` when this chat is actually waiting. Run
   `/lorvik-unlisten` before closing or changing chats.

The listener receives that explicit label as both `--session` and
`LORVIK_SESSION`, starts `inbox --follow --exit-on-eof`, and keeps stdin open
without writing until teardown. An abrupt plugin/TUI death therefore
closes that pipe and stops the listener. Board lines are passed as child-process
argv and JSON request data, never parsed by a shell. The queued text tells the agent to read
`lorvik --session <label> inbox`. Follow mode does not acknowledge the stored
inbox cursor.

## Lifetime and delivery

One plugin instance permits one armed listener. It watches the displayed route
every 100 ms and disarms on navigation; this local timer never submits a
prompt. Disposal and explicit disarm discard undelivered lines, abort in-flight
HTTP calls, send the child `SIGTERM`, and
use `SIGKILL` after one second. It keeps one 1 MiB coalesced batch behind one
in-flight admission, evicting oldest complete lines and reporting their count
once; oversized records are counted once and newest complete lines remain. Each
admission has a 10-second local deadline which races the SDK promise, so
teardown settles even if a worker RPC ignores its abort signal. A failure or
timeout disarms without retry, but cannot cancel an already accepted worker
request. A serialized teardown keeps the listener slot
occupied until the old child exits, so concurrent `/lorvik-unlisten` and
`/lorvik-listen` cannot overlap listeners. Listener self-exit sends its final
complete batch once and reports one UI error, but keeps its slot until explicit
disarm or disposal so that final delivery remains cancellable. It never
restarts.

An admission can still win a close or navigation race. Once Kilo has durably
accepted a queue item, aborting the request cannot retract it, so every already
admitted batch may run after the visible chat is closed; until local disarm wins,
new output can also start another turn. There is no verified viewer
lease in Kilo 7.8.27. Arming is therefore explicit. Use one TUI per chat and
disarm before closing or switching; abrupt TUI death, power loss, and multiple
viewers remain untested.

If the child or delivery request fails, the adapter disarms and does not retry:
transport failure does not reveal whether Kilo admitted the input. Inspect the
ordinary inbox, fix the cause, then explicitly rearm. A board with no automatic
wake should use the waiting fallback in `skills/lorvik/reference/waiting.md`.

## Source verification

The cloned source is tagged `v7.8.27`; its root version is `7.8.27`
(`kilocode/package.json:329`) and the plugin package has the same version
(`kilocode/packages/plugin/package.json:3-4`). The following claims were
checked against that source, not an installed package.

| Contract | Tagged source |
| --- | --- |
| TUI module shape, route and client access | `kilocode/packages/plugin/src/tui.ts:54-68,589-641` |
| Legacy slash-command registration used here | `kilocode/packages/plugin/src/tui.ts:87-120`; `kilocode/packages/opencode/src/plugin/tui/runtime.ts:615-635` |
| Lifecycle disposal | `kilocode/packages/plugin/src/tui.ts:531-536`; `kilocode/packages/opencode/src/plugin/tui/runtime.ts:391-469` |
| `.kilo/tui.json` discovery and `plugin` array | `kilocode/packages/opencode/src/config/paths.ts:23-44`; `kilocode/packages/opencode/src/config/tui.ts:90-99,219-235` |
| File URL entry and default-export validation | `kilocode/packages/opencode/src/plugin/shared.ts:137-157,283-314` |
| Exact v2 method and nested request shape | `kilocode/packages/sdk/js/src/v2/gen/sdk.gen.ts:11226-11264`; `kilocode/packages/sdk/js/src/v2/gen/types.gen.ts:21282-21324` |
| HTTP route, durable admission, and `resume` wake | `kilocode/packages/protocol/src/groups/session.ts:216-234`; `kilocode/packages/core/src/session.ts:368-391` |
| FIFO queued promotion and serialized successor turns | `kilocode/packages/core/src/session/input.ts:268-288`; `kilocode/packages/core/src/session/runner/llm.ts:187-195`; `kilocode/packages/core/src/session/run-coordinator.ts:51-92` |
| Worker RPC leaves served proxied calls unbounded | `kilocode/packages/opencode/src/kilocode/util/rpc.ts:159-165` |
| Lorvik EOF exit contract and cancellation | `internal/cli/inbox.go` |
| TUI toast, dialog, and working directory | `kilocode/packages/plugin/src/tui.ts:227-232,129-135,376-386` |
| v2 client getter and session getter | `kilocode/packages/sdk/js/src/v2/gen/sdk.gen.ts:12934-12936,12635-12637` |
| Prompt text request type | `kilocode/packages/sdk/js/src/v2/gen/types.gen.ts:5259-5263` |
| SDK request signal and throw-on-error fields | `kilocode/packages/sdk/js/src/v2/gen/client/types.gen.ts:10-12,45-51`; `kilocode/packages/sdk/js/src/v2/gen/client/client.gen.ts:81-100,207-208` |
| Embedded fetch omits request signal | `kilocode/packages/opencode/src/cli/cmd/tui.ts:83-98,437-448`; `kilocode/packages/opencode/src/kilocode/util/rpc.ts:212-220` |
| Lorvik root, session label, and config inputs | `internal/cli/root.go`; `internal/cli/session.go`; `internal/cli/config.go` |

**Native shell session variable: none verified for Kilo 7.8.27.** The native
v2 bash tool creates its child process without a session-specific environment
(`kilocode/packages/core/src/tool/bash.ts:154-164`), and explicitly records
session-aware plugin environment augmentation as future work
(`kilocode/packages/core/src/tool/bash.ts:62-75`). This is why the adapter
uses its own explicit Lorvik label and does not claim `KILO_SESSION_ID` or
`OPENCODE_SESSION_ID` exists in agent shell tools.

OpenCode 2.1.4 differs materially: its plugin shell tool writes
`OPENCODE_SESSION_ID` before executing a shell command
(`opencode-2.1.4/packages/core/src/tool/plugin/shell.ts:203-218`) and its TUI
plugin API is `Plugin.define({ setup(context) })`
(`opencode-2.1.4/packages/plugin/src/tui/plugin.ts:5-13`), not Kilo's TUI
module contract above. An OpenCode adapter must not be copied into Kilo.

## Checked and untested

Offline validation ran `bun build --no-bundle --target=bun ...` successfully
and `bun test skills/lorvik/adapters/kilo/lorvik-wake.test.ts` successfully:
**13 pass, 0 fail**. The tests exercise the real plugin path against a fake Kilo
API and fake `lorvik` child. They pin the explicit label/config argv, 250 ms
literal-line batching, the nested queued prompt call, silence, navigation
cleanup, one self-exit report/no restart, disposal, concurrent teardown/re-arm
exclusion, ordered admission, and dropping a queued batch at disarm. They do
not execute Kilo, contact a server, or call a model. They include abrupt-exit
SIGKILL/EOF cleanup, bounded overflow, and an injected deadline whose fake
request ignores AbortSignal. The self-exit test also
pins that explicit disarm aborts its final held request and prevents a queued
remainder from being admitted.

Full TypeScript checking against Kilo's installed SDK, Kilo plugin loading,
real Kilo HTTP admission, provider execution, board delivery, Windows, and
abrupt-death cleanup are **UNTESTED**. See [check.md](check.md).
