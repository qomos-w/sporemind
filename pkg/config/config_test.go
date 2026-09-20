package config

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/persist"
)

func TestGatewayAddrEnvironmentOverride(t *testing.T) {
	t.Setenv(gatewayAddrEnv, ":18090")

	if got := GatewayAddr(); got != ":18090" {
		t.Fatalf("GatewayAddr() = %q, want %q", got, ":18090")
	}
}

func TestGatewayAddrUsesConfigWithoutEnvironmentOverride(t *testing.T) {
	t.Setenv(gatewayAddrEnv, "")
	original := cfg.GatewayAddr
	cfg.GatewayAddr = ":18081"
	t.Cleanup(func() { cfg.GatewayAddr = original })

	if got := GatewayAddr(); got != ":18081" {
		t.Fatalf("GatewayAddr() = %q, want %q", got, ":18081")
	}
}

func TestMemoryLimitDefault(t *testing.T) {
	t.Setenv(memoryLimitEnv, "")
	if got := MemoryLimit(); got != DefaultMemoryLimit {
		t.Fatalf("MemoryLimit() = %d, want default %d", got, DefaultMemoryLimit)
	}
}

func TestMemoryLimitEnvOverride(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"2GiB", 2 << 30},
		{"2gib", 2 << 30},
		{"2GB", 2 << 30},
		{"512MiB", 512 << 20},
		{"1024", 1024},
		{"1KiB", 1 << 10},
		{"0", 0},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			t.Setenv(memoryLimitEnv, c.in)
			if got := MemoryLimit(); got != c.want {
				t.Fatalf("MemoryLimit(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

func TestMemoryLimitInvalidFallsBackToDefault(t *testing.T) {
	t.Setenv(memoryLimitEnv, "not-a-size")
	if got := MemoryLimit(); got != DefaultMemoryLimit {
		t.Fatalf("MemoryLimit(invalid) = %d, want default %d", got, DefaultMemoryLimit)
	}
}

func TestValidateBackend(t *testing.T) {
	cases := []struct {
		backend string
		wantErr bool
	}{
		{"fs", false},
		{"", false}, // empty defaults to fs
		{"mongo", false},
		{"mongodb", true},    // wrong name: registered backend is "mongo"
		{"postgres", false},  // B5
		{"postgresql", true}, // wrong name: registered backend is "postgres"
		{"oss", false},       // S3-compatible backend (B6)
		{"webdav", false},    // B8
		{"mysql", false},     // B5
		{"redis", false},     // B4
		{"etcd", false},      // B4
		{"goleveldb", false}, // B2
		{"monga", true},      // typo
		{"filesystem", true}, // wrong name
		{"FS", true},         // case-sensitive
		{"fs ", true},        // trailing space
	}
	for _, c := range cases {
		t.Run(c.backend, func(t *testing.T) {
			err := validateBackend(c.backend)
			if (err != nil) != c.wantErr {
				t.Errorf("validateBackend(%q) error = %v, wantErr %v", c.backend, err, c.wantErr)
			}
		})
	}
}

// TestBackendValidationAgainstPersistRegistry verifies config validation
// resolves through the pkg/persist backend registry (single source of
// truth): fs is accepted, empty defaults are accepted, and unregistered
// names — typos and not-yet-implemented backends alike — are rejected.
func TestBackendValidationAgainstPersistRegistry(t *testing.T) {
	if err := validateBackend("fs"); err != nil {
		t.Errorf("validateBackend(fs) = %v, want nil", err)
	}
	if err := validateBackend(""); err != nil {
		t.Errorf("validateBackend(empty) = %v, want nil", err)
	}
	for _, invalid := range []string{"mongod", "postgress", "monga", "FS", "fs "} {
		if err := validateBackend(invalid); err == nil {
			t.Errorf("validateBackend(%q) = nil, want error", invalid)
		}
	}
}

// TestPersistConfigCarriesBackendConnectionFields verifies the non-sensitive
// sporemind.yaml backend fields flow into persist.PersistConfig, and that
// CredentialRef stays empty (credentials belong to dbmanager profiles, not
// yaml — workflow decision point 1).
func TestPersistConfigCarriesBackendConnectionFields(t *testing.T) {
	orig := cfg
	t.Cleanup(func() { cfg = orig })

	cfg.Backend = "redis"
	cfg.BackendEndpoint = "127.0.0.1:6379"
	cfg.BackendDatabase = "sporemind"
	cfg.BackendTunnel = "host-1"

	pc := PersistConfig("agent")
	if pc.Backend != persist.BackendType("redis") {
		t.Errorf("Backend = %q, want redis", pc.Backend)
	}
	if pc.Endpoint != "127.0.0.1:6379" {
		t.Errorf("Endpoint = %q, want 127.0.0.1:6379", pc.Endpoint)
	}
	if pc.Database != "sporemind" {
		t.Errorf("Database = %q, want sporemind", pc.Database)
	}
	if pc.TunnelRef != "host-1" {
		t.Errorf("TunnelRef = %q, want host-1", pc.TunnelRef)
	}
	if pc.CredentialRef != "" {
		t.Errorf("CredentialRef = %q, want empty (dbmanager-owned, not yaml)", pc.CredentialRef)
	}
}

// TestAppStateQuotaGetters verifies the plugin state quotas: defaults
// apply when yaml is unset or non-positive, explicit values win otherwise.
func TestAppStateQuotaGetters(t *testing.T) {
	orig := cfg
	t.Cleanup(func() { cfg = orig })

	cfg.AppStateMaxDocs = 0
	cfg.AppStateMaxValue = -1
	cfg.AppStateMaxAppend = 0
	if got := AppStateMaxDocsPerApp(); got != DefaultAppStateMaxDocsPerApp {
		t.Errorf("docs default = %d, want %d", got, DefaultAppStateMaxDocsPerApp)
	}
	if got := AppStateMaxValueBytes(); got != DefaultAppStateMaxValueBytes {
		t.Errorf("value default = %d, want %d", got, DefaultAppStateMaxValueBytes)
	}
	if got := AppStateMaxAppendBytes(); got != DefaultAppStateMaxAppendBytes {
		t.Errorf("append default = %d, want %d", got, DefaultAppStateMaxAppendBytes)
	}

	cfg.AppStateMaxDocs = 7
	cfg.AppStateMaxValue = 99
	cfg.AppStateMaxAppend = 123
	if got := AppStateMaxDocsPerApp(); got != 7 {
		t.Errorf("docs = %d, want 7", got)
	}
	if got := AppStateMaxValueBytes(); got != 99 {
		t.Errorf("value = %d, want 99", got)
	}
	if got := AppStateMaxAppendBytes(); got != 123 {
		t.Errorf("append = %d, want 123", got)
	}
}

// TestLogsRetentionDays verifies the backend-log retention window: the
// default applies when yaml is unset or non-positive, explicit values win
// otherwise.
func TestLogsRetentionDays(t *testing.T) {
	orig := cfg
	t.Cleanup(func() { cfg = orig })

	cfg.LogsRetentionDays = 0
	if got := LogsRetentionDays(); got != DefaultLogsRetentionDays {
		t.Errorf("unset = %d, want %d", got, DefaultLogsRetentionDays)
	}

	cfg.LogsRetentionDays = -3
	if got := LogsRetentionDays(); got != DefaultLogsRetentionDays {
		t.Errorf("negative = %d, want %d", got, DefaultLogsRetentionDays)
	}

	cfg.LogsRetentionDays = 30
	if got := LogsRetentionDays(); got != 30 {
		t.Errorf("explicit = %d, want 30", got)
	}
}

// TestAistatsRetentionDays verifies the aistats raw/daily retention windows:
// the defaults apply when yaml is unset or non-positive, explicit values win
// otherwise.
func TestAistatsRetentionDays(t *testing.T) {
	orig := cfg
	t.Cleanup(func() { cfg = orig })

	cfg.AistatsRawDays = 0
	if got := AistatsRawDays(); got != DefaultAistatsRawDays {
		t.Errorf("raw unset = %d, want %d", got, DefaultAistatsRawDays)
	}

	cfg.AistatsRawDays = -3
	if got := AistatsRawDays(); got != DefaultAistatsRawDays {
		t.Errorf("raw negative = %d, want %d", got, DefaultAistatsRawDays)
	}

	cfg.AistatsRawDays = 30
	if got := AistatsRawDays(); got != 30 {
		t.Errorf("raw explicit = %d, want 30", got)
	}

	cfg.AistatsDailyDays = 0
	if got := AistatsDailyDays(); got != DefaultAistatsDailyDays {
		t.Errorf("daily unset = %d, want %d", got, DefaultAistatsDailyDays)
	}

	cfg.AistatsDailyDays = -3
	if got := AistatsDailyDays(); got != DefaultAistatsDailyDays {
		t.Errorf("daily negative = %d, want %d", got, DefaultAistatsDailyDays)
	}

	cfg.AistatsDailyDays = 365
	if got := AistatsDailyDays(); got != 365 {
		t.Errorf("daily explicit = %d, want 365", got)
	}
}

// TestValidateScopedBackend verifies the scoped backend validator accepts only
// empty/fs/goleveldb and rejects everything else (including registered remote
// backends like redis/mongo that are valid globally but not for scoped keys).
func TestValidateScopedBackend(t *testing.T) {
	cases := []struct {
		backend string
		wantErr bool
	}{
		{"", false},          // empty = follow default (goleveldb)
		{"fs", false},        // filesystem
		{"goleveldb", false}, // embedded leveldb
		{"redis", true},      // registered but not scoped-allowed
		{"mongo", true},      // registered but not scoped-allowed
		{"postgres", true},   // registered but not scoped-allowed
		{"mysql", true},      // registered but not scoped-allowed
		{"etcd", true},       // registered but not scoped-allowed
		{"webdav", true},     // registered but not scoped-allowed
		{"oss", true},        // registered but not scoped-allowed
		{"filesystem", true}, // typo / wrong name
		{"leveldb", true},    // wrong name (registered is goleveldb)
		{"golevldb", true},   // typo
		{"FS", true},         // case-sensitive
	}
	for _, c := range cases {
		t.Run(c.backend, func(t *testing.T) {
			err := validateScopedBackend(c.backend)
			if (err != nil) != c.wantErr {
				t.Errorf("validateScopedBackend(%q) error = %v, wantErr %v", c.backend, err, c.wantErr)
			}
		})
	}
}

// TestScopedBackendResolution verifies ScopedBackend returns the effective
// backend for each scope, defaulting to "goleveldb" when unset and not
// inheriting the global backend. Explicit "fs" is the fallback path.
func TestScopedBackendResolution(t *testing.T) {
	orig := cfg
	t.Cleanup(func() { cfg = orig })

	// Defaults: empty scoped keys resolve to goleveldb even when global is
	// non-fs (the global backend must not leak into scoped resolution).
	cfg.Backend = "redis"
	cfg.BackendAIStats = ""
	cfg.BackendLogs = ""
	if got := ScopedBackend("aistats"); got != "goleveldb" {
		t.Errorf("ScopedBackend(aistats) default = %q, want goleveldb", got)
	}
	if got := ScopedBackend("logs"); got != "goleveldb" {
		t.Errorf("ScopedBackend(logs) default = %q, want goleveldb", got)
	}
	if got := ScopedBackend("unknown"); got != "fs" {
		t.Errorf("ScopedBackend(unknown) = %q, want fs", got)
	}

	// Explicit goleveldb.
	cfg.BackendAIStats = "goleveldb"
	cfg.BackendLogs = "goleveldb"
	if got := ScopedBackend("aistats"); got != "goleveldb" {
		t.Errorf("ScopedBackend(aistats) = %q, want goleveldb", got)
	}
	if got := ScopedBackend("logs"); got != "goleveldb" {
		t.Errorf("ScopedBackend(logs) = %q, want goleveldb", got)
	}

	// Explicit fs fallback.
	cfg.BackendAIStats = "fs"
	if got := ScopedBackend("aistats"); got != "fs" {
		t.Errorf("ScopedBackend(aistats) = %q, want fs", got)
	}

	// Global backend must not leak into scoped resolution.
	cfg.Backend = "mongo"
	cfg.BackendAIStats = ""
	cfg.BackendLogs = ""
	if got := ScopedBackend("aistats"); got != "goleveldb" {
		t.Errorf("ScopedBackend(aistats) with global=mongo = %q, want goleveldb (no inheritance)", got)
	}
	if got := ScopedBackend("logs"); got != "goleveldb" {
		t.Errorf("ScopedBackend(logs) with global=mongo = %q, want goleveldb (no inheritance)", got)
	}
}

// TestScopedBackendSetters verifies the settings-UI write path: valid values
// land in the raw keys, invalid values error, and unknown scopes error.
func TestScopedBackendSetters(t *testing.T) {
	orig := cfg
	t.Cleanup(func() { cfg = orig })

	if err := SetScopedBackend("aistats", ""); err != nil {
		t.Errorf("SetScopedBackend(aistats, \"\") = %v, want nil", err)
	}
	if err := SetScopedBackend("logs", "fs"); err != nil {
		t.Errorf("SetScopedBackend(logs, fs) = %v, want nil", err)
	}
	if got := ScopedBackendRaw("logs"); got != "fs" {
		t.Errorf("ScopedBackendRaw(logs) = %q, want fs", got)
	}
	if got := ScopedBackend("logs"); got != "fs" {
		t.Errorf("ScopedBackend(logs) after set = %q, want fs", got)
	}
	if err := SetScopedBackend("aistats", "goleveldb"); err != nil {
		t.Errorf("SetScopedBackend(aistats, goleveldb) = %v, want nil", err)
	}
	if err := SetScopedBackend("aistats", "redis"); err == nil {
		t.Error("SetScopedBackend(aistats, redis) = nil, want error")
	}
	if err := SetScopedBackend("bogus", "fs"); err == nil {
		t.Error("SetScopedBackend(bogus, fs) = nil, want error")
	}
}
