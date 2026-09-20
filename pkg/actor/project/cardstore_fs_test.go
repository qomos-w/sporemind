package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFSCardStoreColonCardIDRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	s := newFSCardStore(filepath.Join(tmp, wikiDir))
	id := "prompt:profile:project.coder"
	if err := s.Save(&CardRecord{Title: id, Raw: "---\nid: prompt:profile:project.coder\n---\n\nYou are a coder.\n"}); err != nil {
		t.Fatal(err)
	}
	card, err := s.Get(id)
	if err != nil || card.Title != id {
		t.Fatalf("get %q = %#v, %v", id, card, err)
	}
	cards, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || cards[0].Title != id {
		t.Fatalf("list = %#v, want card %q", cards, id)
	}
}

func TestNormalizeFrontmatterCloser(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			name: "glued closer gets its own line",
			in:   "---\nid: a\ntype: task\ndata:\n  category: \"review\"---\n\nBody.",
			want: "---\nid: a\ntype: task\ndata:\n  category: \"review\"\n---\n\nBody.",
		},
		{
			name: "healthy closer unchanged",
			in:   "---\nid: a\n---\n\nBody.",
			want: "---\nid: a\n---\n\nBody.",
		},
		{
			name: "no frontmatter unchanged",
			in:   "# Just a heading\n",
			want: "# Just a heading\n",
		},
		{
			name: "body dashes untouched",
			in:   "---\nid: a\n---\n\n--- Generated go.mod ---\n",
			want: "---\nid: a\n---\n\n--- Generated go.mod ---\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeFrontmatterCloser(tc.in); got != tc.want {
				t.Errorf("normalizeFrontmatterCloser(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFSCardStoreSaveNormalizesGluedCloser(t *testing.T) {
	tmp := t.TempDir()
	s := newFSCardStore(filepath.Join(tmp, wikiDir))
	glued := "---\nid: glued\ntype: task\ndata:\n  category: \"code\"---\n\nBody."
	if err := s.Save(&CardRecord{Title: "glued", Raw: glued}); err != nil {
		t.Fatal(err)
	}
	card, err := s.Get("glued")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card.Raw, "category: \"code\"\n---\n") {
		t.Errorf("saved raw still has glued closer:\n%s", card.Raw)
	}
}

func TestFSCardStoreDeduplicatesLegacySkillCard(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, wikiDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := "---\ntags: [__builtin_skill__]\n---\n\nlegacy\n"
	canonical := "---\ntags: [component, skill]\n---\n\ncanonical\n"
	if err := os.WriteFile(filepath.Join(dir, "skill$skill-creator.md"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, cardFileName("skill:skill-creator")), []byte(canonical), 0o644); err != nil {
		t.Fatal(err)
	}
	cards, err := newFSCardStore(filepath.Join(tmp, wikiDir)).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || cards[0].Title != "skill:skill-creator" || !strings.Contains(cards[0].Raw, "component, skill") {
		t.Fatalf("cards = %#v", cards)
	}
}

func TestFSCardStoreCRUD(t *testing.T) {
	tmp := t.TempDir()
	s := newFSCardStore(filepath.Join(tmp, wikiDir))

	raw := "---\nid: c1\ntags: [a]\ncreated: \"2026-01-01T00:00:00Z\"\nmodified: \"2026-01-01T00:00:00Z\"\n---\n\nBody.\n"

	// Save
	if err := s.Save(&CardRecord{Title: "c1", Raw: raw}); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Get
	card, err := s.Get("c1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if card.Title != "c1" {
		t.Errorf("card identity = %q, want c1", card.Title)
	}
	if card.Raw != raw {
		t.Errorf("raw mismatch")
	}

	// List
	cards, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("list count = %d, want 1", len(cards))
	}

	// Get not-found
	_, err = s.Get("missing")
	if !errors.Is(err, ErrCardNotFound) {
		t.Errorf("get missing = %v, want ErrCardNotFound", err)
	}

	// Delete
	if err := s.Delete("c1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, wikiDir, "c1.md")); !os.IsNotExist(err) {
		t.Errorf("expected file removed")
	}

	// Delete missing is not an error
	if err := s.Delete("c1"); err != nil {
		t.Errorf("delete missing = %v, want nil", err)
	}
}

func TestFSCardStoreNormalizesCRLF(t *testing.T) {
	tmp := t.TempDir()
	s := newFSCardStore(filepath.Join(tmp, wikiDir))

	raw := "---\r\nid: crlf\r\ntags: []\r\n---\r\n\r\nLine one\r\nLine two\r\n"
	path := filepath.Join(tmp, wikiDir, "crlf.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("write CRLF file: %v", err)
	}
	want := strings.ReplaceAll(raw, "\r\n", "\n")
	card, err := s.Get("crlf")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if card.Raw != want {
		t.Errorf("loaded raw = %q, want %q", card.Raw, want)
	}
	cards, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(cards) != 1 || cards[0].Raw != want {
		t.Errorf("listed raw = %q, want %q", cards[0].Raw, want)
	}
	if err := s.Save(card); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != want {
		t.Errorf("stored raw = %q, want %q", string(data), want)
	}
}

func TestFSCardStoreListEmpty(t *testing.T) {
	tmp := t.TempDir()
	s := newFSCardStore(filepath.Join(tmp, wikiDir))

	cards, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(cards) != 0 {
		t.Fatalf("list count = %d, want 0", len(cards))
	}
}

