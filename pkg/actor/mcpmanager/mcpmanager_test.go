package mcpmanager

import (
	"encoding/json"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/projection"
	"github.com/qomos-w/gospore/ref"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func validAddReq() domain.McpAddServerReq {
	return domain.McpAddServerReq{
		Config: domain.McpServerConfig{
			Name:      "primary",
			Transport: "stdio",
			Stdio: &domain.McpStdioTransport{
				Command: "node",
				Args:    []string{"server.js"},
				Env:     map[string]string{"API_KEY": "super-secret"},
			},
			Enabled: true,
		},
	}
}

func freshMM(t *testing.T) (*Actor, *testutil.FakeCtx) {
	t.Helper()
	a := &Actor{store: persist.NewFSPersist(t.TempDir())}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return testutil.NewFakeRef(testutil.GenActorID(), nil), nil
	}
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	return a, ctx
}

func freshMMAnon(t *testing.T) (*Actor, *testutil.FakeCtx) {
	t.Helper()
	a := &Actor{store: persist.NewFSPersist(t.TempDir())}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	// The real anonymous web role arrives as an explicit "anonymous" role via
	// the frontgate; the zero identity AnonCtx produces is reserved for
	// internal actor-to-actor calls (e.g. the agent turn engine).
	ctx.Identity_ = id.Identity{Role: id.RoleAnonymous}
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	return a, ctx
}

func TestHandleListServers_Empty(t *testing.T) {
	a, ctx := freshMM(t)
	resp, err := a.handleListServers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 0 {
		t.Errorf("expected empty list, got %d items", len(resp.Items))
	}
}

func TestHandleListServers_RequiresHuman(t *testing.T) {
	a, ctx := freshMMAnon(t)
	if _, err := a.handleListServers(ctx); err == nil {
		t.Error("expected error for anonymous role")
	}
}

// TestHandleListServers_AllowsInternalCaller: internal (zero identity or
// "system" role — the project McpExternalCardProvider path) callers may read
// the safe server list; anonymous is denied.
func TestHandleListServers_AllowsInternalCaller(t *testing.T) {
	for _, role := range []id.Role{"", "system"} {
		a, ctx := freshMM(t)
		ctx.Identity_ = id.Identity{Role: role}
		if _, err := a.handleListServers(ctx); err != nil {
			t.Fatalf("role %q: internal caller should be allowed, got %v", role, err)
		}
	}
}

func TestHandleAddServer_Valid(t *testing.T) {
	a, ctx := freshMM(t)
	resp, err := a.handleAddServer(ctx, validAddReq())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Server.ID != "srv-0" {
		t.Errorf("expected first ID srv-0, got %q", resp.Server.ID)
	}
	if resp.Server.Name != "primary" {
		t.Errorf("expected name primary, got %q", resp.Server.Name)
	}
	if len(a.Servers) != 1 {
		t.Fatalf("expected 1 persisted server, got %d", len(a.Servers))
	}
	// The durable record keeps secrets.
	if a.Servers[0].Stdio.Env["API_KEY"] != "super-secret" {
		t.Errorf("persisted record must keep env secret, got %q", a.Servers[0].Stdio.Env["API_KEY"])
	}
	if a.nextID != 1 {
		t.Errorf("nextID should advance to 1, got %d", a.nextID)
	}
	if len(a.childActorIDs) != 1 {
		t.Errorf("expected 1 spawned child tracked, got %d", len(a.childActorIDs))
	}
}

// TestHandleAddServer_SecretsNeverExposed: the add response (which goes back
// to the AdminOnly caller) must not carry env/header VALUES — key names with
// a Configured flag only. Defense in depth, since the schema already makes
// the view type value-less.
func TestHandleAddServer_SecretsNeverExposed(t *testing.T) {
	a, ctx := freshMM(t)
	resp, err := a.handleAddServer(ctx, validAddReq())
	if err != nil {
		t.Fatal(err)
	}
	stdio := resp.Server.Stdio
	if stdio == nil {
		t.Fatal("expected stdio view in response")
	}
	if len(stdio.Env) != 1 {
		t.Fatalf("expected 1 env entry, got %d", len(stdio.Env))
	}
	if stdio.Env[0].Key != "API_KEY" || !stdio.Env[0].Configured {
		t.Errorf("expected key API_KEY with Configured=true, got %+v", stdio.Env[0])
	}
	// The persisted record must keep the value untouched.
	if a.Servers[0].Stdio.Env["API_KEY"] != "super-secret" {
		t.Errorf("response must not mutate stored secret, got %q", a.Servers[0].Stdio.Env["API_KEY"])
	}
}

func TestHandleAddServer_EmptyName(t *testing.T) {
	a, ctx := freshMM(t)
	req := validAddReq()
	req.Config.Name = ""
	if _, err := a.handleAddServer(ctx, req); err == nil {
		t.Error("expected error for empty name")
	}
	if len(a.Servers) != 0 {
		t.Errorf("invalid create must not persist; got %d servers", len(a.Servers))
	}
}

