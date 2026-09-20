package appmanager

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// backendHTTPAddr is the listen address pushed to every spawned subprocess
// plugin's OnLoad config: the SDK binds an ephemeral port on loopback and
// reports the bound address back, so the host never manages a port pool.
const backendHTTPAddr = "127.0.0.1:0"

// instanceSecretBytes is the size of a per-instance session cookie secret.
const instanceSecretBytes = 32

// newInstanceSecret returns a fresh random per-instance secret as lowercase
// hex (64 chars). The secret never leaves the host except inside the OnLoad
// config delivered to the plugin process, and it is regenerated on every
// artifact load, so a respawned (or reloaded) plugin invalidates every cookie
// minted against the previous instance.
func newInstanceSecret() (string, error) {
	b := make([]byte, instanceSecretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("appmanager: generate instance secret: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// backendLoadConfig is the wire shape of the OnLoad config the SDK consumes
// (sporemind-plugin-sdk LoadConfig). The SDK owns the contract; the host
// produces matching JSON. ProjectID is host-side only — the SDK ignores it,
// while the pluginhost reads it back (ArtifactLoads) to route the plugin's
// project.wiki_* host calls to its bound project.
type backendLoadConfig struct {
	HTTPAddr      string `json:"httpAddr,omitempty"`
	SessionSecret string `json:"sessionSecret,omitempty"`
	StaticDir     string `json:"staticDir,omitempty"`
	DataDir       string `json:"dataDir,omitempty"`
	ProjectID     string `json:"projectId,omitempty"`
}

// resolveBackendAppDir returns the app directory anchoring a native app's
// static root (SDK LoadConfig.StaticDir) and app.data grant (DataDir). An
// inventory-owned artifact (zip install) anchors to the app's materialized
// install directory (installedAppDir); everything else derives from the build
// layout (<appDir>/.sporecode/build/plugin-*). Unknown layouts yield "" so the
// SDK stays fail-closed instead of guessing a root.
func resolveBackendAppDir(appID, artifactPath string) string {
	if appID != "" && inventoryOwned(artifactPath) {
		// Guard the materialized path against a hostile app id: a cleaned dir
		// must stay under the inventory root, else fail closed.
		if dir := installedAppDir(appID); inventoryOwned(dir) {
			return dir
		}
		return ""
	}
	return appbinding.AppDirFromArtifactPath(artifactPath)
}

// appDataDirFor returns the app-private writable data directory granted when
// the manifest declares the app.data capability: <appDir>/.sporecode/appdata.
// Undeclared permissions, or an empty app dir (fail-closed resolution), yield
// "" — the grant is never a guessed location. The directory is created eagerly
// so the plugin can open embedded databases (e.g. goleveldb) in it at OnLoad.
// It is never HTTP-served (unlike <appDir>/media) and survives
// unload/reload/re-register (same semantics as the appstate store).
func appDataDirFor(manifest gen.AppManifest, appDir string) (string, error) {
	granted := false
	for _, p := range manifest.Permissions {
		if p == appbinding.CapAppData {
			granted = true
			break
		}
	}
	if !granted || appDir == "" {
		return "", nil
	}
	dir := filepath.Join(appDir, ".sporecode", "appdata")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("appmanager: mkdir appdata for %q: %w", manifest.ID, err)
	}
	return dir, nil
}

// withBackendLoadReq wraps a native artifact load request with the
// per-instance backend bootstrap: it attaches the OnLoad config asking the
// SDK to bind an ephemeral HTTP port and enable cookie auth, and returns the
// session secret so the caller can record it on the app record when the
// plugin confirms the listener (HttpAddr non-empty in the load response).
// Load failures must discard the secret.
//
// The secret is the record's EXISTING one whenever the app already carries
// one, minted fresh only when absent. An idempotent artifact load (same
// artifact already loaded) reuses the running process — the new OnLoadConfig
// is never delivered to it — so rotating the secret here would desync the
// record from the live plugin and the gateway proxy would mint tokens the
// listener rejects (every /plugin/{id} request 401). Keeping the secret
// stable for the process lifetime keeps record and plugin in lockstep; a true
// respawn (reload prepare, fresh load after unload clears the secret) still
// delivers whatever secret the record holds, so rotation happens only on
// genuine process replacement.
func (a *Actor) withBackendLoadReq(req gen.PluginArtifactLoadReq) (gen.PluginArtifactLoadReq, string, error) {
	secret := a.existingBackendSecret(req.Manifest.ID)
	if secret == "" {
		var err error
		secret, err = newInstanceSecret()
		if err != nil {
			return req, "", err
		}
	}
	appDir := resolveBackendAppDir(req.Manifest.ID, req.ArtifactPath)
	dataDir, err := appDataDirFor(req.Manifest, appDir)
	if err != nil {
		return req, "", err
	}
	projectID := a.boundProjectID(req.Manifest.ID)
	raw, err := json.Marshal(backendLoadConfig{HTTPAddr: backendHTTPAddr, SessionSecret: secret, StaticDir: appDir, DataDir: dataDir, ProjectID: projectID})
	if err != nil {
		return req, "", fmt.Errorf("appmanager: marshal backend load config: %w", err)
	}
	req.OnLoadConfig = raw
	return req, secret, nil
}

// boundProjectID reads the app record's persisted project binding (set by
// register_project/reload_project; empty for zip installs and pre-binding
// records).
func (a *Actor) boundProjectID(appID string) string {
	if appID == "" {
		return ""
	}
	var projectID string
	a.withMu(func() {
		projectID = strings.TrimSpace(a.Records[appID].ProjectID)
	})
	return projectID
}

// recordProjectBinding persists a non-empty project binding onto the app
// record (fill-first: an existing binding is never overwritten, and an empty
// projectID is a no-op so zip installs stay unbound).
func (a *Actor) recordProjectBinding(ctx actor.Context, appID, projectID string) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return
	}
	changed := false
	a.withMu(func() {
		rec, ok := a.Records[appID]
		if !ok || (rec.ProjectID != "" && rec.ProjectID != projectID) {
			return
		}
		if rec.ProjectID == projectID {
			return
		}
		rec.ProjectID = projectID
		a.Records[appID] = rec
		changed = true
	})
	if !changed {
		return
	}
	if err := a.Save(); err != nil && ctx != nil {
		ctx.Logger().Warn("appmanager: persist project binding failed", "app", appID, "error", err)
	}
}

