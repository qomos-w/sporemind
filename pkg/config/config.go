package config

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/qomos-w/sporemind/pkg/persist"
	"gopkg.in/yaml.v2"
)

const fileName = "sporemind.yaml"
const actorDirName = ".actors"
const (
	gatewayAddrEnv     = "SPOREMIND_GATEWAY_ADDR"
	memoryLimitEnv     = "SPOREMIND_MEMORY_LIMIT"
	cloudAPIURLEnv     = "SPOREMIND_CLOUD_API_URL"
	sporemindWebURLEnv = "SPOREMIND_SPOREMIND_WEB_URL"
)

// DefaultCloudAPIURL is the sporemind cloud backend base URL used when
// SPOREMIND_CLOUD_API_URL is unset. Production builds talk to the public API;
// local dev overrides it via the env var (or sporemind.yaml).
const DefaultCloudAPIURL = "https://api.sporemind.ai"

// DefaultSporemindWebURL is the public sporemind website base URL. The desktop
// login overlay (M3-3) opens this site's login page in a captive WebView2
// window locked to the sporemind domain. Production overrides it via
// SPOREMIND_SPOREMIND_WEB_URL.
const DefaultSporemindWebURL = "https://www.sporemind.ai"

// DefaultGatewayAddr is the single source of truth for the gateway listen
// address used when sporemind.yaml omits gateway_addr and no env override
// is set. It binds loopback only; LAN access ("0.0.0.0" / ":18080") is an
// explicit opt-in from the mobile sync panel. Every package that needs a
// default or fallback must reference these constants instead of
// re-hardcoding a port, so the value can never drift between the YAML
// reader, the runtime listener, frp, and the LAN URL builder.
const (
	DefaultGatewayAddr = "127.0.0.1:18080"
	// DefaultGatewayPort is the numeric TCP port component of
	// DefaultGatewayAddr for callers that need an int.
	DefaultGatewayPort = 18080
)

// DefaultCookieBridgeAddr is the loopback listener of the cookiebridge actor
// (Chrome cookie migration bridge: MCP endpoint /mcp plus extension endpoints
// /push and /pending). Loopback only; a different port may be configured via
// cookie_bridge_addr in sporemind.yaml, but the host must stay loopback.
const DefaultCookieBridgeAddr = "127.0.0.1:47613"

type config struct {
	GatewayAddr       string   `yaml:"gateway_addr"`
	GatewayBindAddrs  []string `yaml:"gateway_bind_addrs"`
	DataDir           string   `yaml:"data_dir"`
	Namespace         string   `yaml:"namespace"`
	Backend           string   `yaml:"backend"`
	BackendEndpoint   string   `yaml:"backend_endpoint"`
	BackendDatabase   string   `yaml:"backend_database"`
	BackendTunnel     string   `yaml:"backend_tunnel"`
	BackendAIStats    string   `yaml:"backend_aistats"`
	BackendLogs       string   `yaml:"backend_logs"`
	AppStateMaxDocs   int      `yaml:"appstate_max_docs_per_app"`
	AppStateMaxValue  int64    `yaml:"appstate_max_value_bytes"`
	AppStateMaxAppend int64    `yaml:"appstate_max_append_bytes"`
	Locale            string   `yaml:"locale"`
	DesktopTransport  string   `yaml:"desktop_transport"`
	CookieBridgeAddr  string   `yaml:"cookie_bridge_addr"`
	LogsRetentionDays int      `yaml:"logs_retention_days"`
	AistatsRawDays    int      `yaml:"aistats_raw_days"`
	AistatsDailyDays  int      `yaml:"aistats_daily_days"`
}

