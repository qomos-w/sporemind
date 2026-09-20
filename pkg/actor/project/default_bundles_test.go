package project

import (
	"reflect"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestMergeExtraBundleIDs(t *testing.T) {
	cases := []struct {
		name     string
		defaults []string
		extras   []string
		want     []string
	}{
		{"no defaults keeps extras untouched (passthrough)", nil, []string{"b", "b"}, []string{"b", "b"}},
		{"no extras yields deduped defaults", []string{"a", "a"}, nil, []string{"a"}},
		{"both nil yields nil", nil, nil, nil},
		{"defaults first then extras", []string{"a", "b"}, []string{"c"}, []string{"a", "b", "c"}},
		{"dup across sides kept once (defaults win)", []string{"a", "b"}, []string{"b", "d"}, []string{"a", "b", "d"}},
		{"dups within defaults deduped", []string{"a", "a", "b"}, nil, []string{"a", "b"}},
		{"dups within extras deduped when defaults present", []string{"a"}, []string{"x", "x"}, []string{"a", "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mergeExtraBundleIDs(tc.defaults, tc.extras); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("mergeExtraBundleIDs(%v, %v) = %v, want %v", tc.defaults, tc.extras, got, tc.want)
			}
		})
	}
}

// TestDefaultBundlesGetSetRoundTrip locks the persistence contract: set
// replaces the whole list (deduped), get reads it back from the config card.
func TestDefaultBundlesGetSetRoundTrip(t *testing.T) {
	a, ctx := freshProject(t, t.TempDir())

	got, err := a.handleDefaultBundlesGet(ctx, gen.ProjectDefaultBundlesGetReq{})
	if err != nil {
		t.Fatalf("initial get: %v", err)
	}
	if len(got.BundleIDs) != 0 {
		t.Fatalf("initial BundleIDs = %v, want empty", got.BundleIDs)
	}

	set, err := a.handleDefaultBundlesSet(ctx, gen.ProjectDefaultBundlesSetReq{
		BundleIDs: []string{"builtin:bundle:web-search", "app-bundle:demo", "builtin:bundle:web-search"},
	})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	want := []string{"builtin:bundle:web-search", "app-bundle:demo"}
	if !reflect.DeepEqual(set.BundleIDs, want) {
		t.Errorf("set resp = %v, want %v", set.BundleIDs, want)
	}

	got, err = a.handleDefaultBundlesGet(ctx, gen.ProjectDefaultBundlesGetReq{})
	if err != nil {
		t.Fatalf("get after set: %v", err)
	}
	if !reflect.DeepEqual(got.BundleIDs, want) {
		t.Errorf("get after set = %v, want %v", got.BundleIDs, want)
	}

	// Replacement (not append): clearing works.
	if _, err := a.handleDefaultBundlesSet(ctx, gen.ProjectDefaultBundlesSetReq{BundleIDs: []string{}}); err != nil {
		t.Fatalf("clear set: %v", err)
	}
	got, err = a.handleDefaultBundlesGet(ctx, gen.ProjectDefaultBundlesGetReq{})
	if err != nil {
		t.Fatalf("get after clear: %v", err)
	}
	if len(got.BundleIDs) != 0 {
		t.Errorf("get after clear = %v, want empty", got.BundleIDs)
	}
}

// TestHandleSpawnAgent_DefaultBundlesMerge verifies the spawn path tolerates
// configured defaults: with defaults set, spawn still succeeds (the merge into
// the agent props is covered by TestMergeExtraBundleIDs; the opaque
// PropsFromFunc factory cannot be introspected from this package).
func TestHandleSpawnAgent_DefaultBundlesMerge(t *testing.T) {
	a, ctx := freshProject(t, t.TempDir())
	if _, err := a.handleDefaultBundlesSet(ctx, gen.ProjectDefaultBundlesSetReq{
		BundleIDs: []string{"builtin:bundle:web-search"},
	}); err != nil {
		t.Fatalf("set defaults: %v", err)
	}
	agentID := testutil.GenActorID()
	ctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return testutil.NewFakeRef(agentID, nil), nil
	}
	resp, err := a.handleSpawnAgent(ctx, domain.ProjectSpawnAgentReq{
		SpawnName:      "Coder#abcd",
		ProjectID:      "proj-1",
		AgentKind:      "coder",
		ExtraBundleIDs: []string{"builtin:bundle:web-search", "builtin:bundle:file-tools"},
	})
	if err != nil {
		t.Fatalf("handleSpawnAgent: %v", err)
	}
	if resp.ActorID != agentID.String() {
		t.Errorf("ActorID = %q, want %q", resp.ActorID, agentID.String())
	}
}
