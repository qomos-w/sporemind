//go:build !windows

package desktop

import (
	"fmt"
	"image"
	"unsafe"
)

func captureWindows() ([]WindowInfo, error) {
	return nil, nil
}

func activateWindow(_ uintptr) error {
	return fmt.Errorf("window activation not supported on this platform")
}

func captureScreen() (image.Image, error) {
	return nil, fmt.Errorf("screenshot not supported on this platform")
}

func captureScreenRegion(_, _, _, _ int) (image.Image, error) {
	return nil, fmt.Errorf("screenshot not supported on this platform")
}

func setChildWindow(_, _ unsafe.Pointer) {}

// encodeImageToPNGBase64 is a no-op stub for non-Windows platforms.
func encodeImageToPNGBase64(_ image.Image) (string, error) {
	return "", fmt.Errorf("screenshot not supported on this platform")
}

func captureWindowRegion(_ unsafe.Pointer) (string, error) {
	return "", fmt.Errorf("screenshot not supported on this platform")
}
