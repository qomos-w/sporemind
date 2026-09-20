package project

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// ── template_save tests ────────────────────────────────────────────────────

// TestHandleWikiTemplateSave_BasicSnapshot builds a workflow map with three
// tasks and one depends_on edge, calls template_save, and verifies:
//   - the template map is created with data.template:true;
//   - each source task is copied to a namespaced id under the template;
//   - the graph snapshot holds the remapped nodes + the remapped edge;
//   - the source map and source tasks are untouched (snapshot semantics).
func TestHandleWikiTemplateSave_BasicSnapshot(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "src"}); err != nil {
		t.Fatalf("create src map: %v", err)
	}
	if _, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "src", Title: "a", Question: "Q-a"}); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if _, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "src", Title: "b", Question: "Q-b"}); err != nil {
		t.Fatalf("create b: %v", err)
	}
	if _, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "src", Title: "c", Question: "Q-c", DependsOn: []string{"a"}}); err != nil {
		t.Fatalf("create c: %v", err)
	}

	resp, err := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{MapID: "src", TemplateID: "tpl"})
	if err != nil {
		t.Fatalf("template_save: %v", err)
	}
	if resp.TemplateMap.ID != "tpl" {
		t.Errorf("TemplateMap.ID = %q, want tpl", resp.TemplateMap.ID)
	}
	if got, want := len(resp.NodeMap), 3; got != want {
		t.Errorf("NodeMap len = %d, want %d", got, want)
	}

	// Template map frontmatter carries the template marker.
	tplCard, err := a.store.Get("tpl")
	if err != nil {
		t.Fatalf("get template map: %v", err)
	}
	if !cardIsTemplate(tplCard) {
		t.Errorf("template map should have data.template:true; data=%v", tplCard.Data)
	}

	// Each source task has a copy under the namespaced id.
	for _, m := range resp.NodeMap {
		copied, err := a.store.Get(m.To)
		if err != nil {
			t.Errorf("template task %q missing: %v", m.To, err)
			continue
		}
		if copied.Parent != "tpl" {
			t.Errorf("template task %q parent = %q, want tpl", m.To, copied.Parent)
		}
		if !cardIsTemplate(copied) {
			t.Errorf("template task %q should have data.template:true; data=%v", m.To, copied.Data)
		}
		// Runtime fields must be stripped: no owner, no task_outputs.
		// depends_on is projected from the remapped graph so the topology
		// is derivable from card frontmatter (stateless workflow_topo).
		if _, has := copied.Data["ownerAgentId"]; has {
			t.Errorf("template task %q must not carry ownerAgentId", m.To)
		}
		if _, has := copied.Data["task_outputs"]; has {
			t.Errorf("template task %q must not carry task_outputs", m.To)
		}
	}
	// Verify the remapped edge was projected to frontmatter.
	cTpl, _ := a.store.Get("tpl::c")
	deps := toStringSlice(cTpl.Data["depends_on"])
	if len(deps) != 1 || deps[0] != "tpl::a" {
		t.Errorf("tpl::c depends_on = %v, want [tpl::a]", deps)
	}

	// Graph for the template: 3 nodes + the remapped edge.
	tplGraph, rev := a.loadWorkflowTopo("tpl")
	if rev == "" {
		t.Fatal("template graph missing")
	}
	if len(tplGraph.Nodes) != 3 {
		t.Errorf("template graph nodes = %d, want 3", len(tplGraph.Nodes))
	}
	// Build expected edge: src "c" -> src "a" maps to "tpl::c" -> "tpl::a".
	wantFrom, wantTo := "tpl::c", "tpl::a"
	var found bool
	for _, e := range tplGraph.Edges {
		if e.From == wantFrom && e.To == wantTo && e.Kind == "depends_on" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected remapped edge %s -> %s, got edges=%+v", wantFrom, wantTo, tplGraph.Edges)
	}

	// Source map and source tasks must be untouched.
	srcCard, _ := a.store.Get("src")
	if cardIsTemplate(srcCard) {
		t.Errorf("source map must not gain data.template:true")
	}
	for _, id := range []string{"a", "b", "c"} {
		c, err := a.store.Get(id)
		if err != nil {
			t.Errorf("source task %q disappeared: %v", id, err)
			continue
		}
		if c.Parent != "src" {
			t.Errorf("source task %q parent changed: %q", id, c.Parent)
		}
	}
}

