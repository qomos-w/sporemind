package desktop

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/config"
)

func TestParseLoginCallback(t *testing.T) {
	t.Run("snake_case complete", func(t *testing.T) {
		q := url.Values{
			"account_id":    {"acc-1"},
			"access_token":  {"at"},
			"refresh_token": {"rt"},
			"expires_in":    {"3600"},
			"display_name":  {"Alice"},
			"avatar_url":    {"https://sporemind.ai/a.png"},
		}
		f, ok := parseLoginCallback(q)
		if !ok {
			t.Fatal("expected ok=true for complete fields")
		}
		if f.AccountID != "acc-1" || f.AccessToken != "at" || f.RefreshToken != "rt" {
			t.Fatalf("unexpected token fields: %+v", f)
		}
		if f.ExpiresIn != 3600 || f.DisplayName != "Alice" || f.AvatarURL == "" {
			t.Fatalf("unexpected optional fields: %+v", f)
		}
	})

	t.Run("camelCase aliases", func(t *testing.T) {
		q := url.Values{
			"accountId":    {"acc-2"},
			"accessToken":  {"at2"},
			"refreshToken": {"rt2"},
			"expiresIn":    {"7200"},
		}
		f, ok := parseLoginCallback(q)
		if !ok {
			t.Fatal("expected ok=true for camelCase aliases")
		}
		if f.AccountID != "acc-2" || f.AccessToken != "at2" || f.RefreshToken != "rt2" || f.ExpiresIn != 7200 {
			t.Fatalf("unexpected: %+v", f)
		}
	})

	t.Run("bare token alias", func(t *testing.T) {
		q := url.Values{
			"account_id":    {"acc-3"},
			"token":         {"bare"},
			"refresh_token": {"rt3"},
		}
		f, ok := parseLoginCallback(q)
		if !ok {
			t.Fatal("expected ok=true for bare token alias")
		}
		if f.AccessToken != "bare" {
			t.Fatalf("expected AccessToken=bare, got %q", f.AccessToken)
		}
	})

	t.Run("missing required rejected", func(t *testing.T) {
		for _, missing := range []string{"account_id", "access_token", "refresh_token"} {
			q := url.Values{
				"account_id":    {"acc"},
				"access_token":  {"at"},
				"refresh_token": {"rt"},
			}
			q.Del(missing)
			if _, ok := parseLoginCallback(q); ok {
				t.Fatalf("expected ok=false when %q missing", missing)
			}
		}
	})

	t.Run("invalid expires_in ignored", func(t *testing.T) {
		q := url.Values{
			"account_id":    {"acc"},
			"access_token":  {"at"},
			"refresh_token": {"rt"},
			"expires_in":    {"not-a-number"},
		}
		f, ok := parseLoginCallback(q)
		if !ok {
			t.Fatal("expected ok=true despite invalid expires_in")
		}
		if f.ExpiresIn != 0 {
			t.Fatalf("expected ExpiresIn=0, got %d", f.ExpiresIn)
		}
	})
}

func TestLoginCallbackTimedOut(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	timeout := 5 * time.Minute

	t.Run("before timeout", func(t *testing.T) {
		if loginCallbackTimedOut(start, start.Add(4*time.Minute+59*time.Second), timeout) {
			t.Fatal("expected false before the timeout elapses")
		}
	})

	t.Run("exactly at timeout", func(t *testing.T) {
		if !loginCallbackTimedOut(start, start.Add(timeout), timeout) {
			t.Fatal("expected true exactly at the timeout instant")
		}
	})

	t.Run("after timeout", func(t *testing.T) {
		if !loginCallbackTimedOut(start, start.Add(timeout+time.Second), timeout) {
			t.Fatal("expected true after the timeout elapses")
		}
	})

	t.Run("zero timeout is immediately expired", func(t *testing.T) {
		if !loginCallbackTimedOut(start, start, 0) {
			t.Fatal("expected true for zero timeout at the same instant")
		}
	})
}

