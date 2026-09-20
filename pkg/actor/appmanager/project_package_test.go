package appmanager

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestProjectPackageExcludesBuildArtifacts covers D5: generated files and
// build noise (go.sum, client.gen.ts, go.work, …) must not be packaged as
// runtime assets or served over the plugin HTTP surface, and must not churn
// packageHash. Real assets like index.html must still be packaged, and a new
// real asset must still move the hash (positive control).
func TestProjectPackageExcludesBuildArtifacts(t *testing.T) {
	env := newBP7Project(t, "pkg-exclude-")
	env.write("app.manifest.json", `{
		"id": "app.package-exclude",
		"name": "PackageExclude",
		"version": "0.1.0",
		"runtime": "spore",
		"protocolVersion": 1,
		"namespace": "app.packageexclude"
	}`)
	env.write("main.spore", "export fun main(): int = 42")
	env.write("index.html", "<html>real asset</html>")

	a := newBP7Actor(t)
	ctx := env.ctx()

	pkg, err := a.handleProjectPackage(ctx, gen.AppManagerProjectPackageReq{ProjectID: bp7ProjectID(t)})
	if err != nil {
		t.Fatalf("handleProjectPackage (baseline): %v", err)
	}
	assertAssetsExactly(t, pkg.Assets, "index.html")
	baselineHash := pkg.PackageHash

	// Build artifacts arrive. Assets must stay {index.html} and the hash stable.
	for _, f := range []struct{ name, content string }{
		{"go.sum", "github.com/some/dep v1.2.3 h1:abcdef==\n"},
		{"client.gen.ts", "export const client = {};\n"},
		{"go.work", "go 1.22\n"},
		{"go.work.sum", "github.com/some/dep v1.2.3 h1:abcdef==\n"},
		{".appdef", "app PackageExclude {}\n"},
	} {
		env.write(f.name, f.content)
	}

	pkg2, err := a.handleProjectPackage(ctx, gen.AppManagerProjectPackageReq{ProjectID: bp7ProjectID(t)})
	if err != nil {
		t.Fatalf("handleProjectPackage (after build artifacts): %v", err)
	}
	assertAssetsExactly(t, pkg2.Assets, "index.html")
	if pkg2.PackageHash != baselineHash {
		t.Fatalf("packageHash changed after adding excluded build artifacts:\n  baseline = %s\n  after   = %s", baselineHash, pkg2.PackageHash)
	}
	for _, excluded := range []string{"go.sum", "client.gen.ts", "go.work", "go.work.sum"} {
		if _, leaked := pkg2.Assets[excluded]; leaked {
			t.Errorf("excluded build artifact %q was packaged as an asset", excluded)
		}
	}

	// Positive control: a new real asset is packaged and moves the hash,
	// proving the hash is sensitive to genuine assets (not frozen trivially).
	env.write("style.css", "body { color: black; }\n")
	pkg3, err := a.handleProjectPackage(ctx, gen.AppManagerProjectPackageReq{ProjectID: bp7ProjectID(t)})
	if err != nil {
		t.Fatalf("handleProjectPackage (after real asset): %v", err)
	}
	assertAssetsExactly(t, pkg3.Assets, "index.html", "style.css")
	if pkg3.PackageHash == baselineHash {
		t.Fatalf("packageHash did not change after adding a real asset (style.css): %s", baselineHash)
	}
}

// TestExcludedAssetsListsNonRuntimeArtifacts documents the excluded set and
// guards against accidental removal of an entry.
func TestExcludedAssetsListsNonRuntimeArtifacts(t *testing.T) {
	want := map[string]bool{
		"go.sum": true, "client.gen.ts": true, "main.gen.go": true,
		"schemas_gen.go": true, ".appdef": true, "go.work": true, "go.work.sum": true,
	}
	for name := range want {
		if !excludedAssets[name] {
			t.Errorf("excludedAssets missing %q", name)
		}
	}
}

// implicitProjectReadLineCap mirrors the project actor's implicit display
// cap (pkg/actor/project/fileops.go implicitLimit). Duplicated as a constant
// so the fixture fails loudly if the cap ever moves.
const implicitProjectReadLineCap = 2000

