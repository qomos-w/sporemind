package persist

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Persist is the abstract actor-state persistence interface. Backends
// (filesystem, MongoDB, Postgres, etc.) implement this contract.
//
// # 合同 (Contract)
//
// Every backend MUST satisfy the semantics below; the reusable suite
// RunContractTests in contract_test.go verifies them. New backends must pass
// the suite before they are considered production-ready.
//
//   - name identifies a single logical entry. It is a forward-slash relative
//     key (e.g. "agent/abc-123"), never a filesystem path: implementations
//     MUST reject empty names, names that are absolute paths, and names whose
//     components contain ".." (directory traversal). No state may be stored
//     outside the backend's root for any accepted name.
//   - Save persists v under name, atomically. A subsequent Load MUST return
//     exactly the saved value (JSON roundtrip), and a second Save MUST
//     overwrite the previous value. A Save that returns nil MUST NOT leave
//     temporary artifacts (e.g. *.tmp files) behind.
//   - Load decodes the saved value into v. When no state exists for name it
//     MUST return an error for which errors.Is(err, ErrNotExist) is true and
//     which is distinguishable from genuine I/O failures: real read, decode,
//     or network errors MUST NOT match ErrNotExist.
//   - Delete removes all state for name. It MUST be idempotent (deleting a
//     missing name returns nil), and after Delete, Load MUST return
//     ErrNotExist. Hierarchical backends MAY additionally reclaim a
//     same-named child directory (the "<actorID>/xxx" sub-document
//     convention), so callers can rely on Delete(actorID) cleaning up an
//     actor's whole subtree.
//   - Concurrent Save/Load/Delete on the same name from multiple goroutines
//     MUST NOT produce torn reads: Load either returns ErrNotExist or a
//     complete, decodable value written by some Save.
//
// Backends are NOT required to implement the optional Lister interface, but
// callers that rely on enumeration MUST degrade gracefully when it is absent.
type Persist interface {
	Load(name string, v any) error
	Save(name string, v any) error
	Delete(name string) error
}

// Lister is an optional Persist capability for backends whose storage can
// enumerate persisted names. Sweep-style cleanup (e.g. memory monocard
// garbage collection) uses it to remove entries no longer referenced by the
// current state, instead of diffing against a possibly-stale previous state.
// Backends that cannot enumerate degrade gracefully: callers must fall back.
//
// Both built-in backends implement it — FSPersist (actor state documents)
// and MarkdownPersist (memory monocard files) — so enumeration is available
// regardless of which document backend a store uses; callers must not assume
// a markdown backend just to get listing.
type Lister interface {
	// List returns the persisted names under prefix (forward-slash relative
	// paths, no file extension), matching the names accepted by Save/Delete.
	List(prefix string) ([]string, error)
}

// Appender is an optional Persist capability for backends that support
// append-only data (e.g. step JSONL files, request ledgers). Append writes
// data to the end of the named entry; a single Append call is atomic — it
// never interleaves with a concurrent Append or WriteFileAtomic on the same
// physical path (guaranteed by the per-path lock shared with WriteFileAtomic).
//
// The caller is responsible for the integrity of each individual line or
// record within the batched data: Append does not parse, validate, or
// newline-terminate the payload. If the caller appends multiple logical
// records in one call, it must ensure they are self-delimiting (e.g. each
// ends with '\n').
//
// The name follows the same contract as Save/Load/Delete (forward-slash
// relative key, validated against traversal/absolute paths). Unlike Save,
// which maps name to <BaseDir>/<name>.json, Append maps name to
// <BaseDir>/<name> verbatim, so the caller controls the file extension
// (e.g. ".jsonl", ".log").
type Appender interface {
	Append(name string, data []byte) error
}

// BasePather is an optional Persist capability for backends that expose their
// root path. This lets callers derive sub-paths (e.g. an agent session's
// turns/steps/snapshots/requests directories) from the persist instance rather
// than reaching past it to a global config function. Non-filesystem backends
// typically do not implement this; callers must degrade gracefully.
type BasePather interface {
	BasePath() string
}

