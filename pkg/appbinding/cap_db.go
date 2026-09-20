package appbinding

import "reflect"

// Database profile capabilities (dbmanager). db.profile.read exposes the
// masked profile list — endpoints, usernames and Has* secret-presence flags,
// never the secrets themselves — so an app can offer a "connect via host
// profile" picker without the user re-typing endpoints. db.dial is RiskHigh:
// db.profile_dial resolves the FULL credential plus the tunnel-local dial
// address and TLS decision, i.e. the response is everything needed to open a
// direct connection to the database — equivalent to handing the app the
// database password. Both callIDs are served locally by pluginhost (the
// dbmanager CRUD surfaces stay admin-gated; pluginhost forwards to the
// ungated internal profile_views/profile_lookup/profile_resolve surfaces).
func init() {
	RegisterCapability(CapabilityDef{
		ID:                 CapDbProfileRead,
		Title:              "读取数据库连接配置",
		Description:        "允许应用读取宿主 dbmanager 的数据库连接 profile 列表（脱敏：仅 endpoint/用户名与凭据存在标志，不含凭据本体）。",
		RiskLevel:          RiskMedium,
		I18nTitleKey:       "capability.db.profile.read.title",
		I18nDescriptionKey: "capability.db.profile.read.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "读取数据库连接配置", Description: "允许应用读取宿主 dbmanager 的数据库连接 profile 列表（脱敏：仅 endpoint/用户名与凭据存在标志，不含凭据本体）。"},
			"en-US": {Title: "Read database connection profiles", Description: "Allows the app to read the host dbmanager connection profile list (masked: endpoints, usernames and credential-presence flags only, never the credentials themselves)."},
		},
	})
	RegisterCapability(CapabilityDef{
		ID:                 CapDbDial,
		Title:              "解析数据库连接凭据（高风险）",
		Description:        "允许应用按 profile 解析出完整的数据库拨号参数：endpoint、隧道本地地址、TLS 决策与明文凭据——等同把数据库密码交给应用。",
		RiskLevel:          RiskHigh,
		I18nTitleKey:       "capability.db.dial.title",
		I18nDescriptionKey: "capability.db.dial.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "解析数据库连接凭据（高风险）", Description: "允许应用按 profile 解析出完整的数据库拨号参数：endpoint、隧道本地地址、TLS 决策与明文凭据——等同把数据库密码交给应用。"},
			"en-US": {Title: "Resolve database credentials (high risk)", Description: "Allows the app to resolve a profile into complete dial parameters: endpoint, tunnel-local address, TLS decision and plaintext credentials — equivalent to handing the app the database password."},
		},
	})
	RegisterHostCall(HostCallDef{
		CallID:     "db.profile_list",
		Capability: CapDbProfileRead,
		Local:      true,
		Note:       "payload handled locally by pluginhost; response is dbmanager DbProfileListResp (masked DbProfileView items)",
	})
	RegisterHostCall(HostCallDef{
		CallID:     "db.profile_dial",
		Capability: CapDbDial,
		Local:      true,
		ReqType:    reflect.TypeOf(DbProfileDialReq{}),
		RespType:   reflect.TypeOf(DbProfileDialResp{}),
		Note:       "payload handled locally by pluginhost; resolves profile + credential + tunnel + TLS into dial-ready params",
	})
}
