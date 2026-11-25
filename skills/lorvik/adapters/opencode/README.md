# Lorvik wake for OpenCode

An **opt-in v2 TUI plugin for OpenCode 2.1.4** (source tag `v2.1.4`).
Source at `v2.2.5` was also compared. Loading it does nothing until the human
invokes `/lorvik-listen` in the target chat. This adapter targets Linux.

## Install and arm

After the human has installed OpenCode, copy `tui.ts` into exactly one location:

```text
<project>/.opencode/plugins/lorvik-wake/tui.ts
<global-config-directory>/plugins/lorvik-wake/tui.ts
```

Use one of those locations, not both. No package installation, `index.ts`, or
server plugin is needed. Discovery and direct `@opencode/plugin/tui` imports
are documented in tagged [cli/plugins.mdx:50-61](https://github.com/anomalyco/opencode/blob/v2.1.4/services/www/src/docs/content/cli/plugins.mdx#L50-L61);
[host.ts:17-43](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/plugin/src/host.ts#L17-L43)
resolves the optional server and TUI entrypoints independently.

Put a `lorvik` with **`inbox --follow --exit-on-eof`** on `PATH`.
Export an absolute agent config
**before starting OpenCode**:

```sh
export LORVIK_CONFIG=/absolute/path/to/agent.toml
```

Open a chat in a local directory with `.lorvik.toml` in it or an ancestor.
Enter `/lorvik-listen`; the armed toast confirms startup was requested.
Enter `/lorvik-unlisten` before closing the chat or when work is complete.
Rearming the same chat keeps its existing listener. Navigation away disarms;
returning to the chat requires explicit rearming. Load the Lorvik skill separately.
Separate committed route changes are observed synchronously, including a brief
leave-and-return. Changes coalesced inside one Solid batch expose only the final
committed route.

The TUI and the agent's shell must use the **same** `LORVIK_CONFIG` and any
intentional `LORVIK_SESSION` override. Do not inherit `CLAUDE_CODE_SESSION_ID`,
which takes precedence over OpenCode's identity in Lorvik. The plugin supplies
`OPENCODE_SESSION_ID` to its child explicitly. The agent shell receives that
native ID from OpenCode itself, verified at
[core/src/tool/plugin/shell.ts:211-216](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/core/src/tool/plugin/shell.ts#L211-L216).
Lorvik hashes the ID into a label; do not pass the native ID as `--session`.
Remote server environments and per-session environment overrides are not copied
into this local plugin: use matching environments and a locally accessible binding.

## Strategy and lifetime

The plugin owns one `lorvik inbox --follow --exit-on-eof` process for the selected chat,
coalesces complete stdout lines for about 250 ms, and calls:

```ts
await ctx.client.session.prompt({
  sessionID,
  text: message,
  delivery: "queue",
  resume: true,
}, { signal })
```

This queues behind an active turn and schedules a turn when idle. It does not
launch `opencode run`, resume a session through a separate CLI, or poll with a
model. `resume: true` is the existing server's **prompt scheduling field**.
See [client.ts:733-753](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/client/src/promise/generated/client.ts#L733-L753),
[session.ts:155-187](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/core/src/session/session.ts#L155-L187),
and [inbox.ts:500-538](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/core/src/session/inbox.ts#L500-L538).
The notification asks the agent to run `lorvik inbox` to advance the stored
cursor. The listener itself does not acknowledge events; rearming can replay
events that the agent has not acknowledged. After self-exit or uncertain
admission, inspect the chat's pending input and handled messages before rearming;
an earlier admitted batch may still execute. Do not treat rearming as a retry.

There are **two distinct v2 contexts**. The research's `ctx.session.prompt`
and `ctx.event.subscribe` are server-plugin APIs
([promise/plugin.ts:26-54](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/plugin/src/promise/plugin.ts#L26-L54),
[promise/event.ts:1-3](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/plugin/src/promise/event.ts#L1-L3)).
This TUI plugin uses `Plugin.define({ id, setup })`, `ctx.client.session.prompt`
and `ctx.data.listen(({ details: event }) => ...)`. Those are valid **v2 TUI**
APIs, not v1 hooks. The TUI context provides local navigation and cleanup that
the server context cannot observe. Verified at
[tui/plugin.ts:5-13](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/plugin/src/tui/plugin.ts#L5-L13),
[tui/context.ts:63-75](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/plugin/src/tui/context.ts#L63-L75),
[407-470](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/plugin/src/tui/context.ts#L407-L470),
[490-500](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/plugin/src/tui/context.ts#L490-L500)
and [544-559](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/plugin/src/tui/context.ts#L544-L559).

Disarm, archive/delete, execution interruption/failure, navigation, reconnect,
and plugin cleanup revoke dispatch and stop the child. A `createComputed`
observer revokes ownership on route departure, including during asynchronous
teardown. Startup snapshots the native session ID before awaiting teardown.
The route is a mutable store, verified at
[api.tsx:226-227](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/tui/src/plugin/api.tsx#L226-L227)
and [route.tsx:33-42](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/tui/src/context/route.tsx#L33-L42).
The host declares `solid-js` as a peer
([package.json:40-45](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/plugin/package.json#L40-L45));
its ownership observer uses `createComputed`
([session-ownership.ts:1-16](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/app/src/session/session-ownership.ts#L1-L16)).
The root/disposer pattern is exercised at
[solid-runtime.test.ts:64-88](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/app/test-browser/solid-runtime.test.ts#L64-L88).
TUI cleanup is owned and
awaited at [context.tsx:182-217](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/tui/src/plugin/context.tsx#L182-L217)
and [571-592](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/tui/src/plugin/context.tsx#L571-L592).
Lifecycle event names and `event.data.sessionID` come from
[session-event.ts:133-138](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/schema/src/session-event.ts#L133-L138),
[182-190](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/schema/src/session-event.ts#L182-L190)
and [244-263](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/schema/src/session-event.ts#L244-L263).

The plugin spawns Lorvik directly with an unwritten stdin pipe, kept open until
shutdown. `--exit-on-eof` makes the listener exit when that pipe closes, including
after adapter SIGKILL while the board is quiet. No shell guardian is used, and
board text never enters shell source. Shutdown
drops buffered lines and aborts in-flight requests. A self-exit flushes its final
batch, reports once, and stays stopped. Admission failure also stops without
retrying. There is at most one delivery in flight and one pending batch. Pending
data keeps the newest 128 lines within 64 KiB, reserving 256 bytes for one dropped
count notice directing the agent to run `lorvik inbox`. Overflow evicts oldest
lines; lines over 16 KiB are discarded and counted, including fragmented lines.
No delivery waiter or separate batch is accumulated per incoming chunk.
Duplicate plugin copies in one TUI share an ownership guard. **Multiple TUIs
viewing the same chat are unsupported**; arm it in only one terminal.

## Known gap: durable admission outlives the chat

**Closing or disarming cannot retract a prompt already admitted by the server.**
UI cleanup or HTTP abort can race admission. **Every batch already admitted can
still run after the chat or viewer closes**, and execution may recover after a
server restart. There is no one-turn ceiling. While armed, each new event batch
can schedule another turn; route departure or explicit disarm stops further
submissions, but cannot revoke admitted batches. This is why arming is explicit.
A successful prompt call is an admission
receipt, not proof the model handled the event. The adapter makes no automatic
retry after an uncertain receipt.

Admission and wake ordering are verified at
[session.ts:155-187](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/core/src/session/session.ts#L155-L187),
durable execution intent at
[execution.ts:73-77](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/core/src/session/execution.ts#L73-L77),
and recovery at
[execution/restart.ts:192-226](https://github.com/anomalyco/opencode/blob/v2.1.4/packages/core/src/session/execution/restart.ts#L192-L226).
Real-CLI cleanup after SIGKILL, network loss detection timing, actual Solid route
observation and cross-process ownership remain **UNVERIFIED at runtime**.

Additional defining anchors in **v2.1.4**:

| Used shape | Source |
| --- | --- |
| Named `Plugin` export | `packages/plugin/src/tui/index.ts:1` |
| Route fields; toast options | `packages/plugin/src/tui/context.ts:142-145,269-281` |
| Session archive time; directory | `packages/schema/src/session.ts:46-54`; `packages/schema/src/location.ts:8-12` |
| Request `signal` | `packages/client/src/promise/generated/client.ts:291-295,333` |
| `server.connected`; base `event.data.sessionID` | `packages/schema/src/server-event.ts:5`; `packages/schema/src/session-event.ts:29-31` |

Lorvik-local contracts: config and binding search in
`internal/cli/config.go`, identity and precedence in
`internal/cli/session.go`, and follow flags and the EOF flag in
`internal/cli/inbox.go`.

## Version comparison and validation

The adapter-facing `packages/plugin/src/tui/context.ts` and `tui/plugin.ts` are
byte-identical in the two supplied clones, v2.1.4 and v2.2.5. No change to this
plugin's TUI calls is needed based on that comparison. The **server** plugin API
does differ: v2.2.5 gives `ctx.event.subscribe` typed filters and excludes
`server.connected` ([promise/event.ts:1-10](https://github.com/anomalyco/opencode/blob/v2.2.5/packages/plugin/src/promise/event.ts#L1-L10)),
and adds `experimental.idle` hooks
([promise/session.ts:140-198](https://github.com/anomalyco/opencode/blob/v2.2.5/packages/plugin/src/promise/session.ts#L140-L198)).
Neither feature is assumed here. v2.2.5 also changes package entrypoint resolution
([host.ts:17-30](https://github.com/anomalyco/opencode/blob/v2.2.5/packages/plugin/src/host.ts#L17-L30));
the simple local `tui.ts` layout avoids package-exports differences.

**Verified:** the interfaces above were read from tagged source, not installed
packages. Bun transpilation and JavaScript syntax checks are reported with the
delivery. **Untested:** full TypeScript checking, actual plugin loading, live
listener identity, busy/idle model delivery, actual Solid integration and all
server recovery cases. `node check.mjs` passed offline using hand-written API and
reactive-runtime fakes plus a fake CLI. It covers mutable-route retirement,
brief departure, capped overflow during held admission, oversized-line recovery,
stdin EOF, SIGKILL and
self-exit/failure behavior. It starts no server or socket and runs no harness
implementation. See [check.md](check.md).
No harness, cloned-repository code, Lorvik server or database was run.