func init() {
	var err error
	exeDir, err = getExeDir()
	if err != nil {
		// Fallback to cwd so the binary can still start.
		exeDir, _ = os.Getwd()
	}

	path := filepath.Join(exeDir, fileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg = defaultConfig()
			_ = writeDefault(path, cfg)
			return
		}
		panic(fmt.Sprintf("config: read %s: %v", path, err))
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		panic(fmt.Sprintf("config: parse %s: %v", path, err))
	}

	cfg.GatewayAddr = resolveGatewayDefault(cfg.GatewayAddr)
	if cfg.DataDir == "" {
		cfg.DataDir = defaultDataDir()
	}
	if cfg.Namespace == "" {
		cfg.Namespace = "sporemind"
	}
	if cfg.DesktopTransport == "" {
		cfg.DesktopTransport = "wails"
	}
	if cfg.Backend == "" {
		cfg.Backend = "fs"
	}
	if err := validateBackend(cfg.Backend); err != nil {
		panic(fmt.Sprintf("config: %s: %v", path, err))
	}
	if cfg.BackendAIStats == "" {
		cfg.BackendAIStats = "fs"
	}
	if err := validateScopedBackend(cfg.BackendAIStats); err != nil {
		panic(fmt.Sprintf("config: %s: %v", path, err))
	}
	if cfg.BackendLogs == "" {
		cfg.BackendLogs = "fs"
	}
	if err := validateScopedBackend(cfg.BackendLogs); err != nil {
		panic(fmt.Sprintf("config: %s: %v", path, err))
	}
}

func defaultConfig() config {
	return config{
		// The stock default is written to sporemind.yaml so a shared config
		// file never bakes a flavor-specific (or ephemeral ":0") address into
		// a lane the flavor remapping cannot recognize back. devrelease
		// builds remap it at load time via resolveGatewayDefault.
		GatewayAddr:       DefaultGatewayAddr,
		DataDir:           defaultDataDir(),
		Namespace:         "sporemind",
		Backend:           "fs",
		BackendAIStats:    "fs",
		BackendLogs:       "fs",
		Locale:            "zh-CN",
		DesktopTransport:  "wails",
		LogsRetentionDays: DefaultLogsRetentionDays,
		AistatsRawDays:    DefaultAistatsRawDays,
		AistatsDailyDays:  DefaultAistatsDailyDays,
		AppStateMaxDocs:   1024,
		AppStateMaxValue:  1 << 20,
		AppStateMaxAppend: 4 << 20,
	}
}

