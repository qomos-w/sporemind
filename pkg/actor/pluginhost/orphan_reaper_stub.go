//go:build !windows

package pluginhost

// reapOrphanedPluginProcesses is a no-op off Windows: the plugin subprocess
// transport targets the desktop host, and orphan cleanup is only needed where
// child processes survive their parent's death.
func reapOrphanedPluginProcesses() []uint32 { return nil }
