package project

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// ── pure function helpers: map-level inputs table ───────────────────────────

func TestTopoSetMapInputs_SetAndClear(t *testing.T) {
	g := WorkflowTopoGraph{Nodes: []WorkflowTopoNode{{ID: "a"}}}
	topoSetMapInputs(&g, map[string]any{"repo": "x/y", "branch": "main"})
	if g.Inputs == nil {
		t.Fatal("expected Inputs map after set")
	}
	if g.Inputs["repo"] != "x/y" || g.Inputs["branch"] != "main" {
		t.Errorf("unexpected inputs: %+v", g.Inputs)
	}

	// Empty map clears the table so JSON omitempty drops it.
	topoSetMapInputs(&g, nil)
	if g.Inputs != nil {
		t.Errorf("expected nil Inputs after clear, got %+v", g.Inputs)
	}
}

func TestTopoMergeMapInputs_PreservesAndOverwrites(t *testing.T) {
	g := WorkflowTopoGraph{Inputs: map[string]any{"keep": 1, "overwrite": "old"}}
	topoMergeMapInputs(&g, map[string]any{"overwrite": "new", "newKey": true})
	if g.Inputs["keep"] != 1 {
		t.Errorf("expected keep=1, got %v", g.Inputs["keep"])
	}
	if g.Inputs["overwrite"] != "new" {
		t.Errorf("expected overwrite=new, got %v", g.Inputs["overwrite"])
	}
	if g.Inputs["newKey"] != true {
		t.Errorf("expected newKey=true, got %v", g.Inputs["newKey"])
	}

	// Empty merge is a no-op.
	topoMergeMapInputs(&g, nil)
	if len(g.Inputs) != 3 {
		t.Errorf("expected 3 keys after nil merge, got %d", len(g.Inputs))
	}
}

// ── binding validation with map-level sentinel ───────────────────────────────

func TestValidateBindings_MapSourceAllowedWithoutDeps(t *testing.T) {
	deps := []string{"up"}
	bindings := []WorkflowTopoBinding{
		{Node: "down", Input: "x", FromNode: MapInputSourceNode, FromOutput: "repo"},
		{Node: "down", Input: "y", FromNode: "up", FromOutput: "score"},
	}
	if err := validateBindings(deps, bindings); err != nil {
		t.Fatalf("$map binding should be allowed: %v", err)
	}
}

func TestValidateBindings_MapSourceRequiresFromOutput(t *testing.T) {
	deps := []string{}
	bindings := []WorkflowTopoBinding{
		{Node: "down", Input: "x", FromNode: MapInputSourceNode, FromOutput: ""},
	}
	err := validateBindings(deps, bindings)
	if err == nil {
		t.Fatal("expected error for empty FromOutput on $map binding")
	}
	if !strings.Contains(err.Error(), "empty FromOutput") {
		t.Errorf("error should mention empty FromOutput: %v", err)
	}
}

func TestValidateBindings_StillRejectsUnknownNode(t *testing.T) {
	deps := []string{"up"}
	bindings := []WorkflowTopoBinding{
		{Node: "down", Input: "x", FromNode: "other", FromOutput: "o"},
	}
	err := validateBindings(deps, bindings)
	if err == nil {
		t.Fatal("expected error for unknown FromNode")
	}
	if !strings.Contains(err.Error(), "not in depends_on") {
		t.Errorf("error should mention depends_on: %v", err)
	}
}

// ── resolveTaskBindings from map-level inputs ────────────────────────────────

