# Go exemplar projects: how well-regarded open source Go projects are built

Survey date: 2026-10-18. Purpose: feed a future "Go coding skill" for AI coding agents and inform Lorvik (Go monorepo: agent-oriented CLI, HTTP API on Postgres, Vue dashboard, hosted SaaS plus self-hosted single binary).

Method and caveats:
- Data was pulled from the GitHub API (stars, `pushed_at`, repo trees) and `raw.githubusercontent.com` (go.mod, AGENTS.md, Makefile, goreleaser configs, source files). Nothing here is from memory.
- "Pushed" is the GitHub `pushed_at` of the default branch, used as the last-commit proxy. All repos below were pushed to within the last 4 weeks of the survey date, none archived.
- Stars are rounded. Where a detail was not verified in the repo it is marked "not verified".
- Link base: `https://github.com/<repo>/blob/<branch>/<path>`. Branch is given per project.

## Summary table

| Project | Shape | CLI lib | Router | DB layer | Embed UI? | Release tool |
|---|---|---|---|---|---|---|
| cli/cli | CLI only (talks to GitHub API) | cobra | n/a | none (YAML config files) | n/a | goreleaser + signing scripts |
| pocketbase/pocketbase | single binary, embedded admin UI, SQLite | cobra | stdlib ServeMux (own generic router) | modernc sqlite via own dbx, Go migrations | yes, `go:embed all:dist` (dist committed) | goreleaser |
| go-gitea/gitea | big server + CLI, embedded assets | urfave/cli v3 | chi (via `modules/web`) | xorm, versioned Go migrations | yes (bindata/embed, Vite built) | Makefile + CI (no goreleaser) |
| grafana/grafana | server + separate frontend monorepo | custom (urfave/cli v2 for grafana-cli) | in-house macaron-derived `pkg/web` | xorm + in-house migrator | no, served from `public/` on disk | Makefile + `packaging/` (details not verified) |
| mattermost/mattermost | server + webapp + mmctl CLI | cobra | gorilla/mux | sqlx + squirrel, golang-migrate/morph SQL files | no, webapp served from disk | Makefile (`build/release.mk`) |
| caddyserver/caddy | modular server, JSON config | cobra | own handler chain (chi in go.mod, not router core) | none (pluggable storage) | n/a | goreleaser + cosign + SBOM |
| tailscale/tailscale | daemon + CLI + many tools | peterbourgon/ff ffcli | stdlib `net/http` (tsweb) | none (state files) | web client built separately | custom (`cmd/dist`, `release/`) |
| charmbracelet/bubbletea, huh, lipgloss | TUI libraries | n/a | n/a | n/a | n/a | goreleaser (shared meta config) |
| charmbracelet/gum | CLI of subcommands | kong | n/a | n/a | n/a | goreleaser (includes shared config) |
| charmbracelet/glow | CLI + TUI | cobra + viper | n/a | n/a | n/a | goreleaser |
| temporalio/cli | CLI over gRPC server | cobra (YAML-generated) | n/a | n/a | n/a | goreleaser + `install.sh` + Docker |
| goreleaser/goreleaser | CLI tool | cobra + charmbracelet/fang | n/a | n/a | n/a | goreleaser (releases itself) |
| coder/coder | server + CLI + React site, Postgres | coder/serpent (cobra-like) | chi | sqlc + lib/pq (forked), golang-migrate | yes, build tag `embed` ("fat" vs "slim") | Makefile + scripts |
| woodpecker-ci/woodpecker | server + agent + CLI + Vue 3 web | urfave/cli v3 | gin | xorm + xormigrate | yes, `go:embed all:dist/*` | own CI pipelines (`.woodpecker/binaries.yaml`) |
| knadh/listmonk | single binary, Vue admin, Postgres | pflag + koanf | echo | sqlx + goyesql (.sql files), Go upgrade migrations | yes, stuffbin appended to binary | goreleaser + `make pack-bin` |
| usememos/memos | single binary, React, SQLite/MySQL/Postgres | cobra + viper | stdlib ServeMux + connect-rpc + grpc-gateway | hand-written store, driver per DB | yes, `go:embed dist/*` | not verified |
| pocket-id/pocket-id | single binary, Svelte, SQLite/Postgres | cobra | gin | GORM + golang-migrate | yes, build tags included/excluded | goreleaser |
| cloudreve/Cloudreve | server, React frontend (git submodule) | cobra | gin | ent | frontend as submodule `assets` | goreleaser |
| riverqueue/river (+ riverui) | library on Postgres, UI as Go handler | n/a | stdlib (riverui) | sqlc + pgx v5 | riverui embeds Vite build | not verified |
| mr-karan/logchef | server + Vue 3 + Rust CLI, SQLite or Postgres | n/a (server), Rust CLI | gofiber v3 | sqlc (pgx/v5 and database/sql), golang-migrate | yes, `go:embed all:ui` + `.placeholder` | goreleaser + Justfile |

Added projects beyond the brief: coder/coder, woodpecker-ci/woodpecker, knadh/listmonk, usememos/memos, pocket-id/pocket-id, cloudreve/Cloudreve, riverqueue/river, mr-karan/logchef, vikunja (go-vikunja/vikunja, noted briefly).

---

## 1. cli/cli (GitHub CLI)

- Repo: https://github.com/cli/cli (branch `trunk`). Stars ~46.7k. Pushed 2026-10-16. `go 1.27.0`, toolchain go1.27.2.
- Layout: `cmd/gh` (thin main), `internal/` (ghcmd, config, keyring, authflow, gh, prompter, tableprinter, telemetry, ...), `pkg/cmd/<group>/<verb>/` (one package per command), `pkg/cmdutil`, `pkg/iostreams`, `pkg/extensions`, `pkg/httpmock`, `acceptance/`, `docs/`, `script/`, `.goreleaser.yml`, `AGENTS.md`, `skills/`.
- CLI framework: cobra. HTTP router: n/a. DB: none; config is YAML files (`config.yml`, `hosts.yml`) via `internal/config`.
- Config: `internal/config/config.go` with migration of config schema in `internal/config/migration/`. Env var overrides (GH_TOKEN, GH_REPO, GH_HOST) are documented in help topics.
- Logging: no log library; output goes through `IOStreams` (stdout/stderr, TTY detection, colour).
- Tests: stdlib `testing` + testify + go-cmp. HTTP faked with `pkg/httpmock`. Live end-to-end "acceptance" tests use `rogpeppe/go-internal/testscript` (txtar) behind `//go:build acceptance`.
- Frontend: none. Releases: goreleaser (`.goreleaser.yml`), per-OS builds, signing scripts, draft release, then manual publish after Windows MSI upload. Version injected with `-X .../internal/build.Version`.

