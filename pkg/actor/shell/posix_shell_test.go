package shell

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/util"
)

func TestDeriveExtraBin_GitForWindowsBinLayout(t *testing.T) {
	tmp := t.TempDir()
	gitRoot := filepath.Join(tmp, "Git")
	binDir := filepath.Join(gitRoot, "bin")
	usrBin := filepath.Join(gitRoot, "usr", "bin")
	mingwBin := filepath.Join(gitRoot, "mingw64", "bin")
	for _, d := range []string{binDir, usrBin, mingwBin} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	shellPath := filepath.Join(binDir, "bash.exe")
	if err := os.WriteFile(shellPath, []byte{}, 0755); err != nil {
		t.Fatal(err)
	}

	got := util.DeriveExtraBin(shellPath)
	want := []string{binDir, usrBin, mingwBin}
	if !sameSet(got, want) {
		t.Fatalf("util.DeriveExtraBin = %v, want %v (any order)", got, want)
	}
}

func TestDeriveExtraBin_GitForWindowsUsrBinLayout(t *testing.T) {
	tmp := t.TempDir()
	gitRoot := filepath.Join(tmp, "Git")
	binDir := filepath.Join(gitRoot, "bin")
	usrBin := filepath.Join(gitRoot, "usr", "bin")
	mingwBin := filepath.Join(gitRoot, "mingw64", "bin")
	for _, d := range []string{binDir, usrBin, mingwBin} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	shellPath := filepath.Join(usrBin, "sh.exe")
	if err := os.WriteFile(shellPath, []byte{}, 0755); err != nil {
		t.Fatal(err)
	}

	got := util.DeriveExtraBin(shellPath)
	want := []string{binDir, usrBin, mingwBin}
	if !sameSet(got, want) {
		t.Fatalf("util.DeriveExtraBin = %v, want %v (any order)", got, want)
	}
}

func TestDeriveExtraBin_LoneBin(t *testing.T) {
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	shellPath := filepath.Join(binDir, "sh")
	if err := os.WriteFile(shellPath, []byte{}, 0755); err != nil {
		t.Fatal(err)
	}

	got := util.DeriveExtraBin(shellPath)
	if len(got) != 1 || got[0] != binDir {
		t.Fatalf("util.DeriveExtraBin = %v, want [%s]", got, binDir)
	}
}

func TestDeriveExtraBin_NonBinParent(t *testing.T) {
	shellPath := filepath.Join("custom", "tools", "myshell")
	got := util.DeriveExtraBin(shellPath)
	wantDir := filepath.Join("custom", "tools")
	if len(got) != 1 || got[0] != wantDir {
		t.Fatalf("util.DeriveExtraBin = %v, want [%s]", got, wantDir)
	}
}

func TestEnvWithExtras_PrependsToPath(t *testing.T) {
	extras := []string{"zzz_test_only_a", "zzz_test_only_b"}
	env := envWithExtras(extras)
	if env == nil {
		t.Fatal("envWithExtras returned nil for non-empty extras")
	}
	sep := string(os.PathListSeparator)
	prefix := extras[0] + sep + extras[1] + sep
	found := false
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		key := kv[:eq]
		if runtime.GOOS == "windows" {
			if !strings.EqualFold(key, "PATH") {
				continue
			}
		} else if key != "PATH" {
			continue
		}
		val := kv[eq+1:]
		if !strings.HasPrefix(val, prefix) {
			t.Fatalf("PATH %q does not start with %q", val, prefix)
		}
		found = true
		break
	}
	if !found {
		t.Fatal("PATH entry not found in returned env")
	}
}

func TestEnvWithExtras_NoExtras(t *testing.T) {
	if got := envWithExtras(nil); got != nil {
		t.Fatalf("envWithExtras(nil) = %v, want nil", got)
	}
	if got := envWithExtras([]string{}); got != nil {
		t.Fatalf("envWithExtras([]) = %v, want nil", got)
	}
}

func TestResolveShell_FindsSomething(t *testing.T) {
	sh, err := resolveShell()
	if err != nil {
		t.Skipf("no shell available on this machine: %v", err)
	}
	if sh.path == "" {
		t.Fatal("resolveShell returned empty path with nil error")
	}
	if _, err := os.Stat(sh.path); err != nil {
		t.Fatalf("resolved shell %q does not exist: %v", sh.path, err)
	}
}

// TestAutoDiscovery_LocalShell exercises resolveShell on the host machine and
// prints what was found. On Windows it asserts discovery must succeed. The
// discovered shell may be bash/git-bash or PowerShell, depending on what is
// installed.
func TestAutoDiscovery_LocalShell(t *testing.T) {
	sh, err := resolveShell()
	if err != nil {
		if runtime.GOOS == "windows" {
			t.Fatalf("expected auto-discovery to succeed on Windows: %v", err)
		}
		t.Skipf("no shell on this machine: %v", err)
	}
	t.Logf("discovered shell: %s", sh.path)
	t.Logf("extra bin dirs:")
	for _, d := range sh.extraBin {
		t.Logf("  %s", d)
	}

	out, err := exec.Command(sh.path, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("running %q --version failed: %v\n%s", sh.path, err, out)
	}
	first := string(out)
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	t.Logf("version:          %s", first)

	if runtime.GOOS != "windows" {
		return
	}
	// On Windows, auto-discovery is handled entirely by util.DetectShellInfo().
	// The resolved shell may be bash/git-bash or PowerShell; either is valid.
	if !strings.Contains(strings.ToLower(sh.path), "bash") && !strings.Contains(strings.ToLower(sh.path), "powershell") && !strings.Contains(strings.ToLower(sh.path), "pwsh") {
		t.Logf("note: discovered shell %q does not look like bash or powershell", sh.path)
	}
}

