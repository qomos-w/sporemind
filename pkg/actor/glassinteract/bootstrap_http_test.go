package glassinteract_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/actor/glassinteract"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// startGlassHTTPRuntime boots a runtime with the HTTP gateway and the
// glassinteract actor, then waits until the gateway accepts requests.
func startGlassHTTPRuntime(t *testing.T) *runtime.Handle {
	t.Helper()
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	t.Setenv("SPOREMIND_GLASS_KEY", "http-e2e-key")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	handle, err := runtime.Bootstrap(ctx, runtime.Config{
		GatewayAddr: "127.0.0.1:0",
		Children: []runtime.ChildSpec{
			{Name: "glassinteract", Factory: func() actor.Actor { return &glassinteract.Actor{} }, RequirePersistent: true, Role: "system", Planner: true},
		},
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = handle.Wait()
	})

	select {
	case <-handle.GatewayReady():
	case <-time.After(10 * time.Second):
		t.Fatal("gateway did not become ready")
	}
	return handle
}

func bootstrapURL(t *testing.T, h *runtime.Handle) string {
	t.Helper()
	gs := h.App().GatewayServer()
	if gs == nil {
		t.Fatal("no gateway server")
	}
	return "http://" + gs.Addr() + glassinteract.BootstrapRoute
}

func TestBootstrapHTTP_Success(t *testing.T) {
	h := startGlassHTTPRuntime(t)
	url := bootstrapURL(t, h)

	resp, err := http.Post(url, "application/json", bytes.NewReader([]byte(`{"Key":"http-e2e-key","DeviceId":"dev-http"}`)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, raw)
	}
	var boot gen.GlassBootstrapResp
	if err := json.Unmarshal(raw, &boot); err != nil {
		t.Fatalf("response parse: %v raw=%s", err, raw)
	}
	if boot.SessionID == "" || boot.DeviceID != "dev-http" || boot.Token == "" || boot.ExpiresAt == "" ||
		boot.Kind != "glass" || boot.Scope != "glass" {
		t.Fatalf("bootstrap resp = %+v", boot)
	}
}

func TestBootstrapHTTP_WrongMethod(t *testing.T) {
	h := startGlassHTTPRuntime(t)
	url := bootstrapURL(t, h)

	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, raw)
	}
	if allow := resp.Header.Get("Allow"); allow != http.MethodPost {
		t.Fatalf("Allow header = %q", allow)
	}
}

func TestBootstrapHTTP_InvalidBody(t *testing.T) {
	h := startGlassHTTPRuntime(t)
	url := bootstrapURL(t, h)

	resp, err := http.Post(url, "application/json", bytes.NewReader([]byte(`{not json`)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, raw)
	}
}

func TestBootstrapHTTP_WrongKey(t *testing.T) {
	h := startGlassHTTPRuntime(t)
	url := bootstrapURL(t, h)

	resp, err := http.Post(url, "application/json", bytes.NewReader([]byte(`{"Key":"wrong-key"}`)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, raw)
	}
}

func TestBootstrapHTTP_BodyTooLarge(t *testing.T) {
	h := startGlassHTTPRuntime(t)
	url := bootstrapURL(t, h)

	large := `{"Key":"` + strings.Repeat("a", 20<<10) + `"}`
	resp, err := http.Post(url, "application/json", bytes.NewReader([]byte(large)))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, raw)
	}
}
