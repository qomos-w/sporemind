package hooks

// Mutable singletons
// TODO(actor-ownership): migrate to actor-owned state
var global = &Registry{entries: make(map[string]map[string]Factory)}