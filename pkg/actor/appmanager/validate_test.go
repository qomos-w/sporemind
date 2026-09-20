package appmanager

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func newSecurityTestActor() *Actor {
	return &Actor{
		Apps:              map[string]gen.AppManifest{},
		Records:           map[string]appRecord{},
		children:          map[string]string{},
		bindings:          appbinding.NewRegistry(),
		FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
	}
}

func TestRegisterRejectsUnknownCapability(t *testing.T) {
	a := newSecurityTestActor()
	manifest := gen.AppManifest{ID: "app.unknown-cap", Name: "unknown-cap", Namespace: "app.unknown-cap", Version: "1.0.0", Runtime: "spore", ProtocolVersion: 1, Permissions: []string{"fs.admin"}, Callables: []gen.AppCallableDescriptor{{ID: "noop", RequestSchema: "Any", ResponseSchema: "Any"}}}
	_, err := a.handleRegister(&testutil.FakeCtx{}, gen.AppManagerRegisterReq{Manifest: manifest, EntryModule: "index.js", Modules: map[string]string{"index.js": "export default {}"}})
	if appbinding.DenialCode(err) != appbinding.CodePermissionDenied {
		t.Fatalf("expected unknown capability denied, got %v", err)
	}
	if !strings.Contains(err.Error(), "fs.admin") {
		t.Fatalf("expected error to mention capability 'fs.admin', got %v", err)
	}
	// denial must be audited
	audits := a.AuditRecords
	if len(audits) == 0 || audits[len(audits)-1].Callable != "register" || audits[len(audits)-1].Allowed {
		t.Fatalf("expected register denial audit, got %+v", audits)
	}
}

func TestRegisterAcceptsKnownCapabilities(t *testing.T) {
	a := newSecurityTestActor()
	manifest := gen.AppManifest{ID: "app.known", Name: "known", Namespace: "app.known", Version: "1.0.0", Runtime: "spore", ProtocolVersion: 1, Permissions: []string{"fs.read", "llm.invoke"}, Callables: []gen.AppCallableDescriptor{{ID: "noop", RequestSchema: "NoopReq", ResponseSchema: "NoopResp"}}}
	if err := validateManifestSecurity(manifest, a.Records); err != nil {
		t.Fatalf("expected declared known capabilities to pass, got %v", err)
	}
}

func TestRegisterEmptyPermissionsPasses(t *testing.T) {
	a := newSecurityTestActor()
	manifest := gen.AppManifest{ID: "app.empty", Name: "empty", Namespace: "app.empty", Version: "1.0.0", Runtime: "spore", ProtocolVersion: 1, Callables: []gen.AppCallableDescriptor{{ID: "noop", RequestSchema: "NoopReq", ResponseSchema: "NoopResp"}}}
	if err := validateManifestSecurity(manifest, a.Records); err != nil {
		t.Fatalf("expected empty permissions to pass, got %v", err)
	}
}

func TestRegisterRejectsCallableWithUndeclaredPermission(t *testing.T) {
	a := newSecurityTestActor()
	manifest := gen.AppManifest{
		ID: "app.perm2", Name: "perm2", Namespace: "app.perm2", Version: "1.0.0", Runtime: "spore", ProtocolVersion: 1,
		Permissions: []string{"fs.read"},
		Callables:   []gen.AppCallableDescriptor{{ID: "read", RequestSchema: "ReadReq", ResponseSchema: "ReadResp", Permission: "fs.admin"}},
	}
	_, err := a.handleRegister(&testutil.FakeCtx{}, gen.AppManagerRegisterReq{Manifest: manifest, EntryModule: "index.js", Modules: map[string]string{"index.js": "export default {}"}})
	if appbinding.DenialCode(err) != appbinding.CodePermissionDenied {
		t.Fatalf("expected permission denied, got %v", err)
	}
}