// TestHandleWikiTemplateSave_BindingsRemapped verifies that data-flow
// bindings are copied with both endpoints (owning node + FromNode) translated
// through the id map, and validate-cleanly against the new graph (i.e. the
// remapped bindings don't drop to dangling).
func TestHandleWikiTemplateSave_BindingsRemapped(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "src"}); err != nil {
		t.Fatalf("create src: %v", err)
	}
	if _, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "src", Title: "a", Question: "Q"}); err != nil {
		t.Fatalf("create a: %v", err)
	}
	// Build a binding by writing it directly into the graph (the wire
	// validation path is covered by data binding tests; here we only need to
	// confirm remap preserves the binding end-to-end).
	if _, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID:     "src",
		Title:     "b",
		Question:  "Q",
		DependsOn: []string{"a"},
		Bindings:  []gen.TaskDataBinding{{Input: "in_x", FromNode: "a", FromOutput: "out_y"}},
	}); err != nil {
		t.Fatalf("create b with binding: %v", err)
	}

	resp, err := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{MapID: "src", TemplateID: "tpl"})
	if err != nil {
		t.Fatalf("template_save: %v", err)
	}
	if len(resp.NodeMap) != 2 {
		t.Fatalf("NodeMap = %d, want 2", len(resp.NodeMap))
	}

	tplGraph, _ := a.loadWorkflowTopo("tpl")
	if len(tplGraph.Bindings) != 1 {
		t.Fatalf("template graph bindings = %d, want 1; got=%+v", len(tplGraph.Bindings), tplGraph.Bindings)
	}
	b := tplGraph.Bindings[0]
	if b.Node != "tpl::b" || b.Input != "in_x" || b.FromNode != "tpl::a" || b.FromOutput != "out_y" {
		t.Errorf("binding not remapped cleanly: %+v", b)
	}
	// No dangling: every FromNode in the template's bindings should be a
	// known node.
	known := make(map[string]bool, len(tplGraph.Nodes))
	for _, n := range tplGraph.Nodes {
		known[n.ID] = true
	}
	for _, b := range tplGraph.Bindings {
		if !known[b.FromNode] {
			t.Errorf("binding FromNode %q is dangling in template graph", b.FromNode)
		}
	}
}

// TestHandleWikiTemplateSave_RejectsNonWorkflow ensures non-workflow cards
// cannot be saved as templates.
func TestHandleWikiTemplateSave_RejectsNonWorkflow(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Hand-craft a non-workflow card directly.
	if err := a.store.Save(&CardRecord{Title: "wiki-card", Type: "wiki", Raw: "---\nid: wiki-card\ntype: wiki\ntags: []\n---\n\nbody"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, err := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{MapID: "wiki-card", TemplateID: "tpl"})
	if err == nil {
		t.Fatal("template_save should reject non-workflow source")
	}
	if !strings.Contains(err.Error(), "not workflow") {
		t.Errorf("error should mention non-workflow source, got: %v", err)
	}
	if _, err := a.store.Get("tpl"); err == nil {
		t.Errorf("template card must not have been created on rejection")
	}
}

// TestHandleWikiTemplateSave_RejectsTemplateAsSource prevents the
// template-of-a-template path. Templates are definitions, not source maps.
func TestHandleWikiTemplateSave_RejectsTemplateAsSource(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Build a real template first.
	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "src"}); err != nil {
		t.Fatalf("create src: %v", err)
	}
	if _, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "src", Title: "a", Question: "Q"}); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if _, err := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{MapID: "src", TemplateID: "tpl1"}); err != nil {
		t.Fatalf("first template_save: %v", err)
	}

	// Re-save tpl1: must fail because tpl1 is already a template.
	_, err := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{MapID: "tpl1", TemplateID: "tpl2"})
	if err == nil {
		t.Fatal("template_save should reject template-as-source")
	}
	if !strings.Contains(err.Error(), "already a template") {
		t.Errorf("error should mention 'already a template', got: %v", err)
	}
	if _, err := a.store.Get("tpl2"); err == nil {
		t.Errorf("rejected template_save must not create the new map")
	}
}

