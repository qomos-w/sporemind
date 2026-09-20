package workspace

import (
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestRegistrationSurface_Declarations verifies that callables carry the
// correct WithEffect and WithService values declared at registration.
func TestRegistrationSurface_Declarations(t *testing.T) {
	wantEffect := map[string]string{
		"workspace.list_project":            "none",
		"workspace.list_agents":             "none",
		"workspace.agents":                  "none",
		"workspace.account":                 "none",
		"workspace.session":                 "none",
		"workspace.list_agent_kinds":        "none",
		"workspace.list_agent_kind_configs": "none",
		"workspace.get_agent_kind_config":   "none",
		"workspace.git_status":              "none",
		"workspace.git_log":                 "none",
		"workspace.git_diff":                "none",
		"workspace.git_add":                 "reversible",
		"workspace.git_commit":              "reversible",
		"workspace.git_push":                "irreversible",
		"workspace.git_pull":                "reversible",
		"workspace.git_branch":              "reversible",
		"workspace.git_checkout":            "irreversible",
		"workspace.git_show":                "none",
		"workspace.git_fetch":               "reversible",
		"workspace.git_discard":             "irreversible",
		"workspace.git_amend":               "reversible",
		"workspace.git_tag_list":            "none",
		"workspace.git_tag_create":          "irreversible",
		"workspace.git_tag_delete":          "irreversible",
		"workspace.git_merge":               "irreversible",
		"workspace.agent_pause":             "reversible",
		"workspace.agent_resume":            "reversible",
		"workspace.agent_unload":            "reversible",
		"workspace.agent_spawn_scheduler":   "reversible",
	}

	a := &Actor{}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	for id, wantEff := range wantEffect {
		opts, ok := ctx.RegOpts[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
		if got := actor.ResolveEffect(opts...); got != wantEff {
			t.Errorf("%s EffectKind = %q, want %q", id, got, wantEff)
		}
	}
}

// TestDeletionCallables_NoLoop verifies the loop wiring of the
// cascade-deletion surface after the Wave2-C stateless conversion:
//
//   - finalize, retry and sweep are PureContext handlers and must NOT declare
//     a WithLoop — a named loop would enqueue the stateless handler onto that
//     stateful lane and re-serialize it; without one they default to the
//     pure loop (forked goroutine). Their concurrency safety comes from
//     deletionMu + agentsMu (see workspace_cascade_delete.go), not mailbox
//     ordering. The former "deletion" lane is no longer registered anywhere.
func TestDeletionCallables_NoLoop(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	if got, ok := ctx.Loops["deletion"]; ok {
		t.Errorf("deletion loop still registered (%v); stateless finalize must not pin a lane", got)
	}

	for _, id := range []string{
		"workspace.internal_deletion_finalize",
		"workspace.internal_deletion_retry",
		"workspace.internal_deletion_sweep",
	} {
		opts, ok := ctx.RegOpts[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
		if got := actor.ResolveLoop(opts...); got != "" {
			t.Errorf("%s declares loop %q; stateless handlers must not pin a loop", id, got)
		}
		if got := actor.ResolveLoopOrDefault(actor.ModeStateless, opts...); got != actor.DefaultLoopPure {
			t.Errorf("%s stateless loop = %q, want %q", id, got, actor.DefaultLoopPure)
		}
	}
}

// TestRegistrationSurface_LifecycleCallablesNoLoop pins the Wave2-B agent
// lifecycle surface: mount, create, create_agent, delete_agent, load_agent
// and clone_agent are PureContext handlers and must NOT declare a WithLoop —
// the former "lifecycle" lane assignment would enqueue these stateless
// handlers onto a stateful lane and re-serialize them. agent_review joined
// the stateless surface in Wave2-C
// (asserted in TestRegistrationSurface_WorkflowOrchestrationLoops).
func TestRegistrationSurface_LifecycleCallablesNoLoop(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{
		"workspace.mount",
		"workspace.create",
		"workspace.create_agent",
		"workspace.delete_agent",
		"workspace.load_agent",
		"workspace.clone_agent",
	} {
		opts, ok := ctx.RegOpts[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
		if got := actor.ResolveLoop(opts...); got != "" {
			t.Errorf("%s declares loop %q; stateless handlers must not pin a loop", id, got)
		}
		if got := actor.ResolveLoopOrDefault(actor.ModeStateless, opts...); got != actor.DefaultLoopPure {
			t.Errorf("%s stateless loop = %q, want %q", id, got, actor.DefaultLoopPure)
		}
	}
}