func TestHandleAddServer_DuplicateName(t *testing.T) {
	a, ctx := freshMM(t)
	if _, err := a.handleAddServer(ctx, validAddReq()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleAddServer(ctx, validAddReq()); err == nil {
		t.Error("expected error for duplicate name")
	}
	if len(a.Servers) != 1 {
		t.Errorf("expected 1 server after duplicate rejection, got %d", len(a.Servers))
	}
}

func TestHandleAddServer_InvalidConfig(t *testing.T) {
	a, ctx := freshMM(t)
	req := validAddReq()
	req.Config.Stdio.Command = ""
	if _, err := a.handleAddServer(ctx, req); err == nil {
		t.Error("expected error for invalid config")
	}
	if len(a.Servers) != 0 {
		t.Errorf("invalid create must not persist; got %d servers", len(a.Servers))
	}
	if a.nextID != 0 {
		t.Errorf("nextID must not advance on validation failure; got %d", a.nextID)
	}
}

func TestHandleAddServer_DeniesAnonymousRole(t *testing.T) {
	a, ctx := freshMMAnon(t)
	if _, err := a.handleAddServer(ctx, validAddReq()); err == nil {
		t.Error("expected error for anonymous role")
	}
}

// TestHandleAddServer_AllowsInternalCaller: agent-initiated registration is a
// supported flow (the bundle-use bundle mounts mcp.add_server; the agent turn
// engine's service-ref call arrives with a zero/system identity). Anonymous
// stays denied.
func TestHandleAddServer_AllowsInternalCaller(t *testing.T) {
	for _, role := range []id.Role{"", "system"} {
		a, ctx := freshMM(t)
		ctx.Identity_ = id.Identity{Role: role}
		resp, err := a.handleAddServer(ctx, validAddReq())
		if err != nil {
			t.Fatalf("role %q: internal caller should be allowed, got %v", role, err)
		}
		if resp.Server.ID != "srv-0" {
			t.Fatalf("role %q: expected srv-0, got %q", role, resp.Server.ID)
		}
	}
}

func TestHandleRemoveServer_Valid(t *testing.T) {
	a, ctx := freshMM(t)
	created, err := a.handleAddServer(ctx, validAddReq())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleRemoveServer(ctx, domain.McpRemoveServerReq{ID: created.Server.ID}); err != nil {
		t.Fatal(err)
	}
	if len(a.Servers) != 0 {
		t.Errorf("expected list empty after remove, got %d items", len(a.Servers))
	}
	if len(a.childActorIDs) != 0 {
		t.Errorf("expected child tracking cleared, got %d", len(a.childActorIDs))
	}
}

func TestHandleRemoveServer_NotFound(t *testing.T) {
	a, ctx := freshMM(t)
	if _, err := a.handleRemoveServer(ctx, domain.McpRemoveServerReq{ID: "ghost"}); err == nil {
		t.Error("expected error for missing id")
	}
}

func TestHandleRemoveServer_RequiresHuman(t *testing.T) {
	a, ctx := freshMMAnon(t)
	if _, err := a.handleRemoveServer(ctx, domain.McpRemoveServerReq{ID: "srv-0"}); err == nil {
		t.Error("expected error for anonymous role")
	}
}

func TestPersistRoundTrip(t *testing.T) {
	a, ctx := freshMM(t)
	if _, err := a.handleAddServer(ctx, validAddReq()); err != nil {
		t.Fatal(err)
	}
	req2 := validAddReq()
	req2.Config.Name = "secondary"
	req2.Config.Stdio.Env = map[string]string{"TOKEN": "another-secret"}
	if _, err := a.handleAddServer(ctx, req2); err != nil {
		t.Fatal(err)
	}
	// Simulate restart: new actor with the same actorID + store path.
	b := &Actor{actorID: a.actorID, store: a.store}
	if err := b.Load(); err != nil {
		t.Fatal(err)
	}
	if len(b.Servers) != 2 {
		t.Fatalf("expected 2 servers restored, got %d", len(b.Servers))
	}
	if b.Servers[0].Stdio.Env["API_KEY"] != "super-secret" {
		t.Errorf("expected env secret restored, got %q", b.Servers[0].Stdio.Env["API_KEY"])
	}
	if b.Servers[0].Name != "primary" || b.Servers[1].Name != "secondary" {
		t.Errorf("expected names restored, got %q, %q", b.Servers[0].Name, b.Servers[1].Name)
	}
	if b.nextID != 2 {
		t.Errorf("expected nextID restored to 2, got %d", b.nextID)
	}
}

// TestLoad_RehydratesNextID: if the persisted counter is behind the actual
// max(id), Load bumps it to max+1 so new creates don't reuse an ID.
func TestLoad_RehydratesNextID(t *testing.T) {
	a := &Actor{store: persist.NewFSPersist(t.TempDir()), actorID: "actor-1"}
	a.Servers = []domain.McpServerConfig{
		{ID: "srv-0", Name: "a", Transport: "stdio", Stdio: &domain.McpStdioTransport{Command: "node"}},
		{ID: "srv-3", Name: "b", Transport: "stdio", Stdio: &domain.McpStdioTransport{Command: "node"}},
	}
	a.nextID = 0
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	b := &Actor{actorID: "actor-1", store: a.store}
	if err := b.Load(); err != nil {
		t.Fatal(err)
	}
	if b.nextID != 4 {
		t.Errorf("expected nextID rehydrated to 4 (max+1), got %d", b.nextID)
	}
}

func TestHandleUpdateServer_Valid(t *testing.T) {
	a, ctx := freshMM(t)
	created, err := a.handleAddServer(ctx, validAddReq())
	if err != nil {
		t.Fatal(err)
	}
	req := domain.McpUpdateServerReq{
		ID: created.Server.ID,
		Config: domain.McpServerConfig{
			Name:      "renamed",
			Transport: "stdio",
			Stdio: &domain.McpStdioTransport{
				Command: "python",
				Args:    []string{"mcp.py"},
				// API_KEY omitted → must be preserved from the stored record.
				Env: map[string]string{},
			},
			Enabled: true,
		},
	}
	resp, err := a.handleUpdateServer(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Server.ID != created.Server.ID {
		t.Errorf("expected ID preserved, got %q", resp.Server.ID)
	}
	if resp.Server.Name != "renamed" {
		t.Errorf("expected name updated, got %q", resp.Server.Name)
	}
	if a.Servers[0].Name != "renamed" {
		t.Errorf("expected stored name updated, got %q", a.Servers[0].Name)
	}
	if a.Servers[0].Stdio.Command != "python" {
		t.Errorf("expected stdio command updated, got %q", a.Servers[0].Stdio.Command)
	}
	// Secret preservation: the blank env in the request must NOT clear it.
	if a.Servers[0].Stdio.Env["API_KEY"] != "super-secret" {
		t.Errorf("expected env secret preserved on blank, got %q", a.Servers[0].Stdio.Env["API_KEY"])
	}
}

func TestHandleUpdateServer_NotFound(t *testing.T) {
	a, ctx := freshMM(t)
	req := domain.McpUpdateServerReq{
		ID:     "ghost",
		Config: validAddReq().Config,
	}
	if _, err := a.handleUpdateServer(ctx, req); err == nil {
		t.Error("expected error for missing id")
	}
}

func TestHandleUpdateServer_DuplicateName(t *testing.T) {
	a, ctx := freshMM(t)
	if _, err := a.handleAddServer(ctx, validAddReq()); err != nil {
		t.Fatal(err)
	}
	req2 := validAddReq()
	req2.Config.Name = "secondary"
	second, err := a.handleAddServer(ctx, req2)
	if err != nil {
		t.Fatal(err)
	}
	update := domain.McpUpdateServerReq{
		ID:     second.Server.ID,
		Config: validAddReq().Config, // name "primary" is taken
	}
	if _, err := a.handleUpdateServer(ctx, update); err == nil {
		t.Error("expected error for duplicate name")
	}
}

// TestHandleUpdateServer_SameNameAllowed: an update that keeps the existing
// name must not be rejected as a duplicate against itself.
func TestHandleUpdateServer_SameNameAllowed(t *testing.T) {
	a, ctx := freshMM(t)
	created, err := a.handleAddServer(ctx, validAddReq())
	if err != nil {
		t.Fatal(err)
	}
	req := domain.McpUpdateServerReq{
		ID: created.Server.ID,
		Config: domain.McpServerConfig{
			Name:      "primary", // same as create
			Transport: "http",
			Http:      &domain.McpHttpTransport{URL: "https://new.example.com/mcp", Headers: map[string]string{}},
			Enabled:   true,
		},
	}
	if _, err := a.handleUpdateServer(ctx, req); err != nil {
		t.Errorf("update with same name must succeed, got %v", err)
	}
	if a.Servers[0].Transport != "http" {
		t.Errorf("expected transport updated, got %q", a.Servers[0].Transport)
	}
}

func TestHandleUpdateServer_InvalidConfig(t *testing.T) {
	a, ctx := freshMM(t)
	created, err := a.handleAddServer(ctx, validAddReq())
	if err != nil {
		t.Fatal(err)
	}
	req := domain.McpUpdateServerReq{
		ID: created.Server.ID,
		Config: domain.McpServerConfig{
			Name:      "renamed",
			Transport: "sse", // invalid
		},
	}
	if _, err := a.handleUpdateServer(ctx, req); err == nil {
		t.Error("expected error for invalid transport")
	}
	if a.Servers[0].Name != "primary" {
		t.Errorf("invalid update must not mutate stored cfg, got name %q", a.Servers[0].Name)
	}
}

func TestHandleUpdateServer_RequiresHuman(t *testing.T) {
	a, ctx := freshMMAnon(t)
	if _, err := a.handleUpdateServer(ctx, domain.McpUpdateServerReq{
		ID: "srv-0", Config: validAddReq().Config,
	}); err == nil {
		t.Error("expected error for anonymous role")
	}
}

// TestViewOf_SecretsStripped locks the view contract: env/header values never
// appear in the read-safe view.
func TestViewOf_SecretsStripped(t *testing.T) {
	cfg := domain.McpServerConfig{
		ID: "srv-0", Name: "n", Transport: "stdio",
		Stdio: &domain.McpStdioTransport{
			Command: "node", Args: []string{"a.js"},
			Env: map[string]string{"API_KEY": "secret", "EMPTY": ""},
		},
		Http: &domain.McpHttpTransport{URL: "https://x", Headers: map[string]string{"Authorization": "Bearer z"}},
	}
	view := viewOf(cfg)
	if len(view.Stdio.Env) != 2 {
		t.Fatalf("expected 2 env entries, got %d", len(view.Stdio.Env))
	}
	byKey := map[string]bool{}
	for _, e := range view.Stdio.Env {
		byKey[e.Key] = e.Configured
	}
	if !byKey["API_KEY"] {
		t.Error("expected API_KEY entry with Configured=true")
	}
	if !byKey["EMPTY"] {
		t.Error("expected EMPTY entry (present key = configured)")
	}
	if len(view.Http.Headers) != 1 || view.Http.Headers[0].Key != "Authorization" {
		t.Errorf("expected Authorization header key, got %+v", view.Http.Headers)
	}
	if view.Http.URL != "https://x" {
		t.Errorf("expected url 'https://x', got %q", view.Http.URL)
	}
	if view.Http.Proxy != "" {
		t.Errorf("expected empty proxy, got %q", view.Http.Proxy)
	}
}

func TestViewOf_HttpProxyProjected(t *testing.T) {
	cfg := domain.McpServerConfig{
		ID: "srv-0", Name: "n", Transport: "http",
		Http: &domain.McpHttpTransport{
			URL:     "https://mcp.example.com",
			Headers: map[string]string{"Authorization": "Bearer z"},
			Proxy:   "http://proxy.local:3128",
		},
	}
	view := viewOf(cfg)
	if view.Http.URL != "https://mcp.example.com" {
		t.Errorf("expected url, got %q", view.Http.URL)
	}
	if view.Http.Proxy != "http://proxy.local:3128" {
		t.Errorf("expected proxy url, got %q", view.Http.Proxy)
	}
	if len(view.Http.Headers) != 1 || view.Http.Headers[0].Key != "Authorization" {
		t.Errorf("expected Authorization header key, got %+v", view.Http.Headers)
	}
}

func TestMergeKeep_PreservesOmittedSecrets(t *testing.T) {
	old := map[string]string{"A": "v1", "B": "v2"}
	new := map[string]string{"A": "v1-new", "C": "v3"}
	out := mergeKeep(old, new)
	if out["A"] != "v1-new" {
		t.Errorf("expected A updated, got %q", out["A"])
	}
	if out["B"] != "v2" {
		t.Errorf("expected B preserved from old, got %q", out["B"])
	}
	if out["C"] != "v3" {
		t.Errorf("expected C added, got %q", out["C"])
	}
}

// ---------------------------------------------------------------------------
// Child routing (fake child refs)
// ---------------------------------------------------------------------------

// attachFakeChild registers a fake child actor that answers status/connect/
// disconnect/call_tool/tools invokes with canned values.
func attachFakeChild(a *Actor, ctx *testutil.FakeCtx, serverID string, resp domain.McpCallToolResp) {
	childID := testutil.GenActorID()
	a.childActorIDs[serverID] = childID.String()
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid != childID {
			return nil, false
		}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "mcpinstance.status":
				return domain.McpServerStatus{ID: serverID, Connected: true, ToolCount: 3}
			case "mcpinstance.connect":
				return domain.McpServerStatus{ID: serverID, Connected: true, ToolCount: 3}
			case "mcpinstance.disconnect":
				return domain.McpServerStatus{ID: serverID, Connected: false}
			case "mcpinstance.call_tool":
				return resp
			case "mcpinstance.tools":
				return domain.McpServerTools{
					ID: serverID, Name: "primary",
					Tools: []domain.McpToolView{
						{Name: "echo", Description: "echoes text back", InputSchema: `{"type":"object"}`},
					},
				}
			}
			return nil
		}), true
	}
}

