package puppeteditor

import (
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// paramTestActor returns a fresh seeded actor (same fixture as edit tests) and
// applies a convenience param used across the suite: a float "smile" param
// ranged 0..1 with default 0.5.
func paramTestActor(t *testing.T) *Actor {
	t.Helper()
	a := editTestActor(t)
	resp, err := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdParamCreate,
			TargetGuid: "smile",
			Params: map[string]string{
				paramArgName:    "Smile",
				paramArgKind:    "float",
				paramArgDefault: "0.5",
				paramArgMin:     "0",
				paramArgMax:     "1",
				paramArgGroup:   "Face",
			},
			RequestID: "req-create-smile",
		},
	})
	if err != nil || !resp.Applied {
		t.Fatalf("seed param_create failed: err=%v resp=%+v", err, resp)
	}
	return a
}

func mustApply(t *testing.T, a *Actor, cmd gen.PuppetEditCommand) gen.PuppetEditResp {
	t.Helper()
	resp, err := a.handleEdit(nil, gen.PuppetEditReq{Command: cmd})
	if err != nil {
		t.Fatalf("handleEdit(%s): %v", cmd.Kind, err)
	}
	if !resp.Applied {
		t.Fatalf("handleEdit(%s) not applied: %s", cmd.Kind, resp.Detail)
	}
	return resp
}

func mustReject(t *testing.T, a *Actor, cmd gen.PuppetEditCommand) string {
	t.Helper()
	resp, err := a.handleEdit(nil, gen.PuppetEditReq{Command: cmd})
	if err != nil {
		t.Fatalf("handleEdit(%s): unexpected transport error: %v", cmd.Kind, err)
	}
	if resp.Applied {
		t.Fatalf("handleEdit(%s) applied, want rejection", cmd.Kind)
	}
	return resp.Detail
}

// ---------------------------------------------------------------------------
// Param CRUD
// ---------------------------------------------------------------------------

func TestParams_Create_Success(t *testing.T) {
	a := paramTestActor(t)
	p := findParam(&a.state.Document, "smile")
	if p == nil {
		t.Fatal("param smile not stored on document")
	}
	if p.Name != "Smile" || p.Kind != "float" || p.Default != "0.5" || p.Min != "0" || p.Max != "1" || p.Group != "Face" {
		t.Errorf("stored param = %+v", *p)
	}
	if n := len(a.state.Document.Params); n != 1 {
		t.Errorf("len(Params) = %d, want 1", n)
	}
}

func TestParams_Create_DuplicateRejected(t *testing.T) {
	a := paramTestActor(t)
	detail := mustReject(t, a, gen.PuppetEditCommand{
		Kind:       cmdParamCreate,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgName: "Smile2", paramArgKind: "float", paramArgDefault: "0"},
	})
	if !strings.Contains(detail, "already exists") {
		t.Errorf("detail = %q, want already-exists rejection", detail)
	}
}

func TestParams_Create_Validation(t *testing.T) {
	a := editTestActor(t)
	cases := []struct {
		name   string
		params map[string]string
		want   string
	}{
		{"missing name", map[string]string{paramArgKind: "float", paramArgDefault: "0"}, "name"},
		{"unknown kind", map[string]string{paramArgName: "X", paramArgKind: "quaternion", paramArgDefault: "0"}, "unknown kind"},
		{"bad default grammar", map[string]string{paramArgName: "X", paramArgKind: "float", paramArgDefault: "zero"}, "invalid default"},
		{"default below min", map[string]string{paramArgName: "X", paramArgKind: "float", paramArgDefault: "-1", paramArgMin: "0"}, "below min"},
		{"default above max", map[string]string{paramArgName: "X", paramArgKind: "float", paramArgDefault: "2", paramArgMax: "1"}, "above max"},
		{"min above max", map[string]string{paramArgName: "X", paramArgKind: "float", paramArgDefault: "0", paramArgMin: "1", paramArgMax: "0"}, "exceeds max"},
		{"range on bool kind", map[string]string{paramArgName: "X", paramArgKind: "bool", paramArgDefault: "true", paramArgMin: "0"}, "only valid for numeric kinds"},
		{"bad no_inherit", map[string]string{paramArgName: "X", paramArgKind: "bool", paramArgDefault: "true", paramArgNoInherit: "maybe"}, "no_inherit"},
	}
	for _, tc := range cases {
		detail := mustReject(t, a, gen.PuppetEditCommand{
			Kind: cmdParamCreate, TargetGuid: "p-" + tc.name, Params: tc.params,
		})
		if !strings.Contains(detail, tc.want) {
			t.Errorf("%s: detail = %q, want substring %q", tc.name, detail, tc.want)
		}
	}
	if len(a.state.Document.Params) != 0 {
		t.Errorf("rejected creates must not mutate Params, got %d", len(a.state.Document.Params))
	}
	if a.state.Document.Revision != 0 {
		t.Errorf("rejected creates must not bump Revision, got %d", a.state.Document.Revision)
	}
}

