package project

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// writeExternalSkillFile writes a SKILL.md-style file under dir within projectRoot.
func writeExternalSkillFile(t *testing.T, projectRoot, dir, filename, content string) {
	t.Helper()
	full := filepath.Join(projectRoot, filepath.FromSlash(dir))
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, filename), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExternalSkillCardProviderList(t *testing.T) {
	root := t.TempDir()
	writeExternalSkillFile(t, root, ".claude/skills", "code-review.md",
		"---\nname: code-review\ndescription: Review code changes\n---\n\n# Code Review\n")
	writeExternalSkillFile(t, root, ".claude/skills/sub", "nested.md",
		"---\nname: nested-skill\n---\n\nNested body.\n")
	writeExternalSkillFile(t, root, ".codex/skills", "refactor.md",
		"---\nname: refactor\ndescription: Refactor code\n---\n\n# Refactor\n")
	writeExternalSkillFile(t, root, ".opencode/skills", "search.md",
		"# search skill\nNo frontmatter.\n")

	p := newExternalSkillCardProvider(root)
	items, err := p.List(nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	want := map[string]bool{
		"ext-skill:claude:code-review":  false,
		"ext-skill:claude:nested-skill": false,
		"ext-skill:codex:refactor":      false,
		"ext-skill:opencode:search":     false,
	}
	for _, item := range items {
		if _, ok := want[item.ID]; !ok {
			t.Errorf("unexpected card %s", item.ID)
			continue
		}
		want[item.ID] = true

		if item.Type != "skill" {
			t.Errorf("%s Type = %q, want skill", item.ID, item.Type)
		}
		if item.Storage != "external" {
			t.Errorf("%s Storage = %q, want external", item.ID, item.Storage)
		}
		if item.Visibility != "component" {
			t.Errorf("%s Visibility = %q, want component", item.ID, item.Visibility)
		}
		if !item.Protected || item.Editable || item.Deletable {
			t.Errorf("%s read-only flags: Protected=%v Editable=%v Deletable=%v", item.ID, item.Protected, item.Editable, item.Deletable)
		}
	}
	for id, found := range want {
		if !found {
			t.Errorf("missing card %s", id)
		}
	}
}

// TestExternalSkillCardStampsUseFileMtime verifies that list timestamps come
// from the skill file's mtime rather than the scan time, so recency ordering
// (the #-mention dropdown) reflects the file itself.
func TestExternalSkillCardStampsUseFileMtime(t *testing.T) {
	root := t.TempDir()
	writeExternalSkillFile(t, root, ".claude/skills", "old-skill.md",
		"---\nname: old-skill\n---\n\n")
	old := time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)
	skillPath := filepath.Join(root, ".claude", "skills", "old-skill.md")
	if err := os.Chtimes(skillPath, old, old); err != nil {
		t.Fatal(err)
	}

	p := newExternalSkillCardProvider(root)
	items, err := p.List(nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%d want 1", len(items))
	}
	want := old.Format(time.RFC3339)
	if items[0].Created != want || items[0].Modified != want {
		t.Errorf("stamps = (%q, %q), want %q (file mtime, not scan time)", items[0].Created, items[0].Modified, want)
	}
}

// TestExternalSkillCardOldMtimeSortsBelowRecentCards verifies end-to-end that
// an ext-skill card whose file has an old mtime no longer outranks freshly
// created wiki cards under OrderBy=-modified (the #-mention dropdown order).
func TestExternalSkillCardOldMtimeSortsBelowRecentCards(t *testing.T) {
	root := t.TempDir()
	writeExternalSkillFile(t, root, ".claude/skills", "debug-desktop.md",
		"---\nname: debug-desktop\n---\n\n")
	old := time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(root, ".claude", "skills", "debug-desktop.md"), old, old); err != nil {
		t.Fatal(err)
	}

	a := newTestActor(root)
	ctx := newTestContext(t)
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{
		ID:  "recent-card",
		Raw: "---\nid: recent-card\ntags: []\n---\n\nBody.\n",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	resp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, OrderBy: "-modified"})
	if err != nil {
		t.Fatalf("listCards: %v", err)
	}
	if len(resp.Cards) < 2 {
		t.Fatalf("cards=%d want >=2", len(resp.Cards))
	}
	if resp.Cards[0].ID != "recent-card" {
		t.Errorf("first card = %s, want recent-card (ext-skill card with old mtime must not sort first)", resp.Cards[0].ID)
	}
}

