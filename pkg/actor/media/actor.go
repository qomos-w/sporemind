// Package media manages credentials for image and video generation services.
package media

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// Actor owns persisted image and video generation accounts.
type Actor struct {
	actor.Host
	store   persist.Persist
	actorID string
}

var _ persist.Persistent = (*Actor)(nil)

// Type returns the actor type identifier.
func (a *Actor) Type() string { return "media" }

// Load validates that persisted state can be restored by the actor.
func (a *Actor) Load() error {
	_, err := a.loadStore()
	return err
}

// Save persists the current state. Account mutations save synchronously, so
// this lifecycle hook only preserves the existing stored representation.
func (a *Actor) Save() error {
	st, err := a.loadStore()
	if err != nil {
		return err
	}
	return a.saveStore(st)
}

// OnInit initializes the actor-owned persistence store.
func (a *Actor) OnInit(ctx actor.Context) error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("media"))
		if err != nil {
			return err
		}
	}
	a.actorID = ctx.Self().ID().String()
	return nil
}

// OnStart registers account management and internal credential lookup callables.
func (a *Actor) OnStart(ctx actor.Context) error {
	if err := ctx.Register("media.list_accounts", a.handleAccountList, actor.Public()); err != nil {
		return fmt.Errorf("media: register list_accounts: %w", err)
	}
	if err := ctx.Register("media.create_account", a.handleAccountCreate, actor.AdminOnly()); err != nil {
		return fmt.Errorf("media: register create_account: %w", err)
	}
	if err := ctx.Register("media.update_account", a.handleAccountUpdate, actor.AdminOnly()); err != nil {
		return fmt.Errorf("media: register update_account: %w", err)
	}
	if err := ctx.Register("media.delete_account", a.handleAccountDelete, actor.AdminOnly()); err != nil {
		return fmt.Errorf("media: register delete_account: %w", err)
	}
	if err := ctx.Register("media.activate_account", a.handleAccountActivate, actor.AdminOnly()); err != nil {
		return fmt.Errorf("media: register activate_account: %w", err)
	}
	if err := ctx.Register("media.provider_model_set", a.handleProviderModelSet, actor.AdminOnly()); err != nil {
		return fmt.Errorf("media: register provider_model_set: %w", err)
	}
	if err := ctx.Register("media.active_account", a.handleActiveAccount, actor.Internal()); err != nil {
		return fmt.Errorf("media: register active_account: %w", err)
	}
	if err := ctx.RegisterDomain("media").Expose(); err != nil {
		return fmt.Errorf("media: expose service: %w", err)
	}
	return nil
}

type mediaStore struct {
	Accounts      []gen.MediaAccount `json:"Accounts"`
	ActiveImageID string             `json:"ActiveImageId,omitempty"`
	ActiveVideoID string             `json:"ActiveVideoId,omitempty"`
	BoundImage    *providerModelBinding `json:"BoundImage,omitempty"`
	BoundVideo    *providerModelBinding `json:"BoundVideo,omitempty"`
	BoundVision   *providerModelBinding `json:"BoundVision,omitempty"`
	// VisionAggregatorID binds recognition routing to a named aggregator
	// config instead of a (Provider, Model) pin. Mutually exclusive with
	// BoundVision: setting one clears the other.
	VisionAggregatorID string `json:"VisionAggregatorId,omitempty"`
}

// providerModelBinding is the per-Kind selection of one (Provider, Model)
// pair from the LLM providers' image/video-classified models. It routes the
// aggregator fallback and is mutually exclusive with the active account.
type providerModelBinding struct {
	Provider string `json:"Provider"`
	Model    string `json:"Model"`
}

func mediaKind(kind string) (string, error) {
	switch kind {
	case "", "image":
		return "image", nil
	case "video":
		return "video", nil
	case "vision":
		return "vision", nil
	default:
		return "", fmt.Errorf("media: unsupported account kind %q", kind)
	}
}

// accountKind reports whether kind supports media accounts. Vision is
// binding-only: recognition models are chat LLMs reached through the
// aggregator, so they have no standalone service account of their own.
func accountKind(kind string) bool { return kind != "vision" }

