package project

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/actor/pluginhost"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestDecodeCardFrontmatter(t *testing.T) {
	raw := `---
id: hello
tags: [kanban-task, a, b, c]
created: 2026-01-01T00:00:00Z
modified: 2026-01-02T00:00:00Z
due: 2026-01-03T00:00:00Z
priority: high
status: doing
parent: parent-card
---
Body line 1
Body line 2
`
	card := decodeCard("hello", raw)
	if card.Title != "hello" {
		t.Errorf("identity = %q, want hello", card.Title)
	}
	if len(card.Tags) != 4 || card.Tags[0] != "kanban-task" || card.Tags[3] != "c" {
		t.Errorf("tags = %v, want [kanban-task a b c]", card.Tags)
	}
	if card.Priority != "high" {
		t.Errorf("priority = %q, want high", card.Priority)
	}
	if card.Status != "doing" {
		t.Errorf("status = %q, want doing", card.Status)
	}
	if card.Parent != "parent-card" {
		t.Errorf("parent = %q, want parent-card", card.Parent)
	}
}

func TestDecodeCardNormalizesTaskStatus(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{"in_progress", "doing"},
		{"In_Progress", "doing"},
		{"completed", "done"},
		{"Completed", "done"},
		{"open", "todo"},
		{"pending", "todo"},
		{"doing", "doing"},
		{"blocked", "blocked"},
		{"frozen", "frozen"},
		{"", ""},
	}
	for _, c := range cases {
		raw := "---\nid: t\ntype: task\ntags: []\nstatus: " + c.input + "\n---\n\nBody."
		card := decodeCard("t", raw)
		if card.Status != c.want {
			t.Errorf("decode status %q: got %q, want %q", c.input, card.Status, c.want)
		}
	}
}

func TestDecodeCardMultilineTags(t *testing.T) {
	raw := `---
id: multi
tags:
  - idea
  - draft
created: 2026-01-01T00:00:00Z
modified: 2026-01-02T00:00:00Z
---
Body
`
	card := decodeCard("multi", raw)
	if len(card.Tags) != 2 || card.Tags[0] != "idea" || card.Tags[1] != "draft" {
		t.Errorf("tags = %v, want [idea draft]", card.Tags)
	}
	if card.Title != "multi" {
		t.Errorf("identity = %q, want multi", card.Title)
	}
}

func newTestActor(tmp string) *Actor {
	a := &Actor{
		actorID:           "test-actor",
		initialPath:       tmp,
		store:             newFSCardStore(filepath.Join(tmp, wikiDir)),
		externalProviders: []ExternalCardProvider{newPluginExternalCardProvider(), newExternalSkillCardProvider(tmp)},
		persistStore:      persist.NewFSPersist(filepath.Join(tmp, ".sporecode")),
	}
	a.seedBuiltinCards()
	a.initWorkflowTopoDirtyTracking()
	if err := a.ensureConfigCardSeed(); err != nil {
		panic(err)
	}
	if err := a.updateConfigCard(func(c *configCard) {
		c.Roots = []domain.RootDirEntry{{Name: "root", Path: tmp}}
	}); err != nil {
		panic(err)
	}
	return a
}

func newTestContext(t *testing.T) actor.Context {
	return testutil.AdminCtx(testutil.GenActorID())
}

// newTestContextWithWorkspaceMounts returns a FakeCtx wired with a fake
// workspace service that responds to "workspace.list_project" with the given
// mount list. Non-dev-app mounts are returned as-is so callers can also test
// filtering behaviour.
func newTestContextWithWorkspaceMounts(t *testing.T, mounts []domain.ProjectRef) *testutil.FakeCtx {
	t.Helper()
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name != "workspace" {
			return nil, false
		}
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
			if callID != "workspace.list_project" {
				return nil
			}
			return domain.ProjectRefListResp{Items: mounts}
		}), true
	}
	return ctx
}

func TestComponentAndPromptTagNodesAreSeeded(t *testing.T) {
	a := newTestActor(t.TempDir())
	component, err := a.store.Get(builtinComponentID)
	if err != nil {
		t.Fatalf("component tag node missing: %v", err)
	}
	if hasTag(component.Tags, "toc") {
		t.Fatalf("component tag node must not hang under toc: %+v", component.Tags)
	}
	prompt, err := a.store.Get(builtinPromptID)
	if err != nil {
		t.Fatalf("prompt tag node missing: %v", err)
	}
	if len(prompt.Tags) != 1 || prompt.Tags[0] != "component" {
		t.Fatalf("prompt tag node must hang under component: %+v", prompt.Tags)
	}
}

func TestBuiltinSkillCardsAreSeeded(t *testing.T) {
	a := newTestActor(t.TempDir())
	a.seedBuiltinSkillCards()
	for _, id := range []string{"skill:skill-creator", "skill:plan-module"} {
		card, err := a.store.Get(id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if card.Body == "" || !strings.Contains(card.Body, "#") {
			t.Fatalf("%s body was not seeded: %q", id, card.Body)
		}
		if card.Data["componentKind"] != "skill" || card.Data["source"] != "builtin" {
			t.Fatalf("%s metadata = %#v", id, card.Data)
		}
		if !strings.Contains(card.Raw, "tags: [component, skill]") {
			t.Fatalf("%s raw missing skill tags: %q", id, card.Raw)
		}
	}
}

func TestWikiCRUD(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	ctx := newTestContext(t)

	raw := `---
id: card1
type: wiki
tags: []
created: 2026-01-01T00:00:00Z
modified: 2026-01-01T00:00:00Z
---
Hello
`

	// Create
	createResp, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "card1", Raw: raw})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if createResp.Card.ID != "card1" {
		t.Errorf("create card = %+v", createResp.Card)
	}
	if createResp.Card.Type != "wiki" {
		t.Errorf("create card type = %q, want wiki", createResp.Card.Type)
	}
	if _, err := os.Stat(filepath.Join(tmp, wikiDir, "card1.md")); err != nil {
		t.Errorf("expected file to exist: %v", err)
	}

	// List all (default strips builtin virtual mount nodes)
	listResp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var testCard *domain.MonoCardListItem
	for i := range listResp.Cards {
		if listResp.Cards[i].ID == "card1" {
			testCard = &listResp.Cards[i]
		}
	}
	if testCard == nil {
		t.Fatalf("card1 not found in list of %d cards", len(listResp.Cards))
	}
	if testCard.ID != "card1" {
		t.Errorf("list title = %q, want card1", testCard.ID)
	}

	// list_all_cards must expose metadata only; read card bodies through get_card.
	if testCard.Raw != "" {
		t.Errorf("list returned card body")
	}
	if testCard.Data != nil {
		t.Errorf("list returned card data: %#v", testCard.Data)
	}

	// Get
	getResp, err := a.handleWikiGetCard(ctx, domain.WikiGetCardReq{ID: "card1"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(getResp.Raw, "Hello") {
		t.Errorf("get raw body mismatch: %q", getResp.Raw)
	}
	if strings.Contains(getResp.Raw, "2026-01-01T00:00:00Z") {
		t.Errorf("caller-supplied timestamp should be overwritten: %q", getResp.Raw)
	}
	_, err = a.handleWikiGetCard(ctx, domain.WikiGetCardReq{ID: "missing-card"})
	if err == nil || !strings.Contains(err.Error(), `project.wiki.getCard "missing-card": card not found`) {
		t.Fatalf("missing card error lacks request title: %v", err)
	}

	// Update
	updatedRaw := `---
id: card1
type: task
tags: [x]
created: 2026-01-01T00:00:00Z
modified: 2026-01-02T00:00:00Z
---
Updated body
`
	updateResp, err := a.handleWikiEditCard(ctx, domain.WikiEditCardReq{ID: "card1", Raw: updatedRaw})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updateResp.Card.ID != "card1" {
		t.Errorf("update title = %q, want card1", updateResp.Card.ID)
	}
	if updateResp.Card.Type != "task" {
		t.Errorf("update card type = %q, want task", updateResp.Card.Type)
	}

	// Delete
	_, err = a.handleWikiDeleteCard(ctx, domain.WikiDeleteCardReq{ID: "card1"})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, wikiDir, "card1.md")); !os.IsNotExist(err) {
		t.Errorf("expected file to not exist")
	}
}

func TestWikiGetCardsBatch(t *testing.T) {
	a := newTestActor(t.TempDir())
	ctx := newTestContext(t)
	for _, id := range []string{"card-a", "card-b"} {
		raw := "---\nid: " + id + "\ntype: wiki\ntags: []\n---\nBody " + id + "."
		if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: id, Raw: raw}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	resp, err := a.handleWikiGetCardsBatch(ctx, domain.WikiGetCardsBatchReq{Ids: []string{"card-a", "", "missing", "card-b"}})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if len(resp.Cards) != 3 {
		t.Fatalf("cards = %d, want 3 (empty id skipped)", len(resp.Cards))
	}
	if resp.Cards[0].ID != "card-a" || !resp.Cards[0].Found || !strings.Contains(resp.Cards[0].Raw, "Body card-a") {
		t.Errorf("card-a entry = %+v", resp.Cards[0])
	}
	if resp.Cards[1].ID != "missing" || resp.Cards[1].Found || resp.Cards[1].Raw != "" {
		t.Errorf("missing entry should be Found=false with empty raw, got %+v", resp.Cards[1])
	}
	if resp.Cards[2].ID != "card-b" || !resp.Cards[2].Found {
		t.Errorf("card-b entry = %+v", resp.Cards[2])
	}
	if !strings.Contains(resp.Cards[2].Raw, "id: card-b") {
		t.Errorf("card-b raw should carry frontmatter: %q", resp.Cards[2].Raw)
	}
}

func TestWikiCreateCardOverwritesCallerTimestamps(t *testing.T) {
	a := newTestActor(t.TempDir())
	ctx := newTestContext(t)

	raw := "---\nid: fake-ts\ntype: wiki\ntags: []\ncreated: 2020-01-01T00:00:00Z\nmodified: 2020-01-01T00:00:00Z\n---\n\nBody."
	resp, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "fake-ts", Raw: raw})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.Card.Created == "2020-01-01T00:00:00Z" {
		t.Errorf("created not overwritten: %q", resp.Card.Created)
	}
	if resp.Card.Modified == "2020-01-01T00:00:00Z" {
		t.Errorf("modified not overwritten: %q", resp.Card.Modified)
	}
	if resp.Card.Created != resp.Card.Modified {
		t.Errorf("created %q != modified %q on create", resp.Card.Created, resp.Card.Modified)
	}
}

func TestWikiSaveNormalizesTaskStatus(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	createRaw := "---\nid: task-card\ntype: task\ntags: []\nstatus: in_progress\n---\n\nBody."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "task-card", Raw: createRaw}); err != nil {
		t.Fatalf("create: %v", err)
	}
	getResp, err := a.handleWikiGetCard(ctx, domain.WikiGetCardReq{ID: "task-card"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(getResp.Raw, "status: doing") {
		t.Errorf("create did not normalize status in raw: %q", getResp.Raw)
	}

	updateRaw := "---\nid: task-card\ntype: task\ntags: []\nstatus: completed\n---\n\nBody."
	if _, err := a.handleWikiEditCard(ctx, domain.WikiEditCardReq{ID: "task-card", Raw: updateRaw}); err != nil {
		t.Fatalf("update: %v", err)
	}
	getResp, err = a.handleWikiGetCard(ctx, domain.WikiGetCardReq{ID: "task-card"})
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if !strings.Contains(getResp.Raw, "status: done") {
		t.Errorf("update did not normalize status in raw: %q", getResp.Raw)
	}
}

func TestProjectCardMountUsesWikiCardCRUD(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	a.persistStore = persist.NewFSPersist(t.TempDir())
	ctx := newTestContext(t)
	raw := "---\nid: project-knowledge\ntags: [knowledge]\n---\n\nProject truth.\n"
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "project-knowledge", Raw: raw}); err != nil {
		t.Fatalf("create project card: %v", err)
	}
	mounted, err := a.handleProjectCardMount(ctx, gen.ProjectCardMountReq{Ref: gen.CardRef{ID: "project-knowledge"}})
	if err != nil {
		t.Fatalf("mount created project card: %v", err)
	}
	if mounted.Ref.Scope != "project" {
		t.Fatalf("mount scope = %q, want project", mounted.Ref.Scope)
	}
	listed, err := a.handleProjectCardList(ctx, gen.ProjectCardListReq{})
	if err != nil || len(listed.Refs) != 1 || listed.Refs[0].ID != "project-knowledge" {
		t.Fatalf("mounted refs = %+v, err=%v", listed.Refs, err)
	}
	if _, err := a.handleProjectCardUnmount(ctx, gen.ProjectCardUnmountReq{ID: "project-knowledge"}); err != nil {
		t.Fatalf("unmount project card: %v", err)
	}
	if _, err := a.handleWikiDeleteCard(ctx, domain.WikiDeleteCardReq{ID: "project-knowledge"}); err != nil {
		t.Fatalf("delete after unmount: %v", err)
	}
}
func TestWikiDataBlockIsolation(t *testing.T) {
	// A data block with keys that collide with known fields (title, tags)
	// must not corrupt the parsed card record.
	raw := `---
id: data-card
tags: [real]
created: 2026-01-01T00:00:00Z
modified: 2026-01-02T00:00:00Z
data:
  title: Should Not Override
  tags: [fake]
  count: 42
  url: https://example.com
---
Body
`
	card := decodeCard("data-card", raw)
	if card.Title != "data-card" {
		t.Errorf("identity = %q, want data-card", card.Title)
	}
	if len(card.Tags) != 1 || card.Tags[0] != "real" {
		t.Errorf("tags = %v, want [real] (data block leaked)", card.Tags)
	}
	// Raw must preserve the full file including data block
	if !strings.Contains(card.Raw, "data:") {
		t.Errorf("raw should contain data block")
	}
}

func TestWikiBuiltinCardSeeding(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	ctx := newTestContext(t)

	// List triggers seeding
	_, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	// Builtin TOC card should exist
	if _, err := os.Stat(filepath.Join(tmp, wikiDir, "toc.md")); err != nil {
		t.Errorf("expected builtin card file to exist: %v", err)
	}

	// Cannot delete builtin card
	_, err = a.handleWikiDeleteCard(ctx, domain.WikiDeleteCardReq{ID: "toc"})
	if err == nil {
		t.Errorf("expected error when deleting builtin card")
	}
}

