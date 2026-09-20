package persist

import (
	"strings"
	"testing"
)

// TestBackendRegistry_FSRegistered verifies the built-in fs backend is
// registered at package init and visible through the registry introspection
// helpers that config validation relies on.
func TestBackendRegistry_FSRegistered(t *testing.T) {
	if !IsBackendRegistered(BackendFS) {
		t.Fatal("fs backend must be registered at package init")
	}
	supported := SupportedBackends()
	found := false
	for _, name := range supported {
		if name == string(BackendFS) {
			found = true
		}
	}
	if !found {
		t.Fatalf("SupportedBackends() = %v, must contain fs", supported)
	}
	// Unknown backend names must NOT be registered: a miss is the
	// constraint面 signal that makes typos visible. All workflow backends
	// (B2 goleveldb, B4 redis/etcd, B5 mongo/mysql/postgres, B6 oss, B8
	// webdav) have graduated, so the list is now pure typos and reserved
	// names that must never silently resolve.
	for _, unimplemented := range []string{"mongod", "postgress", "rediss", "sqlite", "s3", "dav"} {
		if IsBackendRegistered(BackendType(unimplemented)) {
			t.Errorf("backend %q must not be registered yet", unimplemented)
		}
	}
}

// TestBackendRegistry_ErrorListsRegistered verifies that constructing an
// unregistered backend fails with an error that names the offending value
// and lists the registered backends for diagnosis.
func TestBackendRegistry_ErrorListsRegistered(t *testing.T) {
	_, err := New(PersistConfig{Backend: "monga", DataDir: t.TempDir(), Prefix: "test"})
	if err == nil {
		t.Fatal("expected error for unregistered backend")
	}
	if !strings.Contains(err.Error(), "monga") {
		t.Errorf("error %q should name the offending backend", err)
	}
	if !strings.Contains(err.Error(), string(BackendFS)) || !strings.Contains(err.Error(), string(BackendOSS)) {
		t.Errorf("error %q should list registered backends (fs, oss, ...)", err)
	}
}

// TestBackendRegistry_FactoryUsed verifies that New dispatches to the
// factory registered under the requested backend type, passing the full
// PersistConfig through. A test-only factory backed by a temp dir stands in
// for a future network backend.
func TestBackendRegistry_FactoryUsed(t *testing.T) {
	const testBackend BackendType = "testmem"
	var gotCfg PersistConfig
	RegisterBackend(testBackend, func(cfg PersistConfig) (Persist, error) {
		gotCfg = cfg
		return NewFSPersist(cfg.DataDir), nil
	})

	cfg := PersistConfig{
		Backend:       testBackend,
		DataDir:       t.TempDir(),
		Prefix:        "test",
		Endpoint:      "127.0.0.1:6399",
		Database:      "sporemind",
		CredentialRef: "profile-1",
		TunnelRef:     "host-abc",
	}
	p, err := New(cfg)
	if err != nil {
		t.Fatalf("New(%q) error = %v, want nil", testBackend, err)
	}
	if p == nil {
		t.Fatalf("New(%q) returned nil Persist", testBackend)
	}
	if gotCfg.Endpoint != cfg.Endpoint || gotCfg.Database != cfg.Database ||
		gotCfg.CredentialRef != cfg.CredentialRef || gotCfg.TunnelRef != cfg.TunnelRef {
		t.Errorf("factory received cfg %+v, want connection fields %+v", gotCfg, cfg)
	}

	// The registered name must also appear in diagnostics.
	if !IsBackendRegistered(testBackend) {
		t.Fatalf("backend %q should report as registered", testBackend)
	}
}

// TestBackendRegistry_DuplicatePanics verifies that a second registration
// under an existing name fails loudly instead of silently shadowing the
// first factory.
func TestBackendRegistry_DuplicatePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("duplicate RegisterBackend should panic")
		}
	}()
	RegisterBackend(BackendFS, func(cfg PersistConfig) (Persist, error) {
		return nil, nil
	})
}

// TestBackendRegistry_InvalidRegistrationPanics verifies that malformed
// registrations (empty name, nil factory) fail loudly at registration time.
func TestBackendRegistry_InvalidRegistrationPanics(t *testing.T) {
	assertPanics := func(name string, f func()) {
		t.Helper()
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("%s should panic", name)
			}
		}()
		f()
	}
	assertPanics("empty backend type", func() { RegisterBackend("", func(cfg PersistConfig) (Persist, error) { return nil, nil }) })
	assertPanics("nil factory", func() { RegisterBackend("nilfactory-test", nil) })
}
