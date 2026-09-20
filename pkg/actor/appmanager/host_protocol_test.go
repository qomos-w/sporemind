package appmanager

import (
	"strings"
	"testing"

	hostgen "github.com/qomos-w/sporemind/gen"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func gen2Req(query, capability string) gen.AppManagerHostProtocolReq {
	return gen.AppManagerHostProtocolReq{Query: query, Capability: capability}
}

func TestResolveHostCallsFromEmbeddedManifest(t *testing.T) {
	calls, err := ResolveHostCalls(hostgen.ManifestJSON())
	if err != nil {
		t.Fatalf("ResolveHostCalls: %v", err)
	}

	// Aliased SDK callIDs must resolve to their manifest targets with the
	// full wire protocol attached.
	llm, ok := calls["llm.complete"]
	if !ok {
		t.Fatal("llm.complete missing from resolved host calls")
	}
	if llm.TargetCallID != "aiaggregator.dispatch" || !llm.Streaming {
		t.Fatalf("llm.complete = %+v, want target aiaggregator.dispatch (streaming)", llm)
	}
	chat, ok := calls["llm.chat"]
	if !ok || chat.TargetCallID != "aiaggregator.dispatch" {
		t.Fatalf("llm.chat alias missing or wrong target: %+v", chat)
	}
	rf, ok := calls["project.read_file"]
	if !ok || rf.TargetCallID != "filesystem.read" || rf.ReqSchemaID == 0 {
		t.Fatalf("project.read_file = %+v, want filesystem.read with schemas", rf)
	}
	// workspace.list_projects must resolve to the workspace actor's mount
	// snapshot with the ProjectRefListResp wire attached (void request).
	wl, ok := calls["workspace.list_projects"]
	if !ok || wl.TargetCallID != "workspace.list_project" || wl.FinalSchemaID == 0 {
		t.Fatalf("workspace.list_projects = %+v, want workspace.list_project with response schema", wl)
	}

	// Direct (non-aliased) host callIDs must be reported under their own name.
	if se, ok := calls["shell.exec"]; !ok || se.TargetCallID != "shell.exec" {
		t.Fatalf("shell.exec = %+v, want self-targeted entry", se)
	}
	if _, ok := calls["sshmanager.exec"]; !ok {
		t.Fatal("sshmanager.exec missing from resolved host calls")
	}

	// media.accounts.list must resolve to the media actor's exposed callable
	// with the redacted account-list wire attached (data-driven from the
	// manifest, no hand-maintained typing list).
	ma, ok := calls["media.accounts.list"]
	if !ok {
		t.Fatal("media.accounts.list missing from resolved host calls")
	}
	if ma.TargetCallID != "media.list_accounts" || ma.ReqSchemaID == 0 || ma.FinalSchemaID == 0 {
		t.Fatalf("media.accounts.list = %+v, want media.list_accounts with schemas", ma)
	}

	// Locally-handled calls are present but carry no wire protocol.
	if c, ok := calls["config.get"]; !ok || c.ReqSchemaID != 0 {
		t.Fatalf("config.get = %+v, want local entry without schemas", c)
	}

	// Host-internal callIDs must never leak: they gate to no capability.
	for callID := range calls {
		if appbinding.HostCallCapability(callID) == "" {
			t.Errorf("host-internal callID %s leaked into plugin protocol", callID)
		}
	}
}

func TestHandleHostProtocolCapabilityFilter(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleHostProtocol(nil, gen2Req("", "fs.read"))
	if err != nil {
		t.Fatalf("handleHostProtocol: %v", err)
	}
	if len(resp.Capabilities) != 1 || resp.Capabilities[0].Capability != "fs.read" {
		t.Fatalf("capability filter: %+v", resp.Capabilities)
	}
	cap0 := resp.Capabilities[0]
	if cap0.Title == "" || cap0.RiskLevel == "" {
		t.Errorf("capability metadata missing: %+v", cap0)
	}
	found := false
	for _, c := range cap0.Calls {
		if c.CallID == "project.read_file" {
			found = true
			if c.TargetCallID != "filesystem.read" || c.RequestType != "FileSystemReadReq" || c.RequestSchemaID == 0 {
				t.Errorf("project.read_file info incomplete: %+v", c)
			}
		}
	}
	if !found {
		t.Errorf("fs.read must list project.read_file, got %+v", cap0.Calls)
	}
}

func TestHandleHostProtocolQueryFilter(t *testing.T) {
	a := &Actor{}
	// A query matching a host callID narrows to its capability.
	resp, err := a.handleHostProtocol(nil, gen2Req("read_file", ""))
	if err != nil {
		t.Fatalf("handleHostProtocol: %v", err)
	}
	if len(resp.Capabilities) != 1 || resp.Capabilities[0].Capability != "fs.read" {
		t.Fatalf("query filter 'read_file': %+v", resp.Capabilities)
	}

	// A broad query matches by capability/callID text across capabilities.
	resp, err = a.handleHostProtocol(nil, gen2Req("ssh", ""))
	if err != nil {
		t.Fatalf("handleHostProtocol: %v", err)
	}
	if len(resp.Capabilities) == 0 {
		t.Fatal("query filter 'ssh' must match ssh capabilities")
	}
	for _, cap0 := range resp.Capabilities {
		if !strings.HasPrefix(cap0.Capability, "ssh.") {
			t.Errorf("query 'ssh' matched unrelated capability %s", cap0.Capability)
		}
	}
}

func TestHandleHostProtocolLocalCallNote(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleHostProtocol(nil, gen2Req("", "app.state"))
	if err != nil {
		t.Fatalf("handleHostProtocol: %v", err)
	}
	if len(resp.Capabilities) != 1 {
		t.Fatalf("app.state capability missing: %+v", resp.Capabilities)
	}
	for _, c := range resp.Capabilities[0].Calls {
		if c.Note == "" {
			t.Errorf("local call %s must carry a catalog note, got %q", c.CallID, c.Note)
		}
		if c.RequestType == "" {
			t.Errorf("local call %s must surface its catalog wire contract, got empty request type", c.CallID)
		}
	}
	// state.get is the canonical local entry: typed Key/Value wrapper shapes.
	for _, c := range resp.Capabilities[0].Calls {
		if c.CallID == "state.get" && (c.RequestType != "StateKeyReq" || c.ResponseType != "StateGetResp") {
			t.Errorf("state.get wire contract = %s/%s, want StateKeyReq/StateGetResp", c.RequestType, c.ResponseType)
		}
	}
}

// TestHandleHostProtocolVoice pins the voice pass-through chain: the embedded
// manifest carries voice.recognize/voice.synthesize with gen schema IDs, the
// registration tables gate them to voice.stt/voice.tts, and the protocol
// query surfaces them with manifest-extracted types (no SDKCallCatalog
// override — the wire IS the backing callable schema).
func TestHandleHostProtocolVoice(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleHostProtocol(nil, gen2Req("", "voice.stt"))
	if err != nil {
		t.Fatalf("handleHostProtocol: %v", err)
	}
	if len(resp.Capabilities) != 1 || resp.Capabilities[0].Capability != "voice.stt" {
		t.Fatalf("voice.stt capability missing: %+v", resp.Capabilities)
	}
	found := false
	for _, c := range resp.Capabilities[0].Calls {
		if c.CallID == "voice.recognize" {
			found = true
			if c.TargetCallID != "voice.recognize" {
				t.Errorf("voice.recognize must be self-targeted (pass-through), got %q", c.TargetCallID)
			}
			if c.RequestType != "VoiceRecognizeReq" || c.RequestSchemaID == 0 {
				t.Errorf("voice.recognize request type = %s (schema %d), want manifest-extracted VoiceRecognizeReq", c.RequestType, c.RequestSchemaID)
			}
			if c.ResponseType != "VoiceRecognizeResp" {
				t.Errorf("voice.recognize response type = %s, want VoiceRecognizeResp", c.ResponseType)
			}
		}
	}
	if !found {
		t.Error("voice.recognize missing from voice.stt calls")
	}

	resp, err = a.handleHostProtocol(nil, gen2Req("", "voice.tts"))
	if err != nil {
		t.Fatalf("handleHostProtocol: %v", err)
	}
	if len(resp.Capabilities) != 1 || resp.Capabilities[0].Capability != "voice.tts" {
		t.Fatalf("voice.tts capability missing: %+v", resp.Capabilities)
	}
	found = false
	for _, c := range resp.Capabilities[0].Calls {
		if c.CallID == "voice.synthesize" {
			found = true
			if c.RequestType != "VoiceSynthesizeReq" || c.ResponseType != "VoiceSynthesizeResp" {
				t.Errorf("voice.synthesize contract = %s/%s, want VoiceSynthesizeReq/VoiceSynthesizeResp", c.RequestType, c.ResponseType)
			}
		}
	}
	if !found {
		t.Error("voice.synthesize missing from voice.tts calls")
	}
}

// TestHandleHostProtocolCatalogOverride pins that catalog callIDs report the
// SDK wire contract (LLMReq/LLMResp), not the backing gospore schema
// (SendSessionMessageReq) — app payloads are adapted.
func TestHandleHostProtocolCatalogOverride(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleHostProtocol(nil, gen2Req("llm.complete", ""))
	if err != nil {
		t.Fatalf("handleHostProtocol: %v", err)
	}
	if len(resp.Capabilities) != 1 || len(resp.Capabilities[0].Calls) == 0 {
		t.Fatalf("llm.complete query must return the llm capability: %+v", resp.Capabilities)
	}
	for _, c := range resp.Capabilities[0].Calls {
		if !c.Streaming {
			t.Errorf("%s must be reported streaming", c.CallID)
		}
		if strings.Contains(c.RequestType+c.ResponseType, "SendSessionMessage") {
			t.Errorf("%s leaked backing schema names: %s/%s", c.CallID, c.RequestType, c.ResponseType)
		}
	}
	found := false
	for _, c := range resp.Capabilities[0].Calls {
		if c.CallID == "llm.complete" {
			found = true
			if c.RequestType != "LLMReq" || c.ResponseType != "LLMResp" {
				t.Errorf("llm.complete contract = %s/%s, want LLMReq/LLMResp", c.RequestType, c.ResponseType)
			}
		}
	}
	if !found {
		t.Error("llm.complete missing from llm capability calls")
	}
}

// TestHandleHostProtocolInsightCaps pins the web.search / web.fetch /
// stats.read / discovery.read / wiki.read pass-through chain: the embedded
// manifest carries the backing callables (websearch.*, crawl.*, aistats.*,
// oracle.* + unified_graph.history, project.wiki_*), the appbinding
// registration tables gate them to the new capabilities, and the protocol
// query surfaces them self-targeted with manifest-extracted types — no
// SDKCallCatalog override, the wire IS the backing callable schema.
func TestHandleHostProtocolInsightCaps(t *testing.T) {
	cases := []struct {
		capID string
		calls map[string][2]string // callID -> {requestType, responseType} ("" = no schema on that side)
	}{
		{appbinding.CapWebSearch, map[string][2]string{
			"websearch.search":   {"WebSearchReq", "WebSearchResp"},
			"websearch.fetch":    {"WebFetchReq", "WebFetchResp"},
			"websearch.download": {"WebDownloadReq", "WebDownloadResp"},
		}},
		{appbinding.CapWebFetch, map[string][2]string{
			"crawl.start":   {"BrowserCrawlStartReq", "BrowserCrawlStartResp"},
			"crawl.status":  {"BrowserCrawlStatusReq", "BrowserCrawlStatusResp"},
			"crawl.results": {"BrowserCrawlResultsReq", "BrowserCrawlResultsResp"},
			"crawl.handoff": {"BrowserCrawlHandoffReq", "BrowserCrawlHandoffResp"},
		}},
		{appbinding.CapStatsRead, map[string][2]string{
			"aistats.query":      {"AIStatsQueryReq", "AIStatsQueryResp"},
			"aistats.series":     {"AIStatsSeriesReq", "AIStatsSeriesResp"},
			"aistats.cost_list":  {"AIStatsCostListReq", "AIStatsCostListResp"},
			"aistats.aggregates": {"AIStatsAggregatesReq", "AIStatsAggregatesResp"},
		}},
		{appbinding.CapDiscoveryRead, map[string][2]string{
			"oracle.capability_discover": {"OracleCapabilityDiscoverReq", "OracleCapabilityDiscoverResp"},
			"oracle.capability_explain":  {"OracleCapabilityExplainReq", "OracleCapabilityExplainResp"},
			"oracle.search_services":     {"OracleSearchServicesReq", "OracleSearchServicesResp"},
			"oracle.get_diagnostic":      {"OracleGetDiagnosticReq", "Diagnostic"},
			"oracle.list_diagnostics":    {"OracleListDiagnosticsReq", "OracleListDiagnosticsResp"},
			"unified_graph.history":      {"", "TopologyHistoryResp"}, // no req schema on the manifest callable
		}},
		{appbinding.CapWikiRead, map[string][2]string{
			"project.wiki_get_card":            {"WikiGetCardReq", "WikiGetCardResp"},
			"project.wiki_get_card_hierarchy":  {"WikiGetCardHierarchyReq", "WikiGetCardHierarchyResp"},
			"project.wiki_get_cards_batch":     {"WikiGetCardsBatchReq", "WikiGetCardsBatchResp"},
			"project.wiki_get_concept_tree":    {"WikiGetConceptTreeReq", "WikiGetConceptTreeResp"},
			"project.wiki_list_cards":          {"WikiListCardsReq", "WikiListCardsResp"},
			"project.wiki_search_card_content": {"WikiSearchCardContentReq", "WikiSearchCardContentResp"},
		}},
	}

	// The unfiltered query must surface every cataloged capability — the
	// projection iterates appbinding.CapabilityCatalog directly, so the 5
	// new capabilities appear automatically once registered (23 total).
	a := &Actor{}
	resp, err := a.handleHostProtocol(nil, gen2Req("", ""))
	if err != nil {
		t.Fatalf("handleHostProtocol: %v", err)
	}
	if len(resp.Capabilities) != len(appbinding.CapabilityCatalog) {
		t.Fatalf("projection lists %d capabilities, want all %d cataloged", len(resp.Capabilities), len(appbinding.CapabilityCatalog))
	}

	for _, tc := range cases {
		resp, err := a.handleHostProtocol(nil, gen2Req("", tc.capID))
		if err != nil {
			t.Fatalf("handleHostProtocol(%s): %v", tc.capID, err)
		}
		if len(resp.Capabilities) != 1 || resp.Capabilities[0].Capability != tc.capID {
			t.Fatalf("capability filter %s: %+v", tc.capID, resp.Capabilities)
		}
		got := map[string][2]string{}
		for _, c := range resp.Capabilities[0].Calls {
			got[c.CallID] = [2]string{c.RequestType, c.ResponseType}
			if c.TargetCallID != c.CallID {
				t.Errorf("%s.%s must be self-targeted (pass-through), got %q", tc.capID, c.CallID, c.TargetCallID)
			}
		}
		for callID, want := range tc.calls {
			g, ok := got[callID]
			if !ok {
				t.Errorf("%s must list %s, got %+v", tc.capID, callID, resp.Capabilities[0].Calls)
				continue
			}
			if g != want {
				t.Errorf("%s contract = %s/%s, want %s/%s", callID, g[0], g[1], want[0], want[1])
			}
		}
	}
}

// TestHandleHostProtocolBrowserCookies pins the browser.cookies.read chain:
// the capability is cataloged with RiskHigh, and the protocol query surfaces
// browser.cookies_export as a pluginhost-local call whose adapted wire
// contract (BrowserCookiesExportReq → BrowserManagerExportCookiesResp) comes
// from the SDKCallCatalog, not a manifest schema.
func TestHandleHostProtocolBrowserCookies(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleHostProtocol(nil, gen2Req("", "browser.cookies.read"))
	if err != nil {
		t.Fatalf("handleHostProtocol: %v", err)
	}
	if len(resp.Capabilities) != 1 {
		t.Fatalf("capability filter browser.cookies.read: %+v", resp.Capabilities)
	}
	capInfo := resp.Capabilities[0]
	if capInfo.RiskLevel != "high" {
		t.Errorf("risk level = %q, want high (cookies are plaintext credentials)", capInfo.RiskLevel)
	}
	if len(capInfo.Calls) != 1 || capInfo.Calls[0].CallID != "browser.cookies_export" {
		t.Fatalf("browser.cookies.read calls = %+v, want exactly browser.cookies_export", capInfo.Calls)
	}
	c := capInfo.Calls[0]
	if c.TargetCallID != "browser.cookies_export" {
		t.Errorf("browser.cookies_export must be self-targeted (local), got %q", c.TargetCallID)
	}
	if c.RequestType != "BrowserCookiesExportReq" || c.ResponseType != "BrowserManagerExportCookiesResp" {
		t.Errorf("browser.cookies_export contract = %s/%s, want BrowserCookiesExportReq/BrowserManagerExportCookiesResp", c.RequestType, c.ResponseType)
	}
	if c.Streaming {
		t.Error("browser.cookies_export must not be reported streaming")
	}
}

// TestHandleHostProtocolBrowserInstances pins the browser.instances.list
// chain: RiskMedium catalog entry with browser_instances_list as a
// pluginhost-local call carrying the minimal BrowserInstancesListReq →
// BrowserInstancesListResp contract.
func TestHandleHostProtocolBrowserInstances(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleHostProtocol(nil, gen2Req("", "browser.instances.list"))
	if err != nil {
		t.Fatalf("handleHostProtocol: %v", err)
	}
	if len(resp.Capabilities) != 1 {
		t.Fatalf("capability filter browser.instances.list: %+v", resp.Capabilities)
	}
	capInfo := resp.Capabilities[0]
	if capInfo.RiskLevel != "medium" {
		t.Errorf("risk level = %q, want medium (instance existence + URL/title, no credentials)", capInfo.RiskLevel)
	}
	if len(capInfo.Calls) != 1 || capInfo.Calls[0].CallID != "browser_instances_list" {
		t.Fatalf("browser.instances.list calls = %+v, want exactly browser_instances_list", capInfo.Calls)
	}
	c := capInfo.Calls[0]
	if c.TargetCallID != "browser_instances_list" {
		t.Errorf("browser_instances_list must be self-targeted (local), got %q", c.TargetCallID)
	}
	if c.RequestType != "BrowserInstancesListReq" || c.ResponseType != "BrowserInstancesListResp" {
		t.Errorf("browser_instances_list contract = %s/%s, want BrowserInstancesListReq/BrowserInstancesListResp", c.RequestType, c.ResponseType)
	}
	if c.Streaming {
		t.Error("browser_instances_list must not be reported streaming")
	}
}
