package agent

// E2E: the browser-chat:<instanceId> mount chain that the composer %-mention
// feature activates. Pins the two halves the agent's crawl capability rides on:
//
//	1. Tool surface: a mounted browser-chat card must materialize the crawl.*
//	   ToolSpecs from the REAL topology (crawl node → callables map →
//	   componentSnapshotToolIDs allowlist), with ServiceName "crawl" so the
//	   turn engine routes the call to the exposed crawl service.
//	2. Execution routing + role gate: runOneCall (the turn engine's dispatch
//	   path) delivers crawl.start to the exposed "crawl" service; the crawl
//	   handler's RequireDeveloperOrManager gate admits project agents (cell
//	   role "system") and rejects global agents (RoleAgent).
//
// The real crawl actor lives in pkg/actor/crawl, which (via pkg/actor/project)
// imports this package — a test-only import cycle — so this file drives a stub
// actor that mirrors the real registration surface exactly: same domain name,
// same five callable IDs, same policy.RequireDeveloperOrManager gate. The real
// handler's gate is pinned separately in pkg/actor/crawl
// (TestBrowserCrawlStart_RoleGate).

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/gospore/resource"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/policy"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// crawlStubActor mirrors pkg/actor/crawl's registration surface: the five
// crawl.* callables behind the exposed "crawl" domain, with the same
// policy.RequireDeveloperOrManager gate on crawl.start.
type crawlStubActor struct {
	actor.Host
}

func (a *crawlStubActor) Type() string { return "crawl" }

func (a *crawlStubActor) OnStart(ctx actor.Context) error {
	for _, id := range []string{"crawl.start", "crawl.status", "crawl.results", "crawl.cancel", "crawl.handoff"} {
		if err := ctx.Register(id, func(ctx actor.Context, req gen.BrowserCrawlStartReq) (gen.BrowserCrawlStartResp, error) {
			if err := policy.RequireDeveloperOrManager(ctx.Identity().Role); err != nil {
				return gen.BrowserCrawlStartResp{}, err
			}
			return gen.BrowserCrawlStartResp{TaskID: "crawl-stub-1"}, nil
		}, actor.Public(), actor.WithDescription("crawl stub "+id)); err != nil {
			return fmt.Errorf("crawl stub: register %s: %w", id, err)
		}
	}
	if err := ctx.RegisterDomain("crawl").Expose(); err != nil {
		return fmt.Errorf("crawl stub: expose crawl domain: %w", err)
	}
	return nil
}

type crawlChainResult struct {
	specNames []string
	out       string
	isErr     bool
}

type crawlChainProbe struct {
	actor.Host
	resultCh chan crawlChainResult
}

func (p *crawlChainProbe) OnStart(ctx actor.Context) error {
	// 1. Tool surface from the real topology provider: a bare Actor with only
	// the browser-chat mount, exactly what componentMount persists.
	testAgent := &Actor{ComponentMounts: []domain.AgentComponentMount{
		{CardID: "browser-chat:inst-0", Enabled: true, Scope: "user"},
	}}
	if prov, ok := resource.Get[runtime.TopologyProvider](ctx.Resources(), runtime.TopologyKey); ok {
		testAgent.topo = prov
	}
	specs := testAgent.resolveTools(ctx, domain.AgentKindConfig{Kind: "coder"}, testAgent.callablesMap())
	crawlNames := make([]string, 0, 5)
	for _, s := range specs {
		switch s.CallableID {
		case "crawl.start", "crawl.status", "crawl.results", "crawl.cancel", "crawl.handoff":
			crawlNames = append(crawlNames, s.Name+"/"+s.ServiceName)
		}
	}

	// 2. Execution: the turn engine's dispatch path for a crawl.start tool
	// call with a pre-bound Config.InstanceID, mirroring the descriptor Usage.
	out := ""
	isErr := false
	crawlRef, ok := ctx.LookupService("crawl")
	if !ok {
		out, isErr = "crawl service not exposed", true
	} else {
		e := &turnEngine{done: make(chan struct{}), logger: newNopActorLogger(), allTools: specs}
		res := e.runOneCall(ctx, ctx.Planner(), pendingToolCall{
			ID:          "tool-crawl",
			LLMName:     "crawl-start",
			CallableID:  "crawl.start",
			ServiceName: "crawl",
			Input:       `{"Config":{"Seeds":["https://example.com"],"InstanceID":"inst-0"}}`,
		}, map[string]ref.Ref{"crawl": crawlRef}, "step-1")
		out, isErr = res.out, res.isErr
	}
	p.resultCh <- crawlChainResult{specNames: crawlNames, out: out, isErr: isErr}
	return nil
}