func TestHandleConnect_RoutesToChild(t *testing.T) {
	a, ctx := freshMM(t)
	created, err := a.handleAddServer(ctx, validAddReq())
	if err != nil {
		t.Fatal(err)
	}
	attachFakeChild(a, ctx, created.Server.ID, domain.McpCallToolResp{})
	resp, err := a.handleConnect(ctx, domain.McpConnectReq{ID: created.Server.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Status.Connected || resp.Status.ToolCount != 3 {
		t.Errorf("expected connected status from child, got %+v", resp.Status)
	}
}

func TestHandleConnect_NotFound(t *testing.T) {
	a, ctx := freshMM(t)
	if _, err := a.handleConnect(ctx, domain.McpConnectReq{ID: "ghost"}); err == nil {
		t.Error("expected error for missing id")
	}
}

func TestHandleConnect_RequiresHuman(t *testing.T) {
	a, ctx := freshMMAnon(t)
	if _, err := a.handleConnect(ctx, domain.McpConnectReq{ID: "srv-0"}); err == nil {
		t.Error("expected error for anonymous role")
	}
}

func TestHandleDisconnect_RoutesToChild(t *testing.T) {
	a, ctx := freshMM(t)
	created, err := a.handleAddServer(ctx, validAddReq())
	if err != nil {
		t.Fatal(err)
	}
	attachFakeChild(a, ctx, created.Server.ID, domain.McpCallToolResp{})
	resp, err := a.handleDisconnect(ctx, domain.McpDisconnectReq{ID: created.Server.ID})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status.Connected {
		t.Error("expected disconnected status from child")
	}
}

func TestHandleDisconnect_RequiresHuman(t *testing.T) {
	a, ctx := freshMMAnon(t)
	if _, err := a.handleDisconnect(ctx, domain.McpDisconnectReq{ID: "srv-0"}); err == nil {
		t.Error("expected error for anonymous role")
	}
}

func TestHandleCallTool_RoutesToChild(t *testing.T) {
	a, ctx := freshMM(t)
	created, err := a.handleAddServer(ctx, validAddReq())
	if err != nil {
		t.Fatal(err)
	}
	want := domain.McpCallToolResp{
		Content: []domain.McpToolContent{{Type: "text", Text: "ok"}},
	}
	attachFakeChild(a, ctx, created.Server.ID, want)
	resp, err := a.handleCallTool(ctx, domain.McpCallToolReq{
		ID: created.Server.ID, Tool: "echo", Arguments: map[string]any{"text": "hi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "ok" {
		t.Errorf("expected child call_tool response, got %+v", resp.Content)
	}
}

func TestHandleCallTool_NotFound(t *testing.T) {
	a, ctx := freshMM(t)
	if _, err := a.handleCallTool(ctx, domain.McpCallToolReq{ID: "ghost", Tool: "echo"}); err == nil {
		t.Error("expected error for missing id")
	}
}

func TestHandleCallTool_RequiresHuman(t *testing.T) {
	a, ctx := freshMMAnon(t)
	if _, err := a.handleCallTool(ctx, domain.McpCallToolReq{ID: "srv-0", Tool: "echo"}); err == nil {
		t.Error("expected error for anonymous role")
	}
}

// TestHandleListServers_ComposesChildStatus: after a mutation the list
// snapshot carries the child's live status when the child is reachable.
func TestHandleListServers_ComposesChildStatus(t *testing.T) {
	a, ctx := freshMM(t)
	created, err := a.handleAddServer(ctx, validAddReq())
	if err != nil {
		t.Fatal(err)
	}
	attachFakeChild(a, ctx, created.Server.ID, domain.McpCallToolResp{})
	a.refreshListSnapshot(ctx)
	resp, err := a.handleListServers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Items))
	}
	if !resp.Items[0].Status.Connected {
		t.Error("expected list status connected from child")
	}
	if resp.Items[0].Status.ToolCount != 3 {
		t.Errorf("expected toolCount 3, got %d", resp.Items[0].Status.ToolCount)
	}
}

// ---------------------------------------------------------------------------
// Agent-facing callables (mcp.discover_tools / mcp.call_tool gate)
// ---------------------------------------------------------------------------

// TestHandleCallTool_AllowsInternalCaller locks the agent-facing gate: calls
// through the mcpmanager service ref carry either the manager's own "system"
// role (appRef default caller role) or, on a raw internal invoke, zero
// identity — both must pass; only the explicit anonymous web role is denied.
func TestHandleCallTool_AllowsInternalCaller(t *testing.T) {
	for _, role := range []id.Role{"", "system"} {
		a, ctx := freshMM(t)
		created, err := a.handleAddServer(ctx, validAddReq())
		if err != nil {
			t.Fatal(err)
		}
		want := domain.McpCallToolResp{
			Content: []domain.McpToolContent{{Type: "text", Text: "ok"}},
		}
		attachFakeChild(a, ctx, created.Server.ID, want)
		ctx.Identity_ = id.Identity{Role: role}
		resp, err := a.handleCallTool(ctx, domain.McpCallToolReq{
			ID: created.Server.ID, Tool: "echo", Arguments: map[string]any{"text": "hi"},
		})
		if err != nil {
			t.Fatalf("role %q: %v", role, err)
		}
		if len(resp.Content) != 1 || resp.Content[0].Text != "ok" {
			t.Errorf("role %q: expected child call_tool response, got %+v", role, resp.Content)
		}
	}
}

// TestHandleCallTool_DeniesAnonymousRole: the relaxed gate must still deny the
// explicit anonymous web role.
func TestHandleCallTool_DeniesAnonymousRole(t *testing.T) {
	a, ctx := freshMM(t)
	ctx.Identity_ = id.Identity{Role: id.RoleAnonymous}
	if _, err := a.handleCallTool(ctx, domain.McpCallToolReq{ID: "srv-0", Tool: "echo"}); err == nil {
		t.Error("expected error for anonymous role")
	}
}

// TestHandleDiscoverTools_CollectsChildTools verifies the manager aggregates
// each reachable child's cached tool list for agent tool injection.
func TestHandleDiscoverTools_CollectsChildTools(t *testing.T) {
	a, ctx := freshMM(t)
	created, err := a.handleAddServer(ctx, validAddReq())
	if err != nil {
		t.Fatal(err)
	}
	attachFakeChild(a, ctx, created.Server.ID, domain.McpCallToolResp{})
	resp, err := a.handleDiscoverTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Servers) != 1 {
		t.Fatalf("expected 1 server with tools, got %d", len(resp.Servers))
	}
	srv := resp.Servers[0]
	if srv.ID != created.Server.ID || srv.Name != "primary" {
		t.Errorf("expected server %q/%q, got %q/%q", created.Server.ID, "primary", srv.ID, srv.Name)
	}
	if len(srv.Tools) != 1 || srv.Tools[0].Name != "echo" {
		t.Fatalf("expected echo tool, got %+v", srv.Tools)
	}
	if srv.Tools[0].InputSchema != `{"type":"object"}` {
		t.Errorf("inputSchema must pass through verbatim, got %q", srv.Tools[0].InputSchema)
	}
}

