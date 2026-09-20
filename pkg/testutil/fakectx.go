package testutil

import (
	"io"
	"strings"
	"sync"
	"time"

	"context"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/codec"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/gospore/resource"
	"github.com/qomos-w/gospore/schema"
)

type noOpLogger struct{}

func (noOpLogger) Debug(string, ...any)  {}
func (noOpLogger) Info(string, ...any)   {}
func (noOpLogger) Warn(string, ...any)   {}
func (noOpLogger) Error(string, ...any)  {}

type fakeRef struct {
	actorID  id.ActorID
	invokeFn func(callID string, payload any) any
}

func (f fakeRef) ID() id.ActorID        { return f.actorID }
func (fakeRef) Service() (string, bool) { return "", false }
func (f fakeRef) Invoke(_ context.Context, callID string, payload any, _ ...map[string]string) *invoke.Call {
	if f.invokeFn == nil {
		return nil
	}
	v := f.invokeFn(callID, payload)
	return invoke.NewCall(invoke.CallModeUnary, &fakeStream{value: v})
}

// fakeStream returns one value frame then io.EOF.
type fakeStream struct {
	value any
	consumed bool
}

func (s *fakeStream) Recv() (any, error) {
	if s.consumed {
		return nil, io.EOF
	}
	s.consumed = true
	if err, ok := s.value.(error); ok {
		return nil, err
	}
	return s.value, nil
}
func (s *fakeStream) RecvRaw() ([]byte, error) { return nil, io.EOF }
func (s *fakeStream) Close() error             { return nil }

// NewFakeRef constructs a fake ref.Ref that returns a value for unary Invokes.
func NewFakeRef(actorID id.ActorID, invokeFn func(callID string, payload any) any) ref.Ref {
	return fakeRef{actorID: actorID, invokeFn: invokeFn}
}

// FakeCtx provides a minimal actor.Context for unit-testing handlers.
type FakeCtx struct {
	mu sync.Mutex
	SelfRef    ref.Ref
	ParentRef  ref.Ref
	CallerRef  ref.Ref // optional; Caller() returns this (nil by default)
	Identity_  id.Identity
	Regs       map[string]any
	// RegOpts, when non-nil, records the RegisterOptions passed to each
	// Register call so tests can assert visibility/description declarations
	// (via actor.ResolveVisibility etc.).
	RegOpts map[string][]actor.RegisterOption
	// Loops records RegisterLoop calls as loopName -> HandlerMode.
	Loops     map[string]actor.HandlerMode
	SpawnFn    func(props actor.Props, name string) (ref.Ref, error)
	// EmittedEvents records calls to EmitEvent as [kind, payload].
	EmittedEvents []EmittedEvent
	Children_     []ref.Ref
	PlannerFn     func() actor.Planner
	StopFn          func(target ref.Ref) error
	DestroyFn       func(target ref.Ref) error
	LookupIDFn      func(id.ActorID) (ref.Ref, bool)
	LookupServiceFn func(name string) (ref.Ref, bool)
	RootFn          func() ref.Ref
	// RegisteredDomains records RegisterDomain calls so tests can assert
	// which domains were registered and exposed.
	RegisteredDomains []string
	// domains mirrors the real cell's domain tracking (cell.AddDomain) and
	// feeds ServiceNameFor derivation in Register/RegisterScript.
	domains []string
	// ServiceNames records the derived route service name for every
	// Register/RegisterScript call, mirroring the real cell's handler table:
	// the name derives via actor.ServiceNameFor from the callID's first
	// segment when it matches a declared domain (the sole source — the
	// WithService option no longer exists). Flat (dotless) callIDs never
	// derive — "" means agent-local, the routing layer falls back to Self().
	ServiceNames map[string]string
	// AfterFn overrides the default no-op After for tests that need to
	// verify delayed self-invocation (e.g. deletion finalization).
	AfterFn              func(delay time.Duration, callID string, payload any) error
	// HasEventSubscribersFn overrides the default false return for tests
	// that verify event emission.
	HasEventSubscribersFn func(kind string) bool
	idGen                  *id.Canonical
}

// EmittedEvent captures a single EmitEvent call.
type EmittedEvent struct {
	Kind    string
	Payload any
}

func (f *FakeCtx) Self() ref.Ref          { return f.SelfRef }
func (f *FakeCtx) Parent() ref.Ref        { return f.ParentRef }
func (f *FakeCtx) Children() []ref.Ref     { return f.Children_ }
func (f *FakeCtx) Caller() ref.Ref         { return f.CallerRef }
func (*FakeCtx) CallID() string          { return "" }
func (f *FakeCtx) Identity() id.Identity { return f.Identity_ }
func (*FakeCtx) Done() <-chan struct{}   { return nil }
func (*FakeCtx) Lifecycle() context.Context { return context.Background() }
func (*FakeCtx) Logger() actor.Logger    { return noOpLogger{} }
func (f *FakeCtx) NewID() id.ActorID {
	if f.idGen == nil {
		f.idGen = id.NewCanonical(1, 0, func() uint64 { return uint64(time.Now().UnixMilli()) })
	}
	return f.idGen.Next()
}

