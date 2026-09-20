package cookiebridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

const testToken = "test-pairing-token-0123456789abcdef"

type fakeImporter struct {
	mu         sync.Mutex
	resolvedTo string
	resolveErr error
	importErr  error
	importedID string
	imported   map[string][]gen.BrowserCookieEntry
}

func (f *fakeImporter) ResolveInstance(_ context.Context, idOrName string) (string, error) {
	if f.resolveErr != nil {
		return "", f.resolveErr
	}
	if f.resolvedTo != "" {
		return f.resolvedTo, nil
	}
	return idOrName, nil
}

func (f *fakeImporter) Import(_ context.Context, instanceID string, cookies map[string][]gen.BrowserCookieEntry) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.importErr != nil {
		return 0, f.importErr
	}
	f.importedID = instanceID
	f.imported = cookies
	var n int64
	for _, g := range cookies {
		n += int64(len(g))
	}
	return n, nil
}

// newTestServer serves the actor's mux under httptest with a fake importer.
func newTestServer(t *testing.T, imp CookieImporter) (*Actor, *httptest.Server) {
	t.Helper()
	a := &Actor{
		pending:  newPendingRegistry(),
		importer: imp,
	}
	a.mu.Lock()
	a.pairingToken = testToken
	a.mu.Unlock()
	ts := httptest.NewServer(a.newMux())
	t.Cleanup(ts.Close)
	a.mu.Lock()
	a.listenerAddr = strings.TrimPrefix(ts.URL, "http://")
	a.server = &http.Server{}
	a.mu.Unlock()
	return a, ts
}

func authorizedClient(token string) *http.Client {
	return &http.Client{Transport: bearerTransport{token: token, rt: http.DefaultTransport}}
}

type bearerTransport struct {
	token string
	rt    http.RoundTripper
}

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.rt.RoundTrip(r)
}

func TestEndpointsRejectMissingOrWrongToken(t *testing.T) {
	_, ts := newTestServer(t, &fakeImporter{})
	for _, path := range []string{"/pending", "/mcp"} {
		for _, token := range []string{"", "wrong"} {
			req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("GET %s token=%q: status = %d, want 401", path, token, resp.StatusCode)
			}
		}
	}
}

