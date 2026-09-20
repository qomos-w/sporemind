// Package cloudaccount implements the desktop-runtime actor that links the
// local sporemind instance to a sporemind cloud account.
//
// It stores the OAuth access/refresh tokens and a cached entitlements snapshot
// in its persist state (following the user actor pattern), proactively
// refreshes the access token before it expires, periodically re-fetches
// entitlements, and degrades to a free-tier fallback (core features still
// enabled) when the cache is stale or the cloud is unreachable.
//
// Callable surface (schema-first, see schemas/cloudaccount._2900.spore):
//   - cloudaccount.link             store tokens from the OAuth flow
//   - cloudaccount.unlink           drop the local linkage
//   - cloudaccount.status           link status (tokens never exposed)
//   - cloudaccount.get_entitlements cached entitlements with TTL fallback
//
// The refresh-decision and TTL-degradation logic live in refresh.go and
// entitlements.go as pure functions (time is a parameter) so they can be unit
// tested without HTTP servers or actor assembly.
package cloudaccount

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/buildinfo"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// refreshInterval is how often the background loop wakes to refresh the token
// and re-fetch entitlements. Short enough that a 15-minute access token is
// always refreshed within its 2-minute skew window.
const refreshInterval = 1 * time.Minute

// httpTimeout caps each cloud HTTP call.
const httpTimeout = 15 * time.Second

// Actor manages the local sporemind cloud-account linkage.
type Actor struct {
	actor.Host
	store persist.Persist
	// stateLoaded is set once Load() has completed (first start included);
	// OnStop skips the save before that (gospore ForceCleanup may stop us
	// mid-OnInit, and saving the zero-value state would wipe the record).
	stateLoaded atomic.Bool

	mu      sync.RWMutex
	actorID string
	state   CloudAccountState

	// Cloud integration. baseURL + fetcher/refresher default to the real HTTP
	// client in OnInit; tests inject fakes before the actor starts.
	baseURL   string
	fetcher   entitlementsFetcher
	refresher tokenRefresher

	// Content store (M4-B). contentFetcher proxies the cloud /store/content
	// API; dispatcher routes installs to the runtime installer. Both default
	// to real implementations in OnInit; tests inject fakes.
	contentFetcher contentFetcher
	dispatcher     contentDispatcher

	// CDKey redemption. redeemer proxies the cloud /cdkeys/redeem API.
	// Defaults to the real HTTP client in OnInit; tests inject fakes.
	redeemer cdkeyRedeemer

	actorCtx actor.Context
	bgCancel context.CancelFunc
	bgDone   chan struct{}
}

var _ persist.Persistent = (*Actor)(nil)

// NewActor returns a cloud-account actor using the configured cloud API URL.
func NewActor() *Actor {
	return &Actor{
		baseURL: config.CloudAPIURL(),
	}
}

// Type returns the actor type identifier.
func (a *Actor) Type() string { return "cloudaccount" }

// OnInit initializes the persist store, wires the cloud HTTP client, and loads
// persisted state.
func (a *Actor) OnInit(ctx actor.Context) error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("cloudaccount"))
		if err != nil {
			return err
		}
	}
	a.actorID = ctx.Self().ID().String()
	if a.baseURL == "" {
		a.baseURL = config.CloudAPIURL()
	}
	if a.fetcher == nil || a.refresher == nil {
		hc := &cloudHTTPClient{baseURL: a.baseURL, client: &http.Client{Timeout: httpTimeout}}
		a.fetcher = hc
		a.refresher = hc
	}
	if a.contentFetcher == nil {
		hc := &cloudHTTPClient{baseURL: a.baseURL, client: &http.Client{Timeout: httpTimeout}}
		a.contentFetcher = hc
	}
	if a.redeemer == nil {
		a.redeemer = &cloudHTTPClient{baseURL: a.baseURL, client: &http.Client{Timeout: httpTimeout}}
	}
	if a.dispatcher == nil {
		a.dispatcher = runtimeDispatcher{}
	}
	if err := a.Load(); err != nil {
		ctx.Logger().Error("cloudaccount: load state failed", "error", err)
	}
	return nil
}

