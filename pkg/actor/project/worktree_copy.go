package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/qomos-w/gospore/actor"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

const copyLogLimit = 512

// truncatePathList joins a slice of paths and caps the result at limit bytes
// with a visible marker, matching the openai respSnippet truncation convention
// used elsewhere in the project.
func truncatePathList(paths []string, limit int) string {
	s := strings.Join(paths, ", ")
	if len(s) > limit {
		s = s[:limit] + "...(truncated)"
	}
	return s
}

// handleWorktreeCopy copies files from the main repo root into the caller's
// bound worktree. Sources are relative to the main repo root; DestDir is
// relative to the worktree root and defaults to the worktree root itself.
// Existing destination files are only overwritten when Force=true.
func (a *Actor) handleWorktreeCopy(ctx actor.PureContext, req gen.ProjectWorktreeCopyReq) (gen.ProjectWorktreeCopyResp, error) {
	if req.WorktreeID == "" {
		return gen.ProjectWorktreeCopyResp{}, fmt.Errorf("project.worktree_copy: WorktreeID required")
	}
	if len(req.Sources) == 0 {
		return gen.ProjectWorktreeCopyResp{}, fmt.Errorf("project.worktree_copy: Sources required")
	}

	cid := callerID(ctx)
	if cid == "" {
		return gen.ProjectWorktreeCopyResp{}, fmt.Errorf("project.worktree_copy: no caller identity")
	}
	state, wtPath := a.classifyBinding(cid)
	if state != bindingActive {
		return gen.ProjectWorktreeCopyResp{}, fmt.Errorf("project.worktree_copy: caller is not bound to an active worktree")
	}
	a.worktreeParentMu.RLock()
	wt, ok := a.worktrees[req.WorktreeID]
	a.worktreeParentMu.RUnlock()
	if !ok {
		return gen.ProjectWorktreeCopyResp{}, fmt.Errorf("project.worktree_copy: worktree %q not found", req.WorktreeID)
	}
	if wt.Status != "active" {
		return gen.ProjectWorktreeCopyResp{}, fmt.Errorf("project.worktree_copy: worktree %q is %s", req.WorktreeID, wt.Status)
	}
	if wt.Path != wtPath {
		return gen.ProjectWorktreeCopyResp{}, fmt.Errorf("project.worktree_copy: caller bound to a different worktree")
	}

	mainRoot, err := a.rootPath()
	if err != nil {
		return gen.ProjectWorktreeCopyResp{}, fmt.Errorf("project.worktree_copy: %w", err)
	}

	if req.DestDir != "" {
		clean := filepath.Clean(req.DestDir)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return gen.ProjectWorktreeCopyResp{}, fmt.Errorf("project.worktree_copy: DestDir escapes worktree")
		}
	}

	ctx.Logger().Info("project.worktree_copy: starting",
		"worktreeId", req.WorktreeID,
		"sources", truncatePathList(req.Sources, copyLogLimit),
		"destDir", req.DestDir,
		"force", req.Force)

	resp := gen.ProjectWorktreeCopyResp{
		Copied:  make([]gen.ProjectWorktreeCopyResult, 0, len(req.Sources)),
		Skipped: make([]gen.ProjectWorktreeCopySkipped, 0),
	}
	for _, src := range req.Sources {
		copied, skipped, err := a.copyOneSource(mainRoot, wtPath, src, req.DestDir, req.Force)
		if err != nil {
			return resp, fmt.Errorf("project.worktree_copy: source %q: %w", src, err)
		}
		if skipped != nil {
			resp.Skipped = append(resp.Skipped, *skipped)
			continue
		}
		resp.Copied = append(resp.Copied, *copied)
	}

	ctx.Logger().Info("project.worktree_copy: completed",
		"worktreeId", req.WorktreeID,
		"copied", len(resp.Copied),
		"skipped", len(resp.Skipped))
	return resp, nil
}

// copyOneSource handles a single source path. Only regular files are copied;
// non-regular sources are skipped with a descriptive reason.
func (a *Actor) copyOneSource(mainRoot, wtRoot, src, destBase string, force bool) (*gen.ProjectWorktreeCopyResult, *gen.ProjectWorktreeCopySkipped, error) {
	absSource, err := validateCopySource(mainRoot, src)
	if err != nil {
		return nil, &gen.ProjectWorktreeCopySkipped{Source: src, Reason: err.Error()}, nil
	}

	fi, err := os.Stat(absSource)
	if err != nil {
		return nil, &gen.ProjectWorktreeCopySkipped{Source: src, Reason: fmt.Sprintf("cannot stat source: %v", err)}, nil
	}
	if !fi.Mode().IsRegular() {
		return nil, &gen.ProjectWorktreeCopySkipped{Source: src, Reason: "source is not a regular file"}, nil
	}

	destRel := copyDestRel(src, destBase)
	if err := validateCopyDest(wtRoot, destRel); err != nil {
		return nil, &gen.ProjectWorktreeCopySkipped{Source: src, Reason: fmt.Sprintf("destination validation failed: %v", err)}, nil
	}

	if !force {
		safe, err := checkOverwriteSafe(mainRoot, wtRoot, destRel)
		if err != nil {
			return nil, &gen.ProjectWorktreeCopySkipped{Source: src, Reason: fmt.Sprintf("overwrite check failed: %v", err)}, nil
		}
		if !safe {
			return nil, &gen.ProjectWorktreeCopySkipped{Source: src, Reason: "destination exists or has diverged (use Force=true)"}, nil
		}
	}

	data, err := os.ReadFile(absSource)
	if err != nil {
		return nil, nil, fmt.Errorf("read source %q: %w", absSource, err)
	}

	destAbs := filepath.Join(wtRoot, destRel)
	if err := os.MkdirAll(filepath.Dir(destAbs), 0755); err != nil {
		return nil, nil, fmt.Errorf("create destination directory for %q: %w", destAbs, err)
	}

	overwritten := false
	if _, err := os.Stat(destAbs); err == nil {
		overwritten = true
	}

	if err := persist.WriteFileAtomic(destAbs, data, fi.Mode().Perm()); err != nil {
		return nil, nil, fmt.Errorf("write destination %q: %w", destAbs, err)
	}

	return &gen.ProjectWorktreeCopyResult{
		Source:      src,
		Dest:        destRel,
		Overwritten: overwritten,
	}, nil, nil
}

// copyDestRel maps a source path (relative to the main repo root) to a
// destination path relative to the worktree root. When destBase is non-empty
// the source's base name is placed under destBase; otherwise the source's
// relative path is preserved.
func copyDestRel(src, destBase string) string {
	if destBase == "" {
		return src
	}
	return filepath.Join(destBase, filepath.Base(src))
}
