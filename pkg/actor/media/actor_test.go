package media

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/ref"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func newTestActor(t *testing.T) *Actor {
	t.Helper()
	return &Actor{store: persist.NewFSPersist(t.TempDir()), actorID: "media-test"}
}

func createAccount(t *testing.T, a *Actor, kind, name, key string) gen.MediaAccountCreateResp {
	t.Helper()
	resp, err := a.handleAccountCreate(nil, gen.MediaAccountCreateReq{Kind: kind, Name: name, Provider: "openai", APIKey: key, Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// TestAccountProxyRoundTrip verifies the Proxy field survives the create →
// view → update path and is returned to callers of the internal
// active-account lookup (which the agent uses to dial through the proxy).
func TestAccountProxyRoundTrip(t *testing.T) {
	a := newTestActor(t)
	created, err := a.handleAccountCreate(nil, gen.MediaAccountCreateReq{Kind: "image", Name: "proxied", Provider: "openai", APIKey: "k", Proxy: "http://media-proxy:7890"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Account.Proxy != "http://media-proxy:7890" {
		t.Fatalf("create view lost proxy: %+v", created.Account)
	}

	updated, err := a.handleAccountUpdate(nil, gen.MediaAccountUpdateReq{ID: created.Account.ID, Proxy: "socks5://media-proxy:1080"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Account.Proxy != "socks5://media-proxy:1080" {
		t.Fatalf("update view lost proxy: %+v", updated.Account)
	}

	active, err := a.handleActiveAccount(nil, gen.MediaActiveAccountReq{Kind: "image"})
	if err != nil {
		t.Fatal(err)
	}
	if active.Account.Proxy != "socks5://media-proxy:1080" {
		t.Fatalf("active account lost proxy: %+v", active.Account)
	}
}

func TestAccountCRUDAndRedaction(t *testing.T) {
	a := newTestActor(t)
	created := createAccount(t, a, "image", "primary", "secret-key")
	if !created.Account.HasAPIKey {
		t.Fatal("create response should report a configured API key")
	}

	list, err := a.handleAccountList(nil, gen.MediaAccountListReq{Kind: "image"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.ActiveID != created.Account.ID {
		t.Fatalf("unexpected list response: %+v", list)
	}
	encoded, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret-key") || strings.Contains(string(encoded), `"ApiKey":`) {
		t.Fatalf("list leaked API key: %s", encoded)
	}

	updated, err := a.handleAccountUpdate(nil, gen.MediaAccountUpdateReq{ID: created.Account.ID, Name: "renamed", Model: "updated-model"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Account.Name != "renamed" || updated.Account.Model != "updated-model" {
		t.Fatalf("unexpected updated account: %+v", updated.Account)
	}
	if _, err := a.handleAccountDelete(nil, gen.MediaAccountDeleteReq{ID: created.Account.ID}); err != nil {
		t.Fatal(err)
	}
	list, err = a.handleAccountList(nil, gen.MediaAccountListReq{Kind: "image"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 0 || list.ActiveID != "" {
		t.Fatalf("delete did not clear last active account: %+v", list)
	}
}

func TestAccountsPersistAcrossActorInstances(t *testing.T) {
	a := newTestActor(t)
	created := createAccount(t, a, "video", "primary", "persisted-secret")

	restarted := &Actor{store: a.store, actorID: a.actorID}
	active, err := restarted.handleActiveAccount(nil, gen.MediaActiveAccountReq{Kind: "video"})
	if err != nil {
		t.Fatal(err)
	}
	if active.Account.ID != created.Account.ID || active.Account.APIKey != "persisted-secret" {
		t.Fatalf("persisted account was not restored: %+v", active.Account)
	}
}

func TestAccountUpdateEmptyAPIKeyPreservesSecret(t *testing.T) {
	a := newTestActor(t)
	created := createAccount(t, a, "image", "primary", "secret")
	if _, err := a.handleAccountUpdate(nil, gen.MediaAccountUpdateReq{ID: created.Account.ID, Model: "new"}); err != nil {
		t.Fatal(err)
	}
	st, err := a.loadStore()
	if err != nil {
		t.Fatal(err)
	}
	if st.Accounts[0].APIKey != "secret" || st.Accounts[0].Model != "new" {
		t.Fatalf("empty key update changed persisted account: %+v", st.Accounts[0])
	}
}

func TestImageAndVideoAccountsActivateIndependently(t *testing.T) {
	a := newTestActor(t)
	image1 := createAccount(t, a, "image", "image-one", "image-one-key")
	image2 := createAccount(t, a, "image", "image-two", "image-two-key")
	video := createAccount(t, a, "video", "video-one", "video-key")

	if _, err := a.handleAccountActivate(nil, gen.MediaAccountActivateReq{Kind: "image", ID: image2.Account.ID}); err != nil {
		t.Fatal(err)
	}
	imageActive, err := a.handleActiveAccount(nil, gen.MediaActiveAccountReq{Kind: "image"})
	if err != nil {
		t.Fatal(err)
	}
	if imageActive.Account.ID != image2.Account.ID || imageActive.Account.APIKey != "image-two-key" {
		t.Fatalf("unexpected image active account: %+v", imageActive.Account)
	}
	videoActive, err := a.handleActiveAccount(nil, gen.MediaActiveAccountReq{Kind: "video"})
	if err != nil {
		t.Fatal(err)
	}
	if videoActive.Account.ID != video.Account.ID || videoActive.Account.APIKey != "video-key" {
		t.Fatalf("unexpected video active account: %+v", videoActive.Account)
	}
	if _, err := a.handleAccountActivate(nil, gen.MediaAccountActivateReq{Kind: "video", ID: image1.Account.ID}); err == nil {
		t.Fatal("expected activation with the wrong kind to fail")
	}
}

// aimanagerTestCtx returns a FakeCtx whose aimanager service stub serves
// provider_full_list and provider_resolve_token.
func aimanagerTestCtx(providerList []gen.Provider, resolveTokens map[string]string) *testutil.FakeCtx {
	aimanagerRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		switch callID {
		case "aimanager.provider_full_list":
			return gen.ProviderListResp{Items: providerList}
		case "aimanager.provider_resolve_token":
			req := payload.(gen.AIManagerProviderResolveTokenReq)
			return gen.AIManagerProviderResolveTokenResp{AuthToken: resolveTokens[req.Name]}
		}
		return nil
	})
	ctx := &testutil.FakeCtx{}
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name != "aimanager" {
			return nil, false
		}
		return aimanagerRef, true
	}
	return ctx
}

func TestHandleActiveAccountFallbackResolvesToken(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleAccountCreate(nil, gen.MediaAccountCreateReq{
		Kind: "image", Name: "no-key", Provider: "openai", Model: "test-model", BaseURL: "https://api.openai.com/v1",
	}); err != nil {
		t.Fatal(err)
	}

	ctx := aimanagerTestCtx([]gen.Provider{
		{Name: "openai", Kind: "openai", Endpoint: "https://api.openai.com/v1", HasAuthToken: true},
		{Name: "unrelated", Kind: "openai", Endpoint: "https://api.other.com/v1"},
	}, map[string]string{"openai": "resolved-openai-key"})

	resp, err := a.handleActiveAccount(ctx, gen.MediaActiveAccountReq{Kind: "image"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Account.APIKey != "resolved-openai-key" {
		t.Fatalf("api key = %q, want resolved-openai-key", resp.Account.APIKey)
	}

	st, err := a.loadStore()
	if err != nil {
		t.Fatal(err)
	}
	if st.Accounts[0].APIKey != "" {
		t.Fatalf("fallback token was persisted into the media store: %q", st.Accounts[0].APIKey)
	}
}

func TestHandleActiveAccountFallbackUsesProviderDefaultDomain(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleAccountCreate(nil, gen.MediaAccountCreateReq{
		Kind: "video", Name: "glm-no-key", Provider: "glm", Model: "test-model",
	}); err != nil {
		t.Fatal(err)
	}

	ctx := aimanagerTestCtx([]gen.Provider{
		{Name: "glm", Kind: "glm", Endpoint: "https://open.bigmodel.cn/api/paas/v4", HasAuthToken: true},
	}, map[string]string{"glm": "resolved-glm-key"})

	resp, err := a.handleActiveAccount(ctx, gen.MediaActiveAccountReq{Kind: "video"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Account.APIKey != "resolved-glm-key" {
		t.Fatalf("api key = %q, want resolved-glm-key", resp.Account.APIKey)
	}
}

func TestHandleActiveAccountPrefersAccountKey(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleAccountCreate(nil, gen.MediaAccountCreateReq{
		Kind: "image", Name: "own-key", Provider: "openai", APIKey: "own-key", Model: "test-model", BaseURL: "https://api.openai.com/v1",
	}); err != nil {
		t.Fatal(err)
	}

	// A nil context proves the fallback path (the only context consumer) is
	// never reached when the account carries its own key.
	resp, err := a.handleActiveAccount(nil, gen.MediaActiveAccountReq{Kind: "image"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Account.APIKey != "own-key" {
		t.Fatalf("api key = %q, want own-key", resp.Account.APIKey)
	}
}

func TestHandleActiveAccountFallbackNoMatchErrors(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleAccountCreate(nil, gen.MediaAccountCreateReq{
		Kind: "image", Name: "no-key", Provider: "openai", Model: "test-model", BaseURL: "https://api.openai.com/v1",
	}); err != nil {
		t.Fatal(err)
	}

	ctx := aimanagerTestCtx([]gen.Provider{
		{Name: "glm", Kind: "glm", Endpoint: "https://open.bigmodel.cn/api/paas/v4", HasAuthToken: true},
	}, map[string]string{"glm": "glm-key"})

	_, err := a.handleActiveAccount(ctx, gen.MediaActiveAccountReq{Kind: "image"})
	if err == nil || !strings.Contains(err.Error(), "no matching LLM provider found") {
		t.Fatalf("error = %v, want no-matching-provider error", err)
	}
}

func TestProviderDomainMapping(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		baseURL  string
		want     string
	}{
		{"openai default", "openai", "", "api.openai.com"},
		{"openai account base url", "openai", "https://api.openai.com/v1", "api.openai.com"},
		{"openai custom with proxy base url", "openai_custom", "https://maiai.ai/v1", "maiai.ai"},
		{"openai custom schemeless", "openai_custom", "maiai.ai/v1", "maiai.ai"},
		{"gemini default", "gemini", "", "generativelanguage.googleapis.com"},
		{"glm default", "glm", "", "bigmodel.cn"},
		{"glm base url", "glm", "https://open.bigmodel.cn/api/paas/v4", "open.bigmodel.cn"},
		{"ark default", "ark", "", "volcengine.com"},
		{"ark with china endpoint", "ark", "https://ark.cn-beijing.volces.com/api/v3", "ark.cn-beijing.volces.com"},
		{"ark with overseas endpoint", "ark", "https://ark.ap-southeast.bytepluses.com/api/v3", "ark.ap-southeast.bytepluses.com"},
		{"doubao default", "doubao", "", "volcengine.com"},
		{"doubao with china endpoint", "doubao", "https://ark.cn-beijing.volces.com/api/v3", "ark.cn-beijing.volces.com"},
		{"imagex image default", "imagex", "", "volcengine.com"},
		{"unknown provider", "weird", "", ""},
		{"unparsable base url", "openai", "://bad", "api.openai.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := providerDomain(tc.provider, tc.baseURL); got != tc.want {
				t.Fatalf("providerDomain(%q, %q) = %q, want %q", tc.provider, tc.baseURL, got, tc.want)
			}
		})
	}
}