func (st *mediaStore) activeIDFor(kind string) string {
	switch kind {
	case "video":
		return st.ActiveVideoID
	case "vision":
		// Vision has no account side; a non-empty ID here would wrongly match
		// the active image account in activeAccount.
		return ""
	}
	return st.ActiveImageID
}

func (st *mediaStore) setActiveIDFor(kind, id string) {
	if kind == "video" {
		st.ActiveVideoID = id
		return
	}
	if kind == "vision" {
		return
	}
	st.ActiveImageID = id
}

func (st *mediaStore) boundFor(kind string) *providerModelBinding {
	switch kind {
	case "video":
		return st.BoundVideo
	case "vision":
		return st.BoundVision
	}
	return st.BoundImage
}

func (st *mediaStore) setBoundFor(kind, provider, model string) {
	b := &providerModelBinding{Provider: provider, Model: model}
	switch kind {
	case "video":
		st.BoundVideo = b
	case "vision":
		st.BoundVision = b
	default:
		st.BoundImage = b
	}
}

func (st *mediaStore) clearBoundFor(kind string) {
	switch kind {
	case "video":
		st.BoundVideo = nil
	case "vision":
		st.BoundVision = nil
	default:
		st.BoundImage = nil
	}
}

func (a *Actor) loadStore() (mediaStore, error) {
	var st mediaStore
	if err := persist.LoadOrZero(a.store, a.actorID, &st); err != nil {
		return mediaStore{}, err
	}
	return st, nil
}

func (a *Actor) saveStore(st mediaStore) error {
	return a.store.Save(a.actorID, st)
}

func (a *Actor) activeAccount(kind string) (gen.MediaAccount, error) {
	kind, err := mediaKind(kind)
	if err != nil {
		return gen.MediaAccount{}, err
	}
	st, err := a.loadStore()
	if err != nil {
		return gen.MediaAccount{}, err
	}
	activeID := st.activeIDFor(kind)
	for i := range st.Accounts {
		if st.Accounts[i].ID == activeID {
			return st.Accounts[i], nil
		}
	}
	return gen.MediaAccount{}, fmt.Errorf("media: no active %s account configured", kind)
}

func accountView(acc gen.MediaAccount) gen.MediaAccountView {
	return gen.MediaAccountView{
		ID:        acc.ID,
		Kind:      acc.Kind,
		Name:      acc.Name,
		Provider:  acc.Provider,
		HasAPIKey: acc.APIKey != "",
		Model:     acc.Model,
		BaseURL:   acc.BaseURL,
		Proxy:     acc.Proxy,
	}
}

func newAccountID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "ma_" + hex.EncodeToString(b[:])
}

func (a *Actor) handleAccountList(_ actor.PureContext, req gen.MediaAccountListReq) (gen.MediaAccountListResp, error) {
	kind, err := mediaKind(req.Kind)
	if err != nil {
		return gen.MediaAccountListResp{}, err
	}
	st, err := a.loadStore()
	if err != nil {
		return gen.MediaAccountListResp{}, fmt.Errorf("media: load accounts: %w", err)
	}
	items := make([]gen.MediaAccountView, 0, len(st.Accounts))
	for i := range st.Accounts {
		if st.Accounts[i].Kind == kind {
			items = append(items, accountView(st.Accounts[i]))
		}
	}
	resp := gen.MediaAccountListResp{Items: items, ActiveID: st.activeIDFor(kind)}
	if b := st.boundFor(kind); b != nil {
		resp.BoundProvider = b.Provider
		resp.BoundModel = b.Model
	}
	if kind == "vision" {
		resp.BoundAggregator = st.VisionAggregatorID
	}
	return resp, nil
}