// KV is a name-value pair returned by prefix scans.
type KV struct {
	Name  string
	Value []byte // raw JSON bytes (same as Save/Load roundtrip)
}

// PrefixScanner is an optional Persist capability for backends that can
// enumerate key-value pairs by document-name prefix in a single iterator
// pass, avoiding the N+1 query pattern of List+Load. Backends that do not
// implement it force callers to fall back to List + Load.
type PrefixScanner interface {
	// ScanPrefix returns all key-value pairs whose document name starts
	// with prefix, in ascending name order.
	ScanPrefix(prefix string) ([]KV, error)

	// ScanPrefixReverse returns up to limit key-value pairs in descending
	// name order (newest-first when names are time-ordered).
	// limit <= 0 means no limit.
	ScanPrefixReverse(prefix string, limit int) ([]KV, error)
}

// LoadOrZero returns nil when the actor has no persisted state (first start,
// post-drift recovery). The data v is left at its zero value in that case —
// legitimate "no prior state". Actual I/O or decode errors are returned.
//
// Use this when the caller treats "missing state" as a valid loaded state
// (most actor Load() paths). Use Load directly when the caller needs to
// distinguish "new" from "lost persistence" (e.g., agent.loadMailbox, which
// logs the recovery case for diagnostic visibility).
func LoadOrZero(p Persist, name string, v any) error {
	if err := p.Load(name, v); err != nil && !errors.Is(err, ErrNotExist) {
		return err
	}
	return nil
}

// PersistConfig describes which backend to use and its parameters.
// Prefix is the actor-type segment (e.g. "agent", "workspace").
//
// Backend selects the implementation via the backend registry (see
// registry.go); the empty string selects BackendFS. The remaining fields
// are consumed by the network backends (B4/B5/B6/B8) and by tunnel wiring
// (B7):
//
//   - Endpoint: host:port or URL of the remote service for a direct
//     connection. Ignored by embedded backends (fs, goleveldb).
//   - Database: database / collection namespace / bucket within the
//     service. Maps to a store-global namespace for kv backends.
//   - CredentialRef: id of a connection profile owned by the dbmanager
//     actor (workflow decision point 1). Secrets never travel here or in
//     sporemind.yaml; backends resolve the profile to credentials at dial
//     time.
//   - TunnelRef: sshmanager hostID. When set, the backend dials the local
//     forward listener opened by sshmanager (ssh -L semantics) instead of
//     Endpoint directly; tunnel lifecycle stays owned by sshmanager.
type PersistConfig struct {
	Backend       BackendType
	DataDir       string
	Prefix        string
	Endpoint      string
	Database      string
	CredentialRef string
	TunnelRef     string
}

// New creates a Persist from cfg by resolving cfg.Backend through the
// backend registry. The empty string selects BackendFS. Any unregistered
// value — typos and not-yet-implemented backends alike — returns an error
// listing the registered backends instead of silently falling back to fs.
func New(cfg PersistConfig) (Persist, error) {
	t := cfg.Backend
	if t == "" {
		t = BackendFS
	}
	backendMu.RLock()
	f, ok := backends[t]
	backendMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("persist: unknown or unimplemented backend %q (registered: %s)", t, joinSupported())
	}
	return f(cfg)
}

// MustNew creates a Persist from cfg, panicking on error. It is intended for
// package-level initialization and test setup where the backend has already
// been validated by the config layer. Use New for runtime construction where
// the error must be handled explicitly.
func MustNew(cfg PersistConfig) Persist {
	p, err := New(cfg)
	if err != nil {
		panic(err)
	}
	return p
}

// FSPersist is the default filesystem-backed state helper. Actors may
// ignore this and manage their own storage backend; it exists only as a
// convenience for the common local-file case.
type FSPersist struct {
	BaseDir string
}

// NewFSPersist creates a filesystem Persist rooted at baseDir.
func NewFSPersist(baseDir string) *FSPersist {
	return &FSPersist{BaseDir: baseDir}
}

