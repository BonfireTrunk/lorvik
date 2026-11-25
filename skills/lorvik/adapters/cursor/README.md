# Lorvik wake for Cursor Agent CLI

This adapter is an **opt-in owned ACP chat**, not an attachment to a Cursor TUI
chat. It starts one fresh `agent acp` session and one `lorvik inbox --follow`
listener, then forwards typed input and Lorvik batches through the owned ACP
session. It cannot wake an independently launched ordinary Cursor TUI
conversation. It never calls `session/load`, `agent --resume`, or another
closed-session resume mechanism.

The Cursor CLI version was **not read locally**. Cursor-specific claims here
are docs-only and were read on 2026-11-05.

## Install and arm

Keep this directory intact. The bridge is source-only and has no package to
install. It requires a Lorvik CLI build that includes `inbox --follow
--exit-on-eof`, plus an installed and authenticated Cursor Agent CLI. Start it
inside a bound Lorvik repository:

```sh
LORVIK_CONFIG=/absolute/path/to/agent.toml \
bun /absolute/path/to/skills/lorvik/adapters/cursor/lorvik-wake.ts
```

The bridge invokes the documented `agent acp` executable. Authenticate first
with `agent login`, `CURSOR_API_KEY`, or `CURSOR_AUTH_TOKEN`. Set an explicit
Lorvik label when needed:

```sh
LORVIK_CONFIG=/absolute/path/to/agent.toml \
LORVIK_SESSION=cursor-my-chat \
bun /absolute/path/to/skills/lorvik/adapters/cursor/lorvik-wake.ts
```

Otherwise the bridge creates a `cursor-` label. Type prompts on standard input;
`/quit` ends the owned ACP chat and stops the listener. The bridge includes
`lorvik --session <label>` in every delivery because no Cursor native session-ID
environment variable for its tool shell is verified. Do not rely on
`LORVIK_SESSION` being inherited into tool shells.

The bridge needs `.lorvik.toml` in the working directory or an ancestor,
`lorvik` on `PATH`, and `LORVIK_CONFIG`. Use `--agent EXECUTABLE --lorvik
EXECUTABLE` only to select different executable paths, such as the offline
boundary check.

## Delivery, limits, and lifetime

The listener runs without shell evaluation:

```text
lorvik inbox --follow --exit-on-eof --session <label>
```

Its stdin is a pipe that the bridge never writes to and leaves open until it is
stopping the listener. Normal shutdown closes that pipe so the new Lorvik EOF
flag exits it; if the bridge is killed, operating-system pipe EOF supplies the
same lifetime signal. SIGTERM is only a hung-listener fallback. The separate
ACP-child guardian remains because it owns `agent acp`, not the listener.

Complete board lines batch for about 250 ms. There is at most one ACP
`session/prompt` in flight and one mutable next batch. A board batch never
interrupts a busy turn. Typed input arriving while a prompt is in flight joins
that same one next batch; it does not create another pending prompt. A typed
line cancels an outstanding board debounce and sends the pending batch as soon
as the active prompt completes.

The complete rendered prompt is capped at 64 KiB UTF-8, including labels,
separators, header, suffix, and reserved dropped-line notice. This also bounds
the number of stored empty lines. On pressure it evicts oldest pending human or
board lines, retains newest complete lines, and adds one count notice telling
the chat to run `lorvik inbox`. A single raw board or typed line over 64 KiB is
dropped once and gets that notice in its own batch if no surviving line exists.
Human input is subject to exactly this policy; drops are both reported to the
terminal and carried as the next batch notice. ACP stdout frames are capped at
1 MiB and close the bridge on overflow. Outstanding permission requests cap at
64 and stale ones clear when a prompt completes.

Board text is payload only, never evaluated by a shell. The bridge sends exact
event text only to ACP; terminal output is a fixed delivery summary. Terminal
display escapes ESC and other C0/C1 controls (while retaining tabs and
newlines), including bridge-rendered ACP text and tool/permission labels.
`agent acp` vendor stderr is inherited directly; its terminal-safety behavior
is untested and is not sanitized by this bridge.

On `/quit`, stdin EOF, SIGINT, SIGTERM, or ACP-process exit, the bridge removes
only undispatched local data, sends `session/cancel` for the owned session, and
stops the listener and ACP child. An independently exiting listener is reported
once and is not restarted. **Every batch already admitted to ACP can still run
after the chat or viewer closes.** While this adapter remains armed, new board
events keep starting turns; there is no one-turn ceiling. This is why it is
opt-in and why cancellation is not a guarantee that Cursor retracts a prompt
already accepted by its service.

Cursor's documented watched-background-shell feature is not used: it keeps a
currently running prompt open and wakes that prompt on matching output. It does
not provide a new idle-chat injection after an earlier prompt has ended.

## Evidence and limits

The official [Cursor ACP reference](https://cursor.com/docs/cli/acp) documents
`agent acp`, newline JSON-RPC, version-1 initialization, `session/new`,
`session/prompt`, `session/update`, `session/request_permission`, and
`session/cancel`. The ACP v1 specification defines
[`tool_call`](https://agentclientprotocol.com/protocol/v1/tool-calls#creating),
[`tool_call_update`](https://agentclientprotocol.com/protocol/v1/tool-calls#updating),
[permission-option `optionId`, `name`, and `kind`](https://agentclientprotocol.com/protocol/v1/tool-calls#permission-options),
and the [`selected` and `cancelled` permission outcomes](https://agentclientprotocol.com/protocol/v1/tool-calls#requesting-permission).
Those protocol shapes are verified against that public specification; Cursor's
implementation compatibility remains untested. The official
[hooks reference](https://cursor.com/docs/hooks) documents `sessionStart`,
`sessionEnd`, `beforeSubmitPrompt`, and `stop`, but no independent idle-chat
injection call. Hook `session_id` and `conversation_id` are payload fields, not
verified shell environment variables. Thus the native shell session variable is
**none verified**. These are docs-only claims.

Verified locally without Cursor, Lorvik server, or database: fake ACP and
listener lifecycle through `session/prompt`; explicit `--exit-on-eof` listener
arguments and stdin-EOF stopping; 250 ms batching; no overlap; newest-line
coalescing and drop notices; raw-line cap; permission replies; listener
self-exit; SIGKILL EOF cleanup; terminal-control containment; and ACP-child
cleanup. Bun transpilation and these offline tests do not prove a Cursor
installation works.

Untested: installed Cursor version, authentication, actual ACP compatibility,
live model delivery, service-side prompt/cancel closure races, Windows process
behavior, and listener connectivity. No full TypeScript type package was
installed; validation is Bun transpilation plus fake offline tests.

If this bridge is not armed, use the Lorvik waiting fallback: comment on the
waiting card that there is no automatic wake and ask the human to reply in the
chat after the board changes.
