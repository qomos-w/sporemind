//go:build devrelease

package config

import (
	"net"
	"os"
	"path/filepath"
	"strings"
)

// `make dev-release` desktop builds (build tag "devrelease") get their own
// instance identity so a dev-release process and a dev process can run side
// by side: a separate gateway lane (ephemeral port, never a fixed number —
// nothing to collide with and nothing for another process to squat on) and a
// separate default data dir (the single-writer invariant the same-flavor
// startup takeover used to enforce). Same-flavor takeover discovers the
// previous instance through the gateway.port file recorded in the data dir.
const (
	devReleaseGatewayAddr = "127.0.0.1:0"
	devReleaseDataDirName = ".sporemind-devrelease"
)

// gatewayPortFileName (vars.go) records the actual address the ephemeral
// gateway bound, under the flavor's data dir. A new dev-release instance
// reads it to shut down its predecessor; stale records just fail the probe
// and are ignored.

// announceGatewayAddr records the bound gateway address for same-flavor
// instance discovery. Best-effort: a failed write only costs the next
// launch's takeover probe.
func announceGatewayAddr(addr string) {
	if addr == "" {
		return
	}
	dir := DataDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, gatewayPortFileName), []byte(addr), 0o644)
}

// previousGatewayAddr reports the recorded address of a previous same-flavor
// instance, but only while the current build itself is ephemeral (port 0):
// an explicitly configured fixed address is reachable directly and must not
// be shadowed by a stale record from an earlier ephemeral run.
func previousGatewayAddr(current string) (string, bool) {
	if _, port, err := net.SplitHostPort(current); err != nil || port != "0" {
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(DataDir(), gatewayPortFileName))
	if err != nil {
		return "", false
	}
	addr := strings.TrimSpace(string(data))
	if addr == "" {
		return "", false
	}
	return addr, true
}
