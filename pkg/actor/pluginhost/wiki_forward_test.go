package pluginhost

import (
	"encoding/json"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestParseOnLoadProjectID(t *testing.T) {
	if got := parseOnLoadProjectID(nil); got != "" {
		t.Errorf("nil config = %q, want empty", got)
	}
	if got := parseOnLoadProjectID([]byte("not json")); got != "" {
		t.Errorf("unparseable config = %q, want empty", got)
	}
	if got := parseOnLoadProjectID([]byte(`{"projectId":" novelking "}`)); got != "novelking" {
		t.Errorf("projectId = %q, want trimmed mount name", got)
	}
	if got := parseOnLoadProjectID([]byte(`{"staticDir":"C:\\x"}`)); got != "" {
		t.Errorf("config without projectId = %q, want empty", got)
	}
}

// TestStoreDispatchRoutesWikiHostCalls pins the wiki.read host-call branch:
// project.wiki_* must reach the workspace forward (plugin-project bound via
// ArtifactLoads), never the filesystem service the "project" prefix alias
// otherwise selects (the "call ID not registered" bug).
func TestStoreDispatchRoutesWikiHostCalls(t *testing.T) {
	a := &Actor{ArtifactLoads: map[string]gen.PluginArtifactLoadReq{}}
	dispatch := a.buildStoreDispatch(nil)

	// Unbound plugin (no projectId in the load record): fail closed with the
	// binding error, not a filesystem-cell "not registered".
	_, err := dispatch("project.wiki_list_cards", []byte(`{"Plugin":"app.demo"}`))
	if err == nil || !strings.Contains(err.Error(), "not bound to a project") {
		t.Fatalf("unbound wiki call error = %v, want binding failure", err)
	}

	// Bound plugin without a workspace service ref: the forward must report
	// the workspace as unavailable (streamServiceRef resolves nil without an
	// actorCtx).
	cfg, merr := json.Marshal(map[string]string{"projectId": "novelking"})
	if merr != nil {
		t.Fatal(merr)
	}
	a.ArtifactLoads["app.demo"] = gen.PluginArtifactLoadReq{OnLoadConfig: cfg}
	_, err = dispatch("project.wiki_list_cards", []byte(`{"Plugin":"app.demo"}`))
	if err == nil || !strings.Contains(err.Error(), "workspace service not available") {
		t.Fatalf("bound wiki call error = %v, want workspace unavailable", err)
	}

	// A non-wiki project.* call keeps the old routing (filesystem alias) and
	// must NOT hit the wiki branch: with no loader it surfaces the loader
	// path, not a wiki error.
	a2 := &Actor{ArtifactLoads: map[string]gen.PluginArtifactLoadReq{}}
	d2 := a2.buildStoreDispatch(nil)
	_, err = d2("project.read_file", []byte(`{"Path":"x"}`))
	if err == nil || strings.Contains(err.Error(), "wiki") {
		t.Fatalf("project.read_file error = %v, want the non-wiki path", err)
	}
}

// TestHostBridgeWikiDispatchGating pins the bridge side: wiki.read host calls
// are capability-gated like every host call and, when granted, land on the
// store dispatch with the calling plugin's identity injected.
func TestHostBridgeWikiDispatchGating(t *testing.T) {
	// Denied without the wiki.read capability.
	b := NewHostBridge(nil, nil, "app.demo", nil)
	if b.Allows("project.wiki_list_cards") {
		t.Fatal("Allows(project.wiki_list_cards) must be false without wiki.read")
	}

	var gotCallID string
	var gotReq string
	b2 := NewHostBridge(
		map[string]struct{}{"wiki.read": {}},
		func(callID string, _ []byte) ([]byte, error) {
			t.Errorf("plain dispatch must not see wiki call %q", callID)
			return []byte("{}"), nil
		},
		"app.demo",
		func(callID string, req []byte) ([]byte, error) {
			gotCallID = callID
			gotReq = string(req)
			return []byte(`{}`), nil
		},
	)
	if !b2.Allows("project.wiki_list_cards") {
		t.Fatal("Allows(project.wiki_list_cards) must be true with wiki.read")
	}
	if _, err := b2.Dispatch("project.wiki_list_cards", []byte(`{"Flat":true}`)); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if gotCallID != "project.wiki_list_cards" {
		t.Fatalf("store dispatch callID = %q, want project.wiki_list_cards", gotCallID)
	}
	if !strings.Contains(gotReq, `"Plugin":"app.demo"`) {
		t.Fatalf("store dispatch req = %s, want Plugin identity injected", gotReq)
	}
}
