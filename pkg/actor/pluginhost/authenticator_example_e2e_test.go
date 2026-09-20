package pluginhost

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestAuthenticatorExampleArtifactEndToEnd verifies the TOTP authenticator
// plugin-dev-example end-to-end through the production ArtifactLoader
// path, including app.state persistence via the host bridge:
//
//  1. Build the subprocess-mode executable (CGO_ENABLED=0, staged module).
//  2. Load it over the subprocess transport with app.state granted and the
//     state bridge routed to the pluginhost actor's real state handlers.
//  3. provision imports the RFC 6238 SHA1 test seed.
//  4. codes returns the same code as an independent local TOTP oracle.
//  5. verify accepts the current code, rejects a wrong one.
//     5b. update renames the account and rotates the secret; codes track both.
//  6. Unload + Load again: the account survives (persisted state, not
//     in-memory).
//  7. remove drops the account.
func TestAuthenticatorExampleArtifactEndToEnd(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("native plugins not supported on this platform")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go toolchain not available")
	}

	sourceDir, err := filepath.Abs("../../../plugin-dev-example")
	if err != nil {
		t.Fatalf("resolve example dir: %v", err)
	}
	manifestData, err := os.ReadFile(filepath.Join(sourceDir, "app.manifest.json"))
	if err != nil {
		t.Skipf("generated artifacts missing at %s (run appmanager.dev_generate first)", sourceDir)
	}
	var manifest gen.AppManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.ID != "app.authenticator" {
		t.Skipf("plugin-dev-example holds app %q, not app.authenticator", manifest.ID)
	}

	outDir := filepath.Join(sourceDir, ".build")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stageDir := t.TempDir() // staged copy lives outside the source tree
	// Subprocess (dev) build: a standalone executable — CGO_ENABLED=0 comes
	// from ModeSubprocess, no -buildmode, no cgo export shim. The checked-in
	// example go.mod replaces point at ./vendor-sdk (self-contained app,
	// gitignored); build from a staged copy with absolute replaces so the
	// test runs in any checkout layout, including agent worktrees.
	artifactPath, artifactHash, err := pluginhost.BuildNativeArtifact(pluginhost.NativeBuildOptions{
		ProjectID:   manifest.ID,
		SourceRoot:  sourceDir,
		EntryModule: "main.gen.go",
		OutDir:      outDir,
		Mode:        pluginhost.ModeSubprocess,
		GoBuild: func(dir, outPath string, env []string) ([]byte, error) {
			modDir := testutil.StageExampleModuleDir(t, sourceDir, stageDir)
			return testutil.GoRunOutput(modDir, env, "build", "-o", outPath, ".")
		},
	})
	if err != nil {
		t.Fatalf("build authenticator artifact: %v", err)
	}

	// Real state handlers, isolated to this test's plugin key.
	stateActor := &Actor{}
	cleanupState := func() {
		_, _ = stateActor.handleStateDelete(nil, gen.PluginStateDeleteReq{Plugin: manifest.ID, Key: "accounts"})
	}
	cleanupState()
	t.Cleanup(cleanupState)

	storeDispatch := func(callID string, req []byte) ([]byte, error) {
		switch callID {
		case "state.get":
			var r gen.PluginStateGetReq
			if err := json.Unmarshal(req, &r); err != nil {
				return nil, err
			}
			resp, err := stateActor.handleStateGet(nil, r)
			if err != nil {
				return nil, err
			}
			return json.Marshal(resp)
		case "state.set":
			var r gen.PluginStateSetReq
			if err := json.Unmarshal(req, &r); err != nil {
				return nil, err
			}
			resp, err := stateActor.handleStateSet(nil, r)
			if err != nil {
				return nil, err
			}
			return json.Marshal(resp)
		case "state.delete":
			var r gen.PluginStateDeleteReq
			if err := json.Unmarshal(req, &r); err != nil {
				return nil, err
			}
			resp, err := stateActor.handleStateDelete(nil, r)
			if err != nil {
				return nil, err
			}
			return json.Marshal(resp)
		}
		return nil, fmt.Errorf("unknown state callID %q", callID)
	}

	host := &captureHost{}
	loader := pluginhost.NewArtifactLoader(host)
	// transportOpener picks the transport from the on-disk artifact: the
	// subprocess build is an executable, so dev mode routes this load to the
	// processOpener. (The in-process purego opener could not open an
	// executable, so a successful Load + invoke below is the proof that the
	// subprocess transport served the plugin.)
	pop := &processOpener{storeDispatch: storeDispatch}
	var live *processOpener
	pop.observeClone = func(o *processOpener) { live = o }
	loader.SetOpener(&transportOpener{devMode: true, inprocess: loaderOpener{}, subprocess: pop})
	abi := gen.PluginAbi{
		Name: "spore-plugin", Version: 1, Encoding: pluginhost.BinaryCodecV1,
		InvokeSymbol: "PluginInvoke", ContractVersion: "1",
		Isolation: pluginhost.IsolationSubprocess, TrustClass: pluginhost.TrustFirstParty,
		Signer: "sporemind.first-party",
	}
	loadReq := gen.PluginArtifactLoadReq{
		ArtifactPath: artifactPath, ArtifactHash: artifactHash,
		Manifest: manifest, Abi: abi,
	}
	if _, err := loader.Load(context.Background(), loadReq); err != nil {
		t.Fatalf("ArtifactLoader.Load: %v", err)
	}
	// Explicit subprocess-transport assertion: the processOpener must have
	// spawned the plugin executable (a load error here could otherwise be
	// misread as an FFI success from another opener). transportOpener hands
	// each load a fresh processOpener clone; observeClone captured it.
	if live == nil || live.cmd == nil || live.cmd.Process == nil {
		t.Fatal("expected the subprocess transport to spawn the plugin process; live clone has no cmd")
	}

	call := func(name string, payload any, out any) {
		t.Helper()
		h, ok := host.handler(pluginhost.PluginCallID(manifest.ID, name))
		if !ok {
			t.Fatalf("handler for %q not registered", name)
		}
		raw, _ := json.Marshal(payload)
		resp, err := h(context.Background(), raw)
		if err != nil {
			t.Fatalf("invoke %s: %v", name, err)
		}
		if err := json.Unmarshal(resp, out); err != nil {
			t.Fatalf("decode %s response %q: %v", name, resp, err)
		}
	}

	// Independent local TOTP oracle, pinned to the RFC 6238 SHA1 vector
	// (t=59, 8 digits → 94287082) before use.
	oracleSecret := func(secret []byte, t time.Time, digits int) string {
		return localTOTP(secret, t, digits)
	}
	oracle := func(t time.Time, digits int) string {
		return oracleSecret([]byte("12345678901234567890"), t, digits)
	}
	if got := oracle(time.Unix(59, 0), 8); got != "94287082" {
		t.Fatalf("local oracle RFC vector mismatch: got %s, want 94287082", got)
	}

	// 3. provision: import the RFC seed, 8 digits.
	seedB32 := base32.StdEncoding.EncodeToString([]byte("12345678901234567890"))
	var prov struct {
		AccountId string
		Name      string
		Digits    int32
	}
	call("provision", map[string]any{"Name": "rfc-test", "Secret": seedB32, "Digits": 8}, &prov)
	if prov.AccountId == "" || prov.Name != "rfc-test" || prov.Digits != 8 {
		t.Fatalf("provision response unexpected: %+v", prov)
	}

	// 4. codes: plugin code must equal the local oracle within one step.
	var codes struct {
		Accounts []struct {
			AccountId        string
			Name             string
			Code             string
			SecretBase32     string
			SecondsRemaining int32
		}
	}
	call("codes", map[string]any{}, &codes)
	if len(codes.Accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(codes.Accounts))
	}
	now := time.Now()
	got := codes.Accounts[0]
	if got.AccountId != prov.AccountId || got.Code != oracle(now, 8) {
		// Window boundary: accept ±1 step before declaring mismatch.
		if got.Code != oracle(now.Add(-30*time.Second), 8) && got.Code != oracle(now.Add(30*time.Second), 8) {
			t.Fatalf("code mismatch: plugin=%s local=%s", got.Code, oracle(now, 8))
		}
	}
	if got.SecondsRemaining <= 0 || got.SecondsRemaining > 30 {
		t.Fatalf("SecondsRemaining out of range: %d", got.SecondsRemaining)
	}

	// 5. verify: accept current code, reject a distinct wrong one.
	var verify struct {
		Valid     bool
		AccountId string
	}
	call("verify", map[string]any{"AccountId": prov.AccountId, "Code": got.Code, "Window": 1}, &verify)
	if !verify.Valid {
		t.Fatalf("verify rejected the live code %q", got.Code)
	}
	wrong := fmt.Sprintf("%08d", (mustAtoi64(got.Code)+1)%100000000)
	call("verify", map[string]any{"AccountId": prov.AccountId, "Code": wrong}, &verify)
	if verify.Valid {
		t.Fatalf("verify accepted a wrong code %q (live %q)", wrong, got.Code)
	}

	// 5b. update: rename + rotate secret; codes must track both, and an
	// unknown AccountId must report Updated=false.
	seed2 := []byte("abcdefghijklmnopqrst") // distinct 20-byte secret
	seed2B32 := base32.StdEncoding.EncodeToString(seed2)
	var upd struct {
		Updated      bool
		AccountId    string
		Name         string
		SecretBase32 string
	}
	call("update", map[string]any{"AccountId": prov.AccountId, "Name": "rfc-renamed", "Secret": seed2B32}, &upd)
	if !upd.Updated || upd.AccountId != prov.AccountId || upd.Name != "rfc-renamed" || upd.SecretBase32 != seed2B32 {
		t.Fatalf("update response unexpected: %+v", upd)
	}
	call("codes", map[string]any{}, &codes)
	now = time.Now()
	if len(codes.Accounts) != 1 || codes.Accounts[0].Name != "rfc-renamed" || codes.Accounts[0].SecretBase32 != seed2B32 {
		t.Fatalf("codes after update unexpected: %+v", codes.Accounts)
	}
	if live := codes.Accounts[0].Code; live != oracleSecret(seed2, now, 8) &&
		live != oracleSecret(seed2, now.Add(-30*time.Second), 8) &&
		live != oracleSecret(seed2, now.Add(30*time.Second), 8) {
		t.Fatalf("code after secret rotation mismatch: plugin=%s local=%s", live, oracleSecret(seed2, now, 8))
	}
	call("update", map[string]any{"AccountId": "acc_missing", "Name": "x"}, &upd)
	if upd.Updated {
		t.Fatalf("update on unknown account reported Updated: %+v", upd)
	}

	// 6. persistence across Unload + Load.
	if _, err := loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: manifest.ID}); err != nil {
		t.Fatalf("unload: %v", err)
	}
	if _, err := loader.Load(context.Background(), loadReq); err != nil {
		t.Fatalf("reload after unload: %v", err)
	}
	call("codes", map[string]any{}, &codes)
	if len(codes.Accounts) != 1 || codes.Accounts[0].AccountId != prov.AccountId {
		t.Fatalf("account lost across unload/load cycle: %+v", codes.Accounts)
	}

	// 7. remove.
	var remove struct {
		Removed   bool
		AccountId string
	}
	call("remove", map[string]any{"AccountId": prov.AccountId}, &remove)
	if !remove.Removed {
		t.Fatalf("remove failed: %+v", remove)
	}
	call("codes", map[string]any{}, &codes)
	if len(codes.Accounts) != 0 {
		t.Fatalf("expected no accounts after remove, got %+v", codes.Accounts)
	}
}

func mustAtoi64(s string) int64 {
	var v uint64
	for _, c := range s {
		v = v*10 + uint64(c-'0')
	}
	return int64(v)
}

// localTOTP is an independent RFC 4226/6238 SHA1 implementation used as the
// test oracle. Kept deliberately separate from the plugin's code path.
func localTOTP(secret []byte, t time.Time, digits int) string {
	counter := uint64(t.Unix() / 30)
	mac := hmac.New(sha1.New, secret)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[offset])&0x7f)<<24 |
		uint32(sum[offset+1])<<16 |
		uint32(sum[offset+2])<<8 |
		uint32(sum[offset+3])
	mod := uint32(1)
	for i := 0; i < digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, bin%mod)
}
