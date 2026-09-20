package project

import (
	"context"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

type prefsWorkspaceRef struct {
	prefs map[string]string
}

func (r *prefsWorkspaceRef) ID() id.ActorID          { return id.ActorID{} }
func (r *prefsWorkspaceRef) Service() (string, bool) { return "workspace", true }
func (r *prefsWorkspaceRef) Invoke(_ context.Context, callID string, _ any, _ ...map[string]string) *invoke.Call {
	switch callID {
	case "workspace.preferences_get":
		return invoke.NewCall(invoke.CallModeUnary, &oneShotStream{value: domain.AccountPreferencesSnapshot{Preferences: r.prefs}})
	}
	return nil
}

func TestGlobalPermissionMode_ReturnsAccountPreference(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return &prefsWorkspaceRef{prefs: map[string]string{"permissionMode": "auto"}}, true
		}
		return nil, false
	}

	if got := a.globalPermissionMode(ctx); got != "auto" {
		t.Errorf("globalPermissionMode() = %q, want %q", got, "auto")
	}
}

func TestGlobalPermissionMode_FallsBackToEmptyWhenUnset(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return &prefsWorkspaceRef{prefs: map[string]string{}}, true
		}
		return nil, false
	}

	if got := a.globalPermissionMode(ctx); got != "" {
		t.Errorf("globalPermissionMode() = %q, want empty string", got)
	}
}

func TestGlobalPermissionMode_FallsBackToEmptyWhenWorkspaceUnavailable(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(string) (ref.Ref, bool) { return nil, false }

	if got := a.globalPermissionMode(ctx); got != "" {
		t.Errorf("globalPermissionMode() = %q, want empty string when workspace is unavailable", got)
	}
}
