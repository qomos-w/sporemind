// main_run.gen.go — Code generated from appdef. DO NOT EDIT.
// Subprocess entry (dev transport): built only with cgo disabled; the release
// c-shared build stages its own cgo shim elsewhere. Starts the plugin's
// direct-HTTP listener (same-origin data path for the panel frontend) in
// parallel to the stdin/stdout frame protocol (sdk.RunProcess).

//go:build !cgo

package main

import sdk "github.com/qomos-w/sporemind-plugin-sdk"

func main() {
	// Direct-HTTP data path: static files, POST /invoke/{id} and GET /events
	// SSE, served same-origin with no CORS. server.gen.go fills the HTTP
	// handler map in init; the bound address goes to the log stream and the
	// OnLoad response for the host to discover (T4).
	if srv, err := sdk.ServeHTTP("127.0.0.1:0"); err != nil {
		sdk.Log(sdk.LogLevelError, "serve http: %v", err)
	} else {
		sdk.Log(sdk.LogLevelInfo, "http listener on %s", srv.Addr())
	}
	sdk.RunProcess()
}
