package appbinding

import (
	"reflect"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// TestStreamRouteTypesMatchBackingDeclaration pins the standard chain: the
// route's advisory ChunkType must equal the type the backing actor declared
// with actor.Streaming[T]() (aiaggregator.dispatch → domain.AggregatorChunk),
// and the TerminalType must equal the SDKCallCatalog RespType. A drift here
// means the bridge route and the actor declaration disagree — the exact
// duplication class the generic registration was introduced to prevent.
func TestStreamRouteTypesMatchBackingDeclaration(t *testing.T) {
	for _, callID := range []string{"llm.complete", "llm.chat"} {
		route, ok := LookupStreamRoute(callID)
		if !ok {
			t.Fatalf("%s: no stream route registered", callID)
		}
		if route.ChunkType != reflect.TypeOf(domain.AggregatorChunk{}) {
			t.Errorf("%s: route ChunkType = %v, want domain.AggregatorChunk (the type aiaggregator.dispatch declared via actor.Streaming[T]())", callID, route.ChunkType)
		}
		sc, ok := LookupSDKCall(callID)
		if !ok {
			t.Fatalf("%s: missing from SDKCallCatalog", callID)
		}
		if route.TerminalType != sc.RespType {
			t.Errorf("%s: route TerminalType = %v, want catalog RespType %v", callID, route.TerminalType, sc.RespType)
		}
		// LLM generation runs minutes; the route must carry a Budget above
		// the generic transport cap or HTTP-data-path reverse calls die at
		// that cap mid-stream.
		if route.Budget <= 30*time.Second {
			t.Errorf("%s: route Budget = %v, want > 30s generic cap", callID, route.Budget)
		}
	}
}

type fakeChunk struct{ N int }
type fakeTerminal struct{ Sum int }

type fakeAgg struct {
	onPush     func(fakeChunk) error
	onTerminal func() fakeTerminal
}

func (a *fakeAgg) Push(c fakeChunk) error  { return a.onPush(c) }
func (a *fakeAgg) Terminal() fakeTerminal  { return a.onTerminal() }

// TestRegisterTypedStreamRouteWrapsTypes verifies the generic registration
// itself: chunk type-assertion lives in the wrapper (wrong chunk type errors
// instead of panicking), the typed aggregator's terminal flows through
// verbatim, and the advisory fields are stamped from the type parameters.
func TestRegisterTypedStreamRouteWrapsTypes(t *testing.T) {
	var lastChunk fakeChunk
	agg := &fakeAgg{onPush: func(c fakeChunk) error { lastChunk = c; return nil }, onTerminal: func() fakeTerminal { return fakeTerminal{Sum: lastChunk.N} }}
	RegisterTypedStreamRoute[fakeChunk, fakeTerminal]("test.typed-stream", StreamRoute{Service: "test", Callable: "test.call"}, func(c fakeChunk) (StreamChunkEnvelope, error) {
		return StreamChunkEnvelope{Kind: "n", Data: nil}, nil
	}, func() TypedStreamAggregator[fakeChunk, fakeTerminal] { return agg })

	route, ok := LookupStreamRoute("test.typed-stream")
	if !ok {
		t.Fatal("typed route not registered")
	}
	if route.ChunkType != reflect.TypeOf(fakeChunk{}) || route.TerminalType != reflect.TypeOf(fakeTerminal{}) {
		t.Fatalf("advisory types = %v/%v, want fakeChunk/fakeTerminal", route.ChunkType, route.TerminalType)
	}
	if _, err := route.Encode("not a chunk"); err == nil {
		t.Fatal("Encode with wrong chunk type must error, not panic")
	}
	aggInst := route.Aggregate()
	if err := aggInst.Push(struct{ Wrong bool }{}); err == nil {
		t.Fatal("Push with wrong chunk type must error")
	}
	if err := aggInst.Push(fakeChunk{N: 7}); err != nil {
		t.Fatalf("Push typed chunk: %v", err)
	}
	if got := aggInst.Terminal(); !reflect.DeepEqual(got, fakeTerminal{Sum: 7}) {
		t.Fatalf("Terminal = %#v, want fakeTerminal{Sum:7}", got)
	}
	delete(streamRoutes, "test.typed-stream")
}

func TestHostCallBudget_MediaGenLiftsGenericCap(t *testing.T) {
	// Media generation is a legitimately long unary host call. The per-callID
	// budget registered on HostCallDef.Budget must lift the generic 30s
	// reverse-call cap on every transport path (framed and HTTP-data).
	cases := map[string]struct {
		min time.Duration
	}{
		"image.generate": {min: 2 * time.Minute},
		"video.generate": {min: 10 * time.Minute}, // video LRO backends run long
	}
	for callID, c := range cases {
		b := HostCallBudget(callID)
		if b < c.min {
			t.Errorf("HostCallBudget(%q) = %v, want >= %v", callID, b, c.min)
		}
		// The generate callIDs are unary locals, not stream routes: a stream
		// route here would misroute the unary path into the streaming catalog.
		if _, ok := LookupStreamRoute(callID); ok {
			t.Errorf("%q must not register a stream route (unary local host service)", callID)
		}
	}
	if b := HostCallBudget("media.list_units"); b != 0 {
		t.Errorf("media.list_units must keep the generic cap, got budget %v", b)
	}
}
