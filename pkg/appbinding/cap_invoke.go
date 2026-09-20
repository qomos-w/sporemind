package appbinding

// Spore script invoke capability. This is a load-time grant for Spore Apps
// (Runtime="spore"): declaring "spore.invoke" in the manifest Permissions
// unlocks the script-runtime host functions host.invoke / host.invoke_app
// (see sporeapp.capabilityHostBindings), letting the app's spore script
// relay to host actor callables. There are no SDK-facing pluginhost
// callIDs — like app.data this is a load-time grant exempt from the
// callID-coverage invariant (CapabilityHasCallGate returns false).

func init() {
	RegisterCapability(CapabilityDef{
		ID:                 CapSporeInvoke,
		Title:              "Spore 脚本调用宿主 callable",
		Description:        "允许应用的 Spore 脚本中继调用宿主 actor callable（含 MCP 工具与其他应用），调用仍受目标侧策略与超时约束。",
		RiskLevel:          RiskMedium,
		I18nTitleKey:       "capability.spore.invoke.title",
		I18nDescriptionKey: "capability.spore.invoke.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "Spore 脚本调用宿主 callable", Description: "允许应用的 Spore 脚本中继调用宿主 actor callable（含 MCP 工具与其他应用），调用仍受目标侧策略与超时约束。"},
			"en-US": {Title: "Spore script host invoke", Description: "Allows the app's Spore script to relay invocations to host actor callables (including MCP tools and other apps); targets still enforce their own policy and timeouts."},
		},
	})
}
