package filesystem

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func freshFS(t *testing.T) (*Actor, *testutil.FakeCtx) {
	t.Helper()
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	return a, ctx
}

// --- list ---

func TestHandleList(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0644)
	_ = os.Mkdir(filepath.Join(dir, "sub"), 0755)

	entries, err := a.handleListJSON(ctx, domain.FileSystemListReq{Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries.Items) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries.Items))
	}
	var fileFound, dirFound bool
	for _, e := range entries.Items {
		if e.Name == "a.txt" && !e.IsDir {
			fileFound = true
		}
		if e.Name == "sub" && e.IsDir {
			dirFound = true
		}
	}
	if !fileFound || !dirFound {
		t.Errorf("expected a.txt and sub, got %+v", entries.Items)
	}
}

func TestHandleList_EmptyPathDefaults(t *testing.T) {
	a, ctx := freshFS(t)
	if _, err := a.handleListJSON(ctx, domain.FileSystemListReq{Path: ""}); err != nil {
		t.Fatal(err)
	}
}

func TestHandleRoots(t *testing.T) {
	a, ctx := freshFS(t)
	resp, err := a.handleRoots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Roots) == 0 {
		t.Fatal("expected at least one volume root")
	}
	for _, root := range resp.Roots {
		fi, err := os.Stat(root)
		if err != nil || !fi.IsDir() {
			t.Errorf("root %q is not a browsable directory (err=%v)", root, err)
		}
	}
	if runtime.GOOS != "windows" {
		if len(resp.Roots) != 1 || resp.Roots[0] != "/" {
			t.Errorf("non-Windows roots = %v, want [\"/\"]", resp.Roots)
		}
	}
}

// Recursive listing must return Name as the path relative to the listed root
// (including subdirectory prefixes). Consumers such as the appmanager
// stub-fill gate build read paths as root + "/" + Name and would silently skip
// subdirectory files if this regressed to base names.
func TestHandleList_RecursiveRelPath(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "root.go"), []byte("x"), 0644)
	_ = os.MkdirAll(filepath.Join(dir, "handlers"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "handlers", "extra.go"), []byte("x"), 0644)
	_ = os.MkdirAll(filepath.Join(dir, "handlers", "inner"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "handlers", "inner", "deep.go"), []byte("x"), 0644)

	entries, err := a.handleListJSON(ctx, domain.FileSystemListReq{Path: dir, Depth: -1, NoIgnore: true})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range entries.Items {
		got[e.Name] = true
	}
	for _, want := range []string{"root.go", "handlers", "handlers/extra.go", "handlers/inner", "handlers/inner/deep.go"} {
		if !got[want] {
			t.Errorf("recursive list missing %q; got %v", want, got)
		}
	}
}

func TestHandleList_Missing(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_, err := a.handleListJSON(ctx, domain.FileSystemListReq{Path: filepath.Join(dir, "no_such_subdir")})
	if err == nil {
		t.Error("expected error for missing dir")
	}
}

// --- read ---

func TestHandleRead(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "test.txt")
	_ = os.WriteFile(p, []byte("hello world"), 0644)

	resp, err := a.handleRead(ctx, domain.FileSystemReadReq{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello world\n" {
		t.Errorf("expected 'hello world\\n', got %q", resp.Content)
	}
	if resp.TotalLines != 1 {
		t.Errorf("expected totalLines=1, got %d", resp.TotalLines)
	}
	if resp.StartLine != 1 {
		t.Errorf("expected startLine=1, got %d", resp.StartLine)
	}
	if resp.NumLines != 1 {
		t.Errorf("expected numLines=1, got %d", resp.NumLines)
	}
	if resp.Truncated {
		t.Errorf("expected truncated=false for whole-file read, got true")
	}
}

