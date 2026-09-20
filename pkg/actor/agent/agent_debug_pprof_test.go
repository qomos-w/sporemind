package agent

import (
	"strings"
	"testing"
)

// sampleGoroutineProfile is representative debug=1 text output from
// pprof.Lookup("goroutine").WriteTo(w, 1).
const sampleGoroutineProfile = `goroutine profile: total 15
10 @ 0x100
#	0x100	runtime.gopark+0x50
#	0x200	github.com/qomos-w/sporemind/pkg/actor/agent.(*Actor).handleChatSubmit+0x180
#	0x300	github.com/qomos-w/sporemind/pkg/actor/agent.(*Actor).handleMessage+0x60

3 @ 0x400
#	0x100	runtime.gopark+0x50
#	0x400	github.com/qomos-w/sporemind/pkg/runtime.(*Runtime).Start+0x200

2 @ 0x500
#	0x100	runtime.gopark+0x50
#	0x500	github.com/qomos-w/sporemind/pkg/actor/shell.(*Actor).readPump+0x120
`

// sampleHeapProfile is representative debug=1 text output from
// pprof.Lookup("heap").WriteTo(w, 1).
const sampleHeapProfile = `heap profile: 1000: 51200 [1000: 51200] @ heap/1
500: 40960 [500: 40960] @ 0x100
#	0x100	runtime.mallocgc+0x50
#	0x200	github.com/qomos-w/sporemind/pkg/domain.MarshalMessage+0x80

300: 8192 [300: 8192] @ 0x300
#	0x100	runtime.mallocgc+0x50
#	0x300	bytes.(*Buffer).grow+0x100

200: 2048 [200: 2048] @ 0x500
#	0x100	runtime.mallocgc+0x50
#	0x500	net/http.(*Client).do+0x200
`

func TestParseProfileEntries_Goroutine(t *testing.T) {
	header, entries := parseProfileEntries(sampleGoroutineProfile)

	if !strings.Contains(header, "goroutine profile") {
		t.Errorf("header = %q, want to contain 'goroutine profile'", header)
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}

	// First entry: 10 goroutines, 3 frames
	if entries[0].count != 10 {
		t.Errorf("entries[0].count = %d, want 10", entries[0].count)
	}
	if entries[0].bytes != 0 {
		t.Errorf("entries[0].bytes = %d, want 0", entries[0].bytes)
	}
	if len(entries[0].frames) != 3 {
		t.Errorf("entries[0].frames = %d, want 3", len(entries[0].frames))
	}
	if !strings.Contains(entries[0].frames[0], "runtime.gopark") {
		t.Errorf("entries[0].frames[0] = %q, want runtime.gopark", entries[0].frames[0])
	}

	// Second entry: 3 goroutines
	if entries[1].count != 3 {
		t.Errorf("entries[1].count = %d, want 3", entries[1].count)
	}

	// Third entry: 2 goroutines
	if entries[2].count != 2 {
		t.Errorf("entries[2].count = %d, want 2", entries[2].count)
	}

	// Total
	if total := totalEntryCount(entries); total != 15 {
		t.Errorf("totalEntryCount = %d, want 15", total)
	}
}

func TestParseProfileEntries_Heap(t *testing.T) {
	header, entries := parseProfileEntries(sampleHeapProfile)

	if !strings.Contains(header, "heap profile") {
		t.Errorf("header = %q, want to contain 'heap profile'", header)
	}
	if len(entries) != 3 {
		t.Fatalf("len(entries) = %d, want 3", len(entries))
	}

	// First entry: 500 objects, 40960 bytes
	if entries[0].count != 500 {
		t.Errorf("entries[0].count = %d, want 500", entries[0].count)
	}
	if entries[0].bytes != 40960 {
		t.Errorf("entries[0].bytes = %d, want 40960", entries[0].bytes)
	}

	// Second entry: 300 objects, 8192 bytes
	if entries[1].count != 300 {
		t.Errorf("entries[1].count = %d, want 300", entries[1].count)
	}
	if entries[1].bytes != 8192 {
		t.Errorf("entries[1].bytes = %d, want 8192", entries[1].bytes)
	}
}

func TestParseProfileEntries_Empty(t *testing.T) {
	header, entries := parseProfileEntries("")
	if header != "" {
		t.Errorf("header = %q, want empty", header)
	}
	if len(entries) != 0 {
		t.Errorf("len(entries) = %d, want 0", len(entries))
	}
}

