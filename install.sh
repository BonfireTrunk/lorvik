#!/bin/sh
# Install the lorvik CLI from a GitHub release.
#
#   sh install.sh [version]
#
# version is a release tag such as v0.3.0; default is the latest release.
# Environment:
#   LORVIK_INSTALL_DIR     target directory (default $HOME/.local/bin)
#   LORVIK_DOWNLOAD_BASE   directory or URL prefix holding the archive and
#                          checksums.txt, replacing the GitHub release URL
set -eu

REPO="BonfireTrunk/lorvik"

fail() {
	printf 'install.sh: %s\n' "$*" >&2
	exit 1
}

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
linux | darwin) ;;
*) fail "unsupported OS: $os (linux and darwin only)" ;;
esac

case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) fail "unsupported architecture: $(uname -m) (amd64 and arm64 only)" ;;
esac

version=${1:-}
if [ -n "${LORVIK_DOWNLOAD_BASE:-}" ]; then
	base=${LORVIK_DOWNLOAD_BASE%/}
elif [ -n "$version" ]; then
	base="https://github.com/$REPO/releases/download/$version"
else
	base="https://github.com/$REPO/releases/latest/download"
fi

install_dir=${LORVIK_INSTALL_DIR:-${HOME:?HOME is not set}/.local/bin}
asset="lorvik_${os}_${arch}.tar.gz"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

fetch() {
	case "$base" in
	file://*) cp "${base#file://}/$1" "$2" ;;
	http://* | https://*)
		if command -v curl >/dev/null 2>&1; then
			curl -fsSL -o "$2" "$base/$1"
		elif command -v wget >/dev/null 2>&1; then
			wget -q -O "$2" "$base/$1"
		else
			fail "curl or wget is required"
		fi
		;;
	*) cp "$base/$1" "$2" ;;
	esac
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d ' ' -f 1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d ' ' -f 1
	else
		fail "sha256sum or shasum is required to verify the download"
	fi
}

printf 'Downloading %s\n' "$asset" >&2
fetch "$asset" "$tmp/$asset" || fail "could not download $asset from $base"
fetch checksums.txt "$tmp/checksums.txt" || fail "could not download checksums.txt from $base"

want=$(awk -v f="$asset" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || fail "$asset is not listed in checksums.txt"
got=$(sha256_of "$tmp/$asset")
[ "$want" = "$got" ] || fail "checksum mismatch for $asset: expected $want, got $got"

tar -xzf "$tmp/$asset" -C "$tmp" lorvik || fail "could not extract lorvik from $asset"
mkdir -p "$install_dir"
mv "$tmp/lorvik" "$install_dir/lorvik.new"
chmod 755 "$install_dir/lorvik.new"
mv "$install_dir/lorvik.new" "$install_dir/lorvik"

printf 'Installed %s\n' "$install_dir/lorvik"
"$install_dir/lorvik" version

case ":${PATH}:" in
*":$install_dir:"*) ;;
*) printf '\n%s is not on your PATH. Add it, for example:\n  export PATH="%s:$PATH"\n' "$install_dir" "$install_dir" >&2 ;;
esac
