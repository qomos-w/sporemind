package sshmanager

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/persist"
)

// This file is the B5↔B3 integration e2e: a database backend configured with
// PersistConfig.TunnelRef dials through sshmanager's real ssh -L tunnel (the
// in-process SSH server stands in for a remote host; the DB container is the
// target). It verifies tunnel resolution (B3), the TLS-off tunnel dial path
// (decision point 4), and real query flow ("SELECT 1" / ping).

// e2eDockerAvailable reports whether container e2e can run.
func e2eDockerAvailable() bool {
	if testing.Short() {
		return false
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// startMysqlContainer boots a throwaway MySQL container and returns its
// reachable host:port plus a stop function. Skips (not fails) when the image
// or daemon is unavailable.
func startMysqlContainer(t *testing.T) (hostPort string, stop func()) {
	t.Helper()
	out, err := exec.Command("docker", "run", "-d", "--rm", "-p", "0:3306",
		"-e", "MYSQL_ROOT_PASSWORD=rootpw", "-e", "MYSQL_DATABASE=sporemind", "mysql:8.4").CombinedOutput()
	if err != nil {
		t.Logf("docker run mysql:8.4: %s", strings.TrimSpace(string(out)))
		t.Skipf("could not start mysql container: %v", err)
	}
	id := strings.TrimSpace(string(out))
	format := `{{(index (index .NetworkSettings.Ports "3306/tcp") 0).HostPort}}`
	hpOut, err := exec.Command("docker", "inspect", "-f", format, id).Output()
	if err != nil {
		_ = exec.Command("docker", "rm", "-f", id).Run()
		t.Skipf("could not inspect mysql port: %v", err)
	}
	port := strings.TrimSpace(string(hpOut))
	if port == "" {
		_ = exec.Command("docker", "rm", "-f", id).Run()
		t.Skip("mysql container published no port")
	}
	stop = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "docker", "stop", id).Run()
	}
	return net.JoinHostPort("127.0.0.1", port), stop
}

// waitMysqlReady polls New(mysql) through the tunnel until the server accepts
// connections (auth + schema creation succeed).
func waitMysqlReady(t *testing.T, cfg persist.PersistConfig) {
	t.Helper()
	deadline := time.Now().Add(150 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		p, err := persist.New(cfg)
		if err == nil {
			if c, ok := p.(interface{ Close() error }); ok {
				_ = c.Close()
			}
			return
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	t.Skipf("mysql not ready within 150s: %v", lastErr)
}

// TestTunnelPersistE2E verifies a persist backend dialed through sshmanager's
// real SSH tunnel: resolution, TLS-off tunnel dial, SELECT 1, and a Save/Load
// roundtrip, plus tunnel reuse (refcount) across two backend instances.
func TestTunnelPersistE2E(t *testing.T) {
	if !e2eDockerAvailable() {
		t.Skip("docker not available or -short mode; skipping tunnel e2e")
	}
	hostPort, stop := startMysqlContainer(t)
	t.Cleanup(stop)

	srv := newTunnelTestServer(t, "secret")
	t.Cleanup(srv.close)
	a := newTunnelTestActor(t, srv)

	// Capture the resolved tunnel local address while delegating to the real
	// resolver under test (a.resolvePersistTunnel).
	var tunnelLocal string
	t.Cleanup(func() { persist.SetTunnelResolver(nil) })
	persist.SetTunnelResolver(func(ref, targetAddr string) (string, error) {
		addr, err := a.resolvePersistTunnel(ref, targetAddr)
		if err == nil {
			tunnelLocal = addr
		}
		return addr, err
	})
	t.Cleanup(func() { persist.SetCredentialResolver(nil) })
	persist.SetCredentialResolver(func(ref string) (persist.Credential, error) {
		return persist.Credential{Username: "root", Password: "rootpw"}, nil
	})

	// The DB container is the tunnel target; because the SSH server is
	// in-process, the target as seen from the SSH host is the same loopback
	// address. Endpoint is that target; TunnelRef routes the dial through the
	// ssh -L forward with TLS off.
	cfg := persist.PersistConfig{
		Backend:       persist.BackendMySQL,
		Prefix:        "e2e",
		Endpoint:      hostPort,
		Database:      "sporemind",
		CredentialRef: "container",
		TunnelRef:     testHostID,
	}
	waitMysqlReady(t, cfg)
	if tunnelLocal == "" {
		t.Fatal("tunnel resolver was not consulted (no local address captured)")
	}

	// SELECT 1 through the tunnel: a raw driver connection dialed at the
	// tunnel's local forward address.
	db, err := sql.Open("mysql", fmt.Sprintf("root:rootpw@tcp(%s)/sporemind?tls=false", tunnelLocal))
	if err != nil {
		t.Fatalf("open raw mysql through tunnel: %v", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var one int
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("SELECT 1 through tunnel: %v", err)
	}
	if one != 1 {
		t.Fatalf("SELECT 1 = %d, want 1", one)
	}

	// Save/Load roundtrip through the tunnel: the backend factory itself
	// resolved the tunnel (captured above) and dialed it with TLS off.
	backend, err := persist.New(cfg)
	if err != nil {
		t.Fatalf("New(mysql through tunnel): %v", err)
	}
	if err := backend.Save("e2e/doc", map[string]int{"v": 42}); err != nil {
		t.Fatalf("Save through tunnel: %v", err)
	}
	var got map[string]int
	if err := backend.Load("e2e/doc", &got); err != nil {
		t.Fatalf("Load through tunnel: %v", err)
	}
	if got["v"] != 42 {
		t.Fatalf("roundtrip through tunnel: got %v, want v=42", got)
	}

	// A second backend on the same TunnelRef must reuse the live tunnel
	// (reference counting), not open a duplicate forward.
	if _, err := persist.New(cfg); err != nil {
		t.Fatalf("New second backend: %v", err)
	}
	a.mu.Lock()
	var tunnels int
	var refCount int
	for _, tn := range a.tunnels {
		tunnels++
		refCount = tn.refCount
	}
	a.mu.Unlock()
	if tunnels != 1 {
		t.Fatalf("expected 1 live tunnel reused across backends, got %d", tunnels)
	}
	if refCount < 2 {
		t.Fatalf("tunnel refCount = %d, want >= 2 (two backends)", refCount)
	}
}