func TestWikiSkillBuiltinSeeded(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// List triggers seeding; virtual mount nodes require IncludeBuiltin.
	listResp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, IncludeBuiltin: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	var skill *domain.MonoCardListItem
	for i := range listResp.Cards {
		c := &listResp.Cards[i]
		if c.ID == builtinSkillID {
			skill = c
		}
	}
	if skill == nil {
		t.Errorf("expected __builtin_skill__ card to be seeded")
	} else if hasTag(skill.Tags, "toc") {
		t.Errorf("skill card must not have toc tag: %q", skill.Tags)
	}

	// Removed builtin cards must not be seeded.
	for _, id := range []string{
		builtinCardPrefix + "kanban__",
		builtinCardPrefix + "automation__",
		builtinCardPrefix + "plan__",
	} {
		for i := range listResp.Cards {
			if listResp.Cards[i].ID == id {
				t.Errorf("expected %s card to not be seeded", id)
			}
		}
	}
}

func TestListAllCards_StripsBuiltinVirtualNodesByDefault(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "user-card", Raw: "---\nid: user-card\n---\n\nBody."}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Default: every __builtin_*__ virtual mount node is excluded, while toc
	// (unprefixed root) and real user cards survive.
	listResp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, c := range listResp.Cards {
		if strings.HasPrefix(c.ID, builtinCardPrefix) {
			t.Errorf("builtin virtual node %q leaked into default list", c.ID)
		}
	}
	var userFound, tocFound bool
	for _, c := range listResp.Cards {
		switch c.ID {
		case "user-card":
			userFound = true
		case tocID:
			tocFound = true
		}
	}
	if !userFound {
		t.Fatal("user-card missing from default list")
	}
	if !tocFound {
		t.Fatal("toc root card missing from default list")
	}

	// Opt-in: virtual mount nodes are present again.
	withBuiltin, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, IncludeBuiltin: true})
	if err != nil {
		t.Fatalf("list with builtins: %v", err)
	}
	var skillFound bool
	for _, c := range withBuiltin.Cards {
		if c.ID == builtinSkillID {
			skillFound = true
		}
	}
	if !skillFound {
		t.Fatal("__builtin_skill__ missing despite IncludeBuiltin=true")
	}
}

func TestWellKnownCardsMountUnderTocTree(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.ensureProjectInfoCard()
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{
		ID:  "summary",
		Raw: "---\nid: summary\ntype: wiki\ntags: []\n---\n\nLegacy project summary.",
	}); err != nil {
		t.Fatalf("create summary: %v", err)
	}

	resp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{})
	if err != nil {
		t.Fatalf("listCards: %v", err)
	}
	// project_info mounts under toc via the well-known rule; summary is an
	// ordinary parentless wiki card and default-mounts under toc alongside it
	// (applyDefaultTocMounting) — it is no longer pinned under project_info.
	if !strings.Contains(resp.Tree, "project_info") {
		t.Errorf("project_info not found in toc tree:\n%s", resp.Tree)
	}
	if !strings.Contains(resp.Tree, "summary") {
		t.Errorf("summary (parentless wiki card) should default-mount under toc:\n%s", resp.Tree)
	}
}

func TestBuiltinAggregatorBodiesCleared(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	// After seeding, aggregator builtin cards should have empty bodies.
	for _, id := range []string{
		builtinSkillID, builtinPluginID,
		builtinAgentID, builtinComponentID, builtinPromptID,
	} {
		card, err := a.store.Get(id)
		if err != nil {
			t.Errorf("get %s: %v", id, err)
			continue
		}
		if strings.TrimSpace(card.Body) != "" {
			t.Errorf("aggregator %s should have empty body, got %q", id, card.Body)
		}
	}
}

func TestWikiBuiltinTagsImmutable(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Update a remaining builtin (skill) and try to change its tags; the
	// canonical tags must be restored. summary/constraints are no longer
	// builtins, so their tags are mutable like any regular card.
	updatedRaw := "---\nid: __builtin_skill__\ntags: [custom-tag]\nlist: []\ncreated: \"1970-01-01T00:00:00Z\"\nmodified: \"1970-01-01T00:00:00Z\"\n---\n\nUpdated body.\n"
	_, err := a.handleWikiEditCard(ctx, domain.WikiEditCardReq{ID: builtinSkillID, Raw: updatedRaw})
	if err != nil {
		t.Fatalf("update skill: %v", err)
	}

	card, err := a.handleWikiGetCard(ctx, domain.WikiGetCardReq{ID: builtinSkillID})
	if err != nil {
		t.Fatalf("get skill: %v", err)
	}
	if strings.Contains(card.Raw, "custom-tag") {
		t.Errorf("builtin card tags should be immutable; raw still contains custom-tag: %q", card.Raw)
	}

	// Cannot create a builtin card manually
	_, err = a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "__builtin_custom__", Raw: "---\nid: __builtin_custom__\ntags: []\n---\n\n"})
	if err == nil {
		t.Errorf("expected error when creating builtin card")
	}
}

func TestRuntimeCardsAreExcludedAndProtectedMetadataIsHonored(t *testing.T) {
	a := newTestActor(t.TempDir())
	ctx := newTestContext(t)
	runtimeRaw := "---\nid: plan:runtime\ntags: [plan]\ndata:\n  type: plan\n  storage: runtime\n  visibility: runtime\n---\n\nplan\n"
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "plan:runtime", Raw: runtimeRaw}); err != nil {
		t.Fatalf("create runtime card: %v", err)
	}
	protectedRaw := "---\nid: capability:protected\ntags: [component]\ndata:\n  type: capability\n  protected: true\n  deletable: false\n---\n\nprotected\n"
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "capability:protected", Raw: protectedRaw}); err != nil {
		t.Fatalf("create protected card: %v", err)
	}
	list, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true})
	if err != nil {
		t.Fatalf("list cards: %v", err)
	}
	for _, card := range list.Cards {
		if card.ID == "plan:runtime" {
			t.Fatalf("runtime card leaked into list_all_cards: %+v", card)
		}
	}
	if _, err := a.handleWikiDeleteCard(ctx, domain.WikiDeleteCardReq{ID: "capability:protected"}); err == nil {
		t.Fatal("expected metadata-protected card deletion to fail")
	}
}

func TestWikiDeleteCardIDValidation(t *testing.T) {
	a := newTestActor(t.TempDir())
	ctx := newTestContext(t)

	if _, err := a.handleWikiDeleteCard(ctx, domain.WikiDeleteCardReq{ID: ""}); err == nil {
		t.Fatal("expected error for empty card id")
	}
	if _, err := a.handleWikiDeleteCard(ctx, domain.WikiDeleteCardReq{ID: "   "}); err == nil {
		t.Fatal("expected error for whitespace-only card id")
	}
	oversized := strings.Repeat("a", maxCardIDLen+1)
	if _, err := a.handleWikiDeleteCard(ctx, domain.WikiDeleteCardReq{ID: oversized}); err == nil {
		t.Fatal("expected error for oversized card id")
	}
}

func TestBuiltinPromptAndSkillCardsCannotBeDeleted(t *testing.T) {
	a := newTestActor(t.TempDir())
	a.seedPromptCards()
	a.seedBuiltinSkillCards()
	ctx := newTestContext(t)
	for _, id := range []string{"prompt:profile:project.coder", "skill:skill-creator"} {
		if _, err := a.handleWikiDeleteCard(ctx, domain.WikiDeleteCardReq{ID: id}); err == nil {
			t.Fatalf("expected protected builtin card %s to reject deletion", id)
		}
	}
}

func TestGoalBuiltinCardRemoved(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// New projects should not create the legacy Goals builtin card.
	_, err := a.handleWikiGetCard(ctx, domain.WikiGetCardReq{ID: "__builtin_goal__"})
	if err == nil {
		t.Fatalf("expected legacy goal builtin card to be removed, but it still exists")
	}

	// Existing projects should have the legacy card cleaned up on seeding.
	legacyPath := filepath.Join(tmp, wikiDir, "__builtin_goal__.md")
	if err := os.WriteFile(legacyPath, []byte("---\nid: __builtin_goal__\ntags: [toc, goal]\n---\n\nlegacy"), 0644); err != nil {
		t.Fatalf("write legacy goal card: %v", err)
	}
	a.seedBuiltinCards()
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("expected legacy goal builtin card to be deleted during seeding")
	}
}

func TestSeedBuiltinCardsRecreatesMissing(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	// Delete the root TOC card to simulate missing card
	if err := os.Remove(filepath.Join(tmp, wikiDir, "toc.md")); err != nil {
		t.Fatalf("remove toc: %v", err)
	}

	created := a.seedBuiltinCards()
	if len(created) != 1 || created[0] != tocID {
		t.Errorf("expected created [%q], got %v", tocID, created)
	}

	// Second call should create nothing
	if created2 := a.seedBuiltinCards(); len(created2) != 0 {
		t.Errorf("expected no created cards, got %v", created2)
	}
}

func TestWikiObsoleteBuiltinCleanup(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	// Simulate a legacy __builtin_system__ card left over from a previous version.
	systemPath := filepath.Join(tmp, wikiDir, "__builtin_system__.md")
	if err := os.WriteFile(systemPath, []byte("---\nid: __builtin_system__\ntags: [__builtin_system__]\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// seedBuiltinCards triggers seeding/cleanup.
	a.seedBuiltinCards()

	if _, err := os.Stat(systemPath); !os.IsNotExist(err) {
		t.Errorf("expected obsolete __builtin_system__ card to be removed")
	}
}

func TestApplyCardEdit(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		old        string
		new        string
		replaceAll bool
		want       string
		wantError  bool
		wantErrSub string
	}{
		{
			name: "replace body text preserving frontmatter",
			raw:  "---\nid: summary\n---\n\nOriginal body.\n",
			old:  "Original body.",
			new:  "Updated body.",
			want: "---\nid: summary\n---\n\nUpdated body.\n",
		},
		{
			name: "no frontmatter replaces whole raw",
			raw:  "Plain body\n",
			old:  "Plain body",
			new:  "Updated",
			want: "Updated\n",
		},
		{
			name:       "old string not found errors with context",
			raw:        "---\nid: summary\n---\n\nBody line one.\n",
			old:        "Missing",
			new:        "X",
			wantError:  true,
			wantErrSub: "first 3 lines of card body",
		},
		{
			name:       "empty old string errors",
			raw:        "body\n",
			old:        "",
			new:        "X",
			wantError:  true,
			wantErrSub: "old_string is empty",
		},
		{
			name:       "no-op old equals new errors",
			raw:        "Same text\n",
			old:        "Same text",
			new:        "Same text",
			wantError:  true,
			wantErrSub: "must differ",
		},
		{
			name:       "multiple occurrences without replace_all errors listing lines",
			raw:        "---\nid: c\n---\n\ndup\ndup\nother\n",
			old:        "dup",
			new:        "one",
			wantError:  true,
			wantErrSub: "occurs 2 times",
		},
		{
			name:       "replace_all replaces every occurrence",
			raw:        "---\nid: c\n---\n\ndup\ndup\nother\n",
			old:        "dup",
			new:        "one",
			replaceAll: true,
			want:       "---\nid: c\n---\n\none\none\nother\n",
		},
		{
			name: "CRLF line endings normalized before matching",
			raw:  "---\r\nid: c\r\n---\r\n\r\nOriginal body.\r\n",
			old:  "Original body.",
			new:  "Updated body.",
			want: "---\nid: c\n---\n\nUpdated body.\n",
		},
		{
			name: "lone carriage return in old string tolerated",
			raw:  "Line one\r\nLine two\n",
			old:  "Line one\r",
			new:  "Replaced",
			want: "Replaced\nLine two\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := applyCardEdit(tc.raw, tc.old, tc.new, tc.replaceAll)
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				if tc.wantErrSub != "" && !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("applyCardEdit() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProjectInfoCardsMigratedFromLegacy(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	// Simulate a legacy project whose __builtin_summary__ holds real content
	// and the obsolete __builtin_project_info__ tag.
	legacyRaw := "---\nid: __builtin_summary__\ntags: [__builtin_project_info__]\nlist: []\ncreated: \"2024-01-01T00:00:00Z\"\nmodified: \"2024-01-01T00:00:00Z\"\n---\n\nMy real project summary content.\n"
	if err := a.store.Save(&CardRecord{Title: "__builtin_summary__", Raw: legacyRaw}); err != nil {
		t.Fatalf("seed legacy summary: %v", err)
	}

	// Migration copies body content to the regular card and drops the tag.
	a.migrateProjectInfoCards()

	card, err := a.store.Get("summary")
	if err != nil {
		t.Fatalf("get migrated summary: %v", err)
	}
	if !strings.Contains(card.Raw, "My real project summary content.") {
		t.Errorf("migrated summary lost body content: %q", card.Raw)
	}
	if strings.Contains(card.Raw, "__builtin_project_info__") {
		t.Errorf("migrated summary still carries builtin tag: %q", card.Raw)
	}

	// seedBuiltinCards removes the obsolete legacy file but keeps the regular card.
	a.seedBuiltinCards()
	if _, err := a.store.Get("__builtin_summary__"); err == nil {
		t.Errorf("expected legacy __builtin_summary__ to be deleted")
	}
	if _, err := a.store.Get("summary"); err != nil {
		t.Errorf("regular summary card should survive seeding: %v", err)
	}
}

func hasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}

func TestWikiBuiltinPluginCardSeeded(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	listResp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, IncludeBuiltin: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var plugin *domain.MonoCardListItem
	for i := range listResp.Cards {
		if listResp.Cards[i].ID == builtinPluginID {
			plugin = &listResp.Cards[i]
		}
	}
	if plugin == nil {
		t.Fatalf("expected %s card to be seeded", builtinPluginID)
	}
	if hasTag(plugin.Tags, "toc") {
		t.Errorf("plugin card must not have toc tag: %q", plugin.Tags)
	}
}

func TestAgentExternalCardListTitleIsResolvableID(t *testing.T) {
	ref := domain.AgentRef{ID: "agent-brain", DisplayName: "AgentBrain", Title: "Agent Brain"}
	item := agentCardItem(ref, "2026-01-01T00:00:00Z")
	if item.ID != "agent:agent-brain" {
		t.Fatalf("external card title = %q, want resolvable card ID", item.ID)
	}
	if item.Data["agentTitle"] != "Agent Brain" || item.Data["agentDisplayName"] != "AgentBrain" {
		t.Fatalf("display metadata lost: %#v", item.Data)
	}

	a := &Actor{externalProviders: []ExternalCardProvider{newAgentExternalCardProvider()}}
	_, err := a.handleWikiGetCard(newTestContext(t), domain.WikiGetCardReq{ID: "agent:missing-agent"})
	if err == nil || !strings.Contains(err.Error(), `project.wiki.getCard "agent:missing-agent":`) {
		t.Fatalf("external card error lacks request title: %v", err)
	}
}

// newAgentListTestContext wires a fake workspace service whose
// workspace.list_agents returns the given refs, so the agent external card
// provider lists real agent:* rows.
func newAgentListTestContext(t *testing.T, refs ...domain.AgentRef) *testutil.FakeCtx {
	t.Helper()
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name != "workspace" {
			return nil, false
		}
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
			if callID != "workspace.list_agents" {
				return nil
			}
			return domain.AgentRefListResp{Items: refs}
		}), true
	}
	return ctx
}

