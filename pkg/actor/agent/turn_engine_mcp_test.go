package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/promise"
	"github.com/qomos-w/gospore/ref"

	"github.com/qomos-w/sporemind/pkg/actor/mcpmanager"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/runtime"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// ---------------------------------------------------------------------------
// Pure helpers: catalog → ToolSpecs
// ---------------------------------------------------------------------------

func TestMCPSpecsFromCatalog(t *testing.T) {
	resp := domain.McpDiscoverToolsResp{
		Servers: []domain.McpServerTools{
			{
				ID: "srv-0", Name: "my server",
				Tools: []domain.McpToolView{
					{Name: "echo", Description: "echoes text back", InputSchema: `{"type":"object","properties":{"text":{"type":"string"}}}`},
					{Name: "no.schema", Description: "no input schema"},
				},
			},
			{
				ID: "srv-1", Name: "my_server", // sanitization collision with srv-0's name
				Tools: []domain.McpToolView{{Name: "echo", Description: "second echo"}},
			},
		},
	}
	specs := mcpToolSpecsFromCatalog(resp)
	if len(specs) != 3 {
		t.Fatalf("expected 3 specs, got %d: %+v", len(specs), specs)
	}

	// Naming: mcp-<server>-<tool> (hyphen-delimited) with the server name sanitized.
	if specs[0].Name != "mcp-my_server-echo" {
		t.Errorf("spec[0].Name = %q, want mcp-my_server-echo", specs[0].Name)
	}
	// inputSchema 透传 verbatim.
	if specs[0].InputSchema != `{"type":"object","properties":{"text":{"type":"string"}}}` {
		t.Errorf("inputSchema must pass through verbatim, got %q", specs[0].InputSchema)
	}
	if specs[0].Description != "echoes text back" {
		t.Errorf("description = %q, want echoed", specs[0].Description)
	}
	// Routing: CallableID encodes the stable server ID, ServiceName = mcp.
	if specs[0].CallableID != "mcp.srv-0.echo" || specs[0].ServiceName != "mcp" {
		t.Errorf("routing fields = %q/%q, want mcp.srv-0.echo/mcp", specs[0].CallableID, specs[0].ServiceName)
	}
	if specs[0].EffectKind != string(domain.EffectNone) {
		t.Errorf("effect kind = %q, want none", specs[0].EffectKind)
	}
	// Tool names with dots survive in the routing key.
	if specs[1].CallableID != "mcp.srv-0.no.schema" {
		t.Errorf("dotted tool routing key = %q, want mcp.srv-0.no.schema", specs[1].CallableID)
	}
	// Sanitization collision falls back to the server ID.
	if specs[2].Name != "mcp-srv-1-echo" {
		t.Errorf("collision fallback name = %q, want mcp-srv-1-echo", specs[2].Name)
	}
}

// ---------------------------------------------------------------------------
// Mount filtering: resolveMCPTools injects only mounted servers' tools
// ---------------------------------------------------------------------------

