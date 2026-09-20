package timer

import "sync"

// Mutable singletons
// TODO(actor-ownership): migrate to actor-owned state
var (
	once sync.Once
	ins  Timer
)

var logger = timerLoggerT{}

var _timeTestHandler ITimeTestHandler = nil

// Function seams
var get10Ms = _get10Ms
