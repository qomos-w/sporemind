package pluginhost

import (
	"strings"
)

// orphanPluginDecision reports whether a process table entry is a plugin
// subprocess orphaned by a dead host. Plugin dev builds are content-addressed
// executables named plugin-<name>-<version>-<hash>.exe spawned as children of
// a sporemind host; on Windows children do not die with the host, so every
// host crash/kill leaks all running plugin processes (each holding a loopback
// listener and locking its exe against artifact GC). A process is an orphan
// when its exe name matches the plugin artifact pattern AND its parent PID no
// longer exists in the live process set — children of any live host (this
// one, or a concurrently running instance such as a test gateway) are left
// alone.
func orphanPluginDecision(exeBase string, parentPID uint32, alive map[uint32]bool, selfPID uint32) bool {
	lower := strings.ToLower(strings.TrimSpace(exeBase))
	if !strings.HasPrefix(lower, "plugin-") || !strings.HasSuffix(lower, ".exe") {
		return false
	}
	// "plugin-" + name + "-" + [version + "-"] + hash: at least two dashes
	// beyond the prefix (version keeps its dots, never a dash count issue).
	if strings.Count(lower, "-") < 2 {
		return false
	}
	if parentPID == 0 || parentPID == selfPID {
		return false
	}
	return !alive[parentPID]
}