func TestPushUnknownPendingID(t *testing.T) {
	_, ts := newTestServer(t, &fakeImporter{})
	body, _ := json.Marshal(pushRequest{PendingID: "nope", Cookies: []ChromeCookie{{Domain: "x.com", Name: "a", Value: "b"}}})
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/push", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestMCPImportSiteEndToEnd drives the whole chain: MCP client calls
// import_site, the extension polls /pending and pushes cookies via /push, and
// the tool result carries counts and domains but no cookie values.
func TestMCPImportSiteEndToEnd(t *testing.T) {
	imp := &fakeImporter{resolvedTo: "inst-1"}
	_, ts := newTestServer(t, imp)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "cookiebridge-test", Version: "1.0"}, nil)
	sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             ts.URL + "/mcp",
		HTTPClient:           authorizedClient(testToken),
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer sess.Close()

	// status before any import
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "status"})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(toolJSON(t, res), `"listening":true`) {
		t.Fatalf("status result = %s", toolJSON(t, res))
	}

	type callOutcome struct {
		json string
		err  error
	}
	outcome := make(chan callOutcome, 1)
	go func() {
		res, err := sess.CallTool(ctx, &mcp.CallToolParams{
			Name:      "import_site",
			Arguments: map[string]any{"domain": "github.com", "waitSeconds": 20},
		})
		if err != nil {
			outcome <- callOutcome{err: err}
			return
		}
		outcome <- callOutcome{json: toolJSON(t, res)}
	}()

	// Extension side: poll /pending until the request appears.
	var pendingID string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/pending", nil)
		req.Header.Set("X-Pairing-Token", testToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var pr pendingResponse
		_ = json.NewDecoder(resp.Body).Decode(&pr)
		resp.Body.Close()
		if len(pr.Requests) > 0 {
			r := pr.Requests[0]
			if r.Domain != "github.com" || r.InstanceID != "inst-1" {
				t.Fatalf("pending request = %+v", r)
			}
			pendingID = r.ID
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if pendingID == "" {
		t.Fatal("no pending request appeared")
	}

	// Push two matching cookies and one unrelated one.
	push := pushRequest{
		PendingID: pendingID,
		Cookies: []ChromeCookie{
			{Domain: ".github.com", Name: "user_session", Value: "super-secret-value", ExpirationDate: 1893456000},
			{Domain: "github.com", Name: "logged_in", Value: "yes", ExpirationDate: 1893456000},
			{Domain: ".unrelated.org", Name: "x", Value: "y", ExpirationDate: 1893456000},
		},
	}
	body, _ := json.Marshal(push)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/push", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var pr pushResponse
	_ = json.NewDecoder(resp.Body).Decode(&pr)
	if resp.StatusCode != http.StatusOK || pr.Status != "ok" || pr.Imported != 2 || pr.Skipped != 1 {
		t.Fatalf("push resp = %+v (http %d)", pr, resp.StatusCode)
	}

	select {
	case o := <-outcome:
		if o.err != nil {
			t.Fatalf("import_site: %v", o.err)
		}
		if !strings.Contains(o.json, `"status":"imported"`) || !strings.Contains(o.json, `"imported":2`) {
			t.Fatalf("import_site result = %s", o.json)
		}
		if !strings.Contains(o.json, "github.com") {
			t.Fatalf("domains missing in result: %s", o.json)
		}
		if strings.Contains(o.json, "super-secret-value") || strings.Contains(o.json, `"logged_in"`) {
			t.Fatalf("cookie values leaked into tool result: %s", o.json)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("import_site did not return")
	}

	if imp.importedID != "inst-1" {
		t.Fatalf("import targeted %q, want inst-1", imp.importedID)
	}
	if n := len(imp.imported["github.com"]); n != 2 {
		t.Fatalf("imported github.com cookies = %d, want 2", n)
	}
	if v := imp.imported["github.com"][0].Value; v != "super-secret-value" {
		t.Fatalf("cookie value not delivered to importer: %q", v)
	}
}

func TestMCPImportSiteTimeout(t *testing.T) {
	_, ts := newTestServer(t, &fakeImporter{resolvedTo: "inst-1"})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "cookiebridge-test", Version: "1.0"}, nil)
	sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             ts.URL + "/mcp",
		HTTPClient:           authorizedClient(testToken),
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer sess.Close()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "import_site",
		Arguments: map[string]any{"domain": "github.com", "waitSeconds": 1},
	})
	if err != nil {
		t.Fatalf("import_site: %v", err)
	}
	got := toolJSON(t, res)
	if !strings.Contains(got, `"status":"timeout"`) {
		t.Fatalf("result = %s", got)
	}

	// The expired pending entry must be gone.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/pending", nil)
	req.Header.Set("X-Pairing-Token", testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var pr pendingResponse
	_ = json.NewDecoder(resp.Body).Decode(&pr)
	if len(pr.Requests) != 0 {
		t.Fatalf("pending after timeout = %+v, want empty", pr.Requests)
	}
}

func TestTokenPersistenceAndRegen(t *testing.T) {
	store, err := persist.New(persist.PersistConfig{DataDir: t.TempDir(), Prefix: "cookiebridge"})
	if err != nil {
		t.Fatal(err)
	}
	a := &Actor{store: store, pending: newPendingRegistry(), importer: &fakeImporter{}}
	if err := a.Load(); err != nil {
		t.Fatal(err)
	}
	first := a.currentToken()
	if len(first) != 64 {
		t.Fatalf("token = %q, want 32-byte hex", first)
	}
	// Reload must restore the same token, not mint a new one.
	a2 := &Actor{store: store, pending: newPendingRegistry(), importer: &fakeImporter{}}
	if err := a2.Load(); err != nil {
		t.Fatal(err)
	}
	if a2.currentToken() != first {
		t.Fatalf("token drifted across restarts: %q vs %q", a2.currentToken(), first)
	}
}

// toolJSON extracts the JSON text content of a CallToolResult.
func toolJSON(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	if res.StructuredContent != nil {
		b, _ := json.Marshal(res.StructuredContent)
		return string(b)
	}
	t.Fatalf("no content in result: %+v", res)
	return ""
}
