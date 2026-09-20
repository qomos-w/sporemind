package appmanager

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// This file covers BP9: template scaffolding must be explicit. dev_generate
// on a directory without .appdef must fail with a clear error and write
// nothing; only Template=true scaffolds, and even then only into a directory
// without go.mod / *.go.

// rootSnapshot lists every entry under the project root (files + dirs,
// recursive) so tests can prove zero writes.
func rootSnapshot(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	err := fs.WalkDir(os.DirFS(root), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		out[p] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestDevGenerateWithoutAppDefFailsAndWritesNothing: the default call on a
// project root that has no .appdef returns an error naming the directory and
// the Template opt-in, and leaves the root untouched (BP9 main-repo-root
// pollution scenario).
func TestDevGenerateWithoutAppDefFailsAndWritesNothing(t *testing.T) {
	env := newBP7Project(t, "bp9-empty-")
	a := newBP7Actor(t)
	ctx := env.ctx()

	before := rootSnapshot(t, env.root)

	resp, err := a.handleDevGenerate(ctx, gen.AppManagerDevGenerateReq{
		ProjectID: bp7ProjectID(t),
	})
	if err != nil {
		t.Fatalf("handleDevGenerate: %v", err)
	}
	if resp.Error == "" {
		t.Fatal("dev_generate on appdef-less project succeeded; want refusal")
	}
	if !strings.Contains(resp.Error, "no .appdef found") || !strings.Contains(resp.Error, "pass Template") {
		t.Errorf("Error = %q, want no-.appdef refusal with Template hint", resp.Error)
	}
	if !strings.Contains(resp.Error, env.root) {
		t.Errorf("Error = %q, want it to name the target directory %s", resp.Error, env.root)
	}

	// Zero writes: the project root is bit-identical to before.
	after := rootSnapshot(t, env.root)
	if len(after) != len(before) {
		t.Fatalf("project root changed: before=%v after=%v", before, after)
	}
	for k := range after {
		if !before[k] {
			t.Fatalf("dev_generate wrote %q despite refusal; root now: %v", k, after)
		}
	}

	// No manifest may be persisted for a refused generate.
	if len(a.GeneratedManifests) != 0 {
		t.Errorf("GeneratedManifests populated on refusal: %v", mapKeys(a.GeneratedManifests))
	}
}

// TestDevGenerateTemplateScaffoldsExplicitly: Template=true scaffolds the full
// default template into an empty project root and reports Template=true in
// the response (resp semantics unchanged).
func TestDevGenerateTemplateScaffoldsExplicitly(t *testing.T) {
	env := newBP7Project(t, "bp9-tmpl-")
	a := newBP7Actor(t)
	ctx := env.ctx()

	resp, err := a.handleDevGenerate(ctx, gen.AppManagerDevGenerateReq{
		ProjectID: bp7ProjectID(t),
		Template:  true,
	})
	if err != nil {
		t.Fatalf("handleDevGenerate: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("dev_generate error: %s", resp.Error)
	}
	if !resp.Template {
		t.Error("resp.Template = false; want true for explicit template scaffold")
	}

	for _, name := range []string{"app.appdef", "main.gen.go", "handlers.go", "app.manifest.json", "schemas_gen.go", "client.gen.ts", "go.mod"} {
		if !env.exists(name) {
			t.Errorf("expected scaffold artifact %s in project root", name)
		}
	}

	// The generated manifest is persisted under the bare project key (root app).
	if _, ok := a.GeneratedManifests[bp7ProjectID(t)]; !ok {
		t.Errorf("GeneratedManifests missing root key; keys: %v", mapKeys(a.GeneratedManifests))
	}
}

// TestDevGenerateTemplateRefusesGoProjectDir: even with Template=true the
// scaffold refuses a directory that already holds go.mod / *.go and writes
// nothing.
func TestDevGenerateTemplateRefusesGoProjectDir(t *testing.T) {
	env := newBP7Project(t, "bp9-go-")
	env.write("main.go", "package main\n\nfunc main() {}\n")
	a := newBP7Actor(t)
	ctx := env.ctx()

	before := rootSnapshot(t, env.root)

	resp, err := a.handleDevGenerate(ctx, gen.AppManagerDevGenerateReq{
		ProjectID: bp7ProjectID(t),
		Template:  true,
	})
	if err != nil {
		t.Fatalf("handleDevGenerate: %v", err)
	}
	if resp.Error == "" || !strings.Contains(resp.Error, "refused") {
		t.Fatalf("Error = %q, want template-scaffold refusal", resp.Error)
	}
	if resp.Template {
		t.Error("resp.Template = true on refusal")
	}

	after := rootSnapshot(t, env.root)
	if len(after) != len(before) {
		t.Fatalf("project root changed: before=%v after=%v", before, after)
	}
	if len(a.GeneratedManifests) != 0 {
		t.Errorf("GeneratedManifests populated on refusal: %v", mapKeys(a.GeneratedManifests))
	}
}
