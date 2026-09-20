package appmanager

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestWorkspaceActionCallMapping(t *testing.T) {
	tests := []struct {
		action  string
		want    string
		wantErr bool
	}{
		{"create", "workspace.create_agent", false},
		{"switch", "workspace.load_agent", false},
		{"message", "workspace.agent_send_message", false},
		{"unknown", "", true},
	}
	for _, tt := range tests {
		got, err := workspaceActionCall(tt.action)
		if (err != nil) != tt.wantErr {
			t.Fatalf("workspaceActionCall(%q) error = %v, wantErr %v", tt.action, err, tt.wantErr)
		}
		if got != tt.want {
			t.Fatalf("workspaceActionCall(%q) = %q, want %q", tt.action, got, tt.want)
		}
	}
}

func TestAgentActionDeniedWithoutFreeAgentBinding(t *testing.T) {
	manifest := gen.AppManifest{
		ID:      "noaction.app",
		Name:    "NoAction",
		Version: "1.0.0",
		Runtime: "spore",
		AgentBinding: &gen.AppAgentBinding{
			Surface: &gen.AgentSurfaceBinding{AgentID: "agent-1", Entrypoint: "home"},
		},
	}
	a := &Actor{
		Apps:              map[string]gen.AppManifest{"noaction.app": manifest},
		bindings:          appbinding.NewRegistry(),
		FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
	}
	if err := a.bindFreeAgentPolicy(manifest); err != nil {
		t.Fatalf("bind: %v", err)
	}

	_, err := a.handleAgentAction(nil, gen.AppManagerAgentActionReq{
		ID:        "noaction.app",
		Action:    "create",
		Kind:      "coder",
		AgentID:   "agent-1",
		Role:      "coder",
		ProjectID: "proj-1",
	})
	if err == nil {
		t.Fatal("expected free-agent denial")
	}
	if code := appbinding.DenialCode(err); code != appbinding.CodeBindingMissing {
		t.Fatalf("expected binding-missing code, got %q (%v)", code, err)
	}
}

func TestAgentActionDeniedByFreeAgentPolicy(t *testing.T) {
	manifest := gen.AppManifest{
		ID:      "policy.app",
		Name:    "Policy",
		Version: "1.0.0",
		Runtime: "spore",
		AgentBinding: &gen.AppAgentBinding{
			Surface: &gen.AgentSurfaceBinding{AgentID: "agent-1", Entrypoint: "home"},
			FreeAgent: &gen.FreeAgentBinding{
				AllowCreate:  false,
				AllowSwitch:  true,
				AllowMessage: true,
				AgentKinds:   []string{"reviewer"},
			},
		},
	}
	a := &Actor{
		Apps:              map[string]gen.AppManifest{"policy.app": manifest},
		bindings:          appbinding.NewRegistry(),
		FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
	}
	if err := a.bindFreeAgentPolicy(manifest); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// create is denied by policy
	_, err := a.handleAgentAction(nil, gen.AppManagerAgentActionReq{
		ID:        "policy.app",
		Action:    "create",
		Kind:      "reviewer",
		AgentID:   "agent-1",
		Role:      "coder",
		ProjectID: "proj-1",
	})
	if err == nil {
		t.Fatal("expected create to be denied")
	}
	if code := appbinding.DenialCode(err); code != appbinding.CodeFreeAgentDenied {
		t.Fatalf("expected free-agent denied, got %q (%v)", code, err)
	}

	// create with wrong kind is also denied
	_, err = a.handleAgentAction(nil, gen.AppManagerAgentActionReq{
		ID:        "policy.app",
		Action:    "create",
		Kind:      "coder",
		AgentID:   "agent-1",
		Role:      "coder",
		ProjectID: "proj-1",
	})
	if err == nil {
		t.Fatal("expected create with wrong kind to be denied")
	}
}

func TestAgentActionRequiresAppToExist(t *testing.T) {
	a := &Actor{Apps: map[string]gen.AppManifest{}, bindings: appbinding.NewRegistry()}
	_, err := a.handleAgentAction(nil, gen.AppManagerAgentActionReq{
		ID:        "missing.app",
		Action:    "create",
		Kind:      "coder",
		AgentID:   "agent-1",
		Role:      "coder",
		ProjectID: "proj-1",
	})
	if err == nil {
		t.Fatal("expected app-not-found error")
	}
}

