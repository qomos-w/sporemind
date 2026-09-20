package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"testing"
)

// ---------- SafeName ----------

func TestSafeName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"simple file", "file.txt", "file.txt", false},
		{"nested file", "sub/deep/file.txt", "sub/deep/file.txt", false},
		{"trailing slash dir", "sub/", "sub", false},
		{"dot segments", "a/./b", "a/b", false},
		{"double slash", "a//b", "a/b", false},
		{"empty", "", "", true},
		{"absolute path", "/etc/passwd", "", true},
		{"parent traversal", "../etc", "", true},
		{"nested traversal", "sub/../../../etc", "", true},
		{"NUL byte", "fi\x00le", "", true},
		{"backslash separator", "sub\\..\\..\\etc", "", true},
		{"dot only", ".", "", true},
		{"dot dot", "..", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SafeName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SafeName(%q) err=%v wantErr=%v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("SafeName(%q) = %q want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------- ContainsPath ----------

func TestContainsPath(t *testing.T) {
	tests := []struct {
		parent, child string
		want          bool
	}{
		{"/a", "/a/b", true},
		{"/a", "/a", true},
		{"/a", "/ab", false},
		{"/a/b", "/a", false},
		{"/a/b", "/a/b/c/d", true},
		{"/data", "/data/../etc", false}, // after clean: /etc not under /data
	}
	for _, tt := range tests {
		t.Run(tt.parent+"->"+tt.child, func(t *testing.T) {
			if got := ContainsPath(tt.parent, tt.child); got != tt.want {
				t.Fatalf("ContainsPath(%q, %q) = %v want %v", tt.parent, tt.child, got, tt.want)
			}
		})
	}
}

// ---------- Build / Extract round-trip ----------

func TestBuildAndExtractRoundTrip(t *testing.T) {
	entries := []Entry{
		{Name: "root.txt", Mode: 0644, Content: []byte("hello\n")},
		{Name: "sub/", Mode: 0755, IsDir: true},
		{Name: "sub/a.txt", Mode: 0644, Content: []byte("aaa\n")},
		{Name: "sub/deep/", Mode: 0755, IsDir: true},
		{Name: "sub/deep/b.bin", Mode: 0644, Content: []byte{0x00, 0x01, 0xFF, 0xFE}},
		{Name: "empty/", Mode: 0755, IsDir: true},
	}

	data, n, err := BuildBytes(entries)
	if err != nil {
		t.Fatalf("BuildBytes: %v", err)
	}
	if n != len(entries) {
		t.Fatalf("entry count: got %d want %d", n, len(entries))
	}

	// Extract into an in-memory map FS.
	fs := newMemFS()
	gotEntries, bytesWritten, err := ExtractBytes(data, "/dest", fs.handlers())
	if err != nil {
		t.Fatalf("ExtractBytes: %v", err)
	}
	if gotEntries != n {
		t.Fatalf("extracted entry count: got %d want %d", gotEntries, n)
	}

	// Verify file contents.
	wantFiles := map[string]string{
		"/dest/root.txt":          "hello\n",
		"/dest/sub/a.txt":         "aaa\n",
		"/dest/sub/deep/b.bin":    string([]byte{0x00, 0x01, 0xFF, 0xFE}),
	}
	for p, want := range wantFiles {
		got, ok := fs.files[p]
		if !ok {
			t.Fatalf("missing extracted file %q", p)
		}
		if string(got) != want {
			t.Fatalf("content mismatch %q: got %q want %q", p, got, want)
		}
	}

	// Verify directories were created.
	for _, dir := range []string{"/dest/sub", "/dest/sub/deep", "/dest/empty"} {
		if !fs.dirs[dir] {
			t.Fatalf("directory %q was not created", dir)
		}
	}

	if bytesWritten <= 0 {
		t.Fatalf("bytesWritten should be positive, got %d", bytesWritten)
	}
}

// ---------- Symlink rejection ----------

func TestExtractRejectsSymlink(t *testing.T) {
	data := buildArchiveWithSymlink(t, "link", "/etc/passwd")
	fs := newMemFS()
	_, _, err := ExtractBytes(data, "/dest", fs.handlers())
	if err == nil {
		t.Fatal("symlink entry must be rejected")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("wrong error: %v", err)
	}
}

func TestExtractRejectsHardlink(t *testing.T) {
	data := buildArchiveWithHardlink(t, "link", "target")
	fs := newMemFS()
	_, _, err := ExtractBytes(data, "/dest", fs.handlers())
	if err == nil {
		t.Fatal("hardlink entry must be rejected")
	}
}

// ---------- Traversal rejection ----------

func TestExtractRejectsTraversal(t *testing.T) {
	entries := []Entry{
		{Name: "../escape.txt", Content: []byte("evil")},
	}
	data, _, err := BuildBytes(entries)
	if err != nil {
		t.Fatal(err)
	}
	fs := newMemFS()
	_, _, err = ExtractBytes(data, "/dest", fs.handlers())
	if err == nil {
		t.Fatal("traversal entry must be rejected")
	}
}

func TestExtractRejectsAbsolute(t *testing.T) {
	entries := []Entry{
		{Name: "/etc/passwd", Content: []byte("evil")},
	}
	data, _, err := BuildBytes(entries)
	if err != nil {
		t.Fatal(err)
	}
	fs := newMemFS()
	_, _, err = ExtractBytes(data, "/dest", fs.handlers())
	if err == nil {
		t.Fatal("absolute path entry must be rejected")
	}
}

// ---------- Duplicate rejection ----------

func TestExtractRejectsDuplicate(t *testing.T) {
	entries := []Entry{
		{Name: "file.txt", Content: []byte("a")},
		{Name: "file.txt", Content: []byte("b")},
	}
	data, _, err := BuildBytes(entries)
	if err != nil {
		t.Fatal(err)
	}
	fs := newMemFS()
	_, _, err = ExtractBytes(data, "/dest", fs.handlers())
	if err == nil {
		t.Fatal("duplicate entry must be rejected")
	}
}

// ---------- Compressed size limit ----------

func TestExtractRejectsOversizedCompressed(t *testing.T) {
	// ExtractBytes enforces the compressed size limit before any decompression.
	// This mirrors the handler check on decoded base64 bytes.
	data := make([]byte, MaxCompressedSize+1)
	fs := newMemFS()
	_, _, err := ExtractBytes(data, "/dest", fs.handlers())
	if err == nil {
		t.Fatal("oversized compressed archive must be rejected")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------- Entry count limit ----------

func TestExtractRejectsTooManyEntries(t *testing.T) {
	var entries []Entry
	for i := 0; i < MaxEntries+5; i++ {
		entries = append(entries, Entry{
			Name:    fmt.Sprintf("file_%d.txt", i),
			Content: []byte("x"),
		})
	}
	data, _, err := BuildBytes(entries)
	if err != nil {
		t.Fatal(err)
	}
	fs := newMemFS()
	got, _, err := ExtractBytes(data, "/dest", fs.handlers())
	if err == nil {
		t.Fatal("too many entries must be rejected")
	}
	if got > MaxEntries {
		t.Fatalf("extracted %d entries before error, should be <= %d", got, MaxEntries)
	}
}

// ---------- Uncompressed size limit ----------

func TestExtractRejectsOversizedUncompressed(t *testing.T) {
	// Highly compressible data: compresses well but decompresses huge.
	big := bytes.Repeat([]byte("A"), int(MaxUncompressedSize)+1024)
	entries := []Entry{{Name: "big.txt", Content: big}}
	data, _, err := BuildBytes(entries)
	if err != nil {
		t.Fatal(err)
	}
	fs := newMemFS()
	_, _, err = ExtractBytes(data, "/dest", fs.handlers())
	if err == nil {
		t.Fatal("oversized uncompressed archive must be rejected")
	}
}

// ---------- Implicit parent directory creation ----------

func TestExtractCreatesImplicitParents(t *testing.T) {
	// Archive with no explicit directory entries but nested file paths.
	entries := []Entry{
		{Name: "a/b/c.txt", Content: []byte("deep")},
	}
	data, _, err := BuildBytes(entries)
	if err != nil {
		t.Fatal(err)
	}
	fs := newMemFS()
	_, _, err = ExtractBytes(data, "/dest", fs.handlers())
	if err != nil {
		t.Fatalf("extraction with implicit parents failed: %v", err)
	}
	content, ok := fs.files["/dest/a/b/c.txt"]
	if !ok || string(content) != "deep" {
		t.Fatalf("file not correctly extracted: %q", content)
	}
}

// ---------- Empty archive ----------

func TestExtractEmptyArchive(t *testing.T) {
	data, _, err := BuildBytes(nil)
	if err != nil {
		t.Fatal(err)
	}
	fs := newMemFS()
	n, bw, err := ExtractBytes(data, "/dest", fs.handlers())
	if err != nil {
		t.Fatalf("empty archive should succeed: %v", err)
	}
	if n != 0 || bw != 0 {
		t.Fatalf("empty archive: n=%d bw=%d want 0,0", n, bw)
	}
}

// =====================================================================
// Helpers
// =====================================================================

// memFS is an in-memory filesystem for testing extraction.
type memFS struct {
	dirs  map[string]bool
	files map[string][]byte
}

func newMemFS() *memFS {
	return &memFS{
		dirs:  make(map[string]bool),
		files: make(map[string][]byte),
	}
}

func (m *memFS) handlers() ExtractHandlers {
	return ExtractHandlers{
		Mkdir: func(p string) error {
			m.dirs[p] = true
			return nil
		},
		WriteFile: func(p string, content []byte) error {
			m.files[p] = content
			return nil
		},
	}
}

// buildArchiveWithSymlink constructs a raw tar.gz containing a symlink entry.
func buildArchiveWithSymlink(t *testing.T, name, link string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Linkname: link,
		Typeflag: tar.TypeSymlink,
		Format:   tar.FormatGNU,
	}); err != nil {
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

func buildArchiveWithHardlink(t *testing.T, name, target string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Linkname: target,
		Typeflag: tar.TypeLink,
		Format:   tar.FormatGNU,
	}); err != nil {
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

// TestBuildProducesValidTarGz verifies that Build output can be parsed by the
// standard library gzip+tar readers.
func TestBuildProducesValidTarGz(t *testing.T) {
	entries := []Entry{
		{Name: "a.txt", Content: []byte("alpha")},
		{Name: "dir/", IsDir: true},
		{Name: "dir/b.txt", Content: []byte("beta")},
	}
	data, n, err := BuildBytes(entries)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("entry count: %d want 3", n)
	}

	gr, err := gzip.NewReader(bytes.NewReader(data))
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
	sort.Strings(names)
	want := []string{"a.txt", "dir/", "dir/b.txt"}
	sort.Strings(want)
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Fatalf("tar names: got %v want %v", names, want)
	}
}

// TestPathJoinSafety is a regression test verifying that the defense against
// traversal lives in SafeName (which rejects ".." components), not in
// path.Join. path.Join itself does NOT prevent traversal, so the Extract
// pipeline must call SafeName before joining.
func TestPathJoinSafety(t *testing.T) {
	// path.Join alone does NOT prevent traversal:
	joined := path.Clean(path.Join("/dest", "../escape"))
	if joined == "/escape" {
		// This confirms path.Join is insufficient on its own — the real
		// defense is SafeName, which is called before Join in Extract.
	} else if !ContainsPath("/dest", joined) {
		t.Fatalf("unexpected path.Join result: %q", joined)
	}

	// SafeName rejects the traversal so Join never sees it:
	_, err := SafeName("../escape")
	if err == nil {
		t.Fatal("SafeName must reject parent traversal")
	}
}