func listCardIDs(resp domain.WikiListCardsResp) []string {
	ids := make([]string, 0, len(resp.Cards))
	for _, c := range resp.Cards {
		ids = append(ids, c.ID)
	}
	return ids
}

func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// TestWikiListCardsHidesAgentInstanceCardsByDefault guards the flat-list
// contract: agent:* instance cards are workspace runtime nodes, not wiki
// content. They appear only when the caller opts in (IncludeBuiltin — the
// topology graph) or explicitly filters for them (Type "agent" / Source
// "workspace"). Persisted cards of type agent are unaffected.
func TestWikiListCardsHidesAgentInstanceCardsByDefault(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	a.externalProviders = append(a.externalProviders, newAgentExternalCardProvider())
	ctx := newAgentListTestContext(t, domain.AgentRef{ID: "agent-coder", DisplayName: "Coder"})

	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{
		ID:  "agent-notes",
		Raw: "---\nid: agent-notes\ntype: agent\ntags: []\n---\n\nPersisted agent-type card.\n",
	}); err != nil {
		t.Fatalf("create persisted agent card: %v", err)
	}

	resp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, Limit: -1})
	if err != nil {
		t.Fatalf("list default: %v", err)
	}
	if ids := listCardIDs(resp); containsID(ids, "agent:agent-coder") {
		t.Errorf("default flat list must hide agent instance cards, got %v", ids)
	} else if !containsID(ids, "agent-notes") {
		t.Errorf("persisted type-agent card must stay visible, got %v", ids)
	}

	typed, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, Limit: -1, Type: "agent"})
	if err != nil {
		t.Fatalf("list Type=agent: %v", err)
	}
	if ids := listCardIDs(typed); !containsID(ids, "agent:agent-coder") {
		t.Errorf("Type=agent filter must include agent instance card, got %v", ids)
	}

	sourced, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, Limit: -1, Source: "workspace"})
	if err != nil {
		t.Fatalf("list Source=workspace: %v", err)
	}
	if ids := listCardIDs(sourced); !containsID(ids, "agent:agent-coder") {
		t.Errorf("Source=workspace filter must include agent instance card, got %v", ids)
	}

	withBuiltin, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, Limit: -1, IncludeBuiltin: true})
	if err != nil {
		t.Fatalf("list IncludeBuiltin: %v", err)
	}
	if ids := listCardIDs(withBuiltin); !containsID(ids, "agent:agent-coder") {
		t.Errorf("IncludeBuiltin=true (topology graph contract) must include agent instance card, got %v", ids)
	}
}

func TestWikiBuiltinAgentCardSeeded(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	listResp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, IncludeBuiltin: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var agent *domain.MonoCardListItem
	for i := range listResp.Cards {
		if listResp.Cards[i].ID == builtinAgentID {
			agent = &listResp.Cards[i]
		}
	}
	if agent == nil {
		t.Fatalf("expected %s card to be seeded", builtinAgentID)
	}
	if hasTag(agent.Tags, "toc") {
		t.Errorf("agent card must not have toc tag: %q", agent.Tags)
	}
}

// TestWikiBidirectionalParentHierarchy verifies that a card declaring a parent
// (which is a member of its tags) appears as a child of that parent in the
// tree even without the parent's List field being updated. Under the type
// driven refactor the parent must be a user card referenced via tags; builtin
// virtual nodes are reached through type-based mounting, not the parent field.
func TestWikiBidirectionalParentHierarchy(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Create a parent card.
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "parent-card", Raw: "---\nid: parent-card\ntags: [group]\n---\n\nParent body."}); err != nil {
		t.Fatalf("create parent: %v", err)
	}

	// Create a child that declares parent but the parent doesn't list it. The
	// parent id must appear in the child's tags (parent∈tags invariant).
	childRaw := "---\nid: child-card\ntags: [group, parent-card]\nparent: parent-card\n---\n\nChild body."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "child-card", Raw: childRaw}); err != nil {
		t.Fatalf("create child: %v", err)
	}

	// List cards from the parent root — the child should appear via the
	// bidirectional Parent-field derivation.
	resp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{RootID: "parent-card"})
	if err != nil {
		t.Fatalf("listCards: %v", err)
	}
	if !strings.Contains(resp.Tree, "child-card") {
		t.Errorf("child card not found in tree under parent-card:\n%s", resp.Tree)
	}
}

func TestWikiDispatchPlanValidation(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Missing card ID
	_, err := a.handleWikiDispatchPlan(ctx, gen.WikiDispatchPlanReq{AgentID: "agent:foo"})
	if err == nil {
		t.Errorf("expected error for missing CardId")
	}

	// Missing agent ID
	_, err = a.handleWikiDispatchPlan(ctx, gen.WikiDispatchPlanReq{ID: "plan-test"})
	if err == nil {
		t.Errorf("expected error for missing AgentId")
	}

	// Non-existent card
	_, err = a.handleWikiDispatchPlan(ctx, gen.WikiDispatchPlanReq{ID: "nonexistent", AgentID: "agent:foo"})
	if err == nil {
		t.Errorf("expected error for non-existent card")
	}

	// Card exists but not a plan card
	_, err = a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{
		ID:  "regular-card",
		Raw: "---\nid: regular-card\ntags: [note]\n---\n\nNot a plan.",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = a.handleWikiDispatchPlan(ctx, gen.WikiDispatchPlanReq{ID: "regular-card", AgentID: "agent:foo"})
	if err == nil {
		t.Errorf("expected error for non-plan card")
	}
}

func TestWikiExternalPluginCardFromDevDir(t *testing.T) {
	dataDir := t.TempDir()
	config.SetDataDirForTest(dataDir)
	t.Cleanup(config.ResetForTest)

	devDir := filepath.Join(dataDir, "data", "dev-app", "hello-world")
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		t.Fatal(err)
	}

	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContextWithWorkspaceMounts(t, []domain.ProjectRef{
		{Name: "hello-world", Path: devDir, AppKind: "dev-app"},
	})

	listResp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var card *domain.MonoCardListItem
	for i := range listResp.Cards {
		if listResp.Cards[i].ID == "plugin:hello-world" {
			card = &listResp.Cards[i]
		}
	}
	if card == nil {
		t.Fatalf("expected plugin:hello-world in list, got %d cards", len(listResp.Cards))
	}
	if !hasTag(card.Tags, "dev") {
		t.Errorf("expected dev tag, got %v", card.Tags)
	}
	if card.Type != "capability" || card.Source != "pluginhost" || card.Storage != "external" || card.Visibility != "component" {
		t.Errorf("unexpected plugin metadata: %+v", *card)
	}
	if !card.Protected || card.Editable || card.Deletable {
		t.Errorf("plugin protection metadata = %+v", *card)
	}

	getResp, err := a.handleWikiGetCard(ctx, domain.WikiGetCardReq{ID: "plugin:hello-world"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(getResp.Raw, "hello-world") {
		t.Errorf("get raw missing plugin name: %q", getResp.Raw)
	}

	updatedRaw := "---\nid: plugin:hello-world\ntags: [__builtin_plugin__, plugin, dev]\n---\n\nCustom body.\n"
	_, err = a.handleWikiEditCard(ctx, domain.WikiEditCardReq{ID: "plugin:hello-world", Raw: updatedRaw})
	if err == nil {
		t.Fatalf("expected read-only rejection on edit")
	}
	archive := filepath.Join(devDir, "plugin-hello-world.md")
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatalf("expected no archive written for read-only card")
	}

	_, err = a.handleWikiDeleteCard(ctx, domain.WikiDeleteCardReq{ID: "plugin:hello-world"})
	if err == nil {
		t.Fatalf("expected read-only rejection on delete")
	}
}

func TestWikiExternalPluginCardFromInstalled(t *testing.T) {
	dataDir := t.TempDir()
	config.SetDataDirForTest(dataDir)
	t.Cleanup(config.ResetForTest)

	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name != "pluginhost" {
			return nil, false
		}
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			if callID != "pluginhost.list_plugins" {
				return nil
			}
			return []pluginhost.PluginDescriptor{
				{ID: "installed-one", Name: "Installed One", Version: "1.0", Namespace: "ns", Status: "active"},
			}
		}), true
	}

	listResp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var card *domain.MonoCardListItem
	for i := range listResp.Cards {
		if listResp.Cards[i].ID == "plugin:installed-one" {
			card = &listResp.Cards[i]
		}
	}
	if card == nil {
		t.Fatalf("expected plugin:installed-one in list")
	}
	if !hasTag(card.Tags, "installed") {
		t.Errorf("expected installed tag, got %v", card.Tags)
	}

	updatedRaw := "---\nid: plugin:installed-one\ntags: [__builtin_plugin__, plugin, installed]\n---\n\nNotes.\n"
	_, err = a.handleWikiEditCard(ctx, domain.WikiEditCardReq{ID: "plugin:installed-one", Raw: updatedRaw})
	if err == nil {
		t.Fatalf("expected read-only rejection on edit")
	}
	archive := filepath.Join(dataDir, "data", "plugins", "installed-one", "plugin-installed-one.md")
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatalf("expected no archive written for read-only card")
	}
}

func TestWikiExternalPluginCardCreateRejected(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	_, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "plugin:foo", Raw: "---\nid: plugin:foo\ntags: []\n---\n\n"})
	if err == nil {
		t.Errorf("expected error when creating external card")
	}
}

func TestPluginExternalCardRejectsUnsafeIDs(t *testing.T) {
	provider := newPluginExternalCardProvider()
	ctx := testutil.AdminCtx(testutil.GenActorID())
	for _, id := range []string{"plugin:../outside", "plugin:foo/bar", "plugin:foo\\\\bar", "plugin:foo bar", "plugin:.."} {
		if _, err := provider.Get(ctx, id); err == nil {
			t.Errorf("Get(%q) expected error", id)
		}
		if err := provider.Save(ctx, id, "body"); err == nil {
			t.Errorf("Save(%q) expected error", id)
		}
		if err := provider.Delete(ctx, id); err == nil {
			t.Errorf("Delete(%q) expected error", id)
		}
	}
}