func TestBrowserChatCrawlChain_E2E(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	handle, err := runtime.Bootstrap(ctx, runtime.Config{
		Children: []runtime.ChildSpec{
			{Name: "crawl", Factory: func() actor.Actor { return &crawlStubActor{} }, RequirePersistent: false, Role: "system"},
		},
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() {
		cancel()
		_ = handle.Wait()
	}()

	// Wait for the crawl service domain to be exposed.
	svcDeadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(svcDeadline) {
		if _, ok := handle.App().LookupService("crawl"); ok {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, ok := handle.App().LookupService("crawl"); !ok {
		t.Fatal("crawl service was never exposed")
	}

	// Wait until the topology snapshot carries the crawl node's callables.
	topo, ok := resource.Get[runtime.TopologyProvider](handle.App().Resources(), runtime.TopologyKey)
	if !ok {
		t.Fatal("topology provider not available")
	}
	topoDeadline := time.Now().Add(20 * time.Second)
	crawlReady := false
	for time.Now().Before(topoDeadline) {
		for _, node := range topo.Snapshot() {
			if node.Kind != "crawl" {
				continue
			}
			for _, ci := range node.Callables {
				if ci.Name == "crawl.start" {
					crawlReady = true
					break
				}
			}
		}
		if crawlReady {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !crawlReady {
		t.Fatal("topology snapshot never carried crawl.start")
	}

	// Both caller roles the product uses: project agents inherit the project
	// cell's "system" role; workspace-global agents are stamped RoleAgent.
	results := map[string]crawlChainResult{}
	for _, role := range []string{"system", "agent"} {
		ch := make(chan crawlChainResult, 1)
		if _, err := handle.App().Spawn(actor.PropsFromFunc(func() actor.Actor {
			return &crawlChainProbe{resultCh: ch}
		}).WithPlanner().WithRole(role), "crawl-chain-probe-"+role); err != nil {
			t.Fatalf("spawn probe(%s): %v", role, err)
		}
		select {
		case results[role] = <-ch:
		case <-time.After(30 * time.Second):
			t.Fatalf("probe(%s) did not finish in time", role)
		}
	}

	// 1. Tool surface: both roles resolve the crawl specs with ServiceName
	// "crawl" (routing target), regardless of the caller role.
	for role, res := range results {
		if len(res.specNames) < 5 {
			t.Fatalf("probe(%s): browser-chat mount materialized only %v crawl specs, want all 5 (crawl.start/status/results/cancel/handoff)", role, res.specNames)
		}
		for _, name := range res.specNames {
			if !strings.HasSuffix(name, "/crawl") {
				t.Errorf("probe(%s): crawl spec %q must route to ServiceName \"crawl\"", role, name)
			}
		}
	}

	// 2. Role gate: "system" (project agents) passes and gets a TaskID;
	// "agent" (global agents) is rejected by RequireDeveloperOrManager.
	sys := results["system"]
	if sys.isErr {
		t.Fatalf("system-role probe: crawl.start failed: %q", sys.out)
	}
	if !strings.Contains(sys.out, "crawl-stub-1") {
		t.Fatalf("system-role probe: crawl.start returned %q, want the stub TaskID", sys.out)
	}
	ag := results["agent"]
	if !ag.isErr || !strings.Contains(ag.out, "forbidden") {
		t.Fatalf("agent-role probe: expected the role-gate rejection, got isErr=%v out=%q", ag.isErr, ag.out)
	}
}