// TestHandleDiscoverTools_Empty: no servers → empty catalog, no error.
func TestHandleDiscoverTools_Empty(t *testing.T) {
	a, ctx := freshMM(t)
	resp, err := a.handleDiscoverTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Servers) != 0 {
		t.Errorf("expected empty catalog, got %d servers", len(resp.Servers))
	}
}

// TestHandleDiscoverTools_AllowsInternalCaller: internal (zero identity or
// "system" role — the agent turn engine path) callers may discover tools;
// anonymous is denied.
func TestHandleDiscoverTools_AllowsInternalCaller(t *testing.T) {
	for _, role := range []id.Role{"", "system"} {
		a, ctx := freshMM(t)
		ctx.Identity_ = id.Identity{Role: role}
		if _, err := a.handleDiscoverTools(ctx); err != nil {
			t.Fatalf("role %q: internal caller should be allowed, got %v", role, err)
		}
	}
}

func TestHandleDiscoverTools_DeniesAnonymousRole(t *testing.T) {
	a, ctx := freshMM(t)
	ctx.Identity_ = id.Identity{Role: id.RoleAnonymous}
	if _, err := a.handleDiscoverTools(ctx); err == nil {
		t.Error("expected error for anonymous role")
	}
}

// ---------------------------------------------------------------------------
// No McpServerConfig projection (secret-leak regression)
// ---------------------------------------------------------------------------
//
// McpServerConfig carries stdio env / http header VALUES. It must never be
// reachable through the projection layer (component → projection store →
// manifest → gen-clients), only through the write-side callables. These tests
// lock that contract at three levels: the struct tag, the runtime component
// scanner, and the committed manifest.

