// Package sdk provides a Go SDK for building sporemind plugins as C-shared libraries.
//
// Usage:
//
//	package main
//
//	import (
//	    "C"
//	    "unsafe"
//
//	    sdk "github.com/qomos-w/sporemind-plugin-sdk"
//	)
//
//	func init() {
//	    sdk.Register(&sdk.Plugin{...})
//	}
//
//	//export PluginManifest
//	func PluginManifest(buf *C.char, n C.int) C.int {
//	    return C.int(sdk.WriteManifest(unsafe.Pointer(buf), int32(n)))
//	}
//
//	//export PluginOnLoad
//	func PluginOnLoad(pluginID *C.char, config *C.char) C.int {
//	    return C.int(sdk.HandleOnLoad(unsafe.Pointer(pluginID), unsafe.Pointer(config)))
//	}
//
//	//export PluginOnUnload
//	func PluginOnUnload(pluginID *C.char) C.int {
//	    return C.int(sdk.HandleOnUnload(unsafe.Pointer(pluginID)))
//	}
//
//	//export PluginOnConfigChange
//	func PluginOnConfigChange(pluginID *C.char, config *C.char) C.int {
//	    return C.int(sdk.HandleOnConfigChange(unsafe.Pointer(pluginID), unsafe.Pointer(config)))
//	}
//
//	//export PluginInvoke
//	func PluginInvoke(pluginID *C.char, callID *C.char, req *C.char, res *C.char, resLen C.int) C.int {
//	    return C.int(sdk.HandleInvoke(unsafe.Pointer(pluginID), unsafe.Pointer(callID), unsafe.Pointer(req), unsafe.Pointer(res), int32(resLen)))
//	}
//
//	//export PluginSetHostBridge
//	func PluginSetHostBridge(bridge unsafe.Pointer) C.int {
//	    return C.int(sdk.HandleSetHostBridge(bridge))
//	}
//
//	//export PluginLog
//	func PluginLog(buf *C.char, n C.int) C.int {
//	    return C.int(sdk.HandlePluginLog(unsafe.Pointer(buf), int32(n)))
//	}
//
//	func main() {}
package sdk