func TestHandleRead_Missing(t *testing.T) {
	a, ctx := freshFS(t)
	_, err := a.handleRead(ctx, domain.FileSystemReadReq{Path: "/no/such/file"})
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestHandleRead_WithOffsetLimit(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "lines.txt")
	_ = os.WriteFile(p, []byte("line1\nline2\nline3\nline4\nline5\n"), 0644)

	resp, err := a.handleRead(ctx, domain.FileSystemReadReq{Path: p, Offset: 2, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	expected := "line2\nline3\n"
	if resp.Content != expected {
		t.Errorf("expected %q, got %q", expected, resp.Content)
	}
	if resp.TotalLines != 5 {
		t.Errorf("expected totalLines=5, got %d", resp.TotalLines)
	}
	if resp.StartLine != 2 {
		t.Errorf("expected startLine=2, got %d", resp.StartLine)
	}
	if resp.NumLines != 2 {
		t.Errorf("expected numLines=2, got %d", resp.NumLines)
	}
	if !resp.Truncated {
		t.Errorf("expected truncated=true (limit=2 cut off lines 4-5), got false")
	}
}

func TestHandleRead_Truncated(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "lines.txt")
	_ = os.WriteFile(p, []byte("line1\nline2\nline3\nline4\nline5\n"), 0644)

	// Whole-file read (no offset/limit, small file): not truncated.
	resp, err := a.handleRead(ctx, domain.FileSystemReadReq{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Truncated {
		t.Errorf("whole-file read: expected truncated=false, got true")
	}

	// limit < total: truncated.
	resp, err = a.handleRead(ctx, domain.FileSystemReadReq{Path: p, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if resp.NumLines != 3 || resp.StartLine != 1 {
		t.Fatalf("limit=3: expected 3 lines from 1, got %d from %d", resp.NumLines, resp.StartLine)
	}
	if !resp.Truncated {
		t.Errorf("limit=3 of 5: expected truncated=true, got false")
	}

	// limit exactly covers the rest (offset=4, limit=2 reads lines 4-5): not truncated.
	resp, err = a.handleRead(ctx, domain.FileSystemReadReq{Path: p, Offset: 4, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if resp.NumLines != 2 || resp.StartLine != 4 {
		t.Fatalf("offset=4 limit=2: expected 2 lines from 4, got %d from %d", resp.NumLines, resp.StartLine)
	}
	if resp.Truncated {
		t.Errorf("offset=4 limit=2 (reaches EOF): expected truncated=false, got true")
	}

	// tail reads the last window: not truncated (tail always fits).
	resp, err = a.handleRead(ctx, domain.FileSystemReadReq{Path: p, Tail: 2})
	if err != nil {
		t.Fatal(err)
	}
	if resp.NumLines != 2 {
		t.Fatalf("tail=2: expected 2 lines, got %d", resp.NumLines)
	}
	if resp.Truncated {
		t.Errorf("tail=2: expected truncated=false, got true")
	}
}

// --- read_base64 ---

func TestHandleReadBase64(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "bin.dat")
	_ = os.WriteFile(p, []byte{0xDE, 0xAD, 0xBE, 0xEF}, 0644)

	out, err := a.handleReadBase64(ctx, domain.FileSystemReadBase64Req{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "3q2+7w==" {
		t.Errorf("expected base64 '3q2+7w==', got %q", out.Content)
	}
}

func TestHandleReadBase64_Missing(t *testing.T) {
	a, ctx := freshFS(t)
	_, err := a.handleReadBase64(ctx, domain.FileSystemReadBase64Req{Path: "/no/such/file"})
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestHandleReadBase64_RejectsOversized(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "big.dat")
	if err := os.WriteFile(p, make([]byte, maxBase64Size+1), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleReadBase64(ctx, domain.FileSystemReadBase64Req{Path: p}); err == nil {
		t.Error("expected size-cap error")
	}
}

// --- write_base64 ---

func TestHandleWriteBase64(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "bin.dat")
	want := []byte{0x01, 0x02, 0x03, 0x04}

	resp, err := a.handleWriteBase64(ctx, domain.FileSystemWriteBase64Req{
		Path:    p,
		Content: base64.StdEncoding.EncodeToString(want),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Warning != "" {
		t.Errorf("expected empty warning, got %q", resp.Warning)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, want) {
		t.Errorf("expected %v, got %v", want, data)
	}
}

func TestHandleWriteBase64_Overwrites(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "bin.dat")
	if err := os.WriteFile(p, []byte("old-content"), 0644); err != nil {
		t.Fatal(err)
	}
	want := []byte{0xFF, 0xFE, 0xFD}

	_, err := a.handleWriteBase64(ctx, domain.FileSystemWriteBase64Req{
		Path:    p,
		Content: base64.StdEncoding.EncodeToString(want),
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, want) {
		t.Errorf("expected overwrite to %v, got %v", want, data)
	}
}

func TestHandleWriteBase64_InvalidBase64(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "bin.dat")

	_, err := a.handleWriteBase64(ctx, domain.FileSystemWriteBase64Req{
		Path:    p,
		Content: "!!!not-base64!!!",
	})
	if err == nil {
		t.Fatal("expected error for invalid base64 content")
	}
}

func TestHandleWriteBase64_OutsideRootsRequiresConfirm(t *testing.T) {
	dir := t.TempDir()
	a := &Actor{}
	a.setAllowedRoots([]string{dir})
	ctx := testutil.HumanCtx(testutil.GenActorID())

	_, err := a.handleWriteBase64(ctx, domain.FileSystemWriteBase64Req{
		Path:    filepath.Join(dir, "..", "escaped.dat"),
		Content: base64.StdEncoding.EncodeToString([]byte("x")),
	})
	if err == nil {
		t.Fatal("expected outside-root error without confirm")
	}

	_, err = a.handleWriteBase64(ctx, domain.FileSystemWriteBase64Req{
		Path:    filepath.Join(dir, "..", "escaped.dat"),
		Content: base64.StdEncoding.EncodeToString([]byte("x")),
		Confirm: true,
	})
	if err != nil {
		t.Fatalf("expected confirmed outside-root write to succeed, got: %v", err)
	}
}

// --- read_chunk ---

func TestHandleReadChunk(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "chunk.dat")
	payload := []byte("hello world chunk test")
	_ = os.WriteFile(p, payload, 0644)

	resp, err := a.handleReadChunk(ctx, domain.FileSystemReadChunkReq{Path: p, Offset: 0, Length: 5})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Length != 5 || resp.Total != int64(len(payload)) || resp.Offset != 0 {
		t.Errorf("unexpected resp: %+v", resp)
	}
	decoded, err := base64.StdEncoding.DecodeString(resp.Data)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != "hello" {
		t.Errorf("expected 'hello', got %q", string(decoded))
	}
}

func TestHandleReadChunk_PastEOFReturnsZero(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "chunk.dat")
	_ = os.WriteFile(p, []byte("abc"), 0644)

	resp, err := a.handleReadChunk(ctx, domain.FileSystemReadChunkReq{Path: p, Offset: 100, Length: 16})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Length != 0 || resp.Total != 3 {
		t.Errorf("expected zero-length resp at EOF, got %+v", resp)
	}
}

func TestHandleReadChunk_FileShorterThanLength(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "chunk.dat")
	_ = os.WriteFile(p, []byte("abc"), 0644)

	resp, err := a.handleReadChunk(ctx, domain.FileSystemReadChunkReq{Path: p, Offset: 0, Length: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Length != 3 {
		t.Errorf("expected partial length 3, got %d", resp.Length)
	}
	decoded, _ := base64.StdEncoding.DecodeString(resp.Data)
	if string(decoded) != "abc" {
		t.Errorf("expected 'abc', got %q", string(decoded))
	}
}

func TestHandleReadChunk_LengthOverCapClamped(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "chunk.dat")
	_ = os.WriteFile(p, make([]byte, maxChunkSize*2), 0644)

	resp, err := a.handleReadChunk(ctx, domain.FileSystemReadChunkReq{Path: p, Offset: 0, Length: maxChunkSize * 4})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Length != maxChunkSize {
		t.Errorf("expected clamp to %d, got %d", maxChunkSize, resp.Length)
	}
}

func TestHandleReadChunk_RejectsZeroLength(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "x.dat")
	_ = os.WriteFile(p, []byte("x"), 0644)

	if _, err := a.handleReadChunk(ctx, domain.FileSystemReadChunkReq{Path: p, Offset: 0, Length: 0}); err == nil {
		t.Error("expected error for length=0")
	}
}

func TestHandleReadChunk_RejectsNegativeOffset(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "x.dat")
	_ = os.WriteFile(p, []byte("x"), 0644)

	if _, err := a.handleReadChunk(ctx, domain.FileSystemReadChunkReq{Path: p, Offset: -1, Length: 4}); err == nil {
		t.Error("expected error for negative offset")
	}
}

// --- write ---

func TestHandleWrite(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "out.txt")

	err := a.handleWrite(ctx, domain.FileSystemWriteReq{Path: p, Content: "written"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "written" {
		t.Errorf("expected 'written', got %q", string(data))
	}
}

func TestHandleWrite_CreatesDir(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "dir", "out.txt")

	err := a.handleWrite(ctx, domain.FileSystemWriteReq{Path: p, Content: "deep"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "deep" {
		t.Errorf("expected 'deep', got %q", string(data))
	}
}

// --- edit ---

func TestHandleEdit(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "edit.txt")
	_ = os.WriteFile(p, []byte("foo bar baz"), 0644)

	resp, err := a.handleEdit(ctx, domain.FileSystemEditReq{
		Path:      p,
		OldString: "bar",
		NewString: "qux",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Replacements != 1 {
		t.Errorf("expected 1 replacement, got %d", resp.Replacements)
	}
	if len(resp.Hunks) == 0 {
		t.Error("expected hunks in response")
	}
	data, _ := os.ReadFile(p)
	if string(data) != "foo qux baz" {
		t.Errorf("expected 'foo qux baz', got %q", string(data))
	}
}

func TestHandleEdit_ReplaceAll(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "multi.txt")
	_ = os.WriteFile(p, []byte("aaa bbb aaa"), 0644)

	resp, err := a.handleEdit(ctx, domain.FileSystemEditReq{
		Path:       p,
		OldString:  "aaa",
		NewString:  "ccc",
		ReplaceAll: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Replacements != 2 {
		t.Errorf("expected 2 replacements, got %d", resp.Replacements)
	}
	if len(resp.Hunks) == 0 {
		t.Error("expected hunks in response")
	}
	data, _ := os.ReadFile(p)
	if string(data) != "ccc bbb ccc" {
		t.Errorf("expected 'ccc bbb ccc', got %q", string(data))
	}
}

func TestHandleEdit_NotFound(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "edit.txt")
	_ = os.WriteFile(p, []byte("hello"), 0644)

	_, err := a.handleEdit(ctx, domain.FileSystemEditReq{
		Path:      p,
		OldString: "missing",
		NewString: "x",
	})
	if err == nil {
		t.Error("expected error when old_string not found")
	}
}

func TestHandleWrite_CreateFile(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "newdir", "new.txt")

	err := a.handleWrite(ctx, domain.FileSystemWriteReq{
		Path:    p,
		Content: "hello world",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(data))
	}
}

func TestHandleEdit_QuoteNormalization(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "quotes.txt")
	// File contains straight quotes; request uses curly quotes.
	_ = os.WriteFile(p, []byte(`say "hello" and 'world'`), 0644)

	resp, err := a.handleEdit(ctx, domain.FileSystemEditReq{
		Path:      p,
		OldString: `say "hello" and 'world'`,
		NewString: `say "goodbye" and 'moon'`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Replacements != 1 {
		t.Errorf("expected 1 replacement after quote normalization, got %d", resp.Replacements)
	}
	data, _ := os.ReadFile(p)
	if string(data) != `say "goodbye" and 'moon'` {
		t.Errorf("expected quote-normalized replacement, got %q", string(data))
	}
}

func TestHandleEdit_MultipleWithoutReplaceAll(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "multi.txt")
	_ = os.WriteFile(p, []byte("aaa bbb aaa"), 0644)

	_, err := a.handleEdit(ctx, domain.FileSystemEditReq{
		Path:      p,
		OldString: "aaa",
		NewString: "ccc",
	})
	if err == nil {
		t.Error("expected error when multiple matches without replace_all")
	}
}

func TestHandleEdit_CRLFFile(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "crlf.txt")
	// File uses CRLF line endings.
	_ = os.WriteFile(p, []byte("line1\r\nline2\r\nline3\r\n"), 0644)

	// old_string uses LF (as produced by Read tool).
	// Edit normalizes file to LF, performs match, writes LF.
	resp, err := a.handleEdit(ctx, domain.FileSystemEditReq{
		Path:      p,
		OldString: "line2\nline3",
		NewString: "replaced\nend",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Replacements != 1 {
		t.Errorf("expected 1 replacement, got %d", resp.Replacements)
	}
	data, _ := os.ReadFile(p)
	// CRLF is normalized to LF on read, replacement is LF, write is LF.
	if string(data) != "line1\nreplaced\nend\n" {
		t.Errorf("expected LF-normalized output, got %q", string(data))
	}
}

func TestHandleEdit_LFFileWithCRLFOldString(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "lf.txt")
	// File uses LF line endings.
	_ = os.WriteFile(p, []byte("line1\nline2\nline3\n"), 0644)

	// old_string accidentally contains CRLF.
	resp, err := a.handleEdit(ctx, domain.FileSystemEditReq{
		Path:      p,
		OldString: "line2\r\nline3",
		NewString: "replaced\nend",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Replacements != 1 {
		t.Errorf("expected 1 replacement, got %d", resp.Replacements)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "line1\nreplaced\nend\n" {
		t.Errorf("expected LF preserved, got %q", string(data))
	}
}

func TestHandleEdit_MixedLineEndingsAndEscapedOldString(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "mixed.txt")
	if err := os.WriteFile(p, []byte("first\r\nsecond\rthird\n"), 0644); err != nil {
		t.Fatal(err)
	}

	resp, err := a.handleEdit(ctx, domain.FileSystemEditReq{
		Path:      p,
		OldString: `first\r\nsecond\rthird\n`,
		NewString: "done\r\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Replacements != 1 {
		t.Fatalf("expected 1 replacement, got %d", resp.Replacements)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "done\n" {
		t.Errorf("expected LF-normalized output, got %q", string(data))
	}
}

func TestHandleEdit_OverwriteReplacesEntireFile(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "target.txt")
	_ = os.WriteFile(p, []byte("old content\nline two\n"), 0644)

	resp, err := a.handleEdit(ctx, domain.FileSystemEditReq{
		Path:      p,
		NewString: "new content\nline b\n",
		Overwrite: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Replacements != 1 {
		t.Errorf("expected 1 replacement, got %d", resp.Replacements)
	}

	data, _ := os.ReadFile(p)
	if string(data) != "new content\nline b\n" {
		t.Errorf("unexpected file content: %q", string(data))
	}
}

func TestHandleEdit_TabsInContentSpacesInSearch(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "tabs.go")
	content := "func main() {\n\t\tcase \"type\":\n\t\t\treturn nil\n}\n"
	_ = os.WriteFile(p, []byte(content), 0644)

	resp, err := a.handleEdit(ctx, domain.FileSystemEditReq{
		Path:      p,
		OldString: "    case \"type\":\n        return nil",
		NewString: "    case \"int\":\n        return 42",
	})
	if err != nil {
		t.Fatalf("edit should succeed with whitespace-tolerant matching: %v", err)
	}
	if resp.Replacements != 1 {
		t.Errorf("expected 1 replacement, got %d", resp.Replacements)
	}
	data, _ := os.ReadFile(p)
	expected := "func main() {\n    case \"int\":\n        return 42\n}\n"
	if string(data) != expected {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", string(data), expected)
	}
}

func TestHandleEdit_SpacesInContentTabsInSearch(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "spaces.go")
	content := "func main() {\n    case \"type\":\n        return nil\n}\n"
	_ = os.WriteFile(p, []byte(content), 0644)

	resp, err := a.handleEdit(ctx, domain.FileSystemEditReq{
		Path:      p,
		OldString: "\t\tcase \"type\":\n\t\t\treturn nil",
		NewString: "\t\tcase \"int\":\n\t\t\treturn 42",
	})
	if err != nil {
		t.Fatalf("edit should succeed with whitespace-tolerant matching: %v", err)
	}
	if resp.Replacements != 1 {
		t.Errorf("expected 1 replacement, got %d", resp.Replacements)
	}
	data, _ := os.ReadFile(p)
	expected := "func main() {\n\t\tcase \"int\":\n\t\t\treturn 42\n}\n"
	if string(data) != expected {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", string(data), expected)
	}
}

func TestFindWhitespaceTolerant(t *testing.T) {
	tests := []struct {
		name    string
		content string
		search  string
		want    string
		ok      bool
	}{
		{"content tabs search spaces", "\t\tcase \"type\":", "    case \"type\":", "\t\tcase \"type\":", true},
		{"content spaces search tabs", "    case \"type\":", "\t\tcase \"type\":", "    case \"type\":", true},
		{"different tab count", "\t\t\t\tdeep", "\tdeep", "\t\t\t\tdeep", true},
		{"no match", "hello world", "goodbye", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := findWhitespaceTolerant(tt.content, tt.search)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// --- rm ---

func TestHandleRm_DefaultReturnsPreview(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "target.txt")
	_ = os.WriteFile(p, []byte("x"), 0644)

	resp, err := a.handleRm(ctx, domain.FileSystemRmReq{Path: p})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Preview {
		t.Errorf("expected preview, got Preview=%v", resp.Preview)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("file should still exist in preview mode: %v", err)
	}
}

func TestHandleRm_ConfirmDeletes(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "target.txt")
	_ = os.WriteFile(p, []byte("x"), 0644)

	resp, err := a.handleRm(ctx, domain.FileSystemRmReq{Path: p, Confirm: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Preview {
		t.Errorf("expected deletion, got preview")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("file should be removed: %v", err)
	}
}

func TestHandleRm_ForceDeletes(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "target.txt")
	_ = os.WriteFile(p, []byte("x"), 0644)

	resp, err := a.handleRm(ctx, domain.FileSystemRmReq{Path: p, Force: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Preview {
		t.Errorf("expected deletion, got preview")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("file should be removed: %v", err)
	}
}

func TestHandleRm_RecursiveDirectoryConfirm(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	_ = os.MkdirAll(sub, 0755)
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0644)
	_ = os.WriteFile(filepath.Join(sub, "b.txt"), []byte("y"), 0644)

	resp, err := a.handleRm(ctx, domain.FileSystemRmReq{Path: dir, Recursive: true, Confirm: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Preview {
		t.Errorf("expected deletion, got preview")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("directory should be removed: %v", err)
	}
}

// --- glob ---

func TestHandleGlob(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte{}, 0644)
	_ = os.WriteFile(filepath.Join(dir, "b.go"), []byte{}, 0644)
	_ = os.Mkdir(filepath.Join(dir, "sub"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "sub", "c.txt"), []byte{}, 0644)

	resp, err := a.handleGlob(ctx, domain.FileSystemGlobReq{Pattern: "*.txt", Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Files) != 1 || resp.Files[0] != "a.txt" {
		t.Errorf("expected [a.txt], got %v", resp.Files)
	}
	if resp.NumFiles != 1 {
		t.Errorf("expected numFiles=1, got %d", resp.NumFiles)
	}
	if resp.Truncated {
		t.Error("expected truncated=false")
	}
}

func TestHandleGlob_NoMatches(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte{}, 0644)

	resp, err := a.handleGlob(ctx, domain.FileSystemGlobReq{Pattern: "*.go", Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Files) != 0 {
		t.Errorf("expected no matches, got %v", resp.Files)
	}
	if resp.NumFiles != 0 {
		t.Errorf("expected numFiles=0, got %d", resp.NumFiles)
	}
}

// --- grep ---

func TestHandleGrep(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello world\nfoo bar\nhello again"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "b.txt"), []byte("no match here"), 0644)

	result, err := a.handleGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "hello",
		Path:       dir,
		Recursive:  true,
		OutputMode: "content",
	})
	if err != nil {
		t.Fatal(err)
	}
	matches := result.Matches
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	if matches[0].Line != 1 || matches[1].Line != 3 {
		t.Errorf("expected lines 1 and 3, got %d and %d", matches[0].Line, matches[1].Line)
	}
}

func TestHandleGrep_FilesWithMatches(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "b.txt"), []byte("nope"), 0644)

	result, err := a.handleGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "hello",
		Path:       dir,
		Recursive:  true,
		OutputMode: "files",
	})
	if err != nil {
		t.Fatal(err)
	}
	files := result.Files
	if len(files) != 1 || files[0] != "a.txt" {
		t.Errorf("expected [a.txt], got %v", files)
	}
}

func TestHandleGrep_Count(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nhello\nworld"), 0644)

	result, err := a.handleGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "hello",
		Path:       dir,
		Recursive:  true,
		OutputMode: "count",
	})
	if err != nil {
		t.Fatal(err)
	}
	counts := result.Counts
	if len(counts) != 1 || counts[0].Count != 2 {
		t.Errorf("expected count 2, got %v", counts)
	}
}

func TestHandleGrep_CaseInsensitive(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("Hello World"), 0644)

	result, err := a.handleGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "hello",
		Path:       dir,
		Recursive:  true,
		IgnoreCase: true,
		OutputMode: "content",
	})
	if err != nil {
		t.Fatal(err)
	}
	matches := result.Matches
	if len(matches) != 1 {
		t.Errorf("expected 1 match, got %d", len(matches))
	}
}

func TestHandleGrep_WithContext(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("line1\nline2\nmatch\nline4\nline5"), 0644)

	result, err := a.handleGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "match",
		Path:       dir,
		Recursive:  true,
		Context:    1,
		OutputMode: "content",
	})
	if err != nil {
		t.Fatal(err)
	}
	matches := result.Matches
	if len(matches) != 3 {
		t.Fatalf("expected 3 lines (context+match+context), got %d", len(matches))
	}
	if matches[0].Line != 2 || matches[1].Line != 3 || matches[2].Line != 4 {
		t.Errorf("expected lines 2,3,4 got %v", matches)
	}
}

func TestHandleGrep_LineNumberFlag(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nworld"), 0644)

	result, err := a.handleGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "hello",
		Path:       dir,
		Recursive:  true,
		OutputMode: "content",
	})
	if err != nil {
		t.Fatal(err)
	}
	// -n switches output_mode from default "files" to "content".
	if result.OutputMode != "content" {
		t.Errorf("expected output_mode=content (from -n), got %q", result.OutputMode)
	}
	if len(result.Matches) != 1 {
		t.Errorf("expected 1 match, got %d", len(result.Matches))
	}
}

