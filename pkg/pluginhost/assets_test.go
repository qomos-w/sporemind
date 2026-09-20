package pluginhost

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAssetsHandlerServesDeclaredPaths(t *testing.T) {
	h := AssetsHandler(map[string][]byte{
		"index.html":  []byte("<html>hello</html>"),
		"css/app.css": []byte("body{margin:0}"),
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/index.html", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /index.html: code = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); !strings.Contains(got, "<html>hello") {
		t.Fatalf("GET /index.html: body = %q, should contain original HTML", got)
	}
	if !strings.Contains(rec.Body.String(), "sporemind:ready-for-bootstrap") {
		t.Fatalf("GET /index.html: body should contain bridge bootstrap snippet")
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("GET /index.html: content-type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("GET /index.html: cache-control = %q, want no-store", cc)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/css/app.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /css/app.css: code = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/css; charset=utf-8" {
		t.Fatalf("GET /css/app.css: content-type = %q", ct)
	}
}

func TestAssetsHandlerRootServesIndex(t *testing.T) {
	h := AssetsHandler(map[string][]byte{"index.html": []byte("root")})

	for _, p := range []string{"/", "/index.html"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "root") {
			t.Fatalf("GET %s: code = %d body = %q", p, rec.Code, rec.Body.String())
		}
	}
}

func TestAssetsHandlerDirectoryFallsBackToIndex(t *testing.T) {
	h := AssetsHandler(map[string][]byte{"dashboard/index.html": []byte("dash")})

	for _, p := range []string{"/dashboard", "/dashboard/"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "dash") {
			t.Fatalf("GET %s: code = %d body = %q", p, rec.Code, rec.Body.String())
		}
	}
}

func TestAssetsHandlerUnknownPath404(t *testing.T) {
	h := AssetsHandler(map[string][]byte{"index.html": []byte("root")})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/missing.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /missing.js: code = %d, want 404", rec.Code)
	}
}

func TestAssetsHandlerRejectsNonGet(t *testing.T) {
	h := AssetsHandler(map[string][]byte{"index.html": []byte("root")})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/index.html", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /index.html: code = %d, want 405", rec.Code)
	}
}

func TestAssetsHandlerHeadIsEmptyBody(t *testing.T) {
	h := AssetsHandler(map[string][]byte{"index.html": []byte("<html>root</html>")})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/index.html", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD /index.html: code = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD /index.html: body = %q, want empty", rec.Body.String())
	}
}

func TestAssetsHandlerNormalizesLeadingSlashKeys(t *testing.T) {
	// Bundles assembled elsewhere may key assets with a leading slash; the
	// handler must still serve them from slash-less request paths.
	h := AssetsHandler(map[string][]byte{"/index.html": []byte("root")})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/index.html", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "root") {
		t.Fatalf("GET /index.html: code = %d body = %q", rec.Code, rec.Body.String())
	}
}

func TestAssetsHandlerRouterIntegrationServesAfterRegisterAnd404AfterUnregister(t *testing.T) {
	router := NewRouter()

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/plugin/com.example.test/index.html", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("before register: code = %d, want 404", rec.Code)
	}

	router.Register("com.example.test", AssetsHandler(map[string][]byte{"index.html": []byte("<html>app</html>")}))

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/plugin/com.example.test/index.html", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<html>app") {
		t.Fatalf("after register: code = %d body = %q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/plugin/com.example.test/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("after register root: code = %d", rec.Code)
	}

	router.Unregister("com.example.test")

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/plugin/com.example.test/index.html", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("after unregister: code = %d, want 404", rec.Code)
	}
}
