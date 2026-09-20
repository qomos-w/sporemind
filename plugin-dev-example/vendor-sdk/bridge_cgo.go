//go:build cgo

package sdk

import (
	"C"
	"encoding/json"
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// hostBridgeFunc matches the C signature used by the host for reverse calls.
// The host exposes a callback: int host_invoke(char* callID, char* req, char* res, int resLen)
type hostBridgeFunc func(*C.char, *C.char, *C.char, C.int) C.int

var hostBridge = struct {
	sync.RWMutex
	fn hostBridgeFunc
}{}

// SetHostBridge receives the function pointer passed by the host.
// This is called when the host invokes PluginSetHostBridge.
func SetHostBridge(bridge unsafe.Pointer) {
	hostBridge.Lock()
	defer hostBridge.Unlock()
	if bridge == nil {
		hostBridge.fn = nil
		return
	}
	purego.RegisterFunc(&hostBridge.fn, uintptr(bridge))
}

// setHostBridge is the build-tag-neutral bridge-install entry used by
// HandleSetHostBridge. The cgo build installs the FFI callback; the !cgo
// (subprocess) build has no FFI bridge and returns -1 instead.
func setHostBridge(bridge unsafe.Pointer) int32 {
	SetHostBridge(bridge)
	return 0
}

// Invoke calls the host's FFI bridge callback (c-shared transport). This
// file is only compiled when cgo is enabled; the !cgo (subprocess) build
// provides an erroring fallback in bridge_nocgo.go.
func (h *defaultHost) Invoke(callID string, payload any) ([]byte, error) {
	hostBridge.RLock()
	available := hostBridge.fn != nil
	hostBridge.RUnlock()
	if !available {
		return nil, fmt.Errorf("host bridge not set")
	}
	reqBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return bridgeCall(callID, reqBytes)
}

func bridgeCall(callID string, req []byte) ([]byte, error) {
	cid := append([]byte(callID), 0)
	reqBuf := append(req, 0)
	res := make([]byte, 65536)
	hostBridge.RLock()
	fn := hostBridge.fn
	hostBridge.RUnlock()
	if fn == nil {
		return nil, fmt.Errorf("host bridge not set")
	}

	n := fn(
		(*C.char)(unsafe.Pointer(&cid[0])),
		(*C.char)(unsafe.Pointer(&reqBuf[0])),
		(*C.char)(unsafe.Pointer(&res[0])),
		C.int(len(res)),
	)
	if n < 0 || int(n) >= len(res) {
		return nil, fmt.Errorf("host invoke %s returned %d", callID, n)
	}
	end := int(n)
	for i := 0; i < int(n); i++ {
		if res[i] == 0 {
			end = i
			break
		}
	}
	return bridgeResult(callID, res[:end])
}