func TestParams_Create_KindGrammars(t *testing.T) {
	a := editTestActor(t)
	cases := []struct {
		id, kind, def string
	}{
		{"p-int", "int", "3"},
		{"p-bool", "bool", "true"},
		{"p-vec2", "vec2", "1.5, -2"},
		{"p-color", "color", "#FF8800AA"},
	}
	for _, tc := range cases {
		mustApply(t, a, gen.PuppetEditCommand{
			Kind:       cmdParamCreate,
			TargetGuid: tc.id,
			Params:     map[string]string{paramArgName: tc.id, paramArgKind: tc.kind, paramArgDefault: tc.def},
		})
	}
	// Each grammar also rejects garbage.
	bad := []struct {
		id, kind, def string
	}{
		{"q-int", "int", "1.5"},
		{"q-bool", "bool", "yes"},
		{"q-vec2", "vec2", "1.5"},
		{"q-color", "color", "#FF880"},
	}
	for _, tc := range bad {
		detail := mustReject(t, a, gen.PuppetEditCommand{
			Kind:       cmdParamCreate,
			TargetGuid: tc.id,
			Params:     map[string]string{paramArgName: tc.id, paramArgKind: tc.kind, paramArgDefault: tc.def},
		})
		if !strings.Contains(detail, "invalid default") {
			t.Errorf("%s: detail = %q, want invalid default", tc.id, detail)
		}
	}
}

func TestParams_Update_MergesProvidedFields(t *testing.T) {
	a := paramTestActor(t)
	resp := mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdParamUpdate,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgDefault: "0.8", paramArgGroup: "Mouth"},
	})
	if resp.Revision.CommandKind != cmdParamUpdate {
		t.Errorf("CommandKind = %q, want param_update", resp.Revision.CommandKind)
	}
	p := findParam(&a.state.Document, "smile")
	if p.Default != "0.8" || p.Group != "Mouth" {
		t.Errorf("merged param = %+v, want default 0.8 group Mouth", *p)
	}
	// Untouched fields survive the merge.
	if p.Name != "Smile" || p.Kind != "float" || p.Min != "0" || p.Max != "1" {
		t.Errorf("merge clobbered untouched fields: %+v", *p)
	}
	// An explicit empty string clears an optional field.
	mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdParamUpdate,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgGroup: ""},
	})
	if p.Group != "" {
		t.Errorf("group after clear = %q, want empty", p.Group)
	}
	// Merged model is validated: default outside the surviving range fails.
	detail := mustReject(t, a, gen.PuppetEditCommand{
		Kind:       cmdParamUpdate,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgDefault: "5"},
	})
	if !strings.Contains(detail, "above max") {
		t.Errorf("detail = %q, want above-max rejection", detail)
	}
	// No fields at all is rejected.
	detail = mustReject(t, a, gen.PuppetEditCommand{
		Kind: cmdParamUpdate, TargetGuid: "smile",
	})
	if !strings.Contains(detail, "no fields to update") {
		t.Errorf("detail = %q, want no-fields rejection", detail)
	}
	// Unknown param is rejected.
	mustReject(t, a, gen.PuppetEditCommand{
		Kind: cmdParamUpdate, TargetGuid: "ghost",
		Params: map[string]string{paramArgName: "Ghost"},
	})
}

