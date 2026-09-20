package lspserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This file verifies the real gopls references analysis path across Go symbol
// kinds (functions, struct types, methods, fields, interface methods) and the
// includeDeclaration semantics. It drives an actual gopls server, so it is
// slower than the fake-based handler tests and requires a working `go` toolchain.

// refRange mirrors the LSP Range shape returned inside a Location.
type refRange struct {
	Start struct {
		Line      uint32 `json:"line"`
		Character uint32 `json:"character"`
	} `json:"start"`
	End struct {
		Line      uint32 `json:"line"`
		Character uint32 `json:"character"`
	} `json:"end"`
}

// refLocation mirrors a single LSP Location element.
type refLocation struct {
	URI   string   `json:"uri"`
	Range refRange `json:"range"`
}

const sampleSource = `package sample

type Greeter struct {
	Name string
	greet string
}

func (g *Greeter) Greet() string {
	return g.greet + g.Name
}

type Speaker interface {
	Greet() string
}

func NewGreeter() *Greeter {
	return &Greeter{Name: "world", greet: "hi "}
}

func UseIt() string {
	g := NewGreeter()
	return g.Greet()
}

func UseIt2() string {
	g := NewGreeter()
	return g.Greet()
}
`

// lineCharAt returns the 0-based (line, character) of the start of the first
// whole-word occurrence of needle in src (bounded by non-identifier bytes).
func lineCharAt(t *testing.T, src, needle string) (uint32, uint32) {
	t.Helper()
	idx := wholeWordIndex(src, needle)
	if idx < 0 {
		t.Fatalf("needle %q not found as whole word in source", needle)
	}
	line := uint32(strings.Count(src[:idx], "\n"))
	lastNL := strings.LastIndex(src[:idx], "\n")
	col := uint32(idx - lastNL - 1)
	return line, col
}

// lineCharAtN returns the 0-based (line, character) of the n-th (1-based)
// whole-word occurrence of needle in src.
func lineCharAtN(t *testing.T, src, needle string, n int) (uint32, uint32) {
	t.Helper()
	searchFrom := 0
	var idx int
	for i := 0; i < n; i++ {
		j := wholeWordIndexFrom(src, needle, searchFrom)
		if j < 0 {
			t.Fatalf("needle %q: only found %d whole-word occurrences (need %d)", needle, i, n)
		}
		idx = j
		searchFrom = idx + len(needle)
	}
	line := uint32(strings.Count(src[:idx], "\n"))
	lastNL := strings.LastIndex(src[:idx], "\n")
	col := uint32(idx - lastNL - 1)
	return line, col
}

// wholeWordIndex returns the index of the first occurrence of needle whose
// preceding and following bytes are not identifier characters, or -1.
func wholeWordIndex(src, needle string) int {
	return wholeWordIndexFrom(src, needle, 0)
}

// wholeWordIndexFrom is wholeWordIndex searching from byte offset start.
func wholeWordIndexFrom(src, needle string, start int) int {
	searchFrom := start
	for {
		idx := strings.Index(src[searchFrom:], needle)
		if idx < 0 {
			return -1
		}
		abs := searchFrom + idx
		beforeOK := abs == 0 || !isIdentByte(src[abs-1])
		after := abs + len(needle)
		afterOK := after >= len(src) || !isIdentByte(src[after])
		if beforeOK && afterOK {
			return abs
		}
		searchFrom = abs + 1
	}
}

func isIdentByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// newRealServer spins up a real gopls server via GoEngine against a temp
// module rooted at the given directory, initializes it, opens the file, and
// returns the engine plus the file URI and path.
func newRealServer(t *testing.T, rootDir, relFile, source string) (*GoEngine, string) {
	t.Helper()
	filePath := filepath.Join(rootDir, relFile)
	if err := os.WriteFile(filePath, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	rootURI := fileURIRoot(t, rootDir)
	fileURI := fileURIRoot(t, filePath)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	srv, err := NewGoEngine(ctx, &testLogger{}, nil)
	if err != nil {
		// The engine now shells out to a real gopls binary; without one there
		// is nothing to integration-test (CI machines may not carry gopls).
		t.Skipf("gopls unavailable, skipping live references test: %v", err)
	}
	if _, err := srv.Initialize(ctx, rootURI); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := srv.DidOpen(ctx, fileURI, "go", source, 1); err != nil {
		t.Fatalf("didOpen: %v", err)
	}
	return srv, fileURI
}

// fileURIRoot converts an absolute path to a file:// URI, preserving case on
// Windows (uppercase drive letter, matching gopls URIFromPath normalization).
// Produces the canonical form with an authority-less triple slash:
// "file:///C:/..." on Windows, "file:///home/..." on Unix.
func fileURIRoot(t *testing.T, absPath string) string {
	t.Helper()
	abs, err := filepath.Abs(absPath)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	p := filepath.ToSlash(abs)
	if strings.HasPrefix(p, "/") {
		return "file://" + p // "file://" + "/home/x" => "file:///home/x"
	}
	return "file:///" + p // "file:///" + "C:/x" => "file:///C:/x"
}

// refsAt runs References at the given position and unmarshals the Location[].
func refsAt(t *testing.T, srv *GoEngine, fileURI string, line, char uint32, includeDecl bool) []refLocation {
	t.Helper()
	raw, err := srv.References(context.Background(), fileURI, line, char, includeDecl)
	if err != nil {
		t.Fatalf("references: %v", err)
	}
	var locs []refLocation
	if err := json.Unmarshal(raw, &locs); err != nil {
		t.Fatalf("unmarshal Location[]: %v (raw=%s)", err, raw)
	}
	return locs
}

// sameFileCount counts locations whose URI equals the file URI.
func sameFileCount(fileURI string, locs []refLocation) int {
	n := 0
	for _, l := range locs {
		if l.URI == fileURI {
			n++
		}
	}
	return n
}

// startLines returns the set of start lines across all locations in the file.
func startLines(fileURI string, locs []refLocation) map[uint32]bool {
	set := map[uint32]bool{}
	for _, l := range locs {
		if l.URI == fileURI {
			set[l.Range.Start.Line] = true
		}
	}
	return set
}

func writeGoMod(t *testing.T, rootDir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(rootDir, "go.mod"), []byte("module refsample\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
}

// TestRealGoplsReferences verifies that gopls returns the full set of
// reference locations for functions, struct types, methods, fields, and
// interface methods, and that includeDeclaration controls whether the
// declaration is present in the result.
func TestRealGoplsReferences(t *testing.T) {
	rootDir := t.TempDir()
	writeGoMod(t, rootDir)
	srv, fileURI := newRealServer(t, rootDir, "sample.go", sampleSource)
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	t.Run("function", func(t *testing.T) {
		// Query on the NewGreeter declaration.
		line, char := lineCharAt(t, sampleSource, "func NewGreeter()")
		// char should point at the identifier "NewGreeter".
		line, char = lineCharAt(t, sampleSource, "NewGreeter")
		locs := refsAt(t, srv, fileURI, line, char, true)
		// Declaration + 2 call sites (UseIt and UseIt2), all in sample.go.
		if got := sameFileCount(fileURI, locs); got != 3 {
			t.Fatalf("function NewGreeter: got %d locations in file, want 3 (decl + 2 calls): %+v", got, locs)
		}
	})

	t.Run("struct type", func(t *testing.T) {
		line, char := lineCharAt(t, sampleSource, "type Greeter struct")
		line, char = lineCharAt(t, sampleSource, "Greeter")
		locs := refsAt(t, srv, fileURI, line, char, true)
		// Declaration, method receiver *Greeter, return type *Greeter (2), composite literal &Greeter{...}.
		if got := sameFileCount(fileURI, locs); got < 4 {
			t.Fatalf("struct Greeter: got %d locations in file, want >= 4 (decl + receiver + returns + literal): %+v", got, locs)
		}
	})

	t.Run("method", func(t *testing.T) {
		// Query on the Greet method declaration. First whole-word "Greet" is
		// the method name in "func (g *Greeter) Greet() string".
		line, char := lineCharAtN(t, sampleSource, "Greet", 1)
		locs := refsAt(t, srv, fileURI, line, char, true)
		// Method decl + 2 call sites (g.Greet() in UseIt and UseIt2).
		if got := sameFileCount(fileURI, locs); got != 3 {
			t.Fatalf("method Greet: got %d locations in file, want 3 (decl + 2 calls): %+v", got, locs)
		}
	})

	t.Run("field", func(t *testing.T) {
		line, char := lineCharAt(t, sampleSource, "func (g *Greeter) Greet() string {")
		line, char = lineCharAt(t, sampleSource, "g.greet")
		locs := refsAt(t, srv, fileURI, line, char, true)
		// Field decl "greet string" + literal "greet: \"hi \"" + read "g.greet".
		if got := sameFileCount(fileURI, locs); got != 3 {
			t.Fatalf("field greet: got %d locations in file, want 3 (decl + literal + read): %+v", got, locs)
		}
	})

	t.Run("interface method", func(t *testing.T) {
		// Query on the interface's Greet declaration. Whole-word "Greet"
		// occurrences: 1=method decl, 2=interface method, 3-4=call sites.
		line, char := lineCharAtN(t, sampleSource, "Greet", 2)
		locs := refsAt(t, srv, fileURI, line, char, true)
		// The interface method Greet is a distinct declaration. gopls reports
		// the interface method and its implementations; at least the interface
		// declaration itself must be present.
		if sameFileCount(fileURI, locs) < 1 {
			t.Fatalf("interface method Greet: got no locations: %+v", locs)
		}
	})

	t.Run("includeDeclaration", func(t *testing.T) {
		line, char := lineCharAt(t, sampleSource, "func NewGreeter()")
		line, char = lineCharAt(t, sampleSource, "NewGreeter")
		withDecl := refsAt(t, srv, fileURI, line, char, true)
		withoutDecl := refsAt(t, srv, fileURI, line, char, false)

		declLine, _ := lineCharAt(t, sampleSource, "func NewGreeter()")
		declLine, _ = lineCharAt(t, sampleSource, "NewGreeter")
		withLines := startLines(fileURI, withDecl)
		withoutLines := startLines(fileURI, withoutDecl)

		if !withLines[declLine] {
			t.Errorf("includeDeclaration=true: declaration line %d missing: %+v", declLine, withLines)
		}
		if withoutLines[declLine] {
			t.Errorf("includeDeclaration=false: declaration line %d should be excluded: %+v", declLine, withoutLines)
		}
		// Both must still find the 2 call sites.
		if sameFileCount(fileURI, withoutDecl) != 2 {
			t.Errorf("includeDeclaration=false: got %d locations, want 2 call sites: %+v", sameFileCount(fileURI, withoutDecl), withoutDecl)
		}
	})
}

func TestRealGoplsReferences_LocationShape(t *testing.T) {
	rootDir := t.TempDir()
	writeGoMod(t, rootDir)
	srv, fileURI := newRealServer(t, rootDir, "sample.go", sampleSource)
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	line, char := lineCharAt(t, sampleSource, "NewGreeter")
	raw, err := srv.References(context.Background(), fileURI, line, char, true)
	if err != nil {
		t.Fatalf("references: %v", err)
	}
	// Every element must be a valid LSP Location: {uri, range{start,end}}.
	var locs []map[string]any
	if err := json.Unmarshal(raw, &locs); err != nil {
		t.Fatalf("unmarshal Location[]: %v", err)
	}
	for i, loc := range locs {
		uri, ok := loc["uri"].(string)
		if !ok || uri == "" {
			t.Errorf("Location[%d].uri missing or empty: %v", i, loc)
		}
		rng, ok := loc["range"].(map[string]any)
		if !ok {
			t.Errorf("Location[%d].range missing: %v", i, loc)
			continue
		}
		for _, key := range []string{"start", "end"} {
			pos, ok := rng[key].(map[string]any)
			if !ok {
				t.Errorf("Location[%d].range.%s missing: %v", i, key, loc)
				continue
			}
			if _, ok := pos["line"]; !ok {
				t.Errorf("Location[%d].range.%s.line missing", i, key)
			}
			if _, ok := pos["character"]; !ok {
				t.Errorf("Location[%d].range.%s.character missing", i, key)
			}
		}
	}
}

// TestRealGoplsReferences_NoDeclaration_Count guards against a common bug
// where includeDeclaration=false accidentally drops the call sites too.
func TestRealGoplsReferences_NoDeclaration_Count(t *testing.T) {
	rootDir := t.TempDir()
	writeGoMod(t, rootDir)
	srv, fileURI := newRealServer(t, rootDir, "sample.go", sampleSource)
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	// Query at the first call site of NewGreeter.
	call1 := "g := NewGreeter()"
	idx := strings.Index(sampleSource, call1)
	if idx < 0 {
		t.Fatal("call site not found")
	}
	// Position on the identifier within the call.
	identStart := idx + strings.Index(call1, "NewGreeter")
	line := uint32(strings.Count(sampleSource[:identStart], "\n"))
	lastNL := strings.LastIndex(sampleSource[:identStart], "\n")
	char := uint32(identStart - lastNL - 1)

	withoutDecl := refsAt(t, srv, fileURI, line, char, false)
	if sameFileCount(fileURI, withoutDecl) != 2 {
		t.Errorf("includeDeclaration=false at call site: got %d locations, want 2 call sites: %+v", sameFileCount(fileURI, withoutDecl), withoutDecl)
	}
}
