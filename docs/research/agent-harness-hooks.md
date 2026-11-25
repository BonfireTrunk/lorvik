# Agent harness hooks and skill loading (verified research)

Verified on 2026-10-18 from primary docs and repositories (fetched that day). Anything not confirmed in a primary source is marked "not verified". Versions are the latest stable release the releases page or npm registry showed on that date.

Purpose: Lorvik is a CLI plus SKILL.md task tracker. A running agent cannot receive a push, so Lorvik wants to inject "what changed since your last call" at natural lifecycle points. This file records which points exist per harness and how output reaches the model.

## Comparison table

| Harness | Version (date seen) | Session-start hook? | Pre/post tool hooks? | Stop/idle hook? | Hook output injected into model context? | Skills (SKILL.md)? | AGENTS.md? |
|---|---|---|---|---|---|---|---|
| Claude Code | 2.1.302 (2026-10-16) | Yes: `SessionStart` | Yes: `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PostToolBatch` | Yes: `Stop`, `SubagentStop`, `Notification` (`idle_prompt`) | Yes, for `UserPromptSubmit`, `SessionStart`, tool hooks (`additionalContext`), `Stop` (`additionalContext` keeps turn going) | Yes (native) | No; reads CLAUDE.md. AGENTS.md not verified in this pass |
| Codex CLI | 0.163.0 (2026-10-15) | Yes: `SessionStart` | Yes: `PreToolUse`, `PostToolUse`, `PermissionRequest` | Yes: `Stop`, `Interrupt`; legacy `notify` on turn complete | Yes: `additionalContext`, plus plain stdout for `SessionStart`/`UserPromptSubmit`/`SubagentStart` | Yes | Yes (native) |
| OpenCode | 1.19.3 (2026-10-16) | Event `session.created` (in-process plugin, no stdin hook) | Yes: `tool.execute.before`, `tool.execute.after` | Event `session.idle` | Via plugin code: can mutate tool output, user message parts, system prompt (experimental hooks) | Yes | Yes (native), CLAUDE.md fallback |
| Gemini CLI | 0.64.0 (2026-10-13) | Yes: `SessionStart` | Yes: `BeforeTool`, `AfterTool` (plus model hooks) | Yes: `AfterAgent`; `SessionEnd` | Yes: `additionalContext` on `SessionStart`, `BeforeAgent`, `AfterTool` | Yes | Only if `context.fileName` includes it |
| GitHub Copilot CLI | 1.0.98 (2026-10-14) | Yes: `sessionStart` / `SessionStart` | Yes: `preToolUse`, `postToolUse` | Yes: `agentStop`, `subagentStop`; `notification` | Yes: `sessionStart`, `postToolUse`, `notification`, `subagentStart` `additionalContext`. `userPromptSubmitted` command output is dropped | Yes | Yes (AGENTS.md, CLAUDE.md, GEMINI.md) |
| Cursor CLI | version not verified (cursor-agent builds are date versioned, e.g. 2026.08.11 in a forum report) | `sessionStart` documented for hooks; CLI coverage not verified, forum reports conflict | `preToolUse`, `postToolUse` documented; CLI coverage partial/not verified | `stop` documented (`followup_message`) | `sessionStart` and `postToolUse` `additional_context` documented | Yes (`/` menu) | Yes (AGENTS.md and CLAUDE.md at project root) |

Notes on the table:
- "Injected" means documented to reach the model, not only the user.
- Cursor CLI hook parity with the Cursor IDE is unclear: the hooks doc says only `workspaceOpen` explicitly "runs in the Cursor desktop app and CLI"; a forum post reports a Linux regression where no hook fires. Treat Cursor CLI hooks as unreliable (not verified end to end).

---

## 1. Claude Code (Anthropic)