func (a *Actor) handleAccountCreate(_ actor.PureContext, req gen.MediaAccountCreateReq) (gen.MediaAccountCreateResp, error) {
	if req.Name == "" || req.Provider == "" {
		return gen.MediaAccountCreateResp{}, fmt.Errorf("media: name and provider are required")
	}
	kind, err := mediaKind(req.Kind)
	if err != nil {
		return gen.MediaAccountCreateResp{}, err
	}
	if !accountKind(kind) {
		return gen.MediaAccountCreateResp{}, fmt.Errorf("media: kind %q does not support accounts; configure a provider-model binding instead", kind)
	}
	st, err := a.loadStore()
	if err != nil {
		return gen.MediaAccountCreateResp{}, fmt.Errorf("media: load accounts: %w", err)
	}
	acc := gen.MediaAccount{ID: newAccountID(), Kind: kind, Name: req.Name, Provider: req.Provider, APIKey: req.APIKey, Model: req.Model, BaseURL: req.BaseURL, Proxy: req.Proxy}
	st.Accounts = append(st.Accounts, acc)
	if st.activeIDFor(kind) == "" {
		st.setActiveIDFor(kind, acc.ID)
		// Auto-activation takes over from a provider-model binding (mutual
		// exclusion: one active selection per Kind).
		st.clearBoundFor(kind)
	}
	if err := a.saveStore(st); err != nil {
		return gen.MediaAccountCreateResp{}, fmt.Errorf("media: save accounts: %w", err)
	}
	return gen.MediaAccountCreateResp{Account: accountView(acc)}, nil
}

func (a *Actor) handleAccountUpdate(_ actor.PureContext, req gen.MediaAccountUpdateReq) (gen.MediaAccountUpdateResp, error) {
	if req.ID == "" {
		return gen.MediaAccountUpdateResp{}, fmt.Errorf("media: account id is required")
	}
	st, err := a.loadStore()
	if err != nil {
		return gen.MediaAccountUpdateResp{}, fmt.Errorf("media: load accounts: %w", err)
	}
	for i := range st.Accounts {
		if st.Accounts[i].ID != req.ID {
			continue
		}
		acc := st.Accounts[i]
		if req.Name != "" {
			acc.Name = req.Name
		}
		if req.Provider != "" {
			acc.Provider = req.Provider
		}
		if req.APIKey != "" {
			acc.APIKey = req.APIKey
		}
		if req.Model != "" {
			acc.Model = req.Model
		}
		if req.BaseURL != "" {
			acc.BaseURL = req.BaseURL
		}
		if req.Proxy != "" {
			acc.Proxy = req.Proxy
		}
		st.Accounts[i] = acc
		if err := a.saveStore(st); err != nil {
			return gen.MediaAccountUpdateResp{}, fmt.Errorf("media: save accounts: %w", err)
		}
		return gen.MediaAccountUpdateResp{Account: accountView(acc)}, nil
	}
	return gen.MediaAccountUpdateResp{}, fmt.Errorf("media: account %q not found", req.ID)
}

func (a *Actor) handleAccountDelete(_ actor.PureContext, req gen.MediaAccountDeleteReq) (gen.MediaAccountDeleteResp, error) {
	if req.ID == "" {
		return gen.MediaAccountDeleteResp{}, fmt.Errorf("media: account id is required")
	}
	st, err := a.loadStore()
	if err != nil {
		return gen.MediaAccountDeleteResp{}, fmt.Errorf("media: load accounts: %w", err)
	}
	remaining := st.Accounts[:0]
	deletedKind := ""
	for i := range st.Accounts {
		if st.Accounts[i].ID == req.ID {
			deletedKind = st.Accounts[i].Kind
			continue
		}
		remaining = append(remaining, st.Accounts[i])
	}
	if deletedKind == "" {
		return gen.MediaAccountDeleteResp{}, fmt.Errorf("media: account %q not found", req.ID)
	}
	st.Accounts = remaining
	if st.activeIDFor(deletedKind) == req.ID {
		fallbackID := ""
		for i := range st.Accounts {
			if st.Accounts[i].Kind == deletedKind {
				fallbackID = st.Accounts[i].ID
				break
			}
		}
		st.setActiveIDFor(deletedKind, fallbackID)
	}
	if err := a.saveStore(st); err != nil {
		return gen.MediaAccountDeleteResp{}, fmt.Errorf("media: save accounts: %w", err)
	}
	return gen.MediaAccountDeleteResp{}, nil
}

