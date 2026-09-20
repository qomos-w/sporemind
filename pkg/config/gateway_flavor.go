//go:build !devrelease

package config

// Per-flavor instance identity, split by build tag. Non-devrelease builds keep
// the stock behavior: default gateway address and data dir are unchanged.
const (
	devReleaseGatewayAddr = DefaultGatewayAddr
	devReleaseDataDirName = ".sporemind"
)