func writeDefault(path string, c config) error {
	body := `# sporemind runtime configuration
# Changes take effect after restart.

# HTTP gateway listen address. Loopback-only by default; set to ":18080"
# or "0.0.0.0:18080" to allow LAN devices (e.g. phone sync) to connect.
# Use gateway_bind_addrs for dual binding (loopback + LAN) without 0.0.0.0.
gateway_addr: "127.0.0.1:18080"

# Extra gateway bind addresses for dual binding. Each gets its own listener
# sharing the same handler. Enables LAN access while keeping loopback for frp.
# gateway_bind_addrs:
#   - "192.168.1.10:18080"

# Desktop transport: wails (raw Wails IPC) or ws (WebSocket via gateway_addr).
# Changes take effect after restart.
desktop_transport: "wails"

# Data storage root directory. Relative paths are resolved against the
# directory that contains the sporemind executable. Actor state is stored
# under <data_dir>/.actors.
# data_dir: ".sporemind"

# Storage backend. "fs" (default) stores actor state as JSON files under
# <data_dir>/.actors. Other backends (goleveldb, redis, etcd, mongo, mysql,
# postgres, oss, webdav) register in pkg/persist's backend registry; a
# misspelled or unregistered name is a startup error, never a silent
# fallback.
# backend: "fs"
#
# Remote service address for network backends (host:port or URL). Ignored
# by embedded backends (fs, goleveldb).
# backend_endpoint: "127.0.0.1:6379"
#
# Database / collection namespace / bucket within the remote service.
# backend_database: "sporemind"
#
# Connect through an SSH tunnel instead of directly: value is an sshmanager
# host id. The tunnel (local forward listener) is opened and owned by
# sshmanager; the backend dials the local port.
# backend_tunnel: ""

# Scoped backends for subsystems that use isolated goleveldb DBs.
# An empty value (default) means "goleveldb" — both scopes default to their
# isolated goleveldb DB and start up with an idempotent migration from the
# legacy fs layout (source files are kept). Set "fs" explicitly to fall back
# to the filesystem layout; remote backends (redis, mongo, etc.) are not
# supported here and a typo is a startup error, never a silent fallback.
# backend_aistats: ""    # AI statistics storage (pkg/actor/aistats)
# backend_logs: ""       # backend log storage (pkg/logging)
#
# Retention window for backend logs, in days. A yaml value <= 0 (including
# unset) falls back to the default of 7 days.
# logs_retention_days: 7
#
# Retention windows for aistats, in days. Raw per-request records roll up into
# daily aggregates once the raw window expires; the daily aggregates live for
# the daily window. A yaml value <= 0 (including unset) falls back to the
# default (raw 90 days, daily 550 days).
# aistats_raw_days: 90
# aistats_daily_days: 550

# Per-app persistence quotas for the plugin document store
# (pluginhost state.*). The backend choice (fs, goleveldb, webdav, ...) does
# not affect these limits.
# appstate_max_docs_per_app caps how many documents one app may hold
# (default 1024; overwrites of existing keys do not count).
# appstate_max_value_bytes caps one state.set value (default 1 MiB).
# appstate_max_append_bytes caps one state.append payload (default 4 MiB).
# appstate_max_docs_per_app: 1024
# appstate_max_value_bytes: 1048576
# appstate_max_append_bytes: 4194304

# gospore namespace. Must match the namespaces declared in the Spore
# manifest; changing this without updating the schema will break callable
# routing.
# namespace: "sporemind"

# Default UI locale. Supported values: zh-CN, en-US.
# locale: "zh-CN"
`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		return fmt.Errorf("config: write default %s: %w", path, err)
	}
	return nil
}

func getExeDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

func resolveDataDir(dir string) string {
	if filepath.IsAbs(dir) {
		return dir
	}
	if strings.HasPrefix(dir, "~/") {
		if u, err := user.Current(); err == nil && u.HomeDir != "" {
			return filepath.Join(u.HomeDir, dir[2:])
		}
	}
	return filepath.Join(exeDir, dir)
}

// GatewayAddr returns the HTTP gateway listen address. While the gateway
// requests an ephemeral port this still reports the ":0" form; once the
// listener has bound, AdoptBoundGatewayAddr replaces it with the actual
// address.
func GatewayAddr() string {
	if addr := os.Getenv(gatewayAddrEnv); addr != "" {
		return addr
	}
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return cfg.GatewayAddr
}

// resolveGatewayDefault maps a missing or stock-default gateway address onto
// this build's flavor default. It is a no-op for regular builds; devrelease
// builds remap the stock default — including the value auto-written into a
// sporemind.yaml shared with a sibling dev binary — onto an ephemeral port
// (":0", resolved to the real address at runtime) so the two flavors never
// listen on, or shut down, the same gateway. Explicit non-default addresses
// (including the LAN ":18080" form) are honored as-is.
func resolveGatewayDefault(addr string) string {
	if addr == "" || addr == DefaultGatewayAddr {
		return devReleaseGatewayAddr
	}
	return addr
}

// CookieBridgeAddr returns the cookiebridge actor's listen address. A bare
// ":port" form is normalized onto loopback so the bridge can never silently
// bind all interfaces. The host must be loopback — the actor re-validates at
// startup and fails loudly otherwise.
func CookieBridgeAddr() string {
	addr := cfg.CookieBridgeAddr
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	if addr == "" {
		return DefaultCookieBridgeAddr
	}
	return addr
}

