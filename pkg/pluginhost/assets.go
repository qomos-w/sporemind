package pluginhost

import (
	"bytes"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	sdk "github.com/qomos-w/sporemind-plugin-sdk"
)

// bridgeBootstrapSnippet is the SDK's single-source snippet: the gateway
// fallback route injects the exact same script the plugin process serves on
// its own listener (sdk.BridgeBootstrapSnippet), so the two serving paths
// speak the same bootstrap protocol by construction instead of by convention.
var bridgeBootstrapSnippet = sdk.BridgeBootstrapSnippet

// injectBridgeBootstrap inserts the bridge bootstrap snippet into an HTML
// document. It tries to place it before </head>, falling back to </body>,
// </html>, or appending at the end. Candidate closers are matched only when
// they are real markup closes: a </head> literal inside an HTML comment or a
// script block (e.g. a JS line comment) must not hijack the injection point —
// the snippet would land inside the comment and silently disable appBase
// injection, cookie minting, and the bridge client load.
func injectBridgeBootstrap(data []byte) []byte {
	lower := bytes.ToLower(data)
	for _, tag := range []string{"</head>", "</body>", "</html>"} {
		if idx := realCloseIndex(lower, tag); idx >= 0 {
			result := make([]byte, 0, len(data)+len(bridgeBootstrapSnippet))
			result = append(result, data[:idx]...)
			result = append(result, bridgeBootstrapSnippet...)
			result = append(result, data[idx:]...)
			return result
		}
	}
	return append(data, bridgeBootstrapSnippet...)
}

// realCloseIndex returns the offset of the first occurrence of tag that is not
// inside an HTML comment (<!-- -->) or a script block (<script ...>
// </script>). lower must be an already-lowercased copy of the document.
func realCloseIndex(lower []byte, tag string) int {
	t := []byte(tag)
	n := len(lower)
	for i := 0; i < n; i++ {
		if lower[i] != '<' {
			continue
		}
		rest := lower[i:]
		if bytes.HasPrefix(rest, []byte("<!--")) {
			end := bytes.Index(rest[4:], []byte("-->"))
			if end < 0 {
				return -1
			}
			i += 4 + end + 3 - 1
			continue
		}
		if bytes.HasPrefix(rest, []byte("<script")) {
			after := lower[i+7:]
			if len(after) == 0 || after[0] == '>' || after[0] == '/' || after[0] == ' ' || after[0] == '\t' {
				end := bytes.Index(rest, []byte("</script>"))
				if end < 0 {
					return -1
				}
				i += end + len("</script>") - 1
				continue
			}
		}
		if bytes.HasPrefix(rest, t) {
			return i
		}
	}
	return -1
}

// AssetsHandler returns an http.Handler that serves a plugin's asset bundle
// from an in-memory map. The handler receives request paths with the
// /plugin/{pluginID} prefix already stripped (see Router.ServeHTTP), so map
// keys are plain relative paths such as "index.html" or "css/app.css".
//
// Semantics:
//   - GET/HEAD only; other methods get 405.
//   - A request for "/" or any path ending in "/" serves "index.html" under
//     that directory when present.
//   - A directory path without a trailing slash also falls back to its
//     index.html so entrypoint routes like /dashboard resolve.
//   - Unknown paths return 404.
//   - Paths are cleaned and anchored at "/", so traversal segments cannot
//     escape the bundle.
//
// The returned handler is immutable and safe for concurrent use.
func AssetsHandler(assets map[string][]byte) http.Handler {
	files := make(map[string][]byte, len(assets))
	for p, data := range assets {
		if p == "" {
			continue
		}
		files[normalizeAssetPath(p)] = data
	}
	return &assetsHandler{files: files}
}

type assetsHandler struct {
	files map[string][]byte
}

func (h *assetsHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	name := normalizeAssetPath(req.URL.Path)
	if data, ok := h.files[name]; ok {
		serveAsset(w, req, name, data)
		return
	}
	// Directory fallback: /dir/ and /dir both resolve to /dir/index.html.
	if data, ok := h.files[path.Join(name, "index.html")]; ok {
		serveAsset(w, req, path.Join(name, "index.html"), data)
		return
	}
	http.NotFound(w, req)
}

// serveAsset writes one asset body. http.ServeContent derives the
// Content-Type from the file name's extension and handles HEAD and range
// requests; a zero moddate disables Last-Modified/caching headers.
// For HTML files, the bridge bootstrap snippet is injected so the host
// shell can load the bridge client via postMessage (cross-origin safe).
func serveAsset(w http.ResponseWriter, req *http.Request, name string, data []byte) {
	if isHTMLAsset(name) {
		data = injectBridgeBootstrap(data)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		// HTML is the entry document: the browser must revalidate it on
		// every navigation so a reload-swapped asset bundle is picked up
		// instead of a cached copy.
		w.Header().Set("Cache-Control", "no-store")
		if req.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write(data)
		return
	}
	http.ServeContent(w, req, path.Base(name), time.Time{}, bytes.NewReader(data))
}

// isHTMLAsset returns true for .html and .htm files.
func isHTMLAsset(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return ext == ".html" || ext == ".htm"
}

// normalizeAssetPath cleans an asset path and anchors it at "/". Because the
// input is prefixed with "/" before cleaning, ".." segments can only collapse
// back to the root, never escape the bundle.
func normalizeAssetPath(p string) string {
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}
