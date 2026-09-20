package textdiff

import (
	"strconv"
	"strings"
	"testing"
)

// diffOp snapshots simplify assertions over the internal diffOp type.
type opSnapshot struct {
	Kind     string
	Line     string
	OldIndex int
	NewIndex int
}

func mustDiff(t *testing.T, oldLines, newLines []string) []diffOp {
	t.Helper()
	ops, ok := computeLineDiff(oldLines, newLines)
	if !ok {
		t.Fatalf("computeLineDiff aborted over the work budget")
	}
	return ops
}

func snapshotOps(ops []diffOp) []opSnapshot {
	out := make([]opSnapshot, len(ops))
	for i, op := range ops {
		var k string
		switch op.Kind {
		case diffEqual:
			k = "equal"
		case diffDelete:
			k = "delete"
		case diffInsert:
			k = "insert"
		}
		out[i] = opSnapshot{Kind: k, Line: op.Line, OldIndex: op.OldIndex, NewIndex: op.NewIndex}
	}
	return out
}

func TestComputeLineDiff_EmptyInputs(t *testing.T) {
	if got := mustDiff(t, nil, nil); got != nil {
		t.Errorf("empty inputs: want nil, got %v", got)
	}
}

func TestComputeLineDiff_PureInsert(t *testing.T) {
	ops := snapshotOps(mustDiff(t, []string{}, []string{"a", "b", "c"}))
	if len(ops) != 3 || ops[0] != (opSnapshot{"insert", "a", 0, 0}) {
		t.Errorf("pure insert: %+v", ops)
	}
}

func TestComputeLineDiff_PureDelete(t *testing.T) {
	ops := snapshotOps(mustDiff(t, []string{"a", "b", "c"}, []string{}))
	if len(ops) != 3 || ops[0] != (opSnapshot{"delete", "a", 0, 0}) {
		t.Errorf("pure delete: %+v", ops)
	}
}

func TestComputeLineDiff_Identical(t *testing.T) {
	ops := snapshotOps(mustDiff(t, []string{"a", "b", "c"}, []string{"a", "b", "c"}))
	want := []opSnapshot{
		{"equal", "a", 0, 0},
		{"equal", "b", 1, 1},
		{"equal", "c", 2, 2},
	}
	if len(ops) != len(want) {
		t.Fatalf("identical: got %d ops, want %d: %+v", len(ops), len(want), ops)
	}
	for i, op := range ops {
		if op != want[i] {
			t.Errorf("op[%d] = %+v, want %+v", i, op, want[i])
		}
	}
}

func TestComputeLineDiff_SingleLineChange(t *testing.T) {
	// "a","b","c" -> "a","x","c"
	// Note: NewIndex on the deletion is 0 (the new-line cursor before any
	// new line is consumed at that point) — this matches the prior LCS DP
	// behavior and is what BuildHunks expects for hunk NewStart computation.
	ops := snapshotOps(mustDiff(t, []string{"a", "b", "c"}, []string{"a", "x", "c"}))
	want := []opSnapshot{
		{"equal", "a", 0, 0},
		{"delete", "b", 1, 0},
		{"insert", "x", 1, 1},
		{"equal", "c", 2, 2},
	}
	if len(ops) != len(want) {
		t.Fatalf("single change: got %d ops, want %d: %+v", len(ops), len(want), ops)
	}
	for i, op := range ops {
		if op != want[i] {
			t.Errorf("op[%d] = %+v, want %+v", i, op, want[i])
		}
	}
}

func TestComputeLineDiff_AppendAtEnd(t *testing.T) {
	ops := snapshotOps(mustDiff(t, []string{"a", "b"}, []string{"a", "b", "c", "d"}))
	want := []opSnapshot{
		{"equal", "a", 0, 0},
		{"equal", "b", 1, 1},
		{"insert", "c", 1, 2},
		{"insert", "d", 1, 3},
	}
	if len(ops) != len(want) {
		t.Fatalf("append: got %d ops, want %d: %+v", len(ops), len(want), ops)
	}
	for i, op := range ops {
		if op != want[i] {
			t.Errorf("op[%d] = %+v, want %+v", i, op, want[i])
		}
	}
}

