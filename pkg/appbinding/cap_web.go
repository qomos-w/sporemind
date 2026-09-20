package appbinding

// Web capabilities. Web search and browser crawl are medium risk: search
// consumes the host's configured web-search provider quota and fetch/crawl
// drive outbound HTTP (and a real browser session for crawl), so an app
// declaring them can reach arbitrary URLs. All callIDs are pass-through: the
// wire IS the backing actor callable schema (gen.WebSearchReq/Resp,
// gen.WebFetchReq/Resp, gen.WebDownloadReq/Resp for websearch; and
// gen.BrowserCrawl*Req/Resp for crawl in pkg/domain/gen), so no adapted wire
// contract is declared and codegen resolves types from the embedded manifest
// schema IDs.
//
// crawl.handoff is registered AdminOnly on the crawl actor — the backing
// callable's own permission check still applies on the pass-through route.

func init() {
	RegisterCapability(CapabilityDef{
		ID:                 CapWebSearch,
		Title:              "网页搜索与抓取",
		Description:        "允许应用调用宿主网页搜索服务（websearch.search/fetch/download；消耗搜索供应商额度，fetch/download 触发对外 HTTP 请求）。",
		RiskLevel:          RiskMedium,
		I18nTitleKey:       "capability.web.search.title",
		I18nDescriptionKey: "capability.web.search.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "网页搜索与抓取", Description: "允许应用调用宿主网页搜索服务（websearch.search/fetch/download；消耗搜索供应商额度，fetch/download 触发对外 HTTP 请求）。"},
			"en-US": {Title: "Web search & fetch", Description: "Allows the app to call the host web-search service (websearch.search/fetch/download; consumes search-provider quota, fetch/download trigger outbound HTTP requests)."},
		},
	})
	RegisterHostCall(HostCallDef{CallID: "websearch.search", Capability: CapWebSearch})
	RegisterHostCall(HostCallDef{CallID: "websearch.fetch", Capability: CapWebSearch})
	RegisterHostCall(HostCallDef{CallID: "websearch.download", Capability: CapWebSearch})

	RegisterCapability(CapabilityDef{
		ID:                 CapWebFetch,
		Title:              "浏览器爬取",
		Description:        "允许应用驱动宿主浏览器爬取会话（crawl.start/status/results/handoff；拉起真实浏览器、访问任意 URL 并可能运行数分钟）。",
		RiskLevel:          RiskMedium,
		I18nTitleKey:       "capability.web.fetch.title",
		I18nDescriptionKey: "capability.web.fetch.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "浏览器爬取", Description: "允许应用驱动宿主浏览器爬取会话（crawl.start/status/results/handoff；拉起真实浏览器、访问任意 URL 并可能运行数分钟）。"},
			"en-US": {Title: "Browser crawl", Description: "Allows the app to drive host browser-crawl sessions (crawl.start/status/results/handoff; launches a real browser, visits arbitrary URLs, and may run for minutes)."},
		},
	})
	RegisterHostCall(HostCallDef{CallID: "crawl.start", Capability: CapWebFetch})
	RegisterHostCall(HostCallDef{CallID: "crawl.status", Capability: CapWebFetch})
	RegisterHostCall(HostCallDef{CallID: "crawl.results", Capability: CapWebFetch})
	RegisterHostCall(HostCallDef{CallID: "crawl.handoff", Capability: CapWebFetch})
}
