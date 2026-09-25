package desktop

import (
	"fmt"
	"os"
)

// openDirectoryInFileManager opens the directory itself (as opposed to
// selecting it inside its parent, which revealInFileManager does). Assigned
// per-platform in reveal_windows.go / reveal_other.go.
var openDirectoryInFileManager func(path string) error

// OpenDirectory opens an existing host directory in the OS file manager.
// Bound to the frontend via Wails v3. The plugin toolbar uses it to open a
// plugin's app.data storage directory; paths come from host-side callables,
// never from untrusted renderer input.
func (a *App) OpenDirectory(path string) error {
	if path == "" {
		return fmt.Errorf("desktop: empty path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("desktop: stat %s: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("desktop: not a directory: %s", path)
	}
	if openDirectoryInFileManager == nil {
		return fmt.Errorf("desktop: open directory not supported on this platform")
	}
	return openDirectoryInFileManager(path)
}
