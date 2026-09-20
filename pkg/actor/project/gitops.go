package project

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
)

func openGitRepo(path string) (*git.Repository, error) {
	// EnableDotGitCommonDir is required for linked worktrees: the worktree's
	// HEAD lives under .git/worktrees/<id>/HEAD but the branch refs it points
	// to (refs/heads/...) are in the common .git directory. Without this flag
	// PlainOpen only sees the per-worktree .git dir and returns "reference
	// not found" when trying to resolve HEAD → refs/heads/<branch>.
	return git.PlainOpenWithOptions(path, &git.PlainOpenOptions{EnableDotGitCommonDir: true})
}

// gitRootFor resolves the repository directory a git callable operates on.
//
// Repo "" (default): the caller's bound worktree when attached (spawn
// confinement), else the main repo root; stale bindings fail closed via
// rootPathFor.
//
// Repo "main": the main repo root directly — the workflow owner's recovery
// channel. A bound owner uses it to inspect and stash the main repo when
// workflow_stop reports uncommitted blockers, then retries. Restricted to
// root agents (no agentParent record): spawned workers must never escape
// their worktree sandbox. A stale worktree binding does not block this
// scope — recovery must stay available precisely when the binding is broken.
func (a *Actor) gitRootFor(cid, repo, callable string) (string, error) {
	switch repo {
	case "":
		return a.rootPathFor(cid)
	case "main":
		if cid != "" {
			a.worktreeParentMu.RLock()
			parent := a.agentParent[cid]
			a.worktreeParentMu.RUnlock()
			if parent != "" {
				return "", fmt.Errorf("%s: repo \"main\" is restricted to root agents (caller is a spawned child of %s)", callable, parent)
			}
		}
		return a.rootPath()
	default:
		return "", fmt.Errorf("%s: unknown Repo %q (want \"\" or \"main\")", callable, repo)
	}
}

func parentHashes(c *object.Commit) []string {
	out := make([]string, 0, len(c.ParentHashes))
	for _, h := range c.ParentHashes {
		out = append(out, h.String())
	}
	return out
}

func (a *Actor) handleGitLog(ctx actor.PureContext, req domain.ProjectGitLogReq) (domain.ProjectGitLogResp, error) {
	root, err := a.gitRootFor(callerID(ctx), req.Repo, "project.git_log")
	if err != nil {
		return domain.ProjectGitLogResp{}, err
	}
	repo, err := openGitRepo(root)
	if err != nil {
		return domain.ProjectGitLogResp{}, err
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}

	iter, err := repo.Log(&git.LogOptions{Order: git.LogOrderCommitterTime})
	if err != nil {
		return domain.ProjectGitLogResp{}, err
	}
	defer iter.Close()

	var commits []domain.ProjectGitCommitInfo
	for i := int64(0); i < int64(limit); i++ {
		commit, err := iter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return domain.ProjectGitLogResp{}, err
		}
		commits = append(commits, domain.ProjectGitCommitInfo{
			Hash:    commit.Hash.String(),
			Short:   commit.Hash.String()[:7],
			Message: commit.Message,
			Author:  commit.Author.Name,
			Email:   commit.Author.Email,
			When:    fmt.Sprintf("%d", commit.Author.When.Unix()),
			Parents: parentHashes(commit),
		})
	}
	return domain.ProjectGitLogResp{Commits: commits}, nil
}

func (a *Actor) handleGitDiff(ctx actor.PureContext, req domain.ProjectGitDiffReq) (domain.ProjectGitDiffResp, error) {
	cid := callerID(ctx)
	done := ctx.Done()
	if req.Repo != "main" {
		var response domain.ProjectGitDiffResp
		used, err := a.worktreeDo(cid, func(root string) error {
			var innerErr error
			response, innerErr = a.handleGitDiffAtRoot(done, root, req)
			return innerErr
		})
		if used {
			return response, err
		}
	}
	root, err := a.gitRootFor(cid, req.Repo, "project.git_diff")
	if err != nil {
		return domain.ProjectGitDiffResp{}, err
	}
	return a.handleGitDiffAtRoot(done, root, req)
}