func TestHandleGrep_FilesWithMatchesFlag(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "b.txt"), []byte("nope"), 0644)

	result, err := a.handleGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "hello",
		Path:       dir,
		Recursive:  true,
		OutputMode: "files",
	})
	if err != nil {
		t.Fatal(err)
	}
	files := result.Files
	if len(files) != 1 || files[0] != "a.txt" {
		t.Errorf("expected [a.txt], got %v", files)
	}
}

func TestHandleGrep_InvertMatch(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nworld\nhello"), 0644)

	result, err := a.handleGrep(ctx, domain.FileSystemGrepReq{
		Pattern:     "hello",
		Path:        dir,
		Recursive:   true,
		InvertMatch: true,
		OutputMode:  "content",
	})
	if err != nil {
		t.Fatal(err)
	}
	matches := result.Matches
	if len(matches) != 1 {
		t.Fatalf("expected 1 match (invert), got %d", len(matches))
	}
	if matches[0].Content != "world" {
		t.Errorf("expected 'world', got %q", matches[0].Content)
	}
}

func TestHandleGrep_WordRegexp(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nhelloworld\nhello world"), 0644)

	result, err := a.handleGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "hello",
		Path:       dir,
		Recursive:  true,
		WordRegexp: true,
		OutputMode: "content",
	})
	if err != nil {
		t.Fatal(err)
	}
	matches := result.Matches
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches (word boundary), got %d", len(matches))
	}
	for _, m := range matches {
		if m.Content != "hello" && m.Content != "hello world" {
			t.Errorf("unexpected match content: %q", m.Content)
		}
	}
}