// validateName enforces the name contract shared by all backends: the name is
// a forward-slash relative key, never a filesystem path. It rejects empty
// names, names that resolve to the base directory itself (e.g. "."), absolute
// paths (POSIX or Windows drive-letter), root-relative paths (leading "/" or
// "\"), and any component equal to ".." (directory traversal). Forward-slash
// separators within a name (e.g. "session/node-1") are allowed and treated as
// hierarchical sub-keys. This mirrors MarkdownPersist.path.
func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("persist: name must not be empty")
	}
	if filepath.IsAbs(name) {
		return fmt.Errorf("persist: name must not be an absolute path: %q", name)
	}
	// Reject root-relative paths: on POSIX a leading "/" is already caught by
	// IsAbs, but on Windows it is not absolute (it is relative to the current
	// drive root) and must still be rejected because it escapes BaseDir.
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) {
		return fmt.Errorf("persist: name must not be a root-relative path: %q", name)
	}
	// Reject names that resolve to the base directory itself, aligning with
	// MarkdownPersist.path ("." is not a valid document name).
	if filepath.Clean(filepath.FromSlash(name)) == "." {
		return fmt.Errorf("persist: name must not resolve to the base directory: %q", name)
	}
	for _, part := range strings.Split(filepath.ToSlash(name), "/") {
		if part == ".." {
			return fmt.Errorf("persist: name must not contain parent-directory traversal: %q", name)
		}
	}
	return nil
}

// docPath returns the on-disk file path for a persisted name. The layout is
// one document per name: <BaseDir>/<name>.json. A name containing forward
// slash separators (e.g. "session/node-1") maps to a nested document whose
// parent directory is created on Save.
func (s *FSPersist) docPath(name string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	return filepath.Join(s.BaseDir, clean+".json"), nil
}

// docDir returns the on-disk directory that holds sub-documents of name:
// <BaseDir>/<name>/. This is the cascade target for Delete. The convention is
// that an actor with ID "abc" stores its main state at <BaseDir>/abc.json and
// its child documents under <BaseDir>/abc/, so Delete("abc") reclaims the
// whole subtree in one call.
func (s *FSPersist) docDir(name string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	return filepath.Join(s.BaseDir, clean), nil
}

func fileLock(path string) *sync.RWMutex {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	fileLocksMu.Lock()
	defer fileLocksMu.Unlock()
	if mu, ok := fileLocks[abs]; ok {
		return mu
	}
	mu := &sync.RWMutex{}
	fileLocks[abs] = mu
	return mu
}

const errorSharingViolation = 32 // Windows ERROR_SHARING_VIOLATION

// isSharingViolation reports whether err is a Windows sharing-violation
// (ERROR_SHARING_VIOLATION), which is transient and safe to retry.
func isSharingViolation(err error) bool {
	if err == nil {
		return false
	}
	var sysErr syscall.Errno
	if errors.As(err, &sysErr) {
		return sysErr == errorSharingViolation
	}
	return false
}

// WriteFileAtomic writes data to path using a write-temp → fsync → rename
// sequence so that a crash or kill mid-write cannot leave a truncated or
// partially written file. The temp file is created next to the target as
// <path>.tmp and renamed over it once synced. The parent directory must
// already exist. On any error the temp file is removed.
//
// Concurrent calls on the same path are serialized via an in-process lock;
// Windows rename additionally retries a small number of times on transient
// sharing violations (e.g. antivirus or indexer briefly holding the target).
//
// This is the shared atomic-write primitive; prefer it over a bare
// os.WriteFile whenever the target must not be observably half-written.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	mu := fileLock(path)
	mu.Lock()
	defer mu.Unlock()
	return writeFileAtomicUnlocked(path, data, perm)
}

func writeFileAtomicUnlocked(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	tf, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("persist: write: %w", err)
	}
	if _, err := tf.Write(data); err != nil {
		_ = tf.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("persist: write: %w", err)
	}
	if err := tf.Sync(); err != nil {
		_ = tf.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("persist: sync: %w", err)
	}
	if err := tf.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("persist: write: %w", err)
	}
	if err := renameWithRetry(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("persist: rename: %w", err)
	}
	return nil
}

