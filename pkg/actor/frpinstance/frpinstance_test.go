package frpinstance

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	frpconfig "github.com/fatedier/frp/pkg/config/v1"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func validCfg(id string) domain.FrpInstanceConfig {
	return domain.FrpInstanceConfig{
		ID:         id,
		Name:       "test-instance",
		ServerAddr: "frps.example.com:7000",
		Token:      "super-secret",
		Tls:        true,
		Proxies: []domain.FrpProxy{
			{Name: "ssh", Kind: "tcp", LocalIP: "127.0.0.1", LocalPort: 22, RemotePort: 6022},
		},
	}
}

// freshInstance constructs an Actor via NewActor and skips OnStart so the
// auto-start path doesn't try to dial a real frps server in unit tests.
func freshInstance(t *testing.T, id string) *Actor {
	t.Helper()
	a, ok := NewActor(validCfg(id))().(*Actor)
	if !ok {
		t.Fatal("NewActor: expected *Actor")
	}
	return a
}

func TestNewActor_SeedsCfg(t *testing.T) {
	a := freshInstance(t, "inst-x")
	if a.cfg.ID != "inst-x" {
		t.Errorf("expected cfg.Id seeded, got %q", a.cfg.ID)
	}
	if a.cfg.Token != "super-secret" {
		t.Errorf("expected cfg.Token seeded, got %q", a.cfg.Token)
	}
	if a.cfg.ServerAddr != "frps.example.com:7000" {
		t.Errorf("expected cfg.ServerAddr seeded, got %q", a.cfg.ServerAddr)
	}
}

// TestAllProxiesRunning: the tunnel-up judgment requires every proxy in the
// running phase; "wait start"/"start error"/empty all mean not connected.
func TestAllProxiesRunning(t *testing.T) {
	running := domain.FrpProxyStatus{Name: "ssh", Status: "running"}
	waiting := domain.FrpProxyStatus{Name: "ssh", Status: "wait start"}
	failed := domain.FrpProxyStatus{Name: "ssh", Status: "start error"}

	if !allProxiesRunning([]domain.FrpProxyStatus{running}) {
		t.Error("single running proxy should be connected")
	}
	if !allProxiesRunning([]domain.FrpProxyStatus{running, {Name: "web", Status: "running"}}) {
		t.Error("all running proxies should be connected")
	}
	if allProxiesRunning(nil) {
		t.Error("no proxies must not be connected")
	}
	if allProxiesRunning([]domain.FrpProxyStatus{running, waiting}) {
		t.Error("one proxy still waiting must not be connected")
	}
	if allProxiesRunning([]domain.FrpProxyStatus{failed}) {
		t.Error("failed proxy must not be connected")
	}
}

