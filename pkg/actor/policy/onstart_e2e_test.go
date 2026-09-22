package policy_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/actor/policy"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// TestPolicyOnStartRegistersService boots only the policy actor in a real App
// and drives policy.status + policy.configure through the service-ref path.
// If OnStart failed, LookupService would miss — the same failure class as
// "call ID ... not registered" cell errors.
func TestPolicyOnStartRegistersService(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	h, err := runtime.Bootstrap(ctx, runtime.Config{
		NoGateway: true,
		Children: []runtime.ChildSpec{
			{Name: "policy", Factory: func() actor.Actor { return &policy.Actor{} }, RequirePersistent: true, Role: "system"},
		},
	})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	defer func() {
		cancel()
		_ = h.Wait()
	}()

	time.Sleep(500 * time.Millisecond)

	app := h.App()
	ref, ok := app.LookupService("policy")
	if !ok {
		t.Fatal("LookupService(\"policy\") missed — policy service not exposed (OnStart likely failed)")
	}

	// status before any configuration: auto mode, nothing configured.
	call := ref.Invoke(context.Background(), "policy.status", map[string]any{})
	if call == nil {
		t.Fatal("policy.status invoke returned nil")
	}
	raw, err := call.RecvRaw()
	if call != nil {
		defer call.Close()
	}
	if err != nil {
		t.Fatalf("policy.status returned error: %v", err)
	}
	if strings.Contains(string(raw), "not registered") {
		t.Fatalf("call routed to wrong cell: %s", string(raw))
	}
	var status struct {
		Backend      string `json:"Backend"`
		JevConfigured bool  `json:"JevConfigured"`
		LLMConfigured bool  `json:"LLMConfigured"`
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		t.Fatalf("decode status %q: %v", string(raw), err)
	}
	if status.Backend != "auto" || status.JevConfigured || status.LLMConfigured {
		t.Fatalf("fresh status should be auto/empty, got %+v", status)
	}

	// configure roundtrip: the key must never echo back.
	cfgCall := ref.Invoke(context.Background(), "policy.configure", map[string]any{
		"Backend": "jev",
		"Jev":     map[string]any{"ApiKey": "secret-e2e"},
	})
	if cfgCall != nil {
		defer cfgCall.Close()
		cfgRaw, err := cfgCall.RecvRaw()
		if err != nil {
			t.Fatalf("policy.configure returned error: %v", err)
		}
		if strings.Contains(string(cfgRaw), "secret-e2e") {
			t.Fatalf("configure response leaked the key: %s", string(cfgRaw))
		}
		var cfgResp struct {
			Status struct {
				Backend       string `json:"Backend"`
				JevConfigured bool   `json:"JevConfigured"`
			} `json:"Status"`
		}
		if err := json.Unmarshal(cfgRaw, &cfgResp); err != nil {
			t.Fatalf("decode configure resp: %v", err)
		}
		if cfgResp.Status.Backend != "jev" || !cfgResp.Status.JevConfigured {
			t.Fatalf("configure status = %+v", cfgResp.Status)
		}
	}
}
