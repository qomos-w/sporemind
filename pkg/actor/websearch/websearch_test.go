package websearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/ref"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// ---------------------------------------------------------------------------
// Zhipu provider adapter tests
// ---------------------------------------------------------------------------

func TestZhipuSearch_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected auth header: %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected content type: %q", r.Header.Get("Content-Type"))
		}

		var req zhipuRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.SearchQuery != "golang testing" {
			t.Errorf("unexpected query: %q", req.SearchQuery)
		}
		if req.SearchEngine != "search_std" {
			t.Errorf("unexpected engine: %q", req.SearchEngine)
		}
		if req.Count != 5 {
			t.Errorf("unexpected count: %d", req.Count)
		}

		resp := zhipuResponse{
			ID:      "test-id",
			Created: 1234567890,
		}
		resp.SearchResult = append(resp.SearchResult,
			struct {
				Title       string `json:"title"`
				Link        string `json:"link"`
				Content     string `json:"content"`
				Media       string `json:"media"`
				Icon        string `json:"icon"`
				Refer       string `json:"refer"`
				PublishDate string `json:"publish_date"`
			}{
				Title:       "Go Testing",
				Link:        "https://go.dev/doc/testing",
				Content:     "Go testing package",
				Media:       "go.dev",
				Icon:        "https://go.dev/favicon.ico",
				PublishDate: "2024-01-01",
			},
		)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := &zhipuProvider{http: server.Client()}
	results, err := p.Search(context.Background(), "golang testing", SearchOpts{
		MaxResults: 5,
		ApiKey:     "test-key",
		Endpoint:   server.URL,
	})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Title != "Go Testing" {
		t.Errorf("unexpected title: %q", r.Title)
	}
	if r.URL != "https://go.dev/doc/testing" {
		t.Errorf("unexpected URL: %q", r.URL)
	}
	if r.Snippet != "Go testing package" {
		t.Errorf("unexpected snippet: %q", r.Snippet)
	}
	if r.Source != "go.dev" {
		t.Errorf("unexpected source: %q", r.Source)
	}
	if r.PublishedDate != "2024-01-01" {
		t.Errorf("unexpected publish date: %q", r.PublishedDate)
	}
}