// TestHandleWikiTemplateSave_RejectsEmptySource ensures a workflow map with
// no scoped tasks cannot be saved (snapshot would be empty).
func TestHandleWikiTemplateSave_RejectsEmptySource(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "empty"}); err != nil {
		t.Fatalf("create empty map: %v", err)
	}

	_, err := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{MapID: "empty", TemplateID: "tpl"})
	if err == nil {
		t.Fatal("template_save should reject empty source map")
	}
	if !strings.Contains(err.Error(), "no task cards") {
		t.Errorf("error should mention no task cards, got: %v", err)
	}
}

// TestHandleWikiTemplateSave_AtomicityOnNameCollision forces a mid-save
// collision and asserts no partial state remains. The source map has a task
// whose remapped id (e.g. "tpl::collision") collides with an existing card,
// triggering a rollback after the template map itself was already saved.
func TestHandleWikiTemplateSave_AtomicityOnNameCollision(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "src"}); err != nil {
		t.Fatalf("create src: %v", err)
	}
	if _, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "src", Title: "a", Question: "Q"}); err != nil {
		t.Fatalf("create a: %v", err)
	}
	// Plant a card that will collide with the remapped task id.
	if err := a.store.Save(&CardRecord{
		Title: "tpl::a",
		Type:  "task",
		Raw:   "---\nid: tpl::a\ntype: task\ntags: [tpl]\nstatus: backlog\n---\n\npre-existing",
	}); err != nil {
		t.Fatalf("plant collision: %v", err)
	}

	_, err := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{MapID: "src", TemplateID: "tpl"})
	if err == nil {
		t.Fatal("template_save should fail on collision")
	}
	if !strings.Contains(err.Error(), "overwrite") {
		t.Errorf("error should mention overwrite, got: %v", err)
	}
	// Rollback must have cleaned up the template map.
	if _, err := a.store.Get("tpl"); err == nil {
		t.Errorf("template map should have been rolled back")
	}
	// The pre-existing collision card must still be intact.
	if _, err := a.store.Get("tpl::a"); err != nil {
		t.Errorf("pre-existing card was removed by rollback")
	}
	// The template's graph snapshot should not exist after rollback.
	if g, rev := a.loadWorkflowTopo("tpl"); rev != "" || len(g.Nodes) != 0 {
		t.Errorf("template graph snapshot should be empty after rollback; rev=%q, nodes=%v", rev, g.Nodes)
	}
}

// ── template_instantiate tests ─────────────────────────────────────────────