// TestLoginCallbackServer exercises the local callback HTTP server plumbing
// (bind + routing + parsing + response + auto-stop after a valid token)
// without a live Wails app. emitLoginToken is a no-op when a.app == nil, so
// the test asserts only the HTTP behaviour, which is the Wails-independent
// part of the contract.
func TestLoginCallbackServer(t *testing.T) {
	a := &App{}
	if err := a.ensureLoginCallbackServerLocked(); err != nil {
		t.Fatalf("ensureLoginCallbackServerLocked: %v", err)
	}
	if a.loginCallbackURL == "" {
		t.Fatal("loginCallbackURL not set")
	}
	t.Cleanup(func() { a.stopLoginCallbackServerLocked() })

	client := &http.Client{Timeout: 3 * time.Second}

	t.Run("missing token -> 400 error page", func(t *testing.T) {
		resp, err := client.Get(a.loginCallbackURL)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		// Server must still be running after a rejected callback.
		if a.loginCallbackServer == nil {
			t.Fatal("server stopped after rejected callback")
		}
	})

	t.Run("valid token -> 200 success page and auto-stop", func(t *testing.T) {
		u := a.loginCallbackURL + "?account_id=acc&access_token=at&refresh_token=rt&expires_in=60"
		resp, err := client.Get(u)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		// The server shuts down after handing off a valid token.
		deadline := time.Now().Add(3 * time.Second)
		for a.loginCallbackServer != nil && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if a.loginCallbackServer != nil {
			t.Fatal("server still running after a valid token handoff")
		}
		if a.loginCallbackURL != "" {
			t.Fatal("callback URL not cleared after server stop")
		}
	})
}

// TestLoginCallbackServerAutoStopTimeout verifies that the callback server
// schedules an auto-stop timer at the configured timeout and that firing the
// timer tears the server down. The timer function is a seam so the test does
// not wait the real 5 minutes.
func TestLoginCallbackServerAutoStopTimeout(t *testing.T) {
	a := &App{}
	var fired func()
	a.loginCallbackTimerFn = func(d time.Duration, f func()) *time.Timer {
		if d != loginCallbackTimeout {
			t.Fatalf("timeout = %v, want %v", d, loginCallbackTimeout)
		}
		fired = f
		return time.NewTimer(time.Hour) // never fires in the test
	}

	if err := a.ensureLoginCallbackServerLocked(); err != nil {
		t.Fatalf("ensureLoginCallbackServerLocked: %v", err)
	}
	t.Cleanup(func() { a.stopLoginCallbackServerLocked() })

	if a.loginCallbackServer == nil {
		t.Fatal("callback server not started")
	}
	if fired == nil {
		t.Fatal("auto-stop timer not scheduled")
	}

	fired()

	if a.loginCallbackServer != nil {
		t.Fatal("callback server still running after timeout fired")
	}
	if a.loginCallbackURL != "" {
		t.Fatal("callback URL not cleared after timeout fired")
	}
}

// TestFinalizeLoginURL checks that the desktop_callback parameter is appended
// to both query-less and query-bearing login URLs.
func TestFinalizeLoginURL(t *testing.T) {
	a := &App{loginCallbackURL: "http://127.0.0.1:12345/callback"}

	t.Run("no existing query", func(t *testing.T) {
		got := a.finalizeLoginURL("https://sporemind.ai/login")
		want := "https://sporemind.ai/login?" + loginCallbackParamName + "=" + url.QueryEscape(a.loginCallbackURL)
		if got != want {
			t.Fatalf("finalizeLoginURL = %q, want %q", got, want)
		}
	})

	t.Run("existing query", func(t *testing.T) {
		got := a.finalizeLoginURL("https://sporemind.ai/login?locale=zh")
		want := "https://sporemind.ai/login?locale=zh&" + loginCallbackParamName + "=" + url.QueryEscape(a.loginCallbackURL)
		if got != want {
			t.Fatalf("finalizeLoginURL = %q, want %q", got, want)
		}
	})

	t.Run("empty falls back to configured login page", func(t *testing.T) {
		got := a.finalizeLoginURL("")
		want := config.SporemindLoginURL() + "?" + loginCallbackParamName + "=" + url.QueryEscape(a.loginCallbackURL)
		if got != want {
			t.Fatalf("finalizeLoginURL(\"\") = %q, want %q", got, want)
		}
	})
}