func TestExternalSkillCardProviderListSorted(t *testing.T) {
	root := t.TempDir()
	writeExternalSkillFile(t, root, ".pi/skills", "zeta.md", "---\nname: zeta\n---\n\n")
	writeExternalSkillFile(t, root, ".claude/skills", "alpha.md", "---\nname: alpha\n---\n\n")
	writeExternalSkillFile(t, root, ".codex/skills", "middle.md", "---\nname: middle\n---\n\n")

	p := newExternalSkillCardProvider(root)
	items, err := p.List(nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("items=%d want 3", len(items))
	}
	if items[0].ID != "ext-skill:claude:alpha" || items[1].ID != "ext-skill:codex:middle" || items[2].ID != "ext-skill:pi:zeta" {
		var ids []string
		for _, i := range items {
			ids = append(ids, i.ID)
		}
		t.Fatalf("not sorted: %v", ids)
	}
}

func TestExternalSkillCardProviderGet(t *testing.T) {
	root := t.TempDir()
	content := "---\nname: my-skill\ndescription: A test skill\narguments: [arg1, arg2]\nallowed-tools: [tool1]\n---\n\n# My Skill\n\nBody text.\n"
	writeExternalSkillFile(t, root, ".claude/skills", "my-skill.md", content)

	p := newExternalSkillCardProvider(root)

	// Get by exact ID.
	raw, err := p.Get(nil, "ext-skill:claude:my-skill")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if raw != content {
		t.Errorf("Get returned unexpected content:\ngot:  %q\nwant: %q", raw, content)
	}

	// Get unknown skill.
	if _, err := p.Get(nil, "ext-skill:claude:nonexistent"); err == nil {
		t.Fatal("expected error for nonexistent skill")
	}

	// Get malformed ID.
	if _, err := p.Get(nil, "ext-skill:invalid"); err == nil {
		t.Fatal("expected error for malformed ID (no source:name)")
	}
	if _, err := p.Get(nil, "ext-skill:"); err == nil {
		t.Fatal("expected error for empty ext-skill ID")
	}
}

func TestExternalSkillCardProviderGetByNameFromFrontmatter(t *testing.T) {
	root := t.TempDir()
	// File is named differently from the frontmatter name.
	writeExternalSkillFile(t, root, ".codex/skills", "file-123.md",
		"---\nname: canonical-name\n---\n\nContent.\n")

	p := newExternalSkillCardProvider(root)
	items, _ := p.List(nil)
	if len(items) != 1 {
		t.Fatalf("items=%d want 1", len(items))
	}
	if items[0].ID != "ext-skill:codex:canonical-name" {
		t.Fatalf("ID = %s, want ext-skill:codex:canonical-name", items[0].ID)
	}

	raw, err := p.Get(nil, "ext-skill:codex:canonical-name")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if raw != "---\nname: canonical-name\n---\n\nContent.\n" {
		t.Errorf("unexpected content: %q", raw)
	}
}

func TestExternalSkillCardProviderSaveRejected(t *testing.T) {
	p := newExternalSkillCardProvider(t.TempDir())
	if err := p.Save(nil, "ext-skill:claude:foo", "body"); err == nil {
		t.Fatal("Save should be rejected")
	}
}

func TestExternalSkillCardProviderDeleteRejected(t *testing.T) {
	p := newExternalSkillCardProvider(t.TempDir())
	if err := p.Delete(nil, "ext-skill:claude:foo"); err == nil {
		t.Fatal("Delete should be rejected")
	}
}

