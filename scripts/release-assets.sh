#!/usr/bin/env bash
# Builds the release files: the CLI and server archives and checksums.txt.
# The shared release workflow runs this as its assets-command and sets the
# three variables below (docs/requirements/release.md, section 2.5).
set -euo pipefail

: "${RELEASE_VERSION:?the version being released, for example 1.2.0}"
: "${RELEASE_TAG:?the tag of that version, for example v1.2.0}"
: "${RELEASE_ASSETS_DIR:?an empty directory to leave the release files in}"

GORELEASER=github.com/goreleaser/goreleaser/v2@v2.20.3

assets_dir=$(cd "$RELEASE_ASSETS_DIR" && pwd)
cd "$(dirname "$0")/.."

. scripts/pinned-bun.sh

# GoReleaser wants the release tag on HEAD. The workflow creates the real tag
# only after this build, so a local one stands in; it is never pushed and is
# removed again when this script made it.
if ! git rev-parse -q --verify "refs/tags/$RELEASE_TAG" >/dev/null; then
  git tag "$RELEASE_TAG"
  trap 'git tag -d "$RELEASE_TAG" >/dev/null' EXIT
fi

GORELEASER_CURRENT_TAG="$RELEASE_TAG" go run "$GORELEASER" release --clean --skip=publish

if [ ! -f "dist/lorvikd_${RELEASE_VERSION}_linux_amd64.tar.gz" ]; then
  echo "release-assets: the build is not version $RELEASE_VERSION" >&2
  exit 1
fi
cp dist/*.tar.gz dist/checksums.txt "$assets_dir"/
(cd "$assets_dir" && sha256sum --check --strict --quiet checksums.txt)
if [ "$(ls -- "$assets_dir"/*.tar.gz | wc -l)" -ne "$(wc -l < "$assets_dir/checksums.txt")" ]; then
  echo "release-assets: checksums.txt does not list exactly the archives" >&2
  exit 1
fi
