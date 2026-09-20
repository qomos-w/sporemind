package project

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

const wikiDir = ".sporecode/wiki"

// isTransientFileError reports whether err looks like a Windows sharing
// violation / access-denied on a file that is concurrently open or being
// replaced. These are transient: another handle (our own rename-based Save,
// an indexer, a git process) holds the file for microseconds. Unix error
// strings never match, so the check is free there.
func isTransientFileError(err error) bool {
	if err == nil {
		return false
	}
	if runtime.GOOS != "windows" {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "being used by another process") ||
		strings.Contains(msg, "Access is denied")
}

// fsCardStore implements CardStore using .md files in a wiki directory.
// wikiPath is the absolute (or root-relative) path to the directory holding
// the *.md card files.
//
// An in-memory cache is maintained for both List results and individual Get
// results. The cache is invalidated on Save/Delete and on external file
// changes detected by a dedicated fsnotify watcher on the wiki directory.
type fsCardStore struct {
	wikiPath string

	mu        sync.RWMutex
	cardCache map[string]*CardRecord // title → card (single-card cache)
	listCache []*CardRecord          // cached List result; nil when invalid
	// titleLocks serializes same-title writes within this process so two
	// concurrent Saves cannot stampede the OS-level replace (Windows
	// rename-over-replace fails transiently under such contention). Entries
	// accumulate per distinct written title — bounded by card count.
	titleLocks map[string]*sync.Mutex

	// test-only counters exposed for white-box assertions.
	globCount atomic.Int64
	getCount  atomic.Int64

	// invalidateCallback is invoked when the cache is invalidated by external
	// file-system events (fsnotify watcher) or by directory-level exceptions.
	// It is intentionally NOT called from Save/Delete: those are the actor's
	// own write paths and the caller is responsible for keeping dependent
	// caches (such as workflow_topo graphs) in sync.
	invalidateCallback func(reason string)

	watcher     *fsnotify.Watcher
	watcherDone chan struct{}
	watcherOnce sync.Once
}

func newFSCardStore(wikiPath string) *fsCardStore {
	s := &fsCardStore{
		wikiPath:   wikiPath,
		cardCache:  make(map[string]*CardRecord),
		titleLocks: make(map[string]*sync.Mutex),
	}
	s.startCacheWatcher()
	return s
}

// SetInvalidateCallback registers a function to be called when the in-memory
// cache is invalidated by external events. The callback is invoked from the
// watcher goroutine, so it must not block and should not call back into the
// store.
func (s *fsCardStore) SetInvalidateCallback(cb func(reason string)) {
	s.mu.Lock()
	s.invalidateCallback = cb
	s.mu.Unlock()
}

// GetCount returns the number of Get calls observed by this store. It is used
// only by tests to assert that read paths avoid unnecessary store probes.
func (s *fsCardStore) GetCount() int64 {
	return s.getCount.Load()
}

// lockTitle acquires the per-title write mutex, blocking concurrent
// Save/Delete of the same card until the returned release func is called.
func (s *fsCardStore) lockTitle(title string) func() {
	s.mu.Lock()
	lk, ok := s.titleLocks[title]
	if !ok {
		lk = &sync.Mutex{}
		s.titleLocks[title] = lk
	}
	s.mu.Unlock()
	lk.Lock()
	return lk.Unlock
}

// windowsReservedIDChars maps the characters that Windows forbids in
// filenames to their percent-encoded form. These are encoded on Windows only;
// on Unix they are legal filename characters and stay literal.
var windowsReservedIDChars = map[byte]string{
	'?': "%3F",
	'*': "%2A",
	':': "%3A",
	'|': "%7C",
	'"': "%22",
	'<': "%3C",
	'>': "%3E",
}

// encodeCardID maps a card id to its filename stem. The mapping is injective
// on every platform: path separators and control characters are percent-
// encoded (never folded to '_'), '_' and spaces stay literal, and '%' itself
// is escaped first, so decodeCardID inverts it exactly. Windows additionally
// encodes its reserved set. The two platforms therefore disagree on the
// address of an id containing those characters, but identity always comes
// from the frontmatter id, so reads still resolve via readCardRaw's legacy
// fallbacks and the next Save rewrites the file at the canonical address.
func encodeCardID(id string) string {
	var b strings.Builder
	b.Grow(len(id))
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c == '%':
			b.WriteString("%25")
		case c == '/' || c == '\\':
			b.WriteString(fmt.Sprintf("%%%02X", c))
		case c < 0x20:
			b.WriteString(fmt.Sprintf("%%%02X", c))
		default:
			if runtime.GOOS == "windows" {
				if enc, ok := windowsReservedIDChars[c]; ok {
					b.WriteString(enc)
					continue
				}
			}
			b.WriteByte(c)
		}
	}
	return b.String()
}