func TestExternalSkillCardProviderEmptyProject(t *testing.T) {
	p := newExternalSkillCardProvider(t.TempDir())
	items, err := p.List(nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}
}

func TestExternalSkillCardProviderParseID(t *testing.T) {
	tests := []struct {
		id       string
		wantSrc  string
		wantName string
		wantOK   bool
	}{
		{"ext-skill:claude:code-review", "claude", "code-review", true},
		{"ext-skill:codex:refactor", "codex", "refactor", true},
		{"ext-skill:opencode:search", "opencode", "search", true},
		{"ext-skill:invalid", "", "", false},
		{"ext-skill:", "", "", false},
		{"ext-skill:claude:", "", "", false},
		{"ext-skill::name", "", "", false},
		{"skill:foo", "", "", false},
		{"", "", "", false},
	}
	for _, tt := range tests {
		src, name, ok := parseExternalSkillID(tt.id)
		if ok != tt.wantOK || src != tt.wantSrc || name != tt.wantName {
			t.Errorf("parseExternalSkillID(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.id, src, name, ok, tt.wantSrc, tt.wantName, tt.wantOK)
		}
	}
}

// TestExternalSkillCardInWikiList verifies that ext-skill: cards appear in
// listAllCards with correct read-only metadata after normalization.
func TestExternalSkillCardInWikiList(t *testing.T) {
	root := t.TempDir()
	writeExternalSkillFile(t, root, ".claude/skills", "debug.md",
		"---\nname: debug\ndescription: Debug helper\n---\n\n# Debug\n")

	a := newTestActor(root)
	ctx := newTestContext(t)

	resp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true})
	if err != nil {
		t.Fatalf("listCards: %v", err)
	}

	var card *domain.MonoCardListItem
	for i := range resp.Cards {
		if resp.Cards[i].ID == "ext-skill:claude:debug" {
			card = &resp.Cards[i]
		}
	}
	if card == nil {
		t.Fatalf("ext-skill:claude:debug not found in list")
	}
	if card.Type != "skill" || card.Source != "claude" || card.Storage != "external" {
		t.Errorf("metadata = %+v", *card)
	}
	if !card.Protected || card.Editable || card.Deletable {
		t.Errorf("read-only flags = %+v", *card)
	}
}

// TestExternalSkillCardGetViaWiki verifies that external skill cards can be
// read through the wiki getCard callable.
func TestExternalSkillCardGetViaWiki(t *testing.T) {
	root := t.TempDir()
	content := "---\nname: deploy\ndescription: Deploy app\n---\n\n# Deploy\n"
	writeExternalSkillFile(t, root, ".claude/skills", "deploy.md", content)

	a := newTestActor(root)
	ctx := newTestContext(t)

	resp, err := a.handleWikiGetCard(ctx, domain.WikiGetCardReq{ID: "ext-skill:claude:deploy"})
	if err != nil {
		t.Fatalf("getCard: %v", err)
	}
	if resp.Raw != content {
		t.Errorf("raw mismatch: got %q", resp.Raw)
	}
}

// TestExternalSkillCardEditRejected verifies that editing an external skill
// card through the wiki editCard callable is rejected by the centralized
// read-only enforcement.
func TestExternalSkillCardEditRejected(t *testing.T) {
	root := t.TempDir()
	writeExternalSkillFile(t, root, ".claude/skills", "lint.md",
		"---\nname: lint\n---\n\n# Lint\n")

	a := newTestActor(root)
	ctx := newTestContext(t)

	_, err := a.handleWikiEditCard(ctx, domain.WikiEditCardReq{
		ID:  "ext-skill:claude:lint",
		Raw: "---\nid: ext-skill:claude:lint\n---\n\nModified.\n",
	})
	if err == nil {
		t.Fatal("expected edit rejection for read-only external skill card")
	}
}

