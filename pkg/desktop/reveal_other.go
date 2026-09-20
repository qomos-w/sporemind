//go:build !windows

package desktop

import "fmt"

func init() {
	revealInFileManager = func(path string) error {
		return fmt.Errorf("desktop: reveal not supported on this platform")
	}
}
