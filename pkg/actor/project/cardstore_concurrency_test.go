package project

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// The tests in this file are regression coverage for the fsCardStore write
// hardening (investigation card 卡片状态回滚根因调查): Get must never persist,
// set_status must validate its write and replay on lost races, and Save must
// replace files atomically via temp+rename.

// ---------------------------------------------------------------------------
// 1. Get repairs in memory only — never persists (no recursive Save)
// ---------------------------------------------------------------------------

func TestFSCardStoreGetRepairsWithoutPersisting(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, wikiDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, cardFileName("repair-card"))
	rawOnDisk := "---\nid: repair-card\ntype: task\nstatus: in-progress\ntags: []\n---\n\nBody.\n"
	// Bypass Save so no normalization lands on disk through the front door.
	if err := os.WriteFile(path, []byte(rawOnDisk), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newFSCardStore(dir)
	defer s.Close()

	card, err := s.Get("repair-card")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if card.Status != "doing" {
		t.Errorf("returned status = %q, want doing (in-memory repair)", card.Status)
	}
	if !strings.Contains(card.Raw, "status: doing") {
		t.Errorf("returned raw not repaired: %q", card.Raw)
	}
	if !card.Repaired {
		t.Error("returned card not marked Repaired")
	}

	// The whole point: Get is a read path and must not have written the
	// repair back. Otherwise a read racing a concurrent writer can revert
	// fresher content (the lost-update behind card status rollbacks).
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != rawOnDisk {
		t.Errorf("Get persisted the repair; disk changed to:\n%s", onDisk)
	}
}

func TestFSCardStoreListMarksRepairWithoutPersisting(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, wikiDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, cardFileName("repair-list"))
	rawOnDisk := "---\nid: repair-list\ntype: task\nstatus: in-progress\ntags: []\n---\n\nBody.\n"
	if err := os.WriteFile(path, []byte(rawOnDisk), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newFSCardStore(dir)
	defer s.Close()

	cards, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Fatalf("List = %d cards, want 1", len(cards))
	}
	if cards[0].Status != "doing" || !cards[0].Repaired {
		t.Errorf("List card status=%q repaired=%v, want doing/true", cards[0].Status, cards[0].Repaired)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != rawOnDisk {
		t.Errorf("List persisted the repair; disk changed to:\n%s", onDisk)
	}
}

// ---------------------------------------------------------------------------
// 2. set_status validate loop: replay after losing a race (调查卡序列 A)
// ---------------------------------------------------------------------------

// Deterministic reproduction of investigation sequence A: an edit_card body
// change lands between set_status's Save and its verification read. Before the
// fix, set_status returned success with stale content and the body was
// silently lost; after the fix, the mismatch is detected and the transition is
// replayed on top of the winner's version.
func TestSetStatusReplaysAfterConcurrentEdit(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	base := "---\nid: race-a\ntype: task\ntags: []\nstatus: todo\n---\n\nBase body.\n"
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "race-a", Raw: base}); err != nil {
		t.Fatalf("create: %v", err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	setStatusVerifyHook = func(cardID string) {
		if cardID != "race-a" {
			return
		}
		once.Do(func() {
			close(entered)
			<-release
		})
	}
	defer func() { setStatusVerifyHook = nil }()

	respCh := make(chan error, 1)
	go func() {
		_, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "race-a", Status: "doing"})
		respCh <- err
	}()

	<-entered // set_status has saved; we are inside the Save→verify window
	// A non-cooperating writer (another process, git reconcile) replaces the
	// card file behind the store's back — exactly the 调查卡序列A shape where
	// edit_card(body=总结A) lands between set_status's Save and its verify.
	// Direct file write because handleWikiEditCard would block on the
	// per-title lock this handler holds for its whole cycle.
	editorRaw := "---\nid: race-a\ntype: task\ntags: []\nstatus: todo\n---\n\n总结A body.\n"
	wikiFile := filepath.Join(tmp, wikiDir, cardFileName("race-a"))
	if err := os.WriteFile(wikiFile, []byte(editorRaw), 0o644); err != nil {
		t.Fatalf("concurrent edit: %v", err)
	}
	close(release)

	if err := <-respCh; err != nil {
		t.Fatalf("set_status: %v", err)
	}

	final, err := a.store.Get("race-a")
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != "doing" {
		t.Errorf("final status = %q, want doing", final.Status)
	}
	if !strings.Contains(final.Raw, "总结A body.") {
		t.Errorf("body silently lost — set_status clobbered the concurrent edit:\n%s", final.Raw)
	}
}

