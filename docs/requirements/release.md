# Release: archives, installer and server image

## 1. Purpose

Ship Lorvik as versioned archives for the CLI and the server, a one-line installer for the CLI, and a container image for the server. Everything here is built from a tag by GoReleaser; nothing is published without the owner's go.

## 2. Behaviour

### 2.1 What a release contains

Configured in `.goreleaser.yaml`. Targets are linux and darwin on amd64 and arm64, built with `CGO_ENABLED=0 -trimpath`. The linker stamps `internal/build.Version`, `.Commit` and `.Date` (same variables as the Taskfile `LDFLAGS`).

| Archive | Name | Contents |
|---|---|---|
| CLI | `lorvik_<os>_<arch>.tar.gz` | `lorvik`, `LICENSE-APACHE`, `cmd/lorvik/LICENSE`, `skills/**` |
| Server | `lorvikd_<version>_<os>_<arch>.tar.gz` | `lorvikd` (dashboard embedded), `LICENSE`, `LICENSING.md`, `deploy/compose/compose.yaml` |

Plus `checksums.txt` (sha256, one line per archive, both kinds). The CLI archive name carries no version so that `releases/latest/download/<asset>` resolves to it; the server archives keep the version.

The dashboard is built once by the `before` hook (`task web:build`), so every `lorvikd` carries it. A build without `web/dist` still compiles (`web/dist/.gitkeep` is committed) and ships an empty UI.

`task release:snapshot` runs GoReleaser with `--snapshot --clean --skip=publish` and writes `dist/` (git-ignored). It builds everything locally and publishes nothing. The release itself is created as a draft (`release.draft: true`).

### 2.2 install.sh

`install.sh` at the repo root, POSIX `sh`, `set -eu`, no `sudo`. Usage: `sh install.sh [version]`.

- OS from `uname -s` (`linux`, `darwin`; anything else is refused). Arch from `uname -m` (`x86_64`/`amd64` to `amd64`, `aarch64`/`arm64` to `arm64`; anything else is refused).
- No version: `https://github.com/BonfireTrunk/lorvik/releases/latest/download/<asset>`. With a version (a tag such as `v0.3.0`): `.../releases/download/<version>/<asset>`.
- Downloads `lorvik_<os>_<arch>.tar.gz` and `checksums.txt` with `curl`, falling back to `wget`.
- Verifies the archive's sha256 against `checksums.txt` using `sha256sum` or `shasum -a 256`. A missing entry, a missing hash tool or a mismatch aborts before anything is installed.
- Extracts only `lorvik` and moves it into `${LORVIK_INSTALL_DIR:-$HOME/.local/bin}` (created if missing; replaced via a temp name in the same directory, so an upgrade never leaves a half-written binary).
- Prints the result of `lorvik version` and, when the directory is not on `PATH`, says so on stderr with the line to add.
- `LORVIK_DOWNLOAD_BASE` replaces the GitHub URL prefix with a directory, a `file://` URL or an http(s) prefix holding the archive and `checksums.txt`. It exists to test against a snapshot's `dist/`.

### 2.3 The server image

`Dockerfile` has two stages. Stage one is `golang:1.27` (matches the `go` line in `go.mod`) and builds `lorvikd` with `CGO_ENABLED=0 -trimpath` and `VERSION`/`COMMIT` build args. Stage two is `gcr.io/distroless/static-debian13:nonroot` holding only `/lorvikd`. It runs as the distroless non-root user, `EXPOSE 8080`, `ENTRYPOINT ["/lorvikd"]`, `CMD ["serve"]`.

The dashboard is not built in the image: the build context's `web/dist` is embedded. Run `task web:build` before `task docker:build` (`docker build -t lorvikd:snapshot .` with `VERSION`/`COMMIT` build args). `.dockerignore` keeps `.git`, `bin/`, `dist/`, `tmp/` and `web/node_modules` out of the context but keeps `web/dist`.

Configuration is by environment variable, read by `internal/config` (prefix `LORVIK_`):