// OnStart registers the callable surface and starts the background sync loop.
func (a *Actor) OnStart(ctx actor.Context) error {
	a.actorCtx = ctx
	ctx.Logger().Info("cloudaccount: starting", "id", a.actorID, "linked", a.state.Linked())

	// cloudaccount.sync dials the cloud (seconds-scale HTTP) and must not run
	// on the shared stateful lane; route it to a dedicated loop.
	if err := ctx.RegisterLoop("cloud_sync", actor.ModeStateful); err != nil {
		return fmt.Errorf("cloudaccount: register loop cloud_sync: %w", err)
	}

	if err := ctx.Register("cloudaccount.link", a.handleLink, actor.Public()); err != nil {
		return fmt.Errorf("cloudaccount: register link: %w", err)
	}
	if err := ctx.Register("cloudaccount.unlink", a.handleUnlink, actor.Public()); err != nil {
		return fmt.Errorf("cloudaccount: register unlink: %w", err)
	}
	if err := ctx.Register("cloudaccount.status", a.handleStatus, actor.Public()); err != nil {
		return fmt.Errorf("cloudaccount: register status: %w", err)
	}
	if err := ctx.Register("cloudaccount.sync", a.handleSync, actor.Public(), actor.WithLoop("cloud_sync"), actor.WithDescription("Force an immediate token-refresh + entitlements re-fetch from the cloud and return the resulting status")); err != nil {
		return fmt.Errorf("cloudaccount: register sync: %w", err)
	}
	if err := ctx.Register("cloudaccount.session_token", a.handleSessionToken, actor.AdminOnly(), actor.WithDescription("Return a fresh cloud access token for host-internal callers (e.g. plugin-store downloads). Refreshes first when near expiry; empty token when no account is linked.")); err != nil {
		return fmt.Errorf("cloudaccount: register session_token: %w", err)
	}
	if err := ctx.Register("cloudaccount.get_entitlements", a.handleGetEntitlements, actor.Public()); err != nil {
		return fmt.Errorf("cloudaccount: register get_entitlements: %w", err)
	}
	if err := ctx.Register("cloudaccount.content_search", a.handleContentSearch, actor.Public()); err != nil {
		return fmt.Errorf("cloudaccount: register content_search: %w", err)
	}
	if err := ctx.Register("cloudaccount.content_detail", a.handleContentDetail, actor.Public()); err != nil {
		return fmt.Errorf("cloudaccount: register content_detail: %w", err)
	}
	if err := ctx.Register("cloudaccount.content_install", a.handleContentInstall, actor.Public()); err != nil {
		return fmt.Errorf("cloudaccount: register content_install: %w", err)
	}
	if err := ctx.Register("cloudaccount.redeem", a.handleRedeem, actor.Public()); err != nil {
		return fmt.Errorf("cloudaccount: register redeem: %w", err)
	}

	if err := ctx.RegisterDomain("cloudaccount").Expose(); err != nil {
		return fmt.Errorf("cloudaccount: expose service: %w", err)
	}

	// Background sync dials the cloud API on start and every refresh tick. A
	// dev build with no explicitly configured cloud URL must not open outbound
	// connections on its own (Windows firewall popup on first run); callables
	// still work and the loop starts on the first link.
	if !config.CloudURLConfigured() && !buildinfo.IsRelease() && !buildinfo.IsBeta() {
		ctx.Logger().Info("cloudaccount: background sync skipped (dev build, no cloud URL configured)")
		return nil
	}
	a.startBackground()
	return nil
}

// OnStop stops the background loop and persists state.
func (a *Actor) OnStop(_ actor.Context) error {
	a.stopBackground()
	if !a.stateLoaded.Load() {
		// Stopped mid-OnInit (gospore ForceCleanup): skip the wipe-prone save.
		return nil
	}
	return a.Save()
}

// ---------------------------------------------------------------------------
// persist.Persistent
// ---------------------------------------------------------------------------

