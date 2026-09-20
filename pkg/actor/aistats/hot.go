package aistats

import gen "github.com/qomos-w/sporemind/pkg/domain/gen"

// hotChunkSize is the number of records per chunk in the hot cache. Full
// chunks are never mutated again, which is what makes iterating a snapshot
// safe after the actor lock is released: a push that fills the tail chunk
// replaces the chunk header with a fresh backing array instead of reusing the
// old one.
const hotChunkSize = 128

// hotCache is a bounded, oldest-first buffer of the most recent records
// written through the actor. Both push and snapshot are O(1) amortized with no
// per-record allocation: pushes append to the tail chunk in place (a new chunk
// is allocated only at chunk boundaries), and a snapshot merely copies the
// chunk headers, aliasing the chunk backing arrays. When the buffer exceeds
// hotRecordsCap, whole full chunks are dropped from the front.
//
// Concurrency: push and snapshot must be called with the actor lock held
// (write lock for push, at least a read lock for snapshot). A snapshot stays
// valid after the caller releases the lock because writers only append to the
// tail chunk at indices past the snapshot's visible length, a chunk switch
// allocates a new array, and eviction drops whole chunks without touching
// their contents.
type hotCache struct {
	chunks [][]gen.AIStatsRecord // oldest-first; every chunk is non-empty
	n      int                   // total records currently held
}

// push appends the newest record, evicting whole chunks from the front once
// the buffer exceeds hotRecordsCap. The caller must hold the actor write lock.
func (c *hotCache) push(r gen.AIStatsRecord) {
	if len(c.chunks) == 0 || len(c.chunks[len(c.chunks)-1]) == hotChunkSize {
		c.chunks = append(c.chunks, make([]gen.AIStatsRecord, 0, hotChunkSize))
	}
	c.chunks[len(c.chunks)-1] = append(c.chunks[len(c.chunks)-1], r)
	c.n++
	for c.n > hotRecordsCap && len(c.chunks) > 1 {
		c.n -= len(c.chunks[0])
		c.chunks = c.chunks[1:]
	}
}

// snapshot returns a copy of the chunk headers, oldest chunk first. The
// returned chunks alias the backing arrays, which are never mutated in the
// visible range, so callers can iterate the snapshot after releasing the actor
// lock. The caller must hold the actor lock (a read lock suffices).
func (c *hotCache) snapshot() [][]gen.AIStatsRecord {
	out := make([][]gen.AIStatsRecord, len(c.chunks))
	copy(out, c.chunks)
	return out
}

// recordCount returns the number of records currently held.
func (c *hotCache) recordCount() int {
	return c.n
}