func TestPluginExternalCardListIsSorted(t *testing.T) {
	dataDir := t.TempDir()
	config.SetDataDirForTest(dataDir)
	t.Cleanup(config.ResetForTest)
	devRoot := filepath.Join(dataDir, "data", "dev-app")
	for _, name := range []string{"zeta", "alpha", "middle"} {
		if err := os.MkdirAll(filepath.Join(devRoot, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	ctx := newTestContextWithWorkspaceMounts(t, []domain.ProjectRef{
		{Name: "zeta", Path: filepath.Join(devRoot, "zeta"), AppKind: "dev-app"},
		{Name: "alpha", Path: filepath.Join(devRoot, "alpha"), AppKind: "dev-app"},
		{Name: "middle", Path: filepath.Join(devRoot, "middle"), AppKind: "dev-app"},
	})
	items, err := newPluginExternalCardProvider().List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var ids []string
	for _, item := range items {
		if strings.HasPrefix(item.ID, "plugin:") {
			ids = append(ids, item.ID)
		}
	}
	if want := []string{"plugin:alpha", "plugin:middle", "plugin:zeta"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("plugin IDs not sorted: got %v, want %v", ids, want)
	}
}

func TestWikiListCardsTree(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	create := func(id, title string, list []string) {
		t.Helper()
		listVal := "[]"
		if len(list) > 0 {
			listVal = "[" + strings.Join(list, ", ") + "]"
		}
		raw := "---\nid: " + id + "\ntags: []\nlist: " + listVal + "\ncreated: \"2026-01-01T00:00:00Z\"\nmodified: \"2026-01-01T00:00:00Z\"\n---\n\nbody"
		if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: id, Raw: raw}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	create("root", "Root", []string{"parent-a", "parent-b"})
	create("parent-a", "Parent A", []string{"child-a1", "child-a2"})
	create("parent-b", "Parent B", []string{"child-a1"})
	create("child-a1", "Child A1", []string{"grandchild"})
	create("child-a2", "Child A2", []string{})
	create("grandchild", "Grandchild", []string{})

	resp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{RootID: "root"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if resp.Tree == "" {
		t.Fatalf("expected non-empty Tree field")
	}

	// Child A1 is reached through Parent A first; Parent B's reference is marked
	// "[see above]" instead of duplicated. All cards share the same timestamp so
	// the default -modified sort keeps insertion order. Each node now carries
	// an @date suffix (wall-clock dependent), so we verify structure with a
	// regexp that allows any date.
	treeRe := regexp.MustCompile(`(?m)` + regexp.QuoteMeta(`└─ root @`) + `[0-9-]+` + `
` + regexp.QuoteMeta(`   ├─ parent-a @`) + `[0-9-]+` + `
` + regexp.QuoteMeta(`   │  ├─ child-a1 @`) + `[0-9-]+` + `
` + regexp.QuoteMeta(`   │  │  └─ grandchild @`) + `[0-9-]+` + `
` + regexp.QuoteMeta(`   │  └─ child-a2 @`) + `[0-9-]+` + `
` + regexp.QuoteMeta(`   └─ parent-b @`) + `[0-9-]+` + `
` + regexp.QuoteMeta(`      └─ child-a1 [see above]`))
	if !treeRe.MatchString(resp.Tree) {
		t.Errorf("tree mismatch.\ngot:\n%s", resp.Tree)
	}
}

func TestWikiListCardsDepthAndLimit(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	create := func(id string, list []string) {
		t.Helper()
		listVal := "[]"
		if len(list) > 0 {
			listVal = "[" + strings.Join(list, ", ") + "]"
		}
		raw := "---\nid: " + id + "\ntags: []\nlist: " + listVal + "\ncreated: \"2026-01-01T00:00:00Z\"\nmodified: \"2026-01-01T00:00:00Z\"\n---\n\nbody"
		if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: id, Raw: raw}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	// root -> a -> b -> c (depth 3 chain)
	create("root", []string{"a"})
	create("a", []string{"b"})
	create("b", []string{"c"})
	create("c", []string{})

	// Depth=2 stops before c (root=0, a=1, b=2 rendered but not expanded).
	depth2, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{RootID: "root", Depth: 2})
	if err != nil {
		t.Fatalf("depth: %v", err)
	}
	if strings.Count(depth2.Tree, "c") != 0 {
		t.Errorf("Depth=2 should not render c:\n%s", depth2.Tree)
	}
	if !strings.Contains(depth2.Tree, "├─ b @") && !strings.Contains(depth2.Tree, "└─ b @") {
		t.Errorf("Depth=2 should render b:\n%s", depth2.Tree)
	}

	// Limit=2 renders two nodes then a truncation marker.
	limited, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{RootID: "root", Limit: 2})
	if err != nil {
		t.Fatalf("limit: %v", err)
	}
	if !strings.Contains(limited.Tree, "... (truncated)") {
		t.Errorf("Limit=2 should append a truncation marker:\n%s", limited.Tree)
	}
}

func TestWikiListCardsInlineMetadataAndSort(t *testing.T) {
	items := []domain.MonoCardListItem{
		{ID: "__builtin_task__", Type: "task", Source: "builtin"},
		{ID: "__builtin_todo__", Type: "task", Source: "builtin", Status: "todo"},
		{ID: "__builtin_done__", Type: "task", Source: "builtin", Status: "done"},
		{ID: "new-task", Type: "task", Status: "todo", Modified: "2026-06-15T10:00:00+08:00"},
		{ID: "old-task", Type: "task", Status: "done", Modified: "2026-01-10T08:00:00+08:00"},
		{ID: "older-todo", Type: "task", Status: "todo", Modified: "2026-01-05T08:00:00+08:00"},
	}

	// Wire the hierarchy: __builtin_task__ → status nodes → task cards.
	items[0].List = []string{builtinTodoID, builtinDoneID}
	items[1].List = []string{"new-task", "older-todo"}
	items[2].List = []string{"old-task"}

	// Default OrderBy=-modified: newest first within each status bucket.
	tree := renderTreeFromRoot(items, builtinTaskID, 0, 0, "-modified")

	// Inline metadata: type [task] and status appear in [brackets].
	if !strings.Contains(tree, "new-task [task] [todo]") {
		t.Errorf("expected [task] [todo] inline for new-task in:\n%s", tree)
	}
	if !strings.Contains(tree, "old-task [task] [done]") {
		t.Errorf("expected [task] [done] inline for old-task in:\n%s", tree)
	}

	// Per-bucket sort: new-task (Jun) before older-todo (Jan) within todo.
	todoIdx := strings.Index(tree, "new-task")
	olderIdx := strings.Index(tree, "older-todo")
	if todoIdx < 0 || olderIdx < 0 {
		t.Fatalf("missing tasks in tree:\n%s", tree)
	}
	if todoIdx > olderIdx {
		t.Errorf("expected new-task before older-todo (newest first).\nTree:\n%s", tree)
	}

	// Reverse sort: OrderBy=modified (ascending = oldest first).
	asc := renderTreeFromRoot(items, builtinTaskID, 0, 0, "modified")
	ascTodoIdx := strings.Index(asc, "new-task")
	ascOlderIdx := strings.Index(asc, "older-todo")
	if ascTodoIdx < 0 || ascOlderIdx < 0 {
		t.Fatalf("missing tasks in asc tree:\n%s", asc)
	}
	if ascOlderIdx > ascTodoIdx {
		t.Errorf("expected older-todo before new-task (oldest first).\nTree:\n%s", asc)
	}

	// Builtin virtual nodes render without @date suffix (no meaningful mtime).
	if strings.Contains(tree, "__builtin_todo__ @") {
		t.Errorf("builtin virtual node should not have @date suffix:\n%s", tree)
	}
}

func TestCardDisplayTitle(t *testing.T) {
	named := domain.MonoCardListItem{ID: "__builtin_backlog__", Data: map[string]any{"builtin_title": "Backlog"}}
	if got := cardDisplayTitle(named); got != "Backlog" {
		t.Fatalf("builtin title = %q, want Backlog", got)
	}
	if got := cardDisplayTitle(domain.MonoCardListItem{ID: "mycard"}); got != "mycard" {
		t.Fatalf("plain title = %q, want mycard", got)
	}
	if got := formatTreeNode("mycard", "mycard"); got != "mycard" {
		t.Fatalf("redundant formatTreeNode = %q, want mycard", got)
	}
	if got := formatTreeNode("Backlog", "__builtin_backlog__"); got != "Backlog (__builtin_backlog__)" {
		t.Fatalf("formatTreeNode = %q, want Backlog (__builtin_backlog__)", got)
	}
}

func TestWikiGetCardHierarchy(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	create := func(id, title string, list []string, status string) {
		t.Helper()
		listVal := "[]"
		if len(list) > 0 {
			listVal = "[" + strings.Join(list, ", ") + "]"
		}
		statusLine := ""
		if status != "" {
			statusLine = "status: " + status + "\n"
		}
		raw := "---\nid: " + id + "\ntags: []\nlist: " + listVal + "\n" + statusLine + "created: \"2026-01-01T00:00:00Z\"\nmodified: \"2026-01-01T00:00:00Z\"\n---\n\nbody"
		if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: id, Raw: raw}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	create("grandparent", "Grandparent", []string{"parent-a", "parent-b"}, "done")
	create("parent-a", "Parent A", []string{"target"}, "todo")
	create("parent-b", "Parent B", []string{"target"}, "todo")
	create("target", "Target", []string{"child", "child2"}, "doing")
	create("child", "Child", []string{"grandchild"}, "todo")
	create("child2", "Child 2", []string{}, "todo")
	create("grandchild", "Grandchild", []string{}, "todo")

	resp, err := a.handleWikiGetCardHierarchy(ctx, domain.WikiGetCardHierarchyReq{ID: "target"})
	if err != nil {
		t.Fatalf("hierarchy: %v", err)
	}
	if resp.Tree == "" {
		t.Fatalf("expected non-empty Tree field")
	}

	checks := []string{
		"target [doing] [current]",
		"↑ = parent/ancestor, ↓ = child/descendant",
		"↑ parent-a [todo]",
		"↑ grandparent [done]",
		"↑ parent-b [todo]",
		"↓ child [todo]",
		"↓ grandchild [todo]",
		"↓ child2 [todo]",
	}
	for _, want := range checks {
		if !strings.Contains(resp.Tree, want) {
			t.Errorf("expected tree to contain %q.\n\ngot:\n%s", want, resp.Tree)
		}
	}
}

func TestWikiOpenCard(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Open first card → should prepend to empty list
	resp, err := a.handleWikiOpenCard(ctx, domain.WikiOpenCardReq{ID: "card-a"})
	if err != nil {
		t.Fatalf("openCard card-a: %v", err)
	}
	if len(resp.OpenCards) != 1 || resp.OpenCards[0] != "card-a" {
		t.Fatalf("openCards = %v, want [card-a]", resp.OpenCards)
	}

	// Open second card → should prepend (card-b before card-a)
	resp, err = a.handleWikiOpenCard(ctx, domain.WikiOpenCardReq{ID: "card-b"})
	if err != nil {
		t.Fatalf("openCard card-b: %v", err)
	}
	if len(resp.OpenCards) != 2 || resp.OpenCards[0] != "card-b" || resp.OpenCards[1] != "card-a" {
		t.Fatalf("openCards = %v, want [card-b card-a]", resp.OpenCards)
	}

	// Open same card again → idempotent, no duplicate
	resp, err = a.handleWikiOpenCard(ctx, domain.WikiOpenCardReq{ID: "card-a"})
	if err != nil {
		t.Fatalf("openCard card-a again: %v", err)
	}
	if len(resp.OpenCards) != 2 {
		t.Fatalf("openCards = %v, want length 2 (idempotent)", resp.OpenCards)
	}

	// Empty CardId → error
	_, err = a.handleWikiOpenCard(ctx, domain.WikiOpenCardReq{ID: ""})
	if err == nil {
		t.Errorf("expected error for empty CardId")
	}
}

func TestWikiCloseCard(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Seed: open three cards
	a.handleWikiOpenCard(ctx, domain.WikiOpenCardReq{ID: "card-c"})
	a.handleWikiOpenCard(ctx, domain.WikiOpenCardReq{ID: "card-b"})
	a.handleWikiOpenCard(ctx, domain.WikiOpenCardReq{ID: "card-a"})
	// State should be [card-a, card-b, card-c]

	// Close middle card
	resp, err := a.handleWikiCloseCard(ctx, domain.WikiCloseCardReq{ID: "card-b"})
	if err != nil {
		t.Fatalf("closeCard card-b: %v", err)
	}
	if len(resp.OpenCards) != 2 || resp.OpenCards[0] != "card-a" || resp.OpenCards[1] != "card-c" {
		t.Fatalf("openCards = %v, want [card-a card-c]", resp.OpenCards)
	}

	// Close non-existent card → list unchanged
	resp, err = a.handleWikiCloseCard(ctx, domain.WikiCloseCardReq{ID: "nonexistent"})
	if err != nil {
		t.Fatalf("closeCard nonexistent: %v", err)
	}
	if len(resp.OpenCards) != 2 {
		t.Fatalf("openCards = %v, want length 2 (unchanged)", resp.OpenCards)
	}

	// Empty CardId → error
	_, err = a.handleWikiCloseCard(ctx, domain.WikiCloseCardReq{ID: ""})
	if err == nil {
		t.Errorf("expected error for empty CardId")
	}
}

func TestListAllCards_ExposesDescription(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	raw := "---\nid: skill:my-skill\ndescription: \"does something useful\"\n---\n\nBody not exposed in list."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "skill:my-skill", Raw: raw}); err != nil {
		t.Fatalf("create: %v", err)
	}

	listResp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found *domain.MonoCardListItem
	for i := range listResp.Cards {
		if listResp.Cards[i].ID == "skill:my-skill" {
			found = &listResp.Cards[i]
		}
	}
	if found == nil {
		t.Fatal("skill:my-skill not found in list")
	}
	if found.Raw != "" {
		t.Errorf("expected Raw to be stripped, got %q", found.Raw)
	}
	desc, _ := found.Data["description"].(string)
	if desc != "does something useful" {
		t.Errorf("expected description in Data, got %q", desc)
	}
}

func TestListAllCards_ExposesTaskCategory(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	raw := "---\nid: research-task\ntype: task\ndata:\n  category: research\n---\n\nBody."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "research-task", Raw: raw}); err != nil {
		t.Fatalf("create: %v", err)
	}

	listResp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found *domain.MonoCardListItem
	for i := range listResp.Cards {
		if listResp.Cards[i].ID == "research-task" {
			found = &listResp.Cards[i]
		}
	}
	if found == nil {
		t.Fatal("research-task not found in list")
	}
	if cat, _ := found.Data["category"].(string); cat != "research" {
		t.Errorf("expected category in Data, got %q", cat)
	}
}

func TestListAllCards_IncludeRawTruePreservesRaw(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	raw := "---\nid: my-card\ndescription: \"card with body\"\n---\n\nThis is the body."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "my-card", Raw: raw}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Default (IncludeRaw=false / omitted): Raw is stripped, metadata preserved.
	listResp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found *domain.MonoCardListItem
	for i := range listResp.Cards {
		if listResp.Cards[i].ID == "my-card" {
			found = &listResp.Cards[i]
		}
	}
	if found == nil {
		t.Fatal("my-card not found in list")
	}
	if found.Raw != "" {
		t.Errorf("IncludeRaw=false: expected Raw stripped, got %q", found.Raw)
	}

	// IncludeRaw=true: Raw is preserved.
	listRespRaw, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, IncludeRaw: true})
	if err != nil {
		t.Fatalf("list with raw: %v", err)
	}
	for i := range listRespRaw.Cards {
		if listRespRaw.Cards[i].ID == "my-card" {
			found = &listRespRaw.Cards[i]
		}
	}
	if found == nil {
		t.Fatal("my-card not found in raw list")
	}
	if found.Raw == "" {
		t.Error("IncludeRaw=true: expected Raw to be preserved, got empty string")
	}
	if !strings.Contains(found.Raw, "This is the body.") {
		t.Errorf("IncludeRaw=true: expected body in Raw, got %q", found.Raw)
	}
}

// searchItem builds a MonoCardListItem for search-filter unit tests.
func searchItem(id string, fields domain.MonoCardListItem) domain.MonoCardListItem {
	fields.ID = id
	if fields.ID == "" {
		fields.ID = id
	}
	if fields.Type == "" {
		fields.Type = "wiki"
	}
	if fields.Source == "" {
		fields.Source = "project"
	}
	return fields
}

func TestWikiSearchTitleQuery(t *testing.T) {
	cases := []struct {
		name, title, query string
		want               bool
	}{
		{"substring case-insensitive", "Sprint Review", "sprint", true},
		{"substring no match", "Sprint Review", "retro", false},
		{"empty query matches all", "Anything", "", true},
		{"regexp with flag", "Gamma Spec", "/spec$/i", true},
		{"regexp anchored no match", "Retro", "/^spec/", false},
		{"invalid regexp matches nothing", "anything", "/(/", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			re, isRe, invalid := compileWikiTitleQuery(c.query)
			got := cardTitleMatches(domain.MonoCardListItem{ID: c.title}, c.query, re, isRe, invalid)
			if got != c.want {
				t.Errorf("titleMatch(%q,%q) = %v, want %v", c.title, c.query, got, c.want)
			}
		})
	}
}

