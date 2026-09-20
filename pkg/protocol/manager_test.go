package protocol

import (
	"encoding/json"
	"testing"

	spore "github.com/qomos-w/spore/schema"
)

func TestManager_WireID_Resolve(t *testing.T) {
	fragment := StaticFragment{
		NamespaceOffsets: map[string]uint64{
			SystemNamespace: 0,
			"external":      0x200000,
		},
		Schemas: []StaticSchemaEntry{
			{Namespace: SystemNamespace, SchemaID: 170, Name: "AgentChatSubmitReq", Object: spore.ObjectDesc{Kind: spore.TypeKindStruct, Name: "AgentChatSubmitReq"}},
			{Namespace: SystemNamespace, SchemaID: 171, Name: "AgentChatSubmitResp", Object: spore.ObjectDesc{Kind: spore.TypeKindStruct, Name: "AgentChatSubmitResp"}},
		},
	}

	m, err := NewManager(fragment)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Internal namespace: wire ID equals local ID in Phase 1.
	id, err := m.WireID(SystemNamespace, 170)
	if err != nil {
		t.Fatalf("WireID: %v", err)
	}
	if id != 170 {
		t.Errorf("WireID(system, 170) = %d, want 170", id)
	}

	ns, local, err := m.Resolve(170)
	if err != nil {
		t.Fatalf("Resolve(170): %v", err)
	}
	if ns != SystemNamespace || local != 170 {
		t.Errorf("Resolve(170) = (%s, %d), want (system, 170)", ns, local)
	}

	// External namespace: wire ID includes high-bit offset.
	id, err = m.WireID("external", 170)
	if err != nil {
		t.Fatalf("WireID external: %v", err)
	}
	if id != 0x2000AA {
		t.Errorf("WireID(external, 170) = %d, want 0x2000AA", id)
	}

	ns, local, err = m.Resolve(0x2000AA)
	if err != nil {
		t.Fatalf("Resolve(0x2000AA): %v", err)
	}
	if ns != "external" || local != 170 {
		t.Errorf("Resolve(0x2000AA) = (%s, %d), want (external, 170)", ns, local)
	}
}

func TestManager_ReservedLocalID(t *testing.T) {
	fragment := StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
		Schemas: []StaticSchemaEntry{
			{Namespace: SystemNamespace, SchemaID: 64, Name: "Bad", Object: spore.ObjectDesc{Kind: spore.TypeKindStruct, Name: "Bad"}},
		},
	}
	if _, err := NewManager(fragment); err == nil {
		t.Fatal("expected error for reserved local id")
	}
}

func TestManager_DuplicateWireID(t *testing.T) {
	fragment := StaticFragment{
		NamespaceOffsets: map[string]uint64{
			SystemNamespace: 0,
			"other":         0,
		},
		Schemas: []StaticSchemaEntry{
			{Namespace: SystemNamespace, SchemaID: 170, Name: "A", Object: spore.ObjectDesc{Kind: spore.TypeKindStruct, Name: "A"}},
			{Namespace: "other", SchemaID: 170, Name: "B", Object: spore.ObjectDesc{Kind: spore.TypeKindStruct, Name: "B"}},
		},
	}
	if _, err := NewManager(fragment); err == nil {
		t.Fatal("expected error for duplicate wire id across shared-offset namespaces")
	}
}

func TestManager_RegisterExternal(t *testing.T) {
	fragment := StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
		Schemas: []StaticSchemaEntry{
			{Namespace: SystemNamespace, SchemaID: 170, Name: "A", Object: spore.ObjectDesc{Kind: spore.TypeKindStruct, Name: "A"}},
		},
	}
	m, err := NewManager(fragment)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.RegisterExternalWithOffset("peer", 0x400000, map[string]uint64{"TurnInput": 890}); err != nil {
		t.Fatalf("RegisterExternalWithOffset: %v", err)
	}

	id, err := m.WireID("peer", 890)
	if err != nil {
		t.Fatalf("WireID peer: %v", err)
	}
	if id != 0x40037A {
		t.Errorf("WireID(peer, 890) = %d, want 0x40037A", id)
	}

	ns, local, err := m.Resolve(0x40037A)
	if err != nil {
		t.Fatalf("Resolve(0x40037A): %v", err)
	}
	if ns != "peer" || local != 890 {
		t.Errorf("Resolve(0x40037A) = (%s, %d), want (peer, 890)", ns, local)
	}
}

