//go:build windows

package desktop

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/qomos-w/sporemind/pkg/archive"
)

func TestValidDragOutRootName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"my-folder", true},
		{"data (v2)", true},
		{"", false},
		{".", false},
		{"..", false},
		{"a/b", false},
		{`a\b`, false},
		{"C:", false},
		{"a<b", false},
		{"a?b", false},
		{"a*b", false},
	}
	for _, tc := range cases {
		if got := validDragOutRootName(tc.name); got != tc.want {
			t.Errorf("validDragOutRootName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPrepareDragOutArchiveExtractsTree(t *testing.T) {
	data, _, err := archive.BuildBytes([]archive.Entry{
		{Name: "dir/", IsDir: true, Mode: 0o755},
		{Name: "dir/inner.txt", Mode: 0o644, Content: []byte("inner")},
		{Name: "top.txt", Mode: 0o644, Content: []byte("top")},
	})
	if err != nil {
		t.Fatal(err)
	}

	dir, err := prepareDragOutArchive(base64.StdEncoding.EncodeToString(data), "My Root")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(filepath.Dir(dir)) })

	if filepath.Base(dir) != "My Root" {
		t.Fatalf("extracted folder = %q, want base %q", dir, "My Root")
	}
	for name, want := range map[string]string{
		filepath.Join(dir, "dir", "inner.txt"): "inner",
		filepath.Join(dir, "top.txt"):          "top",
	} {
		got, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestPrepareDragOutArchiveDefaultsRootName(t *testing.T) {
	data, _, err := archive.BuildBytes([]archive.Entry{
		{Name: "f.txt", Mode: 0o644, Content: []byte("x")},
	})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := prepareDragOutArchive(base64.StdEncoding.EncodeToString(data), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(filepath.Dir(dir)) })

	if filepath.Base(dir) != defaultDragOutRootName {
		t.Fatalf("default root = %q, want %q", dir, defaultDragOutRootName)
	}
}

func TestPrepareDragOutArchiveRejectsUnsafeEntries(t *testing.T) {
	for name, entry := range map[string]string{
		"parent traversal": "../evil.txt",
		"absolute path":    "/etc/passwd",
		"backslash escape": `..\\evil.txt`,
	} {
		t.Run(name, func(t *testing.T) {
			b64 := base64.StdEncoding.EncodeToString(mustTarGz(t, entry))
			dir, err := prepareDragOutArchive(b64, "root")
			if err == nil {
				os.RemoveAll(filepath.Dir(dir))
				t.Fatalf("entry %q accepted, want error", entry)
			}
			if !strings.Contains(err.Error(), "extract archive") {
				t.Fatalf("error = %v, want extract-archive failure", err)
			}
		})
	}
}

func TestPrepareDragOutArchiveRejectsBadRootAndBase64(t *testing.T) {
	data, _, err := archive.BuildBytes([]archive.Entry{{Name: "f.txt", Mode: 0o644, Content: []byte("x")}})
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.StdEncoding.EncodeToString(data)

	if _, err := prepareDragOutArchive(b64, "../escape"); err == nil {
		t.Fatal("root name with traversal accepted")
	}
	if _, err := prepareDragOutArchive("!!!not-base64!!!", "root"); err == nil {
		t.Fatal("invalid base64 accepted")
	}
}

func TestAllocHDROPMultiFileLayout(t *testing.T) {
	paths := []string{`C:\tmp\a.txt`, `C:\tmp\sub dir\b.txt`}
	hMem, err := allocHDROP(paths...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { procGlobalFree.Call(hMem) })

	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		t.Fatal("GlobalLock failed")
	}
	defer procGlobalUnlock.Call(hMem)

	total := 20 + (len(paths[0])+len(paths[1])+2+1+1)*2 // rough upper bound
	data := unsafeSlice(ptr, 256)

	if got := binary.LittleEndian.Uint32(data[0:4]); got != 20 {
		t.Errorf("pFiles = %d, want 20", got)
	}
	if got := binary.LittleEndian.Uint32(data[16:20]); got != 1 {
		t.Errorf("fWide = %d, want 1", got)
	}

	// Decode the UTF-16 double-null-terminated list after the DROPFILES header.
	u16 := make([]uint16, 0, 64)
	for i := 20; i+2 <= len(data); i += 2 {
		c := uint16(data[i]) | uint16(data[i+1])<<8
		if c == 0 {
			u16 = append(u16, 0)
			if len(u16) > 1 && u16[len(u16)-2] == 0 {
				break // double null: end of list
			}
			continue
		}
		u16 = append(u16, c)
	}
	_ = total

	var got []string
	cur := []rune{}
	for _, c := range u16 {
		if c == 0 {
			if len(cur) == 0 {
				continue
			}
			got = append(got, string(cur))
			cur = nil
			continue
		}
		cur = append(cur, rune(c))
	}
	if len(got) != 2 || got[0] != paths[0] || got[1] != paths[1] {
		t.Fatalf("HDROP list = %#v, want %#v", got, paths)
	}
}

func TestAllocHDROPMultiRejectsEmpty(t *testing.T) {
	if _, err := allocHDROP(); err == nil {
		t.Fatal("empty file list accepted")
	}
	if _, err := allocHDROP("ok", ""); err == nil {
		t.Fatal("empty path accepted")
	}
}

func TestAllocDragOutDWORD(t *testing.T) {
	hMem, err := allocDragOutDWORD(dropEffectCopy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { procGlobalFree.Call(hMem) })

	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		t.Fatal("GlobalLock failed")
	}
	defer procGlobalUnlock.Call(hMem)

	if got := binary.LittleEndian.Uint32(unsafeSlice(ptr, 4)); got != dropEffectCopy {
		t.Fatalf("DWORD = %d, want %d", got, dropEffectCopy)
	}
}

func TestDragOutDataObjectFormats(t *testing.T) {
	const fakePrefer = 49161 // arbitrary registered format id
	obj := newDragOutDataObject([]string{"a", "b"}, fakePrefer)
	fmts := obj.formats()
	if len(fmts) != 2 {
		t.Fatalf("formats = %d, want 2", len(fmts))
	}
	if fmts[0].cfFormat != cfHDROP || fmts[1].cfFormat != fakePrefer {
		t.Fatalf("format ids = [%d %d], want [%d %d]", fmts[0].cfFormat, fmts[1].cfFormat, cfHDROP, fakePrefer)
	}
	for _, fe := range fmts {
		if fe.tymed != tymedHGlobal || fe.dwAspect != dvaspectCont || fe.ptd != 0 {
			t.Errorf("format %+v has wrong aspect/tymed/ptd", fe)
		}
	}
	if !obj.formatSupported(cfHDROP) || !obj.formatSupported(fakePrefer) || obj.formatSupported(3) {
		t.Error("formatSupported disagrees with advertised formats")
	}
}

// mustTarGz builds a single regular-file tar.gz with the given entry name,
// bypassing pkg/archive's validation so rejection paths can be exercised.
func mustTarGz(t *testing.T, entryName string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	hdr := &tar.Header{Name: entryName, Mode: 0o644, Size: 4, Typeflag: tar.TypeReg, Format: tar.FormatGNU}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("evil")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// unsafeSlice views n bytes at ptr (test helper).
func unsafeSlice(ptr uintptr, n int) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(ptr)), n)
}
