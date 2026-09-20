package persist

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/syndtr/goleveldb/leveldb"
)

// BackendLevelDB is the embedded goleveldb backend. It stores every prefix in
// its own DB directory <DataDir>/leveldb/<prefix>/ and keeps all documents as
// JSON values keyed by the document name, so the T1 contract semantics
// (name → JSON bytes) are identical to fs; only the storage mapping differs.
const BackendLevelDB BackendType = "goleveldb"

// Key prefixes inside the per-prefix DB. They keep document entries, append
// records, and append sequence counters in disjoint key ranges so range scans
// never mix kinds. The separator after a name is 0x00, which cannot occur in
// a validated name, so "actor/abc" sub-keys scan contiguously.
const (
	ldbDocPrefix = "d:"
	ldbSeqPrefix = "s:"
	ldbRecPrefix = "a:"
)

// ldbHandles refcounts open DBs by directory. A goleveldb directory is
// exclusively locked: two Open calls on the same path fail (even in-process).
// Actor code constructs persist instances per actor, often several per prefix
// in one process, so instances opened on the same directory must share one
// *leveldb.DB. The map is keyed by the cleaned absolute directory path and is
// process-wide by construction (one persist package per process); it holds no
// actor business state.
var (
	ldbMu       sync.Mutex
	ldbHandles  = map[string]*ldbHandle{}
	ldbDisabled = false // test hook: force fresh Open instead of reuse
)

type ldbHandle struct {
	db   *leveldb.DB
	refs int
}

// ldbHolderMarker returns the path of the lock-holder marker written beside
// the DB directory. goleveldb's LOCK file records no owner information, so
// every successful open writes pid@host here to make cross-process lock
// conflicts diagnosable. The marker is best-effort: a stale marker after a
// crash is harmless (the next successful open overwrites it, and it is only
// consulted when the OS lock already failed).
func ldbHolderMarker(dir string) string { return dir + ".holder" }

func writeLdbHolder(dir string) {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	_ = WriteFileAtomic(ldbHolderMarker(dir), []byte(fmt.Sprintf("%d@%s\n", os.Getpid(), host)), 0o644)
}

