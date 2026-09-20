// Package websearch implements a provider-agnostic web search actor.
//
// The actor exposes websearch.search / provider_list / account.* callables.
// v1 ships with two web search provider adapters: the Zhipu (智谱) Web Search
// API adapter and a DeepSeek adapter that drives the Anthropic-compatible
// Messages API with the server-side web_search_20250305 tool. Additional
// providers (Tavily, Brave, SearXNG) can be added by implementing the
// SearchProvider interface.
//
// Accounts: like voice STT/TTS, websearch stores a list of named accounts
// (provider + API key + optional endpoint) with a single active account.
// Credential strategy: the active account's key is used first; when the
// account has no key, the actor resolves the provider token from the
// aimanager actor (e.g. the Zhipu key shared with GLM models).
package websearch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

const defaultHTTPTimeout = 30 * time.Second

// Actor manages web search configuration and execution.
type Actor struct {
	actor.Host

	store        persist.Persist
	actorID      string
	http         *http.Client
	downloadHTTP *http.Client
	lifecycleCtx context.Context

	mu        sync.RWMutex
	providers map[string]SearchProvider // id → adapter
	state     webSearchStore            // in-memory state, synced via Save/Load
}

// Type returns the actor type identifier.
func (a *Actor) Type() string { return "websearch" }

// OnInit loads persisted configuration and registers provider adapters.
func (a *Actor) OnInit(ctx actor.Context) error {
	var err error
	a.store, err = persist.New(config.PersistConfig("websearch"))
	if err != nil {
		return err
	}
	a.actorID = ctx.Self().ID().String()
	a.http = &http.Client{Timeout: defaultHTTPTimeout}
	a.downloadHTTP = &http.Client{Timeout: downloadHTTPTimeout}
	a.lifecycleCtx = ctx.Lifecycle()

	a.providers = make(map[string]SearchProvider)
	a.providers["zhipu"] = &zhipuProvider{http: a.http}
	a.providers["deepseek"] = &deepseekProvider{http: a.http}

	if err := a.Load(); err != nil {
		ctx.Logger().Error("websearch: load failed", "err", err)
	}
	if err := a.migrateLegacyConfig(); err != nil {
		ctx.Logger().Error("websearch: legacy config migration failed", "err", err)
	}

	return nil
}

// OnStart registers callables and exposes the websearch service domain.
// Every callID lives in the "websearch" domain: the cell derives each
// callable's ServiceName from the callID's first segment (see
// actor.ServiceNameFor), and the agent tool router
// (ToolSpecsFromCallables → resolveServiceRefs) routes by that derived
// ServiceName, falling back to the calling agent's own cell when it is
// empty, which surfaces as "call ID ... not registered".
func (a *Actor) OnStart(ctx actor.Context) error {
	if err := ctx.Register("websearch.search", a.handleSearch, actor.Public(),
		actor.WithDescription("Web search via the active search account's provider (falls back to the default provider with an aimanager-resolved key). Returns results plus the provider id; MaxResults default 10, cap 50; optional TimeRange and Engine filters."),
	); err != nil {
		return fmt.Errorf("websearch: register search: %w", err)
	}
	if err := ctx.Register("websearch.provider_list", a.handleProviderList, actor.Public()); err != nil {
		return fmt.Errorf("websearch: register provider_list: %w", err)
	}
	if err := ctx.Register("websearch.account_list", a.handleAccountList, actor.Public()); err != nil {
		return fmt.Errorf("websearch: register account_list: %w", err)
	}
	if err := ctx.Register("websearch.account_create", a.handleAccountCreate, actor.AdminOnly()); err != nil {
		return fmt.Errorf("websearch: register account_create: %w", err)
	}
	if err := ctx.Register("websearch.account_update", a.handleAccountUpdate, actor.AdminOnly()); err != nil {
		return fmt.Errorf("websearch: register account_update: %w", err)
	}
	if err := ctx.Register("websearch.account_delete", a.handleAccountDelete, actor.AdminOnly()); err != nil {
		return fmt.Errorf("websearch: register account_delete: %w", err)
	}
	if err := ctx.Register("websearch.account_activate", a.handleAccountActivate, actor.AdminOnly()); err != nil {
		return fmt.Errorf("websearch: register account_activate: %w", err)
	}
	if err := ctx.Register("websearch.fetch", a.handleFetch, actor.Public(),
		actor.WithDescription("Fetch a URL over HTTP, strip non-content HTML, and return the visible text plus page metadata. Body capped at 2 MB, extracted text at MaxChars (default 10000). Use it to read a page found by websearch.search; use websearch.download for binaries."),
	); err != nil {
		return fmt.Errorf("websearch: register fetch: %w", err)
	}
	if err := ctx.Register("websearch.download", a.handleDownload, actor.Public(),
		actor.WithDescription("Stream a URL's raw bytes to a SavePath on disk (binary-safe; parent directory created on demand). Capped at MaxBytes, default 500 MB — larger bodies are truncated with Truncated=true. Requires URL and SavePath."),
	); err != nil {
		return fmt.Errorf("websearch: register download: %w", err)
	}
	if err := ctx.RegisterDomain("websearch").Expose(); err != nil {
		return fmt.Errorf("websearch: expose service: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Persistence — implements persist.Persistent
// ---------------------------------------------------------------------------

// webSearchStore is the on-disk shape: a list of search accounts plus the
// active account id.
type webSearchStore struct {
	Accounts []gen.WebSearchAccount `json:"accounts"`
	ActiveID string                 `json:"activeId,omitempty"`
}

// Load restores the persisted state into memory. Implements persist.Persistent.
func (a *Actor) Load() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("websearch"))
		if err != nil {
			return err
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return persist.LoadOrZero(a.store, a.actorID, &a.state)
}

// Save persists the in-memory state to disk. Implements persist.Persistent.
func (a *Actor) Save() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.saveLocked()
}

func (a *Actor) saveLocked() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("websearch"))
		if err != nil {
			return err
		}
	}
	return a.store.Save(a.actorID, a.state)
}

