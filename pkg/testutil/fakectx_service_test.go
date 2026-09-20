package testutil

import (
	"testing"

	"github.com/qomos-w/gospore/actor"
)

// These tests pin FakeCtx's ServiceName derivation to the real cell's
// behavior (gospore internal/cell/context.go):
//   - Register/RegisterScript forward derivation via actor.ServiceNameFor
//     against declared domains (the sole source — the WithService option
//     no longer exists);
//   - RegisterDomain backfilling already-recorded callables whose first
//     callID segment matches and whose derived name is still empty;
//   - flat (dotless) callIDs never derive — "" means agent-local.

func TestFakeCtx_ServiceForwardDerivation(t *testing.T) {
	f := HumanCtx(GenActorID())
	f.RegisterDomain("oracle")
	if err := f.Register("oracle.list_diagnostics", nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	if got := f.ServiceNames["oracle.list_diagnostics"]; got != "oracle" {
		t.Errorf("ServiceNames[oracle.list_diagnostics] = %q, want oracle (forward derivation from declared domain)", got)
	}

	// Flat callables never derive, even with a domain whose name matches the
	// callable's (only) segment.
	f.RegisterDomain("agent")
	if err := f.Register("list_callables", nil); err != nil {
		t.Fatalf("register flat: %v", err)
	}
	if got := f.ServiceNames["list_callables"]; got != "" {
		t.Errorf("ServiceNames[list_callables] = %q, want \"\" (flat callID never derives)", got)
	}

	// Exact first-segment match only: no prefix collision between domains.
	f.RegisterDomain("app")
	if err := f.Register("appmanager.list", nil); err != nil {
		t.Fatalf("register prefix: %v", err)
	}
	if got := f.ServiceNames["appmanager.list"]; got != "" {
		t.Errorf("ServiceNames[appmanager.list] = %q, want \"\" (app must not match appmanager)", got)
	}
}

func TestFakeCtx_ServiceDerivationWithOtherOpts(t *testing.T) {
	f := HumanCtx(GenActorID())
	f.RegisterDomain("oracle")
	if err := f.Register("oracle.list_diagnostics", nil, actor.Public()); err != nil {
		t.Fatalf("register: %v", err)
	}
	if got := f.ServiceNames["oracle.list_diagnostics"]; got != "oracle" {
		t.Errorf("ServiceNames[oracle.list_diagnostics] = %q, want oracle (derivation unaffected by other register options)", got)
	}
}

func TestFakeCtx_ServiceNoDomainDeclared(t *testing.T) {
	f := HumanCtx(GenActorID())
	if err := f.Register("oracle.list_diagnostics", nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	if got := f.ServiceNames["oracle.list_diagnostics"]; got != "" {
		t.Errorf("ServiceNames[oracle.list_diagnostics] = %q, want \"\" (no domain declared, nothing derives)", got)
	}
}

func TestFakeCtx_RegisterDomainBackfill(t *testing.T) {
	f := HumanCtx(GenActorID())
	// Register before RegisterDomain: nothing derives yet.
	_ = f.Register("oracle.search_services", nil)
	_ = f.Register("list_callables", nil)

	f.RegisterDomain("oracle")

	if got := f.ServiceNames["oracle.search_services"]; got != "oracle" {
		t.Errorf("ServiceNames[oracle.search_services] = %q, want oracle (backfilled by RegisterDomain)", got)
	}
	if got := f.ServiceNames["list_callables"]; got != "" {
		t.Errorf("ServiceNames[list_callables] = %q, want \"\" (flat callID is never backfilled)", got)
	}

	// Idempotent: declaring the domain again changes nothing.
	f.RegisterDomain("oracle")
	if got := f.ServiceNames["oracle.search_services"]; got != "oracle" {
		t.Errorf("after second RegisterDomain: %q, want oracle", got)
	}

	// After the domain exists, later registrations derive forward.
	if err := f.Register("oracle.list_diagnostics", nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	if got := f.ServiceNames["oracle.list_diagnostics"]; got != "oracle" {
		t.Errorf("ServiceNames[oracle.list_diagnostics] = %q, want oracle (forward derivation after domain declared)", got)
	}
}

func TestFakeCtx_RegisterScriptDerivation(t *testing.T) {
	f := HumanCtx(GenActorID())
	f.RegisterDomain("voice")
	if err := f.RegisterScript("voice.speak", "source", actor.ModeStateful); err != nil {
		t.Fatalf("register script: %v", err)
	}
	if got := f.ServiceNames["voice.speak"]; got != "voice" {
		t.Errorf("ServiceNames[voice.speak] = %q, want voice (script registration derives like Register)", got)
	}
	if err := f.RegisterScript("speak_flat", "source", actor.ModeStateful); err != nil {
		t.Fatalf("register flat script: %v", err)
	}
	if got := f.ServiceNames["speak_flat"]; got != "" {
		t.Errorf("ServiceNames[speak_flat] = %q, want \"\" (flat script callID never derives)", got)
	}
}

func TestFakeCtx_ExistingRecordContractsPreserved(t *testing.T) {
	f := HumanCtx(GenActorID())
	f.RegisteredDomains = []string{}
	f.RegOpts = map[string][]actor.RegisterOption{}
	handler := func() {}
	if err := f.Register("oracle.x", handler, actor.Public()); err != nil {
		t.Fatalf("register: %v", err)
	}
	if f.Regs["oracle.x"] == nil {
		t.Errorf("Regs[oracle.x] must record the handler")
	}
	if actor.ResolveVisibility(f.RegOpts["oracle.x"]...) != actor.VisibilityPublic {
		t.Errorf("RegOpts[oracle.x] must still record the raw options")
	}
	if len(f.RegisteredDomains) != 0 {
		t.Errorf("RegisteredDomains = %v, want empty before any RegisterDomain call", f.RegisteredDomains)
	}
	h := f.RegisterDomain("oracle")
	if h == nil {
		t.Fatalf("RegisterDomain must return a non-nil DomainHandle")
	}
	if len(f.RegisteredDomains) != 1 || f.RegisteredDomains[0] != "oracle" {
		t.Errorf("RegisteredDomains = %v, want [oracle]", f.RegisteredDomains)
	}
	if d := f.Domains(); len(d) != 1 || d[0] != "oracle" {
		t.Errorf("Domains() = %v, want [oracle]", d)
	}
}