func TestComputeLineDiff_CompletelyDifferent(t *testing.T) {
	ops := snapshotOps(mustDiff(t, []string{"a", "b"}, []string{"c", "d"}))
	// No common lines: 2 deletes + 2 inserts, in some order.
	counts := map[string]int{}
	for _, op := range ops {
		counts[op.Kind]++
	}
	if counts["delete"] != 2 || counts["insert"] != 2 || counts["equal"] != 0 {
		t.Errorf("completely different: counts = %+v, want 2 delete + 2 insert + 0 equal", counts)
	}
}

// TestComputeLineDiff_LargeInputDoesNotExplode verifies the Myers algorithm
// handles inputs that the previous LCS DP would have crashed on. For a small
// edit in a large file, d stays small and the work budget is never approached.
func TestComputeLineDiff_LargeInputDoesNotExplode(t *testing.T) {
	const size = 50_000
	old := make([]string, size)
	for i := range old {
		old[i] = "line " + strconv.Itoa(i)
	}
	// Single-line change in the middle.
	new := make([]string, size)
	copy(new, old)
	new[size/2] = "CHANGED"

	ops := mustDiff(t, old, new)
	if len(ops) < 3 {
		t.Fatalf("expected at least 3 ops (equal/change/equal), got %d", len(ops))
	}
	// First op should be an equal run, somewhere in the middle a delete+insert
	// pair, then more equals.
	if ops[0].Kind != diffEqual {
		t.Errorf("first op kind = %v, want equal", ops[0].Kind)
	}
}

// TestBuildHunks_BailOutOnHugeRewrite verifies the work budget: a whole-file
// rewrite of a large file aborts the per-line diff and returns empty hunks +
// simple counts. This is the regression guard for the OOM crash during
// lazy-load recovery (52K-line file × 52K-line file × 8 bytes/int = ~21GB
// under the old LCS DP).
func TestBuildHunks_BailOutOnHugeRewrite(t *testing.T) {
	const size = 20_000
	var sb strings.Builder
	for i := 0; i < size; i++ {
		sb.WriteString("a\n")
	}
	old := sb.String()
	sb.Reset()
	for i := 0; i < size; i++ {
		sb.WriteString("b\n")
	}
	new := sb.String()

	hunks, additions, deletions := BuildHunks(old, new)
	if len(hunks) != 0 {
		t.Errorf("bail-out should return empty hunks, got %d", len(hunks))
	}
	// Counts still reported (all lines differ).
	if additions != size || deletions != size {
		t.Errorf("counts = (%d, %d), want (%d, %d)", additions, deletions, size, size)
	}
}

// TestBuildHunks_LargeFileSmallEdit is the regression for large files losing
// their diff in the UI: a small edit to a file far over the old maxDiffProduct
// threshold must still produce hunks, because Myers cost scales with the edit
// distance, not the file size.
func TestBuildHunks_LargeFileSmallEdit(t *testing.T) {
	const size = 50_000
	var sb strings.Builder
	for i := 0; i < size; i++ {
		sb.WriteString("line " + strconv.Itoa(i) + "\n")
	}
	old := sb.String()
	new := strings.Replace(old, "line 25000\n", "CHANGED\n", 1)

	hunks, additions, deletions := BuildHunks(old, new)
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(hunks))
	}
	if additions != 1 || deletions != 1 {
		t.Errorf("additions=%d deletions=%d, want 1/1", additions, deletions)
	}
}

// TestBuildHunks_SmallChangeReturnsHunks verifies the normal path still
// produces hunks for reasonable inputs.
func TestBuildHunks_SmallChangeReturnsHunks(t *testing.T) {
	old := "line1\nline2\nline3\nline4\n"
	new := "line1\nCHANGED\nline3\nline4\n"

	hunks, additions, deletions := BuildHunks(old, new)
	if len(hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d: %+v", len(hunks), hunks)
	}
	if additions != 1 || deletions != 1 {
		t.Errorf("additions=%d deletions=%d, want 1/1", additions, deletions)
	}
}