func TestParams_Remove_CascadesBindingsAndValues(t *testing.T) {
	a := paramTestActor(t)
	mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgAction: bindingActionAdd, paramArgNode: "face", paramArgProperty: "opacity"},
	})
	mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgAction: bindingActionAdd, paramArgNode: "eye-l", paramArgProperty: "opacity"},
	})
	mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamValue,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgValue: "0.9"},
	})

	resp := mustApply(t, a, gen.PuppetEditCommand{Kind: cmdParamRemove, TargetGuid: "smile"})
	// AffectedGuids = the nodes that lost a binding.
	if len(resp.AffectedGuids) != 2 || resp.AffectedGuids[0] != "face" || resp.AffectedGuids[1] != "eye-l" {
		t.Errorf("AffectedGuids = %v, want [face eye-l]", resp.AffectedGuids)
	}
	if len(a.state.Document.Params) != 0 {
		t.Error("param not removed")
	}
	if len(a.state.Document.Bindings) != 0 {
		t.Errorf("bindings not cascaded, %d left", len(a.state.Document.Bindings))
	}
	if _, ok := a.state.Document.ParamValues["smile"]; ok {
		t.Error("explicit value not removed with param")
	}
	// Removing again is rejected.
	mustReject(t, a, gen.PuppetEditCommand{Kind: cmdParamRemove, TargetGuid: "smile"})
}

func TestParams_ParamListCallable(t *testing.T) {
	a := paramTestActor(t)
	mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgAction: bindingActionAdd, paramArgNode: "face", paramArgProperty: "opacity", paramArgMultiplier: "2"},
	})
	resp, err := a.handleParamList(nil, gen.PuppetParamListReq{})
	if err != nil {
		t.Fatalf("handleParamList: %v", err)
	}
	if len(resp.Params) != 1 || resp.Params[0].ID != "smile" {
		t.Errorf("Params = %+v", resp.Params)
	}
	if len(resp.Bindings) != 1 {
		t.Fatalf("Bindings = %+v", resp.Bindings)
	}
	b := resp.Bindings[0]
	if b.ParamID != "smile" || b.NodeGuid != "face" || b.Property != "opacity" || b.Multiplier != 2 {
		t.Errorf("binding = %+v", b)
	}
	// The response shares no mutable reference with actor state.
	resp.Params[0].Name = "mutated"
	if findParam(&a.state.Document, "smile").Name == "mutated" {
		t.Error("param list response aliases actor state")
	}
}

// ---------------------------------------------------------------------------
// set_param_value
// ---------------------------------------------------------------------------

func TestParams_SetValue_SuccessAndAffectedGuids(t *testing.T) {
	a := paramTestActor(t)
	mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgAction: bindingActionAdd, paramArgNode: "face", paramArgProperty: "opacity"},
	})
	resp := mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamValue,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgValue: "0.25"},
	})
	if got := a.state.Document.ParamValues["smile"]; got != "0.25" {
		t.Errorf("ParamValues[smile] = %q, want 0.25", got)
	}
	if len(resp.AffectedGuids) != 1 || resp.AffectedGuids[0] != "face" {
		t.Errorf("AffectedGuids = %v, want [face]", resp.AffectedGuids)
	}
}

func TestParams_SetValue_Validation(t *testing.T) {
	a := paramTestActor(t)
	cases := []struct {
		value, want string
	}{
		{"1.5", "above max"},
		{"-0.5", "below min"},
		{"high", "not a float"},
	}
	for _, tc := range cases {
		detail := mustReject(t, a, gen.PuppetEditCommand{
			Kind:       cmdSetParamValue,
			TargetGuid: "smile",
			Params:     map[string]string{paramArgValue: tc.value},
		})
		if !strings.Contains(detail, tc.want) {
			t.Errorf("value %q: detail = %q, want substring %q", tc.value, detail, tc.want)
		}
	}
	// Missing value param.
	detail := mustReject(t, a, gen.PuppetEditCommand{Kind: cmdSetParamValue, TargetGuid: "smile"})
	if !strings.Contains(detail, "missing") {
		t.Errorf("detail = %q, want missing rejection", detail)
	}
	// Unknown param.
	detail = mustReject(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamValue,
		TargetGuid: "ghost",
		Params:     map[string]string{paramArgValue: "0.5"},
	})
	if !strings.Contains(detail, "not found") {
		t.Errorf("detail = %q, want not-found rejection", detail)
	}
	// A rejection leaves the previous value untouched.
	if _, ok := a.state.Document.ParamValues["smile"]; ok {
		t.Error("rejected set must not write a value")
	}
}

