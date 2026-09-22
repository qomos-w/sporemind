package workspace

import (
	"fmt"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/policy"
	"github.com/qomos-w/sporemind/pkg/sporebridge"
)

// hostCallSeams adapts the workspace actor's PureContext into the minimal
// sporebridge.ServiceHost surface, mirroring sporeapp.actorSeams.
type hostCallSeams struct {
	lookupService func(string) (ref.Ref, bool)
	self          ref.Ref
}

func (s hostCallSeams) LookupService(name string) (ref.Ref, bool) { return s.lookupService(name) }
func (s hostCallSeams) Self() ref.Ref                             { return s.self }

// handleHostCall backs workspace.host_call, the generic host-callable relay
// declared by the builtin:bundle:sporecall bundle: it resolves CallId's
// service prefix, forwards Payload, and returns the decoded JSON response.
// Mounting that bundle is the agent-side gate; the target callable's own
// policy ladder still applies to every relayed call (the caller role is
// propagated verbatim). Stateless (PureContext): the relayed call runs on
// this forked goroutine, capped at domain.DefaultInvokeTimeout by the bridge.
func (a *Actor) handleHostCall(ctx actor.PureContext, req domain.WorkspaceHostCallReq) (domain.WorkspaceHostCallResp, error) {
	if err := requireAgentOrHuman(ctx.Identity().Role); err != nil {
		return domain.WorkspaceHostCallResp{}, err
	}
	if req.CallID == "" {
		return domain.WorkspaceHostCallResp{}, fmt.Errorf("workspace.host_call: callId is required")
	}
	bridge := sporebridge.NewHost(hostCallSeams{lookupService: ctx.LookupService, self: ctx.Self()}).
		WithRole(string(ctx.Identity().Role))
	result, err := bridge.InvokeCtx(ctx.Lifecycle(), req.CallID, req.Payload)
	if err != nil {
		return domain.WorkspaceHostCallResp{}, fmt.Errorf("workspace.host_call: %w", err)
	}
	return domain.WorkspaceHostCallResp{Result: result}, nil
}

// requireAgentOrHuman mirrors mcpmanager: internal callers (zero identity)
// pass, human roles pass, the anonymous web role is denied.
func requireAgentOrHuman(role id.Role) error {
	if role == "" {
		return nil
	}
	return policy.RequireAgentOrHuman(role)
}
