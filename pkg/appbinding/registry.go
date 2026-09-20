package appbinding

import (
	"sync"
)

type Registry struct {
	mu      sync.RWMutex
	surface map[string]SurfaceBinding
}

func NewRegistry() *Registry {
	return &Registry{surface: map[string]SurfaceBinding{}}
}

// HasBinding reports whether a surface binding exists for the
// (appID, agentID) pair. Cast/emit use this to require that the caller is a
// bound agent of the target app before accepting an event broadcast, closing
// the gap where any authenticated agent could broadcast events as any app.
func (r *Registry) HasBinding(appID, agentID string) bool {
	key := appID + "\x00" + agentID
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, surfOk := r.surface[key]
	return surfOk
}

func (r *Registry) Unbind(appID, agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.surface, appID+"\x00"+agentID)
}

func (r *Registry) BindSurface(binding SurfaceBinding) error {
	if err := binding.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.surface[binding.AppID+"\x00"+binding.AgentID] = binding
	return nil
}

func (r *Registry) Surface(appID, agentID string) (SurfaceBinding, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.surface[appID+"\x00"+agentID]
	return b, ok
}
