package frpinstance

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	frpclient "github.com/fatedier/frp/client"
	frpconfig "github.com/fatedier/frp/pkg/config/v1"
)

// probeTimeout is the maximum time to wait for a probe connection.
const probeTimeout = 2 * time.Second

// dialTimeout bounds the raw TCP reachability check in DetectServerCompatibility.
// A black-holed server address (packets silently dropped) would otherwise stall
// the underlying frpc dial until the OS default TCP connect timeout (~2min),
// hanging frpmanager.detect far past any acceptable bound. The audit ceiling is
// 3s (OwnerLane阻塞审计2026-08-27 P2); 2s leaves margin under it.
const dialTimeout = 2 * time.Second

// isVersionMismatchError checks whether an frpc error suggests the server
// speaks an incompatible (legacy) protocol.
func isVersionMismatchError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "session shutdown") ||
		strings.Contains(msg, "protocol") ||
		strings.Contains(msg, "version mismatch")
}

// DetectServerCompatibility probes the remote frps server to determine
// whether legacy mode (TCPMux=false) is required.
// It tries a minimal connection with TCPMux=true first; if that fails with a
// version-mismatch-like error, it retries with TCPMux=false.
// Exported so frpmanager can use it for the pre-flight detect callable.
func DetectServerCompatibility(serverAddr, token string, tlsEnabled bool) (legacyNeeded bool, err error) {
	host, port, err := splitHostPort(serverAddr)
	if err != nil {
		return false, fmt.Errorf("invalid serverAddr: %w", err)
	}

	// Pre-flight TCP reachability check with an explicit dial bound. The
	// frpc probes below have no dependable dial timeout of their own, so
	// without this gate an unreachable server address (black-holed or
	// unroutable) would hang the caller until the OS default TCP connect
	// timeout instead of failing fast.
	if err := probeTCPReachable(host, port); err != nil {
		return false, fmt.Errorf("server %q unreachable: %w", serverAddr, err)
	}

	// Quick probe with TCPMux=true (modern default).
	modernErr := probeWithTCPMux(host, port, token, tlsEnabled, true)
	if modernErr == nil {
		return false, nil
	}
	if !isVersionMismatchError(modernErr) {
		// Auth errors, network errors, etc. — not a version mismatch.
		return false, modernErr
	}

	// Retry with TCPMux=false (legacy compatibility).
	legacyErr := probeWithTCPMux(host, port, token, tlsEnabled, false)
	if legacyErr == nil {
		return true, nil
	}
	if isVersionMismatchError(legacyErr) {
		// Both modes failed with version mismatch — server is truly incompatible
		// or not a frps server at all.
		return false, fmt.Errorf("server incompatible with frpc %s: %w", FrpVersion, legacyErr)
	}
	// Legacy mode connected further but hit a different error (e.g. auth).
	// That's enough evidence that legacy mode is needed.
	return true, nil
}

// probeTCPReachable dials host:port with a bounded timeout, proves the TCP
// endpoint answers, and immediately closes the connection. It says nothing
// about the frp protocol — it only guards against the dial step hanging.
func probeTCPReachable(host string, port int) error {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), dialTimeout)
	if err != nil {
		return err
	}
	return conn.Close()
}

// probeWithTCPMux creates a throw-away frpc service with a dummy TCP proxy,
// runs it for probeTimeout, and returns any error encountered.
func probeWithTCPMux(host string, port int, token string, tlsEnabled, tcpMux bool) error {
	loginFailExit := true // fast fail for probe
	common := &frpconfig.ClientCommonConfig{
		ServerAddr:    host,
		ServerPort:    port,
		LoginFailExit: &loginFailExit,
		Auth:          frpconfig.AuthClientConfig{Token: token},
		Transport: frpconfig.ClientTransportConfig{
			TCPMux: &tcpMux,
			TLS:    frpconfig.TLSClientConfig{Enable: &tlsEnabled},
		},
	}

	testProxy := &frpconfig.TCPProxyConfig{
		ProxyBaseConfig: frpconfig.ProxyBaseConfig{
			Name: "sporemind-probe",
			Type: "tcp",
			ProxyBackend: frpconfig.ProxyBackend{
				LocalIP:   "127.0.0.1",
				LocalPort: 59999,
			},
		},
		RemotePort: 59999,
	}

	svc, err := frpclient.NewService(frpclient.ServiceOptions{
		Common:    common,
		ProxyCfgs: []frpconfig.ProxyConfigurer{testProxy},
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- svc.Run(ctx)
	}()

	select {
	case err := <-done:
		// Close the service to free resources.
		svc.GracefulClose(500 * time.Millisecond)
		return err
	case <-ctx.Done():
		// Timeout means the connection is still alive (or hanging) —
		// for our purposes that's "not a version mismatch".
		svc.GracefulClose(500 * time.Millisecond)
		return nil
	}
}
