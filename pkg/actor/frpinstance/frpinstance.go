// Package frpinstance runs a single embedded frpc client. One instance per
// persisted FrpInstanceConfig held by frpmanager.
//
// Lifecycle:
//   - Manager spawns NewActor(cfg) → constructor seeds a.cfg
//   - OnStart auto-starts frpc unless cfg.Disabled
//   - configure (Internal, on the "frp_ops" loop) is the manager-owned
//     hot-update entrypoint; reconciles target state from cfg.Disabled and
//     restarts frpc with new config when it should be running. It lives on a
//     dedicated stateful loop (not the owner lane) because the reconcile does
//     blocking IO — graceful shutdown, legacy-server probe, PEM
//     materialization — and the Public status callable must keep answering
//     while a restart is in flight.
//
// Token lives in a.cfg (not exposed as a component); the Public status
// callable strips it before returning.
package frpinstance

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	frpclient "github.com/fatedier/frp/client"
	frpproxy "github.com/fatedier/frp/client/proxy"
	frpconfig "github.com/fatedier/frp/pkg/config/v1"
	frplog "github.com/fatedier/frp/pkg/util/log"
	golib "github.com/fatedier/golib/log"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
)

const FrpVersion = "v0.67.0"

// GatewayLocalPort extracts the TCP port from the configured gateway address.
func GatewayLocalPort() int {
	_, port, err := net.SplitHostPort(config.GatewayAddr())
	if err != nil || port == "" {
		return config.DefaultGatewayPort
	}
	p, _ := strconv.Atoi(port)
	if p <= 0 {
		return config.DefaultGatewayPort
	}
	return p
}

// FrpWebProxyMode values.
const (
	WebProxyModeTCP   = "tcp"
	WebProxyModeHTTPS = "https"
)

// webProxyMode normalizes empty mode to the legacy TCP default.
func webProxyMode(web *domain.FrpWebProxy) string {
	if web == nil || web.Mode == "" {
		return WebProxyModeTCP
	}
	return web.Mode
}

// Actor owns one frpc client. cfg is seeded by NewActor and overwritten by
// handleConfigure; nothing is persisted on disk — the manager holds the
// durable record. webPEMDir is the per-run temp dir holding cert/key files
// materialized from FrpWebProxy.CertPem/KeyPem for the https2http plugin;
// it lives only while frpc is running and is removed on stop.
type Actor struct {
	actor.Host

	mu             sync.Mutex
	cfg            domain.FrpInstanceConfig
	svc            *frpclient.Service
	cancel         context.CancelFunc
	done           chan struct{}
	running        bool
	startedAt      time.Time
	lastErr        string
	webPEMDir      string
	detectedLegacy bool // set by probe or auto-fallback; not persisted
}

// NewActor returns a factory closure that captures the seed config.
func NewActor(cfg domain.FrpInstanceConfig) func() actor.Actor {
	return func() actor.Actor {
		return &Actor{cfg: cfg}
	}
}

func (a *Actor) Type() string { return "frpinstance" }

func (a *Actor) OnStart(ctx actor.Context) error {
	ctx.Logger().Info("frpinstance: starting",
		"id", a.cfg.ID,
		"name", a.cfg.Name,
		"server", a.cfg.ServerAddr,
		"disabled", a.cfg.Disabled,
		"frp_version", FrpVersion,
	)
	if err := ctx.Register("frpinstance.status", a.handleStatus, actor.Public()); err != nil {
		return fmt.Errorf("frpinstance: register status: %w", err)
	}
	// Ops lane: configure performs blocking IO (graceful teardown wait,
	// legacy-server compatibility probe, PEM materialization). It must not
	// occupy the owner lane, or frpinstance.status — the same loop — would
	// queue behind a restart for seconds. status stays on the owner lane,
	// which is µs-fast (mutex snapshot only).
	if err := ctx.RegisterLoop("frp_ops", actor.ModeStateful); err != nil {
		return fmt.Errorf("frpinstance: register ops loop: %w", err)
	}
	if err := ctx.Register("frpinstance.configure", a.handleConfigure, actor.Internal(), actor.WithLoop("frp_ops")); err != nil {
		return fmt.Errorf("frpinstance: register configure: %w", err)
	}
	if a.cfg.Disabled {
		return nil
	}
	if err := a.startService(ctx); err != nil {
		a.mu.Lock()
		a.lastErr = err.Error()
		a.mu.Unlock()
		ctx.Logger().Error("frpinstance: auto-start failed", "id", a.cfg.ID, "error", err)
	}
	return nil
}