// TestRegisterBundlePermissionRequiresDependency pins that a plugin.*
// permission (per-callID bundle gate) is rejected unless the target plugin is
// a declared dependency in the same manifest.
func TestRegisterBundlePermissionRequiresDependency(t *testing.T) {
	a := newSecurityTestActor()
	a.Records["translator"] = appRecord{Manifest: gen.AppManifest{ID: "translator", Version: "1.0.0"}, PackageHash: "h1"}

	base := gen.AppManifest{
		ID: "app.bundle", Name: "bundle", Namespace: "app.bundle", Version: "1.0.0", Runtime: "spore", ProtocolVersion: 1,
		Callables: []gen.AppCallableDescriptor{{ID: "noop", RequestSchema: "NoopReq", ResponseSchema: "NoopResp"}},
	}

	// plugin.* permission without the matching dependency block: denied.
	m := base
	m.Permissions = []string{"plugin.translator.translate"}
	err := validateManifestSecurity(m, a.Records)
	if appbinding.DenialCode(err) != appbinding.CodeDependencyMissing {
		t.Fatalf("expected dependency-missing denial, got %v", err)
	}

	// With the dependency declared and the target registered: passes.
	m = base
	m.Permissions = []string{"plugin.translator.translate"}
	m.Dependencies = []gen.AppDependency{{ID: "translator", Version: "1.0.0"}}
	if err := validateManifestSecurity(m, a.Records); err != nil {
		t.Fatalf("declared bundle permission must pass: %v", err)
	}

	// Permission naming a different plugin than any declared dependency: denied.
	m = base
	m.Permissions = []string{"plugin.other.callable"}
	m.Dependencies = []gen.AppDependency{{ID: "translator", Version: "1.0.0"}}
	if appbinding.DenialCode(validateManifestSecurity(m, a.Records)) != appbinding.CodeDependencyMissing {
		t.Fatal("expected denial for permission naming undeclared plugin")
	}
}

// TestRegisterRejectsDependencyCycle pins that a manifest closing a loop
// through registered apps' dependencies is rejected (A→B→A).
func TestRegisterRejectsDependencyCycle(t *testing.T) {
	a := newSecurityTestActor()
	// Registered app "libb" depends on "liba".
	a.Records["liba"] = appRecord{Manifest: gen.AppManifest{ID: "liba", Version: "1.0.0"}}
	a.Records["libb"] = appRecord{Manifest: gen.AppManifest{ID: "libb", Version: "1.0.0", Dependencies: []gen.AppDependency{{ID: "liba"}}}}

	// New app "liba" (overwriting) now depends on "libb" → liba→libb→liba cycle.
	m := gen.AppManifest{
		ID: "liba", Name: "liba", Version: "1.0.0", Runtime: "spore", ProtocolVersion: 1,
		Dependencies: []gen.AppDependency{{ID: "libb"}},
		Callables:    []gen.AppCallableDescriptor{{ID: "noop", RequestSchema: "NoopReq", ResponseSchema: "NoopResp"}},
	}
	err := validateManifestSecurity(m, a.Records)
	if appbinding.DenialCode(err) != appbinding.CodeDependencyMissing {
		t.Fatalf("expected dependency-cycle denial, got %v", err)
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("error should mention cycle, got %v", err)
	}

	// Acyclic chain passes: libc → libb → liba.
	a.Records["libc"] = appRecord{Manifest: gen.AppManifest{ID: "libc", Version: "1.0.0"}}
	m2 := gen.AppManifest{
		ID: "libc2", Name: "libc2", Version: "1.0.0", Runtime: "spore", ProtocolVersion: 1,
		Dependencies: []gen.AppDependency{{ID: "libc"}},
		Callables:    []gen.AppCallableDescriptor{{ID: "noop", RequestSchema: "NoopReq", ResponseSchema: "NoopResp"}},
	}
	if err := validateManifestSecurity(m2, a.Records); err != nil {
		t.Fatalf("acyclic dependency chain must pass, got %v", err)
	}
}

