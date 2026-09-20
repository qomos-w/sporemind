package agent

// Integration test against a REAL MCP server: the official filesystem server
// (npx @modelcontextprotocol/server-filesystem) over the stdio transport.
//
// It drives the exact chain the app UI wires together:
//
//	设置面板添加 (mcp.add_server)
//	  → 连接 (mcp.connect)
//	  → 拓扑图出现节点 (UnifiedGraph mcp-server node, status connected)
//	  → agent turn 调用其工具 (resolveMCPTools + runOneCall read_file)
//	  → ai-step 渲染结果 (tool frame output carries the file content; the
//	    frontend rendering of that frame is covered by McpToolView.test.tsx)
//
// The test is skipped when node/npx are not on PATH or the server package
// cannot be fetched (no network), so the default suite stays green on
// machines without a Node toolchain.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/gospore/resource"

	"github.com/qomos-w/sporemind/pkg/actor/mcpmanager"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// fsE2EProbe runs the agent-side chain (resolveMCPTools + runOneCall) inside a
// real runtime actor whose planner can reach the real mcpmanager.
type fsE2EProbe struct {
	actor.Host
	resultCh chan fsE2EResult
	call     pendingToolCall
}

type fsE2EResult struct {
	specs []domain.ToolSpec
	out   string
	isErr bool
	debug string
}

func (p *fsE2EProbe) OnStart(ctx actor.Context) error {
	planner := ctx.Planner()
	mcpRef, svcOK := ctx.LookupService("mcp")
	// Seed an mcp:<server-id> component mount so resolveMCPTools injects
	// tools only for mounted servers (here: the connected srv-0).
	testActor := &Actor{ComponentMounts: []domain.AgentComponentMount{
		{CardID: "mcp:srv-0", Enabled: true, Scope: "test"},
	}}
	specs := testActor.resolveMCPTools(ctx)
	debug := ""
	if len(specs) == 0 {
		// Surface the raw discover_tools payload for diagnosis.
		if planner != nil && svcOK {
			if payload, err := json.Marshal(domain.McpDiscoverToolsReq{}); err == nil {
				if res, err := planner.Call(ctx.Lifecycle(), mcpRef, "mcp.discover_tools", payload).Await(); err == nil {
					debug = fmt.Sprintf("%v", res)
				} else {
					debug = "discover_tools error: " + err.Error()
				}
			}
		} else {
			debug = "planner or mcp service unavailable"
		}
	}
	e := &turnEngine{done: make(chan struct{}), logger: newNopActorLogger(), allTools: specs}
	svcRefs := map[string]ref.Ref{}
	if svcOK {
		svcRefs["mcp"] = mcpRef
	}
	res := e.runOneCall(ctx, planner, p.call, svcRefs, "step-1")
	p.resultCh <- fsE2EResult{specs: specs, out: res.out, isErr: res.isErr, debug: debug}
	return nil
}

