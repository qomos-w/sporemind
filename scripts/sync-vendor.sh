#!/usr/bin/env bash
# Sync vendored @qomos packages (web/vendor/) from the UPSTREAM TAGS pinned in
# go.mod — not from the local working trees, which may be ahead of the pins.
# With --check, verify vendor/ matches the pinned tags and exit non-zero on
# drift (CI); without it, refresh the vendor trees in place.
set -euo pipefail

mode="${1:-sync}"   # sync | --check
root="$(cd "$(dirname "$0")/.." && pwd)"

# Only package sources, manifests and configs are vendored. Lockfiles and
# build outputs stay out — the consuming app (web/) installs the file: dep
# and bundles the TS sources directly.
copy_tree() { # <src-dir> <dst-dir>
  local src="$1" dst="$2"
  rm -rf "$dst"
  mkdir -p "$dst"
  (cd "$src" && find . -type d \( -name node_modules -o -name dist -o -name .git \) -prune -o -type f ! -name '*.js' ! -name '*.tsbuildinfo' ! -name bun.lock ! -name package-lock.json ! -name pnpm-lock.yaml -print0 | tar --null -cf - -T -) | (cd "$dst" && tar -xf -)
}

pin() { grep -oP "github.com/qomos-w/$1 \K\S+" "$root/go.mod"; }

# gospore/web-client and spore/ts live in sibling checkouts; git archive the
# pinned tag's subtree so the source equals exactly what go.mod resolves to.
sibling() { # <repo-name> <tag> <subdir> <dst-name>
  local repo="$1" tag="$2" subdir="$3" name="$4"
  local dir="$root/../$repo"
  if [ ! -d "$dir/.git" ]; then
    echo "sync-vendor: $dir is not a git checkout — $name left untouched" >&2
    [ "$mode" = "--check" ] && exit 1 || exit 0
  fi
  local tmp; tmp="$(mktemp -d)"
  (cd "$tmp" && git -C "$dir" archive "$tag:$subdir" | tar -x --strip-components=1)
  local dst="$root/web/vendor/$name"
  if [ "$mode" = "--check" ]; then
    if ! diff -r "$tmp" "$dst" >/dev/null 2>&1; then
      echo "sync-vendor: DRIFT in $name (pinned $repo@$tag != vendor $dst)" >&2
      rm -rf "$tmp"; exit 1
    fi
    rm -rf "$tmp"; echo "sync-vendor: $name matches $repo@$tag"
  else
    rm -rf "$dst"; mv "$tmp" "$dst"; echo "sync-vendor: $name refreshed from $repo@$tag"
  fi
}

sibling gospore "$(pin gospore)" web-client gospore-client
sibling spore   "$(pin spore)"   ts        spore-ts
