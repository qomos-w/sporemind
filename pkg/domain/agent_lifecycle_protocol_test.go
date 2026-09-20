package domain

// Protocol tests for the agent parent-child lifecycle schema (task card
// "定义父子关系与会话协议"). These validate that the newly defined wire shapes
// (ParentAgentId / LifecycleScope / ConversationTarget / fork transcript
// archive / deletion status / agent_child edge / injected CallerAgentId) are
// registered in the generated manifest and survive the binary + JSON wire
// formats exactly as the runtime encodes them.

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/qomos-w/gospore/codec"
	"github.com/qomos-w/gospore/message"
	"github.com/qomos-w/gospore/schema"
	"github.com/qomos-w/spore/identity"
	spore "github.com/qomos-w/spore/schema"
	"github.com/qomos-w/spore/transport"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// loadProtocolSchemaSet builds a schema.Set from the static schema fragment,
// exactly as the runtime does (pkg/runtime New → protocol.NewManager with
// gen.StaticSchemaFragmentJSON). The fragment — not the cross-app manifest —
// is the complete source of truth for internal schema IDs, including types
// whose callables are registered later (e.g. fork transcript archive/read).
func loadProtocolSchemaSet(t *testing.T) schema.Set {
	t.Helper()
	data, err := os.ReadFile("../../gen/static_schema_fragment.json")
	if err != nil {
		t.Fatalf("read static fragment: %v", err)
	}
	var gm struct {
		Schemas []struct {
			Namespace string           `json:"namespace"`
			SchemaID  uint64           `json:"schemaId"`
			Name      string           `json:"name"`
			Object    spore.ObjectDesc `json:"object"`
		} `json:"schemas"`
	}
	if err := json.Unmarshal(data, &gm); err != nil {
		t.Fatalf("parse static fragment: %v", err)
	}
	ss, err := schema.New("protocol")
	if err != nil {
		t.Fatalf("new set: %v", err)
	}
	for _, s := range gm.Schemas {
		desc := spore.TypeDesc{
			Kind:      s.Object.Kind,
			Name:      s.Object.Name,
			ClassName: s.Object.Name,
			ClassID:   s.SchemaID,
		}
		if err := ss.Register(s.SchemaID, s.Name, desc, s.Object); err != nil {
			if err == schema.ErrSchemaIDTaken || err == schema.ErrSchemaNameTaken {
				continue
			}
			t.Fatalf("register %s/%d: %v", s.Namespace, s.SchemaID, err)
		}
	}
	return ss
}

func protocolCodec(t *testing.T, ss schema.Set) codec.Codec {
	t.Helper()
	return codec.NewMulti(ss)
}

// lookupProtocolID returns the schema ID registered under the given struct
// name. Tests use names instead of hard-coded IDs so they stay valid when the
// manifest assigns stable IDs.
func lookupProtocolID(t *testing.T, ss schema.Set, name string) uint64 {
	t.Helper()
	entry, ok := ss.LookupByName(name)
	if !ok {
		t.Fatalf("lookup schema %q: not found", name)
	}
	return entry.ID
}

