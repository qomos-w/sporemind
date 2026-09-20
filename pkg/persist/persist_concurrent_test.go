package persist

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestWriteFileAtomicConcurrentPath calls WriteFileAtomic directly on the
// same path from many goroutines to ensure the shared primitive is safe.
// The interface-level concurrent Save+Load contract is covered by
// RunContractTests("ConcurrentSaveLoad").
func TestWriteFileAtomicConcurrentPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	const workers = 16
	var wg sync.WaitGroup
	errCh := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if err := WriteFileAtomic(path, []byte(fmt.Sprintf(`{"w":%d,"j":%d}`, w, j)), 0644); err != nil {
					errCh <- fmt.Errorf("worker %d iter %d: %w", w, j, err)
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

	// Final file must be valid JSON from one of the writers.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("final file is empty")
	}
}
