package browserinstance

import "github.com/qomos-w/sporemind/pkg/domain"

// ProxyMode values for BrowserInstanceConfig.ProxyMode.
const (
	ProxyModeSystem = "system"
	ProxyModeNone   = "none"
	ProxyModeCustom = "custom"
)

// EffectiveProxyMode normalizes cfg.ProxyMode into one of the three modes.
// Legacy configs carry an empty ProxyMode: a non-empty Proxy means custom,
// otherwise system. An explicit "custom" without a Proxy URL behaves as system
// (no proxy switches passed to WebView2).
func EffectiveProxyMode(cfg domain.BrowserInstanceConfig) string {
	switch cfg.ProxyMode {
	case ProxyModeNone:
		return ProxyModeNone
	case ProxyModeCustom:
		if cfg.Proxy == "" {
			return ProxyModeSystem
		}
		return ProxyModeCustom
	default:
		if cfg.Proxy != "" {
			return ProxyModeCustom
		}
		return ProxyModeSystem
	}
}

// ProxyChanged reports whether switching from prev to next changes the proxy
// WebView2 will use. --proxy-server/--no-proxy-server are creation-time
// parameters, so any change requires a window recreate.
func ProxyChanged(prev, next domain.BrowserInstanceConfig) bool {
	pm, nm := EffectiveProxyMode(prev), EffectiveProxyMode(next)
	if pm != nm {
		return true
	}
	return nm == ProxyModeCustom && prev.Proxy != next.Proxy
}
