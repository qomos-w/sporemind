#!/usr/bin/env bash
# Close the running sporemind desktop app, rebuild it, then relaunch.
#
# Usage:
#   bash scripts/rebuild-desktop.sh              # make build-desktop (default)
#   bash scripts/rebuild-desktop.sh dev-build    # use a different make target
#   make rebuild-desktop                          # via Makefile
#
# The running .exe is file-locked on Windows, so the build cannot overwrite
# it while the app is alive. But this script typically runs as a child of
# the desktop app itself (via shell_exec), so when the app exits it kills
# all child processes — including this script — before the build starts.
#
# To survive the app exit, the script runs in two phases:
#
#   Phase 1 (in-process):  launches Phase 2 as a detached process via
#                          `cmd //c start` (new process group), then sends
#                          the graceful-shutdown request. Phase 1 is killed
#                          when the app exits — that's fine, it's done.
#
#   Phase 2 (detached):    polls tasklist until the old process is gone,
#                          force-kills if needed, runs make, relaunches.
set -euo pipefail

EXE_NAME="sporemind.exe"
BIN_PATH="build/bin/${EXE_NAME}"
GATEWAY_ADDR="${SPOREMIND_GATEWAY_ADDR:-127.0.0.1:18080}"
MAKE_TARGET="${1:-build-desktop}"

# ── Phase 2: wait → build → relaunch (detached) ──────────────────────────
if [[ "${2:-}" == "--phase2" ]]; then
  echo "[rebuild:phase2] waiting for ${EXE_NAME} to exit ..."
  for _ in $(seq 1 30); do
    if ! tasklist //FI "IMAGENAME eq ${EXE_NAME}" 2>/dev/null | grep -qi "${EXE_NAME}"; then
      break
    fi
    sleep 0.5
  done

  if tasklist //FI "IMAGENAME eq ${EXE_NAME}" 2>/dev/null | grep -qi "${EXE_NAME}"; then
    echo "[rebuild:phase2] process still alive, force-killing ..."
    taskkill //F //IM "${EXE_NAME}" 2>/dev/null || true
    sleep 1
  fi

  # Ensure the file lock is fully released before overwriting.
  sleep 1

  echo "[rebuild:phase2] running make ${MAKE_TARGET} ..."
  make "${MAKE_TARGET}"

  echo "[rebuild:phase2] launching ${BIN_PATH} ..."
  cmd //c start "" "${BIN_PATH}"
  echo "[rebuild:phase2] done — app relaunched"
  exit 0
fi

# ── Phase 1: launch detached Phase 2, then shut down the app ─────────────
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT_PATH="${SCRIPT_DIR}/rebuild-desktop.sh"

http_addr="$GATEWAY_ADDR"
if [[ "$http_addr" == :* ]]; then
  http_addr="localhost$http_addr"
fi

echo "[rebuild] launching detached build process ..."
cmd //c start "rebuild-desktop" bash "${SCRIPT_PATH}" "${MAKE_TARGET}" --phase2

# Give the detached process time to start before we kill the app.
sleep 1

echo "[rebuild] requesting graceful shutdown ..."
curl -sf -X POST --max-time 3 "http://${http_addr}/debug/shutdown" 2>/dev/null || true
echo "[rebuild] shutdown sent — the detached process will build and relaunch"
