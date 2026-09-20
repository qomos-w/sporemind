package websearch_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/actor/websearch"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// TestWebSearchOnStartRegistersService boots only the websearch actor in a
// real App and invokes websearch.provider_list through the service-ref path
// (the same path the turn engine's resolveServiceRefs uses). If OnStart
// failed, the service would not be exposed and LookupService would miss,
// causing "call ID "websearch.search" not registered" style cell errors.
func TestWebSearchOnStartRegistersService(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	h, err := runtime.Bootstrap(ctx, runtime.Config{
		NoGateway: true,
		Children: []runtime.ChildSpec{
			{Name: "websearch", Factory: func() actor.Actor { return &websearch.Actor{} }, RequirePersistent: true, Role: "system"},
		},
	})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	defer func() {
		cancel()
		_ = h.Wait()
	}()

	// Give the ordered start a moment to run OnStart for the websearch cell.
	time.Sleep(500 * time.Millisecond)

	app := h.App()
	ref, ok := app.LookupService("websearch")
	if !ok {
		t.Fatal("LookupService(\"websearch\") missed — websearch service not exposed (OnStart likely failed)")
	}

	call := ref.Invoke(context.Background(), "websearch.provider_list", nil)
	if call == nil {
		t.Fatal("websearch.provider_list invoke returned nil")
	}
	defer call.Close()
	raw, err := call.RecvRaw()
	if err != nil {
		t.Fatalf("websearch.provider_list returned error: %v", err)
	}
	if strings.Contains(string(raw), "not registered") {
		t.Fatalf("call routed to wrong cell (not registered): %s", string(raw))
	}

	var resp struct {
		Providers []struct {
			ID          string `json:"ID"`
			Name        string `json:"Name"`
			RequiresKey bool   `json:"RequiresKey"`
		} `json:"Providers"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode provider_list resp %q: %v", string(raw), err)
	}
	if len(resp.Providers) == 0 {
		t.Fatalf("expected providers, got %s", string(raw))
	}
	t.Logf("providers: %+v", resp.Providers)
}