func TestZhipuSearch_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{
				"code":    "1701",
				"message": "concurrent limit exceeded",
			},
		})
	}))
	defer server.Close()

	p := &zhipuProvider{http: server.Client()}
	_, err := p.Search(context.Background(), "test", SearchOpts{
		ApiKey:   "test-key",
		Endpoint: server.URL,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !contains(got, "1701") {
		t.Errorf("expected error code 1701, got: %s", got)
	}
}

func TestZhipuSearch_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"code":"401","message":"unauthorized"}}`))
	}))
	defer server.Close()

	p := &zhipuProvider{http: server.Client()}
	_, err := p.Search(context.Background(), "test", SearchOpts{
		ApiKey:   "bad-key",
		Endpoint: server.URL,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !contains(got, "401") {
		t.Errorf("expected HTTP 401, got: %s", got)
	}
}

func TestZhipuSearch_DefaultEngine(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req zhipuRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.SearchEngine != "search_std" {
			t.Errorf("expected default engine search_std, got %q", req.SearchEngine)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(zhipuResponse{})
	}))
	defer server.Close()

	p := &zhipuProvider{http: server.Client()}
	_, err := p.Search(context.Background(), "test", SearchOpts{
		ApiKey:   "key",
		Endpoint: server.URL,
	})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
}

func TestZhipuSearch_CustomEngine(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req zhipuRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.SearchEngine != "search_pro_sogou" {
			t.Errorf("expected engine search_pro_sogou, got %q", req.SearchEngine)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(zhipuResponse{})
	}))
	defer server.Close()

	p := &zhipuProvider{http: server.Client()}
	_, err := p.Search(context.Background(), "test", SearchOpts{
		ApiKey:   "key",
		Endpoint: server.URL,
		Engine:   "search_pro_sogou",
	})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Persistence tests
// ---------------------------------------------------------------------------

func TestSaveLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	a := &Actor{
		store:   persist.NewFSPersist(dir),
		actorID: "test-actor",
	}

	a.state = webSearchStore{
		Accounts: []gen.WebSearchAccount{
			{ID: "wsa_1", Name: "main", Provider: "zhipu", APIKey: "secret-key", Endpoint: "https://custom.example.com"},
			{ID: "wsa_2", Name: "backup", Provider: "zhipu", APIKey: "other-key"},
		},
		ActiveID: "wsa_2",
	}

	if err := a.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Create a new actor and load.
	a2 := &Actor{
		store:   persist.NewFSPersist(dir),
		actorID: "test-actor",
	}
	if err := a2.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	if a2.state.ActiveID != "wsa_2" {
		t.Errorf("activeId = %q, want %q", a2.state.ActiveID, "wsa_2")
	}
	if len(a2.state.Accounts) != 2 {
		t.Fatalf("accounts = %d, want 2", len(a2.state.Accounts))
	}
	acc := a2.state.Accounts[0]
	if acc.APIKey != "secret-key" {
		t.Errorf("apiKey = %q, want %q", acc.APIKey, "secret-key")
	}
	if acc.Endpoint != "https://custom.example.com" {
		t.Errorf("endpoint = %q, want %q", acc.Endpoint, "https://custom.example.com")
	}
}

func TestLoad_FreshStart(t *testing.T) {
	dir := t.TempDir()
	a := &Actor{
		store:   persist.NewFSPersist(dir),
		actorID: "new-actor",
	}

	if err := a.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(a.state.Accounts) != 0 {
		t.Errorf("accounts = %d, want 0", len(a.state.Accounts))
	}
	if a.state.ActiveID != "" {
		t.Errorf("activeId = %q, want empty", a.state.ActiveID)
	}
}

func TestMigrateLegacyConfig(t *testing.T) {
	dir := t.TempDir()
	store := persist.NewFSPersist(dir)

	// Seed the legacy single-provider shape.
	legacy := map[string]any{
		"providers": map[string]any{
			"zhipu": map[string]any{"apiKey": "legacy-key", "endpoint": "https://legacy.example.com"},
		},
		"active": "zhipu",
	}
	if err := store.Save("test-actor", legacy); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}

	a := &Actor{store: store, actorID: "test-actor"}
	if err := a.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := a.migrateLegacyConfig(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if len(a.state.Accounts) != 1 {
		t.Fatalf("accounts = %d, want 1", len(a.state.Accounts))
	}
	acc := a.state.Accounts[0]
	if acc.Provider != "zhipu" || acc.APIKey != "legacy-key" || acc.Endpoint != "https://legacy.example.com" {
		t.Errorf("migrated account = %+v", acc)
	}
	if a.state.ActiveID != acc.ID {
		t.Errorf("activeId = %q, want %q", a.state.ActiveID, acc.ID)
	}

	// Idempotent: second run must not duplicate accounts.
	if err := a.migrateLegacyConfig(); err != nil {
		t.Fatalf("migrate (2nd): %v", err)
	}
	if len(a.state.Accounts) != 1 {
		t.Errorf("accounts after 2nd migrate = %d, want 1", len(a.state.Accounts))
	}

	// Migration must persist.
	a2 := &Actor{store: store, actorID: "test-actor"}
	if err := a2.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(a2.state.Accounts) != 1 || a2.state.Accounts[0].APIKey != "legacy-key" {
		t.Errorf("reloaded accounts = %+v", a2.state.Accounts)
	}
}

// ---------------------------------------------------------------------------
// Account CRUD tests
// ---------------------------------------------------------------------------

func newTestActor(t *testing.T) *Actor {
	t.Helper()
	return &Actor{
		store:   persist.NewFSPersist(t.TempDir()),
		actorID: "test-actor",
		http:    &http.Client{Timeout: 30 * time.Second},
		providers: map[string]SearchProvider{
			"zhipu": &zhipuProvider{},
		},
	}
}

func TestAccountCRUD_Lifecycle(t *testing.T) {
	a := newTestActor(t)

	// Create: first account becomes active.
	createResp, err := a.handleAccountCreate(nil, gen.WebSearchAccountCreateReq{
		Name: "main", Provider: "zhipu", APIKey: "key-1", Endpoint: "https://a.example.com",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	acc1 := createResp.Account
	if acc1.ID == "" || !acc1.HasAPIKey {
		t.Errorf("created account = %+v", acc1)
	}
	if a.state.ActiveID != acc1.ID {
		t.Errorf("activeId = %q, want %q", a.state.ActiveID, acc1.ID)
	}

	createResp2, err := a.handleAccountCreate(nil, gen.WebSearchAccountCreateReq{Name: "backup", Provider: "zhipu"})
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}
	acc2 := createResp2.Account
	if acc2.HasAPIKey {
		t.Error("second account should have HasApiKey=false")
	}

	// List: redacted views only.
	listResp, err := a.handleAccountList(nil, gen.WebSearchAccountListReq{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listResp.Items) != 2 || listResp.ActiveID != acc1.ID {
		t.Errorf("list = %+v", listResp)
	}
	data, _ := json.Marshal(listResp)
	if contains(string(data), "key-1") {
		t.Error("account_list leaked API key")
	}

	// Update: empty ApiKey preserves the stored secret.
	if _, err := a.handleAccountUpdate(nil, gen.WebSearchAccountUpdateReq{ID: acc1.ID, Name: "main-renamed"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if a.state.Accounts[0].Name != "main-renamed" || a.state.Accounts[0].APIKey != "key-1" {
		t.Errorf("updated account = %+v", a.state.Accounts[0])
	}

	// Activate: switches the active account.
	actResp, err := a.handleAccountActivate(nil, gen.WebSearchAccountActivateReq{ID: acc2.ID})
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if actResp.ActiveID != acc2.ID || a.state.ActiveID != acc2.ID {
		t.Errorf("activeId = %q, want %q", a.state.ActiveID, acc2.ID)
	}

	// Delete: deleting the active account falls back to the first remaining.
	if _, err := a.handleAccountDelete(nil, gen.WebSearchAccountDeleteReq{ID: acc2.ID}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(a.state.Accounts) != 1 || a.state.ActiveID != acc1.ID {
		t.Errorf("after delete: accounts=%d activeId=%q", len(a.state.Accounts), a.state.ActiveID)
	}
}

func TestAccountCreate_UnknownProvider(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleAccountCreate(nil, gen.WebSearchAccountCreateReq{Name: "x", Provider: "nope"}); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestAccountCreate_RequiresNameAndProvider(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleAccountCreate(nil, gen.WebSearchAccountCreateReq{Provider: "zhipu"}); err == nil {
		t.Fatal("expected error for missing name")
	}
	if _, err := a.handleAccountCreate(nil, gen.WebSearchAccountCreateReq{Name: "x"}); err == nil {
		t.Fatal("expected error for missing provider")
	}
}

func TestAccountUpdate_NotFound(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleAccountUpdate(nil, gen.WebSearchAccountUpdateReq{ID: "wsa_missing", Name: "x"}); err == nil {
		t.Fatal("expected error for missing account")
	}
}

func TestAccountActivate_NotFound(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleAccountActivate(nil, gen.WebSearchAccountActivateReq{ID: "wsa_missing"}); err == nil {
		t.Fatal("expected error for missing account")
	}
}

func TestAccountDelete_NotFound(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleAccountDelete(nil, gen.WebSearchAccountDeleteReq{ID: "wsa_missing"}); err == nil {
		t.Fatal("expected error for missing account")
	}
}

// ---------------------------------------------------------------------------
// Provider list tests
// ---------------------------------------------------------------------------

func TestProviderList_Adapters(t *testing.T) {
	a := newTestActor(t)

	resp, err := a.handleProviderList(nil)
	if err != nil {
		t.Fatalf("provider_list: %v", err)
	}
	if len(resp.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(resp.Providers))
	}
	p := resp.Providers[0]
	if p.ID != "zhipu" || !p.RequiresKey {
		t.Errorf("provider = %+v", p)
	}
}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Token resolution regression (constraints: 凭据自动导入 — key shared by domain)
// ---------------------------------------------------------------------------

// TestHandleSearch_NoKey_NoAimanager_ErrorSemanticsUnchanged locks the "no
// key" error contract: when the active account has no API key and the
// aimanager fallback cannot resolve one, the error message must stay exactly
// as before the PureContext conversion (callers surface this text).
func TestHandleSearch_NoKey_NoAimanager_ErrorSemanticsUnchanged(t *testing.T) {
	a := newTestActor(t)
	a.lifecycleCtx = context.Background()

	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return nil, false }

	// No accounts configured → default provider "zhipu" with empty key, and
	// the aimanager fallback fails with "service not available".
	_, err := a.handleSearch(ctx, gen.WebSearchReq{Query: "test"})
	if err == nil {
		t.Fatal("expected error when no key is available")
	}
	want := `websearch.search: no API key for provider "zhipu" (set one on the active account or add a matching LLM provider): aimanager service not available`
	if err.Error() != want {
		t.Errorf("no-key error text changed:\n got: %s\nwant: %s", err.Error(), want)
	}
}

// TestHandleSearch_NoKey_ResolvesTokenFromAimanager verifies the domain-shared
// credential fallback still works from the PureContext handler: a keyless
// active account resolves its token via aimanager.provider_full_list +
// provider_resolve_token (same endpoint-domain match as before) and the search
// request carries that key.
func TestHandleSearch_NoKey_ResolvesTokenFromAimanager(t *testing.T) {
	var gotAuth string
	searchServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(zhipuResponse{})
	}))
	defer searchServer.Close()

	a := newTestActor(t)
	a.providers = map[string]SearchProvider{"zhipu": &zhipuProvider{http: searchServer.Client()}}
	a.lifecycleCtx = context.Background()
	a.state = webSearchStore{
		Accounts: []gen.WebSearchAccount{
			{ID: "wsa_1", Name: "main", Provider: "zhipu", Endpoint: searchServer.URL},
		},
		ActiveID: "wsa_1",
	}

	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name != "aimanager" {
			return nil, false
		}
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			switch callID {
			case "aimanager.provider_full_list":
				return gen.ProviderListResp{Items: []gen.Provider{
					{Name: "glm-official", Endpoint: "https://open.bigmodel.cn/api/paas/v4"},
				}}
			case "aimanager.provider_resolve_token":
				if req, ok := payload.(gen.AIManagerProviderResolveTokenReq); ok && req.Name == "glm-official" {
					return gen.AIManagerProviderResolveTokenResp{AuthToken: "shared-glm-key"}
				}
				return gen.AIManagerProviderResolveTokenResp{}
			}
			return nil
		}), true
	}

	resp, err := a.handleSearch(ctx, gen.WebSearchReq{Query: "test"})
	if err != nil {
		t.Fatalf("handleSearch with aimanager fallback: %v", err)
	}
	if resp.Provider != "zhipu" {
		t.Errorf("Provider = %q, want %q", resp.Provider, "zhipu")
	}
	if gotAuth != "Bearer shared-glm-key" {
		t.Errorf("search Authorization = %q, want %q", gotAuth, "Bearer shared-glm-key")
	}
}

// TestHandleSearch_NoKey_NoMatchingProvider keeps the fallback error contract
// for the "no matching LLM provider" branch.
func TestHandleSearch_NoKey_NoMatchingProvider(t *testing.T) {
	a := newTestActor(t)
	a.lifecycleCtx = context.Background()
	a.state = webSearchStore{
		Accounts: []gen.WebSearchAccount{
			{ID: "wsa_1", Name: "main", Provider: "zhipu"},
		},
		ActiveID: "wsa_1",
	}

	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name != "aimanager" {
			return nil, false
		}
		// A provider list with no endpoint matching bigmodel.cn.
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			if callID == "aimanager.provider_full_list" {
				return gen.ProviderListResp{Items: []gen.Provider{
					{Name: "openai", Endpoint: "https://api.openai.com/v1"},
				}}
			}
			return nil
		}), true
	}

	_, err := a.handleSearch(ctx, gen.WebSearchReq{Query: "test"})
	if err == nil {
		t.Fatal("expected error when no matching provider")
	}
	if !strings.Contains(err.Error(), `no API key for provider "zhipu"`) {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), `no matching LLM provider found for domain "bigmodel.cn"`) {
		t.Errorf("unexpected error: %v", err)
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
