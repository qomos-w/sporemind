package pluginloader

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/qomos-w/sporemind/pkg/testutil"
)

// Go c-shared libraries cannot be loaded and unloaded more than once in a
// single process: the Go runtime initialises on the first dlopen/LoadLibrary
// and dlclose/FreeLibrary corrupts it (golang/go#11181 and friends). The test
// suite therefore builds the SDK example exactly once and keeps the library
// handle open for the lifetime of the process; tests share it via the helpers
// below.

var (
	sharedLibOnce sync.Once
	sharedLib     *Library
	sharedLibErr  error
	sharedLibPath string
)

// sharedExampleLibrary builds (once) and opens the SDK example plugin,
// returning a *Library that must NOT be closed by the caller. The build
// artifact path is also returned for hash/file checks.
func sharedExampleLibrary(t *testing.T) (*Library, string) {
	t.Helper()
	sharedLibOnce.Do(func() {
		sharedLibPath = buildSDKExamplePlugin(t)
		lib, err := Open(sharedLibPath)
		if err != nil {
			sharedLibErr = err
			return
		}
		sharedLib = lib
	})
	if sharedLibErr != nil {
		t.Fatalf("open shared example library: %v", sharedLibErr)
	}
	if sharedLib == nil {
		t.Fatal("shared example library is nil")
	}
	return sharedLib, sharedLibPath
}

// absPluginDir resolves the SDK example directory. Centralised so the skip
// condition is consistent across tests.
func absPluginDir(t *testing.T) string {
	t.Helper()
	sdkDir, ok := testutil.FindSDKDir(t)
	if !ok {
		t.Skip("sporemind-plugin-sdk checkout not found")
	}
	return filepath.Join(sdkDir, "examples", "hello")
}
