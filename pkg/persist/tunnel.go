package persist

import (
	"fmt"
	"sync"
)

// TunnelResolver resolves a TunnelRef (an sshmanager hostID) plus the
// backend's target address to the local listener address opened by
// sshmanager (ssh -L semantics). Network backends dial localAddr; the
// SSH-encrypted tunnel carries traffic to target.
//
// The resolution happens at backend-construction time so tunnel lifecycle
// (reconnect, idle reclaim, cleanup) stays owned by sshmanager. The
// resolver is installed at startup by the runtime wiring layer (B7) via
// LookupService("sshmanager") + tunnel_open; persist itself never imports
// an actor.
type TunnelResolver func(ref string, target string) (localAddr string, err error)

var (
	tunnelMu       sync.RWMutex
	tunnelResolver TunnelResolver
)

// SetTunnelResolver installs the process-wide tunnel resolver. Installing
// over an existing resolver is allowed and replaces it (tests and
// restarts). A nil resolver restores the explicit-error path.
func SetTunnelResolver(r TunnelResolver) {
	tunnelMu.Lock()
	defer tunnelMu.Unlock()
	tunnelResolver = r
}

// ResolveTunnel returns the local listener address for dialing target
// through the tunnel identified by ref. An empty ref is the direct-dial
// path and yields an empty address (callers then dial target itself). A
// non-empty ref with no resolver installed is an explicit error — never a
// silent direct dial, which would route plaintext DB traffic around the
// declared tunnel (约束面).
func ResolveTunnel(ref, target string) (string, error) {
	if ref == "" {
		return "", nil
	}
	tunnelMu.RLock()
	r := tunnelResolver
	tunnelMu.RUnlock()
	if r == nil {
		return "", fmt.Errorf("persist: TunnelRef %q set but no tunnel resolver installed (B7 wiring not started?)", ref)
	}
	addr, err := r(ref, target)
	if err != nil {
		return "", fmt.Errorf("persist: resolve tunnel %q: %w", ref, err)
	}
	if addr == "" {
		return "", fmt.Errorf("persist: resolve tunnel %q: resolver returned empty local address for target %q", ref, target)
	}
	return addr, nil
}
