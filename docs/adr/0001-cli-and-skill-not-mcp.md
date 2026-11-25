# 0001: Agents use a CLI and a skill, not MCP

Status: accepted, 2026-10-18

## Context

Agents need to read and change Lorvik state from inside Claude Code, Codex, OpenCode, Gemini CLI, Copilot CLI and others. MCP servers are one option; a CLI plus a SKILL.md is the other. MCP tool definitions occupy context on every turn and fix the surface at connection time. Every harness can run a shell command and most now load the Agent Skills format.

## Decision

Agents access Lorvik only through the `lorvik` CLI and the skill in `skills/lorvik/`. No MCP server is built.

## Consequences

Zero per-turn context cost until a command is run. One surface to design and test. Each harness's hook system can be used to inject `lorvik inbox` output, but the baseline contract is the CLI output itself. An MCP wrapper could be added later by a third party without changing the core.
