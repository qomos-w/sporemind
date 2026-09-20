package appbinding

// Bundle capability. When an app declares a dependency on another plugin
// (dependency block in .appdef) and lists that plugin's callables in its
// permissions (e.g. "plugin.translator.translate"), the prefix mapping routes
// every plugin.* callID to this single capability. The HostBridge enforces a
// second, exact-callID authorization on top of the capability gate: a plugin
// granted plugin.X.translate cannot reach plugin.Y.other even though both
// map to bundle.invoke. The per-callID set is populated from the app's
// declared permissions at load time via SetAllowedBundleCalls.

func init() {
	RegisterCapability(CapabilityDef{
		ID:                 CapBundleInvoke,
		Title:              "插件互调（Bundle）",
		Description:        "允许应用调用其所依赖的其他插件暴露的 callable（跨插件调用）。",
		RiskLevel:          RiskMedium,
		CallPrefixes:       []string{"plugin."},
		I18nTitleKey:       "capability.bundle.invoke.title",
		I18nDescriptionKey: "capability.bundle.invoke.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "插件互调（Bundle）", Description: "允许应用调用其所依赖的其他插件暴露的 callable（跨插件调用）。"},
			"en-US": {Title: "Bundle invoke", Description: "Allows the app to call callables exposed by other plugins it depends on (cross-plugin invocation)."},
		},
	})
}
