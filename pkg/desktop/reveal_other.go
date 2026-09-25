//go:build !windows

package desktop

import (
	"fmt"
	"os/exec"
	"runtime"
)

func init() {
	revealInFileManager = func(path string) error {
		return fmt.Errorf("desktop: reveal not supported on this platform")
	}
	openDirectoryInFileManager = func(path string) error {
		// macOS uses "open"; Linux/BSD desktops use xdg-open.
		name := "xdg-open"
		if runtime.GOOS == "darwin" {
			name = "open"
		}
		return exec.Command(name, path).Start()
	}
}