func TestMountedMCPServerIDs(t *testing.T) {
	cases := []struct {
		name   string
		mounts []domain.AgentComponentMount
		want   map[string]struct{}
	}{
		{name: "no mounts", mounts: nil, want: nil},
		{name: "no MCP mounts", mounts: []domain.AgentComponentMount{{CardID: "builtin:bundle:debug", Enabled: true}}, want: nil},
		{name: "single MCP mount", mounts: []domain.AgentComponentMount{{CardID: "mcp:srv-0", Enabled: true, Scope: "test"}}, want: map[string]struct{}{"srv-0": {}}},
		{
			name: "disabled and non-MCP mounts excluded",
			mounts: []domain.AgentComponentMount{
				{CardID: "mcp:srv-0", Enabled: true},
				{CardID: "mcp:srv-1", Enabled: false},
				{CardID: "builtin:bundle:debug", Enabled: true},
				{CardID: "mcp:", Enabled: true},
			},
			want: map[string]struct{}{"srv-0": {}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mountedMCPServerIDs(tc.mounts)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("mountedMCPServerIDs = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestResolveMCPTools_FilterByMounts verifies that discover_tools is still
// consulted per turn but only tools from mounted mcp:<server-id> cards
// survive the catalog synthesis (3 servers discovered, 1 mounted → 1 tool).
func TestResolveMCPTools_FilterByMounts(t *testing.T) {
	planner := &mcpRoutingPlanner{
		response: domain.McpDiscoverToolsResp{Servers: []domain.McpServerTools{
			{ID: "srv-0", Name: "server0", Tools: []domain.McpToolView{{Name: "tool0", Description: "from server 0"}}},
			{ID: "srv-1", Name: "server1", Tools: []domain.McpToolView{{Name: "tool1", Description: "from server 1"}}},
			{ID: "srv-2", Name: "server2", Tools: []domain.McpToolView{{Name: "tool2", Description: "from server 2"}}},
		}},
		called: make(chan struct{}),
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner { return planner }
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "mcp" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}

	a := &Actor{ComponentMounts: []domain.AgentComponentMount{
		{CardID: "mcp:srv-1", Enabled: true, Scope: "test"},
	}}
	specs := a.resolveMCPTools(ctx)
	if len(specs) != 1 {
		t.Fatalf("expected exactly the mounted server's 1 tool, got %d: %+v", len(specs), specs)
	}
	if specs[0].CallableID != "mcp.srv-1.tool1" {
		t.Errorf("callable = %q, want mcp.srv-1.tool1", specs[0].CallableID)
	}
	if planner.callID != "mcp.discover_tools" {
		t.Errorf("planner call = %q, want mcp.discover_tools", planner.callID)
	}
}

// TestResolveMCPTools_NoMounts verifies the explicit constraint: without any
// mounted mcp:<server-id> card, no MCP tools are injected and the mcpmanager
// is never consulted.
func TestResolveMCPTools_NoMounts(t *testing.T) {
	planner := &mcpRoutingPlanner{called: make(chan struct{})}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner { return planner }
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "mcp" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}

	specs := (&Actor{}).resolveMCPTools(ctx)
	if specs != nil {
		t.Errorf("expected nil with no MCP mounts, got %d specs", len(specs))
	}
	if planner.callID != "" {
		t.Errorf("mcpmanager must not be consulted without MCP mounts, got call %q", planner.callID)
	}
}

// TestResolveMCPTools_AllMounts covers the all-mounted pole: every discovered
// server is mounted, so every discovered tool is injected.
func TestResolveMCPTools_AllMounts(t *testing.T) {
	planner := &mcpRoutingPlanner{
		response: domain.McpDiscoverToolsResp{Servers: []domain.McpServerTools{
			{ID: "srv-0", Name: "server0", Tools: []domain.McpToolView{{Name: "tool0", Description: "from server 0"}}},
			{ID: "srv-1", Name: "server1", Tools: []domain.McpToolView{{Name: "tool1", Description: "from server 1"}}},
		}},
		called: make(chan struct{}),
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner { return planner }
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "mcp" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}

	a := &Actor{ComponentMounts: []domain.AgentComponentMount{
		{CardID: "mcp:srv-0", Enabled: true, Scope: "test"},
		{CardID: "mcp:srv-1", Enabled: true, Scope: "test"},
	}}
	specs := a.resolveMCPTools(ctx)
	if len(specs) != 2 {
		t.Fatalf("expected 2 specs with both servers mounted, got %d: %+v", len(specs), specs)
	}
	got := map[string]bool{}
	for _, s := range specs {
		got[s.CallableID] = true
	}
	if !got["mcp.srv-0.tool0"] || !got["mcp.srv-1.tool1"] {
		t.Errorf("unexpected tool set: %v", got)
	}
}

// TestResolveMCPTools_MountedServerNotDiscovered: a mount whose server is not
// in the discovery catalog (not connected / removed) yields no specs, but
// discover_tools is still consulted per turn.
func TestResolveMCPTools_MountedServerNotDiscovered(t *testing.T) {
	planner := &mcpRoutingPlanner{
		response: domain.McpDiscoverToolsResp{Servers: []domain.McpServerTools{
			{ID: "srv-0", Name: "server0", Tools: []domain.McpToolView{{Name: "tool0", Description: "from server 0"}}},
		}},
		called: make(chan struct{}),
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner { return planner }
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "mcp" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}

	// srv-9 is mounted but the catalog only knows srv-0.
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{
		{CardID: "mcp:srv-9", Enabled: true, Scope: "test"},
	}}
	specs := a.resolveMCPTools(ctx)
	if specs != nil {
		t.Errorf("expected no specs for mounted-but-not-discovered server, got %d", len(specs))
	}
	if planner.callID != "mcp.discover_tools" {
		t.Errorf("discover_tools must still be consulted per turn, got call %q", planner.callID)
	}
}

// TestFilterMCPServers unit-tests the pure mount filter: given the discover
// catalog and the mounted-server set, only mounted servers survive; an empty
// mounted set drops every server.
func TestFilterMCPServers(t *testing.T) {
	discovered := domain.McpDiscoverToolsResp{Servers: []domain.McpServerTools{
		{ID: "srv-0", Name: "a", Tools: []domain.McpToolView{{Name: "t0"}}},
		{ID: "srv-1", Name: "b", Tools: []domain.McpToolView{{Name: "t1"}}},
		{ID: "srv-2", Name: "c", Tools: []domain.McpToolView{{Name: "t2"}}},
	}}
	cases := []struct {
		name   string
		mounts map[string]struct{}
		want   []string // surviving server IDs in catalog order
	}{
		{name: "empty mount set", mounts: nil, want: nil},
		{name: "single mounted", mounts: map[string]struct{}{"srv-1": {}}, want: []string{"srv-1"}},
		{name: "all mounted", mounts: map[string]struct{}{"srv-0": {}, "srv-1": {}, "srv-2": {}}, want: []string{"srv-0", "srv-1", "srv-2"}},
		{name: "no match", mounts: map[string]struct{}{"srv-9": {}}, want: nil},
		{name: "partial with non-matching extras", mounts: map[string]struct{}{"srv-0": {}, "srv-9": {}}, want: []string{"srv-0"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filterMCPServers(discovered, tc.mounts)
			var ids []string
			for _, s := range got.Servers {
				ids = append(ids, s.ID)
			}
			if !reflect.DeepEqual(ids, tc.want) {
				t.Errorf("filterMCPServers server IDs = %v, want %v", ids, tc.want)
			}
		})
	}
}

// TestResolveMCPTools_MountUnmountBehavior verifies the dynamic mount
// lifecycle at the resolveMCPTools level: mounting an mcp:<server-id> card
// makes that server's tools visible, disabling the mount hides them, and
// unmounting the card drops them again. Each stage rebuilds the component
// snapshot from the current ComponentMounts.
func TestResolveMCPTools_MountUnmountBehavior(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "mcp" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
				if callID == "mcp.discover_tools" {
					return domain.McpDiscoverToolsResp{Servers: []domain.McpServerTools{
						{ID: "srv-0", Name: "server0", Tools: []domain.McpToolView{{Name: "tool0", Description: "from server 0"}}},
						{ID: "srv-1", Name: "server1", Tools: []domain.McpToolView{{Name: "tool1", Description: "from server 1"}}},
					}}, nil
				}
				return nil, fmt.Errorf("unexpected call %s", callID)
			},
		}
	}
	a := &Actor{}

	// Nothing mounted: no tools.
	if specs := a.resolveMCPTools(ctx); specs != nil {
		t.Fatalf("expected nil before mounting, got %d specs", len(specs))
	}

	// Mount mcp:srv-0 → its tool appears.
	a.ComponentMounts = []domain.AgentComponentMount{{CardID: "mcp:srv-0", Enabled: true, Scope: "user"}}
	a.componentSnapshot.Store(nil)
	specs := a.resolveMCPTools(ctx)
	if len(specs) != 1 || specs[0].CallableID != "mcp.srv-0.tool0" {
		t.Fatalf("expected only mcp.srv-0.tool0 after mount, got %+v", specs)
	}

	// Disable the mount → tools disappear.
	a.ComponentMounts[0].Enabled = false
	a.componentSnapshot.Store(nil)
	if specs := a.resolveMCPTools(ctx); specs != nil {
		t.Fatalf("expected nil after disabling the mount, got %d specs", len(specs))
	}

	// Re-enable and mount the second server → both visible.
	a.ComponentMounts = []domain.AgentComponentMount{
		{CardID: "mcp:srv-0", Enabled: true, Scope: "user"},
		{CardID: "mcp:srv-1", Enabled: true, Scope: "user"},
	}
	a.componentSnapshot.Store(nil)
	specs = a.resolveMCPTools(ctx)
	if len(specs) != 2 {
		t.Fatalf("expected 2 specs after mounting both servers, got %d: %+v", len(specs), specs)
	}

	// Unmount mcp:srv-0 → only srv-1's tool remains.
	a.ComponentMounts = []domain.AgentComponentMount{{CardID: "mcp:srv-1", Enabled: true, Scope: "user"}}
	a.componentSnapshot.Store(nil)
	specs = a.resolveMCPTools(ctx)
	if len(specs) != 1 || specs[0].CallableID != "mcp.srv-1.tool1" {
		t.Fatalf("expected only mcp.srv-1.tool1 after unmount, got %+v", specs)
	}
}

