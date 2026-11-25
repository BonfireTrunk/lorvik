# DeepSeek Harness wake adapter

This is an opt-in DeepSeek Harness (dsh) `0.2.2-rc.1` plugin.  It is the
DeepSeek adapter that can genuinely wake an idle chat without model polling:
one `lorvik inbox --follow --exit-on-eof --session <label>` child is attached to that live
agent and each roughly 250 ms batch is sent to `agent.followup()`.

`followup()` is the native ordinary-next-turn API: it queues one message and
wakes the driver, including when the agent is idle
(`packages/core/agent/src/runtime-types.ts:233-251`).  The plugin uses the
supported `defineTool()`/`tools.register()` contract
(`packages/core/tools/src/schema.ts:550-590`,
`packages/core/tools/src/index.ts:120-152`) and makes its wake message with
`createUserMessage()`, which supplies an immutable identified user message
(`packages/llm/llm/src/message.ts:241-252`). Its producer is the distinct
`lorvik-wake` source, never the authority-bearing `user` source: the LLM
message-source map is intentionally extensible for producers
(`packages/llm/llm/src/message.ts:103-147`), while message construction deep
freezes the source and content (`packages/llm/llm/src/message.ts:212-252`).

The tagged Harness also has `agent/pre-step` and `agent/turn-stopping` hooks,
but they run only while a step is being proposed or a turn is closing
(`packages/core/agent/README.md:80`,
`packages/core/agent/src/runtime-types.ts:350-417`).  They cannot observe a
later external board change once an idle turn has ended, so they are not a wake
mechanism.  Its Web API has a Session Controller with prompt/control surfaces
(`packages/api/session-controller/README.md:13,35-38`), but those can resolve
or resume sessions.  This adapter deliberately uses the already-live Agent
plugin and never uses Web control to resume a closed session.

## Install and arm

Nothing here was installed or run against dsh during development.  When the
human has chosen to install it, use a profile with the interactive command and
tool services, substituting the absolute path to this directory:

```sh
dsh plugin --profile web add file:/ABSOLUTE/PATH/TO/skills/lorvik/adapters/deepseek
```

The tagged CLI documents `dsh plugin --profile <name> <pnpm-args...>` and says
it forwards those arguments to pnpm (`docs/cli-help.md:25-80`).  The manifest
is a dsh bundle patch in the same form as the tagged bundle example
(`packages/experimental/badge-skill-bundle/package.json:14-39`,
`packages/experimental/badge-skill-bundle/cordis.patch.yml:1-3`).  Local
directory installation, profile composition, and the required services have
not been exercised here; use [check.md](check.md).

Before starting dsh, arrange all of the following in its launch environment:

- `LORVIK_CONFIG` points at the Lorvik configuration.
- `lorvik` is executable on `PATH`.
- the chat working directory has a `.lorvik.toml` binding in it or an ancestor.

Needs a `lorvik` CLI with `inbox --follow --exit-on-eof`.

Choose a new, explicit lower-case label, such as `dsh-triage-01`. A dsh shell
exports `DSH_SESSION_ID` from its live agent's session id
(`packages/shell/shell-env/src/index.ts:72-81,156-163`). Lorvik
recognizes that variable (`internal/cli/session.go`). This adapter still requires the exact
explicit label on every Lorvik invocation so the shell and sibling listener
agree: `lorvik --session dsh-triage-01 ...`. Explicit labels take precedence
over native detection (`internal/cli/session.go`).

Either explicitly ask the model to call `lorvik_wake` with
`{ "action": "arm", "session": "dsh-triage-01" }`, or use the human-only
slash command:

```text
/lorvik-listen dsh-triage-01
```

`/lorvik-listen` and `/lorvik-unlisten` execute against the receiving agent
without sending the command to the model
(`packages/interaction/commands/src/index.ts:41-77`).  End the wait with
`/lorvik-unlisten` or the `lorvik_wake` `disarm` action.

## Behaviour and boundaries

- At most one listener is held per live dsh agent.  A second arm reports its
  state.  `stop()` marks the entry stopped synchronously before awaiting child
  exit, so an overlapping arm cannot create a duplicate listener.
- Listener stdout is read as plain lines and batched for about 250 ms. Board
  text becomes a text content block; it is never constructed as a shell
  command. The child is spawned with literal argv and `shell: false`, with an
  open stdin pipe that is never written or closed until stop. Lorvik's
  `--exit-on-eof` contract uses that pipe as the owner-lifetime signal
  (`internal/cli/inbox.go`).
- Listener memory is bounded: at most 1 MiB of complete board-event lines is
  coalesced, including newline separators and a 4 KiB recovery-notice/full-text
  reserve, retaining the newest full lines. A line larger than the usable
  space, an evicted older line, or an unterminated/oversized 64 KiB stdout
  frame is counted; the next delivery contains one recovery notice with the
  count and `lorvik --session <label> inbox`. Stderr incomplete frames are
  likewise bounded to 64 KiB and only a 4 KiB diagnostic suffix is retained.
