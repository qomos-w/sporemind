package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// worktreeManifest is the per-worktree ground-truth state file. It carries the
// worktree metadata plus the set of agents bound to this worktree (agentActorID
// → parentAgentID, parent empty for autonomous owners). The file is the
// authoritative source of truth; the Actor's in-memory worktrees /
// agentWorktree / agentParent maps are a non-authoritative write-through cache
// rebuilt from these manifests on restart.
type worktreeManifest struct {
	gen.ProjectWorktree
	// Agents maps each agentActorID bound to this worktree to its parent
	// agentActorID (empty for an autonomous owner). Stored in the worktree's
	// manifest so bindings and parentage survive restarts and are queryable
	// from disk without an in-memory authoritative copy.
	Agents map[string]string `json:"agents,omitempty"`
}

// manifestStoreBase returns the directory holding per-worktree manifest files.
// Each worktree manifest is stored as <base>/<id>.json via
// persist.Persist (FSPersist), co-located with the worktree checkouts under
// the project actor's settings directory so it is removed when the actor is
// destroyed. Manifest files live OUTSIDE the git checkout directories so they
// never dirty a worktree's git status.
func (a *Actor) manifestStoreBase() string {
	return filepath.Join(a.worktreesStateDir(), "manifests")
}

// manifestStore returns a persist.Persist for per-worktree manifest files,
// rooted at manifestStoreBase. Each manifest is keyed by worktree ID.
func (a *Actor) manifestStore() persist.Persist {
	return persist.NewFSPersist(a.manifestStoreBase())
}

// persistWorktreeManifest writes the ground-truth manifest for wtID from the
// in-memory cache. Called after every mutation that changes a worktree's
// metadata or its bound agents. The snapshot is taken under the cache locks so
// concurrent PureContext reads cannot observe a half-written copy; the file
// write happens after the locks are released. Returns nil when the worktree
// is absent from the cache (in which case the manifest is deleted).
func (a *Actor) persistWorktreeManifest(wtID string) error {
	a.worktreeParentMu.RLock()
	wt, ok := a.worktrees[wtID]
	if !ok {
		a.worktreeParentMu.RUnlock()
		// Worktree is gone from cache; ensure no stale state remains.
		return a.deleteWorktreeState(wtID)
	}
	a.bindingMu.RLock()
	agents := make(map[string]string, 4)
	for agent, wid := range a.agentWorktree {
		if wid == wtID {
			agents[agent] = a.agentParent[agent]
		}
	}
	a.bindingMu.RUnlock()
	a.worktreeParentMu.RUnlock()
	m := worktreeManifest{ProjectWorktree: wt, Agents: agents}
	return a.manifestStore().Save(wtID, m)
}

// deleteWorktreeManifest removes the on-disk manifest file for wtID. The
// in-memory cache must be updated separately by the caller.
func (a *Actor) deleteWorktreeManifest(wtID string) error {
	return a.manifestStore().Delete(wtID)
}

// deleteWorktreeState removes every piece of per-worktree ground truth: the
// manifest file and the config card's worktree-scoped protected-files bucket
// (if any). Every worktree-removal path goes through here so a deleted
// worktree never leaves a stale protected-files bucket behind.
func (a *Actor) deleteWorktreeState(wtID string) error {
	if err := a.deleteWorktreeManifest(wtID); err != nil {
		return err
	}
	return a.clearWorktreeProtectedFiles(wtID)
}

// clearWorktreeProtectedFiles drops the worktree-scoped protected-files
// bucket for wtID from the config card. No-op (no card write) when no bucket
// exists, so worktrees that never ran a scoped dev_generate stay free.
func (a *Actor) clearWorktreeProtectedFiles(wtID string) error {
	c, err := a.configSnapshot()
	if err != nil {
		return err
	}
	if _, ok := c.ProtectedFilesByWorktree[wtID]; !ok {
		return nil
	}
	return a.updateConfigCard(func(c *configCard) {
		delete(c.ProtectedFilesByWorktree, wtID)
	})
}