func (a *Actor) handleGitDiffAtRoot(done <-chan struct{}, root string, req domain.ProjectGitDiffReq) (domain.ProjectGitDiffResp, error) {
	repo, err := openGitRepo(root)
	if err != nil {
		return domain.ProjectGitDiffResp{}, err
	}

	if req.CommitHash != "" {
		// quotepath=false keeps non-ASCII paths literal in the diff header
		// (`diff --git a/中文 b/中文`) instead of octal-escaping them.
		args := []string{"-c", "core.quotepath=false", "diff", req.CommitHash, "--"}
		if req.FilePath != "" {
			args = append(args, req.FilePath)
		}
		ctx, cancel := gitCtx(done)
		defer cancel()
		cmd := gitCmd(ctx, root, args...)
		out, err := cmd.Output()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
				return domain.ProjectGitDiffResp{}, fmt.Errorf("git diff failed: %s", exitErr.Stderr)
			}
			return domain.ProjectGitDiffResp{}, fmt.Errorf("git diff failed: %w", err)
		}
		diff := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
		if len(diff) == 1 && diff[0] == "" {
			diff = []string{}
		}
		return domain.ProjectGitDiffResp{Diff: diff}, nil
	}

	// -z: NUL-terminated records, never quoted or escaped. Newline-mode
	// porcelain double-quotes non-ASCII paths (e.g. CJK filenames).
	statusOutput, err := gitRun(done, root, "status", "-z", "--porcelain")
	if err != nil {
		return domain.ProjectGitDiffResp{}, err
	}
	statusPaths := make([]string, 0)
	for _, r := range parseGitPorcelainZ(statusOutput) {
		path := r.path
		if path != "" {
			statusPaths = append(statusPaths, path)
		}
	}

	diff := make([]string, 0)
	for _, filePath := range statusPaths {
		if req.FilePath != "" && filePath != req.FilePath {
			continue
		}

		diff = append(diff, fmt.Sprintf("diff --git a/%s b/%s", filePath, filePath))

		idx, err := repo.Storer.Index()
		if err != nil {
			continue
		}
		var oldContent string
		entry, err := idx.Entry(filePath)
		if err == nil {
			blob, err := repo.BlobObject(entry.Hash)
			if err == nil {
				r, err := blob.Reader()
				if err == nil {
					data, _ := io.ReadAll(r)
					r.Close()
					oldContent = string(data)
				}
			}
		}

		newPath := filepath.Join(root, filePath)
		newData, err := os.ReadFile(newPath)
		if err != nil {
			continue
		}
		newContent := string(newData)

		diff = append(diff, unifiedDiff(oldContent, newContent, filePath, filePath)...)
	}
	return domain.ProjectGitDiffResp{Diff: diff}, nil
}

func (a *Actor) handleGitAdd(ctx actor.PureContext, req domain.ProjectGitAddReq) error {
	cid := callerID(ctx)
	done := ctx.Done()
	used, err := a.worktreeDo(cid, func(wtPath string) error {
		if len(req.Paths) == 0 {
			return nil
		}
		args := append([]string{"add", "--"}, req.Paths...)
		if _, err := gitRun(done, wtPath, args...); err != nil {
			return fmt.Errorf("project.git_add: %w", err)
		}
		return nil
	})
	if used {
		return err
	}
	if err := a.denyUnboundSpawnChild("project.git_add", cid); err != nil {
		return err
	}
	root, err := a.rootPathFor(cid)
	if err != nil {
		return err
	}
	if len(req.Paths) == 0 {
		return nil
	}
	// CLI over go-git: go-git's Worktree.Add runs a full-worktree Status()
	// per call (measured ~6s on this repo), while `git add` completes in ~30ms.
	args := append([]string{"add", "--"}, req.Paths...)
	if _, err := gitRun(done, root, args...); err != nil {
		return fmt.Errorf("project.git_add: %w", err)
	}
	return nil
}