// A persistent racer that wins every Save→verify window must surface as a loud
// error after maxSetStatusAttempts, never as a silent drop of the transition.
func TestSetStatusRetriesExhaustedReportsError(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	base := "---\nid: race-b\ntype: task\ntags: []\nstatus: todo\n---\n\nBody.\n"
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "race-b", Raw: base}); err != nil {
		t.Fatalf("create: %v", err)
	}

	var mu sync.Mutex
	racerSeq := 0
	wikiFile := filepath.Join(tmp, wikiDir, cardFileName("race-b"))
	setStatusVerifyHook = func(cardID string) {
		if cardID != "race-b" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		racerSeq++
		data, err := os.ReadFile(wikiFile)
		if err != nil {
			return
		}
		winner := strings.TrimSuffix(string(data), "\n") + fmt.Sprintf("\n\nRacer-%d.\n", racerSeq)
		_ = os.WriteFile(wikiFile, []byte(winner), 0o644)
	}
	defer func() { setStatusVerifyHook = nil }()

	_, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "race-b", Status: "doing"})
	if err == nil {
		t.Fatal("expected loud error when a persistent racer wins every verify window")
	}
	if !strings.Contains(err.Error(), "concurrent writers") {
		t.Errorf("error should report concurrent writers, got: %v", err)
	}

	final, err := a.store.Get("race-b")
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != "doing" {
		t.Errorf("final status = %q, want doing (replays preserved the transition)", final.Status)
	}
	for i := 1; i <= racerSeq; i++ {
		if !strings.Contains(final.Raw, fmt.Sprintf("Racer-%d.", i)) {
			t.Errorf("racer write %d missing from final content:\n%s", i, final.Raw)
		}
	}
}

// M-goroutine hammer: one patch-mode editor composing markers while many
// set_status writers race on the same card, plus a reader asserting every
// observed on-disk state is structurally complete (no torn reads).
func TestConcurrentEditAndSetStatusNoSilentLoss(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	const (
		statusWriters = 8
		rounds        = 6
	)
	cardID := "hammer-card"
	base := "---\nid: hammer-card\ntype: task\ntags: []\nstatus: todo\n---\n\nBody.\n"
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: cardID, Raw: base}); err != nil {
		t.Fatalf("create: %v", err)
	}

	statuses := []string{"todo", "doing", "pending_review", "done"}
	errCh := make(chan error, statusWriters+1)
	var wg sync.WaitGroup

	// Single editor so patches compose onto fresh snapshots (edit-vs-edit
	// last-write-wins on bodies is inherent to full/partial replace semantics
	// and out of this fix's scope; the fix guarantees status writers never
	// drop body content). The anchor sits inside the body: applyCardEdit's
	// frontmatter/body split consumes one trailing delimiter newline, so
	// anchors must not span the boundary.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for r := 0; r < rounds; r++ {
			if _, err := a.handleWikiEditCard(ctx, domain.WikiEditCardReq{
				ID:        cardID,
				OldString: "Body.",
				NewString: "Marker-R" + fmt.Sprint(r) + ".\nBody.",
			}); err != nil {
				errCh <- fmt.Errorf("editor round %d: %w", r, err)
				return
			}
		}
	}()

	for w := 0; w < statusWriters; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				target := statuses[(w+r)%len(statuses)]
				if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: cardID, Status: target}); err != nil {
					errCh <- fmt.Errorf("writer %d round %d (%s): %w", w, r, target, err)
					return
				}
			}
		}(w)
	}

	// Concurrent reader: every observed state must be structurally complete —
	// atomic rename means readers never see a half-written card. Transient
	// Windows sharing violations are tolerated (readFileResilient heals them;
	// under this synthetic 9-writer storm the retry budget can theoretically
	// exhaust) but any structurally inconsistent read fails the test.
	done := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		valid := map[string]bool{}
		for _, st := range statuses {
			valid[st] = true
		}
		valid["backlog"] = true
		valid["blocked"] = true
		valid["cancelled"] = true
		for {
			select {
			case <-done:
				return
			default:
			}
			card, err := a.store.Get(cardID)
			switch {
			case err == nil:
				if card.Title != cardID || !valid[card.Status] || !strings.HasPrefix(card.Raw, "---\nid: "+cardID) {
					errCh <- fmt.Errorf("reader observed torn/inconsistent card: title=%q status=%q raw-prefix=%.40q", card.Title, card.Status, card.Raw)
					return
				}
			case err == ErrCardNotFound:
				// Fine: card exists on disk; only reachable pre-create.
			case isTransientFileError(err):
				// Mid-replace open collision; retry loop will heal real reads.
			default:
				errCh <- fmt.Errorf("reader: %w", err)
				return
			}
			time.Sleep(time.Microsecond)
		}
	}()

	wg.Wait()
	close(done)
	<-readerDone
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	final, err := a.store.Get(cardID)
	if err != nil {
		t.Fatal(err)
	}
	// No silent body loss: every composed marker must be visible in the final
	// state regardless of interleaving order.
	for r := 0; r < rounds; r++ {
		want := fmt.Sprintf("Marker-R%d.", r)
		if !strings.Contains(final.Raw, want) {
			t.Errorf("marker %q missing from final state (silent loss):\n%s", want, final.Raw)
		}
	}
	if !validTaskStatus(final.Status) {
		t.Errorf("final status %q is not canonical", final.Status)
	}
}

