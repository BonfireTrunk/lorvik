# Lorvik: instructions for coding agents

Read this first. It is short on purpose; details live in the linked files.

## What this is

A task tracker shared by humans and AI agents. Go server (`lorvikd`) on Postgres, Go CLI (`lorvik`), Vue 3 dashboard in `web/`, agent skill in `skills/lorvik/`. Plan and decisions: `docs/plan.md`. Licensing: `LICENSING.md`.

## Commands

```bash
task --list                 # everything below and more
task build                  # bin/lorvik and bin/lorvikd
task test                   # unit tests, race detector, no infrastructure needed
task lint                   # gofmt, go vet, golangci-lint
task check                  # the full gate: lint, tests, drift
task db:up                  # local Postgres on :5432 (user/pass/db: lorvik)
task test:integration       # store tests, needs LORVIK_TEST_DATABASE_URL
task web:build              # dashboard into web/dist (needs bun)
task dev                    # lorvikd with live reload, UI proxied to Vite
go test -race ./internal/cli/ -run TestPing   # one test
```

A fresh clone builds with no Node installed: `web/dist/.gitkeep` is committed and `//go:embed all:dist` matches it. Build the UI only when you need it.

## Layout rules

- Go code follows the `writing-go-code` skill. Go 1.27; the `go` line in `go.mod` is the floor for features.
- `cmd/*/main.go` only wires and exits. Logic lives in `internal/`. No `pkg/`, no `util`, no `common`.
- `internal/apitypes` is wire types only, imported by both `internal/client` and `internal/server`. Server and store packages must never be imported by the CLI side.
- Dependencies point inward: `server` and `store` depend on domain packages, never the reverse.
- `internal/query` holds the list and search value types (filters, pages, cursors, sorts, hits). `server` builds them and `store` consumes them; neither imports the other, and `internal/lorvikd` adapts `store` to `server.Store`.
- Interfaces are declared by the consumer. Loggers, config and DB handles are injected; no package globals.
- Every CLI command supports `--json` with a stable field set. Stdout is data, stderr is diagnostics. One exception, the owner's choice (#372): the cards a chat holds follow every command's output on stdout (`holding  #N ...` lines; a `holding` field in `--json`), see `docs/requirements/board.md`.
- Generated code (sqlc, OpenAPI) is committed. Run `task generate`; `task check` fails on drift.
- Database schema: until the owner declares a production deployment, `internal/store/migrations/00001_init.sql` is edited in place and databases are dropped and recreated (`DROP SCHEMA public CASCADE; CREATE SCHEMA public;` then `lorvikd migrate up`). Do not add a second migration file before then. Columns carry no `DEFAULT`: every value comes from the application.

## Tests

- Unit tests by default: infra-free, beside the code, table-driven, `t.Parallel()`. Every test pins a behaviour that can regress. No passthrough or does-not-panic tests.
- Store tests against real Postgres are behind `//go:build integration` and read `LORVIK_TEST_DATABASE_URL`.
- Never call `t.Fatal` from a non-test goroutine.

## Before you finish

Per card, the fast checks:

```bash
gofmt -l . && go vet ./... && go fix ./... && go test -race ./... && go mod tidy
```

Once per story, in its verify task, the full gate: `task check`, `task test:integration` against a fresh `lorvik_test` database, and `task web:build` when `web/` changed. The verify task is the only card that uses the shared database and the web build; say so in a comment on it while it runs.

Report failures with their output. Do not claim success for steps you skipped.

## Branches and pull requests

`prime` is the main branch. Every change goes on a feature branch off `prime` and reaches it through a pull request, squash-merged, so the PR title becomes the commit title on `prime`. Contributors follow the BonfireTrunk org's `CONTRIBUTING.md` and tick the contributor agreement box (see `LICENSING.md`).

## Licensing of new files

Server-side code is AGPL-3.0-only. `cmd/lorvik`, `internal/cli`, `internal/client`, `internal/apitypes` and `skills/` are Apache-2.0. Do not move code across that line without a decision.
