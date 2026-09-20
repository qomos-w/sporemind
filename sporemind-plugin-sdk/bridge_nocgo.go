//go:build !cgo

package sdk

import (
	"fmt"
	"unsafe"
)

// setHostBridge is the build-tag-neutral bridge-install entry used by
// HandleSetHostBridge. There is no FFI bridge in a !cgo (subprocess) build —
// reverse calls travel through the IPC host injected via SetHost — so
// installing one is rejected.
func setHostBridge(bridge unsafe.Pointer) int32 {
	return -1
}

// Invoke reports that the FFI host bridge is unavailable. A subprocess-mode
// plugin must have an IPC host injected via SetHost (process_main does this
// at startup); reaching this method means the host was not injected.
func (h *defaultHost) Invoke(callID string, payload any) ([]byte, error) {
	return nil, fmt.Errorf("host bridge not set: subprocess build has no FFI host bridge; SetHost was not called")
}
