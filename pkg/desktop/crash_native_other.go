//go:build !windows

package desktop

// InstallNativeCrashHandler is a no-op off Windows; there is no SEH filter
// to install and crash capture relies on the session marker and error log.
func InstallNativeCrashHandler() {}
