package project

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// --- helpers ---

// tarEntry describes one entry in a synthetic test archive.
type tarEntry struct {
	name     string
	content  []byte
	isDir    bool
	typeflag byte // explicit override (e.g. tar.TypeSymlink); 0 = derive from isDir
	link     string
}

// buildArchive encodes a set of entries into a gzip-compressed tar byte slice.
func buildArchive(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0644}
		switch {
		case e.typeflag != 0:
			hdr.Typeflag = e.typeflag
			if e.typeflag == tar.TypeSymlink || e.typeflag == tar.TypeLink {
				hdr.Linkname = e.link
			}
			if err := tw.WriteHeader(hdr); err != nil {
				t.Fatalf("write header %s: %v", e.name, err)
			}
		case e.isDir:
			hdr.Typeflag = tar.TypeDir
			if err := tw.WriteHeader(hdr); err != nil {
				t.Fatalf("write dir header %s: %v", e.name, err)
			}
		default:
			hdr.Typeflag = tar.TypeReg
			hdr.Size = int64(len(e.content))
			if err := tw.WriteHeader(hdr); err != nil {
				t.Fatalf("write file header %s: %v", e.name, err)
			}
			if _, err := tw.Write(e.content); err != nil {
				t.Fatalf("write content %s: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func archiveB64(t *testing.T, entries []tarEntry) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString(buildArchive(t, entries))
}

func newArchiveActor(t *testing.T, root string) (*Actor, actor.PureContext) {
	t.Helper()
	a := &Actor{persistStore: persist.NewFSPersist(t.TempDir())}
	setRoots(t, a, []domain.RootDirEntry{{Name: "default", Path: root}})
	ctx := testutil.AdminCtx(testutil.GenActorID())
	return a, ctx
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// countTransferTemps returns how many ".sporecode-transfer-*.tar.gz" files
// currently live in the OS temp dir.
func countTransferTemps(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".sporecode-transfer-") && strings.HasSuffix(e.Name(), ".tar.gz") {
			n++
		}
	}
	return n
}

// --- export/import round trip ---

func TestArchiveExportImport_RoundTrip(t *testing.T) {
	src := t.TempDir()
	srcDir := filepath.Join(src, "mysrc")
	writeFile(t, filepath.Join(srcDir, "a.txt"), "alpha")
	writeFile(t, filepath.Join(srcDir, "sub", "b.txt"), "beta")
	// binary content
	writeFile(t, filepath.Join(srcDir, "bin", "data.bin"), string([]byte{0, 1, 2, 255, 128, 64}))
	// empty directory
	if err := os.MkdirAll(filepath.Join(srcDir, "empty"), 0755); err != nil {
		t.Fatal(err)
	}

	a, ctx := newArchiveActor(t, src)
	resp, err := a.handleArchiveExport(ctx, domain.ArchiveExportReq{Path: srcDir})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if resp.NumEntries < 5 {
		t.Errorf("expected at least 5 entries (root + 2 dirs + 2 files + empty dir), got %d", resp.NumEntries)
	}
	if resp.Size <= 0 {
		t.Errorf("expected non-zero size, got %d", resp.Size)
	}
	// Decode to confirm it's valid base64 gzip.
	if _, err := base64.StdEncoding.DecodeString(resp.Content); err != nil {
		t.Fatalf("content is not valid base64: %v", err)
	}

	// Import into a fresh target.
	target := t.TempDir()
	a2, ctx2 := newArchiveActor(t, target)
	imp, err := a2.handleArchiveImport(ctx2, domain.ArchiveImportReq{Path: target, Content: resp.Content})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if imp.NumEntries == 0 {
		t.Error("expected non-zero imported entries")
	}

	// Verify round-tripped content.
	mustRead := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := mustRead(filepath.Join(target, "mysrc", "a.txt")); got != "alpha" {
		t.Errorf("a.txt = %q, want alpha", got)
	}
	if got := mustRead(filepath.Join(target, "mysrc", "sub", "b.txt")); got != "beta" {
		t.Errorf("b.txt = %q, want beta", got)
	}
	binData, err := os.ReadFile(filepath.Join(target, "mysrc", "bin", "data.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(binData, []byte{0, 1, 2, 255, 128, 64}) {
		t.Errorf("binary data mismatch: %v", binData)
	}
	// Empty directory preserved.
	if fi, err := os.Stat(filepath.Join(target, "mysrc", "empty")); err != nil || !fi.IsDir() {
		t.Errorf("empty dir not preserved")
	}
}

// tarEntryNames decodes a base64 tar.gz archive and returns the ordered list of
// entry names. Used to assert the exact archive structure produced by export.
func tarEntryNames(t *testing.T, b64 string) []string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new gzip reader: %v", err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		names = append(names, hdr.Name)
	}
	return names
}