func TestManager_ToManifestImportJSON(t *testing.T) {
	fragment := StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
		Schemas: []StaticSchemaEntry{
			{Namespace: SystemNamespace, SchemaID: 170, Name: "AgentChatSubmitReq", Object: spore.ObjectDesc{Kind: spore.TypeKindStruct, Name: "AgentChatSubmitReq"}},
		},
	}
	m, err := NewManager(fragment)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	data, err := m.ToManifestImportJSON()
	if err != nil {
		t.Fatalf("ToManifestImportJSON: %v", err)
	}
	var manifest spore.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if len(manifest.Schemas) != 1 {
		t.Fatalf("expected 1 schema, got %d", len(manifest.Schemas))
	}
	if manifest.Schemas[0].SchemaID != 170 {
		t.Errorf("manifest schema id = %d, want 170", manifest.Schemas[0].SchemaID)
	}
}

func TestManager_RegisterUser(t *testing.T) {
	fragment := StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
		Schemas: []StaticSchemaEntry{
			{Namespace: SystemNamespace, SchemaID: 170, Name: "A", Object: spore.ObjectDesc{Kind: spore.TypeKindStruct, Name: "A"}},
		},
	}
	m, err := NewManager(fragment)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.RegisterUser(map[string]uint64{"CustomTurnInput": 1000}); err != nil {
		t.Fatalf("RegisterUser: %v", err)
	}

	id, err := m.WireID(UserNamespace, 1000)
	if err != nil {
		t.Fatalf("WireID user: %v", err)
	}
	if id != 0x2003E8 {
		t.Errorf("WireID(user, 1000) = %d, want 0x2003E8", id)
	}

	ns, local, err := m.Resolve(0x2003E8)
	if err != nil {
		t.Fatalf("Resolve(0x2003E8): %v", err)
	}
	if ns != UserNamespace || local != 1000 {
		t.Errorf("Resolve(0x2003E8) = (%s, %d), want (user, 1000)", ns, local)
	}
}

func TestManager_RegisterExternalAutoAlloc(t *testing.T) {
	fragment := StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
		Schemas: []StaticSchemaEntry{
			{Namespace: SystemNamespace, SchemaID: 170, Name: "A", Object: spore.ObjectDesc{Kind: spore.TypeKindStruct, Name: "A"}},
		},
	}
	m, err := NewManager(fragment)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.RegisterExternal("peer", map[string]uint64{"TurnInput": 890}); err != nil {
		t.Fatalf("RegisterExternal: %v", err)
	}

	id, err := m.WireID("peer", 890)
	if err != nil {
		t.Fatalf("WireID peer: %v", err)
	}
	if id != 0x40037A {
		t.Errorf("WireID(peer, 890) = %d, want 0x40037A", id)
	}
}

func TestManager_UnregisterExternal(t *testing.T) {
	fragment := StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
		Schemas: []StaticSchemaEntry{
			{Namespace: SystemNamespace, SchemaID: 170, Name: "A", Object: spore.ObjectDesc{Kind: spore.TypeKindStruct, Name: "A"}},
		},
	}
	m, err := NewManager(fragment)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Register an external namespace with explicit offset.
	if err := m.RegisterExternalWithOffset("peer", 0x400000, map[string]uint64{"TurnInput": 890}); err != nil {
		t.Fatalf("RegisterExternalWithOffset: %v", err)
	}

	// Verify lookup works before unregister.
	id, err := m.WireID("peer", 890)
	if err != nil {
		t.Fatalf("WireID peer: %v", err)
	}
	if id != 0x40037A {
		t.Errorf("WireID(peer, 890) = 0x%x, want 0x40037A", id)
	}

	// Unregister the namespace.
	if err := m.UnregisterExternal("peer"); err != nil {
		t.Fatalf("UnregisterExternal: %v", err)
	}

	// After unregister, WireID should fail.
	if _, err := m.WireID("peer", 890); err == nil {
		t.Fatal("expected error after unregistering namespace")
	}

	// After unregister, Resolve should fail for the old wire ID.
	if _, _, err := m.Resolve(0x40037A); err == nil {
		t.Fatal("expected Resolve to fail after unregister")
	}

	// Re-registering the same namespace should reuse the released offset.
	if err := m.RegisterExternalWithOffset("peer", 0x400000, map[string]uint64{"TurnInput": 890}); err != nil {
		t.Fatalf("RegisterExternalWithOffset after unregister: %v", err)
	}
	id, err = m.WireID("peer", 890)
	if err != nil {
		t.Fatalf("WireID peer after re-register: %v", err)
	}
	if id != 0x40037A {
		t.Errorf("WireID(peer, 890) after re-register = 0x%x, want 0x40037A", id)
	}
}

