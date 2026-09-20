package sporeapp

// This file consolidates package-level variable declarations for the sporeapp
// package.

// truncDetail caps error/diagnostic detail at 512 bytes before it is embedded
// in a returned error or crosses an actor boundary. User script compile
// diagnostics and runtime failures carry code/path/span/stack payloads that
// are unbounded in aggregate (openai respSnippet lesson).
func truncDetail(s string) string {
	if len(s) > 512 {
		return s[:512] + "...(truncated)"
	}
	return s
}

// --- Capability host bindings ---

// capabilityHostBindings maps each manifest permission to the host functions
// it unlocks. A QuickApp can only call host functions whose capability was
// declared in its manifest AND allowed by the manager's SecurityPolicy (the
// effective set is passed via NewActor). Adding a new capability requires
// only a new entry here.
var capabilityHostBindings = map[string][]capabilityHostBinding{
	"state": {
		{Namespace: "app", Name: "stateGet", bind: func(a *Actor) any { return a.hostStateGet }},
		{Namespace: "app", Name: "stateSet", bind: func(a *Actor) any { return a.hostStateSet }},
	},
	"spore.invoke": {
		{Namespace: "host", Name: "invoke", bind: func(a *Actor) any { return a.hostInvoke }},
		{Namespace: "host", Name: "invoke_app", bind: func(a *Actor) any { return a.hostInvokeApp }},
	},
}