func protocolTestID(t *testing.T) identity.CanonicalID {
	t.Helper()
	id, err := identity.NewCanonicalID(1000, 1, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// protocolRoundTrip encodes val with the binary wire codec, then decodes via
// DecodeByIDInto (the exact path gospore handlers use).
func protocolRoundTrip(t *testing.T, c codec.Codec, ss schema.Set, schemaID uint64, val any) any {
	t.Helper()
	rv := reflect.ValueOf(val)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	tp := rv.Type()
	desc, ok := ss.LookupSchema(schemaID)
	if !ok {
		t.Fatalf("lookup schema %d: not found", schemaID)
	}
	bc := &transport.BinaryCodec{}
	view, err := bc.Encode(desc, protocolTestID(t), val)
	if err != nil {
		t.Fatalf("encode %s (schema %d): %v", tp.Name(), schemaID, err)
	}
	dst := reflect.New(tp)
	if err := c.DecodeByIDInto(schemaID, message.EncodingBinary, view.Data, dst.Interface()); err != nil {
		t.Fatalf("DecodeByIDInto %s (schema %d): %v", tp.Name(), schemaID, err)
	}
	return dst.Elem().Interface()
}

func protocolMustEqual(t *testing.T, fieldName string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v, want %#v", fieldName, got, want)
	}
}

// TestProtocol_AgentRefLifecycleFields covers ParentAgentId, LifecycleScope,
// DeletionStatus and the read-only Children projection on AgentRef.
func TestProtocol_AgentRefLifecycleFields(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	ref := gen.AgentRef{
		ID:              "agent-1",
		ActorID:         "0123456789abcdef0123456789abcdef",
		ProjectID:       "p1",
		DisplayName:     "Worker",
		AgentKind:       "worker",
		Status:          "active",
		ParentAgentID:   "parent-actor-id",
		LifecycleScope:  "workflow",
		DeletionStatus:  "deleting",
		Children: []gen.AgentChildRef{
			{
				ID:                 "child-1",
				ActorID:            "child-actor-id",
				ParentAgentID:      "parent-actor-id",
				DisplayName:        "Explorer",
				AgentKind:          "worker",
				LifecycleScope:     "turn",
				Status:             "running",
				ConversationTarget: "actor:child-actor-id",
				ToolUseID:          "tooluse-1",
			},
			{
				ID:                 "child-2",
				ActorID:            "child-actor-id-2",
				ParentAgentID:      "parent-actor-id",
				DisplayName:        "Reviewer",
				AgentKind:          "reviewer",
				LifecycleScope:     "turn",
				Status:             "running",
				ConversationTarget: "actor:child-actor-id-2",
				ToolUseID:          "tooluse-2",
				LastActivity:       "2026-08-06T00:00:00Z",
			},
		},
	}

	decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "AgentRef"), ref).(gen.AgentRef)
	protocolMustEqual(t, "ParentAgentID", decoded.ParentAgentID, ref.ParentAgentID)
	protocolMustEqual(t, "LifecycleScope", decoded.LifecycleScope, ref.LifecycleScope)
	protocolMustEqual(t, "DeletionStatus", decoded.DeletionStatus, ref.DeletionStatus)
	protocolMustEqual(t, "Children", decoded.Children, ref.Children)
	protocolMustEqual(t, "Children[0].ConversationTarget", decoded.Children[0].ConversationTarget, "actor:child-actor-id")
	protocolMustEqual(t, "Children[1].ConversationTarget", decoded.Children[1].ConversationTarget, "actor:child-actor-id-2")
	protocolMustEqual(t, "Children[1].LifecycleScope", decoded.Children[1].LifecycleScope, "turn")
}

// TestProtocol_AgentChildRefProjection verifies the unified child projection
// shape (workflow + fork children) round-trips standalone.
func TestProtocol_AgentChildRefProjection(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	child := gen.AgentChildRef{
		ID:                 "child-1",
		ActorID:            "child-actor-id",
		ParentAgentID:      "parent-actor-id",
		DisplayName:        "Coder",
		AgentKind:          "coder",
		LifecycleScope:     "workflow",
		Status:             "deleting",
		ConversationTarget: "actor:child-actor-id",
	}
	decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "AgentChildRef"), child).(gen.AgentChildRef)
	protocolMustEqual(t, "ActorID", decoded.ActorID, child.ActorID)
	protocolMustEqual(t, "ParentAgentID", decoded.ParentAgentID, child.ParentAgentID)
	protocolMustEqual(t, "LifecycleScope", decoded.LifecycleScope, "workflow")
	protocolMustEqual(t, "Status", decoded.Status, "deleting")
	protocolMustEqual(t, "ConversationTarget", decoded.ConversationTarget, "actor:child-actor-id")
}

