package pluginhost

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/spore/identity"

	"github.com/qomos-w/sporemind/pkg/codegen"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// resolveProjectSource reads the project root and manifest path for a native
// build request. When req.SourceRoot is non-empty the caller has already
// resolved the project root (e.g. a worktree path) and PluginHost uses it
// directly, skipping project.info; otherwise the root comes from the project
// actor (project.info). The manifest is always read and validated via
// project.read — PluginHost never touches the filesystem outside of the
// project actor's authority. AppDir optionally scopes source and manifest
// reads to a subdirectory of the project root. Returns (sourceRoot,
// manifestPath, error).
func (a *Actor) resolveProjectSource(ctx actor.Context, req gen.NativeBuildReq) (string, string, error) {
	canonical, err := identity.ParseCanonicalID(req.ProjectID)
	if err != nil {
		return "", "", fmt.Errorf("pluginhost: invalid project id: %w", err)
	}
	appDir, err := codegen.NormalizeAppDir(req.AppDir)
	if err != nil {
		return "", "", fmt.Errorf("pluginhost: invalid AppDir: %w", err)
	}
	projectRef, ok := ctx.LookupID(id.From(canonical))
	if !ok || projectRef == nil {
		return "", "", fmt.Errorf("pluginhost: project %q not found", req.ProjectID)
	}
	planner := ctx.Planner()
	if planner == nil {
		return "", "", fmt.Errorf("pluginhost: planner not available")
	}
	call := func(name string, payload any) (any, error) {
		return planner.Call(ctx.Lifecycle(), projectRef, name, payload).Await()
	}
	var root string
	if req.SourceRoot != "" {
		root = strings.TrimSuffix(req.SourceRoot, "/")
	} else {
		infoValue, err := call("project.info", struct{}{})
		if err != nil {
			return "", "", fmt.Errorf("pluginhost: read project info: %w", err)
		}
		info, ok := infoValue.(gen.ProjectInfoResp)
		if !ok || len(info.Roots) == 0 || info.Roots[0].Path == "" {
			return "", "", fmt.Errorf("pluginhost: project has no package root")
		}
		root = strings.TrimSuffix(info.Roots[0].Path, "/")
	}
	if appDir != "" {
		root = root + "/" + appDir
	}
	manifestPath := filepath.Join(root, "app.manifest.json")
	manifestRaw, err := call("project.read", gen.FileSystemReadReq{Path: manifestPath})
	if err != nil {
		return "", "", fmt.Errorf("pluginhost: read manifest: %w", err)
	}
	manifestResp, ok := manifestRaw.(gen.FileSystemReadResp)
	if !ok {
		return "", "", fmt.Errorf("pluginhost: unexpected project.read response")
	}
	var manifest gen.AppManifest
	if err := json.Unmarshal([]byte(manifestResp.Content), &manifest); err != nil {
		return "", "", fmt.Errorf("pluginhost: decode manifest: %w", err)
	}
	if req.AppID != "" && manifest.ID != req.AppID {
		return "", "", fmt.Errorf("pluginhost: manifest id %q does not match requested app %q", manifest.ID, req.AppID)
	}
	return root, manifestPath, nil
}

// artifactOutputDir returns the directory the compiled artifact is written to.
// It lives under the project root so the build is reproducible and the
// artifact is co-located with its source.
func artifactOutputDir(projectRoot string) string {
	return filepath.Join(projectRoot, ".sporecode", "build")
}
