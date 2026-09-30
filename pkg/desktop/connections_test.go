package desktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSealOpenPasswordRoundtrip(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	enc, err := sealPassword(key, "s3cret-pass")
	if err != nil {
		t.Fatal(err)
	}
	if enc == "" || enc == "s3cret-pass" {
		t.Fatalf("password not sealed: %q", enc)
	}
	got, err := openPassword(key, enc)
	if err != nil {
		t.Fatal(err)
	}
	if got != "s3cret-pass" {
		t.Fatalf("roundtrip mismatch: %q", got)
	}
}

func TestOpenPasswordRejectsWrongKey(t *testing.T) {
	key := make([]byte, 32)
	enc, err := sealPassword(key, "s3cret-pass")
	if err != nil {
		t.Fatal(err)
	}
	wrong := make([]byte, 32)
	wrong[0] = 1
	if _, err := openPassword(wrong, enc); err == nil {
		t.Fatal("expected error unsealing with wrong key")
	}
}

func TestSanitizeHost(t *testing.T) {
	cases := map[string]string{
		" 192.168.1.5 ":        "192.168.1.5",
		"ws://192.168.1.5":     "192.168.1.5",
		"http://host:18080/x":  "host:18080",
		"https://my.box":       "my.box",
		"host.example.com/#/a": "host.example.com",
	}
	for in, want := range cases {
		if got := sanitizeHost(in); got != want {
			t.Errorf("sanitizeHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConnectionBaseURL(t *testing.T) {
	if got := connectionBaseURL("192.168.1.5", 18080); got != "http://192.168.1.5:18080" {
		t.Fatalf("got %q", got)
	}
}

func TestProbeInstanceReportsSelf(t *testing.T) {
	const localID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/instance/info" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"instanceId": localID, "version": "vtest"})
	}))
	defer srv.Close()

	got := probeInstance(srv.URL, localID)
	if !got.Reachable || !got.IsSelf || got.InstanceID != localID || got.Version != "vtest" {
		t.Fatalf("unexpected probe result: %+v", got)
	}

	got = probeInstance(srv.URL, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if got.IsSelf {
		t.Fatalf("different fingerprint must not be self: %+v", got)
	}
}

func TestProbeInstanceOldRemoteWithoutFingerprint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	got := probeInstance(srv.URL, "local")
	if !got.Reachable {
		t.Fatalf("404 gateway should still count as reachable: %+v", got)
	}
	if got.IsSelf || got.InstanceID != "" {
		t.Fatalf("no fingerprint expected: %+v", got)
	}
}

func TestProbeInstanceUnreachable(t *testing.T) {
	got := probeInstance("http://127.0.0.1:1", "local")
	if got.Reachable || got.Message == "" {
		t.Fatalf("expected unreachable with message: %+v", got)
	}
}

func TestRemoteAuthLoginPassesCredentialsThrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user.auth_login" {
			http.NotFound(w, r)
			return
		}
		var req map[string]string
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req["Username"] != "admin" || req["Password"] != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "bad credentials"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Token": "tok", "Account": map[string]string{"Username": "admin"}})
	}))
	defer srv.Close()

	raw, err := remoteAuthLogin(srv.URL, "admin", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if raw.Token != "tok" {
		t.Fatalf("unexpected passthrough body: %+v", raw)
	}

	if _, err := remoteAuthLogin(srv.URL, "admin", "wrong"); err == nil {
		t.Fatal("expected error on bad credentials")
	}
}
