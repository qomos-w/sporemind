package appmanager

import (
	"fmt"
	"path/filepath"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/codegen"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// handleSDKVendor extracts a self-contained copy of the plugin SDK into the
// app directory (vendor-sdk/) and re-points the app go.mod SDK replace at it.
// Template scaffolds vendor by default; this callable brings existing apps to
// the same portable layout and refreshes a stale vendored copy. Idempotent:
// re-running replaces vendor-sdk with the current host SDK (dev checkout or
// release-extracted zip) without touching any other file.
func (a *Actor) handleSDKVendor(ctx actor.Context, req gen.AppManagerSdkVendorReq) (gen.AppManagerSdkVendorResp, error) {
	projectID, err := resolveDevProjectID(ctx, req.ProjectID, req.CallerAgentID)
	if err != nil {
		return gen.AppManagerSdkVendorResp{Error: err.Error()}, nil
	}
	appDir, err := codegen.NormalizeAppDir(req.AppDir)
	if err != nil {
		return gen.AppManagerSdkVendorResp{Error: fmt.Sprintf("invalid AppDir: %v", err)}, nil
	}

	canonical, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return gen.AppManagerSdkVendorResp{
			Error: fmt.Sprintf("invalid project id: %v%s", err, availableProjectsHint(ctx)),
		}, nil
	}
	projectRef, ok := ctx.LookupID(id.From(canonical))
	if !ok || projectRef == nil {
		return gen.AppManagerSdkVendorResp{Error: fmt.Sprintf("project %q not found", projectID)}, nil
	}
	planner := ctx.Planner()
	if planner == nil {
		return gen.AppManagerSdkVendorResp{Error: "planner not available"}, nil
	}
	projectRoot, err := a.resolveDevRoot(ctx, planner, projectRef, req.CallerAgentID)
	if err != nil {
		return gen.AppManagerSdkVendorResp{Error: err.Error()}, nil
	}

	sdkPath, err := codegen.EnsureDevSDKWorkspace(projectRoot)
	if err != nil {
		return gen.AppManagerSdkVendorResp{Error: fmt.Sprintf("SDK workspace: %v", err)}, nil
	}

	appRoot, err := codegen.JoinAppDir(projectRoot, appDir)
	if err != nil {
		return gen.AppManagerSdkVendorResp{Error: fmt.Sprintf("invalid AppDir: %v", err)}, nil
	}
	vendoredPath, err := codegen.VendorSDKIntoAppDir(appRoot, sdkPath)
	if err != nil {
		return gen.AppManagerSdkVendorResp{Error: err.Error()}, nil
	}

	rootRel, err := filepath.Rel(projectRoot, vendoredPath)
	if err != nil {
		rootRel = vendoredPath
	}
	return gen.AppManagerSdkVendorResp{
		Vendored: true,
		Path:     filepath.ToSlash(rootRel),
	}, nil
}