func TestArchiveExport_SingleFile(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "note.txt"), "hello world")
	a, ctx := newArchiveActor(t, src)
	resp, err := a.handleArchiveExport(ctx, domain.ArchiveExportReq{Path: "note.txt"})
	if err != nil {
		t.Fatalf("export regular file: %v", err)
	}
	if resp.NumEntries != 1 {
		t.Errorf("NumEntries = %d, want 1", resp.NumEntries)
	}
	// The archive must contain exactly one entry named path.Base(sourcePath),
	// with no directory wrapper.
	names := tarEntryNames(t, resp.Content)
	if len(names) != 1 || names[0] != "note.txt" {
		t.Errorf("archive entries = %v, want exactly [note.txt]", names)
	}

	// Round-trip through import into a fresh target.
	target := t.TempDir()
	a2, ctx2 := newArchiveActor(t, target)
	if _, err := a2.handleArchiveImport(ctx2, domain.ArchiveImportReq{Path: target, Content: resp.Content}); err != nil {
		t.Fatalf("import: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(target, "note.txt"))
	if err != nil {
		t.Fatalf("read imported file: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("imported content = %q, want %q", got, "hello world")
	}
}

func TestArchiveExport_SingleFile_BinaryWithNUL(t *testing.T) {
	src := t.TempDir()
	content := []byte{0, 'a', 0, 0, 'b', 255, 0, 1, 2, 3, 0}
	writeFile(t, filepath.Join(src, "blob.dat"), string(content))
	a, ctx := newArchiveActor(t, src)
	resp, err := a.handleArchiveExport(ctx, domain.ArchiveExportReq{Path: "blob.dat"})
	if err != nil {
		t.Fatalf("export binary file: %v", err)
	}
	if resp.NumEntries != 1 {
		t.Errorf("NumEntries = %d, want 1", resp.NumEntries)
	}
	target := t.TempDir()
	a2, ctx2 := newArchiveActor(t, target)
	if _, err := a2.handleArchiveImport(ctx2, domain.ArchiveImportReq{Path: target, Content: resp.Content}); err != nil {
		t.Fatalf("import: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(target, "blob.dat"))
	if err != nil {
		t.Fatalf("read imported file: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("binary content mismatch (NUL bytes must round-trip): got %v, want %v", got, content)
	}
}

func TestArchiveExport_SingleFile_Empty(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "empty.txt"), "")
	a, ctx := newArchiveActor(t, src)
	resp, err := a.handleArchiveExport(ctx, domain.ArchiveExportReq{Path: "empty.txt"})
	if err != nil {
		t.Fatalf("export empty file: %v", err)
	}
	if resp.NumEntries != 1 {
		t.Errorf("NumEntries = %d, want 1", resp.NumEntries)
	}
	target := t.TempDir()
	a2, ctx2 := newArchiveActor(t, target)
	if _, err := a2.handleArchiveImport(ctx2, domain.ArchiveImportReq{Path: target, Content: resp.Content}); err != nil {
		t.Fatalf("import: %v", err)
	}
	fi, err := os.Stat(filepath.Join(target, "empty.txt"))
	if err != nil {
		t.Fatalf("stat imported empty file: %v", err)
	}
	if fi.Size() != 0 {
		t.Errorf("imported empty file size = %d, want 0", fi.Size())
	}
}

func TestArchiveExport_SingleFile_TempCleanedUp(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "f.txt"), "x")
	before := countTransferTemps(t)
	a, ctx := newArchiveActor(t, src)
	if _, err := a.handleArchiveExport(ctx, domain.ArchiveExportReq{Path: "f.txt"}); err != nil {
		t.Fatalf("export: %v", err)
	}
	if after := countTransferTemps(t); after != before {
		t.Errorf("temp file leak: before=%d after=%d", before, after)
	}
}

func TestArchiveExport_MissingPath(t *testing.T) {
	root := t.TempDir()
	a, ctx := newArchiveActor(t, root)
	_, err := a.handleArchiveExport(ctx, domain.ArchiveExportReq{Path: "nope"})
	if err == nil {
		t.Fatal("expected error exporting a missing path")
	}
}

func TestArchiveExport_SkipsSymlinks(t *testing.T) {
	src := t.TempDir()
	srcDir := filepath.Join(src, "src")
	writeFile(t, filepath.Join(srcDir, "real.txt"), "real")
	target := filepath.Join(srcDir, "link.txt")
	if err := os.Symlink(filepath.Join(srcDir, "real.txt"), target); err != nil {
		// Symlink creation requires privileges on some Windows configs.
		t.Skipf("cannot create symlink: %v", err)
	}
	a, ctx := newArchiveActor(t, src)
	resp, err := a.handleArchiveExport(ctx, domain.ArchiveExportReq{Path: srcDir})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	// Import and confirm the symlink was NOT carried.
	dst := t.TempDir()
	a2, ctx2 := newArchiveActor(t, dst)
	if _, err := a2.handleArchiveImport(ctx2, domain.ArchiveImportReq{Path: dst, Content: resp.Content}); err != nil {
		t.Fatalf("import: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "src", "link.txt")); !os.IsNotExist(err) {
		t.Errorf("symlink should not have been carried into the archive")
	}
	if _, err := os.Stat(filepath.Join(dst, "src", "real.txt")); err != nil {
		t.Errorf("regular file should have been carried: %v", err)
	}
}

func TestArchiveExport_TempFileCleanedUp(t *testing.T) {
	src := t.TempDir()
	srcDir := filepath.Join(src, "src")
	writeFile(t, filepath.Join(srcDir, "a.txt"), "x")

	before := countTransferTemps(t)
	a, ctx := newArchiveActor(t, src)
	if _, err := a.handleArchiveExport(ctx, domain.ArchiveExportReq{Path: srcDir}); err != nil {
		t.Fatalf("export: %v", err)
	}
	after := countTransferTemps(t)
	if after != before {
		t.Errorf("temp file leak: before=%d after=%d (defer cleanup did not remove the transfer archive)", before, after)
	}
}

func TestArchiveImport_TempFileCleanedUp(t *testing.T) {
	target := t.TempDir()
	content := archiveB64(t, []tarEntry{{name: "src/", isDir: true}, {name: "src/a.txt", content: []byte("x")}})
	before := countTransferTemps(t)
	a, ctx := newArchiveActor(t, target)
	if _, err := a.handleArchiveImport(ctx, domain.ArchiveImportReq{Path: target, Content: content}); err != nil {
		t.Fatalf("import: %v", err)
	}
	after := countTransferTemps(t)
	if after != before {
		t.Errorf("temp file leak: before=%d after=%d", before, after)
	}
}

// --- import security rejections ---

func TestArchiveImport_RejectsTraversal(t *testing.T) {
	target := t.TempDir()
	// Entry escapes the target via "..".
	content := archiveB64(t, []tarEntry{{name: "../evil.txt", content: []byte("pwned")}})
	a, ctx := newArchiveActor(t, target)
	_, err := a.handleArchiveImport(ctx, domain.ArchiveImportReq{Path: target, Content: content})
	if err == nil {
		t.Fatal("expected traversal entry to be rejected")
	}
	if _, err := os.Stat(filepath.Join(target, "..", "evil.txt")); err == nil {
		t.Fatal("traversal file was written outside the target")
	}
}

func TestArchiveImport_RejectsNestedTraversal(t *testing.T) {
	target := t.TempDir()
	content := archiveB64(t, []tarEntry{
		{name: "src/", isDir: true},
		{name: "src/../../escape.txt", content: []byte("x")},
	})
	a, ctx := newArchiveActor(t, target)
	_, err := a.handleArchiveImport(ctx, domain.ArchiveImportReq{Path: target, Content: content})
	if err == nil {
		t.Fatal("expected nested traversal entry to be rejected")
	}
}

func TestArchiveImport_RejectsAbsolutePath(t *testing.T) {
	target := t.TempDir()
	content := archiveB64(t, []tarEntry{{name: "/etc/evil.txt", content: []byte("x")}})
	a, ctx := newArchiveActor(t, target)
	_, err := a.handleArchiveImport(ctx, domain.ArchiveImportReq{Path: target, Content: content})
	if err == nil {
		t.Fatal("expected absolute path entry to be rejected")
	}
}

func TestArchiveImport_RejectsSymlink(t *testing.T) {
	target := t.TempDir()
	writeFile(t, filepath.Join(target, "secret.txt"), "secret")
	content := archiveB64(t, []tarEntry{
		{name: "src/", isDir: true},
		{name: "src/link.txt", typeflag: tar.TypeSymlink, link: "../secret.txt"},
	})
	a, ctx := newArchiveActor(t, target)
	_, err := a.handleArchiveImport(ctx, domain.ArchiveImportReq{Path: target, Content: content})
	if err == nil {
		t.Fatal("expected symlink entry to be rejected")
	}
	if _, err := os.Lstat(filepath.Join(target, "src", "link.txt")); !os.IsNotExist(err) {
		t.Error("symlink must not be written to disk")
	}
}

func TestArchiveImport_RejectsHardLink(t *testing.T) {
	target := t.TempDir()
	content := archiveB64(t, []tarEntry{
		{name: "src/", isDir: true},
		{name: "src/a.txt", content: []byte("x")},
		{name: "src/b.txt", typeflag: tar.TypeLink, link: "src/a.txt"},
	})
	a, ctx := newArchiveActor(t, target)
	_, err := a.handleArchiveImport(ctx, domain.ArchiveImportReq{Path: target, Content: content})
	if err == nil {
		t.Fatal("expected hard link entry to be rejected")
	}
}

func TestArchiveImport_RejectsDuplicate(t *testing.T) {
	target := t.TempDir()
	content := archiveB64(t, []tarEntry{
		{name: "src/", isDir: true},
		{name: "src/a.txt", content: []byte("one")},
		{name: "src/a.txt", content: []byte("two")},
	})
	a, ctx := newArchiveActor(t, target)
	_, err := a.handleArchiveImport(ctx, domain.ArchiveImportReq{Path: target, Content: content})
	if err == nil {
		t.Fatal("expected duplicate entry to be rejected")
	}
}

func TestArchiveImport_RejectsConflictWithExistingFile(t *testing.T) {
	target := t.TempDir()
	// Pre-existing regular file at the destination of an archive entry.
	writeFile(t, filepath.Join(target, "src", "a.txt"), "original")
	content := archiveB64(t, []tarEntry{
		{name: "src/", isDir: true},
		{name: "src/a.txt", content: []byte("overwritten")},
	})
	a, ctx := newArchiveActor(t, target)
	_, err := a.handleArchiveImport(ctx, domain.ArchiveImportReq{Path: target, Content: content})
	if err == nil {
		t.Fatal("expected conflict with existing regular file to be rejected")
	}
	// The original must be untouched.
	got, _ := os.ReadFile(filepath.Join(target, "src", "a.txt"))
	if string(got) != "original" {
		t.Errorf("existing file was overwritten: got %q, want original", string(got))
	}
}

func TestArchiveImport_AllowsDirectoryMerge(t *testing.T) {
	target := t.TempDir()
	// Pre-existing directory should allow new files to be added inside it.
	if err := os.MkdirAll(filepath.Join(target, "src"), 0755); err != nil {
		t.Fatal(err)
	}
	content := archiveB64(t, []tarEntry{
		{name: "src/", isDir: true},
		{name: "src/new.txt", content: []byte("ok")},
	})
	a, ctx := newArchiveActor(t, target)
	if _, err := a.handleArchiveImport(ctx, domain.ArchiveImportReq{Path: target, Content: content}); err != nil {
		t.Fatalf("directory merge should succeed: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(target, "src", "new.txt"))
	if string(got) != "ok" {
		t.Errorf("new file content = %q, want ok", string(got))
	}
}

func TestArchiveImport_NotADirectory(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "file.txt"), "x")
	content := archiveB64(t, []tarEntry{{name: "src/a.txt", content: []byte("x")}})
	a, ctx := newArchiveActor(t, root)
	_, err := a.handleArchiveImport(ctx, domain.ArchiveImportReq{Path: "file.txt", Content: content})
	if err == nil {
		t.Fatal("expected error importing into a non-directory")
	}
}

func TestArchiveImport_InvalidBase64(t *testing.T) {
	target := t.TempDir()
	a, ctx := newArchiveActor(t, target)
	_, err := a.handleArchiveImport(ctx, domain.ArchiveImportReq{Path: target, Content: "!!!not-base64!!!"})
	if err == nil {
		t.Fatal("expected invalid base64 to be rejected")
	}
}

func TestArchiveImport_RejectsBackslashInName(t *testing.T) {
	target := t.TempDir()
	content := archiveB64(t, []tarEntry{{name: "evil\\x.txt", content: []byte("x")}})
	a, ctx := newArchiveActor(t, target)
	_, err := a.handleArchiveImport(ctx, domain.ArchiveImportReq{Path: target, Content: content})
	if err == nil {
		t.Fatal("expected backslash in entry name to be rejected")
	}
}

// --- sanitizeArchiveEntry unit tests ---

func TestSanitizeArchiveEntry(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"empty", "", "", false},
		{"dot", ".", "", false},
		{"regular", "src/a.txt", "src/a.txt", false},
		{"cleans dotdot inside", "a/../b.txt", "b.txt", false},
		{"traversal leading", "../evil.txt", "", true},
		{"traversal nested", "a/../../b.txt", "", true},
		{"absolute posix", "/etc/passwd", "", true},
		{"windows drive", "C:evil", "", true},
		{"nul", "a\x00b", "", true},
		{"backslash", "a\\b", "", true},
		{"parent only", "..", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sanitizeArchiveEntry(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// --- entry limit ---

func TestArchiveImport_RejectsTooManyEntries(t *testing.T) {
	target := t.TempDir()
	const n = maxArchiveEntries + 5
	entries := make([]tarEntry, 0, n)
	entries = append(entries, tarEntry{name: "d/", isDir: true})
	for i := 0; i < n; i++ {
		entries = append(entries, tarEntry{name: "d/f" + itoa(i) + ".txt", content: []byte("x")})
	}
	content := archiveB64(t, entries)
	a, ctx := newArchiveActor(t, target)
	_, err := a.handleArchiveImport(ctx, domain.ArchiveImportReq{Path: target, Content: content})
	if err == nil {
		t.Fatal("expected entry-count limit to be enforced")
	}
}

func itoa(i int) string {
	// small helper to avoid pulling strconv for a single use in a table loop
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
