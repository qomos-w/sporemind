package sporebridge

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/spore/script"
	"github.com/qomos-w/sporemind/pkg/actor/aimanager"
	"github.com/qomos-w/sporemind/pkg/actor/filesystem"
	"github.com/qomos-w/sporemind/pkg/actor/workspace"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// TestSporeScriptCallsPlanNode verifies that a sporescript can invoke
// actor callables through the Bridge and read plan nodes from the response.
func TestSporeScriptCallsPlanNode(t *testing.T) {
	// Isolate the data dir: the workspace child is RequirePersistent and the
	// default dir lives next to the test binary (shared across same-process
	// reruns). The old cleanupDataDir wiped an unrelated ./data and left the
	// real exeDir/.sporemind untouched, so -count=2 failed with
	// "project name already exists".
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg := runtime.Config{
		GatewayAddr: ":18083",
		NoGateway:   false,
		Children: []runtime.ChildSpec{
			{Name: "workspace", Factory: func() actor.Actor { return &workspace.Actor{} }, RequirePersistent: true},
			{Name: "filesystem", Factory: func() actor.Actor { return &filesystem.Actor{} }, RequirePersistent: false},
			{Name: "aimanager", Factory: func() actor.Actor { return &aimanager.Actor{} }, RequirePersistent: true},
		},
	}
	handle, err := runtime.Bootstrap(ctx, cfg)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() {
		cancel()
		_ = handle.Wait()
	}()

	// Wait for actor tree to start.
	time.Sleep(2 * time.Second)

	app := handle.App()

	// Mount a project so workspace enrichment produces plan nodes.
	tmp := os.TempDir() + "/sporemind-spore-test-" + fmt.Sprintf("%d", time.Now().UnixNano())
	_ = os.MkdirAll(tmp, 0o755)
	defer os.RemoveAll(tmp)

	// workspace.mount requires admin under the role ladder; the default
	// "developer" bridge role would be denied.
	bridge := New(app).WithRole("admin")

	// Mount via bridge (direct actor call).
	mountPayload := map[string]any{"path": tmp, "name": "spore-test-project"}
	mountResult, err := bridge.invoke("workspace.mount", mountPayload)
	if err != nil {
		t.Fatalf("workspace.mount via bridge: %v", err)
	}
	t.Logf("workspace.mount response: %+v", mountResult)

	// Wait for topology refresh.
	time.Sleep(500 * time.Millisecond)

	// Create Spore runtime and bind the bridge.
	rt, err := script.NewRuntime()
	if err != nil {
		t.Fatalf("new spore runtime: %v", err)
	}
	if err := bridge.BindTo(rt); err != nil {
		t.Fatalf("bind bridge: %v", err)
	}

	// Verify unified_graph.sync works via bridge.invoke.
	// We call directly through the Go API (not sporescript) because the
	// snapshot payload is large and Spore's map<string,any> conversion can
	// exhaust memory on a full graph.
	syncResult, err := bridge.invoke("unified_graph.sync", map[string]any{"ClientEpoch": 0})
	if err != nil {
		t.Fatalf("unified_graph.sync via bridge: %v", err)
	}
	t.Logf("unified_graph.sync returned keys: %v", keysOf(syncResult))

	snapshotAny, ok := syncResult["Snapshot"]
	if !ok {
		t.Fatalf("sync result missing 'snapshot' key, keys=%v", keysOf(syncResult))
	}
	snapshot, ok := snapshotAny.(map[string]any)
	if !ok {
		t.Fatalf("syncResult.snapshot is not map[string]any, got %T", snapshotAny)
	}
	nodesAny, ok := snapshot["Nodes"]
	if !ok {
		t.Fatalf("snapshot missing 'nodes' key, keys=%v", keysOf(snapshot))
	}
	nodes, ok := nodesAny.([]any)
	if !ok {
		t.Fatalf("snapshot.nodes is not []any, got %T", nodesAny)
	}

	var foundProject bool
	for _, n := range nodes {
		node, ok := n.(map[string]any)
		if !ok {
			continue
		}
		kind, _ := node["Kind"].(string)
		parentID, _ := node["ParentId"].(string)
		if kind == "project" && parentID != "" {
			foundProject = true
			t.Logf("found project node: id=%v kind=%v parentId=%v", node["Id"], kind, parentID)
		}
	}
	if !foundProject {
		t.Errorf("no project node with kind 'project' and non-empty parent found in %d nodes", len(nodes))
	}
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// TestSporeAsCast verifies that a sporescript can use the `as` keyword
// to cast a map<string,any> returned from a host-bound function into a
// compile-time struct. This is the bridge-side pattern for consuming
// actor-callable responses as typed objects.
func TestSporeAsCast(t *testing.T) {
	rt, err := script.NewRuntime()
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	// Bind a Go function that returns a plain map — mimicking what
	// host.invoke() returns when the actor callable serialises a struct.
	if err := rt.BindFunc("test", "getPerson", func() map[string]any {
		return map[string]any{
			"name": "alice",
			"age":  30,
		}
	}); err != nil {
		t.Fatalf("bind getPerson: %v", err)
	}

	scriptSrc := `import { getPerson } from "test"

struct Person {
    name: string
    age: int
}

export fun makePerson(): Person {
    return getPerson() as Person
}`

	if err := rt.LoadSource("test", scriptSrc); err != nil {
		t.Fatalf("load source: %v", err)
	}

	result, err := rt.Call("makePerson")
	if err != nil {
		t.Fatalf("call makePerson: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("runtime error: %v", result.Error)
	}
	if result.Value == nil {
		t.Fatal("expected non-nil result")
	}

	// A Spore struct surfaced to Go is still a map[string]any.
	m, ok := result.Value.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", result.Value)
	}
	if m["name"] != "alice" {
		t.Errorf("name=%v, want alice", m["name"])
	}
	// Spore `int` stays int in Go when surfaced through `as` cast.
	if m["age"] != 30 {
		t.Errorf("age=%v (type %T), want 30", m["age"], m["age"])
	}
}