func (a *Actor) handleGitCommit(ctx actor.PureContext, req domain.ProjectGitCommitReq) (domain.ProjectGitCommitResp, error) {
	cid := callerID(ctx)
	done := ctx.Done()
	var wtResp domain.ProjectGitCommitResp
	used, err := a.worktreeDo(cid, func(wtPath string) error {
		msg := req.Message
		if msg == "" {
			msg = "worktree commit"
		}
		if _, err := gitRun(done, wtPath, "commit", "-m", msg); err != nil {
			return fmt.Errorf("project.git_commit: %w", err)
		}
		out, err := gitRun(done, wtPath, "rev-parse", "HEAD")
		if err != nil {
			return fmt.Errorf("project.git_commit: rev-parse: %w", err)
		}
		hash := strings.TrimSpace(out)
		short := hash
		if len(short) > 7 {
			short = short[:7]
		}
		wtResp = domain.ProjectGitCommitResp{Hash: hash, Short: short}
		return nil
	})
	if used {
		return wtResp, err
	}
	if err := a.denyUnboundSpawnChild("project.git_commit", cid); err != nil {
		return domain.ProjectGitCommitResp{}, err
	}
	root, err := a.rootPathFor(cid)
	if err != nil {
		return domain.ProjectGitCommitResp{}, err
	}
	repo, err := openGitRepo(root)
	if err != nil {
		return domain.ProjectGitCommitResp{}, err
	}
	worktree, err := repo.Worktree()
	if err != nil {
		return domain.ProjectGitCommitResp{}, err
	}
	hash, err := worktree.Commit(req.Message, &git.CommitOptions{})
	if err != nil {
		return domain.ProjectGitCommitResp{}, fmt.Errorf("project.git_commit: %w", err)
	}
	return domain.ProjectGitCommitResp{
		Hash:  hash.String(),
		Short: hash.String()[:7],
	}, nil
}

func (a *Actor) handleGitPush(ctx actor.PureContext, req domain.ProjectGitPushReq) error {
	cid := callerID(ctx)
	if err := a.denyUnboundSpawnChild("project.git_push", cid); err != nil {
		return err
	}
	root, err := a.rootPathFor(cid)
	if err != nil {
		return err
	}
	repo, err := openGitRepo(root)
	if err != nil {
		return err
	}
	remote := req.Remote
	if remote == "" {
		remote = "origin"
	}
	// go-git PushOptions has no Context field, so enforce the timeout
	// with a goroutine bounded by gitCtx.
	gctx, cancel := gitCtx(ctx.Done())
	defer cancel()
	type pushResult struct{ err error }
	resCh := make(chan pushResult, 1)
	go func() { resCh <- pushResult{repo.Push(&git.PushOptions{RemoteName: remote})} }()
	select {
	case r := <-resCh:
		if r.err != nil {
			return fmt.Errorf("project.git_push: %w", r.err)
		}
		return nil
	case <-gctx.Done():
		return fmt.Errorf("project.git_push: timed out after %s", gitTimeout)
	}
}

func (a *Actor) handleGitPull(ctx actor.PureContext, req domain.ProjectGitPullReq) error {
	cid := callerID(ctx)
	done := ctx.Done()
	used, err := a.worktreeDo(cid, func(wtPath string) error {
		remote := req.Remote
		if remote == "" {
			remote = "origin"
		}
		if _, err := gitRun(done, wtPath, "pull", remote); err != nil {
			return fmt.Errorf("project.git_pull: %w", err)
		}
		return nil
	})
	if used {
		return err
	}
	if err := a.denyUnboundSpawnChild("project.git_pull", cid); err != nil {
		return err
	}
	root, err := a.rootPathFor(cid)
	if err != nil {
		return err
	}
	repo, err := openGitRepo(root)
	if err != nil {
		return err
	}
	worktree, err := repo.Worktree()
	if err != nil {
		return err
	}
	remote := req.Remote
	if remote == "" {
		remote = "origin"
	}
	// go-git PullOptions has no Context field, so enforce the timeout
	// with a goroutine bounded by gitCtx.
	gctx, cancel := gitCtx(done)
	defer cancel()
	type pullResult struct{ err error }
	resCh := make(chan pullResult, 1)
	go func() { resCh <- pullResult{worktree.Pull(&git.PullOptions{RemoteName: remote})} }()
	select {
	case r := <-resCh:
		if r.err != nil {
			return fmt.Errorf("project.git_pull: %w", r.err)
		}
		return nil
	case <-gctx.Done():
		return fmt.Errorf("project.git_pull: timed out after %s", gitTimeout)
	}
}