func TestManager_UnregisterExternal_ReservedNamespaces(t *testing.T) {
	m, err := NewManager(StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.UnregisterExternal(SystemNamespace); err == nil {
		t.Fatal("expected error unregistering system namespace")
	}
	if err := m.UnregisterExternal(UserNamespace); err == nil {
		t.Fatal("expected error unregistering user namespace")
	}
}

func TestManager_UnregisterExternal_Idempotent(t *testing.T) {
	m, err := NewManager(StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Unregistering a namespace that was never registered should not error.
	if err := m.UnregisterExternal("not-registered"); err != nil {
		t.Fatalf("UnregisterExternal on unknown namespace: %v", err)
	}
}

func TestManager_PeerCrossApp(t *testing.T) {
	fragment := StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
		Schemas: []StaticSchemaEntry{
			{Namespace: SystemNamespace, SchemaID: 170, Name: "A", Object: spore.ObjectDesc{Kind: spore.TypeKindStruct, Name: "A"}},
		},
	}
	m, err := NewManager(fragment)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// App A connects to app B. App B exposes namespace "appB" with local schemas.
	// App A assigns offset 0x400000 to appB's namespace for sending/receiving.
	if err := m.RegisterPeer("appB", []PeerNamespace{
		{Namespace: "appB", Offset: 0x400000, Schemas: map[string]uint64{"TurnInput": 890}},
	}); err != nil {
		t.Fatalf("RegisterPeer: %v", err)
	}

	// App A encodes a request to app B using app B's wire ID.
	wireID, err := m.WireIDForPeer("appB", "appB", 890)
	if err != nil {
		t.Fatalf("WireIDForPeer: %v", err)
	}
	if wireID != 0x40037A {
		t.Errorf("WireIDForPeer = 0x%x, want 0x40037A", wireID)
	}

	// App A receives a reply from app B with the same peer-relative wire ID.
	ns, local, err := m.ResolveForPeer("appB", 0x40037A)
	if err != nil {
		t.Fatalf("ResolveForPeer: %v", err)
	}
	if ns != "appB" || local != 890 {
		t.Errorf("ResolveForPeer = (%s, %d), want (appB, 890)", ns, local)
	}

	// App A's own "appB" lookup (if it also imported the name) is separate.
	if got := m.LookupPeerLocal("appB", "appB", "TurnInput"); got != 890 {
		t.Errorf("LookupPeerLocal = %d, want 890", got)
	}
}

func TestManager_PeerMultipleNamespaces(t *testing.T) {
	fragment := StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
		Schemas:          []StaticSchemaEntry{},
	}
	m, err := NewManager(fragment)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// App B exposes two service namespaces to App A.
	if err := m.RegisterPeer("appB", []PeerNamespace{
		{Namespace: "appB.chat", Offset: 0x400000, Schemas: map[string]uint64{"TurnInput": 1000}},
		{Namespace: "appB.common", Offset: 0x600000, Schemas: map[string]uint64{"CommonType": 500}},
	}); err != nil {
		t.Fatalf("RegisterPeer: %v", err)
	}

	// Wire IDs fall into different offset slots.
	chatWire, err := m.WireIDForPeer("appB", "appB.chat", 1000)
	if err != nil {
		t.Fatalf("WireIDForPeer chat: %v", err)
	}
	if chatWire != 0x4003E8 {
		t.Errorf("chat wire = 0x%x, want 0x4003E8", chatWire)
	}

	commonWire, err := m.WireIDForPeer("appB", "appB.common", 500)
	if err != nil {
		t.Fatalf("WireIDForPeer common: %v", err)
	}
	if commonWire != 0x6001F4 {
		t.Errorf("common wire = 0x%x, want 0x6001F4", commonWire)
	}

	// Resolve by wire ID automatically returns the right namespace.
	ns, local, err := m.ResolveForPeer("appB", 0x4003E8)
	if err != nil {
		t.Fatalf("ResolveForPeer chat: %v", err)
	}
	if ns != "appB.chat" || local != 1000 {
		t.Errorf("ResolveForPeer chat = (%s, %d), want (appB.chat, 1000)", ns, local)
	}

	ns, local, err = m.ResolveForPeer("appB", 0x6001F4)
	if err != nil {
		t.Fatalf("ResolveForPeer common: %v", err)
	}
	if ns != "appB.common" || local != 500 {
		t.Errorf("ResolveForPeer common = (%s, %d), want (appB.common, 500)", ns, local)
	}
}

