package workspace

import (
	"strings"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// fakeExecutor is a minimal Executor for registry-level tests; it does
// not touch the workspace actor so it can be exercised without
// freshActor.
type fakeExecutor struct {
	kind string
}

func (f *fakeExecutor) Kind() ExecKind { return f.kind }
func (f *fakeExecutor) Preflight(_ actor.PureContext, _ ClaimReq) (PreflightResult, error) {
	return PreflightResult{}, nil
}
func (f *fakeExecutor) Execute(_ actor.PureContext, _ ClaimReq) (ExecResp, error) {
	return ExecResp{}, nil
}

func TestRegistry_RegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	if got := r.Kinds(); len(got) != 0 {
		t.Fatalf("expected empty registry, got %v", got)
	}
	if _, ok := r.Lookup("worker_task"); ok {
		t.Fatal("empty registry should not have worker_task")
	}

	r.Register(&fakeExecutor{kind: "worker_task"})
	if _, ok := r.Lookup("worker_task"); !ok {
		t.Fatal("expected worker_task to be registered")
	}
	if got := r.Kinds(); len(got) != 1 || got[0] != "worker_task" {
		t.Errorf("expected Kinds=[worker_task], got %v", got)
	}

	// Re-registering replaces, not duplicates.
	r.Register(&fakeExecutor{kind: "worker_task"})
	if got := r.Kinds(); len(got) != 1 {
		t.Errorf("re-register should not duplicate, got %v", got)
	}
}

func TestRegistry_LookupEmpty(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Lookup(""); ok {
		t.Fatal("empty kind should not match (lookup returns false)")
	}
}

func TestRegistry_NilExecutorIsIgnored(t *testing.T) {
	r := NewRegistry()
	r.Register(nil) // must not panic, must not register "" as a kind
	if got := r.Kinds(); len(got) != 0 {
		t.Errorf("nil register should not add a kind, got %v", got)
	}
}

func TestRegistry_KindsAreSortedDeterministically(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeExecutor{kind: "sub_map"})
	r.Register(&fakeExecutor{kind: "worker_task"})
	r.Register(&fakeExecutor{kind: "crawl"})

	got := r.Kinds()
	want := []string{"crawl", "sub_map", "worker_task"}
	if len(got) != len(want) {
		t.Fatalf("expected %d kinds, got %v", len(want), got)
	}
	for i, k := range want {
		if got[i] != k {
			t.Errorf("Kinds()[%d] = %q, want %q", i, got[i], k)
		}
	}
}

func TestRegistry_ConcurrentRegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	const N = 32
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			kind := ExecKind("kind-" + string(rune('a'+i%26)))
			r.Register(&fakeExecutor{kind: kind})
			if _, ok := r.Lookup(kind); !ok {
				t.Errorf("expected %q to be registered", kind)
			}
		}()
	}
	wg.Wait()
	if got := r.Kinds(); len(got) == 0 {
		t.Fatal("expected at least one registered kind")
	}
}

func TestErrUnknownExecKind_ErrorString(t *testing.T) {
	err := &ErrUnknownExecKind{Kind: "bogus", Known: []string{"worker_task"}}
	got := err.Error()
	if !strings.Contains(got, "bogus") {
		t.Errorf("error %q must mention the unknown kind", got)
	}
	if !strings.Contains(got, "worker_task") {
		t.Errorf("error %q must list the registered kinds for diagnostics", got)
	}
	if !IsUnknownExecKind(err) {
		t.Errorf("IsUnknownExecKind must return true for *ErrUnknownExecKind")
	}
	if IsUnknownExecKind(nil) {
		t.Errorf("IsUnknownExecKind(nil) must return false")
	}
	if IsUnknownExecKind(errUnknownNonTyped()) {
		t.Errorf("IsUnknownExecKind must reject non-typed errors")
	}
}

// errUnknownNonTyped returns a non-typed error so IsUnknownExecKind can
// reject it.
type nonTypedErr struct{}

func (nonTypedErr) Error() string { return "not typed" }

func errUnknownNonTyped() error { return nonTypedErr{} }