func TestHandleGrep_ExtendedRegexpNoop(t *testing.T) {
	// -E should not produce an error (Go regexp is already ERE-like).
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello world"), 0644)

	result, err := a.handleGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "hello.*world",
		Path:       dir,
		Recursive:  true,
		OutputMode: "content",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 {
		t.Errorf("expected 1 match, got %d", len(result.Matches))
	}
}

func TestHandleGrep_PosixCharClass(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("abc123\nxyz\n456"), 0644)

	result, err := a.handleGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "[[:digit:]]+",
		Path:       dir,
		Recursive:  true,
		OutputMode: "content",
	})
	if err != nil {
		t.Fatal(err)
	}
	// [[:digit:]] should match lines containing digits.
	if len(result.Matches) != 2 {
		t.Errorf("expected 2 matches (digit class), got %d", len(result.Matches))
	}
}

func TestHandleGrep_NegatedPosixCharClass(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("abc123\nxyz\n456"), 0644)

	result, err := a.handleGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "[[:^digit:]]+",
		Path:       dir,
		Recursive:  true,
		OutputMode: "content",
	})
	if err != nil {
		t.Fatal(err)
	}
	// [^0-9]+ matches non-digit runs: 'abc' prefix on line 1 and full 'xyz' on line 2.
	if len(result.Matches) != 2 {
		t.Errorf("expected 2 matches (negated digit class), got %d", len(result.Matches))
	}
}

