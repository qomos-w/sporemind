//go:build devrelease

package config

// `make dev-release` desktop builds (build tag "devrelease") get their own
// instance identity so a dev-release process and a dev process can run side
// by side: a separate gateway port (the startup takeover in
// desktop.ClosePreviousInstance then only ever reaches its own flavor, and the
// two listeners never contend for 18080) and a separate default data dir (the
// single-writer invariant the same-flavor startup kill used to enforce).
const (
	devReleaseGatewayAddr = "127.0.0.1:18081"
	devReleaseDataDirName = ".sporemind-devrelease"
)