// TestHandleWikiTemplateInstantiate_BasicCopy builds a template, instantiates
// it, and verifies:
//   - instance map carries data.instance_of pointing to the template;
//   - each task is copied to a namespaced id, status:backlog, with
//     data.instance_of pointing to the originating template task id;
//   - depends_on is remapped in the instance's frontmatter projection;
//   - the instance graph is the remapped source graph;
//   - the template and its task cards are untouched.
func TestHandleWikiTemplateInstantiate_BasicCopy(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Build: source -> template -> instance.
	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "src"}); err != nil {
		t.Fatalf("create src: %v", err)
	}
	if _, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "src", Title: "a", Question: "Q-a"}); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if _, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID: "src", Title: "b", Question: "Q-b", DependsOn: []string{"a"},
	}); err != nil {
		t.Fatalf("create b: %v", err)
	}
	if _, err := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{MapID: "src", TemplateID: "tpl"}); err != nil {
		t.Fatalf("template_save: %v", err)
	}

	resp, err := a.handleWikiTemplateInstantiate(ctx, domain.WikiTemplateInstantiateReq{
		TemplateMapID: "tpl", InstanceMapID: "inst",
	})
	if err != nil {
		t.Fatalf("template_instantiate: %v", err)
	}
	if resp.InstanceMap.ID != "inst" {
		t.Errorf("InstanceMap.ID = %q, want inst", resp.InstanceMap.ID)
	}
	if got, want := len(resp.NodeMap), 2; got != want {
		t.Errorf("NodeMap len = %d, want %d", got, want)
	}

	// Instance map carries data.instance_of.
	instCard, err := a.store.Get("inst")
	if err != nil {
		t.Fatalf("get instance map: %v", err)
	}
	if got := cardInstanceOf(instCard); got != "tpl" {
		t.Errorf("instance map data.instance_of = %q, want tpl", got)
	}
	if cardIsTemplate(instCard) {
		t.Errorf("instance map must not carry data.template:true")
	}

	// Each instance task card: status=backlog, parent=inst, instance_of set,
	// depends_on projected.
	for _, m := range resp.NodeMap {
		task, err := a.store.Get(m.To)
		if err != nil {
			t.Errorf("instance task %q missing: %v", m.To, err)
			continue
		}
		if task.Parent != "inst" {
			t.Errorf("instance task %q parent = %q, want inst", m.To, task.Parent)
		}
		if task.Status != "backlog" {
			t.Errorf("instance task %q status = %q, want backlog", m.To, task.Status)
		}
		if got := cardInstanceOf(task); got != m.From {
			t.Errorf("instance task %q data.instance_of = %q, want %q", m.To, got, m.From)
		}
		if cardIsTemplate(task) {
			t.Errorf("instance task %q must not carry data.template:true", m.To)
		}
		// The "b"-side instance must have a depends_on in frontmatter. The
		// template task's own id already carries the tpl:: prefix, so the
		// remapped dep is "inst::tpl::a" — full lineage is preserved.
		if strings.HasSuffix(m.From, "::b") {
			deps := toStringSlice(task.Data["depends_on"])
			if len(deps) != 1 || deps[0] != "inst::tpl::a" {
				t.Errorf("instance task %q depends_on = %v, want [inst::tpl::a]", m.To, deps)
			}
		}
		// No runtime outputs were produced yet.
		if _, has := task.Data["task_outputs"]; has {
			t.Errorf("instance task %q must not carry task_outputs (none produced yet)", m.To)
		}
	}

	// Instance graph snapshot has the remapped topology.
	instGraph, rev := a.loadWorkflowTopo("inst")
	if rev == "" {
		t.Fatal("instance graph snapshot missing")
	}
	if len(instGraph.Nodes) != 2 {
		t.Errorf("instance graph nodes = %d, want 2", len(instGraph.Nodes))
	}
	wantFrom, wantTo := "inst::tpl::b", "inst::tpl::a"
	var found bool
	for _, e := range instGraph.Edges {
		if e.From == wantFrom && e.To == wantTo && e.Kind == "depends_on" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected remapped edge %s -> %s, got %+v", wantFrom, wantTo, instGraph.Edges)
	}

	// Template is untouched.
	tplCard, _ := a.store.Get("tpl")
	if !cardIsTemplate(tplCard) {
		t.Errorf("template map lost data.template:true")
	}
	for _, tplTaskID := range []string{"tpl::a", "tpl::b"} {
		if _, err := a.store.Get(tplTaskID); err != nil {
			t.Errorf("template task %q disappeared: %v", tplTaskID, err)
		}
	}
}

// TestHandleWikiTemplateInstantiate_RejectsNonTemplate ensures non-template
// maps cannot be instantiated.
func TestHandleWikiTemplateInstantiate_RejectsNonTemplate(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf"}); err != nil {
		t.Fatalf("create wf: %v", err)
	}
	if _, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "wf", Title: "a", Question: "Q"}); err != nil {
		t.Fatalf("create a: %v", err)
	}

	_, err := a.handleWikiTemplateInstantiate(ctx, domain.WikiTemplateInstantiateReq{
		TemplateMapID: "wf", InstanceMapID: "inst",
	})
	if err == nil {
		t.Fatal("template_instantiate should reject non-template source")
	}
	if !strings.Contains(err.Error(), "not a template") {
		t.Errorf("error should mention 'not a template', got: %v", err)
	}
	if _, err := a.store.Get("inst"); err == nil {
		t.Errorf("instance map must not have been created on rejection")
	}
}