func TestParseMCPToolCallable(t *testing.T) {
	cases := []struct {
		in       string
		serverID string
		tool     string
		ok       bool
	}{
		{"mcp.srv-0.echo", "srv-0", "echo", true},
		{"mcp.srv-1.no.schema", "srv-1", "no.schema", true},
		{"mcp.call_tool", "", "", false}, // real mcpmanager callable, 2 segments
		{"project.read", "", "", false},
		{"mcp", "", "", false},
		{"mcp.srv-0", "", "", false},
	}
	for _, c := range cases {
		serverID, tool, ok := parseMCPToolCallable(c.in)
		if ok != c.ok || serverID != c.serverID || tool != c.tool {
			t.Errorf("parseMCPToolCallable(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.in, serverID, tool, ok, c.serverID, c.tool, c.ok)
		}
	}
}

// ---------------------------------------------------------------------------
// Turn engine routing (fake planner)
// ---------------------------------------------------------------------------

// mcpRoutingPlanner captures the outgoing call and answers with a canned
// result, stubbing the mcpmanager actor.
type mcpRoutingPlanner struct {
	mu       sync.Mutex
	callID   string
	payload  any
	callRef  ref.Ref
	response any
	err      error
	called   chan struct{}
}

func (p *mcpRoutingPlanner) Plan(ref.Ref, string, any, ...plan.Option) (plan.Node, error) {
	return nil, errors.New("not implemented")
}

func (p *mcpRoutingPlanner) Call(_ context.Context, r ref.Ref, callID string, payload any) *promise.Promise[any] {
	p.mu.Lock()
	p.callID = callID
	p.payload = payload
	p.callRef = r
	p.mu.Unlock()
	close(p.called)
	if p.err != nil {
		return promise.Reject[any](p.err)
	}
	return promise.Async(func(resolve func(any), reject func(any)) {
		resolve(p.response)
	})
}

func (p *mcpRoutingPlanner) Stream(context.Context, ref.Ref, string, any, func(any) error) *promise.Promise[any] {
	return promise.Reject[any](errors.New("not implemented"))
}

func newMCPRoutingEngine(t *testing.T, planner actor.Planner) *turnEngine {
	t.Helper()
	return &turnEngine{
		done:   make(chan struct{}),
		logger: newNopActorLogger(),
		allTools: []domain.ToolSpec{{
			Name:        "mcp.primary.echo",
			Description: "echoes text back",
			InputSchema: `{"type":"object","properties":{"text":{"type":"string"}}}`,
			EffectKind:  string(domain.EffectNone),
			CallableID:  "mcp.srv-1.echo",
			ServiceName: "mcp",
		}},
	}
}

// TestRunOneCall_RoutesMCPTool verifies the turn engine transforms the LLM's
// MCP tool call into an mcpmanager.call_tool request and backfills the result
// text into the tool frame (toolExecutionResult.out is the frame content).
func TestRunOneCall_RoutesMCPTool(t *testing.T) {
	planner := &mcpRoutingPlanner{
		response: []byte(`{"Content":[{"Type":"text","Text":"hello back"}],"IsError":false}`),
		called:   make(chan struct{}),
	}
	e := newMCPRoutingEngine(t, planner)
	mcpRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner { return planner }

	call := pendingToolCall{
		ID: "tool-1", LLMName: "mcp.primary.echo", CallableID: "mcp.srv-1.echo",
		ServiceName: "mcp", Input: `{"text":"hello"}`,
	}
	result := e.runOneCall(ctx, planner, call, map[string]ref.Ref{"mcp": mcpRef}, "step-1")

	if result.isErr {
		t.Fatalf("expected success, got error result: %q", result.out)
	}
	if result.out != "hello back" {
		t.Errorf("tool frame text = %q, want %q", result.out, "hello back")
	}
	<-planner.called
	if planner.callID != "mcp.call_tool" {
		t.Errorf("routed callable = %q, want mcp.call_tool", planner.callID)
	}
	if planner.callRef == nil || planner.callRef.ID().String() != mcpRef.ID().String() {
		t.Errorf("routed ref = %v, want the mcp manager ref", planner.callRef)
	}
	raw, ok := planner.payload.([]byte)
	if !ok {
		t.Fatalf("payload type = %T, want []byte", planner.payload)
	}
	var req domain.McpCallToolReq
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("decode request: %v (raw=%s)", err, raw)
	}
	if req.ID != "srv-1" || req.Tool != "echo" {
		t.Errorf("request = %+v, want server srv-1 tool echo", req)
	}
	if req.Arguments["text"] != "hello" {
		t.Errorf("arguments = %+v, want text=hello", req.Arguments)
	}
}