func (a *Actor) handleGitBranch(ctx actor.PureContext, req domain.ProjectGitBranchReq) (domain.ProjectGitBranchResp, error) {
	root, err := a.rootPathFor(callerID(ctx))
	if err != nil {
		return domain.ProjectGitBranchResp{}, err
	}
	repo, err := openGitRepo(root)
	if err != nil {
		return domain.ProjectGitBranchResp{}, err
	}

	headRef, err := repo.Head()
	if err != nil {
		return domain.ProjectGitBranchResp{}, err
	}

	iter, err := repo.References()
	if err != nil {
		return domain.ProjectGitBranchResp{}, err
	}
	defer iter.Close()

	var branches []domain.ProjectGitBranchInfo
	for {
		ref, err := iter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return domain.ProjectGitBranchResp{}, err
		}
		if ref.Type() != plumbing.HashReference {
			continue
		}
		name := ref.Name()
		isRemote := name.IsRemote()
		if !name.IsBranch() && !isRemote {
			continue
		}
		short := name.Short()
		hash := ref.Hash().String()
		if len(hash) > 7 {
			hash = hash[:7]
		}
		remote := ""
		if isRemote {
			if idx := strings.IndexByte(short, '/'); idx >= 0 {
				remote = short[:idx]
			}
		}
		branches = append(branches, domain.ProjectGitBranchInfo{
			Name:     short,
			Current:  name == headRef.Name(),
			Hash:     hash,
			IsRemote: isRemote,
			Remote:   remote,
		})
	}
	return domain.ProjectGitBranchResp{Branches: branches}, nil
}

func (a *Actor) handleGitCheckout(ctx actor.PureContext, req domain.ProjectGitCheckoutReq) error {
	cid := callerID(ctx)
	done := ctx.Done()
	used, err := a.worktreeDo(cid, func(wtPath string) error {
		args := []string{"checkout"}
		if req.Create {
			args = append(args, "-b")
		}
		args = append(args, req.Branch)
		if _, err := gitRun(done, wtPath, args...); err != nil {
			return fmt.Errorf("project.git_checkout: %w", err)
		}
		return nil
	})
	if used {
		return err
	}
	if err := a.denyUnboundSpawnChild("project.git_checkout", cid); err != nil {
		return err
	}
	root, err := a.rootPathFor(cid)
	if err != nil {
		return err
	}
	repo, err := openGitRepo(root)
	if err != nil {
		return err
	}
	worktree, err := repo.Worktree()
	if err != nil {
		return err
	}
	branchRef := plumbing.NewBranchReferenceName(req.Branch)
	if err := worktree.Checkout(&git.CheckoutOptions{
		Branch: branchRef,
		Create: req.Create,
		Force:  false,
	}); err != nil {
		return fmt.Errorf("project.git_checkout: %w", err)
	}
	return nil
}

// ── additional git helpers, not exposed as agent callables by default ──

func (a *Actor) handleGitReset(ctx actor.PureContext, req domain.ProjectGitResetReq) error {
	cid := callerID(ctx)
	if err := a.denyUnboundSpawnChild("project.git_reset", cid); err != nil {
		return err
	}
	root, err := a.rootPathFor(cid)
	if err != nil {
		return err
	}
	if _, err := openGitRepo(root); err != nil {
		return err
	}
	gctx, cancel := gitCtx(ctx.Done())
	defer cancel()
	if len(req.Paths) == 0 {
		cmd := gitCmd(gctx, root, "reset", "HEAD")
		return cmd.Run()
	}
	for _, p := range req.Paths {
		cmd := gitCmd(gctx, root, "reset", "HEAD", "--", p)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("project.git_reset: reset %q failed: %w", p, err)
		}
	}
	return nil
}

func (a *Actor) handleGitStashSave(ctx actor.PureContext, req domain.ProjectGitStashSaveReq) error {
	cid := callerID(ctx)
	if err := a.denyUnboundSpawnChild("project.git_stash_save", cid); err != nil {
		return err
	}
	root, err := a.gitRootFor(cid, req.Repo, "project.git_stash_save")
	if err != nil {
		return err
	}
	args := []string{"stash", "push", "-u"} // -u: workflow_stop blockers can be untracked files
	if req.Message != "" {
		args = append(args, "-m", req.Message)
	}
	gctx, cancel := gitCtx(ctx.Done())
	defer cancel()
	cmd := gitCmd(gctx, root, args...)
	return cmd.Run()
}