// OnStop saves state on shutdown.
func (a *Actor) OnStop(_ actor.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.saveLocked()
}

// migrateLegacyConfig converts the pre-multi-account store shape
// ({"providers": {"zhipu": {"apiKey": ...}}, "active": "zhipu"}) into one
// account per provider and marks the legacy active provider's account active.
// Idempotent: no-op when accounts already exist or no legacy data.
func (a *Actor) migrateLegacyConfig() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.state.Accounts) > 0 {
		return nil
	}
	var m map[string]any
	if err := persist.LoadOrZero(a.store, a.actorID, &m); err != nil {
		return fmt.Errorf("load legacy: %w", err)
	}
	providers, _ := m["providers"].(map[string]any)
	if len(providers) == 0 {
		return nil
	}
	active, _ := m["active"].(string)
	for provider, raw := range providers {
		cfg, _ := raw.(map[string]any)
		acc := gen.WebSearchAccount{
			ID:       newAccountID(),
			Name:     provider,
			Provider: provider,
			APIKey:   strVal(cfg["apiKey"]),
			Endpoint: strVal(cfg["endpoint"]),
		}
		a.state.Accounts = append(a.state.Accounts, acc)
		if provider == active {
			a.state.ActiveID = acc.ID
		}
	}
	if a.state.ActiveID == "" && len(a.state.Accounts) > 0 {
		a.state.ActiveID = a.state.Accounts[0].ID
	}
	return a.saveLocked()
}

func strVal(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// newAccountID returns a short random id, e.g. "wsa_1a2b3c4d".
func newAccountID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "wsa_" + hex.EncodeToString(b[:])
}

func accountView(acc gen.WebSearchAccount) gen.WebSearchAccountView {
	return gen.WebSearchAccountView{
		ID: acc.ID, Name: acc.Name, Provider: acc.Provider, HasAPIKey: acc.APIKey != "", Endpoint: acc.Endpoint,
	}
}