// decodeCardID is the exact inverse of encodeCardID. Sequences that the
// encoder never produces are left literal, so hand-named files like "x%41.md"
// round-trip unchanged.
func decodeCardID(stem string) string {
	var b strings.Builder
	b.Grow(len(stem))
	for i := 0; i < len(stem); i++ {
		c := stem[i]
		if c != '%' || i+2 >= len(stem) {
			b.WriteByte(c)
			continue
		}
		if dec, ok := decodePercentSeq(stem[i+1], stem[i+2]); ok {
			b.WriteByte(dec)
			i += 2
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func decodePercentSeq(hi, lo byte) (byte, bool) {
	v, ok := hexByte(hi, lo)
	if !ok {
		return 0, false
	}
	switch v {
	case '%', '/', '\\':
		return v, true
	}
	if runtime.GOOS == "windows" {
		if _, reserved := windowsReservedIDChars[v]; reserved {
			return v, true
		}
	}
	if v < 0x20 {
		return v, true
	}
	return 0, false
}

func hexByte(hi, lo byte) (byte, bool) {
	h, ok1 := hexVal(hi)
	l, ok2 := hexVal(lo)
	if !ok1 || !ok2 {
		return 0, false
	}
	return h<<4 | l, true
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	}
	return 0, false
}

// cardFileName returns the file that stores the card with the given id.
// cardTitleFromFileName inverts it exactly; the mapping is injective, so two
// distinct ids can never share one file.
func cardFileName(title string) string {
	return encodeCardID(title) + ".md"
}

// foldLegacySeparators applies the separator folding and Windows special
// encoding of older stores to an id stem.
func foldLegacySeparators(s string) string {
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	if runtime.GOOS == "windows" {
		s = strings.ReplaceAll(s, "%", "%25")
		for c, enc := range windowsReservedIDChars {
			s = strings.ReplaceAll(s, string(rune(c)), enc)
		}
	}
	return s
}

// legacyCardFileName reproduces the pre-injective mapping used by older
// stores: path separators folded to '_' and a trailing ".md" stripped from
// the id. It is used to locate files those stores wrote, so reads keep
// working until the next Save migrates the card to its canonical address.
func legacyCardFileName(title string) string {
	return foldLegacySeparators(strings.TrimSuffix(title, ".md")) + ".md"
}

// legacyFoldedAddress is the folded address without the ".md" suffix trim.
// Cache invalidation matches by this variant only: the trim variant maps ids
// ending in ".md" onto other cards' canonical files (legacy "A.md" for id
// "A.md" collides with canonical "A.md" for id "A") and would evict live
// entries.
func legacyFoldedAddress(title string) string {
	return foldLegacySeparators(title) + ".md"
}

func cardTitleFromFileName(name string) (string, bool) {
	title := strings.TrimSuffix(name, ".md")
	legacy := strings.HasPrefix(title, "skill$")
	if legacy {
		title = "skill:" + strings.TrimPrefix(title, "skill$")
	}
	return decodeCardID(title), legacy
}

func (s *fsCardStore) wikiAbsPath(title string) string {
	return filepath.Join(s.wikiPath, cardFileName(title))
}

// legacySkillID converts a "skill:" prefixed ID to the legacy "skill$" form
// used by older card files on disk. Returns the legacy ID and true if the
// input starts with "skill:".
func legacySkillID(id string) (string, bool) {
	if strings.HasPrefix(id, "skill:") {
		return "skill$" + strings.TrimPrefix(id, "skill:"), true
	}
	return "", false
}

// readFileResilient reads a wiki file, retrying briefly on Windows transient
// sharing violations. Atomic rename-based Save can make a concurrent reader's
// open collide with the replace for microseconds; reads must not surface that
// as an error (the pre-rename direct-write world never failed opens).
func (s *fsCardStore) readFileResilient(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil || !isTransientFileError(err) {
		return data, err
	}
	for attempt := 0; attempt < 4 && err != nil && isTransientFileError(err); attempt++ {
		time.Sleep(time.Duration(attempt+1) * 2 * time.Millisecond)
		data, err = os.ReadFile(path)
	}
	return data, err
}

// readCardRaw locates the raw bytes of the file storing title: the canonical
// address first, then the legacy "skill$" alias, then the legacy folded-
// separator address. It keeps reads of cards written by older stores working
// until their next Save migrates them to the canonical address.
func (s *fsCardStore) readCardRaw(title string) ([]byte, error) {
	data, err := s.readFileResilient(s.wikiAbsPath(title))
	if err == nil || !os.IsNotExist(err) {
		return data, err
	}
	if legacyTitle, ok := legacySkillID(title); ok {
		if legacyData, legacyErr := s.readFileResilient(filepath.Join(s.wikiPath, legacyCardFileName(legacyTitle))); legacyErr == nil {
			return legacyData, nil
		}
	}
	if legacyName := legacyCardFileName(title); legacyName != cardFileName(title) {
		if legacyData, legacyErr := s.readFileResilient(filepath.Join(s.wikiPath, legacyName)); legacyErr == nil {
			return legacyData, nil
		}
	}
	return nil, err
}

func (s *fsCardStore) Get(title string) (*CardRecord, error) {
	s.getCount.Add(1)
	// Fast path: read lock, check cache. Returns a defensive copy so callers
	// cannot mutate the cached snapshot.
	s.mu.RLock()
	if card, ok := s.cardCache[title]; ok {
		cloned := cloneCard(card)
		s.mu.RUnlock()
		return cloned, nil
	}
	s.mu.RUnlock()

	data, err := s.readCardRaw(title)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrCardNotFound
		}
		return nil, fmt.Errorf("cardstore: read %s: %w", title, err)
	}
	raw := normalizeLineEndings(string(data))
	card := decodeCard(title, raw)
	// Repair non-canonical status in memory only. Get is a read path: calling
	// Save here (the old behavior) raced concurrent writers and could revert
	// fresher content written between our ReadFile and the repair-Save — a
	// silent lost update. The repaired copy is cached and returned; the
	// Repaired flag lets callers opt into persisting the normalization with
	// their own write.
	repaired := repairCardStatus(card)
	s.cacheCard(card)
	out := cloneCard(card)
	out.Repaired = repaired
	return out, nil
}