// TestProtocol_AgentListItemLifecycleFields covers the workspace agent list
// projection additions consumed by the sidebar/topology tree.
func TestProtocol_AgentListItemLifecycleFields(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	item := gen.AgentListItem{
		ID:                 "agent-1",
		ActorID:            "0123456789abcdef0123456789abcdef",
		DisplayName:        "Worker",
		AgentKind:          "worker",
		ProjectID:          "p1",
		ParentAgentID:      "parent-actor-id",
		LifecycleScope:     "workflow",
		ConversationTarget: "actor:0123456789abcdef0123456789abcdef",
		DeletionStatus:     "",
		Children: []gen.AgentChildRef{
			{
				ID:                 "fork-1",
				ActorID:            "fork-actor-id",
				ParentAgentID:      "0123456789abcdef0123456789abcdef",
				DisplayName:        "Explorer",
				AgentKind:          "worker",
				LifecycleScope:     "turn",
				Status:             "running",
				ConversationTarget: "actor:fork-actor-id",
			},
		},
		CanDelete: true,
	}
	decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "AgentListItem"), item).(gen.AgentListItem)
	protocolMustEqual(t, "ParentAgentID", decoded.ParentAgentID, item.ParentAgentID)
	protocolMustEqual(t, "LifecycleScope", decoded.LifecycleScope, item.LifecycleScope)
	protocolMustEqual(t, "ConversationTarget", decoded.ConversationTarget, item.ConversationTarget)
	protocolMustEqual(t, "Children", decoded.Children, item.Children)
	protocolMustEqual(t, "CanDelete", decoded.CanDelete, true)
}

// TestProtocol_AgentChildEdge covers the agent_child edge shape on the unified
// graph: kind + lifecycle scope + child status.
func TestProtocol_AgentChildEdge(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	edge := gen.UnifiedGraphEdge{
		ID:             "e:child-1",
		From:           "parent-actor-id",
		To:             "child-actor-id",
		Kind:           "agent_child",
		LifecycleScope: "workflow",
		Status:         "running",
	}
	decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "UnifiedGraphEdge"), edge).(gen.UnifiedGraphEdge)
	protocolMustEqual(t, "Kind", decoded.Kind, "agent_child")
	protocolMustEqual(t, "LifecycleScope", decoded.LifecycleScope, "workflow")
	protocolMustEqual(t, "Status", decoded.Status, "running")

	// Archived fork child: the same node ID is reused across live→archive so
	// the topology edge flips to the parent-owned archive target without a jump.
	archived := gen.UnifiedGraphEdge{
		ID:             "e:child-1",
		From:           "parent-actor-id",
		To:             "child-actor-id",
		Kind:           "agent_child",
		LifecycleScope: "turn",
		Status:         "archived",
	}
	decodedArchived := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "UnifiedGraphEdge"), archived).(gen.UnifiedGraphEdge)
	protocolMustEqual(t, "LifecycleScope", decodedArchived.LifecycleScope, "turn")
	protocolMustEqual(t, "Status", decodedArchived.Status, "archived")
}

// TestProtocol_AgentChildRefEmptyOmitted verifies that the optional projection
// fields on AgentChildRef (ParentAgentId, LifecycleScope, Status,
// ConversationTarget, DisplayName, AgentKind) are omitted from JSON when empty,
// matching the optional semantics of the same fields on AgentRef/AgentListItem.
func TestProtocol_AgentChildRefEmptyOmitted(t *testing.T) {
	child := gen.AgentChildRef{
		ID:      "child-1",
		ActorID: "child-actor-id",
	}
	data, err := json.Marshal(child)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	// Identity fields are required and must always be present.
	for _, k := range []string{"Id", "ActorId"} {
		if _, ok := m[k]; !ok {
			t.Errorf("AgentChildRef JSON missing required field %q (raw: %s)", k, data)
		}
	}
	// Optional projection fields must be omitted when empty.
	for _, k := range []string{"ParentAgentId", "DisplayName", "AgentKind", "LifecycleScope", "Status", "ConversationTarget", "ToolUseId", "LastActivity"} {
		if _, ok := m[k]; ok {
			t.Errorf("AgentChildRef JSON should omit empty field %q (raw: %s)", k, data)
		}
	}

	// Same for the binary wire: the child ref must encode/decode with only the
	// identity fields set (optional strings come back empty, not as errors).
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)
	decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "AgentChildRef"), child).(gen.AgentChildRef)
	protocolMustEqual(t, "ID", decoded.ID, "child-1")
	protocolMustEqual(t, "ActorID", decoded.ActorID, "child-actor-id")
	protocolMustEqual(t, "ParentAgentID", decoded.ParentAgentID, "")
	protocolMustEqual(t, "LifecycleScope", decoded.LifecycleScope, "")
	protocolMustEqual(t, "Status", decoded.Status, "")
	protocolMustEqual(t, "ConversationTarget", decoded.ConversationTarget, "")
}