| Variable | Default | Meaning |
|---|---|---|
| `LORVIK_HTTP_ADDR` | `:8080` | listen address; change `EXPOSE`/port mapping with it |
| `LORVIK_DATABASE_URL` | none | Postgres connection string. Without it the server starts but has no store |
| `LORVIK_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `LORVIK_LOG_FORMAT` | `text` | `text` or `json` |
| `LORVIK_AUTO_MIGRATE` | `false` | apply pending migrations when `serve` starts |
| `LORVIK_SHUTDOWN_TIMEOUT` | `15s` | graceful shutdown bound |
| `LORVIK_DEV_UI_PROXY` | none | proxy the UI to a Vite dev server (development only) |
| `LORVIK_CORS_ORIGINS` | none | comma-separated browser origins allowed cross-origin |
| `LORVIK_AUTH_PASSWORD` | `false` | enable `POST /login` (see `docs/requirements/login.md`) |
| `LORVIK_GITHUB_CLIENT_ID`, `LORVIK_GITHUB_CLIENT_SECRET` | none | GitHub OAuth app; GitHub sign-in is on when both are set |
| `LORVIK_PUBLIC_URL` | derived per request | externally visible base URL, for OAuth redirects |
| `LORVIK_TRUSTED_PROXIES` | none | comma-separated IP addresses or CIDR ranges of the reverse proxies in front of the server. Set it when there is one: the sign-in limits then count each client by the address the proxy reports in `X-Forwarded-For`; without it every client behind the proxy shares one allowance, and the server logs one warning when it sees the header from an unlisted peer. A range covering every address is refused (`docs/requirements/login.md` 2.1a) |
| `LORVIK_BOOTSTRAP_PASSWORD` | none | read only by `lorvikd admin bootstrap`, not by `serve` |

Liveness: `GET /api/v1/healthz` answers 200 with `{"status","version","database"}`. `/healthz` without the prefix is served by the dashboard fallback and always answers 200, so it is not a health check.

### 2.4 Migrations

`lorvikd serve` does not migrate unless `LORVIK_AUTO_MIGRATE=true`. The default start is two steps against the same database:

1. `docker run --rm -e LORVIK_DATABASE_URL=... lorvikd:<tag> migrate up`
2. `docker run -d -p 8080:8080 -e LORVIK_DATABASE_URL=... lorvikd:<tag>`

Setting `LORVIK_AUTO_MIGRATE=true` collapses these into one for single-instance setups. `migrate up` is idempotent.

### 2.5 Tagging and publishing (intended change, card #13)

Not built. Today no tag is pushed, no release is created, no image is pushed and no workflow runs GoReleaser. Card #13 covers the tag, the GitHub release and the image push. Until then only `task release:snapshot` and `task docker:build` exist, and both are local.

## 3. Rules

- Nothing is published (tag, GitHub release, registry push) without the owner's explicit go.
- The installer never installs an archive whose sha256 does not match `checksums.txt`.
- The installer needs no privileges and never writes outside `LORVIK_INSTALL_DIR` and a temp directory it removes on exit.
- `skills/` and `cmd/lorvik` ship under Apache-2.0 in the CLI archive; server code under AGPL-3.0-only (`LICENSING.md`).

## 4. Data

None.

## 5. API

None, apart from the existing `GET /api/v1/healthz` used as the image's liveness check (`docs/api.md`).

## 6. Jobs

None.

## 7. Why it is this way

- GoReleaser over hand-rolled scripts: one config yields the cross-compiled archives, checksums and a draft release, and `--snapshot` lets the whole pipeline be tested without publishing. Rejected: a shell loop over `go build`, which would need its own checksum and archive code.
- Distroless `static` over alpine or scratch: a static Go binary needs no shell or package manager, the image is about 30 MB, and `nonroot` and CA certificates come with it. Scratch would lack CA certificates for outbound HTTPS (GitHub sign-in).
- No bun in the image: the UI is built once on the host and shared by the archives and the image, so the image build needs no Node toolchain and no network beyond Go modules.
- Migrations are not run on start by default: a failing migration should fail a deploy step, not crash-loop the server, and schema changes stay an explicit operator action. `LORVIK_AUTO_MIGRATE` stays available for single-instance use.
- The CLI archive has no version in its name: GitHub's `releases/latest/download/<asset>` redirect needs a fixed asset name, and it lets the installer find the latest release without an API call.
