package pluginhost

import (
	"testing"
)

// TestHandleListPluginsReturnNotStructArray guards against reintroducing a
// top-level struct-array return on handleListPlugins. The gospore callable
// surface rejects such handlers at registration ("top-level arrays must
// contain scalar elements"), and the registration error is swallowed by
// `_ =` in OnStart — so the constraint only manifests as a silently missing
// callable in production (list_plugins "not registered", coverage gate
// seeing an empty registry). This test fails to compile if the return type
// regresses to []PluginDescriptor, forcing the wrapper shape.
func TestHandleListPluginsReturnNotStructArray(t *testing.T) {
	a := &Actor{Plugins: []PluginDescriptor{{ID: "x", Status: "active"}}}
	resp, err := a.handleListPlugins(nil, listPluginsReq{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Plugins) != 1 || resp.Plugins[0].ID != "x" {
		t.Fatalf("resp=%+v", resp)
	}
}
