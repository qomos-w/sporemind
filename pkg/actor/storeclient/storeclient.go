package storeclient

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

const (
	defaultBaseURL = "http://localhost:8080"
	httpTimeout    = 60 * time.Second  // index/config fetches
	// downloadTimeout bounds large artifact transfers. Downloads run to
	// 100+ MB over slow links; the snappy httpTimeout measured live as too
	// tight for a 13.5 MB package on a ~200 KB/s international link.
	downloadTimeout = 15 * time.Minute
	// maxDownloadBytes caps index/tarball/artifact downloads (256 MB — well
	// above any plugin package, far below memory danger).
	maxDownloadBytes = 256 << 20

	laneNet     = "store_net"     // index fetches (HTTP, seconds)
	laneInstall = "store_install" // downloads, builds, cross-actor installs
)

// Actor is the sporecloud plugin-store client. It owns the store connection
// config (persisted), the index TTL cache, and the verified install flows.
type Actor struct {
	actor.Host

	store   persist.Persist
	actorID string
	http    *http.Client

	mu    sync.RWMutex
	state storeClientState
	index cachedIndex
}

// storeClientState is the persisted shape: store connection config.
type storeClientState struct {
	BaseURL          string `json:"baseUrl"`
	Channel          string `json:"channel"`
	ProductionPubKey string `json:"productionPubKey,omitempty"`
	AuthToken        string `json:"authToken,omitempty"`
}

// Type returns the actor type identifier.
func (a *Actor) Type() string { return "storeclient" }

// OnInit loads persisted config and wires the HTTP client.
func (a *Actor) OnInit(ctx actor.Context) error {
	var err error
	a.store, err = persist.New(config.PersistConfig("storeclient"))
	if err != nil {
		return err
	}
	a.actorID = ctx.Self().ID().String()
	a.http = &http.Client{Timeout: httpTimeout}
	if err := a.Load(); err != nil {
		ctx.Logger().Error("storeclient: load failed", "err", err)
	}
	if a.state.BaseURL == "" {
		a.state.BaseURL = defaultBaseURL
	}
	if a.state.Channel == "" {
		a.state.Channel = "dev"
	}
	return nil
}

// OnStart registers the callable surface. index runs on its own network
// lane, install on a dedicated build lane (both do IO far above owner-lane
// budgets); config accessors are in-memory fast paths on the owner lane.
func (a *Actor) OnStart(ctx actor.Context) error {
	if err := ctx.RegisterLoop(laneNet, actor.ModeStateful); err != nil {
		return fmt.Errorf("storeclient: register %s loop: %w", laneNet, err)
	}
	if err := ctx.RegisterLoop(laneInstall, actor.ModeStateful); err != nil {
		return fmt.Errorf("storeclient: register %s loop: %w", laneInstall, err)
	}
	if err := ctx.Register("storeclient.index", a.handleIndex, actor.Public(),
		actor.WithLoop(laneNet),
		actor.WithDescription("Fetch the sporecloud plugin-store index (60s TTL cache) and return store + community entries annotated with local compatibility (protocol/sdk/host-version checks)."),
	); err != nil {
		return fmt.Errorf("storeclient: register index: %w", err)
	}
	if err := ctx.Register("storeclient.install", a.handleInstall, actor.AdminOnly(),
		actor.WithLoop(laneInstall),
		actor.WithDescription("Install from the plugin store. Kind=\"store\": authenticated download → Ed25519 verify → AES-256-GCM decrypt → sha256 → appmanager.install_local. Kind=\"community\": commit-pinned GitHub tarball (cloud mirror first, codeload fallback, sha256 verified) → native build from source → register. Version optional for store (default: latest compatible)."),
	); err != nil {
		return fmt.Errorf("storeclient: register install: %w", err)
	}
	if err := ctx.Register("storeclient.config_get", a.handleConfigGet, actor.Public(),
		actor.WithDescription("Return the redacted store client config (base URL, channel, secret-presence flags)."),
	); err != nil {
		return fmt.Errorf("storeclient: register config_get: %w", err)
	}
	if err := ctx.Register("storeclient.config_set", a.handleConfigSet, actor.AdminOnly(),
		actor.WithDescription("Update store client config: BaseURL, Channel (dev|production), ProductionPubKey (hex Ed25519, required for production channel), AuthToken (bearer for authenticated downloads; empty falls back to the linked cloudaccount session token). Only provided fields change."),
	); err != nil {
		return fmt.Errorf("storeclient: register config_set: %w", err)
	}
	return ctx.RegisterDomain("storeclient").Expose()
}