// Save persists the actor state. Caller must NOT hold a.mu.
func (a *Actor) Save() error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.saveLocked()
}

// saveLocked persists state. Caller MUST hold a.mu (read or write) so handlers
// that already took the write lock do not re-enter it.
func (a *Actor) saveLocked() error {
	if a.store == nil {
		return nil
	}
	return a.store.Save(a.actorID, a.state)
}

func (a *Actor) saveOrLog(ctx actor.PureContext) {
	if err := a.saveLocked(); err != nil {
		ctx.Logger().Error("cloudaccount: save state failed", "error", err)
	}
}

// Load restores persisted state.
func (a *Actor) Load() (err error) {
	// Mark state as load-complete only after a successful pass (first start
	// included) — the OnStop wipe guard depends on it.
	defer func() {
		if err == nil {
			a.stateLoaded.Store(true)
		}
	}()
	var s CloudAccountState
	if err := persist.LoadOrZero(a.store, a.actorID, &s); err != nil {
		return err
	}
	a.mu.Lock()
	a.state = s
	a.mu.Unlock()
	return nil
}

// ---------------------------------------------------------------------------
// Background sync loop
// ---------------------------------------------------------------------------

// startBackground launches the periodic token-refresh + entitlements-fetch
// loop. It performs an initial sync immediately so a freshly-linked account
// (or one restored from persist) populates its cache without waiting a tick.
func (a *Actor) startBackground() {
	bgCtx, cancel := context.WithCancel(context.Background())
	a.bgCancel = cancel
	a.bgDone = make(chan struct{})
	go a.loop(bgCtx)
}

func (a *Actor) stopBackground() {
	if a.bgCancel != nil {
		a.bgCancel()
		a.bgCancel = nil
	}
	if a.bgDone != nil {
		<-a.bgDone
		a.bgDone = nil
	}
}

func (a *Actor) loop(ctx context.Context) {
	defer close(a.bgDone)
	a.syncOnce(ctx)
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.syncOnce(ctx)
		}
	}
}

// syncOnce performs one refresh cycle: proactively rotate the access token if
// it is near expiry, then re-fetch entitlements to keep the cache fresh.
// All errors are logged and swallowed — a transient cloud failure leaves the
// existing cache intact, and a stale cache degrades to the free-tier fallback.
func (a *Actor) syncOnce(ctx context.Context) {
	a.mu.RLock()
	linked := a.state.Linked()
	expiresAt := a.state.TokenExpiresAt
	accessToken := a.state.AccessToken
	refreshToken := a.state.RefreshToken
	a.mu.RUnlock()
	if !linked {
		return
	}
	logger := a.logger()

	// Proactive token refresh.
	if ShouldRefreshToken(expiresAt, time.Now(), RefreshSkew) {
		pair, err := a.refresher.Refresh(ctx, refreshToken)
		if err != nil {
			if logger != nil {
				logger.Error("cloudaccount: token refresh failed", "error", err)
			}
		} else {
			a.mu.Lock()
			a.state.AccessToken = pair.AccessToken
			a.state.RefreshToken = pair.RefreshToken
			a.state.TokenExpiresAt = pair.ExpiresAt
			accessToken = pair.AccessToken
			if err := a.saveLocked(); err != nil && logger != nil {
				logger.Error("cloudaccount: save after refresh failed", "error", err)
			}
			a.mu.Unlock()
		}
	}

	// Entitlements refresh.
	snap, err := a.fetcher.Fetch(ctx, accessToken)
	if err != nil {
		if logger != nil {
			logger.Error("cloudaccount: entitlements fetch failed", "error", err)
		}
		return
	}
	a.mu.Lock()
	a.state.Entitlements = &snap
	a.state.EntitlementsFetchedAt = time.Now()
	if err := a.saveLocked(); err != nil && logger != nil {
		logger.Error("cloudaccount: save entitlements failed", "error", err)
	}
	a.mu.Unlock()
}