- Docs: https://code.claude.com/docs/en/hooks (the old docs.claude.com URL returns a 301 to it), skills: https://code.claude.com/docs/en/skills
- Latest release: v2.1.302, 2026-10-16 (https://github.com/anthropics/claude-code/releases). npm `@anthropic-ai/claude-code` 2.1.302. Verified 2026-10-18.

### Hook events (exact names, from the hooks reference)

`SessionStart`, `Setup`, `UserPromptSubmit`, `UserPromptExpansion`, `PreToolUse`, `PermissionRequest`, `PermissionDenied`, `PostToolUse`, `PostToolUseFailure`, `PostToolBatch`, `Notification`, `MessageDisplay`, `SubagentStart`, `SubagentStop`, `TaskCreated`, `TaskCompleted`, `Stop`, `StopFailure`, `TeammateIdle`, `InstructionsLoaded`, `ConfigChange`, `CwdChanged`, `DirectoryAdded`, `FileChanged`, `WorktreeCreate`, `WorktreeRemove`, `PreCompact`, `PostCompact`, `PreModelSwitch`, `PostModelSwitch`, `Elicitation`, `ElicitationResult`, `SessionEnd`.

Cadence per the doc: per session (`SessionStart`, `SessionEnd`), per turn (`UserPromptSubmit`, `Stop`, `StopFailure`), per tool call (`PreToolUse`, `PostToolUse`).

`SessionStart` matcher values (the `source`): `startup`, `resume`, `clear`, `compact`, `fork`. `Notification` matcher types include `permission_prompt`, `idle_prompt`, `agent_needs_input`, `agent_completed`.

### Configuration

Three levels: event, matcher group, handlers. Locations (merged, not replaced; all matching hooks run in parallel):

| Location | Scope |
|---|---|
| `~/.claude/settings.json` | all projects |
| `.claude/settings.json` | project, shareable |
| `.claude/settings.local.json` | project, gitignored |
| Managed policy settings | org-wide |
| Plugin `hooks/hooks.json` | when plugin enabled |
| Skill frontmatter `hooks:` | registered when skill invoked, lasts rest of session |
| Subagent frontmatter | while subagent runs |

Shape:

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          { "type": "command", "command": "${CLAUDE_PROJECT_DIR}/.claude/hooks/x.sh", "timeout": 30 }
        ]
      }
    ]
  }
}
```

Handler types: `command`, `http`, `mcp_tool`, `prompt`, `agent` (agent is marked experimental). Command extras: `args` (exec form, no shell), `async`, `asyncRewake`, `shell`, `onFailure` (`continue`/`block`). Common: `if` (permission-rule syntax, only evaluated on tool events), `timeout` (default 600 s for command/http/mcp_tool, 30 s for prompt; lowered to 30 on `UserPromptSubmit`), `statusMessage`, `once` (skill frontmatter only).

Matcher rules: `*`, empty or omitted matches all; letters/digits/`_`/`-`/`,`/`|` are an exact list; any other character makes it an unanchored JS regex. `UserPromptSubmit`, `Stop`, `PostToolBatch`, `CwdChanged`, `TeammateIdle` and others have no matcher support.

Env vars and placeholders: `CLAUDE_PROJECT_DIR`, `CLAUDE_PLUGIN_ROOT`, `CLAUDE_PLUGIN_DATA`, `CLAUDE_CODE_REMOTE`, `CLAUDE_EFFORT`, `CLAUDE_ENV_FILE` (SessionStart/Setup/CwdChanged/FileChanged hooks append `export` lines that are sourced before every later Bash command).

### Input contract (stdin JSON for command hooks; POST body for http)

Common fields: `session_id`, `prompt_id`, `transcript_path`, `cwd`, `scratchpad_dir`, `permission_mode`, `effort`, `hook_event_name`, `agent_id`, `agent_type`.

Event specifics seen in the doc:
- `SessionStart`: `source`, optional `model`, `agent_type`, `session_title`.
- `UserPromptSubmit`: `prompt`, optional `session_title`. Also fires for scheduled tasks, `/loop`, background subagent reports and cross-session messages.
- `PreToolUse`: `tool_name`, `tool_input`, `tool_use_id`.
- `PostToolUse`: `tool_name`, `tool_input`, `tool_response`, `tool_use_id`, optional `duration_ms`, `mcp_server`.
- `Stop`: `stop_hook_active`, `last_assistant_message`, `background_tasks`, `session_crons`. Does not fire on user interrupt; API errors fire `StopFailure`.
- `Notification`: `message`, optional `title`, `notification_type`.

### Output contract and exit codes

- Exit 0: success. Stdout is parsed as JSON only if the whole output is one JSON object; otherwise plain text. Claude never sees stderr from exit 0.
- Exit 2: blocking error where the event supports it; stderr is the reason. Other non-zero codes (including 1) are non-blocking, first stderr line shown to the user.
- Exit-2 effect per event: blocks `PreToolUse`, `UserPromptSubmit`, `Stop` (prevents stopping), `PostToolBatch` (stops loop), `PreCompact`. For `PostToolUse`/`PostToolUseFailure` it only shows stderr to Claude. For `SessionStart`/`SessionEnd` stderr goes to the user only.
- Plain-text stdout on exit 0 is injected into context ONLY for `UserPromptSubmit`, `UserPromptExpansion`, `SessionStart`, `PostModelSwitch`. For every other event exit-0 stdout goes to the debug log only.
- JSON output: universal `continue`, `stopReason`, `systemMessage` (user-facing warning), `suppressOutput` (documented as no effect), `terminalSequence`. `decision: "block"` + `reason` for `UserPromptSubmit`, `PostToolUse`, `Stop`, etc. `hookSpecificOutput` (needs `hookEventName`) carries `additionalContext` for `SessionStart`, `SubagentStart`, `PostModelSwitch`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PostToolBatch`, `Stop`, `SubagentStop`, and `UserPromptSubmit`.
- Placement of `additionalContext`: `SessionStart` at conversation start; `UserPromptSubmit` alongside the prompt; tool events next to the tool result; `Stop`/`SubagentStop` at end of turn (non-error feedback that continues the conversation). `<system-reminder>` tags inside it are escaped.
- Size cap: `additionalContext`, `systemMessage`, `initialUserMessage` and plain stdout are capped at 10,000 characters (overflow goes to a file with a 2,000 character preview).
- `SessionStart` JSON extras: `initialUserMessage`, `watchPaths`, `sessionTitle`, `reloadSkills`.
- `Stop` loop guard: `stop_hook_active` is true when already continuing due to a stop hook. A cap of 8 consecutive continuations applies (reset when Claude calls a tool); `CLAUDE_CODE_STOP_HOOK_BLOCK_CAP` raises it.
- Push-like mechanisms: `asyncRewake: true` runs the command in the background and "wakes Claude on exit 2 with stderr (or stdout) as system reminder". `FileChanged` with `watchPaths` fires when a watched file changes. (The doc excerpt I fetched did not describe the idle-session behavior of asyncRewake in detail; its behavior while the agent is idle is not verified.)

### Skills