func (s *fsCardStore) List() ([]*CardRecord, error) {
	// Fast path: return a defensive copy of the cached list so callers can
	// sort or mutate the result without corrupting the cache.
	s.mu.RLock()
	if s.listCache != nil {
		out := make([]*CardRecord, len(s.listCache))
		for i, c := range s.listCache {
			out[i] = cloneCard(c)
		}
		s.mu.RUnlock()
		return out, nil
	}
	s.mu.RUnlock()

	dir := s.wikiPath
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("cardstore: mkdir wiki dir: %w", err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return nil, fmt.Errorf("cardstore: glob: %w", err)
	}
	var cards []*CardRecord
	var repairedFlags []bool // parallel to cards; marked on returned copies only
	seen := make(map[string]bool, len(matches))
	// idSeen dedups by the card's true identity (frontmatter id): the same id
	// can be reachable from both a legacy and a canonical address during
	// migration. The canonical address wins so one id lists once.
	idSeen := make(map[string]int, len(matches))
	s.globCount.Add(1)
	for _, path := range matches {
		data, err := s.readFileResilient(path)
		if err != nil {
			continue
		}
		title, legacy := cardTitleFromFileName(filepath.Base(path))
		card := decodeCard(title, normalizeLineEndings(string(data)))
		repaired := repairCardStatus(card)
		// Deduplicate case-insensitively so that a single physical file (on
		// case-insensitive filesystems) cannot produce two list entries when
		// the frontmatter id and the on-disk filename differ in case.
		seenKey := strings.ToLower(title)
		if seen[seenKey] && legacy {
			continue
		}
		if seen[seenKey] {
			if !legacy {
				for i, existing := range cards {
					if strings.EqualFold(existing.Title, card.Title) {
						cards[i] = card
						repairedFlags[i] = repaired
						break
					}
				}
			}
			continue
		}
		seen[seenKey] = true
		idKey := strings.ToLower(card.Title)
		if idx, dup := idSeen[idKey]; dup {
			if filepath.Base(path) == cardFileName(card.Title) {
				cards[idx] = card
				repairedFlags[idx] = repaired
			}
			continue
		}
		idSeen[idKey] = len(cards)
		cards = append(cards, card)
		repairedFlags = append(repairedFlags, repaired)
	}
	s.cacheList(cards)
	// Return defensive copies; the cache keeps the originals. Repaired is
	// marked on the returned copies only so the cache stays flag-clean.
	out := make([]*CardRecord, len(cards))
	for i, c := range cards {
		clone := cloneCard(c)
		clone.Repaired = repairedFlags[i]
		out[i] = clone
	}
	return out, nil
}

