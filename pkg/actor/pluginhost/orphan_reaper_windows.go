//go:build windows

package pluginhost

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// reapOrphanedPluginProcesses terminates plugin-*.exe processes whose parent
// is gone (previous host generations) and returns their PIDs. Failures to open
// or terminate a single process are skipped — reaping is best-effort and runs
// again at the next host start.
func reapOrphanedPluginProcesses() []uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)

	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	var entries []windows.ProcessEntry32
	if err := windows.Process32First(snap, &entry); err == nil {
		for {
			entries = append(entries, entry)
			entry = windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
			if err := windows.Process32Next(snap, &entry); err != nil {
				break
			}
		}
	}

	alive := make(map[uint32]bool, len(entries))
	for _, pe := range entries {
		alive[pe.ProcessID] = true
	}
	self := uint32(os.Getpid())
	var killed []uint32
	for _, pe := range entries {
		name := windows.UTF16ToString(pe.ExeFile[:])
		if !orphanPluginDecision(name, pe.ParentProcessID, alive, self) {
			continue
		}
		handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pe.ProcessID)
		if err != nil {
			continue
		}
		if err := windows.TerminateProcess(handle, 1); err == nil {
			killed = append(killed, pe.ProcessID)
		}
		_ = windows.CloseHandle(handle)
	}
	return killed
}