// logger returns the actor context logger if available, else nil. The
// background loop may run before actorCtx is fully usable, so nil is tolerated.
func (a *Actor) logger() interface {
	Info(msg string, args ...any)
	Error(msg string, args ...any)
} {
	if a.actorCtx == nil {
		return nil
	}
	return a.actorCtx.Logger()
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// handleLink stores the OAuth token pair and kicks off an immediate
// entitlements sync. Returns the resulting status.
func (a *Actor) handleLink(ctx actor.PureContext, req gen.CloudAccountLinkReq) (gen.CloudAccountStatus, error) {
	if req.AccountID == "" || req.AccessToken == "" || req.RefreshToken == "" {
		return gen.CloudAccountStatus{}, fmt.Errorf("cloudaccount.link: account_id, access_token and refresh_token are required")
	}
	now := time.Now()
	expiresAt := now
	if req.ExpiresIn > 0 {
		expiresAt = now.Add(time.Duration(req.ExpiresIn) * time.Second)
	}
	a.mu.Lock()
	a.state = CloudAccountState{
		AccountID:      req.AccountID,
		AccessToken:    req.AccessToken,
		RefreshToken:   req.RefreshToken,
		TokenExpiresAt: expiresAt,
		DisplayName:    req.DisplayName,
		AvatarURL:      req.AvatarURL,
		LinkedAt:       now,
	}
	a.saveOrLog(ctx)
	a.mu.Unlock()

	ctx.Logger().Info("cloudaccount: linked", "account", req.AccountID)

	// Dev builds may have skipped the background loop at OnStart (no cloud URL
	// configured); linking is an explicit user action, so start it now.
	if a.bgDone == nil {
		a.startBackground()
	}

	// Best-effort immediate sync so the cache is populated right away.
	a.syncOnce(context.Background())

	return a.statusSnapshot(), nil
}

// handleUnlink drops the local cloud-account linkage and clears cached state.
func (a *Actor) handleUnlink(ctx actor.PureContext) (gen.CloudAccountUnlinkResp, error) {
	a.mu.Lock()
	a.state = CloudAccountState{}
	a.saveOrLog(ctx)
	a.mu.Unlock()
	ctx.Logger().Info("cloudaccount: unlinked")
	return gen.CloudAccountUnlinkResp{}, nil
}

// handleStatus returns the current link status. Tokens are never included.
func (a *Actor) handleStatus(_ actor.PureContext) (gen.CloudAccountStatus, error) {
	return a.statusSnapshot(), nil
}

// handleSessionToken returns a fresh cloud access token for host-internal
// callers (storeclient authenticated downloads). Refreshes first when the
// token is near expiry. Registered AdminOnly — the token never crosses the
// public callable surface.
func (a *Actor) handleSessionToken(ctx actor.PureContext) (gen.CloudAccountSessionTokenResp, error) {
	a.mu.RLock()
	linked := a.state.Linked()
	a.mu.RUnlock()
	if !linked {
		return gen.CloudAccountSessionTokenResp{}, nil
	}
	if ShouldRefreshToken(a.state.TokenExpiresAt, time.Now(), RefreshSkew) {
		a.mu.RLock()
		refreshToken := a.state.RefreshToken
		a.mu.RUnlock()
		if pair, err := a.refresher.Refresh(ctx.Lifecycle(), refreshToken); err == nil {
			a.mu.Lock()
			a.state.AccessToken = pair.AccessToken
			a.state.RefreshToken = pair.RefreshToken
			a.state.TokenExpiresAt = pair.ExpiresAt
			_ = a.saveLocked()
			a.mu.Unlock()
		} else {
			ctx.Logger().Error("cloudaccount: session_token refresh failed", "error", err)
		}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	expires := ""
	if !a.state.TokenExpiresAt.IsZero() {
		expires = a.state.TokenExpiresAt.UTC().Format(time.RFC3339)
	}
	return gen.CloudAccountSessionTokenResp{AccessToken: a.state.AccessToken, ExpiresAt: expires}, nil
}

// handleSync forces an immediate token-refresh + entitlements re-fetch and
// returns the resulting status. It backs the UI's manual refresh action when
// the entitlements cache is stale (offline/degraded) so the user can recover
// without waiting for the background loop's next tick. Cloud errors are
// logged and swallowed by syncOnce: on failure the returned status stays
// degraded, which is itself the signal that the sync did not succeed.
func (a *Actor) handleSync(ctx actor.PureContext) (gen.CloudAccountStatus, error) {
	a.mu.RLock()
	linked := a.state.Linked()
	a.mu.RUnlock()
	if !linked {
		return gen.CloudAccountStatus{}, fmt.Errorf("cloudaccount.sync: no cloud account linked")
	}
	// Dev builds skip the background loop at OnStart when no cloud URL is
	// configured; a manual sync is an explicit user action, so make sure the
	// loop exists (syncOnce only populates the cache; the loop keeps it fresh).
	if a.bgDone == nil {
		a.startBackground()
	}
	a.syncOnce(ctx.Lifecycle())
	return a.statusSnapshot(), nil
}

// handleGetEntitlements returns the cached entitlements with TTL fallback. It
// is a cache read: the background loop keeps the cache fresh; a stale or
// missing cache degrades to the free-tier fallback (core features enabled).
func (a *Actor) handleGetEntitlements(_ actor.PureContext) (gen.CloudAccountGetEntitlementsResp, error) {
	a.mu.RLock()
	linked := a.state.Linked()
	cached := a.state.Entitlements
	fetchedAt := a.state.EntitlementsFetchedAt
	a.mu.RUnlock()

	if !linked {
		return gen.CloudAccountGetEntitlementsResp{}, fmt.Errorf("cloudaccount.get_entitlements: no cloud account linked")
	}

	snap, degraded := ResolveEntitlements(cached, fetchedAt, time.Now(), EntitlementsTTL)
	return gen.CloudAccountGetEntitlementsResp{
		Entitlements: toEntitlementsView(snap),
		Degraded:     degraded,
	}, nil
}

// ---------------------------------------------------------------------------
// projections
// ---------------------------------------------------------------------------

// statusSnapshot builds the wire status from the current state. Must NOT be
// called while holding a.mu.
func (a *Actor) statusSnapshot() gen.CloudAccountStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	s := a.state

	st := gen.CloudAccountStatus{
		Linked:      s.Linked(),
		AccountID:   s.AccountID,
		DisplayName: s.DisplayName,
		AvatarURL:   s.AvatarURL,
	}
	if s.Entitlements != nil {
		st.RateLimitTier = s.Entitlements.RateLimitTier
		st.IsExperimentalSubscriber = s.Entitlements.IsExperimentalSubscriber
	}
	// Degraded when the cache is stale/missing. Tier is taken from the
	// effective entitlements (cached or free-tier fallback) so callers see
	// "free" whenever the actor is running in degraded/offline mode.
	resolved, degraded := ResolveEntitlements(s.Entitlements, s.EntitlementsFetchedAt, time.Now(), EntitlementsTTL)
	st.Tier = resolved.Tier
	st.EntitlementsDegraded = degraded
	if !s.TokenExpiresAt.IsZero() {
		st.TokenExpiresAt = s.TokenExpiresAt.UTC().Format(time.RFC3339)
	}
	if !s.EntitlementsFetchedAt.IsZero() {
		st.EntitlementsFetchedAt = s.EntitlementsFetchedAt.UTC().Format(time.RFC3339)
	}
	return st
}

// toEntitlementsView projects the internal snapshot to the wire view. The two
// types are field-identical; this keeps persist state and protocol types
// separate (persist uses native types; protocol types are codegen-owned).
func toEntitlementsView(s EntitlementsSnapshot) gen.EntitlementsView {
	features := make(map[string]bool, len(s.Features))
	for k, v := range s.Features {
		features[k] = v
	}
	return gen.EntitlementsView{
		Features:                 features,
		RateLimitTier:            s.RateLimitTier,
		Tier:                     s.Tier,
		Pass:                     s.Pass,
		IsExperimentalSubscriber: s.IsExperimentalSubscriber,
		Credits:                  s.Credits,
	}
}
