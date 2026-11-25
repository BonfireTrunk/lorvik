# Optional hooks

Hooks are an upgrade, not a requirement. Without them, the CLI prints a one-line stderr notice (`N unread, run lorvik inbox`) on every command except `inbox`, unless `-Q` is given, which works on every harness. Hooks add inbox text at session start and after tool use, and keep your claims renewed during long silent work (`lorvik inbox` renews all of this chat's claims).

Source: docs/research/agent-harness-hooks.md, verified 2026-10-18. Harness hook APIs move; each snippet names the version it was checked against. Snippets were not run against a live Lorvik binary.

## Wrapper (Claude Code PostToolUse, Codex, Gemini, Copilot)

Tool-event hooks inject text only through JSON. Save as `~/.local/bin/lorvik-hook-context`, `chmod +x`. Requires `jq`.

```sh
#!/bin/sh
# usage: lorvik-hook-context <hookEventName> <limit>
cat >/dev/null                      # discard the hook's stdin JSON
out=$(lorvik inbox --limit "${2:-3}" --quiet 2>/dev/null) || exit 0
[ -n "$out" ] || exit 0             # nothing new: inject nothing
jq -n --arg e "$1" --arg c "$out" \
  '{hookSpecificOutput:{hookEventName:$e,additionalContext:$c}}'
```

Keep output small. Caps: Claude Code 10,000 characters, Codex about 2,500 tokens, Copilot postToolUse 10 KB.

## Claude Code (verified against 2.1.302)

Plain stdout is injected for `SessionStart`. `PostToolUse` stdout goes to the debug log, so it needs the wrapper. Put in `.claude/settings.json` (project) or `~/.claude/settings.json` (all projects). Timeouts are seconds.

```json
{
  "hooks": {
    "SessionStart": [
      { "hooks": [ { "type": "command", "command": "lorvik inbox --limit 5 --quiet", "timeout": 30 } ] }
    ],
    "PostToolUse": [
      { "hooks": [ { "type": "command", "command": "lorvik-hook-context PostToolUse 3", "timeout": 30 } ] }
    ]
  }
}
```

Omitting `matcher` matches all tools. Install the skill at `.claude/skills/lorvik/` (Claude Code does not read `.agents/skills`).

## Claude Code: unread events on every prompt (recommended)

Checked against https://code.claude.com/docs/en/hooks on 2026-10-23. `UserPromptSubmit` fires before Claude processes each prompt (it has no matcher, so every prompt). The hook gets JSON on stdin with `session_id`, `cwd`, `hook_event_name` and more. For `UserPromptSubmit`, `SessionStart` and two other events, plain stdout from a command hook that exits 0 is added as context Claude can see; no JSON wrapper is needed. Exit 2 would block the prompt, so the script always exits 0. The default timeout on `UserPromptSubmit` is 30 seconds (the field is in seconds); the script caps lorvik at 8.

`--peek` shows unread events without advancing the cursor, so the next explicit `lorvik inbox` still marks them read. With `-Q` and nothing new it prints nothing, so an idle prompt adds no context.

Save as `~/.local/bin/lorvik-prompt-hook` and `chmod +x`. It runs lorvik in the session `cwd` only when a `.lorvik.toml` exists there or in an ancestor, and prints nothing (exit 0) when there is no binding, no `lorvik` on PATH, or the server is unreachable or rejects the call. `jq` is optional.

```sh
#!/bin/sh
# Claude Code UserPromptSubmit / SessionStart hook for Lorvik.
# usage: lorvik-prompt-hook inbox|status
# Reads the hook JSON on stdin, runs lorvik in the session cwd when a
# .lorvik.toml exists there or above, prints the result as plain stdout
# (Claude Code adds it as context). Always exits 0, never blocks the prompt.
input=$(cat)
command -v lorvik >/dev/null 2>&1 || exit 0
field() {
  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$input" | jq -r ".$1 // empty" 2>/dev/null
  else
    printf '%s' "$input" | sed -n "s/.*\"$1\"[[:space:]]*:[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p"
  fi
}
dir=$(field cwd)
# The chat's own agent and session label hang on this variable.
[ -n "$CLAUDE_CODE_SESSION_ID" ] || { CLAUDE_CODE_SESSION_ID=$(field session_id); export CLAUDE_CODE_SESSION_ID; }
[ -n "$dir" ] || dir=$PWD
cd "$dir" 2>/dev/null || exit 0
d=$PWD
while [ ! -f "$d/.lorvik.toml" ]; do
  [ "$d" = / ] && exit 0
  d=$(dirname "$d")
done
case "$1" in
  status) timeout 8 lorvik status -Q 2>/dev/null ;;
  *)      timeout 8 lorvik inbox --peek -Q 2>/dev/null ;;
esac
exit 0
```

Put in `~/.claude/settings.json` (all projects) or `.claude/settings.json`. `SessionStart` prints the board summary once per session; its optional matcher filters on `startup`, `resume`, `clear`, `compact`.

```json
{
  "hooks": {
    "UserPromptSubmit": [
      { "hooks": [ { "type": "command", "command": "lorvik-prompt-hook inbox", "timeout": 15 } ] }
    ],
    "SessionStart": [
      { "hooks": [ { "type": "command", "command": "lorvik-prompt-hook status", "timeout": 15 } ] }
    ]
  }
}
```

Test by hand: `echo '{"session_id":"x","cwd":"'"$PWD"'"}' | lorvik-prompt-hook inbox; echo $?`. The hook acts as the chat's own agent, with its session label, through `CLAUDE_CODE_SESSION_ID`, taken from the hook's `session_id` when Claude Code does not set it.

## Codex CLI (verified against 0.163.0)

Hooks are on by default. `SessionStart` plain stdout is injected; `PostToolUse` needs `additionalContext` JSON, so use the wrapper. Put in `<repo>/.codex/hooks.json` or `~/.codex/hooks.json`. Timeouts are seconds. The research confirms the file names, events and the event / matcher group / handlers structure; the exact top-level shape below mirrors Claude Code and is not confirmed.

```json
{
  "hooks": {
    "SessionStart": [
      { "hooks": [ { "type": "command", "command": "lorvik inbox --limit 5 --quiet", "timeout": 30 } ] }
    ],
    "PostToolUse": [
      { "hooks": [ { "type": "command", "command": "lorvik-hook-context PostToolUse 3", "timeout": 30 } ] }
    ]
  }
}
```

Project hooks load only if the project `.codex/` layer is trusted, and non-managed hooks must be reviewed with `/hooks` before they run.

## Gemini CLI (verified against 0.64.0)

Context injection exists only through `hookSpecificOutput.additionalContext` on `SessionStart`, `AfterTool` and `BeforeAgent`. Stdout must be only the final JSON, so use the wrapper for both events. Put in `.gemini/settings.json` or `~/.gemini/settings.json`. Timeouts are milliseconds.

```json
{
  "hooks": {
    "SessionStart": [
      { "hooks": [ { "type": "command", "name": "lorvik-inbox-start", "command": "lorvik-hook-context SessionStart 5", "timeout": 10000 } ] }
    ],
    "AfterTool": [
      { "hooks": [ { "type": "command", "name": "lorvik-inbox-tool", "command": "lorvik-hook-context AfterTool 3", "timeout": 10000 } ] }
    ]
  }
}
```

Project hooks are fingerprinted; a changed name or command asks for trust again. Manage with `/hooks`. Skills path: `.agents/skills/` or `.gemini/skills/`.

## GitHub Copilot CLI (verified against 1.0.98)

Copilot reads Claude-style hook config and accepts PascalCase events, which select the Claude-compatible payload. The research documents this but did not test parity, so check that output arrives. Native camelCase events (`sessionStart`, `postToolUse`) also take `additionalContext`; their exact output JSON shape is not documented in the research, so prefer the PascalCase form below. Put in `.github/hooks/lorvik.json`. `timeoutSec` default is 30.

```json
{
  "version": 1,
  "hooks": {
    "SessionStart": [
      { "type": "command", "bash": "lorvik-hook-context SessionStart 5", "timeoutSec": 30 }
    ],
    "PostToolUse": [
      { "type": "command", "bash": "lorvik-hook-context PostToolUse 3", "timeoutSec": 30 }
    ]
  }
}
```

Do not use `userPromptSubmitted`: command output is dropped there. Skills path: `.agents/skills/` or `.claude/skills/`.

## OpenCode and Cursor

OpenCode (checked 1.19.3): no shell hooks. Injection needs an in-process TypeScript plugin, which Lorvik does not ship in v1. Only the piggyback notice is available.

Cursor CLI: hook coverage in the CLI is not verified and a Linux report says no hooks fire. Only the piggyback notice is available. Do not configure Cursor hooks for this.

## Dedup

The piggyback notice only counts unread events from other actors, never your own. `lorvik inbox` shows them and advances the cursor stored in the user config file (per token; the server keeps no read position), so a hook run clears the notice. A hook that must not consume events can use `lorvik inbox --peek`. With `-Q`/`--quiet` and nothing new, `lorvik inbox` prints nothing at all; without it, it prints `no new events`.
