package converter

import (
	"reflect"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestGlobConverter(t *testing.T) {
	resp := domain.FileSystemGlobResp{
		Files:    []string{"a.go", "b.go", "c.go"},
		NumFiles: 3,
	}
	out, err := globConverter(resp)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(out, "a.go") {
		t.Errorf("expected a.go in output, got:\n%s", out)
	}
	if !strings.Contains(out, "(3 files)") {
		t.Errorf("expected '(3 files)' in output, got:\n%s", out)
	}
}

func TestGlobConverter_NoteAppended(t *testing.T) {
	resp := domain.FileSystemGlobResp{
		Files:    []string{"a.go"},
		NumFiles: 1,
		Note:     "[worktree note] outside your bound worktree",
	}
	out, err := globConverter(resp)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.HasSuffix(out, "\n\n[worktree note] outside your bound worktree") {
		t.Errorf("expected note appended after the file list, got:\n%s", out)
	}
}

func TestGrepConverter_NoteAppendedAllModes(t *testing.T) {
	note := "[worktree note] outside your bound worktree"
	for _, mode := range []string{"files", "count", "content"} {
		resp := domain.FileSystemGrepResp{OutputMode: mode, Note: note}
		switch mode {
		case "files":
			resp.Files = []string{"a.go"}
			resp.NumMatches = 1
		case "count":
			resp.Counts = []domain.FileSystemGrepCount{{File: "a.go", Count: 2}}
			resp.NumMatches = 2
		default:
			resp.Matches = []domain.FileSystemGrepMatch{{File: "a.go", Line: 1, Content: "x"}}
			resp.NumMatches = 1
		}
		out, err := grepConverter(resp)
		if err != nil {
			t.Fatalf("mode %s: error: %v", mode, err)
		}
		if !strings.HasSuffix(out, "\n\n"+note) {
			t.Errorf("mode %s: expected note appended, got:\n%s", mode, out)
		}
	}
}

func TestReadConverter_NoteAppended(t *testing.T) {
	resp := domain.FileSystemReadResp{
		Content:    "line1\nline2\n",
		TotalLines: 2,
		StartLine:  1,
		NumLines:   2,
		Note:       "[worktree note] outside your bound worktree",
	}
	out, err := readConverter(resp)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.HasSuffix(out, "\n\n[worktree note] outside your bound worktree") {
		t.Errorf("expected note appended after numbered lines, got:\n%s", out)
	}
	if !strings.Contains(out, "1│line1") || !strings.Contains(out, "2│line2") {
		t.Errorf("expected numbered content intact, got:\n%s", out)
	}
}

func TestGrepConverterFiles(t *testing.T) {
	resp := domain.FileSystemGrepResp{
		Files:      []string{"a.go", "b.go"},
		NumMatches: 5,
		OutputMode: "files",
	}
	out, err := grepConverter(resp)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(out, "a.go") {
		t.Errorf("expected a.go in output, got:\n%s", out)
	}
	if !strings.Contains(out, "(2 files)") {
		t.Errorf("expected '(2 files)' in output, got:\n%s", out)
	}
}

func TestGrepConverterContent(t *testing.T) {
	resp := domain.FileSystemGrepResp{
		Matches: []domain.FileSystemGrepMatch{
			{File: "a.go", Line: 1, Content: "func main()"},
			{File: "a.go", Line: 5, Content: "func helper()"},
		},
		NumMatches: 2,
		OutputMode: "content",
	}
	out, err := grepConverter(resp)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(out, "a.go:1:func main()") {
		t.Errorf("expected match line, got:\n%s", out)
	}
	if !strings.Contains(out, "(2 matches in 1 files)") {
		t.Errorf("expected summary, got:\n%s", out)
	}
}

func TestGrepConverterContentTruncatesLongLines(t *testing.T) {
	long := strings.Repeat("a", 1500)
	resp := domain.FileSystemGrepResp{
		Matches: []domain.FileSystemGrepMatch{
			{File: "a.go", Line: 1, Content: long},
		},
		NumMatches: 1,
		OutputMode: "content",
	}
	out, err := grepConverter(resp)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	// The full 1500-char content must not appear; the truncation marker must.
	if strings.Contains(out, long) {
		t.Errorf("expected long line to be truncated, got %d chars of content", len(long))
	}
	if !strings.Contains(out, "... (+1000 more chars)") {
		t.Errorf("expected truncation marker, got:\n%s", out)
	}
}

func TestGrepConverterCount(t *testing.T) {
	resp := domain.FileSystemGrepResp{
		Counts: []domain.FileSystemGrepCount{
			{File: "a.go", Count: 3},
			{File: "b.go", Count: 2},
		},
		NumMatches: 5,
		OutputMode: "count",
	}
	out, err := grepConverter(resp)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(out, "a.go: 3 matches") {
		t.Errorf("expected count line, got:\n%s", out)
	}
	if !strings.Contains(out, "(5 total matches in 2 files)") {
		t.Errorf("expected summary, got:\n%s", out)
	}
}

func TestReadConverter(t *testing.T) {
	resp := domain.FileSystemReadResp{
		Content:    "line1\nline2\nline3\n",
		TotalLines: 10,
		StartLine:  1,
		NumLines:   3,
	}
	out, err := readConverter(resp)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(out, "    1│line1") {
		t.Errorf("expected line 1, got:\n%s", out)
	}
	if !strings.Contains(out, "    3│line3") {
		t.Errorf("expected line 3, got:\n%s", out)
	}
}

func TestRmConverterSingle(t *testing.T) {
	resp := domain.FileSystemRmResp{
		Removed: []string{"build/"},
		Count:   1,
		Preview: false,
	}
	out, err := rmConverter(resp)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out != "removed 'build/'" {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestRmConverterPreview(t *testing.T) {
	resp := domain.FileSystemRmResp{
		Removed: []string{"a", "b", "c"},
		Count:   3,
		Preview: true,
	}
	out, err := rmConverter(resp)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(out, "3 files would be removed") {
		t.Errorf("expected preview header, got:\n%s", out)
	}
	if !strings.Contains(out, "Run with --confirm to proceed") {
		t.Errorf("expected confirm prompt, got:\n%s", out)
	}
}

func TestRegistryLookup(t *testing.T) {
	c := Lookup(reflect.TypeOf(domain.FileSystemGrepResp{}))
	if c == nil {
		t.Error("expected grep converter to be registered")
	}
	c = Lookup(reflect.TypeOf(domain.FileSystemGlobResp{}))
	if c == nil {
		t.Error("expected glob converter to be registered")
	}
	c = Lookup(reflect.TypeOf(domain.FileSystemReadResp{}))
	if c == nil {
		t.Error("expected read converter to be registered")
	}
	c = Lookup(reflect.TypeOf(domain.FileSystemRmResp{}))
	if c == nil {
		t.Error("expected rm converter to be registered")
	}
	c = Lookup(reflect.TypeOf(domain.WorkspaceLogsQueryResp{}))
	if c == nil {
		t.Error("expected logs query converter to be registered")
	}
}

func TestConvertFallback(t *testing.T) {
	// Unregistered type falls back to JSON.
	type unknown struct{ X int }
	out := Convert(unknown{X: 42})
	if !strings.Contains(out, `"X"`) {
		t.Errorf("expected JSON fallback, got: %q", out)
	}
}

func TestLogsQueryConverter(t *testing.T) {
	resp := domain.WorkspaceLogsQueryResp{
		Items: []domain.WorkspaceLogEntry{
			{Timestamp: "2026-07-11T00:00:00.123Z", Level: "info", Caller: "workspace", Message: "started"},
			{Timestamp: "2026-07-11T00:00:01.456Z", Level: "error", Caller: "agent", Message: "boom"},
		},
	}
	out, err := logsQueryConverter(resp)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(out, "[2026-07-11T00:00:00] INFO workspace: started") {
		t.Errorf("expected formatted log line, got:\n%s", out)
	}
	if !strings.Contains(out, "(2 entries)") {
		t.Errorf("expected entry count, got:\n%s", out)
	}
}

func TestLogsQueryConverterTruncated(t *testing.T) {
	resp := domain.WorkspaceLogsQueryResp{
		Items: []domain.WorkspaceLogEntry{
			{Timestamp: "2026-07-11T00:00:00Z", Level: "info", Caller: "ws", Message: "old"},
		},
		Truncated:  true,
		NextBefore: "2026-07-11T00:00:00Z",
	}
	out, err := logsQueryConverter(resp)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(out, `[truncated: 1 entries shown, more available. Use Before="2026-07-11T00:00:00Z" to continue]`) {
		t.Errorf("expected truncation hint, got:\n%s", out)
	}
}

func TestLogsQueryConverterEmpty(t *testing.T) {
	out, err := logsQueryConverter(domain.WorkspaceLogsQueryResp{})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out != "(no log entries)" {
		t.Errorf("expected empty placeholder, got %q", out)
	}
}
