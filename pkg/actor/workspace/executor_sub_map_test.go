package workspace

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/actor/project"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// subMapFakeCtx returns a workspace actor and a fake context wired to answer
// the cross-actor calls a sub_map executor makes. The caller can mutate the
// returned fake stores (cardsByID, templateResult, etc.) before invoking the
// executor.
type subMapTestFixture struct {
	a             *Actor
	ctx           *testutil.FakeCtx
	projectID     string
	agentActorID  string
	callerAgentID string

	cardsByID           map[string]string
	templateResult      domain.WikiTemplateInstantiateResp
	templateInstantiated bool
	templateInputs      map[string]any
	statusSet           map[string]string
	outputsSet          map[string]map[string]any
	outputsValidated    bool
	validateOutputsResp gen.ProjectTaskValidateOutputsResp
	graphByID           map[string]project.WorkflowTopoGraph
}

func newSubMapTestFixture(t *testing.T) *subMapTestFixture {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	f := &subMapTestFixture{
		a:             a,
		ctx:           ctx,
		projectID:     g.Next().String(),
		agentActorID:  g.Next().String(),
		callerAgentID: g.Next().String(),
		cardsByID:     make(map[string]string),
		statusSet:     make(map[string]string),
		outputsSet:    make(map[string]map[string]any),
		validateOutputsResp: gen.ProjectTaskValidateOutputsResp{Valid: true},
		graphByID:     make(map[string]project.WorkflowTopoGraph),
	}
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: f.projectID}}

	f.ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == f.callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
				}
				return nil
			}), true
		}
		if aidStr == f.agentActorID || strings.HasPrefix(aidStr, "agent-") {
			// Owner agent: workflow_start succeeds.
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				if callID == "workflow_start" {
					return domain.AgentWorkflowStartResp{MapCardID: ""}
				}
				return nil
			}), true
		}
		if aidStr == f.projectID {
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				switch callID {
				case "project.wiki_get_card":
					req, ok := payload.(domain.WikiGetCardReq)
					if !ok {
						return nil
					}
					raw, ok := f.cardsByID[req.ID]
					if !ok {
						return fmt.Errorf("card %q not found", req.ID)
					}
					return domain.WikiGetCardResp{ID: req.ID, Raw: raw}
			case "project.wiki_template_instantiate":
				f.templateInstantiated = true
				if req, ok := payload.(domain.WikiTemplateInstantiateReq); ok {
					f.templateInputs = req.Inputs
				}
				return f.templateResult
			case "project.wiki_set_status":
					req, ok := payload.(domain.WikiSetStatusReq)
					if ok {
						f.statusSet[req.ID] = req.Status
					}
					return domain.WikiSetStatusResp{}
				case "project.wiki_set_task_outputs":
					req, ok := payload.(domain.WikiSetTaskOutputsReq)
					if ok {
						f.outputsSet[req.CardID] = req.Outputs
					}
					return domain.WikiSetTaskOutputsResp{}
			case "project.task_validate_outputs":
				f.outputsValidated = true
				return f.validateOutputsResp
				case "project.graph_get":
					req, ok := payload.(gen.ProjectGraphGetReq)
					if !ok {
						return nil
					}
					return gen.ProjectGraphEnvelopeResp{
						EnvelopeText: graphEnvelope(f.graphByID[req.ID]),
					}
				case "project.spawn_agent":
					return domain.ProjectSpawnAgentResp{ActorID: f.agentActorID}
				}
				return nil
			}), true
		}
		return nil, false
	}
	return f
}

func graphEnvelope(g project.WorkflowTopoGraph) string {
	if g.Nodes == nil {
		return "{}"
	}
	b, _ := json.Marshal(g)
	return string(b)
}

func TestSubMapExecutor_Registered(t *testing.T) {
	f := newSubMapTestFixture(t)
	exec, ok := f.a.execRegistry.Lookup(ExecKindSubMap)
	if !ok {
		t.Fatal("sub_map executor not registered")
	}
	if exec.Kind() != ExecKindSubMap {
		t.Fatalf("expected kind sub_map, got %q", exec.Kind())
	}
}

func TestSubMapExecutor_Preflight_RequiresTemplateMapID(t *testing.T) {
	f := newSubMapTestFixture(t)
	e := newSubMapExecutor(f.a)
	req := ClaimReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: "parent-task",
		AgentKind:       domain.AgentKindWorker,
		CardRaw:         "---\nid: parent-task\ntype: task\ndata:\n  exec:\n    kind: sub_map\n---\n",
	}
	if _, err := e.Preflight(f.ctx, req); err == nil {
		t.Fatal("expected error for missing template_map_id")
	} else if !strings.Contains(err.Error(), "template_map_id is required") {
		t.Fatalf("expected template_map_id error, got %v", err)
	}
}

