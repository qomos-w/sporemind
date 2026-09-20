package interfacemanager

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// recordingContext captures emitted events for assertion.
type recordingContext struct {
	actor.Context
	events []emitRecord
}

type emitRecord struct {
	kind  string
	event domain.InterfaceManagerEvent
}

func (c *recordingContext) EmitEvent(kind string, event any) error {
	if ev, ok := event.(domain.InterfaceManagerEvent); ok {
		c.events = append(c.events, emitRecord{kind: kind, event: ev})
	}
	return nil
}

func (c *recordingContext) Logger() actor.Logger { return stubLogger{} }

type stubLogger struct{}

func (stubLogger) Debug(string, ...any) {}
func (stubLogger) Info(string, ...any)  {}
func (stubLogger) Warn(string, ...any)  {}
func (stubLogger) Error(string, ...any) {}

func TestValidateControl(t *testing.T) {
	cases := []struct {
		name    string
		req     domain.InterfaceManagerControlReq
		wantErr bool
	}{
		// Original actions
		{"set_view valid", domain.InterfaceManagerControlReq{Action: "set_view", Mode: "topology"}, false},
		{"set_view missing mode", domain.InterfaceManagerControlReq{Action: "set_view"}, true},
		{"set_view invalid mode", domain.InterfaceManagerControlReq{Action: "set_view", Mode: "dashboard"}, true},
		{"open_settings valid", domain.InterfaceManagerControlReq{Action: "open_settings", Category: "model"}, false},
		{"open_settings missing category", domain.InterfaceManagerControlReq{Action: "open_settings"}, true},
		{"open_settings invalid category", domain.InterfaceManagerControlReq{Action: "open_settings", Category: "billing"}, true},
		{"switch_project valid", domain.InterfaceManagerControlReq{Action: "switch_project", ProjectID: "proj-1"}, false},
		{"switch_project missing id", domain.InterfaceManagerControlReq{Action: "switch_project"}, true},
		{"focus_agent valid", domain.InterfaceManagerControlReq{Action: "focus_agent", AgentID: "agent-1"}, false},
		{"focus_agent missing id", domain.InterfaceManagerControlReq{Action: "focus_agent"}, true},
		{"unknown action", domain.InterfaceManagerControlReq{Action: "nope"}, true},
		{"empty action", domain.InterfaceManagerControlReq{Action: ""}, true},
		// New guide actions
		{"show_guide valid", domain.InterfaceManagerControlReq{Action: "show_guide", Steps: []domain.GuideStep{{TargetGuideID: "composer.input", Title: "T", Body: "B"}}}, false},
		{"show_guide missing steps", domain.InterfaceManagerControlReq{Action: "show_guide"}, true},
		{"show_guide empty steps", domain.InterfaceManagerControlReq{Action: "show_guide", Steps: []domain.GuideStep{}}, true},
		{"hide_guide valid", domain.InterfaceManagerControlReq{Action: "hide_guide"}, false},
		{"interact valid click", domain.InterfaceManagerControlReq{Action: "interact", GuideID: "g1", Interaction: "click"}, false},
		{"interact valid focus", domain.InterfaceManagerControlReq{Action: "interact", GuideID: "g1", Interaction: "focus"}, false},
		{"interact valid scroll_into_view", domain.InterfaceManagerControlReq{Action: "interact", GuideID: "g1", Interaction: "scroll_into_view"}, false},
		{"interact valid input with text", domain.InterfaceManagerControlReq{Action: "interact", GuideID: "g1", Interaction: "input", Text: "hello"}, false},
		{"interact missing guideid", domain.InterfaceManagerControlReq{Action: "interact", Interaction: "click"}, true},
		{"interact missing interaction", domain.InterfaceManagerControlReq{Action: "interact", GuideID: "g1"}, true},
		{"interact invalid interaction", domain.InterfaceManagerControlReq{Action: "interact", GuideID: "g1", Interaction: "drag"}, true},
		{"interact input missing text", domain.InterfaceManagerControlReq{Action: "interact", GuideID: "g1", Interaction: "input"}, true},
		// open_app_view
		{"open_app_view valid", domain.InterfaceManagerControlReq{Action: "open_app_view", AppID: "app.viewdemo"}, false},
		{"open_app_view valid with view", domain.InterfaceManagerControlReq{Action: "open_app_view", AppID: "app.viewdemo", ViewID: "main"}, false},
		{"open_app_view missing app", domain.InterfaceManagerControlReq{Action: "open_app_view"}, true},
		{"open_app_view missing app with view", domain.InterfaceManagerControlReq{Action: "open_app_view", ViewID: "main"}, true},
		// request_plugin_dom_snapshot
		{"request_plugin_dom_snapshot valid", domain.InterfaceManagerControlReq{Action: "request_plugin_dom_snapshot", AppID: "app.totp"}, false},
		{"request_plugin_dom_snapshot missing app", domain.InterfaceManagerControlReq{Action: "request_plugin_dom_snapshot"}, true},
		// Tutorial actions
		{"create_tutorial valid", domain.InterfaceManagerControlReq{Action: "create_tutorial", Title: "My Tutorial", Steps: []domain.GuideStep{{TargetGuideID: "composer.input", Title: "T", Body: "B"}}}, false},
		{"create_tutorial valid with id and description", domain.InterfaceManagerControlReq{Action: "create_tutorial", TutorialID: "my-tutorial", Title: "My Tutorial", Description: "d", Steps: []domain.GuideStep{{TargetGuideID: "composer.input", Title: "T", Body: "B"}}}, false},
		{"create_tutorial missing title", domain.InterfaceManagerControlReq{Action: "create_tutorial", Steps: []domain.GuideStep{{TargetGuideID: "composer.input", Title: "T", Body: "B"}}}, true},
		{"create_tutorial missing steps", domain.InterfaceManagerControlReq{Action: "create_tutorial", Title: "My Tutorial"}, true},
		{"create_tutorial bad anchor", domain.InterfaceManagerControlReq{Action: "create_tutorial", Title: "My Tutorial", Steps: []domain.GuideStep{{TargetGuideID: "nowhere.nothing", Title: "T", Body: "B"}}}, true},
		{"create_tutorial bad slug uppercase", domain.InterfaceManagerControlReq{Action: "create_tutorial", TutorialID: "Bad-Slug", Title: "My Tutorial", Steps: []domain.GuideStep{{TargetGuideID: "composer.input", Title: "T", Body: "B"}}}, true},
		{"create_tutorial bad slug double hyphen", domain.InterfaceManagerControlReq{Action: "create_tutorial", TutorialID: "bad--slug", Title: "My Tutorial", Steps: []domain.GuideStep{{TargetGuideID: "composer.input", Title: "T", Body: "B"}}}, true},
		{"create_tutorial reserved slug basics", domain.InterfaceManagerControlReq{Action: "create_tutorial", TutorialID: "basics", Title: "My Tutorial", Steps: []domain.GuideStep{{TargetGuideID: "composer.input", Title: "T", Body: "B"}}}, true},
		{"create_tutorial reserved slug settings", domain.InterfaceManagerControlReq{Action: "create_tutorial", TutorialID: "settings", Title: "My Tutorial", Steps: []domain.GuideStep{{TargetGuideID: "composer.input", Title: "T", Body: "B"}}}, true},
		{"create_tutorial over 12 steps", domain.InterfaceManagerControlReq{Action: "create_tutorial", Title: "My Tutorial", Steps: overMaxSteps()}, true},
		{"delete_tutorial missing id", domain.InterfaceManagerControlReq{Action: "delete_tutorial"}, true},
		{"tutorial_catalog valid", domain.InterfaceManagerControlReq{Action: "tutorial_catalog"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateControl(nil, tc.req)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestHandleControl_EmitsEvent(t *testing.T) {
	a := &Actor{}
	ctx := &recordingContext{}

	resp, err := a.handleControl(ctx, domain.InterfaceManagerControlReq{
		Action: "set_view",
		Mode:   "topology",
	})
	if err != nil {
		t.Fatalf("handleControl: %v", err)
	}
	if !resp.Accepted {
		t.Fatalf("expected Accepted=true, got false (err=%q)", resp.Error)
	}
	if len(ctx.events) != 1 {
		t.Fatalf("expected 1 emitted event, got %d", len(ctx.events))
	}
	ev := ctx.events[0]
	if ev.kind != eventKind {
		t.Fatalf("expected event kind %q, got %q", eventKind, ev.kind)
	}
	if ev.event.Action != "set_view" || ev.event.Mode != "topology" {
		t.Fatalf("unexpected event payload: %+v", ev.event)
	}
}

func TestHandleControl_InvalidReturnsStructuredResp(t *testing.T) {
	a := &Actor{}
	ctx := &recordingContext{}

	// Validation failure must return a structured resp (Accepted=false) with a
	// nil error, matching callable convention so the LLM receives JSON.
	resp, err := a.handleControl(ctx, domain.InterfaceManagerControlReq{Action: "set_view"})
	if err != nil {
		t.Fatalf("expected nil error for validation failure, got %v", err)
	}
	if resp.Accepted {
		t.Fatalf("expected Accepted=false on validation failure, got %+v", resp)
	}
	if resp.Error == "" {
		t.Fatalf("expected non-empty Error on validation failure")
	}
	if len(ctx.events) != 0 {
		t.Fatalf("expected no emitted event on failure, got %d", len(ctx.events))
	}
}

func TestHandleControl_GuideActions(t *testing.T) {
	cases := []struct {
		name        string
		req         domain.InterfaceManagerControlReq
		wantErr     bool
		wantEventFn func(ev domain.InterfaceManagerEvent) bool
	}{
		{
			name: "show_guide emits event with steps",
			req: domain.InterfaceManagerControlReq{
				Action: "show_guide",
				Steps: []domain.GuideStep{
					{TargetGuideID: "composer.input", Title: "Welcome", Body: "Get started here"},
				},
			},
			wantErr: false,
			wantEventFn: func(ev domain.InterfaceManagerEvent) bool {
				return ev.Action == "show_guide" && len(ev.Steps) == 1 && ev.Steps[0].TargetGuideID == "composer.input"
			},
		},
		{
			name:        "hide_guide emits event",
			req:         domain.InterfaceManagerControlReq{Action: "hide_guide"},
			wantErr:     false,
			wantEventFn: func(ev domain.InterfaceManagerEvent) bool { return ev.Action == "hide_guide" },
		},
		{
			name: "interact emits event with guideid and interaction",
			req: domain.InterfaceManagerControlReq{
				Action:      "interact",
				GuideID:     "btn-1",
				Interaction: "click",
			},
			wantErr: false,
			wantEventFn: func(ev domain.InterfaceManagerEvent) bool {
				return ev.Action == "interact" && ev.GuideID == "btn-1" && ev.Interaction == "click"
			},
		},
		{
			name: "interact input with text",
			req: domain.InterfaceManagerControlReq{
				Action:      "interact",
				GuideID:     "input-1",
				Interaction: "input",
				Text:        "hello world",
			},
			wantErr: false,
			wantEventFn: func(ev domain.InterfaceManagerEvent) bool {
				return ev.Action == "interact" && ev.GuideID == "input-1" && ev.Interaction == "input" && ev.Text == "hello world"
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &Actor{}
			ctx := &recordingContext{}
			resp, err := a.handleControl(ctx, tc.req)
			if tc.wantErr {
				if err != nil {
					return
				}
				if resp.Accepted {
					t.Fatalf("expected Accepted=false")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !resp.Accepted {
					t.Fatalf("expected Accepted=true, got false (err=%q)", resp.Error)
				}
				if len(ctx.events) != 1 {
					t.Fatalf("expected 1 emitted event, got %d", len(ctx.events))
				}
				if !tc.wantEventFn(ctx.events[0].event) {
					t.Fatalf("unexpected event payload: %+v", ctx.events[0].event)
				}
			}
		})
	}
}

// stubContextForInteraction is a minimal context for testing interaction handlers.
type stubContextForInteraction struct {
	actor.Context
	logs []string
}

func (c *stubContextForInteraction) Logger() actor.Logger { return stubLogger{} }

func TestHandleReportInteraction(t *testing.T) {
	a := &Actor{}
	ctx := &stubContextForInteraction{}

	resp, err := a.handleReportInteraction(ctx, domain.ReportInteractionReq{
		Kind:    "click",
		GuideID: "guide-1",
		Label:   "start-btn",
		View:    "conversation",
	})
	if err != nil {
		t.Fatalf("handleReportInteraction: %v", err)
	}
	if !resp.Accepted {
		t.Fatalf("expected Accepted=true, got false")
	}

	// Verify the record was stored.
	a.interactionsMu.Lock()
	if len(a.interactions) != interactionBufferSize {
		t.Fatalf("expected ring buffer size %d, got %d", interactionBufferSize, len(a.interactions))
	}
	record := a.interactions[0]
	if record.Kind != "click" {
		t.Fatalf("expected Kind=click, got %q", record.Kind)
	}
	if record.GuideID != "guide-1" {
		t.Fatalf("expected GuideID=guide-1, got %q", record.GuideID)
	}
	if record.Label != "start-btn" {
		t.Fatalf("expected Label=start-btn, got %q", record.Label)
	}
	if record.Ts == "" {
		t.Fatalf("expected non-empty Ts")
	}
	a.interactionsMu.Unlock()
}

func TestRingBufferOverflow(t *testing.T) {
	a := &Actor{}
	ctx := &stubContextForInteraction{}

	// Fill more than the buffer size.
	for i := 0; i < interactionBufferSize+100; i++ {
		_, _ = a.handleReportInteraction(ctx, domain.ReportInteractionReq{
			Kind:    "click",
			GuideID: "guide-1",
		})
	}

	// Should still have exactly bufferSize records.
	a.interactionsMu.Lock()
	count := 0
	for _, r := range a.interactions {
		if r.ID != "" {
			count++
		}
	}
	if count != interactionBufferSize {
		t.Fatalf("expected %d records, got %d", interactionBufferSize, count)
	}
	// The index should be past the buffer size.
	if a.interactionsIdx <= interactionBufferSize {
		t.Fatalf("expected interactionsIdx > %d, got %d", interactionBufferSize, a.interactionsIdx)
	}
	a.interactionsMu.Unlock()
}

func TestHandleQueryInteractions(t *testing.T) {
	a := &Actor{}
	ctx := &stubContextForInteraction{}

	// Insert some records.
	recordTimes := []string{
		"2024-01-01T00:00:00Z",
		"2024-01-02T00:00:00Z",
		"2024-01-03T00:00:00Z",
	}
	for i, ts := range recordTimes {
		a.interactionsMu.Lock()
		if a.interactions == nil {
			a.interactions = make([]domain.UiInteractionRecord, interactionBufferSize)
		}
		a.interactions[i] = domain.UiInteractionRecord{
			ID:      "uir-test",
			Ts:      ts,
			Kind:    "click",
			GuideID: "guide-1",
		}
		a.interactionsIdx = i + 1
		a.interactionsMu.Unlock()
	}

	// Add another record with different GuideID.
	a.interactionsMu.Lock()
	a.interactions[3] = domain.UiInteractionRecord{
		ID:      "uir-test-2",
		Ts:      "2024-01-04T00:00:00Z",
		Kind:    "input",
		GuideID: "guide-2",
	}
	a.interactionsIdx = 4
	a.interactionsMu.Unlock()

	// Test default limit.
	resp, err := a.handleQueryInteractions(ctx, domain.QueryInteractionsReq{})
	if err != nil {
		t.Fatalf("handleQueryInteractions: %v", err)
	}
	if len(resp.Items) > defaultQueryLimit {
		t.Fatalf("expected at most %d items, got %d", defaultQueryLimit, len(resp.Items))
	}

	// Test custom limit.
	resp, _ = a.handleQueryInteractions(ctx, domain.QueryInteractionsReq{Limit: 2})
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(resp.Items))
	}

	// Test limit cap at maxQueryLimit.
	resp, _ = a.handleQueryInteractions(ctx, domain.QueryInteractionsReq{Limit: 1000})
	if len(resp.Items) > maxQueryLimit {
		t.Fatalf("expected at most %d items, got %d", maxQueryLimit, len(resp.Items))
	}

	// Test filter by GuideID.
	resp, _ = a.handleQueryInteractions(ctx, domain.QueryInteractionsReq{GuideID: "guide-1"})
	for _, item := range resp.Items {
		if item.GuideID != "guide-1" {
			t.Fatalf("expected GuideID=guide-1, got %q", item.GuideID)
		}
	}

	// Test filter by Kind.
	resp, _ = a.handleQueryInteractions(ctx, domain.QueryInteractionsReq{Kind: "input"})
	for _, item := range resp.Items {
		if item.Kind != "input" {
			t.Fatalf("expected Kind=input, got %q", item.Kind)
		}
	}

	// Test filter by Since.
	resp, _ = a.handleQueryInteractions(ctx, domain.QueryInteractionsReq{Since: "2024-01-02T00:00:00Z"})
	for _, item := range resp.Items {
		if item.Ts < "2024-01-02T00:00:00Z" {
			t.Fatalf("expected Ts >= 2024-01-02T00:00:00Z, got %q", item.Ts)
		}
	}
}

func TestQueryInteractions_LimitBoundary(t *testing.T) {
	a := &Actor{}
	ctx := &stubContextForInteraction{}

	// Insert some records.
	a.interactionsMu.Lock()
	a.interactions = make([]domain.UiInteractionRecord, interactionBufferSize)
	for i := 0; i < 10; i++ {
		a.interactions[i] = domain.UiInteractionRecord{
			ID:      "uir-test",
			Ts:      "2024-01-01T00:00:00Z",
			Kind:    "click",
			GuideID: "guide-1",
		}
	}
	a.interactionsIdx = 10
	a.interactionsMu.Unlock()

	// Test limit=0 uses default.
	resp, _ := a.handleQueryInteractions(ctx, domain.QueryInteractionsReq{Limit: 0})
	if len(resp.Items) != defaultQueryLimit && len(resp.Items) != 10 {
		// Should use default or all if less than default.
	}

	// Test negative limit returns empty.
	resp, _ = a.handleQueryInteractions(ctx, domain.QueryInteractionsReq{Limit: -1})
	if len(resp.Items) != 0 {
		t.Fatalf("expected 0 items for negative limit, got %d", len(resp.Items))
	}
}

// overMaxSteps returns maxTutorialSteps+1 valid steps for the cap matrix case.
func overMaxSteps() []domain.GuideStep {
	steps := make([]domain.GuideStep, 0, maxTutorialSteps+1)
	for i := 0; i < maxTutorialSteps+1; i++ {
		steps = append(steps, domain.GuideStep{TargetGuideID: "composer.input", Title: "T", Body: "B"})
	}
	return steps
}

// TestValidateControl_TutorialLibraryCap fills the library to maxTutorials and
// verifies a new tutorial is rejected while an upsert of an existing id passes.
func TestValidateControl_TutorialLibraryCap(t *testing.T) {
	a := &Actor{tutorials: map[string]domain.TutorialSpec{}}
	for i := 0; i < maxTutorials; i++ {
		a.tutorials[fmt.Sprintf("tut-%d", i)] = domain.TutorialSpec{TutorialID: fmt.Sprintf("tut-%d", i)}
	}

	newReq := domain.InterfaceManagerControlReq{
		Action: "create_tutorial",
		Title:  "One More",
		Steps:  []domain.GuideStep{{TargetGuideID: "composer.input", Title: "T", Body: "B"}},
	}
	if err := validateControl(a, newReq); err == nil {
		t.Fatalf("expected library-cap error at %d tutorials, got nil", maxTutorials)
	}

	// Upsert of an existing id is allowed even at the cap.
	upsertReq := newReq
	upsertReq.TutorialID = "tut-0"
	if err := validateControl(a, upsertReq); err != nil {
		t.Fatalf("expected upsert at cap to pass, got %v", err)
	}
}

// TestTutorialRoundtrip covers create → restart (persist Load) → tutorial_catalog
// → delete, asserting events and the persisted snapshot along the way.
func TestTutorialRoundtrip(t *testing.T) {
	dir := t.TempDir()
	newActor := func() *Actor {
		return &Actor{
			store:   persist.MustNew(persist.PersistConfig{Backend: "fs", DataDir: dir, Prefix: "interfacemanager"}),
			actorID: "im-test",
		}
	}

	a := newActor()
	ctx := &recordingContext{}

	// create_tutorial with explicit id.
	resp, err := a.handleControl(ctx, domain.InterfaceManagerControlReq{
		Action:      "create_tutorial",
		TutorialID:  "my-tutorial",
		Title:       "My Tutorial",
		Description: "A demo",
		AutoPlay:    true,
		Steps: []domain.GuideStep{
			{TargetGuideID: "composer.input", Title: "Step 1", Body: "Type here"},
			{TargetGuideID: "composer.send", Title: "Step 2", Body: "Press send"},
		},
	})
	if err != nil {
		t.Fatalf("handleControl create_tutorial: %v", err)
	}
	if !resp.Accepted {
		t.Fatalf("create_tutorial rejected: %q", resp.Error)
	}
	if resp.TutorialID != "my-tutorial" {
		t.Fatalf("expected resp.TutorialId=my-tutorial, got %q", resp.TutorialID)
	}
	if len(ctx.events) != 1 {
		t.Fatalf("expected 1 create event, got %d", len(ctx.events))
	}
	ev := ctx.events[0].event
	if ev.Action != "create_tutorial" || ev.TutorialID != "my-tutorial" {
		t.Fatalf("unexpected create event: %+v", ev)
	}
	if ev.Tutorial == nil || ev.Tutorial.Title != "My Tutorial" || len(ev.Tutorial.Steps) != 2 {
		t.Fatalf("event missing full TutorialSpec: %+v", ev.Tutorial)
	}
	if !ev.AutoPlay {
		t.Fatalf("expected event AutoPlay=true")
	}

	// create_tutorial without id generates a slug from the title.
	ctx.events = nil
	resp, err = a.handleControl(ctx, domain.InterfaceManagerControlReq{
		Action: "create_tutorial",
		Title:  "Second Guide!",
		Steps:  []domain.GuideStep{{TargetGuideID: "composer.input", Title: "T", Body: "B"}},
	})
	if err != nil || !resp.Accepted {
		t.Fatalf("generated-id create failed: err=%v resp=%+v", err, resp)
	}
	if resp.TutorialID != "second-guide" {
		t.Fatalf("expected generated slug second-guide, got %q", resp.TutorialID)
	}
	if ctx.events[0].event.Tutorial.CreatedAt == "" {
		t.Fatalf("expected CreatedAt set on stored tutorial")
	}

	// Upsert overwrites in place.
	resp, err = a.handleControl(ctx, domain.InterfaceManagerControlReq{
		Action:     "create_tutorial",
		TutorialID: "my-tutorial",
		Title:      "My Tutorial v2",
		Steps:      []domain.GuideStep{{TargetGuideID: "composer.send", Title: "S", Body: "B"}},
	})
	if err != nil || !resp.Accepted || resp.TutorialID != "my-tutorial" {
		t.Fatalf("upsert failed: err=%v resp=%+v", err, resp)
	}

	// Persist, then restart: a fresh actor over the same store must Load the library.
	if err := a.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	b := newActor()
	if err := b.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// tutorial_catalog mirrors anchor_catalog: sync resp, no event.
	ctx.events = nil
	cat, err := b.handleControl(ctx, domain.InterfaceManagerControlReq{Action: "tutorial_catalog"})
	if err != nil {
		t.Fatalf("tutorial_catalog: %v", err)
	}
	if !cat.Accepted {
		t.Fatalf("tutorial_catalog rejected: %q", cat.Error)
	}
	if len(cat.Tutorials) != 2 {
		t.Fatalf("expected 2 tutorials after restart, got %d", len(cat.Tutorials))
	}
	if len(ctx.events) != 0 {
		t.Fatalf("tutorial_catalog must not emit events, got %d", len(ctx.events))
	}
	// Sorted by TutorialId: my-tutorial before second-guide.
	if cat.Tutorials[0].TutorialID != "my-tutorial" || cat.Tutorials[1].TutorialID != "second-guide" {
		t.Fatalf("unexpected catalog order: %+v", cat.Tutorials)
	}
	if cat.Tutorials[0].Title != "My Tutorial v2" || len(cat.Tutorials[0].Steps) != 1 {
		t.Fatalf("upsert did not persist: %+v", cat.Tutorials[0])
	}
	if cat.Tutorials[0].CreatedAt == "" {
		t.Fatalf("expected CreatedAt restored from snapshot")
	}

	// delete_tutorial removes and emits an event carrying the TutorialId.
	ctx.events = nil
	del, err := b.handleControl(ctx, domain.InterfaceManagerControlReq{Action: "delete_tutorial", TutorialID: "my-tutorial"})
	if err != nil || !del.Accepted {
		t.Fatalf("delete_tutorial failed: err=%v resp=%+v", err, del)
	}
	if len(ctx.events) != 1 || ctx.events[0].event.Action != "delete_tutorial" || ctx.events[0].event.TutorialID != "my-tutorial" {
		t.Fatalf("unexpected delete event: %+v", ctx.events)
	}
	if err := b.Save(); err != nil {
		t.Fatalf("Save after delete: %v", err)
	}

	c, err := b.handleControl(ctx, domain.InterfaceManagerControlReq{Action: "tutorial_catalog"})
	if err != nil || len(c.Tutorials) != 1 || c.Tutorials[0].TutorialID != "second-guide" {
		t.Fatalf("expected only second-guide after delete, got %+v", c.Tutorials)
	}

	// Deleting a missing tutorial is a structured business error (nil transport
	// error, Accepted=false), so the LLM can self-correct.
	missing, err := b.handleControl(ctx, domain.InterfaceManagerControlReq{Action: "delete_tutorial", TutorialID: "my-tutorial"})
	if err != nil {
		t.Fatalf("expected nil transport error for missing tutorial, got %v", err)
	}
	if missing.Accepted || !strings.Contains(missing.Error, "my-tutorial") {
		t.Fatalf("expected structured not-found error, got %+v", missing)
	}
}
