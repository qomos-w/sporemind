package persist

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// appendLinePattern matches the fixed-width line format used by the
// AppenderConcurrentNoInterleave contract test: "writer-NN-line-NNN".
var appendLinePattern = regexp.MustCompile(`^writer-\d{2}-line-\d{3}$`)

// RunContractTests runs the persist backend contract suite against a Persist
// instance created by factory. Every backend (filesystem, MongoDB, Postgres,
// etc.) must pass this suite; it verifies the semantic contract documented on
// the Persist interface.
//
// factory must return a fresh, empty Persist rooted at a temporary location
// (typically t.TempDir()). Each subtest uses a unique name so they never
// collide. The suite is safe to run in parallel with other test functions
// because each subtest operates on its own isolated Persist instance.
//
// Backend-specific implementation details that cannot be verified through the
// Persist interface alone (e.g. ".tmp file cleanup for filesystem backends",
// "corrupt-state backup behaviour") are covered by companion tests in the
// backend's own test file, not by this suite.
func RunContractTests(t *testing.T, factory func(t *testing.T) Persist) {
	t.Helper()

	// --- Save / Load roundtrip ---

	t.Run("SaveLoadRoundtrip", func(t *testing.T) {
		p := factory(t)
		type payload struct {
			Value string `json:"value"`
			Count int    `json:"count"`
		}
		want := payload{Value: "hello", Count: 42}
		if err := p.Save("roundtrip", want); err != nil {
			t.Fatalf("Save: %v", err)
		}
		var got payload
		if err := p.Load("roundtrip", &got); err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got != want {
			t.Errorf("roundtrip mismatch: got %+v, want %+v", got, want)
		}
	})

	// --- Save overwrites previous value ---

	t.Run("SaveOverwrites", func(t *testing.T) {
		p := factory(t)
		type payload struct{ V int }
		if err := p.Save("overwrite", payload{V: 1}); err != nil {
			t.Fatalf("first Save: %v", err)
		}
		if err := p.Save("overwrite", payload{V: 2}); err != nil {
			t.Fatalf("second Save: %v", err)
		}
		var got payload
		if err := p.Load("overwrite", &got); err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got.V != 2 {
			t.Errorf("expected overwritten value 2, got %d", got.V)
		}
	})

	// --- Load missing → ErrNotExist (distinguishable from IO errors) ---

	t.Run("LoadMissing", func(t *testing.T) {
		p := factory(t)
		var got string
		err := p.Load("nonexistent", &got)
		if !errors.Is(err, ErrNotExist) {
			t.Errorf("missing name must return ErrNotExist (errors.Is), got %v", err)
		}
	})

	// --- Delete idempotent ---

	t.Run("DeleteIdempotent", func(t *testing.T) {
		p := factory(t)
		// Deleting a name that was never saved must succeed.
		if err := p.Delete("never-existed"); err != nil {
			t.Errorf("Delete of missing name must be idempotent (nil), got %v", err)
		}
		// Save something, delete it, then delete again.
		if err := p.Save("to-delete", map[string]string{"k": "v"}); err != nil {
			t.Fatal(err)
		}
		if err := p.Delete("to-delete"); err != nil {
			t.Fatalf("first Delete: %v", err)
		}
		if err := p.Delete("to-delete"); err != nil {
			t.Errorf("second Delete must be idempotent (nil), got %v", err)
		}
	})

	// --- Delete then Load → ErrNotExist ---

	t.Run("DeleteThenLoadMissing", func(t *testing.T) {
		p := factory(t)
		if err := p.Save("gone", map[string]int{"v": 1}); err != nil {
			t.Fatal(err)
		}
		if err := p.Delete("gone"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		var got map[string]int
		err := p.Load("gone", &got)
		if !errors.Is(err, ErrNotExist) {
			t.Errorf("Load after Delete must return ErrNotExist, got %v", err)
		}
	})

	// --- Concurrent Save+Load: no torn reads ---

	t.Run("ConcurrentSaveLoad", func(t *testing.T) {
		p := factory(t)
		type payload struct{ V int }

		// Seed so Load always has something to read.
		if err := p.Save("conc", payload{V: 0}); err != nil {
			t.Fatal(err)
		}

		const writers = 8
		const readers = 8
		const iters = 50
		var wg sync.WaitGroup
		errCh := make(chan error, writers+readers)

		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func(v int) {
				defer wg.Done()
				for j := 0; j < iters; j++ {
					if err := p.Save("conc", payload{V: v}); err != nil {
						errCh <- fmt.Errorf("save %d.%d: %w", v, j, err)
						return
					}
				}
			}(i)
		}

		for i := 0; i < readers; i++ {
			wg.Add(1)
			go func(r int) {
				defer wg.Done()
				for j := 0; j < iters; j++ {
					var got payload
					// No concurrent Delete in this test, so Load must
					// never error: a decode failure means a torn read.
					if err := p.Load("conc", &got); err != nil {
						errCh <- fmt.Errorf("load %d.%d: %w", r, j, err)
						return
					}
				}
			}(i)
		}

		wg.Wait()
		close(errCh)
		for err := range errCh {
			t.Error(err)
		}

		// Final state must be a valid, decodable value from one of the writers.
		var final payload
		if err := p.Load("conc", &final); err != nil {
			t.Fatalf("final Load: %v", err)
		}
	})

	// --- Name rejects traversal / absolute paths ---

	t.Run("NameRejectsTraversal", func(t *testing.T) {
		p := factory(t)
		bad := []string{
			"",              // empty
			".",             // base directory itself
			"..",            // parent dir
			"../escape",     // traversal
			"foo/../../bar", // traversal via components
			"/absolute",     // POSIX absolute / Windows root-relative
			`\..\bar`,       // backslash traversal (Windows-style)
			`foo\..\bar`,    // backslash traversal via components
		}
		for _, name := range bad {
			if err := p.Save(name, "x"); err == nil {
				t.Errorf("Save(%q) should reject invalid name", name)
			}
			var v string
			if err := p.Load(name, &v); err == nil {
				t.Errorf("Load(%q) should reject invalid name", name)
			}
			if err := p.Delete(name); err == nil {
				t.Errorf("Delete(%q) should reject invalid name", name)
			}
		}
	})

	// --- Hierarchical names with forward-slash separators are accepted ---

	t.Run("NameAcceptsHierarchical", func(t *testing.T) {
		p := factory(t)
		type payload struct{ V string }
		if err := p.Save("session/node-1", payload{V: "x"}); err != nil {
			t.Fatalf("Save: %v", err)
		}
		var got payload
		if err := p.Load("session/node-1", &got); err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got.V != "x" {
			t.Errorf("hierarchical name roundtrip: got %q, want %q", got.V, "x")
		}
		if err := p.Delete("session/node-1"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		var v payload
		if err := p.Load("session/node-1", &v); !errors.Is(err, ErrNotExist) {
			t.Errorf("Load after Delete of hierarchical name: got %v, want ErrNotExist", err)
		}
	})

	// --- Optional Appender capability: concurrent Append, no interleave ---

	t.Run("AppenderConcurrentNoInterleave", func(t *testing.T) {
		p := factory(t)
		ap, ok := p.(Appender)
		if !ok {
			t.Skip("backend does not implement Appender")
		}
		bp, hasBP := p.(BasePather)
		if !hasBP {
			t.Skip("backend does not implement BasePather (cannot verify readback)")
		}

		name := "append-conc"
		const writers = 8
		const linesPerWriter = 50
		var wg sync.WaitGroup
		errCh := make(chan error, writers)
		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for j := 0; j < linesPerWriter; j++ {
					line := fmt.Sprintf("writer-%02d-line-%03d\n", w, j)
					if err := ap.Append(name, []byte(line)); err != nil {
						errCh <- fmt.Errorf("append %d.%d: %w", w, j, err)
						return
					}
				}
			}(i)
		}
		wg.Wait()
		close(errCh)
		for err := range errCh {
			t.Error(err)
		}

		// Read back and verify all lines are present and complete (no torn writes).
		path := filepath.Join(bp.BasePath(), filepath.Clean(filepath.FromSlash(name)))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("readback: %v", err)
		}
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		expected := writers * linesPerWriter
		if len(lines) != expected {
			t.Errorf("line count: got %d, want %d", len(lines), expected)
		}
		seen := make(map[string]bool, expected)
		for _, line := range lines {
			if !appendLinePattern.MatchString(line) {
				t.Errorf("torn or malformed line: %q", line)
				continue
			}
			seen[line] = true
		}
		for w := 0; w < writers; w++ {
			for j := 0; j < linesPerWriter; j++ {
				key := fmt.Sprintf("writer-%02d-line-%03d", w, j)
				if !seen[key] {
					t.Errorf("missing line: %s", key)
				}
			}
		}
	})

	// --- Optional Appender capability: Append vs WriteFileAtomic same-path mutex ---

	t.Run("AppenderWriteFileAtomicMutex", func(t *testing.T) {
		p := factory(t)
		ap, ok := p.(Appender)
		if !ok {
			t.Skip("backend does not implement Appender")
		}
		bp, hasBP := p.(BasePather)
		if !hasBP {
			t.Skip("backend does not implement BasePather")
		}

		name := "append-mutex"
		// Derive the physical path the same way Append does so both
		// operations target the same file.
		path := filepath.Join(bp.BasePath(), filepath.Clean(filepath.FromSlash(name)))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		const iters = 100
		var wg sync.WaitGroup
		errCh := make(chan error, 2)

		// Writer 1: WriteFileAtomic on the same physical path.
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				if err := WriteFileAtomic(path, []byte(fmt.Sprintf(`{"v":%d}`, i)), 0644); err != nil {
					errCh <- fmt.Errorf("writefileatomic %d: %w", i, err)
					return
				}
			}
		}()

		// Writer 2: Append on the same name (same physical path).
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				if err := ap.Append(name, []byte(fmt.Sprintf("append-%d\n", i))); err != nil {
					errCh <- fmt.Errorf("append %d: %w", i, err)
					return
				}
			}
		}()

		wg.Wait()
		close(errCh)
		for err := range errCh {
			t.Error(err)
		}

		// The per-path lock must have serialized the operations: no call
		// should have failed with a sharing violation or torn write.
	})
}
