//go:build !windows

package pluginloader

import (
	"fmt"

	"github.com/ebitengine/purego"
)

func openSharedLibrary(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
}

func closeSharedLibrary(handle uintptr) error {
	return purego.Dlclose(handle)
}

func lookupSymbol(handle uintptr, name string) (uintptr, error) {
	sym, err := purego.Dlsym(handle, name)
	if err != nil {
		return 0, fmt.Errorf("lookup symbol %s: %w", name, err)
	}
	return sym, nil
}