// existingBackendSecret returns the app record's current session secret ("" when
// the app has no record or no committed backend). Reads under the actor lock so
// a load racing a concurrent commit sees the latest committed secret.
func (a *Actor) existingBackendSecret(appID string) string {
	if appID == "" {
		return ""
	}
	var secret string
	a.withMu(func() {
		secret = strings.TrimSpace(a.Records[appID].SessionSecret)
	})
	return secret
}

// withBackendPrepareReq is the reload-prepare twin of withBackendLoadReq: the
// candidate process respawns at prepare time, so it needs the same fresh
// secret + HTTP bind request (plus the record's project binding, so the
// reloaded plugin keeps its wiki.read routing anchor).
func (a *Actor) withBackendPrepareReq(req gen.PluginArtifactReloadPrepareReq) (gen.PluginArtifactReloadPrepareReq, string, error) {
	secret, err := newInstanceSecret()
	if err != nil {
		return req, "", err
	}
	appDir := resolveBackendAppDir(req.Manifest.ID, req.ArtifactPath)
	dataDir, err := appDataDirFor(req.Manifest, appDir)
	if err != nil {
		return req, "", err
	}
	projectID := a.boundProjectID(req.Manifest.ID)
	raw, err := json.Marshal(backendLoadConfig{HTTPAddr: backendHTTPAddr, SessionSecret: secret, StaticDir: appDir, DataDir: dataDir, ProjectID: projectID})
	if err != nil {
		return req, "", fmt.Errorf("appmanager: marshal backend load config: %w", err)
	}
	req.OnLoadConfig = raw
	return req, secret, nil
}

// backendReport carries the per-instance secret and the listener address of
// one artifact load, so registration flows that load the artifact BEFORE the
// app record exists can commit the backend state after doRegister lands the
// record. A zero report means no load happened (spore runtime) or the load
// reported no listener.
type backendReport struct {
	secret   string
	httpAddr string
}

// hasBackend reports whether the load confirmed a live HTTP listener.
func (r backendReport) hasBackend() bool { return r.httpAddr != "" }