// TestHandleWikiTemplateInstantiate_DoubleInstantiation verifies the
// namespacing is unique per instance: instantiating the same template twice
// produces two independent instance maps whose task ids do not collide.
func TestHandleWikiTemplateInstantiate_DoubleInstantiation(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Build a template with a single task so the namespace uniqueness is
	// visible (the only way a collision could occur is across instance maps).
	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "src"}); err != nil {
		t.Fatalf("create src: %v", err)
	}
	if _, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "src", Title: "only", Question: "Q"}); err != nil {
		t.Fatalf("create only: %v", err)
	}
	if _, err := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{MapID: "src", TemplateID: "tpl"}); err != nil {
		t.Fatalf("template_save: %v", err)
	}

	resp1, err := a.handleWikiTemplateInstantiate(ctx, domain.WikiTemplateInstantiateReq{
		TemplateMapID: "tpl", InstanceMapID: "inst1",
	})
	if err != nil {
		t.Fatalf("first instantiate: %v", err)
	}
	resp2, err := a.handleWikiTemplateInstantiate(ctx, domain.WikiTemplateInstantiateReq{
		TemplateMapID: "tpl", InstanceMapID: "inst2",
	})
	if err != nil {
		t.Fatalf("second instantiate: %v", err)
	}
	if resp1.NodeMap[0].To == resp2.NodeMap[0].To {
		t.Errorf("two instantiations produced colliding task ids: %q == %q",
			resp1.NodeMap[0].To, resp2.NodeMap[0].To)
	}
	// Both instance cards exist.
	for _, id := range []string{resp1.NodeMap[0].To, resp2.NodeMap[0].To} {
		if _, err := a.store.Get(id); err != nil {
			t.Errorf("instance task %q missing: %v", id, err)
		}
	}
}

// TestHandleWikiTemplateInstantiate_AtomicityOnNameCollision plants a card
// that will collide with an instance task id and asserts that the partial
// state (instance map + any successfully-copied tasks) is fully rolled back.
func TestHandleWikiTemplateInstantiate_AtomicityOnNameCollision(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "src"}); err != nil {
		t.Fatalf("create src: %v", err)
	}
	if _, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "src", Title: "a", Question: "Q"}); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if _, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "src", Title: "b", Question: "Q", DependsOn: []string{"a"}}); err != nil {
		t.Fatalf("create b: %v", err)
	}
	if _, err := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{MapID: "src", TemplateID: "tpl"}); err != nil {
		t.Fatalf("template_save: %v", err)
	}

	// Plant a card that will collide with the remapped "inst::tpl::b" id
	// (lineage-preserving composition: instance + "::" + template task id,
	// which itself is "tpl::b"). The copy loop processes "tpl::a" first
	// (success), then "tpl::b" (collision → rollback). After rollback,
	// neither the instance map nor "inst::tpl::a" should remain.
	if err := a.store.Save(&CardRecord{
		Title: "inst::tpl::b",
		Type:  "task",
		Raw:   "---\nid: inst::tpl::b\ntype: task\ntags: [inst]\nstatus: backlog\n---\n\npre-existing",
	}); err != nil {
		t.Fatalf("plant collision: %v", err)
	}

	_, err := a.handleWikiTemplateInstantiate(ctx, domain.WikiTemplateInstantiateReq{
		TemplateMapID: "tpl", InstanceMapID: "inst",
	})
	if err == nil {
		t.Fatal("template_instantiate should fail on collision")
	}
	if !strings.Contains(err.Error(), "overwrite") {
		t.Errorf("error should mention overwrite, got: %v", err)
	}
	// Instance map and any partially-copied instance task must be gone.
	if _, err := a.store.Get("inst"); err == nil {
		t.Errorf("instance map should have been rolled back")
	}
	if _, err := a.store.Get("inst::tpl::a"); err == nil {
		t.Errorf("partially-copied instance task inst::tpl::a should have been rolled back")
	}
	// Pre-existing collision card must still be intact.
	if _, err := a.store.Get("inst::tpl::b"); err != nil {
		t.Errorf("pre-existing collision card was removed by rollback")
	}
}

