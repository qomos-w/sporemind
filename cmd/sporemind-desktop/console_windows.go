//go:build windows

package main

import (
	"os"
	"syscall"

	"github.com/qomos-w/sporemind/pkg/buildinfo"
)

var (
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	procFreeConsole = kernel32.NewProc("FreeConsole")
)

// freeConsole releases any console that the Go runtime may have allocated
// during process startup. On Windows, a GUI-subsystem binary (-H windowsgui)
// can still briefly flash a console window if the Go runtime or a library
// writes to stderr/stdout before the GUI window appears. Calling FreeConsole
// at the earliest possible point suppresses this flash.
func freeConsole() {
	procFreeConsole.Call()
}

// init runs before main() so FreeConsole is called as early as possible in the
// package init sequence. This catches any console the Go runtime attaches
// during its own startup, before main() even begins.
//
// Dev builds keep the inherited console: they are launched from a terminal
// (wails3 dev / make run-desktop) and their logs are mirrored to stderr, so
// detaching would hide every startup log. Beta/release builds always detach
// unless SPOREMIND_KEEP_CONSOLE is set (debug escape hatch).
func init() {
	if buildinfo.IsDev() || os.Getenv("SPOREMIND_KEEP_CONSOLE") != "" {
		return
	}
	freeConsole()
}
