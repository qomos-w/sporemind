//go:build windows

package desktop

import (
	"os/exec"
	"syscall"
)

func init() {
	revealInFileManager = func(path string) error {
		cmd := exec.Command("explorer.exe", "/select,", path)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		return cmd.Start()
	}
	openDirectoryInFileManager = func(path string) error {
		cmd := exec.Command("explorer.exe", path)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		return cmd.Start()
	}
}
