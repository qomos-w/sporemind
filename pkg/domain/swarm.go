package domain

// Swarm spawn: persistent, card-free sub-agents spawned agent-to-agent.
//
// Unlike workflow workers (workspace.agent_spawn_assign, bound to a task card)
// and fork children (single-turn, auto-terminated), a swarm child is a full
// agent: it self-assigns a spawn-time goal, runs its own turn loop, mounts the
// swarm bundle (so it can spawn further swarm children, bounded by
// MaxSwarmDepth), and stays alive until its parent reviews and terminates it.

const (
	// MaxSwarmDepth bounds the swarm recursion depth. Depth 0 is the
	// spawning root agent; its swarm children are depth 1. An agent at
	// depth MaxSwarmDepth can no longer spawn.
	MaxSwarmDepth = 3

	// MaxSwarmChildrenPerAgent caps the number of live swarm children a
	// single parent agent may hold at once.
	MaxSwarmChildrenPerAgent = 8

	// LifecycleScopeSwarm marks agents spawned via workspace.agent_spawn_swarm.
	LifecycleScopeSwarm = "swarm"
)

// WorkspaceAgentSpawnSwarmReq spawns a persistent, card-free swarm sub-agent
// under the calling agent. CallerAgentId is injected by the turn engine and is
// the authoritative parent identity.
type WorkspaceAgentSpawnSwarmReq struct {
	Description   string     `json:"Description"`
	Prompt        string     `json:"Prompt,omitempty"`
	AgentKind     string     `json:"AgentKind,omitempty"`
	MaxTurns      int32      `json:"MaxTurns,omitempty"`
	Unit          *ModelUnit `json:"Unit,omitempty"`
	ProjectID     string     `json:"ProjectId,omitempty"`
	CallerAgentID string     `json:"CallerAgentId,omitempty"`
}

// WorkspaceAgentSpawnSwarmResp returns the spawned child identity plus the
// depth the child occupies in the swarm recursion tree.
type WorkspaceAgentSpawnSwarmResp struct {
	ChildActorID string `json:"ChildActorId"`
	DisplayName  string `json:"DisplayName"`
	Depth        int32  `json:"Depth"`
}
