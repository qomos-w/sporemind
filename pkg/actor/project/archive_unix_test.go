//go:build !windows

package project

import (
	"path/filepath"
	"syscall"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// TestArchiveExport_RejectsSpecialFile verifies that a non-regular, non-dir
// source (a FIFO here) is rejected rather than archived. Devices, sockets and
// named pipes cannot be carried safely across filesystems. This case is not
// portable to Windows, so the test is confined to POSIX hosts.
func TestArchiveExport_RejectsSpecialFile(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "myfifo")
	if err := syscall.Mkfifo(fifo, 0644); err != nil {
		t.Skipf("cannot create fifo: %v", err)
	}
	a, ctx := newArchiveActor(t, root)
	_, err := a.handleArchiveExport(ctx, domain.ArchiveExportReq{Path: "myfifo"})
	if err == nil {
		t.Fatal("expected error exporting a special file (fifo), got nil")
	}
}