// CloudAPIURL returns the sporemind cloud backend base URL used by the
// cloud-account actor for token refresh and entitlements fetch. Resolved from
// the SPOREMIND_CLOUD_API_URL env var, falling back to DefaultCloudAPIURL.
func CloudAPIURL() string {
	if u := os.Getenv(cloudAPIURLEnv); u != "" {
		return u
	}
	return DefaultCloudAPIURL
}

// CloudURLConfigured reports whether the operator explicitly pointed the
// binary at a cloud backend (env var). Unset means "default" — for dev
// builds the background sync loop must not dial the public API uninvited,
// because that is the first outbound connection the process makes and the
// source of the Windows firewall popup on fresh installs.
func CloudURLConfigured() bool {
	return os.Getenv(cloudAPIURLEnv) != ""
}

// SporemindWebURL returns the sporemind website base URL (no trailing slash).
// The desktop login overlay opens this site's login page. Resolved from the
// SPOREMIND_SPOREMIND_WEB_URL env var, falling back to DefaultSporemindWebURL.
func SporemindWebURL() string {
	if u := os.Getenv(sporemindWebURLEnv); u != "" {
		return strings.TrimRight(u, "/")
	}
	return DefaultSporemindWebURL
}

// SporemindLoginURL returns the sporemind website login page URL — the page the
// desktop login overlay opens in its captive WebView2 (M3-3).
func SporemindLoginURL() string {
	return SporemindWebURL() + "/login"
}

// DefaultMemoryLimit is the soft Go runtime memory cap used when neither
// the env var nor a value configured at runtime is set. 4 GiB leaves room
// for the common multi-agent + lazy-load + large-file mix without GC
// thrashing. Lower it (e.g. "1GiB" via SPOREMIND_MEMORY_LIMIT) to repro
// OOM crash-file behavior on a smaller machine; raise it if the workload
// legitimately needs more headroom.
const DefaultMemoryLimit int64 = 4 << 30

// MemoryLimit returns the Go runtime soft memory cap in bytes.
//
// Resolution order:
//  1. SPOREMIND_MEMORY_LIMIT env var, parsed with binary suffixes
//     (e.g. "2GiB", "512MiB", "1024"). Bare integers are bytes.
//  2. DefaultMemoryLimit (4 GiB).
//
// Unparseable values fall back to the default with a stderr warning so a
// typo never silently doubles the cap.
func MemoryLimit() int64 {
	raw := os.Getenv(memoryLimitEnv)
	if raw == "" {
		return DefaultMemoryLimit
	}
	v, err := parseMemSize(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: invalid %s=%q (%v); using default %d bytes\n", memoryLimitEnv, raw, err, DefaultMemoryLimit)
		return DefaultMemoryLimit
	}
	return v
}

// parseMemSize accepts an integer optionally suffixed with KiB, MiB, GiB
// (case-insensitive; KB/MB/GB also accepted and treated as binary multiples
// to avoid surprising users on Windows). Returns bytes.
func parseMemSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty value")
	}
	// Walk the suffix from longest to shortest.
	for _, suf := range []string{"gib", "gb", "mib", "mb", "kib", "kb"} {
		if strings.HasSuffix(strings.ToLower(s), suf) {
			num := strings.TrimSpace(s[:len(s)-len(suf)])
			n, err := strconv.ParseInt(num, 10, 64)
			if err != nil || n < 0 {
				return 0, fmt.Errorf("invalid number %q", num)
			}
			switch suf {
			case "gib", "gb":
				return n << 30, nil
			case "mib", "mb":
				return n << 20, nil
			case "kib", "kb":
				return n << 10, nil
			}
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid number %q", s)
	}
	return n, nil
}

// DataDir returns the absolute path to the data storage root.
func DataDir() string { return resolveDataDir(cfg.DataDir) }

// ActorDataDir returns the absolute path to the actor persistence root.
// Actor state is stored under <DataDir>/.actors to keep runtime actor data
// separate from other application data.
func ActorDataDir() string { return filepath.Join(DataDir(), actorDirName) }

