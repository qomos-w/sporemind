package sdk

import (
	"os"
	"strings"
	"testing"
)

// extractInjectSnippet pulls the evaluated value of BridgeBootstrapSnippet out
// of http_server.go (backtick-quoted segments before the injector func).
func extractInjectSnippet(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("http_server.go")
	if err != nil {
		t.Fatalf("read http_server.go: %v", err)
	}
	src := string(data)
	start := strings.Index(src, "const BridgeBootstrapSnippet =")
	if start < 0 {
		t.Fatal("BridgeBootstrapSnippet const not found")
	}
	rest := src[start:]
	end := strings.Index(rest, "func injectBridgeBootstrap")
	if end < 0 {
		t.Fatal("injectBridgeBootstrap func not found after the const")
	}
	body := rest[:end]
	var b strings.Builder
	for {
		i := strings.Index(body, "`")
		if i < 0 {
			break
		}
		seg := body[i+1:]
		j := strings.Index(seg, "`")
		if j < 0 {
			t.Fatal("unbalanced backtick in BridgeBootstrapSnippet")
		}
		b.WriteString(seg[:j])
		body = seg[j+1:]
	}
	return b.String()
}

// TestInjectBridgeBootstrapHijackResistance guards the injection point against
// literal closers inside HTML comments and script blocks: the snippet must
// land immediately before the first REAL markup close. This mirrors the
// gateway copy's test in pkg/pluginhost/assets_bridge_test.go — the two
// injectors must stay behavior-identical.
func TestInjectBridgeBootstrapHijackResistance(t *testing.T) {
	snippet := extractInjectSnippet(t)
	cases := []struct {
		name   string
		html   string
		closer string
	}{
		{
			name:   "js line comment with </head> literal",
			html:   "<html><head><script>// (injected at </head>)\n</script></head><body></body></html>",
			closer: "</head>",
		},
		{
			name:   "html comment with </head> literal",
			html:   "<html><head><!-- </head> --></head><body></body></html>",
			closer: "</head>",
		},
		{
			name:   "commented head, real body",
			html:   "<html><!-- </head> --><body></body></html>",
			closer: "</body>",
		},
		{
			name:   "normal document",
			html:   "<html><head></head><body></body></html>",
			closer: "</head>",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := string(injectBridgeBootstrap([]byte(tc.html)))
			idx := strings.Index(result, snippet)
			if idx < 0 {
				t.Fatalf("snippet missing in result:\n%s", result)
			}
			if !strings.HasPrefix(result[idx+len(snippet):], tc.closer) {
				t.Fatalf("snippet not spliced before real %s:\n%s", tc.closer, result)
			}
			origIdx := realCloseIndex([]byte(strings.ToLower(tc.html)), strings.ToLower(tc.closer))
			if origIdx < 0 || idx != origIdx {
				t.Fatalf("snippet offset %d != real closer offset %d:\n%s", idx, origIdx, result)
			}
		})
	}
}

// TestBridgeBootstrapSnippetIdempotentGuard pins the dedup marker: the
// snippet can be injected twice into the same document (gateway fallback +
// plugin listener copies) and the host may answer ready-for-bootstrap more
// than once, but the bridge client is a classic script whose top-level
// declarations throw SyntaxError on a second evaluation. The append must be
// guarded by the data-sporemind-bridge marker.
func TestBridgeBootstrapSnippetIdempotentGuard(t *testing.T) {
	snippet := extractInjectSnippet(t)
	if !strings.Contains(snippet, `querySelector("script[data-sporemind-bridge]")`) {
		t.Error("snippet must check the data-sporemind-bridge marker before appending the bridge client")
	}
	if !strings.Contains(snippet, `setAttribute("data-sporemind-bridge","1")`) {
		t.Error("snippet must tag the appended bridge script with data-sporemind-bridge")
	}
}

