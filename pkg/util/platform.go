package util

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ShellKind identifies a shell family for user-facing selection. It is the
// discriminator used by the environment settings panel and by SetShellPreference.
type ShellKind string

const (
	ShellBash        ShellKind = "bash"
	ShellGitBash     ShellKind = "gitbash"
	ShellPowerShell5 ShellKind = "powershell5" // powershell.exe — Windows PowerShell 5.1
	ShellPowerShell7 ShellKind = "powershell7" // pwsh.exe — PowerShell 7+
	ShellCmd         ShellKind = "cmd"         // cmd.exe — legacy Windows command prompt
	ShellSh          ShellKind = "sh"
)

// shellKindFromInfo maps a discovered ShellInfo.Name to a ShellKind. PowerShell
// variants are distinguished by executable name (pwsh vs powershell).
func shellKindFromInfo(info ShellInfo) ShellKind {
	return ShellKindFromInfo(info)
}

// ShellKindFromInfo maps a discovered ShellInfo to a ShellKind. PowerShell
// variants are distinguished by executable name (pwsh vs powershell). Exported
// so callers (e.g. workspace handlers) can derive a kind from a ShellInfo.
func ShellKindFromInfo(info ShellInfo) ShellKind {
	switch info.Name {
	case "gitbash":
		return ShellGitBash
	case "bash":
		return ShellBash
	case "sh":
		return ShellSh
	case "cmd":
		return ShellCmd
	case "powershell", "pwsh":
		lower := strings.ToLower(filepath.Base(info.Executable))
		if strings.Contains(lower, "pwsh") {
			return ShellPowerShell7
		}
		return ShellPowerShell5
	}
	return ""
}

// shellNameForKind maps a ShellKind to the legacy ShellInfo.Name value that
// ExecFlag and shellNotes switch on. PowerShell 5.1 and 7+ both map to
// "powershell" (they share the -Command flag); the distinction is preserved
// only at the candidate/Kind level.
func shellNameForKind(k ShellKind) string {
	switch k {
	case ShellGitBash:
		return "gitbash"
	case ShellBash:
		return "bash"
	case ShellSh:
		return "sh"
	case ShellCmd:
		return "cmd"
	case ShellPowerShell5, ShellPowerShell7:
		return "powershell"
	}
	return string(k)
}

// DetectedShell is a single probed shell candidate with an availability flag.
type DetectedShell struct {
	Kind       ShellKind
	Executable string
	ExtraBin   []string
	Available  bool
}

// ShellInfo describes the discovered system shell usable for command execution.
type ShellInfo struct {
	Name       string   // human-readable name, e.g. "gitbash", "bash", "zsh", "sh"
	Executable string   // resolved executable path
	ExtraBin   []string // auxiliary binary directories (e.g. Git Bash usr/bin)
}

// IsAvailable reports whether a usable shell was found.
func (s ShellInfo) IsAvailable() bool { return s.Executable != "" }

// ExecFlag returns the flag used to pass a shell command string to the
// discovered executable. Bash-family shells use "-c"; PowerShell uses
// "-Command"; cmd.exe uses "/c".
func (s ShellInfo) ExecFlag() string {
	switch s.Name {
	case "powershell", "pwsh":
		return "-Command"
	case "cmd":
		return "/c"
	default:
		return "-c"
	}
}

// DetectShellInfo returns runtime-detected shell information. The result is
// cached based on the SPOREMIND_BASH, PATH, and SHELL environment variables
// and the current shell preference; if any of these change, the next call
// re-runs detection.
func DetectShellInfo() ShellInfo {
	sig := os.Getenv("SPOREMIND_BASH") + "|" + os.Getenv("PATH") + "|" + os.Getenv("SHELL") + "|" + string(shellPref)
	shellCacheMu.Lock()
	defer shellCacheMu.Unlock()
	if shellCacheSig == sig {
		return shellCache
	}
	shellCache = detectShellInfo()
	shellCacheSig = sig
	return shellCache
}

// SetShellPreference sets the user's preferred shell kind used by
// DetectShellInfo. An empty kind means "auto" — fall back to the built-in
// detection priority. When the preferred shell is available it takes
// precedence over the default order so that the advertised environment and
// the actual executors (project.shell_exec, shell.bash) agree.
func SetShellPreference(kind ShellKind) {
	shellPrefMu.Lock()
	shellPref = kind
	shellPrefMu.Unlock()
}

// ShellPreference returns the currently configured preferred shell kind
// ("" means auto).
func ShellPreference() ShellKind {
	shellPrefMu.RLock()
	defer shellPrefMu.RUnlock()
	return shellPref
}