// TestHandleWikiTemplateInstantiate_AtomicityOnReusedSource ensures that a
// second instantiate of the same template into a different instance map does
// not touch the first instance or the template.
func TestHandleWikiTemplateInstantiate_IndependentInstances(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "src"}); err != nil {
		t.Fatalf("create src: %v", err)
	}
	if _, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "src", Title: "a", Question: "Q"}); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if _, err := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{MapID: "src", TemplateID: "tpl"}); err != nil {
		t.Fatalf("template_save: %v", err)
	}
	if _, err := a.handleWikiTemplateInstantiate(ctx, domain.WikiTemplateInstantiateReq{
		TemplateMapID: "tpl", InstanceMapID: "inst1",
	}); err != nil {
		t.Fatalf("first instantiate: %v", err)
	}
	// Modify the first instance's body so we can detect cross-contamination.
	task1, _ := a.store.Get("inst1::tpl::a")
	if task1 == nil {
		t.Fatal("first instance task inst1::tpl::a missing — naming scheme drift?")
	}
	task1.Body = "edited in inst1"
	task1.Raw = "---\nid: inst1::tpl::a\ntype: task\ntags: [inst1]\nstatus: backlog\n---\n\nedited in inst1\n"
	if err := a.store.Save(task1); err != nil {
		t.Fatalf("mutate inst1 task: %v", err)
	}

	// Instantiate again under a different name.
	if _, err := a.handleWikiTemplateInstantiate(ctx, domain.WikiTemplateInstantiateReq{
		TemplateMapID: "tpl", InstanceMapID: "inst2",
	}); err != nil {
		t.Fatalf("second instantiate: %v", err)
	}
	// First instance's edit survives.
	task1, _ = a.store.Get("inst1::tpl::a")
	if task1 == nil || !strings.Contains(task1.Body, "edited in inst1") {
		t.Errorf("first instance task body mutated by second instantiate: %+v", task1)
	}
	// Second instance's task body is the template's (untouched) body.
	task2, _ := a.store.Get("inst2::tpl::a")
	if task2 == nil {
		t.Fatal("second instance task inst2::tpl::a missing")
	}
	if strings.Contains(task2.Body, "edited in inst1") {
		t.Errorf("second instance leaked body from first instance: %q", task2.Body)
	}
	// Template task untouched.
	tplTask, _ := a.store.Get("tpl::a")
	if tplTask == nil {
		t.Fatal("template task tpl::a missing")
	}
	if strings.Contains(tplTask.Body, "edited in inst1") {
		t.Errorf("template task leaked body from instance: %q", tplTask.Body)
	}
}

// TestHandleWikiTemplateSave_NamespacingIsReversible checks the documented
// guarantee that "<prefix>::<source>" id composition is recoverable by the
// caller. The mapping is uniform across save and instantiate so consumers
// can resolve either direction.
func TestHandleWikiTemplateSave_NamespacingIsReversible(t *testing.T) {
	if got, want := templateTaskID("tpl", "src::a"), "tpl::src::a"; got != want {
		t.Errorf("templateTaskID nested = %q, want %q", got, want)
	}
	if got, want := templateTaskID("inst", "tpl::src::a"), "inst::tpl::src::a"; got != want {
		t.Errorf("templateTaskID three-deep = %q, want %q", got, want)
	}
	// And the inverse direction (caller-side split).
	prefix, rest := splitTemplateTaskID("inst::tpl::src::a")
	if prefix != "inst" || rest != "tpl::src::a" {
		t.Errorf("splitTemplateTaskID = (%q, %q), want (inst, tpl::src::a)", prefix, rest)
	}
}

// splitTemplateTaskID is the reverse of templateTaskID. It exists here (not
// in the production code) because the production code never needs to split
// the id — the NodeMap returned to callers carries the explicit From/To
// translation. The split is exposed for test introspection of the naming
// scheme.
func splitTemplateTaskID(id string) (prefix, rest string) {
	i := strings.Index(id, templateIDPrefix)
	if i < 0 {
		return "", id
	}
	return id[:i], id[i+len(templateIDPrefix):]
}
