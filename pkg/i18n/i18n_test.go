package i18n

import (
	"context"
	"testing"
)

func TestParseLocale(t *testing.T) {
	cases := []struct {
		input string
		want  Locale
	}{
		{"zh-CN", ZhCN},
		{"zh-cn", ZhCN},
		{"zh", ZhCN},
		{"zh_Hans", ZhCN},
		{"en-US", EnUS},
		{"en", EnUS},
		{"en_GB", EnUS},
		{"fr-FR", ZhCN},
		{"", ZhCN},
	}
	for _, c := range cases {
		if got := ParseLocale(c.input); got != c.want {
			t.Errorf("ParseLocale(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestT(t *testing.T) {
	if got := T("common.confirm"); got != "确认" {
		t.Errorf("T(common.confirm) = %q, want 确认", got)
	}
	SetDefault(EnUS)
	defer SetDefault(ZhCN)
	if got := T("common.confirm"); got != "Confirm" {
		t.Errorf("T(common.confirm) with en-US default = %q, want Confirm", got)
	}
}

func TestTCtx(t *testing.T) {
	ctx := WithLocale(context.Background(), EnUS)
	if got := TCtx(ctx, "common.cancel"); got != "Cancel" {
		t.Errorf("TCtx(en-US, common.cancel) = %q, want Cancel", got)
	}
}

func TestMissingKeyFallback(t *testing.T) {
	SetDefault(ZhCN)
	if got := TLocale(EnUS, "missing.key"); got != "missing.key" {
		t.Errorf("missing key fallback = %q, want missing.key", got)
	}
}
