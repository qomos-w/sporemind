package workspace

import (
	"fmt"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestSystemWikiListAllCardsForwardsToSystemMetaProject(t *testing.T) {
	sysActorID := testutil.GenActorID()
	a := &Actor{
		Mounts: []domain.ProjectRef{
			{Name: "workspace", Path: systemMetaProjectPath(), Root: true, System: true, ActorID: sysActorID.String()},
		},
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	var forwardedCallID string
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(sysActorID, func(callID string, _ any) any {
			forwardedCallID = callID
			return domain.WikiListCardsResp{Cards: []domain.MonoCardListItem{{ID: "skill:skill-creator"}}}
		}), true
	}

	resp, err := a.handleSystemWikiListCards(ctx, domain.WikiListCardsReq{Flat: true})
	if err != nil {
		t.Fatalf("handleSystemWikiListCards: %v", err)
	}
	if forwardedCallID != "project.wiki_list_cards" {
		t.Fatalf("expected forward to project.wiki.list_cards, got %s", forwardedCallID)
	}
	if len(resp.Cards) != 1 || resp.Cards[0].ID != "skill:skill-creator" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestSystemWikiListAllCardsErrorWhenNoSystemProject(t *testing.T) {
	a := &Actor{Mounts: nil}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	_, err := a.handleSystemWikiListCards(ctx, domain.WikiListCardsReq{Flat: true})
	if err == nil {
		t.Fatal("expected error when system meta project is unavailable")
	}
}

func TestSystemWikiListAllCardsForwardsIncludeRaw(t *testing.T) {
	sysActorID := testutil.GenActorID()
	a := &Actor{
		Mounts: []domain.ProjectRef{
			{Name: "workspace", Path: systemMetaProjectPath(), Root: true, System: true, ActorID: sysActorID.String()},
		},
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	var forwardedReq any
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(sysActorID, func(callID string, payload any) any {
			forwardedReq = payload
			return domain.WikiListCardsResp{Cards: []domain.MonoCardListItem{{ID: "skill:skill-creator"}}}
		}), true
	}

	_, err := a.handleSystemWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, IncludeRaw: true})
	if err != nil {
		t.Fatalf("handleSystemWikiListCards: %v", err)
	}
	req, ok := forwardedReq.(domain.WikiListCardsReq)
	if !ok {
		t.Fatalf("unexpected forwarded request type: %T", forwardedReq)
	}
	if !req.IncludeRaw {
		t.Fatal("expected IncludeRaw=true to be forwarded to the system meta project")
	}
}

func TestSystemWikiForwardRetriesCallNotRegistered(t *testing.T) {
	sysActorID := testutil.GenActorID()
	a := &Actor{
		Mounts: []domain.ProjectRef{
			{Name: "workspace", Path: systemMetaProjectPath(), Root: true, System: true, ActorID: sysActorID.String()},
		},
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	calls := 0
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(sysActorID, func(callID string, _ any) any {
			calls++
			if calls <= 2 {
				// spawn window: ref resolves before OnStart registered callables
				return fmt.Errorf("gospore.cell: call ID %q not registered", callID)
			}
			return domain.WikiListCardsResp{Cards: []domain.MonoCardListItem{{ID: "skill:skill-creator"}}}
		}), true
	}

	resp, err := a.handleSystemWikiListCards(ctx, domain.WikiListCardsReq{Flat: true})
	if err != nil {
		t.Fatalf("handleSystemWikiListCards should ride out not-registered skew: %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls (2 not-registered + 1 success), got %d", calls)
	}
	if len(resp.Cards) != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestSystemWikiForwardDoesNotRetryOtherErrors(t *testing.T) {
	sysActorID := testutil.GenActorID()
	a := &Actor{
		Mounts: []domain.ProjectRef{
			{Name: "workspace", Path: systemMetaProjectPath(), Root: true, System: true, ActorID: sysActorID.String()},
		},
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	calls := 0
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(sysActorID, func(callID string, _ any) any {
			calls++
			return fmt.Errorf("gospore.handler.error: boom")
		}), true
	}

	_, err := a.handleSystemWikiListCards(ctx, domain.WikiListCardsReq{Flat: true})
	if err == nil {
		t.Fatal("expected error to propagate")
	}
	if calls != 1 {
		t.Fatalf("non-registration errors must not be retried, got %d calls", calls)
	}
}

func TestSystemWikiSearchCardsForwardsToSystemMetaProject(t *testing.T) {
	sysActorID := testutil.GenActorID()
	a := &Actor{
		Mounts: []domain.ProjectRef{
			{Name: "workspace", Path: systemMetaProjectPath(), Root: true, System: true, ActorID: sysActorID.String()},
		},
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	var forwardedCallID string
	var forwardedReq any
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(sysActorID, func(callID string, payload any) any {
			forwardedCallID = callID
			forwardedReq = payload
			return domain.WikiListCardsResp{
				Cards: []domain.MonoCardListItem{{ID: "skill:skill-creator", Tags: []string{"skill"}}},
				Total: 1,
			}
		}), true
	}

	resp, err := a.handleSystemWikiListCards(ctx, domain.WikiListCardsReq{Flat: true, Tags: []string{"skill"}})
	if err != nil {
		t.Fatalf("handleSystemWikiListCards: %v", err)
	}
	if forwardedCallID != "project.wiki_list_cards" {
		t.Fatalf("expected forward to project.wiki.list_cards, got %s", forwardedCallID)
	}
	if req, ok := forwardedReq.(domain.WikiListCardsReq); !ok || len(req.Tags) != 1 || req.Tags[0] != "skill" {
		t.Fatalf("unexpected forwarded request: %#v", forwardedReq)
	}
	if resp.Total != 1 || len(resp.Cards) != 1 || resp.Cards[0].ID != "skill:skill-creator" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}