func (a *Actor) handleGitStashPop(ctx actor.PureContext, req domain.ProjectGitStashPopReq) error {
	cid := callerID(ctx)
	if err := a.denyUnboundSpawnChild("project.git_stash_pop", cid); err != nil {
		return err
	}
	root, err := a.gitRootFor(cid, req.Repo, "project.git_stash_pop")
	if err != nil {
		return err
	}
	args := []string{"stash", "pop"}
	if req.Index > 0 {
		args = append(args, fmt.Sprintf("stash@{%d}", req.Index))
	}
	gctx, cancel := gitCtx(ctx.Done())
	defer cancel()
	cmd := gitCmd(gctx, root, args...)
	return cmd.Run()
}

func (a *Actor) handleGitStashList(ctx actor.PureContext, req domain.ProjectGitStashListReq) (domain.ProjectGitStashListResp, error) {
	root, err := a.gitRootFor(callerID(ctx), req.Repo, "project.git_stash_list")
	if err != nil {
		return domain.ProjectGitStashListResp{}, err
	}
	gctx, cancel := gitCtx(ctx.Done())
	defer cancel()
	cmd := gitCmd(gctx, root, "stash", "list", "--format=%gd|%gs|%H|%at")
	out, err := cmd.Output()
	if err != nil {
		return domain.ProjectGitStashListResp{Stashes: nil}, nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var stashes []domain.ProjectGitStashInfo
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 4 {
			continue
		}
		idxStr := strings.TrimPrefix(parts[0], "stash@{")
		idxStr = strings.TrimSuffix(idxStr, "}")
		idx, _ := strconv.Atoi(idxStr)
		hash := parts[2]
		if len(hash) > 7 {
			hash = hash[:7]
		}
		stashes = append(stashes, domain.ProjectGitStashInfo{
			Index:   int32(idx),
			Message: parts[1],
			Hash:    hash,
			When:    parts[3],
		})
	}
	return domain.ProjectGitStashListResp{Stashes: stashes}, nil
}

func (a *Actor) handleGitStashDrop(ctx actor.PureContext, req domain.ProjectGitStashDropReq) error {
	cid := callerID(ctx)
	if err := a.denyUnboundSpawnChild("project.git_stash_drop", cid); err != nil {
		return err
	}
	root, err := a.rootPathFor(cid)
	if err != nil {
		return err
	}
	args := []string{"stash", "drop"}
	if req.Index > 0 {
		args = append(args, fmt.Sprintf("stash@{%d}", req.Index))
	}
	gctx, cancel := gitCtx(ctx.Done())
	defer cancel()
	cmd := gitCmd(gctx, root, args...)
	return cmd.Run()
}

func (a *Actor) handleGitRemoteList(ctx actor.PureContext, req domain.ProjectGitRemoteListReq) (domain.ProjectGitRemoteListResp, error) {
	root, err := a.rootPathFor(callerID(ctx))
	if err != nil {
		return domain.ProjectGitRemoteListResp{}, err
	}
	repo, err := openGitRepo(root)
	if err != nil {
		return domain.ProjectGitRemoteListResp{}, err
	}
	remotes, err := repo.Remotes()
	if err != nil {
		return domain.ProjectGitRemoteListResp{}, err
	}
	var out []domain.ProjectGitRemoteInfo
	for _, r := range remotes {
		out = append(out, domain.ProjectGitRemoteInfo{
			Name: r.Config().Name,
			Urls: r.Config().URLs,
		})
	}
	return domain.ProjectGitRemoteListResp{Remotes: out}, nil
}

func (a *Actor) handleGitRemoteAdd(ctx actor.PureContext, req domain.ProjectGitRemoteAddReq) error {
	cid := callerID(ctx)
	if err := a.denyUnboundSpawnChild("project.git_remote_add", cid); err != nil {
		return err
	}
	root, err := a.rootPathFor(cid)
	if err != nil {
		return err
	}
	repo, err := openGitRepo(root)
	if err != nil {
		return err
	}
	_, err = repo.CreateRemote(&gitconfig.RemoteConfig{
		Name: req.Name,
		URLs: []string{req.URL},
	})
	if err != nil {
		return fmt.Errorf("project.git_remote_add: %w", err)
	}
	return nil
}