Patterns worth copying:
1. Options/Factory/run-function per command, with injectable `runF` for tests: https://github.com/cli/cli/blob/trunk/pkg/cmd/issue/list/list.go and the rules in https://github.com/cli/cli/blob/trunk/docs/command-development.md. Dependencies come from one `cmdutil.Factory` wired in https://github.com/cli/cli/blob/trunk/pkg/cmd/factory/default.go.
2. Uniform machine output: `--json fields`, `--jq`, `--template` added by one helper, with a clear error if `--jq` is used without `--json`: https://github.com/cli/cli/blob/trunk/pkg/cmdutil/json_flags.go. Field lists are tested with https://github.com/cli/cli/blob/trunk/pkg/jsonfieldstest/jsonfieldstest.go.
3. Token storage behind a tiny wrapper with timeouts over zalando/go-keyring, falling back to a config file: https://github.com/cli/cli/blob/trunk/internal/keyring/keyring.go; login flow in https://github.com/cli/cli/blob/trunk/pkg/cmd/auth/login/login.go. Extension model = external executables named `gh-<name>` dispatched by https://github.com/cli/cli/blob/trunk/pkg/cmd/extension/manager.go.
4. Agent-facing repo docs: https://github.com/cli/cli/blob/trunk/AGENTS.md routes agents to per-topic guides in `docs/` (command development, testing, API and hosts, project layout) with a "read before changing" table.

Do NOT copy: the very large flat `pkg/cmd` tree with ~40 top-level groups and per-command `http.go` files. It suits a product with hundreds of subcommands; Lorvik's CLI will be small and agent-driven, so a flatter `internal/cli/<noun>.go` is cheaper to navigate. Also skip the multi-account config migration machinery until a real second schema exists.

## 2. pocketbase/pocketbase

- Repo: https://github.com/pocketbase/pocketbase (branch `master`). Stars ~61.5k. Pushed 2026-10-14. `go 1.27`.
- Layout: root `pocketbase.go` (app bootstrap), `apis/` (HTTP handlers), `core/` (app, models, db, migrations runner), `cmd/` (serve, superuser), `forms/`, `mails/`, `migrations/` (system migrations), `plugins/` (jsvm, migratecmd, ghupdate), `tools/` (router, hook, search, security, ... small stdlib-style packages), `ui/` (Svelte admin; contains committed `dist/`), `examples/base` (the main package that is built).
- CLI: cobra (`RootCmd` in `pocketbase.go`). Router: stdlib `http.ServeMux` wrapped by generic router in https://github.com/pocketbase/pocketbase/blob/master/tools/router/router.go. DB: `modernc.org/sqlite` (CGO-free) with the author's `dbx`. go.mod has only a handful of direct deps.
- Config: flags plus a `pb_data` directory; settings stored in the DB. Logging: DB-backed logger in `tools/logger` and `core/log_*.go` (slog usage not verified).
- Tests: stdlib `testing`, with a shared harness in `tests/` for API scenarios. Lint: golangci (`golangci.yml`).
- Frontend: Svelte admin UI built to `ui/dist`, which IS COMMITTED, then embedded: https://github.com/pocketbase/pocketbase/blob/master/ui/embed.go (`//go:embed all:dist`, build tag `!no_ui`). Served at `/_/` by https://github.com/pocketbase/pocketbase/blob/master/apis/serve.go.
- Releases: goreleaser, CGO disabled, wide OS/arch matrix: https://github.com/pocketbase/pocketbase/blob/master/.goreleaser.yaml; workflow `.github/workflows/release.yaml`.

Patterns worth copying:
1. Embed the built UI and commit a build (or at least a placeholder) so `go build` and `go install` always work: https://github.com/pocketbase/pocketbase/blob/master/ui/embed.go. A `no_ui` build tag lets people strip it.
2. Hook/event system instead of interfaces and DI everywhere: https://github.com/pocketbase/pocketbase/blob/master/tools/hook/hook.go, consumed in `core/app.go`. Extensibility without a plugin ABI.
3. Go-function migrations registered via `init()` in a list, with a runner and a CLI generator: https://github.com/pocketbase/pocketbase/blob/master/core/migrations_list.go, https://github.com/pocketbase/pocketbase/blob/master/core/migrations_runner.go, https://github.com/pocketbase/pocketbase/blob/master/plugins/migratecmd/migratecmd.go.
4. Small `tools/*` packages each doing one thing, no `util` dumping ground.

Do NOT copy: a reinvented query builder (`dbx`), generic router and hook framework. Lorvik is Postgres-first and should use sqlc plus stdlib; owning a query layer is a maintenance cost the PocketBase author accepts as a solo-maintainer trade. Also do not commit large generated `ui/dist` bundles into a team repo; use a `.placeholder` instead (see logchef).

## 3. go-gitea/gitea

- Repo: https://github.com/go-gitea/gitea (branch `main`). Stars ~58.5k. Pushed 2026-10-18. `go 1.27`, toolchain go1.27.2.
- Layout: `cmd/` (CLI subcommands), `routers/{web,api/v1,private,install,common}`, `services/` (business logic, ~50 domains), `models/` (xorm models by domain), `modules/` (framework-ish packages: setting, log, web, assetfs, git, ...), `modelmigration/v1_6 ... v1_27, v28, v29` (versioned DB migrations), `templates/`, `web_src/` (JS/TS/CSS), `public/`, `options/`, `tests/` (integration, e2e), `AGENTS.md`, `docs/guidelines-backend.md`.
- CLI: urfave/cli v3. Router: chi wrapped in https://github.com/go-gitea/gitea/blob/main/modules/web/router.go. DB: xorm (`xorm.io/xorm`) supporting Postgres, MySQL, SQLite, MSSQL.
- Config: INI file loaded in https://github.com/go-gitea/gitea/blob/main/modules/setting/setting.go (`app.ini`). Logging: in-house `modules/log`.
- Tests: testify; integration tests in `tests/integration` against real databases, fixtures in `models/fixtures`; Playwright e2e (`playwright.config.ts`); vitest for TS.
- Frontend: Vite + Tailwind (`vite.config.ts`, `tailwind.config.ts`), templates server-rendered with Go templates; assets embedded via `modules/assetfs/embed.go` and bindata generator `build/generate-bindata.go`. Makefile controls tags (`TAGS`, `bindata`).
- Releases: Makefile targets plus GitHub Actions (`release-nightly.yml`, `release-tag-version.yml`); no goreleaser (verified: no goreleaser file in tree).

Patterns worth copying:
1. A written backend guideline that fixes package dependency direction (models -> modules -> services -> routers): https://github.com/go-gitea/gitea/blob/main/docs/guidelines-backend.md.
2. Versioned migrations as Go functions with an explicit ID, description and a `minDBVersion`: https://github.com/go-gitea/gitea/blob/main/modelmigration/migrations.go.
3. Concise AGENTS.md of hard rules (conventional commits, `Assisted-by:` trailer, error message conventions, test speed budgets): https://github.com/go-gitea/gitea/blob/main/AGENTS.md.

Do NOT copy: server-side Go templates plus a bespoke JS toolchain, xorm, and the INI settings global. Lorvik has a Vue SPA and Postgres only; global mutable settings (`setting.X`) make unit tests order-dependent.

## 4a. grafana/grafana