// BootThemeCacheFile is the write-through pre-paint theme cache. The
// workspace actor projects the ai-shell theme preference into it on every
// preferences save and at boot; the desktop shell reads it before the actor
// tree starts so window creation never waits for the tree. Derived data,
// not a source of truth — the account preferences card stays authoritative.
const BootThemeCacheFile = "boot-theme.json"

// BootThemeCachePath returns the absolute path of the boot theme cache.
func BootThemeCachePath() string { return filepath.Join(DataDir(), BootThemeCacheFile) }

// Namespace returns the gospore namespace.
func Namespace() string { return cfg.Namespace }

// Locale returns the default UI locale configured for this deployment.
func Locale() string { return cfg.Locale }

// SetDataDirForTest overrides the data directory. Test-only — production
// code must not call this. Always pair with ResetForTest in t.Cleanup.
func SetDataDirForTest(dir string) {
	cfg.DataDir = dir
}

// SetExeDirForTest overrides the executable directory used by Save so tests
// do not write sporemind.yaml into the real binary directory. Test-only —
// production code must not call this. Always restore the original value in
// t.Cleanup.
func SetExeDirForTest(dir string) {
	exeDir = dir
}

// ResetForTest clears test-applied overrides. Test-only.
func ResetForTest() {
	cfg = defaultConfig()
}

// validBackends was the hardcoded backend allowlist; it has been replaced
// by the pkg/persist backend registry (single source of truth, see
// validateBackend below).

// validateBackend returns an error if backend is not a registered persist
// backend. An empty value is valid (defaults to "fs" at the call site).
// The authoritative list lives in pkg/persist's backend registry so config
// validation and persist.New can never drift apart; a registry-external
// type is a startup error, never a silent fallback.
func validateBackend(backend string) error {
	if backend == "" || persist.IsBackendRegistered(persist.BackendType(backend)) {
		return nil
	}
	return fmt.Errorf("invalid backend %q (registered: %s)", backend, strings.Join(persist.SupportedBackends(), ", "))
}

// validateScopedBackend validates a scoped backend key (aistats/logs).
// Unlike the global backend, scoped keys accept only empty/fs/goleveldb:
// these subsystems store data in a single embedded goleveldb DB or on the
// filesystem and do not support remote backends (redis, mongo, etc.). The
// check first reuses the global validateBackend to catch typos against the
// registry, then restricts the result to the scoped allowlist. An
// unsupported value is a startup error, never a silent fallback.
func validateScopedBackend(backend string) error {
	if backend == "" {
		return nil
	}
	if err := validateBackend(backend); err != nil {
		return err
	}
	switch persist.BackendType(backend) {
	case persist.BackendFS, persist.BackendLevelDB:
		return nil
	default:
		return fmt.Errorf("scoped backend %q not supported (only fs/goleveldb)", backend)
	}
}

// ScopedBackend returns the effective backend for a scope ("aistats" or
// "logs"). An empty (unset) scoped key resolves to "goleveldb" — the
// default since the retention-rollup rollout; legacy fs layouts are
// migrated idempotently on first goleveldb open. The global backend is
// intentionally not inherited, because aistats and logs use isolated
// goleveldb DBs and must not silently ride a remote global backend.
// Explicit "fs" is the fallback path. Unknown scopes return "fs".
func ScopedBackend(scope string) string {
	switch scope {
	case "aistats":
		if cfg.BackendAIStats != "" {
			return cfg.BackendAIStats
		}
		return string(persist.BackendLevelDB)
	case "logs":
		if cfg.BackendLogs != "" {
			return cfg.BackendLogs
		}
		return string(persist.BackendLevelDB)
	default:
		return "fs"
	}
}

// ScopedBackendRaw returns the raw yaml value of a scoped backend key
// ("aistats" or "logs") without default resolution — "" means "follow the
// default". Unknown scopes return "".
func ScopedBackendRaw(scope string) string {
	switch scope {
	case "aistats":
		return cfg.BackendAIStats
	case "logs":
		return cfg.BackendLogs
	default:
		return ""
	}
}