func (a *Actor) handleGitRemoteRemove(ctx actor.PureContext, req domain.ProjectGitRemoteRemoveReq) error {
	cid := callerID(ctx)
	if err := a.denyUnboundSpawnChild("project.git_remote_remove", cid); err != nil {
		return err
	}
	root, err := a.rootPathFor(cid)
	if err != nil {
		return err
	}
	repo, err := openGitRepo(root)
	if err != nil {
		return err
	}
	if err := repo.DeleteRemote(req.Name); err != nil {
		return fmt.Errorf("project.git_remote_remove: %w", err)
	}
	return nil
}

func (a *Actor) handleGitBlame(ctx actor.PureContext, req domain.ProjectGitBlameReq) (domain.ProjectGitBlameResp, error) {
	root, err := a.rootPathFor(callerID(ctx))
	if err != nil {
		return domain.ProjectGitBlameResp{}, err
	}
	gctx, cancel := gitCtx(ctx.Done())
	defer cancel()
	cmd := gitCmd(gctx, root, "blame", "--porcelain", "--", req.FilePath)
	out, err := cmd.Output()
	if err != nil {
		return domain.ProjectGitBlameResp{}, fmt.Errorf("project.git_blame: %w", err)
	}
	var lines []domain.ProjectGitBlameLine
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 4 && len(fields[0]) == 40 {
			hash := fields[0][:7]
			lineNo, _ := strconv.Atoi(fields[2])
			lines = append(lines, domain.ProjectGitBlameLine{
				Hash:    hash,
				Line:    int32(lineNo),
				Content: strings.Join(fields[3:], " "),
			})
		}
	}
	return domain.ProjectGitBlameResp{Lines: lines}, nil
}

func (a *Actor) handleGitConfigGet(ctx actor.PureContext, req domain.ProjectGitConfigGetReq) (domain.ProjectGitConfigGetResp, error) {
	root, err := a.rootPathFor(callerID(ctx))
	if err != nil {
		return domain.ProjectGitConfigGetResp{}, err
	}
	gctx, cancel := gitCtx(ctx.Done())
	defer cancel()
	cmd := gitCmd(gctx, root, "config", "--get", req.Key)
	out, err := cmd.Output()
	if err != nil {
		return domain.ProjectGitConfigGetResp{}, nil
	}
	return domain.ProjectGitConfigGetResp{Value: strings.TrimSpace(string(out))}, nil
}

func (a *Actor) handleGitConfigSet(ctx actor.PureContext, req domain.ProjectGitConfigSetReq) error {
	cid := callerID(ctx)
	if err := a.denyUnboundSpawnChild("project.git_config_set", cid); err != nil {
		return err
	}
	root, err := a.rootPathFor(cid)
	if err != nil {
		return err
	}
	repo, err := openGitRepo(root)
	if err != nil {
		return err
	}
	cfg, err := repo.Config()
	if err != nil {
		return err
	}
	if cfg.Raw == nil {
		cfg.Raw = gitconfig.NewConfig().Raw
	}
	parts := strings.SplitN(req.Key, ".", 2)
	if len(parts) != 2 {
		return fmt.Errorf("project.git_config_set: invalid config key %q: expected section.key format", req.Key)
	}
	section := parts[0]
	option := parts[1]
	cfg.Raw.SetOption(section, "", option, req.Value)
	if req.Global {
		return fmt.Errorf("project.git_config_set: global config not supported")
	}
	if err := repo.SetConfig(cfg); err != nil {
		return fmt.Errorf("project.git_config_set: %w", err)
	}
	return nil
}

// lineDiffOp represents a single line-level diff operation.
type lineDiffOp struct {
	kind    string
	oldLine int
	newLine int
	text    string
}