func TestFillDefaultPluginAgentTarget(t *testing.T) {
	manifest := gen.AppManifest{
		ID: "chat.app", Name: "Chat", Version: "1.0.0", Runtime: "native",
		AgentBinding: &gen.AppAgentBinding{
			PluginAgents: []gen.PluginAgentBinding{
				{Name: "assistant", DisplayName: "Chat Assistant"},
				{Name: "reviewer", DisplayName: "Chat Reviewer"},
			},
		},
	}
	a := &Actor{
		pluginAgentSurfaceIDs: map[string]string{
			pluginAgentSurfaceKey("chat.app", "assistant"): "chat-assistant-actor-1",
			pluginAgentSurfaceKey("chat.app", "reviewer"):  "chat-reviewer-actor-1",
		},
	}

	// message without a target fills all workspace conventions
	payload := map[string]any{"Text": "hi"}
	if !a.fillDefaultPluginAgentTarget("chat.app", manifest, "message", payload) {
		t.Fatal("message default target not filled")
	}
	if payload["ToAgentId"] != "chat-assistant-actor-1" || payload["AgentId"] != "chat-assistant-actor-1" || payload["ID"] != "chat-assistant-actor-1" {
		t.Fatalf("message payload = %+v", payload)
	}
	if payload["CallerAgentId"] != "chat-assistant-actor-1" {
		t.Fatalf("message payload must carry CallerAgentId for requireConversableGrant self-allowance: %+v", payload)
	}

	// switch fills AgentId/ID but not ToAgentId
	payload = map[string]any{}
	if !a.fillDefaultPluginAgentTarget("chat.app", manifest, "switch", payload) {
		t.Fatal("switch default target not filled")
	}
	if payload["AgentId"] != "chat-assistant-actor-1" || payload["ID"] != "chat-assistant-actor-1" {
		t.Fatalf("switch payload = %+v", payload)
	}
	if _, ok := payload["ToAgentId"]; ok {
		t.Fatalf("switch payload must not carry ToAgentId: %+v", payload)
	}
	if _, ok := payload["CallerAgentId"]; ok {
		t.Fatalf("switch payload must not carry CallerAgentId (load_agent needs none): %+v", payload)
	}

	// Only the first declared slot is used.
	if payload["AgentId"] != "chat-assistant-actor-1" {
		t.Fatalf("expected first slot resolution, got %q", payload["AgentId"])
	}
}

func TestFillDefaultPluginAgentTargetSkips(t *testing.T) {
	manifest := gen.AppManifest{
		ID: "chat.app", Name: "Chat", Version: "1.0.0", Runtime: "native",
		AgentBinding: &gen.AppAgentBinding{
			PluginAgents: []gen.PluginAgentBinding{{Name: "assistant"}},
		},
	}
	a := &Actor{
		pluginAgentSurfaceIDs: map[string]string{
			pluginAgentSurfaceKey("chat.app", "assistant"): "chat-assistant-actor-1",
		},
	}

	// explicit target in the payload wins over the default
	payload := map[string]any{"ToAgentId": "other-agent"}
	if a.fillDefaultPluginAgentTarget("chat.app", manifest, "message", payload) {
		t.Fatal("explicit payload target must suppress the default")
	}
	if payload["ToAgentId"] != "other-agent" {
		t.Fatalf("explicit target overwritten: %+v", payload)
	}

	// create carries no implicit target
	payload = map[string]any{}
	if a.fillDefaultPluginAgentTarget("chat.app", manifest, "create", payload) {
		t.Fatal("create must not default a target")
	}
	if len(payload) != 0 {
		t.Fatalf("create payload mutated: %+v", payload)
	}

	// app without a plugin_agent binding has nothing to resolve to
	noAgent := gen.AppManifest{ID: "chat.app", AgentBinding: &gen.AppAgentBinding{}}
	if a.fillDefaultPluginAgentTarget("chat.app", noAgent, "message", map[string]any{}) {
		t.Fatal("app without plugin_agent must not resolve a default target")
	}

	// reconcile not yet surfaced → no tracked actor id
	noSurface := gen.AppManifest{
		ID: "fresh.app", AgentBinding: &gen.AppAgentBinding{
			PluginAgents: []gen.PluginAgentBinding{{Name: "assistant"}},
		},
	}
	if a.fillDefaultPluginAgentTarget("fresh.app", noSurface, "message", map[string]any{}) {
		t.Fatal("unreconciled plugin agent must not resolve")
	}
}

