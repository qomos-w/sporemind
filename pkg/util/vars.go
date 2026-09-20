package util

import "sync"

// Mutable singletons
// TODO(actor-ownership): migrate to actor-owned state
var (
	shellCacheMu  sync.Mutex
	shellCache    ShellInfo
	shellCacheSig string

	shellPrefMu sync.RWMutex
	shellPref   ShellKind
)
