package appmanager

import (
	"strings"
	"testing"
)

// TestStaleRegistryDiagnostic pins the coverage-gate freshness rule: the
// running plugin's dispatch registry may only stand in for the freshly built
// artifact when the appmanager record proves they are the same build. A
// staged (restart_pending) or hash-mismatched record means the registry
// predates the build, and the gate must surface that root cause instead of
// diffing the new manifest against the old registry (false "callable not
// registered" positives).
func TestStaleRegistryDiagnostic(t *testing.T) {
	const (
		appID     = "app.demo"
		builtHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		runHash   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)

	t.Run("no record: reuse is all the gate has", func(t *testing.T) {
		a := &Actor{Records: map[string]appRecord{}}
		if got := a.staleRegistryDiagnostic(appID, builtHash); got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})

	t.Run("restart_pending: running artifact predates the staged one", func(t *testing.T) {
		a := &Actor{Records: map[string]appRecord{
			appID: {State: stateRestartPending, ArtifactHash: builtHash},
		}}
		got := a.staleRegistryDiagnostic(appID, builtHash)
		if !strings.Contains(got, "stale running artifact") || !strings.Contains(got, "restart_pending") {
			t.Fatalf("got %q, want stale + restart_pending root cause", got)
		}
	})

	t.Run("hash mismatch: running artifact is an older build", func(t *testing.T) {
		a := &Actor{Records: map[string]appRecord{
			appID: {State: stateRunning, ArtifactHash: runHash},
		}}
		got := a.staleRegistryDiagnostic(appID, builtHash)
		if !strings.Contains(got, "stale running artifact") ||
			!strings.Contains(got, runHash) || !strings.Contains(got, builtHash) ||
			!strings.Contains(got, "reload_project") {
			t.Fatalf("got %q, want both hashes and the reload remediation", got)
		}
	})

	t.Run("hash match: running artifact IS this build", func(t *testing.T) {
		a := &Actor{Records: map[string]appRecord{
			appID: {State: stateRunning, ArtifactHash: strings.ToUpper(builtHash)},
		}}
		if got := a.staleRegistryDiagnostic(appID, builtHash); got != "" {
			t.Fatalf("got %q, want empty (hash compare is case-insensitive)", got)
		}
	})

	t.Run("undecidable hashes stay silent", func(t *testing.T) {
		for _, rec := range []appRecord{
			{State: stateRunning, ArtifactHash: ""},
			{State: stateRunning, ArtifactHash: runHash},
		} {
			a := &Actor{Records: map[string]appRecord{appID: rec}}
			built := builtHash
			if rec.ArtifactHash == runHash {
				built = "" // built hash unavailable → cannot prove staleness
			}
			if got := a.staleRegistryDiagnostic(appID, built); got != "" {
				t.Fatalf("rec %+v built %q: got %q, want empty", rec, built, got)
			}
		}
	})
}
