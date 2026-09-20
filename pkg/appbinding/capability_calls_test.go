package appbinding

import "testing"

func TestHostCallCapability(t *testing.T) {
	cases := []struct {
		callID string
		want   string
	}{
		{"llm.complete", CapLLMInvoke},
		{"llm.chat", CapLLMInvoke},
		{"project.read_file", CapFSRead},
		{"project.write_file", CapFSWrite},
		{"shell.exec", CapShellExec},
		{"config.get", CapConfigRead},
		{"provider.list", CapProviderRead},
		{"provider.get", CapProviderRead},
		{"aggregator.list", CapAggregatorRead},
		{"state.get", CapAppState},
		{"state.set", CapAppState},
		{"sshmanager.exec", CapSSHInvoke},
		{"app.emit", CapAppEmit},
		{"registry.query", CapRegistryRead},
		{"voice.recognize", CapVoiceSTT},
		{"voice.synthesize", CapVoiceTTS},
		{"voice.accounts.list", CapVoiceRead},
		{"media.list_units", CapMediaRead},
		{"media.accounts.list", CapMediaRead},
		{"image.generate", CapImageGen},
		{"video.generate", CapVideoGen},
		// web.search capability (cap_web.go)
		{"websearch.search", CapWebSearch},
		{"websearch.fetch", CapWebSearch},
		{"websearch.download", CapWebSearch},
		// web.fetch capability — browser crawl (cap_web.go)
		{"crawl.start", CapWebFetch},
		{"crawl.status", CapWebFetch},
		{"crawl.results", CapWebFetch},
		{"crawl.handoff", CapWebFetch},
		// stats.read capability (cap_insight.go)
		{"aistats.query", CapStatsRead},
		{"aistats.series", CapStatsRead},
		{"aistats.cost_list", CapStatsRead},
		{"aistats.aggregates", CapStatsRead},
		// discovery.read capability (cap_insight.go)
		{"oracle.capability_discover", CapDiscoveryRead},
		{"oracle.capability_explain", CapDiscoveryRead},
		{"oracle.search_services", CapDiscoveryRead},
		{"oracle.get_diagnostic", CapDiscoveryRead},
		{"oracle.list_diagnostics", CapDiscoveryRead},
		{"unified_graph.history", CapDiscoveryRead},
		// wiki.read capability (cap_insight.go)
		{"project.wiki_get_card", CapWikiRead},
		{"project.wiki_get_card_hierarchy", CapWikiRead},
		{"project.wiki_get_cards_batch", CapWikiRead},
		{"project.wiki_get_concept_tree", CapWikiRead},
		{"project.wiki_list_cards", CapWikiRead},
		{"project.wiki_search_card_content", CapWikiRead},
		// browser.cookies.read capability (cap_browser.go)
		{"browser.cookies_export", CapBrowserCookiesRead},
		{"dialog.openFile", CapDialogOpenFile},
		{"dialog.openFolder", CapDialogOpenFolder},
		{"dialog.saveFile", CapDialogSaveFile},
		// clipboard.write / clipboard.read capabilities (cap_clipboard.go)
		{"clipboard.write", CapClipboardWrite},
		{"clipboard.read", CapClipboardRead},
		// browser.instances.list capability (cap_browser.go)
		{"browser_instances_list", CapBrowserInstancesList},
		// db.profile.read / db.dial capabilities (cap_db.go)
		{"db.profile_list", CapDbProfileRead},
		{"db.profile_dial", CapDbDial},
		{"project.delete_file", ""}, // not an SDK host call
		{"agent.chat.submit", ""},   // host-internal, never granted
		{"workspace.debug", ""},     // host-internal, never granted
		{"", ""},
	}
	for _, c := range cases {
		if got := HostCallCapability(c.callID); got != c.want {
			t.Errorf("HostCallCapability(%q) = %q, want %q", c.callID, got, c.want)
		}
	}
}

func TestHostCallCapabilityCoversCatalog(t *testing.T) {
	// Every cataloged capability must be reachable from at least one callID
	// prefix so the protocol query never advertises an unreachable capability.
	reachable := map[string]bool{}
	for _, callID := range []string{
		"llm.chat", "project.read_file", "project.write_file", "shell.exec",
		"config.get", "provider.list", "aggregator.list", "state.get",
		"sshmanager.exec", "app.emit", "registry.query", "voice.recognize",
		"voice.synthesize", "voice.accounts.list", "media.list_units",
		"image.generate", "video.generate", "plugin.any.callable",
		"websearch.search", "crawl.start", "aistats.query",
		"oracle.capability_discover", "project.wiki_get_card",
		"browser.cookies_export", "browser_instances_list",
		"agent.read_messages", "dialog.openFile", "dialog.openFolder",
		"dialog.saveFile", "db.profile_list", "db.profile_dial",
		"clipboard.write", "clipboard.read", "workspace.list_projects",
	} {
		reachable[HostCallCapability(callID)] = true
	}
	for id := range CapabilityCatalog {
		if !reachable[id] && CapabilityHasCallGate(id) {
			t.Errorf("capability %q in catalog but no callID gates to it", id)
		}
	}
}

func TestResolveHostCall(t *testing.T) {
	cases := []struct {
		callID, want string
	}{
		{"llm.complete", "aiaggregator.dispatch"},
		{"llm.chat", "aiaggregator.dispatch"},
		{"project.read_file", "filesystem.read"},
		{"provider.list", "aimanager.provider_list"},
		{"voice.accounts.list", "voice.list_accounts"},
		{"media.list_units", "aimanager.list_units"},
		{"media.accounts.list", "media.list_accounts"},
		{"shell.exec", "shell.exec"},           // identity
		{"state.get", "state.get"},             // identity
		{"sshmanager.exec", "sshmanager.exec"}, // identity
	}
	for _, c := range cases {
		if got := ResolveHostCall(c.callID); got != c.want {
			t.Errorf("ResolveHostCall(%q) = %q, want %q", c.callID, got, c.want)
		}
	}
}
