package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRealVFS_SandboxEscape(t *testing.T) {
	tmp := t.TempDir()
	vfs := NewRealVFS(tmp)

	// Writing inside sandbox should work.
	if err := vfs.WriteFile("ok.txt", []byte("hello"), 0644); err != nil {
		t.Fatalf("write inside sandbox: %v", err)
	}

	// Reading outside sandbox should fail.
	_, err := vfs.ReadFile("../escape.txt")
	if err == nil {
		t.Fatal("expected sandbox escape error, got nil")
	}
	if !strings.Contains(err.Error(), "escapes sandbox") {
		t.Fatalf("expected 'escapes sandbox' error, got: %v", err)
	}
}

func TestBuiltinLsAndCat(t *testing.T) {
	tmp := t.TempDir()
	vfs := NewRealVFS(tmp)

	_ = vfs.WriteFile("a.txt", []byte("alpha"), 0644)
	_ = vfs.MkdirAll("sub", 0755)
	_ = vfs.WriteFile("sub/b.txt", []byte("beta"), 0644)

	stdout, stderr, code := cmdLs(vfs, []string{})
	if code != 0 {
		t.Fatalf("ls failed: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "a.txt") {
		t.Fatalf("ls output missing a.txt: %q", stdout)
	}
	if !strings.Contains(stdout, "sub/") {
		t.Fatalf("ls output missing sub/: %q", stdout)
	}

	stdout, stderr, code = cmdCat(vfs, []string{"a.txt"})
	if code != 0 {
		t.Fatalf("cat failed: code=%d stderr=%s", code, stderr)
	}
	if stdout != "alpha" {
		t.Fatalf("cat output = %q, want alpha", stdout)
	}
}

func TestBuiltinCpMvRm(t *testing.T) {
	tmp := t.TempDir()
	vfs := NewRealVFS(tmp)

	_ = vfs.WriteFile("src.txt", []byte("data"), 0644)

	_, stderr, code := cmdCp(vfs, []string{"src.txt", "dst.txt"})
	if code != 0 {
		t.Fatalf("cp failed: %s", stderr)
	}
	b, _ := vfs.ReadFile("dst.txt")
	if string(b) != "data" {
		t.Fatalf("cp result = %q, want data", string(b))
	}

	_, stderr, code = cmdMv(vfs, []string{"dst.txt", "final.txt"})
	if code != 0 {
		t.Fatalf("mv failed: %s", stderr)
	}
	_, err := vfs.Stat("dst.txt")
	if !os.IsNotExist(err) {
		t.Fatal("mv did not remove source")
	}
	b, _ = vfs.ReadFile("final.txt")
	if string(b) != "data" {
		t.Fatalf("mv result = %q, want data", string(b))
	}

	_, stderr, code = cmdRm(vfs, []string{"final.txt"})
	if code != 0 {
		t.Fatalf("rm failed: %s", stderr)
	}
	_, err = vfs.Stat("final.txt")
	if !os.IsNotExist(err) {
		t.Fatal("rm did not remove file")
	}
}

func TestBuiltinCpMvDirs(t *testing.T) {
	tmp := t.TempDir()
	vfs := NewRealVFS(tmp)

	_ = vfs.MkdirAll("src/sub", 0755)
	_ = vfs.WriteFile("src/a.txt", []byte("alpha"), 0644)
	_ = vfs.WriteFile("src/sub/b.txt", []byte("beta"), 0644)

	// cp -r src dst
	_, stderr, code := cmdCp(vfs, []string{"src", "dst"})
	if code != 0 {
		t.Fatalf("cp dir failed: %s", stderr)
	}
	b, _ := vfs.ReadFile("dst/a.txt")
	if string(b) != "alpha" {
		t.Fatalf("cp dir a.txt = %q, want alpha", string(b))
	}
	b, _ = vfs.ReadFile("dst/sub/b.txt")
	if string(b) != "beta" {
		t.Fatalf("cp dir sub/b.txt = %q, want beta", string(b))
	}

	// mv dst final
	_, stderr, code = cmdMv(vfs, []string{"dst", "final"})
	if code != 0 {
		t.Fatalf("mv dir failed: %s", stderr)
	}
	_, err := vfs.Stat("dst")
	if !os.IsNotExist(err) {
		t.Fatal("mv dir did not remove source")
	}
	b, _ = vfs.ReadFile("final/sub/b.txt")
	if string(b) != "beta" {
		t.Fatalf("mv dir result = %q, want beta", string(b))
	}

	// rm -r final
	_, stderr, code = cmdRm(vfs, []string{"-r", "final"})
	if code != 0 {
		t.Fatalf("rm -r failed: %s", stderr)
	}
	_, err = vfs.Stat("final")
	if !os.IsNotExist(err) {
		t.Fatal("rm -r did not remove dir")
	}
}

func TestBuiltinRmDashDash(t *testing.T) {
	tmp := t.TempDir()
	vfs := NewRealVFS(tmp)

	_ = vfs.WriteFile("-rf", []byte("tricky"), 0644)

	// Without --, -rf would be parsed as a flag.
	_, stderr, code := cmdRm(vfs, []string{"--", "-rf"})
	if code != 0 {
		t.Fatalf("rm -- failed: %s", stderr)
	}
	_, err := vfs.Stat("-rf")
	if !os.IsNotExist(err) {
		t.Fatal("rm -- did not remove file")
	}
}

func TestBuiltinLsFlags(t *testing.T) {
	tmp := t.TempDir()
	vfs := NewRealVFS(tmp)

	_ = vfs.WriteFile("a.txt", []byte("alpha"), 0644)
	_ = vfs.MkdirAll("sub", 0755)

	stdout, stderr, code := cmdLs(vfs, []string{"-l", "sub"})
	if code != 0 {
		t.Fatalf("ls -l sub failed: %s", stderr)
	}
	if strings.Contains(stdout, "a.txt") {
		t.Fatalf("ls -l sub should not contain a.txt: %q", stdout)
	}
}

func TestResolveEmptyPath(t *testing.T) {
	tmp := t.TempDir()
	vfs := NewRealVFS(tmp)

	abs, err := vfs.resolve("")
	if err != nil {
		t.Fatalf("resolve empty: %v", err)
	}
	if abs != vfs.Root() {
		t.Fatalf("resolve empty = %q, want %q", abs, vfs.Root())
	}
}

func TestBuiltinRmCompoundFlags(t *testing.T) {
	tmp := t.TempDir()
	vfs := NewRealVFS(tmp)

	_ = vfs.MkdirAll("dir1", 0755)
	_ = vfs.WriteFile("dir1/a.txt", []byte("a"), 0644)

	// -fr should be parsed as recursive.
	_, stderr, code := cmdRm(vfs, []string{"-fr", "dir1"})
	if code != 0 {
		t.Fatalf("rm -fr failed: %s", stderr)
	}
	_, err := vfs.Stat("dir1")
	if !os.IsNotExist(err) {
		t.Fatal("rm -fr did not remove dir")
	}

	// -r -f as separate flags.
	_ = vfs.MkdirAll("dir2", 0755)
	_ = vfs.WriteFile("dir2/b.txt", []byte("b"), 0644)
	_, stderr, code = cmdRm(vfs, []string{"-r", "-f", "dir2"})
	if code != 0 {
		t.Fatalf("rm -r -f failed: %s", stderr)
	}
	_, err = vfs.Stat("dir2")
	if !os.IsNotExist(err) {
		t.Fatal("rm -r -f did not remove dir")
	}
}

func TestBuiltinLsDashDash(t *testing.T) {
	tmp := t.TempDir()
	vfs := NewRealVFS(tmp)

	_ = vfs.MkdirAll("-l", 0755)
	_ = vfs.WriteFile("-l/inside.txt", []byte("x"), 0644)
	_ = vfs.WriteFile("a.txt", []byte("y"), 0644)

	// Without --, -l is a flag and target falls back to "."
	stdout, _, code := cmdLs(vfs, []string{"-l"})
	if code != 0 {
		t.Fatal("ls -l failed")
	}
	if !strings.Contains(stdout, "a.txt") {
		t.Fatalf("ls -l should list cwd: %q", stdout)
	}

	// With --, -l is the directory name.
	stdout, _, code = cmdLs(vfs, []string{"--", "-l"})
	if code != 0 {
		t.Fatal("ls -- -l failed")
	}
	if !strings.Contains(stdout, "inside.txt") {
		t.Fatalf("ls -- -l should list inside dir: %q", stdout)
	}
}

func TestBuiltinMvIntoExistingDir(t *testing.T) {
	tmp := t.TempDir()
	vfs := NewRealVFS(tmp)

	_ = vfs.WriteFile("file.txt", []byte("data"), 0644)
	_ = vfs.MkdirAll("box", 0755)

	// mv file.txt box/  →  box/file.txt
	_, stderr, code := cmdMv(vfs, []string{"file.txt", "box"})
	if code != 0 {
		t.Fatalf("mv into dir failed: %s", stderr)
	}
	_, err := vfs.Stat("file.txt")
	if !os.IsNotExist(err) {
		t.Fatal("mv did not remove source")
	}
	b, _ := vfs.ReadFile("box/file.txt")
	if string(b) != "data" {
		t.Fatalf("mv into dir result = %q, want data", string(b))
	}
}

func TestWriteFileMkdirAllEscape(t *testing.T) {
	tmp := t.TempDir()
	vfs := NewRealVFS(tmp)

	// WriteFile with parent traversal should fail.
	err := vfs.WriteFile("../escape.txt", []byte("bad"), 0644)
	if err == nil {
		t.Fatal("expected sandbox escape error for WriteFile, got nil")
	}

	// MkdirAll with parent traversal should fail.
	err = vfs.MkdirAll("../escape_dir", 0755)
	if err == nil {
		t.Fatal("expected sandbox escape error for MkdirAll, got nil")
	}

	// RemoveAll with parent traversal should fail.
	err = vfs.RemoveAll("../escape_rm")
	if err == nil {
		t.Fatal("expected sandbox escape error for RemoveAll, got nil")
	}
}

// TestResolveShellTimeout_DefaultsWhenZero covers the "field omitted" path.
// A zero Timeout (the schema's optional default) must fall back to the
// configured default so the process actually has time to run.
func TestResolveShellTimeout_DefaultsWhenZero(t *testing.T) {
	got := ResolveShellTimeout(0)
	if got != DefaultShellTimeoutMs*time.Millisecond {
		t.Fatalf("ResolveShellTimeout(0) = %v, want %v", got, DefaultShellTimeoutMs*time.Millisecond)
	}
}

// TestResolveShellTimeout_ClampsBelowMinimum reproduces the gofmt bug:
// LLMs that pass seconds (e.g. 30) instead of milliseconds must not get a
// 30ms timeout that fires before the process produces any output.
func TestResolveShellTimeout_ClampsBelowMinimum(t *testing.T) {
	want := DefaultShellTimeoutMs * time.Millisecond
	cases := []int32{1, 30, 100, 500, 999}
	for _, ms := range cases {
		got := ResolveShellTimeout(ms)
		if got != want {
			t.Fatalf("ResolveShellTimeout(%d) = %v, want %v (clamped)", ms, got, want)
		}
	}
}

// TestResolveShellTimeout_ClampsNegative guards against negative inputs
// from malformed requests.
func TestResolveShellTimeout_ClampsNegative(t *testing.T) {
	want := DefaultShellTimeoutMs * time.Millisecond
	got := ResolveShellTimeout(-5)
	if got != want {
		t.Fatalf("ResolveShellTimeout(-5) = %v, want %v", got, want)
	}
}

// TestResolveShellTimeout_PassesValidValues ensures the clamp does not
// swallow legitimate timeout values at or above the 1s minimum, including
// values well above the default.
func TestResolveShellTimeout_PassesValidValues(t *testing.T) {
	cases := []struct {
		ms   int32
		want time.Duration
	}{
		{1000, 1 * time.Second},
		{2000, 2 * time.Second},
		{30000, 30 * time.Second},
		{60000, 60 * time.Second},
		{300000, 5 * time.Minute},
	}
	for _, tc := range cases {
		got := ResolveShellTimeout(tc.ms)
		if got != tc.want {
			t.Fatalf("ResolveShellTimeout(%d) = %v, want %v", tc.ms, got, tc.want)
		}
	}
}

// TestResolveShellDir_EmptyPassesThrough ensures the empty case hands the
// default back to the caller rather than running Stat on the working directory.
func TestResolveShellDir_EmptyPassesThrough(t *testing.T) {
	resolved, warning, err := ResolveShellDir("")
	if err != nil || resolved != "" || warning != "" {
		t.Fatalf(`ResolveShellDir("") = (%q, %q, %v), want ("", "", nil)`, resolved, warning, err)
	}
}

// TestResolveShellDir_DirectoryAccepted ensures real directories round-trip
// without warnings.
func TestResolveShellDir_DirectoryAccepted(t *testing.T) {
	tmp := t.TempDir()
	resolved, warning, err := ResolveShellDir(tmp)
	if err != nil || resolved != tmp || warning != "" {
		t.Fatalf("ResolveShellDir(%q) = (%q, %q, %v), want (%q, %q, nil)", tmp, resolved, warning, err, tmp, "")
	}
}

// TestResolveShellDir_FileFallsBackToParent covers the primary regression:
// an LLM passing a file path as Dir must not surface Windows ERROR_DIRECTORY
// ("The directory name is invalid"). The command still runs, in the parent.
func TestResolveShellDir_FileFallsBackToParent(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "target.go")
	if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	resolved, warning, err := ResolveShellDir(filePath)
	if err != nil {
		t.Fatalf("ResolveShellDir(file) err = %v, want nil", err)
	}
	if resolved != tmp {
		t.Fatalf("resolved = %q, want parent %q", resolved, tmp)
	}
	if !strings.Contains(warning, "target.go") {
		t.Fatalf("warning %q must mention the original file name %q", warning, "target.go")
	}
	if !strings.Contains(warning, "ran in parent") {
		t.Fatalf("warning %q must explain the fallback", warning)
	}
}

// TestResolveShellDir_MissingPathFailsClear ensures a non-existent path
// surfaces a Stat-shaped error instead of being forwarded to CreateProcess.
func TestResolveShellDir_MissingPathFailsClear(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	resolved, warning, err := ResolveShellDir(missing)
	if err == nil {
		t.Fatalf("ResolveShellDir(%q) err = nil, want error", missing)
	}
	if resolved != "" || warning != "" {
		t.Fatalf("on error, resolved/warning must be empty: (%q, %q)", resolved, warning)
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("error %q must mention the offending path %q", err.Error(), missing)
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("error %q should say 'does not exist' instead of forwarding the raw syscall", err.Error())
	}
}