// TestRunOneCall_RoutesMCPTool_ErrorResult verifies an MCP tool error
// (IsError=true) is propagated as an error tool frame.
func TestRunOneCall_RoutesMCPTool_ErrorResult(t *testing.T) {
	planner := &mcpRoutingPlanner{
		response: []byte(`{"Content":[{"Type":"text","Text":"boom"}],"IsError":true,"Error":"boom"}`),
		called:   make(chan struct{}),
	}
	e := newMCPRoutingEngine(t, planner)
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner { return planner }

	call := pendingToolCall{
		ID: "tool-1", LLMName: "mcp.primary.echo", CallableID: "mcp.srv-1.echo",
		ServiceName: "mcp", Input: `{"text":"hello"}`,
	}
	result := e.runOneCall(ctx, planner, call, map[string]ref.Ref{"mcp": testutil.NewFakeRef(testutil.GenActorID(), nil)}, "step-1")
	if !result.isErr {
		t.Fatalf("expected error frame, got %q", result.out)
	}
	if !strings.Contains(result.out, "boom") {
		t.Errorf("error frame should carry the tool error, got %q", result.out)
	}
}

// TestRunOneCall_MCPTool_MalformedInput: bad arguments JSON becomes an error
// frame without hitting the planner.
func TestRunOneCall_MCPTool_MalformedInput(t *testing.T) {
	planner := &mcpRoutingPlanner{called: make(chan struct{})}
	e := newMCPRoutingEngine(t, planner)
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner { return planner }

	call := pendingToolCall{
		ID: "tool-1", LLMName: "mcp.primary.echo", CallableID: "mcp.srv-1.echo",
		ServiceName: "mcp", Input: `{not json`,
	}
	result := e.runOneCall(ctx, planner, call, map[string]ref.Ref{"mcp": testutil.NewFakeRef(testutil.GenActorID(), nil)}, "step-1")
	if !result.isErr || !strings.Contains(result.out, "invalid arguments") {
		t.Fatalf("expected invalid-arguments error frame, got %q isErr=%v", result.out, result.isErr)
	}
}

// ---------------------------------------------------------------------------
// Real chain E2E: turn engine → mcpmanager → mcpinstance → HTTP MCP server
// ---------------------------------------------------------------------------