// setBackendFromLoad applies a successful load's backend report to the record.
// httpAddr is the bound listener address from the plugin's OnLoad response;
// when empty (in-process transport, pre-HTTP SDK, auth/listener disabled) the
// backend fields are cleared and the secret is dropped — a secret that never
// reached a listener must never mint cookies. secret is the per-instance
// secret that was pushed with this load's OnLoad config.
func setBackendFromLoad(record *appRecord, secret, httpAddr string) {
	if httpAddr == "" {
		clearBackend(record)
		return
	}
	host, portStr, err := net.SplitHostPort(httpAddr)
	if err != nil {
		clearBackend(record)
		return
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil || port <= 0 {
		clearBackend(record)
		return
	}
	if host == "" || host == "::" {
		host = "127.0.0.1"
	}
	record.SessionSecret = secret
	record.BackendPort = port
	record.BackendUrl = "http://" + net.JoinHostPort(host, portStr)
}

// backendMatches reports whether the record's stored backend URL already
// points at the given listener addr (same normalization as
// setBackendFromLoad).
func backendMatches(record appRecord, httpAddr string) bool {
	host, portStr, err := net.SplitHostPort(httpAddr)
	if err != nil {
		return false
	}
	if host == "" || host == "::" {
		host = "127.0.0.1"
	}
	return record.BackendUrl == "http://"+net.JoinHostPort(host, portStr)
}

// clearBackend drops every backend field from the record: the plugin process
// (and with it the HTTP listener) is gone or was never started.
func clearBackend(record *appRecord) {
	record.SessionSecret = ""
	record.BackendPort = 0
	record.BackendUrl = ""
}

// commitBackend persists a backend report for an app whose record already
// exists (plugin_load / restore / reload-commit paths). Unknown apps and
// empty reports are ignored; an empty httpAddr clears the stored backend.
//
// The gateway bridge rides on the commit: a confirmed listener (re)attaches
// pluginhost's reverse proxy with a freshly minted gateway token; a clearing
// commit detaches it. Both are fire-and-forget tells that never block the
// commit.
func (a *Actor) commitBackend(ctx actor.Context, appID, secret, httpAddr string) {
	if appID == "" {
		return
	}
	var record appRecord
	var ok bool
	detach := false
	changed := false
	a.withMu(func() {
		// Read-modify-write in ONE critical section: the previous split pair
		// (read under one withMu, write back under another) lost concurrent
		// record writers active in the window — spawnChild's ActorID stamp
		// during a register was clobbered by the stale copy written back
		// here, persisting wedged "running" records with an empty ActorID.
		// setBackendFromLoad/clearBackend are pure record mutations, safe to
		// run under the lock.
		record, ok = a.Records[appID]
		if !ok {
			return
		}
		if httpAddr == "" {
			if record.BackendUrl == "" && record.SessionSecret == "" && record.BackendPort == 0 {
				return
			}
			clearBackend(&record)
			detach = true
		} else {
			setBackendFromLoad(&record, secret, httpAddr)
		}
		a.Records[appID] = record
		changed = true
	})
	if !ok || !changed {
		return
	}
	if err := a.Save(); err != nil {
		// Persistence failure degrades to a stale bootstrap URL, never a
		// wedged handler; the next load re-derives and re-persists.
	}
	if ctx == nil {
		return
	}
	if record.BackendUrl != "" {
		a.attachBackendProxy(ctx, appID, record)
	} else if detach {
		a.detachBackendProxy(ctx, appID)
	}
}

// gatewayProxySessionID is the fixed session id minted into the gateway token
// handed to pluginhost's reverse proxy. The proxy injects the token as the
// X-Spore-Gateway-Token header on every proxied request and the SDK's
// authorizeRequest verifies it through the same validSessionToken path as
// session cookies (T1).
const gatewayProxySessionID = "gateway-proxy"

// backendProxyAddr normalizes the record's bootstrap URL ("http://host:port",
// always constructed by setBackendFromLoad) to the bare host:port form
// pluginhost.proxy_attach expects.
func backendProxyAddr(backendURL string) string {
	if u, err := url.Parse(backendURL); err == nil && u.Host != "" {
		return u.Host
	}
	return strings.TrimPrefix(backendURL, "http://")
}

// attachBackendProxy installs (or replaces) pluginhost's reverse-proxy route
// for a freshly committed backend, minting the gateway token against the
// record's per-instance secret. Re-attach after a reload carries the new
// addr/token and replaces the old route (Router.Register semantics).
//
// Fire-and-forget Tell, mirroring reportPluginProblem: an unreachable
// pluginhost degrades the data path to the bridge, never the commit.
func (a *Actor) attachBackendProxy(ctx actor.PureContext, appID string, record appRecord) {
	pluginRef, ok := ctx.LookupService(pluginhostServiceName)
	if !ok || pluginRef == nil {
		slog.Warn("appmanager: pluginhost service not available; backend proxy not attached", "app", appID)
		return
	}
	req := gen.PluginProxyAttachReq{
		PluginID: appID,
		Addr:     backendProxyAddr(record.BackendUrl),
		Token:    mintSessionCookie(record.SessionSecret, gatewayProxySessionID),
	}
	call := pluginRef.Invoke(ctx.Lifecycle(), "pluginhost.proxy_attach", req)
	if call != nil {
		_ = call.Close()
	}
}

// detachBackendProxy removes pluginhost's reverse-proxy route for an app whose
// backend is gone (clearing commit, crash/stop report, unregister cascade).
// Idempotent on the pluginhost side; best-effort Tell here.
func (a *Actor) detachBackendProxy(ctx actor.PureContext, appID string) {
	if appID == "" {
		return
	}
	pluginRef, ok := ctx.LookupService(pluginhostServiceName)
	if !ok || pluginRef == nil {
		slog.Warn("appmanager: pluginhost service not available; backend proxy not detached", "app", appID)
		return
	}
	call := pluginRef.Invoke(ctx.Lifecycle(), "pluginhost.proxy_detach", gen.PluginProxyDetachReq{PluginID: appID})
	if call != nil {
		_ = call.Close()
	}
}

// mintSessionCookie builds the SDK session-cookie value for one session:
// "sessionId.hex(HMAC-SHA256(secret, sessionId))" — exactly what
// sporemind-plugin-sdk's MintSessionToken produces and validSessionToken
// verifies (the SDK hex-decodes the configured secret before HMACing, so the
// key here is the decoded bytes of the record's hex secret). Returns "" when
// no per-instance secret is configured (dual-track: the frontend keeps using
// the bridge token).
func mintSessionCookie(secretHex, sessionID string) string {
	if secretHex == "" || sessionID == "" {
		return ""
	}
	key, err := hex.DecodeString(secretHex)
	if err != nil || len(key) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(sessionID))
	return sessionID + "." + hex.EncodeToString(mac.Sum(nil))
}
