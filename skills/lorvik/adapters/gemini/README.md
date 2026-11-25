# Gemini CLI 0.67.0: board events on the next human prompt, no idle wake

This **next-prompt fallback** delivers pending board events with the next
prompt the human sends and **never wakes an idle chat**. It is not an automatic
wake adapter. `BeforeAgent` reads pending Lorvik events with `--peek`, selects
`wake: true` events, and appends their JSON to that already submitted prompt.
There is no listener or periodic model turn.

Harness claims below were checked against the read-only `gemini-cli` source at
**v0.67.0** (`package.json:3`). Evidence paths are relative to that clone.
Installed-package evidence from the earlier draft has been replaced with
tagged-source evidence. Runtime integration is untested.

## Install and arm

After the human chooses to install Gemini, keep this adapter in a readable
location and put this revision's `lorvik` on its `PATH`. Use a bound Lorvik
repository and export the agent config and a private label **before launching
Gemini**:

```sh
export LORVIK_CONFIG=/absolute/path/to/agent.toml
export LORVIK_SESSION=gemini-lorvik-1
gemini
```

Use a different label for each chat: 1–40 lowercase letters, digits or hyphens.
The label must match all Lorvik shell commands in this chat. After `/clear` or
a session switch, relaunch with the appropriate private label before using
Lorvik again. Exporting variables inside one shell tool call does not configure
subsequent hook processes. There is **no verified native session-ID variable
exported by Gemini's ordinary shell tool**. `GEMINI_SESSION_ID` is supplied to
hooks only, so this adapter requires `LORVIK_SESSION` explicitly.

Merge this into `.gemini/settings.json` or `~/.gemini/settings.json`. Preserve
other hooks and allowed environment variables. Replace the script path; quote
it inside the command if it contains spaces.

```json
{
  "security": {
    "allowedEnvironmentVariables": ["LORVIK_CONFIG", "LORVIK_SESSION"]
  },
  "hooks": {
    "BeforeAgent": [
      {
        "matcher": "*",
        "hooks": [
          {
            "name": "lorvik-inbox",
            "type": "command",
            "command": "node /absolute/path/to/skills/lorvik/adapters/gemini/lorvik-before-agent.mjs",
            "timeout": 20000,
            "description": "Add pending Lorvik events to this submitted turn"
          }
        ]
      }
    ]
  }
}
```

Loading the hook arms it for subsequent submitted prompts. Remove its settings
entry to disarm it. If hooks were disabled, enable `hooksConfig.enabled` in the
same settings file. It requires Node (the tagged package requires Node >=20),
without adapter dependencies to install. The settings hook command is a fixed
operator-controlled string; the adapter runs `lorvik` with `shell: false`, an
argument array and the explicit label. Board text is serialized as data and
never evaluated as shell code.

`--peek` preserves the inbox cursor. The context asks the agent to run normal
`lorvik inbox`, then follow the Lorvik skill. Until that happens, unread events
can appear again on a later prompt. A failed child or invalid response yields
a visible `systemMessage`, leaves the submitted prompt usable, and adds no
unverified board data. No background process needs cleanup when the chat ends.

Reads share a 15-second child budget, with SIGKILL 500 ms after SIGTERM if
needed. Pagination stops after 10 pages. If more events remain, the injected
context says so and asks for a normal inbox read, even if those pages had no
wake events. A large backlog therefore cannot silently hide later wake events
forever. The hook's outer timeout is 20 seconds.
Hook input is capped at 1 MiB, each child JSON response at 2 MiB, and retained
stderr at about 4 KiB. At most 64 KiB of serialized event lines is retained for
the one prompt: newer lines replace older ones, oversized individual lines are
dropped, and one notice gives the drop count and asks for `lorvik inbox`.
An oversized JSON page instead fails visibly and asks for a normal inbox read.
The hook has no persistent listener or delivery queue, so `--exit-on-eof`
does not apply to its short-lived `inbox --peek` child.

## Why this path

Gemini and Qwen share ancestry, but **one best wake adapter does not fit both**.
Gemini extensions package MCP servers, context, commands, skills and hooks;
the inspected extension contract exposes no Pi-style `sendUserMessage` callback.
`BeforeAgent`, `AfterAgent` and `SessionStart` are real hook names.
`UserPromptSubmit` and `Stop` are not Gemini hook events at this tag.
`AfterAgent` can reject a response and force a retry, but only at the end of
an existing turn. Holding it open waiting for board activity would obstruct
turn completion; periodically retrying it would violate the no-timer decision.

