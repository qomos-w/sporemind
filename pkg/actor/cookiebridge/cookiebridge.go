// Package cookiebridge migrates Chrome login state into independent browser
// instances.
//
// Chrome v127+ app-bound encryption rules out third-party readers of the
// cookie DB; chrome.cookies inside an extension is the only sanctioned
// extraction channel. This actor hosts the receiving side:
//
//   - a loopback HTTP listener with three token-gated endpoints:
//     POST /push (extension uploads confirmed cookies),
//     GET /pending (extension polls what the agent asked for), and
//     /mcp (Streamable HTTP MCP server exposing import_site + status).
//   - the MCP tools convert pushed cookies and forward them to
//     browsermanager.import_cookies via an in-process invoke.
//
// Security contract: the loopback bearer token is minted once and persisted;
// cookie VALUES never appear in tool results, events or logs — results carry
// counts and domain names only.
package cookiebridge

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/policy"
)

const (
	shutdownTimeout = 5 * time.Second
	stateDocName    = "cookiebridge"
)

// Actor owns the cookie bridge listener and pairing token.
type Actor struct {
	actor.Host

	store persist.Persist

	mu           sync.Mutex
	pairingToken string
	listenerAddr string
	// Endpoint URLs, filled during OnStart and immutable while serving.
	mcpURL     string
	pushURL    string
	pendingURL string

	server   *http.Server
	serveErr chan error

	pending  *pendingRegistry
	importer CookieImporter
}

var _ persist.Persistent = (*Actor)(nil)

type bridgeState struct {
	PairingToken string `json:"pairingToken"`
}

func (a *Actor) Type() string { return "cookiebridge" }

func (a *Actor) OnInit(ctx actor.Context) error {
	if a.store == nil {
		store, err := persist.New(config.PersistConfig("cookiebridge"))
		if err != nil {
			return err
		}
		a.store = store
	}
	if err := a.Load(); err != nil {
		return err
	}
	if a.pending == nil {
		a.pending = newPendingRegistry()
	}
	if a.importer == nil {
		a.importer = &browserImporter{}
	}
	return nil
}

func (a *Actor) OnStart(ctx actor.Context) error {
	if err := ctx.Register("cookiebridge.pairing_info", a.handlePairingInfo, actor.AdminOnly(),
		actor.WithEffect(string(domain.EffectNone)),
		actor.WithDescription("Return the Cookie Bridge loopback endpoints (MCP URL, extension push/poll URLs) and pairing token. For pairing the Chrome extension and configuring the MCP server row; the token is a local secret — paste it only into the extension and the MCP server config."),
	); err != nil {
		return fmt.Errorf("cookiebridge: register pairing_info: %w", err)
	}
	if err := ctx.Register("cookiebridge.regen_token", a.handleRegenToken, actor.AdminOnly(),
		actor.WithEffect(string(domain.EffectReversible)),
		actor.WithDescription("Mint a fresh Cookie Bridge pairing token. The previous token stops working immediately; re-pair the Chrome extension and update the MCP server Authorization header afterwards."),
	); err != nil {
		return fmt.Errorf("cookiebridge: register regen_token: %w", err)
	}
	if err := ctx.RegisterDomain("cookiebridge").Expose(); err != nil {
		return fmt.Errorf("cookiebridge: expose: %w", err)
	}

	addr, err := resolveListenAddr()
	if err != nil {
		return err
	}
	if bm, ok := ctx.LookupService("browsermanager"); ok && bm != nil {
		if bi, ok := a.importer.(*browserImporter); ok {
			bi.setRef(bm)
		}
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("cookiebridge: listen on %s (set cookie_bridge_addr in sporemind.yaml to relocate): %w", addr, err)
	}
	a.mu.Lock()
	a.server = &http.Server{Handler: a.newMux(), ReadHeaderTimeout: 5 * time.Second}
	a.listenerAddr = ln.Addr().String()
	base := "http://" + a.listenerAddr
	a.mcpURL = base + "/mcp"
	a.pushURL = base + "/push"
	a.pendingURL = base + "/pending"
	srv := a.server
	a.mu.Unlock()
	a.serveErr = make(chan error, 1)
	go func() { a.serveErr <- srv.Serve(ln) }()
	ctx.Logger().Info("cookiebridge: listening", "addr", a.listenerAddr)
	return nil
}

