// Package desktop provides the desktop application implementation.
package desktop

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/qomos-w/sporemind/pkg/config"
)

// ── Constants ───────────────────────────────────────────────────────────────

// cloudLoginTokenEvent is the event name emitted when the callback server
// receives a valid token.
const cloudLoginTokenEvent = "cloud-login:token"

// loginCallbackParamName is the query parameter name that carries the callback
// payload from the cloud side (desktop_callback redirect).
const loginCallbackParamName = "desktop_callback"

// sporemindLoginScheme is the custom URL scheme used as a fallback when the
// loopback callback server is unavailable.
const sporemindLoginScheme = "sporemind://"

// loginCallbackTimeout is the maximum time the local callback server waits
// for the OAuth redirect before shutting itself down (the user may not come
// back from the browser).
const loginCallbackTimeout = 5 * time.Minute

// loginCallbackSuccessHTML is returned to the browser when the OAuth callback
// succeeds. It tells the user to return to the desktop app.
const loginCallbackSuccessHTML = `<!DOCTYPE html><html><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>body{margin:0;display:flex;align-items:center;justify-content:center;
height:100vh;font-family:system-ui,sans-serif;background:#0d1117;color:#e6edf3}
.card{text-align:center;padding:2rem}h1{font-size:1.1rem;margin:0 0 .5rem}
p{color:#8b949e;margin:0}</style></head>
<body><div class="card"><h1>✓ 登录成功</h1>
<p>正在关联 sporemind 账户，可以关闭此窗口。</p></div></body></html>`

// loginCallbackErrorHTML is returned to the browser when the OAuth callback
// is missing required fields.
const loginCallbackErrorHTML = `<!DOCTYPE html><html><head><meta charset="utf-8">
<style>body{margin:0;display:flex;align-items:center;justify-content:center;
height:100vh;font-family:system-ui,sans-serif;background:#0d1117;color:#f85149}
.card{text-align:center;padding:2rem}</style></head>
<body><div class="card"><h1>登录回调无效</h1>
<p>缺少必要的 token 字段，请重试。</p></div></body></html>`