// filterMatches builds a wikiCardFilter from in (no query) and applies it to card.
func filterMatches(card domain.MonoCardListItem, in wikiCardFilterInput) bool {
	f, err := parseWikiCardFilter(in)
	if err != nil {
		return false
	}
	return f.matches(card)
}

func TestWikiSearchFieldFilters(t *testing.T) {
	card := searchItem("a", domain.MonoCardListItem{
		ID: "Alpha", Tags: []string{"kanban", "draft"}, Status: "done", Type: "agent", Source: "workspace",
		List: []string{"backlog", "sprint-1"}, Parent: "EpicA", Priority: "high", Due: "2026-05-01T00:00:00Z",
		Created: "2026-01-01T00:00:00Z", Modified: "2026-03-15T12:00:00Z",
	})

	// Scalar filters are case-insensitive exact matches.
	if !filterMatches(card, wikiCardFilterInput{Status: "DONE"}) {
		t.Error("status match should be case-insensitive")
	}
	if filterMatches(card, wikiCardFilterInput{Status: "todo"}) {
		t.Error("non-matching status should fail")
	}
	if !filterMatches(card, wikiCardFilterInput{Type: "agent"}) {
		t.Error("type exact match failed")
	}
	if !filterMatches(card, wikiCardFilterInput{Source: "workspace"}) {
		t.Error("source exact match failed")
	}
	if !filterMatches(card, wikiCardFilterInput{Parent: "epica"}) {
		t.Error("parent match should be case-insensitive")
	}
	if filterMatches(card, wikiCardFilterInput{Parent: "EpicB"}) {
		t.Error("non-matching parent should fail")
	}
	if !filterMatches(card, wikiCardFilterInput{Priority: "HIGH"}) {
		t.Error("priority match should be case-insensitive")
	}
	if filterMatches(card, wikiCardFilterInput{Priority: "low"}) {
		t.Error("non-matching priority should fail")
	}
	if !filterMatches(card, wikiCardFilterInput{Due: "2026-05-01T00:00:00Z"}) {
		t.Error("due exact match failed")
	}
	// List: a card may belong to multiple lists; membership is a case-insensitive OR.
	if !filterMatches(card, wikiCardFilterInput{List: "sprint-1"}) {
		t.Error("list membership match failed")
	}
	if filterMatches(card, wikiCardFilterInput{List: "done"}) {
		t.Error("non-member list should fail")
	}

	// Tags use AND semantics: every requested tag must be present.
	if !filterMatches(card, wikiCardFilterInput{Tags: []string{"kanban"}}) {
		t.Error("single tag match failed")
	}
	if !filterMatches(card, wikiCardFilterInput{Tags: []string{"kanban", "draft"}}) {
		t.Error("multi-tag AND match failed")
	}
	if filterMatches(card, wikiCardFilterInput{Tags: []string{"kanban", "missing"}}) {
		t.Error("missing one tag should fail (AND)")
	}

	// Combination: status + type + tag + parent must all hold.
	if !filterMatches(card, wikiCardFilterInput{Status: "done", Type: "agent", Tags: []string{"kanban"}, Parent: "EpicA"}) {
		t.Error("combined match failed")
	}
	if filterMatches(card, wikiCardFilterInput{Status: "done", Type: "wiki", Tags: []string{"kanban"}}) {
		t.Error("combined with one mismatching field should fail")
	}
}

func TestWikiSearchTimeWindows(t *testing.T) {
	card := searchItem("a", domain.MonoCardListItem{
		Modified: "2026-03-15T12:00:00Z",
		Created:  "2025-12-01T00:00:00Z",
		Due:      "2026-05-01T00:00:00Z",
	})

	// Modified after-only: card is newer than the lower bound.
	if !filterMatches(card, wikiCardFilterInput{ModifiedAfter: "2026-01-01T00:00:00Z"}) {
		t.Error("modified after-only filter should match")
	}
	// Modified after-only: card is older than the lower bound.
	if filterMatches(card, wikiCardFilterInput{ModifiedAfter: "2026-06-01T00:00:00Z"}) {
		t.Error("modified after filter past the card should not match")
	}
	// Modified enclosing range.
	if !filterMatches(card, wikiCardFilterInput{ModifiedAfter: "2026-01-01T00:00:00Z", ModifiedBefore: "2026-12-31T00:00:00Z"}) {
		t.Error("enclosing modified range should match")
	}
	// Modified before-only: card older than the cutoff matches.
	if !filterMatches(card, wikiCardFilterInput{ModifiedBefore: "2026-06-01T00:00:00Z"}) {
		t.Error("modified before-only filter (card older) should match")
	}
	if filterMatches(card, wikiCardFilterInput{ModifiedBefore: "2026-01-01T00:00:00Z"}) {
		t.Error("modified before-only filter (card newer) should not match")
	}

	// Created window.
	if !filterMatches(card, wikiCardFilterInput{CreatedAfter: "2025-11-01T00:00:00Z"}) {
		t.Error("created after-only filter should match")
	}
	if filterMatches(card, wikiCardFilterInput{CreatedBefore: "2025-11-01T00:00:00Z"}) {
		t.Error("created before filter before the card should not match")
	}

	// Due window.
	if !filterMatches(card, wikiCardFilterInput{DueAfter: "2026-04-01T00:00:00Z", DueBefore: "2026-06-01T00:00:00Z"}) {
		t.Error("enclosing due range should match")
	}
	if filterMatches(card, wikiCardFilterInput{DueBefore: "2026-04-01T00:00:00Z"}) {
		t.Error("due before filter before the card should not match")
	}

	// Card with unparseable timestamp is excluded when the corresponding filter is active.
	bad := searchItem("b", domain.MonoCardListItem{Modified: "not-a-date"})
	if filterMatches(bad, wikiCardFilterInput{ModifiedAfter: "2026-01-01T00:00:00Z"}) {
		t.Error("unparseable modified should be excluded under a modified filter")
	}
	// No active bound never excludes, regardless of timestamp validity.
	if !filterMatches(bad, wikiCardFilterInput{}) {
		t.Error("no time filter should match regardless of timestamp validity")
	}

	// Malformed RFC3339 bound is rejected by the parser.
	if _, err := parseWikiCardFilter(wikiCardFilterInput{ModifiedAfter: "not-a-date"}); err == nil {
		t.Error("expected error for malformed ModifiedAfter")
	}
	if _, err := parseWikiCardFilter(wikiCardFilterInput{CreatedBefore: "not-a-date"}); err == nil {
		t.Error("expected error for malformed CreatedBefore")
	}
}

func TestWikiSearchOrderBy(t *testing.T) {
	items := []domain.MonoCardListItem{
		{ID: "c", Modified: "2026-03-01T00:00:00Z", Created: "2026-01-03T00:00:00Z", Priority: "low"},
		{ID: "a", Modified: "2026-01-01T00:00:00Z", Created: "2026-01-01T00:00:00Z", Priority: "high"},
		{ID: "b", Modified: "2026-02-01T00:00:00Z", Created: "2026-01-02T00:00:00Z", Priority: "medium"},
	}
	ids := func(s []domain.MonoCardListItem) []string {
		out := make([]string, len(s))
		for i, x := range s {
			out[i] = x.ID
		}
		return out
	}
	clone := func() []domain.MonoCardListItem {
		cp := make([]domain.MonoCardListItem, len(items))
		copy(cp, items)
		return cp
	}

	// modified ascending (oldest first): a, b, c.
	s := clone()
	sortWikiCardItems(s, "modified")
	if got := ids(s); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("modified asc = %v", got)
	}
	// -modified descending (newest first): c, b, a.
	s = clone()
	sortWikiCardItems(s, "-modified")
	if got := ids(s); !reflect.DeepEqual(got, []string{"c", "b", "a"}) {
		t.Errorf("modified desc = %v", got)
	}
	// title ascending.
	s = clone()
	sortWikiCardItems(s, "title")
	if got := ids(s); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("title asc = %v", got)
	}
	// priority ascending (high < medium < low): a, b, c.
	s = clone()
	sortWikiCardItems(s, "priority")
	if got := ids(s); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("priority asc = %v", got)
	}
	// created ascending: a, b, c.
	s = clone()
	sortWikiCardItems(s, "created")
	if got := ids(s); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("created asc = %v", got)
	}
	// unknown / empty field: natural order preserved (c, a, b), stable.
	s = clone()
	sortWikiCardItems(s, "bogus")
	if got := ids(s); !reflect.DeepEqual(got, []string{"c", "a", "b"}) {
		t.Errorf("unknown field should keep natural order, got %v", got)
	}
	// empty/unparseable timestamps sort last when ascending.
	s = []domain.MonoCardListItem{
		{ID: "blank", Modified: ""},
		{ID: "old", Modified: "2025-01-01T00:00:00Z"},
	}
	sortWikiCardItems(s, "modified")
	if got := ids(s); !reflect.DeepEqual(got, []string{"old", "blank"}) {
		t.Errorf("empty-modified-last asc = %v", got)
	}
	// empty/unparseable timestamps also sort last when descending — the
	// direction flip must not surface timestamp-less cards at the top.
	s = []domain.MonoCardListItem{
		{ID: "blank", Modified: ""},
		{ID: "new", Modified: "2026-01-01T00:00:00Z"},
		{ID: "old", Modified: "2025-01-01T00:00:00Z"},
	}
	sortWikiCardItems(s, "-modified")
	if got := ids(s); !reflect.DeepEqual(got, []string{"new", "old", "blank"}) {
		t.Errorf("empty-modified-last desc = %v", got)
	}
	// a card with no Modified but a Created stamp ranks by Created.
	s = []domain.MonoCardListItem{
		{ID: "no-stamps"},
		{ID: "created-only", Created: "2026-02-01T00:00:00Z"},
		{ID: "both", Modified: "2026-01-01T00:00:00Z", Created: "2026-03-01T00:00:00Z"},
	}
	sortWikiCardItems(s, "-modified")
	if got := ids(s); !reflect.DeepEqual(got, []string{"created-only", "both", "no-stamps"}) {
		t.Errorf("created-fallback desc = %v", got)
	}
}

func TestWikiSearchCardsIntegration(t *testing.T) {
	a := newTestActor(t.TempDir())
	ctx := newTestContext(t)

	mkWith := func(id, raw string) {
		if err := a.store.Save(&CardRecord{Title: id, Raw: ensureCardMeta(id, raw, time.Now().UTC().Format(time.RFC3339))}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	mkWith("st-alpha", "---\nid: st-alpha\ntags: [searchtest, kanban]\nstatus: done\nmodified: 2026-01-10T00:00:00Z\n---\nbody alpha\n")
	mkWith("st-beta", "---\nid: st-beta\ntags: [searchtest, draft]\nstatus: todo\nmodified: 2026-06-01T00:00:00Z\n---\nbody beta\n")
	mkWith("st-gamma", "---\nid: st-gamma\ntags: [searchtest, kanban, draft]\nstatus: done\nmodified: 2026-07-01T00:00:00Z\n---\nbody gamma\n")

	ids := func(resp domain.WikiListCardsResp) []string {
		out := make([]string, 0, len(resp.Cards))
		for _, c := range resp.Cards {
			out = append(out, c.ID)
		}
		return out
	}

	// Tag filter narrows to the kanban-tagged cards among the searchtest set.
	resp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, Total: true, Tags: []string{"searchtest", "kanban"}})
	if err != nil {
		t.Fatalf("search tags: %v", err)
	}
	if !reflect.DeepEqual(ids(resp), []string{"st-gamma", "st-alpha"}) {
		t.Errorf("tag search ids = %v", ids(resp))
	}
	if resp.Total != 2 {
		t.Errorf("Total = %d, want 2", resp.Total)
	}

	// Combination: status + tag.
	resp, err = a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, Status: "todo", Tags: []string{"searchtest"}})
	if err != nil {
		t.Fatalf("search status+tag: %v", err)
	}
	if !reflect.DeepEqual(ids(resp), []string{"st-beta"}) {
		t.Errorf("status+tag ids = %v", ids(resp))
	}

	// Id substring (case-insensitive).
	resp, err = a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, Query: "alpha", Tags: []string{"searchtest"}})
	if err != nil {
		t.Fatalf("search query: %v", err)
	}
	if !reflect.DeepEqual(ids(resp), []string{"st-alpha"}) {
		t.Errorf("query ids = %v", ids(resp))
	}

	// Id regexp.
	resp, err = a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, Query: "/^st-beta/i", Tags: []string{"searchtest"}})
	if err != nil {
		t.Fatalf("search regexp: %v", err)
	}
	if !reflect.DeepEqual(ids(resp), []string{"st-beta"}) {
		t.Errorf("regexp ids = %v", ids(resp))
	}

	// Time window: only cards modified after mid-May.
	resp, err = a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, Tags: []string{"searchtest"}, ModifiedAfter: "2026-05-15T00:00:00Z"})
	if err != nil {
		t.Fatalf("search after: %v", err)
	}
	if !reflect.DeepEqual(ids(resp), []string{"st-gamma", "st-beta"}) {
		t.Errorf("after ids = %v", ids(resp))
	}

	// Limit truncates returned Cards but keeps untruncated Total.
	resp, err = a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, Total: true, Tags: []string{"searchtest"}, Limit: 2})
	if err != nil {
		t.Fatalf("search limit: %v", err)
	}
	if resp.Total != 3 {
		t.Errorf("Total = %d, want 3", resp.Total)
	}
	if len(resp.Cards) != 2 {
		t.Errorf("len(Cards) = %d, want 2 (limit)", len(resp.Cards))
	}

	// Metadata-only contract: Raw stripped.
	for _, c := range resp.Cards {
		if c.Raw != "" {
			t.Errorf("search returned card body for %s", c.ID)
		}
	}

	// Malformed RFC3339 bound is an explicit error.
	if _, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, ModifiedAfter: "not-a-date"}); err == nil {
		t.Error("expected error for malformed ModifiedAfter")
	}
}