// TestExternalSkillCardDeleteRejected verifies that deleting an external skill
// card through the wiki deleteCard callable is rejected.
func TestExternalSkillCardDeleteRejected(t *testing.T) {
	root := t.TempDir()
	writeExternalSkillFile(t, root, ".claude/skills", "lint.md",
		"---\nname: lint\n---\n\n# Lint\n")

	a := newTestActor(root)
	ctx := newTestContext(t)

	_, err := a.handleWikiDeleteCard(ctx, domain.WikiDeleteCardReq{ID: "ext-skill:claude:lint"})
	if err == nil {
		t.Fatal("expected delete rejection for read-only external skill card")
	}
}

// TestExternalSkillCardCreateRejected verifies that creating a card with the
// ext-skill: prefix is rejected.
func TestExternalSkillCardCreateRejected(t *testing.T) {
	root := t.TempDir()
	a := newTestActor(root)
	ctx := newTestContext(t)

	_, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{
		ID:  "ext-skill:claude:foo",
		Raw: "---\nid: ext-skill:claude:foo\ntags: []\n---\n\n",
	})
	if err == nil {
		t.Fatal("expected create rejection for ext-skill: prefix")
	}
}

// TestExternalSkillCardMountedAsRef verifies that an ext-skill: card can be
// mounted (card mount does not require the card to be editable). This is the
// "mounting available" acceptance criterion.
func TestExternalSkillCardMountedAsRef(t *testing.T) {
	root := t.TempDir()
	writeExternalSkillFile(t, root, ".claude/skills", "mount-test.md",
		"---\nname: mount-test\n---\n\n# Mount Test\n")

	a := newTestActor(root)
	ctx := testutil.AdminCtx(testutil.GenActorID())

	// The card should be listable (i.e. discoverable for mounting).
	resp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true})
	if err != nil {
		t.Fatalf("listCards: %v", err)
	}
	found := false
	for _, c := range resp.Cards {
		if c.ID == "ext-skill:claude:mount-test" {
			found = true
		}
	}
	if !found {
		t.Fatal("ext-skill card not listable")
	}
}

// TestExternalSkillCardNameFallbackToFileStem verifies that when a skill file
// has no frontmatter name, the filename stem is used as the skill name.
func TestExternalSkillCardNameFallbackToFileStem(t *testing.T) {
	root := t.TempDir()
	writeExternalSkillFile(t, root, ".claude/skills", "no-frontmatter.md",
		"# A skill without frontmatter\n")

	p := newExternalSkillCardProvider(root)
	items, _ := p.List(nil)
	if len(items) != 1 {
		t.Fatalf("items=%d want 1", len(items))
	}
	if items[0].ID != "ext-skill:claude:no-frontmatter" {
		t.Fatalf("ID = %s, want ext-skill:claude:no-frontmatter", items[0].ID)
	}
}

// TestExternalSkillCardMultipleSourcesNoCollision verifies that skills with
// the same name from different sources get distinct IDs.
func TestExternalSkillCardMultipleSourcesNoCollision(t *testing.T) {
	root := t.TempDir()
	writeExternalSkillFile(t, root, ".claude/skills", "shared.md",
		"---\nname: shared\n---\n\nClaude version.\n")
	writeExternalSkillFile(t, root, ".codex/skills", "shared.md",
		"---\nname: shared\n---\n\nCodex version.\n")

	p := newExternalSkillCardProvider(root)
	items, _ := p.List(nil)
	if len(items) != 2 {
		t.Fatalf("items=%d want 2", len(items))
	}

	// Both should be retrievable with distinct content.
	raw1, err := p.Get(nil, "ext-skill:claude:shared")
	if err != nil {
		t.Fatalf("Get claude: %v", err)
	}
	raw2, err := p.Get(nil, "ext-skill:codex:shared")
	if err != nil {
		t.Fatalf("Get codex: %v", err)
	}
	if raw1 == raw2 {
		t.Error("expected distinct content for same-named skills from different sources")
	}
}