func TestHandleGrep_SpaceCharClass(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello world\nnoworld\nfoo bar"), 0644)

	result, err := a.handleGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "hello[[:space:]]world",
		Path:       dir,
		Recursive:  true,
		OutputMode: "content",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 {
		t.Errorf("expected 1 match (space class), got %d", len(result.Matches))
	}
}

func TestHandleGrep_GlobWithQuotes(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("hello"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "b.txt"), []byte("hello"), 0644)

	// --include="*.go" — quotes should be stripped by flagparse setField.
	result, err := a.handleGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "hello",
		Path:       dir,
		Recursive:  true,
		Glob:       "*.go",
		OutputMode: "files",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0] != "a.go" {
		t.Errorf("expected [a.go], got %v", result.Files)
	}
}

func TestHandleGrep_GlobBraceRecursive(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.ts"), []byte("hello"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "b.tsx"), []byte("hello"), 0644)
	_ = os.Mkdir(filepath.Join(dir, "sub"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "sub", "c.ts"), []byte("hello"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "sub", "d.go"), []byte("hello"), 0644)

	result, err := a.handleGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "hello",
		Path:       dir,
		Recursive:  true,
		Glob:       "*.{ts,tsx}",
		OutputMode: "files",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"a.ts": true, "b.tsx": true, "sub/c.ts": true}
	got := map[string]bool{}
	for _, f := range result.Files {
		got[f] = true
	}
	if len(got) != len(want) {
		t.Errorf("expected %v, got %v", want, got)
	}
	for f := range want {
		if !got[f] {
			t.Errorf("missing expected file %q, got %v", f, got)
		}
	}
}