func TestFSCardStoreDataBlockIsolation(t *testing.T) {
	tmp := t.TempDir()
	s := newFSCardStore(filepath.Join(tmp, wikiDir))

	raw := `---
id: data
tags: [real]
created: 2026-01-01T00:00:00Z
modified: 2026-01-02T00:00:00Z
data:
  title: Should Not Override
  tags: [fake]
  count: 42
---
Body
`
	if err := s.Save(&CardRecord{Title: "data", Raw: raw}); err != nil {
		t.Fatalf("save: %v", err)
	}
	card, err := s.Get("data")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if card.Title != "data" {
		t.Errorf("card identity = %q, want data", card.Title)
	}
	if len(card.Tags) != 1 || card.Tags[0] != "real" {
		t.Errorf("tags = %v, want [real] (data leaked)", card.Tags)
	}
}

func TestDecodeCardData(t *testing.T) {
	card := decodeCard("scheduler:test", "---\ndata:\n  executor: agent:coder\n  reviewer: agent:reviewer\n---\n\nbody")
	if card.Data["executor"] != "agent:coder" {
		t.Fatalf("executor = %v", card.Data["executor"])
	}
	if card.Data["reviewer"] != "agent:reviewer" {
		t.Fatalf("reviewer = %v", card.Data["reviewer"])
	}
}

// TestDecodeCardNestedVisual reproduces the exact raw payload that the frontend
// CardVisualDialog produces (data.visual = {icon, accent, color?}). The server
// must decode the nested object so the persisted visual re-appears when the
// dialog is reopened and in the topology card rendering.
func TestDecodeCardNestedVisual(t *testing.T) {
	raw := "---\nid: my-card\ntags: []\nlist: []\ncreated: \"2026-01-01\"\nmodified: \"2026-01-01\"\ndata:\n  visual:\n    icon: rocket\n    accent: blue\n    color: \"#2563eb\"\n---\n\nbody"
	card := decodeCard("my-card", raw)
	visual, ok := card.Data["visual"].(map[string]any)
	if !ok {
		t.Fatalf("visual not decoded as map: %#v", card.Data["visual"])
	}
	if visual["icon"] != "rocket" {
		t.Errorf("icon = %v, want rocket", visual["icon"])
	}
	if visual["accent"] != "blue" {
		t.Errorf("accent = %v, want blue", visual["accent"])
	}
	if visual["color"] != "#2563eb" {
		t.Errorf("color = %v, want #2563eb", visual["color"])
	}
}

// TestDecodeCardNestedVisualUnquotedHex reproduces what the frontend actually
// emits: quoteIfNeeded does not quote a leading '#', so the icon color is
// written as `color: #2563eb` (unquoted). The server must still read the hex
// value rather than treating '#' as a YAML comment.
func TestDecodeCardNestedVisualUnquotedHex(t *testing.T) {
	raw := "---\ndata:\n  visual:\n    icon: rocket\n    accent: blue\n    color: #2563eb\n---\n\nbody"
	card := decodeCard("my-card", raw)
	visual, ok := card.Data["visual"].(map[string]any)
	if !ok {
		t.Fatalf("visual not decoded as map: %#v", card.Data["visual"])
	}
	if visual["color"] != "#2563eb" {
		t.Errorf("color = %v, want #2563eb (unquoted hex must survive)", visual["color"])
	}
}

func TestParseTagsDedup(t *testing.T) {
	tags := parseTags("[agent, __builtin_agent__, agent, agent]")
	if len(tags) != 2 || tags[0] != "agent" || tags[1] != "__builtin_agent__" {
		t.Errorf("tags = %v, want [agent __builtin_agent__] (duplicates not removed)", tags)
	}
	// Single tag stays intact.
	single := parseTags("[agent]")
	if len(single) != 1 || single[0] != "agent" {
		t.Errorf("single tag = %v, want [agent]", single)
	}
	// Empty stays empty.
	empty := parseTags("[]")
	if len(empty) != 0 {
		t.Errorf("empty tags = %v, want []", empty)
	}
}

func TestDecodeCardDedupTags(t *testing.T) {
	card := decodeCard("note", "---\nid: note\ntags: [agent, agent, note]\n---\n\nBody")
	if len(card.Tags) != 2 || card.Tags[0] != "agent" || card.Tags[1] != "note" {
		t.Errorf("tags = %v, want [agent note] (duplicates not removed)", card.Tags)
	}
}

func TestDecodeCardIgnoresLegacyTitleFrontmatter(t *testing.T) {
	// A legacy "title:" frontmatter line no longer has special meaning — the
	// field was removed from the reader so it is treated as an unknown key and
	// silently ignored. Identity is always derived from the decodeCard filename
	// parameter, never from frontmatter.
	card := decodeCard("ordinary", "---\ntitle: Fancy Title\ntags: []\ncreated: \"2026-01-01\"\nmodified: \"2026-01-01\"\n---\n\nBody")
	if card.Title != "ordinary" {
		t.Errorf("identity = %q, want ordinary (frontmatter title must not override filename)", card.Title)
	}
	if _, ok := card.Data["builtin_title"]; ok {
		t.Errorf("builtin_title must not be stored: %#v", card.Data["builtin_title"])
	}
}