// newMux assembles the full gated HTTP surface: /mcp (Streamable HTTP MCP
// server), /pending and /push (Chrome extension). Split from OnStart so tests
// can serve the same tree under httptest.
func (a *Actor) newMux() http.Handler {
	mcpSrv := a.buildMCPServer()
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil)
	mux := http.NewServeMux()
	mux.Handle("/mcp", a.gated(mcpHandler))
	mux.Handle("/pending", a.gatedFunc(a.handlePending))
	mux.Handle("/push", a.gatedFunc(a.handlePush))
	return mux
}

func (a *Actor) OnStop(ctx actor.Context) error {
	a.mu.Lock()
	srv := a.server
	a.server = nil
	a.mu.Unlock()
	if srv != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}
	return a.Save()
}

// Save persists the pairing token.
func (a *Actor) Save() error {
	a.mu.Lock()
	state := bridgeState{PairingToken: a.pairingToken}
	a.mu.Unlock()
	return a.store.Save(stateDocName, &state)
}

// Load restores the pairing token, minting and persisting one on first boot.
func (a *Actor) Load() error {
	var state bridgeState
	err := a.store.Load(stateDocName, &state)
	if errors.Is(err, persist.ErrNotExist) {
		err = nil
	}
	if err != nil {
		return fmt.Errorf("cookiebridge: load state: %w", err)
	}
	if state.PairingToken == "" {
		token, err := mintToken()
		if err != nil {
			return err
		}
		state.PairingToken = token
		if err := a.store.Save(stateDocName, &state); err != nil {
			return fmt.Errorf("cookiebridge: save initial state: %w", err)
		}
	}
	a.mu.Lock()
	a.pairingToken = state.PairingToken
	a.mu.Unlock()
	return nil
}

// handlePairingInfo is a stateless (PureContext) snapshot read: it returns the
// frozen loopback endpoints plus the current pairing token under a.mu, without
// mutating state, so it runs on the forked pure loop.
func (a *Actor) handlePairingInfo(ctx actor.PureContext, _ domain.CookieBridgePairingInfoReq) (domain.CookieBridgePairingInfoResp, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.CookieBridgePairingInfoResp{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	resp := domain.CookieBridgePairingInfoResp{
		McpURL:       a.mcpURL,
		PushURL:      a.pushURL,
		PendingURL:   a.pendingURL,
		PairingToken: a.pairingToken,
	}
	if _, portStr, err := net.SplitHostPort(a.listenerAddr); err == nil {
		var p int
		if _, err := fmt.Sscanf(portStr, "%d", &p); err == nil {
			resp.Port = int64(p)
		}
	}
	return resp, nil
}

func (a *Actor) handleRegenToken(ctx actor.Context, _ domain.CookieBridgeRegenTokenReq) (domain.CookieBridgeRegenTokenResp, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.CookieBridgeRegenTokenResp{}, err
	}
	token, err := mintToken()
	if err != nil {
		return domain.CookieBridgeRegenTokenResp{}, err
	}
	a.mu.Lock()
	a.pairingToken = token
	a.mu.Unlock()
	if err := a.Save(); err != nil {
		return domain.CookieBridgeRegenTokenResp{}, fmt.Errorf("cookiebridge.regen_token: %w", err)
	}
	return domain.CookieBridgeRegenTokenResp{PairingToken: token}, nil
}

// currentToken returns the active pairing token.
func (a *Actor) currentToken() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pairingToken
}

// authorize enforces the shared bearer token for /mcp, /push and /pending.
// The token may travel as an Authorization: Bearer header (MCP server row,
// extension), X-Pairing-Token header, or ?token= query (manual curl).
func (a *Actor) authorize(r *http.Request) bool {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		token = r.Header.Get("X-Pairing-Token")
	}
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	want := a.currentToken()
	if len(token) == 0 || len(want) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(want)) == 1
}

// gated wraps an http.Handler behind the pairing-token gate.
func (a *Actor) gated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.authorize(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// gatedFunc is the http.HandlerFunc flavor of gated.
func (a *Actor) gatedFunc(next http.HandlerFunc) http.Handler {
	return a.gated(next)
}

func mintToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("cookiebridge: mint token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// resolveListenAddr validates the configured address. The bridge must never
// leave the loopback interface: a LAN-bound cookie ingestion endpoint would
// expose every imported login to the local network.
func resolveListenAddr() (string, error) {
	addr := config.CookieBridgeAddr()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("cookiebridge: invalid cookie_bridge_addr %q: %w", addr, err)
	}
	if host == "localhost" {
		return addr, nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("cookiebridge: cookie_bridge_addr %q must bind loopback", addr)
	}
	return addr, nil
}
