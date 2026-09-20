package websearch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestHandleFetch_Basic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html>
<html lang="en">
<head>
<meta name="description" content="A test page">
<meta name="site_name" content="TestSite">
<title>Test Page Title</title>
<style>body { color: red; }</style>
<script>alert('hi');</script>
</head>
<body>
<nav>Navigation</nav>
<header>Header</header>
<main>
<h1>Hello World</h1>
<p>This is a test paragraph with some content.</p>
<p>Another paragraph.</p>
</main>
<footer>Footer</footer>
<aside>Sidebar</aside>
</body>
</html>`)
	}))
	defer server.Close()

	a := newTestActor(t)
	a.lifecycleCtx = context.Background()

	resp, err := a.handleFetch(nil, gen.WebFetchReq{URL: server.URL})
	if err != nil {
		t.Fatalf("handleFetch: %v", err)
	}

	if resp.URL != server.URL {
		t.Errorf("URL = %q, want %q", resp.URL, server.URL)
	}
	if resp.Title != "Test Page Title" {
		t.Errorf("Title = %q, want %q", resp.Title, "Test Page Title")
	}
	if resp.Meta.Description != "A test page" {
		t.Errorf("Description = %q, want %q", resp.Meta.Description, "A test page")
	}
	if resp.Meta.SiteName != "TestSite" {
		t.Errorf("SiteName = %q, want %q", resp.Meta.SiteName, "TestSite")
	}
	if resp.Meta.Lang != "en" {
		t.Errorf("Lang = %q, want %q", resp.Meta.Lang, "en")
	}
	if resp.Truncated {
		t.Error("Truncated should be false")
	}

	// Verify non-content elements were removed.
	text := resp.Text
	if strings.Contains(text, "Navigation") {
		t.Error("text should not contain nav content")
	}
	if strings.Contains(text, "Header") {
		t.Error("text should not contain header content")
	}
	if strings.Contains(text, "Footer") {
		t.Error("text should not contain footer content")
	}
	if strings.Contains(text, "Sidebar") {
		t.Error("text should not contain aside content")
	}
	if strings.Contains(text, "alert") {
		t.Error("text should not contain script content")
	}

	// Verify content elements are present.
	if !strings.Contains(text, "Hello World") {
		t.Errorf("text should contain heading, got: %s", text)
	}
	if !strings.Contains(text, "test paragraph") {
		t.Errorf("text should contain paragraph content, got: %s", text)
	}
}

func TestHandleFetch_EmptyURL(t *testing.T) {
	a := newTestActor(t)
	a.lifecycleCtx = context.Background()

	_, err := a.handleFetch(nil, gen.WebFetchReq{})
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
	if !strings.Contains(err.Error(), "url is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHandleFetch_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "Not Found")
	}))
	defer server.Close()

	a := newTestActor(t)
	a.lifecycleCtx = context.Background()

	_, err := a.handleFetch(nil, gen.WebFetchReq{URL: server.URL})
	if err == nil {
		t.Fatal("expected error for HTTP 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHandleFetch_MaxCharsTruncation(t *testing.T) {
	longText := strings.Repeat("abcdefghij", 2000) // 20000 chars
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><title>T</title></head><body><p>%s</p></body></html>`, longText)
	}))
	defer server.Close()

	a := newTestActor(t)
	a.lifecycleCtx = context.Background()

	resp, err := a.handleFetch(nil, gen.WebFetchReq{URL: server.URL, MaxChars: 100})
	if err != nil {
		t.Fatalf("handleFetch: %v", err)
	}
	if !resp.Truncated {
		t.Error("Truncated should be true when text exceeds MaxChars")
	}
	if len([]rune(resp.Text)) > 100 {
		t.Errorf("text length = %d, want <= 100", len([]rune(resp.Text)))
	}
}

func TestHandleFetch_DefaultMaxChars(t *testing.T) {
	// Generate text that exceeds the default 10000 char limit.
	longText := strings.Repeat("x", 15000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><body><p>%s</p></body></html>`, longText)
	}))
	defer server.Close()

	a := newTestActor(t)
	a.lifecycleCtx = context.Background()

	resp, err := a.handleFetch(nil, gen.WebFetchReq{URL: server.URL})
	if err != nil {
		t.Fatalf("handleFetch: %v", err)
	}
	if !resp.Truncated {
		t.Error("Truncated should be true when text exceeds default MaxChars")
	}
	if len([]rune(resp.Text)) > defaultMaxChars {
		t.Errorf("text length = %d, want <= %d", len([]rune(resp.Text)), defaultMaxChars)
	}
}

func TestHandleFetch_MetaOgFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html lang="fr">
<head>
<meta property="og:description" content="OG description">
<meta property="og:site_name" content="OG Site">
<title>OG Page</title>
</head>
<body><p>Content</p></body>
</html>`)
	}))
	defer server.Close()

	a := newTestActor(t)
	a.lifecycleCtx = context.Background()

	resp, err := a.handleFetch(nil, gen.WebFetchReq{URL: server.URL})
	if err != nil {
		t.Fatalf("handleFetch: %v", err)
	}
	if resp.Meta.Description != "OG description" {
		t.Errorf("Description = %q, want %q", resp.Meta.Description, "OG description")
	}
	if resp.Meta.SiteName != "OG Site" {
		t.Errorf("SiteName = %q, want %q", resp.Meta.SiteName, "OG Site")
	}
	if resp.Meta.Lang != "fr" {
		t.Errorf("Lang = %q, want %q", resp.Meta.Lang, "fr")
	}
}

