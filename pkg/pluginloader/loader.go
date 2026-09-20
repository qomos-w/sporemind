// Package pluginloader provides a purego-based loader for sporemind plugins.
// It loads shared libraries (.dll/.so/.dylib) and binds exported C functions
// without using cgo.
package pluginloader

import (
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/ebitengine/purego"
)

// Library is a loaded plugin shared library.
type Library struct {
	handle uintptr
	path   string
}

// Open loads the shared library at path and returns a Library handle.
func Open(path string) (*Library, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve plugin path: %w", err)
	}

	handle, err := openSharedLibrary(abs)
	if err != nil {
		return nil, fmt.Errorf("open plugin %s: %w", path, err)
	}

	return &Library{handle: handle, path: abs}, nil
}

// Close unloads the library.
func (lib *Library) Close() error {
	if lib.handle != 0 {
		return closeSharedLibrary(lib.handle)
	}
	return nil
}

// Path returns the absolute path of the loaded library.
func (lib *Library) Path() string { return lib.path }

// Symbol binds the exported symbol name to fn. fn must be a pointer to a
// function variable, e.g.:
//
//	var add func(int32, int32) int32
//	err := lib.Symbol("PluginAdd", &add)
func (lib *Library) Symbol(name string, fn any) error {
	if lib.handle == 0 {
		return fmt.Errorf("plugin library is closed")
	}
	sym, err := lookupSymbol(lib.handle, name)
	if err != nil {
		return err
	}
	purego.RegisterFunc(fn, sym)
	return nil
}

// ReadString calls an exported function with signature
//
//	int func(char* buf, int n)
//
// and returns the NUL-terminated string written into buf.
func (lib *Library) ReadString(symbol string) (string, error) {
	var fn func(*byte, int32) int32
	if err := lib.Symbol(symbol, &fn); err != nil {
		return "", err
	}

	buf := make([]byte, 8192)
	n := fn(&buf[0], int32(len(buf)))
	if n < 0 {
		return "", fmt.Errorf("symbol %s returned error %d", symbol, n)
	}

	end := int(n)
	for i := 0; i < int(n); i++ {
		if buf[i] == 0 {
			end = i
			break
		}
	}
	return string(buf[:end]), nil
}

// PlatformSuffix returns the conventional shared library extension for the
// current OS.
func PlatformSuffix() string {
	switch runtime.GOOS {
	case "windows":
		return ".dll"
	case "darwin":
		return ".dylib"
	default:
		return ".so"
	}
}