// Save persists card atomically to the wiki directory. It acquires the
// per-title write mutex internally; callers that already hold the lock (e.g.
// handleWikiSetStatus) should use saveLocked instead.
// normalizeFrontmatterCloser ensures the closing --- delimiter of the YAML
// frontmatter starts on its own line. Some writers (and hand-authored cards)
// produced "key: value---", which the tolerant backend parser accepts (it
// scans for the first "---") but the frontend's stricter line-anchored parser
// rejects — making the card unopenable in the UI despite being listed.
func normalizeFrontmatterCloser(raw string) string {
	if !strings.HasPrefix(raw, "---") {
		return raw
	}
	end := strings.Index(raw[3:], "---")
	if end <= 0 {
		return raw
	}
	if raw[3+end-1] != '\n' {
		return raw[:3+end] + "\n" + raw[3+end:]
	}
	return raw
}

func (s *fsCardStore) Save(card *CardRecord) error {
	release := s.lockTitle(card.Title)
	defer release()
	return s.saveLocked(card)
}

// saveLocked contains the actual file operations of Save. The caller must hold
// the per-title write mutex for card.Title.
func (s *fsCardStore) saveLocked(card *CardRecord) error {
	path := s.wikiAbsPath(card.Title)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("cardstore: create wiki dir: %w", err)
	}
	// On case-insensitive filesystems the target path may resolve to an
	// existing file whose on-disk name differs only in case from
	// cardFileName(card.Title). Rename it to the canonical form first so
	// that git and other case-sensitive tools see a single stable path.
	want := cardFileName(card.Title)
	dir := filepath.Dir(path)
	if existing, err := os.ReadDir(dir); err == nil {
		for _, f := range existing {
			if !f.IsDir() && strings.EqualFold(f.Name(), want) && f.Name() != want {
				if err := s.guardExistingID(filepath.Join(dir, f.Name()), card.Title); err != nil {
					return err
				}
				_ = os.Rename(filepath.Join(dir, f.Name()), path)
				break
			}
		}
	}
	// Never overwrite a file that stores a different card id. The filename
	// mapping used to fold '/' and '\' to '_', so ids "A/B" and "A_B" shared
	// one file and the second Save silently destroyed the first card; any
	// address that already belongs to another id must fail loudly.
	if err := s.guardExistingID(path, card.Title); err != nil {
		return err
	}
	card.Raw = normalizeFrontmatterCloser(normalizeLineEndings(card.Raw))
	data := []byte(card.Raw)
	if err := writeFileAtomic(path, data); err != nil {
		return fmt.Errorf("cardstore: write %s: %w", card.Title, err)
	}
	// Remove legacy addresses holding the same id so List cannot show one id
	// twice (the watcher will drop their stale cache entries).
	s.removeLegacyAddresses(card.Title)
	s.invalidateCacheForSave(card)
	return nil
}

// guardExistingID rejects a write to an existing file that decodes to a
// different card id (case-variant ids are treated as the same card, matching
// the case-insensitive filesystem rename handling above). Unreadable files
// are left to writeFileAtomic to surface.
func (s *fsCardStore) guardExistingID(path, id string) error {
	data, err := s.readFileResilient(path)
	if err != nil || len(data) == 0 {
		return nil
	}
	title, _ := cardTitleFromFileName(filepath.Base(path))
	existing := decodeCard(title, normalizeLineEndings(string(data)))
	if existing.Title != "" && !strings.EqualFold(existing.Title, id) {
		return fmt.Errorf("cardstore: %s already stores card %q; refusing to overwrite with %q", filepath.Base(path), existing.Title, id)
	}
	return nil
}

// removeLegacyAddresses deletes files at the legacy folded-separator (and
// "skill$") addresses of id, but only when they decode to the same id — a
// legacy address may legitimately store a different card (e.g. "A_B.md"
// holding id "A_B" while "A/B" lives at "A%2FB.md") and must be left alone.
func (s *fsCardStore) removeLegacyAddresses(id string) {
	canonical := cardFileName(id)
	candidates := []string{legacyCardFileName(id)}
	if legacyTitle, ok := legacySkillID(id); ok {
		candidates = append(candidates, legacyCardFileName(legacyTitle))
	}
	for _, name := range candidates {
		if name == canonical {
			continue
		}
		path := filepath.Join(s.wikiPath, name)
		data, err := s.readFileResilient(path)
		if err != nil {
			continue
		}
		title, _ := cardTitleFromFileName(name)
		if card := decodeCard(title, normalizeLineEndings(string(data))); card.Title == id {
			_ = os.Remove(path)
		}
	}
}

