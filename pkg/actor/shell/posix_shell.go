package shell

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/qomos-w/sporemind/pkg/util"
)

// systemShell carries the discovered shell binary, the flag to pass a command
// string, and auxiliary bin directories that the subprocess PATH should learn
// about. On Windows the auxiliary dirs surface Git Bash's usr\bin
// (ls/grep/find/…) so callers that name a tool directly (args mode) also
// resolve.
type systemShell struct {
	path     string
	flag     string
	name     string
	extraBin []string
}

// resolveShell discovers the system shell. It delegates to
// util.DetectShellInfo() for platform detection and SPOREMIND_BASH override.
func resolveShell() (systemShell, error) {
	info := util.DetectShellInfo()
	if info.IsAvailable() {
		return systemShell{path: info.Executable, flag: info.ExecFlag(), name: info.Name, extraBin: info.ExtraBin}, nil
	}

	if p := os.Getenv("SPOREMIND_BASH"); p != "" {
		return systemShell{}, errors.New("SPOREMIND_BASH points to a missing or invalid file: " + p)
	}

	if runtime.GOOS == "windows" {
		return systemShell{}, errors.New("no shell found; install Git for Windows (bash.exe/git-bash.exe) or ensure PowerShell is on PATH")
	}

	return systemShell{}, errors.New("no POSIX shell found on PATH")
}

// envWithExtras returns a copy of os.Environ with extras prepended to
// PATH. On Windows the PATH variable's casing varies (usually "Path");
// we rewrite the existing entry in place so a duplicate with different
// casing doesn't shadow it.
func envWithExtras(extras []string) []string {
	if len(extras) == 0 {
		return nil
	}
	env := os.Environ()
	pathKey := "PATH"
	pathIdx := -1
	for i, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		key := kv[:eq]
		if runtime.GOOS == "windows" {
			if strings.EqualFold(key, "PATH") {
				pathKey = key
				pathIdx = i
				break
			}
		} else if key == "PATH" {
			pathIdx = i
			break
		}
	}
	sep := string(os.PathListSeparator)
	prepend := strings.Join(extras, sep)
	if pathIdx >= 0 {
		existing := env[pathIdx][len(pathKey)+1:]
		env[pathIdx] = pathKey + "=" + prepend + sep + existing
	} else {
		env = append(env, pathKey+"="+prepend)
	}
	return env
}

// lookPathWithExtras resolves name to an absolute executable path,
// searching extras before the parent PATH. exec.Command itself only
// consults the parent PATH at construction time, so without this the
// Git Bash bin directories we add to c.Env have no effect on binary
// resolution for the args-mode branch.
func lookPathWithExtras(name string, extras []string) string {
	if name == "" {
		return ""
	}
	if filepath.IsAbs(name) || strings.ContainsAny(name, `/\`) {
		return name
	}
	for _, d := range extras {
		for _, ext := range pathExts() {
			candidate := filepath.Join(d, name+ext)
			if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
				return candidate
			}
		}
	}
	if resolved, err := exec.LookPath(name); err == nil {
		return resolved
	}
	return ""
}

// pathExts returns executable extensions to try on Windows.
func pathExts() []string {
	if runtime.GOOS != "windows" {
		return []string{""}
	}
	pathext := os.Getenv("PATHEXT")
	if pathext == "" {
		return []string{".exe", ".cmd", ".bat"}
	}
	var out []string
	for _, ext := range strings.Split(pathext, ";") {
		out = append(out, strings.ToLower(strings.TrimSpace(ext)))
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}
