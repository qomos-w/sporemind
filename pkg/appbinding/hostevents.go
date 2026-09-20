package appbinding

import (
	"reflect"
	"sort"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// HostEvent describes one app-subscribable host event kind: a gospore event
// bus kind a plugin may listen to via the appdef `listen <kind> { }` block.
// The pluginhost subscribes the bus on the plugin's behalf and forwards
// matching events into the plugin under the reserved dispatch name
// `__event__:<kind>`, delivered to OnXxx handlers codegen emits.
type HostEvent struct {
	Kind        string
	PayloadType reflect.Type // JSON shape forwarded to the plugin
	Note        string       // surfaced in doc comments and the dev guide

	// RequiresCapability names a host capability (appbinding Cap* constant)
	// that the listening plugin's manifest must declare before the
	// pluginhost delivers events of this kind to it. Empty = catalog
	// membership alone authorizes delivery (app_lifecycle, app_event).
	RequiresCapability string
}

// EventCatalog is the single source of truth for app-subscribable host
// events. Only kinds registered here may be declared in a `listen` block —
// dev_generate rejects anything else, so an unknown kind fails at zero
// writes instead of silently never firing. Adding a kind here (plus a
// RegisterEventKind on the host side) is all it takes to expose a new
// event to apps.
var EventCatalog = map[string]HostEvent{
	"app_lifecycle": {
		Kind:        "app_lifecycle",
		PayloadType: reflect.TypeOf(gen.AppLifecycleEvent{}),
		Note:        "fired whenever any app registers, reloads, unloads, or fails; payload is AppLifecycleEvent",
	},
	"app_event": {
		Kind:        "app_event",
		PayloadType: reflect.TypeOf(gen.AppEventMessage{}),
		Note:        "generic envelope for events cast to bound consumers (Id/Event/Payload/Sender); phase 1 delivers the whole class, no per-event filtering",
	},
	"step": {
		Kind:               "step",
		PayloadType:        reflect.TypeOf(gen.StepEvent{}),
		RequiresCapability: CapAgentObserve,
		Note:               "agent step lifecycle: content blocks, deltas, tool calls, usage; payload is StepEvent; requires the agent.observe capability",
	},
	"agent_message_received": {
		Kind:               "agent_message_received",
		PayloadType:        reflect.TypeOf(gen.AgentMessage{}),
		RequiresCapability: CapAgentObserve,
		Note:               "messages received by any agent actor; payload is AgentMessage; requires the agent.observe capability",
	},
}

// LookupHostEvent returns the catalog entry for kind.
func LookupHostEvent(kind string) (HostEvent, bool) {
	he, ok := EventCatalog[kind]
	return he, ok
}

// SubscribableEventKinds returns the sorted catalog keys — the exhaustive
// listen vocabulary for validation error messages.
func SubscribableEventKinds() []string {
	kinds := make([]string, 0, len(EventCatalog))
	for k := range EventCatalog {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds
}
