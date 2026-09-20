package appbinding

// Insight capabilities. Read-only introspection over host usage statistics,
// the capability/service discovery surface (oracle), the runtime unified
// graph history, and the project wiki. All are RiskLow and pass-through: the
// wire IS the backing actor callable schema (aistats/oracle/unified_graph/
// project callables in pkg/domain/gen), so no adapted wire contract is
// declared and codegen resolves types from the embedded manifest schema IDs.
//
// Note: project.wiki_get_concept_tree is registered Internal on the project
// actor — the backing callable's own permission check still applies on the
// pass-through route; the remaining wiki callIDs are Public.

func init() {
	RegisterCapability(CapabilityDef{
		ID:                 CapStatsRead,
		Title:              "读取用量统计",
		Description:        "允许应用读取宿主 AI 用量统计（aistats.query/series/cost_list/aggregates：token 成本、调用序列与聚合视图，只读）。",
		RiskLevel:          RiskLow,
		I18nTitleKey:       "capability.stats.read.title",
		I18nDescriptionKey: "capability.stats.read.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "读取用量统计", Description: "允许应用读取宿主 AI 用量统计（aistats.query/series/cost_list/aggregates：token 成本、调用序列与聚合视图，只读）。"},
			"en-US": {Title: "Read usage statistics", Description: "Allows the app to read host AI usage statistics (aistats.query/series/cost_list/aggregates: token costs, call series, and aggregated views; read-only)."},
		},
	})
	RegisterHostCall(HostCallDef{CallID: "aistats.query", Capability: CapStatsRead})
	RegisterHostCall(HostCallDef{CallID: "aistats.series", Capability: CapStatsRead})
	RegisterHostCall(HostCallDef{CallID: "aistats.cost_list", Capability: CapStatsRead})
	RegisterHostCall(HostCallDef{CallID: "aistats.aggregates", Capability: CapStatsRead})

	RegisterCapability(CapabilityDef{
		ID:                 CapDiscoveryRead,
		Title:              "发现宿主能力与服务",
		Description:        "允许应用查询宿主能力目录与服务注册表（oracle.capability_discover/capability_explain/search_services/get_diagnostic/list_diagnostics + unified_graph.history：能力说明、服务清单、诊断报告与拓扑历史，只读元数据）。",
		RiskLevel:          RiskLow,
		I18nTitleKey:       "capability.discovery.read.title",
		I18nDescriptionKey: "capability.discovery.read.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "发现宿主能力与服务", Description: "允许应用查询宿主能力目录与服务注册表（oracle.capability_discover/capability_explain/search_services/get_diagnostic/list_diagnostics + unified_graph.history：能力说明、服务清单、诊断报告与拓扑历史，只读元数据）。"},
			"en-US": {Title: "Discover host capabilities & services", Description: "Allows the app to query the host capability catalog and service registry (oracle.capability_discover/capability_explain/search_services/get_diagnostic/list_diagnostics + unified_graph.history: capability explanations, service listings, diagnostic reports, and topology history; read-only metadata)."},
		},
	})
	RegisterHostCall(HostCallDef{CallID: "oracle.capability_discover", Capability: CapDiscoveryRead})
	RegisterHostCall(HostCallDef{CallID: "oracle.capability_explain", Capability: CapDiscoveryRead})
	RegisterHostCall(HostCallDef{CallID: "oracle.search_services", Capability: CapDiscoveryRead})
	RegisterHostCall(HostCallDef{CallID: "oracle.get_diagnostic", Capability: CapDiscoveryRead})
	RegisterHostCall(HostCallDef{CallID: "oracle.list_diagnostics", Capability: CapDiscoveryRead})
	RegisterHostCall(HostCallDef{CallID: "unified_graph.history", Capability: CapDiscoveryRead})

	RegisterCapability(CapabilityDef{
		ID:                 CapWikiRead,
		Title:              "读取项目 wiki",
		Description:        "允许应用读取项目 wiki 卡片（project.wiki_get_card/get_card_hierarchy/get_cards_batch/get_concept_tree/list_cards/search_card_content：卡片正文、层级与检索，只读）。",
		RiskLevel:          RiskLow,
		I18nTitleKey:       "capability.wiki.read.title",
		I18nDescriptionKey: "capability.wiki.read.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "读取项目 wiki", Description: "允许应用读取项目 wiki 卡片（project.wiki_get_card/get_card_hierarchy/get_cards_batch/get_concept_tree/list_cards/search_card_content：卡片正文、层级与检索，只读）。"},
			"en-US": {Title: "Read project wiki", Description: "Allows the app to read project wiki cards (project.wiki_get_card/get_card_hierarchy/get_cards_batch/get_concept_tree/list_cards/search_card_content: card bodies, hierarchy, and search; read-only)."},
		},
	})
	RegisterHostCall(HostCallDef{CallID: "project.wiki_get_card", Capability: CapWikiRead})
	RegisterHostCall(HostCallDef{CallID: "project.wiki_get_card_hierarchy", Capability: CapWikiRead})
	RegisterHostCall(HostCallDef{CallID: "project.wiki_get_cards_batch", Capability: CapWikiRead})
	RegisterHostCall(HostCallDef{CallID: "project.wiki_get_concept_tree", Capability: CapWikiRead})
	RegisterHostCall(HostCallDef{CallID: "project.wiki_list_cards", Capability: CapWikiRead})
	RegisterHostCall(HostCallDef{CallID: "project.wiki_search_card_content", Capability: CapWikiRead})
}