type echoArgs struct {
	Text string `json:"text"`
}

// mcpE2EProbe runs the full agent-side chain (resolveMCPTools + runOneCall)
// inside a real runtime actor whose planner can reach the real mcpmanager.
// A non-zero call field overrides the default scripted tool_use.
type mcpE2EProbe struct {
	actor.Host
	resultCh chan mcpE2EResult
	call     pendingToolCall
}

type mcpE2EResult struct {
	specs []domain.ToolSpec
	out   string
	isErr bool
	debug string
}

// OnStart runs the full agent-side chain inside a real runtime actor whose
// planner can reach the real mcpmanager: resolveMCPTools (discovery) then
// runOneCall (routing an LLM tool_use to mcp.call_tool).
func (p *mcpE2EProbe) OnStart(ctx actor.Context) error {
	planner := ctx.Planner()
	mcpRef, svcOK := ctx.LookupService("mcp")
	debug := ""

	// Seed an mcp:<server-id> component mount so resolveMCPTools injects
	// tools only for mounted servers (here: the connected srv-0).
	testActor := &Actor{ComponentMounts: []domain.AgentComponentMount{
		{CardID: "mcp:srv-0", Enabled: true, Scope: "test"},
	}}
	specs := testActor.resolveMCPTools(ctx)
	if len(specs) == 0 {
		// Surface the raw discover_tools result for diagnosis.
		if planner != nil && svcOK {
			payload, _ := json.Marshal(domain.McpDiscoverToolsReq{})
			invokeCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
			result, err := planner.Call(invokeCtx, mcpRef, "mcp.discover_tools", payload).Await()
			cancel()
			debug = "raw discover: "
			if err != nil {
				debug += "err=" + err.Error()
			} else if b, ok := result.([]byte); ok {
				debug += string(b)
			} else {
				debug += "type=" + fmt.Sprintf("%T", result)
			}
		} else {
			debug = "planner=" + fmt.Sprintf("%v", planner != nil) + " svc=" + fmt.Sprintf("%v", svcOK)
		}
	}

	call := p.call
	if call.CallableID == "" {
		call = pendingToolCall{
			ID: "tool-e2e", LLMName: "mcp.echo-server.echo", CallableID: "mcp.srv-0.echo",
			ServiceName: "mcp", Input: `{"text":"hello from agent"}`,
		}
	}
	e := &turnEngine{done: make(chan struct{}), logger: newNopActorLogger(), allTools: specs}
	svcRefs := map[string]ref.Ref{}
	if svcOK {
		svcRefs["mcp"] = mcpRef
	}
	res := e.runOneCall(ctx, planner, call, svcRefs, "step-1")
	p.resultCh <- mcpE2EResult{specs: specs, out: res.out, isErr: res.isErr, debug: debug}
	return nil
}

