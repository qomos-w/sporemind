package appmanager

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/codegen"
)

// The bundle_drift gate compares dev_generate's persisted snapshots against
// what the CURRENT registry resolves. These tests pin the pure comparison:
// never-generated trees pass, stale snapshots fail with a precise diff, and
// diagnostics surface the root cause.

func driftSnapshot(depID, version, pkgHash string, callables ...string) codegen.BundleSnapshot {
	return codegen.BundleSnapshot{
		DepID: depID, DepVersion: version, DepPackageHash: pkgHash,
		Callables: callables, SchemaHashes: map[string]string{"Req": "h1"},
	}
}

func TestBundleDriftGatePassCases(t *testing.T) {
	cases := []struct {
		name         string
		hasGenerated bool
		persisted    []codegen.BundleSnapshot
		expected     []codegen.BundleSnapshot
	}{
		{"nothing bundle-level", true, nil, nil},
		{"never generated", false, nil, []codegen.BundleSnapshot{driftSnapshot("app.dep", "1.0.0", "h", "plugin.app.dep.a")}},
		{"matching snapshots", true,
			[]codegen.BundleSnapshot{driftSnapshot("app.dep", "1.0.0", "h", "plugin.app.dep.a")},
			[]codegen.BundleSnapshot{driftSnapshot("app.dep", "1.0.0", "h", "plugin.app.dep.a")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := checkBundleDriftGate(tc.hasGenerated, tc.persisted, tc.expected, "")
			if !res.Passed {
				t.Fatalf("gate failed: %+v", res.Error)
			}
		})
	}
}

func TestBundleDriftGateDetectsDrift(t *testing.T) {
	base := driftSnapshot("app.dep", "1.0.0", "h1", "plugin.app.dep.a", "plugin.app.dep.b")
	cases := []struct {
		name      string
		persisted []codegen.BundleSnapshot
		expected  []codegen.BundleSnapshot
		want      string
	}{
		{
			"generated before deps registered",
			nil,
			[]codegen.BundleSnapshot{base},
			"snapshots are empty",
		},
		{
			"callable added",
			[]codegen.BundleSnapshot{base},
			[]codegen.BundleSnapshot{driftSnapshot("app.dep", "1.0.0", "h1", "plugin.app.dep.a", "plugin.app.dep.b", "plugin.app.dep.c")},
			"added plugin.app.dep.c",
		},
		{
			"callable removed",
			[]codegen.BundleSnapshot{base},
			[]codegen.BundleSnapshot{driftSnapshot("app.dep", "1.0.0", "h1", "plugin.app.dep.a")},
			"removed plugin.app.dep.b",
		},
		{
			"version changed",
			[]codegen.BundleSnapshot{base},
			[]codegen.BundleSnapshot{driftSnapshot("app.dep", "2.0.0", "h1", "plugin.app.dep.a", "plugin.app.dep.b")},
			"version 1.0.0 -> 2.0.0",
		},
		{
			"package hash changed",
			[]codegen.BundleSnapshot{base},
			[]codegen.BundleSnapshot{driftSnapshot("app.dep", "1.0.0", "h2", "plugin.app.dep.a", "plugin.app.dep.b")},
			"package hash changed",
		},
		{
			"schema hash changed",
			[]codegen.BundleSnapshot{base},
			[]codegen.BundleSnapshot{func() codegen.BundleSnapshot {
				s := driftSnapshot("app.dep", "1.0.0", "h1", "plugin.app.dep.a", "plugin.app.dep.b")
				s.SchemaHashes = map[string]string{"Req": "h9"}
				return s
			}()},
			"Req: h1 -> h9",
		},
		{
			"dependency vanished from registry",
			[]codegen.BundleSnapshot{base},
			nil,
			"no longer resolvable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := checkBundleDriftGate(true, tc.persisted, tc.expected, "")
			if res.Passed {
				t.Fatalf("gate passed, want failure")
			}
			if !strings.Contains(res.Error.Detail, tc.want) {
				t.Errorf("detail %q missing %q", res.Error.Detail, tc.want)
			}
			if res.Error.Fix == "" || !strings.Contains(res.Error.Fix, "dev_generate") {
				t.Errorf("fix hint must point at dev_generate: %q", res.Error.Fix)
			}
		})
	}
}

func TestBundleDriftGateDiagnosticFails(t *testing.T) {
	res := checkBundleDriftGate(true, nil, nil, "bundle permission re-resolution failed: boom")
	if res.Passed {
		t.Fatal("gate passed despite diagnostic")
	}
	if !strings.Contains(res.Error.Detail, "boom") {
		t.Errorf("detail = %q", res.Error.Detail)
	}
}

func TestBundleDriftGateNewDependencyAfterGeneration(t *testing.T) {
	res := checkBundleDriftGate(true, []codegen.BundleSnapshot{driftSnapshot("app.dep", "1.0.0", "h", "plugin.app.dep.a")},
		[]codegen.BundleSnapshot{
			driftSnapshot("app.dep", "1.0.0", "h", "plugin.app.dep.a"),
			driftSnapshot("app.other", "1.0.0", "h", "plugin.app.other.x"),
		}, "")
	if res.Passed {
		t.Fatal("gate passed despite new dependency")
	}
	if !strings.Contains(res.Error.Detail, "app.other") || !strings.Contains(res.Error.Detail, "snapshot missing") {
		t.Errorf("detail = %q", res.Error.Detail)
	}
}
