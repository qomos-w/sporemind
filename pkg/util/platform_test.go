package util

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDetectEnvironment_Values(t *testing.T) {
	env := DetectEnvironment()
	if env["os"] != runtime.GOOS {
		t.Errorf("os = %q, want %q", env["os"], runtime.GOOS)
	}
	if env["shell"] == "" {
		t.Error("shell is empty")
	}
	if env["shell_executable"] == "" {
		t.Error("shell_executable is empty")
	}
	if env["shell_flag"] == "" {
		t.Error("shell_flag is empty")
	}
	if env["shell_notes"] == "" {
		t.Error("shell_notes is empty")
	}
	if !strings.Contains(env["shell_notes"], "NEVER:") {
		t.Errorf("shell_notes %q does not contain NEVER:", env["shell_notes"])
	}
}

func TestDetectEnvironment_ReturnsCopy(t *testing.T) {
	a := DetectEnvironment()
	b := DetectEnvironment()
	if len(a) == 0 || len(b) == 0 {
		t.Fatal("DetectEnvironment returned empty map")
	}
	a["os"] = "mutated"
	if b["os"] == "mutated" {
		t.Error("DetectEnvironment returned a mutable shared map")
	}
}

func TestDetectEnvironment_ShellExecutableMatchesShell(t *testing.T) {
	env := DetectEnvironment()
	exec := strings.ToLower(env["shell_executable"])
	switch env["shell"] {
	case "gitbash", "bash", "sh":
		if !strings.Contains(exec, "bash") && !strings.Contains(exec, "sh") {
			t.Errorf("bash-family executable %q does not contain bash or sh", env["shell_executable"])
		}
	case "powershell", "pwsh":
		if !strings.Contains(exec, "powershell") && !strings.Contains(exec, "pwsh") {
			t.Errorf("powershell executable %q does not look like powershell", env["shell_executable"])
		}
	default:
		t.Errorf("unexpected shell %q with executable %q", env["shell"], env["shell_executable"])
	}
}

func TestDetectShellInfo_HasExecutableWhenAvailable(t *testing.T) {
	info := DetectShellInfo()
	if runtime.GOOS == "windows" {
		// Windows may not have a POSIX shell installed; accept bash, sh, or
		// PowerShell as a fallback, but never cmd.
		if info.Executable != "" {
			lower := strings.ToLower(info.Executable)
			if !strings.Contains(lower, "bash") && !strings.Contains(lower, "sh") && !strings.Contains(lower, "powershell") && !strings.Contains(lower, "pwsh") {
				t.Errorf("unexpected windows shell executable %q", info.Executable)
			}
		}
		return
	}
	if info.Executable == "" {
		t.Error("expected non-empty shell executable on POSIX")
	}
}

func TestWinShellCandidates_NonEmptyOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only probe paths")
	}
	got := winShellCandidates()
	if len(got) == 0 {
		t.Fatal("winShellCandidates returned no paths")
	}
	seen := make(map[string]struct{}, len(got))
	for _, p := range got {
		if _, dup := seen[p]; dup {
			t.Fatalf("duplicate candidate %q", p)
		}
		seen[p] = struct{}{}
	}
}

