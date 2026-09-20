//go:build !cgo

package main

import sdk "github.com/qomos-w/sporemind-plugin-sdk"

// main is the subprocess-mode entry of the plugin executable (dev
// transport). It is only compiled when cgo is disabled; the c-shared build
// (CGO_ENABLED=1) compiles main_cgo.go instead. The whole transport lives
// in the SDK (sdk.RunProcess): stdin/stdout framing, IPC reverse host and
// direct 0x05 log frames.
func main() {
	sdk.RunProcess()
}
