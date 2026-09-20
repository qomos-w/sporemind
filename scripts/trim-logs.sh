#!/usr/bin/env bash
# trim-logs.sh — one-off cleanup of oversized sporemind backend/console logs.
#
# The logging store now caps each file at 64 MiB and rotates by size, plus
# cleans files older than 7 days at startup. This script clears the backlog
# that accumulated before those limits existed (e.g. a single 290 MiB day).
#
# Usage:
#   scripts/trim-logs.sh [logs-dir] [--days N] [--all]
#
#   logs-dir   defaults to <exe-dir>/.sporecode/logs (beside the build).
#   --days N   remove files older than N days (default 7). The current day's
#              active files are always preserved.
#   --all      remove EVERY .jsonl (including today). Only use this when the
#              desktop/headless process is STOPPED — an open file on Windows
#              cannot be deleted and may be recreated truncated.
set -euo pipefail

days=7
all=0
logs_dir=""

while [[ $# -gt 0 ]]; do
	case "$1" in
		--days) days="$2"; shift 2 ;;
		--all) all=1; shift ;;
		-*) echo "unknown option: $1" >&2; exit 2 ;;
		*) logs_dir="$1"; shift ;;
	esac
done

if [[ -z "$logs_dir" ]]; then
	# Best-effort default: look beside the usual build output.
	for cand in \
		"build/bin/.sporecode/logs" \
		"$(dirname "$(readlink -f "$0" 2>/dev/null || echo "$0")")/../build/bin/.sporecode/logs"; do
		if [[ -d "$cand" ]]; then logs_dir="$cand"; break; fi
	done
fi
if [[ -z "$logs_dir" || ! -d "$logs_dir" ]]; then
	echo "logs dir not found; pass it explicitly: scripts/trim-logs.sh <logs-dir>" >&2
	exit 1
fi

echo "logs dir: $logs_dir"
du -sh "$logs_dir" 2>/dev/null || true

today="$(date -u +%Y-%m-%d)"

remove_if() { # path
	local p="$1"
	if [[ "$all" -eq 0 ]]; then
		# Keep the current day's active file(s).
		case "$(basename "$p")" in
			"$today".jsonl|"$today".*.jsonl) return 0 ;;
		esac
		if [[ "$(find "$p" -mtime +$days 2>/dev/null | wc -l)" -eq 0 ]]; then
			return 0 # newer than --days, keep
		fi
	fi
	rm -f "$p"
	echo "removed $(basename "$p")"
}

count=0
while IFS= read -r -d '' f; do
	remove_if "$f"
	count=$((count + 1))
done < <(find "$logs_dir" -type f -name '*.jsonl' -print0)

echo "---"
echo "after:"
du -sh "$logs_dir" 2>/dev/null || true
echo "scanned $count jsonl files (all=$all, days=$days)"