func (a *Actor) OnStop(ctx actor.Context) error {
	a.stopService(ctx)
	return nil
}

// handleStatus is a stateless (PureContext) snapshot read: it takes a.mu and
// returns the sanitized public view without mutating state, so it runs on the
// forked pure loop instead of the owner lane.
func (a *Actor) handleStatus(_ actor.PureContext) (domain.FrpInstance, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snapshotLocked(nil), nil
}

// handleConfigure replaces the in-memory cfg and reconciles frpc state:
// if !Disabled it (re)starts; if Disabled it stops. Visibility is Internal —
// the manager is the only intended caller.
//
// Lane discipline: registered on the "frp_ops" loop (see OnStart), so the
// reconcile's blocking IO never occupies the owner lane. It also never holds
// a.mu across a blocking phase — stopService/startService detach state under
// the mutex then run the slow work unlocked — so the owner-lane status
// callable keeps answering immediately while a reconcile is in flight.
func (a *Actor) handleConfigure(ctx actor.Context, req domain.FrpInstanceConfigureReq) (domain.FrpInstance, error) {
	if err := ValidateConfig(req.Config); err != nil {
		return domain.FrpInstance{}, fmt.Errorf("frpinstance.configure: %w", err)
	}
	// Reconcile target is "stopped": stop any running frpc first. stopService
	// returns with a.mu free during the graceful-close wait.
	a.stopService(ctx)
	a.mu.Lock()
	a.cfg = req.Config
	if a.cfg.Disabled {
		// A deliberate stop supersedes any prior runtime error so the UI
		// shows "stopped", not a stale error.
		a.lastErr = ""
	}
	a.mu.Unlock()
	if !a.cfg.Disabled {
		if err := a.startService(ctx); err != nil {
			a.mu.Lock()
			a.lastErr = err.Error()
			a.mu.Unlock()
			return domain.FrpInstance{}, fmt.Errorf("frpinstance.configure: restart: %w", err)
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snapshotLocked(ctx), nil
}

// snapshotLocked returns the sanitized public view. Caller must hold a.mu.
// PureContext only (it performs no context work) so both the stateless status
// read and the stateful configure handler can call it.
func (a *Actor) snapshotLocked(_ actor.PureContext) domain.FrpInstance {
	cfg := a.cfg
	cfg.Token = ""
	cfg.Proxies = append([]domain.FrpProxy(nil), a.cfg.Proxies...)
	if a.cfg.WebProxy != nil {
		webCopy := *a.cfg.WebProxy
		webCopy.KeyPem = "" // secret — never expose via Public callables
		webCopy.CustomDomains = append([]string(nil), a.cfg.WebProxy.CustomDomains...)
		cfg.WebProxy = &webCopy
	}

	detectedVer := FrpVersion
	if a.cfg.LegacyMode || a.detectedLegacy {
		detectedVer += " (legacy)"
	}
	status := domain.FrpInstanceStatus{
		ID:              a.cfg.ID,
		Running:         a.running,
		DetectedVersion: detectedVer,
		Error:           a.lastErr,
	}
	if a.running {
		status.Proxies = a.liveProxyStatusesLocked()
		status.Connected = allProxiesRunning(status.Proxies)
	}
	if a.running && !a.startedAt.IsZero() {
		status.StartedAt = a.startedAt.UTC().Format(time.RFC3339)
	}
	return domain.FrpInstance{
		Config: cfg,
		Status: status,
	}
}

// liveProxyStatusesLocked reads each configured proxy's live phase from the
// running frpc. Caller must hold a.mu. Before the first login completes the
// service holds no controller, so every lookup misses — that state is
// reported as frpproxy.ProxyPhaseNew ("new"), i.e. still connecting.
func (a *Actor) liveProxyStatusesLocked() []domain.FrpProxyStatus {
	if a.svc == nil {
		return nil
	}
	names := make([]string, 0, len(a.cfg.Proxies)+1)
	for _, p := range a.cfg.Proxies {
		names = append(names, p.Name)
	}
	if webProxyActive(a.cfg.WebProxy) {
		names = append(names, a.cfg.WebProxy.Name)
	}
	exporter := a.svc.StatusExporter()
	out := make([]domain.FrpProxyStatus, 0, len(names))
	for _, name := range names {
		st := domain.FrpProxyStatus{Name: name, Status: frpproxy.ProxyPhaseNew}
		if ws, ok := exporter.GetProxyStatus(name); ok && ws != nil {
			st.Status = ws.Phase
			st.Err = ws.Err
			st.RemoteAddr = ws.RemoteAddr
		}
		out = append(out, st)
	}
	return out
}

// allProxiesRunning is the true tunnel-up judgment: every configured proxy
// must have reached the running phase (server accepted it end-to-end). An
// empty set means no tunnel is established.
func allProxiesRunning(proxies []domain.FrpProxyStatus) bool {
	if len(proxies) == 0 {
		return false
	}
	for _, p := range proxies {
		if p.Status != frpproxy.ProxyPhaseRunning {
			return false
		}
	}
	return true
}

// startService launches the frpc service. Lock discipline: a.mu is held only
// for the short field-snapshot and commit phases; the blocking work — legacy
// compatibility probe (network) and PEM materialization (file IO) — runs
// WITHOUT the lock, so the owner-lane status handler stays responsive during a
// (re)start. Callers must NOT hold a.mu.
func (a *Actor) startService(ctx actor.Context) error {
	a.mu.Lock()
	if a.cfg.ServerAddr == "" {
		a.mu.Unlock()
		return fmt.Errorf("server address is empty")
	}
	if len(a.cfg.Proxies) == 0 && !webProxyActive(a.cfg.WebProxy) {
		a.mu.Unlock()
		return fmt.Errorf("no proxies configured")
	}
	host, port, err := splitHostPort(a.cfg.ServerAddr)
	if err != nil {
		a.mu.Unlock()
		return fmt.Errorf("invalid serverAddr %q: %w", a.cfg.ServerAddr, err)
	}
	serverAddr := a.cfg.ServerAddr
	token := a.cfg.Token
	tlsEnable := a.cfg.Tls
	tcpMux := !a.cfg.LegacyMode
	probeCompatibility := !a.cfg.LegacyMode && !a.detectedLegacy
	var webCfg *domain.FrpWebProxy
	if a.cfg.WebProxy != nil {
		web := *a.cfg.WebProxy
		web.CustomDomains = append([]string(nil), a.cfg.WebProxy.CustomDomains...)
		webCfg = &web
	}
	a.mu.Unlock()

	// LoginFailExit=true: a failed first login (bad token, unreachable
	// server, protocol mismatch) must surface as an error instead of being
	// retried forever while the UI keeps showing "running". Reconnects after
	// a successful first login still retry indefinitely inside frpc's
	// keepControllerWorking loop, so only the initial dial is fail-fast.
	loginFailExit := true

	// First start — probe server compatibility before committing to TCPMux.
	// Runs unlocked: the probe is network IO bounded by dialTimeout/probeTimeout.
	if probeCompatibility {
		legacyNeeded, err := DetectServerCompatibility(serverAddr, token, tlsEnable)
		if err == nil && legacyNeeded {
			tcpMux = false
			a.mu.Lock()
			a.detectedLegacy = true
			a.mu.Unlock()
			ctx.Logger().Info("frpinstance: detected legacy server, disabling TCPMux",
				"id", a.cfg.ID, "server", serverAddr)
		}
	}
	common := &frpconfig.ClientCommonConfig{
		ServerAddr:    host,
		ServerPort:    port,
		LoginFailExit: &loginFailExit,
		Auth:          frpconfig.AuthClientConfig{Token: token},
		Transport: frpconfig.ClientTransportConfig{
			TCPMux: &tcpMux,
			TLS:    frpconfig.TLSClientConfig{Enable: &tlsEnable},
		},
	}

	// For https mode the https2http plugin reads cert/key from disk, so we
	// materialize the inline PEMs into a temp dir owned by this Actor.
	// Runs unlocked: it is file IO. The dir is adopted into a.webPEMDir only
	// in the commit phase below; error paths remove it locally.
	var certPath, keyPath, webPEMDir string
	if webProxyActive(webCfg) && webProxyMode(webCfg) == WebProxyModeHTTPS {
		dir, err := writeWebProxyPEMs(webCfg)
		if err != nil {
			return fmt.Errorf("materialize webProxy cert/key: %w", err)
		}
		webPEMDir = dir
		certPath = filepath.Join(dir, "cert.pem")
		keyPath = filepath.Join(dir, "key.pem")
	}

	// Commit phase (fast, under mu): build the service and publish runtime
	// state. Configure runs serialized on the frp_ops lane, so a.cfg cannot
	// be swapped between the snapshot above and this re-read.
	a.mu.Lock()
	defer a.mu.Unlock()

	// Belt-and-braces: a prior run that exited via the goroutine error path
	// (frpc crashed without going through stopService) may have left a PEM dir
	// behind. Sweep it before adopting the new one.
	a.cleanupWebPEMDirLocked(ctx)

	proxies, err := buildProxyConfigurers(a.cfg.Proxies, a.cfg.WebProxy, certPath, keyPath)
	if err != nil {
		if webPEMDir != "" {
			_ = os.RemoveAll(webPEMDir)
		}
		return err
	}

	svc, err := frpclient.NewService(frpclient.ServiceOptions{
		Common:    common,
		ProxyCfgs: proxies,
	})
	if err != nil {
		if webPEMDir != "" {
			_ = os.RemoveAll(webPEMDir)
		}
		return fmt.Errorf("create frpc service: %w", err)
	}

	// Redirect frpc's internal logger so every line goes through the
	// gospore format (INFO/WARN/ERROR) instead of frpc's native [I]/[W]/[E].
	frplog.Logger = frplog.Logger.WithOptions(
		golib.WithOutput(&gosporeLogBridge{logger: ctx.Logger()}),
		golib.WithCaller(false),
	)

	runCtx, cancel := context.WithCancel(ctx.Lifecycle())
	done := make(chan struct{})
	a.svc = svc
	a.cancel = cancel
	a.done = done
	a.running = true
	a.startedAt = time.Now()
	a.lastErr = ""
	a.webPEMDir = webPEMDir

	logger := ctx.Logger()
	cfgID := a.cfg.ID
	server := serverAddr
	proxyCount := len(a.cfg.Proxies)
	go func() {
		defer close(done)
		logger.Info("frpinstance: frpc running",
			"id", cfgID,
			"server", server,
			"proxies", proxyCount,
		)
		if err := svc.Run(runCtx); err != nil {
			logger.Error("frpinstance: frpc stopped with error", "id", cfgID, "error", err)
			// Run already returned, so release runCtx explicitly — the
			// fail-fast login path exits before anyone cancels it.
			cancel()
			a.mu.Lock()
			if a.svc == svc {
				// Not superseded by a newer start: record the failure and
				// clear runtime state so stopService sees a clean slate.
				a.lastErr = err.Error()
				a.running = false
				a.svc = nil
				a.cancel = nil
				a.done = nil
				a.cleanupWebPEMDirLocked(ctx)
			}
			a.mu.Unlock()
			return
		}
		logger.Info("frpinstance: frpc stopped", "id", cfgID)
		a.mu.Lock()
		a.running = false
		a.cleanupWebPEMDirLocked(ctx)
		a.mu.Unlock()
	}()
	return nil
}

// stopService tears down the running frpc service. Lock discipline: a.mu is
// held only to detach runtime state; the graceful-close wait runs WITHOUT the
// lock so the owner-lane status handler stays responsive during a stop. The
// detached svc is recognised by the run goroutine's error path (which checks
// `a.svc == svc`), so no double-cleanup occurs. Callers must NOT hold a.mu.
func (a *Actor) stopService(ctx actor.Context) {
	a.mu.Lock()
	if !a.running || a.svc == nil {
		a.mu.Unlock()
		return
	}
	ctx.Logger().Info("frpinstance: stopping frpc", "id", a.cfg.ID)
	svc := a.svc
	done := a.done
	if a.cancel != nil {
		a.cancel()
	}
	svc.GracefulClose(3 * time.Second)
	// Detach now: status snapshots taken during the wait below already see
	// the reconciled "stopped" state.
	a.svc = nil
	a.cancel = nil
	a.done = nil
	a.running = false
	// A deliberate stop is the new state; clear any stale runtime error so
	// the UI shows "stopped" instead of a now-irrelevant failure message.
	a.lastErr = ""
	a.cleanupWebPEMDirLocked(ctx)
	a.mu.Unlock()

	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			ctx.Logger().Warn("frpinstance: frpc shutdown timed out", "id", a.cfg.ID)
		}
	}
}