func readLdbHolder(dir string) string {
	b, err := os.ReadFile(ldbHolderMarker(dir))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func openLevelDB(dir string) (*ldbHandle, error) {
	ldbMu.Lock()
	defer ldbMu.Unlock()
	if h, ok := ldbHandles[dir]; ok && !ldbDisabled {
		h.refs++
		return h, nil
	}
	db, err := leveldb.OpenFile(dir, nil)
	if err != nil {
		if ldbLockConflict(err) {
			holder := readLdbHolder(dir)
			if holder == "" {
				holder = "unknown"
			}
			return nil, fmt.Errorf("persist: goleveldb dir %s is locked by another process (holder=%s; goleveldb DBs are single-process — give each sporemind binary its own data_dir): %w", dir, holder, err)
		}
		return nil, fmt.Errorf("persist: goleveldb open %s: %w", dir, err)
	}
	writeLdbHolder(dir)
	h := &ldbHandle{db: db, refs: 1}
	ldbHandles[dir] = h
	return h, nil
}

func (h *ldbHandle) release(dir string) error {
	ldbMu.Lock()
	defer ldbMu.Unlock()
	if cur, ok := ldbHandles[dir]; ok && cur == h {
		if h.refs > 1 {
			h.refs--
			return nil
		}
		delete(ldbHandles, dir)
	}
	if h.refs <= 1 {
		err := h.db.Close()
		_ = os.Remove(ldbHolderMarker(dir))
		return err
	}
	h.refs--
	return nil
}

// LevelDBPersist implements Persist on an embedded goleveldb DB.
type LevelDBPersist struct {
	h   *ldbHandle
	dir string
}

func newLevelDBPersist(cfg PersistConfig) (Persist, error) {
	if cfg.DataDir == "" {
		return nil, fmt.Errorf("persist: goleveldb requires DataDir")
	}
	dir := filepath.Join(cfg.DataDir, "leveldb", cfg.Prefix)
	h, err := openLevelDB(dir)
	if err != nil {
		return nil, err
	}
	return &LevelDBPersist{h: h, dir: dir}, nil
}

// Close releases this instance's reference to the shared DB handle. When the
// last reference goes away the DB is closed and its directory lock freed.
// Close is an optional capability (not part of Persist); callers that never
// close simply keep the handle for the process lifetime.
func (p *LevelDBPersist) Close() error {
	return p.h.release(p.dir)
}

func (p *LevelDBPersist) Save(name string, v any) error {
	if err := validateName(name); err != nil {
		return err
	}
	data, err := encode(v)
	if err != nil {
		return fmt.Errorf("persist: encode: %w", err)
	}
	if err := p.h.db.Put([]byte(ldbDocPrefix+name), data, nil); err != nil {
		return fmt.Errorf("persist: goleveldb put %q: %w", name, err)
	}
	return nil
}

// SaveAll implements Saver as a single atomic leveldb batch write: all docs
// land together or none do. It validates and encodes every entry before
// touching the DB so a bad name or encode error fails before any write.
func (p *LevelDBPersist) SaveAll(docs []Doc) error {
	batch := new(leveldb.Batch)
	for _, d := range docs {
		if err := validateName(d.Name); err != nil {
			return err
		}
		data, err := encode(d.Value)
		if err != nil {
			return fmt.Errorf("persist: encode %q: %w", d.Name, err)
		}
		batch.Put([]byte(ldbDocPrefix+d.Name), data)
	}
	if err := p.h.db.Write(batch, nil); err != nil {
		return fmt.Errorf("persist: goleveldb save-all: %w", err)
	}
	return nil
}

func (p *LevelDBPersist) Load(name string, v any) error {
	if err := validateName(name); err != nil {
		return err
	}
	data, err := p.h.db.Get([]byte(ldbDocPrefix+name), nil)
	if errors.Is(err, leveldb.ErrNotFound) {
		return ErrNotExist
	}
	if err != nil {
		return fmt.Errorf("persist: goleveldb get %q: %w", name, err)
	}
	if err := decode(data, v); err != nil {
		return fmt.Errorf("persist: decode %q: %w", name, err)
	}
	return nil
}

// Delete removes the document for name and cascades into its sub-document
// namespace (name/...), mirroring FSPersist's <name>.json + <name>/ reclaim.
// It is idempotent: deleting a missing name is a no-op.
func (p *LevelDBPersist) Delete(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	batch := new(leveldb.Batch)
	batch.Delete([]byte(ldbDocPrefix + name))
	for _, prefix := range []string{ldbDocPrefix, ldbRecPrefix, ldbSeqPrefix} {
		if err := p.deletePrefixLocked(prefix+name+"/", batch); err != nil {
			return err
		}
	}
	if err := p.h.db.Write(batch, nil); err != nil {
		return fmt.Errorf("persist: goleveldb delete %q: %w", name, err)
	}
	return nil
}

// deletePrefixLocked queues batch deletions for every key sharing prefix.
func (p *LevelDBPersist) deletePrefixLocked(prefix string, batch *leveldb.Batch) error {
	iter := p.h.db.NewIterator(nil, nil)
	defer iter.Release()
	for ok := iter.Seek([]byte(prefix)); ok && iter.Error() == nil && strings.HasPrefix(string(iter.Key()), prefix); ok = iter.Next() {
		batch.Delete(append([]byte(nil), iter.Key()...))
	}
	if err := iter.Error(); err != nil {
		return fmt.Errorf("persist: goleveldb scan %q: %w", prefix, err)
	}
	return nil
}

// List implements Lister with the same hierarchical semantics as FSPersist:
// prefix addresses a directory in the name tree, so List("ws") and
// List("ws/") both enumerate the ws/ subtree and never return the document
// named "ws" itself; List("") enumerates everything. Append records and
// sequence counters are never returned.
func (p *LevelDBPersist) List(prefix string) ([]string, error) {
	scan := ""
	if prefix != "" {
		scan = strings.TrimSuffix(prefix, "/") + "/"
	}
	start := ldbDocPrefix + scan
	var names []string
	iter := p.h.db.NewIterator(nil, nil)
	defer iter.Release()
	for ok := iter.Seek([]byte(start)); ok && iter.Error() == nil && strings.HasPrefix(string(iter.Key()), start); ok = iter.Next() {
		names = append(names, strings.TrimPrefix(string(iter.Key()), ldbDocPrefix))
	}
	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("persist: goleveldb list %q: %w", prefix, err)
	}
	return names, nil
}

// prefixEnd returns the exclusive upper-bound key for a byte-prefix range,
// used to position a reverse iterator at the last in-range key. If all bytes
// are 0xFF the result is nil (no finite upper bound); the caller treats nil
// as "scan to the end of the keyspace".
func prefixEnd(prefix []byte) []byte {
	if len(prefix) == 0 {
		return nil
	}
	end := make([]byte, len(prefix))
	copy(end, prefix)
	for i := len(end) - 1; i >= 0; i-- {
		if end[i] < 0xff {
			end[i]++
			return end[:i+1]
		}
	}
	return nil
}

