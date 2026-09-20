package persist

import (
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// dockerAvailable reports whether a docker daemon responds to `docker info`.
// Container-based contract tests skip when this is false (CI without docker,
// or local docker not running), matching the card's "CI 可选跳过".
func dockerAvailable() bool {
	if testing.Short() {
		return false
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// containerHandle is a started docker container with its published host port.
type containerHandle struct {
	hostPort string // host:port published by the docker host (reachable via 127.0.0.1)
	stop     func()
}

// startContainer runs image with the given env and published container port.
// It does not wait for readiness — call waitReady. cleanup stops/removes the
// container. On any docker error the test is skipped, not failed: containers
// are an optional verification layer.
func startContainer(t *testing.T, image, containerPort string, env []string, cmd []string) *containerHandle {
	t.Helper()
	args := []string{"run", "-d", "--rm", "-p", "0:" + containerPort}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	args = append(args, image)
	args = append(args, cmd...)
	runOut, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Logf("docker run %s: %s", image, strings.TrimSpace(string(runOut)))
		t.Skipf("could not start %s container: %v", image, err)
	}
	id := strings.TrimSpace(string(runOut))
	hostPort, err := dockerHostPort(id, containerPort)
	if err != nil {
		_ = exec.Command("docker", "rm", "-f", id).Run()
		t.Skipf("could not inspect %s port: %v", image, err)
	}
	stop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "docker", "stop", id).Run()
	}
	return &containerHandle{hostPort: hostPort, stop: stop}
}

// dockerHostPort returns the host:port mapped for the container's exposed port.
func dockerHostPort(id, containerPort string) (string, error) {
	format := fmt.Sprintf("{{(index (index .NetworkSettings.Ports %q) 0).HostPort}}", containerPort+"/tcp")
	out, err := exec.Command("docker", "inspect", "-f", format, id).Output()
	if err != nil {
		return "", err
	}
	port := strings.TrimSpace(string(out))
	if port == "" {
		return "", fmt.Errorf("empty host port for %s", containerPort)
	}
	return net.JoinHostPort("127.0.0.1", port), nil
}

// waitReady polls ready every 500ms until deadline, returning once ready()
// succeeds. lastErr is reported on skip.
func waitReady(t *testing.T, name string, deadline time.Duration, ready func() error) {
	t.Helper()
	deadlineTime := time.Now().Add(deadline)
	var lastErr error
	for time.Now().Before(deadlineTime) {
		if err := ready(); err == nil {
			return
		} else {
			lastErr = err
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Skipf("%s not ready within %s: %v", name, deadline, lastErr)
}

// useFakeTunnel installs a process-local tunnel resolver that forwards
// connections from a loopback listener to the real target. It exercises the
// exact production tunnel semantics (backend dials the resolver's localAddr
// with TLS off) without needing sshmanager. The real SSH tunnel is e2e-tested
// in pkg/actor/sshmanager.
func useFakeTunnel(t *testing.T) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("fake tunnel listen: %v", err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go forwardTunnelConn(t, c)
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		SetTunnelResolver(nil)
	})
	SetTunnelResolver(func(ref, targetAddr string) (string, error) {
		return ln.Addr().String(), nil
	})
}

// forwardTunnelConn dials the real target and pipes both directions. The
// target is captured per-accept from the resolver call that opened this
// connection; here the fake forwards to the single target the test set up.
func forwardTunnelConn(t *testing.T, c net.Conn) {
	defer c.Close()
	up, err := net.DialTimeout("tcp", fakeTunnelTarget, 10*time.Second)
	if err != nil {
		t.Logf("fake tunnel dial %s: %v", fakeTunnelTarget, err)
		return
	}
	defer up.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, c); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, up); done <- struct{}{} }()
	<-done
}

// fakeTunnelTarget is the single forward target for the process-local fake
// tunnel. Each container test sets it before useFakeTunnel.
var fakeTunnelTarget string

// containerCredentials installs a credential resolver returning the shared
// container admin credentials and returns the CredentialRef to use.
func containerCredentials(t *testing.T, user, pass string) string {
	t.Helper()
	const ref = "container-admin"
	t.Cleanup(func() { SetCredentialResolver(nil) })
	SetCredentialResolver(func(r string) (Credential, error) {
		if r != ref {
			return Credential{}, fmt.Errorf("unexpected credential ref %q", r)
		}
		return Credential{Username: user, Password: pass}, nil
	})
	return ref
}