func (f *FakeCtx) Register(callID string, handler any, opts ...actor.RegisterOption) error {
	if f.Regs != nil {
		f.Regs[callID] = handler
	}
	if f.RegOpts != nil {
		f.RegOpts[callID] = opts
	}
	f.recordServiceName(callID)
	return nil
}
func (f *FakeCtx) RegisterScript(callID string, _ string, _ actor.HandlerMode, opts ...actor.RegisterOption) error {
	f.recordServiceName(callID)
	return nil
}

// recordServiceName stores the derived service name for callID, mirroring
// the real cell's forward derivation (cell.deriveServiceName): the name
// comes solely from actor.ServiceNameFor against the declared domains.
// Flat callIDs derive to "" (agent-local).
func (f *FakeCtx) recordServiceName(callID string) {
	if f.ServiceNames == nil {
		f.ServiceNames = make(map[string]string)
	}
	f.ServiceNames[callID] = actor.ServiceNameFor(f.domains, callID)
}

// stampServiceForDomain backfills already-recorded callables whose effective
// service name is empty and whose callID first segment equals domain,
// mirroring handler.Table.StampServiceForDomain in the real cell. Flat
// (dotless) callIDs are never stamped. Returns the number stamped.
func (f *FakeCtx) stampServiceForDomain(domain string) int {
	n := 0
	for callID, svc := range f.ServiceNames {
		if svc != "" {
			continue
		}
		i := strings.IndexByte(callID, '.')
		if i <= 0 {
			continue
		}
		if callID[:i] == domain {
			f.ServiceNames[callID] = domain
			n++
		}
	}
	return n
}

func (FakeCtx) AttachComponent(string, uint64, any, ...actor.RegisterOption) error { return nil }

// Domains returns a snapshot of the domain names declared via RegisterDomain,
// mirroring cell.Domains.
func (f *FakeCtx) Domains() []string {
	out := make([]string, len(f.domains))
	copy(out, f.domains)
	return out
}

// RegisterDomain records the domain name and returns a nil-safe DomainHandle.
// RegisteredDomains collects the registered domain names for test assertions.
// Like the real cell, the domain feeds later Register/RegisterScript
// derivation and backfills already-recorded callables whose first callID
// segment matches (and whose derived service name is still empty).
func (f *FakeCtx) RegisterDomain(name string) *actor.DomainHandle {
	if f.RegisteredDomains != nil {
		f.RegisteredDomains = append(f.RegisteredDomains, name)
	}
	f.domains = append(f.domains, name)
	f.stampServiceForDomain(name)
	return actor.NewDomainHandle(name, nil, nil)
}
func (FakeCtx) RegisterEventKind(string, any, ...actor.RegisterOption) error       { return nil }
func (FakeCtx) SubscribeEventKind(string, func(actor.EventEnvelope)) (func(), error) { return func() {}, nil }
func (f *FakeCtx) RegisterLoop(name string, mode actor.HandlerMode) error {
	if f.Loops != nil {
		f.Loops[name] = mode
	}
	return nil
}
func (FakeCtx) RegisterTimer(string, ...actor.RegisterOption) error                  { return nil }

func (f *FakeCtx) Spawn(props actor.Props, name string) (ref.Ref, error) {
	if f.SpawnFn != nil {
		return f.SpawnFn(props, name)
	}
	return nil, nil
}
func (f *FakeCtx) Stop(target ref.Ref) error {
	if f.StopFn != nil {
		return f.StopFn(target)
	}
	return nil
}
func (f *FakeCtx) Destroy(target ref.Ref) error {
	if f.DestroyFn != nil {
		return f.DestroyFn(target)
	}
	return nil
}
func (FakeCtx) Watch(ref.Ref, ...actor.WatchKind) error      { return nil }
func (FakeCtx) Unwatch(ref.Ref)                              {}

func (f *FakeCtx) LookupID(aid id.ActorID) (ref.Ref, bool) {
	if f.LookupIDFn != nil {
		return f.LookupIDFn(aid)
	}
	return nil, false
}
func (f *FakeCtx) LookupService(name string) (ref.Ref, bool) {
	if f.LookupServiceFn != nil {
		return f.LookupServiceFn(name)
	}
	return nil, false
}

func (FakeCtx) Namespace() string                   { return "test" }
func (FakeCtx) Codec() codec.Codec                  { return nil }
func (FakeCtx) Schemas() schema.Reader              { return nil }
func (FakeCtx) Resources() resource.Registry        { return resource.New() }
func (f *FakeCtx) Root() ref.Ref {
	if f.RootFn != nil {
		return f.RootFn()
	}
	return nil
}
func (FakeCtx) Clock() actor.Clock                  { return nil }
func (f *FakeCtx) After(delay time.Duration, callID string, payload any) error {
	if f.AfterFn != nil {
		return f.AfterFn(delay, callID, payload)
	}
	return nil
}