- Discovery (from the skills doc): enterprise (managed settings dir), personal `~/.claude/skills/<name>/SKILL.md`, project `.claude/skills/<name>/SKILL.md` (walks up to the repo root), nested `<subdir>/.claude/skills/` (loaded when Claude works on files there), `--add-dir` dirs, plugin `<plugin>/skills/<name>/SKILL.md` (namespaced `/plugin:skill`), claude.ai synced skills in `~/.claude/skills/synced/`. Live reload for the watched dirs; new top-level skills dirs need `/reload-skills`. Legacy `.claude/commands/*.md` still works. `.agents/skills` is NOT listed in the Claude Code skills doc.
- Frontmatter read by Claude Code (all optional): `name`, `description`, `when_to_use`, `argument-hint`, `arguments`, `disable-model-invocation`, `user-invocable`, `allowed-tools`, `disallowed-tools`, `model`, `effort`, `context` (`fork`), `agent`, `background`, `hooks`, `paths`, `shell`, `metadata`, `license`, `compatibility`. `description` + `when_to_use` are truncated at 1,536 characters in the listing. Unrecognized fields are silently ignored.
- Portability caveat from the doc: outside Claude Code (claude.ai uploads, Skills API) only `name`, `description`, `license`, `compatibility`, `metadata`, `allowed-tools` are allowed; other fields cause a hard error.
- Skill body supports dynamic injection: `` !`command` `` runs before Claude sees the content (non-zero exit aborts the invocation). This is a usable "inject state at skill load" path.
- Skills can carry their own `hooks` (registered on invocation, stay for the session). That means a Lorvik SKILL.md could register `PostToolUse`/`UserPromptSubmit` hooks only in Claude Code.

### Instruction files

CLAUDE.md (and `.claude/rules/*.md`); `InstructionsLoaded` hook event exists. AGENTS.md support in Claude Code: not verified in this pass.

Sources: https://code.claude.com/docs/en/hooks, https://code.claude.com/docs/en/skills, https://github.com/anthropics/claude-code/releases. Verified 2026-10-18. Note: the hooks page is about 273k characters and I read roughly the first 200k; the tail (SessionEnd detail, hooks-in-skills section, background hooks section) was not read.

---

## 2. OpenAI Codex CLI

- Docs: https://developers.openai.com/codex/hooks redirects (308) to https://learn.chatgpt.com/docs/hooks. Skills: https://learn.chatgpt.com/docs/build-skills. AGENTS.md: https://learn.chatgpt.com/docs/agent-configuration/agents-md.
- Repo: https://github.com/openai/codex. Latest stable 0.163.0, released 2026-10-15 (tag `rust-v0.163.0`); alphas 0.164.0-alpha.1..3 on 2026-10-16/17. npm `@openai/codex` 0.163.0. Verified 2026-10-18.
- The in-repo `docs/` folder holds `agents_md.md`, `skills.md`, `config.md`, `example-config.md` etc.; `skills.md` and `agents_md.md` are one-line pointers to the developers.openai.com pages. There is no hooks file in `docs/`. The 0.163.0 release notes do not announce a hooks feature (the only "hooks" mention is a network-proxy fix), so hooks are not new in that release.

### Hook events (exact names, from the hooks doc)

`SessionStart`, `SessionEnd`, `SubagentStart`, `SubagentStop`, `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PreCompact`, `PostCompact`, `UserPromptSubmit`, `Stop`, `Interrupt`.

The doc says hooks are enabled by default. Disable with `[features] hooks = false` (`codex_hooks` is a deprecated alias). A secondary source claims hooks shipped in 0.144 and later; I did not find that in a primary source (not verified).

### Configuration

- Either `hooks.json` or inline `[hooks]` tables in `config.toml`. Documented spots: `~/.codex/hooks.json`, `~/.codex/config.toml`, `<repo>/.codex/hooks.json`, `<repo>/.codex/config.toml`. Plugins can bundle `hooks/hooks.json`; enterprises use `requirements.toml` (`allow_managed_hooks_only`).
- Layers are merged, all matching hooks run. Project hooks load only if the project `.codex/` layer is trusted. Non-managed hooks must be reviewed via `/hooks` before they run; `--dangerously-bypass-hook-trust` skips review for one run.
- Structure: event, matcher group (regex on tool name, compaction trigger, start source, agent type), handlers. Handler types `command` and `mcp_tool` work; `prompt` and `agent` are "parsed but skipped". Options: `timeout` (seconds; default 600, but `SessionEnd`/`Interrupt` default 1 s, max 3 s), `statusMessage`, `async`, `commandWindows`, `additionalContextLimit`.

### Input / output contract

- Input is stdin JSON. Common fields: `session_id`, `transcript_path`, `cwd`, `hook_event_name`, `model`; `permission_mode` on most events; `turn_id` on turn-scoped ones. Event fields: `tool_name`, `tool_use_id`, `tool_input`, `tool_response`, `prompt`, `source`, `trigger`, `reason`, `agent_id`, `agent_type`, `stop_hook_active`, `last_assistant_message`.
- Output is JSON on stdout: `continue`, `stopReason`, `systemMessage`, `suppressOutput` (parsed but not implemented), and `hookSpecificOutput` with `hookEventName` plus `additionalContext`, `permissionDecision` (`allow`/`deny` + `updatedInput`), or `decision: {behavior}` for `PermissionRequest`. Legacy `{"decision":"block","reason":...}` accepted.
- Exit 0 with no output: continue. Exit 2 with stderr: block/deny (for `Stop`, requests continuation). `Stop`/`SubagentStop` require JSON; plain text is invalid there.
- Model context: `additionalContext` is added as developer context. Plain stdout text is also added for `SessionStart`, `UserPromptSubmit`, `SubagentStart`. Output over about 2,500 tokens is spilled to disk with a preview (tunable with `additionalContextLimit`). `Stop` block reasons become a new continuation prompt; `PostToolUse` block feedback replaces the tool result.
- Caveat stated by the doc: hooks are "a useful guardrail, not a complete enforcement boundary"; some tool paths can opt out; `permissionDecision: "ask"` is parsed but unsupported.

### Notification mechanism (legacy `notify`)