// TestWikiListCardsFlatLimitContract guards the explicit limit contract of the
// flat list_cards path: Limit=-1 returns every matching card (the former
// list_all_cards semantics — the topology/workflow graphs, sidebar, and agent
// manifest collect rely on it), an omitted Limit keeps the default search cap,
// and an explicit positive Limit truncates. Regression guard: after the
// list_all_cards merge every full-list caller silently lost cards beyond the
// default cap and the graphs displayed incompletely.
func TestWikiListCardsFlatLimitContract(t *testing.T) {
	a := newTestActor(t.TempDir())
	ctx := newTestContext(t)

	total := defaultWikiSearchLimit + 10
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("bulk-%03d", i)
		raw := fmt.Sprintf("---\nid: %s\ntags: [bulk]\nmodified: %s\n---\nbody %d\n", id, time.Now().UTC().Format(time.RFC3339), i)
		if err := a.store.Save(&CardRecord{Title: id, Raw: ensureCardMeta(id, raw, time.Now().UTC().Format(time.RFC3339))}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}

	// Limit: -1 returns every card (plus the toc root and seeded builtins).
	all, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, IncludeBuiltin: true, Limit: -1})
	if err != nil {
		t.Fatalf("flat list -1: %v", err)
	}
	if len(all.Cards) < total {
		t.Errorf("Limit=-1 returned %d cards, want >= %d", len(all.Cards), total)
	}

	// Limit: -1 with a Query returns every match, uncapped.
	matched, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, Query: "bulk", Limit: -1})
	if err != nil {
		t.Fatalf("flat query -1: %v", err)
	}
	if len(matched.Cards) != total {
		t.Errorf("Query + Limit=-1 returned %d cards, want %d", len(matched.Cards), total)
	}

	// Exact replica of the mono-store request the topology/workflow graphs
	// issue (Flat + IncludeBuiltin + Limit:-1 + WorkflowFilter with empty
	// fold sets and live agent ids): with nothing folded the fold filter
	// must drop zero cards, matching the old list_all_cards result.
	graphReq, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{
		Flat: true, IncludeRaw: false, IncludeBuiltin: true, Limit: -1,
		WorkflowFilter: &domain.WikiWorkflowFilter{
			FoldedWorkflows: []string{},
			FoldedCategories: []string{},
			ExpandedBuckets:  []string{},
			AgentIds:         []string{"agent-1"},
		},
	})
	if err != nil {
		t.Fatalf("graph replica request: %v", err)
	}
	bulk := 0
	for _, c := range graphReq.Cards {
		if strings.HasPrefix(c.ID, "bulk-") {
			bulk++
		}
	}
	if bulk != total {
		t.Errorf("graph replica request returned %d bulk cards, want all %d", bulk, total)
	}

	// Omitted Limit keeps the default search cap.
	capped, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, Query: "bulk"})
	if err != nil {
		t.Fatalf("flat query default: %v", err)
	}
	if len(capped.Cards) != defaultWikiSearchLimit {
		t.Errorf("Query without Limit returned %d cards, want default cap %d", len(capped.Cards), defaultWikiSearchLimit)
	}

	// Explicit positive Limit truncates.
	limited, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, Limit: 5})
	if err != nil {
		t.Fatalf("flat list 5: %v", err)
	}
	if len(limited.Cards) != 5 {
		t.Errorf("Limit=5 returned %d cards, want 5", len(limited.Cards))
	}
}

func TestWikiSearchCardContentIntegration(t *testing.T) {
	a := newTestActor(t.TempDir())
	ctx := newTestContext(t)

	mk := func(id, raw string) {
		if err := a.store.Save(&CardRecord{Title: id, Raw: ensureCardMeta(id, raw, time.Now().UTC().Format(time.RFC3339))}); err != nil {
			t.Fatalf("save %s: %v", id, err)
		}
	}
	mk("sc-alpha", "---\nid: sc-alpha\ntags: [EpicA]\nstatus: done\npriority: high\nparent: EpicA\nmodified: 2026-01-10T00:00:00Z\n---\nholds the keyword needle here\n")
	mk("sc-beta", "---\nid: sc-beta\ntags: [EpicB]\nstatus: todo\npriority: low\nparent: EpicB\nmodified: 2026-06-01T00:00:00Z\n---\nalso contains needle text\n")
	mk("sc-gamma", "---\nid: sc-gamma\ntags: [EpicA]\nstatus: done\npriority: medium\nparent: EpicA\nmodified: 2026-07-01T00:00:00Z\n---\nno match word at all\n")

	ids := func(resp domain.WikiSearchCardContentResp) []string {
		out := make([]string, 0, len(resp.Matches))
		for _, m := range resp.Matches {
			out = append(out, m.Card.ID)
		}
		return out
	}

	// Body match for "needle" across both containing cards.
	resp, err := a.handleWikiSearchCardContent(ctx, domain.WikiSearchCardContentReq{Query: "needle"})
	if err != nil {
		t.Fatalf("content search: %v", err)
	}
	if resp.Total != 2 {
		t.Errorf("Total = %d, want 2", resp.Total)
	}
	for _, m := range resp.Matches {
		if m.Snippet == "" || m.Line <= 0 {
			t.Errorf("match %s missing snippet/line: snippet=%q line=%d", m.Card.ID, m.Snippet, m.Line)
		}
		if m.Card.Raw != "" {
			t.Errorf("card body should be stripped for %s", m.Card.ID)
		}
	}

	// Body match combined with a metadata filter (parent=epica, case-insensitive).
	resp, err = a.handleWikiSearchCardContent(ctx, domain.WikiSearchCardContentReq{Query: "needle", Parent: "epica"})
	if err != nil {
		t.Fatalf("content search + parent: %v", err)
	}
	if !reflect.DeepEqual(ids(resp), []string{"sc-alpha"}) {
		t.Errorf("content+parent ids = %v", ids(resp))
	}

	// Body match combined with a modified-time window. Regression for the
	// previously missing ModifiedAfter/ModifiedBefore in content search.
	resp, err = a.handleWikiSearchCardContent(ctx, domain.WikiSearchCardContentReq{Query: "needle", ModifiedAfter: "2026-05-01T00:00:00Z"})
	if err != nil {
		t.Fatalf("content search + time: %v", err)
	}
	if !reflect.DeepEqual(ids(resp), []string{"sc-beta"}) {
		t.Errorf("content+time ids = %v", ids(resp))
	}

	// OrderBy on content matches: newest modified first.
	resp, err = a.handleWikiSearchCardContent(ctx, domain.WikiSearchCardContentReq{Query: "needle", OrderBy: "-modified"})
	if err != nil {
		t.Fatalf("content search + orderby: %v", err)
	}
	if !reflect.DeepEqual(ids(resp), []string{"sc-beta", "sc-alpha"}) {
		t.Errorf("content orderby ids = %v", ids(resp))
	}

	// Empty query is rejected.
	if _, err := a.handleWikiSearchCardContent(ctx, domain.WikiSearchCardContentReq{Query: ""}); err == nil {
		t.Error("expected error for empty content query")
	}
	// Malformed time bound is rejected.
	if _, err := a.handleWikiSearchCardContent(ctx, domain.WikiSearchCardContentReq{Query: "needle", ModifiedBefore: "not-a-date"}); err == nil {
		t.Error("expected error for malformed ModifiedBefore")
	}
}

// TestStripListCardBodiesPreservesVisual verifies that the list/search
// metadata-only projection keeps the `visual` sub-key so card appearance
// (icon/accent) survives a reload. Regression guard for "保存后丢失".
func TestStripListCardBodiesPreservesVisual(t *testing.T) {
	items := []domain.MonoCardListItem{
		{
			ID:  "my-card",
			Raw: "---\ndata:\n  visual:\n    icon: rocket\n    accent: blue\n  description: hello\n  depends_on:\n    - prerequisite\n---\n\nbody",
			Data: map[string]any{
				"visual":      map[string]any{"icon": "rocket", "accent": "blue"},
				"description": "hello",
				"depends_on":  []any{"prerequisite"},
				"executor":    "agent:coder",
			},
		},
	}
	stripListCardBodies(items)

	if items[0].Raw != "" {
		t.Errorf("Raw should be stripped, got %q", items[0].Raw)
	}
	visual, ok := items[0].Data["visual"].(map[string]any)
	if !ok {
		t.Fatalf("visual must survive strip: %#v", items[0].Data)
	}
	if visual["icon"] != "rocket" || visual["accent"] != "blue" {
		t.Errorf("visual content lost: %#v", visual)
	}
	if items[0].Data["description"] != "hello" {
		t.Errorf("description should be preserved: %#v", items[0].Data["description"])
	}
	deps, ok := items[0].Data["depends_on"].([]any)
	if !ok || len(deps) != 1 || deps[0] != "prerequisite" {
		t.Errorf("depends_on should be preserved: %#v", items[0].Data["depends_on"])
	}
	// Non-essential fields are still dropped to keep the list light.
	if _, ok := items[0].Data["executor"]; ok {
		t.Errorf("executor should be stripped from list item")
	}
}

// TestStripListCardBodiesPreservesTemplateProvenance verifies that the
// template/instance provenance keys survive the metadata-only strip: the
// workflow view's category bands (template, planned) and the topology
// context-menu guards read them from list items. Losing them makes templates
// and instances reclassify (and their menu entries vanish) after the app
// restarts and the frontend reloads cards via the list endpoint.
func TestStripListCardBodiesPreservesTemplateProvenance(t *testing.T) {
	items := []domain.MonoCardListItem{
		{
			ID: "tpl::MyFlow",
			Data: map[string]any{
				"template":   true,
				"source_map": "MyFlow",
				"executor":   "agent:coder",
			},
		},
		{
			ID: "inst::1::tpl::MyFlow",
			Data: map[string]any{
				"instance_of":       "tpl::MyFlow",
				"scheduler_card_id": "scheduler:MyFlow",
				"workflow_template": "tpl::MyFlow",
				"current_instance":  "inst::1::tpl::MyFlow",
				"executor":          "agent:coder",
			},
		},
	}
	stripListCardBodies(items)

	if items[0].Data["template"] != true {
		t.Errorf("template must survive strip: %#v", items[0].Data)
	}
	if items[0].Data["source_map"] != "MyFlow" {
		t.Errorf("source_map must survive strip: %#v", items[0].Data)
	}
	if items[1].Data["instance_of"] != "tpl::MyFlow" {
		t.Errorf("instance_of must survive strip: %#v", items[1].Data)
	}
	if items[1].Data["scheduler_card_id"] != "scheduler:MyFlow" {
		t.Errorf("scheduler_card_id must survive strip: %#v", items[1].Data)
	}
	// current_instance is only rendered from the card detail view (full Raw);
	// the list contract keeps it stripped.
	if _, ok := items[1].Data["current_instance"]; ok {
		t.Errorf("current_instance should stay stripped from list items")
	}
	if _, ok := items[1].Data["executor"]; ok {
		t.Errorf("executor should be stripped from list item")
	}
}

// TestStripListCardBodiesNoVisualKeepsNilData verifies that cards without
// visual keep the original nil-Data contract when they have no description.
func TestStripListCardBodiesNoVisualKeepsNilData(t *testing.T) {
	items := []domain.MonoCardListItem{
		{ID: "plain", Raw: "---\n---\n\nbody", Data: map[string]any{"executor": "agent:coder"}},
	}
	stripListCardBodies(items)
	if items[0].Data != nil {
		t.Errorf("Data should be nil for cards without visual/description, got %#v", items[0].Data)
	}
	if items[0].Raw != "" {
		t.Errorf("Raw should be stripped")
	}
}

// TestStripListCardBodiesPreservesMountRules verifies that builtin virtual-node
// cards keep their mount-rule keys (builtinRole/mountType/mountStatus/autoMount)
// so the frontend can derive the mount table from listAllCards. Dropping these
// orphans every task card from its status node in the topology graph.
func TestStripListCardBodiesPreservesMountRules(t *testing.T) {
	items := []domain.MonoCardListItem{
		{
			ID:  builtinTodoID,
			Raw: "---\n---\n\nbody",
			Data: map[string]any{
				"builtinRole": "mount",
				"mountType":   "task",
				"mountStatus": "todo",
				"autoMount":   true,
				"executor":    "agent:coder", // non-essential, must be dropped
			},
		},
		{
			ID:   builtinTaskID,
			Data: map[string]any{"builtinRole": "aggregator"},
		},
	}
	stripListCardBodies(items)

	todo := items[0]
	if todo.Raw != "" {
		t.Errorf("Raw should be stripped")
	}
	if todo.Data["builtinRole"] != "mount" {
		t.Errorf("builtinRole lost: %#v", todo.Data)
	}
	if todo.Data["mountType"] != "task" {
		t.Errorf("mountType lost: %#v", todo.Data)
	}
	if todo.Data["mountStatus"] != "todo" {
		t.Errorf("mountStatus lost: %#v", todo.Data)
	}
	if todo.Data["autoMount"] != true {
		t.Errorf("autoMount lost: %#v", todo.Data)
	}
	if _, ok := todo.Data["executor"]; ok {
		t.Errorf("executor should be stripped")
	}

	task := items[1]
	if task.Data["builtinRole"] != "aggregator" {
		t.Errorf("aggregator builtinRole lost: %#v", task.Data)
	}
}