// writeFileAtomic replaces path with data atomically: data goes to a unique
// temp file in path's directory (same volume — required for rename) and is
// renamed over the target. A direct os.WriteFile exposes a half-written window
// that readers and the fsnotify watcher can observe as a torn card; rename is
// atomic, so every reader sees either the previous content or the complete new
// content. The temp name deliberately does not end in ".md": both List's *.md
// glob and the watcher's .md suffix filter ignore it, so no spurious cache
// invalidation fires while the temp file exists.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	// Remove the temp on every failure path; after a successful Rename this
	// is a no-op (the name no longer exists).
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	// Match the historical 0o644 mode; CreateTemp defaults to 0600 which
	// would break other tools reading committed wiki files.
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("chmod temp: %w", err)
	}
	// On Windows, rename-over-replace fails transiently (sharing violation /
	// access denied) when another handle holds the target — including our own
	// concurrent Saves replacing the same file. Retry with a short backoff
	// before giving up; contention windows are microseconds wide.
	var renameErr error
	for attempt := 0; attempt < 8; attempt++ {
		renameErr = os.Rename(tmpName, path)
		if renameErr == nil {
			tmpName = ""
			return nil
		}
		if !isTransientFileError(renameErr) {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 2 * time.Millisecond)
	}
	return fmt.Errorf("rename: %w", renameErr)
}

func (s *fsCardStore) Delete(title string) error {
	release := s.lockTitle(title)
	defer release()

	path := s.wikiAbsPath(title)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cardstore: remove %s: %w", title, err)
	}
	// Delete is symmetric with Get's legacy "skill$" fallback (see legacySkillID):
	// without this, a skill card stored only under the legacy filename can be
	// read by Get but never removed by Delete, leaving system-card cleanup
	// (removeSystemCards) unable to clear builtin skill residues.
	if legacyTitle, ok := legacySkillID(title); ok {
		if err := os.Remove(s.wikiAbsPath(legacyTitle)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("cardstore: remove legacy %s: %w", legacyTitle, err)
		}
	}
	// Also clear a legacy folded-separator address when it stores the same id;
	// one holding a different card must survive (see removeLegacyAddresses).
	s.removeLegacyAddresses(title)
	s.invalidateCacheForDelete(title)
	return nil
}

// repairCardStatus normalizes a task card's status to one of the six canonical
// values and keeps the raw frontmatter in sync. It returns true if a change was
// made.
func repairCardStatus(card *CardRecord) bool {
	if card == nil || card.Status == "" {
		return false
	}
	card.Status = normalizeTaskStatus(card.Status)
	repairedRaw := normalizeCardStatusInRaw(card.Raw)
	if repairedRaw == card.Raw {
		return false
	}
	card.Raw = repairedRaw
	return true
}

// decodeCard parses frontmatter from raw markdown into a CardRecord.
// The title parameter is the card identity (from the filename or frontmatter).
// The Raw field is set to the original content for verbatim round-trip.
func decodeCard(title, raw string) *CardRecord {
	card := &CardRecord{
		Title: title,
		Tags:  []string{},
		List:  []string{},
		Raw:   raw,
	}
	if strings.HasPrefix(raw, "---") {
		end := strings.Index(raw[3:], "---")
		if end >= 0 {
			front := raw[3 : 3+end]
			lines := strings.Split(front, "\n")
			for i := 0; i < len(lines); i++ {
				line := strings.TrimSpace(lines[i])
				if line == "" || line == "---" {
					continue
				}
				key, value, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				key = strings.TrimSpace(key)
				value = strings.TrimSpace(value)
				if value == "" {
					if key == "data" {
						card.Data, i = parseDataBlock(lines, i+1)
						continue
					}
					if collected, next := collectListItems(lines, i+1); collected != nil {
						value = "[" + strings.Join(collected, ", ") + "]"
						i = next - 1
					} else if next := skipIndentedBlock(lines, i+1); next > i+1 {
						i = next - 1
					}
				}
				switch key {
				case "id":
					if v := unquote(value); v != "" {
						card.Title = v
					}
				case "type":
					card.Type = unquote(value)
				case "tags":
					card.Tags = parseTags(value)
				case "created":
					card.Created = unquote(value)
				case "modified":
					card.Modified = unquote(value)
				case "due":
					card.Due = unquote(value)
				case "priority":
					card.Priority = unquote(value)
				case "status":
					card.Status = normalizeTaskStatus(unquote(value))
				case "parent":
					card.Parent = unquote(value)
				case "standalone":
					card.Standalone = strings.EqualFold(unquote(value), "true")
				case "list":
					card.List = parseTags(value)
				}
			}
			bodyStart := 3 + end + 3
			if bodyStart < len(raw) {
				card.Body = strings.TrimSpace(raw[bodyStart:])
			}
		}
	}
	return card
}

// ParseCardRaw exports decodeCard so other packages (workspace, tests) can
// parse a mono card's frontmatter without duplicating the parser. The returned
// CardRecord is a read-only snapshot; mutating it does not affect storage.
func ParseCardRaw(title, raw string) *CardRecord {
	return decodeCard(title, raw)
}

