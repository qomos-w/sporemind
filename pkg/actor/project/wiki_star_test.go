package project

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestWikiStarredSetGetRoundTrip covers the happy path: star two cards, observe
// most-recently-starred-first ordering, un-star one, and read the list back.
func TestWikiStarredSetGetRoundTrip(t *testing.T) {
	a := newTestActor(t.TempDir())
	ctx := testutil.AdminCtx(testutil.GenActorID())

	got, err := a.handleWikiGetStarred(ctx, domain.WikiGetStarredReq{})
	if err != nil {
		t.Fatalf("get_starred: %v", err)
	}
	if len(got.Starred) != 0 {
		t.Fatalf("initial starred = %v, want empty", got.Starred)
	}

	if _, err := a.handleWikiSetStarred(ctx, domain.WikiSetStarredReq{ID: "card-a", Starred: true}); err != nil {
		t.Fatalf("star card-a: %v", err)
	}
	resp, err := a.handleWikiSetStarred(ctx, domain.WikiSetStarredReq{ID: "card-b", Starred: true})
	if err != nil {
		t.Fatalf("star card-b: %v", err)
	}
	if want := []string{"card-b", "card-a"}; !reflect.DeepEqual(resp.Starred, want) {
		t.Fatalf("after starring = %v, want %v", resp.Starred, want)
	}

	resp, err = a.handleWikiSetStarred(ctx, domain.WikiSetStarredReq{ID: "card-a", Starred: false})
	if err != nil {
		t.Fatalf("unstar card-a: %v", err)
	}
	if want := []string{"card-b"}; !reflect.DeepEqual(resp.Starred, want) {
		t.Fatalf("after unstarring = %v, want %v", resp.Starred, want)
	}

	got, err = a.handleWikiGetStarred(ctx, domain.WikiGetStarredReq{})
	if err != nil {
		t.Fatalf("get_starred: %v", err)
	}
	if want := []string{"card-b"}; !reflect.DeepEqual(got.Starred, want) {
		t.Fatalf("get_starred = %v, want %v", got.Starred, want)
	}
}

// TestWikiStarredIdempotent verifies re-starring an already-starred card and
// un-starring an unknown card are both no-ops (no duplicates, no error).
func TestWikiStarredIdempotent(t *testing.T) {
	a := newTestActor(t.TempDir())
	ctx := testutil.AdminCtx(testutil.GenActorID())

	if _, err := a.handleWikiSetStarred(ctx, domain.WikiSetStarredReq{ID: "card-a", Starred: true}); err != nil {
		t.Fatal(err)
	}
	resp, err := a.handleWikiSetStarred(ctx, domain.WikiSetStarredReq{ID: "card-a", Starred: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"card-a"}; !reflect.DeepEqual(resp.Starred, want) {
		t.Fatalf("re-star = %v, want %v", resp.Starred, want)
	}

	resp, err = a.handleWikiSetStarred(ctx, domain.WikiSetStarredReq{ID: "never-starred", Starred: false})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"card-a"}; !reflect.DeepEqual(resp.Starred, want) {
		t.Fatalf("unstar unknown = %v, want %v", resp.Starred, want)
	}
}

// TestWikiSetStarredRequiresId guards the empty-id rejection.
func TestWikiSetStarredRequiresId(t *testing.T) {
	a := newTestActor(t.TempDir())
	ctx := testutil.AdminCtx(testutil.GenActorID())
	if _, err := a.handleWikiSetStarred(ctx, domain.WikiSetStarredReq{Starred: true}); err == nil {
		t.Fatal("expected error for empty card id")
	}
}

// TestWikiStarredCoexistsWithOpenCards pins the shared uiStateCard read-modify-
// write: starring must not clobber the open-card list and vice versa.
func TestWikiStarredCoexistsWithOpenCards(t *testing.T) {
	a := newTestActor(t.TempDir())
	ctx := testutil.AdminCtx(testutil.GenActorID())

	if _, err := a.handleWikiOpenCard(ctx, domain.WikiOpenCardReq{ID: "open-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleWikiSetStarred(ctx, domain.WikiSetStarredReq{ID: "star-1", Starred: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleWikiOpenCard(ctx, domain.WikiOpenCardReq{ID: "open-2"}); err != nil {
		t.Fatal(err)
	}

	open, err := a.openCardsSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"open-2", "open-1"}; !reflect.DeepEqual(open, want) {
		t.Fatalf("open cards = %v, want %v", open, want)
	}
	starred, err := a.starredSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"star-1"}; !reflect.DeepEqual(starred, want) {
		t.Fatalf("starred = %v, want %v", starred, want)
	}
}

// TestWikiStarredPersistsAcrossRestart proves the star list round-trips through
// persist.Persist: a freshly constructed actor reading the same store path sees
// the starred cards written by the first actor (the .ropen record).
func TestWikiStarredPersistsAcrossRestart(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := testutil.AdminCtx(testutil.GenActorID())

	if _, err := a.handleWikiSetStarred(ctx, domain.WikiSetStarredReq{ID: "persisted-1", Starred: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleWikiSetStarred(ctx, domain.WikiSetStarredReq{ID: "persisted-2", Starred: true}); err != nil {
		t.Fatal(err)
	}

	// Fresh actor, fresh persist handle on the same directory (no shared memory).
	restarted := &Actor{
		actorID:      a.actorID,
		persistStore: persist.NewFSPersist(filepath.Join(tmp, ".sporecode")),
	}
	starred, err := restarted.starredSnapshot()
	if err != nil {
		t.Fatalf("read after restart: %v", err)
	}
	if want := []string{"persisted-2", "persisted-1"}; !reflect.DeepEqual(starred, want) {
		t.Fatalf("starred after restart = %v, want %v", starred, want)
	}
}
