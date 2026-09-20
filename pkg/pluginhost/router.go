package pluginhost

import (
	"net/http"
	"strings"
	"sync"

	"github.com/qomos-w/gospore/resource"
)

// RouterKey is the gospore resource key for the plugin gateway router.
var RouterKey = resource.Key[*Router]{Name: "sporemind.pluginhost.router"}

// Router routes HTTP requests under /plugin/:id/* to registered plugin
// handlers. It is safe for concurrent use.
type Router struct {
	mu      sync.RWMutex
	plugins map[string]http.Handler
}

// NewRouter returns an empty plugin router.
func NewRouter() *Router {
	return &Router{
		plugins: make(map[string]http.Handler),
	}
}

// Register adds or replaces the handler for pluginID.
func (r *Router) Register(pluginID string, handler http.Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plugins[pluginID] = handler
}

// Unregister removes the handler for pluginID.
func (r *Router) Unregister(pluginID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.plugins, pluginID)
}

// Handler returns the handler for pluginID, or nil if not registered.
func (r *Router) Handler(pluginID string) http.Handler {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.plugins[pluginID]
}

// ServeHTTP implements http.Handler. The request path must start with
// /plugin/:id/; everything after /plugin/:id is forwarded to the plugin
// handler with the prefix stripped.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	pluginID, ok := extractPluginID(req.URL.Path)
	if !ok {
		http.NotFound(w, req)
		return
	}

	r.mu.RLock()
	h := r.plugins[pluginID]
	r.mu.RUnlock()

	if h == nil {
		http.NotFound(w, req)
		return
	}

	prefix := "/plugin/" + pluginID
	http.StripPrefix(prefix, h).ServeHTTP(w, req)
}

// extractPluginID parses "/plugin/:id/..." and returns the plugin ID.
func extractPluginID(path string) (string, bool) {
	const prefix = "/plugin/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	if rest == "" {
		return "", false
	}
	idx := strings.Index(rest, "/")
	if idx == -1 {
		return rest, true
	}
	return rest[:idx], true
}
