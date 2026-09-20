package aiaggregator

// This file consolidates package-level variable declarations for the
// aiaggregator package.

import "errors"

// --- Token-plan errors ---

// errUnitsTokenPlanExhausted is returned when every matching unit is skipped
// because its token plan is expired or exhausted.
var errUnitsTokenPlanExhausted = errors.New("aiaggregator: all matching units have exhausted token plan")

// errPoolExhausted is a sentinel wrapped into the dispatch error when the
// failover loop has tried every candidate and selectUnitExcluding found no
// remaining healthy unit. The turn engine's retry strategy detects this to
// skip pointless retries: if the whole pool is rate-limited or quota-exhausted,
// retrying after a short backoff cannot succeed — the cooldown windows are
// much longer than the dispatch backoff sequence.
var errPoolExhausted = errors.New("aiaggregator: pool exhausted, no failover candidates")
