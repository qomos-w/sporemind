package agent

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestHandleComponentMount_SchedulerModeMounts verifies that
// builtin:mode:scheduler is a mountable component card resolved from the
// embedded builtin assets (no workspace round-trip needed) and that the mount
// lands in ComponentMounts so the composer/sidebar mode badge surface picks it
// up automatically.
func TestHandleComponentMount_SchedulerModeMounts(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{cardRefs: []gen.CardRef{}}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	resp, err := a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: "builtin:mode:scheduler", Enabled: true, Scope: "user"})
	if err != nil {
		t.Fatalf("handleComponentMount(builtin:mode:scheduler): %v", err)
	}
	if resp.Mount.CardID != "builtin:mode:scheduler" {
		t.Fatalf("mounted card = %q, want builtin:mode:scheduler", resp.Mount.CardID)
	}
	if resp.Mount.Kind != "mode" {
		t.Fatalf("mounted kind = %q, want mode", resp.Mount.Kind)
	}
	if resp.Mount.Title == "" || resp.Mount.Icon == "" {
		t.Fatalf("scheduler mount missing badge metadata: %+v", resp.Mount)
	}
	found := false
	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:mode:scheduler" {
			found = true
			if ref.Disabled {
				t.Fatal("expected scheduler mode mount to be enabled")
			}
		}
	}
	if !found {
		t.Fatal("expected builtin:mode:scheduler in cardRefs after mount")
	}
}

// TestHandleComponentUnmount_SchedulerModeClearsActiveScheduler verifies that
// unmounting builtin:mode:scheduler clears the RawSession.ActiveScheduler
// binding list via onModeUnmounted.
func TestHandleComponentUnmount_SchedulerModeClearsActiveScheduler(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			ActiveScheduler: []gen.ActiveSchedulerEntry{
				{SchedulerCardID: "scheduler:daily-sync", SchedulerName: "Daily Sync"},
			},
		},
		cardRefs: []gen.CardRef{
			{ID: "builtin:mode:scheduler", Scope: "user"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	if _, err := a.handleComponentUnmount(ctx, domain.AgentComponentUnmountReq{CardID: "builtin:mode:scheduler"}); err != nil {
		t.Fatalf("handleComponentUnmount: %v", err)
	}
	if a.RawSession.ActiveScheduler != nil {
		t.Fatalf("expected ActiveScheduler to be nil after unmount, got %+v", a.RawSession.ActiveScheduler)
	}
	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:mode:scheduler" {
			t.Fatal("expected builtin:mode:scheduler removed from cardRefs")
		}
	}
}

// TestHandleModesUnloadAll_UnloadsAllModesPreservesBuiltinBundles verifies the
// full modes_unload_all path: every mounted mode card (goal + scheduler) is
// removed, the mode-derived session state (Goal, ActiveScheduler) is cleared,
// and bundles mounted with scope "builtin" (kind config / permission proxies)
// are preserved.
func TestHandleModesUnloadAll_UnloadsAllModesPreservesBuiltinBundles(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{Condition: "do thing", Confirmed: true},
			ActiveScheduler: []gen.ActiveSchedulerEntry{
				{SchedulerCardID: "scheduler:daily-sync", SchedulerName: "Daily Sync"},
			},
		},
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:goal", Scope: "builtin"},
			{ID: "builtin:bundle:workflow-tools", Scope: "builtin"},
			{ID: "builtin:mode:goal", Scope: "user"},
			{ID: "builtin:mode:scheduler", Scope: "user"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	resp, err := a.handleModesUnloadAll(ctx, domain.AgentModesUnloadAllReq{})
	if err != nil {
		t.Fatalf("handleModesUnloadAll: %v", err)
	}
	if len(resp.Unloaded) != 2 {
		t.Fatalf("expected 2 unloaded modes, got %v", resp.Unloaded)
	}
	for _, ref := range a.cardRefs {
		switch ref.ID {
		case "builtin:mode:goal", "builtin:mode:scheduler":
			t.Fatalf("expected mode %q removed by modes_unload_all", ref.ID)
		}
	}
	if a.RawSession.Goal != nil {
		t.Fatalf("expected Goal to be nil after modes_unload_all, got %+v", a.RawSession.Goal)
	}
	if a.RawSession.ActiveScheduler != nil {
		t.Fatalf("expected ActiveScheduler to be nil after modes_unload_all, got %+v", a.RawSession.ActiveScheduler)
	}
	goalBundle := false
	workflowBundle := false
	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:bundle:goal" {
			goalBundle = true
		}
		if ref.ID == "builtin:bundle:workflow-tools" {
			workflowBundle = true
		}
	}
	if !goalBundle || !workflowBundle {
		t.Fatal("expected builtin-scope bundles to be preserved by modes_unload_all")
	}
}

// TestHandleModesUnloadAll_UserScopeBundlesRemoved verifies that bundles
// mounted as mode dependencies with scope "user" (not "builtin") are removed
// alongside their mode, mirroring deactivateMode's contract.
func TestHandleModesUnloadAll_UserScopeBundlesRemoved(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:goal", Scope: "user"},
			{ID: "builtin:mode:goal", Scope: "user"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	resp, err := a.handleModesUnloadAll(ctx, domain.AgentModesUnloadAllReq{})
	if err != nil {
		t.Fatalf("handleModesUnloadAll: %v", err)
	}
	if len(resp.Unloaded) != 1 || resp.Unloaded[0] != "builtin:mode:goal" {
		t.Fatalf("expected [builtin:mode:goal] unloaded, got %v", resp.Unloaded)
	}
	if len(a.cardRefs) != 0 {
		t.Fatalf("expected all user-scope mounts removed, got %+v", a.cardRefs)
	}
}

// TestHandleModesUnloadAll_NoModes verifies an empty mount set returns an
// empty Unloaded list without error.
func TestHandleModesUnloadAll_NoModes(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:goal", Scope: "builtin"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	resp, err := a.handleModesUnloadAll(ctx, domain.AgentModesUnloadAllReq{})
	if err != nil {
		t.Fatalf("handleModesUnloadAll: %v", err)
	}
	if len(resp.Unloaded) != 0 {
		t.Fatalf("expected no modes unloaded, got %v", resp.Unloaded)
	}
}
