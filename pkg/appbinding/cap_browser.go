package appbinding

import (
	"reflect"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// BrowserCookiesExportReq is the adapted SDK wire for browser.cookies_export.
// It reuses the backing browsermanager.export_cookies response contract but
// adds an optional Domain filter so an app can narrow the exposure to the
// cookies of one site instead of receiving the whole profile jar.
type BrowserCookiesExportReq struct {
	ID     string `json:"Id"`
	Domain string `json:"Domain,omitempty"`
}

// Browser capabilities. Cookie export is RiskHigh — the values ARE login
// credentials: a returned wos-session / auth cookie is the equivalent of a
// password for that site, more sensitive than fs.write (which cannot read
// existing host login state). The capability is local-handled by pluginhost
// (not pass-through): pluginhost resolves the instance against
// browsermanager's managed independent-instance list — the shared "global"
// host profile and per-app panel profiles are rejected before any cookie
// bytes are read — and only then calls browsermanager.export_cookies as the
// host.
func init() {
	RegisterCapability(CapabilityDef{
		ID:                 CapBrowserCookiesRead,
		Title:              "读取浏览器 Cookie（明文凭证）",
		Description:        "允许应用读取宿主浏览器独立实例（independent profile）的 Cookie，返回明文 cookie 值——等同该站点的登录凭证。共享的 global 宿主登录态与各应用面板 profile 一律不可读；可选 Domain 过滤只返回匹配站点的 cookie。",
		RiskLevel:          RiskHigh,
		I18nTitleKey:       "capability.browser.cookies.read.title",
		I18nDescriptionKey: "capability.browser.cookies.read.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "读取浏览器 Cookie（明文凭证）", Description: "允许应用读取宿主浏览器独立实例（independent profile）的 Cookie，返回明文 cookie 值——等同该站点的登录凭证。共享的 global 宿主登录态与各应用面板 profile 一律不可读；可选 Domain 过滤只返回匹配站点的 cookie。"},
			"en-US": {Title: "Read browser cookies (plaintext credentials)", Description: "Allows the app to read cookies from the host browser's independent instances; returns plaintext cookie values — the login-equivalent credentials for those sites. The shared global host profile and per-app panel profiles are never readable; an optional Domain filter returns only matching-site cookies."},
		},
	})
	RegisterHostCall(HostCallDef{
		CallID:     "browser.cookies_export",
		Capability: CapBrowserCookiesRead,
		Local:      true,
		ReqType:    reflect.TypeOf(BrowserCookiesExportReq{}),
		RespType:   reflect.TypeOf(gen.BrowserManagerExportCookiesResp{}),
	})
}

// BrowserInstanceRef is the minimal instance descriptor for
// browser_instances_list: just enough for a picker UI. Proxy, proxy mode,
// window state and other config fields are deliberately not forwarded.
type BrowserInstanceRef struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	URL   string `json:"Url"`
	Title string `json:"Title"`
}

// BrowserInstancesListReq is empty: listing takes no parameters.
type BrowserInstancesListReq struct{}

type BrowserInstancesListResp struct {
	Items []BrowserInstanceRef `json:"Items"`
}

// Listing the managed independent instances is RiskMedium: it exposes the
// existence of those windows plus their current URL/title (page titles can
// carry account names), but no credentials and no profile paths. The list is
// the same one browser.cookies.read resolves instance ids against.
func init() {
	RegisterCapability(CapabilityDef{
		ID:                 CapBrowserInstancesList,
		Title:              "列出独立浏览器实例",
		Description:        "返回宿主管理的 independent 浏览器实例列表（仅 Id、名称、当前 URL、页面标题），供前端选择实例。共享 global 宿主 profile 与各应用面板 profile 不在列表内；不含 Cookie、代理或任何凭证。",
		RiskLevel:          RiskMedium,
		I18nTitleKey:       "capability.browser.instances.list.title",
		I18nDescriptionKey: "capability.browser.instances.list.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "列出独立浏览器实例", Description: "返回宿主管理的 independent 浏览器实例列表（仅 Id、名称、当前 URL、页面标题），供前端选择实例。共享 global 宿主 profile 与各应用面板 profile 不在列表内；不含 Cookie、代理或任何凭证。"},
			"en-US": {Title: "List independent browser instances", Description: "Returns the host-managed independent browser instances (Id, name, current URL and page title only) for instance pickers. The shared global host profile and per-app panel profiles are not listed; no cookies, proxy settings, or credentials are included."},
		},
	})
	RegisterHostCall(HostCallDef{
		CallID:     "browser_instances_list",
		Capability: CapBrowserInstancesList,
		Local:      true,
		ReqType:    reflect.TypeOf(BrowserInstancesListReq{}),
		RespType:   reflect.TypeOf(BrowserInstancesListResp{}),
	})
}
