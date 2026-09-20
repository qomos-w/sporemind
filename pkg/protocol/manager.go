// Package protocol provides a global schema/protocol manager that binds
// namespace-local schema IDs to stable wire schema IDs.
//
// Phase 1 (current): system schemas share offset 0; @schema(N) declares the
// local schema ID. The wire ID equals the local ID for system schemas.
//
// Phase 2 (current extension):
//   - system namespace (offset 0): reserved 0-127 + sporemind's own schemas.
//   - user namespace (offset 0x10000): local custom schemas defined via
//     spore script; per-app local, distinguished by namespace name on the wire.
//   - external app namespaces (offset 0x20000+): bound dynamically when
//     connecting to another app.
package protocol

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	spore "github.com/qomos-w/spore/schema"
)

const (
	// BuiltinReserveEnd mirrors gospore/schema.BuiltinReserveEnd.
	// IDs 0-127 are reserved for scalars, void, builtin containers and system structs.
	BuiltinReserveEnd = 127

	// SystemNamespace is the canonical namespace for the app's own built-in
	// schemas (those authored in .spore files and generated into the binary).
	SystemNamespace = "system"
	// UserNamespace is the local namespace for per-app custom schemas defined
	// at runtime, e.g. by spore script agents. It is offset 1 (high bit 1).
	UserNamespace = "user"

	// LocalIDBits is the number of bits reserved for the local schema ID within
	// a wire ID. Local IDs use 21 bits (~2.1M values); the namespace offset
	// index occupies the higher bits up to the JS-safe ceiling minus reserved.
	LocalIDBits = 21
	// LocalIDMask masks the local schema ID out of a wire ID.
	LocalIDMask = (1 << LocalIDBits) - 1
	// NamespaceOffsetStep is the distance between consecutive namespace offsets.
	// System=0, user=1<<LocalIDBits, first external=2<<LocalIDBits, etc.
	NamespaceOffsetStep = 1 << LocalIDBits
	// ReservedHighBits reserves the top 4 bits of the JS-safe 53-bit space.
	// These bits must stay 0 — they leave room for future wire flags/versioning.
	// Namespace effectively gets 28 bits (2^28 ~268M namespaces).
	ReservedHighBits = 4
	// MaxWireSchemaID is the largest usable wire schema ID. It is capped below
	// the JS-safe number ceiling (2^53-1) by ReservedHighBits so the top bits
	// are never set. Wire IDs must not exceed it.
	MaxWireSchemaID = (1 << (53 - ReservedHighBits)) - 1

	// OffsetIndexSystem is the high-bit offset index for the system namespace.
	OffsetIndexSystem = 0
	// OffsetIndexUser is the high-bit offset index for the local user namespace.
	OffsetIndexUser = 1
	// OffsetIndexFirstExternal is the first high-bit offset index available for
	// external apps. External apps register from this index upwards.
	OffsetIndexFirstExternal = 2
)

// StaticSchemaEntry is one struct with a declared schema ID.
type StaticSchemaEntry struct {
	Namespace string           `json:"namespace"`
	SchemaID  uint64           `json:"schemaId"`
	Name      string           `json:"name"`
	Object    spore.ObjectDesc `json:"object"`
}

// StaticFragment is the JSON produced by the build pipeline from .spore files.
type StaticFragment struct {
	// NamespaceOffsets maps a namespace to its high-bit offset.
	// The system namespace uses offset 0; user/external namespaces use non-zero
	// offsets allocated by offset index.
	NamespaceOffsets map[string]uint64   `json:"namespaceOffsets"`
	Schemas          []StaticSchemaEntry `json:"schemas"`
}

// ManagerOption configures a Manager at construction time.
type ManagerOption func(*Manager)

// WithLocalVersion sets the sporemind version of the local app. When a remote
// peer is registered with the same version, the system namespace is shared.
func WithLocalVersion(version string) ManagerOption {
	return func(m *Manager) {
		m.localVersion = version
	}
}

// Manager holds static and dynamic schema ID mappings and resolves wire IDs.
type Manager struct {
	mu sync.RWMutex

	// localVersion is the sporemind version of this app. Used to decide whether
	// another sporemind peer shares the system namespace by default.
	localVersion string

	// offsets: namespace -> high-bit offset.
	offsets map[string]uint64

	// static entries keyed by namespace:name and by wire id.
	static     map[string]StaticSchemaEntry
	staticByID map[uint64]StaticSchemaEntry

	// dynamic entries for user/external protocols, keyed by namespace:name and wire id.
	dynamic     map[string]uint64
	dynamicByID map[uint64]dynamicEntry

	// exposedServices tracks services this app exposes to peers. The key is the
	// service name; protocol schemas always use SystemNamespace locally.
	exposedServices map[string]*exposedServiceRule

	// peerBindings: peerID -> per-peer namespace/schema bindings for cross-app
	// protocol exchange. Peer-relative wire IDs are offsetPeer + localID.
	peerBindings map[string]*peerBinding
}

