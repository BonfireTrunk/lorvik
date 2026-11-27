# Sourced, not run: puts the bun version named in web/package.json on PATH,
# installing it into a temporary directory when the machine has another one
# or none.
bun_version=$(sed -n 's/.*"packageManager": *"bun@\([0-9.]*\)".*/\1/p' web/package.json)
: "${bun_version:?web/package.json names no bun version in packageManager}"
if [ "$(bun --version 2>/dev/null || true)" != "$bun_version" ]; then
  bun_dir=$(mktemp -d)
  npm install --prefix "$bun_dir" --no-save --no-audit --no-fund "bun@$bun_version"
  PATH="$bun_dir/node_modules/.bin:$PATH"
fi