// renameWithRetry retries os.Rename a few times on transient Windows sharing
// violations. Other errors are returned immediately.
func renameWithRetry(src, dst string) error {
	const attempts = 5
	for i := 0; i < attempts; i++ {
		if err := os.Rename(src, dst); err != nil {
			if isSharingViolation(err) && i < attempts-1 {
				time.Sleep(time.Duration(10*(i+1)) * time.Millisecond)
				continue
			}
			return err
		}
		return nil
	}
	return os.Rename(src, dst)
}

// ReadFileWithSharingRetry reads path, retrying briefly on transient Windows
// sharing violations (antivirus or indexer briefly holding a freshly renamed
// file). It is the read-side counterpart of renameWithRetry: scanners that
// enumerate a directory and then open entries can race an atomic rename even
// though both sides use sharing modes. Other errors are returned immediately.
func ReadFileWithSharingRetry(path string) ([]byte, error) {
	const attempts = 5
	for i := 0; i < attempts; i++ {
		b, err := os.ReadFile(path)
		if err == nil {
			return b, nil
		}
		if !isSharingViolation(err) || i == attempts-1 {
			return nil, err
		}
		time.Sleep(time.Duration(10*(i+1)) * time.Millisecond)
	}
	return nil, os.ErrInvalid
}

// Save persists v for the named actor as a single document at
// <BaseDir>/<name>.json. The serialization format is an internal detail;
// callers must not assume any wire format. The write is atomic: data goes to
// <name>.json.tmp first and is then renamed over <name>.json, so a crash or
// kill mid-write cannot leave a truncated document behind. When name contains
// a forward slash the parent directory is created first.
func (s *FSPersist) Save(name string, v any) error {
	p, err := s.docPath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("persist: mkdir: %w", err)
	}
	data, err := encode(v)
	if err != nil {
		return fmt.Errorf("persist: encode: %w", err)
	}
	return WriteFileAtomic(p, data, 0644)
}

// Load restores v for the named actor. Returns ErrNotExist when no document
// exists (first start or post-drift recovery). Other errors indicate genuine
// I/O or decode failures. A document that fails to decode is renamed to
// <name>.json.corrupt-<timestamp> before the error is returned, so the
// original bytes survive for manual recovery instead of being overwritten by
// the next Save.
func (s *FSPersist) Load(name string, v any) error {
	p, err := s.docPath(name)
	if err != nil {
		return err
	}
	mu := fileLock(p)
	mu.RLock()
	data, err := os.ReadFile(p)
	if err != nil {
		mu.RUnlock()
		if os.IsNotExist(err) {
			return ErrNotExist
		}
		return fmt.Errorf("persist: read: %w", err)
	}
	mu.RUnlock()
	if err := decode(data, v); err != nil {
		backup := fmt.Sprintf("%s.corrupt-%s", p, time.Now().UTC().Format("20060102T150405Z"))
		mu.Lock()
		rerr := os.Rename(p, backup)
		mu.Unlock()
		if rerr == nil {
			return fmt.Errorf("persist: decode: %w (corrupt file backed up to %s)", err, backup)
		}
		return fmt.Errorf("persist: decode: %w", err)
	}
	return nil
}

// Delete removes persisted state for the named actor. It removes the document
// <BaseDir>/<name>.json AND cascades into the same-named directory
// <BaseDir>/<name>/ holding the actor's child documents (see docDir), so
// Delete(actorID) reclaims the actor's whole subtree in one call. Both removals
// are idempotent: a missing document or directory yields nil.
func (s *FSPersist) Delete(name string) error {
	p, err := s.docPath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("persist: delete: %w", err)
	}
	dir, err := s.docDir(name)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("persist: delete cascade: %w", err)
	}
	return nil
}