- Repo: https://github.com/grafana/grafana (branch `main`). Stars ~77.3k. Pushed 2026-10-18. `go 1.27.2`, uses `go.work` with many nested modules.
- Layout: `pkg/` (backend: `api`, `services` (~77 packages), `infra`, `setting`, `web`, `registry`, `apis`/`apiserver` k8s-style), `apps/` (app SDK style modules: dashboard, folder, iam, alerting, ...), `public/` (frontend app), `packages/` (published TS packages: grafana-ui, grafana-data, grafana-runtime, ...), `conf/defaults.ini`, `e2e-playwright/`, `contribute/` (developer docs), `kinds/` and `cue.mod/` (schemas), `AGENTS.md`, `CLAUDE.md`.
- Router: macaron-derived in-house https://github.com/grafana/grafana/tree/main/pkg/web plus k8s-style API server for new APIs. DB: xorm with in-house migrator (`pkg/services/sqlstore/migrations/`). Config: INI, layered `conf/defaults.ini` + custom + env overrides: https://github.com/grafana/grafana/blob/main/conf/defaults.ini. Logging: `pkg/infra/log` (wrapper over go-kit/log).
- Frontend: yarn workspaces + nx, webpack/other; NOT embedded in the Go binary; `public/` is shipped alongside and served from disk.
- Tests: stdlib + testify + testcontainers (in go.mod); Playwright e2e.

Patterns worth copying:
1. Backend contributor docs split by concern (errors, services, package hierarchy, database): https://github.com/grafana/grafana/blob/main/contribute/backend/errors.md, https://github.com/grafana/grafana/blob/main/contribute/backend/package-hierarchy.md, https://github.com/grafana/grafana/blob/main/contribute/backend/services.md.
2. Layered config with a documented defaults file as the single source of truth: https://github.com/grafana/grafana/blob/main/conf/defaults.ini.
3. `go.work` to split independently versioned modules inside one repo (the `apps/` tree).

Do NOT copy: nearly anything about scale. Nx, go.work with dozens of modules, CUE schemas and k8s-style apiserver would bury a young project. Treat Grafana as a documentation exemplar, not a layout exemplar.

## 4b. mattermost/mattermost

- Repo: https://github.com/mattermost/mattermost (branch `master`). Stars ~39.4k. Pushed 2026-10-18. `server/go.mod`: `go 1.26.7` (module `github.com/mattermost/mattermost/server/v8`).
- Layout (top two levels): `server/` (`channels/{api4,app,store,db,jobs,web,wsapi,audit}`, `cmd/{mattermost,mmctl}`, `config/`, `public/`, `platform/`, `enterprise/`), `webapp/` (`channels/`, `platform/`), `api/` (OpenAPI source in `api/v4/source/*.yaml`), `e2e-tests/`, `i18n/`, `AGENTS.md`.
- CLI: cobra; includes `mmctl`, a separate CLI that talks to the HTTP API. Router: gorilla/mux in https://github.com/mattermost/mattermost/blob/master/server/channels/api4/api.go. DB: sqlx + squirrel (mattermost fork) in `store/sqlstore`; migrations are plain SQL (466 files under `server/channels/db/migrations/postgres`) run via golang-migrate/morph. Config: JSON file/DB with viper. Logging: `mlog` (`server/public/shared/mlog`) over logr. Tests: testify; store tests run against real Postgres.
- Frontend: webpack React app served from disk by https://github.com/mattermost/mattermost/blob/master/server/channels/web/static.go, not embedded.