func TestDecodeCardFrontmatterIdOverridesFilename(t *testing.T) {
	// frontmatter id: is the authoritative identity. The filename is derived
	// by escaping "/" → "_" which is irreversible, so the frontmatter id must
	// be trusted to restore the original title.
	card := decodeCard("task_I_O_schema", "---\nid: task I/O schema\ntags: []\n---\n\nBody")
	if card.Title != "task I/O schema" {
		t.Errorf("identity = %q, want \"task I/O schema\" (frontmatter id must override escaped filename)", card.Title)
	}
}

func TestDecodeCardFrontmatterIdEmptyFallsBackToFilename(t *testing.T) {
	// When frontmatter has no id: field, identity falls back to the filename.
	card := decodeCard("fallback-id", "---\ntitle: Something\ntags: []\n---\n\nBody")
	if card.Title != "fallback-id" {
		t.Errorf("identity = %q, want fallback-id (filename fallback when no frontmatter id)", card.Title)
	}
}

func TestDecodeCardSystemUsesFilenameIdentity(t *testing.T) {
	card := decodeCard("__builtin_summary__", "---\nid: __builtin_summary__\ntags: []\ncreated: \"2026-01-01\"\nmodified: \"2026-01-01\"\n---\n\nBody")
	if card.Title != "__builtin_summary__" {
		t.Errorf("system identity = %q, want __builtin_summary__", card.Title)
	}
}

func TestEnsureCardMetaInjectsFrontmatterForBareBody(t *testing.T) {
	out := ensureCardMeta("my-card", "Just a body, no frontmatter.", "2026-07-24T00:00:00Z")
	card := decodeCard("my-card", out)
	if card.Title != "my-card" {
		t.Errorf("title = %q, want my-card", card.Title)
	}
	if len(card.Tags) != 0 {
		t.Errorf("tags = %v, want []", card.Tags)
	}
	if card.Created != "2026-07-24T00:00:00Z" {
		t.Errorf("created = %q", card.Created)
	}
	if card.Modified != "2026-07-24T00:00:00Z" {
		t.Errorf("modified = %q", card.Modified)
	}
	if !strings.Contains(card.Body, "Just a body") {
		t.Errorf("body lost: %q", card.Body)
	}
}

func TestEnsureCardMetaFillsMissingFieldsKeepsProvided(t *testing.T) {
	raw := "---\nid: kept-id\ncreated: 2025-01-01T00:00:00Z\n---\n\nBody here"
	out := ensureCardMeta("kept-id", raw, "2026-07-24T00:00:00Z")
	card := decodeCard("kept-id", out)
	if card.Title != "kept-id" {
		t.Errorf("identity = %q, want kept-id", card.Title)
	}
	if card.Created != "2025-01-01T00:00:00Z" {
		t.Errorf("created = %q, want preserved value", card.Created)
	}
	if card.Modified != "2026-07-24T00:00:00Z" {
		t.Errorf("modified = %q, want injected now", card.Modified)
	}
	if len(card.Tags) != 0 {
		t.Errorf("tags = %v, want []", card.Tags)
	}
}

func TestEnsureCardMetaParentNotConcatenatedWithDelimiter(t *testing.T) {
	raw := "---\nid: c\ntype: task\ntags: [work, urgent]\n---\n\nBody."
	out := ensureCardMeta("c", raw, "2026-07-27T00:00:00Z")
	// The auto-filled parent must be on its own line, not concatenated with
	// the closing --- delimiter (which would produce "parent: work---").
	if strings.Contains(out, "parent: work---") {
		t.Fatalf("parent value concatenated with closing delimiter:\n%s", out)
	}
	// Verify the closing --- is on its own line after the parent field.
	if !strings.Contains(out, "parent: work\n---") {
		t.Fatalf("expected parent on its own line followed by ---, got:\n%s", out)
	}
}

func TestStripFrontmatterFields(t *testing.T) {
	raw := "---\nid: c\ntype: wiki\ntags: []\ncreated: \"2020-01-01T00:00:00Z\"\nmodified: \"2020-01-01T00:00:00Z\"\nstatus: backlog\n---\n\nBody."
	out := stripFrontmatterFields(raw, "created", "modified")
	card := decodeCard("c", out)
	if card.Created == "2020-01-01T00:00:00Z" {
		t.Errorf("created not stripped: %q", card.Created)
	}
	if card.Modified == "2020-01-01T00:00:00Z" {
		t.Errorf("modified not stripped: %q", card.Modified)
	}
	if card.Status != "backlog" {
		t.Errorf("unrelated field status should survive: %q", card.Status)
	}
	if !strings.Contains(out, "Body.") {
		t.Errorf("body should survive: %q", out)
	}
}

func TestStripFrontmatterFieldsNoFrontmatter(t *testing.T) {
	raw := "Just a body."
	out := stripFrontmatterFields(raw, "created")
	if out != raw {
		t.Errorf("bare body should pass through unchanged: %q", out)
	}
}

func TestDedupStrings(t *testing.T) {
	if got := dedupStrings([]string{"a", "b", "a", "c", "b"}); len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("dedupStrings = %v, want [a b c]", got)
	}
	if got := dedupStrings([]string{"x"}); len(got) != 1 || got[0] != "x" {
		t.Errorf("dedupStrings single = %v, want [x]", got)
	}
	if got := dedupStrings([]string{}); len(got) != 0 {
		t.Errorf("dedupStrings empty = %v, want []", got)
	}
}

