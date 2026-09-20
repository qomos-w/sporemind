package project

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestMountTargetForCard(t *testing.T) {
	cases := []struct {
		cardType, cardStatus, want string
	}{
		{"task", "", builtinBacklogID}, // empty status -> backlog
		{"task", "todo", builtinTodoID},
		{"task", "doing", builtinDoingID},
		{"task", "done", builtinDoneID},
		{"task", "Done", builtinDoneID}, // case-insensitive
		{"task", "backlog", builtinBacklogID},
		{"task", "blocked", builtinBlockedID},
		{"task", "cancelled", builtinCancelledID},
		{"task", "weird-status", builtinBacklogID}, // unknown status -> backlog
		{"prompt", "", builtinPromptID},
		{"skill", "", builtinSkillID},
		{"callable", "", builtinCallableID},
		{"capability_module", "", builtinCapabilityModuleID},
		{"scheduler", "", builtinSchedulerID},
		{"bundle", "", builtinBundleID},
		{"wiki", "", ""},    // wiki no longer auto-mounts to a virtual node
		{"concept", "", ""}, // autoMount:false
		{"unknown", "", ""},
	}
	for _, c := range cases {
		got := mountTargetForCard(c.cardType, c.cardStatus, nil)
		if got != c.want {
			t.Errorf("mountTargetForCard(%q,%q) = %q, want %q", c.cardType, c.cardStatus, got, c.want)
		}
	}
}

func TestMountTargetForCardNormalizesLegacyTypes(t *testing.T) {
	// plan/goal/kanban-task merge into task and route to per-status nodes.
	if got := mountTargetForCard("plan", "", nil); got != builtinBacklogID {
		t.Errorf("plan -> %q, want %q", got, builtinBacklogID)
	}
	if got := mountTargetForCard("goal", "done", nil); got != builtinDoneID {
		t.Errorf("goal(done) -> %q, want %q", got, builtinDoneID)
	}
	if got := mountTargetForCard("reminder", "", nil); got != builtinSchedulerID {
		t.Errorf("reminder -> %q, want %q", got, builtinSchedulerID)
	}
}

func TestMountTargetForCardAppliesStatusAliases(t *testing.T) {
	cases := []struct {
		status, want string
	}{
		// Legacy synonyms resolved via defaultTaskStatusAliases.
		{"in_progress", builtinDoingID},
		{"active", builtinDoingID},
		{"completed", builtinDoneID},
		{"approved", builtinTodoID},
		{"open", builtinTodoID},
		{"draft", builtinTodoID},
		{"rejected", builtinCancelledID},
		{"canceled", builtinCancelledID},
		// Unknown -> backlog.
		{"weird", builtinBacklogID},
	}
	for _, c := range cases {
		if got := mountTargetForCard("task", c.status, nil); got != c.want {
			t.Errorf("task(%q) -> %q, want %q", c.status, got, c.want)
		}
	}
}

func TestMountTargetForCardRespectsCardAliases(t *testing.T) {
	// A user-edited __builtin_task_status_map__ card overrides the default
	// aliases: here "queued" is mapped to "doing", and "completed" is remapped
	// from done to cancelled. Routing must follow the card, not the defaults.
	items := []gen.MonoCardListItem{{
		ID: statusMapCardID,
		Data: map[string]any{
			"aliases": []any{"queued:doing", "completed:cancelled"},
		},
	}}
	aliases := resolveTaskStatusAliases(items)
	if got := mountTargetForCard("task", "queued", aliases); got != builtinDoingID {
		t.Errorf("queued(card) -> %q, want %q", got, builtinDoingID)
	}
	if got := mountTargetForCard("task", "completed", aliases); got != builtinCancelledID {
		t.Errorf("completed(card-remapped) -> %q, want %q", got, builtinCancelledID)
	}
	// A status with no alias entry still falls back to backlog.
	if got := mountTargetForCard("task", "totally-new", aliases); got != builtinBacklogID {
		t.Errorf("unknown -> %q, want %q", got, builtinBacklogID)
	}
}