// TestTurnEngineMCP_E2E drives the REAL chain: a live Streamable HTTP MCP
// server with an echo tool is connected through mcpmanager/mcpinstance, then
// resolveMCPTools discovers it and the turn engine routes an LLM tool_use
// through mcpmanager.call_tool, backfilling the echoed text into the tool
// frame. This is the closest the test suite gets to "LLM 真实调用一个 MCP 工具"
// without a live model: the tool_use is scripted, the execution chain is real.
func TestTurnEngineMCP_E2E(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	// 1. In-memory Streamable HTTP MCP server exposing one echo tool.
	server := mcp.NewServer(&mcp.Implementation{Name: "e2e-server", Version: "v0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echoes text back"},
		func(_ context.Context, _ *mcp.CallToolRequest, in echoArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: in.Text}}}, nil, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := httptest.NewServer(handler)
	defer func() {
		// The MCP client keeps an active keep-alive connection to the test
		// server; Close alone waits forever on it.
		httpServer.CloseClientConnections()
		httpServer.Close()
	}()

	// 2. Bootstrap the runtime with the real mcpmanager actor.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	handle, err := runtime.Bootstrap(ctx, runtime.Config{
		Children: []runtime.ChildSpec{
			{Name: "mcpmanager", Factory: func() actor.Actor { return &mcpmanager.Actor{} }, RequirePersistent: true, Role: "system"},
		},
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() {
		cancel()
		_ = handle.Wait()
	}()

	invoke := func(t *testing.T, callID string, payload any, role string, mcpRef ref.Ref) ([]byte, error) {
		t.Helper()
		var hdrs map[string]string
		if role != "" {
			hdrs = map[string]string{"gospore.caller_role": role}
		}
		call := mcpRef.Invoke(context.Background(), callID, payload, hdrs)
		if call == nil {
			t.Fatalf("invoke %s returned nil call", callID)
		}
		defer call.Close()
		return call.RecvRaw()
	}

	// 3. Wait for the manager's OnStart (mcp domain exposed), then add the
	//    HTTP server config and connect it.
	var mcpRef ref.Ref
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var ok bool
		mcpRef, ok = handle.App().LookupService("mcp")
		if ok {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if mcpRef == nil {
		t.Fatal("mcp service was never exposed")
	}
	if _, err := invoke(t, "mcp.add_server", domain.McpAddServerReq{Config: domain.McpServerConfig{
		Name: "echo-server", Transport: "http",
		Http:    &domain.McpHttpTransport{URL: httpServer.URL},
		Enabled: true,
	}}, "developer", mcpRef); err != nil {
		t.Fatalf("add_server: %v", err)
	}

	// The child is spawned with async start: its OnStart registers callables
	// and auto-connects. Poll connect (idempotent) until the session is live.
	connectDeadline := time.Now().Add(15 * time.Second)
	for {
		raw, err := invoke(t, "mcp.connect", domain.McpConnectReq{ID: "srv-0"}, "admin", mcpRef)
		if err == nil {
			var connResp domain.McpConnectResp
			if json.Unmarshal(raw, &connResp) == nil && connResp.Status.Connected {
				break
			}
		}
		if time.Now().After(connectDeadline) {
			t.Fatalf("server srv-0 never became connected: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 4. Spawn the probe actor; its OnStart runs resolveMCPTools + runOneCall
	//    against the real manager (internal caller, zero identity).
	probeResult := make(chan mcpE2EResult, 1)
	_, err = handle.App().Spawn(actor.PropsFromFunc(func() actor.Actor {
		return &mcpE2EProbe{resultCh: probeResult}
	}).WithPlanner(), "mcp-probe")
	if err != nil {
		t.Fatalf("spawn probe: %v", err)
	}

	var res mcpE2EResult
	select {
	case res = <-probeResult:
	case <-time.After(20 * time.Second):
		t.Fatal("probe did not finish in time")
	}

	if len(res.specs) != 1 {
		t.Fatalf("resolveMCPTools returned %d specs, want 1 (debug=%s)", len(res.specs), res.debug)
	}
	spec := res.specs[0]
	if spec.Name != "mcp-echo-server-echo" {
		t.Errorf("spec name = %q, want mcp-echo-server-echo", spec.Name)
	}
	if spec.CallableID != "mcp.srv-0.echo" {
		t.Errorf("spec callable = %q, want mcp.srv-0.echo", spec.CallableID)
	}
	if !strings.Contains(spec.InputSchema, `"text"`) {
		t.Errorf("inputSchema must carry the tool's properties, got %q", spec.InputSchema)
	}
	if res.isErr {
		t.Fatalf("MCP tool call failed: %q", res.out)
	}
	if res.out != "hello from agent" {
		t.Errorf("tool frame = %q, want echoed %q", res.out, "hello from agent")
	}
}

// TestTurnEngineMCP_E2E_UnmountedServerInvisible: two real Streamable HTTP MCP
// servers are connected; the agent mounts only one. resolveMCPTools exposes
// exactly the mounted server's tool (the unmounted server's tool is invisible
// to the LLM), and routing a scripted tool_use for the mounted tool
// round-trips through mcpmanager.call_tool into the real server.
func TestTurnEngineMCP_E2E_UnmountedServerInvisible(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	// 1. Two in-memory Streamable HTTP MCP servers, each with a distinct tool.
	mkServer := func(name, toolName, prefix string) *httptest.Server {
		s := mcp.NewServer(&mcp.Implementation{Name: name, Version: "v0.0.1"}, nil)
		mcp.AddTool(s, &mcp.Tool{Name: toolName, Description: "echoes text back"},
			func(_ context.Context, _ *mcp.CallToolRequest, in echoArgs) (*mcp.CallToolResult, any, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: prefix + in.Text}}}, nil, nil
			})
		h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil)
		ts := httptest.NewServer(h)
		return ts
	}
	serverA := mkServer("echo-a", "echo_a", "A:")
	serverB := mkServer("echo-b", "echo_b", "B:")
	defer func() {
		serverA.CloseClientConnections()
		serverA.Close()
		serverB.CloseClientConnections()
		serverB.Close()
	}()

	// 2. Bootstrap the runtime with the real mcpmanager.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	handle, err := runtime.Bootstrap(ctx, runtime.Config{
		Children: []runtime.ChildSpec{
			{Name: "mcpmanager", Factory: func() actor.Actor { return &mcpmanager.Actor{} }, RequirePersistent: true, Role: "system"},
		},
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() {
		cancel()
		_ = handle.Wait()
	}()

	invoke := func(t *testing.T, callID string, payload any, role string, mcpRef ref.Ref) ([]byte, error) {
		t.Helper()
		var hdrs map[string]string
		if role != "" {
			hdrs = map[string]string{"gospore.caller_role": role}
		}
		call := mcpRef.Invoke(context.Background(), callID, payload, hdrs)
		if call == nil {
			t.Fatalf("invoke %s returned nil call", callID)
		}
		defer call.Close()
		return call.RecvRaw()
	}

	// 3. Wait for the mcp service.
	var mcpRef ref.Ref
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var ok bool
		mcpRef, ok = handle.App().LookupService("mcp")
		if ok {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if mcpRef == nil {
		t.Fatal("mcp service was never exposed")
	}

	// 4. Add both servers and connect them.
	for _, server := range []struct {
		name string
		url  string
	}{
		{name: "echo-a", url: serverA.URL},
		{name: "echo-b", url: serverB.URL},
	} {
		if _, err := invoke(t, "mcp.add_server", domain.McpAddServerReq{Config: domain.McpServerConfig{
			Name: server.name, Transport: "http",
			Http:    &domain.McpHttpTransport{URL: server.url},
			Enabled: true,
		}}, "developer", mcpRef); err != nil {
			t.Fatalf("add_server %s: %v", server.name, err)
		}
	}

	connectDeadline := time.Now().Add(15 * time.Second)
	for {
		allConnected := true
		for _, id := range []string{"srv-0", "srv-1"} {
			raw, err := invoke(t, "mcp.connect", domain.McpConnectReq{ID: id}, "admin", mcpRef)
			if err != nil {
				allConnected = false
				break
			}
			var connResp domain.McpConnectResp
			if json.Unmarshal(raw, &connResp) != nil || !connResp.Status.Connected {
				allConnected = false
				break
			}
		}
		if allConnected {
			break
		}
		if time.Now().After(connectDeadline) {
			t.Fatal("servers never both became connected")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 5. Spawn a probe that mounts only mcp:srv-0 (echo-a) and routes a
	//    scripted tool_use for the mounted server's tool.
	probeResult := make(chan mcpE2EResult, 1)
	_, err = handle.App().Spawn(actor.PropsFromFunc(func() actor.Actor {
		return &mcpE2EProbe{resultCh: probeResult, call: pendingToolCall{
			ID: "tool-dual", LLMName: "mcp.echo-a.echo_a", CallableID: "mcp.srv-0.echo_a",
			ServiceName: "mcp", Input: `{"text":"hello from agent"}`,
		}}
	}).WithPlanner(), "mcp-probe-dual")
	if err != nil {
		t.Fatalf("spawn probe: %v", err)
	}

	var res mcpE2EResult
	select {
	case res = <-probeResult:
	case <-time.After(20 * time.Second):
		t.Fatal("probe did not finish in time")
	}

	if len(res.specs) != 1 {
		t.Fatalf("resolveMCPTools returned %d specs (want 1 for mounted server only), debug=%s", len(res.specs), res.debug)
	}
	spec := res.specs[0]
	if spec.Name != "mcp-echo-a-echo_a" {
		t.Errorf("spec name = %q, want mcp-echo-a-echo_a", spec.Name)
	}
	if spec.CallableID != "mcp.srv-0.echo_a" {
		t.Errorf("spec callable = %q, want mcp.srv-0.echo_a", spec.CallableID)
	}
	// Verify the unmounted server's tool is NOT present in any spec.
	for _, s := range res.specs {
		if strings.Contains(s.CallableID, "srv-1") || strings.Contains(s.Name, "echo-b") {
			t.Errorf("unmounted server's tool must not appear: %+v", s)
		}
	}
	if res.isErr {
		t.Fatalf("MCP tool call failed: %q", res.out)
	}
	if res.out != "A:hello from agent" {
		t.Errorf("tool frame = %q, want A:hello from agent", res.out)
	}
}

// TestMcpRespText covers the tool-frame text built from an MCP call result's
// content blocks: text blocks (including the child's diagnostic placeholders)
// are joined directly, image blocks render as a compact marker, and empty
// results produce an empty frame.
func TestMcpRespText(t *testing.T) {
	cases := []struct {
		name string
		resp domain.McpCallToolResp
		want string
	}{
		{
			name: "empty",
			resp: domain.McpCallToolResp{Content: nil},
			want: "",
		},
		{
			name: "text blocks joined",
			resp: domain.McpCallToolResp{Content: []domain.McpToolContent{
				{Type: "text", Text: "first"},
				{Type: "text", Text: "second"},
			}},
			want: "first\nsecond",
		},
		{
			name: "image block renders marker",
			resp: domain.McpCallToolResp{Content: []domain.McpToolContent{
				{Type: "image", Data: "aGVsbG8=", MimeType: "image/png"},
			}},
			want: "[image content block: type=image/png, 8 base64 chars]",
		},
		{
			name: "image block without mime falls back to unknown",
			resp: domain.McpCallToolResp{Content: []domain.McpToolContent{
				{Type: "image", Data: "YQ=="},
			}},
			want: "[image content block: type=unknown, 4 base64 chars]",
		},
		{
			name: "mixed text and image",
			resp: domain.McpCallToolResp{Content: []domain.McpToolContent{
				{Type: "text", Text: "found the diagram"},
				{Type: "image", Data: "aGVsbG8=", MimeType: "image/png"},
				{Type: "text", Text: "[audio content block: type=audio/wav, 1234 bytes]"},
			}},
			want: "found the diagram\n[image content block: type=image/png, 8 base64 chars]\n[audio content block: type=audio/wav, 1234 bytes]",
		},
		{
			name: "placeholder text passes through",
			resp: domain.McpCallToolResp{Content: []domain.McpToolContent{
				{Type: "text", Text: "[resource block: uri=https://example.com/pic.png, type=image/png, 9 bytes]"},
			}},
			want: "[resource block: uri=https://example.com/pic.png, type=image/png, 9 bytes]",
		},
		{
			name: "image without data renders no marker",
			resp: domain.McpCallToolResp{Content: []domain.McpToolContent{
				{Type: "image", MimeType: "image/png"},
			}},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mcpRespText(tc.resp); got != tc.want {
				t.Errorf("mcpRespText = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestMcpRespTextAttached covers the annotation mcpRespTextAttached adds for
// the image block that is delivered as the follow-up observation message.
func TestMcpRespTextAttached(t *testing.T) {
	resp := domain.McpCallToolResp{Content: []domain.McpToolContent{
		{Type: "text", Text: "diagram rendered"},
		{Type: "image", Data: "aGVsbG8=", MimeType: "image/png"},
		{Type: "image", Data: "eHl6", MimeType: "image/png"},
	}}
	want := "diagram rendered\n[image content block: type=image/png, 8 base64 chars, attached in the following message]\n[image content block: type=image/png, 4 base64 chars]"
	if got := mcpRespTextAttached(resp, 1); got != want {
		t.Errorf("mcpRespTextAttached = %q, want %q", got, want)
	}
	// attached=-1 keeps the plain markers (mcpRespText behavior).
	if got := mcpRespTextAttached(resp, -1); got != mcpRespText(resp) {
		t.Errorf("attached=-1 must match mcpRespText: %q vs %q", got, mcpRespText(resp))
	}
}

// TestMcpImageObservation covers the gates of the MCP tool-result image →
// user-role observation conversion: eligible first image yields an embedding
// message, oversized/invalid/absent images and the env opt-out yield none.
func TestMcpImageObservation(t *testing.T) {
	resp := domain.McpCallToolResp{Content: []domain.McpToolContent{
		{Type: "text", Text: "frame exported"},
		{Type: "image", Data: "aGVsbG8=", MimeType: "image/png"},
	}}

	idx, obs := mcpImageObservation(resp)
	if idx != 1 || obs == nil {
		t.Fatalf("idx=%d obs=%v, want idx=1 and an observation", idx, obs)
	}
	if obs.Role != domain.ChatRoleUser || len(obs.Content) != 2 {
		t.Fatalf("observation must be a 2-block user message, got role=%q blocks=%d", obs.Role, len(obs.Content))
	}
	if obs.Content[0].Type != domain.ContentBlockText || obs.Content[0].Text != "[mcp tool image: image/png, 5 bytes]" {
		t.Errorf("meta block = %+v", obs.Content[0])
	}
	img := obs.Content[1]
	if img.Type != domain.ContentBlockImage || img.MimeType != "image/png" || img.ImageURL != "data:image/png;base64,aGVsbG8=" {
		t.Errorf("image block = %+v", img)
	}

	// No image content: no observation.
	if _, obs := mcpImageObservation(domain.McpCallToolResp{Content: []domain.McpToolContent{{Type: "text", Text: "ok"}}}); obs != nil {
		t.Error("text-only result must not produce an observation")
	}

	// Invalid base64: no observation.
	if _, obs := mcpImageObservation(domain.McpCallToolResp{Content: []domain.McpToolContent{{Type: "image", Data: "!!!not-base64!!!", MimeType: "image/png"}}}); obs != nil {
		t.Error("invalid base64 must not produce an observation")
	}

	// Oversized (decoded > mcpImageObservationMaxBytes): no observation.
	big := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("A"), mcpImageObservationMaxBytes+1))
	if _, obs := mcpImageObservation(domain.McpCallToolResp{Content: []domain.McpToolContent{{Type: "image", Data: big, MimeType: "image/png"}}}); obs != nil {
		t.Error("oversized image must not produce an observation")
	}

	// Image blocks without data are ignored (mcpRespText renders no marker
	// for them either): the first image WITH data is the attachment candidate.
	idx, obs = mcpImageObservation(domain.McpCallToolResp{Content: []domain.McpToolContent{
		{Type: "image", Data: "", MimeType: "image/png"},
		{Type: "image", Data: "aGVsbG8=", MimeType: "image/png"},
	}})
	if idx != 1 || obs == nil {
		t.Fatalf("idx=%d obs=%v, want idx=1 (empty-Data block skipped)", idx, obs)
	}

	// Empty mime defaults to image/png in the observation (and to "unknown"
	// in the plain marker — see TestMcpRespText).
	_, obs = mcpImageObservation(domain.McpCallToolResp{Content: []domain.McpToolContent{{Type: "image", Data: "aGVsbG8="}}})
	if obs == nil || obs.Content[1].MimeType != "image/png" {
		t.Errorf("empty mime must default to image/png, got %+v", obs)
	}

	// Env opt-out disables the embedding.
	t.Setenv("SPOREMIND_MCP_IMAGE_OBSERVATION", "0")
	if _, obs := mcpImageObservation(resp); obs != nil {
		t.Error("SPOREMIND_MCP_IMAGE_OBSERVATION=0 must disable the observation")
	}
}