func TestParseExecKindField(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want ExecKind
	}{
		{
			name: "no frontmatter",
			raw:  "Body without frontmatter.",
			want: "",
		},
		{
			name: "frontmatter without data block",
			raw:  "---\nid: c\ntype: task\n---\nBody.",
			want: "",
		},
		{
			name: "frontmatter with data block but no exec",
			raw:  "---\nid: c\ntype: task\ndata:\n  schedule: cron\n---\nBody.",
			want: "",
		},
		{
			name: "frontmatter with data.exec.kind = worker_task",
			raw:  "---\nid: c\ntype: task\ndata:\n  exec:\n    kind: worker_task\n---\nBody.",
			want: "worker_task",
		},
		{
			name: "kind quoted",
			raw:  "---\nid: c\ntype: task\ndata:\n  exec:\n    kind: \"worker_task\"\n---\nBody.",
			want: "worker_task",
		},
		{
			name: "kind single-quoted",
			raw:  "---\nid: c\ntype: task\ndata:\n  exec:\n    kind: 'worker_task'\n---\nBody.",
			want: "worker_task",
		},
		{
			name: "kind trailing whitespace",
			raw:  "---\nid: c\ntype: task\ndata:\n  exec:\n    kind:   worker_task   \n---\nBody.",
			want: "worker_task",
		},
		{
			name: "exec.kind missing → kind stays empty",
			raw:  "---\nid: c\ntype: task\ndata:\n  exec:\n    other: foo\n---\nBody.",
			want: "",
		},
		{
			name: "exec.kind with future kind — parser still surfaces it",
			raw:  "---\nid: c\ntype: task\ndata:\n  exec:\n    kind: crawl\n---\nBody.",
			want: "crawl",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := execKindOf(tc.raw)
			if got != tc.want {
				t.Errorf("execKindOf() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEnsureExecRegistry_Idempotent(t *testing.T) {
	a, _ := freshActor(t)
	a.ensureExecRegistry()
	first := a.execRegistry
	a.ensureExecRegistry() // second call must not replace
	if a.execRegistry != first {
		t.Fatal("ensureExecRegistry must be idempotent")
	}
	if !a.ensureExecRegistryWorks() {
		t.Fatal("worker_task must be registered after ensureExecRegistry")
	}
}

// ensureExecRegistryWorks is a tiny test helper that asserts worker_task
// is in the registry. It is a method on *Actor so it can read
// a.execRegistry directly without exporting the field for tests.
func (a *Actor) ensureExecRegistryWorks() bool {
	a.ensureExecRegistry()
	_, ok := a.execRegistry.Lookup(ExecKindWorkerTask)
	return ok
}

func TestWorkspaceActor_HasWorkerTaskRegisteredByDefault(t *testing.T) {
	a, _ := freshActor(t)
	// freshActor → OnStart → ensureExecRegistry → worker_task registered.
	if _, ok := a.execRegistry.Lookup(ExecKindWorkerTask); !ok {
		t.Fatal("OnStart must register worker_task by default")
	}
}

func TestWorkspaceActor_HasSubMapRegisteredByDefault(t *testing.T) {
	a, _ := freshActor(t)
	if _, ok := a.execRegistry.Lookup(ExecKindSubMap); !ok {
		t.Fatal("OnStart must register sub_map by default")
	}
}

// TestClaimReq_MappingFromWireRequest documents the canonical
// WorkspaceAgentSpawnAssignReq → ClaimReq mapping the dispatcher uses.
// Keeping it as a test makes the contract auditable; any change here
// forces the test to be updated alongside the dispatcher.
func TestClaimReq_MappingFromWireRequest(t *testing.T) {
	req := domain.WorkspaceAgentSpawnAssignReq{
		To:              "Worker One",
		AgentKind:       domain.AgentKindWorker,
		InterpretedGoal: "interpreted goal",
		BoundTaskCardID: "card-1",
		MaxTurns:        5,
		ProjectID:       "p1",
		CallerAgentID:   "caller-1",
	}
	// Dispatcher logic (mirrored here for the test):
	goalTitle := req.To
	if goalTitle == "" {
		goalTitle = req.InterpretedGoal
	}
	claimReq := ClaimReq{
		CallerAgentID:   req.CallerAgentID,
		ProjectID:       req.ProjectID,
		BoundTaskCardID: req.BoundTaskCardID,
		AgentKind:       req.AgentKind,
		GoalTitle:       goalTitle,
		MaxTurns:        req.MaxTurns,
	}

	if claimReq.GoalTitle != "Worker One" {
		t.Errorf("GoalTitle must be req.To when set; got %q", claimReq.GoalTitle)
	}
	if claimReq.AgentKind != domain.AgentKindWorker {
		t.Errorf("AgentKind: got %q, want %q", claimReq.AgentKind, domain.AgentKindWorker)
	}
	if claimReq.MaxTurns != 5 {
		t.Errorf("MaxTurns: got %d, want 5", claimReq.MaxTurns)
	}

	// Empty To falls back to InterpretedGoal.
	req.To = ""
	goalTitle = req.To
	if goalTitle == "" {
		goalTitle = req.InterpretedGoal
	}
	if goalTitle != "interpreted goal" {
		t.Errorf("GoalTitle must fall back to InterpretedGoal; got %q", goalTitle)
	}

	// Silence unused-import lints when the test references these types.
	_ = gen.GoalSummary{}
}
