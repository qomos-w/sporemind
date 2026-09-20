package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
	"github.com/qomos-w/sporemind/pkg/util"
)

func TestEnsureAppDirsCreatesAppDir(t *testing.T) {
	dir := t.TempDir()
	config.SetDataDirForTest(dir)
	t.Cleanup(config.ResetForTest)

	ctx := testutil.AdminCtx(testutil.GenActorID())
	ensureAppDirs(ctx)

	// Only app/ is auto-created; dev-app/ and sporeapp/ are created lazily.
	path := filepath.Join(systemMetaProjectPath(), appDirName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected dir %q to exist: %v", appDirName, err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is not a directory", appDirName)
	}

	for _, name := range []string{devAppDirName, sporeAppDirName} {
		p := filepath.Join(systemMetaProjectPath(), name)
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("expected %q to NOT be auto-created, but stat returned %v", name, err)
		}
	}
}

func TestScanDevApps(t *testing.T) {
	mounts := []domain.ProjectRef{
		{Name: "my-plugin", Path: "/some/path/my-plugin", AppKind: devAppDirName},
		{Name: "hidden-but-still-listed", Path: "/other/hidden", AppKind: devAppDirName},
		{Name: "unrelated", Path: "/elsewhere", AppKind: ""},
		{Name: "spore-thing", Path: "/spore/thing", AppKind: sporeAppDirName},
	}

	nodes := scanDevApps(mounts)
	if len(nodes) != 2 {
		t.Fatalf("expected 2 dev-app nodes, got %d", len(nodes))
	}
	if nodes[0].Name != "my-plugin" {
		t.Errorf("expected node name %q, got %q", "my-plugin", nodes[0].Name)
	}
	if nodes[0].Kind != "dev-app" {
		t.Errorf("expected kind %q, got %q", "dev-app", nodes[0].Kind)
	}
	if nodes[0].Path != "/some/path/my-plugin" {
		t.Errorf("expected path %q, got %q", "/some/path/my-plugin", nodes[0].Path)
	}
	if nodes[0].ID != "dev-app:my-plugin" {
		t.Errorf("expected id %q, got %q", "dev-app:my-plugin", nodes[0].ID)
	}
	if nodes[1].Name != "hidden-but-still-listed" {
		t.Errorf("expected node name %q, got %q", "hidden-but-still-listed", nodes[1].Name)
	}
}

func TestScanSporeApps(t *testing.T) {
	mounts := []domain.ProjectRef{
		{Name: "todo-app", Path: "/custom/todo-app", AppKind: sporeAppDirName},
		{Name: "dev-thing", Path: "/dev/thing", AppKind: devAppDirName},
		{Name: "regular", Path: "/regular", AppKind: ""},
	}

	nodes := scanSporeApps(mounts)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 sporeapp node, got %d", len(nodes))
	}
	if nodes[0].Name != "todo-app" {
		t.Errorf("expected node name %q, got %q", "todo-app", nodes[0].Name)
	}
	if nodes[0].Kind != "sporeapp" {
		t.Errorf("expected kind %q, got %q", "sporeapp", nodes[0].Kind)
	}
	if nodes[0].Path != "/custom/todo-app" {
		t.Errorf("expected path %q, got %q", "/custom/todo-app", nodes[0].Path)
	}
	if nodes[0].ID != "sporeapp:todo-app" {
		t.Errorf("expected id %q, got %q", "sporeapp:todo-app", nodes[0].ID)
	}
}

func TestExtraBundlesForAppKind(t *testing.T) {
	t.Run("dev-app project gets plugin-dev bundle", func(t *testing.T) {
		got := extraBundlesForAppKind(devAppDirName, nil)
		if len(got) != 1 || got[0] != agentkit.PluginDevBundleID {
			t.Fatalf("dev-app spawn should attach %q, got %v", agentkit.PluginDevBundleID, got)
		}
	})

	t.Run("non dev-app tiers and regular projects are untouched", func(t *testing.T) {
		for _, kind := range []string{appDirName, sporeAppDirName, ""} {
			if got := extraBundlesForAppKind(kind, nil); len(got) != 0 {
				t.Errorf("appKind %q: expected no extra bundles, got %v", kind, got)
			}
			existing := []string{"builtin:bundle:file-tools"}
			if got := extraBundlesForAppKind(kind, existing); len(got) != 1 {
				t.Errorf("appKind %q: existing list must pass through unchanged, got %v", kind, got)
			}
		}
	})

	t.Run("idempotent and order-preserving", func(t *testing.T) {
		existing := []string{"builtin:bundle:file-tools", agentkit.PluginDevBundleID}
		got := extraBundlesForAppKind(devAppDirName, existing)
		if len(got) != 2 || got[0] != "builtin:bundle:file-tools" || got[1] != agentkit.PluginDevBundleID {
			t.Fatalf("already-present bundle must not be duplicated, got %v", got)
		}
	})
}