Patterns worth copying:
1. Layering `api4 (HTTP) -> app (business logic) -> store (interface + sqlstore impl)`: https://github.com/mattermost/mattermost/blob/master/server/channels/store/store.go defines the interface, `sqlstore/` implements it.
2. OpenAPI spec as source of truth, one file per resource: https://github.com/mattermost/mattermost/tree/master/api/v4/source.
3. `mmctl` as a thin CLI over the public API (the same shape as Lorvik's CLI): https://github.com/mattermost/mattermost/tree/master/server/cmd/mmctl.

Do NOT copy: the giant `App` god-object and enterprise/open-core split directories (`server/enterprise`, `einterfaces`). Both exist for licensing reasons Lorvik does not have.

## 5a. caddyserver/caddy

- Repo: https://github.com/caddyserver/caddy (branch `master`). Stars ~78.1k. Pushed 2026-10-18. `go 1.26.0`.
- Layout: flat root package `caddy` (caddy.go, admin.go, modules.go, context.go, logging.go, ...), `modules/` (caddyhttp, caddytls, caddypki, logging, metrics, ...), `caddyconfig/` (caddyfile, httpcaddyfile adapters), `cmd/` + `cmd/caddy/main.go`, `caddytest/` (integration harness), `AGENTS.md`.
- CLI: cobra (`cmd/cobra.go`). Router: own handler chain in caddyhttp. DB: none. Config: native JSON, Caddyfile and others via "config adapters": https://github.com/caddyserver/caddy/blob/master/caddyconfig/configadapters.go. Logging: zap, module-scoped loggers from `caddy.Context`.
- Tests: stdlib + testify; `caddytest` runs real servers with config strings: https://github.com/caddyserver/caddy/blob/master/caddytest/caddytest.go. Fuzz tests (`*_fuzz.go`). Lint: strict golangci with gosec, errname, sloglint, modernize: https://github.com/caddyserver/caddy/blob/master/.golangci.yml.
- Releases: goreleaser with cosign signing and SBOMs, nfpm packages: https://github.com/caddyserver/caddy/blob/master/.goreleaser.yml.

Patterns worth copying:
1. Module lifecycle `New -> unmarshal -> Provision -> Validate -> Cleanup` and compile-time interface guards (`var _ caddy.Provisioner = (*X)(nil)`), documented for agents in https://github.com/caddyserver/caddy/blob/master/AGENTS.md. Registration via `init()` and one `modules/standard/imports.go` list: https://github.com/caddyserver/caddy/blob/master/modules/standard/imports.go.
2. One canonical config format (JSON) with adapters in front; the runtime never sees the friendly format.
3. AGENTS.md that states the mission, style rules with tiny code examples, and flags security-critical packages needing extra review.

Do NOT copy: the plugin/module registry with global `RegisterModule` and reflection-based JSON namespace loading. It buys third-party extensibility Lorvik does not need and costs static analyzability.

## 5b. tailscale/tailscale

- Repo: https://github.com/tailscale/tailscale (branch `main`). Stars ~37.7k. Pushed 2026-10-17. `go 1.27.2`, pins its own Go toolchain (`go.toolchain.rev`, `tool/go`).
- Layout: `cmd/` (tailscale, tailscaled, derper, ~45 more), many flat top-level library packages (`ipn`, `net`, `types`, `util`, `wgengine`, `health`, `tsnet`, `tstest`, ...), `feature/` (build-tag-gated features), `licenses/`, `release/`, `.golangci.yml`.
- CLI: peterbourgon/ff ffcli: https://github.com/tailscale/tailscale/blob/main/cmd/tailscale/cli/cli.go. Router: stdlib `net/http` with `tsweb`. DB: none; JSON state files. Config: tailscaled flags + `envknob` debug knobs: https://github.com/tailscale/tailscale/blob/main/envknob/envknob.go. Logging: injected `logger.Logf func(string, ...any)`: https://github.com/tailscale/tailscale/blob/main/types/logger/logger.go. Tests: stdlib + go-cmp (testify present), helpers in `tstest/` and `util/testenv`.
- Releases: custom tooling in `cmd/dist`, `release/`, `build_dist.sh`; not goreleaser.

Patterns worth copying:
1. Tiny, sharp utility packages with doc comments that state usage: https://github.com/tailscale/tailscale/blob/main/util/must/must.go (`must.Get`, `must.Do`) for setup code where errors are programmer errors.
2. Dependency injection of a logging function type rather than a global logger: `types/logger`.
3. CI-enforced repo invariants as Go tests (license headers, import rules, go.mod hygiene): https://github.com/tailscale/tailscale/blob/main/gomod_test.go, https://github.com/tailscale/tailscale/blob/main/license_test.go.

Do NOT copy: vendored toolchain pinning, `tempfork/`, and omit/build-tag feature gating (`feature/`, `omit/`). Valuable for a size-constrained embedded client, overkill for a SaaS API.

## 6. charmbracelet: bubbletea, huh, lipgloss, gum, glow

- bubbletea: https://github.com/charmbracelet/bubbletea (main), ~45.4k stars, pushed 2026-10-08, `go 1.26.0`. Flat root package (`tea.go`, `key.go`, `renderer.go`, `tty_unix.go`/`tty_windows.go`, ...), `examples/`, `tutorials/`. Tests stdlib `testing`, with `t.Parallel()`. Taskfile (`Taskfile.yaml`): `go test -race -count 4 -cpu 1,4 ./...`.
- huh: https://github.com/charmbracelet/huh, ~7.2k, pushed 2026-10-01, `go 1.25.8`. One file per field type (`field_input.go`, `field_select.go`, ...), builder-style API, accessible mode (`form.WithAccessible(true)`, README documents `ACCESSIBLE` env).
- lipgloss: https://github.com/charmbracelet/lipgloss, ~11.9k, pushed 2026-10-11, `go 1.26.7`. Pure functions on immutable `Style`; sub-packages `table/`, `list/`, `tree/`, `compat/`.
- gum: https://github.com/charmbracelet/gum, ~24.5k, pushed 2026-09-24, `go 1.26.7`. CLI framework is kong. Top-level layout is one directory per subcommand (`choose/`, `confirm/`, `filter/`, `input/`, ...), each with `command.go` + `options.go`.
- glow: https://github.com/charmbracelet/glow, ~27.7k, pushed 2026-10-05, `go 1.26.7`. cobra + viper + caarlos0/env + charmbracelet/log; `PersistentPreRunE` validates options.
- Module paths moved to vanity `charm.land/<name>/v2` (visible in gum imports).
- Releases: every repo has a 15-line `.goreleaser.yml` that includes a shared config: https://github.com/charmbracelet/gum/blob/main/.goreleaser.yml pulls `charmbracelet/meta/main/goreleaser-full.yaml` (Homebrew, Scoop, nfpm, etc. defined once).

Patterns worth copying:
1. Options struct IS the command: kong binds `Options` structs with tags, one package per subcommand: https://github.com/charmbracelet/gum/blob/main/gum.go and https://github.com/charmbracelet/gum/blob/main/choose/command.go. Every command doc comment includes a runnable shell example.
2. Version stamping with ldflags falling back to `debug.ReadBuildInfo()` so `go install` still reports a version: https://github.com/charmbracelet/gum/blob/main/main.go.
3. Shared release config included by URL across many repos (single place to change packaging).
4. Non-interactive/accessible fallback paths (huh accessible mode, gum reading stdin) which matter for agent use.

Do NOT copy: TUI-first design. Lorvik's primary user is an AI agent with no TTY; use TUI components only behind `--interactive` and make every command fully flag-driven. Also the unreleased-style dependency pins on `charmbracelet/x/*` pseudo-versions.

## 7a. temporalio/cli

- Repo: https://github.com/temporalio/cli (main). ~388 stars (the CLI is a small repo of a large project). Pushed 2026-10-17. `go 1.26.8`.
- Layout: `cmd/temporal` (main), `cmd/gen-commands`, `cmd/gen-docs`, `internal/temporalcli` (commands, generated code, `commands.yaml`), `internal/commandsgen`, `internal/printer`, `internal/devserver`, `cliext/` (separate Go module, shared client/flag/OAuth config), `install.sh`, `Dockerfile`.
- CLI: cobra, GENERATED from YAML. Server comms: gRPC client SDK. Tests: testify suites against an in-process dev server (`internal/devserver`). Releases: goreleaser (`.goreleaser.yml`, checksums), `install.sh`, Docker via `docker-bake.hcl`.

Patterns worth copying:
1. Command tree defined declaratively in YAML, Go + docs generated from it, with per-command `run` methods hand-written: https://github.com/temporalio/cli/blob/main/internal/temporalcli/commands.yaml, generator https://github.com/temporalio/cli/blob/main/cmd/gen-commands/main.go, output https://github.com/temporalio/cli/blob/main/internal/temporalcli/commands.gen.go, flow in https://github.com/temporalio/cli/blob/main/CONTRIBUTING.md. One source of truth for flags, help text and docs. Strong fit for agent-readable CLI.
2. Single `Printer` that renders the same value as table, JSON or JSONL: https://github.com/temporalio/cli/blob/main/internal/printer/printer.go (`PrintStructured`, `StartList`/`EndList`, broken-pipe handling).
3. Dev server embedded in the CLI for tests and local use (`temporal server start-dev`): `internal/devserver/server.go`. Analogous to Lorvik's self-hosted mode.
4. Style rules for help text enforced in a comment header of `commands.yaml`.

Do NOT copy: a separate nested Go module (`cliext/go.mod`) this early. It adds version-skew chores; use one module until a second consumer exists.

## 7b. goreleaser/goreleaser

- Repo: https://github.com/goreleaser/goreleaser (main), ~16.1k, pushed 2026-10-17, `go 1.27.1`.
- Layout: `main.go`, `cmd/` (build, check, init, release, ...), `internal/` (pipe, builders, artifact, tmpl, golden, testlib, ...), `pkg/` (config, build, archive, context, defaults), `Taskfile.yml`, `www/` (docs site), `THREAT_MODEL.md`, `INCIDENT_RESPONSE.md`.
- CLI: cobra wrapped by charmbracelet/fang for styled help and errors: https://github.com/goreleaser/goreleaser/blob/main/cmd/root.go. Config: YAML with env via caarlos0/env/v11. Tests: testify + golden files (`internal/golden`), strict golangci (`.golangci.yaml` bans `github.com/pkg/errors`, `math/rand`, `ioutil`; enables testifylint, tparallel, thelper, usetesting).
- Releases: releases itself with its own tool: https://github.com/goreleaser/goreleaser/blob/main/.goreleaser.yaml.

Patterns worth copying:
1. Pipeline of small named stages (`internal/pipe/<stage>`), each implementing one interface with `String()`, `Run(ctx)`; easy to test and add: https://github.com/goreleaser/goreleaser/blob/main/internal/pipe/pipe.go.
2. golden-file tests with an `-update` flag: https://github.com/goreleaser/goreleaser/blob/main/internal/golden/golden.go. Ideal for CLI help and JSON output stability.
3. Lint config as policy: banning deprecated imports with `depguard` (see `.golangci.yaml`). `task` as the task runner: https://github.com/goreleaser/goreleaser/blob/main/Taskfile.yml.

Do NOT copy: `www/` docs-site inside the Go repo and the dozens of publisher integrations. Lorvik needs only archives, checksums, Homebrew/Scoop and Docker.

## 8. coder/coder (added: closest large analogue)

- Repo: https://github.com/coder/coder (main). ~17.1k stars. Pushed 2026-10-18. `go 1.26.9`.
- Layout: `cmd/coder` (main), `cli/` (command per file), `coderd/` (API server: ~90 sub-packages, `database/` with `queries/`, `migrations/` (~630 numbered up/down pairs), `dbauthz/`, `dbmock/`, `dbgen/`, `dbtestutil/`, `httpapi/`, `httpmw/`, `rbac/`), `codersdk/` (public Go SDK, used by the CLI), `enterprise/`, `provisioner*/`, `agent/`, `site/` (React/Vite frontend), `scripts/`, `.claude/docs/`, `AGENTS.md`, `CLAUDE.md`.
- CLI: `coder/serpent` (in-house cobra-style). Router: chi (+ cors, httprate). DB: sqlc (`engine: postgresql`) over lib/pq (a coder fork via `replace`; NOT pgx), golang-migrate. Logging: `cdr.dev/slog/v3`. Tests: testify, `go.uber.org/mock` for the DB interface, real-Postgres tests via `dbtestutil`. Errors: `golang.org/x/xerrors`. API docs: swaggo (`.swaggo`, `coderd/apidoc`).
- Frontend: `site/` built into `site/out`; Go embeds it only under build tag `embed`: https://github.com/coder/coder/blob/main/site/site_embed.go. Makefile has `build-fat` (embedded UI) vs `build-slim` (no UI), https://github.com/coder/coder/blob/main/Makefile.
- Releases: Makefile + `scripts/*.sh` + Docker; `install.sh` at repo root.

Patterns worth copying:
1. sqlc workflow: schema dumped from migrated DB to `dump.sql`, queries in `queries/*.sql`, `generate.sh` merges output, CI checks generated files are clean: https://github.com/coder/coder/blob/main/coderd/database/sqlc.yaml, https://github.com/coder/coder/blob/main/coderd/database/generate.sh. Migration helper scripts (`create_migration.sh`, `fix_migration_numbers.sh`) in https://github.com/coder/coder/tree/main/coderd/database/migrations.
2. Authorization as a decorator over the whole DB interface: https://github.com/coder/coder/blob/main/coderd/database/dbauthz/dbauthz.go wraps `database.Store`, so no handler can forget an authz check. Test helpers in https://github.com/coder/coder/blob/main/coderd/database/dbtestutil/db.go and `dbgen/`.
3. `codersdk` as the single typed client used by CLI, tests and frontend codegen: https://github.com/coder/coder/blob/main/codersdk/client.go.
4. Agent-facing docs split by topic under `.claude/docs/` (GO, TESTING, DATABASE, ARCHITECTURE, TROUBLESHOOTING) indexed from AGENTS.md, plus lint scripts that enforce repo structure (`scripts/check_*.sh`, `scripts/rules.go`).

Do NOT copy: custom `serpent` CLI framework, `enterprise/` fork directory, a forked lib/pq, and the sheer size of `coderd/`. For Lorvik use pgx (sqlc supports `sql_package: pgx/v5`) and cobra.

## 9. woodpecker-ci/woodpecker (added)

- Repo: https://github.com/woodpecker-ci/woodpecker (main). ~8.0k stars. Pushed 2026-10-18. `go 1.27.0`.
- Layout: `cmd/{server,agent,cli}`, `server/{api,store,router,web,pipeline,forge,queue,pubsub,rpc,...}`, `agent/`, `cli/`, `pipeline/`, `rpc/`, `woodpecker-go/` (Go client library), `web/` (Vue 3 + Vite + pnpm), `docs/`, `nfpm/`, `.woodpecker/` (CI on itself).
- CLI: urfave/cli v3 (+ cli-docs). Router: gin. DB: xorm + `xormigrate` (38 Go migration files under `server/store/datastore/migration/`), SQLite/Postgres/MySQL. Logging: zerolog. Tests: testify + mockery (`.mockery.yaml`). Mocks regenerated by tooling.
- Frontend: Vue 3 SPA, embedded with `//go:embed all:dist/*` in https://github.com/woodpecker-ci/woodpecker/blob/main/web/web.go, with `web_external.go` as the alternative for serving from disk. Served by https://github.com/woodpecker-ci/woodpecker/blob/main/server/web/web.go.
- Releases: own CI (`.woodpecker/binaries.yaml`, `docker.yaml`) plus `release-config.ts`.

Patterns worth copying:
1. Go + Vue 3 in one repo with the Go package sitting next to the SPA (`web/web.go` embed) and a build-tag escape hatch for dev: https://github.com/woodpecker-ci/woodpecker/blob/main/web/web.go.
2. `woodpecker-go` client package separate from server code, reused by `cli/`: https://github.com/woodpecker-ci/woodpecker/tree/main/woodpecker-go.
3. CI guard against breaking migrations: https://github.com/woodpecker-ci/woodpecker/blob/main/.woodpecker/check-breaking-migrations.sh.

Do NOT copy: server/agent split with gRPC between them and xorm. Lorvik has no distributed build agent.

## 10. knadh/listmonk (added)

- Repo: https://github.com/knadh/listmonk (master). ~23.8k stars. Pushed 2026-10-18. `go 1.26.1`.
- Layout: `cmd/` (main package: handlers per resource, one file each), `internal/` (core, auth, manager, migrations, i18n, ...), `models/`, `queries/*.sql` (one file per resource), `schema.sql`, `static/admin` (Vue), `i18n/`, `config.toml.sample`.
- CLI: spf13/pflag via koanf `posflag` (no cobra). Router: echo. DB: sqlx + `goyesql` (named SQL queries loaded from `.sql` files), Postgres only. Config: TOML file + env + DB-stored settings via koanf. Single binary: stuffbin appends static files, SQL and i18n to the executable.
- Releases: goreleaser with hook `make pack-bin` (post-build) and `make build-frontend` (before): https://github.com/knadh/listmonk/blob/master/.goreleaser.yml, Docker images.

Patterns worth copying:
1. Plain SQL in named files, loaded at startup, no ORM: https://github.com/knadh/listmonk/blob/master/queries/subscribers.sql. The same idea as sqlc without codegen.
2. `install` and `upgrade` as first-class CLI commands; `schema.sql` for fresh installs plus Go upgrade functions per version: https://github.com/knadh/listmonk/blob/master/schema.sql, https://github.com/knadh/listmonk/blob/master/internal/migrations/v0.9.0.go, https://github.com/knadh/listmonk/blob/master/cmd/install.go. Good for self-hosted users who upgrade rarely.
3. Handlers one file per resource under `cmd/`: https://github.com/knadh/listmonk/blob/master/cmd/handlers.go registers routes in one place.

Do NOT copy: everything-in-`cmd/` package with global app struct, and stuffbin (pre-`go:embed` hack). Use `go:embed`.

## 11. usememos/memos (added)

- Repo: https://github.com/usememos/memos (main). ~63.9k stars. Pushed 2026-10-18. `go 1.27.2`.
- Layout: `cmd/memos`, `server/{api,auth,frontend,fileserver,mcp}`, `store/` (domain stores + `store/db/{sqlite,mysql,postgres}` drivers + `store/migration/<driver>/<version>/` + `LATEST.sql`), `proto/` (protobuf APIs, buf), `plugin`-like `provider/`, `web/` (React, Vite, pnpm, biome).
- CLI: cobra + viper. Router: stdlib `http.NewServeMux` plus connect-rpc and grpc-gateway: https://github.com/usememos/memos/blob/main/server/server.go. DB: hand-written SQL behind a driver interface (https://github.com/usememos/memos/blob/main/store/driver.go), testcontainers for MySQL/Postgres tests. Logging: slog (`cmd/memos/log.go`).
- Frontend: `//go:embed dist/*` in https://github.com/usememos/memos/blob/main/server/frontend/frontend.go, SPA fallback served through a mux handler.

Patterns worth copying:
1. Schema-first API with protobuf and buf, generating Go + TS clients, and OpenAPI embedded (`proto/openapi_embed.go`): https://github.com/usememos/memos/blob/main/proto/buf.gen.yaml.
2. Per-version migration folders plus a `LATEST.sql` snapshot for fresh installs.
3. `/healthz` that checks the DB: https://github.com/usememos/memos/blob/main/server/server.go; explicit HTTP server timeouts documented in code comments.

Do NOT copy: supporting SQLite + MySQL + Postgres through one interface. It triples every migration and query. Lorvik should pick Postgres (self-hosted users can run Postgres in Docker or an embedded one; see Section 15 open question).

## 12. pocket-id/pocket-id (added; GORM contrast)

- Repo: https://github.com/pocket-id/pocket-id (main). ~9.6k stars. Pushed 2026-10-16. `backend/go.mod`: `go 1.27.2`.
- Layout: monorepo with `backend/` (Go module: `cmd/main.go`, `internal/{controller,service,model,dto,middleware,bootstrap,cmds,job,httpserver,...}`, `resources/` incl. `migrations/{sqlite,postgres}` ~294 files), `frontend/` (SvelteKit), `docker/`, `tests/` (Playwright), `scripts/`, `.goreleaser.yaml`, `AGENTS.md`.
- CLI: cobra. Router: gin. DB: GORM (+ pgx driver, modernc sqlite) with golang-migrate SQL files per dialect. Config: env vars via `caarlos0/env/v11`. Logging: slog (gin-contrib/slog, otelslog bridge). Tests: testify.
- Frontend embedding with build tags: https://github.com/pocket-id/pocket-id/blob/main/backend/frontend/frontend_included.go vs https://github.com/pocket-id/pocket-id/blob/main/backend/frontend/frontend_excluded.go.

Patterns worth copying:
1. controller -> service -> model layering with explicit DTOs between HTTP and domain: https://github.com/pocket-id/pocket-id/tree/main/backend/internal.
2. Env-struct config: one struct with `env:` tags parsed by caarlos0/env, validated at boot.
3. Embed on/off via build tags (`included`/`excluded`) so backend devs can build without Node.

Do NOT copy: GORM auto-behaviour (implicit hooks, zero-value update gotchas) and dual-dialect SQL migrations. GORM is the "contrast" case: faster to start, harder to keep queries predictable for agents to edit.

## 13. cloudreve/Cloudreve (added; ent contrast)

- Repo: https://github.com/cloudreve/cloudreve (master). ~28.8k stars. Pushed 2026-09-21. `go 1.26.0`, toolchain go1.26.5.
- Layout: `application/`, `cmd/`, `ent/` (generated, 173 files; schemas in `ent/schema/`), `inventory/` (repository layer on top of ent), `middleware/`, `pkg/`, `routers/`, `service/`; frontend lives in a separate repo, mounted as git submodule `assets` (`.gitmodules` -> cloudreve/frontend).
- CLI: cobra. Router: gin. DB: ent v0.13 with auto-migrate (`ent/migrate`), postgres/mysql/sqlite. Tests: testify.

Patterns worth copying:
1. Schema as Go code, generated typed client: https://github.com/cloudreve/Cloudreve/blob/master/ent/schema/file.go and generation hook https://github.com/cloudreve/Cloudreve/blob/master/ent/generate.go. Hide ent behind `inventory/` so handlers never import generated code.

Do NOT copy: committing ~170 generated files that dwarf hand-written code, ent auto-migration for production schema changes (no reviewable SQL), and splitting the frontend into a submodule (breaks atomic commits across API and UI).

## 14. riverqueue/river and riverui, plus mr-karan/logchef (sqlc + pgx in production)

### river
- Repo: https://github.com/riverqueue/river (master). ~5.8k stars. Pushed 2026-10-18. `go 1.26.0`. Multi-module via `go.work`: root client, `riverdriver/` (own go.mod), `riverdriver/riverpgxv5`, `riverdatabasesql`, `riversqlite`, `rivershared`, `cmd/river` CLI.
- DB: sqlc + pgx v5; each driver has `internal/dbsqlc/` with `*.sql` and generated `*.sql.go`: https://github.com/riverqueue/river/tree/master/riverdriver/riverpgxv5/internal/dbsqlc, config https://github.com/riverqueue/river/blob/master/riverdriver/riverpgxv5/internal/dbsqlc/sqlc.yaml. Migrations are embedded SQL up/down files run by the `river` CLI.
- Tests: testify `require`, a shared conformance suite run against every driver: https://github.com/riverqueue/river/blob/master/riverdriver/riverdrivertest/riverdrivertest.go; real Postgres in tests; transaction-per-test isolation helpers.
- AGENTS.md rules (verified): prefer context-rich errors, `require` over `assert`, singular lowercase package names, avoid `common`/`util`.

### riverui
- https://github.com/riverqueue/riverui (master), ~239 stars, `go 1.26.0`. A Go `http.Handler` (`handler.go`) that serves a Vite/React SPA via `embed.go` plus `spa_response_writer.go` for history-mode fallback. Exposes the UI as an embeddable handler as well as a binary: pattern for "host it ourselves or mount it into your app".

### logchef (closest to Lorvik's stack)
- Repo: https://github.com/mr-karan/logchef (main). ~916 stars, AGPL-3.0. Pushed 2026-10-14. `go 1.27.0`.
- Layout: `cmd/server` (main + `web.go` embed), `internal/{server,core,store/{sqlite,postgres},auth,config,datasource,alerts,mcp,provisioning,...}`, `pkg/models`, `frontend/` (Vue 3.5 + Pinia + vue-router + Tailwind 4 + reka-ui + Vite + vitest, bun), `cli/` (Rust CLI), `docs/` (Astro), `deployment/{docker,helm}`, `Justfile`, `sqlc.yaml`, `AGENTS.md`.
- Router: gofiber v3 (with swaggo OpenAPI). DB: sqlc with TWO engines in one `sqlc.yaml`: SQLite (`database/sql`) and Postgres (`pgx/v5`), golang-migrate for migrations. Config: koanf with TOML + env: https://github.com/mr-karan/logchef/blob/main/internal/config/config.go. Auth: OIDC (go-oidc, zitadel/oidc), API tokens, local auth. Also an MCP server (`internal/mcp`).
- Embedding: https://github.com/mr-karan/logchef/blob/main/cmd/server/web.go (`//go:embed all:ui` plus committed `.placeholder` so a fresh checkout compiles without a frontend build). `just build` = `build-ui` then `build-backend` (runs `sqlc-generate` first): https://github.com/mr-karan/logchef/blob/main/Justfile.
- Releases: goreleaser with `before: just build-ui`, ldflags version stamps, buildx Docker images: https://github.com/mr-karan/logchef/blob/main/.goreleaser.yaml.

Patterns worth copying:
1. `.placeholder` + `go:embed all:ui` so Go builds and tests never depend on Node: https://github.com/mr-karan/logchef/blob/main/cmd/server/web.go.
2. sqlc config with pgx/v5 and `emit_interface: true`, one `queries.sql` per store: https://github.com/mr-karan/logchef/blob/main/sqlc.yaml. Store layer behind an interface, `core/` holding business logic with fakes (`fake_datasource_test.go`).
3. Justfile as the only entry point for build, sqlc, UI, CLI.
4. Test seam: river's driver conformance suite (one test suite run against every implementation).

Do NOT copy: supporting two metadata engines (SQLite and Postgres) with parallel query files; it doubles review surface. Also logchef's Rust CLI is a different language from the server; Lorvik wants a Go CLI sharing types with the API.

## 15. go-vikunja/vikunja (brief)

- https://github.com/go-vikunja/vikunja (main). ~5.8k stars. Pushed 2026-10-17. `go 1.27.0`. `pkg/` (models, routes, migration (173 files), config, cron, notifications, ...), `frontend/` (Vue 3) embedded via `frontend/embed.go`, `magefile.go` (Mage as the build tool), echo router, xorm, viper. Has `.agents/`, `.claude/`, `AGENTS.md`, `veans/`. Worth a skim for Vue + Go single binary and Mage tasks; not studied in depth.

---

## Cross-cutting comparison: CLI, DB, config, logging, tests

| Concern | Dominant choice among the best | Notes |
|---|---|---|
| CLI framework | cobra (cli, pocketbase, caddy, temporal, goreleaser, glow, memos, pocket-id, cloudreve) | alternatives: kong (gum), urfave/cli v3 (gitea, woodpecker), ffcli (tailscale) |
| HTTP router | stdlib ServeMux (pocketbase, memos, tailscale) or chi (gitea, coder); gin/echo/fiber in mid-size apps | Go 1.22+ ServeMux patterns removed the main reason for a third-party router |
| DB access | sqlc (coder, river, logchef) or hand SQL (listmonk, memos); ORMs (xorm, GORM, ent) in app-style products | sqlc + pgx v5 is supported via `sql_package: pgx/v5` |
| Migrations | numbered SQL up/down + golang-migrate (coder, mattermost, logchef, pocket-id); Go-function migrations (gitea, pocketbase, woodpecker, listmonk) | |
| Config | env struct (pocket-id, goreleaser), koanf TOML+env (listmonk, logchef), INI (gitea, grafana), JSON (caddy) | |
| Logging | slog (pocketbase, memos, pocket-id); zap (caddy); injected func type (tailscale); slog-style libs (coder) | |
| Tests | stdlib testing + testify (most); go-cmp (cli, tailscale); golden files (goreleaser); testscript (cli acceptance) | |
| UI embedding | `go:embed` of Vite dist (pocketbase, woodpecker, memos, logchef, vikunja, pocket-id), build tags for slim/fat (coder, pocket-id, pocketbase `no_ui`) | |
| Release | goreleaser (most CLI/single-binary projects); Makefile+scripts (coder, gitea, mattermost, tailscale) | |

## Error handling and API conventions observed (verified in repo docs)

| Project | Convention | Source |
|---|---|---|
| cli/cli | Typed sentinel errors drive exit behaviour and printing: `FlagErrorf` (shows usage), `SilentError` (exit 1, no message), `CancelError`, `PendingError`, `NoResultsError`. Scripts rely on stable exit codes and stderr/stdout split. | https://github.com/cli/cli/blob/trunk/docs/command-development.md |
| caddy | Early return, no `else` after error; lowercase error strings without trailing punctuation; do not panic for normal errors; use the context logger. | https://github.com/caddyserver/caddy/blob/master/AGENTS.md |
| gitea | Error prefixes like `fmt.Errorf("Foo: %w", err)` must name the function actually in use; write almost no comments, explain why not what; API v1 changes must document every result code and stay GitHub-compatible. | https://github.com/go-gitea/gitea/blob/main/AGENTS.md, https://github.com/go-gitea/gitea/blob/main/docs/guidelines-backend.md |
| river | Prefer context-rich errors; `require` over `assert`; `Func` suffix not `Fn`; no `common`/`util` packages; singular package names. | https://github.com/riverqueue/river/blob/master/AGENTS.md |
| goreleaser | `github.com/pkg/errors` banned by depguard; error-naming and error-strings via revive. | https://github.com/goreleaser/goreleaser/blob/main/.golangci.yaml |
| coder | `xerrors` wrapping; `coderd/httpapi` package holds HTTP response helpers (contents not read); API docs via swaggo (`.swaggo`). | https://github.com/coder/coder/blob/main/coderd/httpapi/httpapi.go |
| mattermost | OpenAPI YAML per resource is the API contract; handlers live in `api4`. | https://github.com/mattermost/mattermost/tree/master/api/v4/source |
| memos | Protobuf/connect API with embedded OpenAPI; `/healthz` verifies DB. | https://github.com/usememos/memos/blob/main/server/server.go |

Takeaway for a Go coding skill: the repos that are easiest for agents to edit state their rules in a short AGENTS.md with exact commands (lint, single test, generate) and keep conventions enforceable by linters or Go tests, not only prose.

## Build and dev-workflow entry points observed

| Project | Task runner | Frontend step | Codegen step |
|---|---|---|---|
| cli/cli | `Makefile`, `script/build.go` for Windows | none | not verified |
| pocketbase | `Makefile` (lint, test) | `ui/` built, `dist` committed | `plugins/jsvm/internal/types` |
| gitea | `Makefile` (`make help`) | Vite (Makefile `FRONTEND_*` targets) | bindata generator, swagger |
| coder | `Makefile` (`build-fat`, `build-slim`, `gen`) | `site/` pnpm build to `site/out` | sqlc, protoc, swaggo, mockgen |
| listmonk | `Makefile` (`dist`, `build-frontend`, `pack-bin`) | JS package in `static/admin` (package manager not verified) | none |
| woodpecker | `Makefile` | pnpm in `web/` | mockery (`.mockery.yaml`) |
| logchef | `Justfile` (`build`, `build-ui`, `sqlc-generate`) | bun in `frontend/` | sqlc |
| vikunja | `magefile.go` (Mage) | `frontend/` | swag |
| goreleaser, charm libs | `Taskfile` | none | schema, docs |
| temporal cli | `Makefile` | none | `cmd/gen-commands`, `cmd/gen-docs` |

Observation: five different task runners are in use (make, just, task, mage, scripts); all are thin. The constant is that the Go build must succeed with no frontend present (placeholder or build tag) and that `generate` is one command.

## Per-project "what I would NOT copy" in one line (quick reference)

- cli/cli: ~40 top-level command groups and multi-account config migration.
- pocketbase: owning a query builder, router and hook framework; committing big built UI bundles.
- gitea: server-rendered templates plus bespoke JS toolchain; global `setting` package.
- grafana: Nx plus go.work plus CUE plus k8s-style apiserver.
- mattermost: god-object `App`, open-core `enterprise/` split.
- caddy: global module registry with reflection-loaded JSON namespaces.
- tailscale: toolchain pinning, `tempfork/`, build-tag feature omission.
- charm: TUI-first interaction model (agents have no TTY).
- temporal cli: nested Go module for shared CLI code too early.
- goreleaser: docs site and dozens of publishers inside the Go repo.
- coder: custom CLI framework, forked lib/pq, enterprise fork directory.
- woodpecker: server/agent split over gRPC, xorm.
- listmonk: everything in one `cmd/` package with globals; stuffbin.
- memos: three database dialects behind one interface.
- pocket-id: GORM implicit behaviour, dual-dialect migrations.
- cloudreve: committing generated ent code, ent auto-migrate in prod, frontend as submodule.
- river: multi-module go.work for a library (Lorvik is an application).
- logchef: dual SQLite/Postgres metadata stores, separate-language CLI.

## Patterns that recur across the best projects

1. Thin `main`, logic in `internal/` or a library root. `cmd/<bin>/main.go` only wires version, signals and calls into a package (cli: `cmd/gh/main.go` -> `internal/ghcmd`; pocketbase: `examples/base`; coder: `cmd/coder`). Business logic sits in packages importable by tests.
2. One command or handler per file, grouped by noun, with an options struct, a constructor taking dependencies, and a separate run function so tests inject fakes (cli/cli `NewCmdList`/`listRun`; gum `Options`; temporal `run` methods). Dependencies are passed explicitly (cli `Factory`, tailscale `logger.Logf`), not through globals.
3. Embedded UI via `go:embed all:<dir>` with a committed placeholder or build tag so `go build`/`go test` never require Node (logchef `.placeholder`; coder `embed` tag; pocket-id `included/excluded`; pocketbase `no_ui`; woodpecker `web_external.go`). Release builds run the frontend build in a goreleaser `before` hook or Make target first.
4. Machine-readable output is first-class: gh `--json/--jq/--template`; temporal `Printer` with JSON/JSONL; stable field sets tested. Combined with TTY detection and stdout/stderr discipline (gh `IOStreams`, `SilentError`/exit-code helpers).
5. Generated artefacts have one source of truth and are drift-checked in CI: sqlc from `queries/*.sql` (coder, river, logchef); CLI commands and docs from YAML (temporal); OpenAPI/proto (memos, mattermost); `go generate`d mocks. Generated files are committed or regenerated by one `make`/`just` target.
6. Plain-SQL, numbered migrations or version-keyed Go migrations, plus a snapshot for fresh installs (`schema.sql`, `LATEST.sql`, `dump.sql`). Self-hosted upgrade path is an explicit command (`listmonk --upgrade`, `river migrate-up`).
7. Release engineering = goreleaser config with ldflags version stamping, CGO off where possible, checksums, Docker and package-manager publishing; shared or minimal config (Charm's `includes`, pocketbase's 60-line file). Version fallback via `debug.ReadBuildInfo()`.
8. Agent-readable repo docs: `AGENTS.md` that is short, imperative and routes to topic docs (cli/cli `docs/`, coder `.claude/docs/`, gitea `AGENTS.md`, caddy `AGENTS.md`, river `AGENTS.md`). Includes the exact commands to run lint and a single test.
9. Strict-but-small lint policy via golangci-lint v2: explicit enable list, ban deprecated imports (`pkg/errors`, `ioutil`, `math/rand`), `modernize`, `testifylint`, `sloglint`, `errname` (caddy, goreleaser, tailscale).
10. Tests: stdlib `testing` with testify `require`, table tests, `t.Parallel()`, real Postgres for DB code (coder `dbtestutil`, river transaction-per-test, memos testcontainers), a conformance suite shared across implementations (river), golden files for output (goreleaser), testscript for CLI acceptance (cli).

## Closest analogues to Lorvik

1. mr-karan/logchef. Same shape almost exactly: Go server on sqlc + pgx/v5 with Vue 3 + Tailwind + Pinia dashboard embedded via `go:embed all:ui` plus placeholder, koanf TOML/env config, OIDC and API tokens, Justfile, goreleaser, Docker and Helm, AGENTS.md. Study `cmd/server/web.go`, `sqlc.yaml`, `Justfile`, `.goreleaser.yaml`. Differences: its CLI is Rust and it dual-supports SQLite.
2. coder/coder. Closest on "server + CLI that is just a client of the server's SDK + frontend, Postgres, sqlc, self-hosted and hosted". Study `codersdk` (typed client shared by CLI), `coderd/database` (sqlc, migrations scripts, `dbauthz`, `dbtestutil`), `site/site_embed.go` (fat vs slim), and `.claude/docs/`. Scale is far larger; copy the seams, not the volume.
3. cli/cli (for the CLI half) paired with temporalio/cli (for the "CLI over a server API plus embedded dev server plus generated commands" half). gh gives output, auth, and agent-doc conventions; temporal gives declarative command definition and `install.sh`/goreleaser. Secondary: pocketbase (single-binary embed simplicity) and woodpecker (Go + Vue 3 monorepo with a separate Go API client package).

## Suggested defaults for Lorvik (derived from the above; decisions for the owner)

- Single Go module, layout: `cmd/lorvik` (CLI), `cmd/lorvikd` or one binary with `serve` subcommand, `internal/{api,core,store,config,cli}`, `web/` (Vue) with `web/embed.go` + `.placeholder`, `sdk/` or `client/` (typed Go client used by CLI), `queries/` + `migrations/` + `sqlc.yaml`.
- cobra for the CLI; stdlib `ServeMux` (or chi) for HTTP; sqlc + pgx v5; golang-migrate or River-style embedded SQL migrations; slog; env/koanf config; testify `require`; goreleaser; `just` or `make`.
- Open questions for the owner (not decided by this survey): whether self-hosted ships with embedded Postgres or requires an external one (memos/pocketbase avoid it via SQLite; logchef/coder require Postgres); whether to generate the CLI from a declarative spec (temporal) or write cobra by hand (gh).

## Unverified or skipped

- Grafana and tailscale release pipelines and Homebrew formula details were not inspected; mattermost release uses `server/build/release.mk` (not read).
- memos release tooling, vikunja internals, riverui build/release details not read.
- cli/cli distribution channels (Homebrew, apt, winget) not verified; only `.goreleaser.yml` and signing scripts were read.