func unquote(s string) string {
	if len(s) >= 2 && ((s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"')) {
		return s[1 : len(s)-1]
	}
	return s
}

// ensureCardMeta guarantees that a card's raw markdown has a valid frontmatter
// block with id, type, tags, created, and modified. Missing fields are
// filled with sensible defaults (created/modified default to now) so that
// callers who supply only a body still produce a well-formed card. When no
// parent is declared but the card carries tags, the first tag is auto-filled
// as the parent (the parent∈tags invariant).
func ensureCardMeta(title, raw, now string) string {
	prefix := "---\nid: " + title + "\ntype: wiki\ntags: []\ncreated: " + quoteValue(now) + "\nmodified: " + quoteValue(now) + "\n---\n\n"
	body := strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "---") {
		return prefix + body
	}
	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return prefix + body
	}
	front := raw[3 : 3+end]
	lines := strings.Split(front, "\n")
	// Trim trailing empty lines so appended fields are not concatenated with
	// the closing --- delimiter.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	has := func(key string) bool {
		for _, line := range lines {
			if len(line) == 0 || line[0] == ' ' || line[0] == '\t' {
				continue
			}
			k, _, ok := strings.Cut(line, ":")
			if ok && strings.TrimSpace(k) == key {
				return true
			}
		}
		return false
	}
	ensure := func(line string) { lines = append(lines, line) }
	if !has("id") {
		ensure("id: " + title)
	}
	if !has("type") {
		ensure("type: wiki")
	}
	if !has("tags") {
		ensure("tags: []")
	}
	if !has("created") {
		ensure("created: " + quoteValue(now))
	}
	if !has("modified") {
		ensure("modified: " + quoteValue(now))
	}
	if !has("parent") {
		if first := firstTagLineValue(lines); first != "" {
			ensure("parent: " + first)
		}
	}
	return raw[:3] + strings.Join(lines, "\n") + "\n" + raw[3+end:]
}

// stripFrontmatterFields removes the given top-level keys from a card's
// frontmatter so that a subsequent ensureCardMeta re-fills them with the
// server clock. Used on the create path to discard caller-supplied timestamps.
func stripFrontmatterFields(raw string, keys ...string) string {
	if !strings.HasPrefix(raw, "---") {
		return raw
	}
	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return raw
	}
	drop := make(map[string]bool, len(keys))
	for _, k := range keys {
		drop[k] = true
	}
	front := raw[3 : 3+end]
	lines := strings.Split(front, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
			k, _, ok := strings.Cut(line, ":")
			if ok && drop[strings.TrimSpace(k)] {
				continue
			}
		}
		out = append(out, line)
	}
	return raw[:3] + strings.Join(out, "\n") + "\n" + raw[3+end:]
}

// firstTagLineValue scans frontmatter lines for a "tags:" entry and returns the
// first parsed tag, or "" when there are no tags.
func firstTagLineValue(lines []string) string {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		k, value, ok := strings.Cut(trimmed, ":")
		if !ok || strings.TrimSpace(k) != "tags" {
			continue
		}
		tags := parseTags(strings.TrimSpace(value))
		if len(tags) > 0 {
			return tags[0]
		}
		return ""
	}
	return ""
}

func quoteValue(s string) string {
	return `"` + s + `"`
}

func parseTags(value string) []string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")
	if value == "" {
		return []string{}
	}
	parts := strings.Split(value, ",")
	tags := make([]string, 0, len(parts))
	for _, p := range parts {
		p = unquote(strings.TrimSpace(p))
		if p != "" {
			tags = append(tags, p)
		}
	}
	return dedupStrings(tags)
}

// collectListItems reads consecutive "- item" lines starting at start.
func collectListItems(lines []string, start int) ([]string, int) {
	var items []string
	i := start
	for ; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "- ") && trimmed != "-" {
			break
		}
		item := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		item = unquote(item)
		if item != "" {
			items = append(items, item)
		}
	}
	return items, i
}

// skipIndentedBlock returns the index after a run of indented or blank lines.
func skipIndentedBlock(lines []string, start int) int {
	i := start
	for ; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		if lines[i] == trimmed {
			break
		}
	}
	return i
}

// ---------------------------------------------------------------------------
// In-memory cache helpers (concurrency-safe via s.mu)
// ---------------------------------------------------------------------------