// TestBridgeBootstrapSnippetReadyRetry pins the ready-for-bootstrap retry
// protocol: a one-shot announcement is lost when the host's listener is not
// yet mounted, and after a host restart the shell's session bind can exceed
// the 3s "" fallback — the panel would boot on an empty appBase (405/404 at
// the gateway) with no retry path. The announcement must retry on a bounded
// cadence and stop once a bootstrap arrives.
func TestBridgeBootstrapSnippetReadyRetry(t *testing.T) {
	snippet := extractInjectSnippet(t)
	// The retry loop with its stop flag, 400ms cadence and 15s bound.
	if !strings.Contains(snippet, "var __sporemindBooted=false") {
		t.Error("snippet must declare the __sporemindBooted stop flag")
	}
	if !strings.Contains(snippet, `__sporemindBooted=true;`) {
		t.Error("receiving sporemind:bootstrap must set the stop flag")
	}
	if !strings.Contains(snippet, "(function __sporemindAnnounceReady(){") {
		t.Error("snippet must announce via the retrying __sporemindAnnounceReady loop")
	}
	if !strings.Contains(snippet, "if(__sporemindBooted)return;") {
		t.Error("the retry loop must stop once a bootstrap arrived")
	}
	if !strings.Contains(snippet, "setTimeout(__sporemindAnnounceReady,400)") {
		t.Error("the retry loop must reschedule at the 400ms cadence")
	}
	if !strings.Contains(snippet, "__sporemindReadyStart>15000") {
		t.Error("the retry loop must be bounded by the 15s deadline")
	}
	// The first announcement is immediate (the loop body posts before
	// rescheduling) and the 3s "" fallback resolve must survive for
	// standalone file:// opens.
	if !strings.Contains(snippet, `window.parent.postMessage({type:"sporemind:ready-for-bootstrap"},"*");`) {
		t.Error("the loop must post the ready-for-bootstrap message itself")
	}
	if !strings.Contains(snippet, `setTimeout(function(){window.__sporemindResolveAppBase("")},3000)`) {
		t.Error("the 3s standalone '' fallback must be preserved")
	}
}

// TestBridgeBootstrapSnippetVoiceNativeMarker pins the native-voice
// capability handoff: when the host's bootstrap payload carries
// voiceNative=true (Capacitor mobile shell, where the host relays voice:*
// postMessages between the panel and the native shell), the snippet must
// expose window.__sporemindVoiceNative=true so panel-side voice drivers can
// pick the native bridge without probing the parent themselves.
func TestBridgeBootstrapSnippetVoiceNativeMarker(t *testing.T) {
	snippet := extractInjectSnippet(t)
	if !strings.Contains(snippet, `if(e.data.voiceNative)window.__sporemindVoiceNative=true;`) {
		t.Error("snippet must set window.__sporemindVoiceNative when the host advertises voiceNative")
	}
}

// TestBridgeBootstrapSnippetLocalePinning pins the host-locale handoff: the
// bootstrap payload carries the host's active locale, and live changes arrive
// as sporemind:locale-update. Both must land on <html lang> so panels can
// localize off a standard BCP47 attribute (window.sporemind.locale reads it,
// MutationObserver on lang tracks changes).
func TestBridgeBootstrapSnippetLocalePinning(t *testing.T) {
	snippet := extractInjectSnippet(t)
	if !strings.Contains(snippet, `if(e.data.locale)document.documentElement.setAttribute("lang",e.data.locale);`) {
		t.Error("bootstrap handling must apply e.data.locale to <html lang>")
	}
	if !strings.Contains(snippet, `e.data.type==="sporemind:locale-update"`) {
		t.Error("snippet must handle sporemind:locale-update")
	}
	if !strings.Contains(snippet, `document.documentElement.setAttribute("lang",e.data.locale)`) {
		t.Error("locale-update handling must rewrite <html lang>")
	}
}