func validTaskStatus(s string) bool {
	switch s {
	case "backlog", "todo", "doing", "pending_review", "done", "blocked", "cancelled":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// 3. Atomic file replacement (temp + rename)
// ---------------------------------------------------------------------------

func TestWriteFileAtomicSingleWriter(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "card.md")

	if err := writeFileAtomic(target, []byte("---\nid: c\n---\n\nV1.\n")); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("---\nid: c\n---\n\nV1.\n")) {
		t.Errorf("target content = %q", got)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	// No temp residue after a successful rename: the deferred remove is a
	// no-op and the rename consumed the temp entry.
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp residue left behind: %s", e.Name())
		}
	}
}

func TestWriteFileAtomicTempNameIgnoredByMdFilters(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "card.md")
	// Same pattern writeFileAtomic uses: must not end in .md so List's *.md
	// glob and the fsnotify watcher's .md suffix filter ignore it while the
	// temp exists mid-write.
	f, err := os.CreateTemp(dir, "."+filepath.Base(target)+".*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	if strings.HasSuffix(name, ".md") {
		t.Errorf("temp name %q would trip .md filters (spurious cache invalidation)", name)
	}
	if filepath.Dir(name) != dir {
		t.Errorf("temp %q must live in the target directory for same-volume rename", name)
	}
}

func TestWriteFileAtomicConcurrentWritersNoTearing(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hot.md")

	const writers = 16
	payloads := make([][]byte, writers)
	for i := range payloads {
		line := fmt.Sprintf("---\nid: hot\nstatus: doing\n---\n\nPayload-%03d ", i)
		payloads[i] = []byte(line + strings.Repeat(fmt.Sprintf("%04d-", i), 8192)) // ~40KB
	}

	var wg sync.WaitGroup
	errCh := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := writeFileAtomic(target, payloads[i]); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	// Atomic replace ⇒ the final bytes are exactly ONE writer's payload, never
	// an interleaved mix (a torn result would fail this equality).
	intact := false
	for _, p := range payloads {
		if bytes.Equal(got, p) {
			intact = true
			break
		}
	}
	if !intact {
		t.Errorf("final file matches no single payload — write tore or mixed:\n%.120q...", got)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "hot.md" {
			t.Errorf("unexpected residue in wiki dir: %s", e.Name())
		}
	}
}

// Save end-to-end through the store: content lands intact and the fsnotify
// invalidation still fires afterwards (rename targets the .md path), so
// external-change detection behaves as before.
func TestFSCardStoreSaveAtomicEndToEnd(t *testing.T) {
	tmp := t.TempDir()
	s := newFSCardStore(filepath.Join(tmp, wikiDir))
	defer s.Close()

	raw1 := "---\nid: e2e\ntags: [t]\n---\n\nFirst.\n"
	if err := s.Save(&CardRecord{Title: "e2e", Raw: raw1}); err != nil {
		t.Fatal(err)
	}
	card, err := s.Get("e2e")
	if err != nil || card.Body != "First." {
		t.Fatalf("first save/read: %#v, %v", card, err)
	}

	raw2 := "---\nid: e2e\ntags: [t]\n---\n\nSecond.\n"
	if err := s.Save(&CardRecord{Title: "e2e", Raw: raw2}); err != nil {
		t.Fatal(err)
	}

	// Save invalidated the cache; the next read must observe the new content
	// (proves invalidateCacheForSave still runs on the atomic path).
	deadline := time.Now().Add(5 * time.Second)
	for {
		card, err = s.Get("e2e")
		if err != nil {
			t.Fatal(err)
		}
		if card.Body == "Second." && strings.Contains(card.Raw, "Second.") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("atomic save not visible: %#v", card)
		}
		time.Sleep(5 * time.Millisecond)
	}

	onDisk, err := os.ReadFile(filepath.Join(tmp, wikiDir, cardFileName("e2e")))
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != raw2 {
		t.Errorf("disk = %q, want exact saved raw", onDisk)
	}
}
