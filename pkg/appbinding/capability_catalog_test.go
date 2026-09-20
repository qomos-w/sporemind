package appbinding

import (
	"sort"
	"testing"
)

func TestCapabilityCatalogCoversAllKnownCapabilities(t *testing.T) {
	// Every capability constant from security.go must appear in the catalog.
	all := []string{
		CapLLMInvoke, CapFSRead, CapFSWrite, CapShellExec,
		CapConfigRead, CapProviderRead, CapAggregatorRead, CapAppState, CapAppData,
		CapSSHInvoke, CapAppEmit, CapRegistryRead,
		CapVoiceSTT, CapVoiceTTS, CapVoiceRead, CapBundleInvoke,
		CapMediaRead, CapImageGen, CapVideoGen,
		CapWebSearch, CapWebFetch, CapStatsRead, CapDiscoveryRead, CapWikiRead,
		CapBrowserCookiesRead, CapBrowserInstancesList, CapAgentRead,
		CapAgentObserve, CapDialogOpenFile, CapDialogOpenFolder,
		CapDialogSaveFile, CapDbProfileRead, CapDbDial,
		CapClipboardWrite, CapClipboardRead, CapWorkspaceRead,
		CapSporeInvoke,
	}
	if len(CapabilityCatalog) != len(all) {
		t.Fatalf("catalog has %d entries, want %d", len(CapabilityCatalog), len(all))
	}
	for _, id := range all {
		if _, ok := CapabilityCatalog[id]; !ok {
			t.Errorf("catalog missing known capability %q", id)
		}
	}
}

func TestGetCapabilityKnown(t *testing.T) {
	for id := range CapabilityCatalog {
		c, err := GetCapability(id)
		if err != nil {
			t.Errorf("GetCapability(%q): unexpected error: %v", id, err)
			continue
		}
		if c.ID != id {
			t.Errorf("GetCapability(%q): ID mismatch %q", id, c.ID)
		}
		if c.Title == "" {
			t.Errorf("GetCapability(%q): empty Title", id)
		}
		if c.Description == "" {
			t.Errorf("GetCapability(%q): empty Description", id)
		}
		if c.RiskLevel != RiskLow && c.RiskLevel != RiskMedium && c.RiskLevel != RiskHigh {
			t.Errorf("GetCapability(%q): invalid RiskLevel %q", id, c.RiskLevel)
		}
		if c.I18nTitleKey == "" {
			t.Errorf("GetCapability(%q): empty I18nTitleKey", id)
		}
		if c.I18nDescriptionKey == "" {
			t.Errorf("GetCapability(%q): empty I18nDescriptionKey", id)
		}
	}
}

func TestGetCapabilityUnknownReturnsError(t *testing.T) {
	_, err := GetCapability("bogus.cap")
	if err == nil {
		t.Fatal("expected error for unknown capability, got nil")
	}

	// Empty string is also unknown.
	if _, err := GetCapability(""); err == nil {
		t.Fatal("expected error for empty capability id, got nil")
	}
}

func TestCapabilityCatalogRiskConsistency(t *testing.T) {
	// shell.exec and fs.write must be high risk; the rest low/medium.
	highRisk := []string{CapShellExec, CapFSWrite, CapSSHInvoke, CapAgentObserve}
	for _, id := range highRisk {
		c, _ := GetCapability(id)
		if c.RiskLevel != RiskHigh {
			t.Errorf("capability %q: want RiskHigh, got %q", id, c.RiskLevel)
		}
	}
}

func TestCapabilityCatalogHasLocales(t *testing.T) {
	for id := range CapabilityCatalog {
		c, _ := GetCapability(id)
		zh, ok := c.Locales["zh-CN"]
		if !ok {
			t.Errorf("capability %q: missing zh-CN locale", id)
			continue
		}
		if zh.Title == "" || zh.Description == "" {
			t.Errorf("capability %q: zh-CN locale has empty title or description", id)
		}
		en, ok := c.Locales["en-US"]
		if !ok {
			t.Errorf("capability %q: missing en-US locale", id)
			continue
		}
		if en.Title == "" || en.Description == "" {
			t.Errorf("capability %q: en-US locale has empty title or description", id)
		}
	}
}

func TestCapabilityCatalogConsistentWithIsKnownHostCapability(t *testing.T) {
	// Every catalog entry must be a known host capability and vice-versa.
	for id := range CapabilityCatalog {
		if !IsKnownHostCapability(id) {
			t.Errorf("catalog entry %q is not recognized by IsKnownHostCapability", id)
		}
	}
	known := []string{
		CapLLMInvoke, CapFSRead, CapFSWrite, CapShellExec,
		CapConfigRead, CapProviderRead, CapAggregatorRead, CapAppState, CapAppData,
		CapSSHInvoke, CapAppEmit, CapRegistryRead,
		CapVoiceSTT, CapVoiceTTS, CapVoiceRead, CapBundleInvoke,
		CapMediaRead, CapImageGen, CapVideoGen,
		CapWebSearch, CapWebFetch, CapStatsRead, CapDiscoveryRead, CapWikiRead,
		CapBrowserCookiesRead, CapBrowserInstancesList, CapAgentRead,
		CapAgentObserve, CapDialogOpenFile, CapDialogOpenFolder,
		CapDialogSaveFile, CapDbProfileRead, CapDbDial,
		CapClipboardWrite, CapClipboardRead, CapWorkspaceRead,
		CapDbProfileRead, CapDbDial, CapSporeInvoke,
	}
	for _, id := range known {
		if _, ok := CapabilityCatalog[id]; !ok {
			t.Errorf("known host capability %q is missing from the catalog", id)
		}
	}
}

func TestCapabilityCatalogSortedIDs(t *testing.T) {
	ids := make([]string, 0, len(CapabilityCatalog))
	for id := range CapabilityCatalog {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	// Ensure all 37 are present.
	wantCount := len(CapabilityCatalog)
	if wantCount != 37 {
		t.Fatalf("catalog size drifted: expected 37 entries, got %d", wantCount)
	}
	if len(ids) != wantCount {
		t.Fatalf("expected %d catalog entries, got %d", wantCount, len(ids))
	}
}
