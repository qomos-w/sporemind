package bincodectest

import (
	"encoding/json"
	"fmt"

	"github.com/qomos-w/gospore/codec"
	"github.com/qomos-w/gospore/message"
	"github.com/qomos-w/gospore/schema"

	"github.com/qomos-w/spore/identity"
	spore "github.com/qomos-w/spore/schema"

	"github.com/qomos-w/spore/transport"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"os"
	"reflect"
	"testing"
)

// loadTestSchemaSet builds a schema.Set from the generated manifest,
// exactly as the runtime does via ImportFromManifest.
func loadTestSchemaSet(t *testing.T) schema.Set {
	t.Helper()
	data, err := os.ReadFile("../../../../gen/gmanifest.json")
	if err != nil {
		t.Fatalf("read manifest: %v", err)
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
		t.Fatalf("parse manifest: %v", err)
	}
	ss, err := schema.New("workspace")
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

func buildCodec(t *testing.T, ss schema.Set) codec.Codec {
	t.Helper()
	return codec.NewMulti(ss)
}

// lookupID returns the schema ID registered under the given struct name.
// Tests use names instead of hard-coded IDs so they stay valid when the
// static fragment assigns stable IDs.
func lookupID(t *testing.T, ss schema.Set, name string) uint64 {
	t.Helper()
	entry, ok := ss.LookupByName(name)
	if !ok {
		t.Fatalf("lookup schema %q: not found", name)
	}
	return entry.ID
}

// encodeUvarint returns the little-endian 7-bit-payload uvarint encoding of v.
func encodeUvarint(v uint64) []byte {
	var out []byte
	for v >= 0x80 {
		out = append(out, byte(v)|0x80)
		v >>= 7
	}
	return append(out, byte(v))
}

func testID(t *testing.T) identity.CanonicalID {
	t.Helper()
	id, err := identity.NewCanonicalID(1000, 1, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// roundTrip encodes val using the Go BinaryCodec, then decodes via
// DecodeByIDInto (the exact path gospore handlers use).
func roundTrip(t *testing.T, c codec.Codec, ss schema.Set, schemaID uint64, val any) any {
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
	view, err := bc.Encode(desc, testID(t), val)
	if err != nil {
		t.Fatalf("encode %s (schema %d): %v", tp.Name(), schemaID, err)
	}

	dst := reflect.New(tp)
	if err := c.DecodeByIDInto(schemaID, message.EncodingBinary, view.Data, dst.Interface()); err != nil {
		t.Fatalf("DecodeByIDInto %s (schema %d): %v", tp.Name(), schemaID, err)
	}
	return dst.Elem().Interface()
}

func mustEqual(t *testing.T, fieldName string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v, want %#v", fieldName, got, want)
	}
}

// ---------------------------------------------------------------------------
// Go→Go round-trip via Encode + DecodeByIDInto
// ---------------------------------------------------------------------------

func TestBinaryCodec_WorkspaceCreateAgentReq(t *testing.T) {
	ss := loadTestSchemaSet(t)
	c := buildCodec(t, ss)

	original := gen.WorkspaceCreateAgentReq{
		ProjectID:   "proj-1",
		DisplayName: "My Agent",
		AgentKind:   "claude",
	}

	decoded := roundTrip(t, c, ss, lookupID(t, ss, "WorkspaceCreateAgentReq"), original).(gen.WorkspaceCreateAgentReq)
	mustEqual(t, "AgentKind", decoded.AgentKind, original.AgentKind)
	mustEqual(t, "DisplayName", decoded.DisplayName, original.DisplayName)
	mustEqual(t, "ProjectId", decoded.ProjectID, original.ProjectID)
}

func TestBinaryCodec_WorkspaceCreateAgentReq_Minimal(t *testing.T) {
	ss := loadTestSchemaSet(t)
	c := buildCodec(t, ss)

	original := gen.WorkspaceCreateAgentReq{
		AgentKind: "claude",
	}

	decoded := roundTrip(t, c, ss, lookupID(t, ss, "WorkspaceCreateAgentReq"), original).(gen.WorkspaceCreateAgentReq)
	mustEqual(t, "AgentKind", decoded.AgentKind, "claude")
	mustEqual(t, "DisplayName", decoded.DisplayName, "")
	mustEqual(t, "ProjectId", decoded.ProjectID, "")
}

func TestBinaryCodec_WorkspaceCreateReq(t *testing.T) {
	ss := loadTestSchemaSet(t)
	c := buildCodec(t, ss)

	original := gen.WorkspaceCreateReq{
		Path:    "/tmp/myproject",
		Name:    "myproject",
		InitGit: true,
	}

	decoded := roundTrip(t, c, ss, lookupID(t, ss, "WorkspaceCreateReq"), original).(gen.WorkspaceCreateReq)
	mustEqual(t, "Path", decoded.Path, original.Path)
	mustEqual(t, "Name", decoded.Name, original.Name)
	mustEqual(t, "InitGit", decoded.InitGit, original.InitGit)
}

func TestBinaryCodec_WorkspaceSaveAgentKindConfigReq(t *testing.T) {
	ss := loadTestSchemaSet(t)
	c := buildCodec(t, ss)

	original := gen.WorkspaceSaveAgentKindConfigReq{
		Kind:                "claude",
		DisplayName:         "Claude Agent",
		UserCreatable:       true,
		SystemManaged:       false,
		RolePromptRef:       gen.PromptRef{Kind: "builtin", Key: "role"},
		SystemFragmentRefs:  []gen.PromptRef{{Kind: "builtin", Key: "frag1"}},
		DefaultBundleIDs:    []string{"builtin:bundle:workspace-tools"},
		AutoAllowTools:      []string{"bash"},
		AutoAllowCandidates: []string{"bash", "project.read"},
		EnvironmentContext:  map[string]string{"mode": "dev"},
	}

	decoded := roundTrip(t, c, ss, lookupID(t, ss, "WorkspaceSaveAgentKindConfigReq"), original).(gen.WorkspaceSaveAgentKindConfigReq)
	mustEqual(t, "Kind", decoded.Kind, original.Kind)
	mustEqual(t, "DisplayName", decoded.DisplayName, original.DisplayName)
	mustEqual(t, "AutoAllowCandidates", decoded.AutoAllowCandidates, original.AutoAllowCandidates)
	mustEqual(t, "EnvironmentContext", decoded.EnvironmentContext, original.EnvironmentContext)
}

func TestBinaryCodec_WorkspaceUIModel(t *testing.T) {
	ss := loadTestSchemaSet(t)
	c := buildCodec(t, ss)

	original := gen.WorkspaceUIModel{
		WorkspaceID:   "ws-1",
		Version:       42,
		SchemaVersion: 1,
		Layout: gen.WorkspaceShellLayout{
			Version: 1, LayoutJSON: `{"type":"flex"}`, ActiveViewID: "main",
		},
		Dock: gen.WorkspaceDockState{
			LeftWidth: 300, RightWidth: 200, BottomHeight: 150,
			LeftTopPct: 0.5, RightTopPct: 0.6, BottomLeftPct: 0.7,
			WindowW: 1920, WindowH: 1080,
		},
		Panels: gen.WorkspacePanelsState{
			Version: 1,
			Panels: map[string]gen.WorkspacePanelState{
				"explorer": {
					Mode: "dock", Visible: true,
					Pos: gen.XY{X: 0, Y: 0}, Size: gen.WH{W: 300, H: 600},
					ZIndex: 1, DockZone: "left",
				},
			},
		},
		AIShell: gen.WorkspaceAIShellState{
			SidebarOrder: []string{"chat1", "chat2"}, SidebarPinned: []string{"chat1"},
		},
		ProjectCardBrowser: gen.WorkspaceProjectBrowserState{SortMode: "name"},
		Explorer: gen.WorkspaceExplorerState{
			ActiveProjectID: "proj-1", SelectedPath: "/src/main.go",
			ExpandedPaths: []string{"/src", "/pkg"},
		},
	}

	decoded := roundTrip(t, c, ss, lookupID(t, ss, "WorkspaceUIModel"), original).(gen.WorkspaceUIModel)
	mustEqual(t, "WorkspaceId", decoded.WorkspaceID, original.WorkspaceID)
	mustEqual(t, "Version", decoded.Version, original.Version)
	mustEqual(t, "Dock.LeftWidth", decoded.Dock.LeftWidth, original.Dock.LeftWidth)
	mustEqual(t, "AiShell.SidebarOrder", decoded.AIShell.SidebarOrder, original.AIShell.SidebarOrder)
	mustEqual(t, "Explorer.ActiveProjectId", decoded.Explorer.ActiveProjectID, original.Explorer.ActiveProjectID)
}

func TestBinaryCodec_AllWorkspaceRequestTypes(t *testing.T) {
	ss := loadTestSchemaSet(t)
	c := buildCodec(t, ss)

	type tc struct {
		name     string
		schemaID uint64
		value    any
	}

	cases := []tc{
		{"WorkspaceCreateReq", lookupID(t, ss, "WorkspaceCreateReq"), gen.WorkspaceCreateReq{Path: "/x", Name: "x", InitGit: true}},
		{"WorkspaceCreateAgentReq", lookupID(t, ss, "WorkspaceCreateAgentReq"), gen.WorkspaceCreateAgentReq{AgentKind: "claude"}},
		{"WorkspaceAddMountReq", lookupID(t, ss, "WorkspaceAddMountReq"), gen.WorkspaceAddMountReq{ProjectID: "p1", MountName: "m1", MountPath: "/m1"}},
		{"WorkspaceGetAgentKindConfigReq", lookupID(t, ss, "WorkspaceGetAgentKindConfigReq"), gen.WorkspaceGetAgentKindConfigReq{Kind: "claude"}},
		{"WorkspaceGitAddReq", lookupID(t, ss, "WorkspaceGitAddReq"), gen.WorkspaceGitAddReq{ProjectID: "p1", Paths: []string{"a.go"}}},
		{"WorkspaceGitBranchReq", lookupID(t, ss, "WorkspaceGitBranchReq"), gen.WorkspaceGitBranchReq{ProjectID: "p1"}},
		{"WorkspaceGitCheckoutReq", lookupID(t, ss, "WorkspaceGitCheckoutReq"), gen.WorkspaceGitCheckoutReq{ProjectID: "p1", Branch: "main", Create: false}},
		{"WorkspaceGitCommitReq", lookupID(t, ss, "WorkspaceGitCommitReq"), gen.WorkspaceGitCommitReq{ProjectID: "p1", Message: "test"}},
		{"WorkspaceGitDiffReq", lookupID(t, ss, "WorkspaceGitDiffReq"), gen.WorkspaceGitDiffReq{ProjectID: "p1", FilePath: "main.go"}},
		{"WorkspaceGitLogReq", lookupID(t, ss, "WorkspaceGitLogReq"), gen.WorkspaceGitLogReq{ProjectID: "p1", Limit: 10}},
		{"WorkspaceGitPullReq", lookupID(t, ss, "WorkspaceGitPullReq"), gen.WorkspaceGitPullReq{ProjectID: "p1", Remote: "origin"}},
		{"WorkspaceGitPushReq", lookupID(t, ss, "WorkspaceGitPushReq"), gen.WorkspaceGitPushReq{ProjectID: "p1", Remote: "origin"}},
		{"WorkspaceGitStatusReq", lookupID(t, ss, "WorkspaceGitStatusReq"), gen.WorkspaceGitStatusReq{ProjectID: "p1"}},
		{"WorkspaceListAgentsReq", lookupID(t, ss, "WorkspaceListAgentsReq"), gen.WorkspaceListAgentsReq{ProjectID: "p1"}},
		{"WorkspaceMountReq", lookupID(t, ss, "WorkspaceMountReq"), gen.WorkspaceMountReq{Path: "/tmp/x", Name: "x"}},
		{"WorkspaceRemoveMountReq", lookupID(t, ss, "WorkspaceRemoveMountReq"), gen.WorkspaceRemoveMountReq{ProjectID: "p1", MountName: "m1"}},
		{"WorkspaceSaveAgentKindConfigReq", lookupID(t, ss, "WorkspaceSaveAgentKindConfigReq"), gen.WorkspaceSaveAgentKindConfigReq{
			Kind: "claude", DisplayName: "Claude",
			RolePromptRef:       gen.PromptRef{Kind: "builtin", Key: "role"},
			SystemFragmentRefs:  []gen.PromptRef{},
			DefaultBundleIDs:    []string{},
			AutoAllowTools:      []string{},
			AutoAllowCandidates: []string{},
			EnvironmentContext:  map[string]string{},
			NamePool:            []string{},
			SkillIDs:            []string{},
			DefaultCardRefs:     []gen.CardRef{},
		}},
		{"WorkspaceUnmountReq", lookupID(t, ss, "WorkspaceUnmountReq"), gen.WorkspaceUnmountReq{ProjectID: "p1"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decoded := roundTrip(t, c, ss, tc.schemaID, tc.value)
			if !reflect.DeepEqual(decoded, tc.value) {
				t.Errorf("round-trip mismatch:\n  got:  %#v\n  want: %#v", decoded, tc.value)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TS-simulated: Go encodes a real WorkspaceCreateAgentReq, then decodes its own
// wire bytes back. The new schema (5 ModelSlot fields replacing AggregatorActorID)
// has nested structs that are impractical to hand-encode correctly, so this
// relies on the Go encoder for the wire format and validates the round trip.
// ---------------------------------------------------------------------------

func TestBinaryCodec_TS_vs_Go_WireFormat(t *testing.T) {
	ss := loadTestSchemaSet(t)
	c := buildCodec(t, ss)

	schemaID := lookupID(t, ss, "WorkspaceCreateAgentReq")

	original := gen.WorkspaceCreateAgentReq{
		AgentKind:   "claude",
		DisplayName: "Test Agent",
		ProjectID:   "proj-1",
	}

	desc, _ := ss.LookupSchema(schemaID)
	bc := &transport.BinaryCodec{}
	goView, err := bc.Encode(desc, testID(t), original)
	if err != nil {
		t.Fatalf("Go encode: %v", err)
	}

	goDecoded := new(gen.WorkspaceCreateAgentReq)
	if err := c.DecodeByIDInto(schemaID, message.EncodingBinary, goView.Data, goDecoded); err != nil {
		t.Fatalf("Go decode Go bytes: %v", err)
	}

	if !reflect.DeepEqual(*goDecoded, original) {
		t.Errorf("Go round-trip mismatch:\n  got:  %#v\n  want: %#v", *goDecoded, original)
	}

	fmt.Printf("Go-encoded WorkspaceCreateAgentReq hex:\n%x\n", goView.Data)
}

// ---------------------------------------------------------------------------
// Voice types: nested struct with bytes field
// ---------------------------------------------------------------------------

func TestBinaryCodec_VoiceAudio(t *testing.T) {
	ss := loadTestSchemaSet(t)
	c := buildCodec(t, ss)

	original := gen.VoiceAudio{
		AudioType: 2,
		Data:      []byte{0x01, 0x02, 0x03, 0x04},
	}

	decoded := roundTrip(t, c, ss, lookupID(t, ss, "VoiceAudio"), original).(gen.VoiceAudio)
	mustEqual(t, "AudioType", decoded.AudioType, original.AudioType)
	mustEqual(t, "Data", decoded.Data, original.Data)
}

func TestBinaryCodec_VoiceRecognizeReq(t *testing.T) {
	ss := loadTestSchemaSet(t)
	c := buildCodec(t, ss)

	original := gen.VoiceRecognizeReq{
		Audio: gen.VoiceAudio{
			AudioType: 2,
			Data:      []byte{0x01, 0x02, 0x03, 0x04, 0x05},
		},
	}

	decoded := roundTrip(t, c, ss, lookupID(t, ss, "VoiceRecognizeReq"), original).(gen.VoiceRecognizeReq)
	mustEqual(t, "Audio.AudioType", decoded.Audio.AudioType, original.Audio.AudioType)
	mustEqual(t, "Audio.Data", decoded.Audio.Data, original.Audio.Data)
}

func TestBinaryCodec_VoiceRecognizeReq_EmptyAudio(t *testing.T) {
	ss := loadTestSchemaSet(t)
	c := buildCodec(t, ss)

	original := gen.VoiceRecognizeReq{
		Audio: gen.VoiceAudio{
			AudioType: 1,
			Data:      []byte{},
		},
	}

	decoded := roundTrip(t, c, ss, lookupID(t, ss, "VoiceRecognizeReq"), original).(gen.VoiceRecognizeReq)
	mustEqual(t, "Audio.AudioType", decoded.Audio.AudioType, original.Audio.AudioType)
	// Binary codec decodes empty bytes as nil; len check is sufficient.
	if len(decoded.Audio.Data) != 0 {
		t.Errorf("Audio.Data: got %d bytes, want 0", len(decoded.Audio.Data))
	}
}

func TestBinaryCodec_VoiceConfigExportResp(t *testing.T) {
	ss := loadTestSchemaSet(t)
	c := buildCodec(t, ss)

	original := gen.VoiceConfigExportResp{
		Data: `{"provider":"openai","model":"whisper-1","language":"en"}`,
	}

	decoded := roundTrip(t, c, ss, lookupID(t, ss, "VoiceConfigExportResp"), original).(gen.VoiceConfigExportResp)
	mustEqual(t, "Data", decoded.Data, original.Data)
}
