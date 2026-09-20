package aistats

import (
	"fmt"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func hotRec(id string) gen.AIStatsRecord {
	return gen.AIStatsRecord{ID: id, CompletedAt: "2026-01-01T00:00:00Z"}
}

func TestHotCachePushSnapshot(t *testing.T) {
	var c hotCache
	for i := 0; i < hotChunkSize+3; i++ {
		c.push(hotRec(fmt.Sprintf("r%03d", i)))
	}
	if c.recordCount() != hotChunkSize+3 {
		t.Fatalf("count: got %d, want %d", c.recordCount(), hotChunkSize+3)
	}
	got := c.snapshot()
	if len(got) != 2 {
		t.Fatalf("chunks: got %d, want 2", len(got))
	}
	if len(got[0]) != hotChunkSize || len(got[1]) != 3 {
		t.Fatalf("chunk sizes: got %d,%d, want %d,3", len(got[0]), len(got[1]), hotChunkSize)
	}
	// Oldest-first across chunk boundaries: first chunk starts at r000, the
	// tail chunk ends at the most recently pushed record.
	if got[0][0].ID != "r000" || got[1][2].ID != fmt.Sprintf("r%03d", hotChunkSize+2) {
		t.Errorf("order: first=%s last=%s", got[0][0].ID, got[1][2].ID)
	}
}

func TestHotCacheSnapshotStableAfterPush(t *testing.T) {
	var c hotCache
	for i := 0; i < 5; i++ {
		c.push(hotRec(fmt.Sprintf("a%d", i)))
	}
	snap := c.snapshot()
	// A later push appends at an index past the snapshot's visible length, so
	// the snapshot (aliasing the same backing array) must be unchanged.
	c.push(hotRec("new"))
	if len(snap) != 1 || len(snap[0]) != 5 || snap[0][4].ID != "a4" {
		t.Fatalf("snapshot disturbed by later push: %+v", snap)
	}
	if c.recordCount() != 6 {
		t.Fatalf("count after push: got %d, want 6", c.recordCount())
	}
}

func TestHotCacheEviction(t *testing.T) {
	var c hotCache
	total := hotRecordsCap + hotChunkSize + 17
	for i := 0; i < total; i++ {
		c.push(hotRec(fmt.Sprintf("e%05d", i)))
	}
	if c.recordCount() > hotRecordsCap {
		t.Fatalf("count %d exceeds cap %d", c.recordCount(), hotRecordsCap)
	}
	snap := c.snapshot()
	first := snap[0][0].ID
	if first == "e00000" {
		t.Fatal("expected the oldest records to be evicted")
	}
	last := snap[len(snap)-1][len(snap[len(snap)-1])-1].ID
	if last != fmt.Sprintf("e%05d", total-1) {
		t.Errorf("newest record: got %s, want e%05d", last, total-1)
	}
}