func TestResolveTaskBindings_FromMapInputs(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "map-in"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID:    "map-in",
		Title:    "task",
		Question: "Q",
		Bindings: []gen.TaskDataBinding{
			{Input: "repo", FromNode: MapInputSourceNode, FromOutput: "repo"},
			{Input: "branch", FromNode: MapInputSourceNode, FromOutput: "branch"},
		},
	})

	// Channel (a): parsed startup text written via set_map_inputs.
	a.handleWikiSetMapInputs(ctx, domain.WikiSetMapInputsReq{
		MapID:  "map-in",
		Inputs: map[string]any{"repo": "foo/bar", "branch": "main"},
	})

	inputs := a.resolveTaskBindings("map-in", "task")
	if inputs == nil {
		t.Fatal("expected resolved inputs")
	}
	if inputs["repo"] != "foo/bar" {
		t.Errorf("repo = %v, want foo/bar", inputs["repo"])
	}
	if inputs["branch"] != "main" {
		t.Errorf("branch = %v, want main", inputs["branch"])
	}
}

func TestResolveTaskBindings_MapInputMissing(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "map-missing"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID:    "map-missing",
		Title:    "task",
		Question: "Q",
		Bindings: []gen.TaskDataBinding{
			{Input: "repo", FromNode: MapInputSourceNode, FromOutput: "repo"},
			{Input: "missing", FromNode: MapInputSourceNode, FromOutput: "missing"},
		},
	})

	a.handleWikiSetMapInputs(ctx, domain.WikiSetMapInputsReq{
		MapID:  "map-missing",
		Inputs: map[string]any{"repo": "foo/bar"},
	})

	inputs := a.resolveTaskBindings("map-missing", "task")
	if inputs == nil {
		t.Fatal("expected partial inputs")
	}
	if inputs["repo"] != "foo/bar" {
		t.Errorf("repo = %v, want foo/bar", inputs["repo"])
	}
	if _, ok := inputs["missing"]; ok {
		t.Errorf("missing key should not be present: %+v", inputs)
	}
}

// ── channel (a): set_map_inputs ─────────────────────────────────────────────

func TestHandleWikiSetMapInputs_WritesToGraph(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "map-a"})

	resp, err := a.handleWikiSetMapInputs(ctx, domain.WikiSetMapInputsReq{
		MapID:  "map-a",
		Inputs: map[string]any{"repo": "a/b", "branch": "dev"},
	})
	if err != nil {
		t.Fatalf("set_map_inputs: %v", err)
	}
	if resp.Revision == "" {
		t.Error("expected non-empty graph revision")
	}

	g, _ := a.loadWorkflowTopo("map-a")
	if len(g.Inputs) != 2 || g.Inputs["repo"] != "a/b" || g.Inputs["branch"] != "dev" {
		t.Errorf("graph inputs = %+v", g.Inputs)
	}
}

func TestHandleWikiSetMapInputs_ResolvesViaBinding(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "map-a-end"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID:    "map-a-end",
		Title:    "consumer",
		Question: "Q",
		Bindings: []gen.TaskDataBinding{
			{Input: "target", FromNode: MapInputSourceNode, FromOutput: "target"},
		},
	})

	a.handleWikiSetMapInputs(ctx, domain.WikiSetMapInputsReq{
		MapID:  "map-a-end",
		Inputs: map[string]any{"target": "value-from-channel-a"},
	})

	inputs := a.resolveTaskBindings("map-a-end", "consumer")
	if inputs == nil || inputs["target"] != "value-from-channel-a" {
		t.Errorf("resolved inputs = %+v", inputs)
	}
}

// ── channel (c): promote_node_outputs ─────────────────────────────────────────