// ---------------------------------------------------------------------------
// Persistence — implements persist.Persistent
// ---------------------------------------------------------------------------

// Load restores the persisted state into memory.
func (a *Actor) Load() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("storeclient"))
		if err != nil {
			return err
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return persist.LoadOrZero(a.store, a.actorID, &a.state)
}

// Save persists the in-memory state to disk.
func (a *Actor) Save() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.saveLocked()
}

func (a *Actor) saveLocked() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("storeclient"))
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

// ---------------------------------------------------------------------------
// Config helpers
// ---------------------------------------------------------------------------

func (a *Actor) baseURL() (string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	base := a.state.BaseURL
	if base == "" {
		return "", fmt.Errorf("storeclient: no base URL configured (storeclient.config_set)")
	}
	return base, nil
}

func (a *Actor) viewLocked() gen.StoreClientConfigView {
	return gen.StoreClientConfigView{
		BaseURL:             a.state.BaseURL,
		Channel:             a.state.Channel,
		HasAuthToken:        a.state.AuthToken != "",
		HasProductionPubKey: a.state.ProductionPubKey != "",
	}
}

func (a *Actor) handleConfigGet(_ actor.Context) (gen.StoreClientConfigGetResp, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return gen.StoreClientConfigGetResp{Config: a.viewLocked()}, nil
}

func (a *Actor) handleConfigSet(ctx actor.Context, req gen.StoreClientConfigSetReq) (gen.StoreClientConfigSetResp, error) {
	a.mu.Lock()
	if req.BaseURL != "" {
		a.state.BaseURL = req.BaseURL
	}
	if req.Channel != "" {
		channel := req.Channel
		if channel != "dev" && channel != "production" {
			a.mu.Unlock()
			return gen.StoreClientConfigSetResp{}, fmt.Errorf("storeclient: Channel must be dev|production, got %q", channel)
		}
		a.state.Channel = channel
	}
	if req.ProductionPubKey != "" {
		if _, err := parsePubKeyHex(req.ProductionPubKey); err != nil {
			a.mu.Unlock()
			return gen.StoreClientConfigSetResp{}, err
		}
		a.state.ProductionPubKey = req.ProductionPubKey
	}
	if req.AuthToken != "" {
		a.state.AuthToken = req.AuthToken
	}
	// Fail closed: switching to production without a key pins nothing.
	if a.state.Channel == "production" && a.state.ProductionPubKey == "" {
		a.mu.Unlock()
		return gen.StoreClientConfigSetResp{}, fmt.Errorf("storeclient: production channel requires ProductionPubKey (hex Ed25519)")
	}
	view := a.viewLocked()
	// Index cache is keyed to the old config; drop it.
	a.index = cachedIndex{}
	err := a.saveLocked()
	a.mu.Unlock()
	if err != nil {
		return gen.StoreClientConfigSetResp{}, fmt.Errorf("storeclient: save config: %w", err)
	}
	ctx.Logger().Info("storeclient: config updated", "channel", view.Channel)
	return gen.StoreClientConfigSetResp{Config: view}, nil
}

// ---------------------------------------------------------------------------
// Index
// ---------------------------------------------------------------------------

func (a *Actor) handleIndex(ctx actor.Context) (gen.StoreIndexResp, error) {
	wire, fetchedAt, fromCache, err := a.fetchIndex(ctx.Lifecycle(), false)
	if err != nil {
		return gen.StoreIndexResp{}, err
	}
	return gen.StoreIndexResp{
		GeneratedAt: wire.GeneratedAt,
		FetchedAt:   fetchedAt.UTC().Format(time.RFC3339),
		FromCache:   fromCache,
		Store:       indexViews(wire),
		Community:   communityViews(wire),
	}, nil
}
