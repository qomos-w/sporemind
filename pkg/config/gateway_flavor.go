//go:build !devrelease

package config

// Per-flavor instance identity, split by build tag. Non-devrelease builds keep
// the stock behavior: default gateway address and data dir are unchanged, and
// the ephemeral-port discovery hooks are no-ops.
const (
	devReleaseGatewayAddr = DefaultGatewayAddr
	devReleaseDataDirName = ".sporemind"
)

func announceGatewayAddr(string) {}

func previousGatewayAddr(string) (string, bool) { return "", false }
