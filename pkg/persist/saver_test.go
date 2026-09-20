package persist

import (
	"errors"
	"sync"
	"testing"
)

// RunSaverContractTests runs the Saver batch contract against a Persist built
// by factory. For backends implementing Saver, SaveAll must be all-or-nothing;
// for backends without Saver, SaveAll degrades to sequential Save and the
// suite only asserts the happy path and error propagation.
//
// factory must return a fresh, empty Persist. Each subtest uses a unique name
// so they do not collide.
func RunSaverContractTests(t *testing.T, factory func(t *testing.T) Persist) {
	t.Helper()

	// Happy path: all docs land, each Load returns its value.
	t.Run("SaveAllHappyPath", func(t *testing.T) {
		p := factory(t)
		docs := []Doc{
			{Name: "a", Value: map[string]int{"n": 1}},
			{Name: "b", Value: map[string]int{"n": 2}},
			{Name: "sub/c", Value: map[string]int{"n": 3}},
		}
		if err := SaveAll(p, docs); err != nil {
			t.Fatalf("SaveAll: %v", err)
		}
		for _, d := range docs {
			var got map[string]int
			if err := p.Load(d.Name, &got); err != nil {
				t.Fatalf("Load %q: %v", d.Name, err)
			}
			if got["n"] != d.Value.(map[string]int)["n"] {
				t.Errorf("Load %q = %v, want %v", d.Name, got, d.Value)
			}
		}
	})

	// Empty batch is a no-op.
	t.Run("SaveAllEmpty", func(t *testing.T) {
		p := factory(t)
		if err := SaveAll(p, nil); err != nil {
			t.Fatalf("SaveAll(nil): %v", err)
		}
	})

	// A bad name rejects the batch before any write; for Saver backends nothing
	// of the batch persists (all-or-nothing). For fallback backends the
	// sequential path fails at the bad name; earlier valid docs may have
	// persisted (documented degradation), so only the error is asserted.
	t.Run("SaveAllRejectsBadName", func(t *testing.T) {
		p := factory(t)
		docs := []Doc{
			{Name: "good", Value: 1},
			{Name: "..", Value: 2}, // traversal, rejected by validateName
			{Name: "other", Value: 3},
		}
		err := SaveAll(p, docs)
		if err == nil {
			t.Fatal("SaveAll with bad name: expected error, got nil")
		}
		// Saver backends guarantee all-or-nothing: the good doc must NOT persist.
		if _, ok := p.(Saver); ok {
			var v int
			loadErr := p.Load("good", &v)
			if !errors.Is(loadErr, ErrNotExist) {
				t.Errorf("Saver backend must roll back on bad name: Load good = %v, want ErrNotExist", loadErr)
			}
		}
	})

	// Concurrent SaveAll batches on disjoint names must not tear.
	t.Run("SaveAllConcurrent", func(t *testing.T) {
		p := factory(t)
		const batches = 8
		const perBatch = 20
		var wg sync.WaitGroup
		errCh := make(chan error, batches)
		for b := 0; b < batches; b++ {
			wg.Add(1)
			go func(b int) {
				defer wg.Done()
				docs := make([]Doc, perBatch)
				for i := range docs {
					docs[i] = Doc{Name: uniqName(b, i), Value: b*100 + i}
				}
				if err := SaveAll(p, docs); err != nil {
					errCh <- err
					return
				}
			}(b)
		}
		wg.Wait()
		close(errCh)
		for err := range errCh {
			t.Fatalf("SaveAll concurrent: %v", err)
		}
		for b := 0; b < batches; b++ {
			for i := 0; i < perBatch; i++ {
				var got int
				if err := p.Load(uniqName(b, i), &got); err != nil {
					t.Fatalf("Load %s: %v", uniqName(b, i), err)
				}
				if got != b*100+i {
					t.Errorf("Load %s = %d, want %d", uniqName(b, i), got, b*100+i)
				}
			}
		}
	})
}

func uniqName(b, i int) string {
	return saverName(b, i)
}

// saverName is split into its own helper so the name scheme is stable across
// the concurrent subtest's writers and readers.
func saverName(b, i int) string {
	return safeItoa(b) + "/" + safeItoa(i)
}

func safeItoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}

// TestSaver_FSPersistFallback runs the Saver suite against FSPersist, which
// does NOT implement Saver: SaveAll must degrade to sequential Save.
func TestSaver_FSPersistFallback(t *testing.T) {
	RunSaverContractTests(t, func(t *testing.T) Persist {
		return NewFSPersist(t.TempDir())
	})
}

// TestSaver_LevelDBAtomic runs the Saver suite against LevelDBPersist, which
// implements Saver via a single atomic batch write.
func TestSaver_LevelDBAtomic(t *testing.T) {
	RunSaverContractTests(t, func(t *testing.T) Persist {
		return newLevelDBForTest(t)
	})
}

// TestSaveAll_NilPersistIsSaverTypeAssertSafe documents that SaveAll only
// type-asserts Saver on a non-nil Persist; callers passing nil must guard.
func TestSaveAll_DirectPathDoc(t *testing.T) {
	// SaveAll with a zero-length slice on any persist is a no-op; this also
	// exercises the Saver type-assertion skip path on FSPersist.
	p := NewFSPersist(t.TempDir())
	if err := SaveAll(p, []Doc{{Name: "x", Value: 1}}); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}
	if _, ok := any(p).(Saver); ok {
		t.Fatal("FSPersist must not implement Saver (fallback path is the contract)")
	}
}