func TestHandleGrep_GlobRelativePath(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0644)
	_ = os.Mkdir(filepath.Join(dir, "sub"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("hello"), 0644)

	result, err := a.handleGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "hello",
		Path:       dir,
		Recursive:  true,
		Glob:       "sub/*.txt",
		OutputMode: "files",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0] != "sub/b.txt" {
		t.Errorf("expected [sub/b.txt], got %v", result.Files)
	}
}

func TestHandleGrep_GlobNegation(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("hello"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "a_test.go"), []byte("hello"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "b.go"), []byte("hello"), 0644)

	result, err := a.handleGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "hello",
		Path:       dir,
		Recursive:  true,
		Glob:       "*.go,!*_test.go",
		OutputMode: "files",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range result.Files {
		got[f] = true
	}
	if len(got) != 2 || !got["a.go"] || !got["b.go"] || got["a_test.go"] {
		t.Errorf("expected [a.go b.go] without a_test.go, got %v", result.Files)
	}
}

func TestHandleGrep_GlobDoubleStar(t *testing.T) {
	a, ctx := freshFS(t)
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "deep", "nested"), 0755)
	_ = os.WriteFile(filepath.Join(dir, "root.go"), []byte("hello"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "deep", "mid.go"), []byte("hello"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "deep", "nested", "leaf.go"), []byte("hello"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "deep", "nested", "leaf.ts"), []byte("hello"), 0644)

	result, err := a.handleGrep(ctx, domain.FileSystemGrepReq{
		Pattern:    "hello",
		Path:       dir,
		Recursive:  true,
		Glob:       "**/*.go",
		OutputMode: "files",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"root.go": true, "deep/mid.go": true, "deep/nested/leaf.go": true}
	got := map[string]bool{}
	for _, f := range result.Files {
		got[f] = true
	}
	if len(got) != len(want) {
		t.Errorf("expected %v, got %v", want, got)
	}
	for f := range want {
		if !got[f] {
			t.Errorf("missing expected file %q, got %v", f, got)
		}
	}
}