- `notify` in `~/.codex/config.toml` is an argv array; on turn completion Codex appends one JSON string as the final argv argument (not stdin). Payload type `agent-turn-complete` with `thread-id`, `turn-id`, `cwd`, `client` (optional), `input-messages`, `last-assistant-message`. One event type only; ignored in project `.codex/config.toml`. These details come from secondary sources (backgrind.com, anotifier.io) quoting the Rust source `legacy_notify.rs` and issue https://github.com/openai/codex/issues/4005; I did not open the primary config reference (the learn.chatgpt.com config-reference URL I guessed returned 404). Treat as "secondary, not verified from the official page".
- `[tui] notifications` is a separate desktop-notification setting (secondary source).

### Skills

- Discovery (official build-skills page): `$CWD/.agents/skills`, parent dirs up to `$REPO_ROOT/.agents/skills`, `$HOME/.agents/skills`, `/etc/codex/skills` (admin), and bundled system skills. Symlinks followed. Duplicate names are not merged.
- Frontmatter: only `name` and `description` required. Optional `agents/openai.yaml` with `interface` (display metadata), `policy.allow_implicit_invocation` (default true), `dependencies.tools` (MCP deps).
- Context budget: skills list limited to 2% of context window (or 8,000 characters when unknown); `skills.max_context_tokens` adjustable up to 10,000.
- Install: manual folder, `$skill-creator`, `$skill-installer <name>` (pulls from openai/skills, curated/experimental), or plugins (docs say plugins are the recommended distribution). Invoke with `$skill-name` or `/skills`; implicit by description match. Disable via `[[skills.config]] path=... enabled=false`.
- A secondary source notes the installer defaults to `~/.codex/skills` (via `CODEX_HOME`), differing from the documented personal `~/.agents/skills`. Not verified from a primary source.

### AGENTS.md

Built in. Order (official guide): global `~/.codex` (`AGENTS.override.md` then `AGENTS.md`, first non-empty wins), then from project root down to cwd, one file per directory (`AGENTS.override.md`, `AGENTS.md`, then `project_doc_fallback_filenames`). Concatenated root to leaf; deeper files win. Cap `project_doc_max_bytes` default 32 KiB. Built once per run or TUI session (no live refresh).

Sources: https://learn.chatgpt.com/docs/hooks, https://learn.chatgpt.com/docs/build-skills, https://learn.chatgpt.com/docs/agent-configuration/agents-md, https://github.com/openai/codex/releases, https://github.com/openai/codex/tree/main/docs. Verified 2026-10-18.

---

## 3. OpenCode

