//go:build !windows

package desktop

import "fmt"

// startFileDragOut is unsupported outside Windows in v1 — the unified
// frontend interface falls back to no drag when the binding returns this
// error (see web/src/ui/os-file-dnd.ts). The invokeOnMain/fetchArchive
// parameters match the Windows signature (unused here).
func startFileDragOut(_ FileDragOutRequest, _ func(func()), _ fetchDragOutArchive) error {
	return fmt.Errorf("desktop: file drag-out is not supported on this platform")
}
