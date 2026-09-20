package voice

import (
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

func newTestActor(t *testing.T) *Actor {
	t.Helper()
	return &Actor{store: persist.NewFSPersist(t.TempDir()), actorID: "voice-test"}
}

func mkSTT(name, provider, key string) gen.VoiceAccountCreateReq {
	return gen.VoiceAccountCreateReq{Kind: "stt", Name: name, Provider: provider, APIKey: key}
}

func mkTTS(name, provider, key string) gen.VoiceAccountCreateReq {
	return gen.VoiceAccountCreateReq{Kind: "tts", Name: name, Provider: provider, APIKey: key}
}

// TestAccountProxyRoundTrip verifies the Proxy field survives the create →
// view → update path and reaches the active-account lookup used by the
// recognize/synthesize provider dispatch.
func TestAccountProxyRoundTrip(t *testing.T) {
	a := newTestActor(t)
	created, err := a.handleAccountCreate(nil, gen.VoiceAccountCreateReq{Kind: "stt", Name: "openai", Provider: "openai", APIKey: "k", Proxy: "http://voice-proxy:7890"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Account.Proxy != "http://voice-proxy:7890" {
		t.Fatalf("create view lost proxy: %+v", created.Account)
	}

	updated, err := a.handleAccountUpdate(nil, gen.VoiceAccountUpdateReq{ID: created.Account.ID, Proxy: "socks5://voice-proxy:1080"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Account.Proxy != "socks5://voice-proxy:1080" {
		t.Fatalf("update view lost proxy: %+v", updated.Account)
	}

	active, err := a.activeAccount("stt")
	if err != nil {
		t.Fatal(err)
	}
	if active.Proxy != "socks5://voice-proxy:1080" {
		t.Fatalf("active account lost proxy: %+v", active)
	}
}

func TestAccountViewRedactsAPIKey(t *testing.T) {
	view := accountView(gen.VoiceAccount{ID: "va_1", Kind: "stt", Name: "openai", Provider: "openai", APIKey: "secret", Model: "whisper-1", Language: "en-US"})
	if !view.HasAPIKey || view.Kind != "stt" || view.Provider != "openai" || view.Model != "whisper-1" || view.Language != "en-US" {
		t.Fatalf("unexpected account view: %+v", view)
	}
}

func TestAccountListRedactsAPIKey(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleAccountCreate(nil, gen.VoiceAccountCreateReq{Kind: "stt", Name: "openai", Provider: "openai", APIKey: "secret-key", Model: "whisper-1"}); err != nil {
		t.Fatal(err)
	}
	resp, err := a.handleAccountList(nil, gen.VoiceAccountListReq{Kind: "stt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 account, got %d", len(resp.Items))
	}
	if resp.Items[0].HasAPIKey != true {
		t.Fatal("expected HasAPIKey true")
	}
	// Exported JSON must not leak the plaintext key.
	exp, err := a.handleConfigExport(nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(exp.Data, "secret-key") || strings.Contains(exp.Data, `"ApiKey":`) {
		t.Fatalf("export leaked API key: %s", exp.Data)
	}
	if !strings.Contains(exp.Data, "HasApiKey") {
		t.Fatalf("export omitted redacted credential state: %s", exp.Data)
	}
}

func TestAccountCreateFirstBecomesActive(t *testing.T) {
	a := newTestActor(t)
	resp, err := a.handleAccountCreate(nil, mkSTT("glm", "glm", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	acc, err := a.activeAccount("stt")
	if err != nil {
		t.Fatalf("expected active account, got err: %v", err)
	}
	if acc.ID != resp.Account.ID {
		t.Fatalf("active id mismatch: %q vs %q", acc.ID, resp.Account.ID)
	}
}

func TestAccountUpdateEmptyAPIKeyPreservesSecret(t *testing.T) {
	a := newTestActor(t)
	created, err := a.handleAccountCreate(nil, gen.VoiceAccountCreateReq{Kind: "stt", Name: "openai", Provider: "openai", APIKey: "secret", Model: "old"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := a.handleAccountUpdate(nil, gen.VoiceAccountUpdateReq{ID: created.Account.ID, Model: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Account.HasAPIKey {
		t.Fatal("update response should report an existing API key")
	}
	st, _ := a.loadStore()
	if st.Accounts[0].APIKey != "secret" || st.Accounts[0].Model != "new" {
		t.Fatalf("unexpected stored account: %+v", st.Accounts[0])
	}
}

func TestAccountUpdateWithAPIKeyOverwrites(t *testing.T) {
	a := newTestActor(t)
	created, err := a.handleAccountCreate(nil, mkSTT("openai", "openai", "old"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleAccountUpdate(nil, gen.VoiceAccountUpdateReq{ID: created.Account.ID, APIKey: "new"}); err != nil {
		t.Fatal(err)
	}
	st, _ := a.loadStore()
	if st.Accounts[0].APIKey != "new" {
		t.Fatalf("expected APIKey 'new', got %q", st.Accounts[0].APIKey)
	}
}

func TestAccountActivateSwitchesActive(t *testing.T) {
	a := newTestActor(t)
	a1, _ := a.handleAccountCreate(nil, mkSTT("glm", "glm", "k1"))
	a2, _ := a.handleAccountCreate(nil, mkSTT("openai", "openai", "k2"))

	resp, err := a.handleAccountActivate(nil, gen.VoiceAccountActivateReq{Kind: "stt", ID: a2.Account.ID})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ActiveID != a2.Account.ID {
		t.Fatalf("expected active %q, got %q", a2.Account.ID, resp.ActiveID)
	}
	acc, _ := a.activeAccount("stt")
	if acc.Provider != "openai" {
		t.Fatalf("expected active provider openai, got %q", acc.Provider)
	}

	if _, err := a.handleAccountActivate(nil, gen.VoiceAccountActivateReq{Kind: "stt", ID: a1.Account.ID}); err != nil {
		t.Fatal(err)
	}
	acc, _ = a.activeAccount("stt")
	if acc.Provider != "glm" {
		t.Fatalf("expected active provider glm, got %q", acc.Provider)
	}
}

func TestAccountActivateUnknownFails(t *testing.T) {
	a := newTestActor(t)
	a.handleAccountCreate(nil, mkSTT("glm", "glm", "k1"))
	if _, err := a.handleAccountActivate(nil, gen.VoiceAccountActivateReq{Kind: "stt", ID: "nope"}); err == nil {
		t.Fatal("expected error activating unknown account")
	}
}

func TestAccountDeleteActiveFallsBackToFirst(t *testing.T) {
	a := newTestActor(t)
	a1, _ := a.handleAccountCreate(nil, mkSTT("glm", "glm", "k1"))
	a2, _ := a.handleAccountCreate(nil, mkSTT("openai", "openai", "k2"))
	if _, err := a.handleAccountDelete(nil, gen.VoiceAccountDeleteReq{ID: a1.Account.ID}); err != nil {
		t.Fatal(err)
	}
	acc, err := a.activeAccount("stt")
	if err != nil {
		t.Fatalf("expected fallback active account: %v", err)
	}
	if acc.ID != a2.Account.ID {
		t.Fatalf("expected fallback to openai, got %q", acc.ID)
	}
	list, _ := a.handleAccountList(nil, gen.VoiceAccountListReq{Kind: "stt"})
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 remaining account, got %d", len(list.Items))
	}
}

func TestAccountDeleteLastClearsActive(t *testing.T) {
	a := newTestActor(t)
	a1, _ := a.handleAccountCreate(nil, mkSTT("glm", "glm", "k1"))
	if _, err := a.handleAccountDelete(nil, gen.VoiceAccountDeleteReq{ID: a1.Account.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.activeAccount("stt"); err == nil {
		t.Fatal("expected error when no account is active")
	}
}

// STT and TTS are independent services: an stt account must not satisfy a tts
// active lookup, and deleting one kind must not affect the other.
func TestSTTandTTSareIndependent(t *testing.T) {
	a := newTestActor(t)
	// Only an stt account exists; tts active lookup must fail.
	stt1, _ := a.handleAccountCreate(nil, mkSTT("glm-asr", "glm", "stt-key"))
	if _, err := a.activeAccount("tts"); err == nil {
		t.Fatal("expected tts to have no active account despite stt configured")
	}

	// List filters by kind: each kind sees only its own accounts.
	sttList, _ := a.handleAccountList(nil, gen.VoiceAccountListReq{Kind: "stt"})
	if len(sttList.Items) != 1 || sttList.Items[0].Provider != "glm" {
		t.Fatalf("stt list should contain only the stt account: %+v", sttList.Items)
	}
	ttsList, _ := a.handleAccountList(nil, gen.VoiceAccountListReq{Kind: "tts"})
	if len(ttsList.Items) != 0 {
		t.Fatalf("tts list should be empty, got %d", len(ttsList.Items))
	}

	// Activating the stt account as tts must fail (kind mismatch).
	if _, err := a.handleAccountActivate(nil, gen.VoiceAccountActivateReq{Kind: "tts", ID: stt1.Account.ID}); err == nil {
		t.Fatal("expected error activating stt account as tts")
	}

	// Now add a tts account; it becomes active tts independently of stt.
	tts1, _ := a.handleAccountCreate(nil, mkTTS("glm-tts", "glm", "tts-key"))
	if acc, _ := a.activeAccount("tts"); acc.ID != tts1.Account.ID {
		t.Fatalf("expected active tts account, got %+v", acc)
	}

	// Deleting the stt account must not clear the tts active id.
	if _, err := a.handleAccountDelete(nil, gen.VoiceAccountDeleteReq{ID: stt1.Account.ID}); err != nil {
		t.Fatal(err)
	}
	if acc, err := a.activeAccount("tts"); err != nil || acc.ID != tts1.Account.ID {
		t.Fatalf("tts active should survive deleting the stt account: %+v %v", acc, err)
	}
}

func TestMigrateLegacyConfig(t *testing.T) {
	a := newTestActor(t)
	if err := a.store.Save(a.actorID, map[string]any{
		"provider": "openai",
		"apiKey":   "legacy-key",
		"model":    "whisper-1",
		"language": "en-US",
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.migrateLegacyConfig(); err != nil {
		t.Fatal(err)
	}
	st, err := a.loadStore()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Accounts) != 1 {
		t.Fatalf("expected 1 migrated account, got %d", len(st.Accounts))
	}
	acc := st.Accounts[0]
	if acc.Kind != "stt" || acc.Provider != "openai" || acc.APIKey != "legacy-key" || acc.Model != "whisper-1" || acc.Language != "en-US" {
		t.Fatalf("unexpected migrated account: %+v", acc)
	}
	if st.ActiveSTTID != acc.ID {
		t.Fatalf("expected migrated account to be active stt, got %q", st.ActiveSTTID)
	}
	// Idempotent: second migration is a no-op.
	if err := a.migrateLegacyConfig(); err != nil {
		t.Fatal(err)
	}
	st2, _ := a.loadStore()
	if len(st2.Accounts) != 1 {
		t.Fatalf("expected migration to remain idempotent, got %d accounts", len(st2.Accounts))
	}
}

func TestMigrateLegacyNoData(t *testing.T) {
	a := newTestActor(t)
	if err := a.migrateLegacyConfig(); err != nil {
		t.Fatal(err)
	}
	st, _ := a.loadStore()
	if len(st.Accounts) != 0 {
		t.Fatalf("expected zero accounts with no legacy data, got %d", len(st.Accounts))
	}
}