// ---------------------------------------------------------------------------
// Bindings
// ---------------------------------------------------------------------------

func TestParams_Binding_Lifecycle(t *testing.T) {
	a := paramTestActor(t)

	// add: defaults multiplier 1 / offset 0.
	resp := mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgAction: bindingActionAdd, paramArgNode: "face", paramArgProperty: "opacity"},
	})
	if resp.Revision.CommandKind != cmdSetParamBinding {
		t.Errorf("CommandKind = %q", resp.Revision.CommandKind)
	}
	if len(resp.AffectedGuids) != 1 || resp.AffectedGuids[0] != "face" {
		t.Errorf("AffectedGuids = %v, want [face]", resp.AffectedGuids)
	}
	b := a.state.Document.Bindings[0]
	if b.Multiplier != 1 || b.Offset != 0 {
		t.Errorf("add defaults = x%g +%g, want x1 +0", b.Multiplier, b.Offset)
	}

	// add duplicate identity rejected.
	detail := mustReject(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgAction: bindingActionAdd, paramArgNode: "face", paramArgProperty: "opacity"},
	})
	if !strings.Contains(detail, "already bound") {
		t.Errorf("detail = %q, want already-bound rejection", detail)
	}

	// update: provided fields replace, omitted keep — including an explicit 0 multiplier.
	mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgAction: bindingActionUpdate, paramArgNode: "face", paramArgProperty: "opacity", paramArgMultiplier: "0", paramArgOffset: "0.1"},
	})
	b = a.state.Document.Bindings[0]
	if b.Multiplier != 0 || b.Offset != 0.1 {
		t.Errorf("after update = x%g +%g, want x0 +0.1", b.Multiplier, b.Offset)
	}
	mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgAction: bindingActionUpdate, paramArgNode: "face", paramArgProperty: "opacity", paramArgOffset: "-1"},
	})
	b = a.state.Document.Bindings[0]
	if b.Multiplier != 0 || b.Offset != -1 {
		t.Errorf("partial update clobbered multiplier: %+v", b)
	}

	// remove.
	resp = mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgAction: bindingActionRemove, paramArgNode: "face", paramArgProperty: "opacity"},
	})
	if len(a.state.Document.Bindings) != 0 {
		t.Error("binding not removed")
	}
	if len(resp.AffectedGuids) != 1 || resp.AffectedGuids[0] != "face" {
		t.Errorf("remove AffectedGuids = %v, want [face]", resp.AffectedGuids)
	}
	// update/remove of a missing binding rejected.
	mustReject(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgAction: bindingActionRemove, paramArgNode: "face", paramArgProperty: "opacity"},
	})
}

