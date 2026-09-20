package appmanager

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/codegen"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// handleRegistrationPreview resolves the manifest a pending registration
// callable would install — read-only, no build, no load, no persistence.
// The turn engine calls this at permission-interception time to attach the
// requested host capabilities (manifest Permissions) to the permission
// event, so the user sees what approving grants. Source selection mirrors
// the four registration callables: inline Manifest (register), project
// (register_project, bound project when ProjectId is omitted), local
// package path (install_local), cloud slug (content_install).
func (a *Actor) handleRegistrationPreview(ctx actor.Context, req gen.AppManagerRegistrationPreviewReq) (gen.AppManagerRegistrationPreviewResp, error) {
	switch {
	case req.Manifest != nil && req.Manifest.ID != "":
		return gen.AppManagerRegistrationPreviewResp{Manifest: *req.Manifest}, nil

	case req.Slug != "":
		return previewCloudContent(ctx, req.Slug)

	case req.Path != "":
		return previewLocalPackage(req.Path)

	case req.ProjectID != "" || req.CallerAgentID != "":
		return a.previewProjectManifest(ctx, req)

	default:
		return gen.AppManagerRegistrationPreviewResp{}, fmt.Errorf("appmanager: registration preview: no source (one of Manifest, ProjectId/CallerAgentId, Path, Slug is required)")
	}
}

// previewProjectManifest mirrors handleProjectPackage's manifest prefix —
// project.info for the root, then a single project.read of the manifest —
// without packaging modules or assets.
func (a *Actor) previewProjectManifest(ctx actor.Context, req gen.AppManagerRegistrationPreviewReq) (gen.AppManagerRegistrationPreviewResp, error) {
	appDir, err := codegen.NormalizeAppDir(req.AppDir)
	if err != nil {
		return gen.AppManagerRegistrationPreviewResp{}, fmt.Errorf("appmanager: invalid AppDir: %w", err)
	}
	projectID, err := resolveDevProjectID(ctx, req.ProjectID, req.CallerAgentID)
	if err != nil {
		return gen.AppManagerRegistrationPreviewResp{}, err
	}
	canonical, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return gen.AppManagerRegistrationPreviewResp{}, fmt.Errorf("appmanager: invalid project id: %w%s", err, availableProjectsHint(ctx))
	}
	projectRef, ok := ctx.LookupID(id.From(canonical))
	if !ok || projectRef == nil {
		return gen.AppManagerRegistrationPreviewResp{}, fmt.Errorf("appmanager: project %q not found", projectID)
	}
	planner := ctx.Planner()
	if planner == nil {
		return gen.AppManagerRegistrationPreviewResp{}, fmt.Errorf("appmanager: planner not available")
	}
	infoValue, err := planner.Call(ctx.Lifecycle(), projectRef, "project.info", struct{}{}).Await()
	if err != nil {
		return gen.AppManagerRegistrationPreviewResp{}, fmt.Errorf("appmanager: read project info: %w", err)
	}
	info, ok := infoValue.(gen.ProjectInfoResp)
	if !ok || len(info.Roots) == 0 || info.Roots[0].Path == "" {
		return gen.AppManagerRegistrationPreviewResp{}, fmt.Errorf("appmanager: project has no package root")
	}
	appRoot := strings.TrimSuffix(info.Roots[0].Path, "/")
	if appDir != "" {
		appRoot = appRoot + "/" + appDir
	}
	readValue, err := planner.Call(ctx.Lifecycle(), projectRef, "project.read", gen.FileSystemReadReq{Path: appRoot + "/app.manifest.json"}).Await()
	if err != nil {
		if appDir == "" {
			return gen.AppManagerRegistrationPreviewResp{}, fmt.Errorf(
				"appmanager: read manifest: %w (no app.manifest.json at the project root; if this app lives in a subdirectory, pass AppDir)",
				err)
		}
		return gen.AppManagerRegistrationPreviewResp{}, fmt.Errorf("appmanager: read manifest: %w", err)
	}
	read, ok := readValue.(gen.FileSystemReadResp)
	if !ok {
		return gen.AppManagerRegistrationPreviewResp{}, fmt.Errorf("appmanager: unexpected project.read response")
	}
	var manifest gen.AppManifest
	if err := json.Unmarshal([]byte(read.Content), &manifest); err != nil {
		return gen.AppManagerRegistrationPreviewResp{}, fmt.Errorf("appmanager: decode manifest: %w", err)
	}
	return gen.AppManagerRegistrationPreviewResp{Manifest: manifest}, nil
}

// previewLocalPackage reads app.manifest.json from a package directory or
// .zip archive without walking the rest of the package.
func previewLocalPackage(path string) (gen.AppManagerRegistrationPreviewResp, error) {
	if strings.HasSuffix(strings.ToLower(path), ".zip") {
		zr, err := zip.OpenReader(path)
		if err != nil {
			return gen.AppManagerRegistrationPreviewResp{}, fmt.Errorf("appmanager: open package %q: %w", path, err)
		}
		defer zr.Close()
		for _, f := range zr.File {
			if f.Name != localPackageManifestName {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return gen.AppManagerRegistrationPreviewResp{}, fmt.Errorf("appmanager: read %q in package: %w", localPackageManifestName, err)
			}
			content, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return gen.AppManagerRegistrationPreviewResp{}, fmt.Errorf("appmanager: read %q in package: %w", localPackageManifestName, err)
			}
			return decodePreviewManifest(content)
		}
		return gen.AppManagerRegistrationPreviewResp{}, fmt.Errorf("appmanager: missing %s in package %q", localPackageManifestName, path)
	}
	content, err := os.ReadFile(path + "/" + localPackageManifestName)
	if err != nil {
		return gen.AppManagerRegistrationPreviewResp{}, fmt.Errorf("appmanager: read %s in %q: %w", localPackageManifestName, path, err)
	}
	return decodePreviewManifest(content)
}

func decodePreviewManifest(content []byte) (gen.AppManagerRegistrationPreviewResp, error) {
	var manifest gen.AppManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return gen.AppManagerRegistrationPreviewResp{}, fmt.Errorf("appmanager: decode manifest: %w", err)
	}
	return gen.AppManagerRegistrationPreviewResp{Manifest: manifest}, nil
}

// previewCloudContent resolves a cloud content package's manifest via the
// cloudaccount actor's content_detail (raw manifest JSON). Non-app content
// fails manifest decoding and surfaces as a preview error.
func previewCloudContent(ctx actor.Context, slug string) (gen.AppManagerRegistrationPreviewResp, error) {
	cloudRef, ok := ctx.LookupService("cloudaccount")
	if !ok || cloudRef == nil {
		return gen.AppManagerRegistrationPreviewResp{}, fmt.Errorf("appmanager: cloudaccount service not available")
	}
	planner := ctx.Planner()
	if planner == nil {
		return gen.AppManagerRegistrationPreviewResp{}, fmt.Errorf("appmanager: planner not available")
	}
	detailValue, err := planner.Call(ctx.Lifecycle(), cloudRef, "cloudaccount.content_detail", gen.ContentDetailReq{Slug: slug}).Await()
	if err != nil {
		return gen.AppManagerRegistrationPreviewResp{}, fmt.Errorf("appmanager: content detail: %w", err)
	}
	detail, ok := detailValue.(gen.ContentDetailResp)
	if !ok || detail.Manifest == "" {
		return gen.AppManagerRegistrationPreviewResp{}, fmt.Errorf("appmanager: content %q has no manifest", slug)
	}
	return decodePreviewManifest([]byte(detail.Manifest))
}
