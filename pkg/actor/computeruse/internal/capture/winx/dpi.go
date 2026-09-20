//go:build windows

package winx

import "unsafe"

// DPI awareness in sporemind is set at the process level via the wails manifest
// (see cmd/sporemind-desktop/build/windows/wails.exe.manifest, which declares
// permonitorv2,permonitor). GDI / SendInput / cursor APIs therefore operate
// on physical pixels — no thread-level dance required.
//
// What this file owns is the *reading* side: GetDpiForMonitor (shcore, Win8.1+)
// for accurate per-monitor scale factors in Displays(). On older Windows we
// gracefully fall back to 96.

// dpiForMonitor returns the effective DPI for an HMONITOR. Falls back to
// 96 when shcore!GetDpiForMonitor is unavailable (pre-Win8.1).
func dpiForMonitor(hMon HMONITOR) int {
	if pGetDpiForMonitor.Find() != nil {
		return 96
	}
	var xDPI, yDPI uint32
	ret, _, _ := pGetDpiForMonitor.Call(
		uintptr(hMon),
		mdtEffectiveDPI,
		uintptr(unsafe.Pointer(&xDPI)),
		uintptr(unsafe.Pointer(&yDPI)),
	)
	if ret != 0 || xDPI == 0 {
		return 96
	}
	return int(xDPI)
}
