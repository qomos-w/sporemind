package pluginhost

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
)

// mutateHelloWithLifecycleListener stages a hello variant that listens to
// app_lifecycle (sdk.RegisterEventListener) and exposes a lifecycle_probe
// callable returning the last delivered event — the plugin-side observable
// for host→plugin event delivery.
func mutateHelloWithLifecycleListener(t testing.TB, modDir string) {
	t.Helper()
	srcPath := filepath.Join(modDir, "main.go")
	data, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read staged main.go: %v", err)
	}
	src := string(data)

	src = strings.Replace(src, "func init() {",
		"var lastLifecycle string\n\nfunc init() {", 1)
	src = strings.Replace(src,
		"\t\t\tPermissions: []string{sdk.PermConfigRead, sdk.PermLLMInvoke},",
		"\t\t\tPermissions: []string{sdk.PermConfigRead, sdk.PermLLMInvoke},\n\t\t\tListens:     []string{\"app_lifecycle\"},", 1)
	src = strings.Replace(src,
		"\t\t// ask demonstrates streaming LLM",
		`			ctx.RegisterEventListener("app_lifecycle", func(payload json.RawMessage) error {
				var ev struct {
					Kind string `+"`json:\"Kind\"`"+`
					ID   string `+"`json:\"Id\"`"+`
				}
				if err := json.Unmarshal(payload, &ev); err != nil {
					return err
				}
				lastLifecycle = ev.Kind + "|" + ev.ID
				return nil
			})
			ctx.RegisterCallable("lifecycle_probe", func(req sdk.Request) (sdk.Response, error) {
				return sdk.Response{Payload: map[string]string{"last": lastLifecycle}}, nil
			})
			// ask demonstrates streaming LLM`, 1)
	if !strings.Contains(src, "RegisterEventListener") {
		t.Fatal("mutation anchors not found in staged main.go")
	}
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write staged main.go: %v", err)
	}
}

// mutateHelloWithStepListener stages a hello variant that listens to `step`
// (sdk.RegisterEventListener) and exposes a step_probe callable returning the
// last delivered step — the plugin-side observable for the capability-gated
// agent event path.
func mutateHelloWithStepListener(t testing.TB, modDir string) {
	t.Helper()
	srcPath := filepath.Join(modDir, "main.go")
	data, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read staged main.go: %v", err)
	}
	src := string(data)

	src = strings.Replace(src, "func init() {",
		"var lastStep string\n\nfunc init() {", 1)
	src = strings.Replace(src,
		"\t\t\tPermissions: []string{sdk.PermConfigRead, sdk.PermLLMInvoke},",
		"\t\t\tPermissions: []string{sdk.PermConfigRead, sdk.PermLLMInvoke},\n\t\t\tListens:     []string{\"step\"},", 1)
	src = strings.Replace(src,
		"\t\t// ask demonstrates streaming LLM",
		`			ctx.RegisterEventListener("step", func(payload json.RawMessage) error {
				var ev struct {
					StepID string `+"`json:\"StepId\"`"+`
					Delta  string `+"`json:\"Delta\"`"+`
				}
				if err := json.Unmarshal(payload, &ev); err != nil {
					return err
				}
				lastStep = ev.StepID + "|" + ev.Delta
				return nil
			})
			ctx.RegisterCallable("step_probe", func(req sdk.Request) (sdk.Response, error) {
				return sdk.Response{Payload: map[string]string{"last": lastStep}}, nil
			})
			// ask demonstrates streaming LLM`, 1)
	if !strings.Contains(src, "RegisterEventListener(\"step\"") {
		t.Fatal("mutation anchors not found in staged main.go")
	}
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write staged main.go: %v", err)
	}
}

