package pluginhost

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/qomos-w/sporemind-plugin-sdk"
)

// TestBridgeSnippetIsSingleSource pins the single-source wiring: the gateway
// fallback injects the SDK's exported snippet verbatim, and the snippet
// carries no legacy direct-HTTP branches (backendUrl / cookieToken /
// /session/bootstrap) — the data path is gateway-relative via appBase.
func TestBridgeSnippetIsSingleSource(t *testing.T) {
	if bridgeBootstrapSnippet != sdk.BridgeBootstrapSnippet {
		t.Fatalf("gateway snippet must be the SDK const verbatim")
	}
	for _, banned := range []string{"backendUrl", "cookieToken", "session/bootstrap"} {
		if strings.Contains(bridgeBootstrapSnippet, banned) {
			t.Fatalf("snippet still carries legacy branch %q:\n%s", banned, bridgeBootstrapSnippet)
		}
	}
	if !strings.Contains(bridgeBootstrapSnippet, "window.__sporemindAppBase=e.data.appBase") {
		t.Fatalf("snippet missing the __sporemindAppBase write: %q", bridgeBootstrapSnippet)
	}
	// Handshake gate: the ready promise is created at snippet load with a
	// timeout fallback and resolved when the bootstrap message carries the
	// mount base — generated clients await it so the panel's first data
	// fetch cannot 404 at the gateway root.
	for _, want := range []string{
		"window.__sporemindAppBaseReady=new Promise",
		`setTimeout(function(){window.__sporemindResolveAppBase("")},3000)`,
		`window.__sporemindResolveAppBase(e.data.appBase);`,
		// Pointer-release normalization: gateway-served panels are the same
		// cross-origin iframes, so the fallback path must carry the assist too.
		`document.addEventListener("pointerdown",function(e){`,
		`try{t.setPointerCapture(e.pointerId)}catch(err){}`,
	} {
		if !strings.Contains(bridgeBootstrapSnippet, want) {
			t.Fatalf("snippet missing ready-gate wiring %q:\n%s", want, bridgeBootstrapSnippet)
		}
	}
}

func TestServeAssetInjectsBridgeSnippet(t *testing.T) {
	h := AssetsHandler(map[string][]byte{
		"index.html": []byte("<html><head><title>Test</title></head><body>hello</body></html>"),
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/index.html", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "sporemind:ready-for-bootstrap") {
		t.Fatalf("HTML body missing bridge bootstrap snippet; got:\n%s", body)
	}
	if !strings.Contains(body, "<html>") {
		t.Fatalf("HTML body should still contain original content")
	}
}

func TestServeAssetInjectsBridgeSnippetNoHead(t *testing.T) {
	h := AssetsHandler(map[string][]byte{
		"page.html": []byte("<html><body>no head</body></html>"),
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/page.html", nil))

	body := rec.Body.String()
	if !strings.Contains(body, "sporemind:ready-for-bootstrap") {
		t.Fatalf("HTML body missing bridge snippet; got:\n%s", body)
	}
}

func TestServeAssetDoesNotInjectIntoNonHTML(t *testing.T) {
	h := AssetsHandler(map[string][]byte{
		"app.js":  []byte("console.log('hello')"),
		"style.css": []byte("body{margin:0}"),
	})

	for _, p := range []string{"/app.js", "/style.css"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		body := rec.Body.String()
		if strings.Contains(body, "sporemind:ready-for-bootstrap") {
			t.Fatalf("%s: non-HTML asset should not contain bridge snippet; got:\n%s", p, body)
		}
	}
}

func TestServeAssetHTMLHeadOnly(t *testing.T) {
	rec := httptest.NewRecorder()
	serveAsset(rec, httptest.NewRequest(http.MethodHead, "/index.html", nil), "index.html", []byte("<html><head></head><body>x</body></html>"))
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD code = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD should have empty body, got %d bytes", rec.Body.Len())
	}
}

func TestInjectBridgeBootstrapPlacement(t *testing.T) {
	cases := []struct {
		name string
		html string
		want string
	}{
		{"with head", "<html><head></head><body></body></html>", "</head>"},
		{"with body no head", "<html><body></body></html>", "</body>"},
		{"with html only", "<html></html>", "</html>"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := injectBridgeBootstrap([]byte(tc.html))
			if !strings.Contains(string(result), "sporemind:ready-for-bootstrap") {
				t.Fatalf("missing snippet in result:\n%s", string(result))
			}
		})
	}
}

// TestInjectBridgeBootstrapHijackResistance guards the injection point against
// literal closers inside HTML comments and script blocks: the snippet must
// land immediately before the first REAL markup close, never inside a comment
// or script (a hijacked injection silently disables the bridge bootstrap).
func TestInjectBridgeBootstrapHijackResistance(t *testing.T) {
	snippet := bridgeBootstrapSnippet
	cases := []struct {
		name   string
		html   string
		closer string // snippet must be spliced directly before this
	}{
		{
			name:   "js line comment with </head> literal",
			html:   "<html><head><script>// (injected at </head>)\n</script></head><body></body></html>",
			closer: "</head>",
		},
		{
			name:   "js string with </head> literal",
			html:   `<html><head><script>var s="</head>";</script></head><body></body></html>`,
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
			name:   "case-insensitive real close",
			html:   "<html><head></HEAD><body></body></html>",
			closer: "</HEAD>",
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
			// Exact invariant: the snippet must be inserted at the offset of
			// the first real closer in the original document.
			origIdx := realCloseIndex([]byte(strings.ToLower(tc.html)), strings.ToLower(tc.closer))
			if origIdx < 0 || idx != origIdx {
				t.Fatalf("snippet offset %d != real closer offset %d:\n%s", idx, origIdx, result)
			}
		})
	}
}

func TestIsHTMLAsset(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"index.html", true},
		{"page.HTM", true},
		{"app.js", false},
		{"style.css", false},
		{"icon.png", false},
		{"data.json", false},
	}
	for _, tc := range cases {
		if got := isHTMLAsset(tc.name); got != tc.want {
			t.Errorf("isHTMLAsset(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}