func TestVirtualMountBuiltinCardsCarryRules(t *testing.T) {
	cards := virtualMountBuiltinCards()
	byID := make(map[string]*BuiltinCard, len(cards))
	for _, c := range cards {
		byID[c.Title] = c
	}
	// Aggregator node __builtin_task__ exists and never carries a mount rule.
	task := byID[builtinTaskID]
	if task == nil {
		t.Fatal("__builtin_task__ aggregator node missing")
	}
	if role, _ := task.Data["builtinRole"].(string); role != "aggregator" {
		t.Errorf("task builtinRole = %q, want aggregator", role)
	}
	visual, ok := task.Data["visual"].(map[string]any)
	if !ok {
		t.Errorf("__builtin_task__ visual missing")
	} else {
		if visual["icon"] != "layers" {
			t.Errorf("__builtin_task__ visual.icon = %q, want layers", visual["icon"])
		}
		if visual["emphasis"] != "strong" {
			t.Errorf("__builtin_task__ visual.emphasis = %q, want strong", visual["emphasis"])
		}
	}
	// All six status nodes must exist, declare mount role, and be parented to
	// __builtin_task__.
	for _, status := range canonicalTaskStatuses {
		node, ok := taskStatusNode[status]
		if !ok || node == "" {
			t.Fatalf("no virtual node for status %q", status)
		}
		c, ok := byID[node]
		if !ok {
			t.Errorf("virtual node %q (%s) not registered", node, status)
			continue
		}
		role, _ := c.Data["builtinRole"].(string)
		if role != "mount" {
			t.Errorf("%s builtinRole = %q, want mount", c.Title, role)
		}
		if mt, _ := c.Data["mountType"].(string); mt != "task" {
			t.Errorf("%s mountType = %q, want task", c.Title, mt)
		}
		if ms, _ := c.Data["mountStatus"].(string); ms != status {
			t.Errorf("%s mountStatus = %q, want %q", c.Title, ms, status)
		}
		visual, ok := c.Data["visual"].(map[string]any)
		if !ok {
			t.Errorf("%s visual missing", c.Title)
			continue
		}
		wantVisual := statusBuiltinVisuals[status]
		if visual["accent"] != wantVisual["accent"] {
			t.Errorf("%s visual.accent = %q, want %q", c.Title, visual["accent"], wantVisual["accent"])
		}
		if visual["icon"] != wantVisual["icon"] {
			t.Errorf("%s visual.icon = %q, want %q", c.Title, visual["icon"], wantVisual["icon"])
		}
		if c.Parent != builtinTaskID {
			t.Errorf("%s parent = %q, want %q", c.Title, c.Parent, builtinTaskID)
		}
	}
	// Non-task virtual nodes keep their rules and have no parent.
	for _, want := range []string{builtinConceptID, builtinCallableID, builtinCapabilityModuleID, builtinSchedulerID, builtinBundleID} {
		c, ok := byID[want]
		if !ok {
			t.Errorf("virtual node %q not registered", want)
			continue
		}
		if role, _ := c.Data["builtinRole"].(string); role != "mount" {
			t.Errorf("%s builtinRole = %q, want mount", want, role)
		}
		if c.Parent != "" {
			t.Errorf("%s parent = %q, want empty", want, c.Parent)
		}
	}
	// Concept must declare autoMount:false.
	concept := byID[builtinConceptID]
	if concept == nil {
		t.Fatal("concept virtual node missing")
	}
	if auto, _ := concept.Data["autoMount"].(bool); auto {
		t.Error("concept should have autoMount:false")
	}
}

// TestNamedBuiltinsCarryMountData verifies that reused named builtin cards
// (skill, prompt) carry the mount-rule data injected from the spec registry, so
// the frontend can derive mount routing for skill/prompt cards. Without this,
// skill cards never mount under __builtin_skill__.
func TestNamedBuiltinsCarryMountData(t *testing.T) {
	// applyMountDataToNamedBuiltins runs in init(); the package-level builtinCards
	// must already have the data injected.
	for _, b := range builtinCards {
		if b.Title != builtinSkillID && b.Title != builtinPromptID {
			continue
		}
		role, _ := b.Data["builtinRole"].(string)
		if role != "mount" {
			t.Errorf("%s builtinRole = %q, want mount (data=%#v)", b.Title, role, b.Data)
		}
		if mt, _ := b.Data["mountType"].(string); mt == "" {
			t.Errorf("%s mountType missing (data=%#v)", b.Title, b.Data)
		}
		if _, ok := b.Data["autoMount"]; !ok {
			t.Errorf("%s autoMount missing (data=%#v)", b.Title, b.Data)
		}
	}
	// The mount-rule data must also appear in the persisted Raw frontmatter so
	// seedBuiltinCards rewrites the on-disk card and the frontend sees it.
	for _, b := range builtinCards {
		if b.Title != builtinSkillID {
			continue
		}
		raw := b.ToCardRecord().Raw
		if !strings.Contains(raw, "builtinRole: mount") {
			t.Errorf("%s Raw missing builtinRole: mount\n%s", b.Title, raw)
		}
	}
}

