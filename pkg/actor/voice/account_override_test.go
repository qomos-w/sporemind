package voice

import (
	"strings"
	"testing"
)

// TestAccountFor_OverrideAndValidation pins the account-resolution contract
// behind the plugin-facing AccountId field: empty = active account (legacy),
// non-empty = exact account with a mandatory kind match.
func TestAccountFor_OverrideAndValidation(t *testing.T) {
	a := newTestActor(t)
	first, err := a.handleAccountCreate(nil, mkSTT("glm", "glm", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.handleAccountCreate(nil, mkSTT("openai", "openai", "k2"))
	if err != nil {
		t.Fatal(err)
	}
	tts, err := a.handleAccountCreate(nil, mkTTS("glm-tts", "glm", "k3"))
	if err != nil {
		t.Fatal(err)
	}
	_ = tts

	t.Run("empty falls back to active", func(t *testing.T) {
		acc, err := a.accountFor("stt", "")
		if err != nil {
			t.Fatal(err)
		}
		if acc.ID != first.Account.ID {
			t.Fatalf("empty AccountId resolved %q, want active %q", acc.ID, first.Account.ID)
		}
	})

	t.Run("override beats active", func(t *testing.T) {
		acc, err := a.accountFor("stt", second.Account.ID)
		if err != nil {
			t.Fatal(err)
		}
		if acc.ID != second.Account.ID || acc.Provider != "openai" {
			t.Fatalf("override resolved %+v, want %s", acc, second.Account.ID)
		}
	})

	t.Run("kind mismatch rejected", func(t *testing.T) {
		_, err := a.accountFor("stt", tts.Account.ID)
		if err == nil || !strings.Contains(err.Error(), "cannot serve stt") {
			t.Fatalf("stt call with tts account must fail with kind error, got %v", err)
		}
		_, err = a.accountFor("tts", second.Account.ID)
		if err == nil || !strings.Contains(err.Error(), "cannot serve tts") {
			t.Fatalf("tts call with stt account must fail with kind error, got %v", err)
		}
	})

	t.Run("unknown account rejected", func(t *testing.T) {
		if _, err := a.accountFor("stt", "va_nonexistent"); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("unknown account must fail, got %v", err)
		}
	})
}
