package project

import (
	"fmt"

	"github.com/qomos-w/gospore/actor"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// noGitMode returns the persisted no-git workflow flag by reading the config
// card. It is the read-side helper for PureContext handlers (no_git_mode_get,
// workflow_create_worktree). The card is the ground truth; there is no
// in-memory authoritative copy.
func (a *Actor) noGitMode() bool {
	v, err := a.noGitModeSnapshot()
	if err != nil {
		return false
	}
	return v
}

// setNoGitMode updates the config card's no-git workflow flag (thread-safe via
// configMu). The write is durable immediately; no separate Save() call is
// needed.
func (a *Actor) setNoGitMode(v bool) error {
	return a.updateConfigCard(func(c *configCard) {
		c.NoGitMode = v
	})
}

// handleNoGitModeGet reports the persisted no-git mode flag and whether the
// project root is an actual git repository. The two values together let the UI
// auto-suggest no-git mode for projects whose root has no git at all (the
// workflow_create_worktree skip triggers on either condition).
func (a *Actor) handleNoGitModeGet(_ actor.PureContext, _ gen.ProjectNoGitModeGetReq) (gen.ProjectNoGitModeGetResp, error) {
	root, err := a.rootPath()
	return gen.ProjectNoGitModeGetResp{
		NoGitMode:  a.noGitMode(),
		HasGitRepo: err == nil && isGitRepo(root),
	}, nil
}

// handleNoGitModeSet persists the no-git workflow flag through the config
// card. The write is durable immediately.
func (a *Actor) handleNoGitModeSet(_ actor.PureContext, req gen.ProjectNoGitModeSetReq) (gen.ProjectNoGitModeSetResp, error) {
	if err := a.setNoGitMode(req.NoGitMode); err != nil {
		return gen.ProjectNoGitModeSetResp{}, fmt.Errorf("project.no_git_mode_set: update config card: %w", err)
	}
	return gen.ProjectNoGitModeSetResp{NoGitMode: req.NoGitMode}, nil
}