func TestParseProfileEntries_UnsymbolizedFrames(t *testing.T) {
	text := `goroutine profile: total 1
1 @ 0x100
#	0x100	0x100

1 @ 0x200
#	0x200	mains.foo+0x10
`
	_, entries := parseProfileEntries(text)
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	// Uns symbolized frame (just an address) should be excluded.
	if len(entries[0].frames) != 0 {
		t.Errorf("entries[0].frames = %v, want empty (unsymbolized)", entries[0].frames)
	}
	if len(entries[1].frames) != 1 {
		t.Errorf("entries[1].frames = %v, want 1", entries[1].frames)
	}
}

func TestFormatSummary(t *testing.T) {
	_, entries := parseProfileEntries(sampleGoroutineProfile)
	text := formatSummary("goroutine profile: total 15", entries, 2)

	if !strings.Contains(text, "total 15") {
		t.Errorf("missing total: %q", text)
	}
	if !strings.Contains(text, "3 unique stacks") {
		t.Errorf("missing unique stacks: %q", text)
	}
	if !strings.Contains(text, "showing top 2") {
		t.Errorf("missing top N: %q", text)
	}
	if !strings.Contains(text, "[1] 10") {
		t.Errorf("missing first entry: %q", text)
	}
	if !strings.Contains(text, "handleChatSubmit") {
		t.Errorf("missing function name: %q", text)
	}
	if !strings.Contains(text, "+1 more stacks") {
		t.Errorf("missing 'more' hint: %q", text)
	}
}

func TestFormatSummary_TopNCoversAll(t *testing.T) {
	_, entries := parseProfileEntries(sampleGoroutineProfile)
	text := formatSummary("goroutine profile: total 15", entries, 10)
	if strings.Contains(text, "more stacks") {
		t.Errorf("should not have 'more' hint when topN >= entries: %q", text)
	}
}

func TestDiffProfileEntries(t *testing.T) {
	before := []profileEntry{
		{count: 10, frames: []string{"runtime.gopark", "main.foo"}},
		{count: 5, frames: []string{"runtime.gopark", "main.bar"}},
		{count: 3, frames: []string{"runtime.gopark", "main.stable"}},
	}
	after := []profileEntry{
		{count: 10, frames: []string{"runtime.gopark", "main.foo"}},
		{count: 8, frames: []string{"runtime.gopark", "main.bar"}},
		{count: 3, frames: []string{"runtime.gopark", "main.stable"}},
		{count: 2, frames: []string{"runtime.gopark", "main.leak"}},
	}

	added, grown, shrunk := diffProfileEntries(before, after)

	if len(added) != 1 {
		t.Fatalf("len(added) = %d, want 1", len(added))
	}
	if added[0].count != 2 {
		t.Errorf("added[0].count = %d, want 2", added[0].count)
	}
	if !strings.Contains(strings.Join(added[0].frames, "\n"), "main.leak") {
		t.Errorf("added[0] frames = %v, want to contain main.leak", added[0].frames)
	}

	if len(grown) != 1 {
		t.Fatalf("len(grown) = %d, want 1", len(grown))
	}
	if grown[0].count != 3 { // 8 - 5 = 3
		t.Errorf("grown[0].count (delta) = %d, want 3", grown[0].count)
	}

	if len(shrunk) != 0 {
		t.Errorf("len(shrunk) = %d, want 0", len(shrunk))
	}
}

func TestDiffProfileEntries_Shrinking(t *testing.T) {
	before := []profileEntry{
		{count: 10, frames: []string{"main.foo"}},
	}
	after := []profileEntry{
		{count: 4, frames: []string{"main.foo"}},
	}
	added, grown, shrunk := diffProfileEntries(before, after)
	if len(added) != 0 || len(grown) != 0 {
		t.Fatalf("added=%d grown=%d, want 0/0", len(added), len(grown))
	}
	if len(shrunk) != 1 {
		t.Fatalf("len(shrunk) = %d, want 1", len(shrunk))
	}
	if shrunk[0].count != 6 { // 10 - 4 = 6
		t.Errorf("shrunk[0].count = %d, want 6", shrunk[0].count)
	}
}

func TestDiffProfileEntries_NoChanges(t *testing.T) {
	before := []profileEntry{
		{count: 5, frames: []string{"main.foo"}},
	}
	after := []profileEntry{
		{count: 5, frames: []string{"main.foo"}},
	}
	added, grown, shrunk := diffProfileEntries(before, after)
	if len(added)+len(grown)+len(shrunk) != 0 {
		t.Errorf("expected no changes, got added=%d grown=%d shrunk=%d", len(added), len(grown), len(shrunk))
	}
}

func TestFormatDiff_NoChanges(t *testing.T) {
	entries := []profileEntry{{count: 5, frames: []string{"main.foo"}}}
	added, grown, shrunk := diffProfileEntries(entries, entries)
	text := formatDiff("goroutine", entries, entries, added, grown, shrunk, 20)
	if !strings.Contains(text, "No changes") {
		t.Errorf("expected 'No changes': %q", text)
	}
}

