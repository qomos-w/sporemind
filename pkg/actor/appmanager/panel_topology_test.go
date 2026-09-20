package appmanager

import (
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestPanelTopologyReportsGatewayAndAuthSemantics pins the read-only topology
// diagnostic: gateway base derives from config, the panel URL carries the view
// route and generation, and the 401-vs-bug guidance is present.
func TestPanelTopologyReportsGatewayAndAuthSemantics(t *testing.T) {
	// GatewayAddr() falls back to the machine's global config, whose LAN
	// bind form ":18080" has no host — pin the env so the assertion below is
	// deterministic instead of environment-dependent.
	t.Setenv("SPOREMIND_GATEWAY_ADDR", "127.0.0.1:18080")
	manifest := gen.AppManifest{
		ID: "app.topo", Runtime: "native", Version: "1.0.0", Namespace: "topo",
		Entrypoints: []gen.AppEntrypoint{{Kind: "view", ID: "main", Route: "index.html"}},
	}
	a := &Actor{
		Records: map[string]appRecord{manifest.ID: {
			Manifest: manifest, State: stateRunning, Generation: 4,
			BackendUrl: "http://127.0.0.1:62393", SessionSecret: "abc",
		}},
	}
	resp, err := a.handlePanelTopology(nil, gen.AppManagerPanelTopologyReq{ID: "app.topo"})
	if err != nil {
		t.Fatalf("handlePanelTopology: %v", err)
	}
	if resp.GatewayBase == "" || !strings.Contains(resp.GatewayBase, "127.0.0.1:") {
		t.Errorf("GatewayBase missing: %+v", resp)
	}
	if resp.PanelURL != resp.GatewayBase+"/plugin/app.topo/index.html?v=4" {
		t.Errorf("PanelURL wrong: %q", resp.PanelURL)
	}
	if resp.PluginListener != "http://127.0.0.1:62393" {
		t.Errorf("PluginListener wrong: %q", resp.PluginListener)
	}
	joined := strings.Join(resp.AuthNotes, " ")
	if !strings.Contains(joined, "auth WORKING") {
		t.Errorf("auth semantics note missing: %v", resp.AuthNotes)
	}
	if !strings.Contains(strings.Join(resp.ExpectedCodes, " "), "401") {
		t.Errorf("expected-code cheat sheet missing 401: %v", resp.ExpectedCodes)
	}
}

// TestPanelTopologyUnknownApp verifies the not-registered error path.
func TestPanelTopologyUnknownApp(t *testing.T) {
	a := &Actor{Records: map[string]appRecord{}}
	if _, err := a.handlePanelTopology(nil, gen.AppManagerPanelTopologyReq{ID: "app.missing"}); err == nil {
		t.Fatal("expected error for unregistered app")
	}
}
