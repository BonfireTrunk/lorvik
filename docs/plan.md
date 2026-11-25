# Lorvik: project plan

Status: draft, 2026-10-18. Owner: venkata-kishore. This document is the working plan for the first four phases. All open questions are resolved as of 2026-10-18; section 10 records the latest.

## 1. What Lorvik is

A task tracker that AI agents and humans share. Agents pull work, claim it, finish it, create new work they discover, ask humans questions on the board, record decisions, and leave comments for whoever picks up next. Humans see all of it in a dashboard and answer questions from a queue. Access for agents is a CLI plus a SKILL.md. No MCP.

Four nouns: cards, decisions, notes, events. Actors (users and agents) on everything.

Design principles:

- Agents are the primary user. Every CLI response is read by a model; context is the scarce resource.
- The server is the source of truth. Multiple agents on multiple machines coordinate through it.
- The open-source core is complete for one team. The SaaS sells hosting, not features.
- Agent-agnostic: Claude Code, Codex, OpenCode, Gemini CLI, Copilot CLI, Cursor. Anything that can run a shell command and read a skill.

## 2. Decisions

| Topic | Decision | Notes |
|---|---|---|
| Language | Go for CLI and server | Research in `docs/research/`. Conventions in `AGENTS.md` |
| Frontend | Vue 3, TypeScript, Tailwind CSS v4 | Fixed by owner |
| Repo and module path | `github.com/BonfireTrunk/lorvik` | Moved from the personal account to the BonfireTrunk org on 2026-11-09; the org release workflow needs the module path in GitHub's case |
| Agent access | CLI plus SKILL.md only | No MCP. Owner's call |
| Data | Server plus Postgres, never local-only | Multi-machine is the norm |
| Multi-tenancy | In use from day one: org, project, card. A server holds any number of orgs and any signed-in person may create one | Owner's direction 2026-11-12 (#349, #350) |
| Auth in v1 | API tokens for agents and CLI, GitHub OAuth for the dashboard | Owner's call |
| Card model | Parent and child cards allowed; skill teaches agents the discipline | Owner's call. No status roll-up in v1 |
| Knowledge | Decisions are first-class. Notes at org level (cross-project) and project level. Comments attach to cards. In-repo `docs/` stays the home for code documentation | Owner's call 2026-10-18. Not a wiki |
| Binaries | `lorvik` (CLI) and `lorvikd` (server with embedded UI) | See 3.1 |
| License | AGPL-3.0 for server, Apache-2.0 for CLI and skill, the BonfireTrunk Contributor Agreement v1 for all contributions | Licenses decided 2026-10-18; the org's agreement replaced Lorvik's own CLA on 2026-11-09. Lawyer to confirm the agreement text. See 2.1 |
| Self-host DB | External Postgres (docker compose provided), no SQLite, no embedded Postgres | See 3.5 |
| Ids and schema | Snowflake bigint ids, smallint enums, no foreign key constraints, keyset paging everywhere, no cached names | Decided 2026-10-19 to 2026-10-21. See 3.3 |
| Roles | Five hardcoded roles (viewer, member, reviewer, admin, owner) with a permission table in Go; agents capped at reviewer; project-level roles | Decided 2026-10-20. Custom roles deferred |
| Cursors | Client-held, like Redis SCAN: the server stores no read position | Decided 2026-10-21. See 3.3 |
| Card messages | One name, "comment", across table, API, CLI, events and dashboard; no separate "handoff" concept. The practice stays: before releasing a card, leave a comment as "Done: ... Left: ... Gotcha: ..." | Decided 2026-10-22 (question #17) |

### 2.1 License

`cmd/lorvikd`, `internal/server`, `internal/store` and domain packages under AGPL-3.0. `cmd/lorvik`, `internal/cli`, `internal/client`, `internal/apitypes` and `skills/` under Apache-2.0. Per-directory LICENSE files plus a root LICENSE that explains the split.

Contributor agreement: the [BonfireTrunk Contributor Agreement v1](https://github.com/BonfireTrunk/.github/blob/cla-v1/CLA.md), shared by every public BonfireTrunk repository. It grants the maintainer a perpetual, sublicensable copyright and patent license to each contribution, with the right to license it on any terms. A contributor accepts it by ticking its box in the pull request description; the `cla` check (`.github/workflows/cla.yml`, calling the org's `cla-check.yml`) stays red until the box is ticked. This keeps the option to relicense or dual-license future versions. Already released versions stay AGPL and may be forked; relicensing never reaches backwards. Have a lawyer review the agreement text before the first external contribution.

## 3. Architecture

### 3.1 Two binaries

- `lorvik`: the CLI. HTTP client only. No database code linked in. Small download for agents, installs via `curl | sh`, Homebrew cask, deb, rpm, or `go install`.
- `lorvikd`: the server. API, migrations, embedded Vue UI by default, `-tags noui` for the SaaS API image. Self-hosters run one binary plus Postgres.

One binary with a `serve` subcommand was considered. Two keeps the agent-facing CLI free of pgx and the UI, and keeps the SaaS API image free of CLI code.

### 3.2 Deployment modes

- Self-host: `lorvikd serve` serves UI at `/` and API at `/api/v1/`. Same origin. Docker compose with Postgres provided in `deploy/compose/`.
- SaaS: Vue app on a static host or CDN, `lorvikd` built with `-tags noui` behind a load balancer, cross-origin with CORS and bearer tokens. Same code, different env.

The frontend reads its API base URL from config, never assumes same origin. The API never assumes it serves the UI.

### 3.3 Data model (v3, 2026-11-02)

Implemented in the single migration `internal/store/migrations/00001_init.sql`, which is edited in place until the first production deployment; this section follows it. Conventions: snowflake bigint ids, smallint enums, no foreign keys (the service writes related rows in one transaction), no column defaults (every value comes from the application), settings as one jsonb document read whole. `org_id` appears only on tables queried by org without going through a parent.

- Ids: bigint snowflakes (1 zero bit, 41 bits of milliseconds since 2026-01-01, 6-bit node, 16-bit counter seeded at random), unique across tables. On the wire they are decimal strings. Cards also carry a per-project number (`#42`).
- Enums: smallint, negative for terminal states, 0 for the initial or pending state, positive for alive states. Card status: dropped -20, done -10, backlog 0, ready 5, waiting 7, working 10. Record status shared by orgs, users, agents, memberships, tokens, projects and notes: deleted/archived/revoked/removed -10, suspended -5, pending 0, rotated 5, active 10.
- Actors: a user or an agent is named by a `*_kind` (1 user, 2 agent) and `*_id` pair; names are never stored. Every kind is checked to be 1 or 2, and an optional pair is null or set as a whole. Columns that can only name a user are plain `*_user_id` columns with no kind: `agents.created_by_user_id`, `memberships.granted_by_user_id`, `project_members.granted_by_user_id`.
- `orgs`: id, name, status, settings jsonb (agent approval auto or admin, token ttl default and max, project visibility default). No slug: URLs use the id.
- `users`: id, login, name, email, avatar_url, locale, timezone, status. Users exist outside orgs. `identities`: one row per way to log in (GitHub, password for development and self-hosting; Google, Microsoft and passkeys reserved) with the provider's permanent id, unique per provider, and for passwords an argon2id hash.
- `memberships`: (org_id, actor) key, role, status, granted_by_user_id (null for the bootstrap owner and for a request made by signing in with GitHub). Users and agents are both principals; an agent with a membership is org-wide. `project_members`: the same shape per project, roles capped at reviewer.
- Roles are hardcoded in `domain/perm.go`: viewer 10, member 20, reviewer 30, admin 40, owner 50, with a permission table (read; card write, question ask, decision propose, note write, comment write; card triage, question answer, decision accept; agent, project and member manage; org manage). Agents hold at most reviewer. In an org-visible project the effective role is the higher of org and project role; a private project needs a project role, except for admins and owners, who see every project. In the global project the org role applies, and an actor with only project roles reads it as a viewer.
- `agents`: id, org_id, name (unique among the org's pending and active agents; a revoked or removed agent frees its name), harness, machine, created_by_user_id, status. Members may register agents; an org setting makes them pending until an admin approves. Revoking an agent revokes its tokens and clears its assignment on open cards; it can be restored. Removing one is for good.
- `tokens`: id, org_id, actor, name, prefix, SHA3-384 hash (unique), scope (read or write), status, expires_at, replaced_by, last_used_at. Default life 30 days, org-configurable from an hour up to a year. A token used inside its last 7 days is replaced: the successor comes back in `X-Lorvik-Token`, the old one works for 24 hours more, and any later use of it revokes it and records `token.reuse_detected`. Tokens living under 14 days never rotate. Rotation, revocation and reuse handling lock the token row.
- `projects`: id, org_id, slug (unique per org), name, description, visibility (org or private), concurrency (parallel or serial), scope (project, or global: exactly one global project per org, created with it, holding org-wide cards, decisions and notes), lease_seconds, next_card_number, created_by, status.
- `cards`: id, org_id, project_id, number (unique per project), parent_id, kind (task, question, story, decision), status, drop_reason (set iff dropped), priority, created_by, assignee, question fields (choices, answer, answered_by, answered_at; for a decision, who accepted it and when), closed_at, closing_note, fold fields (folded_by, folded_at, folded_into), branch, lane, version, title, body. Decisions are cards of kind decision. Indexes: the board order `(project_id, status, priority DESC, number)` for lists and `next`; one per sorted list order (`created_at`, `updated_at`, `priority`, each then `number`); the unfolded safety net; full-text search on title and body. An assignee must be an active actor with a role reaching the project; when an actor is revoked or loses that role, the assignment is cleared on their open cards.
- `claims`: card_id key, org_id, project_id, actor, session, expires_at. A claim lives apart from the card so renewals never rewrite it. Any authenticated request renews the caller's claims that are past half their lease. The sweeper settles each lapsed claim in one transaction: it locks the claim, checks the lease again, deletes it, returns a working card to `ready` and emits `card.claim_expired` with the former claimant as actor.
- `locks`: named locks on shared resources, unique per org name, with holder, optional card, session, purpose and expiry. An expired row stays until the sweeper removes it or the next acquire takes it over.
- `card_links`: (from, to, kind) key, kinds blocks, relates, duplicates, supersedes, applies. Two cards link within a project, or across projects only through the global project. A question blocks a card by link; the card goes to `waiting` and returns to `ready` when its last open blocker closes. Events about a link between a global card and another project's card belong to that other project.
- `comments`: append-only messages on a card; the id carries the time. `card_reads`: per actor and card, the snowflake minted when the actor last wrote on or opened it, so a newer comment is an unseen reply.
- `card_revisions` and `note_revisions`: the title and body an edit replaced, keyed by (parent, version). Two edits from the same version: the second loses with a 409 version conflict. Revision lists carry no bodies; one revision is read with its body.
- `notes`: id, org_id, project_id (org-wide notes live in the global project), status, version, slug (unique per project), title, body, created_by, updated_by. Notes resolve by slug within a project or by id from anywhere.
- `refs`: resolved `[[card:…]]` and `[[note:…]]` references as (source type, source id, target type, target id). Only targets that exist in the source's org, as the type named, are stored, so backlinks never dangle and survive renames.
- `events`: (org_id, id) key, project_id (null for org-level events), xid8 of the writing transaction, type, actor, actor_session, subject (type, id, number), payload jsonb. No stored names or summaries: clients render the sentence from type and payload and resolve actor ids through `GET /actors`. Readers page in (xid, id) order and only below the oldest transaction still open, so an event that commits late is never left behind a cursor. Each event's position, its (xid, id) as a fixed-width string, is what clients store and compare, as strings (#184). `pg_notify` wakes streams. Retention is a sweeper job with a warning marker at a million rows per org.
- No cursors table: every list is keyset-paged with an opaque client-held cursor (`next`, `prev`, `remaining` capped at 100), and the inbox cursor is the client's own event position. The server stores no read position. Comment and revision histories take an optional `before` and `limit`.

Concurrency: every card and note update sends the `version` it read. Mismatch returns 409 with the current version.

Terminal statuses: `done` and `dropped`. `dropped` requires a reason: `obsolete`, `duplicate` or `wontfix` for any card, `rejected` (a proposed decision) or `revoked` (an accepted one) for decisions; `superseded` is set only by the server when a successor decision is accepted. `duplicate` requires the surviving card and records a `duplicates` link.

Parent and child: only stories are parents and stories have no parent, so the tree is one level deep.

### 3.4 API

REST, JSON, versioned at `/api/v1`. Bearer token auth. Code-first OpenAPI 3.1 via huma on the stdlib mux, which also generates the TypeScript client for the dashboard. Problem+JSON errors. Server-sent events at `/api/v1/events/stream?after=<event position>` for the dashboard and for the CLI's blocking wait. Route table and semantics: `docs/api.md`.

Resources: orgs, projects, cards, decisions, notes, events, tokens, agents, me. Full list in the spec once huma emits it.

### 3.5 Postgres only

No SQLite, no embedded Postgres. Supporting two engines doubles every query and migration (see logchef and memos in the exemplar research). Self-hosters get a compose file. Postgres 15 or newer; the compose files, CI and local development run Percona Distribution for PostgreSQL 18 (a drop-in build of community Postgres), so that is the tested version.

### 3.6 Notifications to agents

A running agent cannot receive a push. Delivery is layered over the event log and the client-held cursor:

1. Piggyback: every CLI response includes a compact "since your last call" header, capped at a few lines. Works on every harness with zero setup. Baseline.
2. Harness hooks: `lorvik inbox` at session start and after tool calls where the harness supports context injection. Verified support as of 2026-10-18 (details in `docs/research/agent-harness-hooks.md`):
   - Claude Code: SessionStart stdout is injected; PostToolUse and Stop need JSON `additionalContext`.
   - Codex: hooks on by default, SessionStart, PostToolUse, Stop, UserPromptSubmit with `additionalContext`.
   - Gemini CLI: SessionStart, BeforeAgent, AfterTool with `additionalContext`.
   - Copilot CLI: sessionStart, postToolUse, notification with `additionalContext`; also reads Claude-style hook config.
   - OpenCode: no shell hooks; in-process TypeScript plugin with `session.created`, `tool.execute.after`. A small plugin shipped later, or rely on layer 1.
   - Cursor CLI: hooks documented but CLI coverage unverified. Rely on layer 1.
3. Blocking wait: `lorvik wait --card 42 --timeout 10m` over SSE, for agents that are genuinely blocked. Phase 3; the SSE endpoint exists in phase 1, the command does not.
4. Webhooks: fire on events to external URLs. Used to notify humans (Slack, email) and to start new agent runs when a question is answered. Phase 3.

The skill ships per-harness hook snippets as an optional upgrade; layer 1 is the contract.

## 4. CLI surface

Global: `--json`, `-p/--project`, `-Q/--quiet`, `-L/--limit` on lists, `LORVIK_TOKEN`, `LORVIK_API_URL`, `LORVIK_PROJECT`, `.lorvik.toml` in repo root for the project binding, non-TTY detection, `NO_COLOR`. Exit codes: 0 ok, 1 error, 2 usage, 3 not found, 4 conflict, 5 unauthorized. The implemented surface is `lorvik --help`; this table is the design intent.

```
lorvik login <user> --password   password login; stores the token and server URL in the user config
lorvik init [--create --name --visibility]  guided on a terminal, flags only for agents; writes .lorvik.toml binding this repo to a project
lorvik status                    project summary: counts per status, open questions, my cards
lorvik inbox [--from now|oldest|<id>] [--peek]   events by others since my cursor, then advances it
lorvik ls [--status s] [--mine] [--open] [--cursor c]   10 cards, with remaining count and next-page command
lorvik next                      one ready card by priority, not claimed, never a question
lorvik show <n>                  card with links, children, blockers, decisions, latest comment, recent events
lorvik add <title> [-b body] [--parent n] [--priority p] [--ready] [--kind question --choice ...]
lorvik claim <n>                 lease; fails if claimed by another actor
lorvik heartbeat <n>             renew lease
lorvik start|wait|done <n> [-m note]   done claims first if the card is unclaimed; block is an alias of wait
lorvik ready|reopen|release <n>
lorvik drop <n> --reason obsolete|duplicate|wontfix [--dup m] [-m note]
lorvik edit <n> [--title] [-b] [--priority] [--parent] [--assignee]
lorvik link|unlink <n> blocks|relates|duplicates <m>
lorvik ask <title> [--choice a --choice b] [--blocks n]   creates a question card
lorvik answer <n> <text> [--decide]       --decide promotes the answer into a decision (users only)
lorvik wait [--card n] [--timeout d]     phase 3, not built
lorvik decisions ls|show|search|accept|reject|revoke|edit|link|unlink
lorvik decide <title> -m <decision> [--context] [--consequences] [--card n] [--supersedes d] [--org]
lorvik comment <n> -m <note>             comment on a card, read by the next claimant
lorvik note ls|show|new|edit|history|archive|search [--org]
lorvik search <query>                    cards, decisions and notes at once
lorvik actors <id>...                    resolve actor ids to names
lorvik project ls|show|create|edit|archive|member
lorvik member ls|add|role|remove         org members (admin)
lorvik agent register|ls|show|approve|revoke|restore|remove|bind|unbind
lorvik token ls|create|revoke
lorvik org show|set
lorvik me | ping | version
```

Rules: stdout is data, stderr is diagnostics, exit codes are stable, errors name the fixing command, every list is capped, `--json` field sets are tested with golden files.

## 5. Skill (v1 outline)

`skills/lorvik/SKILL.md`, Agent Skills frontmatter only (name, description, license, compatibility, metadata). No Claude-only fields in the shared skill; a Claude-specific wrapper can add hooks.

Sections: when to pull work; claim before working, heartbeat on long tasks; when to create cards (discovered work, follow-ups, bugs); parent and child discipline; when to ask a question on the board instead of guessing or asking in the terminal; what to do when blocked; finishing a card (comment, links); searching decisions before any consequential choice and recording one after a question is answered; when to write an org-level note versus a project note; reading the inbox; context economy rules (prefer `next` over `ls`, prefer `decisions search` and `notes search` over `show`).

Install: `npx skills add BonfireTrunk/lorvik` once published; the installer symlinks into each harness's skill directory. Note: Claude Code does not read `.agents/skills`, so the Claude path is `.claude/skills`.

## 6. Repository layout

```
go.mod  Taskfile.yml  .goreleaser.yaml  Dockerfile  AGENTS.md  LICENSE  LICENSE-APACHE  README.md
cmd/lorvik/            CLI main
cmd/lorvikd/           server main
internal/cli/          cobra commands by noun; cmdutil for Factory, IOStreams, JSON exporter
internal/client/       Go API client used by the CLI
internal/apitypes/     wire DTOs, JSON tags, no logic
internal/server/       huma API, middleware, SSE, static UI, shutdown
internal/domain/       card, decision, note, project, actor, event, refs; pure Go, no I/O
internal/store/        Postgres: migrations/ (goose SQL), queries/ (sqlc), sqlcgen/
internal/lorvikd/      server commands: serve, migrate, admin bootstrap
internal/config/ logging/ build/
web/                   Vue app; embed.go, noui.go, dist/.gitkeep
skills/lorvik/         SKILL.md and references
docs/                  plan, ADRs, research
deploy/compose/        self-host compose
scripts/install.sh
.github/workflows/     ci.yml, release.yml
```

## 7. Phases

### Phase 0: bootstrap (1 to 2 sessions)

- License files in place, the contributor agreement check in `.github/workflows/cla.yml`, `AGENTS.md` with exact commands and layout rules pointing at the Go skill.
- `go mod init github.com/BonfireTrunk/lorvik`, `go 1.27.0` line set deliberately.
- Taskfile with `generate`, `build`, `test`, `lint`, `dev`, `web:build`. golangci-lint v2 config. `go tool` pins for sqlc and goose.
- Vue 3 + TS + Tailwind v4 scaffold in `web/` with `dist/.gitkeep`; embed shim compiles with and without `noui`.
- `lorvikd serve` returns a health check. `lorvik version` prints a stamped version.
- CI: vet, lint, test with race, generated-code drift check, build matrix.
- First commit and push.

Exit: fresh clone builds both binaries with no Node installed.

### Phase 1: core, ready for our own use (the bulk of the work)

- Schema and goose migrations for section 3.3. sqlc queries. Store with tx helper and domain error translation.
- Domain logic: card state machine, claims and leases with expiry sweeper, optimistic versioning, question cards and blocking links, events on every change, actor cursors.
- Decisions: CRUD, supersede chain, promotion from an answered question, links to cards, list and search.
- Notes: org-level and project-level pages, comments on cards, revisions, link extraction, Postgres full-text search returning sections.
- API via huma: all resources, token auth middleware, SSE events stream, problem+json errors.
- Token issuance: `lorvikd admin bootstrap` creates the first org and user token; `lorvik agent register` mints agent tokens.
- CLI: every command in section 4 except `login`. `--json` everywhere with golden tests.
- Skill v1.
- Run `lorvikd` plus Postgres locally via `deploy/compose/` on the owner's machine and start tracking Lorvik's remaining work in Lorvik, using Claude Code and at least one other harness.

Exit: a week of real use by the owner's agents without reaching for another tracker.

### Phase 2: dashboard and GitHub login (v1 complete)

- GitHub OAuth for humans, session cookies, CSRF protection via `http.CrossOriginProtection`, `lorvik login` device-code flow.
- Vue dashboard: board by status, card detail with events and children, questions inbox, decisions list with detail and supersede chain, notes browser with backlinks, live updates over SSE, agent activity view.
- TypeScript client generated from the OpenAPI spec.
- Release pipeline: GoReleaser, `install.sh`, Homebrew cask, deb and rpm, Docker image, compose file. Tagged v0.1.0.

Exit: a second person can self-host from the README and log in.

### Phase 3: agent experience

- Per-harness hook snippets in the skill, verified against the research.
- Webhooks on events with HMAC signatures. Slack and email notifications for open questions.
- Optional OpenCode plugin.
- Search quality pass across decisions and notes; pgvector only if full-text proves insufficient.
- `lorvik notes export` to mirror project notes into a repo `docs/lorvik/` folder if users want them in git.
- Agent runner example: a webhook target that starts a headless agent when a question is answered.

### Phase 4: SaaS

- Limits on creating orgs and on invitations.
- Cloud-only modules behind interfaces wired in `cmd/lorvikd` and, if they must stay private, in a separate private module that imports this one.
- Billing, rate limits, usage metering, backups.
- Hosting target decided then. Not now.

## 8. Working method

- Agents build Lorvik. `AGENTS.md` is short and routes to `docs/` and the Go skill. Conventions that can be linted are linted.
- ADRs in `docs/adr/` for decisions that were argued: binary split, license, Postgres only, huma, parent cards, decisions as a first-class entity, notes instead of a wiki.
- Unit tests infra-free by default. Store tests against real Postgres behind an `integration` build tag, run in CI.
- Every CLI `--json` output and every `--help` text is golden-tested so the agent contract cannot drift silently.
- Generated code (sqlc, OpenAPI, TS client) committed, regenerated by `task generate`, drift-checked in CI.

## 9. Risks

- Lease and version semantics are the hardest correctness surface. Model them in Go unit tests before writing SQL.
- Context economy is a product feature. Measure bytes per command in tests and fail on regressions.
- GitHub OAuth in v1 adds session handling early. Keep it isolated in `internal/server/auth` so phase 1 agents never touch it.
- Harness hook APIs are moving. Treat snippets as versioned per harness release.
- License choice is irreversible once outside contributions arrive. Decide before the first public push.

## 10. Open questions for the owner

None at this time. Resolved 2026-10-18: lease 30 minutes, renewed implicitly by CLI calls; `dropped` with a required reason; decisions carry context, decision and consequences.