- The adapter holds no growing message-id history: each synchronous native
  `followup()` call is the only invocation in progress, and disarm performs a
  one-time reverse scan of the actual pending inbox by its per-listener source
  token. Its retained bookkeeping is O(1). The host may still
  retain an admitted next-turn backlog while a chat is busy; that is not
  adapter memory and those batches are not guaranteed retractable.
- The event batch is delivered with `followup`, not `inject`: `inject` does
  not wake an idle agent (`packages/core/agent/src/runtime-types.ts:262-270`).
  A busy turn receives the ordinary follow-up in the next-turn queue, rather
  than being interrupted.
- The delivery is user-role input for the ordinary follow-up boundary, but it
  carries the distinct non-human `lorvik-wake` producer source. Native policy
  may therefore treat it as automation rather than a host-attested human
  request; it must not be used to bypass approval or goal authority.
- Agent disposal, plugin disposal, and explicit disarm terminate the child and
  scan the native next-turn inbox for this listener's immutable producer token
  and remove still-pending matches. The registry emits `agent/disposed` when
  its exact live entry is removed (`packages/core/agent/src/index.ts:521-550`),
  and `Inbox.remove` only removes messages that remain pending
  (`packages/core/agent/src/runtime-types.ts:74-111`). A listener that exits by
  itself is reported once, flushes already-complete lines, and is never
  restarted.
- This adapter never resumes a closed session headlessly and never schedules
  timer-driven model work.  Its timer only coalesces already-received lines.

### Important close race

This is deliberately opt-in. The dsh Agent API says a follow-up submitted
after active cancellation may remain queued until activity becomes idle
(`packages/core/agent/src/runtime-types.ts:233-239`). Closing a Web viewer
does not prove the agent has been disposed. While its Agent remains live, new
board events continue to create turns; every batch already admitted to the
native inbox may execute. Disarm before closing or completing a request. The
adapter can remove only entries still visible in `nextTurn`, and cannot retract
a turn that has already begun. This is why it never arms itself automatically.

If that gap is unacceptable, use the waiting fallback instead: keep the task
in Lorvik's waiting state and have a human reopen the chat or run
`lorvik --session <label> inbox --follow --once` from an existing turn.  There is then
no automatic wake.

## Verification status

Verified by tagged source inspection:

- version `0.2.2-rc.1` (`package.json:1-5`);
- plugin bundle/patch shape, native follow-up, tool registration, immutable
  message creation, human-command no-model dispatch, and shell `DSH_SESSION_ID`
  overlay at the source locations cited above;
- Lorvik listener/session-label behaviour against
  `internal/cli/inbox_follow.go` and `internal/cli/session.go`,
  plus EOF-owner shutdown against `internal/cli/inbox.go`.

### Contract inventory

| Adapter use | Verified source |
| --- | --- |
| Plugin `name`, `inject`, and `apply` shape | `vendor/cordis/src/registry.ts:90-121,300-330` |
| Agent-scoped effect and asynchronous disposer ordering | `packages/core/agent/README.md:72-74`; `vendor/cordis/src/fiber.ts:408-447` |
| Exact live-agent lookup before delivery | `packages/core/agent/src/index.ts:586-593` |
| Exact-agent disposal event and identity guard | `packages/core/agent/src/runtime-types.ts:291-299` |
| Native `followup()` next-turn wake | `packages/core/agent/src/runtime-types.ts:233-251` |
| Native pending inbox inspection/removal | `packages/core/agent/src/runtime-types.ts:74-111` |
| Tool call's optional owning `exec.agent` | `packages/core/tools/src/contracts.ts:174-201` |
| Tool definition/registration | `packages/core/tools/src/schema.ts:550-590`; `packages/core/tools/src/index.ts:120-152` |
| Model-free slash-command registration | `packages/interaction/commands/src/index.ts:41-77,280-291` |
| Session working directory | `packages/core/session/src/types.ts:95-106` |
| Logger capability | `vendor/cordis/src/context.ts:25-29`; `vendor/cordis/src/logger.ts:73-76` |
| Shell's native dsh session id | `packages/shell/shell-env/src/index.ts:72-81,156-163` |
| Lorvik explicit session flag/config path | `internal/cli/root.go`, `internal/cli/session.go`, `internal/cli/config.go` |
| Lorvik follow validation and EOF lifetime | `internal/cli/inbox.go` |

Untested against a real installation: package installation and profile loading,
Web disposal timing, provider login, actual board filtering, and actual model
delivery.  The exact future acceptance procedure is [check.md](check.md).

Offline validation run locally, without a dsh binary, a Lorvik server, a
database, or a model: `bun build --no-bundle index.js --outfile
"$TMPDIR/lorvik-wake-dsh.js" --external:@deepseek-ai/dsh-llm
--external:@deepseek-ai/dsh-tools` and `bun test check.test.js` (ten fake-host
boundary tests, including bounded-frame recovery, same-id disposal/recreation,
and offline owner-SIGKILL pipe-EOF behaviour).