// persistWorktreeManifests persists manifests for the given worktree IDs,
// joining any errors. Best-effort helper for multi-write paths (e.g. marking
// an owner worktree and its children stale).
func (a *Actor) persistWorktreeManifests(ids []string) error {
	var errs []error
	for _, id := range ids {
		if err := a.persistWorktreeManifest(id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// persistAllWorktreeManifests rewrites every known worktree's manifest from
// the in-memory cache. Used after bulk mutations (startup reconcile) where
// tracking individual IDs is not worth the complexity; the set of worktrees
// is small so this stays cheap.
func (a *Actor) persistAllWorktreeManifests() error {
	a.worktreeParentMu.RLock()
	ids := make([]string, 0, len(a.worktrees))
	for id := range a.worktrees {
		ids = append(ids, id)
	}
	a.worktreeParentMu.RUnlock()
	return a.persistWorktreeManifests(ids)
}

// staleWorktreeIDs returns wtID plus every direct child currently marked
// stale under it — the set whose manifests must be rewritten after a
// subtree-wide stale transition (markChildWorktreesStale).
func (a *Actor) staleWorktreeIDs(parentID string) []string {
	a.worktreeParentMu.RLock()
	defer a.worktreeParentMu.RUnlock()
	ids := []string{parentID}
	for id, wt := range a.worktrees {
		if wt.ParentWorktreeID == parentID && wt.Status == "stale" {
			ids = append(ids, id)
		}
	}
	return ids
}

// loadWorktreeCache rebuilds the in-memory worktrees / agentWorktree /
// agentParent maps from the per-worktree manifest files on disk. The maps are a
// non-authoritative cache; the manifest files are the ground truth. Called on
// startup (OnInit) and whenever the cache must be resynced from disk.
func (a *Actor) loadWorktreeCache() error {
	// Migration: if the legacy worktrees-state.json exists, import it into
	// per-worktree manifests then remove the legacy file so future starts use
	// manifests only.
	if err := a.migrateLegacyWorktreesState(); err != nil {
		// Non-fatal: continue with whatever manifests exist.
	}
	store := a.manifestStore()
	// Enumerate manifests through the Lister capability: the manifest store
	// is FSPersist, whose document layout (<id>.json) names each manifest by
	// worktree ID with the extension stripped. Listing via the interface
	// (rather than os.ReadDir + IsDir) keeps this correct regardless of the
	// on-disk layout and skips transient/corrupt artifacts.
	lister, ok := store.(persist.Lister)
	if !ok {
		return fmt.Errorf("project: manifest store %T does not implement persist.Lister", store)
	}
	names, err := lister.List("")
	if err != nil {
		return fmt.Errorf("project: list manifests: %w", err)
	}
	wts := make(map[string]gen.ProjectWorktree, len(names))
	bindings := make(map[string]string, 4)
	parents := make(map[string]string, 4)
	for _, id := range names {
		if id == "" {
			continue
		}
		var m worktreeManifest
		if err := store.Load(id, &m); err != nil {
			continue // skip unreadable manifests
		}
		if m.ID == "" {
			m.ID = id
		}
		wts[m.ID] = m.ProjectWorktree
		for agent, parent := range m.Agents {
			bindings[agent] = m.ID
			if parent != "" {
				parents[agent] = parent
			}
		}
	}
	a.worktreeParentMu.Lock()
	a.bindingMu.Lock()
	a.worktrees = wts
	a.agentWorktree = bindings
	a.agentParent = parents
	a.bindingMu.Unlock()
	a.worktreeParentMu.Unlock()
	return nil
}

// migrateLegacyWorktreesState imports the pre-migration worktrees-state.json
// (a single bulk file written via persist.WriteFileAtomic) into per-worktree
// manifest files, then removes the legacy file. Idempotent: a no-op when the
// legacy file is absent.
func (a *Actor) migrateLegacyWorktreesState() error {
	legacy := filepath.Join(a.worktreesStateDir(), "worktrees-state.json")
	data, err := os.ReadFile(legacy)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("project: read legacy worktrees state: %w", err)
	}
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	if len(trimmed) > 0 && trimmed[0] == '[' {
		// Legacy bare-array format: []ProjectWorktree
		var list []gen.ProjectWorktree
		if err := json.Unmarshal(data, &list); err != nil {
			return fmt.Errorf("project: unmarshal legacy worktrees array: %w", err)
		}
		for _, wt := range list {
			if wt.ID == "" {
				continue
			}
			m := worktreeManifest{ProjectWorktree: wt}
			if err := a.manifestStore().Save(wt.ID, m); err != nil {
				return fmt.Errorf("project: migrate legacy worktree %s: %w", wt.ID, err)
			}
		}
	} else {
		// Envelope format: {"worktrees": [...], "agentWorktreeBindings": {...},
		// "agentParents": {...}}
		var env struct {
			Worktrees             []gen.ProjectWorktree `json:"worktrees"`
			AgentWorktreeBindings map[string]string     `json:"agentWorktreeBindings,omitempty"`
			AgentParents          map[string]string     `json:"agentParents,omitempty"`
		}
		if err := json.Unmarshal(data, &env); err != nil {
			return fmt.Errorf("project: unmarshal legacy worktrees envelope: %w", err)
		}
		perWt := make(map[string]map[string]string, 8)
		for agent, wid := range env.AgentWorktreeBindings {
			if wid == "" {
				continue
			}
			if perWt[wid] == nil {
				perWt[wid] = make(map[string]string, 2)
			}
			perWt[wid][agent] = env.AgentParents[agent]
		}
		for _, wt := range env.Worktrees {
			if wt.ID == "" {
				continue
			}
			m := worktreeManifest{ProjectWorktree: wt, Agents: perWt[wt.ID]}
			if err := a.manifestStore().Save(wt.ID, m); err != nil {
				return fmt.Errorf("project: migrate legacy worktree %s: %w", wt.ID, err)
			}
		}
	}
	return os.Remove(legacy)
}