// TestBridgeBootstrapSnippetEventSuspend pins the panel-activity contract:
// the host posts sporemind:event-suspend / sporemind:event-resume when a
// keepAlive panel tab becomes hidden/visible, and the snippet must forward
// them to every window-level event channel (created by the bridge client or
// the generated client) so hidden panels stop holding event connections —
// the structural half of the 2026-09-14 pool-starvation invariant.
func TestBridgeBootstrapSnippetEventSuspend(t *testing.T) {
	snippet := extractInjectSnippet(t)
	for _, want := range []string{
		`sporemind:event-suspend`,
		`sporemind:event-resume`,
		`window.__sporemindEventChannels`,
		`typeof ch[m]==="function"`,
	} {
		if !strings.Contains(snippet, want) {
			t.Errorf("snippet missing event-suspend marker %q", want)
		}
	}
}

// TestBridgeBootstrapSnippetPointerCaptureAssist pins the pointer-release
// normalization: plugin panels live in cross-origin iframes, and a press that
// releases outside the frame never delivers mouseup to the panel document —
// naive drag/slider/selection logic hangs and click is swallowed. The snippet
// must auto-capture the press target in the CAPTURE phase (so a plugin's own
// setPointerCapture wins as last-write) and skip editable/native-control
// targets whose press semantics the browser already manages.
func TestBridgeBootstrapSnippetPointerCaptureAssist(t *testing.T) {
	snippet := extractInjectSnippet(t)
	for _, want := range []string{
		`document.addEventListener("pointerdown",function(e){`,
		`if(e.pointerType!=="mouse"&&e.pointerType!=="pen")return;`,
		`try{t.setPointerCapture(e.pointerId)}catch(err){}`,
		`input,textarea,select,[contenteditable],iframe`,
		`},true);`,
	} {
		if !strings.Contains(snippet, want) {
			t.Errorf("snippet missing pointer-capture assist marker %q", want)
		}
	}
}

// TestBridgeBootstrapSnippetFileDrop pins the OS file-drop contract of the
// snippet: unclaimed file drops (drag carries Files, target outside a
// plugin-managed [data-file-drop-target] zone) are claimed at document
// capture so the iframe never navigates to the dropped file; the expanded
// tree is announced as a cancelable sporemind:file-drop CustomEvent (a
// preventDefault skips the backend upload), and the backend delivery uses
// the /__sdk/file-drop begin → raw-body upload → end protocol against the
// plugin's own listener.
func TestBridgeBootstrapSnippetFileDrop(t *testing.T) {
	snippet := extractInjectSnippet(t)
	for _, want := range []string{
		`document.addEventListener("dragover",function(e){if(__sporemindFileDrag(e)){e.preventDefault();try{e.dataTransfer.dropEffect="copy"}catch(err){}}},true);`,
		`document.addEventListener("drop",function(e){if(!__sporemindFileDrag(e))return;e.preventDefault();`,
		`Array.prototype.indexOf.call(e.dataTransfer.types,"Files")>=0){__sporemindHandleDrop(e);return}`,
		`__sporemindHandleHostDrop(e)},true);`,
		`!has("Files")&&!has("text/plain")&&!has("application/x-sporemind-file-local")&&!has("application/x-sporemind-file-remote")`,
		`tg.closest("[data-file-drop-target]")`,
		`new CustomEvent("sporemind:file-drop",{cancelable:true,detail:{entries:entries}})`,
		`detail:{origin:"host-browser",entries:entries}`,
		`if(ev.defaultPrevented)return;`,
		`it.webkitGetAsEntry?it.webkitGetAsEntry():null`,
		`entry.createReader()`,
		`/__sdk/file-drop/"+id+"/begin"`,
		`/__sdk/file-drop/"+id+"/"+idx`,
		`/__sdk/file-drop/"+id+"/end"`,
		`/__sdk/file-drop/host"`,
		`var mark="x-sporemind-dnd:";`,
		`window.__sporemindAppBaseReady.then(function(base){`,
	} {
		if !strings.Contains(snippet, want) {
			t.Errorf("snippet missing file-drop marker %q", want)
		}
	}
}