// SetScopedBackend sets the raw scoped backend key ("aistats" or "logs").
// An empty value follows the default; only "" / fs / goleveldb are valid —
// anything else is an error, never a silent fallback. Persisted by Save().
func SetScopedBackend(scope string, backend string) error {
	if err := validateScopedBackend(backend); err != nil {
		return err
	}
	switch scope {
	case "aistats":
		cfg.BackendAIStats = backend
	case "logs":
		cfg.BackendLogs = backend
	default:
		return fmt.Errorf("unknown storage scope %q", scope)
	}
	return nil
}

// PersistConfig returns a persist.PersistConfig for the given actor-type
// prefix. Backend connection parameters come from the non-sensitive
// sporemind.yaml fields; CredentialRef is intentionally not sourced from
// yaml — credentials belong to the dbmanager actor's connection profiles
// (workflow decision point 1) and are resolved at dial time.
func PersistConfig(prefix string) persist.PersistConfig {
	return persist.PersistConfig{
		Backend:   persist.BackendType(cfg.Backend),
		DataDir:   ActorDataDir(),
		Prefix:    prefix,
		Endpoint:  cfg.BackendEndpoint,
		Database:  cfg.BackendDatabase,
		TunnelRef: cfg.BackendTunnel,
	}
}

// Quota defaults for the plugin document store (pluginhost state.*).
// A yaml value <= 0 (including unset) falls back to the default: quotas are
// 约束面 and must be explicit, but a blank config must still start.
const (
	DefaultAppStateMaxDocsPerApp  = 1024
	DefaultAppStateMaxValueBytes  = 1 << 20
	DefaultAppStateMaxAppendBytes = 4 << 20
)

// DefaultLogsRetentionDays is the fallback retention window for backend logs,
// in days. A yaml value <= 0 (including unset) falls back to this default:
// the retention is 约束面 and must be explicit, but a blank config must
// still start.
const DefaultLogsRetentionDays = 7

// DefaultAistatsRawDays is the fallback retention window for raw (per-request)
// aistats records, in days. A yaml value <= 0 (including unset) falls back to
// this default: the retention is 约束面 and must be explicit, but a blank
// config must still start.
const DefaultAistatsRawDays = 90

// DefaultAistatsDailyDays is the fallback retention window for daily-roll-up
// aistats aggregates, in days. A yaml value <= 0 (including unset) falls back
// to this default.
const DefaultAistatsDailyDays = 550

// AppStateMaxDocsPerApp returns the per-app document quota for the plugin
// state store. Overwrites of existing keys do not count against it.
func AppStateMaxDocsPerApp() int {
	if cfg.AppStateMaxDocs <= 0 {
		return DefaultAppStateMaxDocsPerApp
	}
	return cfg.AppStateMaxDocs
}

// AppStateMaxValueBytes returns the per-document byte quota for state.set.
func AppStateMaxValueBytes() int64 {
	if cfg.AppStateMaxValue <= 0 {
		return DefaultAppStateMaxValueBytes
	}
	return cfg.AppStateMaxValue
}

// AppStateMaxAppendBytes returns the per-call byte quota for state.append.
func AppStateMaxAppendBytes() int64 {
	if cfg.AppStateMaxAppend <= 0 {
		return DefaultAppStateMaxAppendBytes
	}
	return cfg.AppStateMaxAppend
}

// LogsRetentionDays returns the retention window for backend logs, in days.
// A yaml value <= 0 (including unset) falls back to DefaultLogsRetentionDays.
func LogsRetentionDays() int {
	if cfg.LogsRetentionDays <= 0 {
		return DefaultLogsRetentionDays
	}
	return cfg.LogsRetentionDays
}

// AistatsRawDays returns the retention window for raw aistats records, in
// days. A yaml value <= 0 (including unset) falls back to
// DefaultAistatsRawDays.
func AistatsRawDays() int {
	if cfg.AistatsRawDays <= 0 {
		return DefaultAistatsRawDays
	}
	return cfg.AistatsRawDays
}