// TestAgentActionForwardsDefaultPluginAgentTarget drives the resolution through
// handleAgentAction and asserts the workspace forward carries the app's own
// plugin agent for a target-less message.
func TestAgentActionForwardsDefaultPluginAgentTarget(t *testing.T) {
	manifest := gen.AppManifest{
		ID: "chat.app", Name: "Chat", Version: "1.0.0", Runtime: "native",
		AgentBinding: &gen.AppAgentBinding{
			FreeAgent: &gen.FreeAgentBinding{AllowMessage: true, AllowSwitch: true},
			PluginAgents: []gen.PluginAgentBinding{
				{Name: "assistant", DisplayName: "Chat Assistant"},
			},
		},
	}
	a := &Actor{
		Apps:                  map[string]gen.AppManifest{"chat.app": manifest},
		Records:               map[string]appRecord{},
		FreeAgentPolicies:     map[string]appbinding.FreeAgentPolicy{},
		pluginAgentSurfaceIDs: map[string]string{pluginAgentSurfaceKey("chat.app", "assistant"): "chat-assistant-actor-1"},
	}
	if err := a.bindFreeAgentPolicy(manifest); err != nil {
		t.Fatalf("bind: %v", err)
	}

	type forwarded struct {
		callID  string
		payload map[string]any
	}
	var calls []forwarded
	ctx := fakeWorkspaceCtx(t, func(callID string, payload any) (any, error) {
		calls = append(calls, forwarded{callID: callID, payload: payload.(map[string]any)})
		return nil, nil
	})

	if _, err := a.handleAgentAction(ctx, gen.AppManagerAgentActionReq{
		ID: "chat.app", Action: "message", AgentID: "agent-1", Role: "coder", ProjectID: "proj-1",
		Payload: map[string]any{"Text": "hi"},
	}); err != nil {
		t.Fatalf("handleAgentAction message: %v", err)
	}
	if len(calls) != 1 || calls[0].callID != "workspace.agent_send_message" {
		t.Fatalf("forwarded calls = %+v, want one agent_send_message", calls)
	}
	p := calls[0].payload
	if p["ToAgentId"] != "chat-assistant-actor-1" || p["AgentId"] != "chat-assistant-actor-1" || p["ID"] != "chat-assistant-actor-1" {
		t.Fatalf("forwarded payload = %+v, want own plugin agent target", p)
	}
	if p["CallerAgentId"] != "chat-assistant-actor-1" {
		t.Fatalf("forwarded payload must carry CallerAgentId or requireConversableGrant rejects the non-developer caller: %+v", p)
	}
	if p["Text"] != "hi" {
		t.Fatalf("app payload not preserved: %+v", p)
	}

	// switch without a target forwards the same implicit agent to load_agent
	calls = nil
	if _, err := a.handleAgentAction(ctx, gen.AppManagerAgentActionReq{
		ID: "chat.app", Action: "switch", AgentID: "agent-1", Role: "coder", ProjectID: "proj-1",
	}); err != nil {
		t.Fatalf("handleAgentAction switch: %v", err)
	}
	if len(calls) != 1 || calls[0].callID != "workspace.load_agent" {
		t.Fatalf("forwarded calls = %+v, want one load_agent", calls)
	}
	if calls[0].payload["AgentId"] != "chat-assistant-actor-1" {
		t.Fatalf("forwarded switch payload = %+v, want own plugin agent target", calls[0].payload)
	}
}

func TestOriginOverrideRules(t *testing.T) {
	records := map[string]appRecord{
		"example.app": {Manifest: gen.AppManifest{ID: "example.app"}, Origin: "builtin"},
	}

	// builtin cannot override builtin
	rec := appRecord{Manifest: gen.AppManifest{ID: "example.app"}, Origin: "builtin"}
	if err := checkOriginOverride(records, rec); err != nil {
		t.Fatalf("builtin -> builtin should be allowed, got %v", err)
	}

	// user can override builtin (user rank 2 > builtin rank 1)
	rec.Origin = "user"
	if err := checkOriginOverride(records, rec); err != nil {
		t.Fatalf("user -> builtin should be allowed, got %v", err)
	}

	// builtin cannot override user
	records["example.app"] = appRecord{Manifest: gen.AppManifest{ID: "example.app"}, Origin: "user"}
	rec.Origin = "builtin"
	if err := checkOriginOverride(records, rec); err == nil {
		t.Fatal("builtin -> user should be denied")
	} else {
		if code := appbinding.DenialCode(err); code != appbinding.CodePermissionDenied {
			t.Fatalf("expected permission denied, got %q (%v)", code, err)
		}
	}

	// project can override user
	rec.Origin = "project"
	if err := checkOriginOverride(records, rec); err != nil {
		t.Fatalf("project -> user should be allowed, got %v", err)
	}
}
