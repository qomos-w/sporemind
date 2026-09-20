package pluginhost

import "context"

type invokeMetaKey struct{}

// InvokeMeta carries traceable metadata for a single native plugin invocation.
type InvokeMeta struct {
	RequestID string
	SessionID string
	CallSeq   int64
	// AgentID / WorkspaceID identify the actor caller of the forward invoke;
	// they are propagated into the ABI envelope and mirrored into reverse
	// calls so bridge-routed llm.* dispatches can attribute usage stats to
	// the originating agent and workspace instead of leaving both empty.
	AgentID     string
	WorkspaceID string
}

// WithInvokeMeta attaches meta to ctx so that loader/transport layers can
// encode it into the ABI envelope without threading extra arguments.
func WithInvokeMeta(ctx context.Context, meta InvokeMeta) context.Context {
	return context.WithValue(ctx, invokeMetaKey{}, meta)
}

// InvokeMetaFrom extracts the meta previously attached by WithInvokeMeta.
func InvokeMetaFrom(ctx context.Context) (InvokeMeta, bool) {
	meta, ok := ctx.Value(invokeMetaKey{}).(InvokeMeta)
	return meta, ok
}