// TestServersComponentNotExposed: the durable Servers slice must not carry a
// gospore component tag. A projection of McpServerConfig would bypass the
// safe-view masking of the callable layer and leak secrets to any caller of
// gospore.projection.get/watch.
func TestServersComponentNotExposed(t *testing.T) {
	field, ok := reflect.TypeOf(Actor{}).FieldByName("Servers")
	if !ok {
		t.Fatal("Servers field missing")
	}
	if got := field.Tag.Get("gospore"); got != "" {
		t.Fatalf("Servers gospore tag = %q, want none (McpServerConfig must not be a projection)", got)
	}
}

// TestNoComponentProjectionExposesMcpServerConfig uses the same scanner the
// runtime uses (gospore/projection.ScanComponents): it must find no component
// slot that is or contains McpServerConfig on the manager actor.
func TestNoComponentProjectionExposesMcpServerConfig(t *testing.T) {
	slots, err := projection.ScanComponents(&Actor{})
	if err != nil {
		t.Fatal(err)
	}
	cfgType := reflect.TypeOf(domain.McpServerConfig{})
	cfgSliceType := reflect.TypeOf([]domain.McpServerConfig(nil))
	for _, s := range slots {
		if s.Name == "Servers" || s.Type == cfgType || s.Type == cfgSliceType {
			t.Fatalf("component %q exposes McpServerConfig (env/header values would leak)", s.Name)
		}
	}
}