- Current repo: https://github.com/anomalyco/opencode (live repo, default branch `dev`, about 215.8k stars). The agentskills.io showcase still links https://github.com/sst/opencode; whether sst/opencode redirects was not confirmed. Docs: https://opencode.ai/docs.
- Latest release: v1.19.3, 2026-10-16 (https://github.com/anomalyco/opencode/releases/latest). npm `@opencode-ai/plugin` 1.19.3 (modified 2026-10-16). Verified 2026-10-18.

### Plugin system (in-process JS/TS, not stdin/stdout scripts)

- Locations (docs): `.opencode/plugins/` (project), `~/.config/opencode/plugins/` (global); npm packages listed under `"plugin"` in `opencode.json` (installed via Bun, cached in `~/.cache/opencode/node_modules/`). Load order: global config, project config, global plugin dir, project plugin dir.
- A plugin is a module exporting async functions: `async ({ project, client, $, directory, worktree }) => hooks`. `client` is the OpenCode SDK client; `$` is the Bun shell.
- Events (docs, consumed through the `event` hook): `command.executed`, `file.edited`, `file.watcher.updated`, `installation.updated`, `lsp.client.diagnostics`, `lsp.updated`, `message.part.removed`, `message.part.updated`, `message.removed`, `message.updated`, `permission.asked`, `permission.replied`, `server.connected`, `session.created`, `session.compacted`, `session.deleted`, `session.diff`, `session.error`, `session.idle`, `session.status`, `session.updated`, `shell.env`, `tool.execute.after`, `tool.execute.before`, `tui.prompt.append`, `tui.command.execute`, `tui.toast.show`.
- Typed `Hooks` interface (read from the published `@opencode-ai/plugin@1.19.3` `dist/index.d.ts`): `event`, `config`, `tool`, `auth`, `provider`, `chat.message`, `chat.params`, `chat.headers`, `permission.ask`, `command.execute.before`, `tool.execute.before`, `shell.env`, `tool.execute.after`, `experimental.chat.messages.transform`, `experimental.chat.system.transform`, `experimental.provider.small_model`, `experimental.session.compacting`, `experimental.compaction.autocontinue`, `experimental.text.complete`, `tool.definition`, `dispose`.
- Signatures that matter for Lorvik:
  - `tool.execute.before(input{tool,sessionID,callID}, output{args})`: mutate args; throwing blocks the call (docs example).
  - `tool.execute.after(input{tool,sessionID,callID,args}, output{title,output,metadata})`: `output.output` is a mutable string, so a plugin can append text to what the model sees as the tool result. This follows from the type; I did not find a doc example of it (inference from types, not a documented pattern).
  - `chat.message(input, output{message, parts})`: runs when a new user message is received; `parts` is mutable, so a plugin can add parts to the user turn (inference from types).
  - `experimental.chat.system.transform(input{sessionID,model}, output{system: string[]})`: edit the system prompt per request. Marked experimental; NOT mentioned in the plugins docs page.
  - `experimental.chat.messages.transform`: edit the message list before the request.
  - `experimental.session.compacting(output{context: string[], prompt?})`: add to compaction prompt (documented).
  - `session.idle` is an event, usable for notifications (documented example shells out via `$`).
- Session start: there is no named "SessionStart" hook; use the `session.created` event (docs list it). Whether it can inject context into the first turn is not documented; combine with `chat.message` or `system.transform` (not verified).
- A new "V2 Promise Plugin API" (`@opencode/plugin`, `Plugin.define({ id, setup })`, `ctx.session.hook("context", ...)` which can edit system/messages/tools immediately before provider dispatch) exists on the `dev` branch (packages/plugin/src/README.md). I could not confirm it is released or that v1 plugins are deprecated. The docs page and the 1.19.3 npm package still describe the v1 `Hooks` interface (the npm package also contains a `dist/v2/` folder). Treat the v1 hooks as current for planning and flag v2 as a migration risk.

### Skills

Docs: https://opencode.ai/docs/skills/. Scans `.opencode/skills/`, `~/.config/opencode/skills/`, `.claude/skills/`, `~/.claude/skills/`, `.agents/skills/`, `~/.agents/skills/` (project paths walk up to the git worktree). Frontmatter: `name`, `description` required; `license`, `compatibility`, `metadata` (string map) optional. Name regex `^[a-z0-9]+(-[a-z0-9]+)*$`, 1-64 chars, must equal directory name; description 1-1024. OpenCode-specific keys live under `metadata`: `opencode/slash: "true"`, `opencode/autoinvoke: "false"`. Permissions: `permission.skill` patterns with `allow`/`deny`/`ask`. Loaded through a `skill` tool and an `<available_skills>` block.

### Instruction files

Docs: https://opencode.ai/docs/rules/. `AGENTS.md` project root (walks up) and global `~/.config/opencode/AGENTS.md`. `CLAUDE.md` and `~/.claude/CLAUDE.md` are fallbacks used only when no AGENTS.md exists. Env switches `OPENCODE_DISABLE_CLAUDE_CODE`, `OPENCODE_DISABLE_CLAUDE_CODE_PROMPT`, `OPENCODE_DISABLE_CLAUDE_CODE_SKILLS`. `instructions` array in `opencode.json` accepts files, globs, URLs.

Sources: https://opencode.ai/docs/plugins/, https://opencode.ai/docs/skills/, https://opencode.ai/docs/rules/, https://github.com/anomalyco/opencode, npm tarball `@opencode-ai/plugin@1.19.3`. Verified 2026-10-18.

---

## 4. Gemini CLI

- Repo: https://github.com/google-gemini/gemini-cli. Latest stable v0.64.0, 2026-10-13 (npm `@google/gemini-cli` 0.64.0). Verified 2026-10-18.
- Docs (raw from repo `docs/`): `docs/hooks/reference.md`, `docs/hooks/index.md`, `docs/cli/skills.md`, `docs/cli/gemini-md.md`. Site: https://geminicli.com/docs/.

### Hooks

- Config: `hooks` object in `settings.json`. Layers merged, highest to lowest: project `.gemini/settings.json`, user `~/.gemini/settings.json`, system `/etc/gemini-cli/settings.json`, extensions. Each event holds an array of `{ matcher?, sequential?, hooks: [ { type: "command", command, name?, timeout (ms, default 60000), description? } ] }`. Only `command` type exists. Manage with `/hooks enable-all|disable-all|enable <name>|disable <name>`. Project hooks are fingerprinted; a changed name/command is treated as untrusted and warns before running.
- Events (exact): `BeforeTool`, `AfterTool`, `BeforeAgent`, `AfterAgent`, `BeforeModel`, `BeforeToolSelection`, `AfterModel`, `SessionStart`, `SessionEnd`, `Notification`, `PreCompress`.
- Input (stdin JSON) common: `session_id`, `transcript_path`, `cwd`, `hook_event_name`, `timestamp`. Event extras: `BeforeTool` `tool_name`, `tool_input`, `mcp_context`, `original_request_name`; `AfterTool` adds `tool_response`; `BeforeAgent` `prompt`; `AfterAgent` `prompt`, `prompt_response`, `stop_hook_active`; `SessionStart` `source` (`startup`/`resume`/`clear`); `Notification` `notification_type` (`ToolPermission`), `message`, `details`.
- Exit codes: 0 success (stdout parsed as JSON), 2 block with stderr as reason, other is a non-fatal warning. Stdout must contain only the final JSON ("Silence is Mandatory"); logs go to stderr.
- Output: `systemMessage`, `suppressOutput`, `continue`, `stopReason`, `decision` (`allow`/`deny`, alias `block`), `reason`, and `hookSpecificOutput`.
- Context injection (per the doc): `hookSpecificOutput.additionalContext` on `AfterTool` (appended to the tool result), `BeforeAgent` (appended to the prompt for that turn), `SessionStart` (first history turn when interactive; prepended to the prompt when non-interactive). `AfterAgent` with `decision: deny` forces a retry using `reason` as the new prompt (so it can act as a "do not stop yet, here is news" lever); `AfterAgent` can also set `clearContext`. `AfterTool` can also return `tailToolCallRequest {name,args}` to chain another tool call.
- Idle/stop: `AfterAgent` is the end-of-turn hook; `SessionEnd` is not awaited.

### Skills

Docs: `docs/cli/skills.md`. Precedence low to high: built-in, extension skills, user (`~/.gemini/skills/` or `~/.agents/skills/`), workspace (`.gemini/skills/` or `.agents/skills/`); `.agents/skills/` wins within a tier. Follows the Agent Skills standard. Commands: `gemini skills list --all`, `gemini skills install <git-url-or-dir> [--path] [--scope user|workspace] [--consent]`, `gemini skills uninstall`, in-session `/skills list|link|enable|disable|reload`. Name and description are injected into the system prompt at startup; body and folder structure are added to history on activation. The skills page does not document frontmatter fields (defers to the spec guide).

### Instruction files

`GEMINI.md` is the default. AGENTS.md is supported only after setting `context.fileName` in `settings.json`, e.g. `["AGENTS.md","GEMINI.md"]` (this exact example is in `docs/cli/gemini-md.md`). Loads global `~/.gemini/GEMINI.md`, workspace and parent dirs, and just-in-time directories when tools touch files; supports `@file.md` imports.

Sources: https://raw.githubusercontent.com/google-gemini/gemini-cli/main/docs/hooks/reference.md, .../docs/hooks/index.md, .../docs/cli/skills.md, .../docs/cli/gemini-md.md, https://github.com/google-gemini/gemini-cli/releases. Verified 2026-10-18.

---

## 5. GitHub Copilot CLI and Cursor CLI (brief)

### GitHub Copilot CLI

- Repo/releases: https://github.com/github/copilot-cli/releases; latest stable v1.0.98, 2026-10-14 (pre-release 1.0.100-1 on 2026-10-16); npm `@github/copilot` 1.0.98. Release notes mention "more reliable command hooks".
- Hooks doc: https://docs.github.com/en/copilot/reference/hooks-configuration. Config merged from: policy (`/etc/github-copilot/policy.d/*.json`), `.github/hooks/*.json`, `~/.copilot/hooks/*.json` (or `$COPILOT_HOME/hooks/`), inline `hooks` in `.github/copilot/settings.json`/`settings.local.json` (it also reads `.claude/settings.json` and `.claude/settings.local.json`), `~/.copilot/settings.json`, plugin `hooks.json`, and custom-agent frontmatter. Format `{ "version": 1, "hooks": { "<event>": [...] } }`. Entry types `command` (`bash`/`powershell`, `cwd`, `env`, `timeoutSec` default 30, `matcher`), `http`, `prompt` (only on `sessionStart`).
- Events: camelCase `sessionStart`, `sessionEnd`, `userPromptSubmitted`, `userPromptTransformed`, `preToolUse`, `postToolUse`, `postToolUseFailure`, `agentStop`, `subagentStart`, `subagentStop`, `errorOccurred`, `preCompact`, `permissionRequest`, `notification`. PascalCase Claude-compatible `SessionStart`, `SessionEnd`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `Stop`, `SubagentStop`, `ErrorOccurred`, `PreCompact`, `PermissionRequest`, `Notification`. The casing selects the payload format (camelCase fields vs snake_case + `hook_event_name`).
- Input on stdin: `sessionId`, `timestamp`, `cwd` plus event fields (`source`, `prompt`, `toolName`, `toolArgs`, `toolResult`, `stopReason`, `stop_hook_active`, ...).
- Output to model: `sessionStart` `additionalContext` (injected into the session), `postToolUse` `additionalContext` (appended to result, 10 KB cap), `notification` `additionalContext` (injected as a user message and "can wake an idle session"), `subagentStart` `additionalContext`. `agentStop`/`subagentStop` `decision: "block"` forces another turn with `reason` as prompt (cap of 8 consecutive blocks). `userPromptSubmitted` `modifiedPrompt` is only honored by SDK programmatic hooks; command/HTTP output is dropped. Command `preToolUse` hooks are fail-closed; timeouts are fail-open.
- Skills (https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-skills): project `.github/skills`, `.claude/skills`, `.agents/skills`; personal `~/.copilot/skills`, `~/.agents/skills`; `copilot skill add <file|url|dir>` and `/skills add`. Frontmatter: `name`, `description` required; `license`, `allowed-tools` optional.
- Instructions (https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/add-custom-instructions): `AGENTS.md`, `CLAUDE.md` (and `.claude/CLAUDE.md`), `GEMINI.md`, `.github/copilot-instructions.md`, `.github/instructions/**/*.instructions.md`, `~/.copilot/copilot-instructions.md`, `COPILOT_CUSTOM_INSTRUCTIONS_DIRS`. Edits take effect on a new or resumed session.

### Cursor CLI (cursor-agent)

- Hooks doc: https://cursor.com/docs/hooks. Config `hooks.json` with `"version": 1` at `<project>/.cursor/hooks.json`, `~/.cursor/hooks.json`, team (dashboard, Enterprise), enterprise (`/etc/cursor/hooks.json` on Linux). Project hooks need a trusted workspace. Merge: `deny` beats `ask` beats `allow`.
- Events (exact): `sessionStart`, `sessionEnd`, `preToolUse`, `postToolUse`, `postToolUseFailure`, `subagentStart`, `subagentStop`, `beforeShellExecution`, `afterShellExecution`, `beforeMCPExecution`, `afterMCPExecution`, `beforeReadFile`, `afterFileEdit`, `beforeSubmitPrompt`, `preCompact`, `stop`, `afterAgentResponse`, `afterAgentThought`; Tab hooks `beforeTabFileRead`, `afterTabFileEdit`; `workspaceOpen`.
- I/O: JSON over stdio. Base fields `conversation_id`, `generation_id`, `model`, `hook_event_name`, `workspace_roots`, `transcript_path`, etc. Output examples: `permission` allow/deny/ask with `user_message`/`agent_message`; `beforeSubmitPrompt` `continue` + `user_message` (cannot add context per the doc's listed output); `stop`/`subagentStop` `followup_message` to auto-continue (default `loop_limit` 5). Context injection: `sessionStart` `additional_context` (+ `env`); `postToolUse`/`postToolUseFailure` `additional_context`. Exit 2 blocks; other non-zero is fail-open unless `failClosed`.
- CLI coverage: the hooks doc says only `workspaceOpen` "Runs in the Cursor desktop app and CLI". A Cursor forum answer (found via search, secondary) said the CLI initially fired only shell hooks and later expanded; another forum report (cursor-agent 2026.08.11 on Linux) says no hook fires. Net: Cursor CLI hook coverage is not verified and appears inconsistent. Sources: https://forum.cursor.com/t/cursor-agent-cli-never-invokes-hooks-json-on-linux-2026-08-11-same-version-works-on-macos/168326.
- Skills (https://cursor.com/docs/skills): `.agents/skills/`, `.cursor/skills/`, `~/.agents/skills/`, `~/.cursor/skills/`, plus compatibility reads of `.claude/skills/` and `.codex/skills/`. Frontmatter: `name`, `description` required; `paths` (older `globs`), `disable-model-invocation`, `icon`, `color`, `metadata`. CLI: skills picked from the `/` menu (https://cursor.com/docs/cli/using).
- Instruction files: the CLI "reads AGENTS.md and CLAUDE.md at the project root (if present) and applies them as rules alongside .cursor/rules" (https://cursor.com/docs/cli/using).
- Cursor CLI latest version and date: not verified.

Verified 2026-10-18.

---

## 6. Agent Skills open specification and installers

- Spec: https://agentskills.io/specification ; repo https://github.com/agentskills/agentskills ; validator `skills-ref validate ./my-skill`. Verified 2026-10-18. The format was originally developed by Anthropic and released as an open standard.
- Directory: `SKILL.md` required; optional `scripts/`, `references/`, `assets/`.
- Frontmatter fields (spec): `name` (required, 1-64 chars, lowercase letters/digits/hyphens, no leading/trailing/double hyphen, must match parent directory), `description` (required, 1-1024 chars), `license` (optional), `compatibility` (optional, 1-500 chars), `metadata` (optional string-to-string map), `allowed-tools` (optional, space separated, marked experimental).
- Progressive disclosure: metadata (about 100 tokens) at startup, body (under 5,000 tokens recommended, under 500 lines) on activation, resources on demand.
- Hooks are NOT part of the spec. Claude Code's `hooks`, `context`, `paths`, `disable-model-invocation` etc. are extensions; vercel-labs/skills states "`context: fork` is Claude Code-only; hooks are supported by Claude Code, Cline, and Kiro CLI".
- Harnesses that state they implement it (agentskills.io home page client showcase, fetched 2026-10-18; a partial list): Claude Code, Claude, ChatGPT and Codex, Gemini CLI, OpenCode, Cursor, GitHub Copilot, VS Code, Amp, Goose, Junie, OpenHands, Roo Code, Kiro, Factory, pi, Letta, Mistral Vibe, Tabnine, Hermes Agent, OpenClaw and many more. Copilot CLI, Codex, Gemini CLI, OpenCode and Cursor were each also confirmed on their own docs above.
- Installer: `npx skills` from https://github.com/vercel-labs/skills (MIT; npm package `skills` 1.7.2, modified 2026-10-09). Commands: `add`, `use`, `list`/`ls`, `find`, `remove`/`rm`, `update`, `init`. `npx skills add <source>` accepts GitHub shorthand (`owner/repo`), GitHub/GitLab/Azure URLs, any git URL, local path, or a direct URL to a SKILL.md or archive. Flags: `-g/--global`, `-a/--agent`, `-s/--skill`, `-l/--list`, `--copy` (default is symlink), `-y`, `--all`, `--full-depth`. Anonymous telemetry on by default (`DISABLE_TELEMETRY=1` or `DO_NOT_TRACK=1` to disable).
- Install paths per agent (from its README): `claude-code` `.claude/skills/` and `~/.claude/skills/`; `cursor` `.agents/skills/` and `~/.cursor/skills/`; `opencode` `.agents/skills/` and `~/.config/opencode/skills/`; `codex` `.agents/skills/` and `~/.agents/skills/`; `gemini-cli` `.agents/skills/` and `~/.gemini/skills/`; `github-copilot` `.agents/skills/` and `~/.copilot/skills/`. Many agents share `.agents/skills/` as the project path, Claude Code is the notable exception (`.claude/skills/`). Because the installer symlinks by default, one canonical copy can serve several agents.
- Other installers seen: Gemini `gemini skills install`, Copilot `copilot skill add`, Codex `$skill-installer` and plugins.

---

## Implications for a CLI-plus-skill product

These are conclusions drawn only from the verified facts above. "Piggyback" means the Lorvik CLI appends a "changes since your last call" block to the stdout of every command the agent runs, which needs no harness support.

### Universal baseline

1. Piggybacking on CLI output is the only mechanism present on every harness, because every harness feeds a shell command's stdout back to the model as the tool result. It works wherever a SKILL.md or AGENTS.md can tell the agent to call `lorvik`. It only fires when the agent calls Lorvik, so staleness between calls is invisible to the agent.
2. Hooks are not portable: event names, config files and JSON shapes differ (Claude Code and Codex use PascalCase and `hookSpecificOutput.additionalContext`; Gemini uses `BeforeTool`/`AfterTool`/`BeforeAgent`; Cursor uses camelCase and snake_case `additional_context`; Copilot accepts both casings; OpenCode uses in-process JS plugins). A Lorvik hook adapter has to be written per harness, but all command-hook harnesses share a stdin-JSON-in, stdout-JSON-out model, so a single `lorvik hook --harness <name>` subcommand can translate.
3. No harness lets a hook push text into an idle session except via limited mechanisms (see below). The agent loop is turn based; the earliest injection points are the next user prompt, the next tool result, or the end of the current turn.
4. SKILL.md is the portable carrier for instructions. The spec fields (`name`, `description`, `license`, `compatibility`, `metadata`, `allowed-tools`) are accepted everywhere; Claude Code-only fields (`hooks`, `context`, `paths`) should be kept optional because outside Claude Code surfaces (claude.ai, Skills API) unknown fields cause a hard error. Skill `name` must equal the directory name and match `^[a-z0-9]+(-[a-z0-9]+)*$` (OpenCode and the spec enforce this).
5. Install once at `.agents/skills/` (project) or `~/.agents/skills/` (user): Codex, Gemini CLI, Copilot CLI, Cursor and OpenCode read it; Claude Code does not (it reads `.claude/skills/`; OpenCode and Copilot also read `.claude/skills/`). `npx skills add` handles the per-agent paths and symlinks.

### Per harness: most reliable delivery

| Harness | Most reliable path | Why (facts) |
|---|---|---|
| Claude Code | Hook injection: `SessionStart` + `UserPromptSubmit` plain stdout or `additionalContext`, with `PostToolUse` `additionalContext` for mid-turn updates; piggyback as fallback | Plain stdout is injected only for `UserPromptSubmit`, `SessionStart` (and two others); tool events need JSON `additionalContext` (exit-0 stdout from `PostToolUse` goes to the debug log). `Stop` with `additionalContext` or `decision: "block"` can keep the turn going when something changed (8-continuation cap, check `stop_hook_active`). The hook can ship inside the SKILL.md `hooks` frontmatter or a plugin. 10,000 character cap, so send a diff summary not a dump. |
| Codex CLI | Hook injection through `UserPromptSubmit`/`SessionStart` plain stdout and `additionalContext` for `PostToolUse`; piggyback fallback. Do not rely on `notify` | Hooks are on by default, `additionalContext` is added as developer context, project hooks need trust review and `.codex/` layer trust. `Stop` requires JSON and can continue the turn. Legacy `notify` is argv-only, one event, fire-and-forget, and ignored in project config, so it cannot feed the model. The doc says hooks are "not a complete enforcement boundary", fine for advisory context. |
| OpenCode | A plugin (JS) using `tool.execute.after` to append to `output.output` and/or `chat.message` parts; piggyback is the safe default | No stdin hooks exist; everything is in-process TypeScript, so Lorvik must ship an npm or local plugin that shells out to `lorvik`. System-prompt injection exists only as `experimental.chat.system.transform`. Hook typing came from the published d.ts, not from docs prose, and a V2 plugin API is in development on `dev`, so API stability is a risk. `session.idle` is an event only; no documented way to inject from it. |
| Gemini CLI | Hook injection: `BeforeAgent` `additionalContext` (per turn), `SessionStart`, `AfterTool` `additionalContext`; `AfterAgent` deny to force one more turn | Documented context injection on exactly these three events. Hooks live in `settings.json`; extensions can bundle hooks (lowest precedence). Project hooks are fingerprint-trusted. Timeout is in milliseconds (default 60000), unlike Claude Code and Codex which use seconds. |
| GitHub Copilot CLI | Hook injection: `sessionStart` and `postToolUse` `additionalContext`; `notification` hook can wake an idle session; do not use `userPromptSubmitted` | Command/HTTP `userPromptSubmitted` output is dropped (only SDK hooks may modify prompts), so there is no per-prompt injection from a script. Copilot reads `.claude/settings.json` hooks and accepts PascalCase events, so a Claude Code hook config may be reused as is (documented in the hooks reference; behavior parity not tested). `agentStop` `decision: "block"` forces another turn. |
| Cursor CLI | Piggyback on CLI output | Hook docs show `sessionStart`/`postToolUse` `additional_context`, but CLI coverage is not verified and a recent Linux report says no hooks fire. `beforeSubmitPrompt` output only carries `continue` and `user_message`. Rely on AGENTS.md and skill text plus CLI output; treat hooks as a bonus on the IDE. |

### The "blocking wait command" option

- A `lorvik wait` / `lorvik watch` command that blocks until something changes is just a long-running shell tool call from the harness's point of view. It works on every harness only within that harness's shell tool timeout. Verified timeouts: Claude Code hook timeouts do not bound it (the Bash tool has its own timeout, not verified here), Codex and others were not checked. So: not verified as a general solution; it also ties up the agent turn while waiting, and the agent can only act on the result when the call returns.
- The closest verified push-like primitives are harness specific: Claude Code `asyncRewake` hook (background command wakes Claude on exit 2; idle behavior not verified) and `FileChanged` + `watchPaths`; Copilot `notification` hook ("can wake an idle session"). They are not portable.

### Recommended layering for Lorvik (conclusions from the above)

1. Always: CLI output piggyback ("changes since your last call", with a cursor token), plus SKILL.md and AGENTS.md instructions telling the agent to run `lorvik` at the start of work and before reporting done.
2. Where a verified injecting hook exists (Claude Code, Codex, Gemini CLI, Copilot CLI): ship an optional hook config calling `lorvik hook` on session start and on the user-prompt/before-agent event, and on post-tool where a field exists. Keep payloads well under each cap (Claude Code 10,000 characters, Codex about 2,500 tokens, Copilot `postToolUse` 10 KB).
3. OpenCode: ship an optional plugin only after deciding whether to depend on the unversioned experimental hooks; otherwise rely on 1.
4. Cursor CLI: rely on 1 only until hook coverage in the CLI is confirmed.
5. Dedup: because hooks and piggyback can both fire, key deltas by a per-session cursor stored by the CLI, so the same change is not injected twice.

## Items not verified (collected)

- Claude Code AGENTS.md support; tail of the hooks page (SessionEnd detail, background-hooks section, hooks-in-skills section); `asyncRewake` behavior on an idle session; Bash tool timeout.
- Codex: official page for `notify` payload; hooks introduction version (0.144 claim is from a secondary source); skills installer default path.
- OpenCode: whether `sst/opencode` redirects to `anomalyco/opencode`; `session.created` context injection; V2 plugin API release status; documented behavior of experimental hooks.
- Cursor CLI: version/date and which hook events actually fire in the CLI.
- Copilot CLI: exact semantics when both Claude-format and native hook files exist.
