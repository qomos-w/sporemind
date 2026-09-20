package appmanager

import (
	"fmt"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/codegen"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// devRootState is the outcome of resolving the effective project root for a
// dev entrypoint: the directory all reads/writes target, plus whether the
// caller is worktree-bound (and which worktree).
type devRootState struct {
	root         string // effective project root (worktree path when bound)
	bound        bool   // caller agent is bound to an active worktree
	worktreePath string // bound worktree path; "" when unbound
}

// resolveDevRoot resolves the effective project root for plugin dev
// entrypoints. A worktree-bound calling agent routes the whole pipeline at
// its worktree checkout (generation, packaging, and native builds then read
// and write inside the isolation fence; the artifact stays valid until
// worktree merge/exit). An unbound caller (human/internal, or an agent
// without a worktree) gets the main-tree root from project.info.
// CallerAgentID is injected by the turn engine (unforgeable); empty means a
// human/internal caller, which has no agent worktree binding. Any failure of
// the binding check is fail-closed: proceeding without it risks writing
// generated artifacts into the main tree from a worktree caller.
func (a *Actor) resolveDevRoot(ctx actor.Context, planner actor.Planner, projectRef ref.Ref, callerAgentID string) (string, error) {
	st, err := a.resolveDevRootState(ctx, planner, projectRef, callerAgentID)
	return st.root, err
}

// resolveDevRootState is resolveDevRoot plus the binding state callers need
// for worktree-scoped manifest keys and bound-caller warnings.
func (a *Actor) resolveDevRootState(ctx actor.Context, planner actor.Planner, projectRef ref.Ref, callerAgentID string) (devRootState, error) {
	var st devRootState
	callerAgentID = strings.TrimSpace(callerAgentID)
	if callerAgentID != "" {
		v, err := planner.Call(ctx.Lifecycle(), projectRef, "project.worktree_bound_check",
			gen.ProjectWorktreeBoundCheckReq{AgentActorID: callerAgentID}).Await()
		if err != nil {
			return st, fmt.Errorf("worktree binding check failed: %w", err)
		}
		if resp, ok := v.(gen.ProjectWorktreeBoundCheckResp); ok && resp.Bound {
			wt := strings.TrimSuffix(strings.TrimSpace(resp.WorktreePath), "/")
			if wt == "" {
				return st, fmt.Errorf("worktree binding check failed: bound worktree for agent %s reported an empty path", callerAgentID)
			}
			st.bound = true
			st.worktreePath = wt
			st.root = wt
			return st, nil
		}
	}
	infoValue, err := planner.Call(ctx.Lifecycle(), projectRef, "project.info", struct{}{}).Await()
	if err != nil {
		return st, fmt.Errorf("project.info: %w", err)
	}
	info, ok := infoValue.(gen.ProjectInfoResp)
	if !ok || len(info.Roots) == 0 || info.Roots[0].Path == "" {
		return st, fmt.Errorf("project has no package root")
	}
	st.root = strings.TrimSuffix(info.Roots[0].Path, "/")
	return st, nil
}

// generatedManifestKey scopes the persisted GeneratedManifest to the app
// directory it was produced from. The project root keeps the bare projectID
// key (bit-identical with pre-subdir state); subdirectory apps are keyed as
// "<projectID>::<appDir>" so two apps in one project cannot overwrite each
// other's drift-detection record. A worktree-bound generation additionally
// prefixes the key with a hash of the worktree path
// ("wt<hash16>::<projectID>[::<appDir>]") so a worktree build never clobbers
// — or falsely satisfies — the main tree's drift record; the worktree
// manifest dies with the worktree. An empty worktreePath yields the legacy
// format bit-identically.
func generatedManifestKey(projectID, appDir, worktreePath string) string {
	key := projectID
	if worktreePath != "" {
		key = "wt" + hashString(worktreePath)[:16] + "::" + projectID
	}
	if appDir == "" {
		return key
	}
	return key + "::" + appDir
}

// handleDevGenerate reads .appdef from the project directory, generates all
// artifacts (main.gen.go, handlers.go, app.manifest.json, schemas_gen.go,
// client.gen.ts, go.mod), and persists the GeneratedManifest into the
// appmanager state. The manifest is consumed by project.write write-protection
// and gate manifest-consistency checks.
// AppDir optionally points the whole pipeline at a subdirectory of the project
// root (e.g. "plugin-dev-example"); empty or "." targets the root.
// Template=true explicitly requests the default scaffold for a directory that
// has no .appdef (and no go.mod / *.go); the default (false) refuses with an
// error and writes nothing.
func (a *Actor) handleDevGenerate(ctx actor.Context, req gen.AppManagerDevGenerateReq) (gen.AppManagerDevGenerateResp, error) {
	projectID, err := resolveDevProjectID(ctx, req.ProjectID, req.CallerAgentID)
	if err != nil {
		return gen.AppManagerDevGenerateResp{
			Error: err.Error(),
		}, nil
	}
	appDir, err := codegen.NormalizeAppDir(req.AppDir)
	if err != nil {
		return gen.AppManagerDevGenerateResp{
			Error: fmt.Sprintf("invalid AppDir: %v", err),
		}, nil
	}

	// Resolve project root via project.info.
	canonical, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return gen.AppManagerDevGenerateResp{
			Error: fmt.Sprintf("invalid project id: %v%s", err, availableProjectsHint(ctx)),
		}, nil
	}
	projectRef, ok := ctx.LookupID(id.From(canonical))
	if !ok || projectRef == nil {
		return gen.AppManagerDevGenerateResp{
			Error: fmt.Sprintf("project %q not found", projectID),
		}, nil
	}
	planner := ctx.Planner()
	if planner == nil {
		return gen.AppManagerDevGenerateResp{
			Error: "planner not available",
		}, nil
	}
	st, err := a.resolveDevRootState(ctx, planner, projectRef, req.CallerAgentID)
	if err != nil {
		return gen.AppManagerDevGenerateResp{Error: err.Error()}, nil
	}
	projectRoot := st.root
	call := func(name string, payload any) (any, error) {
		return planner.Call(ctx.Lifecycle(), projectRef, name, payload).Await()
	}

	// Resolve SDK path against the project root (unchanged by AppDir: the SDK
	// is a workspace-level concern, not per-app).
	sdkPath, err := codegen.EnsureDevSDKWorkspace(projectRoot)
	if err != nil {
		return gen.AppManagerDevGenerateResp{
			Error: fmt.Sprintf("SDK workspace: %v", err),
		}, nil
	}

	// Run codegen against the app directory (root when AppDir is empty).
	// Template is passed through so the default scaffold only happens on an
	// explicit request; a directory without .appdef fails instead of being
	// implicitly populated (BP9 guard).
	genDir, err := codegen.JoinAppDir(projectRoot, appDir)
	if err != nil {
		return gen.AppManagerDevGenerateResp{
			Error: fmt.Sprintf("invalid AppDir: %v", err),
		}, nil
	}
	// Resolve the host protocol map for extraction (compile-time-embedded
	// manifest; cached). Failures degrade to no extraction: generation must
	// not block on the discovery layer.
	hostCalls, err := a.hostCalls()
	if err != nil {
		hostCalls = nil
		ctx.Logger().Error("appmanager: host protocol resolution failed; hostproto.gen.go will not be emitted", "error", err)
	}

	// Resolve the registered-app manifests for bundle-level permission typing.
	// Every registered app is offered; the codegen resolves each plugin.*
	// permission against them by longest-prefix and only types bundle slugs.
	bundleDeps := a.bundleDepsSnapshot()

	result, err := codegen.Generate(genDir, codegen.Options{SDKPath: sdkPath, Template: req.Template, HostCalls: hostCalls, BundleDeps: bundleDeps})
	if err != nil {
		return gen.AppManagerDevGenerateResp{
			Error: fmt.Sprintf("codegen: %v", err),
		}, nil
	}

	// Convert to generated response type. Paths are reported relative to the
	// project root, so subdirectory apps prefix every artifact with AppDir.
	files := make([]gen.AppManagerDevGenerateFileEntry, len(result.Files))
	for i, f := range result.Files {
		rootRel, err := codegen.RelToRoot(appDir, f)
		if err != nil {
			return gen.AppManagerDevGenerateResp{Error: fmt.Sprintf("invalid AppDir: %v", err)}, nil
		}
		files[i] = gen.AppManagerDevGenerateFileEntry{
			Path:        rootRel,
			ContentHash: result.Manifest.Files[f],
		}
	}

	// Persist the GeneratedManifest into appmanager state, keyed by app dir.
	a.withMu(func() {
		if a.GeneratedManifests == nil {
			a.GeneratedManifests = make(map[string]codegen.GeneratedManifest)
		}
		a.GeneratedManifests[generatedManifestKey(projectID, appDir, st.worktreePath)] = result.Manifest
	})

	if err := a.Save(); err != nil {
		ctx.Logger().Error("appmanager: persist generated manifest failed", "project", projectID, "error", err)
	}

	// Update the project's protected-files list so project.write refuses
	// hand-edits to fully-generated files. Template mode also returns a
	// non-empty list (main.gen.go, manifest, schemas, client TS). Paths are
	// project-root-relative, so subdirectory apps carry the AppDir prefix.
	protected := make([]string, len(result.ProtectedFiles))
	for i, f := range result.ProtectedFiles {
		rootRel, err := codegen.RelToRoot(appDir, f)
		if err != nil {
			return gen.AppManagerDevGenerateResp{Error: fmt.Sprintf("invalid AppDir: %v", err)}, nil
		}
		protected[i] = rootRel
	}
	// Publish the protected-files list. The turn-engine-injected
	// CallerAgentID is forwarded so the project actor routes a worktree-bound
	// caller's list into that worktree's bucket (global list untouched, see
	// project write_guard); unbound callers write the global list as before.
	if _, err := call("project.set_protected_files", gen.SetProtectedFilesReq{
		Files: protected,
		// Forward the turn-engine-injected caller identity so the project
		// actor can route the list into the caller's worktree bucket when the
		// agent is worktree-bound (see project write_guard).
		CallerAgentID: req.CallerAgentID,
	}); err != nil {
		ctx.Logger().Error("appmanager: set protected files failed", "project", projectID, "error", err)
	}
	warnings := result.Warnings
	if st.bound {
		warnings = append(append([]string{}, warnings...),
			fmt.Sprintf("generated inside worktree %s: artifacts and protected-files bucket are valid until worktree merge/exit; re-run appmanager.dev_generate after merge to protect the main-tree artifacts", st.worktreePath))
	}

	return gen.AppManagerDevGenerateResp{
		Files:      files,
		AppDefHash: result.Manifest.AppDefHash,
		SDKPath:    result.SDKPath,
		FreshSDK:   result.FreshSDK,
		Template:   result.Template,
		Warnings:   warnings,
	}, nil
}
