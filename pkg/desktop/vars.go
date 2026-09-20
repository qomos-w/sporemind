package desktop

import (
	"fmt"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/qomos-w/sporemind/pkg/actor/browserinstance"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Function seams

// openDevToolsImpl triggers the DevTools window via platform-specific means.
// On Windows this synthesises Ctrl+Shift+F12 via SendInput; on other platforms
// it is a no-op. Assigned in devtools_windows.go / devtools_other.go init().
var openDevToolsImpl func()

// Lookup tables

// browserBaseArgs are Chromium switches applied to every WebView2 browser
// window. ThirdPartyStoragePartitioning (on by default since Chromium 118)
// partitions storage in cross-origin iframes; Cloudflare Turnstile's
// challenge iframe (challenges.cloudflare.com) breaks against partitioned
// storage and shows "Can't verify the user is human" in embedded contexts.
var browserBaseArgs = []string{
	"--disable-blink-features=AutomationControlled",
	"--disable-features=ThirdPartyStoragePartitioning",
	// Cloudflare Turnstile uses WebGPU requestAdapter() for proof-of-work;
	// WebView2 may blocklist GPU features that real Edge/Chrome would use.
	"--enable-features=WebGPU",
	"--ignore-gpu-blocklist",
}

// appendProxyArgs appends the Chromium proxy switches implied by the instance
// config: custom -> --proxy-server, none -> --no-proxy-server, system -> none
// (WebView2 then follows the OS proxy settings).
func appendProxyArgs(args []string, cfg domain.BrowserInstanceConfig) []string {
	switch browserinstance.EffectiveProxyMode(cfg) {
	case browserinstance.ProxyModeNone:
		return append(args, "--no-proxy-server")
	case browserinstance.ProxyModeCustom:
		return append(args, fmt.Sprintf("--proxy-server=%s", cfg.Proxy))
	default:
		return args
	}
}

// sharedBrowserCacheDir is the centralized HTTP disk-cache directory shared by
// all independent browser instances. Their per-instance profile dirs then hold
// only durable state (cookies, storage, login sessions) while page/image caches
// accumulate under the global profile directory instead of one copy per
// instance.
func sharedBrowserCacheDir() string {
	return filepath.Join(config.DataDir(), "browser-profiles", "global", "shared-cache")
}

// appendSharedCacheArg points the Chromium HTTP disk cache (images, scripts and
// other cached resources) at sharedBrowserCacheDir. The value is quoted because
// Wails joins AdditionalBrowserArgs with spaces before handing the string to
// WebView2; data-dir paths may contain spaces. Chromium's Simple Cache backend
// tolerates several WebView2 processes sharing one directory, and an
// unopenable cache dir only degrades to an in-memory cache — cookies and login
// state stay in the per-instance profile.
func appendSharedCacheArg(args []string) []string {
	return append(args, `--disk-cache-dir="`+sharedBrowserCacheDir()+`"`)
}

// Mutable singletons

// TODO(actor-ownership): migrate to actor-owned state.

// store is the package-level persistence backend for desktop window/config
// state.
var store persist.Persist = persist.MustNew(config.PersistConfig("desktop"))

// crashApp/crashWindow hold the Wails handles wired by SetCrashApp /
// SetCrashWindow so the panic handler can show a dialog and emit events before
// the process exits. crashSuppressBrowsers is wired by App.Bind so the panic
// path can hide (and keep hidden) the native browser child windows that would
// otherwise cover the live crash UI.
var (
	crashApp              *application.App
	crashAppMu            sync.RWMutex
	crashWindow           *application.WebviewWindow
	crashOnce             sync.Once
	crashSuppressBrowsers func()
)

// b64BufPool reuses the scratch byte slices used by base64 encode/decode so the
// hot outbound path avoids per-frame allocations. Returned buffers must not be
// retained after their immediate use.
var b64BufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 0, 1024)
		return &buf
	},
}

// Win32 clipboard helpers — user32 is already loaded in devtools_windows.go.
var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procCloseClipboard   = user32.NewProc("CloseClipboard")

	procGetClipboardData             = user32.NewProc("GetClipboardData")
	procIsClipboardFormatAvailable   = user32.NewProc("IsClipboardFormatAvailable")

	procGlobalAlloc  = kernel32.NewProc("GlobalAlloc")
	procGlobalLock   = kernel32.NewProc("GlobalLock")
	procGlobalUnlock = kernel32.NewProc("GlobalUnlock")
	procGlobalFree   = kernel32.NewProc("GlobalFree")
	procGlobalSize   = kernel32.NewProc("GlobalSize")
)

var (
	procGetDesktopWindow = user32.NewProc("GetDesktopWindow")
)
