package workspace

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestProjectWikiForwardWhitelistAndRole pins the two fail-closed gates on
// workspace.project_wiki_forward: non-system callers are refused, and only
// the read-only project.wiki_* set may be forwarded.
func TestProjectWikiForwardWhitelistAndRole(t *testing.T) {
	a, _ := freshActor(t)
	projectID := addTestProject(t, a)
	systemCtx := roleCtx(t, a, "system")

	// Role gate: a developer-role caller is refused even with valid input.
	devCtx := roleCtx(t, a, "developer")
	if _, err := a.handleProjectWikiForward(devCtx, domain.ProjectWikiForwardReq{
		ProjectID: projectID, Callable: "project.wiki_list_cards",
	}); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("developer role error = %v, want forbidden", err)
	}

	// Whitelist: mutating project callables are refused.
	for _, callable := range []string{"project.wiki_create_card", "project.wiki_edit_card", "project.wiki_delete_card", "project.list", ""} {
		if _, err := a.handleProjectWikiForward(systemCtx, domain.ProjectWikiForwardReq{
			ProjectID: projectID, Callable: callable,
		}); err == nil || !strings.Contains(err.Error(), "whitelisted") {
			t.Fatalf("callable %q error = %v, want whitelist rejection", callable, err)
		}
	}

	// Unknown project: refused with the resolver error.
	if _, err := a.handleProjectWikiForward(systemCtx, domain.ProjectWikiForwardReq{
		ProjectID: "no-such-project", Callable: "project.wiki_list_cards",
	}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown project error = %v, want resolver failure", err)
	}
}

// TestProjectWikiForwardForwardsToProjectActor pins the happy path: the
// whitelisted callable and raw payload reach the bound project actor and the
// raw JSON response rides back.
func TestProjectWikiForwardForwardsToProjectActor(t *testing.T) {
	a, _ := freshActor(t)
	projectID := addTestProject(t, a)

	var gotCallID string
	var gotPayload string
	fctx := testutil.AdminCtx(testutil.GenActorID())
	fctx.Identity_.Role = id.Role("system")
	fctx.SpawnFn = noOpSpawn
	fctx.LookupIDFn = func(id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			gotCallID = callID
			switch p := payload.(type) {
			case []byte:
				gotPayload = string(p)
			case json.RawMessage:
				gotPayload = string(p)
			default:
				encoded, _ := json.Marshal(p)
				gotPayload = string(encoded)
			}
			return domain.WikiListCardsResp{Total: 7}
		}), true
	}
	ctx := actor.PureContext(fctx)

	resp, err := a.handleProjectWikiForward(ctx, domain.ProjectWikiForwardReq{
		ProjectID: projectID,
		Callable:  "project.wiki_list_cards",
		Payload:   json.RawMessage(`{"Flat":true,"Type":"agent"}`),
	})
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	if gotCallID != "project.wiki_list_cards" {
		t.Fatalf("forwarded callID = %q, want project.wiki_list_cards", gotCallID)
	}
	if !strings.Contains(gotPayload, `"Flat":true`) {
		t.Fatalf("forwarded payload = %s, want the caller's raw JSON", gotPayload)
	}
	var decoded struct {
		Total int `json:"Total"`
	}
	if err := json.Unmarshal(resp.Result, &decoded); err != nil {
		t.Fatalf("decode forwarded response %s: %v", resp.Result, err)
	}
	if decoded.Total != 7 {
		t.Fatalf("forwarded response Total = %d, want 7", decoded.Total)
	}
}