func (a *Actor) handleAccountActivate(_ actor.PureContext, req gen.MediaAccountActivateReq) (gen.MediaAccountActivateResp, error) {
	if req.ID == "" {
		return gen.MediaAccountActivateResp{}, fmt.Errorf("media: account id is required")
	}
	kind, err := mediaKind(req.Kind)
	if err != nil {
		return gen.MediaAccountActivateResp{}, err
	}
	if !accountKind(kind) {
		return gen.MediaAccountActivateResp{}, fmt.Errorf("media: kind %q does not support accounts; configure a provider-model binding instead", kind)
	}
	st, err := a.loadStore()
	if err != nil {
		return gen.MediaAccountActivateResp{}, fmt.Errorf("media: load accounts: %w", err)
	}
	for i := range st.Accounts {
		if st.Accounts[i].ID != req.ID {
			continue
		}
		if st.Accounts[i].Kind != kind {
			return gen.MediaAccountActivateResp{}, fmt.Errorf("media: account %q is not an %s account", req.ID, kind)
		}
		st.setActiveIDFor(kind, req.ID)
		// Account activation takes over from a provider-model binding (mutual
		// exclusion: one active selection per Kind).
		st.clearBoundFor(kind)
		if err := a.saveStore(st); err != nil {
			return gen.MediaAccountActivateResp{}, fmt.Errorf("media: save accounts: %w", err)
		}
		return gen.MediaAccountActivateResp{ActiveID: req.ID}, nil
	}
	return gen.MediaAccountActivateResp{}, fmt.Errorf("media: account %q not found", req.ID)
}

// handleProviderModelSet activates or clears the per-Kind provider-model
// binding used by the agent's aggregator fallback. Each (Provider, Model) pair
// is a distinct selectable entry — the same model on two providers yields two
// independent rows. Activating a binding deactivates the Kind's media account
// (mutual exclusion); empty Provider/Model clears the binding only.
func (a *Actor) handleProviderModelSet(_ actor.PureContext, req gen.MediaProviderModelSetReq) (gen.MediaProviderModelSetResp, error) {
	kind, err := mediaKind(req.Kind)
	if err != nil {
		return gen.MediaProviderModelSetResp{}, err
	}
	if (req.Provider == "") != (req.Model == "") {
		return gen.MediaProviderModelSetResp{}, fmt.Errorf("media: provider and model must be set together")
	}
	if req.Aggregator != "" && kind != "vision" {
		return gen.MediaProviderModelSetResp{}, fmt.Errorf("media: aggregator binding is only supported for kind %q", "vision")
	}
	if req.Aggregator != "" && req.Provider != "" {
		return gen.MediaProviderModelSetResp{}, fmt.Errorf("media: aggregator and provider-model bindings are mutually exclusive")
	}
	st, err := a.loadStore()
	if err != nil {
		return gen.MediaProviderModelSetResp{}, fmt.Errorf("media: load accounts: %w", err)
	}
	switch {
	case req.Aggregator != "":
		st.VisionAggregatorID = req.Aggregator
		st.BoundVision = nil
	case req.Provider != "":
		st.setBoundFor(kind, req.Provider, req.Model)
		st.VisionAggregatorID = ""
		// Binding activation takes over from an active account (mutual
		// exclusion: one active selection per Kind).
		st.setActiveIDFor(kind, "")
	default:
		st.clearBoundFor(kind)
		st.VisionAggregatorID = ""
	}
	if err := a.saveStore(st); err != nil {
		return gen.MediaProviderModelSetResp{}, fmt.Errorf("media: save accounts: %w", err)
	}
	resp := gen.MediaProviderModelSetResp{Kind: kind, Aggregator: st.VisionAggregatorID}
	if b := st.boundFor(kind); b != nil {
		resp.Provider = b.Provider
		resp.Model = b.Model
	}
	return resp, nil
}