func TestHandleWikiPromoteNodeOutputs(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "map-c"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID:    "map-c",
		Title:    "intake",
		Question: "Q",
	})
	a.handleWikiSetMapInputs(ctx, domain.WikiSetMapInputsReq{
		MapID:  "map-c",
		Inputs: map[string]any{"keep": "existing"},
	})

	// Persist intake grilling outputs on the node card (review-approve path).
	if _, err := a.handleWikiSetTaskOutputs(ctx, domain.WikiSetTaskOutputsReq{
		CardID:  "intake",
		Outputs: map[string]any{"repo": "foo/bar", "branch": "main"},
	}); err != nil {
		t.Fatalf("set_task_outputs: %v", err)
	}

	resp, err := a.handleWikiPromoteNodeOutputs(ctx, domain.WikiPromoteNodeOutputsReq{
		MapID:  "map-c",
		NodeID: "intake",
	})
	if err != nil {
		t.Fatalf("promote_node_outputs: %v", err)
	}
	if resp.Revision == "" {
		t.Error("expected non-empty graph revision")
	}
	if len(resp.Inputs) != 3 {
		t.Errorf("expected 3 inputs, got %d: %+v", len(resp.Inputs), resp.Inputs)
	}
	if resp.Inputs["repo"] != "foo/bar" || resp.Inputs["branch"] != "main" || resp.Inputs["keep"] != "existing" {
		t.Errorf("unexpected inputs = %+v", resp.Inputs)
	}

	// Graph is actually mutated.
	g, _ := a.loadWorkflowTopo("map-c")
	if g.Inputs["repo"] != "foo/bar" {
		t.Errorf("graph inputs repo = %v", g.Inputs["repo"])
	}
}

func TestHandleWikiPromoteNodeOutputs_NoOutputs(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "map-c-none"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID:    "map-c-none",
		Title:    "intake",
		Question: "Q",
	})

	if _, err := a.handleWikiPromoteNodeOutputs(ctx, domain.WikiPromoteNodeOutputsReq{
		MapID:  "map-c-none",
		NodeID: "intake",
	}); err == nil {
		t.Fatal("expected error when node has no task_outputs")
	}
}

// ── inputs value validation (shared with outputs validator) ───────────────────

func TestValidateInputsAgainstContract_NoContractValid(t *testing.T) {
	card := &CardRecord{Data: map[string]any{}}
	if errs := validateInputsAgainstContract(card, map[string]any{"x": 1}); len(errs) != 0 {
		t.Fatalf("no contract should be valid, got %+v", errs)
	}
}

func TestValidateInputsAgainstContract_MissingAndMismatch(t *testing.T) {
	card := &CardRecord{Data: map[string]any{
		"inputs": map[string]any{
			"repo":   "string",
			"branch": "string",
		},
	}}
	errs := validateInputsAgainstContract(card, map[string]any{"repo": "foo"})
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %+v", errs)
	}
	if errs[0].Code != "inputs_missing_field" || errs[0].Field != "inputs.branch" {
		t.Errorf("expected inputs_missing_field for inputs.branch, got %+v", errs[0])
	}
}

func TestValidateInputsAgainstContract_TypeMismatch(t *testing.T) {
	card := &CardRecord{Data: map[string]any{
		"inputs": map[string]any{"count": "int"},
	}}
	errs := validateInputsAgainstContract(card, map[string]any{"count": "not-a-number"})
	if len(errs) != 1 || errs[0].Code != "inputs_type_mismatch" {
		t.Fatalf("expected inputs_type_mismatch, got %+v", errs)
	}
}

// ── channel (b): template_instantiate with inputs validation ──────────────────

func TestTemplateInstantiate_WithInputs_WritesToInstanceGraph(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	createSourceMapWithTask(t, a, ctx, "src-in", "src-task")
	saveTemplate(t, a, ctx, "src-in", "tpl-in")

	// Add a data.inputs contract to the template map card so channel (b)
	// has a schema to validate against.
	addMapInputsContract(t, a, "tpl-in", map[string]any{"repo": "string", "branch": "string"})

	resp, err := a.handleWikiTemplateInstantiate(ctx, domain.WikiTemplateInstantiateReq{
		TemplateMapID: "tpl-in",
		InstanceMapID: "inst-in",
		Inputs:        map[string]any{"repo": "foo/bar", "branch": "main"},
	})
	if err != nil {
		t.Fatalf("template_instantiate: %v", err)
	}
	if resp.InstanceMap.ID != "inst-in" {
		t.Errorf("InstanceMap.ID = %q", resp.InstanceMap.ID)
	}

	g, _ := a.loadWorkflowTopo("inst-in")
	if g.Inputs == nil || g.Inputs["repo"] != "foo/bar" || g.Inputs["branch"] != "main" {
		t.Errorf("instance graph inputs = %+v", g.Inputs)
	}
}