// TestStripListCardBodiesPreservesComponentKind verifies that builtin component
// cards keep componentKind in their list Data so the frontend omnibox can filter
// bundle/mode cards. The BuiltinComponentCardProvider writes this key; dropping
// it makes every builtin component (e.g. builtin:mode:memory) disappear from the
// /-command mount picker. icon/title are likewise preserved so the omnibox can
// render the card's own badge glyph and friendly name.
func TestStripListCardBodiesPreservesComponentKind(t *testing.T) {
	items := []domain.MonoCardListItem{
		{
			ID:   "builtin:mode:memory",
			Type: "mode",
			Data: map[string]any{"componentKind": "mode", "executor": "agent:coder", "icon": "🧠", "title": "Memory Mode", "settingsVisible": true, "tools": []string{"tool.call"}},
		},
		{
			ID:   "builtin:bundle:planning",
			Type: "bundle",
			Data: map[string]any{"componentKind": "bundle"},
		},
	}
	stripListCardBodies(items)

	if items[0].Data["componentKind"] != "mode" {
		t.Errorf("mode componentKind lost: %#v", items[0].Data)
	}
	if items[0].Data["icon"] != "🧠" {
		t.Errorf("mode icon lost: %#v", items[0].Data)
	}
	if items[0].Data["title"] != "Memory Mode" {
		t.Errorf("mode title lost: %#v", items[0].Data)
	}
	if items[0].Data["settingsVisible"] != true {
		t.Errorf("settingsVisible lost: %#v", items[0].Data)
	}
	if _, ok := items[0].Data["tools"]; !ok {
		t.Errorf("tools lost: %#v", items[0].Data)
	}
	// modeManaged must survive stripListCardBodies so the frontend slash
	// panel can filter mode-managed bundles out of the mount picker.
	items = append(items, domain.MonoCardListItem{
		ID:   "builtin:bundle:goal",
		Type: "bundle",
		Data: map[string]any{"componentKind": "bundle", "modeManaged": true},
	})
	stripListCardBodies(items)
	if items[2].Data["modeManaged"] != true {
		t.Errorf("modeManaged lost: %#v", items[2].Data)
	}
	// lifecycleManaged + requires must survive so the frontend can gate a
	// mode's slash-panel visibility on its required bundle being mounted.
	items = append(items, domain.MonoCardListItem{
		ID:   "builtin:mode:workflow",
		Type: "mode",
		Data: map[string]any{"componentKind": "mode", "lifecycleManaged": true, "requires": []string{"builtin:bundle:workflow-tools"}, "flow": "orchestration"},
	})
	stripListCardBodies(items)
	if items[3].Data["lifecycleManaged"] != true {
		t.Errorf("lifecycleManaged lost: %#v", items[3].Data)
	}
	if req, ok := items[3].Data["requires"].([]string); !ok || len(req) != 1 || req[0] != "builtin:bundle:workflow-tools" {
		t.Errorf("requires lost or corrupted: %#v", items[3].Data["requires"])
	}
	if items[3].Data["flow"] != "orchestration" {
		t.Errorf("flow lost: %#v", items[3].Data)
	}
	if items[1].Data["componentKind"] != "bundle" {
		t.Errorf("bundle componentKind lost: %#v", items[1].Data)
	}
	if _, ok := items[0].Data["executor"]; ok {
		t.Errorf("executor should be stripped")
	}
}

// TestStripListCardBodiesPreservesSchedulerConfig verifies that the
// scheduler card config keys survive the metadata-only strip: the Scheduled
// view and the schedule modal read cron, bound_agent, bind_mode and task
// mode from list items. Without them the schedule vanishes after a
// metadata-only list reload and the cron editor re-seeds from an empty cron,
// defaulting to "0 9 * * *" (the createSchedulerCard default) while the bound
// agent reads as unbound — the "task reset to every-day-09:00 / bound agent
// lost after a run" bug (the run triggers a card-list reload).
func TestStripListCardBodiesPreservesSchedulerConfig(t *testing.T) {
	const probeCron = "0 */2 * * *"
	items := []domain.MonoCardListItem{
		{
			ID:   "scheduler:probe",
			Type: "scheduler",
			Data: map[string]any{
				"schedule":      map[string]any{"cron": probeCron, "enabled": true},
				"schedule_type": "task",
				"task_mode":     "prompt",
				"bind_mode":     "bound",
				"bound_agent":   "agent:coder",
				"agent_kind":    "coder",
				"model_slots":   `{"primary":"agent:coder"}`,
				"agent_actions": `[{"action":"pause","target":"agent:coder"}]`,
				"agent_action":  "pause",
				"target_agent":  "agent:coder",
				// Legacy non-essential field: must stay stripped.
				"executor": "agent:coder",
			},
		},
	}
	stripListCardBodies(items)

	sched, ok := items[0].Data["schedule"].(map[string]any)
	if !ok || sched["cron"] != probeCron || sched["enabled"] != true {
		t.Fatalf("schedule must survive strip verbatim: %#v", items[0].Data["schedule"])
	}
	for _, k := range []string{"schedule_type", "task_mode", "bind_mode", "bound_agent", "agent_kind", "model_slots", "agent_actions", "agent_action", "target_agent"} {
		if _, ok := items[0].Data[k]; !ok {
			t.Errorf("%s must survive strip: %#v", k, items[0].Data)
		}
	}
	if items[0].Data["bound_agent"] != "agent:coder" {
		t.Errorf("bound_agent lost: %#v", items[0].Data["bound_agent"])
	}
	// executor stays stripped (legacy non-essential; has its own contract).
	if _, ok := items[0].Data["executor"]; ok {
		t.Errorf("executor should stay stripped for scheduler cards")
	}
	if items[0].Raw != "" {
		t.Errorf("Raw should be stripped")
	}
}

func TestMigrateStripBuiltinTags(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	// Seed a card carrying __builtin_* tags that must be stripped.
	raw := "---\nid: legacy-card\ntags: [__builtin_plan__, note, __builtin_kanban__]\n---\n\nBody."
	if err := a.store.Save(&CardRecord{Title: "legacy-card", Raw: raw}); err != nil {
		t.Fatal(err)
	}

	a.migrateStripBuiltinTags()

	card, err := a.store.Get("legacy-card")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if strings.Contains(card.Raw, "__builtin_") {
		t.Errorf("builtin tag not stripped: %q", card.Raw)
	}
	if !strings.Contains(card.Raw, "note") {
		t.Errorf("non-builtin tag 'note' should survive: %q", card.Raw)
	}
}

func TestStripBuiltinTagsFromRaw(t *testing.T) {
	cases := []struct {
		name, input, want string
	}{
		{
			"removes builtin and preserves others",
			"---\nid: T\ntags: [__builtin_plan__, keep]\n---\n\nbody",
			"---\nid: T\ntags: [keep]\n---\n\nbody",
		},
		{
			"removes bare builtin tag",
			"---\nid: T\ntags: [builtin, x]\n---\n\nbody",
			"---\nid: T\ntags: [x]\n---\n\nbody",
		},
		{
			"empties when all tags are builtin",
			"---\nid: T\ntags: [__builtin_kanban__]\n---\n\nbody",
			"---\nid: T\ntags: []\n---\n\nbody",
		},
		{
			"no frontmatter unchanged",
			"just body",
			"just body",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stripBuiltinTagsFromRaw(c.input); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestSeedBuiltinSkillCards_PreservesLegacySkill reproduces the skill-load
// regression: when a project still carries legacy "skill$" files on disk (no
// canonical %3A-encoded file yet), seedBuiltinSkillCards must not delete the
// only copy without writing the canonical one, otherwise the skill disappears
// from list_all_cards and never reaches agents.
func TestSeedBuiltinSkillCards_PreservesLegacySkill(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	dir := filepath.Join(tmp, wikiDir)
	legacyRaw := "---\nname: skill-creator\ndescription: legacy\n---\n\nlegacy skill body\n"
	if err := os.WriteFile(filepath.Join(dir, "skill$skill-creator.md"), []byte(legacyRaw), 0o644); err != nil {
		t.Fatal(err)
	}

	a.seedBuiltinSkillCards()

	if _, err := a.store.Get("skill:skill-creator"); err != nil {
		t.Fatalf("skill:skill-creator dropped after seed: %v", err)
	}
	items, err := a.listAllCardItems(newTestContext(t))
	if err != nil {
		t.Fatalf("listAllCardItems: %v", err)
	}
	var found bool
	for _, it := range items {
		if it.ID == "skill:skill-creator" {
			found = true
		}
	}
	if !found {
		t.Fatal("skill:skill-creator missing from list_all_cards after seed")
	}
}

// TestRemoveSystemCards_ClearsBuiltinSkillResidue verifies that
// removeSystemCards removes builtin skill residues that lost their protected
// marker during the legacy SkillEditor migration (the card is matched against
// the embed builtin skill id set, not the protected flag), while preserving
// genuine project-level skills whose id is not an embed builtin. It also
// exercises the Get/Delete legacy "skill$" symmetry.
func TestRemoveSystemCards_ClearsBuiltinSkillResidue(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	dir := filepath.Join(tmp, wikiDir)

	// Builtin residue: SkillEditor data-block format with no source/protected
	// marker, stored under the legacy "skill$" filename — same id as an embed
	// builtin skill. Must be removed.
	builtinResidue := "---\ntitle: skill-creator\ntype: skill\ndata:\n  id: skill-creator\n  description: legacy\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "skill$skill-creator.md"), []byte(builtinResidue), 0o644); err != nil {
		t.Fatal(err)
	}
	// Project-level skill: id is not an embed builtin. Must survive cleanup.
	projectSkill := "---\ntitle: my-project-skill\ntype: skill\ndata:\n  id: my-project-skill\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "skill$my-project-skill.md"), []byte(projectSkill), 0o644); err != nil {
		t.Fatal(err)
	}

	a.removeSystemCards()

	if _, err := os.Stat(filepath.Join(dir, "skill$skill-creator.md")); !os.IsNotExist(err) {
		t.Fatalf("builtin skill residue skill$skill-creator.md must be removed, stat err=%v", err)
	}
	if _, err := a.store.Get("skill:skill-creator"); err == nil {
		t.Fatal("builtin skill residue must not be reachable after cleanup")
	}
	if _, err := a.store.Get("skill:my-project-skill"); err != nil {
		t.Fatalf("project-level skill must be preserved: %v", err)
	}
}

func TestWikiEditCardPatch(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := testutil.AdminCtx(testutil.GenActorID())

	createRaw := "---\nid: patch-card\ntype: wiki\ntags: [foo]\ncreated: 2026-01-01T00:00:00Z\nmodified: 2026-01-01T00:00:00Z\n---\n\n# Title\n\nOriginal body text here.\n"
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "patch-card", Raw: createRaw}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Patch replaces a body fragment while preserving frontmatter.
	if _, err := a.handleWikiEditCard(ctx, domain.WikiEditCardReq{
		ID:        "patch-card",
		OldString: "Original body text here.",
		NewString: "Patched body text.",
	}); err != nil {
		t.Fatalf("patch edit: %v", err)
	}

	getResp, err := a.handleWikiGetCard(ctx, domain.WikiGetCardReq{ID: "patch-card"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(getResp.Raw, "Patched body text.") {
		t.Errorf("body not patched: %q", getResp.Raw)
	}
	if strings.Contains(getResp.Raw, "Original body text here.") {
		t.Errorf("old fragment still present: %q", getResp.Raw)
	}
	if !strings.Contains(getResp.Raw, "id: patch-card") || !strings.Contains(getResp.Raw, "tags: [foo]") {
		t.Errorf("frontmatter not preserved: %q", getResp.Raw)
	}

	// Patch path emits the same card_changed event as full replacement.
	// Snapshot under FakeCtx.mu: the file watcher goroutine may append
	// events concurrently with this read.
	var sawChange bool
	for _, e := range ctx.EmittedEventsSnapshot() {
		if e.Kind == "card_changed" {
			sawChange = true
		}
	}
	if !sawChange {
		t.Errorf("card_changed event not emitted after patch")
	}

	// Patch miss must hard-error.
	if _, err := a.handleWikiEditCard(ctx, domain.WikiEditCardReq{
		ID:        "patch-card",
		OldString: "Nonexistent fragment",
		NewString: "X",
	}); err == nil {
		t.Errorf("expected error on patch miss, got nil")
	}

	// Backward compat: omitting OldString keeps full raw replacement behavior.
	fullRaw := "---\nid: patch-card\ntype: wiki\ntags: [foo]\ncreated: 2026-01-01T00:00:00Z\nmodified: 2026-01-01T00:00:00Z\n---\n\nReplaced entirely.\n"
	if _, err := a.handleWikiEditCard(ctx, domain.WikiEditCardReq{ID: "patch-card", Raw: fullRaw}); err != nil {
		t.Fatalf("full edit: %v", err)
	}
	getResp, err = a.handleWikiGetCard(ctx, domain.WikiGetCardReq{ID: "patch-card"})
	if err != nil {
		t.Fatalf("get after full: %v", err)
	}
	if !strings.Contains(getResp.Raw, "Replaced entirely.") || strings.Contains(getResp.Raw, "Patched body text.") {
		t.Errorf("full replacement not applied: %q", getResp.Raw)
	}
}

func TestWikiEditCardPatchReplaceAll(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	createRaw := "---\nid: patch-multi\ntype: wiki\ntags: []\ncreated: 2026-01-01T00:00:00Z\nmodified: 2026-01-01T00:00:00Z\n---\n\nshared fragment\nshared fragment\nunique line\n"
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "patch-multi", Raw: createRaw}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Multiple matches without ReplaceAll must hard-error with a diagnostic.
	_, err := a.handleWikiEditCard(ctx, domain.WikiEditCardReq{
		ID:        "patch-multi",
		OldString: "shared fragment",
		NewString: "replaced",
	})
	if err == nil || !strings.Contains(err.Error(), "occurs 2 times") {
		t.Fatalf("expected multiple-occurrence error, got %v", err)
	}

	// ReplaceAll=true replaces every occurrence.
	if _, err := a.handleWikiEditCard(ctx, domain.WikiEditCardReq{
		ID:         "patch-multi",
		OldString:  "shared fragment",
		NewString:  "replaced",
		ReplaceAll: true,
	}); err != nil {
		t.Fatalf("replaceAll edit: %v", err)
	}
	getResp, err := a.handleWikiGetCard(ctx, domain.WikiGetCardReq{ID: "patch-multi"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if strings.Contains(getResp.Raw, "shared fragment") {
		t.Errorf("not all occurrences replaced: %q", getResp.Raw)
	}
	if c := strings.Count(getResp.Raw, "replaced"); c != 2 {
		t.Errorf("expected 2 replacements, got %d: %q", c, getResp.Raw)
	}

	// no-op patch (old==new) must hard-error, not silently refresh meta.
	if _, err := a.handleWikiEditCard(ctx, domain.WikiEditCardReq{
		ID:        "patch-multi",
		OldString: "unique line",
		NewString: "unique line",
	}); err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("expected no-op error, got %v", err)
	}
}

func TestWikiEditCardPatchEnforcesBuiltinTags(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Seed a builtin card directly with non-canonical tags so the patch path's
	// tag enforcement is observable (a patch only touches the body).
	seedRaw := "---\nid: __builtin_skill__\ntags: [custom-tag]\nlist: []\ncreated: \"1970-01-01T00:00:00Z\"\nmodified: \"1970-01-01T00:00:00Z\"\n---\n\nAlpha body.\n"
	if err := a.store.Save(&CardRecord{Title: builtinSkillID, Raw: seedRaw}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := a.handleWikiEditCard(ctx, domain.WikiEditCardReq{
		ID:        builtinSkillID,
		OldString: "Alpha body.",
		NewString: "Beta body.",
	}); err != nil {
		t.Fatalf("patch builtin: %v", err)
	}

	card, err := a.handleWikiGetCard(ctx, domain.WikiGetCardReq{ID: builtinSkillID})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(card.Raw, "Beta body.") {
		t.Errorf("body not patched: %q", card.Raw)
	}
	if strings.Contains(card.Raw, "custom-tag") {
		t.Errorf("builtin tags not enforced in patch path: %q", card.Raw)
	}
}

func TestWikiEditCardPatchSyncsScheduler(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := testutil.AdminCtx(testutil.GenActorID())

	var schedulerCalls []string
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name != "scheduler" {
			return nil, false
		}
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
			schedulerCalls = append(schedulerCalls, callID)
			return nil
		}), true
	}

	createRaw := "---\nid: scheduler:demo\ntype: scheduler\ntags: []\ndata:\n  executor: agent:coder\n  schedule:\n    cron: \"*/5 * * * *\"\n---\n\nTimer body text.\n"
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "scheduler:demo", Raw: createRaw}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Clear calls triggered by create to observe the patch in isolation.
	schedulerCalls = nil
	if _, err := a.handleWikiEditCard(ctx, domain.WikiEditCardReq{
		ID:        "scheduler:demo",
		OldString: "Timer body text.",
		NewString: "Patched timer body.",
	}); err != nil {
		t.Fatalf("patch scheduler card: %v", err)
	}

	var registered bool
	for _, c := range schedulerCalls {
		if c == "scheduler.register" {
			registered = true
		}
	}
	if !registered {
		t.Errorf("expected scheduler.register after patch; got %v", schedulerCalls)
	}
}