func TestSkillCardMountsUnderSkillBuiltinInTree(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Skill card IDs use the skill:<name> namespace form. They must still
	// participate in type-driven mounting despite the ":" — only __builtin_*
	// nodes are skipped as mount sources.
	raw := "---\nid: skill:my-skill\ntype: skill\ntags: []\n---\n\nSkill body."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "skill:my-skill", Raw: raw}); err != nil {
		t.Fatalf("create: %v", err)
	}

	resp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{RootID: builtinSkillID})
	if err != nil {
		t.Fatalf("listCards skill: %v", err)
	}
	if !strings.Contains(resp.Tree, "skill:my-skill") {
		t.Errorf("skill card not mounted under __builtin_skill__:\n%s", resp.Tree)
	}
}

func TestTypeMountingAttachesTaskToTodoInTree(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	raw := "---\nid: task-1\ntype: task\ntags: []\nstatus: todo\n---\n\nTask body."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "task-1", Raw: raw}); err != nil {
		t.Fatalf("create: %v", err)
	}

	resp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{RootID: builtinTodoID})
	if err != nil {
		t.Fatalf("listCards todo: %v", err)
	}
	if !strings.Contains(resp.Tree, "task-1") {
		t.Errorf("task card not mounted under __builtin_todo__:\n%s", resp.Tree)
	}
}

func TestTypeMountingKeepsParentAndDoneMount(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	parentRaw := "---\nid: plan\ntype: task\ntags: []\nstatus: todo\n---\n\nPlan."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "plan", Raw: parentRaw}); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	childRaw := "---\nid: done-child\ntype: task\ntags: [plan]\nstatus: done\nparent: plan\n---\n\nDone."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "done-child", Raw: childRaw}); err != nil {
		t.Fatalf("create child: %v", err)
	}

	parentResp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{RootID: "plan"})
	if err != nil || !strings.Contains(parentResp.Tree, "done-child") {
		t.Fatalf("done child missing under parent: err=%v tree=%s", err, parentResp.Tree)
	}
	doneResp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{RootID: builtinDoneID})
	if err != nil || !strings.Contains(doneResp.Tree, "done-child") {
		t.Fatalf("done child missing under done mount: err=%v tree=%s", err, doneResp.Tree)
	}
}

func TestTypeMountingRoutesDoneTaskToDone(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	raw := "---\nid: task-done\ntype: task\ntags: []\nstatus: done\n---\n\nDone body."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "task-done", Raw: raw}); err != nil {
		t.Fatalf("create: %v", err)
	}

	doneResp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{RootID: builtinDoneID})
	if err != nil {
		t.Fatalf("listCards done: %v", err)
	}
	if !strings.Contains(doneResp.Tree, "task-done") {
		t.Errorf("done task not mounted under __builtin_done__:\n%s", doneResp.Tree)
	}

	// And it must NOT appear under todo.
	todoResp, _ := a.handleWikiListCards(ctx, domain.WikiListCardsReq{RootID: builtinTodoID})
	if strings.Contains(todoResp.Tree, "task-done") {
		t.Errorf("done task leaked into todo:\n%s", todoResp.Tree)
	}
}

func TestTypeMountingSkipsConceptAutoMount(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	raw := "---\nid: concept-1\ntype: concept\ntags: []\n---\n\nConcept body."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "concept-1", Raw: raw}); err != nil {
		t.Fatalf("create: %v", err)
	}

	resp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{RootID: builtinConceptID})
	if err != nil {
		t.Fatalf("listCards concept: %v", err)
	}
	if strings.Contains(resp.Tree, "concept-1") {
		t.Errorf("concept card should not auto-mount:\n%s", resp.Tree)
	}
}