// List implements Lister: it walks the subtree under prefix and returns the
// persisted names (relative to BaseDir, forward slashes, ".json" stripped),
// matching the names accepted by Save/Delete. Transient atomic-write temp
// files ("<name>.json.tmp") and corrupt backups ("<name>.json.corrupt-*") are
// skipped. A prefix that does not exist yields an empty list.
func (s *FSPersist) List(prefix string) ([]string, error) {
	root := filepath.Join(s.BaseDir, filepath.Clean(filepath.FromSlash(prefix)))
	var names []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && path == root {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		// Belt-and-suspenders: ".tmp" and ".corrupt-*" artifacts do not end in
		// ".json" and are already skipped above, but skip them explicitly per
		// the enumeration contract.
		if strings.HasSuffix(d.Name(), ".tmp") || strings.Contains(d.Name(), ".corrupt-") {
			return nil
		}
		rel, relErr := filepath.Rel(s.BaseDir, path)
		if relErr != nil {
			return relErr
		}
		names = append(names, strings.TrimSuffix(filepath.ToSlash(rel), ".json"))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("persist: list: %w", err)
	}
	return names, nil
}

// BasePath implements BasePather, returning the filesystem root for this
// persist backend. Non-FS backends do not implement this; callers must
// degrade gracefully (type-assert to BasePather).
func (s *FSPersist) BasePath() string {
	return s.BaseDir
}

// Append implements Appender. It appends data to the end of the file at
// <BaseDir>/<name> (no ".json" suffix — the caller controls the extension).
// The parent directory is created if needed. A single Append call is atomic:
// per-path locking (the same fileLock used by WriteFileAtomic) ensures that
// concurrent Appends and concurrent WriteFileAtomic calls on the same
// physical path are serialized — no interleaving, no torn writes.
//
// The caller is responsible for the integrity of each line/record within the
// batched data. If the data contains multiple logical records, the caller
// must ensure they are self-delimiting (e.g. each ends with '\n'). Append
// does not parse or validate the payload.
func (s *FSPersist) Append(name string, data []byte) error {
	if err := validateName(name); err != nil {
		return err
	}
	p := filepath.Join(s.BaseDir, filepath.Clean(filepath.FromSlash(name)))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("persist: append mkdir: %w", err)
	}
	mu := fileLock(p)
	mu.Lock()
	defer mu.Unlock()
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("persist: append open: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("persist: append write: %w", err)
	}
	return nil
}

// MigrateFSLegacyLayout performs a one-time migration of the legacy filesystem
// persist layout — one directory per name holding a state.json blob
// (<name>/state.json) — to the document layout (<name>.json). It walks root,
// finds every directory that directly contains a state.json file, and moves
// that file to <root>/<relative-name>.json, creating parent directories as
// needed. The now-empty source directory is removed; directories that still
// hold other files (e.g. markdown memory sub-trees) are left alone.
//
// The migration is idempotent: re-running on an already-migrated tree is a
// no-op because no <name>/state.json files remain. When both the legacy
// <name>/state.json and the new <name>.json exist (a mixed layout), the new
// document wins and the legacy source is discarded.
//
// Call this once from the startup path, before any actor loads state. The
// persist package keeps no long-term dual-read logic.
func MigrateFSLegacyLayout(root string) error {
	// Pass 1: collect legacy entries without mutating the tree.
	type legacyEntry struct{ src, rel string }
	var entries []legacyEntry
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && path == root {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			return nil
		}
		statePath := filepath.Join(path, "state.json")
		fi, statErr := os.Stat(statePath)
		if statErr != nil || fi.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		entries = append(entries, legacyEntry{src: statePath, rel: rel})
		return nil
	})
	if err != nil {
		return fmt.Errorf("persist: migrate: %w", err)
	}
	// Pass 2: move each legacy entry into the document layout.
	for _, e := range entries {
		dst := filepath.Join(root, e.rel+".json")
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return fmt.Errorf("persist: migrate %s: %w", e.rel, err)
		}
		if _, statErr := os.Stat(dst); statErr == nil {
			// New-layout document already exists: it wins, discard the legacy
			// source so a single name never maps to two documents.
			_ = os.Remove(e.src)
		} else {
			if err := os.Rename(e.src, dst); err != nil {
				return fmt.Errorf("persist: migrate %s: %w", e.rel, err)
			}
		}
		// Best-effort removal of the now-empty source directory. It may still
		// hold non-state.json files; os.Remove only succeeds when empty.
		_ = os.Remove(filepath.Join(root, e.rel))
	}
	return nil
}

func encode(v any) ([]byte, error) {
	return json.Marshal(v)
}

func decode(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