func TestHandleFetch_NoLangAttribute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>No Lang</title></head><body><p>Content</p></body></html>`)
	}))
	defer server.Close()

	a := newTestActor(t)
	a.lifecycleCtx = context.Background()

	resp, err := a.handleFetch(nil, gen.WebFetchReq{URL: server.URL})
	if err != nil {
		t.Fatalf("handleFetch: %v", err)
	}
	if resp.Meta.Lang != "" {
		t.Errorf("Lang = %q, want empty", resp.Meta.Lang)
	}
}

func TestHandleFetch_ScriptStyleRemoved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>
<p>Visible</p>
<script>var x = document.createElement('div');</script>
<style>.hidden { display: none; }</style>
<noscript>JavaScript is disabled</noscript>
</body></html>`)
	}))
	defer server.Close()

	a := newTestActor(t)
	a.lifecycleCtx = context.Background()

	resp, err := a.handleFetch(nil, gen.WebFetchReq{URL: server.URL})
	if err != nil {
		t.Fatalf("handleFetch: %v", err)
	}
	if strings.Contains(resp.Text, "createElement") {
		t.Error("text should not contain script content")
	}
	if strings.Contains(resp.Text, "display: none") {
		t.Error("text should not contain style content")
	}
	if !strings.Contains(resp.Text, "Visible") {
		t.Error("text should contain visible paragraph")
	}
}

func TestHandleFetch_HTTPError500(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "Internal Server Error")
	}))
	defer server.Close()

	a := newTestActor(t)
	a.lifecycleCtx = context.Background()

	_, err := a.handleFetch(nil, gen.WebFetchReq{URL: server.URL})
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHandleFetch_NonHTMLContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		fmt.Fprint(w, `{"key": "value", "num": 42}`)
	}))
	defer server.Close()

	a := newTestActor(t)
	a.lifecycleCtx = context.Background()

	resp, err := a.handleFetch(nil, gen.WebFetchReq{URL: server.URL})
	if err != nil {
		t.Fatalf("handleFetch: %v", err)
	}
	if resp.Text != "" {
		t.Errorf("Text should be empty for non-HTML content, got: %q", resp.Text)
	}
	if resp.Title != "" {
		t.Errorf("Title should be empty for non-HTML content, got: %q", resp.Title)
	}
}

func TestHandleFetch_RedirectFollow(t *testing.T) {
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html lang="de"><head><title>Redirect Target</title></head><body><p>Landed here</p></body></html>`)
	}))
	defer targetServer.Close()

	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetServer.URL+"/final", http.StatusFound)
	}))
	defer redirectServer.Close()

	a := newTestActor(t)
	a.lifecycleCtx = context.Background()

	resp, err := a.handleFetch(nil, gen.WebFetchReq{URL: redirectServer.URL})
	if err != nil {
		t.Fatalf("handleFetch: %v", err)
	}
	if resp.Title != "Redirect Target" {
		t.Errorf("Title = %q, want %q", resp.Title, "Redirect Target")
	}
	if !strings.Contains(resp.Text, "Landed here") {
		t.Errorf("text should contain redirect target content, got: %q", resp.Text)
	}
	if resp.Meta.Lang != "de" {
		t.Errorf("Lang = %q, want %q", resp.Meta.Lang, "de")
	}
}

func TestHandleFetch_BodyTruncation(t *testing.T) {
	// Generate body slightly larger than maxBodyBytes to trigger body-level truncation.
	body := strings.Repeat("A", int(maxBodyBytes)+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, body)
	}))
	defer server.Close()

	a := newTestActor(t)
	a.lifecycleCtx = context.Background()

	resp, err := a.handleFetch(nil, gen.WebFetchReq{URL: server.URL})
	if err != nil {
		t.Fatalf("handleFetch: %v", err)
	}
	if !resp.Truncated {
		t.Error("Truncated should be true when body exceeds 2MB")
	}
}

func TestHandleFetch_InvalidURL(t *testing.T) {
	a := newTestActor(t)
	a.lifecycleCtx = context.Background()

	_, err := a.handleFetch(nil, gen.WebFetchReq{URL: "not-a-url"})
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}