func TestRegisterOriginOverrideRule(t *testing.T) {
	mk := func(origin string) appRecord {
		return appRecord{Manifest: gen.AppManifest{ID: "app.origin"}, Origin: normalizeOrigin(origin)}
	}
	records := map[string]appRecord{}
	// first registration always allowed
	if err := checkOriginOverride(records, mk("user")); err != nil {
		t.Fatalf("first register: %v", err)
	}
	records["app.origin"] = mk("user")
	// builtin cannot override user
	if err := checkOriginOverride(records, mk("builtin")); appbinding.DenialCode(err) != appbinding.CodePermissionDenied {
		t.Fatalf("expected builtin override denied, got %v", err)
	}
	// equal rank (user over user) allowed
	if err := checkOriginOverride(records, mk("user")); err != nil {
		t.Fatalf("user over user: %v", err)
	}
	// project overrides user
	if err := checkOriginOverride(records, mk("project")); err != nil {
		t.Fatalf("project over user: %v", err)
	}
	// user cannot override project
	records["app.origin"] = mk("project")
	if err := checkOriginOverride(records, mk("user")); appbinding.DenialCode(err) != appbinding.CodePermissionDenied {
		t.Fatalf("expected user override of project denied, got %v", err)
	}
}

func TestAgentActionFreeAgentGate(t *testing.T) {
	a := newSecurityTestActor()
	manifest := gen.AppManifest{
		ID: "app.agent", Name: "agent", Version: "1.0.0", Runtime: "spore", ProtocolVersion: 1,
		AgentBinding: &gen.AppAgentBinding{
			Surface:   &gen.AgentSurfaceBinding{AgentID: "agent-1", Entrypoint: "home"},
			FreeAgent: &gen.FreeAgentBinding{AllowMessage: true},
		},
	}
	a.Apps["app.agent"] = manifest
	a.Records["app.agent"] = appRecord{Manifest: manifest}
	if err := a.bindFreeAgentPolicy(manifest); err != nil {
		t.Fatalf("bind: %v", err)
	}
	ctx := &testutil.FakeCtx{}
	// create is not allowed by policy
	_, err := a.handleAgentAction(ctx, gen.AppManagerAgentActionReq{ID: "app.agent", Action: "create", AgentID: "agent-1"})
	if appbinding.DenialCode(err) != appbinding.CodeFreeAgentDenied {
		t.Fatalf("expected free agent denied, got %v", err)
	}
	// The wildcard policy applies to any caller: a policy denial for one
	// agent is a denial for all (no per-agent free-agent binding distinction).
	_, err = a.handleAgentAction(ctx, gen.AppManagerAgentActionReq{ID: "app.agent", Action: "create", AgentID: "intruder"})
	if appbinding.DenialCode(err) != appbinding.CodeFreeAgentDenied {
		t.Fatalf("expected wildcard policy denial for any caller, got %v", err)
	}
	// allowed action proceeds to workspace dispatch, which is unavailable in
	// this unit context — the audit trail records the outcome either way.
	_, _ = a.handleAgentAction(ctx, gen.AppManagerAgentActionReq{ID: "app.agent", Action: "message", AgentID: "agent-1"})
	var sawDeny bool
	for _, rec := range a.AuditRecords {
		if rec.Callable == "agent.create" && !rec.Allowed && strings.Contains(rec.Reason, appbinding.CodeFreeAgentDenied) {
			sawDeny = true
		}
	}
	if !sawDeny {
		t.Fatalf("expected free-agent denial audit, got %+v", a.AuditRecords)
	}
}

// bundlePermTestRecords builds a records map with one registered dependency
// "app.translator" exposing a bundle ("Translate", tools translate_detect +
// translate_run) and standalone callables lookup_glossary / summarize.
func bundlePermTestRecords() map[string]appRecord {
	return map[string]appRecord{
		"app.translator": {
			Manifest: gen.AppManifest{
				ID: "app.translator", Version: "1.0.0",
				Callables: []gen.AppCallableDescriptor{
					{ID: "translate_detect"}, {ID: "translate_run"},
					{ID: "lookup_glossary"}, {ID: "summarize"},
				},
				Bundles: []gen.AppBundle{{
					Title: "Translate",
					Tools: []gen.AppBundleTool{
						{CallableID: "translate_detect"},
						{CallableID: "translate_run"},
					},
				}},
			},
			PackageHash: "h1",
		},
	}
}