func (a *Actor) handleActiveAccount(ctx actor.PureContext, req gen.MediaActiveAccountReq) (gen.MediaActiveAccountResp, error) {
	kind, err := mediaKind(req.Kind)
	if err != nil {
		return gen.MediaActiveAccountResp{}, err
	}
	acc, accErr := a.activeAccount(kind)
	if accErr != nil {
		// No active media account: surface the provider-model binding (if
		// set) instead of failing, so the agent can route the aggregator
		// fallback to the user's selected (Provider, Model) pair.
		st, stErr := a.loadStore()
		if stErr != nil {
			return gen.MediaActiveAccountResp{}, stErr
		}
		if b := st.boundFor(kind); b != nil {
			return gen.MediaActiveAccountResp{BoundProvider: b.Provider, BoundModel: b.Model}, nil
		}
		if kind == "vision" && st.VisionAggregatorID != "" {
			return gen.MediaActiveAccountResp{BoundAggregator: st.VisionAggregatorID}, nil
		}
		return gen.MediaActiveAccountResp{}, accErr
	}
	// Account key first; when it is empty, reuse the aimanager token of the
	// LLM provider sharing the same endpoint domain. The resolved token is
	// returned transiently in this response and never written to the store.
	if acc.APIKey == "" {
		token, resolveErr := a.resolveProviderToken(ctx, acc.Provider, acc.BaseURL)
		if resolveErr != nil {
			return gen.MediaActiveAccountResp{}, fmt.Errorf("media: active %s account %q has no API key and aimanager fallback failed: %w", acc.Kind, acc.Name, resolveErr)
		}
		acc.APIKey = token
	}
	return gen.MediaActiveAccountResp{Account: acc}, nil
}

// ---------------------------------------------------------------------------
// Credential resolution
// ---------------------------------------------------------------------------

// aimanagerTokenResolveTimeout bounds the aimanager credential fallback call.
const aimanagerTokenResolveTimeout = 10 * time.Second

// resolveProviderToken fetches the API key from aimanager for the LLM provider
// that shares the same endpoint domain as the media account. It returns an
// error when the aimanager service is unavailable or no matching provider has
// a token.
func (a *Actor) resolveProviderToken(ctx actor.PureContext, providerID, baseURL string) (string, error) {
	aimanagerRef, ok := ctx.LookupService("aimanager")
	if !ok {
		return "", fmt.Errorf("aimanager service not available")
	}

	// List all providers to find one matching this media account's domain.
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), aimanagerTokenResolveTimeout)
	defer cancel()

	listCall := aimanagerRef.Invoke(callCtx, "aimanager.provider_full_list", struct{}{})
	listResult, err := listCall.Final(callCtx)
	if err != nil {
		return "", fmt.Errorf("list providers: %w", err)
	}

	var providers []gen.Provider
	switch r := listResult.(type) {
	case gen.ProviderListResp:
		providers = r.Items
	case *gen.ProviderListResp:
		if r != nil {
			providers = r.Items
		}
	}

	matchDomain := providerDomain(providerID, baseURL)
	if matchDomain == "" {
		return "", fmt.Errorf("no LLM provider mapping for media provider %q", providerID)
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

// providerDomain maps a media provider ID (and its configured base URL) to the
// LLM provider endpoint domain that shares the same API key. The account's own
// base URL wins when set because that is where generation requests actually
// go; without one the well-known public endpoint of the provider is used.
func providerDomain(providerID, baseURL string) string {
	if host := urlHost(baseURL); host != "" {
		return host
	}
	switch providerID {
	case "openai", "openai_custom":
		return "api.openai.com"
	case "gemini":
		return "generativelanguage.googleapis.com"
	case "minimax":
		return "api.minimaxi.com"
	case "glm":
		return "bigmodel.cn"
	case "ark", "doubao", "imagex":
		// Ark shares the aimanager key of the volcengine.com (mainland) /
		// bytepluses.com (overseas) LLM providers, like every other domain.
		// "doubao" is the media panel's brand-facing id for the same backend
		// (Doubao/Seedance 2.0 rides the Ark contents/generations API), and
		// Doubao image generation rides the same Ark platform too.
		return "volcengine.com"
	default:
		return ""
	}
}

// urlHost extracts the hostname from a provider base URL, tolerating missing
// schemes. Returns "" for empty or unparsable input.
func urlHost(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	if !strings.Contains(rawURL, "://") {
		rawURL = "https://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// matchEndpoint checks if an LLM provider endpoint contains the expected domain.
func matchEndpoint(endpoint, domain string) bool {
	return strings.Contains(endpoint, domain)
}