type dynamicEntry struct {
	namespace string
	name      string
	localID   uint64
}

// PeerNamespace describes one namespace exposed by a remote peer.
type PeerNamespace struct {
	Namespace string
	Offset    uint64
	Schemas   map[string]uint64 // name -> local schema id
}

// ExportedService describes one service an app exposes to peers.
// ServiceName is the callable/service route. ProtocolNamespace is always the
// canonical system namespace for core schemas, or an explicit external namespace.
// Schemas maps schema name to the local schema id within ProtocolNamespace.
type ExportedService struct {
	ServiceName       string
	ProtocolNamespace string
	Schemas           map[string]uint64
}

// exposedServiceRule tracks who can see an exported service.
type exposedServiceRule struct {
	svc      ExportedService
	allPeers bool
	peerIDs  map[string]struct{}
}

// peerBinding holds the per-peer namespace offsets used for cross-app
// protocol exchange. The same namespace name may be bound to different
// offsets by different peers (and to offset 0 locally).
type peerBinding struct {
	version           string                       // sporemind version; empty means non-sporemind / unknown
	offsets           map[string]uint64            // protocol namespace -> peer's wire offset
	serviceNamespaces map[string]string            // service name -> protocol namespace
	schemas           map[string]map[string]uint64 // protocol namespace -> name -> localID
	byWire            map[uint64]peerSchemaRef     // peer-relative wireID -> schema
}

type peerSchemaRef struct {
	namespace string
	localID   uint64
}

// NewManager creates a manager from a static fragment.
// It validates local ID ranges and duplicate names/IDs across all namespaces.
func NewManager(fragment StaticFragment, opts ...ManagerOption) (*Manager, error) {
	m := &Manager{
		offsets:         make(map[string]uint64, len(fragment.NamespaceOffsets)),
		static:          make(map[string]StaticSchemaEntry, len(fragment.Schemas)),
		staticByID:      make(map[uint64]StaticSchemaEntry, len(fragment.Schemas)),
		dynamic:         make(map[string]uint64),
		dynamicByID:     make(map[uint64]dynamicEntry),
		exposedServices: make(map[string]*exposedServiceRule),
		peerBindings:    make(map[string]*peerBinding),
	}
	for _, opt := range opts {
		opt(m)
	}

	// Load namespace offsets. Multiple internal namespaces may share offset 0.
	for ns, off := range fragment.NamespaceOffsets {
		if ns == "" {
			return nil, fmt.Errorf("namespace offset cannot be empty")
		}
		m.offsets[ns] = off
	}
	// Ensure the system namespace exists.
	if _, ok := m.offsets[SystemNamespace]; !ok {
		m.offsets[SystemNamespace] = 0
	}

	for i, e := range fragment.Schemas {
		if err := validateLocalID(e.SchemaID); err != nil {
			return nil, fmt.Errorf("schemas[%d] %s.%s: %w", i, e.Namespace, e.Name, err)
		}
		if _, ok := m.offsets[e.Namespace]; !ok {
			return nil, fmt.Errorf("schemas[%d] %s.%s: namespace %q has no offset declared", i, e.Namespace, e.Name, e.Namespace)
		}
		wireID, err := m.wireIDLocked(e.Namespace, e.SchemaID)
		if err != nil {
			return nil, fmt.Errorf("schemas[%d] %s.%s: %w", i, e.Namespace, e.Name, err)
		}
		k := key(e.Namespace, e.Name)
		if prev, ok := m.static[k]; ok {
			return nil, fmt.Errorf("schema conflict: duplicate struct %q in namespace %q (ids %d and %d)", e.Name, e.Namespace, prev.SchemaID, e.SchemaID)
		}
		if prev, ok := m.staticByID[wireID]; ok {
			return nil, fmt.Errorf("schema conflict: wire id %d (0x%x) already used by %s.%s and %s.%s", wireID, wireID, prev.Namespace, prev.Name, e.Namespace, e.Name)
		}
		m.static[k] = e
		m.staticByID[wireID] = e
	}

	return m, nil
}

// MustNewManager is like NewManager but panics on error.
func MustNewManager(fragment StaticFragment) *Manager {
	m, err := NewManager(fragment)
	if err != nil {
		panic(err)
	}
	return m
}