func bundlePermTestManifest(perms []string) gen.AppManifest {
	return gen.AppManifest{
		ID: "app.caller", Name: "caller", Namespace: "app.caller", Version: "1.0.0", Runtime: "spore", ProtocolVersion: 1,
		Permissions:  perms,
		Dependencies: []gen.AppDependency{{ID: "app.translator", Version: "1.0.0"}},
		Callables:    []gen.AppCallableDescriptor{{ID: "noop", RequestSchema: "NoopReq", ResponseSchema: "NoopResp"}},
	}
}

func TestExpandBundlePermissions(t *testing.T) {
	records := bundlePermTestRecords()

	// Bundle-level permission expands to the bundle's exact callIDs,
	// preserving order; non-plugin permissions pass through.
	m := bundlePermTestManifest([]string{"fs.read", "plugin.app.translator.translate"})
	got, err := expandBundlePermissions(m, records)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	want := []string{"fs.read", "plugin.app.translator.translate_detect", "plugin.app.translator.translate_run"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("expanded = %v, want %v", got, want)
	}

	// Callable-level permission passes through verbatim.
	got, err = expandBundlePermissions(bundlePermTestManifest([]string{"plugin.app.translator.lookup_glossary"}), records)
	if err != nil {
		t.Fatalf("expand callable-level: %v", err)
	}
	if len(got) != 1 || got[0] != "plugin.app.translator.lookup_glossary" {
		t.Fatalf("callable-level passthrough = %v", got)
	}

	// Callable-level entries do not require the callable to exist in the
	// dependency manifest at expansion time (host bridge enforces exactness
	// at invoke time).
	got, err = expandBundlePermissions(bundlePermTestManifest([]string{"plugin.app.translator.not_yet_declared"}), records)
	if err != nil {
		t.Fatalf("expand unknown callable: %v", err)
	}
	if len(got) != 1 || got[0] != "plugin.app.translator.not_yet_declared" {
		t.Fatalf("unknown callable passthrough = %v", got)
	}

	// Bundle + explicit callable overlap dedupes.
	got, err = expandBundlePermissions(bundlePermTestManifest([]string{
		"plugin.app.translator.translate", "plugin.app.translator.translate_detect",
	}), records)
	if err != nil {
		t.Fatalf("expand overlap: %v", err)
	}
	if len(got) != 2 || got[0] != "plugin.app.translator.translate_detect" || got[1] != "plugin.app.translator.translate_run" {
		t.Fatalf("overlap dedupe = %v", got)
	}

	// No plugin.* permissions: the input list is returned unchanged.
	m = bundlePermTestManifest([]string{"fs.read"})
	got, err = expandBundlePermissions(m, records)
	if err != nil || len(got) != 1 || got[0] != "fs.read" {
		t.Fatalf("no-plugin perms = %v, %v", got, err)
	}
}

func TestExpandBundlePermissionsDottedDepID(t *testing.T) {
	// Longest dep-ID prefix wins when one dependency ID is a prefix of
	// another ("app" vs "app.translator").
	records := bundlePermTestRecords()
	records["app"] = appRecord{Manifest: gen.AppManifest{ID: "app", Version: "1.0.0", Bundles: []gen.AppBundle{{Title: "Ghost", Tools: []gen.AppBundleTool{{CallableID: "nope"}}}}}}

	m := bundlePermTestManifest([]string{"plugin.app.translator.translate"})
	m.Dependencies = append(m.Dependencies, gen.AppDependency{ID: "app"})
	got, err := expandBundlePermissions(m, records)
	if err != nil {
		t.Fatalf("expand longest-prefix: %v", err)
	}
	if len(got) != 2 || !strings.Contains(strings.Join(got, ","), "plugin.app.translator.translate_detect") {
		t.Fatalf("longest-prefix expansion = %v", got)
	}
}

func TestExpandBundlePermissionsSlugDedup(t *testing.T) {
	records := map[string]appRecord{
		"dep": {Manifest: gen.AppManifest{
			ID: "dep", Version: "1.0.0",
			Bundles: []gen.AppBundle{
				{Title: "Tools", Tools: []gen.AppBundleTool{{CallableID: "first"}}},
				{Title: "tools", Tools: []gen.AppBundleTool{{CallableID: "second"}}},
			},
		}},
	}
	m := gen.AppManifest{
		ID: "app.dedup", Version: "1.0.0", Runtime: "spore", ProtocolVersion: 1,
		Permissions:  []string{"plugin.dep.tools-2"},
		Dependencies: []gen.AppDependency{{ID: "dep"}},
	}
	got, err := expandBundlePermissions(m, records)
	if err != nil {
		t.Fatalf("expand dedup slug: %v", err)
	}
	if len(got) != 1 || got[0] != "plugin.dep.second" {
		t.Fatalf("dedup slug expansion = %v, want [plugin.dep.second]", got)
	}
}