func TestFormatDiff_WithChanges(t *testing.T) {
	before := []profileEntry{
		{count: 5, frames: []string{"main.foo"}},
	}
	after := []profileEntry{
		{count: 8, frames: []string{"main.foo"}},
		{count: 2, frames: []string{"main.leak"}},
	}
	added, grown, shrunk := diffProfileEntries(before, after)
	text := formatDiff("goroutine", before, after, added, grown, shrunk, 20)

	if !strings.Contains(text, "before=5") || !strings.Contains(text, "after=10") {
		t.Errorf("missing totals: %q", text)
	}
	if !strings.Contains(text, "+5") { // delta
		t.Errorf("missing delta: %q", text)
	}
	if !strings.Contains(text, "NEW") {
		t.Errorf("missing NEW section: %q", text)
	}
	if !strings.Contains(text, "GROWING") {
		t.Errorf("missing GROWING section: %q", text)
	}
	if !strings.Contains(text, "main.leak") {
		t.Errorf("missing leak function: %q", text)
	}
}

func TestProfileEntryKey(t *testing.T) {
	e1 := profileEntry{count: 5, frames: []string{"main.foo", "main.bar"}}
	e2 := profileEntry{count: 10, frames: []string{"main.foo", "main.bar"}}
	e3 := profileEntry{count: 5, frames: []string{"main.foo", "main.baz"}}

	if e1.key() != e2.key() {
		t.Error("same frames should produce same key regardless of count")
	}
	if e1.key() == e3.key() {
		t.Error("different frames should produce different keys")
	}
}

func TestProfileEntryLabel(t *testing.T) {
	e := profileEntry{count: 5, bytes: 0}
	if got := e.label(); got != "5" {
		t.Errorf("label() = %q, want '5'", got)
	}
	e2 := profileEntry{count: 500, bytes: 40960}
	if got := e2.label(); !strings.Contains(got, "500") || !strings.Contains(got, "KB") {
		t.Errorf("label() = %q, want '500 objs / ...KB'", got)
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{0, "0B"},
		{512, "512B"},
		{1024, "1.0KB"},
		{1048576, "1.0MB"},
		{1073741824, "1.0GB"},
	}
	for _, tt := range tests {
		if got := formatBytes(tt.in); got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestIsEntryLine(t *testing.T) {
	valid := []string{
		"10 @ 0x1234",
		"100: 1024 @ 0x5678",
		"5: 2048 [heap] @ 0x9a",
	}
	for _, l := range valid {
		if !isEntryLine(l) {
			t.Errorf("isEntryLine(%q) = false, want true", l)
		}
	}
	invalid := []string{
		"",
		"goroutine profile: total 10",
		"#\t0x100\tmain.foo",
		"	",
		"@ 0x123",
	}
	for _, l := range invalid {
		if isEntryLine(l) {
			t.Errorf("isEntryLine(%q) = true, want false", l)
		}
	}
}

func TestParseEntryLine(t *testing.T) {
	// Goroutine format
	e := parseEntryLine("10 @ 0x1234")
	if e.count != 10 || e.bytes != 0 {
		t.Errorf("parseEntryLine goroutine: count=%d bytes=%d, want 10/0", e.count, e.bytes)
	}

	// Heap format
	e = parseEntryLine("500: 40960 [heap] @ 0x100")
	if e.count != 500 || e.bytes != 40960 {
		t.Errorf("parseEntryLine heap: count=%d bytes=%d, want 500/40960", e.count, e.bytes)
	}

	// Heap format without bracket
	e = parseEntryLine("300: 8192 @ 0x300")
	if e.count != 300 || e.bytes != 8192 {
		t.Errorf("parseEntryLine heap-nobracket: count=%d bytes=%d, want 300/8192", e.count, e.bytes)
	}
}

func TestExtractFunctionName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"#\t0x100\tmain.foo+0x50", "main.foo+0x50"},
		{"#\t0x100\truntime.gopark+0x50", "runtime.gopark+0x50"},
		{"#\t0x100\t0x100", ""}, // unsymbolized
		{"#\t0x100", ""},        // too few fields
		{"main.foo", ""},        // not a frame line
	}
	for _, tt := range tests {
		if got := extractFunctionName(tt.in); got != tt.want {
			t.Errorf("extractFunctionName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMinInt(t *testing.T) {
	if minInt(3, 7) != 3 {
		t.Error("minInt(3,7) != 3")
	}
	if minInt(10, 2) != 2 {
		t.Error("minInt(10,2) != 2")
	}
}
