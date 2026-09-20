package project

import (
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
)

var foldNow = time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)

func foldCard(id string, over func(*domain.MonoCardListItem)) domain.MonoCardListItem {
	c := domain.MonoCardListItem{
		ID:         id,
		Type:       "wiki",
		Source:     "project",
		Storage:    "cardstore",
		Visibility: "wiki",
		Tags:       []string{},
		List:       []string{},
		Protected:  false,
		Editable:   true,
		Deletable:  true,
		Modified:   "2026-08-14T12:00:00Z",
	}
	if over != nil {
		over(&c)
	}
	return c
}

func foldMap(id string, include []string, owner string, status string, modified string) domain.MonoCardListItem {
	data := map[string]any{}
	if len(include) > 0 || owner != "" {
		if len(include) > 0 {
			data["scope"] = map[string]any{"include": toAnySlice(include)}
		}
		if owner != "" {
			data["ownerAgentId"] = owner
		}
	}
	return foldCard(id, func(c *domain.MonoCardListItem) {
		c.Type = "workflow"
		c.Status = status
		c.Modified = modified
		c.Data = data
	})
}

func foldTask(id string, parent string, tags []string) domain.MonoCardListItem {
	return foldCard(id, func(c *domain.MonoCardListItem) {
		c.Type = "task"
		c.Parent = parent
		c.Tags = tags
	})
}

func foldTaskOld(id string, parent string, modified string) domain.MonoCardListItem {
	return foldCard(id, func(c *domain.MonoCardListItem) {
		c.Type = "task"
		c.Parent = parent
		c.Modified = modified
	})
}

func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func idsOf(items []domain.MonoCardListItem) []string {
	out := make([]string, len(items))
	for i, c := range items {
		out[i] = c.ID
	}
	return out
}

