package pluginhost

import (
	"context"
	"errors"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestArtifactLoaderInProcessDeferredSentinels pins the two in-process
// reload blockers as errors.Is-addressable sentinels (S1/A1): a c-shared
// library cannot be swapped while the host lives, so the loader surfaces the
// blocker as a programmatically classifiable error instead of a bare string.
func TestArtifactLoaderInProcessDeferredSentinels(t *testing.T) {
	host := newStubHost()
	loader := NewArtifactLoader(host)
	loader.SetOpener(&stubOpener{})

	a := makeArtifactFile(t, "binary-A")
	b := makeArtifactFile(t, "binary-B")
	if _, err := loader.Load(context.Background(), validLoadReq(a)); err != nil {
		t.Fatalf("load A: %v", err)
	}

	// Same plugin, different artifact already mapped → different-artifact
	// blocker, classifiable via errors.Is.
	_, err := loader.Load(context.Background(), validLoadReq(b))
	if err == nil {
		t.Fatal("expected different-artifact blocker")
	}
	if !errors.Is(err, ErrInProcessDifferentArtifact) {
		t.Fatalf("err = %v, want ErrInProcessDifferentArtifact", err)
	}
	if !IsInProcessDeferredError(err) {
		t.Fatal("IsInProcessDeferredError should classify the different-artifact blocker")
	}

	// In-process unload degrades to unload-pending; a re-load is blocked
	// with the unload-pending sentinel.
	if _, err := loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: "test.native"}); err != nil {
		t.Fatalf("unload: %v", err)
	}
	_, err = loader.Load(context.Background(), validLoadReq(a))
	if err == nil {
		t.Fatal("expected unload-pending blocker")
	}
	if !errors.Is(err, ErrInProcessUnloadPending) {
		t.Fatalf("err = %v, want ErrInProcessUnloadPending", err)
	}
	if !IsInProcessDeferredError(err) {
		t.Fatal("IsInProcessDeferredError should classify the unload-pending blocker")
	}

	// Unrelated errors are not deferred load errors.
	if IsInProcessDeferredError(errors.New("boom")) {
		t.Fatal("unrelated error misclassified")
	}
}

// TestArtifactLoaderSubprocessDifferentArtifactStillErrors pins that the
// same "already loaded with a different artifact" class remains an error for
// subprocess plugins: the classification into restart_pending is the
// decision of the caller (transport-aware), not the loader.
func TestArtifactLoaderSubprocessDifferentArtifactStillErrors(t *testing.T) {
	host := newStubHost()
	loader := NewArtifactLoader(host)
	loader.SetOpener(&stubOpener{})

	a := makeArtifactFile(t, "sub-A")
	b := makeArtifactFile(t, "sub-B")
	if _, err := loader.Load(context.Background(), validSubprocessLoadReq(a)); err != nil {
		t.Fatalf("load A: %v", err)
	}
	_, err := loader.Load(context.Background(), validSubprocessLoadReq(b))
	if err == nil {
		t.Fatal("expected different-artifact error for subprocess")
	}
	if !errors.Is(err, ErrInProcessDifferentArtifact) {
		t.Fatalf("want the sentinel wrapped regardless of transport; got %v", err)
	}
}
