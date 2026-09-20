package appmanager

import (
	"encoding/json"
	"strings"
	"testing"

	pluginhostactor "github.com/qomos-w/sporemind/pkg/actor/pluginhost"
)

// bridgedHost adapts a pluginhost HostBridge into the plugin-side sdk.Host
// interface — the same adaptation the subprocess transport performs for
// reverse frames, so the wire bytes here match what a real plugin's generated
// typed caller produces.
type bridgedHost struct {
	b *pluginhostactor.HostBridge
}

func (h bridgedHost) Invoke(callID string, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return h.b.DispatchContext(pluginhostactor.DispatchContext{}, callID, raw)
}

func (h bridgedHost) InvokeStream(callID string, payload any, onChunk func([]byte) error) ([]byte, error) {
	return h.Invoke(callID, payload)
}

// TestCrossPluginTypedBundleCallE2E wires the three production layers of a
// bundle-shell cross-plugin call in-process: bundle permission expansion
// (appmanager) → capability set → HostBridge exact-callID gate + bundle
// dispatch (pluginhost) → sdk.Host Invoke (plugin side). The request/response
// shapes match what bundle_calls.gen.go marshals for the dependency's
// descriptor schemas.
func TestCrossPluginTypedBundleCallE2E(t *testing.T) {
	a := newSecurityTestActor()
	a.Records = bundlePermTestRecords()

	// The caller declares the whole Translate bundle, not individual callIDs.
	manifest := bundlePermTestManifest([]string{"plugin.app.translator.translate"})
	expanded, err := expandBundlePermissions(manifest, a.Records)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if got, want := strings.Join(expanded, ","), "plugin.app.translator.translate_detect,plugin.app.translator.translate_run"; got != want {
		t.Fatalf("expanded = %q, want %q", got, want)
	}

	// Production authorization shape: the expanded permission list IS the
	// granted set the loader builds from the manifest (manifestCapabilitySet).
	allowed := make(map[string]struct{}, len(expanded))
	for _, p := range expanded {
		allowed[p] = struct{}{}
	}
	bridge := pluginhostactor.NewHostBridge(allowed, nil, "app.caller", nil)

	// The target plugin decodes the same wire shape its own generated structs
	// define for TranslateRunReq/TranslateRunResp.
	var gotCallID string
	bridge.SetBundleDispatch(func(callID string, req []byte) ([]byte, error) {
		gotCallID = callID
		var in struct {
			Text string `json:"Text"`
		}
		if err := json.Unmarshal(req, &in); err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			Text string `json:"Text"`
		}{Text: strings.ToUpper(in.Text)})
	})

	// Plugin side: the generated typed caller is host.Invoke(callID, typedReq).
	host := bridgedHost{b: bridge}
	raw, err := host.Invoke("plugin.app.translator.translate_run", map[string]string{"Text": "hello"})
	if err != nil {
		t.Fatalf("typed bundle call: %v", err)
	}
	if gotCallID != "plugin.app.translator.translate_run" {
		t.Errorf("dispatch saw callID %q", gotCallID)
	}
	var out struct {
		Text string `json:"Text"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Text != "HELLO" {
		t.Fatalf("response = %s err=%v, want HELLO", raw, err)
	}

	// Second bundle member is granted by the same expansion.
	if _, err := host.Invoke("plugin.app.translator.translate_detect", map[string]string{"Text": "x"}); err != nil {
		t.Errorf("sibling bundle callable denied: %v", err)
	}

	// A callable of the dependency that is NOT in the granted bundle stays
	// denied — expansion widens only to the bundle's own tools.
	if _, err := host.Invoke("plugin.app.translator.summarize", nil); err == nil {
		t.Error("non-bundle callable must be denied by the exact-callID gate")
	}

	// And another app's plugin.* space is entirely out of scope.
	if _, err := host.Invoke("plugin.app.other.anything", nil); err == nil {
		t.Error("cross-app plugin callID must be denied")
	}
}
