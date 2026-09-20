package appmanager

import (
	"os"
	"path/filepath"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
)

func TestGCSupersededArtifacts(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "plugin-proj1-aaaaaaaaaaaaaaaa.exe")
	old1 := filepath.Join(dir, "plugin-proj1-bbbbbbbbbbbbbbbb.exe")
	old2 := filepath.Join(dir, "plugin-proj1-cccccccccccccccc.dll")
	temp := filepath.Join(dir, "plugin-proj1-build-1234567890.exe")
	otherProject := filepath.Join(dir, "plugin-proj2-dddddddddddddddd.exe")
	unrelated := filepath.Join(dir, "notes.txt")
	for _, f := range []string{cur, old1, old2, temp, otherProject, unrelated} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	a := &Actor{Records: map[string]appRecord{}}
	// A sibling app of the same project still references old2 via .dll: it
	// must survive even though it shares the prefix.
	a.Records["app.other"] = appRecord{Manifest: gen.AppManifest{ID: "app.other"}, ArtifactPath: old2}

	if n := a.gcSupersededArtifacts(cur, ""); n != 2 {
		t.Fatalf("removed %d files, want 2 (old1 + stale build temp)", n)
	}
	for _, gone := range []string{old1, temp} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s should be removed, stat err = %v", gone, err)
		}
	}
	for _, kept := range []string{cur, old2, otherProject, unrelated} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s should be kept: %v", kept, err)
		}
	}
}

// The sweep prefix derived from the manifest name must cover every version of
// the app: a version bump sweeps the previous version's binaries too.
func TestGCSupersededArtifactsSweepsAcrossVersions(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "plugin-daily-tools-1.2.4-aaaaaaaaaaaaaaaa.exe")
	prevVersion := filepath.Join(dir, "plugin-daily-tools-1.2.3-bbbbbbbbbbbbbbbb.exe")
	noVersionLegacy := filepath.Join(dir, "plugin-daily-tools-cccccccccccccccc.exe")
	otherApp := filepath.Join(dir, "plugin-other-app-1.0.0-dddddddddddddddd.exe")
	for _, f := range []string{cur, prevVersion, noVersionLegacy, otherApp} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	a := &Actor{Records: map[string]appRecord{}}
	if n := a.gcSupersededArtifacts(cur, pluginhost.PluginArtifactSweepPrefix("Daily Tools")); n != 2 {
		t.Fatalf("removed %d files, want 2 (previous version + legacy no-version build)", n)
	}
	for _, gone := range []string{prevVersion, noVersionLegacy} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s should be removed, stat err = %v", gone, err)
		}
	}
	for _, kept := range []string{cur, otherApp} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s should be kept: %v", kept, err)
		}
	}
}

// A sweep prefix that does not match the keep path must be a no-op, never a
// self-delete.
func TestGCSupersededArtifactsPrefixMismatch(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "plugin-renamed-1.0.0-aaaaaaaaaaaaaaaa.exe")
	if err := os.WriteFile(cur, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &Actor{Records: map[string]appRecord{}}
	if n := a.gcSupersededArtifacts(cur, pluginhost.PluginArtifactSweepPrefix("Other App")); n != 0 {
		t.Fatalf("removed %d files, want 0", n)
	}
	if _, err := os.Stat(cur); err != nil {
		t.Errorf("keep path should survive prefix mismatch: %v", err)
	}
}

func TestGCSupersededArtifactsLockedFileIsSkipped(t *testing.T) {
	if filepath.Separator == '/' && os.Getenv("OS") == "" {
		// Unix remove of an open file succeeds; the skip path is Windows-only.
		t.Skip("lock behavior is Windows-specific")
	}
	dir := t.TempDir()
	cur := filepath.Join(dir, "plugin-proj1-aaaaaaaaaaaaaaaa.exe")
	locked := filepath.Join(dir, "plugin-proj1-bbbbbbbbbbbbbbbb.exe")
	for _, f := range []string{cur, locked} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fh, err := os.Open(locked)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	// os.Open alone does not prevent deletion on Unix; on Windows neither
	// does it (delete-share modes) — the real lock comes from a mapped exe.
	// This test pins the no-panic, continue-on-error contract only.
	a := &Actor{Records: map[string]appRecord{}}
	_ = a.gcSupersededArtifacts(cur, "")
}

func TestGCSupersededArtifactsEmptyPath(t *testing.T) {
	a := &Actor{}
	if n := a.gcSupersededArtifacts("", pluginhost.PluginArtifactSweepPrefix("x")); n != 0 {
		t.Fatalf("empty path removed %d, want 0", n)
	}
}
