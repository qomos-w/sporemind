package appbinding

// Agent conversation access. One read-only callID aliased to the workspace
// agent-orchestration callable: a plugin (typically the app owning a
// plugin_agent) reads its bound agent's closed conversation steps so its
// panel can render the assistant side of the chat. Write-side agent
// orchestration (send/spawn/pause/...) stays bundle-gated and intentionally
// NOT exposed here.
//
// Authorization is two-layered, same as every host call: the capability gate
// here (manifest declaration), plus the backing handler's own check —
// workspace.agent_read_message's requireConversableGrant accepts a payload
// CallerAgentId equal to the target agent itself, which is how the caller
// scopes itself to its own plugin agent (the appmanager agentAction bridge
// uses the identical self-claim shape).
func init() {
	RegisterCapability(CapabilityDef{
		ID:          CapAgentRead,
		Title:       "读取 agent 会话",
		Description: "允许应用读取指定 agent 的对话文本（最近 N 条,只读）,用于面板渲染助手回复。",
		RiskLevel:   RiskLow,
		I18nTitleKey:       "capability.agent.read.title",
		I18nDescriptionKey: "capability.agent.read.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "读取 agent 会话", Description: "允许应用读取指定 agent 的对话文本（最近 N 条,只读）,用于面板渲染助手回复。"},
			"en-US": {Title: "Read agent conversation", Description: "Allows the app to read a target agent's conversation text (last N entries, read-only) to render assistant replies in its panel."},
		},
	})
	RegisterHostCall(HostCallDef{
		CallID:     "agent.read_messages",
		Capability: CapAgentRead,
		Target:     "workspace.agent_read_message",
	})
}