func TestExpandBundlePermissionsRejections(t *testing.T) {
	records := bundlePermTestRecords()

	// Bundle slug that is also a callable ID of the dependency: ambiguous.
	ambRecords := bundlePermTestRecords()
	dep := ambRecords["app.translator"]
	dep.Manifest.Bundles = append(dep.Manifest.Bundles, gen.AppBundle{
		Title: "Summarize", Tools: []gen.AppBundleTool{{CallableID: "summarize"}},
	})
	ambRecords["app.translator"] = dep
	m := bundlePermTestManifest([]string{"plugin.app.translator.summarize"})
	_, err := expandBundlePermissions(m, ambRecords)
	if appbinding.DenialCode(err) != appbinding.CodePermissionDenied || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguous denial, got %v", err)
	}

	// Unknown bundle slug falls back to callable-level passthrough (host
	// bridge rejects it at invoke time) — not an expansion error.

	// Bundle with no callables grants nothing: rejected.
	emptyRecords := map[string]appRecord{
		"dep": {Manifest: gen.AppManifest{
			ID: "dep", Version: "1.0.0",
			Bundles: []gen.AppBundle{{Title: "Prompts Only"}},
		}},
	}
	m = gen.AppManifest{
		ID: "app.empty", Version: "1.0.0", Runtime: "spore", ProtocolVersion: 1,
		Permissions:  []string{"plugin.dep.prompts-only"},
		Dependencies: []gen.AppDependency{{ID: "dep"}},
	}
	_, err = expandBundlePermissions(m, emptyRecords)
	if appbinding.DenialCode(err) != appbinding.CodeDependencyMissing || !strings.Contains(err.Error(), "grants nothing") {
		t.Fatalf("expected grants-nothing denial, got %v", err)
	}

	// Plugin permission naming an undeclared dependency: rejected.
	m = bundlePermTestManifest([]string{"plugin.app.other.translate"})
	_, err = expandBundlePermissions(m, records)
	if appbinding.DenialCode(err) != appbinding.CodeDependencyMissing {
		t.Fatalf("expected undeclared-dependency denial, got %v", err)
	}

	// Declared but unregistered dependency: rejected.
	m = bundlePermTestManifest([]string{"plugin.app.translator.translate"})
	_, err = expandBundlePermissions(m, map[string]appRecord{})
	if appbinding.DenialCode(err) != appbinding.CodeDependencyMissing {
		t.Fatalf("expected unregistered-dependency denial, got %v", err)
	}
}

// TestValidateManifestSecurityBundlePermission pins that bundle-level
// permissions validate through validateManifestSecurity (bundle resolves) and
// that an ambiguous name is denied at register time.
func TestValidateManifestSecurityBundlePermission(t *testing.T) {
	records := bundlePermTestRecords()

	m := bundlePermTestManifest([]string{"fs.read", "plugin.app.translator.translate"})
	if err := validateManifestSecurity(m, records); err != nil {
		t.Fatalf("bundle-level permission must validate: %v", err)
	}

	ambRecords := bundlePermTestRecords()
	dep := ambRecords["app.translator"]
	dep.Manifest.Bundles = append(dep.Manifest.Bundles, gen.AppBundle{
		Title: "Summarize", Tools: []gen.AppBundleTool{{CallableID: "summarize"}},
	})
	ambRecords["app.translator"] = dep
	m = bundlePermTestManifest([]string{"plugin.app.translator.summarize"})
	if err := validateManifestSecurity(m, ambRecords); appbinding.DenialCode(err) != appbinding.CodePermissionDenied {
		t.Fatalf("expected ambiguous denial through validateManifestSecurity, got %v", err)
	}
}