// --- sandbox ---

func TestCollectMountPaths(t *testing.T) {
	mounts := []domain.ProjectRef{
		{ActorID: "p1", Path: filepath.FromSlash("/home/user/proj1")},
		{ActorID: "p2", Path: filepath.FromSlash("/home/user/proj2"), Mounts: []domain.ProjectMount{
			{Name: "sub", Path: filepath.FromSlash("/home/user/proj2/sub")},
		}},
	}
	got := collectMountPaths(mounts)
	want := []string{
		filepath.FromSlash("/home/user/proj1"),
		filepath.FromSlash("/home/user/proj2"),
		filepath.FromSlash("/home/user/proj2/sub"),
	}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}

func TestIsPathAllowed_InternalBypasses(t *testing.T) {
	a := &Actor{allowedRoots: []string{"/workspace"}}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	if !a.isPathAllowed(ctx, "/etc/passwd") {
		t.Error("internal caller should bypass sandbox")
	}
}

func TestIsPathAllowed_ExternalBlockedOutsideRoots(t *testing.T) {
	a := &Actor{allowedRoots: []string{filepath.FromSlash("/workspace"), filepath.FromSlash("/data/proj")}}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	if a.isPathAllowed(ctx, filepath.FromSlash("/etc/passwd")) {
		t.Error("external caller should be blocked outside roots")
	}
	if a.isPathAllowed(ctx, filepath.FromSlash("/work")) {
		t.Error("external caller should be blocked by prefix confusion")
	}
}

