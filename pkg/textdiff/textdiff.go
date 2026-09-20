// Package textdiff computes line-based diffs between two texts, producing
// unified-diff hunks for the UI. The Myers algorithm runs in O((n+m)·d) time
// where d is the edit distance, so a small edit to a huge file is cheap; a
// work budget aborts pathological whole-file rewrites before the trace
// history can exhaust memory (the original O(m·n) LCS DP allocated ~21GB on
// a 52K-line file and OOM-crashed the desktop process).
package textdiff

import (
	"strings"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// maxDiffWork bounds computeLineDiff, measured in edit-distance × total-line
// cells. The Myers trace history holds one v[] snapshot (2(n+m)+1 ints) per
// edit-distance round, so memory stays under ~2×maxDiffWork ints (~64MB);
// past the budget the per-line hunk view is dropped and only the
// addition/deletion counts are returned.
const maxDiffWork = 4_000_000

// BuildHunks 计算两段文本的行级 diff，返回 hunks、新增行数、删除行数。
// 超出工作预算（整文件重写等大改动）时返回 nil hunks 与行数兜底计数。
func BuildHunks(oldContent, newContent string) ([]domain.FileSystemEditHunk, int, int) {
	oldLines := splitLines(oldContent)
	newLines := splitLines(newContent)

	diff, ok := computeLineDiff(oldLines, newLines)
	if !ok {
		return nil, len(newLines), len(oldLines)
	}

	var hunks []domain.FileSystemEditHunk
	var additions, deletions int
	var current *domain.FileSystemEditHunk

	for _, op := range diff {
		switch op.Kind {
		case diffEqual:
			if current != nil {
				hunks = append(hunks, *current)
				current = nil
			}
		case diffDelete:
			if current == nil {
				current = startHunk(op.OldIndex+1, op.NewIndex+1)
			}
			current.OldLines++
			current.Lines = append(current.Lines, "-"+op.Line)
			deletions++
		case diffInsert:
			if current == nil {
				current = startHunk(op.OldIndex+1, op.NewIndex+1)
			}
			current.NewLines++
			current.Lines = append(current.Lines, "+"+op.Line)
			additions++
		}
	}
	if current != nil {
		hunks = append(hunks, *current)
	}
	return hunks, additions, deletions
}

func splitLines(s string) []string {
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func startHunk(oldStart, newStart int) *domain.FileSystemEditHunk {
	return &domain.FileSystemEditHunk{
		OldStart: int32(oldStart),
		OldLines: 0,
		NewStart: int32(newStart),
		NewLines: 0,
	}
}

type diffOpKind int

const (
	diffEqual diffOpKind = iota
	diffDelete
	diffInsert
)

// diffOp 表示一行文本的 diff 操作。
type diffOp struct {
	Kind     diffOpKind
	Line     string
	OldIndex int
	NewIndex int
}

// computeLineDiff returns the diff ops turning oldLines into newLines, ordered
// from the start of the file to the end. Implements the Myers O((n+m)d) diff
// algorithm (An O(ND) Difference Algorithm and Its Variations, Myers 1986),
// where d is the edit distance — for typical agent edits (a few lines changed
// in a source file) d is tiny and the algorithm is effectively linear.
//
// The second return value is false when the edit distance exceeds the work
// budget; the caller then falls back to plain line counts.
func computeLineDiff(oldLines, newLines []string) ([]diffOp, bool) {
	n, m := len(oldLines), len(newLines)
	if n == 0 && m == 0 {
		return nil, true
	}
	if n == 0 {
		ops := make([]diffOp, m)
		for i, line := range newLines {
			ops[i] = diffOp{Kind: diffInsert, Line: line, OldIndex: 0, NewIndex: i}
		}
		return ops, true
	}
	if m == 0 {
		ops := make([]diffOp, n)
		for i, line := range oldLines {
			ops[i] = diffOp{Kind: diffDelete, Line: line, OldIndex: i, NewIndex: 0}
		}
		return ops, true
	}

	// Myers forward search. v is indexed by diagonal k = x - y, offset by
	// `offset` so negative k indices fit. The maximum |k| at edit distance d
	// is d, so v needs 2*(m+n)+1 slots.
	max := n + m
	offset := max
	v := make([]int, 2*max+1)
	// trace[d] is a copy of v after round d — needed for backtracking.
	trace := make([][]int, 0, 64)

	var d int
	found := false
	for d = 0; d <= max; d++ {
		// Work-budget guard: each round appends a 2(n+m)+1-int snapshot to
		// the trace, so memory grows by O(n+m) per round. Abort before the
		// next round once d·(n+m) crosses the budget.
		if d*(n+m) > maxDiffWork {
			return nil, false
		}
		vCopy := make([]int, len(v))
		copy(vCopy, v)
		trace = append(trace, vCopy)

		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[k-1+offset] < v[k+1+offset]) {
				x = v[k+1+offset] // move down (insertion from b)
			} else {
				x = v[k-1+offset] + 1 // move right (deletion from a)
			}
			y := x - k
			for x < n && y < m && oldLines[x] == newLines[y] {
				x++
				y++
			}
			v[k+offset] = x
			if x >= n && y >= m {
				found = true
			}
		}
		if found {
			break
		}
	}

	// Backtrack: walk trace[d] → trace[0], emitting ops in reverse, then flip.
	var ops []diffOp
	x, y := n, m
	for d = len(trace) - 1; d > 0; d-- {
		v := trace[d]
		k := x - y

		var prevK int
		if k == -d || (k != d && v[k-1+offset] < v[k+1+offset]) {
			prevK = k + 1 // came from above (insertion)
		} else {
			prevK = k - 1 // came from left (deletion)
		}

		prevX := v[prevK+offset]
		prevY := prevX - prevK

		// Snake: any equal-line runs we traversed on this round.
		for x > prevX && y > prevY {
			ops = append(ops, diffOp{Kind: diffEqual, Line: oldLines[x-1], OldIndex: x - 1, NewIndex: y - 1})
			x--
			y--
		}

		if d > 0 {
			if x == prevX {
				// insertion: line came from newLines[y-1]
				ops = append(ops, diffOp{Kind: diffInsert, Line: newLines[y-1], OldIndex: x - 1, NewIndex: y - 1})
			} else {
				// deletion: line came from oldLines[x-1]
				ops = append(ops, diffOp{Kind: diffDelete, Line: oldLines[x-1], OldIndex: x - 1, NewIndex: y - 1})
			}
			x = prevX
			y = prevY
		}
	}
	// Leading snake at d=0 (any common prefix before the first edit).
	for x > 0 && y > 0 {
		ops = append(ops, diffOp{Kind: diffEqual, Line: oldLines[x-1], OldIndex: x - 1, NewIndex: y - 1})
		x--
		y--
	}

	for left, right := 0, len(ops)-1; left < right; left, right = left+1, right-1 {
		ops[left], ops[right] = ops[right], ops[left]
	}
	return ops, true
}