func TestClassifyWorkflowCategory(t *testing.T) {
	cases := []struct {
		name     string
		card     domain.MonoCardListItem
		agentIDs map[string]bool
		extra    []domain.MonoCardListItem
		want     epicCategory
	}{
		{"standalone is template", foldCard("tpl", func(c *domain.MonoCardListItem) { c.Type = "workflow"; c.Standalone = true }), nil, nil, catTemplate},
		{"data.template is template (not standalone)", foldCard("tpl::m1", func(c *domain.MonoCardListItem) { c.Type = "workflow"; c.Data = map[string]any{"template": true} }), nil, nil, catTemplate},
		{"scheduler is planned", foldCard("sched", func(c *domain.MonoCardListItem) { c.Type = "scheduler" }), nil, nil, catPlanned},
		{"doing with live owner is in-progress", foldMap("m1", nil, "agent-1", "doing", "2026-08-14T12:00:00Z"), map[string]bool{"agent-1": true}, nil, catInProgress},
		{"todo with live owner is in-progress (TS parity)", foldMap("m1", nil, "agent-1", "todo", "2026-08-14T12:00:00Z"), map[string]bool{"agent-1": true}, nil, catInProgress},
		{"empty status with live owner is in-progress (TS parity)", foldMap("m1", nil, "agent-1", "", "2026-08-14T12:00:00Z"), map[string]bool{"agent-1": true}, nil, catInProgress},
		{"todo with live owner and no agent filter is in-progress (TS parity)", foldMap("m1", nil, "agent-1", "todo", "2026-08-14T12:00:00Z"), nil, nil, catInProgress},
		{"done with live owner is completed (done outranks owner)", foldMap("m1", nil, "agent-1", "done", "2026-08-14T12:00:00Z"), map[string]bool{"agent-1": true}, nil, catCompleted},
		{"todo with dead owner is to-start (TS parity)", foldMap("m1", nil, "agent-gone", "todo", "2026-08-14T12:00:00Z"), map[string]bool{"agent-1": true}, nil, catToStart},
		{"ownerless todo instance_of is planned (TS parity)", foldCard("inst", func(c *domain.MonoCardListItem) {
			c.Type = "workflow"
			c.Status = "todo"
			c.Data = map[string]any{"instance_of": "tpl::x"}
		}), map[string]bool{"agent-1": true}, nil, catPlanned},
		{"doing with dead owner is to-start", foldMap("m1", nil, "agent-gone", "doing", "2026-08-14T12:00:00Z"), map[string]bool{"agent-1": true}, nil, catToStart},
		{"doing without agent filter is in-progress", foldMap("m1", nil, "agent-1", "doing", "2026-08-14T12:00:00Z"), nil, nil, catInProgress},
		{"doing ownerless instance_of is planned", foldCard("inst", func(c *domain.MonoCardListItem) {
			c.Type = "workflow"
			c.Status = "doing"
			c.Data = map[string]any{"instance_of": "tpl::x"}
		}), map[string]bool{"agent-1": true}, nil, catPlanned},
		{"done is completed", foldMap("m1", nil, "", "done", "2026-08-14T12:00:00Z"), nil, nil, catCompleted},
		{"done older than 7d is archived", foldMap("m1", nil, "", "done", "2026-08-01T00:00:00Z"), nil, nil, catArchived},
		{"todo is to-start", foldMap("m1", nil, "", "todo", "2026-08-14T12:00:00Z"), nil, nil, catToStart},
		{"empty status is to-start", foldMap("m1", nil, "", "", "2026-08-14T12:00:00Z"), nil, nil, catToStart},
		{"archived uses task activity", foldMap("m1", []string{"t1"}, "", "done", "2026-08-01T00:00:00Z"), nil,
			[]domain.MonoCardListItem{foldCard("t1", func(c *domain.MonoCardListItem) { c.Type = "task"; c.Modified = "2026-08-13T00:00:00Z" })}, catCompleted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			all := append([]domain.MonoCardListItem{tc.card}, tc.extra...)
			if got := classifyWorkflowCategory(tc.card, tc.agentIDs, all, foldNow); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

// D3 reproduction: a scheduler referencing "tpl::map-a" must NOT cause the
// original map-a to be misclassified as planned. The old collectScheduledMapIDs
// matched scheduler ref "tpl::map-a" against map-a by suffix ("::map-a"),
// flagging map-a planned and dropping its tasks when the planned band folded.
// With the scheduled branch deleted, planned comes only from the scheduler card
// itself or an ownerless instance_of.
func TestClassifyWorkflowCategory_D3Repro(t *testing.T) {
	scheduler := foldCard("sched-1", func(c *domain.MonoCardListItem) {
		c.Type = "scheduler"
		c.Data = map[string]any{"workflow_template": "tpl::map-a"}
	})
	tpl := foldCard("tpl::map-a", func(c *domain.MonoCardListItem) {
		c.Type = "workflow"
		c.Data = map[string]any{"template": true}
	})
	mapA := foldMap("map-a", nil, "agent-1", "doing", "2026-08-14T12:00:00Z")
	all := []domain.MonoCardListItem{scheduler, tpl, mapA}
	agentIDs := map[string]bool{"agent-1": true}

	if got := classifyWorkflowCategory(mapA, agentIDs, all, foldNow); got != catInProgress {
		t.Fatalf("map-a: got %q want in-progress (scheduler ref must not leak)", got)
	}
	if got := classifyWorkflowCategory(tpl, agentIDs, all, foldNow); got != catTemplate {
		t.Fatalf("tpl::map-a: got %q want template (data.template:true)", got)
	}
	if got := classifyWorkflowCategory(scheduler, agentIDs, all, foldNow); got != catPlanned {
		t.Fatalf("scheduler card: got %q want planned", got)
	}
}

// D3 end-to-end through the fold filter: folding the planned band must not hide
// map-a's tasks because map-a is in-progress, not planned.
func TestFilterWorkflowFoldVisible_D3Repro(t *testing.T) {
	scheduler := foldCard("sched-1", func(c *domain.MonoCardListItem) {
		c.Type = "scheduler"
		c.Data = map[string]any{"workflow_template": "tpl::map-a"}
	})
	tpl := foldCard("tpl::map-a", func(c *domain.MonoCardListItem) {
		c.Type = "workflow"
		c.Data = map[string]any{"template": true}
	})
	mapA := foldMap("map-a", []string{"t-map-a"}, "agent-1", "doing", "2026-08-14T12:00:00Z")
	taskA := foldTask("t-map-a", "map-a", nil)
	cards := []domain.MonoCardListItem{scheduler, tpl, mapA, taskA}

	foldedPlanned := &domain.WikiWorkflowFilter{
		FoldedCategories: []string{"planned"},
		AgentIds:         []string{"agent-1"},
	}
	got := idsOf(filterWorkflowFoldVisible(cards, foldedPlanned, foldNow))
	if !sliceContains(got, "map-a") || !sliceContains(got, "t-map-a") {
		t.Fatalf("folding planned must keep in-progress map-a plus its task, got %v", got)
	}
	if !sliceContains(got, "tpl::map-a") || !sliceContains(got, "sched-1") {
		t.Fatalf("template and scheduler rows must be kept, got %v", got)
	}
}

// Waiting-owner regression: a user-started map (workflow_start binds
// ownerAgentId without stamping status=doing — activateWorkflow) carries a
// todo/empty status while its owner agent runs or parks in waiting. The client
// classifies it in-progress (live owner) and keeps it expanded; the Go port
// must agree, otherwise every monoStore.load() with the to-start band folded
// dropped the workflow's task cards and the graph collapsed as soon as the
// user looked at it (typically right after the owner parked waiting).
func TestFilterWorkflowFoldVisible_LiveOwnerTodoMapKeepsTasks(t *testing.T) {
	mapA := foldMap("map-a", []string{"t-map-a"}, "agent-1", "todo", "2026-08-14T12:00:00Z")
	taskA := foldTask("t-map-a", "map-a", nil)
	unstarted := foldMap("map-b", []string{"t-map-b"}, "", "todo", "2026-08-14T12:00:00Z")
	taskB := foldTask("t-map-b", "map-b", nil)
	cards := []domain.MonoCardListItem{mapA, taskA, unstarted, taskB}

	foldedToStart := &domain.WikiWorkflowFilter{
		FoldedCategories: []string{"to-start"},
		AgentIds:         []string{"agent-1"},
	}
	got := idsOf(filterWorkflowFoldVisible(cards, foldedToStart, foldNow))
	if !sliceContains(got, "map-a") || !sliceContains(got, "t-map-a") {
		t.Fatalf("folding to-start must keep live-owner todo map-a plus its task, got %v", got)
	}
	if sliceContains(got, "t-map-b") {
		t.Fatalf("folded to-start band must drop ownerless map-b tasks, got %v", got)
	}
}

// An ownerless instance (data.instance_of, doing, no live owner) is planned.
// Its scheduler_card_id points to an existing scheduler card, which forms a
// planned bucket: collapsed bucket hides the instance's tasks (map row kept),
// expanded bucket restores them.
func TestFilterWorkflowFoldVisible_OwnerlessPlannedBucket(t *testing.T) {
	scheduler := foldCard("sched-x", func(c *domain.MonoCardListItem) {
		c.Type = "scheduler"
	})
	inst := foldCard("inst-x", func(c *domain.MonoCardListItem) {
		c.Type = "workflow"
		c.Status = "doing"
		c.Modified = "2026-08-14T12:00:00Z"
		c.Data = map[string]any{
			"instance_of":       "tpl::x",
			"scheduler_card_id": "sched-x",
			"scope":             map[string]any{"include": toAnySlice([]string{"t-inst-x"})},
		}
	})
	taskX := foldTask("t-inst-x", "inst-x", nil)
	cards := []domain.MonoCardListItem{scheduler, inst, taskX}
	agentIDs := []string{"agent-1"}

	if got := classifyWorkflowCategory(inst, toSet(agentIDs), cards, foldNow); got != catPlanned {
		t.Fatalf("ownerless instance: got %q want planned", got)
	}

	folded := &domain.WikiWorkflowFilter{AgentIds: agentIDs}
	got := idsOf(filterWorkflowFoldVisible(cards, folded, foldNow))
	if !sliceContains(got, "inst-x") || sliceContains(got, "t-inst-x") {
		t.Fatalf("collapsed planned bucket keeps map row, drops its task, got %v", got)
	}

	expanded := &domain.WikiWorkflowFilter{
		AgentIds:        agentIDs,
		ExpandedBuckets: []string{"sched-x"},
	}
	got = idsOf(filterWorkflowFoldVisible(cards, expanded, foldNow))
	if !sliceContains(got, "inst-x") || !sliceContains(got, "t-inst-x") {
		t.Fatalf("expanded planned bucket must keep map + task, got %v", got)
	}
}

func TestArchivedBucketID(t *testing.T) {
	tenDays := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	if got := archivedBucketID(tenDays, foldNow); got != epicBucketPrefix+"date:2026-08-04" {
		t.Fatalf("date bucket: %q", got)
	}
	hundredDays := time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC)
	if got := archivedBucketID(hundredDays, foldNow); got != epicBucketPrefix+"month:2026-05" {
		t.Fatalf("month bucket: %q", got)
	}
	twoYears := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	if got := archivedBucketID(twoYears, foldNow); got != epicBucketPrefix+"year:2024" {
		t.Fatalf("year bucket: %q", got)
	}
}

func TestFilterWorkflowFoldVisible(t *testing.T) {
	tenDaysAgo := "2026-08-04T00:00:00Z"
	cards := []domain.MonoCardListItem{
		foldMap("w-doing", []string{"t-doing"}, "agent-1", "doing", "2026-08-14T12:00:00Z"),
		foldTask("t-doing", "w-doing", nil),
		foldMap("w-todo", []string{"t-todo"}, "", "todo", "2026-08-14T12:00:00Z"),
		foldTask("t-todo", "w-todo", nil),
		foldMap("w-old", []string{"t-old"}, "", "done", tenDaysAgo),
		foldTaskOld("t-old", "w-old", tenDaysAgo),
		foldCard("plain", nil),
	}
	noFilter := &domain.WikiWorkflowFilter{}
	got := idsOf(filterWorkflowFoldVisible(cards, noFilter, foldNow))
	// Map rows are always kept (folded/archived maps render as collapsed
	// nodes); only their task cards drop. Archived buckets default to
	// folded, so w-old stays but t-old drops even with an empty filter.
	if len(got) != 6 || !sliceContains(got, "w-old") || sliceContains(got, "t-old") {
		t.Fatalf("empty filter keeps map rows, drops archived tasks, got %v", got)
	}
	if filterWorkflowFoldVisible(cards, nil, foldNow) == nil && cards != nil {
		t.Fatal("nil filter must return input unchanged")
	}

	foldedCat := &domain.WikiWorkflowFilter{ExpandedBuckets: []string{epicBucketPrefix + "date:2026-08-04"}}
	got = idsOf(filterWorkflowFoldVisible(cards, foldedCat, foldNow))
	if len(got) != 7 {
		t.Fatalf("fully expanded must keep everything, got %v", got)
	}

	foldedBand := &domain.WikiWorkflowFilter{
		FoldedCategories: []string{"to-start"},
		ExpandedBuckets:  []string{epicBucketPrefix + "date:2026-08-04"},
	}
	got = idsOf(filterWorkflowFoldVisible(cards, foldedBand, foldNow))
	if !sliceContains(got, "w-todo") || sliceContains(got, "t-todo") {
		t.Fatalf("folded band keeps the map row but hides its tasks, got %v", got)
	}

	foldedMap := &domain.WikiWorkflowFilter{FoldedWorkflows: []string{"w-doing"}}
	got = idsOf(filterWorkflowFoldVisible(cards, foldedMap, foldNow))
	if !sliceContains(got, "w-doing") || sliceContains(got, "t-doing") {
		t.Fatalf("folded map keeps its row, drops its task, got %v", got)
	}

	// Archived maps keep their row; expanding their bucket restores tasks.
	bucketID := epicBucketPrefix + "date:2026-08-04"
	got = idsOf(filterWorkflowFoldVisible(cards, noFilter, foldNow))
	if !sliceContains(got, "w-old") || contains(got, "t-old") {
		t.Fatalf("collapsed archived bucket keeps the map row, hides its tasks, got %v", got)
	}
	expanded := &domain.WikiWorkflowFilter{ExpandedBuckets: []string{bucketID}}
	got = idsOf(filterWorkflowFoldVisible(cards, expanded, foldNow))
	if !contains(got, "w-old") || !contains(got, "t-old") {
		t.Fatalf("expanded bucket must show map+task, got %v", got)
	}

	// Folding the archived band hides even expanded buckets.
	archivedFolded := &domain.WikiWorkflowFilter{FoldedCategories: []string{"archived"}, ExpandedBuckets: []string{bucketID}}
	got = idsOf(filterWorkflowFoldVisible(cards, archivedFolded, foldNow))
	if !sliceContains(got, "w-old") || sliceContains(got, "t-old") {
		t.Fatalf("folded archived band keeps the map row, hides its tasks, got %v", got)
	}
}

// Parity regression: a present-but-empty AgentIds filter must behave like the
// frontend's empty Set (owner-guarded), not like nil (unguarded in-progress).
// Cold start / no-agents projects previously classified doing maps as
// in-progress server-side while the client classified them to-start/planned,
// so folding hid tasks on one side that the other side showed.
func TestFilterWorkflowFoldVisible_EmptyAgentIDsParity(t *testing.T) {
	doing := foldMap("w-doing", []string{"t-doing"}, "agent-1", "doing", "2026-08-14T12:00:00Z")
	task := foldTask("t-doing", "w-doing", nil)
	cards := []domain.MonoCardListItem{doing, task}

	// Empty AgentIds (omitted) + nothing folded: doing map has no live owner
	// and no instance_of → to-start, so neither row nor task may be dropped.
	got := idsOf(filterWorkflowFoldVisible(cards, &domain.WikiWorkflowFilter{AgentIds: []string{}}, foldNow))
	if !sliceContains(got, "w-doing") || !sliceContains(got, "t-doing") {
		t.Fatalf("empty AgentIds, nothing folded: doing map and its task must be kept, got %v", got)
	}
	// Omitted AgentIds field behaves identically.
	got = idsOf(filterWorkflowFoldVisible(cards, &domain.WikiWorkflowFilter{}, foldNow))
	if !sliceContains(got, "w-doing") || !sliceContains(got, "t-doing") {
		t.Fatalf("omitted AgentIds, nothing folded: doing map and its task must be kept, got %v", got)
	}

	// Folding the to-start band now hides the doing map's tasks (row kept) —
	// matching the client's empty-Set placement of this map.
	foldedToStart := &domain.WikiWorkflowFilter{FoldedCategories: []string{"to-start"}}
	got = idsOf(filterWorkflowFoldVisible(cards, foldedToStart, foldNow))
	if !sliceContains(got, "w-doing") || sliceContains(got, "t-doing") {
		t.Fatalf("folded to-start band keeps the map row, drops its task, got %v", got)
	}

	// Ownerless doing template instance with empty AgentIds is planned
	// (frontend empty-Set parity); folding the planned band drops its tasks.
	inst := foldCard("inst-e", func(c *domain.MonoCardListItem) {
		c.Type = "workflow"
		c.Status = "doing"
		c.Modified = "2026-08-14T12:00:00Z"
		c.Data = map[string]any{
			"instance_of": "tpl::e",
			"scope":       map[string]any{"include": toAnySlice([]string{"t-inst-e"})},
		}
	})
	taskE := foldTask("t-inst-e", "inst-e", nil)
	instCards := []domain.MonoCardListItem{inst, taskE}
	if cat := classifyWorkflowCategory(inst, toSet(nil), instCards, foldNow); cat != catPlanned {
		t.Fatalf("ownerless instance with empty agent set: got %q want planned", cat)
	}
	foldedPlanned := &domain.WikiWorkflowFilter{FoldedCategories: []string{"planned"}}
	got = idsOf(filterWorkflowFoldVisible(instCards, foldedPlanned, foldNow))
	if !sliceContains(got, "inst-e") || sliceContains(got, "t-inst-e") {
		t.Fatalf("folded planned band keeps the instance row, drops its task, got %v", got)
	}
	unfolded := idsOf(filterWorkflowFoldVisible(instCards, &domain.WikiWorkflowFilter{}, foldNow))
	if !sliceContains(unfolded, "inst-e") || !sliceContains(unfolded, "t-inst-e") {
		t.Fatalf("unfolded planned instance keeps row and task, got %v", unfolded)
	}
}

func sliceContains(items []string, want string) bool {
	for _, s := range items {
		if s == want {
			return true
		}
	}
	return false
}
