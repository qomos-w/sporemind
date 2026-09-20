package project

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func newCardHandlerTestActor(t *testing.T) *Actor {
	t.Helper()
	a := NewActor(t.TempDir())().(*Actor)
	a.actorID = "test-project"
	a.persistStore = persist.NewFSPersist(t.TempDir())
	// Mount lifecycle semantics are scope-independent; register the builtin
	// provider explicitly so builtin:* refs resolve for these tests.
	a.externalProviders = append([]ExternalCardProvider{newBuiltinComponentCardProvider()}, a.externalProviders...)
	return a
}

func TestProjectCardMutationRequiresHumanIdentity(t *testing.T) {
	a := newCardHandlerTestActor(t)
	ctx := testutil.AnonCtx(testutil.GenActorID())
	if _, err := a.handleProjectCardMount(ctx, gen.ProjectCardMountReq{Ref: gen.CardRef{ID: "builtin:bundle:debug"}}); err == nil {
		t.Fatal("anonymous caller must not mount project cards")
	}
	if _, err := a.handleProjectCardUnmount(ctx, gen.ProjectCardUnmountReq{ID: "builtin:bundle:debug"}); err == nil {
		t.Fatal("anonymous caller must not unmount project cards")
	}
}

func TestProjectCardMountLifecycle(t *testing.T) {
	a := newCardHandlerTestActor(t)
	ref := gen.CardRef{ID: "builtin:bundle:debug", Source: "builtin"}
	mounted, err := a.handleProjectCardMount(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectCardMountReq{Ref: ref})
	if err != nil {
		t.Fatal(err)
	}
	refs, err := a.mountedCardRefsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if mounted.Ref.Scope != "project" || refs[0].Scope != "project" {
		t.Fatalf("project scope must be explicit: mounted=%+v refs=%+v", mounted.Ref, refs)
	}
	if _, err := a.handleProjectCardMount(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectCardMountReq{Ref: ref}); err != nil {
		t.Fatalf("duplicate mount should be idempotent: %v", err)
	}
	refs, err = a.mountedCardRefsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("duplicate mount changed refs: %+v", refs)
	}
	if _, err := a.handleProjectCardMount(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectCardMountReq{Ref: gen.CardRef{ID: "missing"}}); err == nil {
		t.Fatal("missing card should be rejected")
	}
	if _, err := a.handleProjectCardMount(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectCardMountReq{Ref: gen.CardRef{ID: ref.ID, Scope: "agent"}}); err == nil {
		t.Fatal("project mount should reject agent scope")
	}
	if _, err := a.handleProjectCardUnmount(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectCardUnmountReq{ID: ref.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleProjectCardUnmount(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectCardUnmountReq{}); err == nil {
		t.Fatal("empty card id should be rejected")
	}
}

func TestProjectCardScopePolicyMatrix(t *testing.T) {
	a := newCardHandlerTestActor(t)
	human := testutil.AdminCtx(testutil.GenActorID())
	if _, err := a.handleProjectCardMount(human, gen.ProjectCardMountReq{Ref: gen.CardRef{ID: "builtin:bundle:debug", Scope: "agent"}}); err == nil {
		t.Fatal("agent-scoped ref must be rejected by project mount")
	}
	mounted, err := a.handleProjectCardMount(human, gen.ProjectCardMountReq{Ref: gen.CardRef{ID: "builtin:bundle:debug", Scope: "project"}})
	if err != nil {
		t.Fatal(err)
	}
	if mounted.Ref.Scope != "project" {
		t.Fatalf("mount scope = %q, want project", mounted.Ref.Scope)
	}
	list, err := a.handleProjectCardList(testutil.AnonCtx(testutil.GenActorID()), gen.ProjectCardListReq{})
	if err != nil || len(list.Refs) != 1 || list.Refs[0].Scope != "project" {
		t.Fatalf("public project card list = %+v, err=%v", list.Refs, err)
	}
	if _, err := a.handleProjectCardUnmount(testutil.AnonCtx(testutil.GenActorID()), gen.ProjectCardUnmountReq{ID: mounted.Ref.ID}); err == nil {
		t.Fatal("anonymous caller must not unmount project card")
	}
}

func TestProjectCardMountPersists(t *testing.T) {
	a := newCardHandlerTestActor(t)
	ref := gen.CardRef{ID: "builtin:bundle:debug"}
	if _, err := a.handleProjectCardMount(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectCardMountReq{Ref: ref}); err != nil {
		t.Fatal(err)
	}
	var card configCard
	if err := a.persistStore.Load(a.configCardName(), &card); err != nil {
		t.Fatal(err)
	}
	if len(card.MountedCardRefs) != 1 || card.MountedCardRefs[0].ID != ref.ID {
		t.Fatalf("unexpected persisted refs: %+v", card.MountedCardRefs)
	}
	b := NewActor(t.TempDir())().(*Actor)
	b.actorID = a.actorID
	b.persistStore = a.persistStore
	refs, err := b.mountedCardRefsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].ID != ref.ID {
		t.Fatalf("unexpected restored refs: %+v", refs)
	}
}
