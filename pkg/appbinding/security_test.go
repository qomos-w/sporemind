package appbinding

import "testing"

func TestIsKnownHostCapability(t *testing.T) {
	known := []string{CapLLMInvoke, CapFSRead, CapFSWrite, CapShellExec, CapConfigRead, CapAppState, CapAppEmit, CapProviderRead, CapAggregatorRead, CapSSHInvoke,
		CapVoiceSTT, CapVoiceTTS, CapVoiceRead, CapMediaRead, CapImageGen, CapVideoGen, CapRegistryRead,
		CapWebSearch, CapWebFetch, CapStatsRead, CapDiscoveryRead, CapWikiRead, CapBrowserCookiesRead, CapAgentObserve,
		CapDbProfileRead, CapDbDial, CapSporeInvoke}
	for _, c := range known {
		if !IsKnownHostCapability(c) {
			t.Errorf("expected %q to be a known host capability", c)
		}
	}
	unknown := []string{"", "filesystem", "network", "admin", "events"}
	for _, c := range unknown {
		if IsKnownHostCapability(c) {
			t.Errorf("expected %q to NOT be a known host capability", c)
		}
	}
}