func TestIsPathAllowed_ExternalAllowedInsideRoots(t *testing.T) {
	a := &Actor{allowedRoots: []string{filepath.FromSlash("/workspace"), filepath.FromSlash("/data/proj")}}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	if !a.isPathAllowed(ctx, filepath.FromSlash("/workspace")) {
		t.Error("external caller should be allowed at exact root")
	}
	if !a.isPathAllowed(ctx, filepath.FromSlash("/workspace/sub/file.go")) {
		t.Error("external caller should be allowed inside root")
	}
	if !a.isPathAllowed(ctx, filepath.FromSlash("/data/proj")) {
		t.Error("external caller should be allowed at second root")
	}
}

func TestIsPathAllowed_EmptyRootsDenies(t *testing.T) {
	a := &Actor{allowedRoots: []string{}}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	if a.isPathAllowed(ctx, "/any/path") {
		t.Error("empty roots should deny all non-internal paths")
	}
}

func TestHandleWrite_OutsideRootsRequiresConfirm(t *testing.T) {
	dir := t.TempDir()
	a := &Actor{}
	a.setAllowedRoots([]string{dir})
	ctx := testutil.HumanCtx(testutil.GenActorID())

	err := a.handleWrite(ctx, domain.FileSystemWriteReq{Path: filepath.Join(dir, "..", "escaped.txt"), Content: "x"})
	if err == nil {
		t.Fatal("expected outside-root error without confirm")
	}

	err = a.handleWrite(ctx, domain.FileSystemWriteReq{Path: filepath.Join(dir, "..", "escaped.txt"), Content: "x", Confirm: true})
	if err != nil {
		t.Fatalf("expected confirmed outside-root write to succeed, got: %v", err)
	}
}

func TestHandleWrite_SandboxAllowsInsideRoot(t *testing.T) {
	dir := t.TempDir()
	a := &Actor{}
	a.setAllowedRoots([]string{dir})
	ctx := testutil.HumanCtx(testutil.GenActorID())
	p := filepath.Join(dir, "sub", "out.txt")

	err := a.handleWrite(ctx, domain.FileSystemWriteReq{Path: p, Content: "allowed"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "allowed" {
		t.Errorf("expected 'allowed', got %q", string(data))
	}
}

func TestHandleEdit_OutsideRootsRequiresConfirm(t *testing.T) {
	dir := t.TempDir()
	a := &Actor{}
	a.setAllowedRoots([]string{dir})
	ctx := testutil.HumanCtx(testutil.GenActorID())

	_, err := a.handleEdit(ctx, domain.FileSystemEditReq{
		Path:      filepath.Join(dir, "..", "escaped.txt"),
		OldString: "old",
		NewString: "new",
	})
	if err == nil {
		t.Fatal("expected outside-root error without confirm")
	}
}

func TestHandleWrite_SandboxCreatesInsideRoot(t *testing.T) {
	dir := t.TempDir()
	a := &Actor{}
	a.setAllowedRoots([]string{dir})
	ctx := testutil.HumanCtx(testutil.GenActorID())
	p := filepath.Join(dir, "created.txt")

	err := a.handleWrite(ctx, domain.FileSystemWriteReq{
		Path:    p,
		Content: "created",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "created" {
		t.Errorf("expected 'created', got %q", string(data))
	}
}