// loginTokenFields carries the OAuth token pair returned by the sporemind
// cloud after a successful login in the system browser. JSON tag names are the
// snake_case API names; the frontend maps them to the codegen PascalCase
// CloudAccountLinkReq before calling cloudaccount.link.
type loginTokenFields struct {
	AccountID    string `json:"account_id"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	DisplayName  string `json:"display_name,omitempty"`
	AvatarURL    string `json:"avatar_url,omitempty"`
}

// ── OpenSystemLogin ─────────────────────────────────────────────────────────

// OpenSystemLogin opens the sporemind login page in the system default
// browser and starts the local callback server (if not already running) to
// receive the OAuth redirect. An empty loginURL opens the configured sporemind
// login page (config.SporemindLoginURL). It returns an error if the callback
// server cannot be started or the browser cannot be opened.
func (a *App) OpenSystemLogin(loginURL string) error {
	if a.app == nil {
		return fmt.Errorf("desktop: app not started")
	}
	if err := a.ensureLoginCallbackServer(); err != nil {
		return fmt.Errorf("desktop: start login callback server: %w", err)
	}
	return a.app.Browser.OpenURL(a.finalizeLoginURL(loginURL))
}

// ensureLoginCallbackServer starts the local callback HTTP server on a random
// 127.0.0.1 port if one is not already running. It is safe to call
// concurrently.
func (a *App) ensureLoginCallbackServer() error {
	a.loginCallbackMu.Lock()
	defer a.loginCallbackMu.Unlock()
	return a.ensureLoginCallbackServerLocked()
}

// ensureLoginCallbackServerLocked is the locked core of
// ensureLoginCallbackServer. Caller must hold loginCallbackMu.
func (a *App) ensureLoginCallbackServerLocked() error {
	if a.loginCallbackServer != nil {
		// Server already running — refresh the auto-stop timer.
		a.resetLoginCallbackTimerLocked()
		return nil
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen loopback: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", a.handleLoginCallbackHTTP)
	mux.HandleFunc("/", a.handleLoginCallbackHTTP) // fallback for any path

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	a.loginCallbackServer = server
	a.loginCallbackURL = fmt.Sprintf("http://127.0.0.1:%d/callback", listener.Addr().(*net.TCPAddr).Port)
	a.loginCallbackStartedAt = time.Now()

	// Schedule auto-stop after timeout.
	a.resetLoginCallbackTimerLocked()

	// Start serving in background.
	go func() {
		// Server Shutdown will close the listener, causing Serve to return
		// http.ErrServerClosed.
		_ = server.Serve(listener)
	}()

	return nil
}

// resetLoginCallbackTimerLocked resets the auto-stop timer for the callback
// server. Caller must hold loginCallbackMu.
func (a *App) resetLoginCallbackTimerLocked() {
	if a.loginCallbackTimer != nil {
		a.loginCallbackTimer.Stop()
	}
	// Determine the timeout duration.
	timeout := a.loginCallbackTimeout
	if timeout <= 0 {
		timeout = loginCallbackTimeout
	}
	// Capture the timer function seam for testability.
	timerFn := a.loginCallbackTimerFn
	if timerFn == nil {
		timerFn = time.AfterFunc
	}
	a.loginCallbackTimer = timerFn(timeout, func() {
		a.loginCallbackMu.Lock()
		a.stopLoginCallbackServerLocked()
		a.loginCallbackMu.Unlock()
	})
}

// handleLoginCallbackHTTP is the local callback endpoint. The cloud redirects
// here after a successful login with the token fields in the query. It emits
// cloud-login:token so the frontend can call cloudaccount.link, then responds
// with a simple success page shown in the browser.
func (a *App) handleLoginCallbackHTTP(w http.ResponseWriter, r *http.Request) {
	f, ok := parseLoginCallback(r.URL.Query())
	if !ok {
		// Try reading the body for POST callbacks.
		if r.Method == "POST" {
			body, err := io.ReadAll(r.Body)
			if err == nil {
				if q, err := url.ParseQuery(string(body)); err == nil {
					f, ok = parseLoginCallback(q)
				}
			}
		}
	}
	if !ok {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintln(w, loginCallbackErrorHTML)
		return
	}

	a.emitLoginToken(f)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintln(w, loginCallbackSuccessHTML)

	// Stop the server after receiving a valid token.
	a.loginCallbackMu.Lock()
	a.stopLoginCallbackServerLocked()
	a.loginCallbackMu.Unlock()
}

// stopLoginCallbackServerLocked shuts down the callback server and clears the
// state. Caller must hold loginCallbackMu. Safe to call multiple times and
// from the auto-stop timer callback.
func (a *App) stopLoginCallbackServerLocked() {
	if a.loginCallbackTimer != nil {
		a.loginCallbackTimer.Stop()
		a.loginCallbackTimer = nil
	}
	if a.loginCallbackServer != nil {
		srv := a.loginCallbackServer
		a.loginCallbackServer = nil
		a.loginCallbackURL = ""
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		go func() { _ = srv.Shutdown(ctx) }()
		return
	}
	a.loginCallbackURL = ""
	a.loginCallbackStartedAt = time.Time{}
}

// finalizeLoginURL appends the local callback URL to the login URL so the
// cloud side knows where to redirect after OAuth. An empty loginURL falls
// back to the configured sporemind login page.
func (a *App) finalizeLoginURL(loginURL string) string {
	if loginURL == "" {
		loginURL = config.SporemindLoginURL()
	}
	if strings.Contains(loginURL, "?") {
		return loginURL + "&" + loginCallbackParamName + "=" + url.QueryEscape(a.loginCallbackURL)
	}
	return loginURL + "?" + loginCallbackParamName + "=" + url.QueryEscape(a.loginCallbackURL)
}

// emitLoginToken forwards the parsed token to the frontend. The frontend maps
// the fields and calls cloudaccount.link.
func (a *App) emitLoginToken(f loginTokenFields) {
	if a.app == nil {
		return
	}
	a.app.Event.Emit(cloudLoginTokenEvent, f)
}

// ── parseLoginCallback ──────────────────────────────────────────────────────

// parseLoginCallback extracts the login token fields from the callback query.
// It accepts snake_case and camelCase field names.
func parseLoginCallback(q url.Values) (loginTokenFields, bool) {
	get := func(keys ...string) string {
		for _, k := range keys {
			if v := q.Get(k); v != "" {
				return v
			}
		}
		return ""
	}
	f := loginTokenFields{
		AccountID:    get("account_id", "accountId"),
		AccessToken:  get("access_token", "accessToken", "token"),
		RefreshToken: get("refresh_token", "refreshToken"),
		DisplayName:  get("display_name", "displayName", "name"),
		AvatarURL:    get("avatar_url", "avatarUrl", "avatar"),
	}
	if v := get("expires_in", "expiresIn"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.ExpiresIn = n
		}
	}
	if f.AccountID == "" || f.AccessToken == "" || f.RefreshToken == "" {
		return f, false
	}
	return f, true
}

// ── loginCallbackTimedOut (pure, time-parameterised) ────────────────────────

// loginCallbackTimedOut reports whether the callback server started at
// startedAt has exceeded timeout by now. It is a pure function so the
// timeout logic is unit-testable without a real timer.
func loginCallbackTimedOut(startedAt, now time.Time, timeout time.Duration) bool {
	return !now.Before(startedAt.Add(timeout))
}
