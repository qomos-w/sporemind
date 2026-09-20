package appbinding

import (
	"reflect"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestEventCatalogAgentEntries(t *testing.T) {
	step, ok := EventCatalog["step"]
	if !ok {
		t.Fatal("catalog missing step entry")
	}
	if step.PayloadType != reflect.TypeOf(gen.StepEvent{}) {
		t.Errorf("step payload type = %v, want gen.StepEvent", step.PayloadType)
	}
	if step.RequiresCapability != CapAgentObserve {
		t.Errorf("step RequiresCapability = %q, want %q", step.RequiresCapability, CapAgentObserve)
	}

	msg, ok := EventCatalog["agent_message_received"]
	if !ok {
		t.Fatal("catalog missing agent_message_received entry")
	}
	if msg.PayloadType != reflect.TypeOf(gen.AgentMessage{}) {
		t.Errorf("agent_message_received payload type = %v, want gen.AgentMessage", msg.PayloadType)
	}
	if msg.RequiresCapability != CapAgentObserve {
		t.Errorf("agent_message_received RequiresCapability = %q, want %q", msg.RequiresCapability, CapAgentObserve)
	}
}

func TestEventCatalogLegacyEntriesUnrestricted(t *testing.T) {
	// app_lifecycle / app_event must remain capability-free: catalog
	// membership alone authorizes them.
	for _, kind := range []string{"app_lifecycle", "app_event"} {
		he, ok := EventCatalog[kind]
		if !ok {
			t.Fatalf("catalog missing %s entry", kind)
		}
		if he.RequiresCapability != "" {
			t.Errorf("%s RequiresCapability = %q, want empty", kind, he.RequiresCapability)
		}
	}
}

func TestSubscribableEventKindsIncludesAgentKinds(t *testing.T) {
	kinds := SubscribableEventKinds()
	want := map[string]bool{"step": false, "agent_message_received": false, "app_lifecycle": false, "app_event": false}
	for _, k := range kinds {
		if _, ok := want[k]; ok {
			want[k] = true
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("SubscribableEventKinds missing %q", k)
		}
	}
}

func TestLookupHostEventCapabilityIsKnown(t *testing.T) {
	// Every RequiresCapability value must itself be a known host capability,
	// otherwise the pluginhost gate would reject every delivery.
	for kind, he := range EventCatalog {
		if he.RequiresCapability == "" {
			continue
		}
		if !IsKnownHostCapability(he.RequiresCapability) {
			t.Errorf("event %q requires unknown capability %q", kind, he.RequiresCapability)
		}
	}
}