There is a native completion injection path: setting
`tools.shell.backgroundCompletionBehavior` to `"inject"` and enabling
`experimental.modelSteering` can submit pending hints while idle. It fires on
**process completion**, rather than each listener stdout line. In active work,
global hints can be consumed between tool turns by running local agents.
That does not guarantee delivery after the busy turn completes, as Pi's
`followUp` does. It is therefore not used. A plain background
`lorvik inbox --follow` is not enough to wake this TUI. Using `--once` would
require re-arming, and shell inactivity expiry must not become a wake policy.

ACP accepts prompts from the client that owns its stdio connection. The
inspected ACP transport does not attach to an independently running TUI;
building a separate owning chat client would change the integration scope.
Concurrent ACP prompts cancel the existing prompt, so even such a client
would need to serialize submissions. No headless session resume is used here.

When waiting, tell the human and comment on the waiting card:
**“Waiting for the board; no automatic wake in Gemini. Events arrive on your
next prompt. Tell me here once it is answered.”** A manually chosen
`lorvik inbox --follow --once --session "$LORVIK_SESSION"` can wait for one
batch in a foreground tool, subject to that tool's timeout. It is not an
automatic idle wake recipe.

## Tagged-source evidence

| Claim | `gemini-cli` v0.67.0 source |
| --- | --- |
| Supported hook names; command fields and grouping | `packages/core/src/hooks/types.ts:43-55`, `:96-120` |
| Hook input and BeforeAgent output | `packages/core/src/hooks/types.ts:141-146`, `:554-558` |
| Visible `systemMessage` output | `packages/core/src/hooks/types.ts:156`; `packages/core/src/hooks/hookEventHandler.ts:464-469` |
| Node >=20 runtime requirement | `package.json:4-5` |
| BeforeAgent context appended to submitted request | `packages/core/src/core/client.ts:960-985`; `docs/hooks/reference.md:145-161` |
| AfterAgent continuation | `packages/core/src/core/client.ts:1003-1063`; `docs/hooks/reference.md:163-180` |
| SessionStart and SessionEnd lifecycle limits | `docs/hooks/reference.md:245-273` |
| Settings paths and hook configuration | `docs/hooks/index.md:94-132`; `packages/cli/src/config/settingsSchema.ts:2548-2565`, `:2628-2638` |
| Sanitized hook env; hook-only `GEMINI_SESSION_ID` | `packages/core/src/hooks/hookRunner.ts:347-366` |
| Literal `GEMINI_CLI=1` and shell use, no native session ID | `packages/core/src/services/shellExecutionService.ts:59-64`, `:563-589` |
| Allowed environment variable setting and sanitization | `docs/reference/configuration.md:2872-2883`; `packages/core/src/services/environmentSanitization.ts:13-45` |
| Declarative extensions, including hook loading | `packages/cli/src/config/extension.ts:24-49`; `packages/cli/src/config/extension-manager.ts:1076-1099`; `docs/extensions/reference.md:112-185` |
| Completion behavior, silence and steering defaults | `docs/reference/configuration.md:1814-1840`, `:2196-2200` |
| Completion injection | `packages/core/src/services/executionLifecycleService.ts:419-435` |
| Idle hints submit a prompt | `packages/cli/src/ui/AppContainer.tsx:2652-2668` |
| Running agents consume global hints at tool boundaries | `packages/core/src/agents/local-executor.ts:638-645`, `:723-741` |
| ACP stdio ownership and concurrent prompt cancellation | `packages/cli/src/acp/acpStdioTransport.ts:15-34`; `packages/cli/src/acp/acpSession.ts:319-322` |

Lorvik citations: `--json` and `--session` are defined in
`internal/cli/root.go`; `LORVIK_CONFIG` selects the config in
`internal/cli/config.go`. `internal/cli/inbox.go` defines
`--peek`, pagination via `--from`, JSON inbox output and cursor preservation;
`internal/cli/session.go` gives the explicit label precedence.

See [check.md](check.md) for offline checks and the unrun human verification
procedure. Local syntax/transpilation checks passed:

```sh
node --check skills/lorvik/adapters/gemini/lorvik-before-agent.mjs
bun build --no-bundle skills/lorvik/adapters/gemini/lorvik-before-agent.mjs --outfile "$TMPDIR/lorvik-gemini-hook-check.mjs"
```

These are syntax/transpilation checks, not full TypeScript checking. Gemini hook
execution, actual prompt-context delivery, login/model
behavior, sandbox access to Lorvik, and Windows behavior remain **UNTESTED**.
The separate `sh skills/lorvik/adapters/gemini/check-offline.sh` fixture passed
with a fake CLI: explicit session/config, two-page delivery, quiet output,
newest-event byte cap, oversized page, invalid data, child failure, and inactive
gates. No installed harness was involved.
No harness binary, cloned harness code, model session, server or database was
run during this work. Source inspection proves the contract, not a working
installed integration.