// TestProjectPackageReadsBeyondInteractiveLineCap pins the fix for the
// silent-truncation bug: project.read applies an implicit 2000-line cap for
// interactive display, so a pretty-printed app.descriptors.json (or any long
// .go/.spore module) was cut mid-document and failed to decode. Package
// assembly must read byte-exact via project.read_base64 (16MB cap, no line
// semantics); any use of project.read here is a regression.
func TestProjectPackageReadsBeyondInteractiveLineCap(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("app.manifest.json", `{"Id":"app.bigdesc","Name":"BigDesc","Version":"0.1.0","Runtime":"spore","Namespace":"bigdesc"}`)

	var entry strings.Builder
	for i := 0; i < 2100; i++ {
		fmt.Fprintf(&entry, "// pad line %d\n", i)
	}
	entry.WriteString("app BigDesc {}\n")
	write("main.spore", entry.String())

	// Pretty-printed descriptors: 400 entries × 6 lines = 2400+ lines, well
	// past the 2000-line interactive cap.
	const descriptorCount = 400
	var desc strings.Builder
	desc.WriteString("{\n")
	for i := 0; i < descriptorCount; i++ {
		comma := ","
		if i == descriptorCount-1 {
			comma = ""
		}
		fmt.Fprintf(&desc, "  \"Obj%04d\": {\n    \"Kind\": \"struct\",\n    \"Name\": \"Obj%04d\",\n    \"Fields\": []\n  }%s\n", i, i, comma)
	}
	desc.WriteString("}\n")
	write(descriptorFile, desc.String())
	if lines := strings.Count(desc.String(), "\n"); lines <= implicitProjectReadLineCap {
		t.Fatalf("fixture must exceed the interactive line cap: got %d lines", lines)
	}

	planner := lifecyclePlanner{call: func(callID string, payload any) (any, error) {
		switch callID {
		case "project.info":
			return gen.ProjectInfoResp{Roots: []gen.ProjectInfoRoot{{Name: "t", Path: root}}}, nil
		case "project.read":
			t.Fatal("handleProjectPackage must not use line-capped project.read")
			return nil, nil
		case "project.read_base64":
			req := payload.(gen.FileSystemReadBase64Req)
			data, err := os.ReadFile(filepath.FromSlash(req.Path))
			if err != nil {
				return gen.FileSystemReadBase64Resp{}, err
			}
			return gen.FileSystemReadBase64Resp{Content: base64.StdEncoding.EncodeToString(data)}, nil
		case "project.list":
			return listText(
				gen.FileEntry{Name: "app.manifest.json"},
				gen.FileEntry{Name: "main.spore"},
				gen.FileEntry{Name: descriptorFile},
			), nil
		default:
			t.Fatalf("unexpected planner call %s", callID)
			return nil, nil
		}
	}}

	ctx := testutil.HumanCtx(testutil.GenActorID())
	projectCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 62)
	if err != nil {
		t.Fatal(err)
	}
	projectRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	projectActorID := id.From(projectCID)
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return projectRef, aid == projectActorID
	}
	ctx.PlannerFn = func() actor.Planner { return planner }

	a := &Actor{}
	resp, err := a.handleProjectPackage(ctx, gen.AppManagerProjectPackageReq{ProjectID: projectCID.String()})
	if err != nil {
		t.Fatalf("handleProjectPackage: %v", err)
	}
	if got := len(resp.SchemaDescriptors); got != descriptorCount {
		t.Errorf("SchemaDescriptors = %d, want %d (truncated read would fail decode or drop entries)", got, descriptorCount)
	}
	if got := strings.Count(resp.Modules["main.spore"], "\n"); got < 2100 {
		t.Errorf("entry module truncated: got %d newlines, want >= 2100", got)
	}
}

// TestProjectPackageExcludesDependencyTrees pins the fix for the state-bloat
// bug: the NoIgnore=true project.list sweep must not package files under
// dependency trees (node_modules/, vendor/) — a dev app with a frontend/
// workspace otherwise sweeps thousands of toolchain files (esbuild.exe,
// typescript.js, …) into appRecord.Assets, base64-inflating the appmanager
// state document to hundreds of megabytes on every Save. Real assets under
// nested dirs (frontend/dist/…) still package.
func TestProjectPackageExcludesDependencyTrees(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(name, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("app.manifest.json", `{"Id":"app.dep-tree","Name":"DepTree","Version":"0.1.0","Runtime":"native","Namespace":"deptree"}`)
	mustWrite("main.gen.go", "package main\n")
	mustWrite("frontend/dist/index.html", "<html>built</html>")
	mustWrite("frontend/node_modules/typescript/lib/typescript.js", "// 12MB of toolchain")
	mustWrite("frontend/node_modules/@esbuild/win32-x64/esbuild.exe", "binary")
	mustWrite("vendor/lib/dep.js", "// vendored dependency")

	planner := lifecyclePlanner{call: func(callID string, payload any) (any, error) {
		switch callID {
		case "project.info":
			return gen.ProjectInfoResp{Roots: []gen.ProjectInfoRoot{{Name: "t", Path: root}}}, nil
		case "project.read_base64":
			req := payload.(gen.FileSystemReadBase64Req)
			data, err := os.ReadFile(filepath.FromSlash(req.Path))
			if err != nil {
				return gen.FileSystemReadBase64Resp{}, err
			}
			return gen.FileSystemReadBase64Resp{Content: base64.StdEncoding.EncodeToString(data)}, nil
		case "project.list":
			return listText(
				gen.FileEntry{Name: "app.manifest.json"},
				gen.FileEntry{Name: "main.gen.go"},
				gen.FileEntry{Name: "frontend/dist/index.html"},
				gen.FileEntry{Name: "frontend/node_modules/typescript/lib/typescript.js"},
				gen.FileEntry{Name: "frontend/node_modules/@esbuild/win32-x64/esbuild.exe"},
				gen.FileEntry{Name: "vendor/lib/dep.js"},
			), nil
		default:
			t.Fatalf("unexpected planner call %s", callID)
			return nil, nil
		}
	}}

	ctx := testutil.HumanCtx(testutil.GenActorID())
	projectCID, err := identity.NewCanonicalID(1700000000000, 1, 2, 62)
	if err != nil {
		t.Fatal(err)
	}
	projectRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return projectRef, aid == id.From(projectCID)
	}
	ctx.PlannerFn = func() actor.Planner { return planner }

	a := &Actor{}
	resp, err := a.handleProjectPackage(ctx, gen.AppManagerProjectPackageReq{ProjectID: projectCID.String()})
	if err != nil {
		t.Fatalf("handleProjectPackage: %v", err)
	}
	assertAssetsExactly(t, resp.Assets, "frontend/dist/index.html")
}

func assertAssetsExactly(t *testing.T, assets map[string][]byte, want ...string) {
	t.Helper()
	if len(assets) != len(want) {
		t.Fatalf("assets = %v, want exactly %v", sortedKeys(assets), want)
	}
	for _, name := range want {
		if _, ok := assets[name]; !ok {
			t.Errorf("asset %q missing from %v", name, sortedKeys(assets))
		}
	}
}

func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
