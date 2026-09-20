package lspserver

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// withPath replaces PATH for the duration of the test and restores it after.
func withPath(t *testing.T, dir string) {
	t.Helper()
	orig := os.Getenv("PATH")
	os.Setenv("PATH", dir)
	t.Cleanup(func() { os.Setenv("PATH", orig) })
}

// TestFindGoplsInstall_Path verifies that a gopls on PATH wins over the Go
// toolchain install dir.
func TestFindGoplsInstall_Path(t *testing.T) {
	pathDir := t.TempDir()
	bin := writeFakeExecutable(t, pathDir, "gopls")
	withPath(t, pathDir)

	got, err := findGoplsInstall()
	if err != nil {
		t.Fatalf("findGoplsInstall: %v", err)
	}
	if got != bin {
		t.Fatalf("got %q, want %q", got, bin)
	}
}

// TestFindGoplsInstall_Missing verifies that with neither gopls nor go
// reachable on PATH the probe reports not found with an actionable error.
func TestFindGoplsInstall_Missing(t *testing.T) {
	withPath(t, t.TempDir())

	if _, err := findGoplsInstall(); err == nil {
		t.Fatal("expected error when gopls and go are both unavailable")
	} else if !strings.Contains(err.Error(), "lsp.install") && !strings.Contains(err.Error(), "go install") {
		t.Fatalf("error should point at the install remedy: %v", err)
	}
}

// TestProbeGoInstall covers the probe outcomes: PATH hit reports sourcePath,
// nothing found reports not installed.
func TestProbeGoInstall(t *testing.T) {
	withPath(t, t.TempDir())
	st := probeGoInstall()
	if st.Installed {
		t.Fatalf("expected not installed with empty PATH, got %+v", st)
	}

	pathDir := t.TempDir()
	bin := writeFakeExecutable(t, pathDir, "gopls")
	withPath(t, pathDir)
	st = probeGoInstall()
	if !st.Installed {
		t.Fatalf("expected PATH gopls found, got %+v", st)
	}
	if st.DownloadSource != sourcePath {
		t.Fatalf("expected sourcePath, got %q", st.DownloadSource)
	}
	if st.BinaryPath != bin {
		t.Fatalf("expected BinaryPath %q, got %q", bin, st.BinaryPath)
	}
}

// TestGoInstallTool_MissingToolchain verifies the toolchain-managed install
// fails with an actionable error when no go binary is reachable.
func TestGoInstallTool_MissingToolchain(t *testing.T) {
	withPath(t, t.TempDir())

	_, err := goInstallTool(context.Background(), &goInstallSpec{Tool: "gopls", Module: "golang.org/x/tools/gopls", Version: goplsVersion})
	if err == nil {
		t.Fatal("expected error with no go toolchain on PATH")
	}
	if !strings.Contains(err.Error(), "go toolchain") {
		t.Fatalf("error should mention the missing go toolchain: %v", err)
	}
}

// TestHandleInstall_GoFailsWithoutToolchain drives the lsp.install go branch
// end to end on its failure path (no go toolchain reachable) and verifies the
// failed progress event reaches the emit channel.
func TestHandleInstall_GoFailsWithoutToolchain(t *testing.T) {
	withPath(t, t.TempDir())
	a, ctx := installTestActor(t)

	if _, err := a.handleInstall(ctx, gen.LspInstallReq{Language: "go"}); err != nil {
		t.Fatalf("handleInstall go: %v", err)
	}
	waitFor(t, 10*time.Second, func() bool {
		events := progressEvents(t, ctx, "go")
		return len(events) > 0 && events[len(events)-1].State == installStateFailed
	})
}

// TestHandleStatus_GoManagedRecord verifies that a persisted toolchain-managed
// record (no HTTP asset) is validated by binary existence and reported as
// sourceManaged, and that a vanished binary falls back to the native probe.
func TestHandleStatus_GoManagedRecord(t *testing.T) {
	a, _ := installTestActor(t)
	withPath(t, t.TempDir())

	binDir := t.TempDir()
	bin := writeFakeExecutable(t, binDir, "gopls")
	a.installMu.Lock()
	a.installs["go"] = managedInstall{
		Tool:        "gopls",
		Version:     goplsVersion,
		Dir:         binDir,
		BinaryPath:  bin,
		InstalledAt: "2026-01-01T00:00:00Z",
	}
	a.installMu.Unlock()

	st := a.installStateFor("go")
	if !st.Installed {
		t.Fatalf("expected managed record honored, got %+v", st)
	}
	if st.DownloadSource != sourceManaged {
		t.Fatalf("expected sourceManaged, got %q", st.DownloadSource)
	}
	if st.BinaryPath != bin || st.Version != goplsVersion {
		t.Fatalf("record fields not preserved: %+v", st)
	}

	// Vanished binary invalidates the record; probe takes over.
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	st = a.installStateFor("go")
	if st.Installed {
		t.Fatalf("expected not installed after binary removal, got %+v", st)
	}
}

// TestFindGoplsInstall_GoBinDir verifies the GOBIN/GOPATH-bin fallback by
// pointing PATH at a fake `go` that reports a controlled GOPATH.
func TestFindGoplsInstall_GoBinDir(t *testing.T) {
	fakeGoDir := t.TempDir()
	gopath := t.TempDir()
	goBinDir := filepath.Join(gopath, "bin")
	if err := os.MkdirAll(goBinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := writeFakeExecutable(t, goBinDir, "gopls")

	if runtime.GOOS == "windows" {
		script := "@echo off\r\necho.\r\necho " + gopath + "\r\n"
		if err := os.WriteFile(filepath.Join(fakeGoDir, "go.bat"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	} else {
		script := "#!/bin/sh\necho \"\"\necho \"" + gopath + "\"\n"
		if err := os.WriteFile(filepath.Join(fakeGoDir, "go"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	withPath(t, fakeGoDir)

	got := goplsInGoInstallDir()
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
