#!/usr/bin/env bash
# Sync vendored @qomos packages (web/vendor/) from the sibling upstream
# checkouts. With --check, verify vendor/ matches upstream and exit non-zero
# on drift (for CI); without it, refresh the vendor trees in place.
set -euo pipefail

gospore_src="${1:?usage: sync-vendor.sh <gospore/web-client> <spore/ts> [--check]}"
spore_src="${2:?usage: sync-vendor.sh <gospore/web-client> <spore/ts> [--check]}"
mode="${3:-sync}"
root="$(cd "$(dirname "$0")/.." && pwd)"

# Only package sources, manifests and configs are vendored. Lockfiles and
# build outputs stay out — the consuming app (web/) installs the file: dep
# and bundles the TS sources directly.
copy_tree() {
  local src="$1" dst="$2"
  rm -rf "$dst"
  mkdir -p "$dst"
  (cd "$src" && find . -type d \( -name node_modules -o -name dist -o -name .git \) -prune -o -type f ! -name '*.js' ! -name '*.tsbuildinfo' ! -name bun.lock ! -name package-lock.json ! -name pnpm-lock.yaml -print0 | tar --null -cf - -T -) | (cd "$dst" && tar -xf -)
}

check_one() {
  local src="$1" dst="$2" name="$3"
  tmp="$(mktemp -d)"
  copy_tree "$src" "$tmp/pkg"
  if ! diff -r "$tmp/pkg" "$dst" >/dev/null 2>&1; then
    echo "sync-vendor: DRIFT in $name (upstream $src != vendor $dst)" >&2
    rm -rf "$tmp"
    return 1
  fi
  rm -rf "$tmp"
  echo "sync-vendor: $name up to date"
}

for pair in "$gospore_src:web/vendor/gospore-client:gospore-client" "$spore_src:web/vendor/spore-ts:spore-ts"; do
  IFS=: read -r src dst name <<<"$pair"
  case "$mode" in
    --check) check_one "$root/$src" "$root/$dst" "$name" || exit 1 ;;
    *)       copy_tree "$root/$src" "$root/$dst"; echo "sync-vendor: $name refreshed" ;;
  esac
done