// AistatsDailyDays returns the retention window for daily-roll-up aistats
// aggregates, in days. A yaml value <= 0 (including unset) falls back to
// DefaultAistatsDailyDays.
func AistatsDailyDays() int {
	if cfg.AistatsDailyDays <= 0 {
		return DefaultAistatsDailyDays
	}
	return cfg.AistatsDailyDays
}

// Retention setters for the settings UI. Values <= 0 are stored as-is and
// the accessors fall back to the defaults — a blank config must still
// start (quota-key precedent). Persisted by Save().

// SetLogsRetentionDays updates the in-memory backend-log retention window.
func SetLogsRetentionDays(days int) { cfg.LogsRetentionDays = days }

// SetAistatsRawDays updates the in-memory raw-record retention window.
func SetAistatsRawDays(days int) { cfg.AistatsRawDays = days }

// SetAistatsDailyDays updates the in-memory daily-rollup retention window.
func SetAistatsDailyDays(days int) { cfg.AistatsDailyDays = days }

// ExeDir returns the directory that contains the sporemind executable.
func ExeDir() string { return exeDir }

// DesktopTransport returns the configured desktop transport mode.
func DesktopTransport() string { return cfg.DesktopTransport }

// SetGatewayAddr updates the in-memory gateway address. The change is not
// persisted until Save is called.
func SetGatewayAddr(addr string) {
	addr = resolveGatewayDefault(addr)
	cfgMu.Lock()
	cfg.GatewayAddr = addr
	cfgMu.Unlock()
}

// GatewayAddrIsEphemeral reports whether the effective gateway address
// requests an OS-assigned port (":0") — the actual address is only known
// after the listener has bound and AdoptBoundGatewayAddr has run.
func GatewayAddrIsEphemeral() bool {
	_, port, err := net.SplitHostPort(GatewayAddr())
	return err == nil && port == "0"
}

// AdoptBoundGatewayAddr records the address the ephemeral gateway actually
// bound as the effective in-process gateway address (frontend bindings, frp,
// and the LAN URL builders read it afterwards), and announces it for
// same-flavor instance discovery on flavors that record one.
func AdoptBoundGatewayAddr(addr string) {
	if addr == "" {
		return
	}
	SetGatewayAddr(addr)
	announceGatewayAddr(addr)
}

// TakeoverGatewayAddr returns the address a previous same-flavor instance may
// still be listening on: the recorded ephemeral port when this build requests
// one, otherwise the configured address. Best-effort — stale or missing
// records simply fail the caller's probe.
func TakeoverGatewayAddr() string {
	addr := GatewayAddr()
	if prev, ok := previousGatewayAddr(addr); ok {
		return prev
	}
	return addr
}

// GatewayBindAddrs returns the additional gateway bind addresses beyond the
// primary GatewayAddr. Each gets its own listener sharing the same handler,
// enabling dual binding (loopback + LAN IP) without exposing 0.0.0.0.
func GatewayBindAddrs() []string {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return cfg.GatewayBindAddrs
}

// SetGatewayBindAddrs updates the in-memory extra bind addresses. Pass nil
// or an empty slice to clear. The change is not persisted until Save is called.
func SetGatewayBindAddrs(addrs []string) {
	cfgMu.Lock()
	cfg.GatewayBindAddrs = addrs
	cfgMu.Unlock()
}

// SetDesktopTransport updates the in-memory desktop transport mode. Valid
// values are "wails" and "ws". The change is not persisted until Save is
// called.
func SetDesktopTransport(transport string) {
	if transport != "ws" {
		transport = "wails"
	}
	cfg.DesktopTransport = transport
}

// Save writes the current in-memory configuration back to sporemind.yaml.
func Save() error {
	path := filepath.Join(exeDir, fileName)
	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}