// TestGeneratedManifestHasNoMcpManagerProjection guards the committed
// codegen artifact: gen/gmanifest.json (regenerated by `make gen-ts`) must
// carry no mcpmanager projection entry. Re-adding a component tag on any
// McpServerConfig-carrying field would surface here before it reaches the TS
// projection client.
func TestGeneratedManifestHasNoMcpManagerProjection(t *testing.T) {
	data, err := os.ReadFile("../../../gen/gmanifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Projections []struct {
			Namespace string `json:"namespace"`
			ActorPath string `json:"actorPath"`
			Component string `json:"component"`
		} `json:"projections"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, p := range manifest.Projections {
		if p.Namespace == "mcpmanager" || p.ActorPath == "mcpmanager" {
			t.Fatalf("manifest exports mcpmanager projection %q; McpServerConfig must never be a projection", p.Component)
		}
	}
}

// ---------------------------------------------------------------------------
// Status change → agent tools_refresh_notify fan-out
// ---------------------------------------------------------------------------

// TestHandleServerStatusChanged_RefreshesListSnapshot verifies the handler
// refreshes the cached mcp.list_servers snapshot synchronously even when no
// workspace service is reachable (the agent fan-out is skipped, not fatal).
func TestHandleServerStatusChanged_RefreshesListSnapshot(t *testing.T) {
	a, ctx := freshMM(t)
	// No LookupServiceFn: workspace lookup fails; the handler must be a no-op
	// for the agent fan-out but still refresh the snapshot.
	a.handleServerStatusChanged(ctx, domain.McpServerStatusEvent{
		Status: domain.McpServerStatus{ID: "srv-0", Connected: true, ToolCount: 3},
	})
	if s := a.listSnapshot.Load(); s == nil {
		t.Fatal("expected list snapshot to be refreshed")
	}
}

// TestHandleServerStatusChanged_NotifiesMountedAgents verifies the full
// child→parent→agent chain: a server status change pushes tools_refresh_notify
// to every LOADED agent that mounts mcp:<server-id>, while unloaded agents and
// agents without the MCP mount are skipped.
func TestHandleServerStatusChanged_NotifiesMountedAgents(t *testing.T) {
	a, ctx := freshMM(t)

	// Distinct actor IDs: GenActorID is deterministic (same value every
	// call), so build small sequence generators sharing one counter.
	ts := 0
	gen := func() id.ActorID {
		g := id.NewCanonical(99, 0, func() uint64 { ts++; return uint64(ts) })
		return g.Next()
	}

	notified := make(chan string, 4)
	mounted := testutil.NewFakeRef(gen(), func(callID string, _ any) any {
		switch callID {
		case "component_list":
			return domain.AgentComponentListResp{Items: []domain.AgentComponentMount{
				{CardID: "mcp:srv-0", Enabled: true, Scope: "builtin"},
				{CardID: "builtin:bundle:file-tools", Enabled: true, Scope: "builtin"},
			}}
		case "tools_refresh_notify":
			notified <- "mounted"
			return struct{}{}
		}
		t.Fatalf("unexpected callable %q on mounted agent", callID)
		return nil
	})
	unmounted := testutil.NewFakeRef(gen(), func(callID string, _ any) any {
		if callID == "tools_refresh_notify" {
			t.Error("agent without mcp:srv-0 mount must not be notified")
		}
		switch callID {
		case "component_list":
			return domain.AgentComponentListResp{Items: []domain.AgentComponentMount{
				{CardID: "builtin:bundle:file-tools", Enabled: true, Scope: "builtin"},
			}}
		default:
			return nil
		}
	})
	unloaded := testutil.NewFakeRef(gen(), func(callID string, _ any) any {
		t.Errorf("unloaded agent must be skipped entirely, got callable %q", callID)
		return nil
	})

	wsRef := testutil.NewFakeRef(gen(), func(callID string, _ any) any {
		if callID != "workspace.list_agents" {
			t.Fatalf("unexpected workspace callable %q", callID)
		}
		return domain.AgentRefListResp{Items: []domain.AgentRef{
			{ActorID: mounted.ID().String(), LoadState: "loaded"},
			{ActorID: unmounted.ID().String(), LoadState: "loaded"},
			{ActorID: unloaded.ID().String(), LoadState: "unloaded"},
		}}
	})
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	byID := map[id.ActorID]ref.Ref{
		mounted.ID():   mounted,
		unmounted.ID(): unmounted,
		unloaded.ID():  unloaded,
	}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		r, ok := byID[aid]
		return r, ok
	}

	a.handleServerStatusChanged(ctx, domain.McpServerStatusEvent{
		Status: domain.McpServerStatus{ID: "srv-0", Connected: false},
	})

	select {
	case <-notified:
	case <-time.After(2 * time.Second):
		t.Fatal("mounted agent was not notified of the status change")
	}
	select {
	case got := <-notified:
		t.Fatalf("unexpected extra notification %q: only the mounted agent may be refreshed", got)
	case <-time.After(150 * time.Millisecond):
	}
}

// TestAgentMountsMCPServer verifies the enabled mcp:<server-id> mount check
// used to gate the agent fan-out: disabled MCP mounts and other cards never
// match.
func TestAgentMountsMCPServer(t *testing.T) {
	a := &Actor{}
	agentRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
		if callID != "component_list" {
			t.Fatalf("unexpected callable %q", callID)
		}
		return domain.AgentComponentListResp{Items: []domain.AgentComponentMount{
			{CardID: "mcp:srv-1", Enabled: true, Scope: "builtin"},
			{CardID: "mcp:srv-2", Enabled: false, Scope: "user"}, // disabled → not served
			{CardID: "builtin:bundle:file-tools", Enabled: true, Scope: "builtin"},
		}}
	})
	lifecycle := testutil.AdminCtx(testutil.GenActorID()).Lifecycle()
	if !a.agentMountsMCPServer(lifecycle, agentRef, "srv-1") {
		t.Error("enabled mcp:srv-1 mount must match")
	}
	if a.agentMountsMCPServer(lifecycle, agentRef, "srv-2") {
		t.Error("disabled mcp:srv-2 mount must not match")
	}
	if a.agentMountsMCPServer(lifecycle, agentRef, "srv-3") {
		t.Error("unmounted server must not match")
	}
}

// ---------------------------------------------------------------------------
// Owner-lane de-blocking: stateless routing + parallel discover_tools
// ---------------------------------------------------------------------------
//
// mcp.connect/disconnect/call_tool/discover_tools wait on child invokes whose
// budgets are 60s / 60s / 2min / 15s. They must run as stateless (PureContext)
// handlers so the owner queue is never parked behind one slow child. The
// CRUD/lifecycle mutation handlers (add/update/remove) stay stateful on the
// owner loop. TestRegistrationRoutingLanes locks that contract at
// registration time.

// firstParamIsPure reports whether a registered Go handler's first parameter
// is actor.PureContext (stateless), using the same signal the runtime uses to
// infer handler mode.
func firstParamIsPure(fn any) bool {
	if fn == nil {
		return false
	}
	t := reflect.TypeOf(fn)
	if t == nil || t.Kind() != reflect.Func || t.NumIn() < 1 {
		return false
	}
	return t.In(0) == reflect.TypeOf((*actor.PureContext)(nil)).Elem()
}

func TestRegistrationRoutingLanes(t *testing.T) {
	_, ctx := freshMM(t)
	if ctx.Regs == nil {
		t.Fatal("expected registration table populated")
	}
	stateless := map[string]string{
		"mcp.discover_tools":                 "must run stateless (PureContext)",
		"mcp.call_tool":                      "must run stateless (PureContext)",
		"mcp.connect":                        "must run stateless (PureContext)",
		"mcp.disconnect":                     "must run stateless (PureContext)",
		"mcp.internal_server_status_changed": "must run stateless (PureContext; atomic cache store only)",
	}
	for callID, why := range stateless {
		if fn, ok := ctx.Regs[callID]; !ok {
			t.Errorf("%s: not registered", callID)
		} else if !firstParamIsPure(fn) {
			t.Errorf("%s %s", callID, why)
		}
	}
	stateful := map[string]string{
		"mcp.add_server":    "stateful mutation must stay on the owner loop",
		"mcp.update_server": "stateful mutation must stay on the owner loop",
		"mcp.remove_server": "stateful mutation must stay on the owner loop",
	}
	for callID, why := range stateful {
		if fn, ok := ctx.Regs[callID]; !ok {
			t.Errorf("%s: not registered", callID)
		} else if firstParamIsPure(fn) {
			t.Errorf("%s %s", callID, why)
		}
	}
}

// fakeChildBehavior drives one fake mcpinstance child ref. toolsDelay models
// a slow server whose mcpinstance.tools invoke is slow; toolsDone records the
// completion timestamps of each tools invoke so a test can prove the fan-out
// ran concurrently (fast children finished before the slow one's delay).
type fakeChildBehavior struct {
	toolsDelay time.Duration
	tools      domain.McpServerTools
	callDelay  time.Duration
	callResp   domain.McpCallToolResp

	mu        sync.Mutex
	toolsDone []time.Time
}

func (b *fakeChildBehavior) recordToolsDone() {
	b.mu.Lock()
	b.toolsDone = append(b.toolsDone, time.Now())
	b.mu.Unlock()
}

// toolsDoneAt returns the i-th completed tools invoke timestamp (by arrival).
func (b *fakeChildBehavior) toolsDoneAt(i int) (time.Time, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if i < 0 || i >= len(b.toolsDone) {
		return time.Time{}, false
	}
	return b.toolsDone[i], true
}

// attachFakeChildren installs one fake child per serverID with distinct actor
// IDs (shared monotonic generator) and per-child behavior.
func attachFakeChildren(a *Actor, ctx *testutil.FakeCtx, plans map[string]*fakeChildBehavior) {
	var ts int64
	g := id.NewCanonical(200, 0, func() uint64 { ts++; return uint64(ts) })
	byID := make(map[id.ActorID]ref.Ref)
	for serverID, plan := range plans {
		childID := g.Next()
		a.childActorIDs[serverID] = childID.String()
		plan := plan
		byID[childID] = testutil.NewFakeRef(childID, func(callID string, _ any) any {
			switch callID {
			case "mcpinstance.status":
				return domain.McpServerStatus{ID: serverID, Connected: true, ToolCount: int32(len(plan.tools.Tools))}
			case "mcpinstance.tools":
				if plan.toolsDelay > 0 {
					time.Sleep(plan.toolsDelay)
				}
				plan.recordToolsDone()
				return plan.tools
			case "mcpinstance.call_tool":
				if plan.callDelay > 0 {
					time.Sleep(plan.callDelay)
				}
				return plan.callResp
			}
			return nil
		})
	}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		r, ok := byID[aid]
		return r, ok
	}
}

func toolsFor(serverID, name, tool string) domain.McpServerTools {
	return domain.McpServerTools{
		ID:   serverID,
		Name: name,
		Tools: []domain.McpToolView{
			{Name: tool, Description: "echoes text back", InputSchema: `{"type":"object"}`},
		},
	}
}

// TestHandleDiscoverTools_SlowServerDoesNotDragOthers is the discover_tools
// acceptance: one server with a slow tools invoke (300ms) must not delay the
// other servers' tool collection. Parallel fan-out means the fast children
// complete their tools invokes almost immediately while the slow one still
// runs; the response still aggregates all of them.
func TestHandleDiscoverTools_SlowServerDoesNotDragOthers(t *testing.T) {
	a, ctx := freshMM(t)

	slow := &fakeChildBehavior{toolsDelay: 300 * time.Millisecond, tools: toolsFor("srv-0", "primary", "slow_tool")}
	fast1 := &fakeChildBehavior{tools: toolsFor("srv-1", "secondary", "fast_tool_1")}
	fast2 := &fakeChildBehavior{tools: toolsFor("srv-2", "tertiary", "fast_tool_2")}

	// Persist three servers (ids srv-0/1/2) via the CRUD path, then attach a
	// fake child to each so discover_tools resolves them.
	for _, name := range []string{"primary", "secondary", "tertiary"} {
		req := domain.McpAddServerReq{Config: domain.McpServerConfig{
			Name: name, Transport: "stdio",
			Stdio:   &domain.McpStdioTransport{Command: "node", Args: []string{"server.js"}, Env: map[string]string{"K": "v"}},
			Enabled: true,
		}}
		if _, err := a.handleAddServer(ctx, req); err != nil {
			t.Fatalf("add %q: %v", name, err)
		}
	}
	attachFakeChildren(a, ctx, map[string]*fakeChildBehavior{
		"srv-0": slow,
		"srv-1": fast1,
		"srv-2": fast2,
	})

	start := time.Now()
	resp, err := a.handleDiscoverTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)

	if len(resp.Servers) != 3 {
		t.Fatalf("expected all 3 servers' tools, got %d: %+v", len(resp.Servers), resp.Servers)
	}

	// Fast children must have completed their tools invokes well before the
	// slow server's delay elapsed — i.e. they were not serialized behind it.
	for id, b := range map[string]*fakeChildBehavior{"srv-1": fast1, "srv-2": fast2} {
		doneAt, ok := b.toolsDoneAt(0)
		if !ok {
			t.Fatalf("%s: fast child never completed its tools invoke", id)
		}
		latency := doneAt.Sub(start)
		if latency >= slow.toolsDelay/2 {
			t.Errorf("%s tools invoke finished after %v — looks serialized behind the slow server", id, latency)
		}
	}
	if elapsed < slow.toolsDelay {
		t.Errorf("expected discover_tools to wait for the slow server (>= %v), got %v", slow.toolsDelay, elapsed)
	}

	// Every server's tools are present in the response, in config order.
	wantOrder := []string{"srv-0", "srv-1", "srv-2"}
	for i, want := range wantOrder {
		if i >= len(resp.Servers) {
			t.Fatalf("response missing server %q", want)
		}
		if resp.Servers[i].ID != want {
			t.Errorf("response[%d].ID = %q, want %q", i, resp.Servers[i].ID, want)
		}
		if len(resp.Servers[i].Tools) != 1 {
			t.Errorf("server %q: expected 1 tool, got %d", want, len(resp.Servers[i].Tools))
		}
	}
}

// TestHandleDiscoverTools_DoesNotBlockManagerCallables is the "manager 其他
// callable 不排队" acceptance: while discover_tools waits on a slow child, a
// stateful CRUD callable and the list view both complete immediately. (The
// stateless registration is locked separately by TestRegistrationRoutingLanes;
// this test proves the fan-out goroutines hold no manager-wide lock across
// the child invokes.)
func TestHandleDiscoverTools_DoesNotBlockManagerCallables(t *testing.T) {
	a, ctx := freshMM(t)

	slow := &fakeChildBehavior{toolsDelay: 250 * time.Millisecond, tools: toolsFor("srv-0", "primary", "slow_tool")}
	if _, err := a.handleAddServer(ctx, validAddReq()); err != nil {
		t.Fatal(err)
	}
	attachFakeChildren(a, ctx, map[string]*fakeChildBehavior{"srv-0": slow})

	discoverDone := make(chan struct{})
	go func() {
		defer close(discoverDone)
		if _, err := a.handleDiscoverTools(ctx); err != nil {
			t.Errorf("handleDiscoverTools: %v", err)
		}
	}()

	time.Sleep(30 * time.Millisecond) // let discover_tools enter the slow invoke

	// The PureContext list view must answer instantly from the cached snapshot.
	// Shared CI runners add enough scheduler/GC noise to a wall-clock assertion
	// that the "not queued" guarantee needs a wider bound there.
	latencyBound := 100 * time.Millisecond
	if os.Getenv("CI") != "" {
		latencyBound = 750 * time.Millisecond
	}
	listStart := time.Now()
	if _, err := a.handleListServers(ctx); err != nil {
		t.Fatalf("handleListServers: %v", err)
	}
	if d := time.Since(listStart); d > latencyBound {
		t.Errorf("handleListServers took %v while discover_tools was in flight", d)
	}

	// A stateful CRUD mutation must not queue behind discover_tools.
	addStart := time.Now()
	if _, err := a.handleAddServer(ctx, domain.McpAddServerReq{Config: domain.McpServerConfig{
		Name: "second", Transport: "stdio",
		Stdio:   &domain.McpStdioTransport{Command: "node", Args: []string{"s.js"}, Env: map[string]string{}},
		Enabled: true,
	}}); err != nil {
		t.Fatalf("handleAddServer while discover in flight: %v", err)
	}
	if d := time.Since(addStart); d > latencyBound {
		t.Errorf("handleAddServer took %v while discover_tools was in flight", d)
	}

	select {
	case <-discoverDone:
	case <-time.After(2 * time.Second):
		t.Fatal("discover_tools did not complete")
	}
}

// TestHandleCallTool_LongCallDoesNotBlockList is the call_tool acceptance on
// the manager side: a 2min-budget child call_tool must not make mcp.list_servers
// (or a CRUD mutation) queue behind it.
func TestHandleCallTool_LongCallDoesNotBlockList(t *testing.T) {
	a, ctx := freshMM(t)

	plan := &fakeChildBehavior{
		callDelay: 250 * time.Millisecond,
		callResp:  domain.McpCallToolResp{Content: []domain.McpToolContent{{Type: "text", Text: "ok"}}},
		tools:     toolsFor("srv-0", "primary", "echo"),
	}
	if _, err := a.handleAddServer(ctx, validAddReq()); err != nil {
		t.Fatal(err)
	}
	attachFakeChildren(a, ctx, map[string]*fakeChildBehavior{"srv-0": plan})

	callDone := make(chan error, 1)
	go func() {
		_, err := a.handleCallTool(ctx, domain.McpCallToolReq{ID: "srv-0", Tool: "echo", Arguments: map[string]any{"text": "hi"}})
		callDone <- err
	}()

	time.Sleep(30 * time.Millisecond) // let the call enter the slow child

	listStart := time.Now()
	if _, err := a.handleListServers(ctx); err != nil {
		t.Fatalf("handleListServers: %v", err)
	}
	if d := time.Since(listStart); d > 100*time.Millisecond {
		t.Errorf("handleListServers took %v while call_tool was in flight", d)
	}

	select {
	case err := <-callDone:
		if err != nil {
			t.Fatalf("handleCallTool: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("call_tool did not complete")
	}
}
