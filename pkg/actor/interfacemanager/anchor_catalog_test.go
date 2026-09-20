package interfacemanager

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// guideIDsFromSource extracts every string literal in the GuideIds block of
// web/src/ui/ai/guide-ids.ts. Anything outside that block (e.g. the
// tooltipRegistry below it) is ignored.
func guideIDsFromSource(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("../../../web/src/ui/ai/guide-ids.ts")
	if err != nil {
		t.Fatalf("read guide-ids.ts: %v", err)
	}
	src := string(raw)
	start := strings.Index(src, "export const GuideIds")
	if start < 0 {
		t.Fatalf("GuideIds block not found in guide-ids.ts")
	}
	end := strings.Index(src[start:], "} as const")
	if end < 0 {
		t.Fatalf("GuideIds block end not found in guide-ids.ts")
	}
	block := src[start : start+end]

	re := regexp.MustCompile(`'([^']+)'`)
	var ids []string
	for _, m := range re.FindAllStringSubmatch(block, -1) {
		ids = append(ids, m[1])
	}
	return ids
}

// TestGuideAnchorCatalog_NoDriftAgainstGuideIDsTS pins the Go catalog to the
// frontend guide-id source of truth: both directions of the set difference
// must be empty, so any frontend anchor addition/removal fails CI until the
// Go catalog is updated.
func TestGuideAnchorCatalog_NoDriftAgainstGuideIDsTS(t *testing.T) {
	tsIDs := guideIDsFromSource(t)
	goIDs := make(map[string]struct{}, len(tsIDs))
	for _, a := range GuideAnchorCatalog() {
		goIDs[a.GuideID] = struct{}{}
	}

	tsSet := make(map[string]struct{}, len(tsIDs))
	for _, id := range tsIDs {
		if _, dup := tsSet[id]; dup {
			t.Fatalf("guide-ids.ts contains duplicate guide id %q", id)
		}
		tsSet[id] = struct{}{}
	}

	var missingInGo, missingInTS []string
	for id := range tsSet {
		if _, ok := goIDs[id]; !ok {
			missingInGo = append(missingInGo, id)
		}
	}
	for id := range goIDs {
		if _, ok := tsSet[id]; !ok {
			missingInTS = append(missingInTS, id)
		}
	}
	if len(missingInGo) > 0 || len(missingInTS) > 0 {
		t.Fatalf("anchor catalog drift: in guide-ids.ts but not Go: %v; in Go but not guide-ids.ts: %v",
			missingInGo, missingInTS)
	}
}

// TestGuideAnchorCatalog_FieldsNonEmpty requires every catalog entry to carry
// a non-empty GuideId, Area, Summary, VisibleWhen and SuggestedGates so the
// anchor_catalog response is always actionable for the agent.
func TestGuideAnchorCatalog_FieldsNonEmpty(t *testing.T) {
	catalog := GuideAnchorCatalog()
	if len(catalog) != 22 {
		t.Fatalf("catalog has %d anchors, want 22", len(catalog))
	}
	for _, a := range catalog {
		if a.GuideID == "" || a.Area == "" || a.Summary == "" || a.VisibleWhen == "" || a.SuggestedGates == "" {
			t.Fatalf("catalog entry %+v has empty fields", a)
		}
	}
}

// TestHandleControl_AnchorCatalog verifies the anchor_catalog action returns
// the full catalog in Resp.Anchors and emits the usual event.
func TestHandleControl_AnchorCatalog(t *testing.T) {
	a := &Actor{}
	ctx := &recordingContext{}

	resp, err := a.handleControl(ctx, domain.InterfaceManagerControlReq{Action: "anchor_catalog"})
	if err != nil {
		t.Fatalf("handleControl: %v", err)
	}
	if !resp.Accepted {
		t.Fatalf("expected Accepted=true, got Error=%q", resp.Error)
	}
	if len(resp.Anchors) != 22 {
		t.Fatalf("anchor_catalog returned %d anchors, want 22", len(resp.Anchors))
	}
	if len(ctx.events) != 1 || ctx.events[0].event.Action != "anchor_catalog" {
		t.Fatalf("expected one anchor_catalog event, got %+v", ctx.events)
	}
}

// TestValidateControl_ShowGuideHardened covers the show_guide step validation:
// valid steps pass, and each failure mode reports the step index and a hint.
func TestValidateControl_ShowGuideHardened(t *testing.T) {
	valid := domain.InterfaceManagerControlReq{
		Action: "show_guide",
		Steps: []domain.GuideStep{
			{
				TargetGuideID:       "composer.input",
				Title:               "Type here",
				Body:                "Type your message in the composer.",
				Placement:           "top",
				ExpectedInteraction: "text:hello, submit",
			},
			{
				TargetGuideID:       "composer.send",
				Title:               "Send",
				Body:                "Press send to submit.",
				ExpectedInteraction: "click:composer.send",
			},
		},
	}
	if err := validateControl(nil, valid); err != nil {
		t.Fatalf("valid show_guide rejected: %v", err)
	}

	cases := []struct {
		name    string
		steps   []domain.GuideStep
		wantSub []string
	}{
		{
			name: "unknown anchor",
			steps: []domain.GuideStep{
				{TargetGuideID: "nowhere.nothing", Title: "T", Body: "B"},
			},
			wantSub: []string{"step 0", "nowhere.nothing", "anchor_catalog"},
		},
		{
			name: "bad placement on second step",
			steps: []domain.GuideStep{
				{TargetGuideID: "composer.input", Title: "T", Body: "B"},
				{TargetGuideID: "composer.send", Title: "T", Body: "B", Placement: "diagonal"},
			},
			wantSub: []string{"step 1", "diagonal", "top, bottom, left, right, auto"},
		},
		{
			name: "bad expected interaction",
			steps: []domain.GuideStep{
				{TargetGuideID: "composer.input", Title: "T", Body: "B", ExpectedInteraction: "hover:composer.input"},
			},
			wantSub: []string{"step 0", "hover:composer.input", "click:<guide-id>"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateControl(nil, domain.InterfaceManagerControlReq{Action: "show_guide", Steps: tc.steps})
			if err == nil {
				t.Fatalf("expected validation error, got nil")
			}
			for _, sub := range tc.wantSub {
				if !strings.Contains(err.Error(), sub) {
					t.Fatalf("error %q missing %q", err.Error(), sub)
				}
			}
		})
	}
}

// TestReportInteraction_GuideProgress ensures guide_progress records (fed back
// by external tutorials) are accepted into the ring buffer and queryable by
// kind.
func TestReportInteraction_GuideProgress(t *testing.T) {
	a := &Actor{}
	ctx := &recordingContext{}

	resp, err := a.handleReportInteraction(ctx, domain.ReportInteractionReq{
		Kind:    "guide_progress",
		GuideID: "composer.input",
		Detail:  "step 2/5 done",
	})
	if err != nil {
		t.Fatalf("handleReportInteraction: %v", err)
	}
	if !resp.Accepted {
		t.Fatalf("expected Accepted=true")
	}

	qresp, err := a.handleQueryInteractions(ctx, domain.QueryInteractionsReq{Kind: "guide_progress"})
	if err != nil {
		t.Fatalf("handleQueryInteractions: %v", err)
	}
	if len(qresp.Items) != 1 {
		t.Fatalf("expected 1 guide_progress record, got %d", len(qresp.Items))
	}
	rec := qresp.Items[0]
	if rec.Kind != "guide_progress" || rec.GuideID != "composer.input" || rec.Detail != "step 2/5 done" {
		t.Fatalf("unexpected record: %+v", rec)
	}
}