// BindNamespace binds namespace to an explicit high-bit offset.
// Use this for reserved namespaces (system, user) or for a known external peer.
func (m *Manager) BindNamespace(namespace string, offset uint64) error {
	if namespace == "" {
		return fmt.Errorf("namespace cannot be empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bindNamespaceLocked(namespace, offset)
}

// BindNamespaceByIndex binds namespace to offsetIndex * NamespaceOffsetStep.
// system=0, user=0x10000, first external=0x20000.
func (m *Manager) BindNamespaceByIndex(namespace string, offsetIndex uint64) error {
	offset := offsetIndex * NamespaceOffsetStep
	if offset == 0 {
		// Only system is allowed at offset 0.
		if namespace != SystemNamespace {
			return fmt.Errorf("namespace %q cannot bind to offset 0; only %q uses offset 0", namespace, SystemNamespace)
		}
	}
	return m.BindNamespace(namespace, offset)
}

func (m *Manager) bindNamespaceLocked(namespace string, offset uint64) error {
	if offset%NamespaceOffsetStep != 0 {
		return fmt.Errorf("namespace %q offset 0x%x is not aligned to NamespaceOffsetStep 0x%x", namespace, offset, NamespaceOffsetStep)
	}
	if prev, ok := m.offsets[namespace]; ok && prev != offset {
		return fmt.Errorf("namespace %q already has offset 0x%x, cannot rebind to 0x%x", namespace, prev, offset)
	}
	for ns, off := range m.offsets {
		if ns != namespace && off == offset {
			return fmt.Errorf("schema conflict: namespace %q offset 0x%x collides with namespace %q", namespace, offset, ns)
		}
	}
	m.offsets[namespace] = offset
	return nil
}

// allocExternalOffset finds the next unused external offset index.
func (m *Manager) allocExternalOffset() (uint64, error) {
	used := make(map[uint64]bool)
	for _, off := range m.offsets {
		used[off] = true
	}
	idx := uint64(OffsetIndexFirstExternal)
	for {
		off := idx * NamespaceOffsetStep
		if !used[off] {
			return off, nil
		}
		idx++
		if idx == 0 {
			return 0, fmt.Errorf("no external namespace offsets available")
		}
	}
}

// RegisterUser binds the local user namespace and registers its schemas.
func (m *Manager) RegisterUser(schemas map[string]uint64) error {
	if err := m.BindNamespaceByIndex(UserNamespace, OffsetIndexUser); err != nil {
		return err
	}
	return m.registerDynamic(UserNamespace, schemas)
}

// RegisterExternal binds an external namespace to the next available high-bit
// offset and registers its schemas.
func (m *Manager) RegisterExternal(namespace string, schemas map[string]uint64) error {
	if namespace == "" {
		return fmt.Errorf("namespace cannot be empty")
	}
	if namespace == SystemNamespace || namespace == UserNamespace {
		return fmt.Errorf("namespace %q is reserved; use BindNamespace or RegisterUser", namespace)
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	off, ok := m.offsets[namespace]
	if !ok {
		var err error
		off, err = m.allocExternalOffset()
		if err != nil {
			return err
		}
	}
	if err := m.bindNamespaceLocked(namespace, off); err != nil {
		return err
	}
	return m.registerDynamicLocked(namespace, schemas)
}

// RegisterExternalWithOffset binds an external namespace to an explicit offset
// and registers its schemas.
func (m *Manager) RegisterExternalWithOffset(namespace string, offset uint64, schemas map[string]uint64) error {
	if namespace == "" {
		return fmt.Errorf("namespace cannot be empty")
	}
	if namespace == SystemNamespace || namespace == UserNamespace {
		return fmt.Errorf("namespace %q is reserved; use BindNamespace or RegisterUser", namespace)
	}
	if offset == 0 {
		return fmt.Errorf("external namespace %q cannot use offset 0 (reserved for %q)", namespace, SystemNamespace)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.bindNamespaceLocked(namespace, offset); err != nil {
		return err
	}
	return m.registerDynamicLocked(namespace, schemas)
}

func (m *Manager) registerDynamic(namespace string, schemas map[string]uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.registerDynamicLocked(namespace, schemas)
}

// UnregisterExternal removes a previously registered external namespace and
// releases its dynamic schema entries and offset. It is safe to call for a
// namespace that is not registered (idempotent). System and user namespaces
// cannot be unregistered.
func (m *Manager) UnregisterExternal(namespace string) error {
	if namespace == "" {
		return fmt.Errorf("namespace cannot be empty")
	}
	if namespace == SystemNamespace || namespace == UserNamespace {
		return fmt.Errorf("namespace %q is reserved and cannot be unregistered", namespace)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Nothing to do if the namespace was never registered.
	if _, ok := m.offsets[namespace]; !ok {
		return nil
	}

	// Remove dynamic schema entries for this namespace.
	off := m.offsets[namespace]
	for k, localID := range m.dynamic {
		ns, _ := splitKey(k)
		if ns == namespace {
			delete(m.dynamicByID, off+localID)
			delete(m.dynamic, k)
		}
	}

	// Release the namespace offset.
	delete(m.offsets, namespace)
	return nil
}

// splitKey returns the namespace and name parts of a dynamic/static map key.
// Keys are built with key(ns, name) using ":" as separator.
func splitKey(k string) (namespace, name string) {
	i := strings.Index(k, ":")
	if i < 0 {
		return k, ""
	}
	return k[:i], k[i+1:]
}

func (m *Manager) registerDynamicLocked(namespace string, schemas map[string]uint64) error {
	off, ok := m.offsets[namespace]
	if !ok {
		return fmt.Errorf("namespace %q is not bound", namespace)
	}
	inserted := make([]struct {
		k      string
		wireID uint64
	}, 0, len(schemas))
	for name, localID := range schemas {
		if err := validateLocalID(localID); err != nil {
			// Partial registration would poison retries (duplicate-dynamic
			// errors on the same namespace); roll back what we inserted.
			for _, e := range inserted {
				delete(m.dynamic, e.k)
				delete(m.dynamicByID, e.wireID)
			}
			return fmt.Errorf("dynamic schema %q in namespace %q: %w", name, namespace, err)
		}
		wireID := off + localID
		k := key(namespace, name)
		if _, ok := m.static[k]; ok {
			for _, e := range inserted {
				delete(m.dynamic, e.k)
				delete(m.dynamicByID, e.wireID)
			}
			return fmt.Errorf("schema conflict: duplicate struct %q in namespace %q", name, namespace)
		}
		if prev, ok := m.dynamic[k]; ok {
			for _, e := range inserted {
				delete(m.dynamic, e.k)
				delete(m.dynamicByID, e.wireID)
			}
			return fmt.Errorf("schema conflict: duplicate dynamic struct %q in namespace %q (ids %d and %d)", name, namespace, prev, localID)
		}
		if prev, ok := m.staticByID[wireID]; ok {
			for _, e := range inserted {
				delete(m.dynamic, e.k)
				delete(m.dynamicByID, e.wireID)
			}
			return fmt.Errorf("schema conflict: wire id 0x%x already used by static %s.%s", wireID, prev.Namespace, prev.Name)
		}
		if prev, ok := m.dynamicByID[wireID]; ok {
			for _, e := range inserted {
				delete(m.dynamic, e.k)
				delete(m.dynamicByID, e.wireID)
			}
			return fmt.Errorf("schema conflict: wire id 0x%x already used by dynamic %s.%s", wireID, prev.namespace, prev.name)
		}
		m.dynamic[k] = localID
		m.dynamicByID[wireID] = dynamicEntry{namespace: namespace, name: name, localID: localID}
		inserted = append(inserted, struct {
			k      string
			wireID uint64
		}{k, wireID})
	}
	return nil
}

// WireID converts a (namespace, localID) pair into a global wire schema ID.
func (m *Manager) WireID(namespace string, localID uint64) (uint64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.wireIDLocked(namespace, localID)
}

func (m *Manager) wireIDLocked(namespace string, localID uint64) (uint64, error) {
	if err := validateLocalID(localID); err != nil {
		return 0, err
	}
	off, ok := m.offsets[namespace]
	if !ok {
		return 0, fmt.Errorf("unknown namespace %q", namespace)
	}
	wireID := off + localID
	if wireID > MaxWireSchemaID {
		return 0, fmt.Errorf("wire schema id %d exceeds JS-safe maximum %d", wireID, MaxWireSchemaID)
	}
	return wireID, nil
}

// Resolve splits a wire schema ID back into (namespace, localID).
// It first checks registered schemas, then falls back to offset-based
// resolution for bound non-system namespaces.
func (m *Manager) Resolve(wireID uint64) (namespace string, localID uint64, err error) {
	if wireID <= BuiltinReserveEnd {
		return "", 0, fmt.Errorf("wire id %d is in the reserved builtin range", wireID)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if e, ok := m.staticByID[wireID]; ok {
		off := m.offsets[e.Namespace]
		return e.Namespace, wireID - off, nil
	}
	if e, ok := m.dynamicByID[wireID]; ok {
		return e.namespace, e.localID, nil
	}
	// Phase 2: bound non-system namespace frames may carry IDs that have not
	// been explicitly registered as schemas yet. Derive namespace from offset.
	localID = wireID & LocalIDMask
	off := wireID - localID
	for ns, nsOff := range m.offsets {
		if ns != SystemNamespace && nsOff == off {
			return ns, localID, nil
		}
	}
	return "", 0, fmt.Errorf("wire id %d is not registered", wireID)
}

// ResolveWithNamespace resolves a wire schema ID when the sender's namespace
// is already known from the wire frame (gospore Frame.SchemaNS). This is the
// correct path for cross-app frames, where the same namespace name may be
// bound to different offsets by different peers.
func (m *Manager) ResolveWithNamespace(namespace string, wireID uint64) (localID uint64, err error) {
	if wireID <= BuiltinReserveEnd {
		return 0, fmt.Errorf("wire id %d is in the reserved builtin range", wireID)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Fast path: exact registered entry.
	if e, ok := m.staticByID[wireID]; ok && e.Namespace == namespace {
		off := m.offsets[e.Namespace]
		return wireID - off, nil
	}
	if e, ok := m.dynamicByID[wireID]; ok && e.namespace == namespace {
		return e.localID, nil
	}

	off, ok := m.offsets[namespace]
	if !ok {
		return 0, fmt.Errorf("unknown namespace %q", namespace)
	}
	localID = wireID - off
	if localID > LocalIDMask {
		return 0, fmt.Errorf("wire id 0x%x does not belong to namespace %q (offset 0x%x)", wireID, namespace, off)
	}
	return localID, nil
}

// RegisterPeer binds the namespaces exposed by a remote app peer.
// Each peer may bind the same namespace name to a different offset; this
// is the cross-app equivalent of RegisterExternal, but scoped to one peer.
func (m *Manager) RegisterPeer(peerID string, namespaces []PeerNamespace) error {
	if peerID == "" {
		return fmt.Errorf("peerID cannot be empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	pb := &peerBinding{
		offsets:           make(map[string]uint64),
		serviceNamespaces: make(map[string]string),
		schemas:           make(map[string]map[string]uint64),
		byWire:            make(map[uint64]peerSchemaRef),
	}
	for _, ns := range namespaces {
		if ns.Namespace == "" {
			return fmt.Errorf("peer %q: namespace cannot be empty", peerID)
		}
		if ns.Namespace == SystemNamespace || ns.Namespace == UserNamespace {
			return fmt.Errorf("peer %q: namespace %q is reserved", peerID, ns.Namespace)
		}
		if ns.Offset%NamespaceOffsetStep != 0 {
			return fmt.Errorf("peer %q namespace %q offset 0x%x is not aligned to 0x%x", peerID, ns.Namespace, ns.Offset, NamespaceOffsetStep)
		}
		if _, ok := pb.offsets[ns.Namespace]; ok {
			return fmt.Errorf("peer %q: duplicate namespace %q", peerID, ns.Namespace)
		}
		pb.offsets[ns.Namespace] = ns.Offset
		pb.schemas[ns.Namespace] = make(map[string]uint64, len(ns.Schemas))
		for name, localID := range ns.Schemas {
			if err := validateLocalID(localID); err != nil {
				return fmt.Errorf("peer %q namespace %q schema %q: %w", peerID, ns.Namespace, name, err)
			}
			wireID := ns.Offset + localID
			if _, ok := pb.byWire[wireID]; ok {
				return fmt.Errorf("peer %q namespace %q: wire id 0x%x already used", peerID, ns.Namespace, wireID)
			}
			pb.schemas[ns.Namespace][name] = localID
			pb.byWire[wireID] = peerSchemaRef{namespace: ns.Namespace, localID: localID}
		}
	}
	m.peerBindings[peerID] = pb
	return nil
}

// UnregisterPeer removes a peer binding and all its wire ID mappings.
func (m *Manager) UnregisterPeer(peerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.peerBindings, peerID)
}

// RegisterSporemindPeer registers a peer running sporemind. If the peer's
// version matches the local version, the system namespace is considered shared:
// the peer's system schemas use offset 0. Only explicitly exposed callables
// can be invoked across apps; this method only wires protocol namespace mapping.
func (m *Manager) RegisterSporemindPeer(peerID, version string) error {
	if peerID == "" {
		return fmt.Errorf("peerID cannot be empty")
	}
	if version == "" {
		return fmt.Errorf("version cannot be empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	sharedSystem := version == m.localVersion && m.localVersion != ""
	pb := &peerBinding{
		version:           version,
		offsets:           make(map[string]uint64),
		schemas:           make(map[string]map[string]uint64),
		byWire:            make(map[uint64]peerSchemaRef),
		serviceNamespaces: make(map[string]string),
	}
	if sharedSystem {
		pb.offsets[SystemNamespace] = 0
	}
	m.peerBindings[peerID] = pb
	return nil
}

// IsSporemindPeer reports whether peerID is registered as a sporemind peer and,
// if so, whether it shares the system namespace with the local app.
func (m *Manager) IsSporemindPeer(peerID string) (version string, sharedSystem bool, ok bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pb, ok := m.peerBindings[peerID]
	if !ok {
		return "", false, false
	}
	return pb.version, pb.version == m.localVersion && m.localVersion != "", true
}

// WireIDForPeer returns the wire schema ID to use when sending to a specific
// peer. The peer's offset for the namespace is added to the local schema ID.
func (m *Manager) WireIDForPeer(peerID, namespace string, localID uint64) (uint64, error) {
	if err := validateLocalID(localID); err != nil {
		return 0, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	pb, ok := m.peerBindings[peerID]
	if !ok {
		return 0, fmt.Errorf("unknown peer %q", peerID)
	}
	off, ok := pb.offsets[namespace]
	if !ok {
		return 0, fmt.Errorf("peer %q does not expose namespace %q", peerID, namespace)
	}
	return off + localID, nil
}

// ResolveForPeer resolves a wire schema ID received from a specific peer.
// It returns the namespace name and local schema ID within that peer's
// namespace binding.
func (m *Manager) ResolveForPeer(peerID string, wireID uint64) (namespace string, localID uint64, err error) {
	if wireID <= BuiltinReserveEnd {
		return "", 0, fmt.Errorf("wire id %d is in the reserved builtin range", wireID)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	pb, ok := m.peerBindings[peerID]
	if !ok {
		return "", 0, fmt.Errorf("unknown peer %q", peerID)
	}
	ref, ok := pb.byWire[wireID]
	if !ok {
		// Fallback: derive from the peer's offset table.
		localID = wireID & LocalIDMask
		off := wireID - localID
		for ns, nsOff := range pb.offsets {
			if nsOff == off {
				return ns, localID, nil
			}
		}
		return "", 0, fmt.Errorf("peer %q: wire id 0x%x is not registered", peerID, wireID)
	}
	return ref.namespace, ref.localID, nil
}

// peerProtocolNamespace resolves a remote service name to its protocol namespace.
// Same-version sporemind peers use the canonical system namespace by default.
func (m *Manager) peerProtocolNamespace(pb *peerBinding, serviceName string) (string, bool) {
	if ns, ok := pb.serviceNamespaces[serviceName]; ok {
		return ns, true
	}
	sharedSystem := pb.version != "" && pb.version == m.localVersion && m.localVersion != ""
	if sharedSystem {
		// Internal sporemind service names are not explicitly listed; they all resolve
		// to the shared system protocol namespace.
		return SystemNamespace, true
	}
	// No service binding and no shared system: treat the service name as its
	// external protocol namespace (it must have been registered by the peer).
	_, hasOffset := pb.offsets[serviceName]
	return serviceName, hasOffset
}

// WireIDWithServiceName returns the wire schema ID for a remote service.
func (m *Manager) WireIDWithServiceName(peerID, serviceName string, localID uint64) (uint64, error) {
	if err := validateLocalID(localID); err != nil {
		return 0, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	pb, ok := m.peerBindings[peerID]
	if !ok {
		return 0, fmt.Errorf("unknown peer %q", peerID)
	}
	protocolNS, ok := m.peerProtocolNamespace(pb, serviceName)
	if !ok {
		return 0, fmt.Errorf("peer %q does not expose service %q", peerID, serviceName)
	}
	off, ok := pb.offsets[protocolNS]
	if !ok {
		return 0, fmt.Errorf("peer %q does not expose protocol namespace %q", peerID, protocolNS)
	}
	return off + localID, nil
}

// ResolveWithServiceName resolves a wire schema ID from a remote service.
func (m *Manager) ResolveWithServiceName(peerID, serviceName string, wireID uint64) (protocolNS string, localID uint64, err error) {
	if wireID <= BuiltinReserveEnd {
		return "", 0, fmt.Errorf("wire id %d is in the reserved builtin range", wireID)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	pb, ok := m.peerBindings[peerID]
	if !ok {
		return "", 0, fmt.Errorf("unknown peer %q", peerID)
	}
	protocolNS, ok = m.peerProtocolNamespace(pb, serviceName)
	if !ok {
		return "", 0, fmt.Errorf("peer %q does not expose service %q", peerID, serviceName)
	}
	off, ok := pb.offsets[protocolNS]
	if !ok {
		return "", 0, fmt.Errorf("peer %q does not expose protocol namespace %q", peerID, protocolNS)
	}
	localID = wireID - off
	if localID > LocalIDMask {
		return "", 0, fmt.Errorf("wire id 0x%x does not belong to namespace %q (offset 0x%x)", wireID, protocolNS, off)
	}
	return protocolNS, localID, nil
}

// BindServiceNamespace binds a remote service name to a protocol namespace
// for one peer. This is an external peer boundary, not a local core alias.
func (m *Manager) BindServiceNamespace(peerID, serviceName, protocolNamespace string) error {
	if peerID == "" {
		return fmt.Errorf("peerID cannot be empty")
	}
	if serviceName == "" {
		return fmt.Errorf("service name cannot be empty")
	}
	if protocolNamespace == "" {
		return fmt.Errorf("protocol namespace cannot be empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	pb, ok := m.peerBindings[peerID]
	if !ok {
		return fmt.Errorf("unknown peer %q", peerID)
	}
	pb.serviceNamespaces[serviceName] = protocolNamespace
	return nil
}

// ExposeService declares that this app exposes a service to external peers.
// ServiceName is the callable/service route. ProtocolNamespace is the local
// protocol namespace where the service's schemas live. Schemas lists the schema
// names exposed by the service. If peerIDs is empty, the service is exposed to
// all peers.
func (m *Manager) ExposeService(svc ExportedService, peerIDs ...string) error {
	if svc.ServiceName == "" {
		return fmt.Errorf("service name cannot be empty")
	}
	if svc.ProtocolNamespace == "" {
		return fmt.Errorf("protocol namespace cannot be empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.offsets[svc.ProtocolNamespace]; !ok {
		return fmt.Errorf("unknown protocol namespace %q", svc.ProtocolNamespace)
	}
	for name := range svc.Schemas {
		k := key(svc.ProtocolNamespace, name)
		if _, ok := m.static[k]; !ok && m.dynamic[k] == 0 {
			return fmt.Errorf("schema %q not found in protocol namespace %q", name, svc.ProtocolNamespace)
		}
	}

	rule := &exposedServiceRule{svc: svc}
	if len(peerIDs) == 0 {
		rule.allPeers = true
	} else {
		rule.peerIDs = make(map[string]struct{}, len(peerIDs))
		for _, pid := range peerIDs {
			if pid == "" {
				return fmt.Errorf("peerID cannot be empty")
			}
			rule.peerIDs[pid] = struct{}{}
		}
	}
	m.exposedServices[svc.ServiceName] = rule
	return nil
}

// ExportForPeer returns the services and schemas this app exposes to a
// specific peer. The caller serialises this surface and sends it during
// handshake.
func (m *Manager) ExportForPeer(peerID string) ([]ExportedService, error) {
	if peerID == "" {
		return nil, fmt.Errorf("peerID cannot be empty")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []ExportedService
	for _, rule := range m.exposedServices {
		if !rule.allPeers {
			if _, ok := rule.peerIDs[peerID]; !ok {
				continue
			}
		}
		out = append(out, rule.svc)
	}
	return out, nil
}

// ImportPeerSurface imports a peer's exposed service surface and binds the
// service names and schemas for that peer.
func (m *Manager) ImportPeerSurface(peerID string, surface []ExportedService) error {
	if peerID == "" {
		return fmt.Errorf("peerID cannot be empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	pb, ok := m.peerBindings[peerID]
	if !ok {
		return fmt.Errorf("unknown peer %q", peerID)
	}

	// Group schemas by the exporter's protocol namespace.
	groups := make(map[string]map[string]uint64)
	aliases := make(map[string]string, len(surface))
	for _, svc := range surface {
		if svc.ServiceName == "" {
			return fmt.Errorf("peer %q: service name cannot be empty", peerID)
		}
		if svc.ProtocolNamespace == "" {
			return fmt.Errorf("peer %q service %q: protocol namespace cannot be empty", peerID, svc.ServiceName)
		}
		if groups[svc.ProtocolNamespace] == nil {
			groups[svc.ProtocolNamespace] = make(map[string]uint64)
		}
		for name, id := range svc.Schemas {
			if err := validateLocalID(id); err != nil {
				return fmt.Errorf("peer %q service %q schema %q: %w", peerID, svc.ServiceName, name, err)
			}
			groups[svc.ProtocolNamespace][name] = id
		}
		aliases[svc.ServiceName] = svc.ProtocolNamespace
	}

	for protocolNS, schemas := range groups {
		localNS := protocolNS
		offset, ok := pb.offsets[localNS]
		if !ok {
			if m.peerSharesSystemLocked(pb) && protocolNS == SystemNamespace {
				offset = 0
				pb.offsets[localNS] = 0
			} else {
				var err error
				offset, err = m.allocPeerExternalOffsetLocked(pb)
				if err != nil {
					return fmt.Errorf("peer %q namespace %q: %w", peerID, protocolNS, err)
				}
				pb.offsets[localNS] = offset
			}
		}
		if pb.schemas[localNS] == nil {
			pb.schemas[localNS] = make(map[string]uint64, len(schemas))
		}
		for name, id := range schemas {
			wireID := offset + id
			if _, taken := pb.byWire[wireID]; taken {
				return fmt.Errorf("peer %q namespace %q: wire id 0x%x already used", peerID, localNS, wireID)
			}
			pb.schemas[localNS][name] = id
			pb.byWire[wireID] = peerSchemaRef{namespace: localNS, localID: id}
		}
	}

	for svcName, protocolNS := range aliases {
		pb.serviceNamespaces[svcName] = protocolNS
	}
	return nil
}

// IsServiceExposedToPeer reports whether the local app exposes serviceName to
// the given peer.
func (m *Manager) IsServiceExposedToPeer(peerID, serviceName string) bool {
	if peerID == "" || serviceName == "" {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	rule, ok := m.exposedServices[serviceName]
	if !ok {
		return false
	}
	if rule.allPeers {
		return true
	}
	_, ok = rule.peerIDs[peerID]
	return ok
}

func (m *Manager) peerSharesSystemLocked(pb *peerBinding) bool {
	return pb.version != "" && pb.version == m.localVersion && m.localVersion != ""
}

func (m *Manager) allocPeerExternalOffsetLocked(pb *peerBinding) (uint64, error) {
	used := make(map[uint64]bool, len(pb.offsets))
	for _, off := range pb.offsets {
		used[off] = true
	}
	idx := uint64(OffsetIndexFirstExternal)
	for {
		off := idx * NamespaceOffsetStep
		if !used[off] {
			return off, nil
		}
		idx++
		if idx == 0 {
			return 0, fmt.Errorf("no external namespace offsets available")
		}
	}
}

func (m *Manager) LookupPeerLocal(peerID, namespace, name string) uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pb, ok := m.peerBindings[peerID]
	if !ok {
		return 0
	}
	ns, ok := pb.schemas[namespace]
	if !ok {
		return 0
	}
	return ns[name]
}

// LookupLocal returns the local schema ID for a struct name in the given namespace.
// Returns 0 if not found.
func (m *Manager) LookupLocal(namespace, name string) uint64 {
	k := key(namespace, name)
	m.mu.RLock()
	defer m.mu.RUnlock()
	if e, ok := m.static[k]; ok {
		return e.SchemaID
	}
	return m.dynamic[k]
}

// LookupLocalSystem is a convenience for the system namespace.
func (m *Manager) LookupLocalSystem(name string) uint64 {
	return m.LookupLocal(SystemNamespace, name)
}

// ToManifestImportJSON returns a manifest JSON fragment suitable for
// gospore's WithManifestImport. All static (system) schemas are converted to wire IDs.
func (m *Manager) ToManifestImportJSON() ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	schemas := make([]spore.ManifestSchema, 0, len(m.static))
	for _, e := range m.static {
		off := m.offsets[e.Namespace]
		schemas = append(schemas, spore.ManifestSchema{
			Namespace:  e.Namespace,
			SchemaID:   off + e.SchemaID,
			Name:       e.Name,
			Visibility: "public",
			Object:     e.Object,
		})
	}
	sort.Slice(schemas, func(i, j int) bool {
		if schemas[i].Namespace != schemas[j].Namespace {
			return schemas[i].Namespace < schemas[j].Namespace
		}
		return schemas[i].SchemaID < schemas[j].SchemaID
	})
	manifest := spore.Manifest{Schemas: schemas}
	return json.Marshal(manifest)
}

func validateLocalID(id uint64) error {
	if id == 0 {
		return fmt.Errorf("schema id 0 is reserved")
	}
	if id <= BuiltinReserveEnd {
		return fmt.Errorf("local schemaId %d is reserved for builtin scalars, use id >= %d", id, BuiltinReserveEnd+1)
	}
	if id > LocalIDMask {
		return fmt.Errorf("local schemaId %d exceeds local id mask 0x%x", id, LocalIDMask)
	}
	return nil
}

func key(namespace, name string) string {
	return namespace + ":" + name
}
