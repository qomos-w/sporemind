package pluginhost

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRouter_RegisterAndServe(t *testing.T) {
	r := NewRouter()
	r.Register("com.example.hello", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("path=" + req.URL.Path))
	}))

	req := httptest.NewRequest(http.MethodGet, "/plugin/com.example.hello/index.html", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if got, want := string(body), "path=/index.html"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestRouter_Unregister(t *testing.T) {
	r := NewRouter()
	r.Register("com.example.hello", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/plugin/com.example.hello/", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("before unregister: status = %d, want 200", rec.Code)
	}

	r.Unregister("com.example.hello")

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("after unregister: status = %d, want 404", rec.Code)
	}
}

func TestRouter_NotFound(t *testing.T) {
	r := NewRouter()

	cases := []string{
		"/plugin/",
		"/plugin",
		"/other/com.example.hello/index.html",
		"/plugin/com.example.missing/index.html",
	}

	for _, path := range cases {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("path %q: status = %d, want 404", path, rec.Code)
		}
	}
}

func TestRouter_StaticFileServer(t *testing.T) {
	r := NewRouter()
	mux := http.NewServeMux()
	mux.HandleFunc("/assets/main.js", func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte("console.log('hello')"))
	})
	r.Register("com.example.hello", mux)

	req := httptest.NewRequest(http.MethodGet, "/plugin/com.example.hello/assets/main.js", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "console.log") {
		t.Fatalf("unexpected body: %q", string(body))
	}
}

func TestExtractPluginID(t *testing.T) {
	cases := []struct {
		path   string
		wantID string
		wantOK bool
	}{
		{"/plugin/com.example.hello/index.html", "com.example.hello", true},
		{"/plugin/com.example.hello/", "com.example.hello", true},
		{"/plugin/com.example.hello", "com.example.hello", true},
		{"/plugin/", "", false},
		{"/plugin", "", false},
		{"/other/com.example.hello/", "", false},
	}

	for _, tc := range cases {
		gotID, gotOK := extractPluginID(tc.path)
		if gotID != tc.wantID || gotOK != tc.wantOK {
			t.Errorf("extractPluginID(%q) = (%q, %v), want (%q, %v)",
				tc.path, gotID, gotOK, tc.wantID, tc.wantOK)
		}
	}
}