// TestProtocol_CallerAgentID covers the turn-engine-injected caller identity on
// the workflow lifecycle requests (spawn / assign / terminate / review) that
// gate parent-only authorization.
func TestProtocol_CallerAgentID(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	spawn := gen.WorkspaceAgentSpawnAssignReq{
		AgentKind:      "worker",
		CallerAgentID:  "parent-actor-id",
		InterpretedGoal: "spawn a worker",
	}
	decodedSpawn := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "WorkspaceAgentSpawnAssignReq"), spawn).(gen.WorkspaceAgentSpawnAssignReq)
	protocolMustEqual(t, "AgentKind", decodedSpawn.AgentKind, "worker")
	protocolMustEqual(t, "CallerAgentID", decodedSpawn.CallerAgentID, "parent-actor-id")

	assign := gen.WorkspaceAgentAssignReq{
		AgentActorID:    "child-actor-id",
		BoundTaskCardID: "card-1",
		CallerAgentID:   "parent-actor-id",
	}
	decodedAssign := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "WorkspaceAgentAssignReq"), assign).(gen.WorkspaceAgentAssignReq)
	protocolMustEqual(t, "AgentActorID", decodedAssign.AgentActorID, "child-actor-id")
	protocolMustEqual(t, "CallerAgentID", decodedAssign.CallerAgentID, "parent-actor-id")

	review := gen.WorkspaceAgentReviewReq{
		AgentActorID:  "child-actor-id",
		Decision:      "approve",
		TaskCardID:    "card-1",
		CallerAgentID: "parent-actor-id",
	}
	decodedReview := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "WorkspaceAgentReviewReq"), review).(gen.WorkspaceAgentReviewReq)
	protocolMustEqual(t, "CallerAgentID", decodedReview.CallerAgentID, "parent-actor-id")

	term := gen.WorkspaceAgentTerminateReq{
		AgentActorID:  "child-actor-id",
		Reason:        "failed",
		CallerAgentID: "parent-actor-id",
	}
	decodedTerm := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "WorkspaceAgentTerminateReq"), term).(gen.WorkspaceAgentTerminateReq)
	protocolMustEqual(t, "CallerAgentID", decodedTerm.CallerAgentID, "parent-actor-id")
}

// TestProtocol_JSONWireNames locks the exact JSON wire names so Go and the
// generated TypeScript clients agree on the same fields.
func TestProtocol_JSONWireNames(t *testing.T) {
	ref := gen.AgentRef{
		ParentAgentID:  "parent",
		LifecycleScope: "workflow",
		DeletionStatus: "deleting",
		Children: []gen.AgentChildRef{
			{ID: "c1", ActorID: "a1", ParentAgentID: "parent", DisplayName: "C", AgentKind: "worker", LifecycleScope: "turn", Status: "running", ConversationTarget: "actor:a1"},
		},
	}
	data, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"ParentAgentId", "LifecycleScope", "DeletionStatus", "Children"} {
		if _, ok := m[k]; !ok {
			t.Errorf("AgentRef JSON missing field %q (raw: %s)", k, data)
		}
	}
	children, ok := m["Children"].([]any)
	if !ok || len(children) == 0 {
		t.Fatalf("AgentRef JSON Children = %#v", m["Children"])
	}
	child := children[0].(map[string]any)
	for _, k := range []string{"Id", "ActorId", "ParentAgentId", "DisplayName", "AgentKind", "LifecycleScope", "Status", "ConversationTarget"} {
		if _, ok := child[k]; !ok {
			t.Errorf("AgentChildRef JSON missing field %q", k)
		}
	}

	// CallerAgentId on workflow lifecycle requests.
	term := gen.WorkspaceAgentTerminateReq{AgentActorID: "c", CallerAgentID: "p"}
	tdata, err := json.Marshal(term)
	if err != nil {
		t.Fatal(err)
	}
	var tm map[string]any
	if err := json.Unmarshal(tdata, &tm); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"AgentActorId", "CallerAgentId"} {
		if _, ok := tm[k]; !ok {
			t.Errorf("WorkspaceAgentTerminateReq JSON missing field %q", k)
		}
	}
}
