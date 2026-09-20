package converter

import (
	"reflect"
	"sync"
)

// Mutable singletons
// TODO(actor-ownership): migrate to actor-owned state
var (
	registryMu sync.RWMutex
	registry   = make(map[reflect.Type]Converter)
)