// TestOwnerInheritedPluginDevBundle pins the spawn_assign inheritance
// decision: the worker inherits the plugin-dev bundle only when the owner
// agent's component mounts include an enabled plugin-dev bundle mount.
// The result merges through extraBundlesForAppKind idempotently — a dev-app
// project must still never produce a duplicate.
func TestOwnerInheritedPluginDevBundle(t *testing.T) {
	t.Run("enabled plugin-dev mount is inherited", func(t *testing.T) {
		got := ownerInheritedPluginDevBundle([]domain.AgentComponentMount{
			{CardID: "builtin:bundle:file-tools", Kind: "bundle", Enabled: true},
			{CardID: agentkit.PluginDevBundleID, Kind: "bundle", Enabled: true},
		})
		if len(got) != 1 || got[0] != agentkit.PluginDevBundleID {
			t.Fatalf("expected inherited [%s], got %v", agentkit.PluginDevBundleID, got)
		}
	})

	t.Run("disabled plugin-dev mount is not inherited", func(t *testing.T) {
		if got := ownerInheritedPluginDevBundle([]domain.AgentComponentMount{
			{CardID: agentkit.PluginDevBundleID, Kind: "bundle", Enabled: false},
		}); len(got) != 0 {
			t.Fatalf("disabled mount must not inherit, got %v", got)
		}
	})

	t.Run("other bundles and empty mount lists inherit nothing", func(t *testing.T) {
		for _, mounts := range [][]domain.AgentComponentMount{
			{
				{CardID: "builtin:bundle:file-tools", Kind: "bundle", Enabled: true},
			},
			{
				{CardID: "builtin:bundle:file-tools", Kind: "bundle", Enabled: true},
				{CardID: agentkit.PluginDevBundleID, Kind: "skill", Enabled: true},
			},
			{},
			nil,
		} {
			if got := ownerInheritedPluginDevBundle(mounts); len(got) != 0 {
				t.Errorf("mounts %+v: expected no inheritance, got %v", mounts, got)
			}
		}
	})

	t.Run("merged through extraBundlesForAppKind without duplication", func(t *testing.T) {
		inherited := ownerInheritedPluginDevBundle([]domain.AgentComponentMount{
			{CardID: agentkit.PluginDevBundleID, Kind: "bundle", Enabled: true},
		})
		// dev-app project + owner-mounted bundle: still exactly one entry.
		got := extraBundlesForAppKind(devAppDirName, inherited)
		if len(got) != 1 || got[0] != agentkit.PluginDevBundleID {
			t.Fatalf("dev-app project must dedup the inherited bundle, got %v", got)
		}
		// regular project + owner-mounted bundle: inherited entry passes through.
		got = extraBundlesForAppKind("", inherited)
		if len(got) != 1 || got[0] != agentkit.PluginDevBundleID {
			t.Fatalf("regular project must keep the inherited bundle, got %v", got)
		}
	})
}

func TestInferAppKind(t *testing.T) {
	dir := t.TempDir()
	config.SetDataDirForTest(dir)
	t.Cleanup(config.ResetForTest)

	sysPath := util.NormalizePath(systemMetaProjectPath())

	tests := []struct {
		relPath string
		want    string
	}{
		{filepath.Join(devAppDirName, "my-plugin"), devAppDirName},
		{filepath.Join(sporeAppDirName, "todo"), sporeAppDirName},
		{filepath.Join(appDirName, "browser"), appDirName},
		{"random-project", ""},
	}

	for _, tt := range tests {
		fullPath := util.NormalizePath(filepath.Join(systemMetaProjectPath(), tt.relPath))
		got := inferAppKind(fullPath, sysPath)
		if got != tt.want {
			t.Errorf("inferAppKind(%q) = %q, want %q", tt.relPath, got, tt.want)
		}
	}
}

func TestIsAppTierSubPath(t *testing.T) {
	dir := t.TempDir()
	config.SetDataDirForTest(dir)
	t.Cleanup(config.ResetForTest)

	sysPath := util.NormalizePath(systemMetaProjectPath())

	// app-tier subdirectory → true
	devAppSub := util.NormalizePath(filepath.Join(systemMetaProjectPath(), devAppDirName, "my-plugin"))
	if !isAppTierSubPath(devAppSub, sysPath) {
		t.Errorf("expected dev-app subdir to be app-tier")
	}

	// non-app-tier system subdirectory → false
	otherSub := util.NormalizePath(filepath.Join(systemMetaProjectPath(), "agents"))
	if isAppTierSubPath(otherSub, sysPath) {
		t.Errorf("expected non-app-tier system subdir to be rejected")
	}

	// external path → false
	external := util.NormalizePath(filepath.Join(dir, "my-project"))
	if isAppTierSubPath(external, sysPath) {
		t.Errorf("expected external path to not be app-tier")
	}
}
