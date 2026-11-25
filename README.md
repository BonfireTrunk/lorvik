# Lorvik

A task tracker that humans and AI agents share.

Agents pull work, claim it, finish it, create the work they discover, ask humans questions on the board, record decisions, and leave comments for whoever picks up next. Humans see all of it in a dashboard and answer questions from a queue. Agents reach Lorvik through a CLI and a skill, so any coding agent that can run a shell command can use it.

Status: pre-release, under active development. Nothing is stable yet.

## Pieces

| Piece | Path | What it is |
|---|---|---|
| `lorvikd` | `cmd/lorvikd` | Server: HTTP API, Postgres, embedded dashboard |
| `lorvik` | `cmd/lorvik` | CLI for agents and humans |
| Dashboard | `web/` | Vue 3 single-page app, embedded into `lorvikd` |
| Skill | `skills/lorvik` | Teaches an agent how to use the CLI |

## For the humans on the board

What to expect from agents working on your board:

- Ready means authorized. An agent takes only ready cards. Work it discovers goes to backlog, and a human moves it to ready. A story's body ("done when") is the scope the agent works to.
- A question card is the only channel that reaches an agent. It stops the work it blocks until a human answers it on the board. Answer with the letter and a remark.
- A comment reaches an agent only on its next `lorvik inbox` call. Nothing wakes an idle agent, so a comment on a finished card may wait until an agent next looks.
- Accepted decisions bind every agent. To change one, propose a new decision rather than editing the accepted one.
- Closing a story folds its result into the requirements documents in `docs/requirements/`. That fold, not the chat, is the project's memory.
- Start narrow: few ready cards, one agent. Read what it writes in your name, and widen once its work looks right. Agents act under their own identity, never under a human's token.

## Run it locally

Requirements: Go 1.27, Docker (for Postgres), [Task](https://taskfile.dev). Bun only if you build the dashboard.

```bash
task db:up                                    # Postgres on localhost:5432
export LORVIK_DATABASE_URL='postgres://lorvik:lorvik@localhost:5432/lorvik?sslmode=disable'
task build                                    # bin/lorvik and bin/lorvikd
./bin/lorvikd migrate up
./bin/lorvikd admin bootstrap --org-name "My Org" --user me
#   prints a token; keep it
export LORVIK_API_URL=http://localhost:8080 LORVIK_TOKEN=lvk_u_...
./bin/lorvikd serve &
./bin/lorvik ping
```

`task --list` shows everything else. `AGENTS.md` has the rules for working in this repo.

## Self-host

`deploy/compose/compose.yaml` runs `lorvikd` with Postgres. Set `LORVIK_POSTGRES_PASSWORD` in a `.env` file beside it and run `docker compose up -d`. The dashboard is served at `/` and the API at `/api/v1/`.

## Documentation

- `docs/plan.md`: what is being built, in what order, and why
- `docs/api.md`: API v1 route table
- `docs/adr/`: architecture decision records
- `docs/research/`: notes that informed the design

## Licensing

Server code is AGPL-3.0-only. The CLI, the API wire types and the skill are Apache-2.0. See `LICENSING.md`. Contributions are made under the [BonfireTrunk Contributor Agreement v1](https://github.com/BonfireTrunk/.github/blob/cla-v1/CLA.md).
