package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
	"github.com/qomos-w/sporemind/pkg/timer"
)

func TestParseCron6(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
		sec     string
		min     string
		hour    string
		day     string
		mon     string
		wd      string
	}{
		{"0 9 * * *", false, "0", "0", "9", "*", "*", "?"},
		{"30 8 * * 1", false, "0", "30", "8", "?", "*", "1"},
		{"*/15 * * * *", false, "0", "*/15", "*", "*", "*", "?"},
		{"0 9 15 * *", false, "0", "0", "9", "15", "*", "?"},
		{"0 9", true, "", "", "", "", "", ""},
		{"0 9 * * * *", true, "", "", "", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			s, m, h, d, mon, wd, err := parseCron6(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if s != tt.sec || m != tt.min || h != tt.hour || d != tt.day || mon != tt.mon || wd != tt.wd {
				t.Errorf("got (%s,%s,%s,%s,%s,%s), want (%s,%s,%s,%s,%s,%s)",
					s, m, h, d, mon, wd, tt.sec, tt.min, tt.hour, tt.day, tt.mon, tt.wd)
			}
		})
	}
}

func TestParseNaturalDuration_EveryMinutes(t *testing.T) {
	d, err := parseNaturalDuration("every 30 minutes", nil)
	if err != nil {
		t.Fatal(err)
	}
	if d != 30*time.Minute {
		t.Errorf("got %v, want 30m", d)
	}
}

func TestParseNaturalDuration_EveryHours(t *testing.T) {
	d, err := parseNaturalDuration("every 2 hours", nil)
	if err != nil {
		t.Fatal(err)
	}
	if d != 2*time.Hour {
		t.Errorf("got %v, want 2h", d)
	}
}

func TestParseNaturalDuration_EverySeconds(t *testing.T) {
	d, err := parseNaturalDuration("every 15 seconds", nil)
	if err != nil {
		t.Fatal(err)
	}
	if d != 15*time.Second {
		t.Errorf("got %v, want 15s", d)
	}
}

