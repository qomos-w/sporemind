package browserinstance

// This file consolidates package-level variable declarations for the
// browserinstance package.

import "sync"

// --- Desktop window operator ---

// TODO(actor-ownership): migrate to actor-owned state
var (
	globalOperator   WindowOperator
	globalOperatorMu sync.RWMutex
)
