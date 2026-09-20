package workspace

import (
	"errors"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// starTestIDs returns n distinct actor ids (testutil.GenActorID always yields
// the same deterministic id, which would collide as a map key here).
func starTestIDs(n int) []string {
	ts := uint64(0)
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	out := make([]string, n)
	for i := range out {
		out[i] = g.Next().String()
	}
	return out
}

func TestWikiListStarredAggregatesAcrossMounts(t *testing.T) {
	ids := starTestIDs(2)
	p1, p2 := ids[0], ids[1]
	a := &Actor{
		Mounts: []domain.ProjectRef{
			{Name: "alpha", ActorID: p1},
			{Name: "beta", ActorID: p2},
		},
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		var reply domain.WikiStarredResp
		switch aid.String() {
		case p1:
			reply = domain.WikiStarredResp{Starred: []string{"c1", "c2"}}
		case p2:
			reply = domain.WikiStarredResp{Starred: []string{"c9"}}
		default:
			return nil, false
		}
		return testutil.NewFakeRef(aid, func(callID string, _ any) any {
			if callID != "project.wiki_get_starred" {
				return errors.New("unexpected callable: " + callID)
			}
			return reply
		}), true
	}

	resp, err := a.handleWikiListStarred(ctx, domain.WikiListStarredReq{})
	if err != nil {
		t.Fatalf("wiki_list_starred: %v", err)
	}
	want := []domain.WikiStarredEntry{
		{ProjectID: p1, ProjectName: "alpha", CardID: "c1"},
		{ProjectID: p1, ProjectName: "alpha", CardID: "c2"},
		{ProjectID: p2, ProjectName: "beta", CardID: "c9"},
	}
	if len(resp.Items) != len(want) {
		t.Fatalf("items = %+v, want %+v", resp.Items, want)
	}
	for i := range want {
		if resp.Items[i] != want[i] {
			t.Fatalf("item[%d] = %+v, want %+v", i, resp.Items[i], want[i])
		}
	}
}

// TestWikiListStarredSkipsUnreachableMounts verifies best-effort aggregation: a
// mount whose actor is not running, and a mount whose forward errors, are both
// skipped without failing the whole query or dropping the reachable projects.
func TestWikiListStarredSkipsUnreachableMounts(t *testing.T) {
	ids := starTestIDs(3)
	good, missing, failing := ids[0], ids[1], ids[2]
	a := &Actor{
		Mounts: []domain.ProjectRef{
			{Name: "good", ActorID: good},
			{Name: "missing", ActorID: missing},
			{Name: "failing", ActorID: failing},
		},
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		switch aid.String() {
		case good:
			return testutil.NewFakeRef(aid, func(string, any) any {
				return domain.WikiStarredResp{Starred: []string{"kept"}}
			}), true
		case failing:
			return testutil.NewFakeRef(aid, func(string, any) any {
				return errors.New("project actor down")
			}), true
		default:
			return nil, false
		}
	}

	resp, err := a.handleWikiListStarred(ctx, domain.WikiListStarredReq{})
	if err != nil {
		t.Fatalf("wiki_list_starred: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].CardID != "kept" || resp.Items[0].ProjectName != "good" {
		t.Fatalf("items = %+v, want single kept entry from good", resp.Items)
	}
}

// TestWikiListStarredSkipsSystemMetaMount verifies the workspace's internal
// system meta project (`System: true`, the mount that holds builtin cards) never
// surfaces in the user-facing global star aggregate, mirroring the front-end
// enumeration policy (`kbData.listKbProjects`).
func TestWikiListStarredSkipsSystemMetaMount(t *testing.T) {
	ids := starTestIDs(2)
	user, sys := ids[0], ids[1]
	a := &Actor{
		Mounts: []domain.ProjectRef{
			{Name: "workspace", ActorID: sys, System: true},
			{Name: "alpha", ActorID: user},
		},
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	lookedUp := make([]string, 0, 2)
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		lookedUp = append(lookedUp, aid.String())
		return testutil.NewFakeRef(aid, func(string, any) any {
			return domain.WikiStarredResp{Starred: []string{"card-" + aid.String()}}
		}), true
	}

	resp, err := a.handleWikiListStarred(ctx, domain.WikiListStarredReq{})
	if err != nil {
		t.Fatalf("wiki_list_starred: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].ProjectID != user {
		t.Fatalf("items = %+v, want only the user project's entries", resp.Items)
	}
	for _, aid := range lookedUp {
		if aid == sys {
			t.Fatalf("system meta mount was queried: %v", lookedUp)
		}
	}
}

func TestWikiListStarredNoMounts(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	resp, err := a.handleWikiListStarred(ctx, domain.WikiListStarredReq{})
	if err != nil {
		t.Fatalf("wiki_list_starred: %v", err)
	}
	if len(resp.Items) != 0 {
		t.Fatalf("items = %+v, want empty", resp.Items)
	}
}