func TestManager_RegisterSporemindPeer_SameVersionSharesSystem(t *testing.T) {
	m, err := NewManager(StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
		Schemas: []StaticSchemaEntry{
			{Namespace: SystemNamespace, SchemaID: 170, Name: "AgentChatSubmitReq", Object: spore.ObjectDesc{Kind: spore.TypeKindStruct, Name: "AgentChatSubmitReq"}},
		},
	}, WithLocalVersion("0.04"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.RegisterSporemindPeer("peerA", "0.04"); err != nil {
		t.Fatalf("RegisterSporemindPeer: %v", err)
	}

	ver, shared, ok := m.IsSporemindPeer("peerA")
	if !ok {
		t.Fatal("expected peerA to be registered")
	}
	if ver != "0.04" {
		t.Errorf("version = %q, want 0.04", ver)
	}
	if !shared {
		t.Fatal("expected shared system namespace for same version")
	}

	// Wire ID for an internal service name resolves through system.
	wireID, err := m.WireIDWithServiceName("peerA", "agent.chat", 170)
	if err != nil {
		t.Fatalf("WireIDWithServiceName: %v", err)
	}
	if wireID != 170 {
		t.Errorf("wireID = %d, want 170", wireID)
	}

	ns, local, err := m.ResolveWithServiceName("peerA", "agent.chat", 170)
	if err != nil {
		t.Fatalf("ResolveWithServiceName: %v", err)
	}
	if ns != SystemNamespace || local != 170 {
		t.Errorf("ResolveWithServiceName = (%s, %d), want (system, 170)", ns, local)
	}
}

func TestManager_RegisterSporemindPeer_DifferentVersionDoesNotShareSystem(t *testing.T) {
	m, err := NewManager(StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
		Schemas: []StaticSchemaEntry{
			{Namespace: SystemNamespace, SchemaID: 170, Name: "AgentChatSubmitReq", Object: spore.ObjectDesc{Kind: spore.TypeKindStruct, Name: "AgentChatSubmitReq"}},
		},
	}, WithLocalVersion("0.04"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.RegisterSporemindPeer("peerB", "0.05"); err != nil {
		t.Fatalf("RegisterSporemindPeer: %v", err)
	}

	_, shared, ok := m.IsSporemindPeer("peerB")
	if !ok {
		t.Fatal("expected peerB to be registered")
	}
	if shared {
		t.Fatal("expected no shared system namespace for different version")
	}

	// Without an explicit service binding/offset, the service is unknown.
	if _, err := m.WireIDWithServiceName("peerB", "agent.chat", 170); err == nil {
		t.Fatal("expected error: peerB does not expose agent.chat")
	}

	// Register an external namespace explicitly; then resolution works.
	if err := m.RegisterPeer("peerB", []PeerNamespace{
		{Namespace: "appB", Offset: 0x400000, Schemas: map[string]uint64{"TurnInput": 890}},
	}); err != nil {
		t.Fatalf("RegisterPeer: %v", err)
	}
	wireID, err := m.WireIDWithServiceName("peerB", "appB", 890)
	if err != nil {
		t.Fatalf("WireIDWithServiceName appB: %v", err)
	}
	if wireID != 0x40037A {
		t.Errorf("wireID = 0x%x, want 0x40037A", wireID)
	}
}

func TestManager_BindServiceNamespace_OverridesSharedSystem(t *testing.T) {
	m, err := NewManager(StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
		Schemas: []StaticSchemaEntry{
			{Namespace: SystemNamespace, SchemaID: 170, Name: "AgentChatSubmitReq", Object: spore.ObjectDesc{Kind: spore.TypeKindStruct, Name: "AgentChatSubmitReq"}},
		},
	}, WithLocalVersion("0.04"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// peerA shares system, but we explicitly map its "custom" service name
	// to a peer-specific external protocol namespace.
	if err := m.RegisterSporemindPeer("peerA", "0.04"); err != nil {
		t.Fatalf("RegisterSporemindPeer: %v", err)
	}
	if err := m.RegisterPeer("peerA", []PeerNamespace{
		{Namespace: "peerA.user", Offset: 0x400000, Schemas: map[string]uint64{"CustomTurnInput": 1000}},
	}); err != nil {
		t.Fatalf("RegisterPeer: %v", err)
	}
	if err := m.BindServiceNamespace("peerA", "custom", "peerA.user"); err != nil {
		t.Fatalf("BindServiceNamespace: %v", err)
	}

	wireID, err := m.WireIDWithServiceName("peerA", "custom", 1000)
	if err != nil {
		t.Fatalf("WireIDWithServiceName custom: %v", err)
	}
	if wireID != 0x4003E8 {
		t.Errorf("wireID = 0x%x, want 0x4003E8", wireID)
	}

	ns, local, err := m.ResolveWithServiceName("peerA", "custom", 0x4003E8)
	if err != nil {
		t.Fatalf("ResolveWithServiceName: %v", err)
	}
	if ns != "peerA.user" || local != 1000 {
		t.Errorf("ResolveWithServiceName = (%s, %d), want (peerA.user, 1000)", ns, local)
	}
}

func TestManager_CrossAppSameVersion_SystemObjectReused(t *testing.T) {
	// App A and App B both run sporemind 0.04. App B exposes a callable whose
	// request type is AgentChatSubmitReq (a system object). Because system is
	// shared, App A can resolve App B's wire ID without redeclaring the type.
	m, err := NewManager(StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
		Schemas: []StaticSchemaEntry{
			{Namespace: SystemNamespace, SchemaID: 170, Name: "AgentChatSubmitReq", Object: spore.ObjectDesc{Kind: spore.TypeKindStruct, Name: "AgentChatSubmitReq"}},
		},
	}, WithLocalVersion("0.04"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.RegisterSporemindPeer("appB", "0.04"); err != nil {
		t.Fatalf("RegisterSporemindPeer: %v", err)
	}

	// App B's service name for the exposed callable is "assistant.chat".
	// It maps to system because versions match.
	ns, local, err := m.ResolveWithServiceName("appB", "assistant.chat", 170)
	if err != nil {
		t.Fatalf("ResolveWithServiceName: %v", err)
	}
	if ns != SystemNamespace || local != 170 {
		t.Errorf("ResolveWithServiceName = (%s, %d), want (system, 170)", ns, local)
	}
}

func TestManager_RegisterSporemindPeer_WithoutLocalVersion(t *testing.T) {
	m, err := NewManager(StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
		Schemas:          []StaticSchemaEntry{},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.RegisterSporemindPeer("peerC", "0.04"); err != nil {
		t.Fatalf("RegisterSporemindPeer: %v", err)
	}
	_, shared, ok := m.IsSporemindPeer("peerC")
	if !ok {
		t.Fatal("expected peerC to be registered")
	}
	if shared {
		t.Fatal("expected no shared system when local version is unset")
	}
}

func TestManager_ResolveWithServiceName_Reserved(t *testing.T) {
	m, err := NewManager(StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
		Schemas:          []StaticSchemaEntry{},
	}, WithLocalVersion("0.04"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := m.RegisterSporemindPeer("peerD", "0.04"); err != nil {
		t.Fatalf("RegisterSporemindPeer: %v", err)
	}
	if _, _, err := m.ResolveWithServiceName("peerD", "agent.chat", 64); err == nil {
		t.Fatal("expected error for reserved wire id")
	}
}

func TestManager_ExposeService_ExportForPeer(t *testing.T) {
	m, err := NewManager(StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
		Schemas: []StaticSchemaEntry{
			{Namespace: SystemNamespace, SchemaID: 170, Name: "AgentChatSubmitReq", Object: spore.ObjectDesc{Kind: spore.TypeKindStruct, Name: "AgentChatSubmitReq"}},
			{Namespace: SystemNamespace, SchemaID: 171, Name: "AgentChatSubmitResp", Object: spore.ObjectDesc{Kind: spore.TypeKindStruct, Name: "AgentChatSubmitResp"}},
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.ExposeService(ExportedService{
		ServiceName:       "agent.chat",
		ProtocolNamespace: SystemNamespace,
		Schemas:           map[string]uint64{"AgentChatSubmitReq": 170, "AgentChatSubmitResp": 171},
	}); err != nil {
		t.Fatalf("ExposeService: %v", err)
	}

	// Expose another service only to a specific peer.
	if err := m.ExposeService(ExportedService{
		ServiceName:       "admin.ops",
		ProtocolNamespace: SystemNamespace,
		Schemas:           map[string]uint64{"AgentChatSubmitReq": 170},
	}, "peerAdmin"); err != nil {
		t.Fatalf("ExposeService peerAdmin: %v", err)
	}

	// Any peer sees the public service.
	public, err := m.ExportForPeer("peerA")
	if err != nil {
		t.Fatalf("ExportForPeer peerA: %v", err)
	}
	if len(public) != 1 || public[0].ServiceName != "agent.chat" {
		t.Errorf("public surface = %v, want [agent.chat]", public)
	}

	// The specific peer sees both.
	admin, err := m.ExportForPeer("peerAdmin")
	if err != nil {
		t.Fatalf("ExportForPeer peerAdmin: %v", err)
	}
	if len(admin) != 2 {
		t.Errorf("admin surface length = %d, want 2", len(admin))
	}

	// Unknown peer does not see the admin-only service.
	unknown, err := m.ExportForPeer("peerX")
	if err != nil {
		t.Fatalf("ExportForPeer peerX: %v", err)
	}
	if len(unknown) != 1 {
		t.Errorf("unknown surface length = %d, want 1", len(unknown))
	}
}

func TestManager_ImportPeerSurface_SameVersionSharesSystem(t *testing.T) {
	m, err := NewManager(StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
		Schemas: []StaticSchemaEntry{
			{Namespace: SystemNamespace, SchemaID: 170, Name: "AgentChatSubmitReq", Object: spore.ObjectDesc{Kind: spore.TypeKindStruct, Name: "AgentChatSubmitReq"}},
			{Namespace: SystemNamespace, SchemaID: 171, Name: "AgentChatSubmitResp", Object: spore.ObjectDesc{Kind: spore.TypeKindStruct, Name: "AgentChatSubmitResp"}},
		},
	}, WithLocalVersion("0.04"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.RegisterSporemindPeer("appB", "0.04"); err != nil {
		t.Fatalf("RegisterSporemindPeer: %v", err)
	}
	if err := m.ImportPeerSurface("appB", []ExportedService{
		{
			ServiceName:       "assistant.chat",
			ProtocolNamespace: SystemNamespace,
			Schemas:           map[string]uint64{"AgentChatSubmitReq": 170, "AgentChatSubmitResp": 171},
		},
	}); err != nil {
		t.Fatalf("ImportPeerSurface: %v", err)
	}

	// Because versions match, appB's system schemas map to local system offset 0.
	wireID, err := m.WireIDWithServiceName("appB", "assistant.chat", 170)
	if err != nil {
		t.Fatalf("WireIDWithServiceName: %v", err)
	}
	if wireID != 170 {
		t.Errorf("wireID = %d, want 170", wireID)
	}

	ns, local, err := m.ResolveWithServiceName("appB", "assistant.chat", 170)
	if err != nil {
		t.Fatalf("ResolveWithServiceName: %v", err)
	}
	if ns != SystemNamespace || local != 170 {
		t.Errorf("ResolveWithServiceName = (%s, %d), want (system, 170)", ns, local)
	}
}

func TestManager_ImportPeerSurface_ExternalNamespace(t *testing.T) {
	m, err := NewManager(StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
		Schemas:          []StaticSchemaEntry{},
	}, WithLocalVersion("0.04"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Different version: system is not shared, external namespace gets offset.
	if err := m.RegisterSporemindPeer("appC", "0.05"); err != nil {
		t.Fatalf("RegisterSporemindPeer: %v", err)
	}
	if err := m.ImportPeerSurface("appC", []ExportedService{
		{
			ServiceName:       "appC.chat",
			ProtocolNamespace: "appC",
			Schemas:           map[string]uint64{"TurnInput": 890},
		},
	}); err != nil {
		t.Fatalf("ImportPeerSurface: %v", err)
	}

	wireID, err := m.WireIDWithServiceName("appC", "appC.chat", 890)
	if err != nil {
		t.Fatalf("WireIDWithServiceName: %v", err)
	}
	if wireID != 0x40037A {
		t.Errorf("wireID = 0x%x, want 0x40037A", wireID)
	}

	ns, local, err := m.ResolveWithServiceName("appC", "appC.chat", 0x40037A)
	if err != nil {
		t.Fatalf("ResolveWithServiceName: %v", err)
	}
	if ns != "appC" || local != 890 {
		t.Errorf("ResolveWithServiceName = (%s, %d), want (appC, 890)", ns, local)
	}
}

func TestManager_ExposeService_UnknownSchema(t *testing.T) {
	m, err := NewManager(StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
		Schemas:          []StaticSchemaEntry{},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := m.ExposeService(ExportedService{
		ServiceName:       "agent.chat",
		ProtocolNamespace: SystemNamespace,
		Schemas:           map[string]uint64{"Missing": 170},
	}); err == nil {
		t.Fatal("expected error for unknown schema")
	}
}

func TestManager_ImportPeerSurface_UnknownPeer(t *testing.T) {
	m, err := NewManager(StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
		Schemas:          []StaticSchemaEntry{},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := m.ImportPeerSurface("unknown", []ExportedService{
		{ServiceName: "x", ProtocolNamespace: SystemNamespace, Schemas: map[string]uint64{"A": 170}},
	}); err == nil {
		t.Fatal("expected error for unknown peer")
	}
}

func TestManager_IsServiceExposedToPeer(t *testing.T) {
	m, err := NewManager(StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
		Schemas: []StaticSchemaEntry{
			{Namespace: SystemNamespace, SchemaID: 170, Name: "AgentChatSubmitReq", Object: spore.ObjectDesc{Kind: spore.TypeKindStruct, Name: "AgentChatSubmitReq"}},
		},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.ExposeService(ExportedService{
		ServiceName:       "agent.chat",
		ProtocolNamespace: SystemNamespace,
		Schemas:           map[string]uint64{"AgentChatSubmitReq": 170},
	}); err != nil {
		t.Fatalf("ExposeService public: %v", err)
	}
	if err := m.ExposeService(ExportedService{
		ServiceName:       "admin.ops",
		ProtocolNamespace: SystemNamespace,
		Schemas:           map[string]uint64{"AgentChatSubmitReq": 170},
	}, "peerAdmin"); err != nil {
		t.Fatalf("ExposeService peerAdmin: %v", err)
	}

	if !m.IsServiceExposedToPeer("anyone", "agent.chat") {
		t.Error("expected agent.chat exposed to anyone")
	}
	if m.IsServiceExposedToPeer("anyone", "admin.ops") {
		t.Error("expected admin.ops not exposed to anyone")
	}
	if !m.IsServiceExposedToPeer("peerAdmin", "admin.ops") {
		t.Error("expected admin.ops exposed to peerAdmin")
	}
	if m.IsServiceExposedToPeer("peerAdmin", "unknown") {
		t.Error("expected unknown not exposed")
	}
}

func TestManager_BindNamespace_Alignment(t *testing.T) {
	fragment := StaticFragment{
		NamespaceOffsets: map[string]uint64{SystemNamespace: 0},
		Schemas:          []StaticSchemaEntry{},
	}
	m, err := NewManager(fragment)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := m.BindNamespace("peer", 0x200001); err == nil {
		t.Fatal("expected error for non-aligned offset")
	}
	if err := m.BindNamespace("peer", 0x200000); err != nil {
		t.Fatalf("BindNamespace aligned: %v", err)
	}
}

func TestManager_ResolveWithNamespace(t *testing.T) {
	fragment := StaticFragment{
		NamespaceOffsets: map[string]uint64{
			SystemNamespace: 0,
			"external":      0x200000,
		},
		Schemas: []StaticSchemaEntry{
			{Namespace: SystemNamespace, SchemaID: 170, Name: "AgentChatSubmitReq", Object: spore.ObjectDesc{Kind: spore.TypeKindStruct, Name: "AgentChatSubmitReq"}},
		},
	}
	m, err := NewManager(fragment)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	local, err := m.ResolveWithNamespace(SystemNamespace, 170)
	if err != nil {
		t.Fatalf("ResolveWithNamespace system: %v", err)
	}
	if local != 170 {
		t.Errorf("ResolveWithNamespace(system, 170) = %d, want 170", local)
	}

	local, err = m.ResolveWithNamespace("external", 0x2000AA)
	if err != nil {
		t.Fatalf("ResolveWithNamespace external: %v", err)
	}
	if local != 170 {
		t.Errorf("ResolveWithNamespace(external, 0x2000AA) = %d, want 170", local)
	}

	if _, err := m.ResolveWithNamespace("external", 170); err == nil {
		t.Fatal("expected error: wire id 170 does not belong to external namespace")
	}
}
