package capture

// Capability is one backend support declaration. Key is a stable machine
// string ("feature.<name>" or "action.<name>"); Supported reports whether
// this backend can honour it; Detail explains limitations or extra context.
type Capability struct {
	Key       string
	Supported bool
	Detail    string
}

// CapabilitySet is a backend's self-declared support matrix.
type CapabilitySet struct {
	Backend string
	Items   []Capability
}

// CapabilitySpec describes a backend's deviations from the full feature
// set. Every interact action not listed in UnsupportedActions is reported
// as supported; Features carries explicit feature.* entries.
type CapabilitySpec struct {
	Backend string
	// UnsupportedActions maps an interact action to the reason it fails
	// on this backend.
	UnsupportedActions map[string]string
	// Features are explicit feature.* entries (element tree, rich
	// clipboard, multi-display, ...).
	Features []Capability
}

// BuildCapabilities assembles a CapabilitySet from a backend spec.
func BuildCapabilities(spec CapabilitySpec) CapabilitySet {
	set := CapabilitySet{Backend: spec.Backend}
	set.Items = append(set.Items, spec.Features...)
	for _, action := range InteractActions {
		if reason, ok := spec.UnsupportedActions[action]; ok {
			set.Items = append(set.Items, Capability{
				Key:    "action." + action,
				Detail: reason,
			})
			continue
		}
		set.Items = append(set.Items, Capability{Key: "action." + action, Supported: true})
	}
	return set
}