// ScanPrefix implements PrefixScanner. It returns all document key-value
// pairs whose name starts with prefix, in ascending name order, using a
// snapshot iterator that holds no write lock. The scan is a pure string
// prefix match on document names; it is independent of List's directory
// semantics (List treats prefix as a directory boundary).
func (p *LevelDBPersist) ScanPrefix(prefix string) ([]KV, error) {
	start := ldbDocPrefix + prefix
	var out []KV
	iter := p.h.db.NewIterator(nil, nil)
	defer iter.Release()
	for ok := iter.Seek([]byte(start)); ok && iter.Error() == nil && strings.HasPrefix(string(iter.Key()), start); ok = iter.Next() {
		out = append(out, KV{
			Name:  strings.TrimPrefix(string(iter.Key()), ldbDocPrefix),
			Value: append([]byte(nil), iter.Value()...),
		})
	}
	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("persist: goleveldb scan-prefix %q: %w", prefix, err)
	}
	return out, nil
}

// ScanPrefixReverse implements PrefixScanner. It returns up to limit
// document key-value pairs whose name starts with prefix, in descending
// name order, using a snapshot iterator that holds no write lock.
// limit <= 0 means no limit.
func (p *LevelDBPersist) ScanPrefixReverse(prefix string, limit int) ([]KV, error) {
	start := ldbDocPrefix + prefix
	end := prefixEnd([]byte(start))
	iter := p.h.db.NewIterator(nil, nil)
	defer iter.Release()
	// Position at the last key in the prefix range. Seek to the exclusive
	// upper bound; if a key exists there it is past the range, so step back.
	// If no key reaches the upper bound, the last key in the DB is the
	// candidate. Either way the positioned key is range-checked below.
	if end != nil {
		if iter.Seek(end) {
			if !iter.Prev() {
				return nil, nil
			}
		} else {
			if !iter.Last() {
				return nil, nil
			}
		}
	} else {
		if !iter.Last() {
			return nil, nil
		}
	}
	if !iter.Valid() || !strings.HasPrefix(string(iter.Key()), start) {
		return nil, nil
	}
	var out []KV
	for {
		out = append(out, KV{
			Name:  strings.TrimPrefix(string(iter.Key()), ldbDocPrefix),
			Value: append([]byte(nil), iter.Value()...),
		})
		if limit > 0 && len(out) >= limit {
			break
		}
		if !iter.Prev() {
			break
		}
		if !strings.HasPrefix(string(iter.Key()), start) {
			break
		}
	}
	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("persist: goleveldb scan-prefix-reverse %q: %w", prefix, err)
	}
	return out, nil
}

// appendMu serializes read-seq → write-record sequences per process. A single
// leveldb batch is atomic, but two concurrent Appends on the same name could
// otherwise read the same sequence number and overwrite each other's record.
var appendMu sync.Mutex

// Append implements Appender. Each call writes one complete record keyed by
// name + monotonically increasing sequence number (fixed-width big-endian, so
// lexicographic scan order equals write order). The record and the sequence
// counter are committed in one atomic batch; readers never observe a torn or
// interleaved record.
func (p *LevelDBPersist) Append(name string, data []byte) error {
	if err := validateName(name); err != nil {
		return err
	}
	appendMu.Lock()
	defer appendMu.Unlock()
	seqKey := []byte(ldbSeqPrefix + name)
	seq := uint64(0)
	if raw, err := p.h.db.Get(seqKey, nil); err == nil && len(raw) == 8 {
		seq = binary.BigEndian.Uint64(raw)
	} else if err != nil && !errors.Is(err, leveldb.ErrNotFound) {
		return fmt.Errorf("persist: goleveldb append seq %q: %w", name, err)
	}
	seq++
	recKey := make([]byte, 0, len(ldbRecPrefix)+len(name)+1+8)
	recKey = append(recKey, ldbRecPrefix...)
	recKey = append(recKey, name...)
	recKey = append(recKey, 0x00)
	recKey = binary.BigEndian.AppendUint64(recKey, seq)
	seqRaw := make([]byte, 8)
	binary.BigEndian.PutUint64(seqRaw, seq)
	batch := new(leveldb.Batch)
	batch.Put(recKey, data)
	batch.Put(seqKey, seqRaw)
	if err := p.h.db.Write(batch, nil); err != nil {
		return fmt.Errorf("persist: goleveldb append %q: %w", name, err)
	}
	return nil
}

// ldbRecords reads back append records for name in write order; used by tests
// to verify the Appender atomicity semantics without a filesystem path.
func (p *LevelDBPersist) ldbRecords(name string) ([][]byte, error) {
	prefix := ldbRecPrefix + name + "\x00"
	var out [][]byte
	iter := p.h.db.NewIterator(nil, nil)
	defer iter.Release()
	for ok := iter.Seek([]byte(prefix)); ok && iter.Error() == nil && strings.HasPrefix(string(iter.Key()), prefix); ok = iter.Next() {
		out = append(out, append([]byte(nil), iter.Value()...))
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return out, nil
}