// TestEventDeliverCapabilityGate drives the agent.observe gate on the step
// event: the same listening plugin binary is loaded twice — first without the
// agent.observe permission (delivery blocked, failure names the capability),
// then with it (delivery reaches the SDK listener) — pinning that the manifest
// permission set, not the listen declaration alone, authorizes
// capability-restricted host events.
func TestEventDeliverCapabilityGate(t *testing.T) {
	exe := buildHelloSubprocessVariant(t, mutateHelloWithStepListener)

	a := &Actor{}
	loader := pluginhost.NewArtifactLoader(a)
	loader.SetOpener(&transportOpener{devMode: true, inprocess: loaderOpener{}, subprocess: &processOpener{}})
	a.loader = loader

	stepPayload := `{"Kind":"step","StepId":"step-7","TurnId":"turn-1","Delta":"hi"}`

	// Load without agent.observe: the listen declaration alone must not
	// authorize delivery.
	manifest := helloManifest()
	manifest.Listens = []string{"step"}
	manifest.Callables = append(manifest.Callables, gen.AppCallableDescriptor{
		ID: "step_probe", RequestSchema: "ProbeRequest", ResponseSchema: "ProbeResponse",
	})
	if _, err := loader.Load(context.Background(), gen.PluginArtifactLoadReq{
		ArtifactPath: exe,
		Manifest:     manifest,
		Abi:          subprocessAbi(),
	}); err != nil {
		t.Fatalf("load: %v", err)
	}
	resp, err := a.handleEventDeliver(nil, gen.PluginEventDeliverReq{Kind: "step", Payload: stepPayload})
	if err != nil {
		t.Fatalf("event deliver: %v", err)
	}
	if resp.Delivered != 0 || len(resp.Failures) != 1 {
		t.Fatalf("delivered=%d failures=%v, want 0/1", resp.Delivered, resp.Failures)
	}
	if !strings.Contains(resp.Failures[0], `requires capability "agent.observe"`) {
		t.Fatalf("failure = %q, want capability gate message", resp.Failures[0])
	}

	// Reload the same binary with agent.observe granted: delivery flows.
	if _, err := loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: manifest.ID}); err != nil {
		t.Fatalf("unload: %v", err)
	}
	manifest.Permissions = append(manifest.Permissions, appbinding.CapAgentObserve)
	if _, err := loader.Load(context.Background(), gen.PluginArtifactLoadReq{
		ArtifactPath: exe,
		Manifest:     manifest,
		Abi:          subprocessAbi(),
	}); err != nil {
		t.Fatalf("reload with capability: %v", err)
	}
	t.Cleanup(func() {
		_, _ = loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: manifest.ID})
	})

	resp, err = a.handleEventDeliver(nil, gen.PluginEventDeliverReq{Kind: "step", Payload: stepPayload})
	if err != nil {
		t.Fatalf("event deliver (granted): %v", err)
	}
	if resp.Delivered != 1 {
		t.Fatalf("delivered = %d, want 1 (failures: %v)", resp.Delivered, resp.Failures)
	}

	// Delivery is asynchronous (bounded drain queue): poll the probe until the
	// event has been dispatched to the plugin.
	probe, ok := a.handler(pluginhost.PluginCallID(manifest.ID, "step_probe"))
	if !ok {
		t.Fatal("step_probe handler not registered")
	}
	var probeOut struct {
		Last string `json:"last"`
	}
	if !waitEventProbe(t, probe, &probeOut, "step-7|hi") {
		t.Fatalf("last step = %q, want step-7|hi", probeOut.Last)
	}
}