func TestDetectShellInfo_SPOREMIND_BASH(t *testing.T) {
	tmp := t.TempDir()
	fakeBash := filepath.Join(tmp, "bash")
	if runtime.GOOS == "windows" {
		fakeBash = filepath.Join(tmp, "bash.exe")
	}
	if err := os.WriteFile(fakeBash, []byte{}, 0755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SPOREMIND_BASH", fakeBash)
	info := DetectShellInfo()
	if info.Executable != fakeBash {
		t.Errorf("Executable = %q, want %q", info.Executable, fakeBash)
	}
	if info.Name != "bash" {
		t.Errorf("Name = %q, want %q", info.Name, "bash")
	}
}

func TestDetectShells_ReturnsAllFamilies(t *testing.T) {
	shells := DetectShells()
	if len(shells) == 0 {
		t.Fatal("DetectShells returned no candidates")
	}
	if runtime.GOOS == "windows" {
		// On Windows we expect bash, gitbash, powershell7, powershell5, cmd.
		kinds := make(map[ShellKind]bool, len(shells))
		for _, s := range shells {
			kinds[s.Kind] = true
		}
		for _, k := range []ShellKind{ShellBash, ShellGitBash, ShellPowerShell5, ShellPowerShell7, ShellCmd} {
			if !kinds[k] {
				t.Errorf("missing expected shell kind %q in candidates %v", k, shells)
			}
		}
	}
	// Every candidate must have Kind set (Executable may be empty when unavailable).
	for _, s := range shells {
		if s.Kind == "" {
			t.Errorf("candidate has empty Kind: %+v", s)
		}
	}
}

func TestDetectShells_PowerShellVersionsDistinct(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only powershell version split")
	}
	shells := DetectShells()
	var ps5, ps7 *DetectedShell
	for i := range shells {
		switch shells[i].Kind {
		case ShellPowerShell5:
			ps5 = &shells[i]
		case ShellPowerShell7:
			ps7 = &shells[i]
		}
	}
	if ps5 == nil || ps7 == nil {
		t.Fatalf("expected both powershell5 and powershell7 candidates, got %+v", shells)
	}
	// powershell5 should resolve to powershell.exe; powershell7 to pwsh.exe.
	if ps5.Available {
		base := strings.ToLower(filepath.Base(ps5.Executable))
		if !strings.Contains(base, "powershell") || strings.Contains(base, "pwsh") {
			t.Errorf("powershell5 executable %q does not look like powershell.exe", ps5.Executable)
		}
	}
	if ps7.Available {
		base := strings.ToLower(filepath.Base(ps7.Executable))
		if !strings.Contains(base, "pwsh") {
			t.Errorf("powershell7 executable %q does not look like pwsh.exe", ps7.Executable)
		}
	}
}

func TestSetShellPreference_Injected(t *testing.T) {
	// Save and restore the preference so this test does not leak state.
	prev := ShellPreference()
	defer SetShellPreference(prev)

	shells := DetectShells()
	var available ShellKind
	for _, s := range shells {
		if s.Available && s.Kind != "" {
			available = s.Kind
			break
		}
	}
	if available == "" {
		t.Skip("no available shell to test preference injection")
	}

	SetShellPreference(available)
	info := DetectShellInfo()
	kind := ShellKindFromInfo(info)
	if kind != available {
		t.Errorf("after SetShellPreference(%q), DetectShellInfo kind = %q, want %q", available, kind, available)
	}
}

func TestSetShellPreference_AutoRestoresDefault(t *testing.T) {
	prev := ShellPreference()
	defer SetShellPreference(prev)

	SetShellPreference("")
	info := DetectShellInfo()
	// With empty preference, the auto-detection path runs; the result must
	// be a valid shell (or empty if none found, but never the wrong kind).
	if info.IsAvailable() {
		k := ShellKindFromInfo(info)
		if k == "" {
			t.Errorf("auto-detect produced available shell with empty kind: %+v", info)
		}
	}
}

// TestAutoDetection_WindowsPrefersGitBash locks the default shell priority:
// on Windows, when Git Bash is available, auto detection (empty preference,
// no SPOREMIND_BASH override) must resolve to git-bash even though
// PowerShell and cmd are also installed.
func TestAutoDetection_WindowsPrefersGitBash(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only default priority")
	}
	prev := ShellPreference()
	defer SetShellPreference(prev)
	t.Setenv("SPOREMIND_BASH", "")

	gitBashAvailable := false
	for _, c := range DetectShells() {
		if c.Kind == ShellGitBash && c.Available {
			gitBashAvailable = true
			break
		}
	}
	if !gitBashAvailable {
		t.Skip("no git bash installed on this machine")
	}

	SetShellPreference("")
	info := DetectShellInfo()
	if got := ShellKindFromInfo(info); got != ShellGitBash {
		t.Errorf("auto detection kind = %q (exe %q), want gitbash", got, info.Executable)
	}
	if info.ExecFlag() != "-c" {
		t.Errorf("auto detection flag = %q, want -c", info.ExecFlag())
	}
	env := DetectEnvironment()
	if env["shell"] != "gitbash" {
		t.Errorf("DetectEnvironment shell = %q, want gitbash", env["shell"])
	}
}

