//go:build !cgo

package sdk

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// RunProcess is the subprocess-mode plugin entry (dev transport, ordinary Go
// executable). It speaks the duplex framing protocol over stdin/stdout
// instead of exporting FFI symbols:
//
//   - forward invokes:   read 0x01 invoke-req -> dispatch -> write 0x02
//   - reverse calls:     injected IPC processHost (via SetHost)
//   - logs:              direct 0x05 log frames (SetProcessLogWriter, D1)
//   - host fatals:       0x06 error frames
//
// Every frame carries a correlation callID in the header (bit 7 set on the
// type byte), enabling full-duplex reverse calls from any goroutine.
//
// stderr is reserved for Go runtime panics and process diagnostics — no data
// frames are ever written there.
//
// NOTE ON THE ENTRY POINT: Go requires the executable entry to live in the
// plugin's own "main" package — a main() in a dependency is never used ("function
// main is undeclared in the main package"). The SDK therefore exposes the
// full transport here and each plugin adds one 8-line file that is compiled
// only for the subprocess build (see examples/hello/main_nocgo.go):
//
//	//go:build !cgo
//	package main
//
//	import sdk "github.com/qomos-w/sporemind-plugin-sdk"
//
//	func main() { sdk.RunProcess() }
func RunProcess() {
	t := newProcessTransport(os.Stdin, os.Stdout)
	SetHost(newProcessHost(t))
	SetProcessLogWriter(t)
	err := t.run()
	if err != nil && !errors.Is(err, io.EOF) {
		// Fatal (protocol violation, host error, broken pipe): best-effort
		// 0x06 to the host, then diagnostics on stderr. A clean stdin EOF is
		// the graceful unload signal: exit 0.
		_ = t.writeFrame(msgError, "", []byte(err.Error()))
		fmt.Fprintf(os.Stderr, "[sporemind-plugin-sdk] plugin process fatal: %v\n", err)
		os.Exit(1)
	}
}