// unifiedDiff generates a unified-diff string slice from two text contents.
func unifiedDiff(oldContent, newContent, oldFile, newFile string) []string {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")
	if len(oldLines) > 0 && oldLines[len(oldLines)-1] == "" {
		oldLines = oldLines[:len(oldLines)-1]
	}
	if len(newLines) > 0 && newLines[len(newLines)-1] == "" {
		newLines = newLines[:len(newLines)-1]
	}

	ops := lcsDiff(oldLines, newLines)

	var result []string
	result = append(result, fmt.Sprintf("--- a/%s", oldFile))
	result = append(result, fmt.Sprintf("+++ b/%s", newFile))

	var changeIdx []int
	for i, op := range ops {
		if op.kind != "eq" {
			changeIdx = append(changeIdx, i)
		}
	}
	if len(changeIdx) == 0 {
		return result
	}

	const ctx = 3
	var groups [][]int
	var cur []int
	for _, idx := range changeIdx {
		if len(cur) == 0 || idx-cur[len(cur)-1] <= 2*ctx {
			cur = append(cur, idx)
		} else {
			groups = append(groups, cur)
			cur = []int{idx}
		}
	}
	if len(cur) > 0 {
		groups = append(groups, cur)
	}

	for _, grp := range groups {
		start := grp[0] - ctx
		if start < 0 {
			start = 0
		}
		end := grp[len(grp)-1] + ctx
		if end >= len(ops) {
			end = len(ops) - 1
		}

		oldStart, newStart := -1, -1
		oldCnt, newCnt := 0, 0
		var lines []string
		for i := start; i <= end; i++ {
			op := ops[i]
			switch op.kind {
			case "eq":
				if oldStart < 0 {
					oldStart = op.oldLine
					newStart = op.newLine
				}
				lines = append(lines, " "+op.text)
				oldCnt++
				newCnt++
			case "add":
				if oldStart < 0 {
					oldStart = op.oldLine
					if oldStart < 0 {
						oldStart = 0
					}
					newStart = op.newLine
				}
				if newStart < 0 {
					newStart = op.newLine
				}
				lines = append(lines, "+"+op.text)
				newCnt++
			case "del":
				if oldStart < 0 {
					oldStart = op.oldLine
				}
				if newStart < 0 {
					newStart = op.newLine
					if newStart < 0 {
						newStart = 0
					}
				}
				lines = append(lines, "-"+op.text)
				oldCnt++
			}
		}
		if oldStart < 0 {
			oldStart = 0
		}
		if newStart < 0 {
			newStart = 0
		}
		result = append(result, fmt.Sprintf("@@ -%d,%d +%d,%d @@", oldStart+1, oldCnt, newStart+1, newCnt))
		result = append(result, lines...)
	}
	return result
}

// lcsDiff computes a line-level diff using simplified LCS.
func lcsDiff(oldLines, newLines []string) []lineDiffOp {
	m, n := len(oldLines), len(newLines)
	const maxDiffSize = 5000
	if m > maxDiffSize || n > maxDiffSize {
		return greedyDiff(oldLines, newLines)
	}

	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if oldLines[i-1] == newLines[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] > dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	var ops []lineDiffOp
	var backtrack func(i, j int)
	backtrack = func(i, j int) {
		if i == 0 && j == 0 {
			return
		}
		if i > 0 && j > 0 && oldLines[i-1] == newLines[j-1] {
			backtrack(i-1, j-1)
			ops = append(ops, lineDiffOp{kind: "eq", oldLine: i - 1, newLine: j - 1, text: oldLines[i-1]})
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			backtrack(i, j-1)
			ops = append(ops, lineDiffOp{kind: "add", newLine: j - 1, text: newLines[j-1]})
		} else {
			backtrack(i-1, j)
			ops = append(ops, lineDiffOp{kind: "del", oldLine: i - 1, text: oldLines[i-1]})
		}
	}
	backtrack(m, n)
	return ops
}

// greedyDiff is a fast fallback for very large files.
func greedyDiff(oldLines, newLines []string) []lineDiffOp {
	var ops []lineDiffOp
	i, j := 0, 0
	for i < len(oldLines) && j < len(newLines) {
		if oldLines[i] == newLines[j] {
			ops = append(ops, lineDiffOp{kind: "eq", oldLine: i, newLine: j, text: oldLines[i]})
			i++
			j++
		} else {
			found := -1
			for k := j + 1; k < len(newLines) && k < j+10; k++ {
				if newLines[k] == oldLines[i] {
					found = k
					break
				}
			}
			if found >= 0 {
				for ; j < found; j++ {
					ops = append(ops, lineDiffOp{kind: "add", newLine: j, text: newLines[j]})
				}
			} else {
				ops = append(ops, lineDiffOp{kind: "del", oldLine: i, text: oldLines[i]})
				i++
			}
		}
	}
	for ; i < len(oldLines); i++ {
		ops = append(ops, lineDiffOp{kind: "del", oldLine: i, text: oldLines[i]})
	}
	for ; j < len(newLines); j++ {
		ops = append(ops, lineDiffOp{kind: "add", newLine: j, text: newLines[j]})
	}
	return ops
}
