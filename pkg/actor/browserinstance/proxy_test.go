package browserinstance

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestEffectiveProxyMode(t *testing.T) {
	cases := []struct {
		name string
		cfg  domain.BrowserInstanceConfig
		want string
	}{
		{"legacy empty", domain.BrowserInstanceConfig{}, ProxyModeSystem},
		{"legacy proxy set", domain.BrowserInstanceConfig{Proxy: "http://p:1"}, ProxyModeCustom},
		{"explicit system", domain.BrowserInstanceConfig{ProxyMode: ProxyModeSystem}, ProxyModeSystem},
		{"none", domain.BrowserInstanceConfig{ProxyMode: ProxyModeNone}, ProxyModeNone},
		{"none wins over proxy", domain.BrowserInstanceConfig{ProxyMode: ProxyModeNone, Proxy: "http://p:1"}, ProxyModeNone},
		{"custom with url", domain.BrowserInstanceConfig{ProxyMode: ProxyModeCustom, Proxy: "http://p:1"}, ProxyModeCustom},
		{"custom without url", domain.BrowserInstanceConfig{ProxyMode: ProxyModeCustom}, ProxyModeSystem},
	}
	for _, tc := range cases {
		if got := EffectiveProxyMode(tc.cfg); got != tc.want {
			t.Errorf("%s: EffectiveProxyMode = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestProxyChanged(t *testing.T) {
	system := domain.BrowserInstanceConfig{}
	none := domain.BrowserInstanceConfig{ProxyMode: ProxyModeNone}
	customA := domain.BrowserInstanceConfig{ProxyMode: ProxyModeCustom, Proxy: "http://a:1"}
	customB := domain.BrowserInstanceConfig{ProxyMode: ProxyModeCustom, Proxy: "http://b:2"}
	legacyCustom := domain.BrowserInstanceConfig{Proxy: "http://a:1"}

	if ProxyChanged(system, system) {
		t.Error("system -> system must not report a change")
	}
	if ProxyChanged(customA, customA) {
		t.Error("same custom proxy must not report a change")
	}
	if !ProxyChanged(system, none) {
		t.Error("system -> none must report a change")
	}
	if !ProxyChanged(system, customA) {
		t.Error("system -> custom must report a change")
	}
	if !ProxyChanged(customA, customB) {
		t.Error("custom url change must report a change")
	}
	if ProxyChanged(customA, legacyCustom) {
		t.Error("explicit custom and legacy custom with same url must be equivalent")
	}
}