func detectShellInfo() ShellInfo {
	if p := strings.TrimSpace(os.Getenv("SPOREMIND_BASH")); p != "" {
		if fileExists(p) {
			return ShellInfo{Name: "bash", Executable: p, ExtraBin: DeriveExtraBin(p)}
		}
		if abs, err := filepath.Abs(p); err == nil && fileExists(abs) {
			return ShellInfo{Name: "bash", Executable: abs, ExtraBin: DeriveExtraBin(abs)}
		}
	}
	if pref := ShellPreference(); pref != "" {
		for _, c := range detectShells() {
			if c.Kind == pref && c.Available {
				return ShellInfo{Name: shellNameForKind(c.Kind), Executable: c.Executable, ExtraBin: c.ExtraBin}
			}
		}
	}
	if runtime.GOOS == "windows" {
		return detectWindowsShellInfo()
	}
	return detectPosixShellInfo()
}

// DetectEnvironment returns the runtime-detected environment that should be
// advertised to LLM agents (os, shell, shell_executable, shell_flag,
// shell_notes). Used as the default value for AgentKindConfig.EnvironmentContext;
// callers may override per agent kind via workspace.save_agent_kind_config.
//
// Computed on every call from DetectShellInfo (which is itself cached), so a
// shell preference change is reflected immediately — including the per-shell
// shell_notes guidance.
func DetectEnvironment() map[string]string {
	info := DetectShellInfo()
	shell := info.Name
	if shell == "" {
		shell = fallbackShellName()
	}
	executable := info.Executable
	if executable == "" {
		executable = fallbackShellExecutable()
	}
	flag := info.ExecFlag()
	if info.Executable == "" {
		flag = fallbackShellFlag()
	}
	return map[string]string{
		"os":               runtime.GOOS,
		"shell":            shell,
		"shell_executable": executable,
		"shell_flag":       flag,
		"shell_notes":      shellNotesForKind(shellKindFromInfo(info), shell, runtime.GOOS),
	}
}

func fallbackShellName() string {
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	return "sh"
}

func fallbackShellExecutable() string {
	if runtime.GOOS == "windows" {
		return "powershell.exe"
	}
	return "/bin/sh"
}

func fallbackShellFlag() string {
	if runtime.GOOS == "windows" {
		return "-Command"
	}
	return "-c"
}

// shellNotesForKind returns concise cautions for the detected shell, focused
// on what the agent must NOT do. The shell identity itself is already
// advertised via the shell/shell_executable environment keys, so the notes
// only carry the prohibitions that prevent syntax errors. kind may be empty
// when the detected shell is not one of the known kinds (e.g. zsh); name and
// goos drive the generic fallback.
func shellNotesForKind(kind ShellKind, name, goos string) string {
	if goos == "windows" {
		switch kind {
		case ShellGitBash:
			return "NEVER: use cmd.exe or cmd /c syntax. NEVER: pass unquoted Windows paths with backslashes (use forward slashes or double quotes)."
		case ShellBash:
			return "NEVER: use cmd.exe or cmd /c syntax. NEVER: pass unquoted Windows paths with backslashes (use forward slashes or double quotes)."
		case ShellPowerShell7:
			return "NEVER: use cmd.exe or cmd /c syntax. NEVER: assume POSIX redirection, quoting, or variable expansion rules."
		case ShellPowerShell5:
			return "NEVER: use cmd.exe or cmd /c syntax. NEVER: assume POSIX redirection, quoting, or variable expansion rules. NEVER: chain commands with && (not supported before PowerShell 7; use ';')."
		case ShellCmd:
			return "NEVER: use POSIX syntax ($VAR, single quotes, forward-slash flags, ls/cat/grep). NEVER: use backslash paths unquoted (use %VAR% and double-quoted backslash paths; dir/type/findstr instead of ls/cat/grep)."
		case ShellSh:
			return "NEVER: use cmd.exe or cmd /c syntax. NEVER: pass unquoted Windows paths with backslashes (use forward slashes or double quotes)."
		default:
			return "NEVER: use cmd.exe or cmd /c syntax. ALWAYS: verify shell syntax before running commands."
		}
	}
	return "NEVER: use cmd.exe or Windows batch syntax."
}