// cloneCard returns a defensive copy of a CardRecord. The Tags and List
// slices and the Data map are copied so callers who mutate a returned card
// cannot corrupt the cached copy. This preserves the documented CardStore
// contract (see ParseCardRaw): mutating a returned record never affects
// storage — with a cache, that means never affecting the cached snapshot.
// Nested values inside Data are shared (deep copy of arbitrary any values is
// not attempted; callers treat Data as read-only).
func cloneCard(c *CardRecord) *CardRecord {
	if c == nil {
		return nil
	}
	cp := *c
	if c.Tags != nil {
		cp.Tags = make([]string, len(c.Tags))
		copy(cp.Tags, c.Tags)
	}
	if c.List != nil {
		cp.List = make([]string, len(c.List))
		copy(cp.List, c.List)
	}
	if c.Data != nil {
		cp.Data = make(map[string]any, len(c.Data))
		for k, v := range c.Data {
			cp.Data[k] = v
		}
	}
	return &cp
}

// cacheCard stores a single card in the card cache under write lock.
func (s *fsCardStore) cacheCard(card *CardRecord) {
	s.mu.Lock()
	s.cardCache[card.Title] = card
	s.mu.Unlock()
}

// cacheList stores the full list result under write lock. It also populates
// the per-card cache so subsequent Get calls are served from memory.
func (s *fsCardStore) cacheList(cards []*CardRecord) {
	s.mu.Lock()
	s.listCache = cards
	for _, c := range cards {
		s.cardCache[c.Title] = c
	}
	s.mu.Unlock()
}

// refreshFromDisk reads the card from disk, bypassing the in-memory cache,
// and updates the per-card and list caches with what it found. It returns a
// defensive copy. Save uses it to keep caches coherent with what was just
// written; the set_status verify loop uses it to observe concurrent external
// writers the fsnotify watcher has not processed yet — a cached read there
// would return our own bytes and mask the racer.
func (s *fsCardStore) refreshFromDisk(title string) (*CardRecord, error) {
	normalized, err := s.readCardByTitle(title)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Remove any case-variant cache entries.
	for k := range s.cardCache {
		if strings.EqualFold(k, normalized.Title) {
			delete(s.cardCache, k)
		}
	}
	s.cardCache[normalized.Title] = normalized

	if s.listCache == nil {
		return cloneCard(normalized), nil
	}
	found := false
	for i, c := range s.listCache {
		if strings.EqualFold(c.Title, normalized.Title) {
			s.listCache[i] = normalized
			found = true
			break
		}
	}
	if !found {
		s.listCache = append(s.listCache, normalized)
	}
	return cloneCard(normalized), nil
}

// invalidateCacheForSave is called after a successful Save. It reads back the
// just-written card, caches the decoded/repaired record and updates the list
// cache in place so a subsequent List does not need a full glob. The callers
// must hold the per-title write mutex for card.Title.
func (s *fsCardStore) invalidateCacheForSave(card *CardRecord) {
	if _, err := s.refreshFromDisk(card.Title); err != nil {
		// Fall back to full invalidation; next Get/List will rebuild from disk.
		s.invalidateAll()
	}
}

// invalidateCacheForDelete is called after a successful Delete. It removes the
// card from the per-card cache and the list cache in place.
func (s *fsCardStore) invalidateCacheForDelete(title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.cardCache {
		if strings.EqualFold(k, title) {
			delete(s.cardCache, k)
		}
	}
	if s.listCache == nil {
		return
	}
	for i, c := range s.listCache {
		if strings.EqualFold(c.Title, title) {
			s.listCache = append(s.listCache[:i], s.listCache[i+1:]...)
			return
		}
	}
}

// invalidateAll clears both caches entirely. Called when a directory-level
// exception occurs (e.g. a filename cannot be resolved to a card title).
func (s *fsCardStore) invalidateAll() {
	s.mu.Lock()
	s.cardCache = make(map[string]*CardRecord)
	s.listCache = nil
	cb := s.invalidateCallback
	s.mu.Unlock()
	if cb != nil {
		cb("bulk")
	}
}