func TestLookPathWithExtras_FindsInExtras(t *testing.T) {
	tmp := t.TempDir()
	name := "sporemind_test_tool"
	if runtime.GOOS == "windows" {
		name = "sporemind_test_tool.exe"
	}
	path := filepath.Join(tmp, name)
	if err := os.WriteFile(path, []byte{}, 0755); err != nil {
		t.Fatal(err)
	}

	lookup := "sporemind_test_tool"
	got := lookPathWithExtras(lookup, []string{tmp})
	if got != path {
		t.Fatalf("lookPathWithExtras = %q, want %q", got, path)
	}
}

func TestLookPathWithExtras_AbsolutePathPassthrough(t *testing.T) {
	abs := filepath.Join("custom", "tool")
	if !filepath.IsAbs(abs) {
		// Use a guaranteed-absolute path.
		if runtime.GOOS == "windows" {
			abs = `C:\custom\tool.exe`
		} else {
			abs = "/custom/tool"
		}
	}
	got := lookPathWithExtras(abs, nil)
	if got != abs {
		t.Fatalf("lookPathWithExtras(%q) = %q, want passthrough", abs, got)
	}
}

func TestLookPathWithExtras_PathSeparatorPassthrough(t *testing.T) {
	rel := filepath.Join("sub", "tool")
	got := lookPathWithExtras(rel, nil)
	if got != rel {
		t.Fatalf("lookPathWithExtras(%q) = %q, want passthrough", rel, got)
	}
}

func TestLookPathWithExtras_MissingReturnsEmpty(t *testing.T) {
	got := lookPathWithExtras("zzz_nonexistent_binary_sporemind_test", []string{t.TempDir()})
	if got != "" {
		t.Fatalf("lookPathWithExtras for missing tool = %q, want empty", got)
	}
}

func TestSmoke_BashShCEcho(t *testing.T) {
	sh, err := resolveShell()
	if err != nil {
		t.Skipf("no shell: %v", err)
	}
	c := exec.Command(sh.path, sh.flag, "echo hello-sporemind")
	if env := envWithExtras(sh.extraBin); env != nil {
		c.Env = env
	}
	out, err := c.Output()
	if err != nil {
		t.Fatalf("exec %s echo: %v (stderr: %s)", sh.name, err, errStderr(err))
	}
	if !strings.Contains(string(out), "hello-sporemind") {
		t.Fatalf("stdout = %q, want contain 'hello-sporemind'", out)
	}
}

func TestSmoke_BashShCResolvesGitBashUtilities(t *testing.T) {
	sh, err := resolveShell()
	if err != nil {
		t.Skipf("no shell: %v", err)
	}
	// This test validates Git Bash's auxiliary bin directories. It only makes
	// sense for POSIX shells; PowerShell does not use the same utilities.
	if sh.name != "bash" && sh.name != "gitbash" && sh.name != "sh" {
		t.Skipf("skipping POSIX utility test for shell %q", sh.name)
	}
	// `ls` is a POSIX utility — on Windows it lives in Git\usr\bin. The
	// envWithExtras call below is what makes bash able to find it inside
	// the sandboxed subprocess.
	c := exec.Command(sh.path, sh.flag, "ls -la")
	c.Dir = t.TempDir()
	if env := envWithExtras(sh.extraBin); env != nil {
		c.Env = env
	}
	if _, err := c.Output(); err != nil {
		t.Fatalf("exec %s 'ls -la': %v (stderr: %s)", sh.name, err, errStderr(err))
	}
}

func TestSmoke_ArgsModeResolvesViaExtras(t *testing.T) {
	sh, err := resolveShell()
	if err != nil {
		t.Skipf("no shell: %v", err)
	}
	if len(sh.extraBin) == 0 && runtime.GOOS == "windows" {
		t.Skip("no extraBin discovered; can't validate Windows-specific tool lookup")
	}
	// Args-mode: cmd resolved via lookPathWithExtras, not via parent PATH.
	binPath := lookPathWithExtras("echo", sh.extraBin)
	if binPath == "" {
		t.Skip("echo not resolvable on this machine")
	}
	c := exec.Command(binPath, "smoke")
	if env := envWithExtras(sh.extraBin); env != nil {
		c.Env = env
	}
	out, err := c.Output()
	if err != nil {
		t.Fatalf("exec %q: %v", binPath, err)
	}
	if !strings.Contains(string(out), "smoke") {
		t.Fatalf("stdout = %q, want 'smoke'", out)
	}
}

func errStderr(err error) string {
	if ee, ok := err.(*exec.ExitError); ok {
		return string(ee.Stderr)
	}
	return ""
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]int, len(a))
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}
