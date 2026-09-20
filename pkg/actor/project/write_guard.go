package project

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// setProtectedFiles replaces the full protected-file set for the caller's
// scope. An empty list clears that scope's protections (the project has been
// regenerated or the generator no longer produces this project's files).
//
// Scope: appmanager.dev_generate forwards its turn-engine-injected
// CallerAgentId. A caller bound to an active worktree writes that worktree's
// bucket (configCard.ProtectedFilesByWorktree[worktreeID]) so a worktree-side
// codegen with a divergent .appdef cannot clobber the main tree's global
// list — and a main-tree regen cannot clobber the worktree's view. Unbound /
// human / internal callers keep writing the global list, exactly as before.
// This callable is called by dev_generate after a successful codegen run.
func (a *Actor) setProtectedFiles(_ actor.PureContext, req gen.SetProtectedFilesReq) (gen.SetProtectedFilesResp, error) {
	files := make([]string, 0, len(req.Files))
	for _, f := range req.Files {
		files = append(files, filepath.Clean(f))
	}
	wtID, scoped, err := a.protectedScope(strings.TrimSpace(req.CallerAgentID))
	if err != nil {
		return gen.SetProtectedFilesResp{}, fmt.Errorf("project.set_protected_files: %w", err)
	}
	var count int
	err = a.updateConfigCard(func(c *configCard) {
		if scoped {
			if len(files) == 0 {
				delete(c.ProtectedFilesByWorktree, wtID)
			} else {
				if c.ProtectedFilesByWorktree == nil {
					c.ProtectedFilesByWorktree = make(map[string][]string, 1)
				}
				c.ProtectedFilesByWorktree[wtID] = files
			}
		} else {
			c.ProtectedFiles = files
		}
		count = len(files)
	})
	if err != nil {
		return gen.SetProtectedFilesResp{}, fmt.Errorf("project.set_protected_files: %w", err)
	}
	return gen.SetProtectedFilesResp{Count: int64(count)}, nil
}

// protectedScope resolves which protected-files scope a set_protected_files
// call targets: the global list (unbound / human / internal callers) or the
// bucket of the worktree the caller agent is bound to. A caller bound to a
// missing or inactive worktree is an error — writing the global list from a
// worktree-side codegen would silently escape the isolation fence into
// main-tree constraint state.
func (a *Actor) protectedScope(callerAgentID string) (wtID string, scoped bool, err error) {
	if callerAgentID == "" {
		return "", false, nil
	}
	a.bindingMu.RLock()
	id, ok := a.agentWorktree[callerAgentID]
	a.bindingMu.RUnlock()
	if !ok {
		return "", false, nil
	}
	a.worktreeParentMu.RLock()
	wt, exists := a.worktrees[id]
	a.worktreeParentMu.RUnlock()
	if !exists || wt.Status != "active" {
		return "", false, fmt.Errorf("caller %s is bound to worktree %s which is not active; exit the worktree before regenerating", callerAgentID, id)
	}
	return id, true, nil
}

// protectedPathsFor returns the protected relPaths that apply to callerID:
// the global list plus, when the caller is bound to an active worktree, that
// worktree's bucket. The union is intentional and fail-closed: main-list
// entries denote project-generated artifacts, and the same relPath inside a
// worktree branched from main is the same artifact until the branch's own
// regen re-buckets the view. Stale-binding callers get just the global list;
// their file operations are rejected upstream before this guard matters.
func (a *Actor) protectedPathsFor(callerID string) []string {
	c, err := a.configSnapshot()
	if err != nil {
		return nil
	}
	paths := c.ProtectedFiles
	if callerID == "" {
		return paths
	}
	a.bindingMu.RLock()
	wtID, ok := a.agentWorktree[callerID]
	a.bindingMu.RUnlock()
	if !ok {
		return paths
	}
	a.worktreeParentMu.RLock()
	wt, exists := a.worktrees[wtID]
	a.worktreeParentMu.RUnlock()
	if !exists || wt.Status != "active" {
		return paths
	}
	if bucket := c.ProtectedFilesByWorktree[wtID]; len(bucket) > 0 {
		// c came from configSnapshot, so c.ProtectedFiles is already a
		// private copy; appending cannot corrupt card state.
		paths = append(paths, bucket...)
	}
	return paths
}

// isProtected checks whether a resolved absolute path corresponds to a
// protected generated file for this caller. rootPath is the effective root
// (the caller's worktree root when bound, else the main root); relPath is
// the relative path within that root. The applicable protected set is read
// fresh from the config card on every call.
func (a *Actor) isProtected(callerID, rootPath, relPath string) (string, bool) {
	clean := filepath.Clean(relPath)
	for _, p := range a.protectedPathsFor(callerID) {
		if p == clean {
			return clean, true
		}
	}
	return "", false
}
