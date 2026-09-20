package sshmanager

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/archive"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// ---------------------------------------------------------------------------
// mockRemoteFS — an in-memory remoteFS for testing archive handlers.
// ---------------------------------------------------------------------------

type mockRemoteFS struct {
	dirs     map[string]bool
	files    map[string][]byte
	symlinks map[string]bool
	listFn   func(dir string) ([]domain.SshFileEntry, error)
}

func newMockRemoteFS() *mockRemoteFS {
	m := &mockRemoteFS{
		dirs:     make(map[string]bool),
		files:    make(map[string][]byte),
		symlinks: make(map[string]bool),
	}
	m.listFn = m.defaultList
	return m
}

// seedTree populates the mock FS with a directory tree under root.
func (m *mockRemoteFS) seedTree(root string) {
	m.dirs[root] = true
	m.addFile(path.Join(root, "readme.txt"), []byte("hello world\n"))
	m.dirs[path.Join(root, "sub")] = true
	m.addFile(path.Join(root, "sub", "a.txt"), []byte("aaa\n"))
	m.dirs[path.Join(root, "sub", "deep")] = true
	m.addFile(path.Join(root, "sub", "deep", "b.bin"), []byte{0x00, 0x01, 0xFF, 0xFE})
	m.dirs[path.Join(root, "empty")] = true
}

func (m *mockRemoteFS) addFile(p string, content []byte) {
	m.files[p] = content
}