func (f *FakeCtx) Planner() actor.Planner {
	if f.PlannerFn != nil {
		return f.PlannerFn()
	}
	return nil
}

func (f *FakeCtx) EmitEvent(kind string, payload any) error {
	f.mu.Lock()
	f.EmittedEvents = append(f.EmittedEvents, EmittedEvent{Kind: kind, Payload: payload})
	f.mu.Unlock()
	return nil
}

// EmittedEventsSnapshot returns a copy of the events emitted so far. Safe to
// call concurrently with EmitEvent (e.g. polling from a test goroutine while a
// background goroutine keeps publishing).
func (f *FakeCtx) EmittedEventsSnapshot() []EmittedEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]EmittedEvent(nil), f.EmittedEvents...)
}
func (f *FakeCtx) HasEventSubscribers(kind string) bool {
	if f.HasEventSubscribersFn != nil {
		return f.HasEventSubscribersFn(kind)
	}
	return false
}
func (FakeCtx) EventSubscriberCount(string) int  { return 0 }

var _ actor.Context = (*FakeCtx)(nil)
var _ actor.PureContext = (*FakeCtx)(nil)

// HumanCtx returns a FakeCtx with role "developer". The legacy "human" role
// has been folded into "developer" as the user-facing UI role.
func HumanCtx(actorID id.ActorID) *FakeCtx {
	return &FakeCtx{
		SelfRef:   fakeRef{actorID: actorID},
		Identity_: id.Identity{Role: "developer"},
		Regs:      map[string]any{},
		Loops:     map[string]actor.HandlerMode{},
	}
}

// AdminCtx returns a FakeCtx with role "admin".
func AdminCtx(actorID id.ActorID) *FakeCtx {
	return &FakeCtx{
		SelfRef:   fakeRef{actorID: actorID},
		Identity_: id.Identity{Role: "admin"},
		Regs:      map[string]any{},
		Loops:     map[string]actor.HandlerMode{},
	}
}

// DeveloperCtx returns a FakeCtx with role "developer".
func DeveloperCtx(actorID id.ActorID) *FakeCtx {
	return &FakeCtx{
		SelfRef:   fakeRef{actorID: actorID},
		Identity_: id.Identity{Role: "developer"},
		Regs:      map[string]any{},
		Loops:     map[string]actor.HandlerMode{},
	}
}

// OperatorCtx returns a FakeCtx with role "operator".
func OperatorCtx(actorID id.ActorID) *FakeCtx {
	return &FakeCtx{
		SelfRef:   fakeRef{actorID: actorID},
		Identity_: id.Identity{Role: "operator"},
		Regs:      map[string]any{},
		Loops:     map[string]actor.HandlerMode{},
	}
}

// AgentCtx returns a FakeCtx with role "agent".
func AgentCtx(actorID id.ActorID) *FakeCtx {
	return &FakeCtx{
		SelfRef:   fakeRef{actorID: actorID},
		Identity_: id.Identity{Role: "agent"},
		Regs:      map[string]any{},
		Loops:     map[string]actor.HandlerMode{},
	}
}

// SystemCtx returns a FakeCtx with role "system".
func SystemCtx(actorID id.ActorID) *FakeCtx {
	return &FakeCtx{
		SelfRef:   fakeRef{actorID: actorID},
		Identity_: id.Identity{Role: "system"},
		Regs:      map[string]any{},
		Loops:     map[string]actor.HandlerMode{},
	}
}

// AnonCtx returns a FakeCtx with empty (anonymous) role.
func AnonCtx(actorID id.ActorID) *FakeCtx {
	return &FakeCtx{
		SelfRef:   fakeRef{actorID: actorID},
		Identity_: id.Identity{},
	}
}

// GenActorID creates a deterministic ActorID for testing.
func GenActorID() id.ActorID {
	g := id.NewCanonical(1, 0, func() uint64 { return 1 })
	return g.Next()
}

// FakeEmitter is an in-memory actor.Emitter for unit-testing streaming
// handlers. Send appends to Chunks; Done returns DoneCh which tests can
// close to simulate caller-side cancellation.
type FakeEmitter struct {
	Chunks []any
	DoneCh chan struct{}
}

// NewFakeEmitter constructs a FakeEmitter with an open Done channel.
func NewFakeEmitter() *FakeEmitter {
	return &FakeEmitter{DoneCh: make(chan struct{})}
}

func (e *FakeEmitter) Send(chunk any) error {
	e.Chunks = append(e.Chunks, chunk)
	return nil
}

func (e *FakeEmitter) Done() <-chan struct{} {
	return e.DoneCh
}

var _ actor.Emitter = (*FakeEmitter)(nil)