func TestFSCardStoreGetLegacySkillFallback(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, wikiDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := "---\ndescription: \"does stuff\"\n---\n\nSkill body.\n"
	if err := os.WriteFile(filepath.Join(dir, "skill$my-skill.md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newFSCardStore(dir)

	// Get with canonical "skill:" ID should find the legacy "skill$" file.
	card, err := s.Get("skill:my-skill")
	if err != nil {
		t.Fatalf("Get skill:my-skill: %v", err)
	}
	if card.Title != "skill:my-skill" {
		t.Errorf("card ID = %q, want skill:my-skill", card.Title)
	}
	if !strings.Contains(card.Raw, "Skill body") {
		t.Errorf("expected skill body in raw, got %q", card.Raw)
	}

	// Non-existent card returns ErrCardNotFound.
	if _, err := s.Get("skill:nonexistent"); err == nil {
		t.Fatal("expected error for nonexistent skill")
	}
}

func TestFSCardStoreRepairsTaskStatusOnFirstRead(t *testing.T) {
	tmp := t.TempDir()
	s := newFSCardStore(filepath.Join(tmp, wikiDir))
	raw := "---\nid: task-1\ntype: task\nstatus: in-progress\ntags: []\n---\n\nBody."
	if err := s.Save(&CardRecord{Title: "task-1", Raw: raw}); err != nil {
		t.Fatal(err)
	}
	card, err := s.Get("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if card.Status != "doing" {
		t.Errorf("Get status = %q, want doing", card.Status)
	}
	if !strings.Contains(card.Raw, "status: doing") {
		t.Errorf("Get raw did not persist repaired status: %q", card.Raw)
	}
	cards, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || cards[0].Status != "doing" {
		t.Errorf("List status = %q, want doing", cards[0].Status)
	}
}

// ---------------------------------------------------------------------------
// Cache tests: cache hit, post-save invalidation, external-change invalidation
// ---------------------------------------------------------------------------

func TestFSCardStoreCacheHit(t *testing.T) {
	tmp := t.TempDir()
	s := newFSCardStore(filepath.Join(tmp, wikiDir))
	defer s.Close()

	raw := "---\nid: cache-hit\ntags: [t]\n---\n\nOriginal body.\n"
	if err := s.Save(&CardRecord{Title: "cache-hit", Raw: raw}); err != nil {
		t.Fatal(err)
	}

	// First Get populates the cache (white-box check).
	card1, err := s.Get("cache-hit")
	if err != nil {
		t.Fatal(err)
	}
	s.mu.RLock()
	_, cached := s.cardCache["cache-hit"]
	s.mu.RUnlock()
	if !cached {
		t.Fatalf("first Get did not populate the card cache")
	}

	// Second Get is served from cache and returns a defensive copy with equal
	// content.
	card2, err := s.Get("cache-hit")
	if err != nil {
		t.Fatal(err)
	}
	if card2.Raw != card1.Raw || !reflect.DeepEqual(card2.Tags, card1.Tags) {
		t.Errorf("cache hit returned different content: got %q / %v, want %q / %v",
			card2.Raw, card2.Tags, card1.Raw, card1.Tags)
	}
	if card2 == card1 {
		t.Errorf("cache hit returned the cached pointer; expected a defensive copy")
	}

	// Mutating the returned copy must not corrupt the cache.
	card2.Raw = "---\nid: cache-hit\n---\n\nMUTATED.\n"
	card3, err := s.Get("cache-hit")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(card3.Raw, "MUTATED") {
		t.Errorf("caller mutation leaked into the cache: %q", card3.Raw)
	}
}

func TestFSCardStoreCacheListHit(t *testing.T) {
	tmp := t.TempDir()
	s := newFSCardStore(filepath.Join(tmp, wikiDir))
	defer s.Close()

	if err := s.Save(&CardRecord{Title: "a", Raw: "---\nid: a\ntags: []\n---\n\nA.\n"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(&CardRecord{Title: "b", Raw: "---\nid: b\ntags: []\n---\n\nB.\n"}); err != nil {
		t.Fatal(err)
	}

	// First List populates the cache (white-box check).
	cards1, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(cards1) != 2 {
		t.Fatalf("list count = %d, want 2", len(cards1))
	}
	s.mu.RLock()
	listCached := s.listCache != nil
	s.mu.RUnlock()
	if !listCached {
		t.Fatalf("first List did not populate the list cache")
	}

	// Second List is served from cache with equal content.
	cards2, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(cards2) != 2 {
		t.Fatalf("cached list count = %d, want 2", len(cards2))
	}
	for i := range cards2 {
		if cards1[i].Title != cards2[i].Title || cards1[i].Raw != cards2[i].Raw {
			t.Errorf("cached list entry %d differs: %q vs %q", i, cards1[i].Raw, cards2[i].Raw)
		}
	}

	// Mutating the returned slice/records must not corrupt the cache.
	cards2[0].Raw = "---\nid: a\n---\n\nMUTATED.\n"
	cards3, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cards3[0].Raw, "MUTATED") {
		t.Errorf("caller mutation leaked into the list cache: %q", cards3[0].Raw)
	}
}

func TestFSCardStoreSaveUpdatesCache(t *testing.T) {
	tmp := t.TempDir()
	s := newFSCardStore(filepath.Join(tmp, wikiDir))
	defer s.Close()

	original := "---\nid: inval\nTags: []\ntags: []\n---\n\nOriginal.\n"
	if err := s.Save(&CardRecord{Title: "inval", Raw: original}); err != nil {
		t.Fatal(err)
	}

	// Populate cache via Get.
	if _, err := s.Get("inval"); err != nil {
		t.Fatal(err)
	}

	// Save a new version — cache should be updated in place with the new content.
	updated := "---\nid: inval\ntags: []\n---\n\nUpdated.\n"
	if err := s.Save(&CardRecord{Title: "inval", Raw: updated}); err != nil {
		t.Fatal(err)
	}

	card, err := s.Get("inval")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(card.Raw, "Updated.") {
		t.Errorf("post-save Get returned stale cache: %q", card.Raw)
	}
}

func TestFSCardStoreSaveBackfillsListCache(t *testing.T) {
	tmp := t.TempDir()
	s := newFSCardStore(filepath.Join(tmp, wikiDir))
	defer s.Close()

	// Populate and verify list cache after first save.
	if err := s.Save(&CardRecord{Title: "c1", Raw: "---\nid: c1\ntags: []\n---\n\nC1.\n"}); err != nil {
		t.Fatal(err)
	}
	cards, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Fatalf("list count = %d, want 1", len(cards))
	}

	// Save a second card — list cache should be updated in place, not nil.
	if err := s.Save(&CardRecord{Title: "c2", Raw: "---\nid: c2\ntags: []\n---\n\nC2.\n"}); err != nil {
		t.Fatal(err)
	}

	s.mu.RLock()
	listCacheStillValid := s.listCache != nil
	s.mu.RUnlock()
	if !listCacheStillValid {
		t.Fatal("Save invalidated the list cache instead of backfilling it")
	}

	cards, err = s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 2 {
		t.Errorf("post-save list count = %d, want 2", len(cards))
	}
}

func TestFSCardStoreDeleteInvalidatesCache(t *testing.T) {
	tmp := t.TempDir()
	s := newFSCardStore(filepath.Join(tmp, wikiDir))
	defer s.Close()

	if err := s.Save(&CardRecord{Title: "del", Raw: "---\nid: del\ntags: []\n---\n\nDel.\n"}); err != nil {
		t.Fatal(err)
	}

	// Populate caches.
	if _, err := s.Get("del"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(); err != nil {
		t.Fatal(err)
	}

	// Delete should invalidate both caches.
	if err := s.Delete("del"); err != nil {
		t.Fatal(err)
	}

	// Get should return ErrCardNotFound (cache cleared, file gone).
	if _, err := s.Get("del"); !errors.Is(err, ErrCardNotFound) {
		t.Errorf("post-delete Get = %v, want ErrCardNotFound", err)
	}

	// List should reflect the deletion.
	cards, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 0 {
		t.Errorf("post-delete list count = %d, want 0", len(cards))
	}
}

// TestFSCardStoreExternalWriteInvalidatesCache verifies that when a file is
// modified externally (bypassing Save), the fsnotify watcher detects the
// change and invalidates the cache so the next Get reads fresh content from
// disk.
func TestFSCardStoreExternalWriteInvalidatesCache(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fsnotify-based test in short mode")
	}
	tmp := t.TempDir()
	s := newFSCardStore(filepath.Join(tmp, wikiDir))
	defer s.Close()

	original := "---\nid: ext\ntags: []\n---\n\nOriginal.\n"
	if err := s.Save(&CardRecord{Title: "ext", Raw: original}); err != nil {
		t.Fatal(err)
	}

	// Populate the cache.
	if _, err := s.Get("ext"); err != nil {
		t.Fatal(err)
	}

	// Modify the file externally (bypassing Save).
	path := filepath.Join(tmp, wikiDir, "ext.md")
	updated := "---\nid: ext\ntags: []\n---\n\nExternally modified.\n"
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait for the fsnotify watcher to process the event. We poll until the
	// cache reflects the new content or give up after a timeout.
	deadline := time.Now().Add(3 * time.Second)
	var card *CardRecord
	for time.Now().Before(deadline) {
		card, _ = s.Get("ext")
		if card != nil && strings.Contains(card.Raw, "Externally modified.") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if card == nil || !strings.Contains(card.Raw, "Externally modified.") {
		t.Errorf("external write did not invalidate cache; raw = %q", card.Raw)
	}
}

// TestFSCardStoreExternalCreateInvalidatesList verifies that when a new .md
// file is created externally, the fsnotify watcher invalidates the list cache
// so the next List picks up the new card.
func TestFSCardStoreExternalCreateInvalidatesList(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fsnotify-based test in short mode")
	}
	tmp := t.TempDir()
	s := newFSCardStore(filepath.Join(tmp, wikiDir))
	defer s.Close()

	if err := s.Save(&CardRecord{Title: "existing", Raw: "---\nid: existing\ntags: []\n---\n\nExisting.\n"}); err != nil {
		t.Fatal(err)
	}

	// Populate list cache.
	cards, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Fatalf("list count = %d, want 1", len(cards))
	}

	// Create a new card file externally.
	path := filepath.Join(tmp, wikiDir, "new-card.md")
	if err := os.WriteFile(path, []byte("---\nid: new-card\ntags: []\n---\n\nNew card.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait for the watcher to invalidate the list cache.
	deadline := time.Now().Add(3 * time.Second)
	var count int
	for time.Now().Before(deadline) {
		cards, err = s.List()
		if err != nil {
			t.Fatal(err)
		}
		count = len(cards)
		if count == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if count != 2 {
		t.Errorf("external create did not invalidate list cache; count = %d, want 2", count)
	}
}

// TestFSCardStoreExternalDeleteInvalidatesCache verifies that when a .md file
// is removed externally, the fsnotify watcher invalidates the cache so the
// next Get returns ErrCardNotFound.
func TestFSCardStoreExternalDeleteInvalidatesCache(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fsnotify-based test in short mode")
	}
	tmp := t.TempDir()
	s := newFSCardStore(filepath.Join(tmp, wikiDir))
	defer s.Close()

	if err := s.Save(&CardRecord{Title: "gone", Raw: "---\nid: gone\ntags: []\n---\n\nGone.\n"}); err != nil {
		t.Fatal(err)
	}

	// Populate cache.
	if _, err := s.Get("gone"); err != nil {
		t.Fatal(err)
	}

	// Delete the file externally.
	path := filepath.Join(tmp, wikiDir, "gone.md")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	// Wait for the watcher to invalidate.
	deadline := time.Now().Add(3 * time.Second)
	var err error
	for time.Now().Before(deadline) {
		_, err = s.Get("gone")
		if errors.Is(err, ErrCardNotFound) {
			return // success
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("external delete did not invalidate cache; Get err = %v, want ErrCardNotFound", err)
}

// TestFSCardStoreCloseIsIdempotent verifies that Close can be called multiple
// times without panicking.
func TestFSCardStoreCloseIsIdempotent(t *testing.T) {
	tmp := t.TempDir()
	s := newFSCardStore(filepath.Join(tmp, wikiDir))
	s.Close()
	s.Close() // must not panic
}

// TestFSCardStoreConcurrentAccess hammers the store with parallel Get/List/
// Save/Delete calls while the fsnotify watcher concurrently invalidates the
// cache. Run with -race to verify the mutex protection.
//
// The deleter goroutine operates on a disjoint title range from the
// readers/writers: on Windows a concurrent os.Remove racing with an open for
// read/write on the same path fails with a sharing violation at the OS level
// (a pre-existing property of fsCardStore, unrelated to the cache). Disjoint
// ranges keep the stress test focused on cache concurrency.
func TestFSCardStoreConcurrentAccess(t *testing.T) {
	tmp := t.TempDir()
	s := newFSCardStore(filepath.Join(tmp, wikiDir))
	defer s.Close()

	const titles = 8
	for i := 0; i < titles; i++ {
		raw := fmt.Sprintf("---\nid: card-%d\ntags: []\n---\n\nBody %d.\n", i, i)
		if err := s.Save(&CardRecord{Title: fmt.Sprintf("card-%d", i), Raw: raw}); err != nil {
			t.Fatal(err)
		}
	}
	half := titles / 2

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				switch g {
				case 0: // readers on lower half (never deleted)
					title := fmt.Sprintf("card-%d", i%half)
					if _, err := s.Get(title); err != nil && !errors.Is(err, ErrCardNotFound) {
						t.Errorf("concurrent Get: %v", err)
						return
					}
				case 1: // listers (List tolerates transient per-file read errors)
					if _, err := s.List(); err != nil {
						t.Errorf("concurrent List: %v", err)
						return
					}
				case 2: // writers on lower half
					title := fmt.Sprintf("card-%d", i%half)
					raw := fmt.Sprintf("---\nid: %s\ntags: []\n---\n\nGen %d.\n", title, i)
					if err := s.Save(&CardRecord{Title: title, Raw: raw}); err != nil {
						t.Errorf("concurrent Save: %v", err)
						return
					}
				case 3: // deleters + re-creators on upper half
					title := fmt.Sprintf("card-%d", half+i%half)
					if i%3 == 0 {
						_ = s.Delete(title)
					} else {
						raw := fmt.Sprintf("---\nid: %s\ntags: []\n---\n\nReborn %d.\n", title, i)
						_ = s.Save(&CardRecord{Title: title, Raw: raw})
					}
				}
			}
		}(g)
	}
	wg.Wait()
}

// TestFSCardStoreCaseInsensitiveDedup verifies that a single physical file
// whose frontmatter id differs in case from the on-disk filename (common on
// Windows) produces only one entry in List().
func TestFSCardStoreCaseInsensitiveDedup(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, wikiDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a file whose on-disk name is lowercase but whose frontmatter id
	// is uppercase — the classic ghost-entry scenario.
	raw := "---\nid: MyCard\ntags: []\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "mycard.md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	cards, err := newFSCardStore(dir).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Fatalf("expected 1 card, got %d: %+v", len(cards), cards)
	}
}

// TestFSCardStoreSaveRenamesCaseVariant verifies that Save renames an existing
// file whose on-disk name differs only in case from the canonical
// cardFileName(card.Title), ensuring a single stable path.
func TestFSCardStoreSaveRenamesCaseVariant(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, wikiDir)
	s := newFSCardStore(dir)
	// Create a card with lowercase id.
	raw := "---\nid: testcard\ntags: []\n---\n\nbody\n"
	if err := s.Save(&CardRecord{Title: "testcard", Raw: raw}); err != nil {
		t.Fatal(err)
	}
	// Manually rename the file to uppercase (simulating an old ghost entry).
	oldPath := filepath.Join(dir, "testcard.md")
	newPath := filepath.Join(dir, "TestCard.md")
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	// Save with the canonical (lowercase) title — should rename back.
	if err := s.Save(&CardRecord{Title: "testcard", Raw: raw}); err != nil {
		t.Fatal(err)
	}
	// The file must now be at the canonical lowercase path.
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("expected file at canonical path %s, got error: %v", oldPath, err)
	}
}

