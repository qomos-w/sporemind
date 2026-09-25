package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/cmd/internal/actorset"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// cleanupDataDir removes persisted workspace state so tests start fresh.
func cleanupDataDir(t *testing.T) {
	t.Helper()
	dataDir := filepath.Join(".", "data")
	if err := os.RemoveAll(dataDir); err != nil {
		t.Logf("cleanup data dir: %v", err)
	}
}

// TestGatewayEphemeralAdopt verifies the ":0 means OS-assigned" semantics end
// to end: a runtime booted with an ephemeral gateway address adopts the
// actually bound address into the in-process config once the listener is
// accepting, and the adopted address is dialable.
func TestGatewayEphemeralAdopt(t *testing.T) {
	cleanupDataDir(t)
	defer cleanupDataDir(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	origAddr := config.GatewayAddr()
	origDataDir := config.DataDir()
	config.SetGatewayAddr("127.0.0.1:0")
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(func() {
		config.SetGatewayAddr(origAddr)
		config.SetDataDirForTest(origDataDir)
	})

	handle, err := runtime.Bootstrap(ctx, runtime.Config{
		GatewayAddr: "127.0.0.1:0",
		Children:    actorset.Default(),
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() {
		cancel()
		_ = handle.Wait()
	}()

	deadline := time.Now().Add(15 * time.Second)
	for config.GatewayAddr() == "127.0.0.1:0" {
		if time.Now().After(deadline) {
			t.Fatal("gateway never adopted the bound address")
		}
		time.Sleep(100 * time.Millisecond)
	}

	bound := config.GatewayAddr()
	host, port, err := net.SplitHostPort(bound)
	if err != nil || port == "" || port == "0" {
		t.Fatalf("adopted address %q is not dialable", bound)
	}
	if host == "" {
		host = "localhost"
	}
	resp, err := http.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		t.Fatalf("get healthz on adopted address %q: %v", bound, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz on adopted address: status %d", resp.StatusCode)
	}
}

// TestGatewayInvoke verifies that the HTTP gateway correctly routes requests
// to actors and returns responses (not timeouts).
func TestGatewayInvoke(t *testing.T) {
	cleanupDataDir(t)
	defer cleanupDataDir(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg := runtime.Config{
		GatewayAddr: ":18082",
		NoGateway:   false,
		Children:    actorset.Default(),
	}
	handle, err := runtime.Bootstrap(ctx, cfg)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() {
		cancel()
		_ = handle.Wait()
	}()

	// Wait for gateway to start.
	time.Sleep(2 * time.Second)

	invoke := func(callID string, payload any, role string) (int, string) {
		body, _ := json.Marshal(payload)
		req, _ := http.NewRequest("POST", "http://localhost:18082/api/"+callID, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if role != "" {
			req.Header.Set("X-Role", role)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("post %s: %v", callID, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	t.Run("healthz", func(t *testing.T) {
		resp, err := http.Get("http://localhost:18082/healthz")
		if err != nil {
			t.Fatalf("get healthz: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("healthz: unexpected status %d body=%s", resp.StatusCode, string(body))
		}
		if string(bytes.TrimSpace(body)) != `{"status":"ok"}` {
			t.Errorf("healthz: unexpected body %s", string(body))
		}
	})

	t.Run("workspace.list_project_unauth", func(t *testing.T) {
		status, body := invoke("workspace.list_project", nil, "")
		if status != 200 && status != 204 {
			t.Errorf("workspace.list_project: unexpected status %d body=%s", status, body)
		}
	})

	t.Run("workspace.mount_denied_without_role", func(t *testing.T) {
		tmp := os.TempDir() + "/sporemind-test-mount-denied-" + fmt.Sprintf("%d", time.Now().UnixNano())
		_ = os.MkdirAll(tmp, 0o755)
		defer os.RemoveAll(tmp)
		status, body := invoke("workspace.mount", map[string]any{
			"Path": tmp,
			"Name": "test-mount-denied",
		}, "")
		if status != 500 {
			t.Errorf("workspace.mount: expected 500 (policy denied), got %d body=%s", status, body)
		}
	})

	// workspace.mount now requires an admin caller under the role ladder.
	t.Run("workspace.mount_ok_with_admin_role", func(t *testing.T) {
		tmp := os.TempDir() + "/sporemind-test-mount-admin-" + fmt.Sprintf("%d", time.Now().UnixNano())
		_ = os.MkdirAll(tmp, 0o755)
		defer os.RemoveAll(tmp)
		status, body := invoke("workspace.mount", map[string]any{
			"Path": tmp,
			"Name": "test-mount-admin",
		}, "admin")
		if status != 200 && status != 204 {
			t.Errorf("workspace.mount: unexpected status %d body=%s", status, body)
		}
		// Verify the mount appears in list.
		status2, body2 := invoke("workspace.list_project", nil, "")
		if status2 != 200 {
			t.Errorf("workspace.list_project after mount: unexpected status %d", status2)
		}
		if !bytes.Contains([]byte(body2), []byte("test-mount-admin")) {
			t.Errorf("workspace.list_project after mount: expected test-mount-admin in body, got %s", body2)
		}
	})

	// Verify browsermanager.list is reachable end-to-end through the gateway.
	t.Run("browsermanager.list", func(t *testing.T) {
		status, body := invoke("browsermanager.list", nil, "")
		if status != 200 {
			t.Fatalf("browsermanager.list: unexpected status %d body=%s", status, body)
		}
		var list struct {
			Items []any `json:"Items"`
		}
		if err := json.Unmarshal([]byte(body), &list); err != nil {
			t.Fatalf("browsermanager.list: unmarshal: %v body=%s", err, body)
		}
		t.Logf("browsermanager.list: items=%d", len(list.Items))
	})

	// Verify unified_graph.sync returns the mounted project as an actor node.
	t.Run("unified_graph.project_node", func(t *testing.T) {
		tmp := os.TempDir() + "/sporemind-test-project-node-" + fmt.Sprintf("%d", time.Now().UnixNano())
		_ = os.MkdirAll(tmp, 0o755)
		defer os.RemoveAll(tmp)

		// Mount a project so workspace enrichment updates the topology node label.
		// workspace.mount requires admin under the role ladder.
		status, _ := invoke("workspace.mount", map[string]any{
			"Path": tmp,
			"Name": "test-project-node",
		}, "admin")
		if status != 200 && status != 204 {
			t.Fatalf("workspace.mount: unexpected status %d", status)
		}

		// Wait for topology refresh to pick up the workspace data.
		time.Sleep(500 * time.Millisecond)

		status, body := invoke("unified_graph.sync", map[string]any{"clientEpoch": 0}, "")
		if status != 200 {
			t.Fatalf("unified_graph.sync: unexpected status %d body=%s", status, body)
		}

		var graph struct {
			Snapshot struct {
				Nodes []struct {
					ID       string `json:"id"`
					Kind     string `json:"kind"`
					Label    string `json:"label"`
					ParentID string `json:"parentId"`
				} `json:"nodes"`
			} `json:"snapshot"`
		}
		if err := json.Unmarshal([]byte(body), &graph); err != nil {
			t.Fatalf("unified_graph.sync: unmarshal: %v body=%s", err, body)
		}

		var foundProject bool
		for _, n := range graph.Snapshot.Nodes {
			if n.Kind == "project" && n.ParentID != "" {
				foundProject = true
				t.Logf("found project node: id=%s kind=%s parentId=%s", n.ID, n.Kind, n.ParentID)
			}
		}
		if !foundProject {
			t.Errorf("unified_graph.sync: no project node found")
		}
	})
}