// invalidateSingleCard invalidates the per-card cache entries reachable from
// the event file fileName (whose filename-derived id is title) and updates
// the list cache in place by re-reading the file. If the file no longer
// exists the entries are removed from both caches. Purging by address as well
// as by derived title matters for legacy-named files: the cache is keyed by
// the frontmatter id, which differs from the filename-derived id exactly when
// the file still sits at a folded-separator address. The per-card
// invalidation replaces the old invalidateAll approach; invalidateAll stays
// reserved for directory-level exceptions.
func (s *fsCardStore) invalidateSingleCard(title, fileName string) {
	addressMatch := func(id string) bool {
		return strings.EqualFold(cardFileName(id), fileName) ||
			strings.EqualFold(legacyFoldedAddress(id), fileName) ||
			strings.EqualFold(id, title)
	}
	// canonicalMissing reports whether the id has no live canonical file.
	// Metadata-only stat; treated as missing only on a definitive NotExist so
	// a transient Windows stat failure cannot evict a live entry.
	canonicalMissing := func(id string) bool {
		_, err := os.Stat(filepath.Join(s.wikiPath, cardFileName(id)))
		return os.IsNotExist(err)
	}
	// Remove card-cache entries that the changed file made stale. An entry is
	// stale when the event file is its own canonical address (the cached
	// content predates the change by definition) or when it has no live
	// canonical file. A legacy-address event must NOT evict an entry that
	// already migrated to a live canonical address.
	stale := func(id string) bool {
		if !addressMatch(id) {
			return false
		}
		return strings.EqualFold(cardFileName(id), fileName) || canonicalMissing(id)
	}
	s.mu.Lock()
	var staleKeys []string
	for k := range s.cardCache {
		if stale(k) {
			staleKeys = append(staleKeys, k)
		}
	}
	listCached := s.listCache != nil
	cb := s.invalidateCallback
	s.mu.Unlock()
	for _, k := range staleKeys {
		s.mu.Lock()
		delete(s.cardCache, k)
		s.mu.Unlock()
	}
	if !listCached {
		if cb != nil {
			cb("external")
		}
		return
	}

	// Re-read the event file outside the lock to avoid I/O under lock.
	card, err := s.readCardByTitle(title)
	if err != nil {
		// File gone — drop list entries that live only at this address.
		s.mu.Lock()
		for i := 0; i < len(s.listCache); i++ {
			c := s.listCache[i]
			if stale(c.Title) {
				s.listCache = append(s.listCache[:i], s.listCache[i+1:]...)
				i--
			}
		}
		cb = s.invalidateCallback
		s.mu.Unlock()
		if cb != nil {
			cb("external")
		}
		return
	}

	// Update or append in list cache. The card keeps its identity unless the
	// frontmatter id was edited externally, in which case the stale entry at
	// this address is dropped so the id change does not duplicate it.
	s.mu.Lock()
	found := false
	for i, c := range s.listCache {
		if strings.EqualFold(c.Title, card.Title) {
			s.listCache[i] = card
			found = true
			break
		}
	}
	if !found {
		s.listCache = append(s.listCache, card)
	}
	for i := 0; i < len(s.listCache); i++ {
		c := s.listCache[i]
		if !strings.EqualFold(c.Title, card.Title) && stale(c.Title) {
			s.listCache = append(s.listCache[:i], s.listCache[i+1:]...)
			i--
		}
	}
	s.cardCache[card.Title] = card
	cb = s.invalidateCallback
	s.mu.Unlock()
	if cb != nil {
		cb("external")
	}
}

// readCardByTitle reads and decodes a card from disk through the canonical
// and legacy address fallbacks of readCardRaw.
func (s *fsCardStore) readCardByTitle(title string) (*CardRecord, error) {
	data, err := s.readCardRaw(title)
	if err != nil {
		return nil, err
	}
	raw := normalizeLineEndings(string(data))
	card := decodeCard(title, raw)
	repairCardStatus(card)
	return card, nil
}

// Close stops the fsnotify watcher and releases resources. It is safe to call
// multiple times and safe to call when no watcher was started.
func (s *fsCardStore) Close() {
	s.watcherOnce.Do(func() {
		if s.watcher != nil {
			_ = s.watcher.Close()
			if s.watcherDone != nil {
				<-s.watcherDone
			}
		}
	})
}

// startCacheWatcher starts a goroutine that watches the wiki directory for
// external file changes (writes, creates, removes, renames by other processes
// or tools). Each .md event is resolved to a single card title and only that
// card's cache entry is invalidated; the list cache is updated in place. The
// full invalidateAll is reserved for directory-level exceptions.
//
// If the wiki directory does not exist yet or fsnotify is unavailable, the
// watcher is silently skipped — the cache still works for Save/Delete-driven
// invalidation; it just won't detect external edits until the store is
// recreated.
func (s *fsCardStore) startCacheWatcher() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return
	}
	// Ensure the wiki directory exists so we can watch it.
	if err := os.MkdirAll(s.wikiPath, 0o755); err != nil {
		_ = watcher.Close()
		return
	}
	if err := watcher.Add(s.wikiPath); err != nil {
		_ = watcher.Close()
		return
	}
	done := make(chan struct{})
	s.watcher = watcher
	s.watcherDone = done

	go func() {
		defer close(done)
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				// Only care about .md files in the wiki directory.
				if !strings.HasSuffix(event.Name, ".md") {
					continue
				}
				if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
					continue
				}
				name := filepath.Base(event.Name)
				title, _ := cardTitleFromFileName(name)
				if title == "" {
					// Degenerate filename: fall back to full invalidation.
					s.invalidateAll()
					continue
				}
				s.invalidateSingleCard(title, name)
			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}
			}
		}
	}()
}
