package appbinding

import "fmt"

type CapabilityBinding struct {
	AppID       string
	AgentID     string
	Role        string
	ProjectID   string
	Permissions map[string]struct{}
	Callables   map[string]struct{}
}

func (b CapabilityBinding) Authorize(agentID, role, projectID, callable string) error {
	if b.AppID == "" || b.AgentID == "" {
		return Deny(CodeIdentityIncomplete, "agent binding identity is incomplete")
	}
	if agentID != b.AgentID {
		return Deny(CodeAgentScopeDenied, "caller agent does not match bound agent")
	}
	if b.Role != "" && role != b.Role {
		return Deny(CodeRoleDenied, "caller role does not match bound role")
	}
	if b.ProjectID != "" && projectID != b.ProjectID {
		return Deny(CodeProjectScopeDenied, "caller project does not match bound project scope")
	}
	if _, ok := b.Callables[callable]; !ok {
		return Deny(CodeCallableDenied, callable)
	}
	return nil
}

type SurfaceBinding struct {
	AppID       string
	AgentID     string
	Entrypoint  string
	Projections map[string]struct{}
	Events      map[string]struct{}
}

func (b SurfaceBinding) Validate() error {
	if b.AppID == "" || b.AgentID == "" || b.Entrypoint == "" {
		return fmt.Errorf("agent surface binding is incomplete")
	}
	return nil
}

type FreeAgentPolicy struct {
	AllowCreate  bool
	AllowSwitch  bool
	AllowMessage bool
	AgentKinds   map[string]struct{}
}

func (p FreeAgentPolicy) Allows(action, kind string) bool {
	return p.Authorize(action, kind) == nil
}

// Authorize returns a stable-coded DeniedError when the action is not allowed.
func (p FreeAgentPolicy) Authorize(action, kind string) error {
	switch action {
	case "create":
		if !p.AllowCreate {
			return Deny(CodeFreeAgentDenied, "create action not allowed")
		}
	case "switch":
		if !p.AllowSwitch {
			return Deny(CodeFreeAgentDenied, "switch action not allowed")
		}
	case "message":
		if !p.AllowMessage {
			return Deny(CodeFreeAgentDenied, "message action not allowed")
		}
	default:
		return Deny(CodeFreeAgentDenied, "unknown action "+action)
	}
	if len(p.AgentKinds) == 0 {
		return nil
	}
	if _, ok := p.AgentKinds[kind]; !ok {
		return Deny(CodeFreeAgentDenied, "agent kind not allowed: "+kind)
	}
	return nil
}
