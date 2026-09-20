//go:build windows

package pluginloader

import (
	"fmt"
	"syscall"
)

func openSharedLibrary(path string) (uintptr, error) {
	handle, err := syscall.LoadLibrary(path)
	if err != nil {
		return 0, err
	}
	return uintptr(handle), nil
}

func closeSharedLibrary(handle uintptr) error {
	return syscall.FreeLibrary(syscall.Handle(handle))
}

func lookupSymbol(handle uintptr, name string) (uintptr, error) {
	sym, err := syscall.GetProcAddress(syscall.Handle(handle), name)
	if err != nil {
		return 0, fmt.Errorf("lookup symbol %s: %w", name, err)
	}
	return sym, nil
}
