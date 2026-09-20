package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/config"
)

// TestLoadStepFilesParallelPreservesSeqOrder verifies the parallel step-file
// loader: every step from every JSONL file is returned, sorted by Seq, even
// when filename order does not match Seq order (the pre-parallelism contract
// relied on the caller sorting; loadStepFiles must keep returning sorted
// steps because rebuildSteps depends on it).
func TestLoadStepFilesParallelPreservesSeqOrder(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	a := &Actor{actorID: "parallel-steps"}
	dir := a.stepsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	// Two files whose names sort opposite to their Seq values.
	write := func(name string, seqs ...int64) {
		var b strings.Builder
		for _, seq := range seqs {
			fmt.Fprintf(&b, `{"ID":"s-%d","TurnID":"t1","Seq":%d,"Closed":true}`+"\n", seq, seq)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("z-late.jsonl", 500, 501)
	write("a-early.jsonl", 1, 2)
	// Enough files to exercise the bounded worker pool past one batch.
	for i := 0; i < 20; i++ {
		write(fmt.Sprintf("m-%02d.jsonl", i), int64(100+i))
	}

	steps := a.loadStepFiles()
	if len(steps) != 24 {
		t.Fatalf("steps = %d, want 24", len(steps))
	}
	for i := 1; i < len(steps); i++ {
		if steps[i-1].Seq >= steps[i].Seq {
			t.Fatalf("steps not sorted by Seq: [%d]=%d >= [%d]=%d", i-1, steps[i-1].Seq, i, steps[i].Seq)
		}
	}
}

// TestLoadTurnsParallelPreservesIndexOrder verifies the parallel turn loader
// keeps index.json order (the timeline order contract) and seeds turnSync so
// the first saveTurns does not rewrite unchanged turns.
func TestLoadTurnsParallelPreservesIndexOrder(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	a := &Actor{actorID: "parallel-turns"}
	dir := a.turnsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	// Index order deliberately differs from lexicographic ID order.
	order := []string{"t-middle", "t-first", "t-last"}
	var b strings.Builder
	b.WriteString(`{"TurnIDs":["t-middle","t-first","t-last"],"ActiveHead":2}`)
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}
	for i, id := range order {
		data := fmt.Sprintf(`{"ID":%q,"Role":"assistant","Seq":%d}`, id, i)
		if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}

	a.loadTurns()
	if len(a.Session.Turns) != 3 {
		t.Fatalf("turns = %d, want 3", len(a.Session.Turns))
	}
	for i, want := range order {
		if a.Session.Turns[i].ID != want {
			t.Fatalf("turn[%d].ID = %q, want %q (index order must be preserved)", i, a.Session.Turns[i].ID, want)
		}
	}
	if a.Session.ActiveHead != 2 {
		t.Fatalf("ActiveHead = %d, want 2", a.Session.ActiveHead)
	}
	if len(a.turnSync) != 3 {
		t.Fatalf("turnSync entries = %d, want 3", len(a.turnSync))
	}
	// A missing turn file must not break the load of its neighbours.
	if err := os.Remove(filepath.Join(dir, "t-last.json")); err != nil {
		t.Fatal(err)
	}
	a.loadTurns()
	if len(a.Session.Turns) != 2 || a.Session.Turns[1].ID != "t-first" {
		t.Fatalf("missing turn file not tolerated: %+v", a.Session.Turns)
	}
}