func TestTemplateInstantiate_RejectsInputsFailingContract(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	createSourceMapWithTask(t, a, ctx, "src-bad", "src-task-bad")
	saveTemplate(t, a, ctx, "src-bad", "tpl-bad")
	addMapInputsContract(t, a, "tpl-bad", map[string]any{"repo": "string", "count": "int"})

	_, err := a.handleWikiTemplateInstantiate(ctx, domain.WikiTemplateInstantiateReq{
		TemplateMapID: "tpl-bad",
		InstanceMapID: "inst-bad",
		Inputs:        map[string]any{"repo": "foo/bar"}, // missing count
	})
	if err == nil {
		t.Fatal("expected instantiation to be rejected for missing input")
	}
	if !strings.Contains(err.Error(), "inputs validation failed") {
		t.Errorf("error should mention inputs validation: %v", err)
	}

	// Instance map should NOT be created on validation failure.
	_, rev := a.loadWorkflowTopo("inst-bad")
	if rev != "" {
		t.Error("instance graph should not exist after validation failure")
	}
}

func TestTemplateInstantiate_NoContractAcceptsAnyInputs(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	createSourceMapWithTask(t, a, ctx, "src-free", "src-task-free")
	saveTemplate(t, a, ctx, "src-free", "tpl-free")

	resp, err := a.handleWikiTemplateInstantiate(ctx, domain.WikiTemplateInstantiateReq{
		TemplateMapID: "tpl-free",
		InstanceMapID: "inst-free",
		Inputs:        map[string]any{"anything": 123, "nested": map[string]any{"x": true}},
	})
	if err != nil {
		t.Fatalf("template_instantiate: %v", err)
	}
	if resp.InstanceMap.ID != "inst-free" {
		t.Errorf("InstanceMap.ID = %q", resp.InstanceMap.ID)
	}
	g, _ := a.loadWorkflowTopo("inst-free")
	if g.Inputs["anything"] != float64(123) {
		t.Errorf("instance inputs = %+v", g.Inputs)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func createSourceMapWithTask(t *testing.T, a *Actor, ctx actor.Context, mapID, taskID string) {
	t.Helper()
	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: mapID}); err != nil {
		t.Fatalf("create map %s: %v", mapID, err)
	}
	if _, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID:    mapID,
		Title:    taskID,
		Question: "Q",
	}); err != nil {
		t.Fatalf("create task %s: %v", taskID, err)
	}
}

func saveTemplate(t *testing.T, a *Actor, ctx actor.Context, srcMapID, tplID string) {
	t.Helper()
	if _, err := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{
		MapID:      srcMapID,
		TemplateID: tplID,
	}); err != nil {
		t.Fatalf("template_save %s: %v", tplID, err)
	}
}

// addMapInputsContract stores a data.inputs contract on a workflow map card.
// The contract is a simple key-to-scalar map, which the inline frontmatter
// parser represents as nested data. For tests this matches how a template map
// declares its parameter schema.
func addMapInputsContract(t *testing.T, a *Actor, mapID string, inputs map[string]any) {
	t.Helper()
	card, err := a.store.Get(mapID)
	if err != nil {
		t.Fatalf("get map card %s: %v", mapID, err)
	}
	// Store the nested map as a JSON-encoded string in the data block, just
	// like task_outputs. The decoder will unmarshal it back to map[string]any.
	card.Raw = setDataBlockField(card.Raw, "inputs", mustJSON(inputs))
	if err := a.store.Save(card); err != nil {
		t.Fatalf("save map card %s: %v", mapID, err)
	}
	// Reload so Data is reparsed.
	card, err = a.store.Get(mapID)
	if err != nil {
		t.Fatalf("reload map card %s: %v", mapID, err)
	}
	if _, ok := card.Data["inputs"]; !ok {
		t.Fatalf("data.inputs not parsed for %s; raw=\n%s", mapID, card.Raw)
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("marshal: %v", err))
	}
	return string(b)
}