func TestParseNaturalDuration_AtFuture(t *testing.T) {
	future := time.Now().Add(2 * time.Hour).Format("2006-01-02 15:04:05")
	d, err := parseNaturalDuration("at "+future, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d <= 0 {
		t.Error("expected positive duration for future time")
	}
}

func TestParseNaturalDuration_AtPast(t *testing.T) {
	past := "2020-01-01 00:00:00"
	_, err := parseNaturalDuration("at "+past, nil)
	if err == nil {
		t.Error("expected error for past time")
	}
}

func TestParseNaturalDuration_Unrecognized(t *testing.T) {
	_, err := parseNaturalDuration("blah blah", nil)
	if err == nil {
		t.Error("expected error for unrecognized expression")
	}
}

func TestParseNaturalDuration_EveryDay(t *testing.T) {
	_, err := parseNaturalDuration("every day at 9:00", nil)
	if err == nil {
		t.Error("expected error for unsupported 'every day at' expression (not yet implemented)")
	}
}

func TestScheduledEntryKey(t *testing.T) {
	e := &scheduledEntry{ProjectID: "proj1", CardID: "card1"}
	if e.key() != "proj1:card1" {
		t.Errorf("got %q, want proj1:card1", e.key())
	}
}

func newTestScheduler(t *testing.T) (*Actor, *testutil.FakeCtx) {
	t.Helper()
	tw := timer.NewTimer()
	t.Cleanup(func() { tw.Stop() })

	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.SelfRef = fakeRef{actorID: testutil.GenActorID()}

	a := &Actor{}
	a.actorID = ctx.Self().ID().String()
	a.timeWheel = tw
	a.timeHandler = a.timeWheel.NewHandler()
	a.entries = make(map[string]*scheduledEntry)
	a.actorCtx = ctx
	a.store = persist.NewFSPersist(t.TempDir())
	return a, ctx
}

// minimal ref.Ref implementation so FakeCtx.Self() is non-nil.
type fakeRef struct {
	actorID id.ActorID
}

func (f fakeRef) ID() id.ActorID { return f.actorID }
func (fakeRef) Service() (string, bool) { return "", false }
func (fakeRef) Invoke(context.Context, string, any, ...map[string]string) *invoke.Call {
	return nil
}

func TestHandleList(t *testing.T) {
	a, _ := newTestScheduler(t)

	node := a.timeHandler.Schedule(5*time.Minute, func(timer.TimeNoder) {})
	a.entries["proj:scheduler:foo"] = &scheduledEntry{
		ProjectID: "proj",
		CardID:    "scheduler:foo",
		Schedule:  gen.TimerSchedule{Cron: "0 9 * * *", Enabled: true},
		node:      node,
	}

	resp, err := a.handleList(nil, gen.SchedulerListReq{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(resp.Entries))
	}
	entry := resp.Entries[0]
	if entry.ProjectID != "proj" || entry.CardID != "scheduler:foo" {
		t.Errorf("got project=%q card=%q", entry.ProjectID, entry.CardID)
	}
	if !entry.Schedule.Enabled {
		t.Error("expected Enabled=true")
	}
	if entry.NextFireAt == "" {
		t.Error("expected NextFireAt to be populated")
	}
}

func TestHandleSetEnabled_DisablesAndStops(t *testing.T) {
	a, ctx := newTestScheduler(t)

	node := a.timeHandler.Schedule(5*time.Minute, func(timer.TimeNoder) {})
	a.entries["proj:scheduler:foo"] = &scheduledEntry{
		ProjectID: "proj",
		CardID:    "scheduler:foo",
		Schedule:  gen.TimerSchedule{Cron: "0 9 * * *", Enabled: true},
		node:      node,
	}

	resp, err := a.handleSetEnabled(ctx, gen.SchedulerSetEnabledReq{
		ProjectID: "proj",
		CardID:    "scheduler:foo",
		Enabled:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Enabled != false {
		t.Errorf("got Enabled=%v, want false", resp.Enabled)
	}

	entry := a.entries["proj:scheduler:foo"]
	if entry.node != nil {
		t.Error("expected node to be cleared after disable")
	}
	if got := entry.Schedule.Enabled; got != false {
		t.Errorf("entry.Enabled=%v, want false", got)
	}

	// Re-enable should schedule a new node.
	resp, err = a.handleSetEnabled(ctx, gen.SchedulerSetEnabledReq{
		ProjectID: "proj",
		CardID:    "scheduler:foo",
		Enabled:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Enabled != true {
		t.Errorf("got Enabled=%v, want true", resp.Enabled)
	}
	if a.entries["proj:scheduler:foo"].node == nil {
		t.Error("expected node to be recreated after enable")
	}
}

func TestHandleFire_SkipsDisabled(t *testing.T) {
	a, ctx := newTestScheduler(t)
	afterCalls := 0
	ctx.AfterFn = func(time.Duration, string, any) error {
		afterCalls++
		return nil
	}

	a.entries["proj:scheduler:foo"] = &scheduledEntry{
		ProjectID: "proj",
		CardID:    "scheduler:foo",
		Schedule:  gen.TimerSchedule{Cron: "0 9 * * *", Enabled: false},
	}

	err := a.handleFire(ctx, gen.ProjectExecuteTimerCardReq{ProjectID: "proj", CardID: "scheduler:foo"})
	if err != nil {
		t.Fatal(err)
	}
	if afterCalls != 0 {
		t.Errorf("handleFire dispatched %d executor calls for disabled timer, want 0", afterCalls)
	}
}

// newRestartActor builds an actor that persists into dir under a fixed state
// name, so successive calls simulate app restarts over the same store.
func newRestartActor(t *testing.T, dir string) (*Actor, *testutil.FakeCtx) {
	t.Helper()
	tw := timer.NewTimer()
	t.Cleanup(func() { tw.Stop() })

	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.SelfRef = fakeRef{actorID: testutil.GenActorID()}

	a := &Actor{}
	a.actorID = "sched-restart"
	a.timeWheel = tw
	a.timeHandler = a.timeWheel.NewHandler()
	a.entries = make(map[string]*scheduledEntry)
	a.actorCtx = ctx
	a.store = persist.NewFSPersist(dir)
	return a, ctx
}

func TestPersistenceRoundTrip_EnabledStateSurvivesReload(t *testing.T) {
	dir := t.TempDir()

	// Session 1: register two timers, disable one (set_enabled persists too).
	a1, ctx1 := newRestartActor(t, dir)
	for _, cardID := range []string{"scheduler:on", "scheduler:off"} {
		err := a1.handleRegister(ctx1, gen.SchedulerRegisterReq{
			ProjectID: "p",
			CardID:    cardID,
			Schedule:  gen.TimerSchedule{Cron: "0 9 * * *", Enabled: true},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a1.handleSetEnabled(ctx1, gen.SchedulerSetEnabledReq{
		ProjectID: "p", CardID: "scheduler:off", Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}

	// The persisted file must carry an explicit false — the old shape
	// (bool with omitempty) dropped it, which is the regression this guards.
	raw, err := os.ReadFile(filepath.Join(dir, "sched-restart.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"Enabled":false`) {
		t.Errorf("persisted state omits explicit Enabled=false:\n%s", raw)
	}

	// Session 2: a fresh actor loading the same state.
	a2, _ := newRestartActor(t, dir)
	if err := a2.Load(); err != nil {
		t.Fatal(err)
	}
	if a2.entries["p:scheduler:off"].Schedule.Enabled {
		t.Error("disabled entry was re-enabled after save/load round-trip")
	}
	if !a2.entries["p:scheduler:on"].Schedule.Enabled {
		t.Error("enabled entry lost its enabled state after reload")
	}
}

func TestLoad_LegacyStateWithoutEnabledDefaultsToEnabled(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"entries":[{"projectId":"p","cardId":"scheduler:old","schedule":{"Cron":"0 9 * * *","Timezone":"UTC"}}]}`
	p := filepath.Join(dir, "sched-restart.json")
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}

	a, _ := newRestartActor(t, dir)
	if err := a.Load(); err != nil {
		t.Fatal(err)
	}
	entry, ok := a.entries["p:scheduler:old"]
	if !ok {
		t.Fatal("legacy entry not loaded")
	}
	if !entry.Schedule.Enabled {
		t.Error("legacy entry without Enabled field must default to enabled")
	}
	if entry.Schedule.Cron != "0 9 * * *" || entry.Schedule.Timezone != "UTC" {
		t.Errorf("legacy fields not restored: %+v", entry.Schedule)
	}
}

// ── register validation (#1 schedule audit) ───────────────────────────────────

func TestHandleRegister_InvalidCronReturnsError(t *testing.T) {
	a, ctx := newTestScheduler(t)

	// Must not panic and must not persist a half-registered entry: the old path
	// reached timer.Cron's logger.Panic, killing the actor and diverging memory
	// from the state file.
	err := a.handleRegister(ctx, gen.SchedulerRegisterReq{
		ProjectID: "p",
		CardID:    "scheduler:bad",
		Schedule:  gen.TimerSchedule{Cron: "0 99 * * *", Enabled: true},
	})
	if err == nil {
		t.Fatal("expected error for out-of-range cron")
	}
	if len(a.entries) != 0 {
		t.Errorf("invalid register left %d entries in memory, want 0", len(a.entries))
	}
}

func TestHandleRegister_InvalidExpressionReturnsError(t *testing.T) {
	a, ctx := newTestScheduler(t)

	for _, expr := range []string{"every 0 minutes", "every frog", "blah blah"} {
		err := a.handleRegister(ctx, gen.SchedulerRegisterReq{
			ProjectID: "p",
			CardID:    "scheduler:x",
			Schedule:  gen.TimerSchedule{Expression: expr, Enabled: true},
		})
		if err == nil {
			t.Errorf("expression %q: expected error, got nil", expr)
		}
	}
	if len(a.entries) != 0 {
		t.Errorf("invalid expressions left %d entries in memory, want 0", len(a.entries))
	}
}

func TestHandleRegister_ValidCronWithTimezone(t *testing.T) {
	a, ctx := newTestScheduler(t)
	if err := a.handleRegister(ctx, gen.SchedulerRegisterReq{
		ProjectID: "p",
		CardID:    "scheduler:ok",
		Schedule:  gen.TimerSchedule{Cron: "0 9 * * *", Timezone: "UTC", Enabled: true},
	}); err != nil {
		t.Fatalf("valid cron rejected: %v", err)
	}
	entry := a.entries["p:scheduler:ok"]
	if entry == nil || entry.node == nil {
		t.Fatal("valid cron did not produce a live node")
	}
	if entry.node.Next().IsZero() {
		t.Error("live node has zero next-fire time")
	}
}

// ── set_enabled re-arm (#2 schedule audit) ────────────────────────────────────

func TestHandleSetEnabled_ReenableStopsOldNode(t *testing.T) {
	a, ctx := newTestScheduler(t)
	if err := a.handleRegister(ctx, gen.SchedulerRegisterReq{
		ProjectID: "p", CardID: "scheduler:x",
		Schedule: gen.TimerSchedule{Cron: "0 9 * * *", Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	old := a.entries["p:scheduler:x"].node
	if old == nil {
		t.Fatal("expected a node after register")
	}

	if _, err := a.handleSetEnabled(ctx, gen.SchedulerSetEnabledReq{
		ProjectID: "p", CardID: "scheduler:x", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	newNode := a.entries["p:scheduler:x"].node
	if newNode == nil {
		t.Fatal("expected a node after re-enable")
	}
	if newNode == old {
		t.Fatal("set_enabled(true) reused the old node instead of rebuilding it")
	}
	// The old node must be stopped, otherwise it keeps firing alongside the new
	// one and the project is executed twice.
	if !old.Next().IsZero() {
		t.Error("old node still live after set_enabled(true): would double-fire")
	}
}

// ── one-shot "at" expression (#3 schedule audit) ──────────────────────────────

func TestExpressionTimerOpts_AtIsOneShot(t *testing.T) {
	if opts := expressionTimerOpts("at 2030-01-01 00:00:00"); len(opts) != 1 {
		t.Errorf("at expression wants one option (WithLoop(1)), got %d", len(opts))
	}
	if opts := expressionTimerOpts("every 5 minutes"); len(opts) != 0 {
		t.Errorf("repeating expression wants no options, got %d", len(opts))
	}
}

func TestScheduleEntryLocked_AtExpressionUsesTargetDelay(t *testing.T) {
	a, _ := newTestScheduler(t)
	target := time.Now().Add(30 * time.Minute).Truncate(time.Second)
	entry := &scheduledEntry{
		ProjectID: "p", CardID: "scheduler:at",
		Schedule: gen.TimerSchedule{
			Expression: "at " + target.Format("2006-01-02 15:04:05"),
			Enabled:    true,
		},
	}
	if err := a.scheduleEntryLocked(entry); err != nil {
		t.Fatal(err)
	}
	if entry.node == nil {
		t.Fatal("at expression did not schedule a node")
	}
	got := entry.node.Next()
	if got.IsZero() {
		t.Fatal("at node has zero next-fire time")
	}
	if diff := got.Sub(target); diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("at node next fire %v not near target %v", got, target)
	}
}

// ── timezone resolution (#4 schedule audit) ───────────────────────────────────

func TestResolveLocation(t *testing.T) {
	if loc, err := resolveLocation(""); err != nil || loc != nil {
		t.Errorf("empty timezone should mean default (nil,nil), got %v,%v", loc, err)
	}
	if loc, err := resolveLocation("UTC"); err != nil || loc == nil {
		t.Errorf("UTC should resolve, got %v,%v", loc, err)
	}
	if _, err := resolveLocation("Not/AReal_Zone"); err == nil {
		t.Error("unknown timezone must error, not silently fall back")
	}
}

// ── Enabled semantics (#8 schedule audit) ─────────────────────────────────────

func TestEnabledOrDefault(t *testing.T) {
	tr, fa := true, false
	if !enabledOrDefault(nil) {
		t.Error("nil (legacy state) should default to enabled")
	}
	if !enabledOrDefault(&tr) {
		t.Error("explicit true must be enabled")
	}
	if enabledOrDefault(&fa) {
		t.Error("explicit false must stay disabled")
	}
}

func TestHandleRegister_WireZeroEnabledIsDisabled(t *testing.T) {
	a, ctx := newTestScheduler(t)
	if err := a.handleRegister(ctx, gen.SchedulerRegisterReq{
		ProjectID: "p", CardID: "scheduler:off",
		Schedule: gen.TimerSchedule{Cron: "0 9 * * *"}, // Enabled omitted → Go zero false
	}); err != nil {
		t.Fatal(err)
	}
	entry := a.entries["p:scheduler:off"]
	if entry == nil {
		t.Fatal("entry not registered")
	}
	if entry.Schedule.Enabled {
		t.Error("wire zero-value Enabled must resolve to disabled")
	}
	if entry.node != nil {
		t.Error("disabled entry must not have a live node")
	}
}

func TestParseNaturalDuration_AtUsesLocation(t *testing.T) {
	target := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	d, err := parseNaturalDuration("at "+target.Format("2006-01-02 15:04:05"), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if d < time.Hour || d > 3*time.Hour {
		t.Errorf("at parsed as %v, want ~2h (timestamp must be read in the given location)", d)
	}
}
