package desktop

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestSameSiteFromInt32(t *testing.T) {
	cases := []struct {
		v    int32
		want string
	}{
		{0, "None"},
		{1, "Lax"},
		{2, "Strict"},
		{42, ""},
	}
	for _, tc := range cases {
		if got := sameSiteFromInt32(tc.v); got != tc.want {
			t.Errorf("sameSiteFromInt32(%d) = %q, want %q", tc.v, got, tc.want)
		}
	}
}

func TestSameSiteToInt32(t *testing.T) {
	cases := []struct {
		s    string
		want int32
	}{
		{"None", 0},
		{"none", 0},
		{"Lax", 1},
		{"lax", 1},
		{"Strict", 2},
		{"strict", 2},
		{"", 1},
		{"unknown", 1},
	}
	for _, tc := range cases {
		if got := sameSiteToInt32(tc.s); got != tc.want {
			t.Errorf("sameSiteToInt32(%q) = %d, want %d", tc.s, got, tc.want)
		}
	}
}

func TestGroupCookiesByDomain(t *testing.T) {
	entries := []domain.BrowserCookieEntry{
		{Name: "a", Domain: "example.com"},
		{Name: "b", Domain: "example.com"},
		{Name: "c", Domain: "other.org"},
	}
	got := groupCookiesByDomain(entries)
	if len(got["example.com"]) != 2 {
		t.Errorf("expected 2 cookies for example.com, got %d", len(got["example.com"]))
	}
	if len(got["other.org"]) != 1 {
		t.Errorf("expected 1 cookie for other.org, got %d", len(got["other.org"]))
	}
	if _, ok := got["missing"]; ok {
		t.Error("unexpected entry for missing domain")
	}
}

func TestGroupCookiesByDomainEmpty(t *testing.T) {
	got := groupCookiesByDomain(nil)
	if got == nil || len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}
