package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/qomos-w/sporemind/pkg/persist"
)

// Registry maps stable service names to their actor IDs so the actor tree
// can be reconstructed across process restarts.
type Registry struct {
	mu       sync.Mutex
	NodeID   string            `json:"nodeId"`
	Services map[string]string `json:"services"` // serviceName → actorID hex
	path     string            // on-disk location, set after load
}

// LoadRegistry reads the registry from disk. Returns a new empty registry
// if the file does not exist.
func LoadRegistry(path string) (*Registry, error) {
	r := &Registry{
		NodeID:   "1",
		Services: make(map[string]string),
		path:     path,
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, fmt.Errorf("registry: read: %w", err)
	}
	if len(data) == 0 {
		return r, nil
	}
	if err := json.Unmarshal(data, r); err != nil {
		// Back up the corrupt file before reporting the error, mirroring
		// FSPersist.Load: the original bytes survive for manual recovery
		// instead of being overwritten by the next Save. registry.json is
		// the sole barrier for actor-ID stability — a torn decode that we
		// silently dropped on the floor would orphan every service's
		// persisted state (runtime.go:613-614 requires hand repair).
		backup := r.path + ".corrupt-" + time.Now().UTC().Format("20060102T150405Z")
		if rerr := os.Rename(r.path, backup); rerr == nil {
			return nil, fmt.Errorf("registry: decode: %w (corrupt file backed up to %s)", err, backup)
		}
		return nil, fmt.Errorf("registry: decode: %w", err)
	}
	if r.Services == nil {
		r.Services = make(map[string]string)
	}
	r.path = path
	return r, nil
}

// Save writes the registry to disk atomically.
func (r *Registry) Save() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.saveLocked()
}

func (r *Registry) saveLocked() error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("registry: encode: %w", err)
	}
	dir := filepath.Dir(r.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("registry: mkdir: %w", err)
	}
	// Atomic write (temp file + fsync + rename) via persist.WriteFileAtomic.
	// A torn registry.json mid-write would orphan every service's persisted
	// state — the runtime refuses to auto-generate replacement IDs
	// (runtime.go:613-614) so corruption is a hard stop.
	if err := persist.WriteFileAtomic(r.path, data, 0644); err != nil {
		return fmt.Errorf("registry: write: %w", err)
	}
	return nil
}

// Lookup returns the stored actorID for a service name, or empty string.
func (r *Registry) Lookup(serviceName string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Services[serviceName]
}

// Record stores the actorID for a service name and saves to disk.
func (r *Registry) Record(serviceName, actorID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Services[serviceName] = actorID
	return r.saveLocked()
}