func TestParams_Binding_Validation(t *testing.T) {
	a := paramTestActor(t)
	// Unknown param.
	detail := mustReject(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "ghost",
		Params:     map[string]string{paramArgAction: bindingActionAdd, paramArgNode: "face", paramArgProperty: "opacity"},
	})
	if !strings.Contains(detail, "not found") {
		t.Errorf("detail = %q, want param not found", detail)
	}
	// Unknown node.
	detail = mustReject(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgAction: bindingActionAdd, paramArgNode: "ghost", paramArgProperty: "opacity"},
	})
	if !strings.Contains(detail, "node") {
		t.Errorf("detail = %q, want node not found", detail)
	}
	// Bad action / missing node / missing property / bad multiplier.
	detail = mustReject(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgAction: "toggle", paramArgNode: "face", paramArgProperty: "opacity"},
	})
	if !strings.Contains(detail, "invalid action") {
		t.Errorf("detail = %q, want invalid action", detail)
	}
	mustReject(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgAction: bindingActionAdd, paramArgProperty: "opacity"},
	})
	mustReject(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgAction: bindingActionAdd, paramArgNode: "face"},
	})
	detail = mustReject(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgAction: bindingActionAdd, paramArgNode: "face", paramArgProperty: "opacity", paramArgMultiplier: "double"},
	})
	if !strings.Contains(detail, "multiplier") {
		t.Errorf("detail = %q, want multiplier rejection", detail)
	}
	// Non-numeric param cannot drive a binding.
	mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdParamCreate,
		TargetGuid: "wink",
		Params:     map[string]string{paramArgName: "Wink", paramArgKind: "bool", paramArgDefault: "false"},
	})
	detail = mustReject(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "wink",
		Params:     map[string]string{paramArgAction: bindingActionAdd, paramArgNode: "face", paramArgProperty: "opacity"},
	})
	if !strings.Contains(detail, "numeric kind") {
		t.Errorf("detail = %q, want numeric-kind rejection", detail)
	}
}

// ---------------------------------------------------------------------------
// Audit chain & idempotency
// ---------------------------------------------------------------------------

func TestParams_AuditChain(t *testing.T) {
	a := paramTestActor(t) // already used RequestId req-create-smile
	mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgAction: bindingActionAdd, paramArgNode: "face", paramArgProperty: "opacity"},
	})

	ctx := &testutil.FakeCtx{Identity_: idIdentity("alice", "human")}
	resp, err := a.handleEdit(ctx, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdSetParamValue,
			TargetGuid: "smile",
			Params:     map[string]string{paramArgValue: "0.25"},
		},
	})
	if err != nil {
		t.Fatalf("handleEdit: %v", err)
	}
	if !resp.Applied {
		t.Fatalf("not applied: %s", resp.Detail)
	}
	if resp.Revision.Author != "alice" {
		t.Errorf("Author = %q, want alice (from caller context)", resp.Revision.Author)
	}
	if resp.Revision.CommandKind != cmdSetParamValue {
		t.Errorf("CommandKind = %q", resp.Revision.CommandKind)
	}
	if resp.Revision.ParentRevisionID == "" {
		t.Error("revision not linked to parent")
	}
	// The persisted revision carries AffectedGuids.
	last := a.state.Revisions[len(a.state.Revisions)-1]
	if last.ID != resp.Revision.ID {
		t.Fatalf("persisted head %q != returned %q", last.ID, resp.Revision.ID)
	}
	if len(last.AffectedGuids) != 1 || last.AffectedGuids[0] != "face" {
		t.Errorf("persisted revision AffectedGuids = %v, want [face]", last.AffectedGuids)
	}
	// Revision log exposes the same chain.
	logResp, _ := a.handleRevisionLog(nil, gen.PuppetRevisionLogReq{})
	kinds := map[string]int{}
	for _, r := range logResp.Revisions {
		kinds[r.CommandKind]++
	}
	if kinds[cmdParamCreate] != 1 || kinds[cmdSetParamBinding] != 1 || kinds[cmdSetParamValue] != 1 {
		t.Errorf("revision kinds = %v", kinds)
	}
}

func TestParams_Idempotency_ReplayReturnsOriginalRevision(t *testing.T) {
	a := paramTestActor(t)
	first := mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamValue,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgValue: "0.7"},
		RequestID:  "req-set-1",
	})
	revsBefore := len(a.state.Revisions)

	// Replay with the same RequestId.
	replay, err := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdSetParamValue,
			TargetGuid: "smile",
			Params:     map[string]string{paramArgValue: "0.7"},
			RequestID:  "req-set-1",
		},
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replay.Applied {
		t.Fatalf("replay not applied: %s", replay.Detail)
	}
	if replay.Revision.ID != first.Revision.ID {
		t.Errorf("replay revision %q != original %q", replay.Revision.ID, first.Revision.ID)
	}
	if len(a.state.Revisions) != revsBefore {
		t.Errorf("replay appended a revision: %d -> %d", revsBefore, len(a.state.Revisions))
	}
	// A different RequestId applies again (new revision), even with the same
	// payload — value writes are not deduped by content.
	second := mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamValue,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgValue: "0.7"},
		RequestID:  "req-set-2",
	})
	if second.Revision.ID == first.Revision.ID {
		t.Error("distinct RequestId must produce a distinct revision")
	}
}