// TestFSCardStoreSaveAvoidsFullGlob verifies that after a Save, List() is
// served from the in-place-updated list cache and does not perform another
// filepath.Glob.
func TestFSCardStoreSaveAvoidsFullGlob(t *testing.T) {
	tmp := t.TempDir()
	s := newFSCardStore(filepath.Join(tmp, wikiDir))
	defer s.Close()

	if err := s.Save(&CardRecord{Title: "a", Raw: "---\nid: a\ntags: []\n---\n\nA.\n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(); err != nil {
		t.Fatal(err)
	}
	globsAfterFirst := s.globCount.Load()

	if err := s.Save(&CardRecord{Title: "b", Raw: "---\nid: b\ntags: []\n---\n\nB.\n"}); err != nil {
		t.Fatal(err)
	}

	cards, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 2 {
		t.Fatalf("list count = %d, want 2", len(cards))
	}
	if s.globCount.Load() != globsAfterFirst {
		t.Errorf("Save triggered a full glob; glob count = %d, want %d",
			s.globCount.Load(), globsAfterFirst)
	}
}

// TestFSCardStoreExternalWriteInvalidatesSingleCard verifies that an external
// write to one .md file only invalidates that card, leaving other cached
// entries untouched.
func TestFSCardStoreExternalWriteInvalidatesSingleCard(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fsnotify-based test in short mode")
	}
	tmp := t.TempDir()
	s := newFSCardStore(filepath.Join(tmp, wikiDir))
	defer s.Close()

	if err := s.Save(&CardRecord{Title: "a", Raw: "---\nid: a\ntags: []\n---\n\nA.\n"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(&CardRecord{Title: "b", Raw: "---\nid: b\ntags: []\n---\n\nB.\n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(); err != nil {
		t.Fatal(err)
	}
	globsBefore := s.globCount.Load()

	path := filepath.Join(tmp, wikiDir, "a.md")
	updated := "---\nid: a\ntags: []\n---\n\nExternally modified.\n"
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if card, err := s.Get("a"); err == nil && strings.Contains(card.Raw, "Externally modified.") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	card, err := s.Get("a")
	if err != nil || !strings.Contains(card.Raw, "Externally modified.") {
		t.Errorf("external write not reflected in A; err=%v raw=%q", err, card.Raw)
	}

	cards, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 2 {
		t.Fatalf("list count = %d, want 2", len(cards))
	}
	if s.globCount.Load() != globsBefore {
		t.Errorf("external write triggered full list glob; glob count = %d, want %d",
			s.globCount.Load(), globsBefore)
	}
}

// TestFSCardStoreExternalDeleteInvalidatesListCache verifies that an external
// delete is reflected in both Get (ErrCardNotFound) and List (card removed)
// without requiring a full glob.
func TestFSCardStoreExternalDeleteInvalidatesListCache(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fsnotify-based test in short mode")
	}
	tmp := t.TempDir()
	s := newFSCardStore(filepath.Join(tmp, wikiDir))
	defer s.Close()

	if err := s.Save(&CardRecord{Title: "a", Raw: "---\nid: a\ntags: []\n---\n\nA.\n"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(&CardRecord{Title: "b", Raw: "---\nid: b\ntags: []\n---\n\nB.\n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(); err != nil {
		t.Fatal(err)
	}
	globsBefore := s.globCount.Load()

	path := filepath.Join(tmp, wikiDir, "a.md")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		cards, err := s.List()
		if err != nil {
			t.Fatal(err)
		}
		if len(cards) == 1 && cards[0].Title == "b" {
			if _, err := s.Get("a"); errors.Is(err, ErrCardNotFound) {
				if s.globCount.Load() != globsBefore {
					t.Errorf("external delete triggered full list glob; glob count = %d, want %d",
						s.globCount.Load(), globsBefore)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	cards, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	var getErr error
	_, getErr = s.Get("a")
	t.Errorf("external delete did not invalidate list cache; count = %d, cards = %+v; Get(a) err = %v",
		len(cards), cards, getErr)
}

func TestCardFileNameRoundTrip(t *testing.T) {
	ids := []string{
		"A/B", "A_B", "A\\B", "My Card", "my card", "A%B", "A%2FB",
		"A.md", "A", "x%41y", "a\tb", "a\nb", "skill:X", "prompt:profile:foo",
		"card 域镜像装载", "中文/卡片", "a?b*c", "a|b<c>d\"e", "CON",
	}
	for _, id := range ids {
		file := cardFileName(id)
		got, _ := cardTitleFromFileName(file)
		if got != id {
			t.Errorf("round trip %q: file %q decoded to %q", id, file, got)
		}
	}
}

func TestCardFileNameInjective(t *testing.T) {
	// Distinct ids must map to distinct files; folded collisions ("A/B" vs
	// "A_B") were the original silent-overwrite defect.
	ids := []string{"A/B", "A_B", "A\\B", "A%2FB", "A.md", "A", "A%2F.md", "a b", "a_b"}
	seen := make(map[string]string)
	for _, id := range ids {
		file := cardFileName(id)
		if other, dup := seen[file]; dup {
			t.Errorf("ids %q and %q share file %q", other, id, file)
		}
		seen[file] = id
	}
}

func TestFSCardStoreSaveRejectsFoldedIDCollision(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, wikiDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A file written by an older store under the folded mapping: address
	// "A_B.md", frontmatter id "A/B". A fresh Save of id "A_B" targets the
	// same file and must fail instead of silently destroying the first card.
	if err := os.WriteFile(filepath.Join(dir, "A_B.md"), []byte("---\nid: A/B\ntags: []\n---\n\nSlash.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newFSCardStore(dir)
	defer s.Close()

	err := s.Save(&CardRecord{Title: "A_B", Raw: "---\nid: A_B\ntags: []\n---\n\nUnderscore.\n"})
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("save A_B err = %v, want collision refusal", err)
	}
	card, err := s.Get("A/B")
	if err != nil || !strings.Contains(card.Raw, "Slash.") {
		t.Fatalf("A/B damaged after refused save; card = %+v, err = %v", card, err)
	}
}

func TestFSCardStoreDotMDSuffixIDRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	s := newFSCardStore(filepath.Join(tmp, wikiDir))
	defer s.Close()

	if err := s.Save(&CardRecord{Title: "A.md", Raw: "---\nid: A.md\ntags: []\n---\n\nDotMD.\n"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(&CardRecord{Title: "A", Raw: "---\nid: A\ntags: []\n---\n\nPlain.\n"}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get("A.md"); err != nil || !strings.Contains(got.Raw, "DotMD.") {
		t.Fatalf("Get(A.md) = %+v, %v", got, err)
	}
	if got, err := s.Get("A"); err != nil || !strings.Contains(got.Raw, "Plain.") {
		t.Fatalf("Get(A) = %+v, %v", got, err)
	}
	cards, err := s.List()
	if err != nil || len(cards) != 2 {
		t.Fatalf("list = %+v, %v; want 2 cards", cards, err)
	}
}

func TestFSCardStoreLegacyFoldedAddressAdoptedAndMigrated(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, wikiDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A file written by an older store: folded address, frontmatter id kept
	// the true identity.
	if err := os.WriteFile(filepath.Join(dir, "A_B.md"), []byte("---\nid: A/B\ntags: []\n---\n\nLegacy.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newFSCardStore(dir)
	defer s.Close()

	card, err := s.Get("A/B")
	if err != nil || !strings.Contains(card.Raw, "Legacy.") {
		t.Fatalf("Get(A/B) through legacy address = %+v, %v", card, err)
	}
	if err := s.Save(&CardRecord{Title: "A/B", Raw: "---\nid: A/B\ntags: []\n---\n\nMigrated.\n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, cardFileName("A/B"))); err != nil {
		t.Errorf("canonical address missing after save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "A_B.md")); !os.IsNotExist(err) {
		t.Errorf("legacy address still present after save: %v", err)
	}
	cards, err := s.List()
	if err != nil || len(cards) != 1 || cards[0].Title != "A/B" {
		t.Fatalf("list = %+v, %v; want single A/B", cards, err)
	}
}

func TestFSCardStoreLegacyAddressOfDifferentIDSurvives(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, wikiDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// "A_B.md" legitimately stores id "A_B"; saving "A/B" must not delete it.
	if err := os.WriteFile(filepath.Join(dir, "A_B.md"), []byte("---\nid: A_B\ntags: []\n---\n\nUnderscore.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newFSCardStore(dir)
	defer s.Close()

	if err := s.Save(&CardRecord{Title: "A/B", Raw: "---\nid: A/B\ntags: []\n---\n\nSlash.\n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "A_B.md")); err != nil {
		t.Fatalf("unrelated legacy address removed: %v", err)
	}
	cards, err := s.List()
	if err != nil || len(cards) != 2 {
		t.Fatalf("list = %+v, %v; want 2 cards", cards, err)
	}
}

func TestFSCardStoreExternalDeleteOfLegacyAddressInvalidatesCache(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping fsnotify-based test in short mode")
	}
	tmp := t.TempDir()
	dir := filepath.Join(tmp, wikiDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "A_B.md"), []byte("---\nid: A/B\ntags: []\n---\n\nLegacy.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newFSCardStore(dir)
	defer s.Close()

	if _, err := s.List(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "A_B.md")); err != nil {
		t.Fatal(err)
	}

	// The cache entry is keyed by the frontmatter id "A/B" while the event
	// filename resolves to "A_B"; invalidation must bridge the two.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := s.Get("A/B"); errors.Is(err, ErrCardNotFound) {
			cards, err := s.List()
			if err != nil {
				t.Fatal(err)
			}
			if len(cards) != 0 {
				t.Errorf("list still shows deleted card: %+v", cards)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("external delete of legacy-addressed file left stale cache entry")
}