// shellNotes returns strongly-worded cautions keyed by the legacy shell name.
// Kept for name-based callers; new code should prefer shellNotesForKind.
func shellNotes(shell, goos string) string {
	if goos == "windows" {
		switch shell {
		case "bash", "gitbash", "sh":
			return "NEVER: use cmd.exe or cmd /c syntax. NEVER: pass unquoted Windows paths with backslashes. ALWAYS: use forward slashes or double-quote paths. ALWAYS: rely on Git Bash Unix utilities (ls, grep, find, sed, awk)."
		case "powershell", "pwsh":
			return "NEVER: use cmd.exe or cmd /c syntax. NEVER: assume POSIX redirection, quoting, or variable expansion rules. ALWAYS: use PowerShell cmdlets or powershell -Command. ALWAYS: double-quote paths and escape $ variables in command strings."
		default:
			return "NEVER: use cmd.exe or cmd /c syntax. ALWAYS: verify shell syntax before running commands."
		}
	}
	return "NEVER: use cmd.exe or Windows batch syntax. ALWAYS: use POSIX shell syntax and forward-slash paths."
}

func detectWindowsShellInfo() ShellInfo {
	// Well-known Git for Windows install layouts. Prefer bash.exe (the actual
	// shell) and git-bash.exe (the Git launcher) over anything else.
	for _, p := range winShellCandidates() {
		if !fileExists(p) {
			continue
		}
		name := shellNameFromPath(p)
		return ShellInfo{Name: name, Executable: p, ExtraBin: DeriveExtraBin(p)}
	}
	if path, err := exec.LookPath("bash"); err == nil {
		name := "bash"
		if strings.Contains(strings.ToLower(path), "git") {
			name = "gitbash"
		}
		return ShellInfo{Name: name, Executable: path, ExtraBin: DeriveExtraBin(path)}
	}
	if path, err := exec.LookPath("git-bash"); err == nil {
		return ShellInfo{Name: "gitbash", Executable: path, ExtraBin: DeriveExtraBin(path)}
	}
	// No POSIX shell available. Prefer PowerShell over the legacy cmd.exe.
	if path, err := exec.LookPath("pwsh"); err == nil {
		return ShellInfo{Name: "powershell", Executable: path}
	}
	if path, err := exec.LookPath("powershell"); err == nil {
		return ShellInfo{Name: "powershell", Executable: path}
	}
	return ShellInfo{}
}

// DetectShells probes all known shell families and returns one DetectedShell
// per family with an Available flag. On Windows this covers bash, git-bash,
// PowerShell 5.1 (powershell.exe) and PowerShell 7+ (pwsh.exe). On POSIX it
// reflects the current $SHELL plus any of bash/zsh/fish/sh found on PATH.
func DetectShells() []DetectedShell {
	return detectShells()
}

func detectShells() []DetectedShell {
	if runtime.GOOS == "windows" {
		return detectWindowsShells()
	}
	return detectPosixShells()
}

func detectWindowsShells() []DetectedShell {
	out := make([]DetectedShell, 0, 4)

	// bash (non-Git) and git-bash share the candidate layout scan; the first
	// match from winShellCandidates classifies as gitbash when the path
	// contains "git", otherwise plain bash.
	var bashPath, gitBashPath string
	var gitBashExtra []string
	for _, p := range winShellCandidates() {
		if !fileExists(p) {
			continue
		}
		name := shellNameFromPath(p)
		if name == "gitbash" && gitBashPath == "" {
			gitBashPath = p
			gitBashExtra = DeriveExtraBin(p)
		} else if name == "bash" && bashPath == "" {
			bashPath = p
		}
		if bashPath != "" && gitBashPath != "" {
			break
		}
	}
	if bashPath == "" {
		if path, err := exec.LookPath("bash"); err == nil {
			if !strings.Contains(strings.ToLower(path), "git") {
				bashPath = path
			}
		}
	}
	if gitBashPath == "" {
		if path, err := exec.LookPath("git-bash"); err == nil {
			gitBashPath = path
			gitBashExtra = DeriveExtraBin(path)
		} else if path, err := exec.LookPath("bash"); err == nil {
			if strings.Contains(strings.ToLower(path), "git") {
				gitBashPath = path
				gitBashExtra = DeriveExtraBin(path)
			}
		}
	}
	out = append(out, DetectedShell{Kind: ShellBash, Executable: bashPath, Available: bashPath != ""})
	if gitBashPath != "" {
		out = append(out, DetectedShell{Kind: ShellGitBash, Executable: gitBashPath, ExtraBin: gitBashExtra, Available: true})
	} else {
		out = append(out, DetectedShell{Kind: ShellGitBash, Available: false})
	}

	// PowerShell 7+ (pwsh.exe) — distinct from 5.1.
	pwshPath, pwshErr := exec.LookPath("pwsh")
	out = append(out, DetectedShell{Kind: ShellPowerShell7, Executable: pwshPath, Available: pwshErr == nil})

	// PowerShell 5.1 (powershell.exe).
	ps5Path, ps5Err := exec.LookPath("powershell")
	out = append(out, DetectedShell{Kind: ShellPowerShell5, Executable: ps5Path, Available: ps5Err == nil})

	// cmd.exe — always present on Windows; LookPath resolves System32\cmd.exe.
	cmdPath, cmdErr := exec.LookPath("cmd")
	out = append(out, DetectedShell{Kind: ShellCmd, Executable: cmdPath, Available: cmdErr == nil})

	return out
}