// ---------------------------------------------------------------------------
// Snapshot projection
// ---------------------------------------------------------------------------

func TestParams_SnapshotProjection_BoundPropertiesTakeEffect(t *testing.T) {
	a := paramTestActor(t)
	// face opacity <- smile (default 0.5, multiplier 1); face z <- smile*4 (0.5*4=2).
	mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgAction: bindingActionAdd, paramArgNode: "face", paramArgProperty: "opacity"},
	})
	mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgAction: bindingActionAdd, paramArgNode: "face", paramArgProperty: "z", paramArgMultiplier: "4"},
	})

	// Default fallback: no explicit value -> Default 0.5 drives the projection.
	snap, err := a.handleSnapshot(nil, gen.PuppetDocumentSnapshotReq{})
	if err != nil {
		t.Fatalf("handleSnapshot: %v", err)
	}
	face := findNode(&snap.Snapshot.Document.Root, "face")
	if got := face.Params["opacity"]; got != "0.5" {
		t.Errorf("projected opacity = %q, want 0.5 (default)", got)
	}
	if face.Z != 2 { // 0.5 * 4 = 2
		t.Errorf("projected z = %d, want 2", face.Z)
	}

	// An explicit value overrides the default and flows through the remap.
	mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamValue,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgValue: "0.25"},
	})
	snap, _ = a.handleSnapshot(nil, gen.PuppetDocumentSnapshotReq{})
	face = findNode(&snap.Snapshot.Document.Root, "face")
	if got := face.Params["opacity"]; got != "0.25" {
		t.Errorf("projected opacity = %q, want 0.25", got)
	}
	if face.Z != 1 { // 0.25 * 4 = 1
		t.Errorf("projected z = %d, want 1", face.Z)
	}

	// The authored document is NOT projected in place: z stays 2 (the
	// authored value), no opacity leaks into the stored node params.
	authored := findNode(&a.state.Document.Root, "face")
	if authored.Z != 2 {
		t.Errorf("authored z mutated to %d; projection must not write through", authored.Z)
	}
	if authored.Params != nil && authored.Params["opacity"] != "" {
		t.Errorf("authored params mutated: %v", authored.Params)
	}
	// Unbound nodes are untouched.
	if other := findNode(&snap.Snapshot.Document.Root, "eye-l"); other.Params != nil {
		t.Errorf("unbound node params = %v, want nil", other.Params)
	}
}

func TestParams_SnapshotProjection_MultiplierOffsetRemap(t *testing.T) {
	a := paramTestActor(t)
	mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "smile",
		Params: map[string]string{
			paramArgAction: bindingActionAdd, paramArgNode: "face",
			paramArgProperty: "opacity", paramArgMultiplier: "-1", paramArgOffset: "1",
		},
	})
	mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamValue,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgValue: "0.75"},
	})
	snap, _ := a.handleSnapshot(nil, gen.PuppetDocumentSnapshotReq{})
	face := findNode(&snap.Snapshot.Document.Root, "face")
	// 0.75 * -1 + 1 = 0.25
	if got := face.Params["opacity"]; got != "0.25" {
		t.Errorf("projected opacity = %q, want 0.25 (remapped)", got)
	}
}

