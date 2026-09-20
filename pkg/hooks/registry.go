package hooks

import "sync"

// Factory creates a middleware from config params.
// The returned any must be Middleware[E] for the target hook's event type.
// Type safety is guaranteed by the registration site, not the registry.
type Factory func(params map[string]any) (any, error)

// Registry maps (hookName, middlewareName) → Factory.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]map[string]Factory
}

// Register adds a factory for the given hook+middleware name pair.
// Safe for concurrent use; writes are serialized.
func Register(hookName, middlewareName string, f Factory) {
	global.mu.Lock()
	defer global.mu.Unlock()
	if global.entries[hookName] == nil {
		global.entries[hookName] = make(map[string]Factory)
	}
	global.entries[hookName][middlewareName] = f
}

// Lookup returns the factory for the given hook+middleware name pair.
func Lookup(hookName, middlewareName string) (Factory, bool) {
	global.mu.RLock()
	defer global.mu.RUnlock()
	m, ok := global.entries[hookName]
	if !ok {
		return nil, false
	}
	f, ok := m[middlewareName]
	return f, ok
}

// HookNames returns all hook names that have at least one factory registered.
func HookNames() []string {
	global.mu.RLock()
	defer global.mu.RUnlock()
	names := make([]string, 0, len(global.entries))
	for k := range global.entries {
		names = append(names, k)
	}
	return names
}
