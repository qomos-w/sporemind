package persist

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/syndtr/goleveldb/leveldb"
)

func newLevelDBForTest(t *testing.T) *LevelDBPersist {
	t.Helper()
	p, err := newLevelDBPersist(PersistConfig{DataDir: t.TempDir(), Prefix: "test"})
	if err != nil {
		t.Fatalf("newLevelDBPersist: %v", err)
	}
	lp := p.(*LevelDBPersist)
	// Windows: t.TempDir cleanup fails if the DB directory stays locked, so
	// release the handle before the tempdir is removed.
	t.Cleanup(func() { _ = lp.Close() })
	return lp
}

// TestLevelDBContract runs the full T1 backend contract suite against the
// goleveldb backend. The Appender subtests inside the suite skip because this
// backend has no BasePather; their semantics are covered by
// TestLevelDBAppenderRecords below.
func TestLevelDBContract(t *testing.T) {
	RunContractTests(t, func(t *testing.T) Persist {
		return newLevelDBForTest(t)
	})
}

// TestLevelDBRefcountShareAndClose verifies that two instances on the same
// prefix share one DB handle (goleveldb directory locks are exclusive even
// in-process) and that the last Close releases the directory for reopening.
func TestLevelDBRefcountShareAndClose(t *testing.T) {
	dir := t.TempDir()
	p1, err := newLevelDBPersist(PersistConfig{DataDir: dir, Prefix: "a"})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := newLevelDBPersist(PersistConfig{DataDir: dir, Prefix: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if p1.(*LevelDBPersist).h != p2.(*LevelDBPersist).h {
		t.Fatal("same-prefix instances must share one DB handle")
	}
	// A different prefix gets a different handle and a different directory.
	p3, err := newLevelDBPersist(PersistConfig{DataDir: dir, Prefix: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if p3.(*LevelDBPersist).h == p1.(*LevelDBPersist).h {
		t.Fatal("different prefixes must not share a handle")
	}
	_ = p3.(*LevelDBPersist).Close()
	// First Close keeps the shared DB open for the second instance.
	if err := p1.(*LevelDBPersist).Close(); err != nil {
		t.Fatal(err)
	}
	if err := p2.(*LevelDBPersist).Save("alive", "yes"); err != nil {
		t.Fatalf("save after sibling Close must work: %v", err)
	}
	// Last Close releases the directory: a fresh Open must succeed.
	if err := p2.(*LevelDBPersist).Close(); err != nil {
		t.Fatal(err)
	}
	p4, err := newLevelDBPersist(PersistConfig{DataDir: dir, Prefix: "a"})
	if err != nil {
		t.Fatalf("reopen after last Close: %v", err)
	}
	t.Cleanup(func() { _ = p4.(*LevelDBPersist).Close() })
	var got string
	if err := p4.Load("alive", &got); err != nil || got != "yes" {
		t.Fatalf("reopened store lost data: %v %q", err, got)
	}
}

// TestLevelDBDirLockExclusive proves the underlying directory lock is
// exclusive: a second Open bypassing the refcount (a separate process would
// take the same path) fails with a diagnosable error rather than corrupting
// the DB.
func TestLevelDBDirLockExclusive(t *testing.T) {
	p := newLevelDBForTest(t)
	t.Cleanup(func() { _ = p.Close() })
	if _, err := leveldb.OpenFile(p.dir, nil); err == nil {
		t.Fatal("second Open on a locked goleveldb directory must fail")
	}
}

// TestLevelDBCorruptionExplicit verifies that a corrupted DB surfaces an
// explicit error instead of silently returning missing or stale data.
func TestLevelDBCorruptionExplicit(t *testing.T) {
	dir := t.TempDir()
	p, err := newLevelDBPersist(PersistConfig{DataDir: dir, Prefix: "x"})
	if err != nil {
		t.Fatal(err)
	}
	lp := p.(*LevelDBPersist)
	if err := lp.Save("k", "v"); err != nil {
		t.Fatal(err)
	}
	if err := lp.Close(); err != nil {
		t.Fatal(err)
	}
	// Corrupt the MANIFEST pointer: any subsequent open must fail loudly.
	if err := os.WriteFile(filepath.Join(lp.dir, "CURRENT"), []byte("garbage-not-a-manifest\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := newLevelDBPersist(PersistConfig{DataDir: dir, Prefix: "x"}); err == nil {
		t.Fatal("open of corrupted DB must return an explicit error")
	} else if !strings.Contains(err.Error(), "goleveldb open") {
		t.Fatalf("error should be attributable to the backend open: %v", err)
	}
}

// TestLevelDBAppenderRecords is the backend-specific companion to the
// suite's Appender tests (which need a BasePather): concurrent Appends must
// yield every record exactly once, whole and uninterleaved, in per-writer
// order.
func TestLevelDBAppenderRecords(t *testing.T) {
	p := newLevelDBForTest(t)
	t.Cleanup(func() { _ = p.Close() })
	const writers = 8
	const linesPerWriter = 50
	var wg sync.WaitGroup
	errCh := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for j := 0; j < linesPerWriter; j++ {
				if err := p.Append("log/jsonl", []byte(fmt.Sprintf("writer-%02d-line-%03d\n", w, j))); err != nil {
					errCh <- err
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	recs, err := p.ldbRecords("log/jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != writers*linesPerWriter {
		t.Fatalf("record count: got %d, want %d", len(recs), writers*linesPerWriter)
	}
	seen := map[string]bool{}
	for _, r := range recs {
		line := strings.TrimRight(string(r), "\n")
		if !appendLinePattern.MatchString(line) {
			t.Fatalf("torn or malformed record: %q", line)
		}
		seen[line] = true
	}
	for w := 0; w < writers; w++ {
		for j := 0; j < linesPerWriter; j++ {
			key := fmt.Sprintf("writer-%02d-line-%03d", w, j)
			if !seen[key] {
				t.Fatalf("missing record: %s", key)
			}
		}
	}
	// Per-writer order: records of one writer must appear in write order.
	// ldbRecords returns global seq order; verify writer 0's lines ascend.
	prev := -1
	for _, r := range recs {
		line := strings.TrimRight(string(r), "\n")
		if !strings.HasPrefix(line, "writer-00-") {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(line, "writer-00-line-%03d", &n); err != nil {
			t.Fatal(err)
		}
		if n <= prev {
			t.Fatalf("writer-00 records out of order: %d after %d", n, prev)
		}
		prev = n
	}
}

// TestLevelDBListAndCascade verifies Lister enumeration and that Delete
// cascades over the <name>/ sub-document namespace, matching FSPersist.
func TestLevelDBListAndCascade(t *testing.T) {
	p := newLevelDBForTest(t)
	t.Cleanup(func() { _ = p.Close() })
	for _, n := range []string{"ws", "ws/abc-1", "ws/abc-1/cards/x", "ws/abc-2", "other"} {
		if err := p.Save(n, "v"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := p.List("ws/")
	if err != nil {
		t.Fatal(err)
	}
	// Hierarchical semantics: the prefix addresses a directory, so the
	// document named "ws" itself is not part of the ws/ subtree.
	want := map[string]bool{"ws/abc-1": false, "ws/abc-1/cards/x": false, "ws/abc-2": false}
	for _, n := range got {
		if _, ok := want[n]; !ok {
			t.Fatalf("unexpected name %q", n)
		}
		want[n] = true
	}
	for n, seen := range want {
		if !seen {
			t.Fatalf("missing name in List: %q", n)
		}
	}
	// List("") enumerates everything, including the "ws" document itself.
	all, err := p.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 {
		t.Fatalf("List(\"\") = %v, want 5 names", all)
	}
	// Cascade: Delete("ws/abc-1") reclaims the whole abc-1 subtree.
	if err := p.Delete("ws/abc-1"); err != nil {
		t.Fatal(err)
	}
	var v string
	for _, n := range []string{"ws/abc-1", "ws/abc-1/cards/x"} {
		if err := p.Load(n, &v); err != ErrNotExist {
			t.Fatalf("Load(%q) after cascade Delete: got %v, want ErrNotExist", n, err)
		}
	}
	for _, n := range []string{"ws", "ws/abc-2", "other"} {
		if err := p.Load(n, &v); err != nil {
			t.Fatalf("Load(%q) must survive sibling cascade: %v", n, err)
		}
	}
	// Append records live in the same cascade namespace.
	if err := p.Append("ws/abc-2/requests", []byte("r1\n")); err != nil {
		t.Fatal(err)
	}
	if err := p.Delete("ws/abc-2"); err != nil {
		t.Fatal(err)
	}
	if recs, err := p.ldbRecords("ws/abc-2/requests"); err != nil || len(recs) != 0 {
		t.Fatalf("append records must be cascaded away: %v %d", err, len(recs))
	}
}

// TestLevelDBRegisteredThroughRegistry proves construction goes through the
// B1 registry and that an unknown backend still errors with the registered
// list including goleveldb.
func TestLevelDBRegisteredThroughRegistry(t *testing.T) {
	p, err := New(PersistConfig{Backend: "goleveldb", DataDir: t.TempDir(), Prefix: "reg"})
	if err != nil {
		t.Fatalf("registered goleveldb backend must construct: %v", err)
	}
	t.Cleanup(func() { _ = p.(*LevelDBPersist).Close() })
	_, err = New(PersistConfig{Backend: "golevldb", DataDir: t.TempDir(), Prefix: "reg"})
	if err == nil || !strings.Contains(err.Error(), "goleveldb") {
		t.Fatalf("typo must list registered backends incl. goleveldb: %v", err)
	}
}

// TestLevelDBScanPrefixForward verifies ScanPrefix returns all documents whose
// name starts with the given prefix, in ascending name order, with correct
// raw JSON values. Keys outside the prefix are excluded.
func TestLevelDBScanPrefixForward(t *testing.T) {
	p := newLevelDBForTest(t)
	t.Cleanup(func() { _ = p.Close() })
	docs := map[string]string{
		"ws/alpha":   "val-alpha",
		"ws/beta":    "val-beta",
		"ws/gamma":   "val-gamma",
		"other/doc":  "val-other",
		"ws":         "val-ws-root",
		"wsalpha":    "val-wsalpha", // starts with "ws" but not "ws/"
	}
	for name, val := range docs {
		if err := p.Save(name, val); err != nil {
			t.Fatal(err)
		}
	}

	// Prefix "ws/" matches only the three ws/* documents (pure string prefix,
	// not List's directory semantics — "ws" root doc is excluded because it
	// does not start with "ws/").
	got, err := p.ScanPrefix("ws/")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ws/alpha", "ws/beta", "ws/gamma"}
	if len(got) != len(want) {
		t.Fatalf("ScanPrefix(ws/) count: got %d, want %d (%v)", len(got), len(want), got)
	}
	for i, kv := range got {
		if kv.Name != want[i] {
			t.Errorf("ScanPrefix(ws/)[%d].Name = %q, want %q", i, kv.Name, want[i])
		}
		var val string
		if err := decode(kv.Value, &val); err != nil {
			t.Fatal(err)
		}
		if val != docs[kv.Name] {
			t.Errorf("ScanPrefix(ws/)[%d].Value = %q, want %q", i, val, docs[kv.Name])
		}
	}

	// Empty prefix returns all documents (every d: key) in ascending order.
	all, err := p.ScanPrefix("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(docs) {
		t.Fatalf("ScanPrefix(\"\") count: got %d, want %d", len(all), len(docs))
	}
	// Verify ascending order.
	for i := 1; i < len(all); i++ {
		if all[i-1].Name >= all[i].Name {
			t.Errorf("ScanPrefix(\"\") not ascending: %q >= %q", all[i-1].Name, all[i].Name)
		}
	}
}

// TestLevelDBScanPrefixReverse verifies ScanPrefixReverse returns documents in
// descending name order, respects the limit, and that limit <= 0 returns all.
func TestLevelDBScanPrefixReverse(t *testing.T) {
	p := newLevelDBForTest(t)
	t.Cleanup(func() { _ = p.Close() })
	names := []string{
		"records/2026-01/a",
		"records/2026-01/b",
		"records/2026-01/c",
		"records/2026-02/d",
		"records/2026-02/e",
		"other/x",
	}
	for _, n := range names {
		if err := p.Save(n, n); err != nil {
			t.Fatal(err)
		}
	}

	// Descending order, no limit.
	got, err := p.ScanPrefixReverse("records/", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"records/2026-02/e",
		"records/2026-02/d",
		"records/2026-01/c",
		"records/2026-01/b",
		"records/2026-01/a",
	}
	if len(got) != len(want) {
		t.Fatalf("ScanPrefixReverse count: got %d, want %d", len(got), len(want))
	}
	for i, kv := range got {
		if kv.Name != want[i] {
			t.Errorf("ScanPrefixReverse[%d].Name = %q, want %q", i, kv.Name, want[i])
		}
	}

	// limit = 2: only the two newest (descending).
	got2, err := p.ScanPrefixReverse("records/", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got2) != 2 {
		t.Fatalf("ScanPrefixReverse limit=2: got %d, want 2", len(got2))
	}
	if got2[0].Name != "records/2026-02/e" || got2[1].Name != "records/2026-02/d" {
		t.Errorf("ScanPrefixReverse limit=2: got %q, %q", got2[0].Name, got2[1].Name)
	}

	// limit = -1: no limit (same as 0).
	gotAll, err := p.ScanPrefixReverse("records/", -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotAll) != len(want) {
		t.Fatalf("ScanPrefixReverse limit=-1: got %d, want %d", len(gotAll), len(want))
	}

	// Negative-limit reverse matches forward scan reversed.
	fwd, err := p.ScanPrefix("records/")
	if err != nil {
		t.Fatal(err)
	}
	if len(fwd) != len(gotAll) {
		t.Fatalf("forward/reverse length mismatch: %d vs %d", len(fwd), len(gotAll))
	}
	for i := 0; i < len(fwd); i++ {
		if fwd[i].Name != gotAll[len(gotAll)-1-i].Name {
			t.Errorf("reverse != forward reversed at %d", i)
		}
	}
}

// TestLevelDBScanPrefixIsolation verifies that ScanPrefix and
// ScanPrefixReverse on one prefix never leak keys from another, and that a
// prefix with no matches returns an empty (non-nil) slice and no error.
func TestLevelDBScanPrefixIsolation(t *testing.T) {
	p := newLevelDBForTest(t)
	t.Cleanup(func() { _ = p.Close() })
	if err := p.Save("a/1", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := p.Save("a/2", "v2"); err != nil {
		t.Fatal(err)
	}
	if err := p.Save("b/1", "v3"); err != nil {
		t.Fatal(err)
	}
	if err := p.Append("a/1", []byte("append-record")); err != nil {
		t.Fatal(err)
	}

	// Prefix "a/" returns only the two a/* documents; append records (a:
	// keys) and b/* keys are excluded.
	got, err := p.ScanPrefix("a/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("ScanPrefix(a/) = %d items, want 2", len(got))
	}
	for _, kv := range got {
		if !strings.HasPrefix(kv.Name, "a/") {
			t.Errorf("leaked key: %q", kv.Name)
		}
	}

	// Prefix "zzz/" has no matches.
	empty, err := p.ScanPrefix("zzz/")
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("ScanPrefix(zzz/) = %d items, want 0", len(empty))
	}

	// Reverse with no matches also returns empty.
	emptyR, err := p.ScanPrefixReverse("zzz/", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyR) != 0 {
		t.Fatalf("ScanPrefixReverse(zzz/) = %d items, want 0", len(emptyR))
	}
}

// TestLevelDBScanPrefixExcludesAppendRecords verifies that ScanPrefix only
// returns document keys (d: prefix) and never append records (a:) or
// sequence counters (s:), even when the document name would overlap.
func TestLevelDBScanPrefixExcludesAppendRecords(t *testing.T) {
	p := newLevelDBForTest(t)
	t.Cleanup(func() { _ = p.Close() })
	if err := p.Save("log/entry", "v"); err != nil {
		t.Fatal(err)
	}
	if err := p.Append("log/entry", []byte("rec1\n")); err != nil {
		t.Fatal(err)
	}
	if err := p.Append("log/entry", []byte("rec2\n")); err != nil {
		t.Fatal(err)
	}
	got, err := p.ScanPrefix("log/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "log/entry" {
		t.Fatalf("ScanPrefix(log/) = %v, want [log/entry]", got)
	}
}

// TestLevelDBLockConflictMessage forces a raw second open on a live DB
// directory (bypassing the in-process refcount) and requires the friendly
// lock-conflict error naming the holder pid. The OS-level exclusive lock
// fails in-process too: Windows share-mode 0 on the LOCK file, flock per
// open file description on unix.
func TestLevelDBLockConflictMessage(t *testing.T) {
	dataDir := t.TempDir()
	p, err := newLevelDBPersist(PersistConfig{DataDir: dataDir, Prefix: "conflict"})
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	lp := p.(*LevelDBPersist)
	t.Cleanup(func() { _ = lp.Close() })

	holder, err := os.ReadFile(ldbHolderMarker(lp.dir))
	if err != nil {
		t.Fatalf("holder marker: %v", err)
	}
	if want := fmt.Sprintf("%d@", os.Getpid()); !strings.HasPrefix(string(holder), want) {
		t.Fatalf("holder marker = %q, want prefix %q", holder, want)
	}

	ldbMu.Lock()
	ldbDisabled = true
	ldbMu.Unlock()
	t.Cleanup(func() {
		ldbMu.Lock()
		ldbDisabled = false
		ldbMu.Unlock()
	})

	_, err = newLevelDBPersist(PersistConfig{DataDir: dataDir, Prefix: "conflict"})
	if err == nil {
		t.Fatal("second open on a locked DB unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "locked by another process") {
		t.Fatalf("err = %v, want lock-conflict message", err)
	}
	if want := fmt.Sprintf("holder=%d@", os.Getpid()); !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want %q in message", err, want)
	}
}

func TestLevelDBHolderMarkerRemovedOnClose(t *testing.T) {
	dataDir := t.TempDir()
	p, err := newLevelDBPersist(PersistConfig{DataDir: dataDir, Prefix: "marker"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	lp := p.(*LevelDBPersist)
	if _, err := os.Stat(ldbHolderMarker(lp.dir)); err != nil {
		t.Fatalf("holder marker missing after open: %v", err)
	}
	if err := lp.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Stat(ldbHolderMarker(lp.dir)); !os.IsNotExist(err) {
		t.Fatalf("holder marker still present after close: %v", err)
	}
}
