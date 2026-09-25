package config

import "sync"

// Mutable singletons
// TODO(actor-ownership): migrate to actor-owned state
var (
	exeDir string
	cfg    config

	// cfgMu serializes runtime cfg mutations (settings setters, ephemeral
	// gateway adoption) against the read accessors, so adopting the bound
	// address concurrently with a UI read can never race.
	cfgMu sync.RWMutex
)

// gatewayPortFileName records the bound gateway address for same-flavor
// instance discovery. Only written by flavors implementing it (devrelease).
const gatewayPortFileName = "gateway.port"