// TestEventDeliverSubprocessEndToEnd drives the full host→plugin event path:
// appmanager-shaped manifest.Listens → loader-installed `__event__:` route →
// subprocess frame → SDK RegisterEventListener dispatch → recorded payload
// readable back through a probe callable. Also pins the fan-out filter (a
// kind the plugin does not listen to delivers zero) and the unknown-kind
// rejection.
func TestEventDeliverSubprocessEndToEnd(t *testing.T) {
	exe := buildHelloSubprocessVariant(t, mutateHelloWithLifecycleListener)

	a := &Actor{}
	loader := pluginhost.NewArtifactLoader(a)
	loader.SetOpener(&transportOpener{devMode: true, inprocess: loaderOpener{}, subprocess: &processOpener{}})
	a.loader = loader

	manifest := helloManifest()
	manifest.Listens = []string{"app_lifecycle"}
	manifest.Callables = append(manifest.Callables, gen.AppCallableDescriptor{
		ID: "lifecycle_probe", RequestSchema: "ProbeRequest", ResponseSchema: "ProbeResponse",
	})
	if _, err := loader.Load(context.Background(), gen.PluginArtifactLoadReq{
		ArtifactPath: exe,
		Manifest:     manifest,
		Abi:          subprocessAbi(),
	}); err != nil {
		t.Fatalf("load: %v", err)
	}
	t.Cleanup(func() {
		_, _ = loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: manifest.ID})
	})

	// Deliver an app_lifecycle event through the fan-out handler.
	resp, err := a.handleEventDeliver(nil, gen.PluginEventDeliverReq{
		Kind:    "app_lifecycle",
		Payload: `{"Kind":"reloaded","Id":"app.todolist","Runtime":"native","State":"running"}`,
	})
	if err != nil {
		t.Fatalf("event deliver: %v", err)
	}
	if resp.Delivered != 1 {
		t.Fatalf("delivered = %d, want 1 (failures: %v)", resp.Delivered, resp.Failures)
	}

	// The plugin recorded the decoded payload (asynchronous delivery — poll).
	probe, ok := a.handler(pluginhost.PluginCallID(manifest.ID, "lifecycle_probe"))
	if !ok {
		t.Fatal("lifecycle_probe handler not registered")
	}
	var probeOut struct {
		Last string `json:"last"`
	}
	if !waitEventProbe(t, probe, &probeOut, "reloaded|app.todolist") {
		t.Fatalf("last lifecycle = %q, want reloaded|app.todolist", probeOut.Last)
	}

	// Fan-out filter: a kind the plugin does not declare delivers zero.
	resp, err = a.handleEventDeliver(nil, gen.PluginEventDeliverReq{Kind: "app_event", Payload: `{}`})
	if err != nil {
		t.Fatalf("event deliver (app_event): %v", err)
	}
	if resp.Delivered != 0 {
		t.Errorf("app_event delivered = %d, want 0 (plugin listens only to app_lifecycle)", resp.Delivered)
	}

	// Unknown kinds are rejected outright.
	if _, err := a.handleEventDeliver(nil, gen.PluginEventDeliverReq{Kind: "not_a_host_event", Payload: `{}`}); err == nil ||
		!strings.Contains(err.Error(), "not a subscribable host event") {
		t.Errorf("unknown kind must be rejected, got %v", err)
	}

	// Unload removes the record and its event route: subsequent delivery is
	// a clean zero (no listeners left), not an error.
	if _, err := loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: manifest.ID}); err != nil {
		t.Fatalf("unload: %v", err)
	}
	resp, err = a.handleEventDeliver(nil, gen.PluginEventDeliverReq{Kind: "app_lifecycle", Payload: `{}`})
	if err != nil {
		t.Fatalf("post-unload deliver: %v", err)
	}
	if resp.Delivered != 0 || len(resp.Failures) != 0 {
		t.Errorf("post-unload: delivered=%d failures=%v, want 0/none", resp.Delivered, resp.Failures)
	}
	if _, ok := a.handler(pluginhost.EventRoute(manifest.ID, "app_lifecycle")); ok {
		t.Error("event route survived unload")
	}
}

// waitEventProbe polls a probe callable until it reports want (host→plugin
// event delivery is asynchronous: a bounded drain queue dispatches events on
// a drainer goroutine) or the deadline passes. It unmarshals the final probe
// response into out and reports whether want was observed.
func waitEventProbe(t testing.TB, probe HandlerFunc, out any, want string) bool {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		probeResp, err := probe(context.Background(), []byte(`{}`))
		if err == nil {
			_ = json.Unmarshal(probeResp, out)
			if probeResp != nil && strings.Contains(string(probeResp), want) {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(25 * time.Millisecond)
	}
}