// cleanupWebPEMDirLocked removes the per-run cert/key materialization dir.
// Caller must hold a.mu. Safe to call when no dir was created.
func (a *Actor) cleanupWebPEMDirLocked(ctx actor.Context) {
	if a.webPEMDir == "" {
		return
	}
	if err := os.RemoveAll(a.webPEMDir); err != nil {
		ctx.Logger().Warn("frpinstance: remove webProxy PEM dir failed",
			"id", a.cfg.ID, "dir", a.webPEMDir, "error", err)
	}
	a.webPEMDir = ""
}

// writeWebProxyPEMs materializes inline PEM cert/key into a fresh 0700 dir so
// the frpc https2http plugin can read them as files. Caller owns the returned
// dir lifecycle (clean up via os.RemoveAll). Files are written 0600.
func writeWebProxyPEMs(web *domain.FrpWebProxy) (string, error) {
	dir, err := os.MkdirTemp("", "sporemind-frpweb-*")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certPath, []byte(web.CertPem), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	if err := os.WriteFile(keyPath, []byte(web.KeyPem), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// ValidateConfig is exported so the manager can validate before persisting.
func ValidateConfig(cfg domain.FrpInstanceConfig) error {
	if cfg.ServerAddr == "" {
		return fmt.Errorf("serverAddr is required")
	}
	if _, _, err := splitHostPort(cfg.ServerAddr); err != nil {
		return fmt.Errorf("invalid serverAddr %q: %w", cfg.ServerAddr, err)
	}
	names := make(map[string]bool, len(cfg.Proxies)+1)
	for i, p := range cfg.Proxies {
		if p.Name == "" {
			return fmt.Errorf("proxies[%d].name is required", i)
		}
		if names[p.Name] {
			return fmt.Errorf("duplicate proxy name %q", p.Name)
		}
		names[p.Name] = true
		switch p.Kind {
		case "tcp", "udp":
		default:
			return fmt.Errorf("proxies[%d].kind must be tcp or udp, got %q", i, p.Kind)
		}
		if p.LocalPort <= 0 || p.LocalPort > 65535 {
			return fmt.Errorf("proxies[%d].localPort out of range", i)
		}
		if p.RemotePort <= 0 || p.RemotePort > 65535 {
			return fmt.Errorf("proxies[%d].remotePort out of range", i)
		}
	}
	if webProxyActive(cfg.WebProxy) {
		if cfg.WebProxy.Name == "" {
			return fmt.Errorf("webProxy.name is required when enabled")
		}
		if names[cfg.WebProxy.Name] {
			return fmt.Errorf("duplicate proxy name %q (collides with webProxy)", cfg.WebProxy.Name)
		}
		switch webProxyMode(cfg.WebProxy) {
		case WebProxyModeTCP:
			if cfg.WebProxy.RemotePort <= 0 || cfg.WebProxy.RemotePort > 65535 {
				return fmt.Errorf("webProxy.remotePort out of range")
			}
		case WebProxyModeHTTPS:
			if len(cfg.WebProxy.CustomDomains) == 0 {
				return fmt.Errorf("webProxy.customDomains is required for https mode")
			}
			for i, d := range cfg.WebProxy.CustomDomains {
				if d == "" {
					return fmt.Errorf("webProxy.customDomains[%d] is empty", i)
				}
			}
			if cfg.WebProxy.CertPem == "" {
				return fmt.Errorf("webProxy.certPem is required for https mode")
			}
			if cfg.WebProxy.KeyPem == "" {
				return fmt.Errorf("webProxy.keyPem is required for https mode")
			}
			if _, err := tls.X509KeyPair([]byte(cfg.WebProxy.CertPem), []byte(cfg.WebProxy.KeyPem)); err != nil {
				return fmt.Errorf("webProxy cert/key invalid: %w", err)
			}
		default:
			return fmt.Errorf("webProxy.mode %q unsupported (want tcp or https)", cfg.WebProxy.Mode)
		}
	}
	return nil
}

func buildProxyConfigurers(in []domain.FrpProxy, web *domain.FrpWebProxy, webCertPath, webKeyPath string) ([]frpconfig.ProxyConfigurer, error) {
	capacity := len(in)
	if webProxyActive(web) {
		capacity++
	}
	out := make([]frpconfig.ProxyConfigurer, 0, capacity)
	for i, p := range in {
		localIP := p.LocalIP
		if localIP == "" {
			localIP = "127.0.0.1"
		}
		base := frpconfig.ProxyBaseConfig{
			Name: p.Name,
			Type: p.Kind,
			ProxyBackend: frpconfig.ProxyBackend{
				LocalIP:   localIP,
				LocalPort: int(p.LocalPort),
			},
		}
		switch p.Kind {
		case "tcp":
			out = append(out, &frpconfig.TCPProxyConfig{
				ProxyBaseConfig: base,
				RemotePort:      int(p.RemotePort),
			})
		case "udp":
			out = append(out, &frpconfig.UDPProxyConfig{
				ProxyBaseConfig: base,
				RemotePort:      int(p.RemotePort),
			})
		default:
			return nil, fmt.Errorf("buildProxyConfigurers: proxies[%d].kind %q unsupported", i, p.Kind)
		}
	}
	if webProxyActive(web) {
		switch webProxyMode(web) {
		case WebProxyModeTCP:
			out = append(out, &frpconfig.TCPProxyConfig{
				ProxyBaseConfig: frpconfig.ProxyBaseConfig{
					Name: web.Name,
					Type: "tcp",
					ProxyBackend: frpconfig.ProxyBackend{
						LocalIP:   "127.0.0.1",
						LocalPort: GatewayLocalPort(),
					},
				},
				RemotePort: int(web.RemotePort),
			})
		case WebProxyModeHTTPS:
			if webCertPath == "" || webKeyPath == "" {
				return nil, fmt.Errorf("buildProxyConfigurers: https webProxy requires cert/key paths")
			}
			plugin := &frpconfig.HTTPS2HTTPPluginOptions{
				Type:              frpconfig.PluginHTTPS2HTTP,
				LocalAddr:         fmt.Sprintf("127.0.0.1:%d", GatewayLocalPort()),
				HostHeaderRewrite: web.HostHeaderRewrite,
				CrtPath:           webCertPath,
				KeyPath:           webKeyPath,
			}
			out = append(out, &frpconfig.HTTPSProxyConfig{
				ProxyBaseConfig: frpconfig.ProxyBaseConfig{
					Name: web.Name,
					Type: string(frpconfig.ProxyTypeHTTPS),
					ProxyBackend: frpconfig.ProxyBackend{
						Plugin: frpconfig.TypedClientPluginOptions{
							Type:                frpconfig.PluginHTTPS2HTTP,
							ClientPluginOptions: plugin,
						},
					},
				},
				DomainConfig: frpconfig.DomainConfig{
					CustomDomains: append([]string(nil), web.CustomDomains...),
				},
			})
		default:
			return nil, fmt.Errorf("buildProxyConfigurers: webProxy.mode %q unsupported", web.Mode)
		}
	}
	return out, nil
}

// webProxyActive reports whether the web proxy is set and turned on.
func webProxyActive(web *domain.FrpWebProxy) bool {
	return web != nil && web.Enabled
}

func splitHostPort(addr string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port %q: %w", portStr, err)
	}
	return host, port, nil
}

// gosporeLogBridge is an io.Writer that receives frpc's formatted log lines
// and re-emits them through the gospore actor.Logger. This unifies all log
// output to the gospore format (INFO/WARN/ERROR) so captureGoLogs sees one
// consistent layout.
type gosporeLogBridge struct {
	logger actor.Logger
}

func (b *gosporeLogBridge) Write(p []byte) (int, error) {
	line := string(p)
	// frpc golib format: "2026-05-30 04:19:42.299 [I] [file:line] [reqID] text\n"
	// Strip timestamp, extract level code, and forward the rest.
	if idx := strings.Index(line, "] "); idx > 0 && idx < 30 {
		// Find the level tag: "[I]", "[W]", "[E]", "[D]", "[T]"
		prefix := line[:idx+2] // includes "] "
		rest := line[idx+2:]
		// Trim leading timestamp to get to level tag
		tsEnd := strings.Index(prefix, "[")
		if tsEnd >= 0 {
			tag := prefix[tsEnd : tsEnd+3] // e.g. "[I]"
			msg := strings.TrimRight(rest, "\n")
			switch tag {
			case "[D]", "[T]":
				b.logger.Debug(msg)
			case "[I]":
				b.logger.Info(msg)
			case "[W]":
				b.logger.Warn(msg)
			case "[E]":
				b.logger.Error(msg)
			default:
				b.logger.Info(msg)
			}
			return len(p), nil
		}
	}
	b.logger.Info(strings.TrimRight(line, "\n"))
	return len(p), nil
}