// TestTurnEngineMCP_E2E_RealFilesystemServer verifies the whole MCP chain
// against the REAL official filesystem server.
func TestTurnEngineMCP_E2E_RealFilesystemServer(t *testing.T) {
	npxBin, err := exec.LookPath("npx")
	if err != nil {
		t.Skip("npx not found on PATH; cannot run the real filesystem server")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found on PATH; cannot run the real filesystem server")
	}

	root := t.TempDir()
	marker := filepath.Join(root, "marker.txt")
	const markerContent = "hello from the real filesystem server\n"
	if err := os.WriteFile(marker, []byte(markerContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pre-warm the npx package cache so the mcpinstance connect handshake
	// budget is not consumed by a cold package download.
	prewarmFilesystemServer(t, npxBin, root)

	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
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

	// Wait for the mcp service (manager OnStart exposed the domain).
	var mcpRef ref.Ref
	svcDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(svcDeadline) {
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

	// 1. 设置面板添加: the same mcp.add_server callable the settings panel
	//    invokes, pointing at the real official filesystem server over stdio.
	if _, err := invoke(t, "mcp.add_server", domain.McpAddServerReq{Config: domain.McpServerConfig{
		Name:      "filesystem",
		Transport: "stdio",
		Stdio: &domain.McpStdioTransport{
			Command: npxBin,
			Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", root},
		},
		Enabled: true,
	}}, "developer", mcpRef); err != nil {
		t.Fatalf("add_server: %v", err)
	}

	// 2. 连接: poll mcp.connect (idempotent) until the session is live.
	connectDeadline := time.Now().Add(60 * time.Second)
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
		time.Sleep(100 * time.Millisecond)
	}

	// 3. 拓扑图出现节点: the mcpinstance child surfaces as a mcp-server node
	//    whose identity/status is enriched from the ServerViews projection.
	//    Each loop iteration re-invokes mcp.connect (idempotent) so the child
	//    cell republishes its projection to the topology provider's Watch
	//    subscription, which marks the graph dirty and rebuilds it.
	topo, ok := resource.Get[runtime.TopologyProvider](handle.App().Resources(), runtime.TopologyKey)
	if !ok {
		t.Fatal("topology provider not available")
	}
	var node domain.UnifiedGraphNode
	found := false
	topoDeadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(topoDeadline) {
		_, _ = invoke(t, "mcp.connect", domain.McpConnectReq{ID: "srv-0"}, "admin", mcpRef)
		g := topo.UnifiedGraph()
		for _, n := range g.Nodes {
			if n.Kind == "mcp-server" && n.ID != "" {
				node = n
				found = true
				break
			}
		}
		if found && node.Status == "connected" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !found {
		t.Fatal("topology graph never contained an mcp-server node")
	}
	if node.Status != "connected" {
		t.Fatalf("mcp-server node status = %q, want connected (node label %q)", node.Status, node.Label)
	}
	if node.Label != "filesystem" {
		t.Errorf("mcp-server node label = %q, want filesystem", node.Label)
	}
	if tc, _ := node.Detail["tool_count"].(int); tc == 0 {
		t.Errorf("mcp-server node tool_count detail = %v, want > 0", node.Detail["tool_count"])
	}

	// 4. agent turn 调用其工具: scripted tool_use for read_file, exactly the
	//    callableID the turn engine resolves from the ToolSpec.
	args, err := json.Marshal(map[string]any{"path": marker})
	if err != nil {
		t.Fatal(err)
	}
	call := pendingToolCall{
		ID:          "tool-fs",
		LLMName:     "mcp.filesystem.read_file",
		CallableID:  "mcp.srv-0.read_file",
		ServiceName: "mcp",
		Input:       string(args),
	}
	probeResult := make(chan fsE2EResult, 1)
	if _, err := handle.App().Spawn(actor.PropsFromFunc(func() actor.Actor {
		return &fsE2EProbe{resultCh: probeResult, call: call}
	}).WithPlanner(), "mcp-fs-probe"); err != nil {
		t.Fatalf("spawn probe: %v", err)
	}

	var res fsE2EResult
	select {
	case res = <-probeResult:
	case <-time.After(30 * time.Second):
		t.Fatal("probe did not finish in time")
	}

	var readSpec *domain.ToolSpec
	for i := range res.specs {
		if res.specs[i].Name == "mcp-filesystem-read_file" {
			readSpec = &res.specs[i]
			break
		}
	}
	if readSpec == nil {
		names := make([]string, 0, len(res.specs))
		for _, s := range res.specs {
			names = append(names, s.Name)
		}
		t.Fatalf("resolveMCPTools did not expose mcp.filesystem.read_file (specs=%v debug=%s)", names, res.debug)
	}
	if readSpec.CallableID != "mcp.srv-0.read_file" {
		t.Errorf("read_file callable = %q, want mcp.srv-0.read_file", readSpec.CallableID)
	}
	if !strings.Contains(readSpec.InputSchema, `"path"`) {
		t.Errorf("read_file inputSchema must carry the path property, got %q", readSpec.InputSchema)
	}
	if res.isErr {
		t.Fatalf("read_file call failed: %q", res.out)
	}
	// 5. ai-step 渲染结果: the tool frame the turn engine backfills carries the
	//    server's answer; McpToolView.test.tsx locks how that frame renders.
	if !strings.Contains(res.out, "hello from the real filesystem server") {
		t.Errorf("tool frame = %q, want it to contain the marker file content", res.out)
	}
}

// prewarmFilesystemServer downloads the official filesystem server package via
// npx (if not already cached), waits until the server reports ready, then
// closes its stdin so it exits cleanly. The real connect in the test then
// starts instantly instead of spending its initialize budget on a cold
// download. The test is skipped when the server cannot be obtained (no node
// toolchain, no network, package fetch failure).
func prewarmFilesystemServer(t *testing.T, npxBin, root string) {
	t.Helper()
	cmd := exec.Command(npxBin, "-y", "@modelcontextprotocol/server-filesystem", root)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("prewarm stdin pipe: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("prewarm stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Skipf("filesystem server cannot start (npx=%q): %v", npxBin, err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	ready := make(chan struct{})
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, rerr := stderr.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
				if strings.Contains(sb.String(), "running on stdio") {
					close(ready)
					return
				}
			}
			if rerr != nil {
				return
			}
		}
	}()
	select {
	case err := <-waitDone:
		t.Skipf("filesystem server exited during prewarm (npx cache/network unavailable?): %v", err)
	case <-ready:
		// Server is up: EOF on stdin makes it exit cleanly.
		_ = stdin.Close()
		<-waitDone
	case <-time.After(120 * time.Second):
		_ = cmd.Process.Kill()
		<-waitDone
		t.Fatalf("filesystem server did not become ready within 120s")
	}
}