func (m *mockRemoteFS) defaultList(dir string) ([]domain.SshFileEntry, error) {
	var entries []domain.SshFileEntry
	for d := range m.dirs {
		if path.Dir(d) == dir {
			entries = append(entries, domain.SshFileEntry{
				Name:     path.Base(d),
				FullPath: d,
				IsDir:    true,
			})
		}
	}
	for f := range m.files {
		if path.Dir(f) == dir {
			entries = append(entries, domain.SshFileEntry{
				Name:     path.Base(f),
				FullPath: f,
				IsDir:    false,
				Size:     int64(len(m.files[f])),
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

func (m *mockRemoteFS) List(dir string) ([]domain.SshFileEntry, error) {
	return m.listFn(dir)
}

func (m *mockRemoteFS) Read(p string) ([]byte, error) {
	data, ok := m.files[p]
	if !ok {
		return nil, fmt.Errorf("not found: %s", p)
	}
	return data, nil
}

func (m *mockRemoteFS) Write(p string, content []byte) error {
	m.files[p] = content
	return nil
}

func (m *mockRemoteFS) Mkdir(p string) error {
	m.dirs[p] = true
	return nil
}

func (m *mockRemoteFS) Remove(p string) error {
	delete(m.files, p)
	return nil
}

func (m *mockRemoteFS) RemoveDir(p string) error {
	delete(m.dirs, p)
	return nil
}

func (m *mockRemoteFS) Rename(from, to string) error {
	return fmt.Errorf("not implemented")
}

func (m *mockRemoteFS) Chmod(p string, mode os.FileMode) error {
	return fmt.Errorf("not implemented")
}

func (m *mockRemoteFS) Stat(p string) (string, int64, bool, string, time.Time, error) {
	if m.symlinks[p] {
		return path.Base(p), 0, false, "Lrwxrwxrwx", time.Now(), nil
	}
	if m.dirs[p] {
		return path.Base(p), 0, true, "drwxr-xr-x", time.Now(), nil
	}
	if data, ok := m.files[p]; ok {
		return path.Base(p), int64(len(data)), false, "-rw-r--r--", time.Now(), nil
	}
	return "", 0, false, "", time.Time{}, fmt.Errorf("not found: %s", p)
}

func (m *mockRemoteFS) Lstat(p string) (string, int64, bool, string, time.Time, error) {
	return m.Stat(p)
}

func (m *mockRemoteFS) Close() error { return nil }

// ---------------------------------------------------------------------------
// archiveRemoteTree tests
// ---------------------------------------------------------------------------

func TestArchiveRemoteTreeProducesValidArchive(t *testing.T) {
	fs := newMockRemoteFS()
	fs.seedTree("/site")

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	n, err := archiveRemoteTree(fs, "/site", tw)
	if err != nil {
		t.Fatalf("archiveRemoteTree: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}

	if n == 0 {
		t.Fatal("expected at least one entry")
	}

	// Verify the archive contains the expected entries.
	gr, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gr)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
	}

	expected := map[string]bool{
		"site/":                 true,
		"site/readme.txt":      true,
		"site/sub/":            true,
		"site/sub/a.txt":       true,
		"site/sub/deep/":       true,
		"site/sub/deep/b.bin":  true,
		"site/empty/":          true,
	}
	for _, name := range names {
		if !expected[name] {
			t.Fatalf("unexpected archive entry: %q", name)
		}
	}
	for want := range expected {
		found := false
		for _, got := range names {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing archive entry: %q (have %v)", want, names)
		}
	}
}

func TestArchiveRemoteTreeEmptyDir(t *testing.T) {
	fs := newMockRemoteFS()
	fs.dirs["/empty"] = true

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	n, err := archiveRemoteTree(fs, "/empty", tw)
	if err != nil {
		t.Fatalf("archiveRemoteTree on empty dir: %v", err)
	}
	tw.Close()
	gw.Close()
	// Root directory entry is always written (1 entry for the empty/ dir itself).
	if n != 1 {
		t.Fatalf("empty dir should have 1 entry (root), got %d", n)
	}

	// Verify the single entry is the root directory.
	gr, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gr)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Name != "empty/" {
		t.Fatalf("expected root entry 'empty/', got %q", hdr.Name)
	}
}

// ---------------------------------------------------------------------------
// extractToRemote tests
// ---------------------------------------------------------------------------

func TestExtractToRemoteRoundTrip(t *testing.T) {
	srcFS := newMockRemoteFS()
	srcFS.seedTree("/src")

	// Export to archive bytes.
	var exportBuf bytes.Buffer
	gw := gzip.NewWriter(&exportBuf)
	tw := tar.NewWriter(gw)
	if _, err := archiveRemoteTree(srcFS, "/src", tw); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gw.Close()

	// Import into a fresh mock FS.
	dstFS := newMockRemoteFS()
	n, bw, err := extractToRemote(dstFS, "/dst", bytes.NewReader(exportBuf.Bytes()))
	if err != nil {
		t.Fatalf("extractToRemote: %v", err)
	}
	if n == 0 {
		t.Fatal("expected at least one extracted entry")
	}
	if bw <= 0 {
		t.Fatalf("expected positive bytesWritten, got %d", bw)
	}

	// Verify file contents survived the round trip. The archive now includes
	// the source root's base name (src/), so entries land under /dst/src/.
	want := map[string][]byte{
		"/dst/src/readme.txt":         []byte("hello world\n"),
		"/dst/src/sub/a.txt":          []byte("aaa\n"),
		"/dst/src/sub/deep/b.bin":     {0x00, 0x01, 0xFF, 0xFE},
	}
	for p, content := range want {
		got, ok := dstFS.files[p]
		if !ok {
			t.Fatalf("missing extracted file %q", p)
		}
		if string(got) != string(content) {
			t.Fatalf("content mismatch %q: got %q want %q", p, got, content)
		}
	}

	// Verify directories were created, including the source root.
	for _, dir := range []string{"/dst/src", "/dst/src/sub", "/dst/src/sub/deep", "/dst/src/empty"} {
		if !dstFS.dirs[dir] {
			t.Fatalf("directory %q was not created", dir)
		}
	}
}

func TestExtractToRemoteRejectsTraversal(t *testing.T) {
	// Craft a malicious archive with a traversal entry.
	data := buildRawArchive(t, []rawEntry{
		{name: "../escape.txt", content: []byte("evil"), isDir: false},
	})
	fs := newMockRemoteFS()
	_, _, err := extractToRemote(fs, "/dst", bytes.NewReader(data))
	if err == nil {
		t.Fatal("traversal entry must be rejected")
	}
}

// ---------------------------------------------------------------------------
// Handler-level tests (temp archive lifecycle, base64, size limits)
// ---------------------------------------------------------------------------

// runExportPipeline runs the export logic without actor context, mirroring
// handleArchiveExport's core sequence (including the file/dir branch). This
// isolates the testable logic from the actor/PureContext plumbing.
func runExportPipeline(fs remoteFS, sourcePath string) (base64Content string, size int64, numEntries int, err error) {
	sourcePath = path.Clean(sourcePath)

	_, _, isDir, modeStr, _, statErr := fs.Stat(sourcePath)
	if statErr != nil {
		return "", 0, 0, fmt.Errorf("stat source: %w", statErr)
	}
	if !isDir && !isRegularFileMode(modeStr) {
		return "", 0, 0, fmt.Errorf("unsupported file type for %q (mode %q)", sourcePath, modeStr)
	}

	f, cerr := newTransferTempFile()
	if cerr != nil {
		return "", 0, 0, fmt.Errorf("create temp archive: %w", cerr)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	var n int
	var walkErr error
	if isDir {
		n, walkErr = archiveRemoteTree(fs, sourcePath, tw)
	} else {
		n, walkErr = archiveRemoteFile(fs, sourcePath, tw)
	}
	if cerr := tw.Close(); cerr != nil && walkErr == nil {
		walkErr = cerr
	}
	if cerr := gw.Close(); cerr != nil && walkErr == nil {
		walkErr = cerr
	}
	if walkErr != nil {
		return "", 0, 0, walkErr
	}

	if _, serr := f.Seek(0, io.SeekStart); serr != nil {
		return "", 0, 0, fmt.Errorf("seek temp: %w", serr)
	}
	data, rerr := io.ReadAll(f)
	if rerr != nil {
		return "", 0, 0, fmt.Errorf("read temp: %w", rerr)
	}
	if int64(len(data)) > archive.MaxCompressedSize {
		return "", 0, 0, fmt.Errorf("compressed size %d exceeds limit", len(data))
	}
	return base64.StdEncoding.EncodeToString(data), int64(len(data)), n, nil
}

func runImportPipeline(fs remoteFS, targetDir, b64 string) (numEntries int, bytesWritten int64, err error) {
	targetDir = path.Clean(targetDir)

	// Stat target — must exist and be a directory.
	_, _, isDir, _, _, statErr := fs.Stat(targetDir)
	if statErr != nil {
		return 0, 0, fmt.Errorf("stat target: %w", statErr)
	}
	if !isDir {
		return 0, 0, fmt.Errorf("target %q is not a directory", targetDir)
	}

	data, derr := base64.StdEncoding.DecodeString(b64)
	if derr != nil {
		return 0, 0, fmt.Errorf("decode base64: %w", derr)
	}
	if int64(len(data)) > archive.MaxCompressedSize {
		return 0, 0, fmt.Errorf("compressed size exceeds limit")
	}

	f, cerr := newTransferTempFile()
	if cerr != nil {
		return 0, 0, fmt.Errorf("create temp archive: %w", cerr)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	if _, werr := f.Write(data); werr != nil {
		return 0, 0, fmt.Errorf("write temp: %w", werr)
	}
	if _, serr := f.Seek(0, io.SeekStart); serr != nil {
		return 0, 0, fmt.Errorf("seek temp: %w", serr)
	}
	return extractToRemote(fs, targetDir, f)
}

func TestExportImportPipelineRoundTrip(t *testing.T) {
	srcFS := newMockRemoteFS()
	srcFS.seedTree("/project")

	b64, size, n, err := runExportPipeline(srcFS, "/project")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if b64 == "" {
		t.Fatal("empty base64 content")
	}
	if size == 0 {
		t.Fatal("zero archive size")
	}
	if n == 0 {
		t.Fatal("zero entries")
	}

	// Verify base64 decodes to valid gzip.
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if int64(len(raw)) != size {
		t.Fatalf("size mismatch: decoded %d vs reported %d", len(raw), size)
	}

	// Import into a fresh mock FS. The target dir /dest must exist.
	dstFS := newMockRemoteFS()
	dstFS.dirs["/dest"] = true
	extractedN, bw, err := runImportPipeline(dstFS, "/dest", b64)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if extractedN != n {
		t.Fatalf("entry count mismatch: exported %d, imported %d", n, extractedN)
	}
	if bw <= 0 {
		t.Fatal("zero bytes written")
	}

	// Spot-check content — the source root "project" is preserved in the
	// archive, so files land under /dest/project/.
	got, ok := dstFS.files["/dest/project/readme.txt"]
	if !ok || string(got) != "hello world\n" {
		t.Fatalf("readme.txt content mismatch: %q", got)
	}
}

func TestExportPipelineRejectsUnsupportedKind(t *testing.T) {
	// Simulate a symlink entry in the mock FS. The mode string "Lrwxrwxrwx"
	// is the Go FileMode.String() for a symlink.
	fs := newMockRemoteFS()
	fs.symlinks["/root/link"] = true
	_, _, _, err := runExportPipeline(fs, "/root/link")
	if err == nil {
		t.Fatal("exporting a symlink should fail")
	}
	if !strings.Contains(err.Error(), "unsupported file type") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExportImportSingleBinaryFileRoundTrip(t *testing.T) {
	// Binary payload with NUL bytes, 0xFF, and mixed content.
	payload := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0x00, 'A', 'B', 'C', 0x00, 0xFD}

	srcFS := newMockRemoteFS()
	srcFS.addFile("/site/blob.bin", payload)

	b64, size, n, err := runExportPipeline(srcFS, "/site/blob.bin")
	if err != nil {
		t.Fatalf("export single file: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 entry, got %d", n)
	}
	if size == 0 {
		t.Fatal("zero archive size")
	}

	// Verify the archive contains exactly one top-level entry named "blob.bin".
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	gr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gr)
	hdr, err := tr.Next()
	if err != nil {
		t.Fatalf("read archive entry: %v", err)
	}
	if hdr.Name != "blob.bin" {
		t.Fatalf("archive entry name: got %q want %q", hdr.Name, "blob.bin")
	}
	// Read the file content from the archive.
	archivedContent, err := io.ReadAll(tr)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(archivedContent, payload) {
		t.Fatalf("content mismatch: got %v want %v", archivedContent, payload)
	}
	// Confirm there is exactly one entry.
	if _, err := tr.Next(); err != io.EOF {
		t.Fatalf("expected EOF after single entry, got %v", err)
	}

	// Import into a fresh mock FS — target dir must exist.
	dstFS := newMockRemoteFS()
	dstFS.dirs["/dest"] = true
	extractedN, bw, err := runImportPipeline(dstFS, "/dest", b64)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if extractedN != 1 {
		t.Fatalf("expected 1 imported entry, got %d", extractedN)
	}

	// Verify the binary content survived the full round trip.
	got, ok := dstFS.files["/dest/blob.bin"]
	if !ok {
		t.Fatal("imported file /dest/blob.bin not found")
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("binary content mismatch after round trip: got %v want %v", got, payload)
	}
	if bw != int64(len(payload)) {
		t.Fatalf("bytesWritten: got %d want %d", bw, len(payload))
	}
}

func TestImportPipelineRejectsBadBase64(t *testing.T) {
	fs := newMockRemoteFS()
	fs.dirs["/dest"] = true // target must exist to pass the Stat check
	_, _, err := runImportPipeline(fs, "/dest", "!!!not-base64!!!")
	if err == nil {
		t.Fatal("invalid base64 must be rejected")
	}
}

func TestImportPipelineRejectsTraversalArchive(t *testing.T) {
	rawData := buildRawArchive(t, []rawEntry{
		{name: "../../etc/passwd", content: []byte("evil")},
	})
	b64 := base64.StdEncoding.EncodeToString(rawData)
	fs := newMockRemoteFS()
	fs.dirs["/dest"] = true // target must exist to pass the Stat check
	_, _, err := runImportPipeline(fs, "/dest", b64)
	if err == nil {
		t.Fatal("traversal archive must be rejected on import")
	}
}

func TestImportPipelineRejectsMissingTarget(t *testing.T) {
	fs := newMockRemoteFS()
	// /dest does not exist in the mock FS.
	_, _, err := runImportPipeline(fs, "/dest", "")
	if err == nil {
		t.Fatal("missing target dir must be rejected")
	}
}

func TestImportPipelineRejectsFileTarget(t *testing.T) {
	fs := newMockRemoteFS()
	fs.addFile("/dest", []byte("not a dir"))
	_, _, err := runImportPipeline(fs, "/dest", "")
	if err == nil {
		t.Fatal("non-directory target must be rejected")
	}
}

// ---------------------------------------------------------------------------
// tempArchivePattern tests
// ---------------------------------------------------------------------------

func TestTempArchivePatternContainsUUID(t *testing.T) {
	name := fmt.Sprintf(tempArchivePattern, "550e8400-e29b-41d4-a716-446655440000")
	if !strings.HasPrefix(name, ".sporecode-transfer-") {
		t.Fatalf("unexpected prefix: %q", name)
	}
	if !strings.HasSuffix(name, ".tar.gz") {
		t.Fatalf("unexpected suffix: %q", name)
	}
}

func TestCreateTempArchiveProducesUniquePaths(t *testing.T) {
	paths := make(map[string]bool)
	for i := 0; i < 100; i++ {
		f, err := newTransferTempFile()
		if err != nil {
			t.Fatal(err)
		}
		p := f.Name()
		f.Close()
		os.Remove(p)
		if paths[p] {
			t.Fatalf("duplicate temp archive path: %s", p)
		}
		paths[p] = true
		if !strings.Contains(p, ".sporecode-transfer-") {
			t.Fatalf("path does not follow pattern: %s", p)
		}
	}
}

func TestNewTransferTempFileIsExclusive(t *testing.T) {
	// O_EXCL guarantees the file does not pre-exist; creating two distinct
	// files yields two different paths and both files exist on disk.
	f1, err := newTransferTempFile()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f1.Name())
	defer f1.Close()

	f2, err := newTransferTempFile()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f2.Name())
	defer f2.Close()

	if f1.Name() == f2.Name() {
		t.Fatal("two calls produced the same path")
	}

	// Verify both files actually exist.
	if _, err := os.Stat(f1.Name()); err != nil {
		t.Fatalf("f1 does not exist: %v", err)
	}
	if _, err := os.Stat(f2.Name()); err != nil {
		t.Fatalf("f2 does not exist: %v", err)
	}
}

// ---------------------------------------------------------------------------
// remoteRelPath tests
// ---------------------------------------------------------------------------

func TestRemoteRelPath(t *testing.T) {
	tests := []struct {
		root, full, want string
	}{
		{"/site", "/site/index.html", "index.html"},
		{"/site", "/site/sub/a.txt", "sub/a.txt"},
		{"/site", "/site", "."},
		{"/a/b", "/a/b/c/d.txt", "c/d.txt"},
	}
	for _, tt := range tests {
		got := remoteRelPath(tt.root, tt.full)
		if got != tt.want {
			t.Fatalf("remoteRelPath(%q, %q) = %q want %q", tt.root, tt.full, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

type rawEntry struct {
	name    string
	content []byte
	isDir   bool
}

func buildRawArchive(t *testing.T, entries []rawEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, e := range entries {
		flag := byte(tar.TypeReg)
		name := e.name
		if e.isDir {
			flag = tar.TypeDir
			if !strings.HasSuffix(name, "/") {
				name += "/"
			}
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0644,
			Size:     int64(len(e.content)),
			Typeflag: flag,
			Format:   tar.FormatGNU,
		}); err != nil {
			t.Fatal(err)
		}
		if !e.isDir && len(e.content) > 0 {
			if _, err := tw.Write(e.content); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