// TestHandleConfigure_DisabledClearsStaleError: a deliberate stop (cfg.Disabled
// arriving via configure) must clear any prior runtime error so the UI shows
// "stopped", not a stale "error" from a prior failed/crashed run — and this
// covers the crashed case (running=false) that stopLocked alone can't reach.
func TestHandleConfigure_DisabledClearsStaleError(t *testing.T) {
	a := freshInstance(t, "inst-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())

	// Simulate a previously-failed run: not running, but an error lingers.
	a.mu.Lock()
	a.lastErr = "login to the server failed: authorization failed"
	a.running = false
	a.mu.Unlock()

	cfg := validCfg("inst-1")
	cfg.Disabled = true
	resp, err := a.handleConfigure(ctx, domain.FrpInstanceConfigureReq{Config: cfg})
	if err != nil {
		t.Fatalf("handleConfigure: %v", err)
	}
	if resp.Status.Error != "" {
		t.Errorf("expected error cleared on disabled configure, got %q", resp.Status.Error)
	}
	if resp.Status.Running {
		t.Error("expected not running after disabled configure")
	}
	if !resp.Config.Disabled {
		t.Error("expected config.Disabled=true")
	}
}

func TestHandleStatus_NotRunning(t *testing.T) {
	a := freshInstance(t, "inst-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())
	resp, err := a.handleStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status.Running {
		t.Error("fresh instance should not be running")
	}
	if resp.Status.DetectedVersion != FrpVersion {
		t.Errorf("expected frp version %q, got %q", FrpVersion, resp.Status.DetectedVersion)
	}
	if resp.Status.ID != "inst-1" {
		t.Errorf("expected status.id, got %q", resp.Status.ID)
	}
}

// TestStartService_LoginFailureSurfaces: with LoginFailExit=true, a failed
// first login (server accepts the TCP dial then drops the connection before
// the frp handshake completes) must set lastErr and flip running off instead
// of retrying forever — the bug this guards against is a failed tunnel showing
// "running" (success) in the UI. The pre-flight reachability dial succeeds
// here (the listener accepts), so the login failure surfaces asynchronously
// from the frpc run goroutine, as in the live runtime.
func TestStartService_LoginFailureSurfaces(t *testing.T) {
	// A listener that accepts TCP dials (so the reachability pre-flight
	// passes) but immediately closes each connection (so frpc's login fails).
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	acceptPort := l.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	a := freshInstance(t, "inst-1")
	a.cfg.ServerAddr = fmt.Sprintf("127.0.0.1:%d", acceptPort)
	ctx := testutil.HumanCtx(testutil.GenActorID())

	err = a.startService(ctx)
	if err != nil {
		t.Fatalf("startService should succeed synchronously; failure is async: %v", err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		a.mu.Lock()
		lastErr, running, svc := a.lastErr, a.running, a.svc
		a.mu.Unlock()
		if lastErr != "" {
			if running {
				t.Error("running must be false after login failure")
			}
			if svc != nil {
				t.Error("svc must be cleared after login failure")
			}
			if !strings.Contains(lastErr, "login to the server failed") {
				t.Errorf("expected frpc login failure message, got %q", lastErr)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for login failure to surface in lastErr")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestHandleStatus_TokenStripped is the security boundary: the Public()
// status handler must never expose the secret token to anonymous callers.
func TestHandleStatus_TokenStripped(t *testing.T) {
	a := freshInstance(t, "inst-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())
	resp, err := a.handleStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Config.Token != "" {
		t.Errorf("status must strip token, got %q", resp.Config.Token)
	}
	if a.cfg.Token != "super-secret" {
		t.Errorf("status must not mutate stored token, got %q", a.cfg.Token)
	}
}

func TestHandleConfigure_Valid(t *testing.T) {
	a := freshInstance(t, "inst-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())
	newCfg := validCfg("inst-1")
	newCfg.Name = "renamed"
	newCfg.ServerAddr = "other.example.com:8000"
	// Disabled=true so configure doesn't kick off a real frpc dial in the
	// test goroutine. The cfg-replacement path is what we care about here.
	newCfg.Disabled = true

	resp, err := a.handleConfigure(ctx, domain.FrpInstanceConfigureReq{Config: newCfg})
	if err != nil {
		t.Fatal(err)
	}
	if a.cfg.Name != "renamed" {
		t.Errorf("expected name updated, got %q", a.cfg.Name)
	}
	if a.cfg.ServerAddr != "other.example.com:8000" {
		t.Errorf("expected serverAddr updated, got %q", a.cfg.ServerAddr)
	}
	if resp.Config.Token != "" {
		t.Errorf("configure response must strip token, got %q", resp.Config.Token)
	}
}

// TestHandleConfigure_PersistsDisabled: the configure path is how the manager
// flips lifecycle state on the child. Disabled must land on a.cfg so future
// observations (e.g. snapshotLocked) reflect the new policy.
func TestHandleConfigure_PersistsDisabled(t *testing.T) {
	a := freshInstance(t, "inst-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())
	cfg := validCfg("inst-1")
	cfg.Disabled = true
	if _, err := a.handleConfigure(ctx, domain.FrpInstanceConfigureReq{Config: cfg}); err != nil {
		t.Fatal(err)
	}
	if !a.cfg.Disabled {
		t.Error("expected a.cfg.Disabled == true after configure")
	}
	if a.running {
		t.Error("expected a.running == false (Disabled=true must not auto-start)")
	}
}

func TestHandleConfigure_EmptyServerAddr(t *testing.T) {
	a := freshInstance(t, "inst-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())
	bad := validCfg("inst-1")
	bad.ServerAddr = ""
	if _, err := a.handleConfigure(ctx, domain.FrpInstanceConfigureReq{Config: bad}); err == nil {
		t.Error("expected error for empty serverAddr")
	}
}

func TestHandleConfigure_InvalidServerAddr(t *testing.T) {
	a := freshInstance(t, "inst-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())
	bad := validCfg("inst-1")
	bad.ServerAddr = "no-port"
	if _, err := a.handleConfigure(ctx, domain.FrpInstanceConfigureReq{Config: bad}); err == nil {
		t.Error("expected error for serverAddr without port")
	}
}

func TestHandleConfigure_DuplicateProxyName(t *testing.T) {
	a := freshInstance(t, "inst-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())
	bad := validCfg("inst-1")
	bad.Proxies = append(bad.Proxies, domain.FrpProxy{
		Name: "ssh", Kind: "tcp", LocalPort: 80, RemotePort: 6080,
	})
	if _, err := a.handleConfigure(ctx, domain.FrpInstanceConfigureReq{Config: bad}); err == nil {
		t.Error("expected error for duplicate proxy name")
	}
}

func TestValidateConfig_InvalidKind(t *testing.T) {
	bad := validCfg("inst-1")
	bad.Proxies[0].Kind = "http"
	if err := ValidateConfig(bad); err == nil {
		t.Error("expected error for unsupported kind")
	}
}

func TestValidateConfig_InvalidLocalPort(t *testing.T) {
	bad := validCfg("inst-1")
	bad.Proxies[0].LocalPort = 0
	if err := ValidateConfig(bad); err == nil {
		t.Error("expected error for invalid localPort")
	}
}

func TestValidateConfig_InvalidRemotePort(t *testing.T) {
	bad := validCfg("inst-1")
	bad.Proxies[0].RemotePort = 70000
	if err := ValidateConfig(bad); err == nil {
		t.Error("expected error for out-of-range remotePort")
	}
}

func TestValidateConfig_EmptyProxyName(t *testing.T) {
	bad := validCfg("inst-1")
	bad.Proxies[0].Name = ""
	if err := ValidateConfig(bad); err == nil {
		t.Error("expected error for empty proxy name")
	}
}

// TestValidateConfig_WebProxyValid: a populated, enabled WebProxy passes the
// same validation as a regular proxy entry.
func TestValidateConfig_WebProxyValid(t *testing.T) {
	cfg := validCfg("inst-1")
	cfg.WebProxy = &domain.FrpWebProxy{Enabled: true, Name: "sporemind-web", RemotePort: 6080}
	if err := ValidateConfig(cfg); err != nil {
		t.Errorf("valid WebProxy should pass, got %v", err)
	}
}

// TestValidateConfig_WebProxyDisabledSkipsChecks: a populated but disabled
// WebProxy is allowed even with empty/invalid fields — users can leave the
// form populated-but-off without spurious save failures.
func TestValidateConfig_WebProxyDisabledSkipsChecks(t *testing.T) {
	cfg := validCfg("inst-1")
	cfg.WebProxy = &domain.FrpWebProxy{Enabled: false, Name: "", RemotePort: 0}
	if err := ValidateConfig(cfg); err != nil {
		t.Errorf("disabled WebProxy must skip field validation, got %v", err)
	}
}

func TestValidateConfig_WebProxyEmptyName(t *testing.T) {
	cfg := validCfg("inst-1")
	cfg.WebProxy = &domain.FrpWebProxy{Enabled: true, Name: "", RemotePort: 6080}
	if err := ValidateConfig(cfg); err == nil {
		t.Error("expected error for empty webProxy name when enabled")
	}
}

func TestValidateConfig_WebProxyInvalidRemotePort(t *testing.T) {
	cfg := validCfg("inst-1")
	cfg.WebProxy = &domain.FrpWebProxy{Enabled: true, Name: "sporemind-web", RemotePort: 70000}
	if err := ValidateConfig(cfg); err == nil {
		t.Error("expected error for out-of-range webProxy remotePort")
	}
}

// TestValidateConfig_WebProxyDuplicateName: WebProxy name collides with a
// regular proxy → frpc would reject the second one at startup, so we reject
// up front.
func TestValidateConfig_WebProxyDuplicateName(t *testing.T) {
	cfg := validCfg("inst-1")
	cfg.WebProxy = &domain.FrpWebProxy{Enabled: true, Name: "ssh", RemotePort: 6080} // collides with cfg.Proxies[0]
	if err := ValidateConfig(cfg); err == nil {
		t.Error("expected error for webProxy name colliding with a regular proxy")
	}
}

// TestBuildProxyConfigurers_IncludesWebProxy: an enabled WebProxy is emitted
// as a TCP proxy targeting the hardcoded sporemind gateway port.
func TestBuildProxyConfigurers_IncludesWebProxy(t *testing.T) {
	web := &domain.FrpWebProxy{Enabled: true, Name: "sporemind-web", RemotePort: 6080}
	out, err := buildProxyConfigurers(nil, web, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 configurer, got %d", len(out))
	}
	base := out[0].GetBaseConfig()
	if base.Name != "sporemind-web" {
		t.Errorf("expected name sporemind-web, got %q", base.Name)
	}
	if base.Type != "tcp" {
		t.Errorf("expected type tcp, got %q", base.Type)
	}
	if base.LocalPort != GatewayLocalPort() {
		t.Errorf("expected localPort %d (sporemind gateway), got %d", GatewayLocalPort(), base.LocalPort)
	}
	if base.LocalIP != "127.0.0.1" {
		t.Errorf("expected localIP 127.0.0.1, got %q", base.LocalIP)
	}
}

// TestBuildProxyConfigurers_DisabledWebProxyExcluded: a disabled WebProxy is
// not emitted, so frpc never advertises an unused proxy to frps.
func TestBuildProxyConfigurers_DisabledWebProxyExcluded(t *testing.T) {
	web := &domain.FrpWebProxy{Enabled: false, Name: "sporemind-web", RemotePort: 6080}
	out, err := buildProxyConfigurers([]domain.FrpProxy{
		{Name: "ssh", Kind: "tcp", LocalPort: 22, RemotePort: 6022},
	}, web, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 configurer (web excluded), got %d", len(out))
	}
	if out[0].GetBaseConfig().Name != "ssh" {
		t.Errorf("expected only the regular ssh proxy, got %q", out[0].GetBaseConfig().Name)
	}
}

// TestBuildProxyConfigurers_WebProxyOnly: no regular proxies, just WebProxy —
// startLocked treats this as a valid "minimum one proxy" configuration.
func TestBuildProxyConfigurers_WebProxyOnly(t *testing.T) {
	web := &domain.FrpWebProxy{Enabled: true, Name: "sporemind-web", RemotePort: 6080}
	out, err := buildProxyConfigurers(nil, web, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 configurer (web only), got %d", len(out))
	}
}

// TestValidateConfig_WebProxyTcpDefaultMode: empty mode falls through to the
// legacy tcp validation path — existing persisted configs keep working.
func TestValidateConfig_WebProxyTcpDefaultMode(t *testing.T) {
	cfg := validCfg("inst-1")
	cfg.WebProxy = &domain.FrpWebProxy{Enabled: true, Name: "sporemind-web", Mode: "", RemotePort: 6080}
	if err := ValidateConfig(cfg); err != nil {
		t.Errorf("empty mode should default to tcp, got %v", err)
	}
}

func TestValidateConfig_WebProxyHttpsRequiresDomains(t *testing.T) {
	certPem, keyPem := testutil.GenTestCert(t)
	cfg := validCfg("inst-1")
	cfg.WebProxy = &domain.FrpWebProxy{
		Enabled: true, Name: "sporemind-web", Mode: "https",
		CertPem: certPem, KeyPem: keyPem,
	}
	if err := ValidateConfig(cfg); err == nil {
		t.Error("expected error: https mode without customDomains")
	}
}

func TestValidateConfig_WebProxyHttpsRequiresCert(t *testing.T) {
	cfg := validCfg("inst-1")
	cfg.WebProxy = &domain.FrpWebProxy{
		Enabled: true, Name: "sporemind-web", Mode: "https",
		CustomDomains: []string{"web.example.com"},
		KeyPem:        "ignored",
	}
	if err := ValidateConfig(cfg); err == nil {
		t.Error("expected error: https mode without certPem")
	}
}

func TestValidateConfig_WebProxyHttpsRequiresKey(t *testing.T) {
	certPem, _ := testutil.GenTestCert(t)
	cfg := validCfg("inst-1")
	cfg.WebProxy = &domain.FrpWebProxy{
		Enabled: true, Name: "sporemind-web", Mode: "https",
		CustomDomains: []string{"web.example.com"},
		CertPem:       certPem,
	}
	if err := ValidateConfig(cfg); err == nil {
		t.Error("expected error: https mode without keyPem")
	}
}

func TestValidateConfig_WebProxyHttpsRejectsBadCert(t *testing.T) {
	cfg := validCfg("inst-1")
	cfg.WebProxy = &domain.FrpWebProxy{
		Enabled: true, Name: "sporemind-web", Mode: "https",
		CustomDomains: []string{"web.example.com"},
		CertPem:       "-----BEGIN CERTIFICATE-----\nnotreallyacert\n-----END CERTIFICATE-----\n",
		KeyPem:        "-----BEGIN EC PRIVATE KEY-----\nnotreallyakey\n-----END EC PRIVATE KEY-----\n",
	}
	if err := ValidateConfig(cfg); err == nil {
		t.Error("expected error: malformed PEM should fail X509KeyPair")
	}
}

func TestValidateConfig_WebProxyHttpsValid(t *testing.T) {
	certPem, keyPem := testutil.GenTestCert(t)
	cfg := validCfg("inst-1")
	cfg.WebProxy = &domain.FrpWebProxy{
		Enabled: true, Name: "sporemind-web", Mode: "https",
		CustomDomains: []string{"web.example.com"},
		CertPem:       certPem,
		KeyPem:        keyPem,
	}
	if err := ValidateConfig(cfg); err != nil {
		t.Errorf("valid https webProxy should pass, got %v", err)
	}
}

func TestValidateConfig_WebProxyInvalidMode(t *testing.T) {
	cfg := validCfg("inst-1")
	cfg.WebProxy = &domain.FrpWebProxy{Enabled: true, Name: "sporemind-web", Mode: "ftp", RemotePort: 6080}
	if err := ValidateConfig(cfg); err == nil {
		t.Error("expected error: unsupported webProxy mode")
	}
}

// TestBuildProxyConfigurers_HttpsModeEmitsPlugin: https mode emits an
// HTTPSProxyConfig wired through the https2http plugin pointing at the sporemind
// gateway, with custom domains and the materialized cert/key paths.
func TestBuildProxyConfigurers_HttpsModeEmitsPlugin(t *testing.T) {
	web := &domain.FrpWebProxy{
		Enabled: true, Name: "sporemind-web", Mode: "https",
		CustomDomains:     []string{"web.example.com"},
		HostHeaderRewrite: "localhost",
	}
	out, err := buildProxyConfigurers(nil, web, "/tmp/cert.pem", "/tmp/key.pem")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 configurer, got %d", len(out))
	}
	httpsCfg, ok := out[0].(*frpconfig.HTTPSProxyConfig)
	if !ok {
		t.Fatalf("expected *HTTPSProxyConfig, got %T", out[0])
	}
	base := httpsCfg.GetBaseConfig()
	if base.Name != "sporemind-web" {
		t.Errorf("expected name sporemind-web, got %q", base.Name)
	}
	if base.Type != string(frpconfig.ProxyTypeHTTPS) {
		t.Errorf("expected type https, got %q", base.Type)
	}
	if len(httpsCfg.CustomDomains) != 1 || httpsCfg.CustomDomains[0] != "web.example.com" {
		t.Errorf("expected customDomains=[web.example.com], got %v", httpsCfg.CustomDomains)
	}
	if base.Plugin.Type != frpconfig.PluginHTTPS2HTTP {
		t.Errorf("expected plugin type %q, got %q", frpconfig.PluginHTTPS2HTTP, base.Plugin.Type)
	}
	plugin, ok := base.Plugin.ClientPluginOptions.(*frpconfig.HTTPS2HTTPPluginOptions)
	if !ok {
		t.Fatalf("expected *HTTPS2HTTPPluginOptions, got %T", base.Plugin.ClientPluginOptions)
	}
	if plugin.LocalAddr != fmt.Sprintf("127.0.0.1:%d", GatewayLocalPort()) {
		t.Errorf("expected LocalAddr 127.0.0.1:18080, got %q", plugin.LocalAddr)
	}
	if plugin.CrtPath != "/tmp/cert.pem" {
		t.Errorf("expected CrtPath /tmp/cert.pem, got %q", plugin.CrtPath)
	}
	if plugin.KeyPath != "/tmp/key.pem" {
		t.Errorf("expected KeyPath /tmp/key.pem, got %q", plugin.KeyPath)
	}
	if plugin.HostHeaderRewrite != "localhost" {
		t.Errorf("expected HostHeaderRewrite localhost, got %q", plugin.HostHeaderRewrite)
	}
}

// TestBuildProxyConfigurers_HttpsModeRequiresCertPath: defense — caller must
// pass non-empty cert/key paths when web mode is https.
func TestBuildProxyConfigurers_HttpsModeRequiresCertPath(t *testing.T) {
	web := &domain.FrpWebProxy{
		Enabled: true, Name: "sporemind-web", Mode: "https",
		CustomDomains: []string{"web.example.com"},
	}
	if _, err := buildProxyConfigurers(nil, web, "", ""); err == nil {
		t.Error("expected error: https mode with empty cert/key paths")
	}
}

// TestHandleStatus_KeyPemStripped: https-mode private key must never reach the
// Public status response. CertPem stays — it's public material.
func TestHandleStatus_KeyPemStripped(t *testing.T) {
	certPem, keyPem := testutil.GenTestCert(t)
	a := freshInstance(t, "inst-1")
	a.cfg.WebProxy = &domain.FrpWebProxy{
		Enabled: true, Name: "sporemind-web", Mode: "https",
		CustomDomains: []string{"web.example.com"},
		CertPem:       certPem,
		KeyPem:        keyPem,
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	resp, err := a.handleStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Config.WebProxy == nil {
		t.Fatal("expected WebProxy in response")
	}
	if resp.Config.WebProxy.KeyPem != "" {
		t.Errorf("status must strip keyPem, got %q", resp.Config.WebProxy.KeyPem)
	}
	if resp.Config.WebProxy.CertPem != certPem {
		t.Errorf("status must preserve certPem (public), got %q", resp.Config.WebProxy.CertPem)
	}
	if a.cfg.WebProxy.KeyPem != keyPem {
		t.Errorf("status must not mutate stored keyPem, got %q", a.cfg.WebProxy.KeyPem)
	}
}

// TestWriteWebProxyPEMs_WritesFiles: cert/key materialization writes both files
// with the inline PEM contents into a temp dir.
func TestWriteWebProxyPEMs_WritesFiles(t *testing.T) {
	web := &domain.FrpWebProxy{
		CertPem: "cert-bytes",
		KeyPem:  "key-bytes",
	}
	dir, err := writeWebProxyPEMs(web)
	if err != nil {
		t.Fatalf("writeWebProxyPEMs: %v", err)
	}
	defer os.RemoveAll(dir)

	certBytes, err := os.ReadFile(filepath.Join(dir, "cert.pem"))
	if err != nil {
		t.Fatalf("read cert.pem: %v", err)
	}
	if string(certBytes) != "cert-bytes" {
		t.Errorf("cert.pem mismatch, got %q", string(certBytes))
	}
	keyBytes, err := os.ReadFile(filepath.Join(dir, "key.pem"))
	if err != nil {
		t.Fatalf("read key.pem: %v", err)
	}
	if string(keyBytes) != "key-bytes" {
		t.Errorf("key.pem mismatch, got %q", string(keyBytes))
	}
}

// TestHandleStatus_WebProxyDeepCopied: snapshotLocked must deep-copy the
// WebProxy pointer so external callers can't mutate the actor's internal cfg.
func TestHandleStatus_WebProxyDeepCopied(t *testing.T) {
	a := freshInstance(t, "inst-1")
	a.cfg.WebProxy = &domain.FrpWebProxy{Enabled: true, Name: "sporemind-web", RemotePort: 6080}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	resp, err := a.handleStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Config.WebProxy == nil {
		t.Fatal("expected WebProxy in response")
	}
	if resp.Config.WebProxy == a.cfg.WebProxy {
		t.Error("response WebProxy must be a deep copy, not the same pointer")
	}
	resp.Config.WebProxy.RemotePort = 9999
	if a.cfg.WebProxy.RemotePort != 6080 {
		t.Errorf("mutating response must not affect stored cfg, got %d", a.cfg.WebProxy.RemotePort)
	}
}

// TestHandleStatus_LegacyMode: when LegacyMode is set (or auto-detected),
// DetectedVersion must include the "(legacy)" suffix.
func TestHandleStatus_LegacyMode(t *testing.T) {
	a := freshInstance(t, "inst-1")
	a.cfg.LegacyMode = true
	ctx := testutil.HumanCtx(testutil.GenActorID())
	resp, err := a.handleStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := FrpVersion + " (legacy)"
	if resp.Status.DetectedVersion != want {
		t.Errorf("expected DetectedVersion=%q, got %q", want, resp.Status.DetectedVersion)
	}
}

// TestDetectServerCompatibility_InvalidAddr rejects malformed addresses
// before attempting any network I/O.
func TestDetectServerCompatibility_InvalidAddr(t *testing.T) {
	_, err := DetectServerCompatibility("not-a-valid-addr", "", false)
	if err == nil {
		t.Error("expected error for invalid address")
	}
}

// TestIsVersionMismatchError classifies known version-mismatch strings.
func TestIsVersionMismatchError(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"session shutdown", true},
		{"protocol version mismatch", true},
		{"version mismatch detected", true},
		{"authentication failed", false},
		{"connection refused", false},
		{"", false},
	}
	for _, tc := range tests {
		got := isVersionMismatchError(fmt.Errorf("%s", tc.msg))
		if got != tc.want {
			t.Errorf("isVersionMismatchError(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

// TestDetectServerCompatibility_UnreachableTimesOut: an unroutable server
// address (RFC 5737 TEST-NET-1, dropped by real networks) must fail fast with
// an error instead of hanging on the OS-level TCP connect timeout. The
// pre-fix probe had no dependable dial bound, so a black-holed address could
// stall the caller (frpmanager.detect) for the OS default timeout (~2min).
func TestDetectServerCompatibility_UnreachableTimesOut(t *testing.T) {
	start := time.Now()
	legacy, err := DetectServerCompatibility("192.0.2.1:7000", "probe-token", false)
	if err == nil {
		t.Fatalf("expected error for unreachable server, got legacy=%v err=nil", legacy)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("detect must return within 3s for an unreachable server, took %v", elapsed)
	}
}

// TestOnStart_RegistersOpsLane pins the lane topology that keeps status
// responsive during a reconcile: configure is registered on the "frp_ops"
// stateful loop (so its blocking IO never occupies the owner lane); status is
// a stateless (PureContext) snapshot read that runs on the forked pure loop.
func TestOnStart_RegistersOpsLane(t *testing.T) {
	cfg := validCfg("inst-1")
	cfg.Disabled = true // skip the auto-start path's real dial in a unit test
	a, ok := NewActor(cfg)().(*Actor)
	if !ok {
		t.Fatal("NewActor: expected *Actor")
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	if got := ctx.Loops["frp_ops"]; got != actor.ModeStateful {
		t.Errorf("expected frp_ops registered as ModeStateful, got %v", got)
	}
	if got := actor.ResolveLoopOrDefault(actor.ModeStateful, ctx.RegOpts["frpinstance.configure"]...); got != "frp_ops" {
		t.Errorf("expected configure pinned to frp_ops, got %q", got)
	}
	statusFn, ok := ctx.Regs["frpinstance.status"]
	if !ok {
		t.Fatal("frpinstance.status not registered")
	}
	pure := reflect.TypeOf((*actor.PureContext)(nil)).Elem()
	if ft := reflect.TypeOf(statusFn); ft == nil || ft.NumIn() < 1 || ft.In(0) != pure {
		t.Errorf("expected frpinstance.status to be stateless (PureContext), got %T", statusFn)
	}
}

// TestHandleStatus_ImmediateDuringConfigure: while a configure reconcile is in
// flight (here: startService blocked in the 2s pre-flight dial to an
// unroutable server), handleStatus must keep answering immediately. The
// pre-fix handler held a.mu across the whole probe, blocking status for
// seconds; the lane split only helps if the lock is also released during
// blocking IO, which is what this test exercises at the method level.
func TestHandleStatus_ImmediateDuringConfigure(t *testing.T) {
	a := freshInstance(t, "inst-1")
	ctx := testutil.HumanCtx(testutil.GenActorID())

	cfg := validCfg("inst-1")
	cfg.ServerAddr = "192.0.2.1:7000" // unroutable: pre-flight dial blocks ~2s
	cfg.Disabled = false              // configure tries to start → hits the probe

	configureDone := make(chan struct{})
	go func() {
		defer close(configureDone)
		// Expected to fail with "unreachable"; the return value is irrelevant.
		_, _ = a.handleConfigure(ctx, domain.FrpInstanceConfigureReq{Config: cfg})
	}()

	// Give the configure goroutine time to reach the probe phase.
	time.Sleep(50 * time.Millisecond)

	probeStart := time.Now()
	for time.Since(probeStart) < 500*time.Millisecond {
		callStart := time.Now()
		if _, err := a.handleStatus(ctx); err != nil {
			t.Fatalf("handleStatus during configure: %v", err)
		}
		if elapsed := time.Since(callStart); elapsed > 250*time.Millisecond {
			t.Errorf("handleStatus took %v while configure was in flight (must return immediately)", elapsed)
		}
		time.Sleep(5 * time.Millisecond)
	}

	select {
	case <-configureDone:
		t.Fatal("configure finished before the probe window elapsed; test is not exercising the in-flight path")
	default:
	}
	<-configureDone
}