func TestSubMapExecutor_Preflight_RejectsNonTemplate(t *testing.T) {
	f := newSubMapTestFixture(t)
	f.cardsByID["not-a-template"] = "---\nid: not-a-template\ntype: map\n---\n"
	e := newSubMapExecutor(f.a)
	req := ClaimReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: "parent-task",
		AgentKind:       domain.AgentKindWorker,
		CardRaw:         "---\nid: parent-task\ntype: task\ndata:\n  exec:\n    kind: sub_map\n    template_map_id: not-a-template\n---\n",
	}
	if _, err := e.Preflight(f.ctx, req); err == nil {
		t.Fatal("expected error for non-template target")
	} else if !strings.Contains(err.Error(), "not a template") {
		t.Fatalf("expected non-template error, got %v", err)
	}
}

func TestSubMapExecutor_Preflight_DetectsDirectCycle(t *testing.T) {
	f := newSubMapTestFixture(t)
	f.cardsByID["tmpl"] = "---\nid: tmpl\ntype: map\ndata:\n  template: true\n---\n"
	f.graphByID["tmpl"] = project.WorkflowTopoGraph{Nodes: []project.WorkflowTopoNode{{ID: "nested-task"}}}
	f.cardsByID["nested-task"] = "---\nid: nested-task\ntype: task\ndata:\n  exec:\n    kind: sub_map\n    template_map_id: tmpl\n---\n"

	e := newSubMapExecutor(f.a)
	req := ClaimReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: "parent-task",
		AgentKind:       domain.AgentKindWorker,
		CardRaw:         "---\nid: parent-task\ntype: task\ndata:\n  exec:\n    kind: sub_map\n    template_map_id: tmpl\n---\n",
	}
	if _, err := e.Preflight(f.ctx, req); err == nil {
		t.Fatal("expected cycle detection error")
	} else if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestSubMapExecutor_Execute_RecordsInstance(t *testing.T) {
	f := newSubMapTestFixture(t)
	f.cardsByID["tmpl"] = "---\nid: tmpl\ntype: map\ndata:\n  template: true\n---\n"
	e := newSubMapExecutor(f.a)
	pf, err := e.Preflight(f.ctx, ClaimReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: "parent-task",
		AgentKind:       domain.AgentKindWorker,
		CardRaw:         "---\nid: parent-task\ntype: task\ndata:\n  exec:\n    kind: sub_map\n    template_map_id: tmpl\n    inputs:\n      repo: foo/bar\n---\n",
	})
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	resp, err := e.Execute(f.ctx, ClaimReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: "parent-task",
		AgentKind:       domain.AgentKindWorker,
		CardRaw:         "---\nid: parent-task\ntype: task\ndata:\n  exec:\n    kind: sub_map\n    template_map_id: tmpl\n    inputs:\n      repo: foo/bar\n---\n",
		Preflight:       &pf,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.AgentActorID != f.agentActorID {
		t.Fatalf("expected owner actor id %q, got %q", f.agentActorID, resp.AgentActorID)
	}
	if !f.templateInstantiated {
		t.Fatal("expected template instantiation")
	}
	if len(f.a.subMapInstances) != 1 {
		t.Fatalf("expected 1 sub-map instance, got %d", len(f.a.subMapInstances))
	}
	inst := f.a.subMapInstances[0]
	if inst.ParentTaskCardID != "parent-task" {
		t.Errorf("parent task id = %q, want parent-task", inst.ParentTaskCardID)
	}
	if inst.TemplateMapID != "tmpl" {
		t.Errorf("template map id = %q, want tmpl", inst.TemplateMapID)
	}
	if inst.OwnerAgentActorID != f.agentActorID {
		t.Errorf("owner actor id = %q, want %q", inst.OwnerAgentActorID, f.agentActorID)
	}
}

func TestSubMapExecutor_PropagateSuccess_PromotesOutputs(t *testing.T) {
	f := newSubMapTestFixture(t)
	f.a.subMapInstances = append(f.a.subMapInstances, subMapInstance{
		ParentTaskCardID:  "parent-task",
		ParentAgentID:     f.callerAgentID,
		InstanceMapID:     "inst-map",
		OwnerAgentActorID: f.agentActorID,
		ProjectID:         f.projectID,
		TemplateMapID:     "tmpl",
		Status:            "active",
	})
	f.cardsByID["inst-map"] = "---\nid: inst-map\ntype: map\ndata:\n  scope:\n    include:\n      - child-task\n---\n"
	f.cardsByID["child-task"] = `---
id: child-task
type: task
status: done
data:
  task_outputs: '{"report":{"summary":"ok"}}'
---
`

	f.a.handleSubMapOwnerStatus(f.ctx, f.agentActorID, "idle", "", "")

	if f.statusSet["parent-task"] != "done" {
		t.Fatalf("expected parent task marked done, got %q", f.statusSet["parent-task"])
	}
	outs := f.outputsSet["parent-task"]
	if outs == nil {
		t.Fatal("expected outputs promoted to parent task")
	}
	report, ok := outs["report"].(map[string]any)
	if !ok || report["summary"] != "ok" {
		t.Fatalf("expected report.summary=ok, got %v", outs)
	}
}

