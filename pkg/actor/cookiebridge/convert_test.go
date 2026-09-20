package cookiebridge

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestConvertChromeCookiesFiltersAndMaps(t *testing.T) {
	cookies := []ChromeCookie{
		{Domain: ".github.com", Name: "user_session", Value: "sekret", Path: "/", Secure: true, HTTPOnly: true, SameSite: "lax", ExpirationDate: 1893456000},
		{Domain: "github.com", Name: "logged_in", Value: "yes", ExpirationDate: 1893456000, SameSite: "no_restriction"},
		{Domain: ".example.org", Name: "other", Value: "nope", ExpirationDate: 1893456000},
		{Domain: ".github.com", Name: "", Value: "unnamed", ExpirationDate: 1893456000},
	}
	groups, skipped := convertChromeCookies(cookies, "github.com")
	if skipped != 2 {
		t.Fatalf("skipped = %d, want 2 (unrelated domain + empty name)", skipped)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %v, want exactly the github.com group", groups)
	}
	got := groups["github.com"]
	if len(got) != 2 {
		t.Fatalf("github.com entries = %d, want 2", len(got))
	}
	if got[0].Name != "user_session" || got[0].Value != "sekret" {
		t.Fatalf("first entry = %+v", got[0])
	}
	if !got[0].HttpOnly || !got[0].Secure {
		t.Fatalf("flags not carried: %+v", got[0])
	}
	if got[0].SameSite != "Lax" {
		t.Fatalf("sameSite lax mapping = %q", got[0].SameSite)
	}
	if got[1].SameSite != "None" {
		t.Fatalf("sameSite no_restriction mapping = %q", got[1].SameSite)
	}
	domains := domainsOf(groups)
	if len(domains) != 1 || domains[0] != "github.com" {
		t.Fatalf("domainsOf = %v", domains)
	}
}

func TestConvertChromeCookiesSessionCookieGetsExpiry(t *testing.T) {
	groups, _ := convertChromeCookies([]ChromeCookie{
		{Domain: "example.com", Name: "sid", Value: "v", Path: ""},
	}, "")
	e := groups["example.com"][0]
	if e.Expires <= 0 {
		t.Fatalf("session cookie must get a durable expiry, got %d", e.Expires)
	}
	if e.Path != "/" {
		t.Fatalf("default path = %q, want /", e.Path)
	}
}

func TestSameSiteFromChrome(t *testing.T) {
	cases := map[string]string{
		"strict":         "Strict",
		"lax":            "Lax",
		"no_restriction": "None",
		"unspecified":    "",
		"":               "",
		"weird":          "",
	}
	for in, want := range cases {
		if got := sameSiteFromChrome(in); got != want {
			t.Errorf("sameSiteFromChrome(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeCookieDomain(t *testing.T) {
	if got := normalizeCookieDomain(" .GitHub.COM "); got != "github.com" {
		t.Fatalf("normalizeCookieDomain = %q", got)
	}
}

// compile-time: entries must stay assignable to the domain wire type.
var _ = domain.BrowserCookieEntry{}
