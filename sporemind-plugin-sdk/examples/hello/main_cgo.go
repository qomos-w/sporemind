//go:build cgo

package main

import (
	"C"
	"unsafe"

	sdk "github.com/qomos-w/sporemind-plugin-sdk"
)

// This file carries the c-shared FFI exports. It is only compiled when cgo
// is enabled (c-shared build); under CGO_ENABLED=0 it is skipped and the
// subprocess entry comes from the SDK's process_main.go instead.

//export PluginManifest
func PluginManifest(buf *C.char, n C.int) C.int {
	return C.int(sdk.WriteManifest(unsafe.Pointer(buf), int32(n)))
}

//export PluginOnLoad
func PluginOnLoad(pluginID *C.char, config *C.char) C.int {
	return C.int(sdk.HandleOnLoad(unsafe.Pointer(pluginID), unsafe.Pointer(config)))
}

//export PluginOnUnload
func PluginOnUnload(pluginID *C.char) C.int {
	return C.int(sdk.HandleOnUnload(unsafe.Pointer(pluginID)))
}

//export PluginOnConfigChange
func PluginOnConfigChange(pluginID *C.char, config *C.char) C.int {
	return C.int(sdk.HandleOnConfigChange(unsafe.Pointer(pluginID), unsafe.Pointer(config)))
}

// PluginInvoke is the official length-prefixed native ABI entry point.
// Signature: int PluginInvoke(const uint8_t* req, size_t reqLen,
//
//	uint8_t* resp, size_t respCap, size_t* respLen)
//
//export PluginInvoke
func PluginInvoke(req *C.char, reqLen C.size_t, resp *C.char, respCap C.size_t, respLen *C.size_t) C.int {
	return C.int(sdk.HandleInvokeFramed(unsafe.Pointer(req), uintptr(reqLen), unsafe.Pointer(resp), uintptr(respCap), (*uintptr)(unsafe.Pointer(respLen))))
}

//export PluginSetHostBridge
func PluginSetHostBridge(bridge unsafe.Pointer) C.int {
	return C.int(sdk.HandleSetHostBridge(bridge))
}

//export PluginLog
func PluginLog(buf *C.char, n C.int) C.int {
	return C.int(sdk.HandlePluginLog(unsafe.Pointer(buf), int32(n)))
}

func main() {}
