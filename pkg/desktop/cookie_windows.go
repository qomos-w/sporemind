//go:build windows

package desktop

import (
	"errors"
	"fmt"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/webview2edge"
)

// exportCookiesForWindow enumerates all cookies from the WebView2 CookieManager
// associated with win and returns them grouped by domain.
//
// Every COM vtable call (manager acquisition, GetCookies initiation, cookie
// enumeration, Releases) is marshaled onto the app main thread via InvokeSync
// — WebView2 objects are apartment-affine and fault on cross-thread access.
// The only step that runs on the calling goroutine is waiting on the
// completion channel; the completion handler itself is invoked by the main
// message loop, so the main thread never blocks on itself.
func exportCookiesForWindow(win *application.WebviewWindow) (map[string][]domain.BrowserCookieEntry, error) {
	if win == nil {
		return nil, errors.New("desktop: browser window is nil")
	}
	cm, err := getCookieManagerWithRetry(win)
	if err != nil {
		return nil, err
	}
	releaseCM := func() {
		application.InvokeSync(func() { cm.Release() })
	}
	defer releaseCM()

	var (
		completion *edge.GetCookiesCompletion
		beginErr   error
	)
	application.InvokeSync(func() {
		completion, beginErr = cm.BeginGetCookies("")
	})
	if beginErr != nil {
		return nil, fmt.Errorf("desktop: GetCookies failed: %w", beginErr)
	}
	list, err := completion.Wait(10 * time.Second)
	if err != nil {
		return nil, fmt.Errorf("desktop: GetCookies failed: %w", err)
	}
	var (
		cookies map[string][]domain.BrowserCookieEntry
		opErr   error
	)
	application.InvokeSync(func() {
		defer list.Release()
		cookies, opErr = enumerateCookieList(list)
	})
	return cookies, opErr
}

// getCookieManagerWithRetry obtains the cookie manager from win, retrying while
// the webview initializes. ErrWebviewWindowNotStarted means the window is still
// deferred in wails' pendingRun (windows created during service startup — e.g.
// the restored/temp cookie window — only get their impl after the main event
// loop starts), so that case gets a long budget; any other error is either
// transient controller init (2s budget) or permanent, and the last error is
// returned for diagnosis instead of a uniform "not available".
func getCookieManagerWithRetry(win *application.WebviewWindow) (*edge.ICoreWebView2CookieManager, error) {
	const (
		delay         = 100 * time.Millisecond
		shortAttempts = 20  // 2s: async WebView2 controller init
		longAttempts  = 150 // 15s: pendingRun deferral during app startup
	)
	var lastErr error
	for i := 0; ; i++ {
		cm, err := win.GetCookieManager()
		if err == nil {
			return cm, nil
		}
		lastErr = err
		limit := shortAttempts
		if errors.Is(err, application.ErrWebviewWindowNotStarted) {
			limit = longAttempts
		}
		if i >= limit-1 {
			return nil, fmt.Errorf("desktop: WebView2 cookie manager not available after %d attempts: %w", limit, lastErr)
		}
		time.Sleep(delay)
	}
}

func enumerateCookieList(list *edge.ICoreWebView2CookieList) (map[string][]domain.BrowserCookieEntry, error) {
	if list == nil {
		return map[string][]domain.BrowserCookieEntry{}, nil
	}

	count, err := list.GetCount()
	if err != nil {
		return nil, fmt.Errorf("desktop: GetCount failed: %w", err)
	}

	entries := make([]domain.BrowserCookieEntry, 0, count)
	for i := uint32(0); i < count; i++ {
		ck, err := list.GetItem(i)
		if err != nil {
			continue
		}
		entry, err := edgeCookieToEntry(ck)
		ck.Release()
		if err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return groupCookiesByDomain(entries), nil
}

func edgeCookieToEntry(ck *edge.ICoreWebView2Cookie) (domain.BrowserCookieEntry, error) {
	var e domain.BrowserCookieEntry
	var err error

	e.Name, err = ck.GetName()
	if err != nil {
		return e, err
	}
	e.Value, err = ck.GetValue()
	if err != nil {
		return e, err
	}
	e.Domain, err = ck.GetDomain()
	if err != nil {
		return e, err
	}
	e.Path, err = ck.GetPath()
	if err != nil {
		return e, err
	}
	if exp, err := ck.GetExpires(); err == nil && exp > 0 {
		// WebView2 cookie expiration is stored as seconds since the Unix epoch.
		e.Expires = int64(exp)
	}
	e.HttpOnly, _ = ck.GetIsHttpOnly()
	e.Secure, _ = ck.GetIsSecure()
	if sameSite, err := ck.GetSameSite(); err == nil {
		e.SameSite = sameSiteFromInt32(sameSite)
	}
	return e, nil
}

// importCookiesForWindow writes the domain-grouped cookies into the WebView2
// CookieManager associated with win. It returns the number of cookies
// successfully written. The writes run on the app main thread (COM thread
// affinity) inside InvokeSync.
func importCookiesForWindow(win *application.WebviewWindow, cookies map[string][]domain.BrowserCookieEntry) (int64, error) {
	if win == nil {
		return 0, errors.New("desktop: browser window is nil")
	}
	cm, err := getCookieManagerWithRetry(win)
	if err != nil {
		return 0, err
	}
	var imported int64
	application.InvokeSync(func() {
		defer cm.Release()
		for _, group := range cookies {
			for _, e := range group {
				if e.Name == "" {
					continue
				}
				if err := importCookieEntry(cm, e); err != nil {
					continue
				}
				imported++
			}
		}
	})
	return imported, nil
}

func importCookieEntry(cm *edge.ICoreWebView2CookieManager, e domain.BrowserCookieEntry) error {
	ck, err := cm.CreateCookie(e.Name, e.Value, e.Domain, e.Path)
	if err != nil {
		return err
	}
	defer ck.Release()

	if e.Expires > 0 {
		_ = ck.PutExpires(float64(e.Expires))
	}
	_ = ck.PutIsHttpOnly(e.HttpOnly)
	_ = ck.PutIsSecure(e.Secure)
	_ = ck.PutSameSite(sameSiteToInt32(e.SameSite))
	return cm.AddOrUpdateCookie(ck)
}