func detectPosixShells() []DetectedShell {
	info := detectPosixShellInfo()
	primary := DetectedShell{Kind: shellKindFromInfo(info), Executable: info.Executable, ExtraBin: info.ExtraBin, Available: info.IsAvailable()}
	out := []DetectedShell{primary}
	for _, name := range []string{"bash", "zsh", "fish", "sh"} {
		if path, err := exec.LookPath(name); err == nil {
			if path == info.Executable {
				continue
			}
			kind := ShellBash
			switch name {
			case "zsh", "fish":
				kind = ShellKind(name)
			case "sh":
				kind = ShellSh
			}
			out = append(out, DetectedShell{Kind: kind, Executable: path, ExtraBin: DeriveExtraBin(path), Available: true})
		}
	}
	return out
}


func shellNameFromPath(p string) string {
	base := strings.ToLower(filepath.Base(p))
	switch {
	case strings.Contains(base, "git-bash"):
		return "gitbash"
	case strings.Contains(base, "bash"):
		if strings.Contains(strings.ToLower(p), "git") {
			return "gitbash"
		}
		return "bash"
	case strings.Contains(base, "sh"):
		return "sh"
	default:
		return "gitbash"
	}
}

func winShellCandidates() []string {
	var roots []string
	for _, key := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)"} {
		if v := os.Getenv(key); v != "" {
			roots = append(roots, v)
		}
	}
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		roots = append(roots, filepath.Join(v, "Programs"))
	}
	for _, d := range []string{"C:", "D:", "E:", "F:"} {
		roots = append(roots, filepath.Join(d+`\`, "Program Files"))
	}

	seen := make(map[string]struct{}, 4*len(roots))
	var out []string
	for _, r := range roots {
		for _, sub := range []string{
			filepath.Join("Git", "bin", "bash.exe"),
			filepath.Join("Git", "usr", "bin", "bash.exe"),
			filepath.Join("Git", "usr", "bin", "sh.exe"),
			filepath.Join("Git", "git-bash.exe"),
			filepath.Join("Git", "bin", "git-bash.exe"),
		} {
			p := filepath.Join(r, sub)
			if _, dup := seen[p]; dup {
				continue
			}
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	return out
}

func detectPosixShellInfo() ShellInfo {
	if v := strings.TrimSpace(os.Getenv("SHELL")); v != "" {
		name := filepath.Base(v)
		if fileExists(v) {
			return ShellInfo{Name: name, Executable: v, ExtraBin: DeriveExtraBin(v)}
		}
		// $SHELL may be a name rather than a path; try to resolve it.
		if path, err := exec.LookPath(name); err == nil {
			return ShellInfo{Name: name, Executable: path, ExtraBin: DeriveExtraBin(path)}
		}
		return ShellInfo{Name: name, Executable: v}
	}
	for _, name := range []string{"bash", "zsh", "fish", "sh"} {
		if path, err := exec.LookPath(name); err == nil {
			return ShellInfo{Name: name, Executable: path, ExtraBin: DeriveExtraBin(path)}
		}
	}
	return ShellInfo{}
}

// DeriveExtraBin returns directories that should join the subprocess PATH
// so common utilities resolve when callers pass them directly via the args
// field. For Git for Windows layouts both bin/, usr/bin/, and mingw64/bin/
// are included when they exist on disk.
func DeriveExtraBin(shellPath string) []string {
	dir := filepath.Dir(shellPath)
	if filepath.Base(dir) != "bin" {
		return []string{dir}
	}

	parent := filepath.Dir(dir)
	var root string
	if filepath.Base(parent) == "usr" {
		root = filepath.Dir(parent)
	} else {
		root = parent
	}

	out := []string{dir}
	for _, sub := range []string{
		filepath.Join(root, "bin"),
		filepath.Join(root, "usr", "bin"),
		filepath.Join(root, "mingw64", "bin"),
	} {
		if sub == dir {
			continue
		}
		if fi, err := os.Stat(sub); err == nil && fi.IsDir() {
			out = append(out, sub)
		}
	}
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
