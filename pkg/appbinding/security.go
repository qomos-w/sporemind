package appbinding

import (
	"strings"
	"time"
)

// Host capability identifiers form the definitive catalog of capabilities an
// app may declare in its appdef Permissions (and that codegen may auto-derive,
// e.g. app.emit from event blocks). A capability string outside this set is
// rejected at registration time by validateManifestSecurity — there is no
// implicit or wildcard grant. Under the declaration-is-authorization model
// there is no separate host allowlist: the app's own declared Permissions are
// its granted set; the catalog is only the vocabulary those declarations must
// draw from.
const (
	CapLLMInvoke            = "llm.invoke"
	CapFSRead               = "fs.read"
	CapFSWrite              = "fs.write"
	CapShellExec            = "shell.exec"
	CapConfigRead           = "config.read"
	CapProviderRead         = "provider.read"
	CapAggregatorRead       = "aggregator.read"
	CapAppState             = "app.state"
	CapAppData              = "app.data"
	CapSSHInvoke            = "ssh.invoke"
	CapAppEmit              = "app.emit"
	CapRegistryRead         = "registry.read"
	CapVoiceSTT             = "voice.stt"
	CapVoiceTTS             = "voice.tts"
	CapVoiceRead            = "voice.read"
	CapBundleInvoke         = "bundle.invoke"
	CapMediaRead            = "media.read"
	CapImageGen             = "image.gen"
	CapVideoGen             = "video.gen"
	CapWebSearch            = "web.search"
	CapWebFetch             = "web.fetch"
	CapStatsRead            = "stats.read"
	CapDiscoveryRead        = "discovery.read"
	CapWikiRead             = "wiki.read"
	CapAgentRead            = "agent.read"
	CapAgentObserve         = "agent.observe"
	CapBrowserCookiesRead   = "browser.cookies.read"
	CapBrowserInstancesList = "browser.instances.list"
	CapDialogOpenFile       = "dialog.openFile"
	CapDialogOpenFolder     = "dialog.openFolder"
	CapDialogSaveFile       = "dialog.saveFile"
	CapClipboardWrite       = "clipboard.write"
	CapClipboardRead        = "clipboard.read"
	CapWorkspaceRead        = "workspace.read"
	CapDbProfileRead        = "db.profile.read"
	CapDbDial               = "db.dial"
	CapSporeInvoke          = "spore.invoke"
)

// IsKnownHostCapability reports whether c is a recognized host capability
// from the registration tables (populated by RegisterCapability in
// capabilities_registered.go and cap_*.go files). Unknown capability strings
// are rejected at registration (validateManifestSecurity) rather than
// silently accepted. plugin.* bundle callIDs are accepted by prefix: they
// are per-callID authorization gates (see DerivedCapabilities) whose real
// enforcement is the host bridge's exact-match check plus the dependency
// declaration check in validateManifestSecurity.
func IsKnownHostCapability(c string) bool {
	if strings.HasPrefix(c, "plugin.") {
		return true
	}
	_, ok := registeredCaps[c]
	return ok
}

type AuditRecord struct {
	Time      time.Time
	RequestID string
	AppID     string
	Runtime   string
	AgentID   string
	Role      string
	ProjectID string
	Callable  string
	Allowed   bool
	Reason    string
	SessionID string
	CallSeq   int64
}
