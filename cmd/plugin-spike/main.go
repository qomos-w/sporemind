package main

// Stub main so `go build ./...` can compile this spike package.
// The plugin-spike executable is exercised through `go test ./cmd/plugin-spike`
// (the test binary re-executes itself as the plugin child process).
func main() {}