func TestTypeMountingRoutesTaskByStatusAndAggregatesUnderTaskNode(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Task with no status defaults to backlog.
	rawBacklog := "---\nid: task-backlog\ntype: task\ntags: []\n---\n\nBody."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "task-backlog", Raw: rawBacklog}); err != nil {
		t.Fatalf("create backlog: %v", err)
	}
	// Task in doing.
	rawDoing := "---\nid: task-doing\ntype: task\ntags: []\nstatus: doing\n---\n\nBody."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "task-doing", Raw: rawDoing}); err != nil {
		t.Fatalf("create doing: %v", err)
	}

	// Each lands under its own status node.
	if resp, _ := a.handleWikiListCards(ctx, domain.WikiListCardsReq{RootID: builtinBacklogID}); !strings.Contains(resp.Tree, "task-backlog") {
		t.Errorf("no-status task not under __builtin_backlog__:\n%s", resp.Tree)
	}
	if resp, _ := a.handleWikiListCards(ctx, domain.WikiListCardsReq{RootID: builtinDoingID}); !strings.Contains(resp.Tree, "task-doing") {
		t.Errorf("doing task not under __builtin_doing__:\n%s", resp.Tree)
	}
	// doing task must not leak into backlog.
	if resp, _ := a.handleWikiListCards(ctx, domain.WikiListCardsReq{RootID: builtinBacklogID}); strings.Contains(resp.Tree, "task-doing") {
		t.Errorf("doing task leaked into backlog:\n%s", resp.Tree)
	}

	// __builtin_task__ aggregates all six status nodes as direct children; task
	// cards appear only as grandchildren (under their status node), never as
	// direct children of __builtin_task__.
	taskTree, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{RootID: builtinTaskID})
	if err != nil {
		t.Fatalf("listCards task: %v", err)
	}
	if !strings.Contains(taskTree.Tree, "__builtin_backlog__") {
		t.Errorf("__builtin_task__ missing backlog status node:\n%s", taskTree.Tree)
	}
	if !strings.Contains(taskTree.Tree, "__builtin_doing__") {
		t.Errorf("__builtin_task__ missing doing status node:\n%s", taskTree.Tree)
	}
	// All six status nodes must appear as direct children.
	for _, status := range canonicalTaskStatuses {
		node := taskStatusNode[status]
		if !strings.Contains(taskTree.Tree, node) {
			t.Errorf("__builtin_task__ missing status node %s:\n%s", node, taskTree.Tree)
		}
	}
}

func TestDefaultTocMountingAttachesParentlessWikiCards(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	cases := []struct {
		id  string
		raw string
	}{
		{"fresh-note", "---\nid: fresh-note\ntype: wiki\ntags: []\n---\n\nNew card body."},
		{"legacy-note", "---\nid: legacy-note\ntype: note\ntags: []\n---\n\nLegacy type."},
		{"untyped", "---\nid: untyped\ntags: []\n---\n\nEmpty type defaults to wiki."},
	}
	for _, c := range cases {
		if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: c.id, Raw: c.raw}); err != nil {
			t.Fatalf("create %s: %v", c.id, err)
		}
	}

	// A wiki card with an explicit parent stays under its parent.
	parented := "---\nid: parented\ntype: wiki\ntags: [fresh-note]\nparent: fresh-note\n---\n\nChild."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "parented", Raw: parented}); err != nil {
		t.Fatalf("create parented: %v", err)
	}
	// Machinery cards never mount under toc.
	standalone := "---\nid: standalone\ntype: wiki\ntags: []\nstandalone: true\n---\n\nHidden."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "standalone", Raw: standalone}); err != nil {
		t.Fatalf("create standalone: %v", err)
	}
	// Non-wiki types keep their own mounts and never land under toc.
	task := "---\nid: a-task\ntype: task\ntags: []\nstatus: todo\n---\n\nTask."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "a-task", Raw: task}); err != nil {
		t.Fatalf("create a-task: %v", err)
	}

	resp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{})
	if err != nil {
		t.Fatalf("listCards: %v", err)
	}
	direct := map[string]bool{}
	if len(resp.Nodes) == 1 {
		for _, child := range resp.Nodes[0].Children {
			direct[child.ID] = true
		}
	}
	for _, id := range []string{"fresh-note", "legacy-note", "untyped"} {
		if !direct[id] {
			t.Errorf("parentless wiki card %s not a direct toc child:\n%s", id, resp.Tree)
		}
	}
	for _, id := range []string{"parented", "standalone", "a-task"} {
		if direct[id] {
			t.Errorf("%s must not be a direct toc child:\n%s", id, resp.Tree)
		}
	}
	// Parented child resolves under its explicit parent instead.
	if resp, _ := a.handleWikiListCards(ctx, domain.WikiListCardsReq{RootID: "fresh-note"}); !strings.Contains(resp.Tree, "parented") {
		t.Errorf("parented child missing under explicit parent:\n%s", resp.Tree)
	}
}