// activeAccountLocked returns the active account, or nil when none is
// configured. Caller must hold a.mu.
func (a *Actor) activeAccountLocked() *gen.WebSearchAccount {
	for i := range a.state.Accounts {
		if a.state.Accounts[i].ID == a.state.ActiveID {
			return &a.state.Accounts[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Callables
// ---------------------------------------------------------------------------

// handleSearch is a stateless (PureContext) handler: the zhipu/deepseek HTTP
// round trip (defaultHTTPTimeout) plus the aimanager token fallback can block
// for tens of seconds, which must not hold the owner lane (see constraints
// "Owner Lane 禁阻塞"). It only reads shared state (providers map and the
// active account) under a.mu.RLock, which is safe from forked goroutines.
func (a *Actor) handleSearch(ctx actor.PureContext, req gen.WebSearchReq) (gen.WebSearchResp, error) {
	if req.Query == "" {
		return gen.WebSearchResp{}, fmt.Errorf("websearch.search: query is required")
	}

	a.mu.RLock()
	var providerID, apiKey, endpoint string
	if acc := a.activeAccountLocked(); acc != nil {
		providerID = acc.Provider
		apiKey = acc.APIKey
		endpoint = acc.Endpoint
	} else {
		// No accounts configured: fall back to the default provider with
		// aimanager credential resolution.
		providerID = "zhipu"
	}
	provider, ok := a.providers[providerID]
	a.mu.RUnlock()
	if !ok {
		return gen.WebSearchResp{}, fmt.Errorf("websearch.search: unknown provider %q", providerID)
	}

	// Resolve API key: account key first, then aimanager fallback.
	if apiKey == "" {
		var resolveErr error
		apiKey, resolveErr = a.resolveProviderToken(ctx, providerID)
		if resolveErr != nil {
			return gen.WebSearchResp{}, fmt.Errorf("websearch.search: no API key for provider %q (set one on the active account or add a matching LLM provider): %w", providerID, resolveErr)
		}
	}

	maxResults := int(req.MaxResults)
	if maxResults <= 0 {
		maxResults = 10
	}
	if maxResults > 50 {
		maxResults = 50
	}

	opts := SearchOpts{
		MaxResults: maxResults,
		TimeRange:  req.TimeRange,
		Engine:     req.Engine,
		ApiKey:     apiKey,
		Endpoint:   endpoint,
	}

	results, err := provider.Search(a.lifecycleCtx, req.Query, opts)
	if err != nil {
		return gen.WebSearchResp{}, fmt.Errorf("websearch.search: %w", err)
	}

	return gen.WebSearchResp{
		Results:  results,
		Provider: providerID,
	}, nil
}

// handleProviderList is a stateless (PureContext) snapshot read: it copies the
// provider table under a.mu without mutating state, so it runs on the forked
// pure loop.
func (a *Actor) handleProviderList(_ actor.PureContext) (gen.WebSearchProviderListResp, error) {
	a.mu.RLock()
	providers := make([]gen.WebSearchProviderInfo, 0, len(a.providers))
	for _, p := range a.providers {
		providers = append(providers, gen.WebSearchProviderInfo{
			ID:          p.ID(),
			Name:        p.Name(),
			RequiresKey: p.RequiresKey(),
		})
	}
	a.mu.RUnlock()

	return gen.WebSearchProviderListResp{Providers: providers}, nil
}

func (a *Actor) handleAccountList(_ actor.Context, _ gen.WebSearchAccountListReq) (gen.WebSearchAccountListResp, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	views := make([]gen.WebSearchAccountView, 0, len(a.state.Accounts))
	for i := range a.state.Accounts {
		views = append(views, accountView(a.state.Accounts[i]))
	}
	return gen.WebSearchAccountListResp{Items: views, ActiveID: a.state.ActiveID}, nil
}

func (a *Actor) handleAccountCreate(_ actor.Context, req gen.WebSearchAccountCreateReq) (gen.WebSearchAccountCreateResp, error) {
	if req.Name == "" || req.Provider == "" {
		return gen.WebSearchAccountCreateResp{}, fmt.Errorf("websearch.account_create: name and provider are required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, known := a.providers[req.Provider]; !known {
		return gen.WebSearchAccountCreateResp{}, fmt.Errorf("websearch.account_create: unknown provider %q", req.Provider)
	}
	acc := gen.WebSearchAccount{
		ID:       newAccountID(),
		Name:     req.Name,
		Provider: req.Provider,
		APIKey:   req.APIKey,
		Endpoint: req.Endpoint,
	}
	a.state.Accounts = append(a.state.Accounts, acc)
	// First account becomes active automatically.
	if a.state.ActiveID == "" {
		a.state.ActiveID = acc.ID
	}
	if err := a.saveLocked(); err != nil {
		return gen.WebSearchAccountCreateResp{}, fmt.Errorf("websearch.account_create: save: %w", err)
	}
	return gen.WebSearchAccountCreateResp{Account: accountView(acc)}, nil
}

func (a *Actor) handleAccountUpdate(_ actor.Context, req gen.WebSearchAccountUpdateReq) (gen.WebSearchAccountUpdateResp, error) {
	if req.ID == "" {
		return gen.WebSearchAccountUpdateResp{}, fmt.Errorf("websearch.account_update: account id is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	idx := -1
	for i := range a.state.Accounts {
		if a.state.Accounts[i].ID == req.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return gen.WebSearchAccountUpdateResp{}, fmt.Errorf("websearch.account_update: account %q not found", req.ID)
	}
	acc := &a.state.Accounts[idx]
	if req.Name != "" {
		acc.Name = req.Name
	}
	if req.Provider != "" {
		if _, known := a.providers[req.Provider]; !known {
			return gen.WebSearchAccountUpdateResp{}, fmt.Errorf("websearch.account_update: unknown provider %q", req.Provider)
		}
		acc.Provider = req.Provider
	}
	// Empty ApiKey preserves the stored secret (redacted edit flow).
	if req.APIKey != "" {
		acc.APIKey = req.APIKey
	}
	if req.Endpoint != "" {
		acc.Endpoint = req.Endpoint
	}
	if err := a.saveLocked(); err != nil {
		return gen.WebSearchAccountUpdateResp{}, fmt.Errorf("websearch.account_update: save: %w", err)
	}
	return gen.WebSearchAccountUpdateResp{Account: accountView(*acc)}, nil
}

func (a *Actor) handleAccountDelete(_ actor.Context, req gen.WebSearchAccountDeleteReq) (gen.WebSearchAccountDeleteResp, error) {
	if req.ID == "" {
		return gen.WebSearchAccountDeleteResp{}, fmt.Errorf("websearch.account_delete: account id is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	idx := -1
	for i := range a.state.Accounts {
		if a.state.Accounts[i].ID == req.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return gen.WebSearchAccountDeleteResp{}, fmt.Errorf("websearch.account_delete: account %q not found", req.ID)
	}
	a.state.Accounts = append(a.state.Accounts[:idx], a.state.Accounts[idx+1:]...)
	if a.state.ActiveID == req.ID {
		a.state.ActiveID = ""
		if len(a.state.Accounts) > 0 {
			a.state.ActiveID = a.state.Accounts[0].ID
		}
	}
	if err := a.saveLocked(); err != nil {
		return gen.WebSearchAccountDeleteResp{}, fmt.Errorf("websearch.account_delete: save: %w", err)
	}
	return gen.WebSearchAccountDeleteResp{}, nil
}

func (a *Actor) handleAccountActivate(_ actor.Context, req gen.WebSearchAccountActivateReq) (gen.WebSearchAccountActivateResp, error) {
	if req.ID == "" {
		return gen.WebSearchAccountActivateResp{}, fmt.Errorf("websearch.account_activate: account id is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	found := false
	for i := range a.state.Accounts {
		if a.state.Accounts[i].ID == req.ID {
			found = true
			break
		}
	}
	if !found {
		return gen.WebSearchAccountActivateResp{}, fmt.Errorf("websearch.account_activate: account %q not found", req.ID)
	}
	a.state.ActiveID = req.ID
	if err := a.saveLocked(); err != nil {
		return gen.WebSearchAccountActivateResp{}, fmt.Errorf("websearch.account_activate: save: %w", err)
	}
	return gen.WebSearchAccountActivateResp{ActiveID: req.ID}, nil
}

// ---------------------------------------------------------------------------
// Credential resolution
// ---------------------------------------------------------------------------

// resolveProviderToken fetches the API key from aimanager for providers that
// share credentials with LLM providers. Returns empty string when no match.
// Takes a PureContext: it is only ever called from stateless IO handlers, and
// LookupService + Invoke/Final are thread-safe from forked goroutines (the
// same pattern as media.resolveProviderToken).
func (a *Actor) resolveProviderToken(ctx actor.PureContext, providerID string) (string, error) {
	aimanagerRef, ok := ctx.LookupService("aimanager")
	if !ok {
		return "", fmt.Errorf("aimanager service not available")
	}

	// List all providers to find one matching this search provider.
	callCtx, cancel := context.WithTimeout(a.lifecycleCtx, 10*time.Second)
	defer cancel()

	listCall := aimanagerRef.Invoke(callCtx, "aimanager.provider_full_list", struct{}{})
	listResult, err := listCall.Final(callCtx)
	if err != nil {
		return "", fmt.Errorf("list providers: %w", err)
	}

	// Extract providers from the result.
	var providers []gen.Provider
	switch r := listResult.(type) {
	case gen.ProviderListResp:
		providers = r.Items
	case *gen.ProviderListResp:
		if r != nil {
			providers = r.Items
		}
	}

	// Find a provider whose endpoint matches this search provider.
	matchDomain := providerDomain(providerID)
	if matchDomain == "" {
		return "", fmt.Errorf("no LLM provider mapping for search provider %q", providerID)
	}

	for _, p := range providers {
		if matchEndpoint(p.Endpoint, matchDomain) {
			tokenCall := aimanagerRef.Invoke(callCtx, "aimanager.provider_resolve_token", gen.AIManagerProviderResolveTokenReq{Name: p.Name})
			tokenResult, err := tokenCall.Final(callCtx)
			if err != nil {
				return "", fmt.Errorf("resolve token for %q: %w", p.Name, err)
			}
			switch r := tokenResult.(type) {
			case gen.AIManagerProviderResolveTokenResp:
				if r.AuthToken != "" {
					return r.AuthToken, nil
				}
			case *gen.AIManagerProviderResolveTokenResp:
				if r != nil && r.AuthToken != "" {
					return r.AuthToken, nil
				}
			}
		}
	}

	return "", fmt.Errorf("no matching LLM provider found for domain %q", matchDomain)
}

// providerDomain maps a search provider ID to the LLM provider endpoint domain
// that shares the same API key.
func providerDomain(providerID string) string {
	switch providerID {
	case "zhipu":
		return "bigmodel.cn"
	case "deepseek":
		return "api.deepseek.com"
	default:
		return ""
	}
}

// matchEndpoint checks if an LLM provider endpoint contains the expected domain.
func matchEndpoint(endpoint, domain string) bool {
	return strings.Contains(endpoint, domain)
}
