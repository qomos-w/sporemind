package persist

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Shared test plumbing for the B4 network kv backends (redis/etcd). The
// contract suites and e2e tests run against real containers on localhost;
// they skip (never fail) when no container answers and under -short, which
// is the CI opt-out marker: CI runs `go test -short`, local runs with
// containers get full coverage.

// netTestSeq makes every test namespace unique even on a dirty shared
// container, so a leftover key from a crashed run can never pollute an
// assertion. The run salt distinguishes re-runs against the same container
// (the sequence alone is deterministic per process).
var (
	netTestSeq atomic.Int64
	netRunSalt = func() string {
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		return hex.EncodeToString(b)
	}()
)

func freshNamespace(t *testing.T) string {
	t.Helper()
	san := strings.NewReplacer("/", "-", `\`, "-", " ", "_", ":", "-").Replace(t.Name())
	return fmt.Sprintf("r%s-t%06d-%s", netRunSalt, netTestSeq.Add(1), san)
}

// tcpProbe reports whether a TCP endpoint accepts a connection within 500ms.
func tcpProbe(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// redisTestAddr resolves the redis container address for tests: env override,
// else the local default. Skips (or fails under -short is reversed: skips
// always) when unreachable.
func redisTestAddr(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping redis container test in -short mode")
	}
	addr := os.Getenv("SPOREMIND_TEST_REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	if !tcpProbe(addr) {
		t.Skipf("redis container not reachable at %s (start one: docker run -d -p 6379:6379 redis:7-alpine, or set SPOREMIND_TEST_REDIS_ADDR)", addr)
	}
	return addr
}

// etcdTestEndpoint resolves the etcd container endpoint for tests.
func etcdTestEndpoint(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping etcd container test in -short mode")
	}
	ep := os.Getenv("SPOREMIND_TEST_ETCD_ENDPOINT")
	if ep == "" {
		ep = "127.0.0.1:2379"
	}
	if !tcpProbe(ep) {
		t.Skipf("etcd container not reachable at %s (start one: docker run -d -p 2379:2379 quay.io/coreos/etcd:v3.5.15, or set SPOREMIND_TEST_ETCD_ENDPOINT)", ep)
	}
	return ep
}

// startTCPForwarder stands up a 127.0.0.1 listener that pipes every
// connection to target — the same shape as a B3 ssh -L tunnel listener
// (tunnel.go binds 127.0.0.1:0 and forwards over direct-tcpip). Backends
// under test dial the forwarder with TunnelRef set, exercising the
// tunnel code path (plaintext dial, decision point 4) without needing a
// real SSH host; B7 replaces this shim with the sshmanager wiring.
func startTCPForwarder(t *testing.T, target string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("forwarder listen: %v", err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				rc, err := net.Dial("tcp", target)
				if err != nil {
					return
				}
				defer rc.Close()
				go func() {
					_, _ = io.Copy(rc, c)
					if cw, ok := rc.(interface{ CloseWrite() error }); ok {
						_ = cw.CloseWrite()
					}
				}()
				_, _ = io.Copy(c, rc)
			}(c)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String()
}

// installTestCredentialResolver swaps the process credential resolver for
// the duration of one test.
func installTestCredentialResolver(t *testing.T, r CredentialResolver) {
	t.Helper()
	credMu.Lock()
	old := credResolver
	credResolver = r
	credMu.Unlock()
	t.Cleanup(func() {
		credMu.Lock()
		credResolver = old
		credMu.Unlock()
	})
}