func TestWikiSearchCardContent(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Card whose body mentions a distinctive term.
	rawA := "---\nid: card-alpha\ntype: wiki\ntags: [docs]\ncreated: 2026-01-01T00:00:00Z\nmodified: 2026-01-01T00:00:00Z\n---\n\n# Alpha\n\nThis card discusses the quaternion rotation convention.\n"
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "card-alpha", Raw: rawA}); err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	// A second card mentioning the same term under a different tag.
	rawB := "---\nid: card-beta\ntype: note\ntags: [draft]\ncreated: 2026-01-01T00:00:00Z\nmodified: 2026-01-01T00:00:00Z\n---\n\nNotes on the quaternion math here.\n"
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "card-beta", Raw: rawB}); err != nil {
		t.Fatalf("create beta: %v", err)
	}
	// Unrelated card.
	rawC := "---\nid: card-gamma\ntype: wiki\ntags: [docs]\n---\n\nNothing relevant.\n"
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "card-gamma", Raw: rawC}); err != nil {
		t.Fatalf("create gamma: %v", err)
	}

	// Substring match is case-insensitive and matches both relevant cards.
	resp, err := a.handleWikiSearchCardContent(ctx, domain.WikiSearchCardContentReq{Query: "quaternion"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if resp.Total != 2 {
		t.Fatalf("Total = %d, want 2; matches=%+v", resp.Total, resp.Matches)
	}
	ids := map[string]bool{}
	for _, m := range resp.Matches {
		ids[m.Card.ID] = true
		if !strings.Contains(strings.ToLower(m.Snippet), "quaternion") {
			t.Errorf("snippet %q does not contain query", m.Snippet)
		}
		if m.Card.Raw != "" {
			t.Errorf("card body not stripped for %s: Raw=%q", m.Card.ID, m.Card.Raw)
		}
	}
	if !ids["card-alpha"] || !ids["card-beta"] {
		t.Errorf("expected both alpha and beta, got %v", ids)
	}

	// tag filter narrows to one.
	resp, err = a.handleWikiSearchCardContent(ctx, domain.WikiSearchCardContentReq{Query: "quaternion", Tags: []string{"draft"}})
	if err != nil {
		t.Fatalf("search tagged: %v", err)
	}
	if resp.Total != 1 || (len(resp.Matches) > 0 && resp.Matches[0].Card.ID != "card-beta") {
		t.Errorf("tag filter: Total=%d matches=%+v", resp.Total, resp.Matches)
	}

	// type filter narrows. card-beta's frontmatter "type: note" is normalized
	// to "wiki" by cardMetadata, so filter by the canonical type.
	resp, err = a.handleWikiSearchCardContent(ctx, domain.WikiSearchCardContentReq{Query: "quaternion", Type: "wiki"})
	if err != nil {
		t.Fatalf("search typed: %v", err)
	}
	if resp.Total != 2 {
		t.Errorf("type filter: Total=%d want 2; matches=%+v", resp.Total, resp.Matches)
	}

	// regexp mode.
	resp, err = a.handleWikiSearchCardContent(ctx, domain.WikiSearchCardContentReq{Query: "/quat.+/i"})
	if err != nil {
		t.Fatalf("search regexp: %v", err)
	}
	if resp.Total != 2 {
		t.Errorf("regexp Total = %d, want 2", resp.Total)
	}

	// invalid regexp matches nothing.
	resp, err = a.handleWikiSearchCardContent(ctx, domain.WikiSearchCardContentReq{Query: "/(/"})
	if err != nil {
		t.Fatalf("search invalid regexp: %v", err)
	}
	if resp.Total != 0 {
		t.Errorf("invalid regexp Total = %d, want 0", resp.Total)
	}

	// no match.
	resp, err = a.handleWikiSearchCardContent(ctx, domain.WikiSearchCardContentReq{Query: "nonexistent-term-xyz"})
	if err != nil {
		t.Fatalf("search miss: %v", err)
	}
	if resp.Total != 0 {
		t.Errorf("miss Total = %d, want 0", resp.Total)
	}

	// empty query errors.
	if _, err := a.handleWikiSearchCardContent(ctx, domain.WikiSearchCardContentReq{Query: ""}); err == nil {
		t.Errorf("expected error on empty query")
	}
}

func TestWikiSearchCardContentLimitAndLine(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Card with the term on a non-first line so line number is observable.
	raw := "---\nid: liney\ntype: wiki\ntags: []\n---\n\nIntro line.\nSecond line.\nTarget term here.\nFourth line.\n"
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "liney", Raw: raw}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Two more matching cards to exercise limit.
	for _, id := range []string{"m1", "m2"} {
		r := "---\nid: " + id + "\ntype: wiki\ntags: []\n---\n\nTarget term.\n"
		if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: id, Raw: r}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	// line number is reported (body starts after frontmatter+blank, so "Target
	// term here." is body line 3 -> overall line 3).
	resp, err := a.handleWikiSearchCardContent(ctx, domain.WikiSearchCardContentReq{Query: "Target term"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	var lineyLine int32
	for _, m := range resp.Matches {
		if m.Card.ID == "liney" {
			lineyLine = m.Line
		}
	}
	if lineyLine != 3 {
		t.Errorf("liney line = %d, want 3", lineyLine)
	}

	// Total untruncated while matches limited.
	resp, err = a.handleWikiSearchCardContent(ctx, domain.WikiSearchCardContentReq{Query: "Target term", Limit: 1})
	if err != nil {
		t.Fatalf("search limited: %v", err)
	}
	if resp.Total != 3 {
		t.Errorf("Total = %d, want 3", resp.Total)
	}
	if len(resp.Matches) != 1 {
		t.Errorf("matches len = %d, want 1 (limit)", len(resp.Matches))
	}
}

func TestMatchCardBody(t *testing.T) {
	body := "alpha\nbeta gamma\ndelta\n"
	re, isRe, invalid := compileWikiTitleQuery("beta")
	line, snippet, ok := matchCardBody(body, "beta", re, isRe, invalid)
	if !ok || line != 2 || !strings.Contains(snippet, "beta") {
		t.Errorf("substring match: ok=%v line=%d snippet=%q", ok, line, snippet)
	}

	// case-insensitive substring.
	re, isRe, invalid = compileWikiTitleQuery("BETA")
	_, _, ok = matchCardBody(body, "BETA", re, isRe, invalid)
	if !ok {
		t.Errorf("case-insensitive substring should match")
	}

	// regexp anchored: without the m flag, ^/$ match body start/end, so a
	// multi-line body does not match. With /m they anchor to line boundaries.
	re, isRe, invalid = compileWikiTitleQuery("/^alpha$/")
	_, _, ok = matchCardBody(body, "/^alpha$/", re, isRe, invalid)
	if ok {
		t.Errorf("anchored regexp without m flag should not match multi-line body")
	}
	re, isRe, invalid = compileWikiTitleQuery("/^alpha$/m")
	_, _, ok = matchCardBody(body, "/^alpha$/m", re, isRe, invalid)
	if !ok {
		t.Errorf("anchored regexp with m flag should match a line")
	}

	// not found.
	re, isRe, invalid = compileWikiTitleQuery("zzz")
	_, _, ok = matchCardBody(body, "zzz", re, isRe, invalid)
	if ok {
		t.Errorf("expected no match")
	}

	// snippet trimmed and bounded.
	long := strings.Repeat("x", 300) + " needle"
	re, isRe, invalid = compileWikiTitleQuery("needle")
	_, snippet, ok = matchCardBody(long, "needle", re, isRe, invalid)
	if !ok || len(snippet) > 160 {
		t.Errorf("snippet not bounded: len=%d snippet=%q", len(snippet), snippet)
	}

	// snippet with multi-byte runes must stay valid UTF-8 after truncation
	// (byte-slice truncation would split a 3-byte CJK rune, producing invalid
	// UTF-8 that the JSON codec rejects).
	cjk := strings.Repeat("中", 100) + " needle"
	re, isRe, invalid = compileWikiTitleQuery("needle")
	_, snippet, ok = matchCardBody(cjk, "needle", re, isRe, invalid)
	if !ok {
		t.Fatalf("cjk match failed")
	}
	if !utf8.ValidString(snippet) {
		t.Errorf("snippet not valid UTF-8: %q", snippet)
	}
}

// TestWikiOpenCardsConcurrent hammers the open-cards handlers: M workers
// open/close disjoint card ids while a dedicated overwriter periodically
// replaces the whole list via handleWikiSaveOpenCards. A monitor polls
// snapshots and asserts the no-duplicates invariant while writes are in
// flight. After quiescence, a deterministic tail verifies last-writer-wins
// semantics: sequential writes after the storm must be reflected exactly.
func TestWikiOpenCardsConcurrent(t *testing.T) {
	a, ctx := freshProject(t, t.TempDir())

	const workers = 8
	const iterations = 50

	universe := map[string]bool{}
	idsFor := func(w int) []string {
		return []string{"card-" + strconv.Itoa(w) + "-a", "card-" + strconv.Itoa(w) + "-b"}
	}
	for w := 0; w < workers; w++ {
		for _, id := range idsFor(w) {
			universe[id] = true
		}
	}
	universe["overwrite-x"] = true
	universe["overwrite-y"] = true

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			ids := idsFor(w)
			for i := 0; i < iterations; i++ {
				open := i%2 == 0
				for _, id := range ids {
					if open {
						if _, err := a.handleWikiOpenCard(ctx, domain.WikiOpenCardReq{ID: id}); err != nil {
							t.Errorf("open_card %q: %v", id, err)
							return
						}
					} else {
						if _, err := a.handleWikiCloseCard(ctx, domain.WikiCloseCardReq{ID: id}); err != nil {
							t.Errorf("close_card %q: %v", id, err)
							return
						}
					}
				}
			}
		}(w)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		lists := [][]string{
			{"overwrite-x"},
			{"overwrite-y", "card-0-a", "card-3-b"},
		}
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := a.handleWikiSaveOpenCards(ctx, domain.WikiSaveOpenCardsReq{OpenCards: lists[i%len(lists)]}); err != nil {
				t.Errorf("save_open_cards: %v", err)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			snap, _ := a.openCardsSnapshot()
			seen := map[string]bool{}
			for _, id := range snap {
				if seen[id] {
					t.Errorf("duplicate open card %q in snapshot %v", id, snap)
					return
				}
				seen[id] = true
				if !universe[id] {
					t.Errorf("unexpected open card %q in snapshot %v", id, snap)
					return
				}
			}
			time.Sleep(50 * time.Microsecond)
		}
	}()

	close(stop)
	wg.Wait()

	snap, _ := a.openCardsSnapshot()
	assertNoDuplicates(t, snap)

	// Deterministic tail: once all writers have stopped, each sequential write
	// must be exactly what the next read observes (last writer wins).
	saved, err := a.handleWikiSaveOpenCards(ctx, domain.WikiSaveOpenCardsReq{OpenCards: []string{"tail-first", "tail-second"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"tail-first", "tail-second"}; !reflect.DeepEqual(saved.OpenCards, want) {
		t.Fatalf("save response = %v, want %v", saved.OpenCards, want)
	}
	got, err := a.handleWikiGetOpenCards(ctx, domain.WikiOpenCardsReq{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"tail-first", "tail-second"}; !reflect.DeepEqual(got.OpenCards, want) {
		t.Fatalf("get_open_cards = %v, want %v", got.OpenCards, want)
	}
	if _, err := a.handleWikiCloseCard(ctx, domain.WikiCloseCardReq{ID: "tail-second"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleWikiOpenCard(ctx, domain.WikiOpenCardReq{ID: "tail-third"}); err != nil {
		t.Fatal(err)
	}
	got, err = a.handleWikiGetOpenCards(ctx, domain.WikiOpenCardsReq{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"tail-third", "tail-first"}; !reflect.DeepEqual(got.OpenCards, want) {
		t.Fatalf("after close+open = %v, want %v (open prepends, close removes)", got.OpenCards, want)
	}
}

func assertNoDuplicates(t *testing.T, cards []string) {
	t.Helper()
	seen := map[string]bool{}
	for _, id := range cards {
		if seen[id] {
			t.Fatalf("duplicate open card %q in %v", id, cards)
		}
		seen[id] = true
	}
}