func TestSubMapExecutor_PropagateFailure(t *testing.T) {
	f := newSubMapTestFixture(t)
	f.a.subMapInstances = append(f.a.subMapInstances, subMapInstance{
		ParentTaskCardID:  "parent-task",
		ParentAgentID:     f.callerAgentID,
		InstanceMapID:     "inst-map",
		OwnerAgentActorID: f.agentActorID,
		ProjectID:         f.projectID,
		TemplateMapID:     "tmpl",
		Status:            "active",
	})

	f.a.handleSubMapOwnerStatus(f.ctx, f.agentActorID, "failed", "", "owner exploded")

	if f.statusSet["parent-task"] != "failed" {
		t.Fatalf("expected parent task marked failed, got %q", f.statusSet["parent-task"])
	}
	if f.a.subMapInstances[0].Status != "failed" {
		t.Fatalf("expected instance status failed, got %q", f.a.subMapInstances[0].Status)
	}
}

// TestSubMapExecutor_Execute_ForwardsInputsToInstance verifies that the
// inputs declared on the parent task card's data.exec block are passed through
// to project.wiki_template_instantiate.
func TestSubMapExecutor_Execute_ForwardsInputsToInstance(t *testing.T) {
	f := newSubMapTestFixture(t)
	f.cardsByID["tmpl"] = "---\nid: tmpl\ntype: map\ndata:\n  template: true\n---\n"
	e := newSubMapExecutor(f.a)
	pf, err := e.Preflight(f.ctx, ClaimReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: "parent-task",
		AgentKind:       domain.AgentKindWorker,
		CardRaw:         "---\nid: parent-task\ntype: task\ndata:\n  exec:\n    kind: sub_map\n    template_map_id: tmpl\n    inputs:\n      repo: foo/bar\n---\n",
	})
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	_, err = e.Execute(f.ctx, ClaimReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: "parent-task",
		AgentKind:       domain.AgentKindWorker,
		CardRaw:         "---\nid: parent-task\ntype: task\ndata:\n  exec:\n    kind: sub_map\n    template_map_id: tmpl\n    inputs:\n      repo: foo/bar\n---\n",
		Preflight:       &pf,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !f.templateInstantiated {
		t.Fatal("expected template instantiation")
	}
	if f.templateInputs == nil {
		t.Fatal("expected inputs forwarded to template_instantiate")
	}
	if f.templateInputs["repo"] != "foo/bar" {
		t.Fatalf("expected repo=foo/bar, got %v", f.templateInputs["repo"])
	}
}

// TestSubMapExecutor_PropagateSuccess_OutputValidationFails verifies that when
// the merged sub-map outputs fail the parent task's data.outputs contract, the
// parent task is marked failed (not done) and the validation error is surfaced.
func TestSubMapExecutor_PropagateSuccess_OutputValidationFails(t *testing.T) {
	f := newSubMapTestFixture(t)
	f.a.subMapInstances = append(f.a.subMapInstances, subMapInstance{
		ParentTaskCardID:  "parent-task",
		ParentAgentID:     f.callerAgentID,
		InstanceMapID:     "inst-map",
		OwnerAgentActorID: f.agentActorID,
		ProjectID:         f.projectID,
		TemplateMapID:     "tmpl",
		Status:            "active",
	})
	f.cardsByID["inst-map"] = "---\nid: inst-map\ntype: map\ndata:\n  scope:\n    include:\n      - child-task\n---\n"
	f.cardsByID["child-task"] = `---
id: child-task
type: task
status: done
data:
  task_outputs: '{"report":{"summary":"ok"}}'
---
`
	f.validateOutputsResp = gen.ProjectTaskValidateOutputsResp{
		Valid: false,
		Errors: []gen.CardValidationError{{
			Code: "outputs_type_mismatch", Field: "outputs.report",
			Message: "expected object matching schema, got string",
		}},
	}

	f.a.handleSubMapOwnerStatus(f.ctx, f.agentActorID, "idle", "", "")

	if f.statusSet["parent-task"] != "failed" {
		t.Fatalf("expected parent task marked failed, got %q", f.statusSet["parent-task"])
	}
	if f.a.subMapInstances[0].Status != "failed" {
		t.Fatalf("expected instance status failed, got %q", f.a.subMapInstances[0].Status)
	}
	if !f.outputsValidated {
		t.Fatal("expected outputs validation to be invoked")
	}
	if f.outputsSet["parent-task"] == nil {
		t.Fatal("expected error outputs written to parent task")
	}
}

func TestSubMapExecutor_SweepPropagatesCancellation(t *testing.T) {
	f := newSubMapTestFixture(t)
	f.a.subMapInstances = append(f.a.subMapInstances, subMapInstance{
		ParentTaskCardID:  "parent-task",
		ParentAgentID:     f.callerAgentID,
		InstanceMapID:     "inst-map",
		OwnerAgentActorID: f.agentActorID,
		ProjectID:         f.projectID,
		TemplateMapID:     "tmpl",
		Status:            "active",
	})
	f.cardsByID["parent-task"] = "---\nid: parent-task\ntype: task\nstatus: cancelled\n---\n"

	f.a.sweepSubMapInstances(f.ctx)

	if f.a.subMapInstances[0].Status != "cancelled" {
		t.Fatalf("expected instance status cancelled, got %q", f.a.subMapInstances[0].Status)
	}
}
