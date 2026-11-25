# Go project structure research for Lorvik

Date: 2026-10-18. Method: primary sources (go.dev, project READMEs, vendor docs) fetched and read on this date. Facts that the fetched pages did not confirm are marked "unverified". Star counts are snapshots from GitHub pages on the research date.

Current toolchain context: Go 1.27.0 (2026-08-19, latest go1.27.2), Go 1.26 still supported, Go 1.25 out of support (https://go.dev/doc/devel/release). The `tool` directive (Go 1.24) is stable and unchanged in 1.27 (https://go.dev/doc/go1.27 lists no changes to it).

Lorvik context: Go monorepo; CLI used mostly by AI agents; HTTP API + Postgres; Vue/TS dashboard; also a hosted SaaS; self-hosters may get one binary with the UI embedded.

---

## Summary of recommendations

| Decision | Choice | One-line reason |
|---|---|---|
| Module layout | One `go.mod` at repo root, `cmd/` + `internal/`, no `pkg/`, no `go.work` | Matches go.dev server-project guidance; nothing here is meant for external import |
| CLI framework | cobra + pflag, no viper (the `gh` pattern) | Built-in bash/zsh/fish/powershell completions, doc generation, largest adoption; gh proves the JSON-output pattern |
| HTTP router | stdlib `net/http` ServeMux (Go 1.22+ patterns), optionally behind huma v2 via `humago` | Method+wildcard routing is in stdlib; no framework lock-in; huma adds OpenAPI 3.1 |
| DB layer | pgx v5 (`pgxpool`) + sqlc (`sql_package: pgx/v5`) | Type-safe generated code from plain SQL, no ORM, full Postgres feature access |
| Migrations | goose (embedded SQL files) | `embed.FS` support, env config, sqlc parses it natively, single-file up/down |
| Build tool | Taskfile (go-task) for dev; GoReleaser for release; `go tool` for pinned Go tools | Cross-platform YAML, up-to-date checks; no Go-toolchain-in-the-loop like mage |
| Config | env vars for server (go-envconfig), flags > env > small file for CLI | 12-factor; avoid viper's global state and lowercase-key behaviour |
| Container | multi-stage, `CGO_ENABLED=0`, `gcr.io/distroless/static-debian13:nonroot` | ~2 MiB base, no shell, nonroot variant exists |

---

## 1. Official guidance and the pkg/ controversy

### 1.1 go.dev "Organizing a Go module"

Source: https://go.dev/doc/modules/layout

What it says (paraphrased from the page, quotes are exact):

- Layout depends on project type: basic package, basic command, package/command with supporting packages, multiple packages, multiple commands, mixed, or server.
- File and package names are "entirely arbitrary"; `main.go` is "just a convention".
- Use `internal/` early: "this prevents other modules from depending on packages we don't necessarily want to expose and support for external uses... we're free to refactor its API and generally move things around without breaking external users." And: "It's recommended to keep packages in `internal` as much as possible."
- `cmd/`: "A common convention is placing all commands in a repository into a `cmd` directory; while this isn't strictly necessary in a repository that consists only of commands, it's very useful in a mixed repository that has both commands and importable packages."
- Server projects: "Server projects typically won't have packages for export, since a server is usually a self-contained binary... it's recommended to keep the Go packages implementing the server's logic in the `internal` directory. Moreover, since the project is likely to have many other directories with non-Go files, it's a good idea to keep all Go commands together in a `cmd` directory."
- Reference tree for a server:

```
project-root-directory/
  go.mod
  internal/
    auth/  metrics/  model/
  cmd/
    api-server/main.go
    metrics-analyzer/main.go
  ... other non-Go directories
```

- "In case the server repository grows packages that become useful for sharing with other projects, it's best to split these off to separate modules."
- The page never mentions `pkg/`.

### 1.2 golang-standards/project-layout and the criticism

Sources:
- https://github.com/golang-standards/project-layout
- https://github.com/golang-standards/project-layout/issues/117

README facts: it states it is "NOT an official standard defined by the core Go dev team", points to the official layout doc, and warns it is overkill for small or PoC projects (a `main.go` plus `go.mod` is enough). It describes `/cmd` (thin mains), `/internal` (compiler-enforced privacy), `/pkg` ("library code that's ok to use by external applications... think twice before you put something here"; it admits `/pkg` is "not universally accepted"), `/web`, `/api`, `/scripts`.

Issue #117 (Russ Cox, April 2021; closed as completed Aug 2023, repo not renamed or archived):
- Claim of inaccuracy: "the vast majority of packages in the Go ecosystem do not put the importable packages in a `pkg` subdirectory."
- "what is described here is just very complex, and Go repos tend to be much simpler."
- "It is unfortunate that this is being put forth as 'golang-standards' when it really is not." Motivation: developers being told "you are not using the standard Go project layout" with this repo as authority.
- Others (rakyll, flowchartsman) noted projects restructuring to match it, causing contributor conflict.

Why `pkg/` is contested:
1. Empirically not what the ecosystem does; importable packages usually sit at the module root or in descriptively named directories.
2. It adds an import-path segment with no meaning (`mod/pkg/auth` instead of `mod/auth`).
3. It blurs the public/private line: `internal/` is compiler-enforced, `pkg/` is only a signal ("please import me"), so inside an application it creates a second, unenforced tier.
4. The "standard" branding caused churn (projects removing `pkg/` after learning it was not standard).
5. Defenders (MrTravisB in the thread) say it helps big apps with lots of non-Go files at the root; that is a taste argument, not a toolchain one.

Note that `cli/cli` (the GitHub CLI) itself does use `pkg/` heavily for command packages (`pkg/cmd/<cmd>/<sub>`), plus `internal/`; see 4.5. This shows `pkg/` is common in older large CLIs but is a historical layout there, not a recommendation (https://raw.githubusercontent.com/cli/cli/trunk/docs/project-layout.md).

Lorvik takeaway: no `pkg/`. Everything is `internal/` until someone concretely needs to import it (for example a public Go SDK), at which point split it into its own module per the official advice.

---

## 2. Layouts in practice

### 2.1 Size ladder

| Stage | Layout | When |
|---|---|---|
| Tiny | `go.mod`, `main.go`, a few files in the root package | Scripts, prototypes (golang-standards README agrees) |
| Small library | Root package named after the module | Importable lib |
| Small app | Root `main` + `internal/<pkg>` | go.dev "supporting packages" |
| Mixed/server | `cmd/<bin>/main.go` + `internal/...` + non-Go dirs | go.dev "server project" |
| Multi-module | Split only when something must be versioned/imported independently | go.dev server note |

Lorvik sits at "mixed/server" from day one because it has three non-Go areas (web, docs, skills) and multiple binaries.

### 2.2 Domain-oriented vs layer-oriented packages

Source: Ben Johnson, Standard Package Layout, https://www.gobeyond.dev/standard-package-layout/

His four rules: (1) root package holds domain types and interfaces and imports nothing from the app; (2) subpackages grouped by external dependency (`postgres`, `http`) as adapters; (3) a shared `mock` package; (4) `main` wires everything. His critique of layer packages (`controllers`, `models`) and module packages (`users`, `accounts`): they produce stutter (`controller.UserController`, `users.User`) and cyclic dependencies because layers/modules need each other ("only works if you have one-way dependencies").

Practical synthesis for a modern server (this is opinion built on the sources, not an official rule):
- Group by capability/domain noun (`internal/project`, `internal/task`), each owning its types, service logic, and a small store interface it defines.
- Put adapters (HTTP handlers, Postgres store) either inside the domain package (small domains) or in dependency-named packages (`internal/store/postgres`, `internal/server`) per Johnson's rule 2. Pick one per codebase and be consistent.
- Keep wiring in `cmd/` or one `internal/app` package.

### 2.3 Package naming

Source: https://go.dev/blog/package-names (Sameer Ajmani) and https://go.dev/blog/organizing-go-code

- Short, lowercase, no underscores or mixedCaps.
- "Packages named `util`, `common`, or `misc` provide clients with no sense of what the package contains"; they accumulate dependencies and slow compilation. The same reasoning applies to `base`, `helpers`, `shared`, `lib` (the post does not name `base` specifically).
- "Don't use a single package for all your APIs": `api`, `types`, `interfaces` grow without bound.
- Avoid stutter: `http.Server`, not `http.HTTPServer`.
- Do not steal good variable names (`buf`).
- Design test: if no name works as a meaningful prefix for the contents, the package boundary is wrong.
- Granularity: avoid grab-bag packages but do not over-split; `package main` may be large (organizing-go-code post).

### 2.4 Import cycles

Go forbids them, which forces one-way dependency graphs. Techniques (synthesized from the sources above):
- Define interfaces in the consumer (the package that calls), implement them in the provider; wire in `main`.
- Domain types and interfaces sit in a low-level package that imports nothing local.
- If two packages need each other, extract the shared concept into a third package with a real noun name (not `common`).
- Do not solve a cycle by merging into a `types` package; that is the catch-all anti-pattern above.

### 2.5 Shared types between CLI client and server

Goal: the CLI (HTTP client) and server agree on wire types without the CLI importing server internals (DB, handlers).

Options:

| Option | Pros | Cons |
|---|---|---|
| A. Hand-written `internal/apitypes` (wire DTOs only; JSON tags; no logic) imported by both | Simple, one source of truth, trivial refactors, compile-time breakage | Must not become a dumping ground; name per resource, not `types` |
| B. OpenAPI spec as source, generate server stubs + Go client (oapi-codegen / ogen) | Contract-first; SDKs for TS dashboard too | Generated code in repo; spec review burden |
| C. Code-first spec (huma) emits OpenAPI; generate TS client for web from it; CLI imports Go DTOs from A | One definition in Go; spec for dashboard and docs | Couples spec to huma struct tags |

Recommendation: A for Go-to-Go sharing (CLI and server both in `internal/`), plus C if huma is adopted so the Vue dashboard gets a generated TS client. Because the CLI lives in the same module, there is no versioning gap; do not split a separate SDK module until an external consumer exists.

---

## 3. Monorepo, embedding the UI, and release packaging

### 3.1 One go.mod vs go.work

Sources: https://go.dev/doc/tutorial/workspaces, https://go.dev/ref/mod#workspaces

- `go.work` defines a multi-module workspace where all listed modules are main modules; it replaces `replace` directives for local iteration across modules. The tutorial frames it as development scaffolding; releasing still requires tagged versions in `go.mod`.
- The modules reference says it is "generally inadvisable" to commit `go.work`: it can override a developer's own parent `go.work`, and it can make CI test the wrong dependency versions. Exception: modules developed exclusively with each other.
- `go.work` `use` does not recurse; every module needs its own line.

Lorvik: one `go.mod` at the root. The web app is a Node project in `web/`, not a Go module, so `go.work` solves nothing. Revisit only if a separately versioned Go SDK or a private SaaS-only module (see 3.5) is created, and then keep `go.work` untracked (.gitignore) unless the modules are only ever developed together.

### 3.2 Embedding the frontend build

Sources: https://pkg.go.dev/embed; practitioner write-ups found by search (not primary): Shallow Brook "Conditional Embedding in Go" https://shallowbrooksoftware.com/posts/conditional-embedding-in-go/, GoVueKit post https://dev.to/benjy33000/embedding-a-vue-3-app-in-a-go-binary-with-goembed-and-keeping-seo-without-ssr-1p2m, olivere/vite https://github.com/olivere/vite, Tyk AI Studio PR (noui tag) https://github.com/TykTechnologies/ai-studio/pull/650.

Facts from `embed` docs:
- Directive must sit directly above a package-level `string`, `[]byte`, or `embed.FS` var; patterns are relative to the package directory, no `..`, no symlinks, may not cross a nested `go.mod`.
- Naming a directory embeds it recursively but skips names starting with `.` or `_`; the `all:` prefix includes them (`//go:embed all:dist`).
- Every pattern must match at least one file or the build fails (so a missing or empty `dist/` is a compile error everywhere: build, vet, test, lint).
- `embed.FS` implements `io/fs.FS`; use `fs.Sub(fsys, "dist")` to strip the prefix (the embed page itself does not discuss `fs.Sub`; the practitioner posts do).
- Embedded assets increase binary size and RSS.

Pattern for Lorvik:

```go
// web/embed.go
//go:build !noui

package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

const Embedded = true

func FS() (fs.FS, error) { return fs.Sub(distFS, "dist") }
```

```go
// web/noui.go
//go:build noui

package web

import "io/fs"

const Embedded = false

func FS() (fs.FS, error) { return nil, nil }
```

- Commit `web/dist/.gitkeep` (or a placeholder `index.html`) so the default build compiles on a fresh clone. With only `.gitkeep` present, `all:dist` is required for the pattern to match.
- Build tags work at file level, not line level: hence two files with complementary constraints.
- The server checks `web.Embedded`; if false it serves a stub or reverse-proxies the Vite dev server. This is the cleanest answer to "exclude the UI": `go build -tags noui` for the SaaS API-only image or CLI-only binaries.
- SPA history mode: the handler must fall back to `index.html` for unknown non-API, non-asset paths; keep `/api/` reserved.
- Tag direction: embed-by-default (tag `noui` excludes) means self-hosters get the UI with a plain `go build`/GoReleaser run; the inverse (`-tags embed`) is the Shallow Brook recipe. Choose per who builds more often. Lorvik: default embed, `noui` to exclude.
- Vite `build.outDir` must resolve to `web/dist` (the Go package directory) because embed cannot reach `..`; with Vue source in `web/` this is natural if the Go files live in `web/` next to `package.json`. Alternative: Vue source in `web/app/`, Go embed package at `web/` with `dist/` copied in; pick one and wire it in the Taskfile.

### 3.3 Wiring the frontend build

Options observed:

| Mechanism | Notes |
|---|---|
| `//go:generate` in `web/embed.go` calling `npm ci && npm run build` | Possible, but `go build` never runs generate automatically, so CI and release must call it anyway; slow if run on every `go generate ./...` |
| Taskfile/Makefile target `web:build` then `go build` | Explicit; use `sources:`/`generates:` (Task) to skip when unchanged |
| GoReleaser global `before.hooks` | Runs once per release (OSS supports `before` only, https://goreleaser.com/customization/general/hooks/). Build-level `hooks.pre` run once per build target and would rebuild the UI repeatedly (https://goreleaser.com/customization/builds/hooks/) |
| Dockerfile Node stage producing `web/dist` | Standard for images; Node absent from final image |

Recommendation: Task tasks as the canonical entry (`task web:build`, `task build`), GoReleaser `before.hooks: [task web:build]`, Docker Node stage. Use `go generate` only for Go-side code generation (sqlc, oapi-codegen, mocks), via `go tool`.

### 3.4 GoReleaser for multiple binaries, Homebrew, install scripts

Sources: https://goreleaser.com/customization/builds/builders/go/, https://goreleaser.com/customization/homebrew_casks/, https://goreleaser.com/customization/homebrew_formulas/, https://raw.githubusercontent.com/cli/cli/trunk/.goreleaser.yml

- `builds` is a list; each entry has its own `id`, `main`, `binary`, `flags`, `ldflags`, `tags`, `env`, `goos`/`goarch`, and hooks. Alternative: one build with `main: ./...` (since v2.15), but it does not apply the build's `tags`/`flags` to discovery.
- Outputs land in `dist/${BuildID}_${BuildTarget}`; rely on `dist/artifacts.json` instead of directory names. Run `go mod tidy` in a `before` hook to avoid parallel download races.
- `archives[].ids` picks which builds go in each archive (archives page returned 404 on fetch; unverified details beyond this, check https://goreleaser.com/customization/archives/ before writing config).
- `gh` pattern: three build IDs per OS family (macos, linux, windows), all `main: ./cmd/gh`, `ldflags: -s -w -X .../internal/build.Version={{.Version}}`, `CGO_ENABLED=0` on linux, per-OS signing hooks, global `before` hooks generating manpages and completions, nfpm deb/rpm with bash/zsh/fish completions and an extra zsh file for Debian's different fpath, release created as draft.
- Homebrew: `brews` (formulas) is deprecated since GoReleaser v2.10 ("Homebrew Casks should be used instead"); use `homebrew_casks` (since v2.10) with `repository` (tap owner/name/token), `binaries`, `completions`, `manpages`, `generate_completions_from_executable` (v2.15). Casks should be signed and notarized, or a post-install `xattr -dr com.apple.quarantine` is needed (and the docs warn that bypasses Gatekeeper). On GitHub Actions the default token cannot write to another repo; a PAT with content-write on the tap is required.
- Homebrew's own docs: casks can now target Linux too (introduced 4.5.0, Apr 2025; `binary` stanza works on both OSes; https://docs.brew.sh/Cask-Cookbook, https://brew.sh/2026/06/11/homebrew-6.0.0/) but Homebrew's guidance still prefers a formula for open-source CLI tools built from source in homebrew/core. For your own tap this tension does not matter; GoReleaser now generates casks only.
- Install scripts: `goreleaser/godownloader` (generated `install.sh`) was archived 2022-01-14 and is unmaintained, no replacement named (https://github.com/goreleaser/godownloader). `gh` does not ship a curl-pipe script (it uses packages and repos). Recommendation: hand-write a small `install.sh` that reads `checksums.txt` from the GitHub release and verifies it; keep it in repo root or `scripts/`. Also publish deb/rpm via nfpm and `go install ./cmd/lorvik@version` as a fallback.

### 3.5 Open source plus hosted SaaS in one repo

Not covered by a single primary source; design notes grounded in the facts above:
- Behaviour differences (multi-tenancy, billing) should be behind interfaces wired in `cmd/<binary>/main.go`, not behind build tags scattered across packages.
- If SaaS-only code must stay private, put it in a second module in a private repo that imports the open module and provides its own `main`. This is the case where a nested module or `go.work` (untracked) is justified; Go docs advise splitting only when something must be shared.
- Binaries: `cmd/lorvik` (CLI), `cmd/lorvik-server` (API + optional embedded UI). Consider also exposing the server as a subcommand of the CLI for the self-host single binary; trade-off is larger agent-facing binary and slower CLI start-up (not measured).

---

## 4. CLI frameworks

### 4.1 Comparison

| | cobra (+pflag) | urfave/cli v3 | kong | stdlib flag |
|---|---|---|---|---|
| Model | Command tree of structs/funcs, builder-style | Declarative commands/flags | Struct tags define the whole CLI | Flat flags; subcommands by hand with `FlagSet` |
| Stars (snapshot) | cobra.dev claims 173k+ dependent projects (marketing figure) | 24.3k | ~3.2k | n/a |
| Version / status | v1.10.2 (Dec 2025); releases ad hoc, mostly maintenance; 260 open issues | v3 current; volunteer-run (README) | 1.0 reached, "stable for a long time"; 33 open issues | stdlib |
| Shell completion | bash, zsh, fish, powershell built in; dynamic completions | bash, zsh, fish, powershell | Not mentioned in README (unverified; check for a plugin) | None |
| Help | Auto-generated, per command; man pages and Markdown docs via `doc` package | Flexible, templated; build tag `urfave_cli_no_template` to shrink | Auto `--help`, per-command, customizable with `Help()` method / `HelpOptions` | `-h` usage only |
| Env var binding | Via viper (optional) or manual | Built in; files and altsrc modules for JSON/YAML/TOML | `env:"X,Y"` tag, first found wins | Manual |
| JSON output mode | Not built in; you add a flag (see gh pattern) | Same | Same | Same |
| Config files | viper integration | altsrc module | resolvers (JSON built in; YAML/TOML/HCL separate repos) | Manual |
| Dependencies | pflag (+ optional viper) | stdlib only | few | none |

Sources: https://cobra.dev/, https://github.com/spf13/cobra/releases, https://github.com/urfave/cli, https://github.com/alecthomas/kong. Note that cobra.dev says its docs generation makes CLIs "LLM-friendly" (marketing wording).

### 4.2 What the GitHub CLI uses

`cli/cli` go.mod (https://raw.githubusercontent.com/cli/cli/trunk/go.mod): `spf13/cobra v1.10.2` and `spf13/pflag v1.0.10` direct; Viper absent; Charm libraries (`huh`, `bubbletea`, `lipgloss`) for prompts and TUI; `cpuguy83/go-md2man` for man pages; Go 1.27.0. Repo has top-level `cmd/`, `internal/`, `pkg/`, `api/`, `git/`, `docs/`, `script/`, `acceptance/`, `skills/`, and an `AGENTS.md`.

Patterns worth copying (https://raw.githubusercontent.com/cli/cli/trunk/docs/command-development.md, project-layout.md):
- One package per command: `NewCmdX(f *cmdutil.Factory, runF func(*Options) error) *cobra.Command`; `RunE` parses flags into an `Options` struct, calls `runF` when injected (test seam), otherwise the `xRun(opts)` function.
- `Options` carries dependencies (IOStreams, HTTP client, config) injected from a `Factory`; defer expensive lookups into `RunE`.
- JSON: `cmdutil.AddJSONFlags(cmd, &opts.Exporter, fields)` adds `--json fields`, `--jq`, `--template`; run function does `if opts.Exporter != nil { return opts.Exporter.Write(opts.IO, data) }` before any human output. Field names and types stay stable regardless of the terminal rendering.
- Stdout for data, stderr for diagnostics; non-TTY output is script-friendly (no color, no truncation, no headers); prompts need a non-interactive alternative.
- Completions and man pages generated at release time via `make completions`/`make manpages` as GoReleaser `before` hooks.

### 4.3 Viper or not

Sources: https://github.com/spf13/viper, https://github.com/knadh/koanf

Viper: keys are case-insensitive by design, not concurrency-safe (reads and writes can panic), no deep merge of overridden complex values, global singleton discouraged and "may be deprecated", precedence Set > flags > env > config file > KV store > defaults, a Viper v2 feedback process is open, 30.5k stars. gh avoids it. For an agent-first CLI with a few settings, a tiny explicit resolution order (flag > env > file > default) is easier to reason about and test than Viper's implicit merging.

### 4.4 Recommendation for Lorvik

cobra + pflag, no viper. Reasons: completions out of the box (agents and humans both benefit from tab-completion and `__complete` dynamic completion), the gh `Options`/`Factory`/exporter pattern gives a proven structure for `--json`, and it has the largest ecosystem so agent-training data skews cobra (inference, not verified). kong is the strongest alternative for terse declarative CLIs but its README does not document completion generation. urfave/cli v3 is fine but volunteer-run. stdlib `flag` is too thin for nested subcommands.

Agent-oriented rules (synthesized from gh's guidance): every list/get command supports `--json` with stable field names; exit codes are meaningful; errors go to stderr as one structured line when `--json` or non-TTY; no interactive prompts when stdin is not a TTY; never colorize non-TTY; `NO_COLOR` honoured.

---

## 5. HTTP server stack

### 5.1 Router/framework comparison

| | stdlib ServeMux (1.22+) | chi v5 | echo v5 | gin | fiber v3 |
|---|---|---|---|---|---|
| Stars (snapshot) | n/a | 23.0k | 32.8k | 89.4k | 40.2k |
| Handler shape | `func(http.ResponseWriter, *http.Request)` | same | `func(*echo.Context) error` | `func(*gin.Context)` | `fiber.Handler`, fasthttp-based |
| net/http compatible | native | "100% compatible", ~1000 LOC, no deps | built on net/http, wrappers `WrapHandler` | uses net/http server; own context type | No: built on fasthttp, uses `unsafe`, ctx values reused, adapters cost |
| Path params | `{id}`, `{rest...}`, `r.PathValue` | `{id}` + regex, `chi.URLParam` | own | own | own |
| Method routing | `GET /x/{id}` patterns | yes | yes | yes | yes |
| Middleware | `func(http.Handler) http.Handler` (convention) | same, `middleware` package | own signature | own | own |
| Status | Core | Active; `RealIP` deprecated as spoofable, replaced by `ClientIPFrom*` | v5 released 2026-01-18; v4 security fixes until 2026-12-31 | v1.12.0, requires Go 1.26 | v3 requires Go 1.26 |

Sources: https://go.dev/blog/routing-enhancements, https://github.com/go-chi/chi, https://github.com/labstack/echo, https://github.com/gin-gonic/gin, https://github.com/gofiber/fiber.

stdlib routing facts (Go 1.22 blog): `GET /posts/{id}` (GET also matches HEAD); `{name}` single segment, `{name...}` remainder; `{$}` anchors exact match; `Request.PathValue`; most-specific-pattern-wins precedence, registration order irrelevant, conflicts panic at registration; 405 with `Allow` header automatically. The team states third-party routers "remain a fine choice" for advanced needs. Gaps vs chi (inferred, not enumerated by the post): route groups/`Route` sub-routers and `Mount`, built-in middleware set, regex params. Group-level middleware is done by wrapping a sub-`ServeMux` with a function.

### 5.2 Middleware pattern

Plain `func(next http.Handler) http.Handler` composed with a small `Chain` helper. Needed set: request ID, panic recovery, structured access log (slog), timeout/body size limit, CORS (dashboard), auth, and client-IP extraction from trusted proxies only (chi's deprecation of `RealIP` shows the spoofing risk; GHSA advisories cited on chi's README).

### 5.3 Graceful shutdown

Sources: https://pkg.go.dev/os/signal#NotifyContext, https://pkg.go.dev/net/http#Server.Shutdown

- `ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)`; `defer stop()`. While active, listed signals cancel the ctx instead of the default exit; `context.Cause(ctx)` names the signal.
- `ListenAndServe` always returns a non-nil error; after `Shutdown`/`Close` it returns `http.ErrServerClosed`, which is the success case, not a failure.
- Run `srv.ListenAndServe()` in a goroutine, wait on `ctx.Done()`, then `srv.Shutdown(shutdownCtx)` with a bounded timeout. Close DB pool after Shutdown returns. `srv.RegisterOnShutdown` exists for hooking long-lived connection cleanup (semantics beyond signature not read; check the Server docs). Set `BaseContext` so request contexts derive from the root ctx. (BaseContext is from memory of the net/http API; verify before relying on it.)
- Kubernetes/SaaS: keep readiness failing during drain (pattern, not from a fetched source).

### 5.4 Structured logging with slog

Source: https://go.dev/blog/slog

- `slog.Logger` (frontend) + `slog.Handler` (backend); built-in `TextHandler` and `JSONHandler`; `Logger.With` pre-formats attrs; `LogValuer` controls representation and redaction; levels are ints (`Info`=0, `Warn`=4).
- Context: methods with and without a `context.Context`; a handler can pull trace/request IDs from the ctx. The proposal to store a Logger inside a context was dropped as "smuggling in an implicit dependency". So: pass `*slog.Logger` explicitly (constructor injection), and use a custom `Handler` wrapper that reads request ID / tenant ID from ctx when you call `logger.InfoContext(ctx, ...)`.
- Echo v5 also moved to `log/slog` for its request logger.
- Go 1.27 lists no slog changes.

### 5.5 Request-scoped context values

Source: https://pkg.go.dev/context

- `WithValue` is for "request-scoped data that transits processes and APIs, not for passing optional parameters to functions."
- Unexported custom key type per package; expose typed accessors (`FromContext`/`NewContext`).
- Do not store a Context in a struct; pass `ctx` first.
- Lorvik: request ID, authenticated principal, tenant ID in ctx via accessors in one `internal/reqctx` package (named for what it is, not `util`). Everything else (DB handles, loggers, config) is injected through constructors.

### 5.6 OpenAPI generation

| | huma v2 | oapi-codegen v2 | ogen |
|---|---|---|---|
| Direction | Code-first (Go structs to spec) | Spec-first (spec to Go) | Spec-first |
| OpenAPI | 3.1 + JSON Schema | 3.0 and 3.1 (3.1 support added via PR #2336; unions of multiple types become `any`) | v3; 3.1 level not stated in README (unverified) |
| Routers | Adapters: stdlib (`humago`), chi, others | stdlib, chi, echo, gin, fiber, gorilla, iris | Own generated static radix router |
| Validation | Built-in from schema, RFC 9457 problem+json | Not in generated code; use request-validator middleware | Generated from spec |
| Extras | `/openapi.json`, `/docs`, content negotiation, SSE, conditional requests, optional PATCH, `humatest` | `strict-server` mode (typed request/response objects); YAML config; run via `go tool` | No reflection, `Optional[T]`/`Nullable[T]`, sum types, OpenTelemetry, generated client |
| Go version | 1.25+ | tool build 1.25+ | not stated |
| Stars | ~4.5k | 8.6k | ~2.1k |
| Caveats | Struct-tag spec authoring; framework-ish; younger | Verbose generated code; no formal release cadence (pin versions); generated code is the SemVer surface | Opinionated types; smaller community; SSE client only |

Sources: https://github.com/danielgtaylor/huma, https://github.com/oapi-codegen/oapi-codegen, https://github.com/ogen-go/ogen.

Lorvik choice: stdlib `ServeMux` for routing. For OpenAPI, huma v2 with the `humago` adapter is the best fit if you want one Go definition emitting a 3.1 spec (useful to generate the TS client for the Vue app and to give agents a machine-readable contract). If your team prefers a reviewed contract file as the source of truth, use oapi-codegen strict-server mode on stdlib instead. Either way, avoid gin/fiber (own context types and, for fiber, fasthttp) and treat chi as the fallback if you outgrow ServeMux. Decide spec direction early because it dictates where DTOs live (see 2.5).

---

## 6. Postgres access and migrations

### 6.1 Access layer comparison

| | pgx v5 (direct) | sqlc | sqlx | GORM | ent | bun |
|---|---|---|---|---|---|---|
| Style | Hand-written SQL, native driver API | Generates typed Go from your SQL and schema | `database/sql` superset with struct scan, named params | Full ORM | Schema-as-Go + codegen graph API | SQL-first ORM on `database/sql` |
| Type safety | Runtime scan | Compile-time generated | Runtime (tag-based) | Runtime | Compile-time (generated) | Mostly runtime |
| Postgres features | Full: COPY, LISTEN/NOTIFY, batch, ~70 types, binary encoding | Uses pgx v5 via `sql_package: pgx/v5` (supported since sqlc v1.18.0) | Via `database/sql` driver; no COPY/LISTEN through stdlib adapter | Via driver | Via driver | Own `pgdriver` or others |
| Stars (snapshot) | ~14.3k | 18.4k | 17.7k | 40.0k | 17.2k | ~5.0k |
| Status | Go 1.25+, PG 14+ supported window; active | Active (CLAUDE.md in repo, sponsors); 586 open issues | Mature; README newest note 1.3.0; 317 open issues | Active; 457 open issues | Pre-1.0 (roadmap issue #46); stewarded by Atlas team | Active, 25 open issues |
| Migrations | none | none (parses your migration files) | none | AutoMigrate | Atlas | built-in |
| Good for | Dynamic queries, bulk ops | Most CRUD with static SQL | Light helpers on stdlib | Rapid prototyping | Complex graph models | SQL-ish query builder |

Sources: https://github.com/jackc/pgx, https://docs.sqlc.dev/en/latest/guides/using-go-and-pgx.html, https://github.com/sqlc-dev/sqlc, https://github.com/jmoiron/sqlx, https://github.com/go-gorm/gorm, https://github.com/ent/ent, https://github.com/uptrace/bun.

pgx README: the native interface is "a low-level, high performance interface" recommended when targeting only PostgreSQL; `stdlib` adapter loses LISTEN/NOTIFY and COPY. sqlc docs: set `sql_package: "pgx/v5"`; use `pgxpool.New(...)` and pass the pool to `db.New(pool)`.

Current recommendation for "type safety without heavy ORM": pgx v5 + sqlc. This is the dominant answer in practice; the sources show the integration is first-class, but no source I fetched ranks them, so "dominant" is a judgment. Use hand-written pgx (`pgx.CollectRows`, `pgx.NamedArgs`, `CopyFrom`) for the few dynamic or bulk queries sqlc handles poorly, in the same store package. Requires Postgres 14+ if you stay inside pgx's support window.

Lorvik rationale: self-hosters and SaaS share Postgres, queries are simple to moderate, and the team (and its AI agents) benefits from plain SQL that is reviewable and compile-checked. An ORM adds a second query language and hides N+1s; GORM/ent/bun are viable but not needed. sqlc outputs structs that must not leak into API DTOs; map in the store layer.

### 6.2 Migration tools

| | goose | golang-migrate | atlas | tern | dbmate |
|---|---|---|---|---|---|
| Format | One `.sql` file with `-- +goose Up/Down`; or Go funcs | Pair `.up.sql`/`.down.sql` | Versioned SQL from declarative schema diff; `atlas.sum` integrity file | One file split by `---- create above / drop below ----`; Go funcs | `-- migrate:up/down` |
| Embed (`embed.FS`) | `goose.SetBaseFS`; Provider API in repo | `iofs` source | not read | `fs.FS` in v2 | `db.FS` |
| As library | Yes | Yes (v4) | CLI/cloud oriented | Yes (`tern/v2/migrate`, pgx native) | Yes |
| Postgres extras | `NO TRANSACTION`, `StatementBegin/End` for PL/pgSQL, schema-qualified table | pgx v4/v5 drivers separate from `postgres` | First-class PG; lint | per-migration `disable-tx`, SSH tunnel, templating | `--migration-lock` (PG only), `--strict` |
| Config | `GOOSE_DRIVER`, `GOOSE_DBSTRING`, `GOOSE_MIGRATION_DIR`, `GOOSE_TABLE`, `.env` | no env magic | `atlas.hcl` | `tern.conf`, PG* env | `DATABASE_URL`, `.env` |
| sqlc parses natively | yes | yes | yes | yes | yes (also sql-migrate) |
| Stars (snapshot) | 11.6k | 19.0k | not read | ~1.3k | ~7.5k |
| Notes | Out-of-order via `-allow-missing`; hybrid versioning (timestamps in dev, `fix` to sequential for prod) | 313 open issues | Repo license Apache-2.0 per GitHub; docs footer carries evaluation-use wording and some flows need `atlas login`; confirm licensing and feature gating before depending on it (unverified) | Frequent releases (2.4.3, 2026-08-22) | Language-agnostic, needs `pg_dump` for schema dump |

Sources: https://github.com/pressly/goose, https://github.com/golang-migrate/migrate, https://atlasgo.io/versioned/intro, https://github.com/ariga/atlas, https://github.com/jackc/tern, https://github.com/amacneil/dbmate, https://docs.sqlc.dev/en/latest/howto/ddl.html.

sqlc caveat: for goose and golang-migrate, sqlc reads files in lexicographic order, so zero-pad version numbers (`00001_...`); sqlc ignores down sections.

Recommendation: goose. Embedded SQL migrations in `internal/store/migrations/*.sql`, run by `lorvik-server migrate up` (so self-hosters need no extra tool), and also by the goose CLI via `go tool goose` in dev. tern is the closest alternative if you want pgx-native migration without `database/sql`; golang-migrate is fine but needs separate up/down files and a pgx-specific driver import. Treat atlas as a later option for declarative diffing if its licensing fits.

Operational note: run migrations as an explicit step (or a guarded `migrate` subcommand) in SaaS deploys, not implicitly on every instance start; for self-host single binary, auto-migrate on start behind a flag. Use goose's Postgres advisory-lock behaviour only if verified in its docs (not confirmed in what I read); dbmate documents `--migration-lock` explicitly.

---

## 7. Configuration

### 7.1 12-factor stance

Source: https://12factor.net/config. Config is whatever varies between deploys; strict separation from code; store in env vars (language/OS agnostic, rarely committed); do not group into named "environments" (`staging`, `qa`, `joes-staging` leads to "combinatorial explosion"); treat each env var as an independent orthogonal control. Test: could the repo be open-sourced right now without leaking secrets? (An open-source repo like Lorvik should literally pass this.)

### 7.2 Libraries

| | koanf v2 | viper | envconfig (kelseyhightower) | go-envconfig v2 (sethvargo) | kong env tag |
|---|---|---|---|---|---|
| Scope | Layered sources: file, env, flags, confmap, S3/Vault/etcd/Consul; JSON/YAML/TOML/HCL/dotenv/HUML parsers | Defaults, files, env, pflag, remote KV, watch | Env to struct only | Env to struct, custom lookupers | Per-flag env fallback |
| Case | Keys case-sensitive | Insensitive by design | n/a | n/a | n/a |
| Dependencies | Providers/parsers are separate modules | Large | none | none | in kong |
| Stars | ~4.2k | 30.5k | ~5.5k | ~1.2k | n/a |
| Notes | Built explicitly against viper's issues; Watch() not goroutine-safe with reads | Not concurrency-safe; no deep merge; global singleton discouraged | Page shows no archive banner or alternatives; low-visibility maintenance (37 open PRs) | `required`, `default=`, `prefix=`, `MapLookuper` for parallel tests; Apache-2.0 | First env in declared order wins |

Sources: https://github.com/knadh/koanf, https://github.com/spf13/viper, https://github.com/kelseyhightower/envconfig, https://github.com/sethvargo/go-envconfig, https://github.com/alecthomas/kong.

### 7.3 Recommendation for Lorvik

- Server and worker: environment variables only, loaded once in `internal/config` into a typed struct with go-envconfig v2 (`LORVIK_DATABASE_URL`, `LORVIK_HTTP_ADDR`, `LORVIK_LOG_LEVEL`). Validate in `Load()` and fail fast. Pass the struct down by value; no globals. Tests use `MapLookuper`, avoiding `t.Setenv` and allowing `t.Parallel`.
- Secrets: env or file-path variants (`..._FILE`), never flags (visible in `ps`). The file-path convention is a common practice, not from a fetched source.
- CLI: cobra flags override env override a small user config file (`$XDG_CONFIG_HOME/lorvik/config.json` or TOML) override defaults, implemented explicitly (about 50 lines), or koanf if layering grows. Auth tokens in the OS keychain or a 0600 file. Support `LORVIK_API_URL`, `LORVIK_TOKEN` env for agents.
- Same binary, different deployments: self-host vs SaaS differ only by env, not by compile-time flags.

---

## 8. Build and dev tooling

### 8.1 Task runners

| | Make | Task (go-task) | mage | just |
|---|---|---|---|---|
| Language | Make syntax + shell | YAML | Go | custom justfile syntax (Rust implementation) |
| Install | Preinstalled on Unix; awkward on Windows | One binary; Homebrew, winget, scoop, npm, apt, etc. (v3.54.0 shown) | `go install`; needs Go toolchain; README shows Travis/AppVeyor badges, 235 commits, 4.7k stars | One binary; 36.3k stars; CC0 |
| Up-to-date checks | File timestamps | `sources`/`generates` skip unchanged work | Manual (`target` package) | None; command runner not a build system |
| Cross-platform | Weak on Windows (bash assumed) | Yes | Yes | Yes |
| Used by | `gh` (Makefile delegating to `script/build.go` for portability) | Docker, Vercel, HashiCorp, GoReleaser, etc. (per taskfile.dev) | Smaller niche | General |

Sources: https://taskfile.dev/, https://github.com/magefile/mage, https://github.com/casey/just, https://raw.githubusercontent.com/cli/cli/trunk/Makefile.

Observation: `gh` uses a Makefile whose build target delegates to a Go program precisely for Windows portability, which is the problem Task and mage solve differently.

Recommendation: Taskfile. It sequences Go, Node and Docker without needing a Go toolchain to run the build tool, and works the same for agents and humans (`task --list`). Keep the Taskfile tiny and put real logic in Go programs under `tools/` or `scripts/` when it grows. Provide a `Makefile` stub only if contributors insist.

### 8.2 `go tool` directive (Go 1.24+)

Sources: https://go.dev/doc/go1.24, https://go.dev/doc/modules/managing-dependencies

- `go get -tool golang.org/x/tools/cmd/stringer` adds a `tool` directive and the `require`; run with `go tool stringer`. `go tool` with no args lists them. Use the full import path if names collide.
- Versions are pinned via normal `require` (MVS, replace and exclude apply); `go get tool` upgrades all, `go install tool` installs all to GOBIN. Executables run via `go run`/`go tool` are cached in the build cache.
- Replaces the old `tools.go` blank-import hack.
- Caveat: tools join your module graph, so their transitive deps appear in `go.sum` and can drive version bumps; module pruning usually limits this. Air is GPL-3.0 (https://github.com/air-verse/air): running it as a tool does not link it into your binary, but it will appear in `go.mod` of an MIT/Apache repo; decide whether that is acceptable or install it outside the module.
- Candidates for `tool`: sqlc, goose, oapi-codegen (its README recommends `go get -tool` and `//go:generate go tool oapi-codegen`), golangci-lint (check its docs for the current install guidance; not read), goreleaser (unverified), mockery or moq.

### 8.3 Live reload

| | air | wgo |
|---|---|---|
| Config | `.air.toml` (`air init`) | None; flags |
| Features | Proxy with browser auto-reload, build rules, per-OS overrides, env files | Command chaining with `::`, parallel watchers with `wgo`, debounce, regex file/dir filters |
| Install | `go install`, `go get -tool` + `go tool air`, brew, etc. | `go install`, brew, binaries |
| Stars | ~24.1k | 578 |
| License | GPL-3.0 | MIT |
| Go version | 1.25+ | not stated |

Sources: https://github.com/air-verse/air, https://github.com/bokwoon95/wgo.

Recommendation: for Lorvik the frontend uses Vite's own HMR; the Go server only needs restart-on-change. Use wgo (zero config, MIT) in a Task target: `wgo run ./cmd/lorvik-server serve`. Use air if the team wants its browser-reload proxy.

In dev, do not embed: build with `-tags noui` and let the Go server reverse-proxy or CORS-allow the Vite dev server (5173) so HMR works.

### 8.4 Dockerfile patterns

Sources: https://docs.docker.com/guides/golang/build-images/, https://github.com/GoogleContainerTools/distroless

- Multi-stage: `golang` build stage, copy `go.mod`/`go.sum` first and `go mod download`, then copy source; `CGO_ENABLED=0 go build`; final stage from distroless.
- Docker's guide shows size falling from ~1.11 GB single-stage to ~28.1 MB with distroless base. It does not cover BuildKit cache mounts (those are from general knowledge: `RUN --mount=type=cache,target=/root/.cache/go-build`; verify against Docker docs before adopting).
- Distroless images (all `-debian13` now): `static` (~2 MiB, for static binaries), `base` (glibc, SSL), `base-nossl`, `cc`. Every image has `latest`, `nonroot`, `debug` (busybox shell), `debug-nonroot`. Pin the Debian suffix because the default will move. Use JSON-array `ENTRYPOINT`/`CMD` (no shell). Images are cosign keyless signed.
- `scratch` alternative: smallest, but you must copy CA certificates and tzdata yourself and you get no nonroot user; distroless `static` bundles those. Prefer `static:nonroot`.
- Node stage for the UI is a separate stage that outputs `web/dist` only.

Skeleton:

```dockerfile
# syntax=docker/dockerfile:1
FROM node:lts AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build            # outputs web/dist

FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/lorvik-server ./cmd/lorvik-server

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/lorvik-server /lorvik-server
ENTRYPOINT ["/lorvik-server"]
CMD ["serve"]
```

(Image tags `node:lts` and `golang:1.27` are placeholders; pin real tags/digests when writing the actual file.)

SaaS variant: build with `-tags noui` and serve the dashboard from a CDN/static host separately, or keep it embedded for simplicity.

---

## 9. Non-obvious findings (digest)

1. GoReleaser's `brews` (Homebrew formulas) is deprecated since v2.10; the replacement is `homebrew_casks`, which needs signing/notarization or a quarantine-strip workaround. Homebrew casks can now target Linux too (4.5.0+, 2025).
2. GoReleaser build-level `hooks.pre` runs per build target; a frontend build there reruns for every OS/arch. Use the global `before.hooks` (OSS supports only `before`).
3. `//go:embed all:dist` is required if the committed placeholder is `.gitkeep`; and a missing `dist/` breaks `go vet`, `go test`, and linting, not only `go build`. Pair with a `noui` build tag via two complementary files.
4. The GitHub CLI uses cobra without viper, and its docs prescribe `--json/--jq/--template` via a shared exporter, stdout/stderr discipline, and non-TTY-safe output. It also now ships `AGENTS.md` and a `skills/` dir at repo root, precedent for Lorvik's `skills/`.
5. The Go modules reference advises against committing `go.work`; `godownloader` (install.sh generator) is archived with no replacement named.
6. Air, the popular live-reloader, is GPL-3.0; with `go get -tool` it enters your `go.mod`. wgo (MIT) is zero-config.
7. kong's README does not mention shell completion; cobra, urfave/cli v3 do.
8. `slog` deliberately dropped "logger in context"; inject loggers, put request IDs in context and extract them in a Handler.
9. sqlc reads goose/golang-migrate files lexicographically, so zero-pad version prefixes.

---

## 10. Proposed directory tree for Lorvik

Single module `github.com/<org>/lorvik` (placeholder path; set the real one at `go mod init`).

```
lorvik/
  go.mod                      # one module; tool directives for sqlc, goose, etc.
  go.sum
  Taskfile.yml                # dev entry points: web:build, build, test, lint, generate, dev
  .goreleaser.yaml            # multi-build release: cli, server; homebrew_casks; nfpm
  Dockerfile                  # node -> go -> distroless static nonroot
  AGENTS.md                   # agent instructions for this repo (CLAUDE.md can point to it)
  README.md
  LICENSE
  cmd/
    lorvik/main.go            # CLI binary; thin: build root cobra cmd, Execute
    lorvik-server/main.go     # server binary; thin: load config, wire, run, graceful shutdown
  internal/
    cli/                      # cobra commands, one package per command group (gh pattern)
      cmdutil/                # Factory, JSON exporter, IOStreams helpers
      root/
      project/                # example: lorvik project list|get|create
    client/                   # Go HTTP client for the Lorvik API, used by cli
    apitypes/                 # wire DTOs shared by client and server (or generated)
    server/                   # HTTP wiring: mux, middleware, route registration, shutdown
      middleware/
    <domain>/                 # one package per domain noun (project, task, ...) with service logic
    store/                    # Postgres access
      migrations/             # goose SQL files (embedded)
      queries/                # sqlc input SQL
      sqlcgen/                # sqlc output (committed generated code)
      pgstore/                # hand-written pgx for dynamic/bulk queries, tx helper
    config/                   # typed env config, Load() with validation
    reqctx/                   # typed context accessors: request ID, principal, tenant
    logging/                  # slog setup and ctx-aware handler
    build/                    # version/commit vars set by ldflags
    testutil/                 # test helpers (named for tests; keep small; or use per-package _test helpers)
  web/                        # Vue 3 + TypeScript dashboard (Node project) plus Go embed shim
    package.json
    src/
    dist/.gitkeep             # build output target; contents gitignored except .gitkeep
    embed.go                  # //go:build !noui ; //go:embed all:dist
    noui.go                   # //go:build noui
  api/                        # OpenAPI spec (generated by huma or authored) and generated TS client inputs
  skills/                     # agent skill files for operating the Lorvik CLI (SKILL.md and references)
  docs/                       # user and contributor docs
    research/                 # research notes like this file
    adr/                      # architecture decision records
  scripts/                    # install.sh and small release helpers (logic that outgrows Taskfile goes to Go)
  deploy/                     # compose file, k8s/Helm manifests for self-host and SaaS
  .github/workflows/          # CI: test, lint, build matrix, release via GoReleaser
```

One line per top-level item:

- `go.mod`/`go.sum`: single module, tool directives pin sqlc/goose/etc via `go tool`.
- `Taskfile.yml`: canonical commands for humans, CI, and agents; wraps Go, Node, Docker.
- `.goreleaser.yaml`: two build IDs (cli, server), global `before` hook builds the UI once, casks + nfpm + checksums.
- `Dockerfile`: three-stage image ending in distroless static nonroot.
- `AGENTS.md`: repo conventions for coding agents (layout rules, commands, test scope).
- `cmd/`: one thin `main` per binary; wiring only, per go.dev server guidance.
- `internal/`: all Go code, compiler-protected from outside imports; no `pkg/`.
- `internal/cli`, `internal/client`, `internal/apitypes`: CLI commands, API client, and shared wire types so CLI and server cannot drift.
- `internal/server`, `internal/<domain>`, `internal/store`: HTTP transport, domain logic, and persistence; dependencies point inward (server and store depend on domain, not reverse).
- `internal/config`, `internal/reqctx`, `internal/logging`, `internal/build`: small named packages instead of `util`/`common`.
- `web/`: Vue dashboard plus the only package that embeds its `dist`.
- `api/`: the OpenAPI contract artifact, source of generated TS client.
- `skills/`: skill files that teach agents to drive the `lorvik` CLI (same idea as `cli/cli`'s `skills/`).
- `docs/`: documentation, research, and ADRs.
- `scripts/`: install script and tiny helpers.
- `deploy/`: self-host and SaaS deployment manifests.
- `.github/`: CI and release workflows.

Layout rules to put in the future Go coding skill:
1. No `pkg/`, no `util`/`common`/`base`/`types`/`api`-as-catch-all packages.
2. `cmd/*/main.go` only parses flags/env, builds dependencies, and calls into `internal`.
3. Interfaces are declared by the consumer; implementations are returned as concrete types.
4. Pass `ctx` first; no contexts in structs; loggers and config are injected, not global.
5. Generated code is committed and regenerated by `task generate`; CI fails on a dirty diff.
6. `--json` output of every CLI command is part of the public contract.

---

## 11. Open questions and unverified items

- GoReleaser archives page returned 404; confirm `archives[].ids` syntax from current docs before writing config.
- Atlas licensing/feature gating not confirmed from primary sources.
- Whether goose takes a Postgres advisory lock for concurrent runs: not confirmed in README read.
- `http.Server.BaseContext` and `RegisterOnShutdown` semantics were not read in full; verify on pkg.go.dev.
- golangci-lint and goreleaser as `go tool` entries: not checked.
- Fiber, gin, echo, chi performance claims were not benchmarked or reviewed; none is a recommended choice here.
- The SaaS/private-module split (3.5) is a design proposal, not sourced from a reference project.
