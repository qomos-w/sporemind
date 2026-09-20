package appbinding

// HostCallAliases maps SDK-facing host callIDs to the actual callIDs of the
// backing registered callables (the gospore manifest surface). It starts
// empty and is populated at package init by RegisterHostCall calls (see
// capabilities_registered.go and cap_*.go). The protocol query resolves
// schema/type information through the alias target; the pluginhost dispatch
// rewrites the callID before routing. CallIDs absent from this map are
// dispatched under their own name.
var HostCallAliases = map[string]string{}

// ResolveHostCall returns the actual callID for an SDK-facing host callID
// (identity when no alias exists).
func ResolveHostCall(callID string) string {
	if target, ok := HostCallAliases[callID]; ok {
		return target
	}
	return callID
}