func TestShellKindFromInfo_PowerShellSplit(t *testing.T) {
	cases := []struct {
		name string
		exec string
		want ShellKind
	}{
		{"powershell", "C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe", ShellPowerShell5},
		{"pwsh", "C:\\Program Files\\PowerShell\\7\\pwsh.exe", ShellPowerShell7},
		{"bash", "/usr/bin/bash", ShellBash},
		{"gitbash", "C:\\Program Files\\Git\\bin\\bash.exe", ShellGitBash},
		{"sh", "/bin/sh", ShellSh},
		{"cmd", "C:\\Windows\\System32\\cmd.exe", ShellCmd},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := ShellInfo{Name: tc.name, Executable: tc.exec}
			got := ShellKindFromInfo(info)
			if got != tc.want {
				t.Errorf("ShellKindFromInfo({Name:%q, Exec:%q}) = %q, want %q", tc.name, tc.exec, got, tc.want)
			}
		})
	}
}

func TestExecFlag_Cmd(t *testing.T) {
	if got := (ShellInfo{Name: "cmd", Executable: `C:\Windows\System32\cmd.exe`}).ExecFlag(); got != "/c" {
		t.Errorf("cmd ExecFlag = %q, want /c", got)
	}
	if got := (ShellInfo{Name: "powershell", Executable: `C:\pwsh.exe`}).ExecFlag(); got != "-Command" {
		t.Errorf("powershell ExecFlag = %q, want -Command", got)
	}
	if got := (ShellInfo{Name: "gitbash", Executable: `C:\Git\bin\bash.exe`}).ExecFlag(); got != "-c" {
		t.Errorf("gitbash ExecFlag = %q, want -c", got)
	}
}

func TestShellNotesForKind_ConciseAndProhibitionFocused(t *testing.T) {
	kinds := []ShellKind{ShellGitBash, ShellBash, ShellSh, ShellPowerShell5, ShellPowerShell7, ShellCmd}
	for _, k := range kinds {
		note := shellNotesForKind(k, shellNameForKind(k), "windows")
		if note == "" {
			t.Errorf("shellNotesForKind(%q) is empty", k)
		}
		if !strings.HasPrefix(note, "NEVER:") {
			t.Errorf("shellNotesForKind(%q) = %q, want notes that lead with NEVER: prohibitions", k, note)
		}
	}
}

func TestShellNotesForKind_SyntaxFamiliesDistinct(t *testing.T) {
	// Notes must differ where the shell syntax actually differs: bash-family
	// vs PowerShell 5.1 vs PowerShell 7+ vs cmd. (bash/gitbash/sh share one
	// note — their prohibitions are identical.)
	families := []ShellKind{ShellBash, ShellPowerShell5, ShellPowerShell7, ShellCmd}
	seen := make(map[string]ShellKind, len(families))
	for _, k := range families {
		note := shellNotesForKind(k, shellNameForKind(k), "windows")
		if prev, dup := seen[note]; dup {
			t.Errorf("shellNotesForKind(%q) duplicates notes of %q; different syntax families must get distinct guidance", k, prev)
		}
		seen[note] = k
	}
}

func TestShellNotesForKind_CmdUsesBatchSyntax(t *testing.T) {
	note := shellNotesForKind(ShellCmd, "cmd", "windows")
	if !strings.Contains(note, "POSIX") {
		t.Errorf("cmd notes %q should prohibit POSIX syntax", note)
	}
	if !strings.Contains(note, "%VAR%") || !strings.Contains(note, "findstr") {
		t.Errorf("cmd notes %q should anchor the batch alternatives (%%VAR%%, findstr)", note)
	}
}

func TestDetectEnvironment_ReflectsPreference(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-only preference reflection")
	}
	prev := ShellPreference()
	defer SetShellPreference(prev)

	SetShellPreference(ShellCmd)
	env := DetectEnvironment()
	if env["shell"] != "cmd" {
		t.Errorf("after SetShellPreference(cmd), shell = %q, want cmd", env["shell"])
	}
	if env["shell_flag"] != "/c" {
		t.Errorf("after SetShellPreference(cmd), shell_flag = %q, want /c", env["shell_flag"])
	}
	if !strings.Contains(env["shell_notes"], "POSIX") {
		t.Errorf("after SetShellPreference(cmd), shell_notes %q should be cmd-specific", env["shell_notes"])
	}

	SetShellPreference("")
	env2 := DetectEnvironment()
	if env2["shell"] == "cmd" {
		t.Error("after clearing preference, shell should no longer be cmd")
	}
}