func TestParams_SnapshotProjection_ZeroMultiplierHonored(t *testing.T) {
	a := paramTestActor(t)
	mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "smile",
		Params: map[string]string{
			paramArgAction: bindingActionAdd, paramArgNode: "face",
			paramArgProperty: "opacity", paramArgMultiplier: "0", paramArgOffset: "0.3",
		},
	})
	mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamValue,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgValue: "1"},
	})
	snap, _ := a.handleSnapshot(nil, gen.PuppetDocumentSnapshotReq{})
	face := findNode(&snap.Snapshot.Document.Root, "face")
	if got := face.Params["opacity"]; got != "0.3" {
		t.Errorf("projected opacity = %q, want 0.3 (explicit zero multiplier)", got)
	}
}

func TestParams_SnapshotProjection_IntParamDrivesZ(t *testing.T) {
	a := editTestActor(t)
	mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdParamCreate,
		TargetGuid: "depth",
		Params:     map[string]string{paramArgName: "Depth", paramArgKind: "int", paramArgDefault: "-3"},
	})
	mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "depth",
		Params:     map[string]string{paramArgAction: bindingActionAdd, paramArgNode: "eyes", paramArgProperty: "z"},
	})
	snap, _ := a.handleSnapshot(nil, gen.PuppetDocumentSnapshotReq{})
	eyes := findNode(&snap.Snapshot.Document.Root, "eyes")
	if eyes.Z != -3 {
		t.Errorf("projected z = %d, want -3 (int default)", eyes.Z)
	}
}

// ---------------------------------------------------------------------------
// Revert integration: param state is part of the revision snapshots
// ---------------------------------------------------------------------------

func TestParams_RevertRestoresParamState(t *testing.T) {
	a := paramTestActor(t) // rev: param_create smile
	mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamBinding,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgAction: bindingActionAdd, paramArgNode: "face", paramArgProperty: "opacity"},
	})
	mustApply(t, a, gen.PuppetEditCommand{
		Kind:       cmdSetParamValue,
		TargetGuid: "smile",
		Params:     map[string]string{paramArgValue: "0.9"},
	})
	// Head: value 0.9 -> projected opacity 0.9.
	snap, _ := a.handleSnapshot(nil, gen.PuppetDocumentSnapshotReq{})
	if got := findNode(&snap.Snapshot.Document.Root, "face").Params["opacity"]; got != "0.9" {
		t.Fatalf("pre-revert projected opacity = %q, want 0.9", got)
	}

	// Revert to the revision right before set_param_value.
	prev := a.state.Revisions[len(a.state.Revisions)-2]
	revResp, err := a.handleRevertTo(nil, gen.PuppetDocumentRevertToReq{RevisionID: prev.ID})
	if err != nil {
		t.Fatalf("handleRevertTo: %v", err)
	}
	if !revResp.Applied {
		t.Fatalf("revert not applied")
	}
	if _, ok := revResp.Document.ParamValues["smile"]; ok {
		t.Error("revert did not restore pre-value state")
	}
	// Projection falls back to Default 0.5 again.
	snap, _ = a.handleSnapshot(nil, gen.PuppetDocumentSnapshotReq{})
	if got := findNode(&snap.Snapshot.Document.Root, "face").Params["opacity"]; got != "0.5" {
		t.Errorf("post-revert projected opacity = %q, want 0.5 (default restored)", got)
	}
	if len(revResp.Document.Bindings) != 1 {
		t.Errorf("revert lost bindings: %+v", revResp.Document.Bindings)
	}
}

// ---------------------------------------------------------------------------
// Whitelist gate
// ---------------------------------------------------------------------------

func TestParams_UnknownKindRejected(t *testing.T) {
	a := editTestActor(t)
	resp, err := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{Kind: "set_param_mesh", TargetGuid: "smile"},
	})
	if err != nil {
		t.Fatalf("handleEdit: %v", err)
	}
	if resp.Applied {
		t.Fatal("unknown kind applied")
	}
	if !strings.Contains(resp.Detail, "unknown command kind") {
		t.Errorf("detail = %q, want unknown command kind", resp.Detail)
	}
}

// Compile-time interface checks for the param command types.
var _ editCommand = paramCreateCmd{}
var _ editCommand = paramUpdateCmd{}
var _ editCommand = paramRemoveCmd{}
var _ editCommand = setParamValueCmd{}
var _ editCommand = setParamBindingCmd{}
